# ADR-0005: Single-node v1 — no cross-instance shared state

## Status

Accepted for v1.x. Revisit for v2.

## Context

Per-backend state — circuit breaker status, rate limiter token buckets — lives in-process, guarded by the `RwLock<Arc<T>>` pattern from ADR-0003, and (as of this session) persisted to a local file across a single instance's restarts. Running two Aegis data-plane instances side by side today gives each one its own independent circuit breaker view and its own independent rate-limit budget for the same backends — they don't coordinate.

## Decision

v1.0 targets a single data-plane instance (with an independently-restartable control plane, per ADR-0001) rather than building cross-instance coordination now.

## Alternatives Considered

**Redis-backed shared rate limiter / circuit breaker state**, enabling true horizontal scaling from day one. Deferred to v2 — explicitly listed in `ENGINEERING_DIRECTION.md`'s Feature Policy under both "Multi-node clustering" and "Redis coordination." Building this now would mean taking on an operational dependency (a broker/store to run and monitor) and a consistency question (how stale can shared breaker state be before a decision made on it is wrong?) before the single-instance case was fully hardened. That hardening turned out to matter: this session's file-persistence work for the *single-instance* case — by itself, no network, no other writers — still surfaced a real reconnect bug and a real test-isolation bug. Distributed state coordination is a strictly harder version of a problem that wasn't fully solved yet at the easy end.

**Kubernetes-native service discovery + Helm autoscaling now**, instead of later. Deferred for the same underlying reason. The Helm chart in `charts/aegis/` already ships with HPA (horizontal pod autoscaling) disabled by default, and says why directly in its own README: replicas don't share rate-limiter or circuit-breaker state yet, so autoscaling would just mean N independent, uncoordinated views of backend health instead of one correct one.

## Consequences

**Benefits**
- The single-node case gets to be solid and well-tested before distributed-systems complexity is layered on top of it — matches the project's stated priority order (`ENGINEERING_DIRECTION.md`: reliability and testing rank above new capability).
- No extra infrastructure (a broker, a shared store) to run, secure, or fail, for a deployment shape (one instance, or several uncoordinated ones) that doesn't need it.

**Drawbacks**
- This is a real, current limitation, not just a documentation gap: Aegis does not horizontally scale correctly today. Running N instances behind a shared VIP or load balancer gives each instance its own rate-limit budget (so the *effective* limit is N× the configured one) and its own circuit breaker state (so one instance can keep hammering a backend the others have already correctly given up on). Anyone deploying more than one instance against the same backend set should know this going in.
- The v2 roadmap items this defers to (multi-node coordination, distributed control plane, Redis-backed shared state) are real, unstarted future work — this ADR records a scoping decision, not a solved problem.
