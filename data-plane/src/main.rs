use std::sync::Arc;
use tokio::signal;
use tracing::{error, info};

mod circuit_breaker;
mod config;
mod grpc_server;
mod load_balancer;
mod metrics;
mod rate_limiter;
mod tcp_proxy;
mod udp_proxy;

use config::ProxyState;
use grpc_server::ProxyControlService;

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    // Initialize tracing
    tracing_subscriber::fmt()
        .with_env_filter(
            tracing_subscriber::EnvFilter::try_from_default_env()
                .unwrap_or_else(|_| tracing_subscriber::EnvFilter::new("info")),
        )
        .init();

    info!("Starting proxy data plane v0.1.0");

    // Create shared proxy state
    let proxy_state = Arc::new(ProxyState::new());

    // Start gRPC server
    let grpc_addr = "0.0.0.0:50051".parse()?;
    let grpc_service = ProxyControlService::new(proxy_state.clone());

    info!("Starting gRPC control server on {}", grpc_addr);

    let cert_file = std::env::var("AEGIS_TLS_CERT_FILE").ok();
    let key_file = std::env::var("AEGIS_TLS_KEY_FILE").ok();

    let tls_identity = match (cert_file, key_file) {
        (Some(cert_path), Some(key_path)) => {
            match (std::fs::read(&cert_path), std::fs::read(&key_path)) {
                (Ok(cert), Ok(key)) => {
                    info!("TLS enabled for gRPC server");
                    Some(tonic::transport::Identity::from_pem(cert, key))
                }
                _ => {
                    error!(
                        "Failed to read TLS cert/key files: cert={}, key={}",
                        cert_path, key_path
                    );
                    return Err("TLS cert/key read failed".into());
                }
            }
        }
        (Some(_), None) => {
            return Err(
                "AEGIS_TLS_CERT_FILE set but AEGIS_TLS_KEY_FILE missing; both required for TLS"
                    .into(),
            );
        }
        (None, Some(_)) => {
            return Err(
                "AEGIS_TLS_KEY_FILE set but AEGIS_TLS_CERT_FILE missing; both required for TLS"
                    .into(),
            );
        }
        (None, None) => {
            info!("gRPC running without TLS");
            None
        }
    };

    let grpc_handle = tokio::spawn(async move {
        let mut server = tonic::transport::Server::builder();

        if let Some(identity) = tls_identity {
            let tls = tonic::transport::ServerTlsConfig::new().identity(identity);
            server = match server.tls_config(tls) {
                Ok(s) => s,
                Err(e) => {
                    error!("Failed to configure gRPC TLS: {}", e);
                    return;
                }
            };
        }

        if let Err(e) = server
            .add_service(grpc_service.into_service())
            .serve(grpc_addr)
            .await
        {
            error!("gRPC server error: {}", e);
        }
    });

    // Wait for initial configuration
    info!("Waiting for configuration from control plane...");
    while !proxy_state.is_configured().await {
        tokio::time::sleep(tokio::time::Duration::from_millis(100)).await;
    }

    info!("Configuration received, starting proxy services");

    // Start TCP proxy
    let tcp_state = proxy_state.clone();
    let tcp_handle = tokio::spawn(async move {
        if let Err(e) = tcp_proxy::run(tcp_state).await {
            error!("TCP proxy error: {}", e);
        }
    });

    // Start UDP proxy
    let udp_state = proxy_state.clone();
    let udp_handle = tokio::spawn(async move {
        if let Err(e) = udp_proxy::run(udp_state).await {
            error!("UDP proxy error: {}", e);
        }
    });

    // Wait for shutdown signal
    info!("Proxy data plane ready");

    #[cfg(unix)]
    {
        use signal::unix::{signal, SignalKind};
        let mut sigterm = signal(SignalKind::terminate())?;
        tokio::select! {
            _ = signal::ctrl_c() => {},
            _ = sigterm.recv() => {},
        }
    }
    #[cfg(not(unix))]
    signal::ctrl_c().await?;

    info!("Shutdown signal received, draining connections...");

    // Graceful shutdown — 30s timeout prevents hang if connections stall
    tokio::time::timeout(
        std::time::Duration::from_secs(30),
        proxy_state.drain_connections(),
    )
    .await
    .ok();

    // Wait for all tasks to complete (with timeout)
    let _ = tokio::time::timeout(tokio::time::Duration::from_secs(30), async {
        let _ = tokio::join!(grpc_handle, tcp_handle, udp_handle);
    })
    .await;

    info!("Shutdown complete");
    Ok(())
}
