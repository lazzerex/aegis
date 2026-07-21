use dashmap::DashMap;
use parking_lot::RwLock;
use std::sync::Arc;
use std::time::Duration;
use tokio::sync::Notify;

use crate::circuit_breaker::CircuitBreakerManager;
use crate::load_balancer::LoadBalancer;
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
    pub circuit_breaker: RwLock<Arc<CircuitBreakerManager>>,
    pub rate_limiter: RwLock<Arc<RateLimiter>>,
    pub metrics: Arc<MetricsCollector>,
    tcp_lb: RwLock<Arc<LoadBalancer>>,
    udp_lb: RwLock<Arc<LoadBalancer>>,
}

impl ProxyState {
    pub fn new() -> Self {
        let default_circuit_breaker = Arc::new(CircuitBreakerManager::new(5, 30));
        let default_rate_limiter = Arc::new(RateLimiter::new(1000, 100));
        let metrics = Arc::new(MetricsCollector::new());
        let default_tcp_lb = Arc::new(LoadBalancer::new(vec![], "round_robin".to_string()));
        let default_udp_lb = Arc::new(LoadBalancer::new(vec![], "round_robin".to_string()));

        Self {
            config: RwLock::new(None),
            config_notify: Arc::new(Notify::new()),
            active_connections: DashMap::new(),
            connection_counter: parking_lot::Mutex::new(0),
            draining: parking_lot::Mutex::new(false),
            circuit_breaker: RwLock::new(default_circuit_breaker),
            rate_limiter: RwLock::new(default_rate_limiter),
            metrics,
            tcp_lb: RwLock::new(default_tcp_lb),
            udp_lb: RwLock::new(default_udp_lb),
        }
    }

    pub fn update_config(&self, config: ProxyConfig) {
        let new_cb_timeout = Duration::from_secs(config.circuit_breaker_timeout_secs as u64);
        let current_cb = self.circuit_breaker.read().clone();
        if current_cb.error_threshold() != config.circuit_breaker_threshold
            || current_cb.timeout() != new_cb_timeout
        {
            *self.circuit_breaker.write() = Arc::new(CircuitBreakerManager::new(
                config.circuit_breaker_threshold,
                config.circuit_breaker_timeout_secs,
            ));
        }
        let rate_limiter = Arc::new(RateLimiter::new(
            config.rate_limit_rps as u64,
            config.rate_limit_burst as u64,
        ));
        let tcp_lb = Arc::new(LoadBalancer::new(
            config.backends.clone(),
            config.algorithm.clone(),
        ));
        let udp_lb = Arc::new(LoadBalancer::new(
            config.udp_backends.clone(),
            config.algorithm.clone(),
        ));

        *self.rate_limiter.write() = rate_limiter;
        *self.tcp_lb.write() = tcp_lb;
        *self.udp_lb.write() = udp_lb;
        *self.config.write() = Some(config);
        self.config_notify.notify_waiters();
    }

    pub fn get_tcp_lb(&self) -> Arc<LoadBalancer> {
        self.tcp_lb.read().clone()
    }

    pub fn get_udp_lb(&self) -> Arc<LoadBalancer> {
        self.udp_lb.read().clone()
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

#[cfg(test)]
mod tests {
    use super::*;

    fn test_config(backend_addr: &str) -> ProxyConfig {
        ProxyConfig {
            tcp_address: "0.0.0.0:8080".to_string(),
            udp_address: "0.0.0.0:8081".to_string(),
            backends: vec![Backend {
                address: backend_addr.to_string(),
                weight: 100,
                healthy: true,
            }],
            udp_backends: vec![],
            algorithm: "round_robin".to_string(),
            session_affinity: false,
            rate_limit_rps: 1000,
            rate_limit_burst: 100,
            connect_timeout_secs: 5,
            idle_timeout_secs: 60,
            read_timeout_secs: 30,
            circuit_breaker_threshold: 5,
            circuit_breaker_timeout_secs: 30,
        }
    }

    /// Regression test for the unsound `unsafe` circuit_breaker/rate_limiter
    /// mutation (config.rs:81): concurrent readers must never observe a torn
    /// or panicking state while update_config swaps the RwLock<Arc<T>> fields.
    #[test]
    fn test_update_config_concurrent_reads_dont_panic() {
        let state = Arc::new(ProxyState::new());

        let writer_state = state.clone();
        let writer = std::thread::spawn(move || {
            for i in 0..200 {
                writer_state.update_config(test_config(&format!("backend-{i}:5432")));
            }
        });

        let mut readers = Vec::new();
        for _ in 0..4 {
            let reader_state = state.clone();
            readers.push(std::thread::spawn(move || {
                for _ in 0..500 {
                    let _ = reader_state.get_tcp_lb();
                    let _ = reader_state.get_udp_lb();
                    let _ = reader_state.circuit_breaker.read().clone();
                    let _ = reader_state.rate_limiter.read().clone();
                    let _ = reader_state.get_config();
                }
            }));
        }

        writer.join().expect("writer thread panicked");
        for reader in readers {
            reader.join().expect("reader thread panicked");
        }

        // Final state must reflect the last applied config.
        assert!(state.get_config().is_some());
    }

    #[test]
    fn test_backend_reload_preserves_circuit_breaker_state() {
        let state = ProxyState::new();
        state.update_config(test_config("backend-cb-reload-test:9999"));

        let cb = state.circuit_breaker.read().clone();
        cb.record_failure("backend-cb-reload-test:9999");
        cb.record_failure("backend-cb-reload-test:9999");
        cb.record_failure("backend-cb-reload-test:9999");

        state.update_config(test_config("backend-cb-reload-test-2:9999"));

        let cb_after_reload = state.circuit_breaker.read().clone();
        let (_, failure_count) = cb_after_reload.get_all_states()["backend-cb-reload-test:9999"];
        assert_eq!(
            failure_count, 3,
            "reloading the backend list must not reset an in-progress circuit breaker count"
        );
    }
}
