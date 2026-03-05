package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/go-redis/redis_rate/v10"

	"gw-go/config"
)

var defaultKeyHeaders = []config.KeyHeader{
	{Header: "X-DEVICE-ID", Prefix: "device"},
	{Header: "USERNAME", Prefix: "user"},
}

// mockRateLimitChecker implements RateLimitChecker for unit tests.
type mockRateLimitChecker struct {
	mu       sync.Mutex
	counters map[string]int
	err      error
}

func newMockChecker() *mockRateLimitChecker {
	return &mockRateLimitChecker{counters: make(map[string]int)}
}

func errMockChecker() *mockRateLimitChecker {
	return &mockRateLimitChecker{
		counters: make(map[string]int),
		err:      errors.New("redis: connection refused"),
	}
}

func (m *mockRateLimitChecker) Allow(_ context.Context, key string, limit redis_rate.Limit) (*redis_rate.Result, error) {
	if m.err != nil {
		return nil, m.err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	m.counters[key]++
	remaining := limit.Rate - m.counters[key]
	if remaining < 0 {
		return &redis_rate.Result{
			Allowed:    0,
			Remaining:  0,
			RetryAfter: limit.Period,
		}, nil
	}
	return &redis_rate.Result{
		Allowed:    1,
		Remaining:  remaining,
		RetryAfter: -1,
	}, nil
}

func TestResolveKey_DeviceID(t *testing.T) {
	rl := &RateLimiter{keyHeaders: defaultKeyHeaders}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-DEVICE-ID", "dev-abc")
	r.Header.Set("USERNAME", "user1")
	if got := rl.resolveKey(r); got != "device:dev-abc" {
		t.Errorf("resolveKey = %q, want device:dev-abc", got)
	}
}

func TestResolveKey_Username(t *testing.T) {
	rl := &RateLimiter{keyHeaders: defaultKeyHeaders}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("USERNAME", "user1")
	if got := rl.resolveKey(r); got != "user:user1" {
		t.Errorf("resolveKey = %q, want user:user1", got)
	}
}

func TestResolveKey_IP(t *testing.T) {
	rl := &RateLimiter{keyHeaders: defaultKeyHeaders}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.1:1234"
	if got := rl.resolveKey(r); got != "ip:10.0.0.1" {
		t.Errorf("resolveKey = %q, want ip:10.0.0.1", got)
	}
}

func TestResolveKey_XForwardedFor(t *testing.T) {
	rl := &RateLimiter{keyHeaders: defaultKeyHeaders}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Forwarded-For", "203.0.113.1")
	if got := rl.resolveKey(r); got != "ip:203.0.113.1" {
		t.Errorf("resolveKey = %q, want ip:203.0.113.1", got)
	}
}

func TestNewRateLimiter(t *testing.T) {
	mock := newMockChecker()
	cfg := config.RateLimit{
		Rate:       50,
		Window:     2 * time.Second,
		KeyPrefix:  "ratelimit:",
		KeyHeaders: defaultKeyHeaders,
	}
	rl := newRateLimiterWithChecker(mock, cfg)
	if rl.limit.Rate != 50 {
		t.Errorf("limit.Rate = %d, want 50", rl.limit.Rate)
	}
	if rl.limit.Burst != 50 {
		t.Errorf("limit.Burst = %d, want 50", rl.limit.Burst)
	}
	if rl.limit.Period != 2*time.Second {
		t.Errorf("limit.Period = %v, want 2s", rl.limit.Period)
	}
	if rl.keyPrefix != "ratelimit:" {
		t.Errorf("keyPrefix = %q, want ratelimit:", rl.keyPrefix)
	}
	if len(rl.keyHeaders) != 2 {
		t.Errorf("keyHeaders len = %d, want 2", len(rl.keyHeaders))
	}
}

func TestRateLimiter_AllowsWithinLimit(t *testing.T) {
	mock := newMockChecker()
	cfg := config.RateLimit{Rate: 5, Window: 10 * time.Second, KeyPrefix: "ratelimit:", KeyHeaders: defaultKeyHeaders}
	rl := newRateLimiterWithChecker(mock, cfg)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := rl.Middleware(inner)

	for i := 0; i < 5; i++ {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("X-DEVICE-ID", "test-allow-device")
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Errorf("request %d: status = %d, want 200", i+1, w.Code)
		}
	}
}

func TestRateLimiter_BlocksOverLimit(t *testing.T) {
	mock := newMockChecker()
	cfg := config.RateLimit{Rate: 3, Window: 10 * time.Second, KeyPrefix: "ratelimit:", KeyHeaders: defaultKeyHeaders}
	rl := newRateLimiterWithChecker(mock, cfg)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := rl.Middleware(inner)

	deviceID := "test-block-device"
	for i := 0; i < 3; i++ {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("X-DEVICE-ID", deviceID)
		handler.ServeHTTP(w, r)
	}

	// 4th request should be blocked
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-DEVICE-ID", deviceID)
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429", w.Code)
	}
	if ra := w.Header().Get("Retry-After"); ra == "" {
		t.Error("missing Retry-After header")
	}
	var body map[string]string
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["message"] != "rate limit exceeded" {
		t.Errorf("message = %q", body["message"])
	}
}

func TestRateLimiter_FailOpen_RedisDown(t *testing.T) {
	mock := errMockChecker()
	cfg := config.RateLimit{Rate: 1, Window: time.Second, KeyPrefix: "ratelimit:", KeyHeaders: defaultKeyHeaders}
	rl := newRateLimiterWithChecker(mock, cfg)

	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	handler := rl.Middleware(inner)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	handler.ServeHTTP(w, r)

	if !called {
		t.Error("handler should be called when redis is down (fail open)")
	}
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (fail open)", w.Code)
	}
}

func TestRateLimiter_DifferentKeys_IndependentLimits(t *testing.T) {
	mock := newMockChecker()
	cfg := config.RateLimit{Rate: 2, Window: 10 * time.Second, KeyPrefix: "ratelimit:", KeyHeaders: defaultKeyHeaders}
	rl := newRateLimiterWithChecker(mock, cfg)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := rl.Middleware(inner)

	// Exhaust limit for device-A
	for i := 0; i < 3; i++ {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("X-DEVICE-ID", "device-A")
		handler.ServeHTTP(w, r)
	}

	// device-B should still be allowed
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-DEVICE-ID", "device-B")
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("device-B status = %d, want 200 (independent limit)", w.Code)
	}
}
