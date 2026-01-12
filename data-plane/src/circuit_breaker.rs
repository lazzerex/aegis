use parking_lot::RwLock;
use std::collections::HashMap;
use std::time::{Duration, Instant};

/// Circuit breaker states
#[derive(Debug, Clone, Copy, PartialEq)]
pub enum CircuitState {
    Closed,      // Normal operation, requests allowed
    Open,        // Failing, requests blocked
    HalfOpen,    // Testing if backend recovered
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

/// Circuit breaker manager for all backends
pub struct CircuitBreakerManager {
    breakers: RwLock<HashMap<String, CircuitBreaker>>,
    error_threshold: u32,
    timeout: Duration,
}

impl CircuitBreakerManager {
    pub fn new(error_threshold: u32, timeout_secs: u32) -> Self {
        Self {
            breakers: RwLock::new(HashMap::new()),
            error_threshold,
            timeout: Duration::from_secs(timeout_secs as u64),
        }
    }

    /// Check if request to backend should be allowed
    pub fn allow_request(&self, backend_addr: &str) -> bool {
        let mut breakers = self.breakers.write();
        let breaker = breakers
            .entry(backend_addr.to_string())
            .or_insert_with(|| CircuitBreaker::new(self.error_threshold, self.timeout));

        breaker.allow_request()
    }

    /// Record successful request to backend
    pub fn record_success(&self, backend_addr: &str) {
        let mut breakers = self.breakers.write();
        if let Some(breaker) = breakers.get_mut(backend_addr) {
            breaker.record_success();
        }
    }

    /// Record failed request to backend
    pub fn record_failure(&self, backend_addr: &str) {
        let mut breakers = self.breakers.write();
        let breaker = breakers
            .entry(backend_addr.to_string())
            .or_insert_with(|| CircuitBreaker::new(self.error_threshold, self.timeout));

        breaker.record_failure();
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
        }
    }

    /// Reset all circuit breakers
    pub fn reset_all(&self) {
        let mut breakers = self.breakers.write();
        for breaker in breakers.values_mut() {
            breaker.reset();
        }
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
        let manager = CircuitBreakerManager::new(2, 5);

        // Backend should start allowing requests
        assert!(manager.allow_request("backend1"));

        // Record failures
        manager.record_failure("backend1");
        manager.record_failure("backend1");

        // Should be blocked
        assert!(!manager.allow_request("backend1"));

        // Different backend should still work
        assert!(manager.allow_request("backend2"));
    }
}
