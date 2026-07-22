package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/lazzerex/aegis/control-plane/internal/config"
	"github.com/lazzerex/aegis/control-plane/internal/metrics"
	"go.uber.org/zap"
)

func setURLParam(ctx context.Context, key, value string) context.Context {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, value)
	return context.WithValue(ctx, chi.RouteCtxKey, rctx)
}

// ── mocks ────────────────────────────────────────────────────────────────────

type mockGRPC struct {
	updateErr   error
	reloadErr   error
	drainErr    error
	reloadCalls int
}

func (m *mockGRPC) UpdateConfig(_ *config.Config) error { return m.updateErr }
func (m *mockGRPC) ReloadBackendsWithHealth(_ []config.Backend, _ map[string]bool) error {
	m.reloadCalls++
	return m.reloadErr
}
func (m *mockGRPC) DrainConnections(_ context.Context, _ int) error { return m.drainErr }

type mockHealth struct {
	state       map[string]bool
	reloadCalls int
}

func (m *mockHealth) GetHealthState() map[string]bool { return m.state }
func (m *mockHealth) Reload(_ *config.Config)         { m.reloadCalls++ }

type mockCircuitStates struct {
	states map[string]string
	stats  map[string]metrics.BackendStat
}

func (m *mockCircuitStates) BackendCircuitStates() map[string]string      { return m.states }
func (m *mockCircuitStates) BackendStats() map[string]metrics.BackendStat { return m.stats }

// ── helpers ──────────────────────────────────────────────────────────────────

func testServer(grpc grpcBackendClient, health healthStateTracker, token string) *Server {
	cfg := &config.Config{
		Proxy: config.ProxyConfig{
			Backends: []config.Backend{
				{Address: "localhost:3000", Weight: 100},
				{Address: "localhost:3001", Weight: 50},
			},
		},
		Admin: config.AdminConfig{APIToken: token},
	}
	return &Server{
		config:        cfg,
		configPath:    "unused",
		grpcClient:    grpc,
		healthChecker: health,
		logger:        zap.NewNop(),
	}
}

func writeTempConfig(t *testing.T) string {
	t.Helper()
	content := `
proxy:
  listen:
    tcp: "0.0.0.0:8080"
    udp: "0.0.0.0:8081"
  backends: []
  load_balancing: {}
admin:
  api_address: "0.0.0.0:9090"
  metrics_address: "0.0.0.0:9091"
grpc:
  control_plane_address: "localhost:50051"
`
	f, err := os.CreateTemp(t.TempDir(), "aegis-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(content)
	f.Close()
	return f.Name()
}

// ── tests ────────────────────────────────────────────────────────────────────

func TestHandleHealth_ReturnsBackendStates(t *testing.T) {
	h := &mockHealth{state: map[string]bool{"localhost:3000": true, "localhost:3001": false}}
	s := testServer(&mockGRPC{}, h, "")

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	s.handleHealth(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}
	var resp map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["status"] != "ok" {
		t.Errorf("status field: got %v, want ok", resp["status"])
	}
	backends := resp["backends"].(map[string]interface{})
	if backends["localhost:3000"] != true {
		t.Error("localhost:3000 should be healthy")
	}
	if backends["localhost:3001"] != false {
		t.Error("localhost:3001 should be unhealthy")
	}
}

func TestHandleListBackends(t *testing.T) {
	h := &mockHealth{state: map[string]bool{"localhost:3000": true, "localhost:3001": false}}
	s := testServer(&mockGRPC{}, h, "")

	req := httptest.NewRequest(http.MethodGet, "/backends", nil)
	rec := httptest.NewRecorder()
	s.handleListBackends(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}
	var resp struct {
		Backends []struct {
			Address string `json:"address"`
			Weight  int    `json:"weight"`
			Healthy bool   `json:"healthy"`
		} `json:"backends"`
	}
	json.NewDecoder(rec.Body).Decode(&resp)
	if len(resp.Backends) != 2 {
		t.Fatalf("expected 2 backends, got %d", len(resp.Backends))
	}
}

func TestHandleStatus_IncludesSessionAffinity(t *testing.T) {
	s := testServer(&mockGRPC{}, &mockHealth{state: map[string]bool{}}, "")
	s.config.Proxy.LoadBalancing.SessionAffinity = true

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	rec := httptest.NewRecorder()
	s.handleStatus(rec, req)

	var resp struct {
		Config struct {
			SessionAffinity bool `json:"session_affinity"`
		} `json:"config"`
	}
	json.NewDecoder(rec.Body).Decode(&resp)
	if !resp.Config.SessionAffinity {
		t.Error("expected session_affinity: true in /status response")
	}
}

func TestHandleStatus_IncludesRateLimitAndCircuitBreakerConfig(t *testing.T) {
	s := testServer(&mockGRPC{}, &mockHealth{state: map[string]bool{}}, "")
	s.config.Proxy.Traffic.RateLimit.RequestsPerSecond = 1000
	s.config.Proxy.Traffic.RateLimit.Burst = 100
	s.config.Proxy.CircuitBreaker.ErrorThreshold = 5
	s.config.Proxy.CircuitBreaker.Timeout = 30 * time.Second

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	rec := httptest.NewRecorder()
	s.handleStatus(rec, req)

	var resp struct {
		Config struct {
			RateLimitRPS   int     `json:"rate_limit_rps"`
			RateLimitBurst int     `json:"rate_limit_burst"`
			CBThreshold    int     `json:"cb_threshold"`
			CBTimeoutSecs  float64 `json:"cb_timeout_secs"`
		} `json:"config"`
	}
	json.NewDecoder(rec.Body).Decode(&resp)

	if resp.Config.RateLimitRPS != 1000 {
		t.Errorf("rate_limit_rps: got %d, want 1000", resp.Config.RateLimitRPS)
	}
	if resp.Config.RateLimitBurst != 100 {
		t.Errorf("rate_limit_burst: got %d, want 100", resp.Config.RateLimitBurst)
	}
	if resp.Config.CBThreshold != 5 {
		t.Errorf("cb_threshold: got %d, want 5", resp.Config.CBThreshold)
	}
	if resp.Config.CBTimeoutSecs != 30 {
		t.Errorf("cb_timeout_secs: got %v, want 30", resp.Config.CBTimeoutSecs)
	}
}

func TestHandleListBackends_IncludesUDPBackends(t *testing.T) {
	h := &mockHealth{state: map[string]bool{
		"localhost:3000":    true,
		"localhost:3001":    false,
		"udp-backend1:5000": true,
	}}
	s := testServer(&mockGRPC{}, h, "")
	s.config.Proxy.UdpBackends = []config.Backend{
		{Address: "udp-backend1:5000", Weight: 100},
	}

	req := httptest.NewRequest(http.MethodGet, "/backends", nil)
	rec := httptest.NewRecorder()
	s.handleListBackends(rec, req)

	var resp map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&resp)

	backends := resp["backends"].([]interface{})
	if len(backends) != 2 {
		t.Fatalf("expected 2 TCP backends, got %d", len(backends))
	}

	udpBackends, ok := resp["udp_backends"].([]interface{})
	if !ok {
		t.Fatal("udp_backends missing from response")
	}
	if len(udpBackends) != 1 {
		t.Fatalf("expected 1 UDP backend, got %d", len(udpBackends))
	}
	entry := udpBackends[0].(map[string]interface{})
	if entry["address"] != "udp-backend1:5000" {
		t.Errorf("address: got %v, want udp-backend1:5000", entry["address"])
	}
	if entry["healthy"] != true {
		t.Errorf("healthy: got %v, want true", entry["healthy"])
	}
}

func TestHandleListBackends_OmitsStatsWhenProviderAbsent(t *testing.T) {
	h := &mockHealth{state: map[string]bool{"localhost:3000": true, "localhost:3001": false}}
	s := testServer(&mockGRPC{}, h, "")

	req := httptest.NewRequest(http.MethodGet, "/backends", nil)
	rec := httptest.NewRecorder()
	s.handleListBackends(rec, req)

	var resp map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&resp)
	backends := resp["backends"].([]interface{})
	first := backends[0].(map[string]interface{})
	if _, ok := first["circuit_state"]; ok {
		t.Error("circuit_state should be absent when no circuitStates provider is set")
	}
	if _, ok := first["total_requests"]; ok {
		t.Error("total_requests should be absent when no circuitStates provider is set")
	}
}

func TestHandleListBackends_IncludesStatsWhenProviderPresent(t *testing.T) {
	h := &mockHealth{state: map[string]bool{"localhost:3000": true, "localhost:3001": false}}
	s := testServer(&mockGRPC{}, h, "")
	s.circuitStates = &mockCircuitStates{
		states: map[string]string{"localhost:3000": "Closed"},
		stats: map[string]metrics.BackendStat{
			"localhost:3000": {ActiveConnections: 3, TotalRequests: 42, FailedRequests: 1, AvgLatencyMs: 2.5},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/backends", nil)
	rec := httptest.NewRecorder()
	s.handleListBackends(rec, req)

	var resp map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&resp)
	backends := resp["backends"].([]interface{})

	var found map[string]interface{}
	for _, b := range backends {
		entry := b.(map[string]interface{})
		if entry["address"] == "localhost:3000" {
			found = entry
		}
	}
	if found == nil {
		t.Fatal("localhost:3000 not found in response")
	}
	if found["circuit_state"] != "Closed" {
		t.Errorf("circuit_state: got %v, want Closed", found["circuit_state"])
	}
	if found["total_requests"] != float64(42) {
		t.Errorf("total_requests: got %v, want 42", found["total_requests"])
	}
	if found["failed_requests"] != float64(1) {
		t.Errorf("failed_requests: got %v, want 1", found["failed_requests"])
	}

	// localhost:3001 has no reported stats — must not appear at all.
	for _, b := range backends {
		entry := b.(map[string]interface{})
		if entry["address"] == "localhost:3001" {
			if _, ok := entry["total_requests"]; ok {
				t.Error("localhost:3001 should have no total_requests (never reported)")
			}
		}
	}
}

func TestHandleAddBackend_Success(t *testing.T) {
	g := &mockGRPC{}
	h := &mockHealth{state: map[string]bool{}}
	s := testServer(g, h, "")

	body := `{"address":"localhost:3002","weight":75}`
	req := httptest.NewRequest(http.MethodPost, "/backends", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	s.handleAddBackend(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201", rec.Code)
	}
	if g.reloadCalls != 1 {
		t.Errorf("ReloadBackendsWithHealth calls: got %d, want 1", g.reloadCalls)
	}
	if h.reloadCalls != 1 {
		t.Errorf("healthChecker.Reload calls: got %d, want 1", h.reloadCalls)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	found := false
	for _, b := range s.config.Proxy.Backends {
		if b.Address == "localhost:3002" && b.Weight == 75 {
			found = true
		}
	}
	if !found {
		t.Error("backend not added to config")
	}
}

func TestHandleAddBackend_Duplicate(t *testing.T) {
	s := testServer(&mockGRPC{}, &mockHealth{state: map[string]bool{}}, "")

	body := `{"address":"localhost:3000","weight":100}`
	req := httptest.NewRequest(http.MethodPost, "/backends", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	s.handleAddBackend(rec, req)

	if rec.Code != http.StatusConflict {
		t.Errorf("status: got %d, want 409", rec.Code)
	}
}

func TestHandleAddBackend_MissingAddress(t *testing.T) {
	s := testServer(&mockGRPC{}, &mockHealth{state: map[string]bool{}}, "")

	req := httptest.NewRequest(http.MethodPost, "/backends", bytes.NewBufferString(`{"weight":100}`))
	rec := httptest.NewRecorder()
	s.handleAddBackend(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
}

func TestHandleRemoveBackend_Success(t *testing.T) {
	g := &mockGRPC{}
	h := &mockHealth{state: map[string]bool{}}
	s := testServer(g, h, "")

	// Use chi context to supply URL param
	req := httptest.NewRequest(http.MethodDelete, "/backends/localhost:3000", nil)
	req = req.WithContext(setURLParam(req.Context(), "address", "localhost:3000"))
	rec := httptest.NewRecorder()
	s.handleRemoveBackend(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, b := range s.config.Proxy.Backends {
		if b.Address == "localhost:3000" {
			t.Error("backend still in config after removal")
		}
	}
}

func TestHandleRemoveBackend_NotFound(t *testing.T) {
	s := testServer(&mockGRPC{}, &mockHealth{state: map[string]bool{}}, "")

	req := httptest.NewRequest(http.MethodDelete, "/backends/nonexistent:9999", nil)
	req = req.WithContext(setURLParam(req.Context(), "address", "nonexistent:9999"))
	rec := httptest.NewRecorder()
	s.handleRemoveBackend(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", rec.Code)
	}
}

func TestHandleReload_UsesConfigPath(t *testing.T) {
	configPath := writeTempConfig(t)
	g := &mockGRPC{}
	h := &mockHealth{state: map[string]bool{}}
	s := testServer(g, h, "")
	s.configPath = configPath

	req := httptest.NewRequest(http.MethodPost, "/reload", nil)
	rec := httptest.NewRecorder()
	s.handleReload(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestHandleReload_WrongPathFails(t *testing.T) {
	g := &mockGRPC{}
	h := &mockHealth{state: map[string]bool{}}
	s := testServer(g, h, "")
	s.configPath = "/nonexistent/config.yaml"

	req := httptest.NewRequest(http.MethodPost, "/reload", nil)
	rec := httptest.NewRecorder()
	s.handleReload(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want 500", rec.Code)
	}
}

func TestRequireToken_AllowsWhenEmpty(t *testing.T) {
	s := testServer(&mockGRPC{}, &mockHealth{state: map[string]bool{}}, "")

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	rec := httptest.NewRecorder()
	s.requireToken(next).ServeHTTP(rec, req)

	if !called {
		t.Error("next handler not called when token is empty")
	}
}

func TestRequireToken_BlocksWrongToken(t *testing.T) {
	s := testServer(&mockGRPC{}, &mockHealth{state: map[string]bool{}}, "secret")

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	rec := httptest.NewRecorder()
	s.requireToken(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", rec.Code)
	}
}

func TestRequireToken_AllowsCorrectToken(t *testing.T) {
	s := testServer(&mockGRPC{}, &mockHealth{state: map[string]bool{}}, "secret")

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	s.requireToken(next).ServeHTTP(rec, req)

	if !called {
		t.Error("next handler not called with correct token")
	}
}
