package proxy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"testing"
	"time"

	"github.com/sony/gobreaker/v2"

	"gw-go/config"
	"gw-go/middleware"
)

func testConfig(upstreamURL string) *config.Config {
	return &config.Config{
		Routes: []config.Route{
			{ID: "svc-a", PathPrefix: "/w/auth", Upstream: upstreamURL, StripPrefix: 1},
			{ID: "svc-b", PathPrefix: "/w/dict", Upstream: upstreamURL, StripPrefix: 1},
		},
		CircuitBreaker: config.CircuitBreaker{
			MaxRequests:  5,
			Interval:     60 * time.Second,
			Timeout:      1 * time.Second,
			FailureRatio: 0.5,
			WindowSize:   4,
		},
	}
}

func TestProxy_RouteMatching(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Got-Path", r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	p := New(testConfig(upstream.URL))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/w/auth/user-info", nil)
	p.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if got := w.Header().Get("X-Got-Path"); got != "/auth/user-info" {
		t.Errorf("upstream path = %q, want /auth/user-info", got)
	}
}

func TestProxy_StripPrefix(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		stripPrefix int
		wantPath    string
	}{
		{"strip 1 segment", "/w/auth/login", 1, "/auth/login"},
		{"strip 2 segments", "/api/v1/users", 2, "/users"},
		{"strip all", "/w/auth", 1, "/auth"},
		{"strip 0", "/w/auth/login", 0, "/w/auth/login"},
		{"strip more than segments", "/w", 2, "/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inReq := httptest.NewRequest(http.MethodGet, tt.path, nil)
			outReq := inReq.Clone(inReq.Context())
			pr := &httputil.ProxyRequest{In: inReq, Out: outReq}
			stripPrefix(pr, tt.stripPrefix)
			if pr.Out.URL.Path != tt.wantPath {
				t.Errorf("stripPrefix(%q, %d) = %q, want %q", tt.path, tt.stripPrefix, pr.Out.URL.Path, tt.wantPath)
			}
		})
	}
}

func TestProxy_NotFound(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("upstream should not be called")
	}))
	defer upstream.Close()

	p := New(testConfig(upstream.URL))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/unknown/path", nil)
	p.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestProxy_EnrichHeaders(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]string{
			"X-USER-ID":         r.Header.Get("X-USER-ID"),
			"X-USERNAME":        r.Header.Get("X-USERNAME"),
			"X-CUSTOMER-ID":     r.Header.Get("X-CUSTOMER-ID"),
			"X-CUSTOMER-TYPE":   r.Header.Get("X-CUSTOMER-TYPE"),
			"X-CIF":             r.Header.Get("X-CIF"),
			"X-TIN":             r.Header.Get("X-TIN"),
			"X-AUTH-TYPE":       r.Header.Get("X-AUTH-TYPE"),
			"X-SIGNATURE-LEVEL": r.Header.Get("X-SIGNATURE-LEVEL"),
			"X-PHONE":           r.Header.Get("X-PHONE"),
			"X-ASAN-ID":         r.Header.Get("X-ASAN-ID"),
			"X-GOOGLE-KEY":      r.Header.Get("X-GOOGLE-KEY"),
			"X-REAL-IP":         r.Header.Get("X-REAL-IP"),
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer upstream.Close()

	p := New(testConfig(upstream.URL))

	claims := &middleware.Claims{
		UserID:         1,
		Username:       "alice",
		CustomerID:     1,
		CustomerType:   "business",
		CIF:            "CIF-001",
		TIN:            "TIN-001",
		AuthType:       "sms",
		SignatureLevel: 1,
		Phone:          "+994551234567",
		AsanID:         "asan-1",
		GoogleKey:      "gkey-1",
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/w/auth/test", nil)
	r.RemoteAddr = "10.0.0.1:5555"

	// Inject claims into context (simulating auth middleware)
	ctx := middleware.ContextWithClaims(r.Context(), claims)
	r = r.WithContext(ctx)

	p.ServeHTTP(w, r)

	var headers map[string]string
	json.Unmarshal(w.Body.Bytes(), &headers)

	expected := map[string]string{
		"X-USER-ID":         "1",
		"X-USERNAME":        "alice",
		"X-CUSTOMER-ID":     "1",
		"X-CUSTOMER-TYPE":   "business",
		"X-CIF":             "CIF-001",
		"X-TIN":             "TIN-001",
		"X-AUTH-TYPE":       "sms",
		"X-SIGNATURE-LEVEL": "1",
		"X-PHONE":           "+994551234567",
		"X-ASAN-ID":         "asan-1",
		"X-GOOGLE-KEY":      "gkey-1",
		"X-REAL-IP":         "10.0.0.1",
	}
	for key, want := range expected {
		if got := headers[key]; got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestProxy_EnrichHeaders_NoClaims(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Without claims, no enrichment headers should be set
		if v := r.Header.Get("X-USER-ID"); v != "" {
			t.Errorf("X-USER-ID should be empty without claims, got %q", v)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	p := New(testConfig(upstream.URL))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/w/auth/test", nil)
	p.ServeHTTP(w, r)
}

func TestProxy_CircuitBreaker_OpensOnFailures(t *testing.T) {
	callCount := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstream.Close()

	cfg := &config.Config{
		Routes: []config.Route{
			{ID: "failing-svc", PathPrefix: "/w/fail", Upstream: upstream.URL, StripPrefix: 1},
		},
		CircuitBreaker: config.CircuitBreaker{
			MaxRequests:  1,
			Interval:     60 * time.Second,
			Timeout:      1 * time.Second,
			FailureRatio: 0.5,
			WindowSize:   4, // Need 4 requests before tripping
		},
	}
	p := New(cfg)

	// Send enough requests to trigger circuit breaker (4 requests, all failing)
	for i := 0; i < 4; i++ {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/w/fail/test", nil)
		p.ServeHTTP(w, r)
	}

	// Next request should hit the circuit breaker (open state)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/w/fail/test", nil)
	p.ServeHTTP(w, r)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 (circuit open)", w.Code)
	}
	var body map[string]string
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["code"] != "error.service-unavailable" {
		t.Errorf("code = %q, want error.service-unavailable", body["code"])
	}
}

func TestFallback_EnglishDefault(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/test", nil)
	fallback(w, r)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", w.Code)
	}
	var body map[string]string
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["message"] != "This service is currently unavailable. Please try again later." {
		t.Errorf("message = %q", body["message"])
	}
}

func TestFallback_AzerbaijaniLocale(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/test", nil)
	r.Header.Set("Accept-Language", "az")
	fallback(w, r)

	var body map[string]string
	json.Unmarshal(w.Body.Bytes(), &body)
	want := "Hazırda bu xidmət əlçatan deyil. Zəhmət olmasa daha sonra yenidən cəhd edin."
	if body["message"] != want {
		t.Errorf("message = %q, want %q", body["message"], want)
	}
}

func TestFallback_AzLocalePrefix(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/test", nil)
	r.Header.Set("Accept-Language", "az-AZ")
	fallback(w, r)

	var body map[string]string
	json.Unmarshal(w.Body.Bytes(), &body)
	want := "Hazırda bu xidmət əlçatan deyil. Zəhmət olmasa daha sonra yenidən cəhd edin."
	if body["message"] != want {
		t.Errorf("az-AZ should match az prefix, message = %q", body["message"])
	}
}

func TestHandleProxyError_BadGateway(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/test", nil)
	handleProxyError(w, r, context.DeadlineExceeded)

	if w.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", w.Code)
	}
	var body map[string]string
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["code"] != "error.bad-gateway" {
		t.Errorf("code = %q, want error.bad-gateway", body["code"])
	}
}

func TestHandleProxyError_CircuitOpen(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/test", nil)
	handleProxyError(w, r, gobreaker.ErrOpenState)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", w.Code)
	}
}

func TestHandleProxyError_TooManyRequests(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/test", nil)
	handleProxyError(w, r, gobreaker.ErrTooManyRequests)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", w.Code)
	}
}

func TestRespondJSON(t *testing.T) {
	w := httptest.NewRecorder()
	respondJSON(w, http.StatusCreated, map[string]string{"key": "val"})

	if w.Code != http.StatusCreated {
		t.Errorf("status = %d, want 201", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	var body map[string]string
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["key"] != "val" {
		t.Errorf("body = %v", body)
	}
}

func TestCBTransport_Success(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	cb := gobreaker.NewCircuitBreaker[*http.Response](gobreaker.Settings{
		Name: "test-cb",
	})
	transport := &cbTransport{base: http.DefaultTransport, cb: cb}

	req, _ := http.NewRequest(http.MethodGet, upstream.URL, nil)
	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestCBTransport_5xxCountsAsFailure(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer upstream.Close()

	cb := gobreaker.NewCircuitBreaker[*http.Response](gobreaker.Settings{
		Name: "test-5xx",
	})
	transport := &cbTransport{base: http.DefaultTransport, cb: cb}

	req, _ := http.NewRequest(http.MethodGet, upstream.URL, nil)
	_, err := transport.RoundTrip(req)
	if err == nil {
		t.Fatal("expected error for 5xx response")
	}
}

func TestCBTransport_NetworkError(t *testing.T) {
	cb := gobreaker.NewCircuitBreaker[*http.Response](gobreaker.Settings{
		Name: "test-net-err",
	})
	transport := &cbTransport{base: http.DefaultTransport, cb: cb}

	// Request to a closed port triggers a transport-level error
	req, _ := http.NewRequest(http.MethodGet, "http://localhost:1/test", nil)
	_, err := transport.RoundTrip(req)
	if err == nil {
		t.Fatal("expected error for unreachable server")
	}
}

func TestProxy_MultipleRoutes_FirstMatch(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Got-Path", r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	p := New(testConfig(upstream.URL))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/w/dict/words", nil)
	p.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if got := w.Header().Get("X-Got-Path"); got != "/dict/words" {
		t.Errorf("upstream path = %q, want /dict/words", got)
	}
}

func TestProxy_InvalidUpstreamURL(t *testing.T) {
	cfg := &config.Config{
		Routes: []config.Route{
			{ID: "bad", PathPrefix: "/bad", Upstream: "://invalid", StripPrefix: 0},
			{ID: "good", PathPrefix: "/good", Upstream: "http://localhost:9999", StripPrefix: 0},
		},
		CircuitBreaker: config.CircuitBreaker{
			MaxRequests:  5,
			Interval:     60 * time.Second,
			Timeout:      5 * time.Second,
			FailureRatio: 0.5,
			WindowSize:   100,
		},
	}
	p := New(cfg)

	// Bad route should be skipped; only good route registered
	if len(p.routes) != 1 {
		t.Errorf("routes = %d, want 1 (bad route skipped)", len(p.routes))
	}
	if p.routes[0].id != "good" {
		t.Errorf("route id = %q, want good", p.routes[0].id)
	}
}
