package health

import (
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
