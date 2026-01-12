use parking_lot::Mutex;
use std::collections::HashMap;
use std::time::{Duration, Instant};

/// Token bucket rate limiter implementation
/// Provides both global and per-connection rate limiting
pub struct TokenBucket {
    capacity: u64,
    tokens: f64,
    refill_rate: f64,
    last_refill: Instant,
}

impl TokenBucket {
    pub fn new(requests_per_second: u64, burst: u64) -> Self {
        Self {
            capacity: burst,
            tokens: burst as f64,
            refill_rate: requests_per_second as f64,
            last_refill: Instant::now(),
        }
    }

    /// Try to consume tokens from the bucket
    pub fn try_consume(&mut self, tokens: u64) -> bool {
        self.refill();

        if self.tokens >= tokens as f64 {
            self.tokens -= tokens as f64;
            true
        } else {
            false
        }
    }

    /// Refill tokens based on elapsed time
    fn refill(&mut self) {
        let now = Instant::now();
        let elapsed = now.duration_since(self.last_refill).as_secs_f64();

        self.tokens = (self.tokens + elapsed * self.refill_rate).min(self.capacity as f64);
        self.last_refill = now;
    }

    /// Get current available tokens
    pub fn available_tokens(&mut self) -> u64 {
        self.refill();
        self.tokens as u64
    }
}

/// Global rate limiter with per-connection tracking
pub struct RateLimiter {
    global_limiter: Mutex<TokenBucket>,
    per_connection_limiters: Mutex<HashMap<String, TokenBucket>>,
    per_connection_limit: Option<(u64, u64)>, // (rps, burst)
    cleanup_interval: Duration,
    last_cleanup: Mutex<Instant>,
}

impl RateLimiter {
    pub fn new(global_rps: u64, global_burst: u64) -> Self {
        Self {
            global_limiter: Mutex::new(TokenBucket::new(global_rps, global_burst)),
            per_connection_limiters: Mutex::new(HashMap::new()),
            per_connection_limit: None,
            cleanup_interval: Duration::from_secs(60),
            last_cleanup: Mutex::new(Instant::now()),
        }
    }

    pub fn with_per_connection_limit(mut self, rps: u64, burst: u64) -> Self {
        self.per_connection_limit = Some((rps, burst));
        self
    }

    /// Check if request should be allowed (global + per-connection limits)
    pub fn allow_request(&self, connection_id: Option<&str>) -> bool {
        // Check global limit first
        if !self.global_limiter.lock().try_consume(1) {
            return false;
        }

        // Check per-connection limit if configured
        if let (Some((rps, burst)), Some(conn_id)) = (self.per_connection_limit, connection_id) {
            let mut limiters = self.per_connection_limiters.lock();

            let limiter = limiters
                .entry(conn_id.to_string())
                .or_insert_with(|| TokenBucket::new(rps, burst));

            if !limiter.try_consume(1) {
                return false;
            }
        }

        // Periodic cleanup of old per-connection limiters
        self.cleanup_old_limiters();

        true
    }

    /// Clean up inactive per-connection limiters
    fn cleanup_old_limiters(&self) {
        let mut last_cleanup = self.last_cleanup.lock();
        if last_cleanup.elapsed() < self.cleanup_interval {
            return;
        }

        let mut limiters = self.per_connection_limiters.lock();
        limiters.retain(|_, limiter| limiter.available_tokens() < limiter.capacity);

        *last_cleanup = Instant::now();
    }

    /// Get current global rate limit status
    pub fn get_global_stats(&self) -> (u64, u64) {
        let mut limiter = self.global_limiter.lock();
        (limiter.available_tokens(), limiter.capacity)
    }

    /// Get number of tracked connections
    pub fn tracked_connections(&self) -> usize {
        self.per_connection_limiters.lock().len()
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::thread;

    #[test]
    fn test_token_bucket_basic() {
        let mut bucket = TokenBucket::new(10, 10);

        // Should allow 10 requests immediately (burst)
        for _ in 0..10 {
            assert!(bucket.try_consume(1));
        }

        // 11th request should fail
        assert!(!bucket.try_consume(1));
    }

    #[test]
    fn test_token_bucket_refill() {
        let mut bucket = TokenBucket::new(10, 5);

        // Consume all tokens
        for _ in 0..5 {
            assert!(bucket.try_consume(1));
        }

        // Wait for refill
        thread::sleep(Duration::from_millis(100));

        // Should have at least 1 token refilled
        assert!(bucket.try_consume(1));
    }

    #[test]
    fn test_rate_limiter_global() {
        let limiter = RateLimiter::new(100, 10);

        // Should allow burst
        for _ in 0..10 {
            assert!(limiter.allow_request(None));
        }

        // Should reject after burst
        assert!(!limiter.allow_request(None));
    }

    #[test]
    fn test_rate_limiter_per_connection() {
        let limiter = RateLimiter::new(1000, 100).with_per_connection_limit(5, 5);

        // Connection 1 should get 5 requests
        for _ in 0..5 {
            assert!(limiter.allow_request(Some("conn1")));
        }

        // 6th request should fail
        assert!(!limiter.allow_request(Some("conn1")));

        // Connection 2 should still work
        assert!(limiter.allow_request(Some("conn2")));
    }
}
