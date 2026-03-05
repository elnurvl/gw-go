package middleware

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"

	"gw-go/config"
)

var rateLimitScript = redis.NewScript(`
local key    = KEYS[1]
local limit  = tonumber(ARGV[1])
local window = tonumber(ARGV[2])

local current = redis.call("INCR", key)
if current == 1 then
    redis.call("PEXPIRE", key, window)
end

if current > limit then
    return 0
end
return 1
`)

type RateLimiter struct {
	rdb        RedisClient
	rate       int
	window     time.Duration
	keyPrefix  string
	keyHeaders []config.KeyHeader
}

func NewRateLimiter(rdb RedisClient, cfg config.RateLimit) *RateLimiter {
	return &RateLimiter{
		rdb:        rdb,
		rate:       cfg.Rate,
		window:     cfg.Window,
		keyPrefix:  cfg.KeyPrefix,
		keyHeaders: cfg.KeyHeaders,
	}
}

func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := rl.resolveKey(r)

		allowed, err := rateLimitScript.Run(
			r.Context(), rl.rdb,
			[]string{rl.keyPrefix + key},
			rl.rate, rl.window.Milliseconds(),
		).Int64()

		if err != nil {
			slog.Warn("rate limit check failed", "err", err)
			next.ServeHTTP(w, r) // fail open
			return
		}

		if allowed == 0 {
			w.Header().Set("Retry-After", fmt.Sprintf("%d", int(rl.window.Seconds())))
			writeJSON(w, http.StatusTooManyRequests, errBody("rate limit exceeded"))
			return
		}

		next.ServeHTTP(w, r)
	})
}

// resolveKey determines the rate-limit bucket using configured header priority, falling back to client IP.
func (rl *RateLimiter) resolveKey(r *http.Request) string {
	for _, kh := range rl.keyHeaders {
		if v := r.Header.Get(kh.Header); v != "" {
			return kh.Prefix + ":" + v
		}
	}
	return "ip:" + ClientIP(r)
}
