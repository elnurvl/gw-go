package middleware

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
	"github.com/redis/go-redis/v9"

	"gw-go/config"
)

type ctxKey int

const claimsCtxKey ctxKey = iota

// Claims holds JWT claims mapped from the token payload.
type Claims struct {
	UserID         string `json:"userId"`
	Username       string `json:"username"`
	CustomerID     string `json:"customerId"`
	CustomerType   string `json:"customerType"`
	CIF            string `json:"cif"`
	TIN            string `json:"tin"`
	AuthType       string `json:"authType"`
	SignatureLevel string `json:"signatureLevel"`
	Phone          string `json:"phone"`
	AsanID         string `json:"asanId"`
	GoogleKey      string `json:"googleKey"`
	SessionID      string `json:"jti"`
	jwt.RegisteredClaims
}

// ClaimsFromContext extracts validated JWT claims stored by the Auth middleware.
func ClaimsFromContext(ctx context.Context) *Claims {
	c, _ := ctx.Value(claimsCtxKey).(*Claims)
	return c
}

// Auth validates JWT tokens via JWKS and checks session revocation in Redis.
type Auth struct {
	cfg    config.JWT
	rdb    *redis.Client
	bypass []string
	jwks   keyfunc.Keyfunc
}

func NewAuth(cfg config.JWT, rdb *redis.Client, bypass []string) (*Auth, error) {
	a := &Auth{cfg: cfg, rdb: rdb, bypass: bypass}

	if cfg.Enabled {
		jwksURL := strings.TrimRight(cfg.AuthURL, "/") + "/internal/auth/jwks"
		k, err := keyfunc.NewDefault([]string{jwksURL})
		if err != nil {
			return nil, fmt.Errorf("initializing JWKS from %s: %w", jwksURL, err)
		}
		a.jwks = k
	}

	return a, nil
}

func (a *Auth) Middleware(next http.Handler) http.Handler {
	if !a.cfg.Enabled {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.shouldBypass(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		raw := bearerToken(r)
		if raw == "" {
			writeJSON(w, http.StatusUnauthorized, errBody("missing authorization token"))
			return
		}

		var claims Claims
		token, err := jwt.ParseWithClaims(raw, &claims, a.jwks.Keyfunc,
			jwt.WithIssuer(a.cfg.Issuer),
			jwt.WithAudience(a.cfg.Audience),
			jwt.WithValidMethods([]string{"RS256"}),
		)
		if err != nil || !token.Valid {
			slog.Warn("auth failed", "err", err, "path", r.URL.Path)
			writeJSON(w, http.StatusUnauthorized, errBody("invalid token"))
			return
		}

		if err := a.checkRevoked(r.Context(), claims.SessionID); err != nil {
			writeJSON(w, http.StatusUnauthorized, errBody("session revoked"))
			return
		}

		ctx := context.WithValue(r.Context(), claimsCtxKey, &claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// --- session revocation ---

func (a *Auth) checkRevoked(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return nil
	}
	exists, err := a.rdb.Exists(ctx, "session:revoked:"+sessionID).Result()
	if err != nil {
		slog.Warn("redis revocation check failed", "err", err)
		return nil // fail open
	}
	if exists > 0 {
		return fmt.Errorf("session %s revoked", sessionID)
	}
	return nil
}

func (a *Auth) shouldBypass(path string) bool {
	for _, p := range a.bypass {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

func bearerToken(r *http.Request) string {
	if after, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		return after
	}
	return ""
}
