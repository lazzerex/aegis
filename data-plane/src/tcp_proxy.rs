use std::sync::Arc;
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::{TcpListener, TcpStream};
use tracing::{debug, error, info, warn};

use crate::config::ProxyState;
use crate::load_balancer::LoadBalancer;

pub async fn run(state: Arc<ProxyState>) -> Result<(), Box<dyn std::error::Error>> {
    let config = state.get_config().ok_or("Proxy not configured")?;

    let listener = TcpListener::bind(&config.tcp_address).await?;
    info!("TCP proxy listening on {}", config.tcp_address);

    let load_balancer = Arc::new(LoadBalancer::new(
        config.backends.clone(),
        config.algorithm.clone(),
    ));

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
            if let Err(e) =
                handle_connection(client_socket, state_clone, lb_clone, config_clone).await
            {
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
    // Get client address for rate limiting and logging
    let client_addr = client.peer_addr()?;

    // Check rate limit
    if !state
        .rate_limiter
        .allow_request(Some(&client_addr.to_string()))
    {
        warn!("Rate limit exceeded for client: {}", client_addr);
        state.metrics.record_rate_limit_denied();
        return Err("Rate limit exceeded".into());
    }
    
    state.metrics.record_rate_limit_allowed();
    state.metrics.record_tcp_connection();

    // Register connection
    let (conn_id, _token) = state.register_connection();

    // Ensure we unregister on drop
    let _guard = ConnectionGuard {
        state: state.clone(),
        conn_id,
    };

    // Select backend with consistent hashing support
    let backend = load_balancer
        .select_backend_with_context(Some(&client_addr.ip().to_string()))
        .ok_or("No healthy backends available")?;

    // Check circuit breaker
    if !state.circuit_breaker.allow_request(&backend.address) {
        warn!(
            "Circuit breaker open for backend: {}, rejecting request",
            backend.address
        );
        state.metrics.record_circuit_breaker_open();
        state.metrics.record_backend_failure(&backend.address);
        return Err("Circuit breaker open".into());
    }

    debug!("Forwarding to backend: {}", backend.address);
    state.metrics.record_backend_connection(&backend.address);

    // Track connection in load balancer
    load_balancer.increment_connections(&backend.address);
    let lb_guard = LoadBalancerGuard {
        load_balancer: load_balancer.clone(),
        backend_addr: backend.address.clone(),
    };

    // Connect to backend with timeout
    let start_time = std::time::Instant::now();
    let backend_result = tokio::time::timeout(
        tokio::time::Duration::from_secs(config.connect_timeout_secs as u64),
        TcpStream::connect(&backend.address),
    )
    .await;

    let mut backend_stream = match backend_result {
        Ok(Ok(stream)) => {
            let latency = start_time.elapsed().as_secs_f64() * 1000.0;
            debug!("Connected to backend {} in {:.2}ms", backend.address, latency);
            state.circuit_breaker.record_success(&backend.address);
            state.metrics.record_backend_request(&backend.address);
            state.metrics.record_latency(latency);
            stream
        }
        Ok(Err(e)) => {
            error!("Failed to connect to backend {}: {}", backend.address, e);
            state.circuit_breaker.record_failure(&backend.address);
            state.metrics.record_backend_failure(&backend.address);
            return Err(e.into());
        }
        Err(_) => {
            error!("Timeout connecting to backend {}", backend.address);
            state.circuit_breaker.record_failure(&backend.address);
            state.metrics.record_backend_failure(&backend.address);
            return Err("Connection timeout".into());
        }
    };

    // Split streams for bidirectional copying
    let (mut client_read, mut client_write) = client.split();
    let (mut backend_read, mut backend_write) = backend_stream.split();
    
    let backend_addr_clone = backend.address.clone();
    let state_clone = state.clone();

    // Bidirectional copy
    let client_to_backend = async {
        let mut buf = vec![0u8; 8192];
        let mut total_bytes = 0u64;
        loop {
            let n = match client_read.read(&mut buf).await {
                Ok(0) => {
                    state_clone.metrics.record_bytes_sent(total_bytes);
                    state_clone.metrics.record_backend_bytes_sent(&backend_addr_clone, total_bytes);
                    return Ok::<_, std::io::Error>(());
                }
                Ok(n) => n,
                Err(e) => return Err(e),
            };

            total_bytes += n as u64;
            backend_write.write_all(&buf[..n]).await?;
        }
    };
    
    let backend_addr_clone2 = backend.address.clone();
    let state_clone2 = state.clone();

    let backend_to_client = async {
        let mut buf = vec![0u8; 8192];
        let mut total_bytes = 0u64;
        loop {
            let n = match backend_read.read(&mut buf).await {
                Ok(0) => {
                    state_clone2.metrics.record_bytes_received(total_bytes);
                    state_clone2.metrics.record_backend_bytes_received(&backend_addr_clone2, total_bytes);
                    return Ok::<_, std::io::Error>(());
                }
                Ok(n) => n,
                Err(e) => return Err(e),
            };

            total_bytes += n as u64;
            client_write.write_all(&buf[..n]).await?;
        }
    };

    // Run both directions concurrently
    tokio::select! {
        result = client_to_backend => {
            if let Err(e) = result {
                warn!("Client to backend error: {}", e);
                state.circuit_breaker.record_failure(&backend.address);
                state.metrics.record_backend_failure(&backend.address);
            }
        }
        result = backend_to_client => {
            if let Err(e) = result {
                warn!("Backend to client error: {}", e);
                state.circuit_breaker.record_failure(&backend.address);
                state.metrics.record_backend_failure(&backend.address);
            }
        }
    }

    // Connection completed successfully
    state.circuit_breaker.record_success(&backend.address);
    state.metrics.close_tcp_connection();
    debug!("Connection closed");

    // Drop the load balancer guard to decrement connection count
    drop(lb_guard);

    Ok(())
}

struct LoadBalancerGuard {
    load_balancer: Arc<LoadBalancer>,
    backend_addr: String,
}

impl Drop for LoadBalancerGuard {
    fn drop(&mut self) {
        self.load_balancer.decrement_connections(&self.backend_addr);
    }
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
