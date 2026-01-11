use std::sync::Arc;
use tonic::{Request, Response, Status};
use tracing::{info, warn};

use crate::config::{proxy, Backend, ProxyConfig, ProxyState};

pub struct ProxyControlService {
    state: Arc<ProxyState>,
}

impl ProxyControlService {
    pub fn new(state: Arc<ProxyState>) -> Self {
        Self { state }
    }

    pub fn into_service(self) -> proxy::proxy_control_server::ProxyControlServer<Self> {
        proxy::proxy_control_server::ProxyControlServer::new(self)
    }
}

#[tonic::async_trait]
impl proxy::proxy_control_server::ProxyControl for ProxyControlService {
    async fn update_config(
        &self,
        request: Request<proxy::ProxyConfig>,
    ) -> Result<Response<proxy::ConfigAck>, Status> {
        let pb_config = request.into_inner();
        
        info!("Received configuration update");

        // Convert protobuf config to internal config
        let config = ProxyConfig {
            tcp_address: pb_config.listen.as_ref()
                .map(|l| l.tcp_address.clone())
                .unwrap_or_default(),
            udp_address: pb_config.listen.as_ref()
                .map(|l| l.udp_address.clone())
                .unwrap_or_default(),
            backends: pb_config.backends.iter().map(|b| Backend {
                address: b.address.clone(),
                weight: b.weight,
                healthy: b.healthy,
            }).collect(),
            algorithm: pb_config.load_balancing.as_ref()
                .map(|lb| lb.algorithm.clone())
                .unwrap_or_else(|| "round_robin".to_string()),
            session_affinity: pb_config.load_balancing.as_ref()
                .map(|lb| lb.session_affinity)
                .unwrap_or(false),
            rate_limit_rps: pb_config.traffic.as_ref()
                .and_then(|t| t.rate_limit.as_ref())
                .map(|rl| rl.requests_per_second)
                .unwrap_or(1000),
            rate_limit_burst: pb_config.traffic.as_ref()
                .and_then(|t| t.rate_limit.as_ref())
                .map(|rl| rl.burst)
                .unwrap_or(100),
            connect_timeout_secs: pb_config.traffic.as_ref()
                .and_then(|t| t.timeout.as_ref())
                .map(|to| to.connect_seconds)
                .unwrap_or(5),
            idle_timeout_secs: pb_config.traffic.as_ref()
                .and_then(|t| t.timeout.as_ref())
                .map(|to| to.idle_seconds)
                .unwrap_or(60),
            read_timeout_secs: pb_config.traffic.as_ref()
                .and_then(|t| t.timeout.as_ref())
                .map(|to| to.read_seconds)
                .unwrap_or(30),
        };

        info!("Configured {} backends on TCP:{}, UDP:{}",
              config.backends.len(),
              config.tcp_address,
              config.udp_address);

        self.state.update_config(config);

        Ok(Response::new(proxy::ConfigAck {
            success: true,
            message: "Configuration updated successfully".to_string(),
        }))
    }

    async fn reload_backends(
        &self,
        request: Request<proxy::BackendList>,
    ) -> Result<Response<proxy::ReloadAck>, Status> {
        let backend_list = request.into_inner();
        
        info!("Reloading {} backends", backend_list.backends.len());

        let mut config = self.state.get_config()
            .ok_or_else(|| Status::failed_precondition("Proxy not configured"))?;

        config.backends = backend_list.backends.iter().map(|b| Backend {
            address: b.address.clone(),
            weight: b.weight,
            healthy: b.healthy,
        }).collect();

        self.state.update_config(config);

        Ok(Response::new(proxy::ReloadAck {
            success: true,
            message: "Backends reloaded successfully".to_string(),
            backends_loaded: backend_list.backends.len() as i32,
        }))
    }

    async fn drain_connections(
        &self,
        request: Request<proxy::DrainRequest>,
    ) -> Result<Response<proxy::DrainResponse>, Status> {
        let drain_req = request.into_inner();
        
        info!("Draining connections with timeout: {}s", drain_req.timeout_seconds);

        let active_before = self.state.active_connection_count();
        
        // Start draining
        let state = self.state.clone();
        let timeout = tokio::time::Duration::from_secs(drain_req.timeout_seconds as u64);
        
        tokio::time::timeout(timeout, async move {
            state.drain_connections().await;
        }).await.ok();

        let active_after = self.state.active_connection_count();
        let drained = active_before.saturating_sub(active_after);

        info!("Drained {} connections ({} remaining)", drained, active_after);

        Ok(Response::new(proxy::DrainResponse {
            success: active_after == 0,
            connections_drained: drained as i32,
        }))
    }

    type StreamMetricsStream = futures::stream::BoxStream<'static, Result<proxy::MetricsAck, Status>>;

    async fn stream_metrics(
        &self,
        request: Request<tonic::Streaming<proxy::MetricsData>>,
    ) -> Result<Response<Self::StreamMetricsStream>, Status> {
        let mut stream = request.into_inner();
        let (tx, rx) = tokio::sync::mpsc::channel(100);

        tokio::spawn(async move {
            while let Ok(Some(_metrics)) = stream.message().await {
                // Acknowledge receipt
                if tx.send(Ok(proxy::MetricsAck { received: true })).await.is_err() {
                    warn!("Failed to send metrics ack");
                    break;
                }
            }
        });

        let stream = futures::stream::unfold(rx, |mut rx| async move {
            rx.recv().await.map(|item| (item, rx))
        });

        Ok(Response::new(Box::pin(stream) as Self::StreamMetricsStream))
    }
}
