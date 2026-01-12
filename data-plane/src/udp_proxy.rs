use dashmap::DashMap;
use std::net::SocketAddr;
use std::sync::Arc;
use std::time::{Duration, Instant};
use tokio::net::UdpSocket;
use tracing::{debug, error, info, warn};

use crate::config::ProxyState;
use crate::load_balancer::LoadBalancer;

const SESSION_TIMEOUT: Duration = Duration::from_secs(60);
const BUFFER_SIZE: usize = 65536;
const CLEANUP_INTERVAL: Duration = Duration::from_secs(10);

/// NAT mapping for UDP sessions with bidirectional tracking
struct UdpSession {
    backend_addr: String,
    backend_socket_addr: SocketAddr,
    client_addr: SocketAddr,
    last_activity: Instant,
    bytes_sent: u64,
    bytes_received: u64,
    packets_sent: u64,
    packets_received: u64,
}

impl UdpSession {
    fn new(
        backend_addr: String,
        backend_socket_addr: SocketAddr,
        client_addr: SocketAddr,
    ) -> Self {
        Self {
            backend_addr,
            backend_socket_addr,
            client_addr,
            last_activity: Instant::now(),
            bytes_sent: 0,
            bytes_received: 0,
            packets_sent: 0,
            packets_received: 0,
        }
    }

    fn update_activity(&mut self) {
        self.last_activity = Instant::now();
    }

    fn is_expired(&self, timeout: Duration) -> bool {
        self.last_activity.elapsed() > timeout
    }

    fn record_sent(&mut self, bytes: u64) {
        self.bytes_sent += bytes;
        self.packets_sent += 1;
        self.update_activity();
    }

    fn record_received(&mut self, bytes: u64) {
        self.bytes_received += bytes;
        self.packets_received += 1;
        self.update_activity();
    }
}

pub async fn run(state: Arc<ProxyState>) -> Result<(), Box<dyn std::error::Error>> {
    let config = state.get_config().ok_or("Proxy not configured")?;

    if config.udp_address.is_empty() {
        info!("UDP proxy disabled (no address configured)");
        return Ok(());
    }

    let socket = Arc::new(UdpSocket::bind(&config.udp_address).await?);
    info!("UDP proxy listening on {}", config.udp_address);

    let load_balancer = Arc::new(LoadBalancer::new(
        config.backends.clone(),
        config.algorithm.clone(),
    ));

    // Session tracking with NAT mapping
    let sessions: Arc<DashMap<String, UdpSession>> = Arc::new(DashMap::new());
    // Reverse mapping for backend -> client lookups
    let reverse_sessions: Arc<DashMap<SocketAddr, String>> = Arc::new(DashMap::new());

    // Session cleanup task - removes expired sessions
    let sessions_clone = sessions.clone();
    let reverse_sessions_clone = reverse_sessions.clone();
    tokio::spawn(async move {
        let mut interval = tokio::time::interval(CLEANUP_INTERVAL);
        loop {
            interval.tick().await;

            let mut expired_keys = Vec::new();

            // Find expired sessions
            for entry in sessions_clone.iter() {
                if entry.value().is_expired(SESSION_TIMEOUT) {
                    debug!(
                        "Session expired: {} -> {} (sent: {}/{} bytes/pkts, received: {}/{} bytes/pkts)",
                        entry.value().client_addr,
                        entry.value().backend_addr,
                        entry.value().bytes_sent,
                        entry.value().packets_sent,
                        entry.value().bytes_received,
                        entry.value().packets_received
                    );
                    expired_keys.push(entry.key().clone());
                }
            }

            // Remove expired sessions
            for key in expired_keys {
                if let Some((_, session)) = sessions_clone.remove(&key) {
                    reverse_sessions_clone.remove(&session.backend_socket_addr);
                }
            }
        }
    });

    let mut buf = vec![0u8; BUFFER_SIZE];

    loop {
        // Check if draining
        if state.is_draining() {
            info!("UDP proxy is draining");
            break;
        }

        let (len, peer_addr) = match socket.recv_from(&mut buf).await {
            Ok(result) => result,
            Err(e) => {
                error!("Failed to receive UDP packet: {}", e);
                continue;
            }
        };

        let packet = buf[..len].to_vec();
        let socket_clone = socket.clone();
        let sessions_clone = sessions.clone();
        let reverse_sessions_clone = reverse_sessions.clone();
        let lb_clone = load_balancer.clone();

        // Process packet asynchronously
        tokio::spawn(async move {
            // Check if this is a response from a backend
            if let Some(client_key) = reverse_sessions_clone.get(&peer_addr) {
                // Packet from backend to client
                let client_key_str = client_key.value().clone();
                if let Some(mut session) = sessions_clone.get_mut(&client_key_str) {
                    session.record_received(len as u64);
                    let client_addr = session.client_addr;

                    debug!(
                        "Forwarding {} bytes from backend {} to client {}",
                        len, peer_addr, client_addr
                    );

                    match socket_clone.send_to(&packet, client_addr).await {
                        Ok(_) => {}
                        Err(e) => error!("Failed to forward to client {}: {}", client_addr, e),
                    }
                }
            } else {
                // Packet from client to backend - establish/update session
                let client_key = peer_addr.to_string();

                // Get or create session with NAT mapping
                let (backend_socket_addr, client_addr) = {
                    let mut session = sessions_clone.entry(client_key.clone()).or_insert_with(|| {
                        let backend = lb_clone
                            .select_backend_with_context(Some(&peer_addr.ip().to_string()))
                            .expect("No healthy backends available");

                        // Resolve backend address
                        let backend_socket_addr: SocketAddr = backend.address.parse()
                            .expect("Invalid backend address");

                        debug!(
                            "New UDP session: {} -> {} (NAT mapping established)",
                            peer_addr, backend.address
                        );

                        UdpSession::new(backend.address, backend_socket_addr, peer_addr)
                    });

                    session.record_sent(len as u64);
                    (session.backend_socket_addr, session.client_addr)
                };

                // Update reverse mapping
                reverse_sessions_clone.insert(backend_socket_addr, client_key);

                debug!(
                    "Forwarding {} bytes from client {} to backend {}",
                    len, client_addr, backend_socket_addr
                );

                // Forward packet to backend
                match socket_clone.send_to(&packet, backend_socket_addr).await {
                    Ok(_) => {}
                    Err(e) => error!("Failed to forward to backend {}: {}", backend_socket_addr, e),
                }
            }
        });
    }

    // Log final statistics on shutdown
    info!("UDP proxy shutdown - active sessions: {}", sessions.len());
    for entry in sessions.iter() {
        debug!(
            "Final session stats: {} -> {} (sent: {}/{}, received: {}/{})",
            entry.value().client_addr,
            entry.value().backend_addr,
            entry.value().bytes_sent,
            entry.value().packets_sent,
            entry.value().bytes_received,
            entry.value().packets_received
        );
    }

    Ok(())
}
