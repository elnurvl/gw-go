package middleware

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/go-redis/redis_rate/v10"
	goredis "github.com/redis/go-redis/v9"

	"gw-go/config"
)

// RateLimitChecker abstracts the rate-limit check for testability.
type RateLimitChecker interface {
	Allow(ctx context.Context, key string, limit redis_rate.Limit) (*redis_rate.Result, error)
}

// redisRateLimiter adapts redis_rate.Limiter to the RateLimitChecker interface.
type redisRateLimiter struct {
	limiter *redis_rate.Limiter
}

func (r *redisRateLimiter) Allow(ctx context.Context, key string, limit redis_rate.Limit) (*redis_rate.Result, error) {
	return r.limiter.Allow(ctx, key, limit)
}

type RateLimiter struct {
	checker    RateLimitChecker
	limit      redis_rate.Limit
	keyPrefix  string
	keyHeaders []config.KeyHeader
}

func NewRateLimiter(rdb *goredis.Client, cfg config.RateLimit) *RateLimiter {
	checker := &redisRateLimiter{limiter: redis_rate.NewLimiter(rdb)}
	return newRateLimiterWithChecker(checker, cfg)
}

func newRateLimiterWithChecker(checker RateLimitChecker, cfg config.RateLimit) *RateLimiter {
	return &RateLimiter{
		checker: checker,
		limit: redis_rate.Limit{
			Rate:   cfg.Rate,
			Burst:  cfg.Rate,
			Period: cfg.Window,
		},
		keyPrefix:  cfg.KeyPrefix,
		keyHeaders: cfg.KeyHeaders,
	}
}

func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := rl.keyPrefix + rl.resolveKey(r)

		res, err := rl.checker.Allow(r.Context(), key, rl.limit)
		if err != nil {
			slog.Warn("rate limit check failed", "err", err)
			next.ServeHTTP(w, r) // fail open
			return
		}

		if res.Allowed == 0 {
			retryAfter := res.RetryAfter
			w.Header().Set("Retry-After", fmt.Sprintf("%d", int(retryAfter.Seconds())))
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
