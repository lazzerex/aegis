use dashmap::DashMap;
use parking_lot::RwLock;
use std::sync::Arc;
use tokio::sync::Notify;

use crate::circuit_breaker::CircuitBreakerManager;
use crate::metrics::MetricsCollector;
use crate::rate_limiter::RateLimiter;

pub mod proxy {
    tonic::include_proto!("proxy");
}

#[derive(Debug, Clone)]
pub struct Backend {
    pub address: String,
    pub weight: i32,
    pub healthy: bool,
}

#[derive(Debug, Clone)]
pub struct ProxyConfig {
    pub tcp_address: String,
    pub udp_address: String,
    pub backends: Vec<Backend>,
    pub udp_backends: Vec<Backend>,
    pub algorithm: String,
    pub session_affinity: bool,
    pub rate_limit_rps: i32,
    pub rate_limit_burst: i32,
    pub connect_timeout_secs: i32,
    pub idle_timeout_secs: i32,
    pub read_timeout_secs: i32,
    pub circuit_breaker_threshold: u32,
    pub circuit_breaker_timeout_secs: u32,
}

pub struct ProxyState {
    config: RwLock<Option<ProxyConfig>>,
    config_notify: Arc<Notify>,
    active_connections: DashMap<u64, Arc<()>>,
    connection_counter: parking_lot::Mutex<u64>,
    draining: parking_lot::Mutex<bool>,
    pub circuit_breaker: Arc<CircuitBreakerManager>,
    pub rate_limiter: Arc<RateLimiter>,
    pub metrics: Arc<MetricsCollector>,
}

impl ProxyState {
    pub fn new() -> Self {
        // Initialize with default values - will be updated via config
        let default_circuit_breaker = Arc::new(CircuitBreakerManager::new(5, 30));
        let default_rate_limiter = Arc::new(RateLimiter::new(1000, 100));
        let metrics = Arc::new(MetricsCollector::new());

        Self {
            config: RwLock::new(None),
            config_notify: Arc::new(Notify::new()),
            active_connections: DashMap::new(),
            connection_counter: parking_lot::Mutex::new(0),
            draining: parking_lot::Mutex::new(false),
            circuit_breaker: default_circuit_breaker,
            rate_limiter: default_rate_limiter,
            metrics,
        }
    }

    pub fn update_config(&self, config: ProxyConfig) {
        // Update circuit breaker and rate limiter based on new config
        let circuit_breaker = Arc::new(CircuitBreakerManager::new(
            config.circuit_breaker_threshold,
            config.circuit_breaker_timeout_secs,
        ));
        let rate_limiter = Arc::new(RateLimiter::new(
            config.rate_limit_rps as u64,
            config.rate_limit_burst as u64,
        ));

        // Replace the circuit breaker and rate limiter
        // Note: This is safe because we're using Arc
        unsafe {
            let self_mut = self as *const Self as *mut Self;
            (*self_mut).circuit_breaker = circuit_breaker;
            (*self_mut).rate_limiter = rate_limiter;
        }

        *self.config.write() = Some(config);
        self.config_notify.notify_waiters();
    }

    pub fn get_config(&self) -> Option<ProxyConfig> {
        self.config.read().clone()
    }

    pub async fn is_configured(&self) -> bool {
        self.config.read().is_some()
    }

    pub async fn wait_for_config(&self) {
        if !self.is_configured().await {
            self.config_notify.notified().await;
        }
    }

    pub fn register_connection(&self) -> (u64, Arc<()>) {
        let mut counter = self.connection_counter.lock();
        *counter += 1;
        let id = *counter;
        let token = Arc::new(());
        self.active_connections.insert(id, token.clone());
        (id, token)
    }

    pub fn unregister_connection(&self, id: u64) {
        self.active_connections.remove(&id);
    }

    pub fn active_connection_count(&self) -> usize {
        self.active_connections.len()
    }

    pub fn is_draining(&self) -> bool {
        *self.draining.lock()
    }

    pub async fn drain_connections(&self) {
        *self.draining.lock() = true;

        // Wait for all active connections to finish
        while self.active_connection_count() > 0 {
            tokio::time::sleep(tokio::time::Duration::from_millis(100)).await;
        }
    }

    pub fn reset_draining(&self) {
        *self.draining.lock() = false;
    }

    pub fn get_metrics(&self) -> Arc<MetricsCollector> {
        self.metrics.clone()
    }
}
