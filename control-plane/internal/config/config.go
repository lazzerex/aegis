package config

import (
	"fmt"
	"os"
	"strings"
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
	UdpBackends    []Backend            `yaml:"udp_backends"`
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
	APIToken       string `yaml:"api_token"`
}

type GRPCConfig struct {
	ControlPlaneAddress string `yaml:"control_plane_address"`
	TLSCACert           string `yaml:"tls_ca_cert"`
	TLSSkipVerify       bool   `yaml:"tls_skip_verify"`
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

	for i := range cfg.Proxy.UdpBackends {
		if cfg.Proxy.UdpBackends[i].Weight == 0 {
			cfg.Proxy.UdpBackends[i].Weight = 100
		}
		if cfg.Proxy.UdpBackends[i].HealthCheck.Interval == 0 {
			cfg.Proxy.UdpBackends[i].HealthCheck.Interval = 5 * time.Second
		}
		if cfg.Proxy.UdpBackends[i].HealthCheck.Timeout == 0 {
			cfg.Proxy.UdpBackends[i].HealthCheck.Timeout = 2 * time.Second
		}
	}

	if token := os.Getenv("AEGIS_API_TOKEN"); token != "" {
		cfg.Admin.APIToken = token
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// validAlgorithms mirrors data-plane/src/load_balancer.rs's Algorithm::from_str
// match arms — kept in sync manually since the two sides don't share types.
var validAlgorithms = map[string]bool{
	"round_robin":          true,
	"weighted_round_robin": true,
	"weighted":             true,
	"least_connections":    true,
	"consistent_hash":      true,
}

// Validate rejects a config that parsed as valid YAML but is semantically
// broken (missing required addresses, unknown algorithm, negative limits,
// duplicate backends) — called from Load() so a bad reload never reaches
// the data plane instead of applying partially.
func (c *Config) Validate() error {
	var errs []string

	if c.Proxy.Listen.TCP == "" {
		errs = append(errs, "proxy.listen.tcp is required")
	}
	if c.Admin.APIAddress == "" {
		errs = append(errs, "admin.api_address is required")
	}
	if c.Admin.MetricsAddress == "" {
		errs = append(errs, "admin.metrics_address is required")
	}
	if c.GRPC.ControlPlaneAddress == "" {
		errs = append(errs, "grpc.control_plane_address is required")
	}
	if !validAlgorithms[c.Proxy.LoadBalancing.Algorithm] {
		errs = append(errs, fmt.Sprintf("proxy.load_balancing.algorithm: unknown algorithm %q", c.Proxy.LoadBalancing.Algorithm))
	}
	if c.Proxy.Traffic.RateLimit.RequestsPerSecond < 0 {
		errs = append(errs, "proxy.traffic.rate_limit.requests_per_second must be >= 0")
	}
	if c.Proxy.Traffic.RateLimit.Burst < 0 {
		errs = append(errs, "proxy.traffic.rate_limit.burst must be >= 0")
	}
	if c.Proxy.CircuitBreaker.ErrorThreshold < 0 {
		errs = append(errs, "proxy.circuit_breaker.error_threshold must be >= 0")
	}

	errs = append(errs, validateBackends("proxy.backends", c.Proxy.Backends)...)
	errs = append(errs, validateBackends("proxy.udp_backends", c.Proxy.UdpBackends)...)

	if len(errs) > 0 {
		return fmt.Errorf("invalid configuration:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}

func validateBackends(field string, backends []Backend) []string {
	var errs []string
	seen := make(map[string]bool, len(backends))
	for i, b := range backends {
		if b.Address == "" {
			errs = append(errs, fmt.Sprintf("%s[%d].address is required", field, i))
			continue
		}
		if seen[b.Address] {
			errs = append(errs, fmt.Sprintf("%s[%d]: duplicate backend address %q", field, i, b.Address))
		}
		seen[b.Address] = true
		if b.Weight < 0 {
			errs = append(errs, fmt.Sprintf("%s[%d] (%s): weight must be >= 0", field, i, b.Address))
		}
	}
	return errs
}
