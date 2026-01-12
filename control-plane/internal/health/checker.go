package health

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/lazzerex/aegis/control-plane/internal/config"
	"github.com/lazzerex/aegis/control-plane/internal/grpc"
	"go.uber.org/zap"
)

type Checker struct {
	config      *config.Config
	grpcClient  *grpc.Client
	logger      *zap.Logger
	stopChan    chan struct{}
	wg          sync.WaitGroup
	healthState map[string]bool
	mu          sync.RWMutex
}

func NewChecker(cfg *config.Config, client *grpc.Client, logger *zap.Logger) *Checker {
	return &Checker{
		config:      cfg,
		grpcClient:  client,
		logger:      logger,
		stopChan:    make(chan struct{}),
		healthState: make(map[string]bool),
	}
}

func (c *Checker) Start() {
	c.logger.Info("Starting health checker")

	for _, backend := range c.config.Proxy.Backends {
		c.healthState[backend.Address] = true // Assume healthy initially
		c.wg.Add(1)
		go c.checkBackend(backend)
	}
}

func (c *Checker) Stop() {
	close(c.stopChan)
	c.wg.Wait()
	c.logger.Info("Health checker stopped")
}

func (c *Checker) checkBackend(backend config.Backend) {
	defer c.wg.Done()

	ticker := time.NewTicker(backend.HealthCheck.Interval)
	defer ticker.Stop()

	client := &http.Client{
		Timeout: backend.HealthCheck.Timeout,
	}

	for {
		select {
		case <-c.stopChan:
			return
		case <-ticker.C:
			healthy := c.performHealthCheck(client, backend)
			c.updateHealthState(backend.Address, healthy)
		}
	}
}

func (c *Checker) performHealthCheck(client *http.Client, backend config.Backend) bool {
	url := fmt.Sprintf("http://%s%s", backend.Address, backend.HealthCheck.Path)
	if backend.HealthCheck.Path == "" {
		url = fmt.Sprintf("http://%s/", backend.Address)
	}

	ctx, cancel := context.WithTimeout(context.Background(), backend.HealthCheck.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		c.logger.Error("Failed to create health check request",
			zap.String("backend", backend.Address),
			zap.Error(err))
		return false
	}

	resp, err := client.Do(req)
	if err != nil {
		c.logger.Warn("Health check failed",
			zap.String("backend", backend.Address),
			zap.Error(err))
		return false
	}
	defer resp.Body.Close()

	healthy := resp.StatusCode >= 200 && resp.StatusCode < 300

	if !healthy {
		c.logger.Warn("Backend unhealthy",
			zap.String("backend", backend.Address),
			zap.Int("status_code", resp.StatusCode))
	}

	return healthy
}

func (c *Checker) updateHealthState(address string, healthy bool) {
	c.mu.Lock()
	previousState := c.healthState[address]
	c.healthState[address] = healthy
	c.mu.Unlock()

	// If state changed, update data plane
	if previousState != healthy {
		c.logger.Info("Backend health state changed",
			zap.String("backend", address),
			zap.Bool("healthy", healthy))

		// Reload backends with updated health state
		backends := make([]config.Backend, 0, len(c.config.Proxy.Backends))
		c.mu.RLock()
		for _, backend := range c.config.Proxy.Backends {
			// Copy backend and update health state
			backendCopy := backend
			if state, exists := c.healthState[backend.Address]; exists {
				// Health state is tracked separately and sent to data plane
				_ = state
			}
			backends = append(backends, backendCopy)
		}
		c.mu.RUnlock()

		if err := c.grpcClient.ReloadBackendsWithHealth(backends, c.healthState); err != nil {
			c.logger.Error("Failed to reload backends", zap.Error(err))
		}
	}
}

func (c *Checker) GetHealthState() map[string]bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	state := make(map[string]bool, len(c.healthState))
	for k, v := range c.healthState {
		state[k] = v
	}
	return state
}
