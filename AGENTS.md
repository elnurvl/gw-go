# AGENTS.md

This file provides guidance to AI Agents when working with code in this repository.

## Project Overview

Go API Gateway — a reverse proxy that routes requests to backend microservices with JWT authentication, Redis-based rate limiting, and per-route circuit breaking.

## Build & Development Commands

```bash
make build                  # Build binary to bin/gateway
make run                    # Run with go run
make test                   # Unit tests in Docker (Redis provided by compose)
make test-integration       # All tests in Docker (ms-auth must run on host)
make coverage               # Run tests, print summary, open HTML report
make lint                   # golangci-lint run
make clean                  # Remove bin/

# Run with custom config
./bin/gateway -config /path/to/config.yaml

# Run a single test
go test ./middleware/ -run TestName -v
```

## Architecture

**Entry point:** `main.go` — loads YAML config, sets up Redis, builds middleware chain, starts HTTP server with graceful shutdown.

**Middleware chain (outermost → innermost):**
```
Logging → Recovery → RateLimit → Auth → Gateway (reverse proxy)
```

**Key packages:**
- `config` — YAML config loading with defaults (port 9000, rate 100 req/s, circuit breaker 50% failure over 100 requests)
- `middleware/jwt.go` — RS256 JWT validation via JWKS endpoint, session revocation check via Redis, bypass paths for public endpoints
- `middleware/ratelimit.go` — Fixed-window rate limiter using atomic Redis Lua script. Key priority: `X-DEVICE-ID` header → `USERNAME` header → client IP
- `middleware/logging.go` — Request logging via `slog` + panic recovery
- `proxy/proxy.go` — Reverse proxy with linear prefix matching, per-route `gobreaker` circuit breakers, header enrichment from JWT claims

**Request flow:** Auth middleware validates JWT and stores `*Claims` in context → Gateway reads claims from context via `enrichHeaders()` and forwards them as `X-USER-ID`, `X-USERNAME`, `X-CUSTOMER-ID`, etc. to upstream services.

## Design Conventions

- **Fail-open on Redis failure:** Both rate limiting and session revocation allow requests through when Redis is unavailable.
- **Per-route circuit breakers:** Each upstream service gets its own independent breaker — one failing service doesn't affect others. HTTP 5xx responses count as failures.
- **Locale-aware errors:** Circuit breaker fallback returns Azerbaijani messages when `Accept-Language` starts with `az`.
- **JWKS double-checked locking:** On unknown `kid`, refresh keys once with proper read→write lock upgrade pattern.
- **stdlib-first approach:** Uses Go's `net/http/httputil.ReverseProxy`, `log/slog`, minimal external deps (jwt, redis, gobreaker, yaml).
- **Context-typed keys:** JWT claims propagated via typed `ctxKey` to avoid string key collisions.

## Configuration

`config.yaml` defines server settings, Redis connection, JWT config (auth URL, bypass paths), routes (path prefix → upstream URL with strip-prefix segments), rate limit, and circuit breaker thresholds.

## Dependencies

Go 1.26 | `golang-jwt/jwt/v5` | `MicahParks/keyfunc/v3` | `redis/go-redis/v9` | `go-redis/redis_rate/v10` | `sony/gobreaker/v2` | `gopkg.in/yaml.v3`

## Commit Convention

Follows [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/).

Types: `build`, `chore`, `ci`, `docs`, `feat`, `fix`, `perf`, `refactor`, `revert`, `style`, `test`.

Format: `<type>(<optional scope>): <description>`