# ADR-0004: Load balancing strategy

## Status

Accepted, implemented.

## Context

Different backend workloads want different distribution behavior: stateless HTTP-shaped services want traffic spread evenly; a pool with heterogeneous backend capacity wants traffic weighted toward the bigger instances; workloads with client-side or session state (long-lived connections, in-memory session data) want the same client consistently routed to the same backend.

## Decision

Implement four algorithms behind one `LoadBalancer` type (`data-plane/src/load_balancer.rs`), selected by a config string (`round_robin`, `least_connections`, `weighted_round_robin`, `consistent_hash`) and swappable at runtime via the same `RwLock<Arc<LoadBalancer>>` pattern described in ADR-0003 — a config reload can change the algorithm without a restart.

## Alternatives Considered

**Power-of-two-choices (P2C).** Provably near-optimal distribution with O(1) state per selection; it's the default in some production proxies (e.g. Envoy). Not implemented: it's a genuinely stronger algorithm than plain least-connections at large backend-pool sizes, but it also introduces a randomness source and a selection rule that's harder to explain and reason about than the four already implemented, for marginal benefit at the pool sizes this project targets. A reasonable v1.1/v2 addition once there's a workload that actually needs it — not a v1.0 requirement.

**A single fixed algorithm (round robin only).** The simplest possible implementation. Rejected: the entire point of a load balancer is this choice, and both weighted distribution (heterogeneous backend capacity) and session affinity (consistent hashing) are common enough production requirements that omitting them would make "load balancing" an incomplete feature, not a simpler one.

**A pluggable/external algorithm registry** (trait objects, dynamic dispatch, third-party algorithm registration). Rejected per the project's own feature policy — `ENGINEERING_DIRECTION.md` explicitly defers "additional load balancing algorithms" to Future Work. A plugin architecture is exactly the kind of speculative extensibility that isn't justified until there's a second real consumer that needs to add an algorithm without editing this file; right now there isn't one.

## Consequences

**Benefits**
- Covers the three practically distinct routing needs (even spread, capacity-aware, sticky) with a single enum dispatch (`Algorithm::from_str` + a `match`) instead of a trait-object hierarchy that would need to exist for exactly four implementations.
- Algorithm choice is a config value, not a compile-time or process-restart decision.
- All four are unit-tested, including a regression test for a real bug found here: weighted round robin's total-weight sum used to be cast to `i32` and could overflow with large weights; `test_weighted_round_robin_large_weights_no_overflow` pins that fix.

**Drawbacks**
- `consistent_hash` here is `hash(client_ip) % backend_count`, not a hash-ring implementation. That means a backend joining or leaving remaps a larger fraction of clients than true ring-based consistent hashing would — the whole point of a hash ring is minimizing remapping on membership change, and this doesn't do that. Acceptable at the current scale; a deliberate simplification, not an oversight.
- `least_connections` does an O(n) linear scan for the minimum on every selection. Fine at the backend-pool sizes a proxy like this realistically targets (single to low double digits); would need a heap-backed implementation if that assumption stopped holding.
