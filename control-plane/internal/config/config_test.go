package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "aegis-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return f.Name()
}

const minimalConfig = `
proxy:
  listen:
    tcp: "0.0.0.0:8080"
    udp: "0.0.0.0:8081"
  backends:
    - address: "localhost:3000"
  load_balancing: {}
admin:
  api_address: "0.0.0.0:9090"
  metrics_address: "0.0.0.0:9091"
grpc:
  control_plane_address: "localhost:50051"
`

func TestLoad_DefaultsApplied(t *testing.T) {
	path := writeTempConfig(t, minimalConfig)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Proxy.LoadBalancing.Algorithm != "round_robin" {
		t.Errorf("algorithm default: got %q, want %q", cfg.Proxy.LoadBalancing.Algorithm, "round_robin")
	}
	b := cfg.Proxy.Backends[0]
	if b.Weight != 100 {
		t.Errorf("backend weight default: got %d, want 100", b.Weight)
	}
	if b.HealthCheck.Interval != 5*time.Second {
		t.Errorf("health interval default: got %v, want 5s", b.HealthCheck.Interval)
	}
	if b.HealthCheck.Timeout != 2*time.Second {
		t.Errorf("health timeout default: got %v, want 2s", b.HealthCheck.Timeout)
	}
	if b.HealthCheck.Scheme != "http" {
		t.Errorf("health scheme default: got %q, want %q", b.HealthCheck.Scheme, "http")
	}
}

func TestLoad_HealthCheckSchemeHTTPS(t *testing.T) {
	yaml := `
proxy:
  listen:
    tcp: "0.0.0.0:8080"
  backends:
    - address: "backend.example.com:443"
      health_check:
        scheme: "https"
  load_balancing: {}
admin:
  api_address: "0.0.0.0:9090"
  metrics_address: "0.0.0.0:9091"
grpc:
  control_plane_address: "localhost:50051"
`
	path := writeTempConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Proxy.Backends[0].HealthCheck.Scheme; got != "https" {
		t.Errorf("health scheme: got %q, want %q", got, "https")
	}
}

func TestLoad_InvalidHealthCheckSchemeRejected(t *testing.T) {
	yaml := `
proxy:
  listen:
    tcp: "0.0.0.0:8080"
  backends:
    - address: "backend.example.com:443"
      health_check:
        scheme: "ftp"
  load_balancing: {}
admin:
  api_address: "0.0.0.0:9090"
  metrics_address: "0.0.0.0:9091"
grpc:
  control_plane_address: "localhost:50051"
`
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid health_check.scheme, got nil")
	}
	if !strings.Contains(err.Error(), `health_check.scheme must be "http" or "https"`) {
		t.Errorf("error missing scheme complaint, got: %v", err)
	}
}

func TestLoad_UDPBackendsGetDefaults(t *testing.T) {
	yaml := `
proxy:
  listen:
    tcp: "0.0.0.0:8080"
    udp: "0.0.0.0:8081"
  backends: []
  udp_backends:
    - address: "localhost:5001"
  load_balancing: {}
admin:
  api_address: "0.0.0.0:9090"
  metrics_address: "0.0.0.0:9091"
grpc:
  control_plane_address: "localhost:50051"
`
	path := writeTempConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Proxy.UdpBackends) != 1 {
		t.Fatalf("expected 1 UDP backend, got %d", len(cfg.Proxy.UdpBackends))
	}
	u := cfg.Proxy.UdpBackends[0]
	if u.Weight != 100 {
		t.Errorf("UDP backend weight default: got %d, want 100", u.Weight)
	}
	if u.HealthCheck.Interval != 5*time.Second {
		t.Errorf("UDP health interval default: got %v, want 5s", u.HealthCheck.Interval)
	}
	if u.HealthCheck.Timeout != 2*time.Second {
		t.Errorf("UDP health timeout default: got %v, want 2s", u.HealthCheck.Timeout)
	}
}

func TestLoad_BadYAMLReturnsError(t *testing.T) {
	path := writeTempConfig(t, "not: valid: yaml: ][")
	_, err := Load(path)
	if err == nil {
		t.Error("expected error for bad YAML, got nil")
	}
}

func TestLoad_MissingFileReturnsError(t *testing.T) {
	_, err := Load("/nonexistent/path/config.yaml")
	if err == nil {
		t.Error("expected error for missing file, got nil")
	}
}

// Regression test: valid YAML that's semantically broken (missing required
// address, unknown algorithm, negative rate limit) used to load and apply
// without complaint. Load() must now reject it before it reaches the data
// plane.
func TestLoad_InvalidConfigRejected(t *testing.T) {
	yaml := `
proxy:
  listen:
    tcp: ""
  backends:
    - address: ""
    - address: "db1:5432"
    - address: "db1:5432"
  load_balancing:
    algorithm: "made_up_algorithm"
  traffic:
    rate_limit:
      requests_per_second: -1
admin:
  api_address: "0.0.0.0:9090"
  metrics_address: "0.0.0.0:9091"
grpc:
  control_plane_address: "localhost:50051"
`
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for semantically invalid config, got nil")
	}

	for _, want := range []string{
		"proxy.listen.tcp is required",
		"address is required",
		"duplicate backend address",
		`unknown algorithm "made_up_algorithm"`,
		"requests_per_second must be >= 0",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q, got: %v", want, err)
		}
	}
}

func TestLoad_ValidConfigAccepted(t *testing.T) {
	path := writeTempConfig(t, minimalConfig)
	if _, err := Load(path); err != nil {
		t.Fatalf("expected valid config to load, got: %v", err)
	}
}

const configWithToken = `
proxy:
  listen:
    tcp: "0.0.0.0:8080"
    udp: "0.0.0.0:8081"
  backends: []
  load_balancing: {}
admin:
  api_address: "0.0.0.0:9090"
  metrics_address: "0.0.0.0:9091"
  api_token: "from-file"
grpc:
  control_plane_address: "localhost:50051"
`

func TestLoad_EnvTokenOverridesConfig(t *testing.T) {
	path := writeTempConfig(t, configWithToken)

	t.Setenv("AEGIS_API_TOKEN", "from-env")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Admin.APIToken != "from-env" {
		t.Errorf("token: got %q, want %q", cfg.Admin.APIToken, "from-env")
	}
}

func TestLoad_EnvTokenEmpty_UsesConfigFile(t *testing.T) {
	path := writeTempConfig(t, configWithToken)

	t.Setenv("AEGIS_API_TOKEN", "")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Admin.APIToken != "from-file" {
		t.Errorf("token: got %q, want %q", cfg.Admin.APIToken, "from-file")
	}
}
