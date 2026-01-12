# Aegis

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Go Version](https://img.shields.io/badge/Go-1.22%2B-blue)](https://golang.org)
[![Rust Version](https://img.shields.io/badge/Rust-1.75%2B-orange)](https://www.rust-lang.org)

Aegis is an in-development high-performance network proxy that combines Go's control plane with Rust's data plane for optimal performance and maintainability.

The control plane handles configuration, health checks, and load balancing logic in Go, while the data plane processes packets in Rust for minimal latency overhead. The two communicate via gRPC, allowing independent development and deployment of each component.

Aegis is designed for production use in microservice architectures and backend infrastructure, but also serves as a reference implementation for building high-performance networked systems.

> 📚 **New to Aegis?** Check out the [Quick Start Guide](QUICKSTART.md) for a fast reference!

## Quick Start

```bash
# Clone and build
git clone https://github.com/lazzerex/aegis.git
cd aegis
make all

# Start test backends
./scripts/test-proxy.sh start

# Run Aegis (in separate terminals)
make run-data      # Terminal 1
make run-control   # Terminal 2

# Test it
./scripts/test-proxy.sh test-proxy

# Stop test backends
./scripts/test-proxy.sh stop
```

See the [Testing](#testing) section for detailed instructions.

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

The binaries will be created in the `bin/` directory:
- `bin/aegis-control` - Go control plane
- `bin/aegis-data` - Rust data plane

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

### Option 1: Manual Start (Development)

Open three terminals:

**Terminal 1: Start Rust Data Plane**
```bash
cd aegis
make run-data
```

**Terminal 2: Start Go Control Plane**
```bash
cd aegis
make run-control
```

**Terminal 3: Test the Proxy**
```bash
# Test TCP proxy
curl http://localhost:8080

# Check metrics
curl http://localhost:9091/metrics

# Check health status
curl http://localhost:9090/health

# Check proxy status
curl http://localhost:9090/status
```

### Option 2: Docker Compose (Production)

Start the entire stack with Docker Compose:

```bash
# Build images
make docker-build

# Start all services
make docker-up

# View logs
make docker-logs

# Test the deployment
make docker-test

# Stop all services
make docker-down
```

Or use docker-compose directly:

```bash
docker-compose up -d
```

This will start:
- **Aegis data plane** (TCP/UDP proxy on ports 8080/8081)
- **Aegis control plane** (Admin API on 9090, Metrics on 9091)
- **Test backend servers** (HTTP backends on ports 3000-3002)
- **UDP backend** (UDP echo server on port 5000)
- **Prometheus** (Metrics collection on port 9092)
- **Grafana** (Visualization on port 3030, default login: admin/admin)

Access services:
- Proxy: http://localhost:8080
- Admin API: http://localhost:9090
- Metrics: http://localhost:9091/metrics
- Prometheus: http://localhost:9092
- Grafana: http://localhost:3030

## Testing

### Quick Start Testing

Aegis includes test backend servers and a convenient test script to quickly verify functionality.

#### Automated Testing (Recommended)

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

### Testing with netcat (Raw TCP)

```bash
# Test raw TCP connection
printf "GET /health HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n" | nc localhost 8080
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

See [ADVANCED_FEATURES.md](ADVANCED_FEATURES.md) for detailed testing instructions.

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

### Prometheus Metrics

Available at `http://localhost:9091/metrics`

Key metrics:
- `proxy_active_connections` - Current active connections
- `proxy_total_connections` - Total connections handled
- `proxy_bytes_sent_total` - Total bytes sent
- `proxy_bytes_received_total` - Total bytes received
- `proxy_latency_avg_ms` - Average latency
- `proxy_latency_p99_ms` - P99 latency
- `proxy_backend_connections{backend="..."}` - Per-backend connection count
- `proxy_backend_requests_total{backend="..."}` - Per-backend request count
- `proxy_backend_failures_total{backend="..."}` - Per-backend failure count

### Admin API Endpoints

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

### Prometheus + Grafana Setup

```yaml
# prometheus.yml
global:
  scrape_interval: 15s

scrape_configs:
  - job_name: 'aegis'
    static_configs:
      - targets: ['localhost:9091']
```

```bash
# Start Prometheus
prometheus --config.file=prometheus.yml

# Start Grafana
docker run -d -p 3000:3000 grafana/grafana

# Access Grafana at http://localhost:3000
# Default credentials: admin/admin
```

## Project Structure

```
aegis/
├── proto/                    # Shared protobuf definitions
│   └── proxy.proto
├── control-plane/           # Go control plane
│   ├── cmd/
│   │   └── main.go
│   ├── internal/
│   │   ├── api/            # REST API handlers
│   │   ├── config/         # Configuration management
│   │   ├── grpc/           # gRPC client to Rust
│   │   ├── health/         # Health checker
│   │   └── metrics/        # Prometheus metrics
│   └── go.mod
├── data-plane/              # Rust data plane
│   ├── src/
│   │   ├── main.rs
│   │   ├── tcp_proxy.rs    # TCP forwarding
│   │   ├── udp_proxy.rs    # UDP forwarding
│   │   ├── grpc_server.rs  # gRPC service
│   │   ├── load_balancer.rs
│   │   └── metrics.rs
│   └── Cargo.toml
├── examples/                # Testing utilities
│   ├── simple-http-server.py  # Test backend server
│   └── README.md           # Examples documentation
├── scripts/
│   ├── test-proxy.sh            # Test automation script
│   └── test-advanced-features.sh # Advanced features tests
├── config.yaml
├── Makefile
├── docker-compose.yml
└── README.md
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

**4. Backend Connection Refused**
- Ensure backend services are running
- Check backend addresses in `config.yaml`
- Verify health check paths are correct

## Roadmap

- [x] Basic TCP proxy
- [x] Health checking
- [x] Metrics pipeline
- [x] Load balancing (round-robin, weighted)
- [ ] UDP proxy with NAT
- [ ] Rate limiting
- [ ] Circuit breaking
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
