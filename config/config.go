package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server         Server         `yaml:"server"`
	Redis          Redis          `yaml:"redis"`
	JWT            JWT            `yaml:"jwt"`
	Routes         []Route        `yaml:"routes"`
	RateLimit      RateLimit      `yaml:"rateLimit"`
	CircuitBreaker CircuitBreaker `yaml:"circuitBreaker"`
	BypassPaths    []string       `yaml:"bypassPaths"`
}

type Server struct {
	Port            int           `yaml:"port"`
	ReadTimeout     time.Duration `yaml:"readTimeout"`
	WriteTimeout    time.Duration `yaml:"writeTimeout"`
	ShutdownTimeout time.Duration `yaml:"shutdownTimeout"`
}

type Redis struct {
	Addr     string `yaml:"addr"`
	Password string `yaml:"password"`
	DB       int    `yaml:"db"`
}

type JWT struct {
	Enabled            bool     `yaml:"enabled"`
	AuthURL            string   `yaml:"authUrl"`
	JwksPath           string   `yaml:"jwksPath"`
	Issuer             string   `yaml:"issuer"`
	Audience           string   `yaml:"audience"`
	ValidMethods       []string `yaml:"validMethods"`
	RevokedTokenPrefix string   `yaml:"revokedTokenPrefix"`
}

type Route struct {
	ID          string `yaml:"id"`
	PathPrefix  string `yaml:"pathPrefix"`
	Upstream    string `yaml:"upstream"`
	StripPrefix int    `yaml:"stripPrefix"`
}

type RateLimit struct {
	Rate       int           `yaml:"rate"`
	Window     time.Duration `yaml:"window"`
	KeyPrefix  string        `yaml:"keyPrefix"`
	KeyHeaders []KeyHeader   `yaml:"keyHeaders"`
}

type KeyHeader struct {
	Header string `yaml:"header"`
	Prefix string `yaml:"prefix"`
}

type CircuitBreaker struct {
	MaxRequests  uint32        `yaml:"maxRequests"`
	Interval     time.Duration `yaml:"interval"`
	Timeout      time.Duration `yaml:"timeout"`
	FailureRatio float64       `yaml:"failureRatio"`
	WindowSize   int           `yaml:"windowSize"`
}

func Load(path string) (*Config, error) {
	loadEnv(path)

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}

	data = expandVars(data)

	cfg := defaults()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	return cfg, nil
}

// loadEnv reads a .env file from the same directory as the config file
// and sets each KEY=VALUE pair as an environment variable.
// Missing .env or malformed lines are silently ignored.
func loadEnv(configPath string) {
	envPath := filepath.Join(filepath.Dir(configPath), ".env")
	f, err := os.Open(envPath)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		os.Setenv(strings.TrimSpace(key), strings.TrimSpace(value))
	}
}

// expandVars replaces ${VAR} placeholders in raw config bytes
// with values from the environment.
func expandVars(data []byte) []byte {
	return []byte(os.Expand(string(data), os.Getenv))
}

func defaults() *Config {
	return &Config{
		Server: Server{
			Port:            80,
			ReadTimeout:     30 * time.Second,
			WriteTimeout:    30 * time.Second,
			ShutdownTimeout: 10 * time.Second,
		},
		Redis: Redis{Addr: "localhost:6379"},
		JWT: JWT{
			Enabled:            true,
			ValidMethods:       []string{"RS256"},
			RevokedTokenPrefix: "token:revoked:",
		},
		RateLimit: RateLimit{
			Rate:      100,
			Window:    time.Second,
			KeyPrefix: "ratelimit:",
			KeyHeaders: []KeyHeader{
				{Header: "X-DEVICE-ID", Prefix: "device"},
				{Header: "USERNAME", Prefix: "user"},
			},
		},
		CircuitBreaker: CircuitBreaker{
			MaxRequests:  5,
			Interval:     60 * time.Second,
			Timeout:      5 * time.Second,
			FailureRatio: 0.5,
			WindowSize:   100,
		},
	}
}
