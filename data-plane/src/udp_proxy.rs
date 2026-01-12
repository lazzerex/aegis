use dashmap::DashMap;
use std::sync::Arc;
use std::time::{Duration, Instant};
use tokio::net::UdpSocket;
use tracing::{debug, error, info};

use crate::config::ProxyState;
use crate::load_balancer::LoadBalancer;

const SESSION_TIMEOUT: Duration = Duration::from_secs(60);
const BUFFER_SIZE: usize = 65536;

struct UdpSession {
    backend_addr: String,
    last_activity: Instant,
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
    let sessions: Arc<DashMap<String, UdpSession>> = Arc::new(DashMap::new());

    // Session cleanup task
    let sessions_clone = sessions.clone();
    tokio::spawn(async move {
        loop {
            tokio::time::sleep(Duration::from_secs(10)).await;

            // Remove expired sessions
            sessions_clone.retain(|_, session| session.last_activity.elapsed() < SESSION_TIMEOUT);
        }
    });

    let mut buf = vec![0u8; BUFFER_SIZE];

    loop {
        // Check if draining
        if state.is_draining() {
            info!("UDP proxy is draining");
            break;
        }

        let (len, client_addr) = match socket.recv_from(&mut buf).await {
            Ok(result) => result,
            Err(e) => {
                error!("Failed to receive UDP packet: {}", e);
                continue;
            }
        };

        debug!("Received {} bytes from {}", len, client_addr);

        let client_key = client_addr.to_string();
        let packet = buf[..len].to_vec();

        // Get or create session
        let backend_addr = {
            let mut session = sessions.entry(client_key.clone()).or_insert_with(|| {
                let backend = load_balancer
                    .select_backend()
                    .expect("No healthy backends available");

                debug!("New UDP session {} -> {}", client_addr, backend.address);

                UdpSession {
                    backend_addr: backend.address.clone(),
                    last_activity: Instant::now(),
                }
            });

            session.last_activity = Instant::now();
            session.backend_addr.clone()
        };

        // Forward packet to backend
        let socket_clone = socket.clone();
        tokio::spawn(async move {
            match socket_clone.send_to(&packet, &backend_addr).await {
                Ok(_) => debug!("Forwarded {} bytes to {}", packet.len(), backend_addr),
                Err(e) => error!("Failed to forward to backend {}: {}", backend_addr, e),
            }
        });
    }

    Ok(())
}
