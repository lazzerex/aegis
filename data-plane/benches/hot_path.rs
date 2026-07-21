use std::hint::black_box;

use aegis_data::circuit_breaker::CircuitBreakerManager;
use aegis_data::config::Backend;
use aegis_data::load_balancer::LoadBalancer;
use aegis_data::rate_limiter::RateLimiter;

use criterion::{criterion_group, criterion_main, Criterion};
use pprof::criterion::{Output, PProfProfiler};

fn backends(n: usize) -> Vec<Backend> {
    (0..n)
        .map(|i| Backend {
            address: format!("backend-{i}:8080"),
            weight: 100,
            healthy: true,
        })
        .collect()
}

fn bench_load_balancer(c: &mut Criterion) {
    let mut group = c.benchmark_group("load_balancer_select");
    for algo in [
        "round_robin",
        "least_connections",
        "weighted_round_robin",
        "consistent_hash",
    ] {
        let lb = LoadBalancer::new(backends(10), algo.to_string());
        group.bench_function(algo, |b| {
            b.iter(|| lb.select_backend_with_context(Some(black_box("198.51.100.7"))))
        });
    }
    group.finish();
}

fn temp_path(name: &str) -> String {
    std::env::temp_dir()
        .join(format!("aegis-bench-cb-{name}-{}.json", std::process::id()))
        .to_string_lossy()
        .into_owned()
}

fn bench_circuit_breaker(c: &mut Criterion) {
    let mut group = c.benchmark_group("circuit_breaker");

    let steady_path = temp_path("steady");
    let _ = std::fs::remove_file(&steady_path);
    let steady = CircuitBreakerManager::new_with_state_file(1000, 30, steady_path.clone());
    group.bench_function("allow_request_closed_steady_state", |b| {
        b.iter(|| steady.allow_request(black_box("backend-a")))
    });
    let _ = std::fs::remove_file(&steady_path);

    let storm_path = temp_path("storm");
    let _ = std::fs::remove_file(&storm_path);
    let storm = CircuitBreakerManager::new_with_state_file(1, 0, storm_path.clone());
    group.bench_function("transition_storm_with_disk_persist", |b| {
        b.iter(|| {
            storm.allow_request(black_box("backend-a"));
            storm.record_failure(black_box("backend-a"));
        })
    });
    let _ = std::fs::remove_file(&storm_path);

    group.finish();
}

fn bench_rate_limiter(c: &mut Criterion) {
    let limiter = RateLimiter::new(1_000_000, 1_000_000);
    c.bench_function("rate_limiter_allow_request", |b| {
        b.iter(|| limiter.allow_request(black_box(Some("198.51.100.7"))))
    });
}

fn profiled() -> Criterion {
    Criterion::default().with_profiler(PProfProfiler::new(100, Output::Flamegraph(None)))
}

criterion_group! {
    name = benches;
    config = profiled();
    targets = bench_load_balancer, bench_circuit_breaker, bench_rate_limiter
}
criterion_main!(benches);
