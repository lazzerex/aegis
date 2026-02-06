# Aegis

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Go Version](https://img.shields.io/badge/Go-1.22%2B-blue)](https://golang.org)
[![Rust Version](https://img.shields.io/badge/Rust-1.75%2B-orange)](https://www.rust-lang.org)

Aegis is a high-performance network proxy that combines Go's control plane with Rust's data plane for optimal performance and maintainability.

The control plane handles configuration, health checks, and load balancing logic in Go, while the data plane processes packets in Rust for minimal latency overhead. The two communicate via gRPC, allowing independent development and deployment of each component.

Aegis is designed for production use in microservice architectures and backend infrastructure, and also serves as a reference implementation for building high-performance networked systems.

#### Grafana Dashboard Screenshots

**Aegis Proxy Dashboard:**


#### Prometheus Dashboard Screenshots

**Prometheus Metrics Explorer:**


## Table of Contents

- [Quick Start](#quick-start)
- [Features](#features)
- [Architecture](#architecture)
- [Installation](#installation)
- [Running Aegis](#running-aegis)
  - [Local Development with Make](#local-development-with-make)
  - [Docker Compose](#docker-compose)
- [Configuration](#configuration)
- [Testing](#testing)
- [Monitoring](#monitoring)
- [Project Structure](#project-structure)
- [Development](#development)
- [Troubleshooting](#troubleshooting)
- [Roadmap](#roadmap)

## Quick Start

```bash
# Clone and build
git clone https://github.com/lazzerex/aegis.git
cd aegis
make all

# Start test backends
./scripts/test-proxy.sh start

# Run Aegis (in separate terminals)
# Terminal 1: Data plane
make run-data

# Terminal 2: Control plane
make run-control

# Test it
./scripts/test-proxy.sh test-proxy

# Stop test backends
./scripts/test-proxy.sh stop
```

## Features

### Proxy Capabilities
- **TCP Proxy**: High-performance TCP forwarding with async I/O (Tokio)
- **UDP Proxy**: Session-based UDP forwarding with bidirectional NAT mapping and connection tracking

### Load Balancing
- **Round-robin**: Equal distribution across backends
- **Weighted round-robin**: Proportional distribution based on backend capacity
- **Least connections**: Routes to backend with fewest active connections
- **Consistent hashing**: Session affinity using client IP

### Reliability & Performance
- **Circuit Breaking**: Automatic failure detection and backend recovery with configurable thresholds
- **Rate Limiting**: Token bucket algorithm with global and per-connection limits
- **Health Checking**: Periodic backend health monitoring with automatic failover
- **Graceful Shutdown**: Connection draining and cleanup

### Observability
- **Prometheus Metrics**: Comprehensive metrics exposed on `/metrics` endpoint
- **Structured Logging**: Detailed tracing with configurable log levels
- **gRPC Communication**: Clean separation between control and data planes

### Coming Soon
- Distributed tracing with OpenTelemetry
- HTTP/2 support and WebSocket proxying
- Hot configuration reload without dropping connections

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                     Go Control Plane                         │
│  ┌────────────┐  ┌──────────┐  ┌─────────────┐             │
│  │ Config Mgmt│  │ Health   │  │ Admin API   │             │
│  │ (Viper)    │  │ Checker  │  │ (Chi Router)│             │
│  └────────────┘  └──────────┘  └─────────────┘             │
│         │              │               │                     │
│         └──────────────┴───────────────┘                     │
│                       │                                      │
│                  gRPC Client                                 │
└───────────────────────┼──────────────────────────────────────┘
                        │
                  gRPC (50051)
                        │
┌───────────────────────┼──────────────────────────────────────┐
│                  gRPC Server                                 │
│                       │                                      │
│                Rust Data Plane                               │
│  ┌────────────┐  ┌──────────┐  ┌─────────────┐             │
│  │ TCP Proxy  │  │ UDP Proxy│  │ Load        │             │
│  │ (Tokio)    │  │ (Tokio)  │  │ Balancer    │             │
│  └────────────┘  └──────────┘  └─────────────┘             │
│         │              │               │                     │
│         └──────────────┴───────────────┘                     │
│                       │                                      │
└───────────────────────┼──────────────────────────────────────┘
                        │
                   Backends
              (NestJS/Any HTTP Service)
```

## Installation

### Prerequisites

- **Go** 1.22+ 
- **Rust** 1.75+
- **Protocol Buffers** compiler (protoc)
- **Make**
- **Docker** (optional, for containerized deployment)

### Install Dependencies

#### Ubuntu/Debian
```bash
# Install protobuf compiler
sudo apt update
sudo apt install -y protobuf-compiler

# Install Go (if not installed)
cd /tmp
wget https://go.dev/dl/go1.22.0.linux-amd64.tar.gz
sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf go1.22.0.linux-amd64.tar.gz
rm go1.22.0.linux-amd64.tar.gz

# Add to PATH
echo 'export PATH=$PATH:/usr/local/go/bin:$HOME/go/bin' >> ~/.bashrc
source ~/.bashrc
```

#### macOS
```bash
# Install protobuf compiler
brew install protobuf

# Install Go
brew install go
```

#### Install Go Protobuf Plugins
```bash
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

# Verify installation
protoc --version
go version
```

#### Configure Go PATH

The Protocol Buffer plugins must be in your PATH for `protoc` to find them. If you get errors like `protoc-gen-go: program not found or is not executable`, follow these steps:

**1. Check if plugins are installed:**
```bash
ls $(go env GOPATH)/bin
```

You should see `protoc-gen-go` and `protoc-gen-go-grpc`.

**2. Add Go bin directory to PATH:**

For **bash** users:
```bash
echo 'export GOPATH="$HOME/go"' >> ~/.bashrc
echo 'export PATH="$PATH:$GOPATH/bin"' >> ~/.bashrc
source ~/.bashrc
```

For **zsh** users:
```bash
echo 'export GOPATH="$HOME/go"' >> ~/.zshrc
echo 'export PATH="$PATH:$GOPATH/bin"' >> ~/.zshrc
source ~/.zshrc
```

**3. Verify PATH is correct:**
```bash
which protoc-gen-go
which protoc-gen-go-grpc
protoc-gen-go --version
```

Expected output should show paths like `/home/yourusername/go/bin/protoc-gen-go`.

### Build Aegis

```bash
# Clone the repository
git clone https://github.com/lazzerex/aegis.git
cd aegis

# Generate protobuf code
make proto

# Build both control and data planes
make all

# Or build individually
make build-go      # Build Go control plane
make build-rust    # Build Rust data plane
```

The binaries will be created at:
- `control-plane/aegis-control` - Go control plane
- `data-plane/target/release/aegis-data` - Rust data plane

## Production Usage

### How Users Deploy Aegis

Aegis is a **CLI-based infrastructure tool** (like nginx or HAProxy), designed for deployment in production environments.

**Common Use Cases:**

1. **Reverse Proxy** - Place Aegis in front of your application servers
   ```
   Internet → Aegis (port 80/443) → Your Backend Servers
   ```

2. **Load Balancer** - Distribute traffic across multiple instances
   ```
   Clients → Aegis → [Server 1, Server 2, Server 3, ...]
   ```

3. **API Gateway** - Route requests with rate limiting and circuit breaking
   ```
   Mobile/Web Apps → Aegis → Microservices
   ```

**Deployment Methods:**

```bash
# Option 1: Systemd Service (Linux)
sudo systemctl start aegis
sudo systemctl enable aegis

# Option 2: Docker Compose (Containers)
docker-compose up -d

# Option 3: Kubernetes (Cloud-native)
kubectl apply -f aegis-deployment.yaml

# Option 4: Direct Binary (Development)
# Use make commands (recommended)
make run-data
make run-control

# Or run binaries directly
./data-plane/target/release/aegis-data --config config.yaml
./control-plane/aegis-control --config config.yaml
```

**Management:**
- Configure via YAML files (`config.yaml`)
- Monitor via Prometheus metrics (`:9091/metrics`)
- Control via Admin API (`:9090`)
- View dashboards with Grafana (connects to Prometheus)

**No GUI Required** - Aegis is infrastructure software managed through:
- Configuration files (YAML)
- Command-line interface
- HTTP Admin API
- Monitoring dashboards (Grafana/Prometheus)

## Configuration

Edit `config.yaml` to configure the proxy:

```yaml
proxy:
  listen:
    tcp: "0.0.0.0:8080"
    udp: "0.0.0.0:8081"
  
  backends:
    - address: "localhost:3000"
      weight: 100
      health_check:
        interval: 5s
        timeout: 2s
        path: "/health"
    - address: "localhost:3001"
      weight: 100
      health_check:
        interval: 5s
        timeout: 2s
        path: "/health"

  load_balancing:
    algorithm: "round_robin"  # round_robin, weighted, least_connections
    session_affinity: false

  traffic:
    rate_limit:
      requests_per_second: 1000
      burst: 100
    timeout:
      connect: 5s
      idle: 60s
      read: 30s

  circuit_breaker:
    error_threshold: 5
    timeout: 30s

admin:
  api_address: "127.0.0.1:9090"
  metrics_address: "0.0.0.0:9091"

grpc:
  control_plane_address: "127.0.0.1:50051"
```

## Running Aegis

### Local Development with Make

This is the recommended method for development and testing.

**Prerequisites:**
- Built binaries (run `make all`)
- Config file: `config.yaml` (uses `localhost` for backends)

**Start Aegis:**

```bash
# Terminal 1: Start Rust data plane
make run-data

# Terminal 2: Start Go control plane
make run-control
```

The binaries are located at:
- Data plane: `data-plane/target/release/aegis-data`
- Control plane: `control-plane/aegis-control`

**Test it:**

```bash
# Terminal 3: Test the proxy
curl http://localhost:8080

# Check health status
curl http://localhost:9090/health

# Check metrics
curl http://localhost:9091/metrics

# Check proxy status
curl http://localhost:9090/status
```

### Docker Compose

This method runs the complete stack including monitoring tools.

**Prerequisites:**
- Docker and Docker Compose installed
- Config file: `config.docker.yaml` (uses Docker service names for backends)

**Start the stack:**

```bash
# Option 1: Using Make commands (wrapper around docker-compose)
make docker-build        # Build images
make docker-up           # Start all services
make docker-logs         # View logs
make docker-down         # Stop all services

# Option 2: Using docker-compose directly (recommended)
docker-compose up --build -d     # Build and start in detached mode
docker-compose ps                # Check status
docker-compose logs -f           # Follow logs
docker-compose logs -f grafana   # Follow specific service
docker-compose down              # Stop all services
docker-compose down -v           # Stop and remove volumes
```

**Services started:**
- **Aegis data plane** - TCP/UDP proxy (ports 8080/8081)
- **Aegis control plane** - Admin API (9090), Metrics (9091)
- **Test backends** - HTTP servers (ports 3000-3002)
- **UDP backends** - UDP echo servers (ports 5001-5003/udp)
- **Prometheus** - Metrics collection (port 9092)
- **Grafana** - Visualization (port 3030, login: admin/admin)

**Access services:**
- TCP Proxy: http://localhost:8080
- UDP Proxy: localhost:8081 (UDP)
- Admin API: http://localhost:9090
- Metrics: http://localhost:9091/metrics
- Prometheus: http://localhost:9092
- Grafana: http://localhost:3030

### Quick Reference

**Essential Commands:**
```bash
# Build
make all              # Build everything
make build-go         # Build control plane only
make build-rust       # Build data plane only

# Run (development)
make run-data         # Start data plane
make run-control      # Start control plane

# Testing
./scripts/test-proxy.sh start       # Start test backends
./scripts/test-proxy.sh test-proxy  # Test load balancing
./scripts/test-proxy.sh status      # Check services
./scripts/test-proxy.sh stop        # Stop backends

# Docker
make docker-build     # Build images
make docker-up        # Start stack
make docker-down      # Stop stack
make docker-logs      # View logs

# Or use docker-compose directly
docker-compose up --build -d    # Build and start
docker-compose ps               # Check status
docker-compose logs -f          # View logs
docker-compose down             # Stop stack

# Development
make test             # Run tests
make fmt              # Format code
make lint             # Lint code
make clean            # Clean build artifacts
```

**Default Ports:**

| Service          | Port  | Purpose                    |
|------------------|-------|----------------------------|
| TCP Proxy        | 8080  | Main proxy entry point     |
| UDP Proxy        | 8081  | UDP proxy entry point      |
| Admin API        | 9090  | Control & management       |
| Metrics          | 9091  | Prometheus metrics         |
| gRPC (Internal)  | 50051 | Control/data plane comms   |
| Prometheus       | 9092  | Metrics scraping           |
| Grafana          | 3030  | Dashboard visualization    |
| Backend 1-3      | 3000-3002 | Test HTTP servers      |
| UDP Backend 1-3  | 5001-5003 | Test UDP servers       |

## Testing

### Quick Start Testing

Aegis includes test backend servers and convenient scripts to verify functionality.

**Automated Testing (Recommended):**

```bash
# 1. Start test backend servers
./test-proxy.sh start

# 2. Build and start Aegis (in separate terminals)
# Terminal 1: Data plane
make run-data

# Terminal 2: Control plane
make run-control

# 3. Test the proxy
./test-proxy.sh test-proxy

# 4. Check status of all services
./test-proxy.sh status

# 5. Stop test backends when done
./test-proxy.sh stop
```

#### Manual Testing

**Step 1: Start Backend Servers**

```bash
# Terminal 1
python3 examples/simple-http-server.py --port 3000 --name backend1

# Terminal 2
python3 examples/simple-http-server.py --port 3001 --name backend2

# Terminal 3
python3 examples/simple-http-server.py --port 3002 --name backend3
```

**Step 2: Start Aegis**

```bash
# Terminal 4: Build and start data plane
make build-rust
make run-data

# Terminal 5: Build and start control plane
make build-go
make run-control
```

**Step 3: Test Load Balancing**

```bash
# Send multiple requests to see round-robin distribution
for i in {1..9}; do
  echo "Request $i:"
  curl -s http://localhost:8080/api/test
  echo ""
done
```

Expected output: Requests should rotate through backend1 → backend2 → backend3 → backend1...

**Step 4: Test Health Checks**

```bash
# Check overall health
curl http://localhost:9090/health

# Expected: {"status":"ok","backends":{"localhost:3000":true,"localhost:3001":true,"localhost:3002":true}}
```

**Step 5: Test Backend Failover**

```bash
# Stop one backend (Ctrl+C in its terminal)
# Then test again
curl http://localhost:8080/api/test

# Verify only healthy backends receive traffic
curl http://localhost:9090/health
```

**Step 6: Monitor Metrics**

```bash
# View Prometheus metrics
curl http://localhost:9091/metrics

# Metrics include:
# - go_goroutines
# - go_memstats_*
# - process_*
# And more...
```

### Testing with netcat (Raw TCP/UDP)

```bash
# Test raw TCP connection
printf "GET /health HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n" | nc localhost 8080

# Test UDP proxy
echo "test_message" | nc -u -w1 localhost 8081
```

### UDP Proxy Testing

Aegis includes a comprehensive UDP test suite:

```bash
# Start UDP backends
make udp-backends-start
# or
./scripts/test-udp-proxy.sh start

# Run comprehensive UDP tests
make test-udp
# or
./scripts/test-udp-proxy.sh test

# Tests include:
# - Basic packet forwarding
# - Load balancing across backends
# - NAT session tracking
# - Rate limiting
# - Circuit breaker
# - Metrics collection
# - Stress testing (1000 packets)

# Stop UDP backends
make udp-backends-stop
# or
./scripts/test-udp-proxy.sh stop
```

### Unit Tests

```bash
# Test Go control plane
cd control-plane && go test ./...

# Test Rust data plane
cd data-plane && cargo test

# Run all tests
make test
```

### Advanced Testing Scenarios

#### Test Advanced Features

```bash
# Run comprehensive test suite for advanced features
./scripts/test-advanced-features.sh

# Tests include:
# - Load balancing algorithms (round-robin, least-connections, weighted, consistent-hash)
# - Rate limiting and burst handling
# - Circuit breaker failure detection
# - UDP proxy session management
# - Metrics collection
```

#### Test 1: Sustained Load
```bash
# Install Apache Bench (if not installed)
# Ubuntu: sudo apt install apache2-utils
# macOS: brew install httpd

# Send 1000 requests with 10 concurrent connections
ab -n 1000 -c 10 http://localhost:8080/api/test
```

#### Test 2: Health Check Failover
```bash
# 1. Monitor backend health in real-time
watch -n 1 'curl -s http://localhost:9090/health | jq'

# 2. Stop a backend (Ctrl+C in backend terminal)
# 3. Observe automatic traffic redirection to healthy backends
# 4. Restart the backend and watch it rejoin the pool
```

#### Test 3: Configuration Validation
```bash
# Edit config.yaml to add/modify backends
vim config.yaml

# Restart control plane to apply changes
# New configuration will be pushed to data plane automatically
```

#### Test 4: Graceful Shutdown
```bash
# Drain connections
curl -X POST http://localhost:9090/drain

# Verify no active connections
curl http://localhost:9090/status
```

### Load Testing

#### Using wrk
```bash
# Install wrk
git clone https://github.com/wg/wrk.git
cd wrk && make

# Run load test
./wrk -t12 -c400 -d30s http://localhost:8080/
```

#### Using hey
```bash
# Install hey
go install github.com/rakyll/hey@latest

# Run load test
hey -n 10000 -c 100 http://localhost:8080/
```

#### Using Apache Bench
```bash
ab -n 10000 -c 100 http://localhost:8080/
```

### Performance Benchmarks

Expected performance targets:

- **TCP Throughput**: 10Gbps+ on modern hardware
- **UDP Packet Rate**: 1M+ packets/sec
- **Latency Overhead**: <1ms p99
- **Memory Usage**: <50MB under load
- **Connection Capacity**: 100K+ concurrent connections

Test with:
```bash
# TCP throughput
iperf3 -c localhost -p 8080

# Custom UDP load generator (to be implemented)
# Monitor with: watch -n 1 'curl -s http://localhost:9091/metrics | grep proxy_'
```

## Monitoring

Aegis provides comprehensive observability through Prometheus metrics and Grafana dashboards.

### Quick Start Monitoring

When using Docker Compose, monitoring is automatically configured:

```bash
# Start the full stack with monitoring
docker-compose up -d

# Access monitoring tools
open http://localhost:3030  # Grafana (admin/admin)
open http://localhost:9092  # Prometheus
```

### Prometheus Metrics

**Metrics Endpoint:** `http://localhost:9091/metrics`

**Key Metrics:**

**Request Metrics:**
- `proxy_requests_total{backend="..."}` - Total requests per backend
- `proxy_errors_total{backend="..."}` - Total errors per backend
- `proxy_request_duration_seconds` - Request latency histogram

**Connection Metrics:**
- `proxy_active_connections` - Current active connections
- `proxy_total_connections` - Total connections handled
- `proxy_bytes_sent_total` - Total bytes sent
- `proxy_bytes_received_total` - Total bytes received

**Circuit Breaker Metrics:**
- `proxy_circuit_breaker_state{backend="..."}` - State (0=closed, 1=half-open, 2=open)
- `proxy_circuit_breaker_trips_total{backend="..."}` - Number of trips

**Rate Limiter Metrics:**
- `proxy_rate_limit_rejected_total` - Rejected requests due to rate limiting

**Backend Health:**
- `proxy_backend_healthy{backend="..."}` - Health status (0=unhealthy, 1=healthy)
- `proxy_backend_connections{backend="..."}` - Per-backend connection count
- `proxy_backend_requests_total{backend="..."}` - Per-backend request count
- `proxy_backend_failures_total{backend="..."}` - Per-backend failure count

**Example Queries:**

```bash
# View all metrics
curl http://localhost:9091/metrics

# View only proxy metrics
curl http://localhost:9091/metrics | grep proxy_

# Check request rate
curl -s http://localhost:9091/metrics | grep proxy_requests_total
```

### Grafana Dashboards

**Pre-configured Dashboard includes:**
- Request rate and error rate
- Request latency (p50, p95, p99)
- Active connections
- Circuit breaker states per backend
- Rate limit rejections
- Backend health status
- Traffic distribution across backends

**Using Grafana:**

1. Navigate to http://localhost:3030
2. Login with `admin` / `admin` (change password on first login)
3. Go to **Dashboards** → **Browse**
4. Select **Aegis Proxy Dashboard**
5. Dashboard auto-refreshes and shows last 15 minutes by default

**Customizing Dashboards:**

1. Open the Aegis Proxy Dashboard
2. Click the gear icon (⚙️) to edit
3. Add new panels or modify existing ones
4. Click "Save dashboard" to persist changes


### Prometheus Configuration

Prometheus is configured to scrape metrics every 10 seconds. Configuration in `prometheus.yml`:

```yaml
global:
  scrape_interval: 10s

scrape_configs:
  - job_name: 'aegis'
    static_configs:
      - targets: ['control-plane:9091']
```

**Alerting Rules:**

The following alerts are configured in `prometheus-alerts.yml`:

| Alert | Severity | Condition | Duration |
|-------|----------|-----------|----------|
| HighProxyErrorRate | warning | Error rate > 5% | 5 minutes |
| CircuitBreakerOpen | warning | Circuit breaker open | 2 minutes |
| HighRateLimitRejections | info | > 10 rejections/sec | 5 minutes |
| BackendUnhealthy | critical | Backend down | 1 minute |
| HighConnectionCount | warning | > 1000 connections | 10 minutes |

**Check Alerts:**
```bash
# View active alerts
open http://localhost:9092/alerts

# View alert rules
open http://localhost:9092/rules
```



### Testing Monitoring

Generate test traffic to see metrics in action:

```bash
# Generate TCP traffic
for i in {1..100}; do
  curl -s http://localhost:8080/ > /dev/null
  sleep 0.1
done

# Check metrics update
curl http://localhost:9091/metrics | grep proxy_requests_total

# View in Grafana
open http://localhost:3030
```

### Admin API Endpoints

Monitor and control Aegis via the Admin API (port 9090):

```bash
# Health status with backend states
curl http://localhost:9090/health

# Proxy configuration and status
curl http://localhost:9090/status

# Reload configuration (hot reload)
curl -X POST http://localhost:9090/reload

# Drain connections for graceful shutdown
curl -X POST http://localhost:9090/drain
```

### Data Persistence

Both Prometheus and Grafana use Docker volumes for data persistence:
- `prometheus-data` - Stores time-series data
- `grafana-storage` - Stores dashboards and settings

**Reset monitoring data:**
```bash
docker-compose down -v
docker-compose up -d
```

### Troubleshooting Monitoring

**Grafana shows "No data":**
```bash
# Check Prometheus is running
docker-compose ps prometheus

# Verify Prometheus is scraping
open http://localhost:9092/targets

# Ensure control-plane exposes metrics
curl http://localhost:9091/metrics
```

**Prometheus can't scrape control-plane:**
```bash
# Check control-plane health
docker-compose ps control-plane

# Test network connectivity
docker-compose exec prometheus wget -O- http://control-plane:9091/metrics
```

**Alerts not firing:**
```bash
# Generate load to trigger conditions
ab -n 1000 -c 50 http://localhost:8080/

# Check alert status
open http://localhost:9092/alerts

# Verify rules are loaded
open http://localhost:9092/rules
```

### Production Recommendations

For production deployments:

1. **Configure AlertManager** for notifications (email, Slack, PagerDuty)
2. **Increase retention** in Prometheus (default: 15 days)
3. **Enable authentication** on Prometheus
4. **Use HTTPS** with proper certificates
5. **Set up Grafana SSO** for team access
6. **Configure backups** for dashboards and Prometheus data
7. **Tune scrape intervals** based on your needs

## Project Structure

```
aegis/
├── proto/                    # Shared protobuf definitions
│   └── proxy.proto          # gRPC service definitions
│
├── control-plane/           # Go control plane
│   ├── cmd/
│   │   └── main.go         # Entry point
│   ├── internal/
│   │   ├── api/            # REST API handlers
│   │   ├── config/         # Configuration management
│   │   ├── grpc/           # gRPC client to data plane
│   │   ├── health/         # Health checker
│   │   └── metrics/        # Prometheus metrics
│   ├── proto/              # Generated protobuf code
│   ├── aegis-control       # Binary (after build)
│   └── go.mod
│
├── data-plane/              # Rust data plane
│   ├── src/
│   │   ├── main.rs         # Entry point
│   │   ├── tcp_proxy.rs    # TCP forwarding logic
│   │   ├── udp_proxy.rs    # UDP forwarding logic
│   │   ├── grpc_server.rs  # gRPC service implementation
│   │   ├── load_balancer.rs # Load balancing algorithms
│   │   ├── rate_limiter.rs  # Rate limiting
│   │   ├── circuit_breaker.rs # Circuit breaker
│   │   ├── connection.rs    # Connection management
│   │   ├── config.rs        # Configuration structures
│   │   └── metrics.rs       # Metrics collection
│   ├── target/release/
│   │   └── aegis-data      # Binary (after build)
│   ├── Cargo.toml
│   └── build.rs
│
├── examples/                # Test utilities
│   ├── simple-http-server.py # Test HTTP backend server
│   ├── udp-echo-server.py    # Test UDP backend server
│   └── examples.md           # Examples documentation
│
├── scripts/                 # Testing and automation scripts
│   ├── test-proxy.sh            # TCP proxy testing
│   ├── test-udp-proxy.sh        # UDP proxy testing
│   └── test-advanced-features.sh # Advanced features testing
│
├── grafana/                 # Grafana configuration
│   └── provisioning/
│       ├── dashboards/     # Pre-configured dashboards
│       └── datasources/    # Datasource configuration
│
├── config.yaml              # Local development config (localhost)
├── config.docker.yaml       # Docker config (service names)
├── docker-compose.yml       # Full stack deployment
├── Dockerfile.control       # Control plane image
├── Dockerfile.data          # Data plane image
├── prometheus.yml           # Prometheus configuration
├── prometheus-alerts.yml    # Alert rules
├── Makefile                 # Build automation
├── README.md                # This file
└── TODO.md                  # Project tasks and roadmap
```

## Development

### Code Formatting
```bash
# Format Go code
cd control-plane && go fmt ./...

# Format Rust code
cd data-plane && cargo fmt
```

### Linting
```bash
# Lint Go code
cd control-plane && go vet ./...

# Lint Rust code
cd data-plane && cargo clippy
```

### Development Workflow
```bash
# Format, lint, test, and build
make dev
```

## Troubleshooting

### Common Issues

**1. gRPC Connection Failed**
- Ensure data plane starts before control plane
- Check port 50051 is not in use: `lsof -i :50051`

**2. Port Already in Use**
- Change ports in `config.yaml`
- Check what's using the port: `lsof -i :8080`

**3. Protobuf Generation Fails**
- Verify protoc is installed: `protoc --version`
- Install Go plugins: `go install google.golang.org/protobuf/cmd/protoc-gen-go@latest`
- **Important**: Ensure Go's bin directory is in your PATH (see [Configure Go PATH](#configure-go-path-important) section)

**4. `protoc-gen-go: program not found or is not executable`**

This error means protoc can't find the Go plugins. Fix it by adding Go's bin directory to your PATH:

```bash
# Check where Go installs binaries
go env GOPATH

# Add to your shell config (~/.bashrc or ~/.zshrc)
export GOPATH="$HOME/go"
export PATH="$PATH:$GOPATH/bin"

# Reload your shell
source ~/.bashrc  # or source ~/.zshrc

# Verify it works
which protoc-gen-go
protoc-gen-go --version
```

**5. Backend Connection Refused**
- Ensure backend services are running
- Check backend addresses in `config.yaml`
- Verify health check paths are correct

## Roadmap

- [x] Basic TCP proxy
- [x] Health checking
- [x] Metrics pipeline
- [x] Load balancing (round-robin, weighted, least-connections, consistent-hash)
- [x] UDP proxy with NAT
- [x] Rate limiting
- [x] Circuit breaking
- [ ] Hot reload without connection drops
- [ ] HTTP/2 support
- [ ] Distributed tracing

## Contributing

Contributions are welcome! Please:

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

## License

MIT License - see LICENSE file for details

## Acknowledgments

Built with:
- Go - Control plane and orchestration
- Rust - High-performance data plane
- gRPC - Inter-process communication
- Tokio - Async runtime for Rust
- Prometheus - Metrics collection

---

**Author**: lazzerex  
**Project**: Aegis Network Proxy
