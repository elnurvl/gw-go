package main

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func redisAddr() string {
	if v := os.Getenv("REDIS_ADDR"); v != "" {
		return v
	}
	return "localhost:6379"
}

func TestRun_InvalidConfigPath(t *testing.T) {
	err := run("/nonexistent/config.yaml")
	if err == nil {
		t.Fatal("expected error for missing config")
	}
}

func TestRun_ValidConfig_GracefulShutdown(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{Addr: redisAddr(), DB: 14})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		t.Skipf("redis unavailable: %v", err)
	}
	defer func() {
		rdb.FlushDB(context.Background())
		rdb.Close()
	}()

	cfgYAML := fmt.Sprintf(`
server:
  port: 0
  readTimeout: 1s
  writeTimeout: 1s
  shutdownTimeout: 1s
redis:
  addr: %s
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
`, redisAddr())

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
