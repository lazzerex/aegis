package main

import "testing"

const dataPlaneMetricsFixture = `# HELP proxy_tcp_active_connections Current active TCP connections
# TYPE proxy_tcp_active_connections gauge
proxy_tcp_active_connections 3
# HELP proxy_pool_hits_total Total backend connections served from the pre-warmed connection pool
# TYPE proxy_pool_hits_total counter
proxy_pool_hits_total 42
# HELP proxy_pool_misses_total Total backend connections that required a fresh dial (pool empty)
# TYPE proxy_pool_misses_total counter
proxy_pool_misses_total 8
# HELP proxy_rate_limit_allowed_total Total requests allowed by the rate limiter
# TYPE proxy_rate_limit_allowed_total counter
proxy_rate_limit_allowed_total 100
# HELP proxy_rate_limit_denied_total Total requests denied by the rate limiter
# TYPE proxy_rate_limit_denied_total counter
proxy_rate_limit_denied_total 5
# HELP proxy_circuit_breaker_open_total Total times a circuit breaker tripped open
# TYPE proxy_circuit_breaker_open_total counter
proxy_circuit_breaker_open_total 2
# HELP proxy_circuit_breaker_half_open_total Total times a circuit breaker moved to half-open
# TYPE proxy_circuit_breaker_half_open_total counter
proxy_circuit_breaker_half_open_total 1
# HELP proxy_latency_avg_ms Average backend connect latency in milliseconds
# TYPE proxy_latency_avg_ms gauge
proxy_latency_avg_ms 1.75
# HELP proxy_latency_p99_ms P99 backend connect latency in milliseconds
# TYPE proxy_latency_p99_ms gauge
proxy_latency_p99_ms 12.4
`

func TestParseDataPlaneMetrics(t *testing.T) {
	stats, err := parseDataPlaneMetrics([]byte(dataPlaneMetricsFixture))
	if err != nil {
		t.Fatalf("parseDataPlaneMetrics: %v", err)
	}

	want := DataPlaneStats{
		PoolHits:               42,
		PoolMisses:             8,
		RateLimitAllowed:       100,
		RateLimitDenied:        5,
		CircuitBreakerOpen:     2,
		CircuitBreakerHalfOpen: 1,
		LatencyAvgMs:           1.75,
		LatencyP99Ms:           12.4,
	}
	if stats != want {
		t.Errorf("got %+v, want %+v", stats, want)
	}
}

func TestParseDataPlaneMetrics_MissingMetricsDefaultToZero(t *testing.T) {
	stats, err := parseDataPlaneMetrics([]byte("# empty scrape\n"))
	if err != nil {
		t.Fatalf("parseDataPlaneMetrics: %v", err)
	}
	if stats != (DataPlaneStats{}) {
		t.Errorf("expected zero-value stats for an empty scrape, got %+v", stats)
	}
}

func TestParseDataPlaneMetrics_InvalidTextReturnsError(t *testing.T) {
	_, err := parseDataPlaneMetrics([]byte("not valid prometheus text{{{"))
	if err == nil {
		t.Error("expected an error for malformed metrics text")
	}
}
