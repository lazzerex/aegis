use parking_lot::RwLock;
use std::collections::HashMap;
use std::hash::{Hash, Hasher};
use std::sync::atomic::{AtomicU64, AtomicUsize, Ordering};

use crate::config::Backend;

/// Load balancing algorithms for distributing traffic across backends
pub enum Algorithm {
    RoundRobin,
    LeastConnections,
    WeightedRoundRobin,
    ConsistentHash,
}

impl Algorithm {
    pub fn from_str(s: &str) -> Self {
        match s {
            "least_connections" => Algorithm::LeastConnections,
            "weighted_round_robin" | "weighted" => Algorithm::WeightedRoundRobin,
            "consistent_hash" => Algorithm::ConsistentHash,
            _ => Algorithm::RoundRobin,
        }
    }
}

pub struct LoadBalancer {
    backends: RwLock<Vec<BackendWithStats>>,
    algorithm: Algorithm,
    round_robin_counter: AtomicUsize,
}

/// Backend with connection tracking for least-connections algorithm
pub struct BackendWithStats {
    pub backend: Backend,
    pub active_connections: AtomicU64,
}

impl Clone for BackendWithStats {
    fn clone(&self) -> Self {
        Self {
            backend: self.backend.clone(),
            active_connections: AtomicU64::new(self.active_connections.load(Ordering::Relaxed)),
        }
    }
}

impl LoadBalancer {
    pub fn new(backends: Vec<Backend>, algorithm: String) -> Self {
        let backends_with_stats = backends
            .into_iter()
            .map(|b| BackendWithStats {
                backend: b,
                active_connections: AtomicU64::new(0),
            })
            .collect();

        Self {
            backends: RwLock::new(backends_with_stats),
            algorithm: Algorithm::from_str(&algorithm),
            round_robin_counter: AtomicUsize::new(0),
        }
    }

    /// Select a backend based on configured algorithm
    pub fn select_backend(&self) -> Option<Backend> {
        self.select_backend_with_context(None)
    }

    /// Select backend with optional context (e.g., client IP for consistent hashing)
    pub fn select_backend_with_context(&self, context: Option<&str>) -> Option<Backend> {
        let backends = self.backends.read();
        let healthy: Vec<_> = backends.iter().filter(|b| b.backend.healthy).collect();

        if healthy.is_empty() {
            return None;
        }

        match self.algorithm {
            Algorithm::RoundRobin => self.round_robin(&healthy),
            Algorithm::LeastConnections => self.least_connections(&healthy),
            Algorithm::WeightedRoundRobin => self.weighted_round_robin(&healthy),
            Algorithm::ConsistentHash => self.consistent_hash(&healthy, context),
        }
    }

    /// Simple round-robin selection
    fn round_robin(&self, backends: &[&BackendWithStats]) -> Option<Backend> {
        if backends.is_empty() {
            return None;
        }

        let index = self.round_robin_counter.fetch_add(1, Ordering::Relaxed) % backends.len();
        Some(backends[index].backend.clone())
    }

    /// Least connections: select backend with fewest active connections
    fn least_connections(&self, backends: &[&BackendWithStats]) -> Option<Backend> {
        if backends.is_empty() {
            return None;
        }

        // Find backend with minimum active connections
        let mut min_connections = u64::MAX;
        let mut selected_idx = 0;

        for (idx, backend) in backends.iter().enumerate() {
            let connections = backend.active_connections.load(Ordering::Relaxed);
            if connections < min_connections {
                min_connections = connections;
                selected_idx = idx;
            }
        }

        Some(backends[selected_idx].backend.clone())
    }

    /// Weighted round-robin based on backend weights
    fn weighted_round_robin(&self, backends: &[&BackendWithStats]) -> Option<Backend> {
        if backends.is_empty() {
            return None;
        }

        // Calculate total weight
        let total_weight: i32 = backends.iter().map(|b| b.backend.weight).sum();
        if total_weight == 0 {
            return self.round_robin(backends);
        }

        // Select based on weight distribution
        let mut counter = self.round_robin_counter.fetch_add(1, Ordering::Relaxed) as i32;
        counter %= total_weight;

        let mut cumulative = 0;
        for backend in backends {
            cumulative += backend.backend.weight;
            if counter < cumulative {
                return Some(backend.backend.clone());
            }
        }

        // Fallback to round-robin
        self.round_robin(backends)
    }

    /// Consistent hashing for session affinity
    fn consistent_hash(&self, backends: &[&BackendWithStats], context: Option<&str>) -> Option<Backend> {
        if backends.is_empty() {
            return None;
        }

        // Use context (e.g., client IP) for hash, or fall back to round-robin
        let hash_value = if let Some(ctx) = context {
            let mut hasher = std::collections::hash_map::DefaultHasher::new();
            ctx.hash(&mut hasher);
            hasher.finish() as usize
        } else {
            return self.round_robin(backends);
        };

        let index = hash_value % backends.len();
        Some(backends[index].backend.clone())
    }

    /// Increment active connection count for a backend
    pub fn increment_connections(&self, backend_addr: &str) {
        let backends = self.backends.read();
        for backend in backends.iter() {
            if backend.backend.address == backend_addr {
                backend.active_connections.fetch_add(1, Ordering::Relaxed);
                break;
            }
        }
    }

    /// Decrement active connection count for a backend
    pub fn decrement_connections(&self, backend_addr: &str) {
        let backends = self.backends.read();
        for backend in backends.iter() {
            if backend.backend.address == backend_addr {
                backend.active_connections.fetch_sub(1, Ordering::Relaxed);
                break;
            }
        }
    }

    /// Update backend list from control plane
    pub fn update_backends(&self, backends: Vec<Backend>) {
        let backends_with_stats = backends
            .into_iter()
            .map(|b| BackendWithStats {
                backend: b,
                active_connections: AtomicU64::new(0),
            })
            .collect();
        *self.backends.write() = backends_with_stats;
    }

    /// Get connection statistics for monitoring
    pub fn get_backend_stats(&self) -> HashMap<String, u64> {
        let backends = self.backends.read();
        backends
            .iter()
            .map(|b| {
                (
                    b.backend.address.clone(),
                    b.active_connections.load(Ordering::Relaxed),
                )
            })
            .collect()
    }
}
