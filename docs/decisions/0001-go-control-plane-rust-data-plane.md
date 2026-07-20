# ADR-0001: Split control plane (Go) and data plane (Rust)

## Status

Accepted, implemented.

## Context

Aegis needs to do two very different kinds of work. First, operational and control-flow work: parse and validate YAML config, run periodic HTTP/TCP health checks, serve an admin API for reload/drain/backend management, and orchestrate reconfiguration. This work is I/O-bound, changes relatively often as features get added, and its correctness matters far more than its raw speed. Second, packet forwarding: accept thousands of concurrent TCP/UDP connections and copy bytes between client and backend with minimal added latency and no unnecessary allocation. This work is on the hot path for every byte the proxy ever forwards.

A single language and a single process is the default starting point for a proxy this size. The question is whether that default holds up once "many concurrent low-latency connections" and "config parsing, HTTP admin API, health-check scheduling" are both in scope.

## Decision

Split by responsibility into two processes: a **Go control plane** (config management, health checking, admin API, orchestration) and a **Rust data plane** (TCP/UDP proxying, load balancing execution, circuit breaker enforcement), communicating over gRPC (see ADR-0002).

## Alternatives Considered

**All Rust, one binary.** No cross-process IPC, no serialization overhead, one toolchain. Rejected because the control-plane surface — HTTP admin API, YAML parsing/validation, health-check scheduling, hot reload — is exactly the kind of work Go's standard library (`net/http`, `encoding/*`, cheap goroutines for "run N independent polling loops") is fast to write and easy to review, without requiring every contributor to reason about async Rust for code where async Rust buys nothing. Writing this part in Rust would work; it wouldn't be a better use of the language.

**All Go, one binary.** Data plane in Go too, one goroutine per connection. Rejected because Go's garbage collector and scheduler introduce latency variance under high connection counts and high-throughput byte copying that Rust's ownership model avoids by construction — no GC pauses, and explicit control over allocation on the hot path. The proxy's core value proposition is low, predictable per-connection overhead; that's a Rust-shaped problem.

**Single process, threads instead of processes, no IPC boundary at all.** Considered and rejected together with the shared-memory option in ADR-0002 — the two languages don't share a runtime, so "one process" doesn't actually remove the boundary, it just makes it implicit instead of explicit.

## Consequences

**Benefits**
- Each language is used for what it's actually good at, instead of picking one language and accepting its weaknesses everywhere.
- The two processes can restart independently. This isn't theoretical: this session's work depended on it directly — the gRPC reconnect fix and circuit breaker persistence work both exist specifically because the data plane can crash or restart without the control plane needing to restart too, and vice versa.
- Each side can be profiled, tuned, and scaled on its own terms.

**Drawbacks**
- The cross-language boundary needs a schema both sides agree on (protobuf — see ADR-0002), and that schema can still drift; `config.go`'s `validAlgorithms` map, which has to be kept in sync with `load_balancer.rs`'s `Algorithm::from_str` by hand and says so in a comment, is a concrete example of the risk this split introduces.
- Two toolchains, two dependency ecosystems, two test suites, two sets of CI tooling to maintain.
- A contributor implementing one feature end-to-end (e.g. a new backend field) needs to touch both a Go struct and a Rust struct and the `.proto` connecting them — more moving parts than a single-language change.
- The process boundary is itself a source of bugs that wouldn't exist in a single process: this session found and fixed two (the missing config re-push on data-plane restart, and a second latent bug in that same fix — an idle gRPC connection that wouldn't redial without an explicit nudge). Splitting the system doesn't just split the work, it adds a new class of failure mode that has to be engineered around.
