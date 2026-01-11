use std::sync::Arc;
use tokio::net::{TcpListener, TcpStream};
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tracing::{info, error, debug, warn};

use crate::config::ProxyState;
use crate::load_balancer::LoadBalancer;

pub async fn run(state: Arc<ProxyState>) -> Result<(), Box<dyn std::error::Error>> {
    let config = state.get_config()
        .ok_or("Proxy not configured")?;

    let listener = TcpListener::bind(&config.tcp_address).await?;
    info!("TCP proxy listening on {}", config.tcp_address);

    let load_balancer = Arc::new(LoadBalancer::new(config.backends.clone(), config.algorithm.clone()));

    loop {
        // Check if draining
        if state.is_draining() {
            info!("TCP proxy is draining, not accepting new connections");
            break;
        }

        let (client_socket, client_addr) = match listener.accept().await {
            Ok(conn) => conn,
            Err(e) => {
                error!("Failed to accept connection: {}", e);
                continue;
            }
        };

        debug!("Accepted connection from {}", client_addr);

        let state_clone = state.clone();
        let lb_clone = load_balancer.clone();
        let config_clone = config.clone();

        tokio::spawn(async move {
            if let Err(e) = handle_connection(client_socket, state_clone, lb_clone, config_clone).await {
                error!("Connection error: {}", e);
            }
        });
    }

    Ok(())
}

async fn handle_connection(
    mut client: TcpStream,
    state: Arc<ProxyState>,
    load_balancer: Arc<LoadBalancer>,
    config: crate::config::ProxyConfig,
) -> Result<(), Box<dyn std::error::Error>> {
    // Register connection
    let (conn_id, _token) = state.register_connection();
    
    // Ensure we unregister on drop
    let _guard = ConnectionGuard {
        state: state.clone(),
        conn_id,
    };

    // Select backend
    let backend = load_balancer.select_backend()
        .ok_or("No healthy backends available")?;

    debug!("Forwarding to backend: {}", backend.address);

    // Connect to backend with timeout
    let mut backend_stream = tokio::time::timeout(
        tokio::time::Duration::from_secs(config.connect_timeout_secs as u64),
        TcpStream::connect(&backend.address)
    ).await??;

    debug!("Connected to backend {}", backend.address);

    // Split streams for bidirectional copying
    let (mut client_read, mut client_write) = client.split();
    let (mut backend_read, mut backend_write) = backend_stream.split();

    // Bidirectional copy
    let client_to_backend = async {
        let mut buf = vec![0u8; 8192];
        loop {
            let n = match client_read.read(&mut buf).await {
                Ok(0) => return Ok::<_, std::io::Error>(()),
                Ok(n) => n,
                Err(e) => return Err(e),
            };

            backend_write.write_all(&buf[..n]).await?;
        }
    };

    let backend_to_client = async {
        let mut buf = vec![0u8; 8192];
        loop {
            let n = match backend_read.read(&mut buf).await {
                Ok(0) => return Ok::<_, std::io::Error>(()),
                Ok(n) => n,
                Err(e) => return Err(e),
            };

            client_write.write_all(&buf[..n]).await?;
        }
    };

    // Run both directions concurrently
    tokio::select! {
        result = client_to_backend => {
            if let Err(e) = result {
                warn!("Client to backend error: {}", e);
            }
        }
        result = backend_to_client => {
            if let Err(e) = result {
                warn!("Backend to client error: {}", e);
            }
        }
    }

    debug!("Connection closed");
    Ok(())
}

struct ConnectionGuard {
    state: Arc<ProxyState>,
    conn_id: u64,
}

impl Drop for ConnectionGuard {
    fn drop(&mut self) {
        self.state.unregister_connection(self.conn_id);
    }
}
