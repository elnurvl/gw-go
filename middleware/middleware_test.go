package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/redis/go-redis/v9"
)

// --- mock Redis ---

type mockRedis struct {
	mu   sync.Mutex
	keys map[string]bool // existing keys for Exists checks
	err  error           // if set, all operations return this error
}

func newMockRedis() *mockRedis {
	return &mockRedis{
		keys: make(map[string]bool),
	}
}

func (m *mockRedis) Exists(ctx context.Context, keys ...string) *redis.IntCmd {
	cmd := redis.NewIntCmd(ctx)
	if m.err != nil {
		cmd.SetErr(m.err)
		return cmd
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var count int64
	for _, k := range keys {
		if m.keys[k] {
			count++
		}
	}
	cmd.SetVal(count)
	return cmd
}

// errMockRedis returns a mock where all operations fail.
func errMockRedis() *mockRedis {
	return &mockRedis{
		keys: make(map[string]bool),
		err:  errors.New("redis: connection refused"),
	}
}

// --- tests ---

func TestClientIP_CFConnectingIP(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("CF-Connecting-IP", "1.2.3.4")
	r.Header.Set("X-Forwarded-For", "5.6.7.8")
	if got := ClientIP(r); got != "1.2.3.4" {
		t.Errorf("ClientIP = %q, want 1.2.3.4", got)
	}
}

func TestClientIP_TrueClientIP(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("True-Client-IP", "10.0.0.1")
	if got := ClientIP(r); got != "10.0.0.1" {
		t.Errorf("ClientIP = %q, want 10.0.0.1", got)
	}
}

func TestClientIP_XForwardedFor(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Forwarded-For", "8.8.8.8, 10.0.0.1")
	if got := ClientIP(r); got != "8.8.8.8" {
		t.Errorf("ClientIP = %q, want 8.8.8.8 (first in chain)", got)
	}
}

func TestClientIP_XRealIP(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Real-IP", "172.16.0.1")
	if got := ClientIP(r); got != "172.16.0.1" {
		t.Errorf("ClientIP = %q, want 172.16.0.1", got)
	}
}

func TestClientIP_RemoteAddr(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "192.168.1.1:12345"
	if got := ClientIP(r); got != "192.168.1.1" {
		t.Errorf("ClientIP = %q, want 192.168.1.1", got)
	}
}

func TestClientIP_RemoteAddrNoPort(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "192.168.1.1"
	if got := ClientIP(r); got != "192.168.1.1" {
		t.Errorf("ClientIP = %q, want 192.168.1.1", got)
	}
}

func TestClientIP_HeaderPriority(t *testing.T) {
	// CF-Connecting-IP takes precedence over all others
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("CF-Connecting-IP", "1.1.1.1")
	r.Header.Set("True-Client-IP", "2.2.2.2")
	r.Header.Set("X-Forwarded-For", "3.3.3.3")
	r.Header.Set("X-Real-IP", "4.4.4.4")
	if got := ClientIP(r); got != "1.1.1.1" {
		t.Errorf("ClientIP = %q, want 1.1.1.1 (CF-Connecting-IP priority)", got)
	}
}

func TestWriteJSON(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, http.StatusTeapot, map[string]string{"hello": "world"})

	if w.Code != http.StatusTeapot {
		t.Errorf("status = %d, want %d", w.Code, http.StatusTeapot)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if body["hello"] != "world" {
		t.Errorf("body = %v", body)
	}
}

func TestErrBody(t *testing.T) {
	body := errBody("something went wrong")
	if body["message"] != "something went wrong" {
		t.Errorf("errBody message = %q", body["message"])
	}
	if len(body) != 1 {
		t.Errorf("errBody len = %d, want 1", len(body))
	}
}
