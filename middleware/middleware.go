package middleware

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"

	"github.com/redis/go-redis/v9"
)

// RedisClient abstracts the Redis operations used by middleware.
type RedisClient interface {
	redis.Scripter
	Exists(ctx context.Context, keys ...string) *redis.IntCmd
}

// ClientIP resolves the originating client IP from proxy headers,
// falling back to the network-level remote address.
func ClientIP(r *http.Request) string {
	for _, h := range []string{
		"CF-Connecting-IP",
		"True-Client-IP",
		"X-Forwarded-For",
		"X-Real-IP",
	} {
		if v := r.Header.Get(h); v != "" {
			ip, _, _ := strings.Cut(v, ",")
			return strings.TrimSpace(ip)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func errBody(msg string) map[string]string {
	return map[string]string{"message": msg}
}
