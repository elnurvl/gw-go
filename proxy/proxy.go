package proxy

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"

	"github.com/sony/gobreaker/v2"

	"gw-go/config"
	"gw-go/middleware"
)

// Proxy routes incoming requests to upstream services, applies per-route
// circuit breaking, and enriches headers with JWT claims.
type Proxy struct {
	routes []route
}

type route struct {
	id    string
	pfx   string
	proxy *httputil.ReverseProxy
}

// New creates a Proxy from the supplied configuration.
func New(cfg *config.Config) *Proxy {
	p := &Proxy{}

	for _, rc := range cfg.Routes {
		upstream, err := url.Parse(rc.Upstream)
		if err != nil {
			slog.Error("invalid upstream", "route", rc.ID, "url", rc.Upstream, "err", err)
			continue
		}

		cb := gobreaker.NewCircuitBreaker[*http.Response](gobreaker.Settings{
			Name:        rc.ID,
			MaxRequests: cfg.CircuitBreaker.MaxRequests,
			Interval:    cfg.CircuitBreaker.Interval,
			Timeout:     cfg.CircuitBreaker.Timeout,
			ReadyToTrip: func(counts gobreaker.Counts) bool {
				if counts.Requests < uint32(cfg.CircuitBreaker.WindowSize) {
					return false
				}
				return float64(counts.TotalFailures)/float64(counts.Requests) >= cfg.CircuitBreaker.FailureRatio
			},
			OnStateChange: func(name string, from, to gobreaker.State) {
				slog.Warn("circuit breaker", "route", name, "from", from, "to", to)
			},
		})

		rc := rc
		rp := &httputil.ReverseProxy{
			Rewrite: func(pr *httputil.ProxyRequest) {
				pr.SetURL(upstream)
				pr.SetXForwarded()
				stripPrefix(pr, rc.StripPrefix)
			},
			Transport:    &cbTransport{base: http.DefaultTransport, cb: cb},
			ErrorHandler: handleProxyError,
		}

		p.routes = append(p.routes, route{id: rc.ID, pfx: rc.PathPrefix, proxy: rp})
		slog.Info("route registered", "id", rc.ID, "prefix", rc.PathPrefix, "upstream", rc.Upstream)
	}

	return p
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	for _, rt := range p.routes {
		if strings.HasPrefix(r.URL.Path, rt.pfx) {
			enrichHeaders(r)
			rt.proxy.ServeHTTP(w, r)
			return
		}
	}
	http.NotFound(w, r)
}

// --- path rewriting ---

func stripPrefix(pr *httputil.ProxyRequest, n int) {
	if n <= 0 {
		return
	}
	segments := strings.SplitN(strings.TrimPrefix(pr.In.URL.Path, "/"), "/", n+1)
	if len(segments) > n {
		pr.Out.URL.Path = "/" + strings.Join(segments[n:], "/")
	} else {
		pr.Out.URL.Path = "/"
	}
	pr.Out.URL.RawPath = ""
}

// --- header enrichment ---

func enrichHeaders(r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		return
	}

	h := r.Header
	set := func(key, val string) {
		if val != "" {
			h.Set(key, val)
		}
	}

	setInt := func(key string, val int64) {
		if val != 0 {
			h.Set(key, strconv.FormatInt(val, 10))
		}
	}

	setInt("X-USER-ID", claims.UserID)
	set("X-USERNAME", claims.Username)
	setInt("X-CUSTOMER-ID", claims.CustomerID)
	set("X-CUSTOMER-TYPE", claims.CustomerType)
	set("X-CIF", claims.CIF)
	set("X-TIN", claims.TIN)
	set("X-AUTH-TYPE", claims.AuthType)
	set("X-SIGNATURE-LEVEL", strconv.Itoa(claims.SignatureLevel))
	set("X-PHONE", claims.Phone)
	set("X-ASAN-ID", claims.AsanID)
	set("X-GOOGLE-KEY", claims.GoogleKey)

	h.Set("X-REAL-IP", middleware.ClientIP(r))
}

// --- circuit breaker transport ---

type cbTransport struct {
	base http.RoundTripper
	cb   *gobreaker.CircuitBreaker[*http.Response]
}

func (t *cbTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return t.cb.Execute(func() (*http.Response, error) {
		resp, err := t.base.RoundTrip(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode >= http.StatusInternalServerError {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			return nil, fmt.Errorf("upstream %d", resp.StatusCode)
		}
		return resp, nil
	})
}

// --- error handling ---

func handleProxyError(w http.ResponseWriter, r *http.Request, err error) {
	slog.Error("proxy error", "path", r.URL.Path, "err", err)

	if err == gobreaker.ErrOpenState || err == gobreaker.ErrTooManyRequests {
		fallback(w, r)
		return
	}

	respondJSON(w, http.StatusBadGateway, map[string]string{
		"code":    "error.bad-gateway",
		"message": "The upstream service is not responding.",
	})
}

func fallback(w http.ResponseWriter, r *http.Request) {
	msg := "This service is currently unavailable. Please try again later."
	if strings.HasPrefix(r.Header.Get("Accept-Language"), "az") {
		msg = "Hazırda bu xidmət əlçatan deyil. Zəhmət olmasa daha sonra yenidən cəhd edin."
	}

	respondJSON(w, http.StatusServiceUnavailable, map[string]string{
		"code":    "error.service-unavailable",
		"message": msg,
	})
}

func respondJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
