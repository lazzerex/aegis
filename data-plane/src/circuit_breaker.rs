use parking_lot::RwLock;
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};
use tracing::warn;

/// Circuit breaker states
#[derive(Debug, Clone, Copy, PartialEq, Serialize, Deserialize)]
pub enum CircuitState {
    Closed,   // Normal operation, requests allowed
    Open,     // Failing, requests blocked
    HalfOpen, // Testing if backend recovered
}

/// Circuit breaker for individual backends
pub struct CircuitBreaker {
    state: CircuitState,
    failure_count: u32,
    success_count: u32,
    last_failure_time: Option<Instant>,
    error_threshold: u32,
    timeout: Duration,
    half_open_max_requests: u32,
}

impl CircuitBreaker {
    pub fn new(error_threshold: u32, timeout: Duration) -> Self {
        Self {
            state: CircuitState::Closed,
            failure_count: 0,
            success_count: 0,
            last_failure_time: None,
            error_threshold,
            timeout,
            half_open_max_requests: 3,
        }
    }

    /// Check if request should be allowed through circuit breaker
    pub fn allow_request(&mut self) -> bool {
        match self.state {
            CircuitState::Closed => true,
            CircuitState::Open => {
                // Check if timeout has elapsed to transition to half-open
                if let Some(last_failure) = self.last_failure_time {
                    if last_failure.elapsed() >= self.timeout {
                        self.transition_to_half_open();
                        true
                    } else {
                        false
                    }
                } else {
                    false
                }
            }
            CircuitState::HalfOpen => {
                // Allow limited requests to test backend
                self.success_count < self.half_open_max_requests
            }
        }
    }

    /// Record successful request
    pub fn record_success(&mut self) {
        match self.state {
            CircuitState::Closed => {
                // Reset failure count on success
                self.failure_count = 0;
            }
            CircuitState::HalfOpen => {
                self.success_count += 1;
                // If enough successes in half-open, close circuit
                if self.success_count >= self.half_open_max_requests {
                    self.transition_to_closed();
                }
            }
            CircuitState::Open => {
                // Should not happen, but handle gracefully
            }
        }
    }

    /// Record failed request
    pub fn record_failure(&mut self) {
        match self.state {
            CircuitState::Closed => {
                self.failure_count += 1;
                self.last_failure_time = Some(Instant::now());

                if self.failure_count >= self.error_threshold {
                    self.transition_to_open();
                }
            }
            CircuitState::HalfOpen => {
                // Immediate transition back to open on failure
                self.transition_to_open();
            }
            CircuitState::Open => {
                self.last_failure_time = Some(Instant::now());
            }
        }
    }

    /// Get current circuit state
    pub fn state(&self) -> CircuitState {
        self.state
    }

    /// Get failure count
    pub fn failure_count(&self) -> u32 {
        self.failure_count
    }

    /// Force reset circuit breaker
    pub fn reset(&mut self) {
        self.transition_to_closed();
    }

    fn transition_to_closed(&mut self) {
        self.state = CircuitState::Closed;
        self.failure_count = 0;
        self.success_count = 0;
        self.last_failure_time = None;
    }

    fn transition_to_open(&mut self) {
        self.state = CircuitState::Open;
        self.last_failure_time = Some(Instant::now());
    }

    fn transition_to_half_open(&mut self) {
        self.state = CircuitState::HalfOpen;
        self.success_count = 0;
    }
}

/// On-disk breaker snapshot. `last_failure_time` is a wall-clock epoch, not
/// an `Instant` — `Instant` isn't serializable across a process restart.
#[derive(Serialize, Deserialize)]
struct PersistedBreaker {
    state: CircuitState,
    failure_count: u32,
    last_failure_epoch_ms: Option<u64>,
}

fn now_epoch_ms() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_millis() as u64
}

fn load_persisted(
    path: &str,
    error_threshold: u32,
    timeout: Duration,
) -> HashMap<String, CircuitBreaker> {
    let Ok(data) = std::fs::read_to_string(path) else {
        return HashMap::new();
    };
    let snapshot: HashMap<String, PersistedBreaker> = match serde_json::from_str(&data) {
        Ok(s) => s,
        Err(e) => {
            warn!("failed to parse persisted circuit breaker state at {path}: {e}");
            return HashMap::new();
        }
    };

    let now_ms = now_epoch_ms();
    snapshot
        .into_iter()
        .map(|(addr, p)| {
            let last_failure_time = p.last_failure_epoch_ms.map(|epoch_ms| {
                let age = Duration::from_millis(now_ms.saturating_sub(epoch_ms));
                Instant::now().checked_sub(age).unwrap_or_else(Instant::now)
            });
            let breaker = CircuitBreaker {
                state: p.state,
                failure_count: p.failure_count,
                success_count: 0,
                last_failure_time,
                error_threshold,
                timeout,
                half_open_max_requests: 3,
            };
            (addr, breaker)
        })
        .collect()
}

fn persist(path: &str, breakers: &HashMap<String, CircuitBreaker>) {
    let now_ms = now_epoch_ms();
    let snapshot: HashMap<String, PersistedBreaker> = breakers
        .iter()
        .map(|(addr, b)| {
            let last_failure_epoch_ms = b
                .last_failure_time
                .map(|t| now_ms.saturating_sub(t.elapsed().as_millis() as u64));
            (
                addr.clone(),
                PersistedBreaker {
                    state: b.state,
                    failure_count: b.failure_count,
                    last_failure_epoch_ms,
                },
            )
        })
        .collect();

    match serde_json::to_string(&snapshot) {
        Ok(json) => {
            if let Err(e) = std::fs::write(path, json) {
                warn!("failed to persist circuit breaker state to {path}: {e}");
            }
        }
        Err(e) => warn!("failed to serialize circuit breaker state: {e}"),
    }
}

/// Circuit breaker manager for all backends
pub struct CircuitBreakerManager {
    breakers: RwLock<HashMap<String, CircuitBreaker>>,
    error_threshold: u32,
    timeout: Duration,
    state_file: String,
}

impl CircuitBreakerManager {
    pub fn new(error_threshold: u32, timeout_secs: u32) -> Self {
        let state_file = std::env::var("AEGIS_CB_STATE_FILE")
            .unwrap_or_else(|_| "aegis-cb-state.json".to_string());
        Self::new_with_state_file(error_threshold, timeout_secs, state_file)
    }

    fn new_with_state_file(error_threshold: u32, timeout_secs: u32, state_file: String) -> Self {
        let timeout = Duration::from_secs(timeout_secs as u64);
        let breakers = load_persisted(&state_file, error_threshold, timeout);
        Self {
            breakers: RwLock::new(breakers),
            error_threshold,
            timeout,
            state_file,
        }
    }

    /// Check if request to backend should be allowed
    pub fn allow_request(&self, backend_addr: &str) -> bool {
        let mut breakers = self.breakers.write();
        let breaker = breakers
            .entry(backend_addr.to_string())
            .or_insert_with(|| CircuitBreaker::new(self.error_threshold, self.timeout));

        let before = breaker.state();
        let allowed = breaker.allow_request();
        if breaker.state() != before {
            persist(&self.state_file, &breakers);
        }
        allowed
    }

    /// Record successful request to backend
    pub fn record_success(&self, backend_addr: &str) {
        let mut breakers = self.breakers.write();
        if let Some(breaker) = breakers.get_mut(backend_addr) {
            let before = breaker.state();
            breaker.record_success();
            if breaker.state() != before {
                persist(&self.state_file, &breakers);
            }
        }
    }

    /// Record failed request to backend
    pub fn record_failure(&self, backend_addr: &str) {
        let mut breakers = self.breakers.write();
        let breaker = breakers
            .entry(backend_addr.to_string())
            .or_insert_with(|| CircuitBreaker::new(self.error_threshold, self.timeout));

        let before = breaker.state();
        breaker.record_failure();
        if breaker.state() != before {
            persist(&self.state_file, &breakers);
        }
    }

    /// Get state of specific backend circuit breaker
    pub fn get_state(&self, backend_addr: &str) -> Option<CircuitState> {
        let breakers = self.breakers.read();
        breakers.get(backend_addr).map(|b| b.state())
    }

    /// Get all circuit breaker states for monitoring
    pub fn get_all_states(&self) -> HashMap<String, (CircuitState, u32)> {
        let breakers = self.breakers.read();
        breakers
            .iter()
            .map(|(addr, breaker)| (addr.clone(), (breaker.state(), breaker.failure_count())))
            .collect()
    }

    /// Reset specific backend circuit breaker
    pub fn reset_backend(&self, backend_addr: &str) {
        let mut breakers = self.breakers.write();
        if let Some(breaker) = breakers.get_mut(backend_addr) {
            breaker.reset();
            persist(&self.state_file, &breakers);
        }
    }

    /// Reset all circuit breakers
    pub fn reset_all(&self) {
        let mut breakers = self.breakers.write();
        for breaker in breakers.values_mut() {
            breaker.reset();
        }
        persist(&self.state_file, &breakers);
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_circuit_breaker_basic() {
        let mut breaker = CircuitBreaker::new(3, Duration::from_secs(5));

        // Initially closed
        assert_eq!(breaker.state(), CircuitState::Closed);
        assert!(breaker.allow_request());

        // Record failures
        breaker.record_failure();
        assert_eq!(breaker.state(), CircuitState::Closed);
        breaker.record_failure();
        assert_eq!(breaker.state(), CircuitState::Closed);
        breaker.record_failure();

        // Should be open after threshold
        assert_eq!(breaker.state(), CircuitState::Open);
        assert!(!breaker.allow_request());
    }

    #[test]
    fn test_circuit_breaker_recovery() {
        let mut breaker = CircuitBreaker::new(2, Duration::from_millis(100));

        // Trip circuit
        breaker.record_failure();
        breaker.record_failure();
        assert_eq!(breaker.state(), CircuitState::Open);

        // Wait for timeout
        std::thread::sleep(Duration::from_millis(150));

        // Should transition to half-open
        assert!(breaker.allow_request());
        assert_eq!(breaker.state(), CircuitState::HalfOpen);

        // Record successes to close circuit
        breaker.record_success();
        breaker.record_success();
        breaker.record_success();

        assert_eq!(breaker.state(), CircuitState::Closed);
    }

    #[test]
    fn test_circuit_breaker_manager() {
        // isolated path: default now persists, would leak across test runs
        let path = temp_state_file("manager-basic");
        let _ = std::fs::remove_file(&path);
        let manager = CircuitBreakerManager::new_with_state_file(2, 5, path.clone());

        // Backend should start allowing requests
        assert!(manager.allow_request("backend1"));

        // Record failures
        manager.record_failure("backend1");
        manager.record_failure("backend1");

        // Should be blocked
        assert!(!manager.allow_request("backend1"));

        // Different backend should still work
        assert!(manager.allow_request("backend2"));

        let _ = std::fs::remove_file(&path);
    }

    fn temp_state_file(name: &str) -> String {
        std::env::temp_dir()
            .join(format!(
                "aegis-cb-test-{}-{}.json",
                name,
                std::process::id()
            ))
            .to_string_lossy()
            .to_string()
    }

    #[test]
    fn test_persist_and_reload_preserves_open_state() {
        let path = temp_state_file("reload");
        let _ = std::fs::remove_file(&path);

        let manager = CircuitBreakerManager::new_with_state_file(2, 30, path.clone());
        manager.record_failure("backend1");
        manager.record_failure("backend1");
        assert_eq!(manager.get_state("backend1"), Some(CircuitState::Open));

        let reloaded = CircuitBreakerManager::new_with_state_file(2, 30, path.clone());
        assert_eq!(reloaded.get_state("backend1"), Some(CircuitState::Open));
        let states = reloaded.get_all_states();
        assert_eq!(states["backend1"].1, 2);

        let _ = std::fs::remove_file(&path);
    }

    #[test]
    fn test_persisted_open_breaker_with_elapsed_timeout_probes_half_open() {
        let path = temp_state_file("elapsed");
        let _ = std::fs::remove_file(&path);

        let mut snapshot = HashMap::new();
        snapshot.insert(
            "backend1".to_string(),
            PersistedBreaker {
                state: CircuitState::Open,
                failure_count: 5,
                last_failure_epoch_ms: Some(now_epoch_ms().saturating_sub(100_000)), // 100s ago
            },
        );
        std::fs::write(&path, serde_json::to_string(&snapshot).unwrap()).unwrap();

        let manager = CircuitBreakerManager::new_with_state_file(5, 1, path.clone());
        assert_eq!(manager.get_state("backend1"), Some(CircuitState::Open));

        assert!(manager.allow_request("backend1"));
        assert_eq!(manager.get_state("backend1"), Some(CircuitState::HalfOpen));

        let _ = std::fs::remove_file(&path);
    }

    #[test]
    fn test_missing_state_file_starts_fresh() {
        let path = temp_state_file("missing");
        let _ = std::fs::remove_file(&path);

        let manager = CircuitBreakerManager::new_with_state_file(2, 30, path);
        assert_eq!(manager.get_state("backend1"), None);
    }
}
