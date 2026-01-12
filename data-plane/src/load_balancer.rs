use parking_lot::RwLock;
use std::sync::atomic::{AtomicUsize, Ordering};

use crate::config::Backend;

pub struct LoadBalancer {
    backends: RwLock<Vec<Backend>>,
    algorithm: String,
    round_robin_counter: AtomicUsize,
}

impl LoadBalancer {
    pub fn new(backends: Vec<Backend>, algorithm: String) -> Self {
        Self {
            backends: RwLock::new(backends),
            algorithm,
            round_robin_counter: AtomicUsize::new(0),
        }
    }

    pub fn select_backend(&self) -> Option<Backend> {
        let backends = self.backends.read();
        let healthy: Vec<_> = backends.iter().filter(|b| b.healthy).collect();

        if healthy.is_empty() {
            return None;
        }

        match self.algorithm.as_str() {
            "round_robin" => self.round_robin(&healthy),
            "weighted" => self.weighted_round_robin(&healthy),
            "least_connections" => self.round_robin(&healthy), // TODO: implement properly
            _ => self.round_robin(&healthy),
        }
    }

    fn round_robin(&self, backends: &[&Backend]) -> Option<Backend> {
        if backends.is_empty() {
            return None;
        }

        let index = self.round_robin_counter.fetch_add(1, Ordering::Relaxed) % backends.len();
        Some(backends[index].clone())
    }

    fn weighted_round_robin(&self, backends: &[&Backend]) -> Option<Backend> {
        if backends.is_empty() {
            return None;
        }

        // Calculate total weight
        let total_weight: i32 = backends.iter().map(|b| b.weight).sum();
        if total_weight == 0 {
            return self.round_robin(backends);
        }

        // Select based on weight distribution
        let mut counter = self.round_robin_counter.fetch_add(1, Ordering::Relaxed) as i32;
        counter %= total_weight;

        let mut cumulative = 0;
        for backend in backends {
            cumulative += backend.weight;
            if counter < cumulative {
                return Some((*backend).clone());
            }
        }

        // Fallback
        self.round_robin(backends)
    }

    pub fn update_backends(&self, backends: Vec<Backend>) {
        *self.backends.write() = backends;
    }
}
