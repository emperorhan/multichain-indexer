package admin

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const (
	// staleLimiterTTL is how long a per-IP limiter can be idle before cleanup.
	staleLimiterTTL = 10 * time.Minute

	// cleanupInterval is how often the background goroutine sweeps stale entries.
	cleanupInterval = 1 * time.Minute
)

// endpointLimit defines rate limit parameters for an endpoint pattern.
type endpointLimit struct {
	rps   rate.Limit
	burst int
}

// limiterEntry wraps a rate.Limiter with a last-accessed timestamp for TTL-based eviction.
type limiterEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// RateLimitMiddleware provides per-endpoint, per-IP rate limiting for the admin API.
type RateLimitMiddleware struct {
	mu       sync.Mutex
	limiters map[string]*limiterEntry // key: "endpoint|clientIP"
	rules    []endpointRule
	logger   *slog.Logger
	nowFunc  func() time.Time // injectable clock for testing
	stopOnce sync.Once
	stopCh   chan struct{}
}

type endpointRule struct {
	method string // "POST", "DELETE", "GET", "" (any)
	prefix string // path prefix to match
	limit  endpointLimit
}

// NewRateLimitMiddleware creates a new rate limiting middleware with default rules.
// It starts a background goroutine that periodically cleans up stale per-IP limiters.
// Call Stop() to release the background goroutine when the middleware is no longer needed.
func NewRateLimitMiddleware(logger *slog.Logger) *RateLimitMiddleware {
	rl := &RateLimitMiddleware{
		limiters: make(map[string]*limiterEntry),
		logger:   logger,
		nowFunc:  time.Now,
		stopCh:   make(chan struct{}),
		rules: []endpointRule{
			{method: "POST", prefix: "/admin/v1/replay", limit: endpointLimit{rps: rate.Limit(1.0 / 300), burst: 1}},           // 1 req/5min
			{method: "POST", prefix: "/admin/v1/reconcile", limit: endpointLimit{rps: rate.Limit(1.0 / 300), burst: 1}},         // 1 req/5min
			{method: "POST", prefix: "/admin/v1/watched-addresses", limit: endpointLimit{rps: rate.Limit(10.0 / 60), burst: 3}}, // 10 req/min
			{method: "DELETE", prefix: "/admin/v1/address-books", limit: endpointLimit{rps: rate.Limit(10.0 / 60), burst: 3}},   // 10 req/min
			{method: "", prefix: "", limit: endpointLimit{rps: 1, burst: 5}},                                                     // default: 60 req/min
		},
	}

	go rl.cleanupLoop()
	return rl
}

// Stop shuts down the background cleanup goroutine. Safe to call multiple times.
func (rl *RateLimitMiddleware) Stop() {
	rl.stopOnce.Do(func() {
		close(rl.stopCh)
	})
}

// cleanupLoop runs periodically to remove stale per-IP limiters.
func (rl *RateLimitMiddleware) cleanupLoop() {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-rl.stopCh:
			return
		case <-ticker.C:
			rl.evictStale()
		}
	}
}

// evictStale removes limiter entries that have not been accessed within the TTL.
func (rl *RateLimitMiddleware) evictStale() {
	now := rl.nowFunc()
	rl.mu.Lock()
	defer rl.mu.Unlock()

	for key, entry := range rl.limiters {
		if now.Sub(entry.lastSeen) > staleLimiterTTL {
			delete(rl.limiters, key)
		}
	}
}

// LimiterCount returns the number of active limiter entries (for testing/monitoring).
func (rl *RateLimitMiddleware) LimiterCount() int {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	return len(rl.limiters)
}

// Wrap returns an http.Handler that applies per-IP rate limiting before delegating to next.
func (rl *RateLimitMiddleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clientIP := extractClientIP(r)
		endpointKey := rl.resolveEndpointKey(r.Method, r.URL.Path)
		key := endpointKey + "|" + clientIP

		limiter := rl.getOrCreateLimiter(key, endpointKey)

		if !limiter.Allow() {
			w.Header().Set("Retry-After", "60")
			http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
			rl.logger.Warn("admin API rate limit exceeded",
				"method", r.Method,
				"path", r.URL.Path,
				"remote_addr", r.RemoteAddr,
				"client_ip", clientIP,
			)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// extractClientIP determines the client's IP address from the request.
// It checks, in order: X-Forwarded-For (first IP), X-Real-IP, then r.RemoteAddr.
func extractClientIP(r *http.Request) string {
	// Try X-Forwarded-For header (may contain comma-separated list of IPs)
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Take the first IP (the original client)
		if idx := strings.IndexByte(xff, ','); idx != -1 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}

	// Try X-Real-IP header
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}

	// Fall back to RemoteAddr, stripping the port
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// RemoteAddr might not have a port (unlikely but handle gracefully)
		return r.RemoteAddr
	}
	return host
}

// resolveEndpointKey matches the request to an endpoint rule and returns its key.
func (rl *RateLimitMiddleware) resolveEndpointKey(method, path string) string {
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

// getOrCreateLimiter retrieves or creates a rate limiter for the given composite key.
func (rl *RateLimitMiddleware) getOrCreateLimiter(key, endpointKey string) *rate.Limiter {
	now := rl.nowFunc()

	rl.mu.Lock()
	defer rl.mu.Unlock()

	if entry, ok := rl.limiters[key]; ok {
		entry.lastSeen = now
		return entry.limiter
	}

	el := rl.resolveLimit(endpointKey)
	limiter := rate.NewLimiter(el.rps, el.burst)
	rl.limiters[key] = &limiterEntry{
		limiter:  limiter,
		lastSeen: now,
	}
	return limiter
}

// resolveLimit finds the endpointLimit for a given endpoint key.
func (rl *RateLimitMiddleware) resolveLimit(endpointKey string) endpointLimit {
	for _, rule := range rl.rules {
		ruleKey := fmt.Sprintf("%s:%s", rule.method, rule.prefix)
		if ruleKey == endpointKey {
			return rule.limit
		}
	}
	// fallback
	return endpointLimit{rps: 1, burst: 5}
}
