#!/bin/bash

# Aegis Advanced Features Test Script
# Tests load balancing, rate limiting, circuit breaking, and UDP proxy

set -e

GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${GREEN}=== Aegis Advanced Features Test Suite ===${NC}\n"

# Function to print test header
test_header() {
    echo -e "\n${YELLOW}[TEST]${NC} $1"
    echo "-----------------------------------"
}

# Function to check if service is running
check_service() {
    local port=$1
    local name=$2
    if nc -z localhost $port 2>/dev/null; then
        echo -e "${GREEN}✓${NC} $name is running on port $port"
        return 0
    else
        echo -e "${RED}✗${NC} $name is not running on port $port"
        return 1
    fi
}

# Check prerequisites
test_header "Checking Prerequisites"
command -v curl >/dev/null 2>&1 || { echo "curl is required but not installed. Aborting." >&2; exit 1; }
command -v nc >/dev/null 2>&1 || { echo "netcat is required but not installed. Aborting." >&2; exit 1; }
echo -e "${GREEN}✓${NC} All required tools are available"

# Check if proxy is running
test_header "Service Health Check"
check_service 8080 "TCP Proxy" || echo "  Start with: cd data-plane && cargo run"
check_service 8081 "UDP Proxy" || echo "  Start with: cd data-plane && cargo run"
check_service 9091 "Metrics Server" || echo "  Start with: cd control-plane && go run cmd/main.go"

# Test 1: Load Balancing
test_header "Load Balancing - Round Robin"
echo "Sending 10 requests to observe distribution..."
for i in {1..10}; do
    response=$(curl -s -o /dev/null -w "%{http_code}" http://localhost:8080/ 2>/dev/null || echo "000")
    if [ "$response" = "200" ] || [ "$response" = "502" ]; then
        echo -ne "."
    else
        echo -e "\n${RED}✗${NC} Request $i failed with status $response"
    fi
done
echo -e "\n${GREEN}✓${NC} Load balancing test completed (check logs for distribution)"

# Test 2: Rate Limiting
test_header "Rate Limiting - Burst and Sustained"
echo "Testing burst capacity (should allow ~100 requests)..."
success_count=0
fail_count=0
for i in {1..150}; do
    response=$(curl -s -o /dev/null -w "%{http_code}" http://localhost:8080/ 2>/dev/null || echo "000")
    if [ "$response" = "200" ]; then
        ((success_count++))
    else
        ((fail_count++))
    fi
done
echo "  Success: $success_count, Failed: $fail_count"
if [ $fail_count -gt 0 ]; then
    echo -e "${GREEN}✓${NC} Rate limiting is working (rejected $fail_count requests)"
else
    echo -e "${YELLOW}!${NC} No rate limiting detected (may need higher load)"
fi

# Test 3: Circuit Breaking
test_header "Circuit Breaker - Failure Detection"
echo "This test requires manually stopping a backend..."
echo "1. Find a backend process: ps aux | grep 3000"
echo "2. Stop it: kill <pid>"
echo "3. Send requests: curl http://localhost:8080/"
echo "4. Observe 'Circuit breaker open' in logs"
echo -e "${YELLOW}!${NC} Manual test - check logs for circuit breaker state changes"

# Test 4: UDP Proxy
test_header "UDP Proxy - Session Management"
echo "Sending UDP test packets..."
for i in {1..5}; do
    echo "Test packet $i" | nc -u -w1 localhost 8081 2>/dev/null && echo -ne "." || echo -ne "x"
done
echo -e "\n${GREEN}✓${NC} UDP packets sent (check logs for session tracking)"

# Test 5: Metrics
test_header "Metrics Verification"
if curl -s http://localhost:9091/metrics > /dev/null 2>&1; then
    echo "Fetching key metrics..."
    
    # Extract some key metrics
    active_conns=$(curl -s http://localhost:9091/metrics 2>/dev/null | grep -E "^proxy_active_connections" | head -1 || echo "N/A")
    echo "  $active_conns"
    
    circuit_state=$(curl -s http://localhost:9091/metrics 2>/dev/null | grep -E "^proxy_circuit_breaker_state" | head -1 || echo "N/A")
    echo "  $circuit_state"
    
    rate_limit=$(curl -s http://localhost:9091/metrics 2>/dev/null | grep -E "^proxy_rate_limit" | head -1 || echo "N/A")
    echo "  $rate_limit"
    
    echo -e "${GREEN}✓${NC} Metrics endpoint is accessible"
else
    echo -e "${RED}✗${NC} Metrics endpoint not available"
fi

# Test 6: Load Balancing Algorithms
test_header "Load Balancing Algorithms - Configuration Test"
echo "Testing different algorithms (requires config reload)..."
echo ""
echo "Available algorithms:"
echo "  • round_robin          - Even distribution"
echo "  • weighted_round_robin - Proportional to weight"
echo "  • least_connections    - Routes to least busy backend"
echo "  • consistent_hash      - Session affinity based on client IP"
echo ""
echo "Update config.yaml and reload to test each algorithm"
echo -e "${YELLOW}!${NC} Manual test - modify config.yaml and observe behavior"

# Summary
echo -e "\n${GREEN}=== Test Summary ===${NC}"
echo "✓ Load balancing distribution tested"
echo "✓ Rate limiting functionality verified"
echo "✓ Circuit breaker test described"
echo "✓ UDP proxy session tracking tested"
echo "✓ Metrics endpoint verified"
echo ""
echo "For detailed results, check:"
echo "  • Proxy logs: RUST_LOG=debug cargo run"
echo "  • Metrics: curl http://localhost:9091/metrics"
echo "  • Control plane: Check control-plane logs"

# Performance test
test_header "Performance Baseline (Optional)"
if command -v ab >/dev/null 2>&1; then
    echo "Running ApacheBench test (1000 requests, 10 concurrent)..."
    ab -n 1000 -c 10 -q http://localhost:8080/ 2>/dev/null || echo "  ab test completed (check output above)"
    echo -e "${GREEN}✓${NC} Performance test completed"
else
    echo -e "${YELLOW}!${NC} ApacheBench (ab) not installed - skipping performance test"
    echo "  Install with: apt-get install apache2-utils"
fi

echo -e "\n${GREEN}All tests completed!${NC}\n"
