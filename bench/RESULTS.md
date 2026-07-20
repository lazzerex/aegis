# Benchmark Results

## Methodology

`bench/run-bench.sh` runs an isolated stack (separate from the main `docker-compose.yml`): nginx as the backend, and Aegis (data-plane + control-plane, built from the real Dockerfiles) in front of it. The same nginx instance backs both arms of the comparison, so the only variable measured is the overhead Aegis itself adds:

- **direct** — `wrk` → nginx (baseline, no proxy)
- **aegis** — `wrk` → Aegis TCP proxy (`:8080`) → nginx

`wrk` runs via the `williamyeh/wrk` Docker image on the same Docker network as the stack — no local install required, no host-level variance.

```bash
./bench/run-bench.sh [duration] [connections] [threads]
# defaults: 15s 100 4
```

## Environment

Run in a 1-vCPU sandbox (confirmed via `nproc` / `docker info`) — one core shared across nginx, the `wrk` load generator, both Aegis processes, and the Docker daemon. Absolute throughput below is sandbox-limited and should not be read as production capacity on real hardware. The direct-vs-through-Aegis *ratio* is the meaningful number here, since it isolates proxy overhead from whatever the box can do; treat it as directional rather than a formal SLA.

## Results (2026-07-19, 15s / 100 connections / 4 threads)

| | direct → nginx | through Aegis | delta |
|---|---|---|---|
| Requests/sec | 4513.18 | 3270.96 | -27.5% |
| Latency p50 | 19.80ms | 23.38ms | +18% |
| Latency p90 | 33.54ms | 60.10ms | +79% |
| Latency p99 | 61.95ms | 73.29ms | +18% |

Raw `wrk` output: [`bench/results/direct-20260719T171822Z.txt`](results/direct-20260719T171822Z.txt), [`bench/results/aegis-20260719T171822Z.txt`](results/aegis-20260719T171822Z.txt).

The p90 gap is proportionally the largest of the three latency percentiles — consistent with connection-pool misses under load being the dominant source of added tail latency, rather than a fixed per-byte proxying cost. Re-running this benchmark against a fix for that would be the natural next data point (not yet done).

## Postmortem: connection-pool deadlock under load

**Symptom.** At concurrency ≥ ~90–100 connections, the benchmark didn't degrade gracefully — it produced zero completed requests and a permanently unresponsive data-plane. Both the proxy port and the independent metrics port stopped answering and never recovered without a manual restart.

**Root cause.** `data-plane/src/connection.rs`'s connection-pool refill task held a `DashMap::entry()` guard — a synchronous, non-async lock — across a `TcpStream::connect().await`. A concurrent `pool.take()` call on the same backend then blocked *synchronously* trying to acquire that same shard lock. On a single-worker Tokio runtime (exactly what a 1-vCPU host gives you), that's a full deadlock: the one worker thread that could have driven the pending `connect()` to completion is the same thread stuck blocked on the lock. On a multi-core host this would present as a latency spike rather than a total freeze — likely why it hadn't been caught before this benchmark — but the bug is real independent of core count.

**Fix.** Changed the pool's storage from `DashMap<String, Mutex<VecDeque<..>>>` to `DashMap<String, Arc<Mutex<VecDeque<..>>>>`, cloning the `Arc` out and dropping the `DashMap` shard guard before ever crossing an `.await` point. See `data-plane/src/connection.rs`.

**Verification.** Reproduced with targeted tracing at every lock/await boundary to pin down the exact stuck call, then confirmed the fix holds at c100 and c200 with no wedge and a live, responsive process throughout.

## Also found while benchmarking

- **`Dockerfile.data` cargo-chef cache invalidation.** The `chef cook` dependency-caching step ran without the `RUSTFLAGS='-C target-feature=+crt-static'` flag that the final `cargo build` step sets, so the cache key never matched and the full ~15-minute dependency tree compiled twice on every build. Fixed by matching the flags on both steps.
- **Control plane didn't re-push config after an independent data-plane restart.** The data plane would come back from a crash/restart at "Waiting for configuration…" and stay there indefinitely — proxying dead — until the control plane was *also* restarted, since the existing gRPC reconnect logic only covered the metrics stream. Fixed in a later session (`internal/grpc/client.go`'s `WatchReconnect`); see [`docs/decisions/0002-grpc-for-control-data-plane-communication.md`](../docs/decisions/0002-grpc-for-control-data-plane-communication.md) for why gRPC's connection-state machine made that fix possible, and `TASK.md` for the full history including a second, related bug the fix's own test suite caught.
