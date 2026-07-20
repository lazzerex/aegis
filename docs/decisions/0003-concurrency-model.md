# ADR-0003: Concurrency model

## Status

Accepted, implemented.

## Context

The two halves of Aegis have different concurrency requirements. The Rust data plane has to hold open thousands of concurrent connections and copy bytes on the hot path without a mutex ever being held across an `.await` (a lock held across a suspension point can deadlock a single-worker async runtime — this project has already hit that exact bug once, see `bench/RESULTS.md`'s connection-pool deadlock). The Go control plane runs a handful of independent, low-frequency loops (one health-check goroutine per backend, one gRPC metrics-streaming goroutine, one HTTP admin server) where correctness matters far more than throughput.

Shared mutable state is unavoidable on both sides: the data plane's backend list, circuit breaker map, and rate limiter are read on every single connection and written only on config reload; the control plane's health-check state map is read by the admin API and written by every health-check tick.

## Decision

**Rust data plane:** Tokio async/await on a multi-threaded runtime, one lightweight task per connection rather than one OS thread. Shared config-derived state (load balancer, circuit breaker manager, rate limiter) is stored as `RwLock<Arc<T>>`: a reader takes a read lock just long enough to clone the `Arc` and then releases it, so an in-flight connection's byte-copy loop is never blocked by a concurrent reconfiguration. A writer (a config reload) builds an entirely new `T`, wraps it in a new `Arc`, and swaps the pointer under a write lock — readers either see the whole old state or the whole new state, never a partial update.

**Go control plane:** goroutines, channels, and a plain `sync.Mutex`/`sync.RWMutex` per component (the health checker's `healthState` map, the gRPC client's `lastCfg`) — the standard idiom, nothing more elaborate, because nothing on this side is performance-sensitive enough to need it.

## Alternatives Considered

**Thread-per-connection in Rust (`std::thread`, blocking I/O).** Simplest possible mental model — no `async`/`await` at all. Rejected: OS threads are expensive to spawn and context-switch at the connection volumes a proxy needs to sustain; that overhead is exactly what async I/O exists to eliminate, and giving it up would undermine the reason Rust was chosen for this side in ADR-0001.

**A single global `Mutex<ProxyConfig>` instead of `RwLock<Arc<T>>` per component.** Simpler — one lock instead of several. Rejected, and not hypothetically: this is the bug that used to be here. TASK.md's Critical section records an earlier, unsound `unsafe` mutation of `circuit_breaker`/`rate_limiter` that predates `RwLock<Arc<T>>` — a coarser locking scheme was tried, produced a real data race, and was replaced with the current pattern specifically because of that failure.

**Actor model** (each backend or breaker as an actor communicating over channels, no shared state at all). Rejected as unwarranted complexity for this scope: `RwLock<Arc<T>>` already gives lock-free reads on the hot path, and config reload frequency is low enough (operator-triggered, not per-request) that a full actor framework wouldn't measurably improve anything — it would only add indirection and a new set of primitives to learn.

## Consequences

**Benefits**
- Config, circuit breaker, and rate limiter updates are atomic from a reader's perspective — a pointer swap, not a copy or a partial mutation — and readers never block behind a reconfiguration.
- This isn't just asserted — it's tested under real concurrent load: `test_update_config_concurrent_reads_dont_panic` (`data-plane/src/config.rs`) runs a writer doing 200 reconfigures against four reader threads continuously reading every `RwLock`-guarded field, and asserts no panic and no torn state.
- The Go side stays idiomatic and simple exactly where performance doesn't matter, instead of importing Rust-side complexity where it isn't earning its cost.

**Drawbacks**
- Two different concurrency idioms live in one codebase. A contributor comfortable in Go's goroutines-and-mutexes model still has to learn Tokio's task/await model (and its sharp edges, like the lock-across-`.await` deadlock) to touch the data plane.
- `RwLock<Arc<T>>` is not wait-free: a writer can in principle be starved under sustained, heavy read contention. In practice config reloads are infrequent and operator-triggered, so this hasn't been a problem, but it's a real limit of the pattern, not a hypothetical one.
