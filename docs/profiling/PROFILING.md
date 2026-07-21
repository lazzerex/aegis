# Profiling

What actually costs time inside Aegis, measured. This goes deeper than the
single end-to-end `wrk` number in [`bench/RESULTS.md`](../../bench/RESULTS.md):
it breaks down per-function costs on both the Rust data plane and the Go
control plane.

Environment: same 1-vCPU sandbox as the main benchmark. `perf` is installed
but blocked here (`perf_event_paranoid=4`, confirmed by a failing
`perf record` attempt), so `cargo-flamegraph` and raw `perf` don't work in
this environment. [`pprof-rs`](https://github.com/tikv/pprof-rs) uses
signal/itimer sampling instead of `perf_event_open`, so it works without
elevated privileges and is what produced the flamegraphs below.

## Rust data plane

Reproduce: `cd data-plane && cargo bench --bench hot_path` for the stats, or
`cargo bench --bench hot_path -- --profile-time 10` for flamegraphs (written
to `target/criterion/<name>/profile/flamegraph.svg`).

| Function | Median | Notes |
|---|---|---|
| `load_balancer::select_backend`, round_robin | 723 ns | 10 backends |
| `load_balancer::select_backend`, least_connections | 669 ns | |
| `load_balancer::select_backend`, weighted_round_robin | 658 ns | |
| `load_balancer::select_backend`, consistent_hash | 865 ns | extra hash computation |
| `circuit_breaker::allow_request`, Closed steady state | 252 ns | no transition, no disk I/O |
| `circuit_breaker::allow_request` + `record_failure`, transition storm | 663 µs | forces a `persist()` disk write every call |
| `rate_limiter::allow_request` | 156 ns | |

Committed flamegraphs (representative, not all 7):
[`load_balancer_select-round_robin.svg`](load_balancer_select-round_robin.svg),
[`circuit_breaker-allow_request_closed_steady_state.svg`](circuit_breaker-allow_request_closed_steady_state.svg),
[`circuit_breaker-transition_storm_with_disk_persist.svg`](circuit_breaker-transition_storm_with_disk_persist.svg).

### Finding 1: load balancer selection cost comes from allocation, not the algorithm

All four algorithms land in the same 650 to 950 ns band despite very
different selection logic (round-robin counter, hashing, weight
accumulation). The flamegraph shows why: `select_backend_with_context`
rebuilds the healthy-backends list with `.filter().collect()` on every
single call (45 to 79% of samples across the four algorithms), and cloning
each backend's address string adds another 2 to 8%. The actual algorithm
logic, the counter increment, the hash computation, the weight math, barely
registers next to that allocation. If this ever needed to go faster,
caching the healthy-backend list between config reloads instead of
rebuilding it per request is the first thing to try, not switching
algorithms.

### Finding 2: circuit breaker persistence is safe today, but the cost is real if that changes

Steady-state `allow_request` (252 ns) is pure lock and hashmap lookup, no
disk I/O shows up in its flamegraph. Forcing a transition on every call
(threshold 1, timeout 0) makes the persist step's file open and write
dominate the flamegraph instead, and the cost is about 2,600x higher (663 µs
versus 252 ns). The persistence design (see `docs/decisions/`) was built on
the assumption that state transitions are rare. This benchmark is the actual
evidence for that, not just an assumption: normal traffic never triggers it,
but many backends flapping open and closed at once (say, a bad deploy)
would cost roughly 0.66 ms per flap. Worth knowing if that scenario ever
needs bounding, not urgent today.

## Go control plane

Reproduce: `cd control-plane && go test -bench=. -benchmem -cpuprofile=cpu.prof ./internal/config/`
then `go tool pprof -top cpu.prof`.

The control plane is deliberately off the request hot path. The benchmark
topology itself proves this: `wrk` hits `data-plane:8080` directly, so the
control plane never touches proxied traffic. Its one real per-event CPU cost
is a config reload (`Load` followed by `Validate`), triggered by
`POST /reload` or a data-plane reconnect re-push.

```
BenchmarkLoad       7,252   139,610 ns/op   13,940 B/op   160 allocs/op
BenchmarkValidate 4,156,801      292.8 ns/op       0 B/op     0 allocs/op
```

`Load` (disk read, YAML parse, and `Validate`) costs about 140 µs.
`Validate` alone is about 293 ns and allocates nothing, confirming that YAML
parsing, not validation, is the expensive part of a reload. Unsurprising for
a config file this size.

### Finding: the duplicate-address check is disproportionately hot

`go tool pprof -top` on the combined profile shows `config.validateBackends`
as the single hottest function (13.9% flat, 38.2% cumulative under
`Validate`, more cumulative time than YAML tokenizing itself in this run).
The cause, read directly from `internal/config/config.go:190`:
`seen := make(map[string]bool, len(backends))` allocates a fresh map on
every call, once per backend list (TCP and UDP), purely to catch duplicate
addresses. For the backend counts this project realistically targets
(single digits to low tens), a linear scan would avoid the allocation and
likely be faster. A real candidate for a future pass, not fixed here since
the absolute cost (a few hundred nanoseconds, once per reload, not
per-request) isn't currently worth trading review time for.

## So what's the biggest bottleneck?

Not one single thing. Two different costs on two different planes:

- **Data plane, per request:** the load balancer's per-call list rebuild,
  not the choice of algorithm.
- **Control plane, per reload:** YAML parsing dominates over validation,
  and within validation, an avoidable map allocation for duplicate-checking
  is the single hottest function.

Neither is a problem at today's traffic or reload rates. Both were
invisible until profiled, which is the actual point of doing this:
replacing "probably fine" with a number.
