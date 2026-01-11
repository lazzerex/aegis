package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/lazzerex/aegis/control-plane/internal/config"
	"github.com/lazzerex/aegis/control-plane/internal/grpc"
	"github.com/lazzerex/aegis/control-plane/internal/health"
	"go.uber.org/zap"
)

type Server struct {
	config        *config.Config
	grpcClient    *grpc.Client
	healthChecker *health.Checker
	logger        *zap.Logger
	server        *http.Server
}

func NewServer(cfg *config.Config, client *grpc.Client, checker *health.Checker, logger *zap.Logger) *Server {
	return &Server{
		config:        cfg,
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
	r.Post("/reload", s.handleReload)
	r.Post("/drain", s.handleDrain)

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
			"backends": len(s.config.Proxy.Backends),
			"algorithm": s.config.Proxy.LoadBalancing.Algorithm,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (s *Server) handleReload(w http.ResponseWriter, r *http.Request) {
	// Reload configuration
	cfg, err := config.Load("config.yaml")
	if err != nil {
		s.logger.Error("Failed to reload config", zap.Error(err))
		http.Error(w, "Failed to reload configuration", http.StatusInternalServerError)
		return
	}

	// Update data plane
	if err := s.grpcClient.UpdateConfig(cfg); err != nil {
		s.logger.Error("Failed to update data plane config", zap.Error(err))
		http.Error(w, "Failed to update data plane", http.StatusInternalServerError)
		return
	}

	s.config = cfg

	response := map[string]interface{}{
		"status":  "reloaded",
		"message": "Configuration reloaded successfully",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
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
