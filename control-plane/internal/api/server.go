package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/lazzerex/aegis/control-plane/internal/config"
	"go.uber.org/zap"
)

type grpcBackendClient interface {
	UpdateConfig(cfg *config.Config) error
	ReloadBackendsWithHealth(backends []config.Backend, healthState map[string]bool) error
	DrainConnections(ctx context.Context, timeoutSeconds int) error
}

type healthStateTracker interface {
	GetHealthState() map[string]bool
	Reload(cfg *config.Config)
}

type Server struct {
	mu            sync.RWMutex
	config        *config.Config
	configPath    string
	grpcClient    grpcBackendClient
	healthChecker healthStateTracker
	logger        *zap.Logger
	server        *http.Server
}

func NewServer(cfg *config.Config, configPath string, client grpcBackendClient, checker healthStateTracker, logger *zap.Logger) *Server {
	return &Server{
		config:        cfg,
		configPath:    configPath,
		grpcClient:    client,
		healthChecker: checker,
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
			"backends":  len(s.config.Proxy.Backends),
			"algorithm": s.config.Proxy.LoadBalancing.Algorithm,
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

	s.mu.RLock()
	backends := make([]map[string]interface{}, len(s.config.Proxy.Backends))
	for i, b := range s.config.Proxy.Backends {
		backends[i] = map[string]interface{}{
			"address": b.Address,
			"weight":  b.Weight,
			"healthy": healthState[b.Address],
		}
	}
	s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"backends": backends})
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
