# Aegis Quick Reference

## Essential Commands

### Build
```bash
make all            # Build everything
make build-go       # Build Go control plane only
make build-rust     # Build Rust data plane only
make proto          # Generate protobuf code
```

### Run (Development)
```bash
# Terminal 1
make run-data       # Start Rust data plane

# Terminal 2  
make run-control    # Start Go control plane
```

### Testing
```bash
./test-proxy.sh start         # Start test backends
./test-proxy.sh test-proxy    # Test load balancing
./test-proxy.sh status        # Check all services
./test-proxy.sh stop          # Stop test backends
```

### Development
```bash
make test           # Run all tests
make fmt            # Format code
make lint           # Lint code
make clean          # Clean build artifacts
```

## Default Ports

| Service          | Port  | Purpose                    |
|------------------|-------|----------------------------|
| TCP Proxy        | 8080  | Main proxy entry point     |
| UDP Proxy        | 8081  | UDP proxy entry point      |
| Admin API        | 9090  | Control & management       |
| Metrics          | 9091  | Prometheus metrics         |
| gRPC (Internal)  | 50051 | Control/data plane comms   |
| Test Backend 1   | 3000  | Test HTTP server           |
| Test Backend 2   | 3001  | Test HTTP server           |
| Test Backend 3   | 3002  | Test HTTP server           |

## Key Endpoints

### Proxy
```bash
# Send requests (automatically load balanced)
curl http://localhost:8080/api/test
```

### Admin API
```bash
# Check health & backends
curl http://localhost:9090/health

# View status
curl http://localhost:9090/status
```

### Metrics
```bash
# Prometheus metrics
curl http://localhost:9091/metrics
```

### Test Backends
```bash
# Health check
curl http://localhost:3000/health
curl http://localhost:3001/health
curl http://localhost:3002/health

# API endpoint
curl http://localhost:3000/api/test
```

## Configuration

Edit `config.yaml`:

```yaml
proxy:
  listen:
    tcp: "0.0.0.0:8080"    # TCP proxy port
    udp: "0.0.0.0:8081"    # UDP proxy port
  
  backends:
    - address: "localhost:3000"
      weight: 100           # Load balancing weight
      health_check:
        interval: 5s
        timeout: 2s
        path: "/health"

  load_balancing:
    algorithm: "round_robin"  # round_robin, weighted
```

## Quick Start Workflow

1. **Build & Start**
   ```bash
   make all
   ./test-proxy.sh start
   make run-data      # Terminal 1
   make run-control   # Terminal 2
   ```

2. **Test**
   ```bash
   ./test-proxy.sh test-proxy
   ```

3. **Monitor**
   ```bash
   curl http://localhost:9090/health
   curl http://localhost:9091/metrics | grep proxy_
   ```

4. **Stop**
   ```bash
   # Ctrl+C in both terminals
   ./test-proxy.sh stop
   ```

## Troubleshooting

**Port already in use?**
```bash
lsof -i :8080
lsof -i :50051
```

**Backends not responding?**
```bash
./test-proxy.sh status
./test-proxy.sh test-backends
```

**Logs location:**
- Data plane: stdout/stderr or `/tmp/data.log`
- Control plane: stdout/stderr or `/tmp/control.log`
- Test backends: `/tmp/backend[1-3].log`

## Load Balancing Test

Watch requests distribute across backends:
```bash
for i in {1..9}; do
  curl -s http://localhost:8080/api/test | grep -o '"server": "[^"]*"'
done
```

Expected output pattern:
```
"server": "backend1"
"server": "backend2"
"server": "backend3"
"server": "backend1"
...
```

## Development Workflow

1. Make changes
2. `make fmt lint`
3. `make test`
4. `make build-go` or `make build-rust`
5. Restart affected component
6. Test with `./test-proxy.sh`

---

For detailed documentation, see [README.md](README.md)
