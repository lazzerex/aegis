use std::sync::Arc;
use tracing::debug;

use crate::config::ProxyState;

pub async fn stream_metrics(state: Arc<ProxyState>) {
    let mut interval = tokio::time::interval(tokio::time::Duration::from_secs(5));

    loop {
        interval.tick().await;

        let active_connections = state.active_connection_count() as i64;
        
        debug!(
            "Metrics: active_connections={}",
            active_connections
        );

        // In a real implementation, we would:
        // 1. Collect detailed metrics (bytes sent/received, latency, etc.)
        // 2. Send via gRPC stream to control plane
        // For now, this is a placeholder for the metrics collection loop
    }
}

pub struct MetricsCollector {
    // TODO: Add fields for tracking:
    // - Total connections
    // - Bytes sent/received
    // - Latency histograms
    // - Per-backend metrics
}

impl MetricsCollector {
    pub fn new() -> Self {
        Self {}
    }

    pub fn record_connection(&self) {
        // TODO: Implement
    }

    pub fn record_bytes_sent(&self, _bytes: u64) {
        // TODO: Implement
    }

    pub fn record_bytes_received(&self, _bytes: u64) {
        // TODO: Implement
    }

    pub fn record_latency(&self, _duration_ms: f64) {
        // TODO: Implement
    }
}
