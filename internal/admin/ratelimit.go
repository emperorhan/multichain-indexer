package admin

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"golang.org/x/time/rate"
)

// endpointLimit defines rate limit parameters for an endpoint pattern.
type endpointLimit struct {
	rps   rate.Limit
	burst int
}

// RateLimitMiddleware provides per-endpoint rate limiting for the admin API.
type RateLimitMiddleware struct {
	limiters sync.Map // key: resolved endpoint key, value: *rate.Limiter
	rules    []endpointRule
	logger   *slog.Logger
}

type endpointRule struct {
	method  string // "POST", "DELETE", "GET", "" (any)
	prefix  string // path prefix to match
	limit   endpointLimit
}

// NewRateLimitMiddleware creates a new rate limiting middleware with default rules.
func NewRateLimitMiddleware(logger *slog.Logger) *RateLimitMiddleware {
	return &RateLimitMiddleware{
		logger: logger,
		rules: []endpointRule{
			{method: "POST", prefix: "/admin/v1/replay", limit: endpointLimit{rps: rate.Limit(1.0 / 300), burst: 1}},         // 1 req/5min
			{method: "POST", prefix: "/admin/v1/reconcile", limit: endpointLimit{rps: rate.Limit(1.0 / 300), burst: 1}},       // 1 req/5min
			{method: "POST", prefix: "/admin/v1/watched-addresses", limit: endpointLimit{rps: rate.Limit(10.0 / 60), burst: 3}}, // 10 req/min
			{method: "DELETE", prefix: "/admin/v1/address-books", limit: endpointLimit{rps: rate.Limit(10.0 / 60), burst: 3}},   // 10 req/min
			{method: "", prefix: "", limit: endpointLimit{rps: 1, burst: 5}},                                                     // default: 60 req/min
		},
	}
}

// Wrap returns an http.Handler that applies rate limiting before delegating to next.
func (rl *RateLimitMiddleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := rl.resolveKey(r.Method, r.URL.Path)
		limiter := rl.getOrCreateLimiter(key)

		if !limiter.Allow() {
			w.Header().Set("Retry-After", "60")
			http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
			rl.logger.Warn("admin API rate limit exceeded",
				"method", r.Method,
				"path", r.URL.Path,
				"remote_addr", r.RemoteAddr,
			)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (rl *RateLimitMiddleware) resolveKey(method, path string) string {
	for _, rule := range rl.rules {
		if rule.method != "" && !strings.EqualFold(rule.method, method) {
			continue
		}
		if rule.prefix != "" && !strings.HasPrefix(path, rule.prefix) {
			continue
		}
		return fmt.Sprintf("%s:%s", rule.method, rule.prefix)
	}
	return "default"
}

func (rl *RateLimitMiddleware) getOrCreateLimiter(key string) *rate.Limiter {
	if v, ok := rl.limiters.Load(key); ok {
		return v.(*rate.Limiter)
	}

	el := rl.resolveLimit(key)
	limiter := rate.NewLimiter(el.rps, el.burst)
	actual, _ := rl.limiters.LoadOrStore(key, limiter)
	return actual.(*rate.Limiter)
}

func (rl *RateLimitMiddleware) resolveLimit(key string) endpointLimit {
	for _, rule := range rl.rules {
		ruleKey := fmt.Sprintf("%s:%s", rule.method, rule.prefix)
		if ruleKey == key {
			return rule.limit
		}
	}
	// fallback
	return endpointLimit{rps: 1, burst: 5}
}
