use std::collections::VecDeque;
use std::sync::Arc;
use std::time::{Duration, Instant};

use dashmap::DashMap;
use tokio::net::TcpStream;
use tokio::sync::Mutex;
use tracing::debug;

use crate::config::ProxyState;

/// Idle connections older than this are dropped rather than handed to a
/// client — bounds how stale a pre-warmed socket can get before we'd rather
/// pay a fresh handshake than risk a backend/NAT-timed-out connection.
const MAX_IDLE_AGE: Duration = Duration::from_secs(30);
const REFILL_INTERVAL: Duration = Duration::from_millis(200);

struct PooledConn {
    stream: TcpStream,
    created_at: Instant,
}

/// Pre-warms idle TCP connections to each backend so a new client can skip
/// the connect handshake on the hot path. This is NOT a request-level reuse
/// pool: each pre-warmed connection is handed to exactly one client for the
/// lifetime of that client's session, same as a freshly dialed connection
/// would be. Aegis proxies opaque byte streams (e.g. the Postgres wire
/// protocol) and has no way to know when it's safe to hand a connection to
/// a *different* client mid-session — that would require parsing the
/// backend protocol (PgBouncer-style pooling), which is out of scope here.
pub struct ConnectionPool {
    pools: DashMap<String, Mutex<VecDeque<PooledConn>>>,
    target_size: usize,
}

impl ConnectionPool {
    pub fn new(target_size: usize) -> Arc<Self> {
        Arc::new(Self {
            pools: DashMap::new(),
            target_size,
        })
    }

    /// Take a pre-warmed connection for `backend_addr`, if one is available
    /// and not too old. Returns `None` on a pool miss — callers must fall
    /// back to dialing fresh.
    pub async fn take(&self, backend_addr: &str) -> Option<TcpStream> {
        let queue = self.pools.get(backend_addr)?;
        let mut queue = queue.lock().await;
        while let Some(conn) = queue.pop_front() {
            if conn.created_at.elapsed() < MAX_IDLE_AGE {
                return Some(conn.stream);
            }
            // Stale — drop it and check the next one instead of falling
            // straight through to a miss.
        }
        None
    }

    /// Background task that keeps each currently-healthy backend topped up
    /// with idle connections, and drops pools for backends that disappeared
    /// from config (so we don't leak sockets to a backend that was removed).
    pub fn spawn_refill_task(self: Arc<Self>, state: Arc<ProxyState>) {
        tokio::spawn(async move {
            let mut ticker = tokio::time::interval(REFILL_INTERVAL);
            loop {
                ticker.tick().await;
                self.refill_once(&state).await;
            }
        });
    }

    async fn refill_once(&self, state: &Arc<ProxyState>) {
        let Some(config) = state.get_config() else {
            return;
        };

        let healthy = state.get_tcp_lb().healthy_backend_addresses();
        let healthy_set: std::collections::HashSet<&str> =
            healthy.iter().map(String::as_str).collect();

        // Drop pools for backends no longer healthy/present.
        self.pools
            .retain(|addr, _| healthy_set.contains(addr.as_str()));

        for addr in &healthy {
            let queue_entry = self
                .pools
                .entry(addr.clone())
                .or_insert_with(|| Mutex::new(VecDeque::new()));

            let deficit = {
                let queue = queue_entry.lock().await;
                self.target_size.saturating_sub(queue.len())
            };

            for _ in 0..deficit {
                let connect = tokio::time::timeout(
                    Duration::from_secs(config.connect_timeout_secs.max(1) as u64),
                    TcpStream::connect(addr),
                )
                .await;

                match connect {
                    Ok(Ok(stream)) => {
                        let mut queue = queue_entry.lock().await;
                        queue.push_back(PooledConn {
                            stream,
                            created_at: Instant::now(),
                        });
                    }
                    _ => {
                        // Backend unreachable right now — health checker
                        // will mark it unhealthy soon; just skip refilling.
                        debug!("Pool refill: failed to pre-warm connection to {}", addr);
                        break;
                    }
                }
            }
        }
    }
}
