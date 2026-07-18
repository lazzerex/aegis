package grpc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/lazzerex/aegis/control-plane/internal/config"
	"github.com/lazzerex/aegis/control-plane/internal/metrics"
	pb "github.com/lazzerex/aegis/control-plane/proto"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"
)

type Client struct {
	conn   *grpc.ClientConn
	client pb.ProxyControlClient
	logger *zap.Logger
}

func NewClient(grpcCfg config.GRPCConfig, logger *zap.Logger) (*Client, error) {
	creds, err := buildTransportCredentials(grpcCfg, logger)
	if err != nil {
		return nil, err
	}

	conn, err := grpc.NewClient(grpcCfg.ControlPlaneAddress, grpc.WithTransportCredentials(creds))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to data plane: %w", err)
	}

	return &Client{
		conn:   conn,
		client: pb.NewProxyControlClient(conn),
		logger: logger,
	}, nil
}

func buildTransportCredentials(grpcCfg config.GRPCConfig, logger *zap.Logger) (credentials.TransportCredentials, error) {
	if grpcCfg.TLSSkipVerify {
		if grpcCfg.TLSCACert != "" {
			logger.Warn("grpc tls_skip_verify is set; tls_ca_cert will be ignored")
		}
		logger.Warn("gRPC TLS verification disabled (tls_skip_verify=true); do not use in production")
		return credentials.NewTLS(&tls.Config{ //nolint:gosec
			InsecureSkipVerify: true, //nolint:gosec
			MinVersion:         tls.VersionTLS12,
		}), nil
	}
	if grpcCfg.TLSCACert != "" {
		caCert, err := os.ReadFile(grpcCfg.TLSCACert)
		if err != nil {
			return nil, fmt.Errorf("failed to read gRPC CA cert %q: %w", grpcCfg.TLSCACert, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse gRPC CA cert %q", grpcCfg.TLSCACert)
		}
		logger.Info("gRPC TLS enabled", zap.String("ca_cert", grpcCfg.TLSCACert))
		return credentials.NewTLS(&tls.Config{
			RootCAs:    pool,
			MinVersion: tls.VersionTLS12,
		}), nil
	}
	logger.Info("gRPC running without TLS")
	return insecure.NewCredentials(), nil
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
		Backends:    make([]*pb.Backend, len(cfg.Proxy.Backends)),
		UdpBackends: make([]*pb.Backend, len(cfg.Proxy.UdpBackends)),
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

	// Convert UDP backends
	for i, backend := range cfg.Proxy.UdpBackends {
		pbConfig.UdpBackends[i] = &pb.Backend{
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
	return c.ReloadBackendsWithHealth(backends, nil)
}

func (c *Client) ReloadBackendsWithHealth(backends []config.Backend, healthState map[string]bool) error {
	pbBackends := make([]*pb.Backend, len(backends))
	for i, backend := range backends {
		healthy := true
		if healthState != nil {
			if state, exists := healthState[backend.Address]; exists {
				healthy = state
			}
		}

		pbBackends[i] = &pb.Backend{
			Address: backend.Address,
			Weight:  int32(backend.Weight),
			Healthy: healthy,
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

func (c *Client) StreamMetrics(collector *metrics.Collector) {
	go func() {
		for {
			stream, err := c.client.StreamMetrics(context.Background(), &emptypb.Empty{})
			if err != nil {
				c.logger.Error("Failed to start metrics stream, retrying in 5s", zap.Error(err))
				time.Sleep(5 * time.Second)
				continue
			}
			for {
				metricsData, err := stream.Recv()
				if err == io.EOF {
					c.logger.Info("Metrics stream closed, reconnecting")
					break
				}
				if err != nil {
					c.logger.Error("Metrics stream error, reconnecting in 5s", zap.Error(err))
					time.Sleep(5 * time.Second)
					break
				}
				collector.UpdateFromProto(metricsData)
			}
		}
	}()
}
