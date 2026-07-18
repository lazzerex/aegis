use std::sync::Arc;

use prometheus::{CounterVec, Encoder, Gauge, GaugeVec, Opts, Registry, TextEncoder};
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::TcpListener;
use tracing::{error, info, warn};

use crate::config::ProxyState;

/// Serves a Prometheus `/metrics` endpoint directly on the data plane, so
/// traffic-shape visibility survives even if the control plane (which
/// normally re-exposes these via gRPC streaming) is down. Every scrape
/// rebuilds a fresh registry from the current atomic counters — there's no
/// persistent state to keep in sync, just a snapshot of what's true right now.
pub async fn run(state: Arc<ProxyState>, addr: String) -> Result<(), Box<dyn std::error::Error>> {
    let listener = TcpListener::bind(&addr).await?;
    info!("Data plane metrics endpoint listening on {}", addr);

    loop {
        let (mut socket, _) = match listener.accept().await {
            Ok(conn) => conn,
            Err(e) => {
                error!("Failed to accept metrics connection: {}", e);
                continue;
            }
        };

        let state = state.clone();
        tokio::spawn(async move {
            // Drain the request; we serve /metrics unconditionally regardless
            // of path/method, so there's nothing to route on.
            let mut buf = [0u8; 1024];
            let _ = tokio::time::timeout(
                tokio::time::Duration::from_secs(2),
                socket.read(&mut buf),
            )
            .await;

            let body = match render_metrics(&state) {
                Ok(body) => body,
                Err(e) => {
                    warn!("Failed to render metrics: {}", e);
                    return;
                }
            };

            let response = format!(
                "HTTP/1.1 200 OK\r\nContent-Type: text/plain; version=0.0.4\r\nContent-Length: {}\r\nConnection: close\r\n\r\n",
                body.len()
            );

            if let Err(e) = socket.write_all(response.as_bytes()).await {
                warn!("Failed to write metrics response headers: {}", e);
                return;
            }
            if let Err(e) = socket.write_all(&body).await {
                warn!("Failed to write metrics response body: {}", e);
            }
        });
    }
}

fn render_metrics(state: &Arc<ProxyState>) -> Result<Vec<u8>, prometheus::Error> {
    let registry = Registry::new();
    let summary = state.metrics.get_summary();

    macro_rules! gauge {
        ($name:expr, $help:expr, $value:expr) => {{
            let g = Gauge::new($name, $help)?;
            g.set($value);
            registry.register(Box::new(g))?;
        }};
    }
    macro_rules! counter_total {
        ($name:expr, $help:expr, $value:expr) => {{
            let c = prometheus::Counter::new($name, $help)?;
            c.inc_by($value as f64);
            registry.register(Box::new(c))?;
        }};
    }

    gauge!(
        "proxy_tcp_active_connections",
        "Current active TCP connections",
        summary.active_tcp_connections as f64
    );
    gauge!(
        "proxy_udp_active_sessions",
        "Current active UDP sessions",
        summary.active_udp_sessions as f64
    );
    counter_total!(
        "proxy_tcp_connections_total",
        "Total TCP connections handled",
        summary.tcp_connections
    );
    counter_total!(
        "proxy_udp_sessions_total",
        "Total UDP sessions handled",
        summary.udp_sessions
    );
    counter_total!(
        "proxy_bytes_sent_total",
        "Total bytes sent to backends",
        summary.bytes_sent
    );
    counter_total!(
        "proxy_bytes_received_total",
        "Total bytes received from backends",
        summary.bytes_received
    );
    counter_total!(
        "proxy_packets_sent_total",
        "Total UDP packets sent to backends",
        summary.packets_sent
    );
    counter_total!(
        "proxy_packets_received_total",
        "Total UDP packets received from backends",
        summary.packets_received
    );
    counter_total!(
        "proxy_rate_limit_allowed_total",
        "Total requests allowed by the rate limiter",
        summary.rate_limit_allowed
    );
    counter_total!(
        "proxy_rate_limit_denied_total",
        "Total requests denied by the rate limiter",
        summary.rate_limit_denied
    );
    counter_total!(
        "proxy_circuit_breaker_open_total",
        "Total times a circuit breaker tripped open",
        summary.circuit_breaker_open
    );
    counter_total!(
        "proxy_circuit_breaker_half_open_total",
        "Total times a circuit breaker moved to half-open",
        summary.circuit_breaker_half_open
    );
    counter_total!(
        "proxy_pool_hits_total",
        "Total backend connections served from the pre-warmed connection pool",
        summary.pool_hits
    );
    counter_total!(
        "proxy_pool_misses_total",
        "Total backend connections that required a fresh dial (pool empty)",
        summary.pool_misses
    );
    gauge!(
        "proxy_latency_avg_ms",
        "Average backend connect latency in milliseconds",
        summary.latency.avg
    );
    gauge!(
        "proxy_latency_p50_ms",
        "P50 backend connect latency in milliseconds",
        summary.latency.p50
    );
    gauge!(
        "proxy_latency_p90_ms",
        "P90 backend connect latency in milliseconds",
        summary.latency.p90
    );
    gauge!(
        "proxy_latency_p99_ms",
        "P99 backend connect latency in milliseconds",
        summary.latency.p99
    );

    let backend_metrics = state.metrics.get_backend_metrics();
    if !backend_metrics.is_empty() {
        let connections = GaugeVec::new(
            Opts::new(
                "proxy_backend_connections",
                "Active connections per backend",
            ),
            &["backend"],
        )?;
        let requests = CounterVec::new(
            Opts::new("proxy_backend_requests_total", "Total requests per backend"),
            &["backend"],
        )?;
        let failures = CounterVec::new(
            Opts::new("proxy_backend_failures_total", "Total failures per backend"),
            &["backend"],
        )?;
        let backend_bytes_sent = CounterVec::new(
            Opts::new(
                "proxy_backend_bytes_sent_total",
                "Total bytes sent per backend",
            ),
            &["backend"],
        )?;
        let backend_bytes_received = CounterVec::new(
            Opts::new(
                "proxy_backend_bytes_received_total",
                "Total bytes received per backend",
            ),
            &["backend"],
        )?;

        for (addr, m) in backend_metrics.iter() {
            use std::sync::atomic::Ordering;
            connections
                .with_label_values(&[addr])
                .set(m.connections.load(Ordering::Relaxed) as f64);
            requests
                .with_label_values(&[addr])
                .inc_by(m.requests.load(Ordering::Relaxed) as f64);
            failures
                .with_label_values(&[addr])
                .inc_by(m.failures.load(Ordering::Relaxed) as f64);
            backend_bytes_sent
                .with_label_values(&[addr])
                .inc_by(m.bytes_sent.load(Ordering::Relaxed) as f64);
            backend_bytes_received
                .with_label_values(&[addr])
                .inc_by(m.bytes_received.load(Ordering::Relaxed) as f64);
        }

        registry.register(Box::new(connections))?;
        registry.register(Box::new(requests))?;
        registry.register(Box::new(failures))?;
        registry.register(Box::new(backend_bytes_sent))?;
        registry.register(Box::new(backend_bytes_received))?;
    }

    let encoder = TextEncoder::new();
    let mut buffer = Vec::new();
    encoder.encode(&registry.gather(), &mut buffer)?;
    Ok(buffer)
}
