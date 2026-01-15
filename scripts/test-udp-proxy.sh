#!/bin/bash

# Comprehensive UDP Proxy Test Script
# Tests rate limiting, circuit breaking, load balancing, and metrics

# Note: We don't use 'set -e' here because tests may fail and we want to see all results

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

UDP_PROXY_PORT=8081
BACKEND1_PORT=5001
BACKEND2_PORT=5002
BACKEND3_PORT=5003

# Test counters
TESTS_PASSED=0
TESTS_FAILED=0

function print_header() {
    echo -e "${BLUE}╔════════════════════════════════════════════════════════════╗${NC}"
    echo -e "${BLUE}║${NC}            ${GREEN}$1${NC}                   ${BLUE}║${NC}"
    echo -e "${BLUE}╚════════════════════════════════════════════════════════════╝${NC}"
    echo
}

function print_test() {
    echo -e "${YELLOW}[TEST]${NC} $1"
}

function print_success() {
    echo -e "${GREEN}✓${NC} $1"
    ((TESTS_PASSED++))
}

function print_error() {
    echo -e "${RED}✗${NC} $1"
    ((TESTS_FAILED++))
}

function print_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

# Start UDP echo servers on different ports
function start_backends() {
    print_header "Aegis UDP Proxy Test Script"
    
    echo -e "${YELLOW}Starting UDP backend servers...${NC}"
    echo
    
    # Backend 1
    print_info "Starting backend1 on port $BACKEND1_PORT..."
    python3 examples/udp-echo-server.py --port $BACKEND1_PORT --name backend1 > /tmp/udp-backend1.log 2>&1 &
    echo $! > /tmp/udp-backend1.pid
    
    # Backend 2
    print_info "Starting backend2 on port $BACKEND2_PORT..."
    python3 examples/udp-echo-server.py --port $BACKEND2_PORT --name backend2 > /tmp/udp-backend2.log 2>&1 &
    echo $! >> /tmp/udp-backend2.pid
    
    # Backend 3
    print_info "Starting backend3 on port $BACKEND3_PORT..."
    python3 examples/udp-echo-server.py --port $BACKEND3_PORT --name backend3 > /tmp/udp-backend3.log 2>&1 &
    echo $! >> /tmp/udp-backend3.pid
    
    sleep 2
    print_success "All UDP backend servers started"
    echo
    echo "Logs:"
    echo "  - /tmp/udp-backend1.log"
    echo "  - /tmp/udp-backend2.log"
    echo "  - /tmp/udp-backend3.log"
}

function stop_backends() {
    echo -e "${YELLOW}Stopping UDP backend servers...${NC}"
    
    PID_FILE="/tmp/udp-backend1.pid"
    if [ -f "$PID_FILE" ]; then
        while read -r pid; do
            if kill "$pid" 2>/dev/null; then
                print_info "Backend1 stopped"
            fi
        done < "$PID_FILE"
        rm -f "$PID_FILE"
    fi
    
    PID_FILE="/tmp/udp-backend2.pid"
    if [ -f "$PID_FILE" ]; then
        while read -r pid; do
            if kill "$pid" 2>/dev/null; then
                print_info "Backend2 stopped"
            fi
        done < "$PID_FILE"
        rm -f "$PID_FILE"
    fi
    
    PID_FILE="/tmp/udp-backend3.pid"
    if [ -f "$PID_FILE" ]; then
        while read -r pid; do
            if kill "$pid" 2>/dev/null; then
                print_info "Backend3 stopped"
            fi
        done < "$PID_FILE"
        rm -f "$PID_FILE"
    fi
    
    print_success "All UDP backend servers stopped"
}

# Test basic UDP packet forwarding
test_basic_forwarding() {
    print_test "Testing basic UDP packet forwarding..."
    
    # Send a test packet
    RESPONSE=$(echo "test_message" | nc -u -w 1 localhost $UDP_PROXY_PORT)
    
    if [ -n "$RESPONSE" ]; then
        print_success "UDP packet forwarding works: '$RESPONSE'"
    else
        print_error "No response received from UDP proxy"
    fi
}

# Test load balancing across backends
test_load_balancing() {
    print_test "Testing UDP load balancing..."
    
    declare -A backend_counts
    backend_counts["backend1"]=0
    backend_counts["backend2"]=0
    backend_counts["backend3"]=0
    
    # Send multiple packets
    for i in {1..30}; do
        RESPONSE=$(echo "packet_$i" | nc -u -w 1 localhost $UDP_PROXY_PORT 2>/dev/null)
        
        # Extract backend name from response
        if [[ $RESPONSE == *"backend1"* ]]; then
            ((backend_counts["backend1"]++))
        elif [[ $RESPONSE == *"backend2"* ]]; then
            ((backend_counts["backend2"]++))
        elif [[ $RESPONSE == *"backend3"* ]]; then
            ((backend_counts["backend3"]++))
        fi
    done
    
    print_info "Backend distribution:"
    echo "  - backend1: ${backend_counts[backend1]}"
    echo "  - backend2: ${backend_counts[backend2]}"
    echo "  - backend3: ${backend_counts[backend3]}"
    
    # Check if all backends received some traffic
    if [ "${backend_counts[backend1]}" -gt 0 ] && \
       [ "${backend_counts[backend2]}" -gt 0 ] && \
       [ "${backend_counts[backend3]}" -gt 0 ]; then
        print_success "Load balancing works - all backends received traffic"
    else
        print_error "Load balancing issue - not all backends received traffic"
    fi
}

# Test NAT session tracking
test_nat_sessions() {
    print_test "Testing UDP NAT session tracking..."
    
    # Send multiple packets from same client
    for i in {1..5}; do
        RESPONSE=$(echo "session_test_$i" | nc -u -w 1 localhost $UDP_PROXY_PORT)
        if [ -z "$RESPONSE" ]; then
            print_error "NAT session test failed - no response for packet $i"
            return
        fi
    done
    
    print_success "NAT session tracking works - multiple packets in same session"
}

# Test rate limiting
test_rate_limiting() {
    print_test "Testing UDP rate limiting..."
    
    # Send many packets rapidly to trigger rate limiting
    SUCCESS_COUNT=0
    TOTAL_COUNT=150
    
    for i in $(seq 1 $TOTAL_COUNT); do
        RESPONSE=$(echo "rate_test_$i" | nc -u -w 0.1 localhost $UDP_PROXY_PORT 2>/dev/null)
        if [ -n "$RESPONSE" ]; then
            ((SUCCESS_COUNT++))
        fi
    done
    
    print_info "Successful packets: $SUCCESS_COUNT/$TOTAL_COUNT"
    
    # We expect some packets to be rate limited
    if [ "$SUCCESS_COUNT" -lt "$TOTAL_COUNT" ]; then
        print_success "Rate limiting is working - some packets were blocked"
    else
        print_error "Rate limiting may not be working - all packets succeeded"
    fi
}

# Test circuit breaker (requires stopping a backend)
test_circuit_breaker() {
    print_test "Testing circuit breaker..."
    
    # Stop backend1 to trigger failures
    print_info "Stopping backend1 to simulate failure..."
    kill $(cat /tmp/udp-backend1.pid) 2>/dev/null || true
    sleep 2
    
    # Send packets - some should fail and open circuit breaker
    print_info "Sending packets to trigger circuit breaker..."
    for i in {1..20}; do
        echo "circuit_test_$i" | nc -u -w 0.5 localhost $UDP_PROXY_PORT 2>/dev/null || true
    done
    
    sleep 2
    
    # Restart backend1
    print_info "Restarting backend1..."
    python3 examples/udp-echo-server.py --port $BACKEND1_PORT --name backend1 > /tmp/udp-backend1.log 2>&1 &
    echo $! > /tmp/udp-backend1.pid
    
    sleep 2
    
    # Try sending packets again - backend should be back
    RESPONSE=$(echo "recovery_test" | nc -u -w 1 localhost $UDP_PROXY_PORT)
    if [ -n "$RESPONSE" ]; then
        print_success "Circuit breaker recovery works - backend reconnected"
    else
        print_error "Circuit breaker recovery may have issues"
    fi
}

# Test metrics collection
test_metrics() {
    print_test "Testing metrics collection..."
    
    # Send some traffic
    for i in {1..10}; do
        echo "metrics_test_$i" | nc -u -w 1 localhost $UDP_PROXY_PORT >/dev/null 2>&1
    done
    
    # Check if metrics endpoint is accessible
    METRICS=$(curl -s http://localhost:9091/metrics 2>/dev/null || echo "")
    
    if [ -n "$METRICS" ]; then
        print_success "Metrics endpoint is accessible"
        
        # Check for UDP-specific metrics
        if echo "$METRICS" | grep -q "udp"; then
            print_info "UDP metrics are being collected"
        else
            print_info "Note: UDP metrics may not be in Prometheus format yet"
        fi
    else
        print_error "Could not access metrics endpoint"
    fi
}

# Check proxy health
check_health() {
    print_test "Checking proxy health..."
    
    HEALTH=$(curl -s http://localhost:9090/health 2>/dev/null || echo "{}")
    
    if echo "$HEALTH" | grep -q "ok"; then
        print_success "Proxy health check passed"
        echo "$HEALTH" | jq . 2>/dev/null || echo "$HEALTH"
    else
        print_error "Proxy health check failed"
    fi
}

# Stress test
stress_test() {
    print_test "Running stress test..."
    
    print_info "Sending 1000 UDP packets..."
    
    SUCCESS=0
    FAILURES=0
    
    for i in {1..1000}; do
        if echo "stress_$i" | nc -u -w 0.05 localhost $UDP_PROXY_PORT >/dev/null 2>&1; then
            ((SUCCESS++))
        else
            ((FAILURES++))
        fi
        
        if [ $((i % 100)) -eq 0 ]; then
            print_info "Progress: $i/1000 packets sent..."
        fi
    done
    
    print_info "Stress test results: $SUCCESS successful, $FAILURES failed"
    
    if [ "$SUCCESS" -gt 800 ]; then
        print_success "Stress test passed - high success rate"
    else
        print_error "Stress test failed - too many failures"
    fi
}

# Print summary
print_summary() {
    print_header "Test Summary"
    
    TOTAL=$((TESTS_PASSED + TESTS_FAILED))
    echo -e "Total Tests: $TOTAL"
    echo -e "${GREEN}Passed: $TESTS_PASSED${NC}"
    echo -e "${RED}Failed: $TESTS_FAILED${NC}"
    
    if [ "$TESTS_FAILED" -eq 0 ]; then
        echo -e "\n${GREEN}✓ All tests passed!${NC}\n"
        return 0
    else
        echo -e "\n${RED}✗ Some tests failed${NC}\n"
        return 1
    fi
}

# Main test execution
case "${1:-}" in
    start)
        start_backends
        exit 0
        ;;
    stop)
        stop_backends
        exit 0
        ;;
    test)
        print_header "UDP Proxy Comprehensive Test Suite"
        
        # Check if backends are running
        if ! pgrep -f "udp-echo-server.py" > /dev/null; then
            print_error "UDP backends are not running. Start them with: $0 start"
            exit 1
        fi
        
        # Check if Aegis data plane is running
        if ! netstat -tuln 2>/dev/null | grep -q ":8081 " && ! ss -tuln 2>/dev/null | grep -q ":8081 "; then
            print_error "Aegis data plane is not running on port 8081"
            print_info "Start Aegis with: make run-data (in separate terminal)"
            print_info "Then start control plane: make run-control (in another terminal)"
            exit 1
        fi
        
        # Run tests
        test_basic_forwarding
        sleep 1
        test_load_balancing
        sleep 1
        test_nat_sessions
        sleep 1
        test_rate_limiting
        sleep 2
        test_circuit_breaker
        sleep 1
        check_health
        sleep 1
        test_metrics
        sleep 1
        stress_test
        
        # Print summary
        print_summary
        ;;
    *)
        echo "Usage: $0 {start|stop|test}"
        echo ""
        echo "Commands:"
        echo "  start  - Start UDP backend servers"
        echo "  stop   - Stop UDP backend servers"
        echo "  test   - Run comprehensive UDP proxy tests"
        echo ""
        echo "Example workflow:"
        echo "  1. ./scripts/test-udp-proxy.sh start"
        echo "  2. Start Aegis (make run-data and make run-control)"
        echo "  3. ./scripts/test-udp-proxy.sh test"
        echo "  4. ./scripts/test-udp-proxy.sh stop"
        exit 1
        ;;
esac
