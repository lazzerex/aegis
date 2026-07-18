use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::Arc;
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::{TcpListener, TcpStream};
use tracing::{debug, error, info, warn};

use crate::access_log::AccessLogEntry;
use crate::config::ProxyState;
use crate::connection::ConnectionPool;
use crate::load_balancer::LoadBalancer;

pub async fn run(
    state: Arc<ProxyState>,
    pool: Arc<ConnectionPool>,
) -> Result<(), Box<dyn std::error::Error>> {
    let config = state.get_config().ok_or("Proxy not configured")?;

    let listener = TcpListener::bind(&config.tcp_address).await?;
    info!("TCP proxy listening on {}", config.tcp_address);

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
        let lb_clone = state.get_tcp_lb();
        let config_clone = config.clone();
        let pool_clone = pool.clone();

        tokio::spawn(async move {
            if let Err(e) = handle_connection(
                client_socket,
                state_clone,
                lb_clone,
                config_clone,
                pool_clone,
            )
            .await
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
    pool: Arc<ConnectionPool>,
) -> Result<(), Box<dyn std::error::Error>> {
    // Get client address for rate limiting and logging
    let client_addr = client.peer_addr()?;

    let conn_start = std::time::Instant::now();
    let client_ip = client_addr.ip().to_string();
    let log_access =
        |backend: &str, bytes_sent: u64, bytes_received: u64, error: Option<String>| {
            AccessLogEntry {
                protocol: "tcp",
                client_ip: client_ip.clone(),
                backend: backend.to_string(),
                bytes_sent,
                bytes_received,
                duration_ms: conn_start.elapsed().as_secs_f64() * 1000.0,
                error,
            }
            .log();
        };

    // Check rate limit
    if !state
        .rate_limiter
        .read()
        .allow_request(Some(&client_addr.to_string()))
    {
        warn!("Rate limit exceeded for client: {}", client_addr);
        state.metrics.record_rate_limit_denied();
        log_access("", 0, 0, Some("rate limit exceeded".to_string()));
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

    // Select backend; only pass client IP for consistent hashing when session_affinity enabled
    let context = if config.session_affinity {
        Some(client_addr.ip().to_string())
    } else {
        None
    };
    let backend = match load_balancer.select_backend_with_context(context.as_deref()) {
        Some(b) => b,
        None => {
            log_access("", 0, 0, Some("no healthy backends available".to_string()));
            return Err("No healthy backends available".into());
        }
    };

    // Check circuit breaker
    if !state.circuit_breaker.read().allow_request(&backend.address) {
        warn!(
            "Circuit breaker open for backend: {}, rejecting request",
            backend.address
        );
        state.metrics.record_circuit_breaker_open();
        state.metrics.record_backend_failure(&backend.address);
        log_access(
            &backend.address,
            0,
            0,
            Some("circuit breaker open".to_string()),
        );
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

    // Connect to backend — try the pre-warmed pool first to skip the
    // handshake on the hot path, falling back to a fresh dial on a miss.
    let start_time = std::time::Instant::now();
    let pooled = pool.take(&backend.address).await;

    let backend_result = if let Some(stream) = pooled {
        state.metrics.record_pool_hit();
        Ok(Ok(stream))
    } else {
        state.metrics.record_pool_miss();
        tokio::time::timeout(
            tokio::time::Duration::from_secs(config.connect_timeout_secs as u64),
            TcpStream::connect(&backend.address),
        )
        .await
    };

    let mut backend_stream = match backend_result {
        Ok(Ok(stream)) => {
            let latency = start_time.elapsed().as_secs_f64() * 1000.0;
            debug!(
                "Connected to backend {} in {:.2}ms",
                backend.address, latency
            );
            state
                .circuit_breaker
                .read()
                .record_success(&backend.address);
            state.metrics.record_backend_request(&backend.address);
            state.metrics.record_latency(latency);
            stream
        }
        Ok(Err(e)) => {
            error!("Failed to connect to backend {}: {}", backend.address, e);
            state
                .circuit_breaker
                .read()
                .record_failure(&backend.address);
            state.metrics.record_backend_failure(&backend.address);
            log_access(&backend.address, 0, 0, Some(e.to_string()));
            return Err(e.into());
        }
        Err(_) => {
            error!("Timeout connecting to backend {}", backend.address);
            state
                .circuit_breaker
                .read()
                .record_failure(&backend.address);
            state.metrics.record_backend_failure(&backend.address);
            log_access(
                &backend.address,
                0,
                0,
                Some("connection timeout".to_string()),
            );
            return Err("Connection timeout".into());
        }
    };

    // Split streams for bidirectional copying
    let read_timeout = if config.read_timeout_secs > 0 {
        Some(tokio::time::Duration::from_secs(
            config.read_timeout_secs as u64,
        ))
    } else {
        None
    };
    let (mut client_read, mut client_write) = client.split();
    let (mut backend_read, mut backend_write) = backend_stream.split();

    let conn_bytes_sent = Arc::new(AtomicU64::new(0));
    let conn_bytes_received = Arc::new(AtomicU64::new(0));

    let backend_addr_clone = backend.address.clone();
    let state_clone = state.clone();
    let conn_bytes_sent_clone = conn_bytes_sent.clone();

    // Bidirectional copy
    let client_to_backend = async move {
        let mut buf = vec![0u8; 8192];
        loop {
            let n = match read_timeout {
                Some(t) => match tokio::time::timeout(t, client_read.read(&mut buf)).await {
                    Ok(Ok(0)) => return Ok::<_, std::io::Error>(()),
                    Ok(Ok(n)) => n,
                    Ok(Err(e)) => return Err(e),
                    Err(_) => {
                        return Err(std::io::Error::new(
                            std::io::ErrorKind::TimedOut,
                            "read timeout",
                        ))
                    }
                },
                None => match client_read.read(&mut buf).await {
                    Ok(0) => return Ok::<_, std::io::Error>(()),
                    Ok(n) => n,
                    Err(e) => return Err(e),
                },
            };

            state_clone.metrics.record_bytes_sent(n as u64);
            state_clone
                .metrics
                .record_backend_bytes_sent(&backend_addr_clone, n as u64);
            conn_bytes_sent_clone.fetch_add(n as u64, Ordering::Relaxed);
            backend_write.write_all(&buf[..n]).await?;
        }
    };

    let backend_addr_clone2 = backend.address.clone();
    let state_clone2 = state.clone();
    let conn_bytes_received_clone = conn_bytes_received.clone();

    let backend_to_client = async move {
        let mut buf = vec![0u8; 8192];
        loop {
            let n = match read_timeout {
                Some(t) => match tokio::time::timeout(t, backend_read.read(&mut buf)).await {
                    Ok(Ok(0)) => return Ok::<_, std::io::Error>(()),
                    Ok(Ok(n)) => n,
                    Ok(Err(e)) => return Err(e),
                    Err(_) => {
                        return Err(std::io::Error::new(
                            std::io::ErrorKind::TimedOut,
                            "read timeout",
                        ))
                    }
                },
                None => match backend_read.read(&mut buf).await {
                    Ok(0) => return Ok::<_, std::io::Error>(()),
                    Ok(n) => n,
                    Err(e) => return Err(e),
                },
            };

            state_clone2.metrics.record_bytes_received(n as u64);
            state_clone2
                .metrics
                .record_backend_bytes_received(&backend_addr_clone2, n as u64);
            conn_bytes_received_clone.fetch_add(n as u64, Ordering::Relaxed);
            client_write.write_all(&buf[..n]).await?;
        }
    };

    // Run both directions concurrently
    let mut conn_error: Option<String> = None;
    let connection_ok = tokio::select! {
        result = client_to_backend => {
            if let Err(e) = result {
                warn!("Client to backend error: {}", e);
                state.circuit_breaker.read().record_failure(&backend.address);
                state.metrics.record_backend_failure(&backend.address);
                conn_error = Some(e.to_string());
                false
            } else {
                true
            }
        }
        result = backend_to_client => {
            if let Err(e) = result {
                warn!("Backend to client error: {}", e);
                state.circuit_breaker.read().record_failure(&backend.address);
                state.metrics.record_backend_failure(&backend.address);
                conn_error = Some(e.to_string());
                false
            } else {
                true
            }
        }
    };

    if connection_ok {
        state
            .circuit_breaker
            .read()
            .record_success(&backend.address);
    }
    state.metrics.close_tcp_connection();
    debug!("Connection closed");
    log_access(
        &backend.address,
        conn_bytes_sent.load(Ordering::Relaxed),
        conn_bytes_received.load(Ordering::Relaxed),
        conn_error,
    );

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
