package middleware

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"

	"gw-go/config"
)

// testKeyID is the kid used in test JWTs and JWKS.
const testKeyID = "test-key-1"

// testAuth creates an Auth with a local JWKS server and mock Redis.
func testAuth(t *testing.T, rdb RedisClient, bypass []string) (*Auth, *rsa.PrivateKey) {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	// Serve JWKS endpoint
	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		jwks := map[string]any{
			"keys": []map[string]any{
				{
					"kty": "RSA",
					"kid": testKeyID,
					"use": "sig",
					"alg": "RS256",
					"n":   base64URLEncode(privateKey.N.Bytes()),
					"e":   base64URLEncode(big.NewInt(int64(privateKey.E)).Bytes()),
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(jwks)
	}))
	t.Cleanup(jwksServer.Close)

	k, err := keyfunc.NewDefault([]string{jwksServer.URL})
	if err != nil {
		t.Fatal(err)
	}

	cfg := config.JWT{
		Enabled:          true,
		AuthURL:          jwksServer.URL,
		Issuer:           "test-issuer",
		Audience:         "test-audience",
		ValidMethods:     []string{"RS256"},
		SessionKeyPrefix: "session:revoked:",
	}

	return &Auth{cfg: cfg, rdb: rdb, bypass: bypass, jwks: k}, privateKey
}

// signToken creates a signed RS256 JWT with the given claims.
func signToken(t *testing.T, key *rsa.PrivateKey, claims Claims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = testKeyID
	s, err := token.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// validClaims returns Claims that pass issuer/audience validation.
func validClaims() Claims {
	now := time.Now()
	return Claims{
		UserID:   "u-123",
		Username: "testuser",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "test-issuer",
			Audience:  jwt.ClaimStrings{"test-audience"},
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now.Add(-time.Minute)),
		},
	}
}

// base64URLEncode encodes bytes as unpadded base64url.
func base64URLEncode(data []byte) string {
	const enc = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	result := make([]byte, 0, (len(data)*4+2)/3)
	for i := 0; i < len(data); i += 3 {
		var b uint32
		remaining := len(data) - i
		switch {
		case remaining >= 3:
			b = uint32(data[i])<<16 | uint32(data[i+1])<<8 | uint32(data[i+2])
			result = append(result, enc[b>>18&0x3F], enc[b>>12&0x3F], enc[b>>6&0x3F], enc[b&0x3F])
		case remaining == 2:
			b = uint32(data[i])<<16 | uint32(data[i+1])<<8
			result = append(result, enc[b>>18&0x3F], enc[b>>12&0x3F], enc[b>>6&0x3F])
		case remaining == 1:
			b = uint32(data[i]) << 16
			result = append(result, enc[b>>18&0x3F], enc[b>>12&0x3F])
		}
	}
	return string(result)
}

func TestClaimsFromContext_Nil(t *testing.T) {
	ctx := context.Background()
	if c := ClaimsFromContext(ctx); c != nil {
		t.Errorf("expected nil claims from empty context, got %+v", c)
	}
}

func TestClaimsFromContext_WithClaims(t *testing.T) {
	claims := &Claims{UserID: "u-42", Username: "alice"}
	ctx := context.WithValue(context.Background(), claimsCtxKey, claims)
	got := ClaimsFromContext(ctx)
	if got == nil {
		t.Fatal("expected claims, got nil")
	}
	if got.UserID != "u-42" || got.Username != "alice" {
		t.Errorf("claims = %+v", got)
	}
}

func TestBearerToken(t *testing.T) {
	tests := []struct {
		header string
		want   string
	}{
		{"Bearer abc123", "abc123"},
		{"Bearer ", ""},
		{"bearer abc", ""},
		{"Basic abc", ""},
		{"", ""},
	}
	for _, tt := range tests {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		if tt.header != "" {
			r.Header.Set("Authorization", tt.header)
		}
		if got := bearerToken(r); got != tt.want {
			t.Errorf("bearerToken(%q) = %q, want %q", tt.header, got, tt.want)
		}
	}
}

func TestShouldBypass(t *testing.T) {
	a := &Auth{bypass: []string{"/health", "/public/", "/w/auth/login"}}

	tests := []struct {
		path string
		want bool
	}{
		{"/health", true},
		{"/health/ready", true},
		{"/public/", true},
		{"/public/docs", true},
		{"/w/auth/login", true},
		{"/w/auth/login/otp", true},
		{"/api/private", false},
		{"/", false},
	}
	for _, tt := range tests {
		if got := a.shouldBypass(tt.path); got != tt.want {
			t.Errorf("shouldBypass(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestShouldBypass_Exclusion(t *testing.T) {
	a := &Auth{bypass: []string{"!/w/auth/user-info", "/w/auth/"}}

	tests := []struct {
		path string
		want bool
	}{
		{"/w/auth/login", true},
		{"/w/auth/otp", true},
		{"/w/auth/user-info", false},
		{"/w/auth/user-info/details", false},
		{"/api/private", false},
	}
	for _, tt := range tests {
		if got := a.shouldBypass(tt.path); got != tt.want {
			t.Errorf("shouldBypass(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestNewAuth_MissingJwksPath(t *testing.T) {
	cfg := config.JWT{Enabled: true, AuthURL: "http://localhost:9060"}
	_, err := NewAuth(cfg, newMockRedis(), nil)
	if err == nil {
		t.Fatal("expected error for missing jwksPath")
	}
}

func TestNewAuth_Disabled(t *testing.T) {
	cfg := config.JWT{Enabled: false}
	auth, err := NewAuth(cfg, newMockRedis(), nil)
	if err != nil {
		t.Fatalf("NewAuth() error: %v", err)
	}

	// When disabled, Middleware should return the handler as-is
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := auth.Middleware(inner)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/protected", nil)
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("disabled auth: status = %d, want 200", w.Code)
	}
}

func TestAuth_MissingToken(t *testing.T) {
	mock := newMockRedis()
	auth, _ := testAuth(t, mock, nil)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called")
	})
	handler := auth.Middleware(inner)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/protected", nil)
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
	var body map[string]string
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["message"] != "missing authorization token" {
		t.Errorf("message = %q", body["message"])
	}
}

func TestAuth_InvalidToken(t *testing.T) {
	mock := newMockRedis()
	auth, _ := testAuth(t, mock, nil)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called")
	})
	handler := auth.Middleware(inner)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/protected", nil)
	r.Header.Set("Authorization", "Bearer invalid.token.here")
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestAuth_ValidToken(t *testing.T) {
	mock := newMockRedis()
	auth, key := testAuth(t, mock, nil)

	var gotClaims *Claims
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotClaims = ClaimsFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	handler := auth.Middleware(inner)

	claims := validClaims()
	token := signToken(t, key, claims)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/protected", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if gotClaims == nil {
		t.Fatal("claims not found in context")
	}
	if gotClaims.UserID != "u-123" {
		t.Errorf("userId = %q, want u-123", gotClaims.UserID)
	}
	if gotClaims.Username != "testuser" {
		t.Errorf("username = %q, want testuser", gotClaims.Username)
	}
}

func TestAuth_ExpiredToken(t *testing.T) {
	mock := newMockRedis()
	auth, key := testAuth(t, mock, nil)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called")
	})
	handler := auth.Middleware(inner)

	claims := validClaims()
	claims.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-time.Hour))
	token := signToken(t, key, claims)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/protected", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestAuth_WrongIssuer(t *testing.T) {
	mock := newMockRedis()
	auth, key := testAuth(t, mock, nil)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called")
	})
	handler := auth.Middleware(inner)

	claims := validClaims()
	claims.Issuer = "wrong-issuer"
	token := signToken(t, key, claims)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/protected", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestAuth_WrongAudience(t *testing.T) {
	mock := newMockRedis()
	auth, key := testAuth(t, mock, nil)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called")
	})
	handler := auth.Middleware(inner)

	claims := validClaims()
	claims.Audience = jwt.ClaimStrings{"wrong-audience"}
	token := signToken(t, key, claims)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/protected", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestAuth_BypassPath(t *testing.T) {
	mock := newMockRedis()
	auth, _ := testAuth(t, mock, []string{"/health", "/w/auth/login"})

	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	handler := auth.Middleware(inner)

	// No token, but path is bypassed
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/health", nil)
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if !called {
		t.Error("inner handler should be called for bypass path")
	}
}

func TestAuth_RevokedSession(t *testing.T) {
	mock := newMockRedis()
	auth, key := testAuth(t, mock, nil)

	// Mark session as revoked
	sessionID := "sess-revoked-123"
	mock.keys["session:revoked:"+sessionID] = true

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called for revoked session")
	})
	handler := auth.Middleware(inner)

	claims := validClaims()
	claims.SessionID = sessionID
	claims.ID = sessionID // jti in RegisteredClaims
	token := signToken(t, key, claims)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/protected", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
	var body map[string]string
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["message"] != "session revoked" {
		t.Errorf("message = %q, want 'session revoked'", body["message"])
	}
}

func TestAuth_CheckRevoked_EmptySessionID(t *testing.T) {
	mock := newMockRedis()
	auth, _ := testAuth(t, mock, nil)

	err := auth.checkRevoked(context.Background(), "")
	if err != nil {
		t.Errorf("checkRevoked with empty sessionID should return nil, got: %v", err)
	}
}

func TestAuth_CheckRevoked_NotRevoked(t *testing.T) {
	mock := newMockRedis()
	auth, _ := testAuth(t, mock, nil)

	err := auth.checkRevoked(context.Background(), "active-session")
	if err != nil {
		t.Errorf("checkRevoked for active session should return nil, got: %v", err)
	}
}

func TestAuth_CheckRevoked_RedisDown_FailOpen(t *testing.T) {
	mock := errMockRedis()
	auth := &Auth{cfg: config.JWT{SessionKeyPrefix: "session:revoked:"}, rdb: mock}

	err := auth.checkRevoked(context.Background(), "some-session")
	if err != nil {
		t.Errorf("checkRevoked should fail open when redis is down, got: %v", err)
	}
}

func TestAuth_TokenWithAllClaims(t *testing.T) {
	mock := newMockRedis()
	auth, key := testAuth(t, mock, nil)

	var gotClaims *Claims
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotClaims = ClaimsFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	handler := auth.Middleware(inner)

	claims := validClaims()
	claims.CustomerID = "cust-1"
	claims.CustomerType = "premium"
	claims.CIF = "CIF001"
	claims.TIN = "TIN001"
	claims.AuthType = "password"
	claims.SignatureLevel = "high"
	claims.Phone = "+994501234567"
	claims.AsanID = "asan-1"
	claims.GoogleKey = "gkey-1"

	token := signToken(t, key, claims)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/protected", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if gotClaims == nil {
		t.Fatal("claims nil")
	}
	checks := []struct {
		field, got, want string
	}{
		{"UserID", gotClaims.UserID, "u-123"},
		{"Username", gotClaims.Username, "testuser"},
		{"CustomerID", gotClaims.CustomerID, "cust-1"},
		{"CustomerType", gotClaims.CustomerType, "premium"},
		{"CIF", gotClaims.CIF, "CIF001"},
		{"TIN", gotClaims.TIN, "TIN001"},
		{"AuthType", gotClaims.AuthType, "password"},
		{"SignatureLevel", gotClaims.SignatureLevel, "high"},
		{"Phone", gotClaims.Phone, "+994501234567"},
		{"AsanID", gotClaims.AsanID, "asan-1"},
		{"GoogleKey", gotClaims.GoogleKey, "gkey-1"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.field, c.got, c.want)
		}
	}
}

func TestContextWithClaims(t *testing.T) {
	claims := &Claims{UserID: "u-99", Username: "bob"}
	ctx := ContextWithClaims(context.Background(), claims)
	got := ClaimsFromContext(ctx)
	if got == nil {
		t.Fatal("expected claims from context")
	}
	if got.UserID != "u-99" {
		t.Errorf("UserID = %q, want u-99", got.UserID)
	}
}

func TestNewAuth_Enabled(t *testing.T) {
	// Start a JWKS server so NewAuth can fetch keys
	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"keys":[]}`))
	}))
	defer jwksServer.Close()

	cfg := config.JWT{
		Enabled:          true,
		AuthURL:          jwksServer.URL,
		JwksPath:         "/",
		Issuer:           "test",
		Audience:         "test",
		ValidMethods:     []string{"RS256"},
		SessionKeyPrefix: "session:revoked:",
	}
	auth, err := NewAuth(cfg, newMockRedis(), []string{"/health"})
	if err != nil {
		t.Fatalf("NewAuth() error: %v", err)
	}
	if auth.jwks == nil {
		t.Error("jwks should be set when enabled")
	}
	if len(auth.bypass) != 1 {
		t.Errorf("bypass len = %d, want 1", len(auth.bypass))
	}
}

func TestAuth_DifferentKeyID_Rejected(t *testing.T) {
	mock := newMockRedis()

	privateKey, _ := rsa.GenerateKey(rand.Reader, 2048)

	// JWKS server serves a DIFFERENT key ID
	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		otherKey, _ := rsa.GenerateKey(rand.Reader, 2048)
		jwks := map[string]any{
			"keys": []map[string]any{
				{
					"kty": "RSA",
					"kid": "other-key",
					"use": "sig",
					"alg": "RS256",
					"n":   base64URLEncode(otherKey.N.Bytes()),
					"e":   base64URLEncode(big.NewInt(int64(otherKey.E)).Bytes()),
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(jwks)
	}))
	defer jwksServer.Close()

	k, _ := keyfunc.NewDefault([]string{jwksServer.URL})
	auth := &Auth{
		cfg:    config.JWT{Enabled: true, Issuer: "test-issuer", Audience: "test-audience", ValidMethods: []string{"RS256"}, SessionKeyPrefix: "session:revoked:"},
		rdb:    mock,
		jwks:   k,
		bypass: nil,
	}

	claims := validClaims()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = testKeyID // signed with kid that server doesn't have
	signed, _ := token.SignedString(privateKey)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called")
	})
	handler := auth.Middleware(inner)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/test", nil)
	r.Header.Set("Authorization", fmt.Sprintf("Bearer %s", signed))
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}
