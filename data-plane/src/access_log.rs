use serde::Serialize;

/// One structured JSON line per finished connection/session — client IP,
/// backend, bytes transferred, duration, and error (if any). Emitted at
/// `target: "access_log"` so it's separable from general application logs.
#[derive(Serialize)]
pub struct AccessLogEntry {
    pub protocol: &'static str,
    pub client_ip: String,
    pub backend: String,
    pub bytes_sent: u64,
    pub bytes_received: u64,
    pub duration_ms: f64,
    pub error: Option<String>,
}

impl AccessLogEntry {
    pub fn log(&self) {
        match serde_json::to_string(self) {
            Ok(json) => tracing::info!(target: "access_log", "{}", json),
            Err(e) => tracing::error!("failed to serialize access log entry: {}", e),
        }
    }
}
