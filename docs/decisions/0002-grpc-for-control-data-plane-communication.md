# ADR-0002: gRPC for control-plane ↔ data-plane communication

## Status

Accepted, implemented.

## Context

Given the split in ADR-0001, the two processes need to exchange four kinds of thing: full config pushes (`UpdateConfig`), backend list/health updates (`ReloadBackends`), a continuous stream of proxy metrics from data plane to control plane (`StreamMetrics`), and one-off control commands (`DrainConnections`). Two of those are request/response; one is a long-lived server stream. Whatever's chosen has to work across a language boundary (Go client, Rust server) with a schema both sides can trust.

## Decision

gRPC over protobuf, defined once in `control-plane/proto/proxy.proto`, code-generated into both a Go client (`protoc-gen-go-grpc`) and a Rust server (`tonic`/`prost`). One `.proto` file is the single source of truth for the wire schema.

## Alternatives Considered

**Shared memory (mmap / shared segment).** Lowest possible latency, no serialization cost. Rejected: it requires both processes to be on the same host, which directly contradicts the reason they were split into separate processes in ADR-0001 — independent restart, independent scaling, and (per the v2 roadmap) eventually running on separate machines or pods entirely. It would also mean hand-rolling cross-language synchronization primitives, since Go and Rust don't share a memory model or ABI — reimplementing, badly, what gRPC already provides (schema, codegen, streaming, connection-state tracking) for free.

**REST/JSON over HTTP.** Simpler tooling, human-readable payloads, easier to curl for debugging. Rejected: config pushes need a strict, versioned schema — this is precisely the kind of drift risk the `validAlgorithms` comment in `config.go` already flags as a manually-maintained sync point between the two codebases, and JSON with hand-written structs on both sides would make that worse, not better, with no compile-time check that Go and Rust agree on a field. JSON also has no native streaming; the metrics feed would need a second mechanism (SSE, WebSocket, or polling) bolted on next to it.

**A message broker (Redis pub/sub, NATS).** Decouples the two processes further and would be a natural fit if there were multiple data-plane instances talking to multiple control-plane instances. Deferred, not rejected outright — this is explicitly `ENGINEERING_DIRECTION.md`'s "Redis coordination," listed under Future Work for v2. Two processes with a direct network connection don't need a broker in between; adding one now would mean running and monitoring an extra piece of infrastructure to solve a multi-node problem this project doesn't have yet (see ADR-0005).

## Consequences

**Benefits**
- One `.proto` generates both sides' bindings, which narrows (though doesn't eliminate — see ADR-0001's drawbacks) the risk of the two languages' config representations silently diverging.
- HTTP/2 multiplexing means the metrics stream and config/control RPCs share a single connection instead of needing separate transports.
- gRPC's connection-state machine is directly inspectable and was load-bearing this session: the `WatchReconnect` fix watches `grpc.ClientConn`'s connectivity states (`Ready`/`Idle`/`TransientFailure`) to detect and react to the data plane coming back after a crash.
- Works across hosts as-is, which the v2 multi-node roadmap depends on without needing a transport change later.

**Drawbacks**
- The process boundary this enables is itself a source of failure modes a single process wouldn't have — see ADR-0001's drawbacks for the two concrete bugs this surfaced this session.
- Requires `protoc` and the Go/Rust protobuf plugins as a build-time dependency; contributors need them installed before either side compiles cleanly.
- gRPC's own reconnect/backoff behavior is more subtle than it looks — `grpc.NewClient`'s modern pick-first policy drops an idle connection straight to `Idle` instead of auto-retrying, which is exactly what caused the second bug found this session. Choosing gRPC means inheriting its connection-management model, not just its wire format.
