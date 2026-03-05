package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaults(t *testing.T) {
	cfg := defaults()

	if cfg.Server.Port != 80 {
		t.Errorf("default port = %d, want 80", cfg.Server.Port)
	}
	if cfg.Server.ReadTimeout != 30*time.Second {
		t.Errorf("default readTimeout = %v, want 30s", cfg.Server.ReadTimeout)
	}
	if cfg.Server.WriteTimeout != 30*time.Second {
		t.Errorf("default writeTimeout = %v, want 30s", cfg.Server.WriteTimeout)
	}
	if cfg.Server.ShutdownTimeout != 10*time.Second {
		t.Errorf("default shutdownTimeout = %v, want 10s", cfg.Server.ShutdownTimeout)
	}
	if cfg.Redis.Addr != "localhost:6379" {
		t.Errorf("default redis addr = %q, want localhost:6379", cfg.Redis.Addr)
	}
	if !cfg.JWT.Enabled {
		t.Error("default jwt.enabled = false, want true")
	}
	if len(cfg.JWT.ValidMethods) != 1 || cfg.JWT.ValidMethods[0] != "RS256" {
		t.Errorf("default jwt.validMethods = %v, want [RS256]", cfg.JWT.ValidMethods)
	}
	if cfg.JWT.RevokedTokenPrefix != "token:revoked:" {
		t.Errorf("default jwt.revokedTokenPrefix = %q, want token:revoked:", cfg.JWT.RevokedTokenPrefix)
	}
	if cfg.RateLimit.Rate != 100 {
		t.Errorf("default rate = %d, want 100", cfg.RateLimit.Rate)
	}
	if cfg.RateLimit.Window != time.Second {
		t.Errorf("default window = %v, want 1s", cfg.RateLimit.Window)
	}
	if cfg.RateLimit.KeyPrefix != "ratelimit:" {
		t.Errorf("default rateLimit.keyPrefix = %q, want ratelimit:", cfg.RateLimit.KeyPrefix)
	}
	if len(cfg.RateLimit.KeyHeaders) != 2 {
		t.Errorf("default rateLimit.keyHeaders len = %d, want 2", len(cfg.RateLimit.KeyHeaders))
	}
	if cfg.CircuitBreaker.MaxRequests != 5 {
		t.Errorf("default maxRequests = %d, want 5", cfg.CircuitBreaker.MaxRequests)
	}
	if cfg.CircuitBreaker.Interval != 60*time.Second {
		t.Errorf("default interval = %v, want 60s", cfg.CircuitBreaker.Interval)
	}
	if cfg.CircuitBreaker.Timeout != 5*time.Second {
		t.Errorf("default timeout = %v, want 5s", cfg.CircuitBreaker.Timeout)
	}
	if cfg.CircuitBreaker.FailureRatio != 0.5 {
		t.Errorf("default failureRatio = %f, want 0.5", cfg.CircuitBreaker.FailureRatio)
	}
	if cfg.CircuitBreaker.WindowSize != 100 {
		t.Errorf("default windowSize = %d, want 100", cfg.CircuitBreaker.WindowSize)
	}
}

func TestLoad(t *testing.T) {
	yaml := `
server:
  port: 8080
  readTimeout: 10s
  writeTimeout: 15s
  shutdownTimeout: 5s

redis:
  addr: redis:6380
  password: secret
  db: 2

jwt:
  enabled: false
  authUrl: http://auth:8080
  issuer: test-issuer
  audience: test-audience

routes:
  - id: svc-a
    pathPrefix: /api/a
    upstream: http://svc-a:8000
    stripPrefix: 1

rateLimit:
  rate: 50
  window: 2s

circuitBreaker:
  maxRequests: 10
  interval: 30s
  timeout: 10s
  failureRatio: 0.6
  windowSize: 200

bypassPaths:
  - /health
  - /public
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Server.Port != 8080 {
		t.Errorf("port = %d, want 8080", cfg.Server.Port)
	}
	if cfg.Server.ReadTimeout != 10*time.Second {
		t.Errorf("readTimeout = %v, want 10s", cfg.Server.ReadTimeout)
	}
	if cfg.Server.WriteTimeout != 15*time.Second {
		t.Errorf("writeTimeout = %v, want 15s", cfg.Server.WriteTimeout)
	}
	if cfg.Server.ShutdownTimeout != 5*time.Second {
		t.Errorf("shutdownTimeout = %v, want 5s", cfg.Server.ShutdownTimeout)
	}
	if cfg.Redis.Addr != "redis:6380" {
		t.Errorf("redis addr = %q, want redis:6380", cfg.Redis.Addr)
	}
	if cfg.Redis.Password != "secret" {
		t.Errorf("redis password = %q, want secret", cfg.Redis.Password)
	}
	if cfg.Redis.DB != 2 {
		t.Errorf("redis db = %d, want 2", cfg.Redis.DB)
	}
	if cfg.JWT.Enabled {
		t.Error("jwt.enabled = true, want false")
	}
	if cfg.JWT.AuthURL != "http://auth:8080" {
		t.Errorf("jwt.authUrl = %q, want http://auth:8080", cfg.JWT.AuthURL)
	}
	if cfg.JWT.Issuer != "test-issuer" {
		t.Errorf("jwt.issuer = %q, want test-issuer", cfg.JWT.Issuer)
	}
	if cfg.JWT.Audience != "test-audience" {
		t.Errorf("jwt.audience = %q, want test-audience", cfg.JWT.Audience)
	}
	if len(cfg.Routes) != 1 {
		t.Fatalf("routes len = %d, want 1", len(cfg.Routes))
	}
	r := cfg.Routes[0]
	if r.ID != "svc-a" || r.PathPrefix != "/api/a" || r.Upstream != "http://svc-a:8000" || r.StripPrefix != 1 {
		t.Errorf("route = %+v", r)
	}
	if cfg.RateLimit.Rate != 50 {
		t.Errorf("rate = %d, want 50", cfg.RateLimit.Rate)
	}
	if cfg.RateLimit.Window != 2*time.Second {
		t.Errorf("window = %v, want 2s", cfg.RateLimit.Window)
	}
	if cfg.CircuitBreaker.MaxRequests != 10 {
		t.Errorf("maxRequests = %d, want 10", cfg.CircuitBreaker.MaxRequests)
	}
	if cfg.CircuitBreaker.FailureRatio != 0.6 {
		t.Errorf("failureRatio = %f, want 0.6", cfg.CircuitBreaker.FailureRatio)
	}
	if cfg.CircuitBreaker.WindowSize != 200 {
		t.Errorf("windowSize = %d, want 200", cfg.CircuitBreaker.WindowSize)
	}
	if len(cfg.BypassPaths) != 2 || cfg.BypassPaths[0] != "/health" || cfg.BypassPaths[1] != "/public" {
		t.Errorf("bypassPaths = %v", cfg.BypassPaths)
	}
}

func TestLoad_DefaultsAppliedForMissingFields(t *testing.T) {
	yaml := `
server:
  port: 7777
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Server.Port != 7777 {
		t.Errorf("port = %d, want 7777", cfg.Server.Port)
	}
	// Defaults should still be present for unset fields
	if cfg.Server.ReadTimeout != 30*time.Second {
		t.Errorf("readTimeout = %v, want 30s (default)", cfg.Server.ReadTimeout)
	}
	if cfg.RateLimit.Rate != 100 {
		t.Errorf("rate = %d, want 100 (default)", cfg.RateLimit.Rate)
	}
	if cfg.Redis.Addr != "localhost:6379" {
		t.Errorf("redis addr = %q, want localhost:6379 (default)", cfg.Redis.Addr)
	}
}

func TestLoad_FileNotFound(t *testing.T) {
	_, err := Load("/nonexistent/path/config.yaml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(path, []byte("{{invalid yaml"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestLoad_EnvExpansion(t *testing.T) {
	dir := t.TempDir()

	env := "REDIS_ADDR=redis:6380\nAUTH_URL=http://auth:8080\n"
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(env), 0644); err != nil {
		t.Fatal(err)
	}

	yaml := `
redis:
  addr: ${REDIS_ADDR}
jwt:
  authUrl: ${AUTH_URL}
`
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Redis.Addr != "redis:6380" {
		t.Errorf("redis addr = %q, want redis:6380", cfg.Redis.Addr)
	}
	if cfg.JWT.AuthURL != "http://auth:8080" {
		t.Errorf("jwt.authUrl = %q, want http://auth:8080", cfg.JWT.AuthURL)
	}
}

func TestLoad_EnvExpansion_MissingVar(t *testing.T) {
	dir := t.TempDir()

	yaml := `
redis:
  addr: ${UNDEFINED_TEST_VAR_12345}
`
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	os.Unsetenv("UNDEFINED_TEST_VAR_12345")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	// Undefined vars expand to empty string; default should apply
	if cfg.Redis.Addr != "localhost:6379" {
		t.Errorf("redis addr = %q, want localhost:6379 (default)", cfg.Redis.Addr)
	}
}

func TestLoad_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.yaml")
	if err := os.WriteFile(path, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	// All defaults should be applied
	if cfg.Server.Port != 80 {
		t.Errorf("port = %d, want 80 (default)", cfg.Server.Port)
	}
}
