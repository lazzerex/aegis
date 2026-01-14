use parking_lot::RwLock;
use std::collections::HashMap;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::Arc;
use tracing::debug;

use crate::config::ProxyState;

pub async fn stream_metrics(state: Arc<ProxyState>) {
    let mut interval = tokio::time::interval(tokio::time::Duration::from_secs(5));

    loop {
        interval.tick().await;

        let active_connections = state.active_connection_count() as i64;
        let metrics = state.get_metrics();

        debug!(
            "Metrics: active_connections={}, total_tcp={}, total_udp={}, total_bytes_sent={}, total_bytes_received={}",
            active_connections,
            metrics.tcp_connections.load(Ordering::Relaxed),
            metrics.udp_sessions.load(Ordering::Relaxed),
            metrics.bytes_sent.load(Ordering::Relaxed),
            metrics.bytes_received.load(Ordering::Relaxed)
        );

        // TODO: Send metrics via gRPC stream to control plane
    }
}

/// Comprehensive metrics collector for proxy operations
pub struct MetricsCollector {
    // Connection metrics
    pub tcp_connections: AtomicU64,
    pub udp_sessions: AtomicU64,
    pub active_tcp_connections: AtomicU64,
    pub active_udp_sessions: AtomicU64,
    
    // Bandwidth metrics
    pub bytes_sent: AtomicU64,
    pub bytes_received: AtomicU64,
    pub packets_sent: AtomicU64,
    pub packets_received: AtomicU64,
    
    // Performance metrics
    latency_samples: RwLock<Vec<f64>>,
    
    // Per-backend metrics
    backend_metrics: RwLock<HashMap<String, BackendMetrics>>,
    
    // Rate limiting metrics
    pub rate_limit_allowed: AtomicU64,
    pub rate_limit_denied: AtomicU64,
    
    // Circuit breaker metrics
    pub circuit_breaker_open: AtomicU64,
    pub circuit_breaker_half_open: AtomicU64,
}

#[derive(Debug)]
pub struct BackendMetrics {
    pub connections: AtomicU64,
    pub requests: AtomicU64,
    pub failures: AtomicU64,
    pub bytes_sent: AtomicU64,
    pub bytes_received: AtomicU64,
}

impl Clone for BackendMetrics {
    fn clone(&self) -> Self {
        Self {
            connections: AtomicU64::new(self.connections.load(Ordering::Relaxed)),
            requests: AtomicU64::new(self.requests.load(Ordering::Relaxed)),
            failures: AtomicU64::new(self.failures.load(Ordering::Relaxed)),
            bytes_sent: AtomicU64::new(self.bytes_sent.load(Ordering::Relaxed)),
            bytes_received: AtomicU64::new(self.bytes_received.load(Ordering::Relaxed)),
        }
    }
}

impl BackendMetrics {
    fn new() -> Self {
        Self {
            connections: AtomicU64::new(0),
            requests: AtomicU64::new(0),
            failures: AtomicU64::new(0),
            bytes_sent: AtomicU64::new(0),
            bytes_received: AtomicU64::new(0),
        }
    }
}

impl MetricsCollector {
    pub fn new() -> Self {
        Self {
            tcp_connections: AtomicU64::new(0),
            udp_sessions: AtomicU64::new(0),
            active_tcp_connections: AtomicU64::new(0),
            active_udp_sessions: AtomicU64::new(0),
            bytes_sent: AtomicU64::new(0),
            bytes_received: AtomicU64::new(0),
            packets_sent: AtomicU64::new(0),
            packets_received: AtomicU64::new(0),
            latency_samples: RwLock::new(Vec::new()),
            backend_metrics: RwLock::new(HashMap::new()),
            rate_limit_allowed: AtomicU64::new(0),
            rate_limit_denied: AtomicU64::new(0),
            circuit_breaker_open: AtomicU64::new(0),
            circuit_breaker_half_open: AtomicU64::new(0),
        }
    }

    // TCP Connection metrics
    pub fn record_tcp_connection(&self) {
        self.tcp_connections.fetch_add(1, Ordering::Relaxed);
        self.active_tcp_connections.fetch_add(1, Ordering::Relaxed);
    }

    pub fn close_tcp_connection(&self) {
        self.active_tcp_connections.fetch_sub(1, Ordering::Relaxed);
    }

    // UDP Session metrics
    pub fn record_udp_session(&self) {
        self.udp_sessions.fetch_add(1, Ordering::Relaxed);
        self.active_udp_sessions.fetch_add(1, Ordering::Relaxed);
    }

    pub fn close_udp_session(&self) {
        self.active_udp_sessions.fetch_sub(1, Ordering::Relaxed);
    }

    // Bandwidth metrics
    pub fn record_bytes_sent(&self, bytes: u64) {
        self.bytes_sent.fetch_add(bytes, Ordering::Relaxed);
    }

    pub fn record_bytes_received(&self, bytes: u64) {
        self.bytes_received.fetch_add(bytes, Ordering::Relaxed);
    }

    pub fn record_packet_sent(&self) {
        self.packets_sent.fetch_add(1, Ordering::Relaxed);
    }

    pub fn record_packet_received(&self) {
        self.packets_received.fetch_add(1, Ordering::Relaxed);
    }

    // Latency tracking
    pub fn record_latency(&self, duration_ms: f64) {
        let mut samples = self.latency_samples.write();
        samples.push(duration_ms);
        
        // Keep only last 1000 samples
        let len = samples.len();
        if len > 1000 {
            samples.drain(0..(len - 1000));
        }
    }

    pub fn get_latency_stats(&self) -> LatencyStats {
        let samples = self.latency_samples.read();
        
        if samples.is_empty() {
            return LatencyStats::default();
        }

        let mut sorted = samples.clone();
        sorted.sort_by(|a, b| a.partial_cmp(b).unwrap());

        let len = sorted.len();
        let p50 = sorted[len / 2];
        let p90 = sorted[(len * 90) / 100];
        let p99 = sorted[(len * 99) / 100];
        let avg = sorted.iter().sum::<f64>() / len as f64;

        LatencyStats { p50, p90, p99, avg }
    }

    // Backend metrics
    pub fn record_backend_request(&self, backend: &str) {
        let mut backends = self.backend_metrics.write();
        backends
            .entry(backend.to_string())
            .or_insert_with(BackendMetrics::new)
            .requests
            .fetch_add(1, Ordering::Relaxed);
    }

    pub fn record_backend_connection(&self, backend: &str) {
        let mut backends = self.backend_metrics.write();
        backends
            .entry(backend.to_string())
            .or_insert_with(BackendMetrics::new)
            .connections
            .fetch_add(1, Ordering::Relaxed);
    }

    pub fn record_backend_failure(&self, backend: &str) {
        let mut backends = self.backend_metrics.write();
        backends
            .entry(backend.to_string())
            .or_insert_with(BackendMetrics::new)
            .failures
            .fetch_add(1, Ordering::Relaxed);
    }

    pub fn record_backend_bytes_sent(&self, backend: &str, bytes: u64) {
        let mut backends = self.backend_metrics.write();
        backends
            .entry(backend.to_string())
            .or_insert_with(BackendMetrics::new)
            .bytes_sent
            .fetch_add(bytes, Ordering::Relaxed);
    }

    pub fn record_backend_bytes_received(&self, backend: &str, bytes: u64) {
        let mut backends = self.backend_metrics.write();
        backends
            .entry(backend.to_string())
            .or_insert_with(BackendMetrics::new)
            .bytes_received
            .fetch_add(bytes, Ordering::Relaxed);
    }

    pub fn get_backend_metrics(&self) -> HashMap<String, BackendMetrics> {
        self.backend_metrics.read().clone()
    }

    // Rate limiting metrics
    pub fn record_rate_limit_allowed(&self) {
        self.rate_limit_allowed.fetch_add(1, Ordering::Relaxed);
    }

    pub fn record_rate_limit_denied(&self) {
        self.rate_limit_denied.fetch_add(1, Ordering::Relaxed);
    }

    // Circuit breaker metrics
    pub fn record_circuit_breaker_open(&self) {
        self.circuit_breaker_open.fetch_add(1, Ordering::Relaxed);
    }

    pub fn record_circuit_breaker_half_open(&self) {
        self.circuit_breaker_half_open.fetch_add(1, Ordering::Relaxed);
    }

    // Get summary for logging/monitoring
    pub fn get_summary(&self) -> MetricsSummary {
        MetricsSummary {
            tcp_connections: self.tcp_connections.load(Ordering::Relaxed),
            udp_sessions: self.udp_sessions.load(Ordering::Relaxed),
            active_tcp_connections: self.active_tcp_connections.load(Ordering::Relaxed),
            active_udp_sessions: self.active_udp_sessions.load(Ordering::Relaxed),
            bytes_sent: self.bytes_sent.load(Ordering::Relaxed),
            bytes_received: self.bytes_received.load(Ordering::Relaxed),
            packets_sent: self.packets_sent.load(Ordering::Relaxed),
            packets_received: self.packets_received.load(Ordering::Relaxed),
            rate_limit_allowed: self.rate_limit_allowed.load(Ordering::Relaxed),
            rate_limit_denied: self.rate_limit_denied.load(Ordering::Relaxed),
            circuit_breaker_open: self.circuit_breaker_open.load(Ordering::Relaxed),
            circuit_breaker_half_open: self.circuit_breaker_half_open.load(Ordering::Relaxed),
            latency: self.get_latency_stats(),
        }
    }
}

impl Default for MetricsCollector {
    fn default() -> Self {
        Self::new()
    }
}

#[derive(Debug, Clone)]
pub struct LatencyStats {
    pub p50: f64,
    pub p90: f64,
    pub p99: f64,
    pub avg: f64,
}

impl Default for LatencyStats {
    fn default() -> Self {
        Self {
            p50: 0.0,
            p90: 0.0,
            p99: 0.0,
            avg: 0.0,
        }
    }
}

#[derive(Debug, Clone)]
pub struct MetricsSummary {
    pub tcp_connections: u64,
    pub udp_sessions: u64,
    pub active_tcp_connections: u64,
    pub active_udp_sessions: u64,
    pub bytes_sent: u64,
    pub bytes_received: u64,
    pub packets_sent: u64,
    pub packets_received: u64,
    pub rate_limit_allowed: u64,
    pub rate_limit_denied: u64,
    pub circuit_breaker_open: u64,
    pub circuit_breaker_half_open: u64,
    pub latency: LatencyStats,
}
