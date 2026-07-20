package health

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/lazzerex/aegis/control-plane/internal/config"
	"go.uber.org/zap"
)

type backendReloader interface {
	ReloadBackendsWithHealth(backends []config.Backend, healthState map[string]bool) error
}

type Checker struct {
	config      *config.Config
	grpcClient  backendReloader
	logger      *zap.Logger
	stopChan    chan struct{}
	wg          sync.WaitGroup
	healthState map[string]bool
	mu          sync.RWMutex
}

func NewChecker(cfg *config.Config, client backendReloader, logger *zap.Logger) *Checker {
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
		c.healthState[backend.Address] = true
		c.wg.Add(1)
		go c.checkBackend(backend)
	}
	for _, backend := range c.config.Proxy.UdpBackends {
		c.healthState[backend.Address] = true
		c.wg.Add(1)
		go c.checkUDPBackend(backend)
	}
}

func (c *Checker) Reload(cfg *config.Config) {
	close(c.stopChan)
	c.wg.Wait()

	c.mu.Lock()
	c.stopChan = make(chan struct{})
	c.config = cfg
	c.healthState = make(map[string]bool)
	c.mu.Unlock()

	for _, backend := range cfg.Proxy.Backends {
		c.healthState[backend.Address] = true
		c.wg.Add(1)
		go c.checkBackend(backend)
	}
	for _, backend := range cfg.Proxy.UdpBackends {
		c.healthState[backend.Address] = true
		c.wg.Add(1)
		go c.checkUDPBackend(backend)
	}
}

func (c *Checker) Stop() {
	close(c.stopChan)
	c.wg.Wait()
	c.logger.Info("Health checker stopped")
}

func (c *Checker) checkBackend(backend config.Backend) {
	defer c.wg.Done()

	interval := backend.HealthCheck.Interval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	timeout := backend.HealthCheck.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	client := &http.Client{
		Timeout: timeout,
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
	scheme := backend.HealthCheck.Scheme
	if scheme == "" {
		scheme = "http"
	}
	url := fmt.Sprintf("%s://%s%s", scheme, backend.Address, backend.HealthCheck.Path)
	if backend.HealthCheck.Path == "" {
		url = fmt.Sprintf("%s://%s/", scheme, backend.Address)
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

func (c *Checker) checkUDPBackend(backend config.Backend) {
	defer c.wg.Done()

	interval := backend.HealthCheck.Interval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-c.stopChan:
			return
		case <-ticker.C:
			healthy := c.performTCPProbe(backend)
			c.updateHealthState(backend.Address, healthy)
		}
	}
}

func (c *Checker) performTCPProbe(backend config.Backend) bool {
	conn, err := net.DialTimeout("tcp", backend.Address, backend.HealthCheck.Timeout)
	if err != nil {
		c.logger.Warn("UDP backend TCP probe failed",
			zap.String("backend", backend.Address),
			zap.Error(err))
		return false
	}
	conn.Close()
	return true
}

func (c *Checker) updateHealthState(address string, healthy bool) {
	c.mu.Lock()
	previousState := c.healthState[address]
	c.healthState[address] = healthy
	c.mu.Unlock()

	if previousState != healthy {
		c.logger.Info("Backend health state changed",
			zap.String("backend", address),
			zap.Bool("healthy", healthy))

		c.mu.RLock()
		backends := make([]config.Backend, 0, len(c.config.Proxy.Backends))
		for _, backend := range c.config.Proxy.Backends {
			backends = append(backends, backend)
		}
		healthState := make(map[string]bool, len(c.healthState))
		for k, v := range c.healthState {
			healthState[k] = v
		}
		c.mu.RUnlock()

		if err := c.grpcClient.ReloadBackendsWithHealth(backends, healthState); err != nil {
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
