package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Proxy ProxyConfig `yaml:"proxy"`
	Admin AdminConfig `yaml:"admin"`
	GRPC  GRPCConfig  `yaml:"grpc"`
}

type ProxyConfig struct {
	Listen         ListenConfig         `yaml:"listen"`
	Backends       []Backend            `yaml:"backends"`
	LoadBalancing  LoadBalancingConfig  `yaml:"load_balancing"`
	Traffic        TrafficConfig        `yaml:"traffic"`
	CircuitBreaker CircuitBreakerConfig `yaml:"circuit_breaker"`
}

type ListenConfig struct {
	TCP string `yaml:"tcp"`
	UDP string `yaml:"udp"`
}

type Backend struct {
	Address     string            `yaml:"address"`
	Weight      int               `yaml:"weight"`
	HealthCheck HealthCheckConfig `yaml:"health_check"`
}

type HealthCheckConfig struct {
	Interval time.Duration `yaml:"interval"`
	Timeout  time.Duration `yaml:"timeout"`
	Path     string        `yaml:"path"`
}

type LoadBalancingConfig struct {
	Algorithm       string `yaml:"algorithm"`
	SessionAffinity bool   `yaml:"session_affinity"`
}

type TrafficConfig struct {
	RateLimit RateLimitConfig `yaml:"rate_limit"`
	Timeout   TimeoutConfig   `yaml:"timeout"`
}

type RateLimitConfig struct {
	RequestsPerSecond int `yaml:"requests_per_second"`
	Burst             int `yaml:"burst"`
}

type TimeoutConfig struct {
	Connect time.Duration `yaml:"connect"`
	Idle    time.Duration `yaml:"idle"`
	Read    time.Duration `yaml:"read"`
}

type CircuitBreakerConfig struct {
	ErrorThreshold int           `yaml:"error_threshold"`
	Timeout        time.Duration `yaml:"timeout"`
}

type AdminConfig struct {
	APIAddress     string `yaml:"api_address"`
	MetricsAddress string `yaml:"metrics_address"`
}

type GRPCConfig struct {
	ControlPlaneAddress string `yaml:"control_plane_address"`
}

func Load(filename string) (*Config, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	// Set defaults
	if cfg.Proxy.LoadBalancing.Algorithm == "" {
		cfg.Proxy.LoadBalancing.Algorithm = "round_robin"
	}

	for i := range cfg.Proxy.Backends {
		if cfg.Proxy.Backends[i].Weight == 0 {
			cfg.Proxy.Backends[i].Weight = 100
		}
		if cfg.Proxy.Backends[i].HealthCheck.Interval == 0 {
			cfg.Proxy.Backends[i].HealthCheck.Interval = 5 * time.Second
		}
		if cfg.Proxy.Backends[i].HealthCheck.Timeout == 0 {
			cfg.Proxy.Backends[i].HealthCheck.Timeout = 2 * time.Second
		}
	}

	return &cfg, nil
}
