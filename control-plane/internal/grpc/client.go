package grpc

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/lazzerex/aegis/control-plane/internal/config"
	"github.com/lazzerex/aegis/control-plane/internal/metrics"
	pb "github.com/lazzerex/aegis/control-plane/proto"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Client struct {
	conn   *grpc.ClientConn
	client pb.ProxyControlClient
	logger *zap.Logger
}

func NewClient(address string, logger *zap.Logger) (*Client, error) {
	conn, err := grpc.Dial(address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
		grpc.WithTimeout(10*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to data plane: %w", err)
	}

	return &Client{
		conn:   conn,
		client: pb.NewProxyControlClient(conn),
		logger: logger,
	}, nil
}

func (c *Client) Close() error {
	return c.conn.Close()
}

func (c *Client) UpdateConfig(cfg *config.Config) error {
	// Convert config to protobuf
	pbConfig := &pb.ProxyConfig{
		Listen: &pb.ListenConfig{
			TcpAddress: cfg.Proxy.Listen.TCP,
			UdpAddress: cfg.Proxy.Listen.UDP,
		},
		Backends: make([]*pb.Backend, len(cfg.Proxy.Backends)),
		LoadBalancing: &pb.LoadBalancingConfig{
			Algorithm:       cfg.Proxy.LoadBalancing.Algorithm,
			SessionAffinity: cfg.Proxy.LoadBalancing.SessionAffinity,
		},
		Traffic: &pb.TrafficConfig{
			RateLimit: &pb.RateLimitConfig{
				RequestsPerSecond: int32(cfg.Proxy.Traffic.RateLimit.RequestsPerSecond),
				Burst:             int32(cfg.Proxy.Traffic.RateLimit.Burst),
			},
			Timeout: &pb.TimeoutConfig{
				ConnectSeconds: int32(cfg.Proxy.Traffic.Timeout.Connect.Seconds()),
				IdleSeconds:    int32(cfg.Proxy.Traffic.Timeout.Idle.Seconds()),
				ReadSeconds:    int32(cfg.Proxy.Traffic.Timeout.Read.Seconds()),
			},
		},
		CircuitBreaker: &pb.CircuitBreakerConfig{
			ErrorThreshold: int32(cfg.Proxy.CircuitBreaker.ErrorThreshold),
			TimeoutSeconds: int32(cfg.Proxy.CircuitBreaker.Timeout.Seconds()),
		},
	}

	// Convert backends
	for i, backend := range cfg.Proxy.Backends {
		pbConfig.Backends[i] = &pb.Backend{
			Address: backend.Address,
			Weight:  int32(backend.Weight),
			Healthy: true, // Initially all are healthy
			HealthCheck: &pb.HealthCheckConfig{
				IntervalSeconds: int32(backend.HealthCheck.Interval.Seconds()),
				TimeoutSeconds:  int32(backend.HealthCheck.Timeout.Seconds()),
				Path:            backend.HealthCheck.Path,
			},
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := c.client.UpdateConfig(ctx, pbConfig)
	if err != nil {
		return fmt.Errorf("failed to update config: %w", err)
	}

	if !resp.Success {
		return fmt.Errorf("config update failed: %s", resp.Message)
	}

	c.logger.Info("Configuration updated successfully", zap.String("message", resp.Message))
	return nil
}

func (c *Client) ReloadBackends(backends []config.Backend) error {
	pbBackends := make([]*pb.Backend, len(backends))
	for i, backend := range backends {
		pbBackends[i] = &pb.Backend{
			Address: backend.Address,
			Weight:  int32(backend.Weight),
			Healthy: true,
			HealthCheck: &pb.HealthCheckConfig{
				IntervalSeconds: int32(backend.HealthCheck.Interval.Seconds()),
				TimeoutSeconds:  int32(backend.HealthCheck.Timeout.Seconds()),
				Path:            backend.HealthCheck.Path,
			},
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := c.client.ReloadBackends(ctx, &pb.BackendList{Backends: pbBackends})
	if err != nil {
		return fmt.Errorf("failed to reload backends: %w", err)
	}

	if !resp.Success {
		return fmt.Errorf("backend reload failed: %s", resp.Message)
	}

	c.logger.Info("Backends reloaded", zap.Int32("count", resp.BackendsLoaded))
	return nil
}

func (c *Client) DrainConnections(ctx context.Context, timeoutSeconds int) error {
	resp, err := c.client.DrainConnections(ctx, &pb.DrainRequest{
		TimeoutSeconds: int32(timeoutSeconds),
	})
	if err != nil {
		return fmt.Errorf("failed to drain connections: %w", err)
	}

	c.logger.Info("Connections drained",
		zap.Bool("success", resp.Success),
		zap.Int32("count", resp.ConnectionsDrained))
	return nil
}

func (c *Client) StreamMetrics(collector *metrics.Collector) error {
	stream, err := c.client.StreamMetrics(context.Background())
	if err != nil {
		return fmt.Errorf("failed to start metrics stream: %w", err)
	}

	// Start receiving metrics
	go func() {
		for {
			ack, err := stream.Recv()
			if err == io.EOF {
				c.logger.Info("Metrics stream closed")
				return
			}
			if err != nil {
				c.logger.Error("Error receiving metrics ack", zap.Error(err))
				return
			}

			c.logger.Debug("Received metrics ack", zap.Bool("received", ack.Received))
		}
	}()

	// Start sending metrics requests (in a real implementation, collect actual metrics)
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			// In a real implementation, we would collect metrics from the data plane
			// For now, just send empty requests
			if err := stream.Send(&pb.MetricsData{}); err != nil {
				c.logger.Error("Error sending metrics request", zap.Error(err))
				return
			}
		}
	}()

	return nil
}
