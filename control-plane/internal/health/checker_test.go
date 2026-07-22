package health

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lazzerex/aegis/control-plane/internal/config"
	"go.uber.org/zap"
)

type mockReloader struct {
	callCount atomic.Int64
}

func (m *mockReloader) ReloadBackendsWithHealth(_ []config.Backend, _ map[string]bool) error {
	m.callCount.Add(1)
	return nil
}

func newTestChecker(reloader backendReloader) *Checker {
	cfg := &config.Config{
		Proxy: config.ProxyConfig{
			Backends: []config.Backend{
				{Address: "localhost:3000", Weight: 100, HealthCheck: config.HealthCheckConfig{Interval: 5 * time.Second, Timeout: 2 * time.Second}},
			},
		},
	}
	return &Checker{
		config:      cfg,
		grpcClient:  reloader,
		logger:      zap.NewNop(),
		stopChan:    make(chan struct{}),
		healthState: map[string]bool{"localhost:3000": true},
	}
}

func TestGetHealthState_ReturnsCopy(t *testing.T) {
	c := newTestChecker(&mockReloader{})
	state := c.GetHealthState()

	if len(state) != 1 {
		t.Fatalf("expected 1 backend, got %d", len(state))
	}
	if !state["localhost:3000"] {
		t.Error("expected localhost:3000 healthy")
	}

	// Mutating returned map must not affect internal state
	state["localhost:3000"] = false
	internal := c.GetHealthState()
	if !internal["localhost:3000"] {
		t.Error("GetHealthState returned reference to internal map, not a copy")
	}
}

func TestUpdateHealthState_NoCallWhenStateUnchanged(t *testing.T) {
	mock := &mockReloader{}
	c := newTestChecker(mock)

	// State is already true; setting it to true again should not call reloader
	c.updateHealthState("localhost:3000", true)

	if mock.callCount.Load() != 0 {
		t.Errorf("ReloadBackendsWithHealth called %d times, want 0", mock.callCount.Load())
	}
}

func TestUpdateHealthState_CallsReloaderOnChange(t *testing.T) {
	mock := &mockReloader{}
	c := newTestChecker(mock)

	// State is true; flip to false — should call reloader
	c.updateHealthState("localhost:3000", false)

	if mock.callCount.Load() != 1 {
		t.Errorf("ReloadBackendsWithHealth called %d times, want 1", mock.callCount.Load())
	}

	// Flip back to true — another call
	c.updateHealthState("localhost:3000", true)

	if mock.callCount.Load() != 2 {
		t.Errorf("ReloadBackendsWithHealth called %d times, want 2", mock.callCount.Load())
	}
}

func TestPerformHealthCheck_HTTPScheme(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newTestChecker(&mockReloader{})
	backend := config.Backend{
		Address:     strings.TrimPrefix(srv.URL, "http://"),
		HealthCheck: config.HealthCheckConfig{Timeout: 2 * time.Second, Scheme: "http"},
	}

	if !c.performHealthCheck(srv.Client(), backend) {
		t.Error("expected health check to succeed against a plain HTTP server with scheme=http")
	}
}

func TestPerformHealthCheck_EmptySchemeDefaultsToHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newTestChecker(&mockReloader{})
	backend := config.Backend{
		Address:     strings.TrimPrefix(srv.URL, "http://"),
		HealthCheck: config.HealthCheckConfig{Timeout: 2 * time.Second},
	}

	if !c.performHealthCheck(srv.Client(), backend) {
		t.Error("expected empty scheme to default to http")
	}
}

func TestPerformHealthCheck_HTTPSScheme(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newTestChecker(&mockReloader{})
	address := strings.TrimPrefix(srv.URL, "https://")

	httpsBackend := config.Backend{
		Address:     address,
		HealthCheck: config.HealthCheckConfig{Timeout: 2 * time.Second, Scheme: "https"},
	}
	if !c.performHealthCheck(srv.Client(), httpsBackend) {
		t.Error("expected health check to succeed against a TLS server with scheme=https")
	}

	// proves scheme actually drives the request, not just defaulting to http
	httpBackend := config.Backend{
		Address:     address,
		HealthCheck: config.HealthCheckConfig{Timeout: 2 * time.Second, Scheme: "http"},
	}
	if c.performHealthCheck(srv.Client(), httpBackend) {
		t.Error("expected health check with scheme=http to fail against a TLS-only server")
	}
}

func TestPerformUDPProbe_HealthyWhenBackendResponds(t *testing.T) {
	addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	go func() {
		buf := make([]byte, 64)
		n, clientAddr, err := conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		conn.WriteToUDP(buf[:n], clientAddr)
	}()

	c := newTestChecker(&mockReloader{})
	backend := config.Backend{
		Address:     conn.LocalAddr().String(),
		HealthCheck: config.HealthCheckConfig{Timeout: 2 * time.Second},
	}

	if !c.performUDPProbe(backend) {
		t.Error("expected healthy against a UDP backend that echoes the probe")
	}
}

func TestPerformUDPProbe_UnhealthyWhenNoResponse(t *testing.T) {
	addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	c := newTestChecker(&mockReloader{})
	backend := config.Backend{
		Address:     conn.LocalAddr().String(),
		HealthCheck: config.HealthCheckConfig{Timeout: 200 * time.Millisecond},
	}

	if c.performUDPProbe(backend) {
		t.Error("expected unhealthy against a UDP socket that never responds")
	}
}

func TestReload_ResetsHealthState(t *testing.T) {
	mock := &mockReloader{}
	c := newTestChecker(mock)

	newCfg := &config.Config{
		Proxy: config.ProxyConfig{
			Backends: []config.Backend{
				{Address: "newhost:4000", Weight: 100, HealthCheck: config.HealthCheckConfig{Interval: 5 * time.Second, Timeout: 2 * time.Second}},
			},
		},
	}

	c.Reload(newCfg)
	defer c.Stop()

	state := c.GetHealthState()
	if _, ok := state["localhost:3000"]; ok {
		t.Error("old backend still in health state after Reload")
	}
	if _, ok := state["newhost:4000"]; !ok {
		t.Error("new backend missing from health state after Reload")
	}
}
