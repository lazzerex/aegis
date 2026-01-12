# Aegis Examples

This directory contains example backend servers and test scripts for Aegis proxy.

## Simple HTTP Server

A lightweight HTTP server for testing the Aegis TCP proxy functionality.

### Features

- Health check endpoint (`/health`)
- API test endpoint (`/api/*`)
- Pretty HTML interface for browser testing
- Configurable port and server name
- JSON responses with timestamps

### Usage

Start a single backend:

```bash
python3 examples/simple-http-server.py --port 3000 --name backend1
```

Start multiple backends for load balancing tests:

```bash
# Terminal 1
python3 examples/simple-http-server.py --port 3000 --name backend1

# Terminal 2
python3 examples/simple-http-server.py --port 3001 --name backend2

# Terminal 3
python3 examples/simple-http-server.py --port 3002 --name backend3
```

### Options

- `--port` - Port to listen on (default: 3000)
- `--name` - Server name identifier (default: backend)
- `--host` - Host to bind to (default: 0.0.0.0)

### Endpoints

- `GET /health` - Returns JSON health status
- `GET /api/*` - Returns JSON with request details
- `GET /` - Returns HTML interface

## UDP Echo Server

A simple UDP echo server for testing the Aegis UDP proxy with NAT mapping.

### Features

- Echoes received packets with metadata
- JSON response format
- Packet counting and statistics
- Session tracking support

### Usage

Start a single UDP backend:

```bash
python3 examples/udp-echo-server.py --port 5000 --name udp-backend1
```

Start multiple UDP backends:

```bash
# Terminal 1
python3 examples/udp-echo-server.py --port 5000 --name udp-backend1

# Terminal 2
python3 examples/udp-echo-server.py --port 5001 --name udp-backend2
```

### Options

- `--port` - Port to listen on (default: 5000)
- `--name` - Server name identifier (default: udp-backend)
- `--host` - Host to bind to (default: 0.0.0.0)

### Testing

Send UDP packets to the proxy:

```bash
# Using netcat
echo "Hello from client" | nc -u localhost 8081

# Using Python
python3 -c "import socket; s=socket.socket(socket.AF_INET, socket.SOCK_DGRAM); s.sendto(b'test', ('localhost', 8081)); print(s.recvfrom(1024))"
```

The server will respond with JSON containing:
- Server name
- Timestamp
- Packet number
- Received bytes count
- Echo of the original message

## Quick Test Script

Use the provided test script to quickly start all backends and test the proxy:

```bash
# Start test backends
./scripts/test-proxy.sh start

# Test the proxy
./scripts/test-proxy.sh test-proxy

# Check backend status
./scripts/test-proxy.sh status

# Stop test backends
./scripts/test-proxy.sh stop
```

Or use the Makefile shortcuts:

```bash
make backends-start    # Start backends
make test-proxy        # Test proxy
make backends-stop     # Stop backends
```

See the main [README.md](../README.md) for complete testing instructions.
