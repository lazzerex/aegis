# Aegis Examples

This directory contains example backend servers and test scripts for Aegis proxy.

## Simple HTTP Server

A lightweight HTTP server for testing the Aegis proxy functionality.

### Features

- Health check endpoint (`/health`)
- API test endpoint (`/api/*`)
- Pretty HTML interface for browser testing
- Configurable port and server name
- JSON responses with timestamps

### Usage

Start a single backend:

```bash
python3 simple-http-server.py --port 3000 --name backend1
```

Start multiple backends for load balancing tests:

```bash
# Terminal 1
python3 simple-http-server.py --port 3000 --name backend1

# Terminal 2
python3 simple-http-server.py --port 3001 --name backend2

# Terminal 3
python3 simple-http-server.py --port 3002 --name backend3
```

### Options

- `--port` - Port to listen on (default: 3000)
- `--name` - Server name identifier (default: backend)
- `--host` - Host to bind to (default: 0.0.0.0)

### Endpoints

- `GET /health` - Returns JSON health status
- `GET /api/*` - Returns JSON with request details
- `GET /` - Returns HTML interface

## Quick Test Script

Use the provided test script to quickly start all backends and test the proxy:

```bash
# Start test backends
./test-proxy.sh start

# Test the proxy
./test-proxy.sh test

# Stop test backends
./test-proxy.sh stop
```

See the main [README.md](../README.md) for complete testing instructions.
