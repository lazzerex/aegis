#!/bin/bash

# Aegis TCP Proxy Test Script
# Tests backend servers and proxy load balancing

set -e

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
EXAMPLE_DIR="${PROJECT_ROOT}/examples"
PID_FILE="/tmp/aegis-test-backends.pid"

function print_header() {
    echo -e "${BLUE}╔════════════════════════════════════════════════════════════╗${NC}"
    echo -e "${BLUE}║${NC}            ${GREEN}Aegis Proxy Test Script${NC}                   ${BLUE}║${NC}"
    echo -e "${BLUE}╚════════════════════════════════════════════════════════════╝${NC}"
    echo
}

function start_backends() {
    echo -e "${YELLOW}Starting test backend servers...${NC}"
    echo
    
    # Check if already running
    if [ -f "$PID_FILE" ]; then
        echo -e "${RED}Backend servers appear to be running already.${NC}"
        echo "Use '$0 stop' first, or check: $PID_FILE"
        exit 1
    fi
    
    # Start backends in background
    python3 "${EXAMPLE_DIR}/simple-http-server.py" --port 3000 --name backend1 > /tmp/backend1.log 2>&1 &
    echo $! > "$PID_FILE"
    
    python3 "${EXAMPLE_DIR}/simple-http-server.py" --port 3001 --name backend2 > /tmp/backend2.log 2>&1 &
    echo $! >> "$PID_FILE"
    
    python3 "${EXAMPLE_DIR}/simple-http-server.py" --port 3002 --name backend3 > /tmp/backend3.log 2>&1 &
    echo $! >> "$PID_FILE"
    
    # Wait a moment for servers to start
    sleep 2
    
    # Check if they're running
    if curl -s http://localhost:3000/health > /dev/null && \
       curl -s http://localhost:3001/health > /dev/null && \
       curl -s http://localhost:3002/health > /dev/null; then
        echo -e "${GREEN}✓ Backend1 started on port 3000${NC}"
        echo -e "${GREEN}✓ Backend2 started on port 3001${NC}"
        echo -e "${GREEN}✓ Backend3 started on port 3002${NC}"
        echo
        echo -e "Logs:"
        echo "  - /tmp/backend1.log"
        echo "  - /tmp/backend2.log"
        echo "  - /tmp/backend3.log"
    else
        echo -e "${RED}✗ Failed to start backend servers${NC}"
        stop_backends
        exit 1
    fi
}

function stop_backends() {
    echo -e "${YELLOW}Stopping test backend servers...${NC}"
    
    if [ ! -f "$PID_FILE" ]; then
        echo -e "${YELLOW}No PID file found. Backends may not be running.${NC}"
        # Try to kill by name anyway
        pkill -f "simple-http-server.py" 2>/dev/null || true
        return
    fi
    
    while read -r pid; do
        if kill "$pid" 2>/dev/null; then
            echo -e "${GREEN}✓ Stopped process $pid${NC}"
        fi
    done < "$PID_FILE"
    
    rm -f "$PID_FILE"
    echo -e "${GREEN}Backend servers stopped.${NC}"
}

function test_backends() {
    echo -e "${YELLOW}Testing backend servers directly...${NC}"
    echo
    
    for port in 3000 3001 3002; do
        if response=$(curl -s http://localhost:${port}/health 2>/dev/null); then
            server=$(echo "$response" | grep -o '"server": "[^"]*"' | cut -d'"' -f4)
            status=$(echo "$response" | grep -o '"status": "[^"]*"' | cut -d'"' -f4)
            echo -e "${GREEN}✓ Port ${port}: ${server} - ${status}${NC}"
        else
            echo -e "${RED}✗ Port ${port}: Not responding${NC}"
        fi
    done
}

function test_proxy() {
    echo -e "${YELLOW}Testing proxy load balancing...${NC}"
    echo
    
    # Check if proxy is running
    if ! curl -s http://localhost:8080/ > /dev/null 2>&1; then
        echo -e "${RED}✗ Proxy not responding on port 8080${NC}"
        echo "Make sure Aegis proxy is running:"
        echo "  Terminal 1: make run-data"
        echo "  Terminal 2: make run-control"
        exit 1
    fi
    
    echo "Sending 9 requests through proxy to observe load balancing:"
    echo
    
    for i in {1..9}; do
        response=$(curl -s http://localhost:8080/api/test)
        server=$(echo "$response" | grep -o '"server": "[^"]*"' | cut -d'"' -f4)
        echo -e "  Request $i: ${GREEN}→ ${server}${NC}"
        sleep 0.1
    done
    
    echo
    echo -e "${GREEN}✓ Load balancing test complete${NC}"
    echo
    echo "Expected pattern: backend1 → backend2 → backend3 → backend1 → ..."
}

function show_status() {
    echo -e "${YELLOW}Checking service status...${NC}"
    echo
    
    # Check backends
    echo -e "${BLUE}Backend Servers:${NC}"
    for port in 3000 3001 3002; do
        if curl -s http://localhost:${port}/health > /dev/null 2>&1; then
            echo -e "  Port ${port}: ${GREEN}✓ Running${NC}"
        else
            echo -e "  Port ${port}: ${RED}✗ Not running${NC}"
        fi
    done
    
    echo
    echo -e "${BLUE}Aegis Proxy:${NC}"
    
    # Check data plane (TCP proxy)
    if netstat -tln 2>/dev/null | grep -q ":8080 "; then
        echo -e "  Data Plane (8080): ${GREEN}✓ Running${NC}"
    else
        echo -e "  Data Plane (8080): ${RED}✗ Not running${NC}"
    fi
    
    # Check control plane
    if curl -s http://localhost:9090/health > /dev/null 2>&1; then
        echo -e "  Control Plane (9090): ${GREEN}✓ Running${NC}"
    else
        echo -e "  Control Plane (9090): ${RED}✗ Not running${NC}"
    fi
    
    # Check metrics
    if curl -s http://localhost:9091/metrics > /dev/null 2>&1; then
        echo -e "  Metrics (9091): ${GREEN}✓ Running${NC}"
    else
        echo -e "  Metrics (9091): ${RED}✗ Not running${NC}"
    fi
}

function show_help() {
    echo "Usage: $0 [command]"
    echo
    echo "Commands:"
    echo "  start          Start test backend servers"
    echo "  stop           Stop test backend servers"
    echo "  test-backends  Test backend servers directly"
    echo "  test-proxy     Test proxy load balancing"
    echo "  status         Show status of all services"
    echo "  help           Show this help message"
    echo
    echo "Examples:"
    echo "  $0 start              # Start backends"
    echo "  $0 test-proxy         # Test the proxy"
    echo "  $0 stop               # Stop backends"
}

# Main script
print_header

case "${1:-help}" in
    start)
        start_backends
        ;;
    stop)
        stop_backends
        ;;
    test-backends)
        test_backends
        ;;
    test-proxy)
        test_proxy
        ;;
    test)
        test_backends
        echo
        test_proxy
        ;;
    status)
        show_status
        ;;
    help|--help|-h)
        show_help
        ;;
    *)
        echo -e "${RED}Unknown command: $1${NC}"
        echo
        show_help
        exit 1
        ;;
esac
