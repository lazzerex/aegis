package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
)

type Backend struct {
	Address           string  `json:"address"`
	Weight            int     `json:"weight"`
	Healthy           bool    `json:"healthy"`
	CircuitState      string  `json:"circuit_state"`
	ActiveConnections int64   `json:"active_connections"`
	TotalRequests     int64   `json:"total_requests"`
	FailedRequests    int64   `json:"failed_requests"`
	AvgLatencyMs      float64 `json:"avg_latency_ms"`
}

type Status struct {
	Version string `json:"version"`
	Config  struct {
		Backends        int    `json:"backends"`
		Algorithm       string `json:"algorithm"`
		SessionAffinity bool   `json:"session_affinity"`
	} `json:"config"`
}

type DataPlaneStats struct {
	PoolHits               float64
	PoolMisses             float64
	RateLimitAllowed       float64
	RateLimitDenied        float64
	CircuitBreakerOpen     float64
	CircuitBreakerHalfOpen float64
	LatencyAvgMs           float64
	LatencyP99Ms           float64
}

func fetchStatus(ctx context.Context, client *http.Client, baseURL string) (Status, error) {
	var status Status
	if err := getJSON(ctx, client, baseURL+"/status", &status); err != nil {
		return Status{}, err
	}
	return status, nil
}

func fetchBackends(ctx context.Context, client *http.Client, baseURL string) ([]Backend, []Backend, error) {
	var resp struct {
		Backends    []Backend `json:"backends"`
		UDPBackends []Backend `json:"udp_backends"`
	}
	if err := getJSON(ctx, client, baseURL+"/backends", &resp); err != nil {
		return nil, nil, err
	}
	return resp.Backends, resp.UDPBackends, nil
}

func getJSON(ctx context.Context, client *http.Client, url string, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned %d", url, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func fetchDataPlaneMetrics(ctx context.Context, client *http.Client, url string) (DataPlaneStats, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return DataPlaneStats{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return DataPlaneStats{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return DataPlaneStats{}, fmt.Errorf("%s returned %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return DataPlaneStats{}, err
	}
	return parseDataPlaneMetrics(body)
}

func parseDataPlaneMetrics(body []byte) (DataPlaneStats, error) {
	var parser expfmt.TextParser
	families, err := parser.TextToMetricFamilies(bytes.NewReader(body))
	if err != nil {
		return DataPlaneStats{}, fmt.Errorf("parsing metrics text: %w", err)
	}

	return DataPlaneStats{
		PoolHits:               metricValue(families, "proxy_pool_hits_total"),
		PoolMisses:             metricValue(families, "proxy_pool_misses_total"),
		RateLimitAllowed:       metricValue(families, "proxy_rate_limit_allowed_total"),
		RateLimitDenied:        metricValue(families, "proxy_rate_limit_denied_total"),
		CircuitBreakerOpen:     metricValue(families, "proxy_circuit_breaker_open_total"),
		CircuitBreakerHalfOpen: metricValue(families, "proxy_circuit_breaker_half_open_total"),
		LatencyAvgMs:           metricValue(families, "proxy_latency_avg_ms"),
		LatencyP99Ms:           metricValue(families, "proxy_latency_p99_ms"),
	}, nil
}

func metricValue(families map[string]*dto.MetricFamily, name string) float64 {
	mf, ok := families[name]
	if !ok || len(mf.Metric) == 0 {
		return 0
	}
	m := mf.Metric[0]
	switch {
	case m.Counter != nil:
		return m.Counter.GetValue()
	case m.Gauge != nil:
		return m.Gauge.GetValue()
	default:
		return 0
	}
}
