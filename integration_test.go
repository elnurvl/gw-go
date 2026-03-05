//go:build integration

package main

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/redis/go-redis/v9"

	"gw-go/config"
	"gw-go/middleware"
	"gw-go/proxy"
)

const (
	msAuthKeyID  = "main-key"
	msAuthIssuer = "auth-service"
	msAuthAud    = "api"
)

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// --- prerequisites ---

func requireRedis(t *testing.T) *redis.Client {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{Addr: envOr("REDIS_ADDR", "localhost:6379"), DB: 14})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		t.Fatalf("redis unavailable: %v", err)
	}
	t.Cleanup(func() {
		rdb.FlushDB(context.Background())
		rdb.Close()
	})
	return rdb
}

func msAuthURL() string {
	return envOr("MS_AUTH_ADDR", "http://localhost:9060")
}

func envFilePath() string {
	return envOr("ENV_FILE_PATH", ".env")
}

func requireMSAuth(t *testing.T) {
	t.Helper()
	addr := msAuthURL()
	resp, err := http.Get(addr + "/auth/public-keys")
	if err != nil {
		t.Fatalf("ms-auth unavailable at %s: %v", addr, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ms-auth /auth/public-keys returned %d", resp.StatusCode)
	}
}

func loadPrivateKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	path := envFilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read %s: %v", path, err)
	}

	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "JWT_PRIVATE_KEY=") {
			continue
		}
		pem := strings.TrimPrefix(line, "JWT_PRIVATE_KEY=")
		pem = strings.TrimSpace(pem)

		// Strip PEM headers and decode base64
		b64 := pem
		b64 = strings.Replace(b64, "-----BEGIN PRIVATE KEY-----", "", 1)
		b64 = strings.Replace(b64, "-----END PRIVATE KEY-----", "", 1)
		b64 = strings.ReplaceAll(b64, " ", "")
		b64 = strings.TrimSpace(b64)

		der, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			t.Fatalf("base64 decode private key: %v", err)
		}

		key, err := x509.ParsePKCS8PrivateKey(der)
		if err != nil {
			t.Fatalf("parse PKCS8 key: %v", err)
		}

		rsaKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			t.Fatal("key is not RSA")
		}
		return rsaKey
	}
	t.Fatal("JWT_PRIVATE_KEY not found in .env")
	return nil
}

// --- gateway builder ---

func buildIntegrationGateway(t *testing.T, rdb *redis.Client) http.Handler {
	t.Helper()

	addr := msAuthURL()
	cfg := &config.Config{
		JWT: config.JWT{
			Enabled:            true,
			AuthURL:            addr,
			JwksPath:           "/auth/public-keys",
			Issuer:             msAuthIssuer,
			Audience:           msAuthAud,
			ValidMethods:       []string{"RS256"},
			RevokedTokenPrefix: "token:revoked:",
		},
		Routes: []config.Route{
			{ID: "ms-auth", PathPrefix: "/w/auth", Upstream: addr, StripPrefix: 1},
		},
		RateLimit: config.RateLimit{
			Rate:      5,
			Window:    10 * time.Second,
			KeyPrefix: "ratelimit:",
			KeyHeaders: []config.KeyHeader{
				{Header: "X-DEVICE-ID", Prefix: "device"},
				{Header: "USERNAME", Prefix: "user"},
			},
		},
		CircuitBreaker: config.CircuitBreaker{
			MaxRequests:  5,
			Interval:     60 * time.Second,
			Timeout:      5 * time.Second,
			FailureRatio: 0.5,
			WindowSize:   100,
		},
		BypassPaths: []string{"/w/auth/public-keys"},
	}

	auth, err := middleware.NewAuth(cfg.JWT, rdb, cfg.BypassPaths)
	if err != nil {
		t.Fatalf("NewAuth: %v", err)
	}

	return middleware.Logging(
		middleware.Recovery(
			middleware.NewRateLimiter(rdb, cfg.RateLimit).Middleware(
				auth.Middleware(proxy.New(cfg)),
			),
		),
	)
}

// --- token helpers ---

func signMSAuthToken(t *testing.T, key *rsa.PrivateKey, sessionID string) string {
	t.Helper()
	now := time.Now()
	claims := middleware.Claims{
		UserID:       999,
		Username:     "integration-test",
		CustomerID:   1,
		CustomerType: "JURIDICAL",
		CIF:          "0000001",
		TIN:          "9999999999",
		AuthType:     "OTP",
		Phone:        "+994501234567",
		SessionID:    sessionID,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    msAuthIssuer,
			Audience:  jwt.ClaimStrings{msAuthAud},
			Subject:   "integration-test",
			ExpiresAt: jwt.NewNumericDate(now.Add(15 * time.Minute)),
			IssuedAt:  jwt.NewNumericDate(now),
			ID:        sessionID,
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = msAuthKeyID
	s, err := token.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// --- Integration Tests (real ms-auth + real Redis) ---

func TestIntegration_PublicKeys(t *testing.T) {
	rdb := requireRedis(t)
	requireMSAuth(t)
	handler := buildIntegrationGateway(t, rdb)

	// /w/auth/public-keys is a bypass path — no token needed
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/w/auth/public-keys", nil)
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	keys, ok := body["keys"].([]any)
	if !ok || len(keys) == 0 {
		t.Fatal("expected non-empty keys array in JWKS response")
	}

	// Verify the key has expected fields
	first := keys[0].(map[string]any)
	if first["kid"] != msAuthKeyID {
		t.Errorf("kid = %v, want %s", first["kid"], msAuthKeyID)
	}
	if first["alg"] != "RS256" {
		t.Errorf("alg = %v, want RS256", first["alg"])
	}
}

func TestIntegration_UserInfo(t *testing.T) {
	rdb := requireRedis(t)
	requireMSAuth(t)
	key := loadPrivateKey(t)
	handler := buildIntegrationGateway(t, rdb)

	sessionID := fmt.Sprintf("integration-%d", time.Now().UnixNano())
	token := signMSAuthToken(t, key, sessionID)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/w/auth/user-info", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	handler.ServeHTTP(w, r)

	// The gateway should pass the token through (not reject at gateway level).
	// ms-auth may return any status depending on DB state, but it should NOT be
	// the gateway's own 401 "missing authorization token" / "invalid token".
	if w.Code == http.StatusUnauthorized {
		var body map[string]string
		json.Unmarshal(w.Body.Bytes(), &body)
		if body["message"] == "missing authorization token" || body["message"] == "invalid token" {
			t.Fatalf("gateway rejected the token: %s", body["message"])
		}
	}

	// Response should be JSON from ms-auth (not an HTML 404 or gateway error)
	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	t.Logf("/auth/user-info status=%d (response from ms-auth)", w.Code)
}

func TestIntegration_UserInfo_NoToken(t *testing.T) {
	rdb := requireRedis(t)
	requireMSAuth(t)
	handler := buildIntegrationGateway(t, rdb)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/w/auth/user-info", nil)
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
	var body map[string]string
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["message"] != "missing authorization token" {
		t.Errorf("message = %q, want 'missing authorization token'", body["message"])
	}
}

func TestIntegration_UserInfo_InvalidToken(t *testing.T) {
	rdb := requireRedis(t)
	requireMSAuth(t)
	handler := buildIntegrationGateway(t, rdb)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/w/auth/user-info", nil)
	r.Header.Set("Authorization", "Bearer garbage.token.here")
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestIntegration_RateLimit(t *testing.T) {
	rdb := requireRedis(t)
	requireMSAuth(t)
	key := loadPrivateKey(t)
	handler := buildIntegrationGateway(t, rdb)

	sessionID := fmt.Sprintf("rate-%d", time.Now().UnixNano())
	token := signMSAuthToken(t, key, sessionID)
	deviceID := fmt.Sprintf("dev-rate-%d", time.Now().UnixNano())

	// Send 5 requests (at the limit)
	for i := 0; i < 5; i++ {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/w/auth/public-keys", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("X-DEVICE-ID", deviceID)
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200", i+1, w.Code)
		}
	}

	// 6th request should be rate limited
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/w/auth/public-keys", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("X-DEVICE-ID", deviceID)
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusTooManyRequests {
		t.Errorf("6th request: status = %d, want 429", w.Code)
	}
	if ra := w.Header().Get("Retry-After"); ra == "" {
		t.Error("missing Retry-After header")
	}
}

func TestIntegration_RateLimit_IndependentDevices(t *testing.T) {
	rdb := requireRedis(t)
	requireMSAuth(t)
	handler := buildIntegrationGateway(t, rdb)

	ts := time.Now().UnixNano()
	deviceA := fmt.Sprintf("dev-A-%d", ts)
	deviceB := fmt.Sprintf("dev-B-%d", ts)

	// Exhaust device-A's limit
	for i := 0; i < 6; i++ {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/w/auth/public-keys", nil)
		r.Header.Set("X-DEVICE-ID", deviceA)
		handler.ServeHTTP(w, r)
	}

	// device-B should still be allowed
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/w/auth/public-keys", nil)
	r.Header.Set("X-DEVICE-ID", deviceB)
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("device-B: status = %d, want 200 (independent limit)", w.Code)
	}
}

func TestIntegration_SessionRevocation(t *testing.T) {
	rdb := requireRedis(t)
	requireMSAuth(t)
	key := loadPrivateKey(t)
	handler := buildIntegrationGateway(t, rdb)

	sessionID := fmt.Sprintf("revoke-%d", time.Now().UnixNano())
	token := signMSAuthToken(t, key, sessionID)

	// First request — not revoked, should pass gateway auth
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/w/auth/user-info", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	handler.ServeHTTP(w, r)

	if w.Code == http.StatusUnauthorized {
		var body map[string]string
		json.Unmarshal(w.Body.Bytes(), &body)
		if body["message"] == "token revoked" {
			t.Fatal("session should not be revoked yet")
		}
	}

	// Revoke session in Redis
	err := rdb.Set(context.Background(), "token:revoked:"+sessionID, "1", time.Minute).Err()
	if err != nil {
		t.Fatalf("redis SET: %v", err)
	}

	// Second request — should be rejected at gateway level
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodGet, "/w/auth/user-info", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("after revocation: status = %d, want 401", w.Code)
	}
	var body map[string]string
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["message"] != "token revoked" {
		t.Errorf("message = %q, want 'token revoked'", body["message"])
	}
}

func TestIntegration_SessionRevocation_Cleared(t *testing.T) {
	rdb := requireRedis(t)
	requireMSAuth(t)
	key := loadPrivateKey(t)
	handler := buildIntegrationGateway(t, rdb)

	sessionID := fmt.Sprintf("revoke-clear-%d", time.Now().UnixNano())
	token := signMSAuthToken(t, key, sessionID)

	// Revoke
	rdb.Set(context.Background(), "token:revoked:"+sessionID, "1", time.Minute)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/w/auth/user-info", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("while revoked: status = %d, want 401", w.Code)
	}

	// Un-revoke
	rdb.Del(context.Background(), "token:revoked:"+sessionID)

	// Should pass gateway auth again
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodGet, "/w/auth/user-info", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	handler.ServeHTTP(w, r)

	// Not blocked by gateway (not the gateway's revocation error)
	if w.Code == http.StatusUnauthorized {
		var body map[string]string
		json.Unmarshal(w.Body.Bytes(), &body)
		if body["message"] == "token revoked" {
			t.Error("session should no longer be revoked")
		}
	}
}

func TestIntegration_UnknownRoute(t *testing.T) {
	rdb := requireRedis(t)
	requireMSAuth(t)
	key := loadPrivateKey(t)
	handler := buildIntegrationGateway(t, rdb)

	sessionID := fmt.Sprintf("unknown-%d", time.Now().UnixNano())
	token := signMSAuthToken(t, key, sessionID)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/unknown/path", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}
