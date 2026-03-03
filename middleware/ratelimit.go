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
	rdb    *redis.Client
	rate   int
	window time.Duration
}

func NewRateLimiter(rdb *redis.Client, cfg config.RateLimit) *RateLimiter {
	return &RateLimiter{rdb: rdb, rate: cfg.Rate, window: cfg.Window}
}

func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := rl.resolveKey(r)

		allowed, err := rateLimitScript.Run(
			r.Context(), rl.rdb,
			[]string{"ratelimit:" + key},
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

// resolveKey determines the rate-limit bucket: device ID → username → client IP.
func (rl *RateLimiter) resolveKey(r *http.Request) string {
	if v := r.Header.Get("X-DEVICE-ID"); v != "" {
		return "device:" + v
	}
	if v := r.Header.Get("USERNAME"); v != "" {
		return "user:" + v
	}
	return "ip:" + ClientIP(r)
}
