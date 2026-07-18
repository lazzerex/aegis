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

        let total_weight: usize = backends.iter().map(|b| b.backend.weight as usize).sum();
        if total_weight == 0 {
            return self.round_robin(backends);
        }

        let counter = self.round_robin_counter.fetch_add(1, Ordering::Relaxed) % total_weight;

        let mut cumulative: usize = 0;
        for backend in backends {
            cumulative += backend.backend.weight as usize;
            if counter < cumulative {
                return Some(backend.backend.clone());
            }
        }

        self.round_robin(backends)
    }

    /// Consistent hashing for session affinity
    fn consistent_hash(
        &self,
        backends: &[&BackendWithStats],
        context: Option<&str>,
    ) -> Option<Backend> {
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

    /// Update backend list from control plane, preserving active connection counts
    pub fn update_backends(&self, backends: Vec<Backend>) {
        let mut current = self.backends.write();
        let existing: HashMap<String, u64> = current
            .iter()
            .map(|b| {
                (
                    b.backend.address.clone(),
                    b.active_connections.load(Ordering::Relaxed),
                )
            })
            .collect();
        *current = backends
            .into_iter()
            .map(|b| {
                let conns = existing.get(&b.address).copied().unwrap_or(0);
                BackendWithStats {
                    backend: b,
                    active_connections: AtomicU64::new(conns),
                }
            })
            .collect();
    }

    /// Addresses of currently healthy backends, for callers (e.g. the
    /// connection pool) that need to know what to pre-warm without going
    /// through backend selection.
    pub fn healthy_backend_addresses(&self) -> Vec<String> {
        let backends = self.backends.read();
        backends
            .iter()
            .filter(|b| b.backend.healthy)
            .map(|b| b.backend.address.clone())
            .collect()
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

#[cfg(test)]
mod tests {
    use super::*;
    use std::collections::HashMap as StdHashMap;

    fn backend(addr: &str, weight: i32) -> Backend {
        Backend {
            address: addr.to_string(),
            weight,
            healthy: true,
        }
    }

    #[test]
    fn test_round_robin_distributes_evenly() {
        let lb = LoadBalancer::new(
            vec![backend("a", 100), backend("b", 100), backend("c", 100)],
            "round_robin".to_string(),
        );

        let mut counts: StdHashMap<String, u32> = StdHashMap::new();
        for _ in 0..300 {
            let b = lb.select_backend().unwrap();
            *counts.entry(b.address).or_insert(0) += 1;
        }

        assert_eq!(counts.len(), 3);
        for count in counts.values() {
            assert_eq!(*count, 100);
        }
    }

    #[test]
    fn test_least_connections_picks_lowest() {
        let lb = LoadBalancer::new(
            vec![backend("a", 100), backend("b", 100)],
            "least_connections".to_string(),
        );

        lb.increment_connections("a");
        lb.increment_connections("a");
        lb.increment_connections("b");

        // "b" has fewer active connections (1 vs 2), must be selected.
        let selected = lb.select_backend().unwrap();
        assert_eq!(selected.address, "b");
    }

    #[test]
    fn test_weighted_round_robin_respects_weights() {
        let lb = LoadBalancer::new(
            vec![backend("a", 300), backend("b", 100)],
            "weighted_round_robin".to_string(),
        );

        let mut counts: StdHashMap<String, u32> = StdHashMap::new();
        for _ in 0..400 {
            let b = lb.select_backend().unwrap();
            *counts.entry(b.address).or_insert(0) += 1;
        }

        // Weight ratio is 3:1 over one full cycle of total_weight (400).
        assert_eq!(counts["a"], 300);
        assert_eq!(counts["b"], 100);
    }

    /// Regression test for the i32-cast overflow bug (load_balancer.rs:119):
    /// summing large weights used to be cast down to i32 and could wrap:
    /// with two backends near i32::MAX, this must not panic or wrap negative.
    #[test]
    fn test_weighted_round_robin_large_weights_no_overflow() {
        let lb = LoadBalancer::new(
            vec![
                backend("a", i32::MAX / 2),
                backend("b", i32::MAX / 2),
            ],
            "weighted_round_robin".to_string(),
        );

        for _ in 0..10 {
            assert!(lb.select_backend().is_some());
        }
    }

    #[test]
    fn test_consistent_hash_sticky_per_client() {
        let lb = LoadBalancer::new(
            vec![backend("a", 100), backend("b", 100), backend("c", 100)],
            "consistent_hash".to_string(),
        );

        let first = lb
            .select_backend_with_context(Some("client-1.2.3.4"))
            .unwrap();
        for _ in 0..20 {
            let again = lb
                .select_backend_with_context(Some("client-1.2.3.4"))
                .unwrap();
            assert_eq!(again.address, first.address);
        }
    }

    #[test]
    fn test_no_healthy_backends_returns_none() {
        let backends = vec![Backend {
            address: "a".to_string(),
            weight: 100,
            healthy: false,
        }];
        let lb = LoadBalancer::new(backends, "round_robin".to_string());
        assert!(lb.select_backend().is_none());
    }
}
