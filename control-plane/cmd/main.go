package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lazzerex/aegis/control-plane/internal/api"
	"github.com/lazzerex/aegis/control-plane/internal/config"
	"github.com/lazzerex/aegis/control-plane/internal/grpc"
	"github.com/lazzerex/aegis/control-plane/internal/health"
	"github.com/lazzerex/aegis/control-plane/internal/metrics"
	"go.uber.org/zap"
)

var (
	configFile = flag.String("config", "config.yaml", "Path to configuration file")
)

func main() {
	flag.Parse()

	// Initialize logger
	logger, err := zap.NewProduction()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync()

	// Load configuration
	cfg, err := config.Load(*configFile)
	if err != nil {
		logger.Fatal("Failed to load configuration", zap.Error(err))
	}

	logger.Info("Starting proxy control plane",
		zap.String("config_file", *configFile),
		zap.String("version", "0.1.0"))

	// Initialize metrics
	metricsCollector := metrics.NewCollector()

	// Initialize gRPC client to Rust data plane
	grpcClient, err := grpc.NewClient(cfg.GRPC.ControlPlaneAddress, logger)
	if err != nil {
		logger.Fatal("Failed to create gRPC client", zap.Error(err))
	}
	defer grpcClient.Close()

	// Send initial configuration to data plane
	if err := grpcClient.UpdateConfig(cfg); err != nil {
		logger.Fatal("Failed to send initial config to data plane", zap.Error(err))
	}

	// Initialize health checker
	healthChecker := health.NewChecker(cfg, grpcClient, logger)
	healthChecker.Start()
	defer healthChecker.Stop()

	// Start metrics streaming from data plane
	go func() {
		if err := grpcClient.StreamMetrics(metricsCollector); err != nil {
			logger.Error("Metrics streaming error", zap.Error(err))
		}
	}()

	// Initialize REST API
	apiServer := api.NewServer(cfg, grpcClient, healthChecker, logger)
	
	// Start API server
	go func() {
		logger.Info("Starting admin API", zap.String("address", cfg.Admin.APIAddress))
		if err := apiServer.Start(cfg.Admin.APIAddress); err != nil && err != http.ErrServerClosed {
			logger.Fatal("Admin API server error", zap.Error(err))
		}
	}()

	// Start metrics server
	metricsServer := metrics.NewServer(metricsCollector)
	go func() {
		logger.Info("Starting metrics server", zap.String("address", cfg.Admin.MetricsAddress))
		if err := metricsServer.Start(cfg.Admin.MetricsAddress); err != nil && err != http.ErrServerClosed {
			logger.Fatal("Metrics server error", zap.Error(err))
		}
	}()

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	logger.Info("Shutting down gracefully...")

	// Graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Drain connections in data plane
	if err := grpcClient.DrainConnections(ctx, 30); err != nil {
		logger.Error("Failed to drain connections", zap.Error(err))
	}

	// Shutdown API servers
	if err := apiServer.Shutdown(ctx); err != nil {
		logger.Error("Error shutting down API server", zap.Error(err))
	}
	if err := metricsServer.Shutdown(ctx); err != nil {
		logger.Error("Error shutting down metrics server", zap.Error(err))
	}

	logger.Info("Shutdown complete")
}
