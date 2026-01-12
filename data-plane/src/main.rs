use std::sync::Arc;
use tokio::signal;
use tracing::{error, info};
use tracing_subscriber;

mod config;
mod connection;
mod grpc_server;
mod load_balancer;
mod metrics;
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
    let grpc_addr = "127.0.0.1:50051".parse()?;
    let grpc_service = ProxyControlService::new(proxy_state.clone());

    info!("Starting gRPC control server on {}", grpc_addr);

    let grpc_handle = tokio::spawn(async move {
        if let Err(e) = tonic::transport::Server::builder()
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

    // Start metrics streaming
    let metrics_state = proxy_state.clone();
    let metrics_handle = tokio::spawn(async move {
        metrics::stream_metrics(metrics_state).await;
    });

    // Wait for shutdown signal
    info!("Proxy data plane ready");
    signal::ctrl_c().await?;
    info!("Shutdown signal received, draining connections...");

    // Graceful shutdown
    proxy_state.drain_connections().await;

    // Wait for all tasks to complete (with timeout)
    let _ = tokio::time::timeout(tokio::time::Duration::from_secs(30), async {
        let _ = tokio::join!(grpc_handle, tcp_handle, udp_handle, metrics_handle);
    })
    .await;

    info!("Shutdown complete");
    Ok(())
}
