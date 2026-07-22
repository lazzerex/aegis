package api

import (
	"context"
	_ "embed"
	"encoding/json"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/lazzerex/aegis/control-plane/internal/config"
	"github.com/lazzerex/aegis/control-plane/internal/metrics"
	"go.uber.org/zap"
)

//go:embed dashboard.html
var dashboardHTML []byte

type grpcBackendClient interface {
	UpdateConfig(cfg *config.Config) error
	ReloadBackendsWithHealth(backends []config.Backend, healthState map[string]bool) error
	DrainConnections(ctx context.Context, timeoutSeconds int) error
}

type healthStateTracker interface {
	GetHealthState() map[string]bool
	Reload(cfg *config.Config)
}

// circuitStateProvider is optional — a Server without one (e.g. in tests)
// just omits circuit_state and per-backend stats from backend responses.
type circuitStateProvider interface {
	BackendCircuitStates() map[string]string
	BackendStats() map[string]metrics.BackendStat
}

type Server struct {
	mu            sync.RWMutex
	config        *config.Config
	configPath    string
	grpcClient    grpcBackendClient
	healthChecker healthStateTracker
	circuitStates circuitStateProvider
	logger        *zap.Logger
	server        *http.Server
}

func NewServer(cfg *config.Config, configPath string, client grpcBackendClient, checker healthStateTracker, circuitStates circuitStateProvider, logger *zap.Logger) *Server {
	return &Server{
		config:        cfg,
		configPath:    configPath,
		grpcClient:    client,
		healthChecker: checker,
		circuitStates: circuitStates,
		logger:        logger,
	}
}

func (s *Server) Start(address string) error {
	r := chi.NewRouter()

	// Middleware
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.RequestID)

	// Routes
	r.Get("/health", s.handleHealth)
	r.Get("/status", s.handleStatus)
	r.Get("/backends", s.handleListBackends)
	r.Get("/dashboard", s.handleDashboard)
	r.With(s.requireToken).Post("/reload", s.handleReload)
	r.With(s.requireToken).Post("/drain", s.handleDrain)
	r.With(s.requireToken).Post("/backends", s.handleAddBackend)
	r.With(s.requireToken).Delete("/backends/{address:.+}", s.handleRemoveBackend)

	s.server = &http.Server{
		Addr:    address,
		Handler: r,
	}

	return s.server.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s.server != nil {
		return s.server.Shutdown(ctx)
	}
	return nil
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	healthState := s.healthChecker.GetHealthState()

	response := map[string]interface{}{
		"status":   "ok",
		"backends": healthState,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	response := map[string]interface{}{
		"version": "0.1.0",
		"config": map[string]interface{}{
			"backends":             len(s.config.Proxy.Backends),
			"algorithm":            s.config.Proxy.LoadBalancing.Algorithm,
			"session_affinity":     s.config.Proxy.LoadBalancing.SessionAffinity,
			"rate_limit_rps":       s.config.Proxy.Traffic.RateLimit.RequestsPerSecond,
			"rate_limit_burst":     s.config.Proxy.Traffic.RateLimit.Burst,
			"cb_threshold":         s.config.Proxy.CircuitBreaker.ErrorThreshold,
			"cb_timeout_secs":      s.config.Proxy.CircuitBreaker.Timeout.Seconds(),
			"connect_timeout_secs": s.config.Proxy.Traffic.Timeout.Connect.Seconds(),
			"idle_timeout_secs":    s.config.Proxy.Traffic.Timeout.Idle.Seconds(),
			"read_timeout_secs":    s.config.Proxy.Traffic.Timeout.Read.Seconds(),
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (s *Server) handleReload(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.Load(s.configPath)
	if err != nil {
		s.logger.Error("Failed to reload config", zap.Error(err))
		http.Error(w, "Failed to reload configuration", http.StatusInternalServerError)
		return
	}

	if err := s.grpcClient.UpdateConfig(cfg); err != nil {
		s.logger.Error("Failed to update data plane config", zap.Error(err))
		http.Error(w, "Failed to update data plane", http.StatusInternalServerError)
		return
	}

	s.healthChecker.Reload(cfg)

	s.mu.Lock()
	s.config = cfg
	s.mu.Unlock()

	response := map[string]interface{}{
		"status":  "reloaded",
		"message": "Configuration reloaded successfully",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (s *Server) handleListBackends(w http.ResponseWriter, r *http.Request) {
	healthState := s.healthChecker.GetHealthState()

	var circuitStates map[string]string
	var backendStats map[string]metrics.BackendStat
	if s.circuitStates != nil {
		circuitStates = s.circuitStates.BackendCircuitStates()
		backendStats = s.circuitStates.BackendStats()
	}

	s.mu.RLock()
	backends := buildBackendEntries(s.config.Proxy.Backends, healthState, circuitStates, backendStats)
	udpBackends := buildBackendEntries(s.config.Proxy.UdpBackends, healthState, circuitStates, backendStats)
	s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"backends":     backends,
		"udp_backends": udpBackends,
	})
}

func buildBackendEntries(backends []config.Backend, healthState map[string]bool, circuitStates map[string]string, backendStats map[string]metrics.BackendStat) []map[string]interface{} {
	entries := make([]map[string]interface{}, len(backends))
	for i, b := range backends {
		entry := map[string]interface{}{
			"address": b.Address,
			"weight":  b.Weight,
			"healthy": healthState[b.Address],
		}
		if state, ok := circuitStates[b.Address]; ok {
			entry["circuit_state"] = state
		}
		if stat, ok := backendStats[b.Address]; ok {
			entry["active_connections"] = stat.ActiveConnections
			entry["total_requests"] = stat.TotalRequests
			entry["failed_requests"] = stat.FailedRequests
			entry["avg_latency_ms"] = stat.AvgLatencyMs
		}
		entries[i] = entry
	}
	return entries
}

// handleDashboard serves a lightweight, read-only static page (no auth,
// same as /health and /backends) that polls the existing JSON endpoints
// client-side. Not a management UI — no write actions are exposed here.
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(dashboardHTML)
}

func (s *Server) handleAddBackend(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Address string `json:"address"`
		Weight  int    `json:"weight"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Address == "" {
		http.Error(w, "Invalid request: address required", http.StatusBadRequest)
		return
	}
	if req.Weight <= 0 {
		req.Weight = 100
	}

	s.mu.Lock()
	for _, b := range s.config.Proxy.Backends {
		if b.Address == req.Address {
			s.mu.Unlock()
			http.Error(w, "Backend already exists", http.StatusConflict)
			return
		}
	}
	newBackend := config.Backend{
		Address: req.Address,
		Weight:  req.Weight,
		HealthCheck: config.HealthCheckConfig{
			Interval: 5 * time.Second,
			Timeout:  2 * time.Second,
		},
	}
	s.config.Proxy.Backends = append(s.config.Proxy.Backends, newBackend)
	backends := s.config.Proxy.Backends
	s.mu.Unlock()

	healthState := s.healthChecker.GetHealthState()
	if err := s.grpcClient.ReloadBackendsWithHealth(backends, healthState); err != nil {
		s.logger.Error("Failed to push new backend to data plane", zap.Error(err))
		s.mu.Lock()
		s.config.Proxy.Backends = s.config.Proxy.Backends[:len(s.config.Proxy.Backends)-1]
		s.mu.Unlock()
		http.Error(w, "Failed to update data plane", http.StatusInternalServerError)
		return
	}

	s.healthChecker.Reload(s.config)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "added",
		"address": req.Address,
		"weight":  req.Weight,
	})
}

func (s *Server) handleRemoveBackend(w http.ResponseWriter, r *http.Request) {
	address, err := url.PathUnescape(chi.URLParam(r, "address"))
	if err != nil || address == "" {
		http.Error(w, "Invalid address", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	found := false
	original := make([]config.Backend, len(s.config.Proxy.Backends))
	copy(original, s.config.Proxy.Backends)
	filtered := make([]config.Backend, 0, len(s.config.Proxy.Backends))
	for _, b := range s.config.Proxy.Backends {
		if b.Address == address {
			found = true
		} else {
			filtered = append(filtered, b)
		}
	}
	if !found {
		s.mu.Unlock()
		http.Error(w, "Backend not found", http.StatusNotFound)
		return
	}
	s.config.Proxy.Backends = filtered
	backends := filtered
	s.mu.Unlock()

	healthState := s.healthChecker.GetHealthState()
	if err := s.grpcClient.ReloadBackendsWithHealth(backends, healthState); err != nil {
		s.logger.Error("Failed to remove backend from data plane", zap.Error(err))
		s.mu.Lock()
		s.config.Proxy.Backends = original
		s.mu.Unlock()
		http.Error(w, "Failed to update data plane", http.StatusInternalServerError)
		return
	}

	s.healthChecker.Reload(s.config)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "removed",
		"address": address,
	})
}

func (s *Server) handleDrain(w http.ResponseWriter, r *http.Request) {
	if err := s.grpcClient.DrainConnections(r.Context(), 30); err != nil {
		s.logger.Error("Failed to drain connections", zap.Error(err))
		http.Error(w, "Failed to drain connections", http.StatusInternalServerError)
		return
	}

	response := map[string]interface{}{
		"status":  "drained",
		"message": "Connections drained successfully",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (s *Server) requireToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.config.Admin.APIToken == "" {
			next.ServeHTTP(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+s.config.Admin.APIToken {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
