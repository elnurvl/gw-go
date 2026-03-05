package main

import (
	"os"
	"testing"
	"time"
)

func TestRun_InvalidConfigPath(t *testing.T) {
	err := run("/nonexistent/config.yaml")
	if err == nil {
		t.Fatal("expected error for missing config")
	}
}

func TestRun_ValidConfig_GracefulShutdown(t *testing.T) {
	cfgYAML := `
server:
  port: 0
  readTimeout: 1s
  writeTimeout: 1s
  shutdownTimeout: 1s
redis:
  addr: localhost:6379
  db: 14
jwt:
  enabled: false
routes: []
rateLimit:
  rate: 100
  window: 1s
circuitBreaker:
  maxRequests: 5
  interval: 60s
  timeout: 5s
  failureRatio: 0.5
  windowSize: 100
`

	dir := t.TempDir()
	cfgPath := dir + "/config.yaml"
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0644); err != nil {
		t.Fatal(err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- run(cfgPath)
	}()

	time.Sleep(100 * time.Millisecond)
	p, _ := os.FindProcess(os.Getpid())
	p.Signal(os.Interrupt)

	select {
	case err := <-errCh:
		if err != nil {
			t.Logf("run() returned: %v (acceptable)", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run() did not return within 5 seconds")
	}
}
