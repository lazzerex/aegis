# Architecture Decision Records

Short records of the significant design decisions in Aegis: what was decided, what else was considered, and the trade-offs actually accepted — not just what shipped. Written to answer the questions a senior reviewer would ask (see `ENGINEERING_DIRECTION.md`'s "Expected Reviewer Questions"), not to restate what the code already shows.

| ADR | Decision |
|---|---|
| [0001](0001-go-control-plane-rust-data-plane.md) | Split control plane (Go) and data plane (Rust) as separate processes |
| [0002](0002-grpc-for-control-data-plane-communication.md) | gRPC (protobuf/HTTP2) for control-plane ↔ data-plane communication |
| [0003](0003-concurrency-model.md) | Tokio async + `RwLock<Arc<T>>` in Rust; goroutines + plain mutexes in Go |
| [0004](0004-load-balancing-strategy.md) | Four selectable load balancing algorithms, not a plugin architecture |
| [0005](0005-single-node-v1.md) | Single-node v1: no cross-instance shared state yet |

## Format

Each ADR has: **Context** (the problem, not the solution), **Decision**, **Alternatives Considered** (with *why not*, not just *what else exists*), and **Consequences** (benefits **and** drawbacks — a decision with no listed drawbacks wasn't examined honestly).
