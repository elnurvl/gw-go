# API Gateway

A reverse proxy that routes requests to backend microservices with JWT authentication, Redis-based rate limiting, and per-route circuit breaking.

## Features

- **Reverse proxy** with prefix-based routing and path stripping
- **JWT authentication** via RS256/JWKS with session revocation checks
- **Rate limiting** via [redis_rate](https://github.com/go-redis/redis_rate) (keyed by device ID / username / IP)
- **Circuit breaking** per upstream route (powered by [gobreaker](https://github.com/sony/gobreaker))
- **Header enrichment** — forwards JWT claims as `X-USER-ID`, `X-USERNAME`, `X-CUSTOMER-ID`, etc.
- **Graceful shutdown** on SIGINT/SIGTERM
- **Structured JSON logging** via `log/slog`

## Prerequisites

- [Go 1.26+](https://go.dev/doc/install)
- An authorization server serving a JWKS endpoint
- A Redis server
- [golangci-lint](https://golangci-lint.run/) (optional, for linting)

## Getting Started

### 1. Start the Authorization Server (`ms-auth`) and Redis server (comes with `ms-auth` docker-compose)

### 2. Configure environment

Copy the example env file
```bash
cp .env.example .env
```
and set the following:
- `AUTH_URL` and `AUDIENCE` must be set according to the Authorization Server configuration
- For integration tests `JWT_PRIVATE_KEY` must be same as the Authorization Server `JWT_PRIVATE_KEY`


### 3. Build and run

```bash
# Build binary
make build

# Run directly
make run

# Or run the built binary with a custom config
./bin/gateway -config /path/to/config.yaml
```

The gateway starts on the configured port (default `9000`).

## Development

### Make targets

| Command                | Description                                            |
|------------------------|--------------------------------------------------------|
| `make build`           | Build binary to `bin/gateway`                          |
| `make run`             | Run with `go run`                                      |
| `make test`            | Unit tests (requires Redis)                            |
| `make test-integration`| Unit + integration tests (requires Redis and `ms-auth`)|
| `make coverage`        | Run tests, print summary, open HTML coverage report    |
| `make lint`            | Run golangci-lint                                      |
| `make clean`           | Remove `bin/` directory                                |

## Architecture

### Request flow

```
Client → Logging → Recovery → RateLimit → Auth → Proxy → Upstream Service
```

1. **Logging** — logs method, path, status, and duration for every request; recovers from panics.
2. **Rate Limiter** — token-bucket rate limiter backed by Redis. Identifies clients by `X-DEVICE-ID` header, then `USERNAME` header, then IP address.
3. **Auth** — validates JWT (RS256 via JWKS endpoint), checks session revocation in Redis. Paths listed in `bypassPaths` skip authentication.
4. **Proxy** — matches request path to a route by longest prefix, strips configured path segments, enriches upstream request with JWT claim headers, and forwards via `httputil.ReverseProxy`.

### Design decisions

- **Fail-open on Redis failure** — both rate limiting and session revocation allow requests through when Redis is unavailable.
- **Per-route circuit breakers** — each upstream service gets its own independent breaker. One failing service does not affect others. HTTP 5xx responses from upstreams count as failures.
- **JWKS double-checked locking** — on encountering an unknown `kid`, JWKS keys are refreshed once using a read-to-write lock upgrade pattern.
- **Locale-aware errors** — circuit breaker fallback returns Azerbaijani error messages when `Accept-Language` starts with `az`.

## Configuration

All settings are defined in `config.yaml` — server port, Redis connection, JWT/JWKS settings, route definitions, rate limit thresholds, circuit breaker parameters, and authentication bypass paths. See the file for details.

## Dependencies

| Package | Purpose                     |
|---------|-----------------------------|
| [golang-jwt/jwt/v5](https://github.com/golang-jwt/jwt) | JWT parsing and validation  |
| [MicahParks/keyfunc/v3](https://github.com/MicahParks/keyfunc) | JWKS key management         |
| [redis/go-redis/v9](https://github.com/redis/go-redis) | Redis client                |
| [go-redis/redis_rate/v10](https://github.com/go-redis/redis_rate) | Redis-based rate limiting   |
| [sony/gobreaker/v2](https://github.com/sony/gobreaker) | Circuit breaker             |
| [gopkg.in/yaml.v3](https://github.com/go-yaml/yaml) | YAML config parsing         |
