package admin

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func newTestRateLimiter(t *testing.T) *RateLimitMiddleware {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	rl := NewRateLimitMiddleware(logger)
	t.Cleanup(rl.Stop)
	return rl
}

func TestRateLimitMiddleware_AllowsNormalRequests(t *testing.T) {
	rl := newTestRateLimiter(t)

	called := false
	handler := rl.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/admin/v1/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !called {
		t.Error("expected handler to be called")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestRateLimitMiddleware_BlocksExcessiveRequests(t *testing.T) {
	rl := newTestRateLimiter(t)

	handler := rl.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Replay endpoint: 1 req/5min with burst=1
	// First request should succeed
	req := httptest.NewRequest(http.MethodPost, "/admin/v1/replay", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("first request: expected 200, got %d", rec.Code)
	}

	// Second request from the same IP should be rate limited
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, httptest.NewRequest(http.MethodPost, "/admin/v1/replay", nil))
	if rec2.Code != http.StatusTooManyRequests {
		t.Errorf("second request: expected 429, got %d", rec2.Code)
	}

	// Check Retry-After header
	if rec2.Header().Get("Retry-After") == "" {
		t.Error("expected Retry-After header on 429 response")
	}
}

func TestRateLimitMiddleware_DifferentEndpointsIndependent(t *testing.T) {
	rl := newTestRateLimiter(t)

	handler := rl.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Exhaust replay limit
	req := httptest.NewRequest(http.MethodPost, "/admin/v1/replay", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	// Reconcile should still work (different endpoint limiter, same IP)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/admin/v1/reconcile", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("reconcile request: expected 200, got %d", rec.Code)
	}
}

func TestRateLimitMiddleware_DifferentIPsIndependent(t *testing.T) {
	rl := newTestRateLimiter(t)

	handler := rl.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Exhaust replay limit from IP-A
	reqA := httptest.NewRequest(http.MethodPost, "/admin/v1/replay", nil)
	reqA.RemoteAddr = "10.0.0.1:12345"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, reqA)
	if rec.Code != http.StatusOK {
		t.Fatalf("IP-A first request: expected 200, got %d", rec.Code)
	}

	// Second request from IP-A should be rate limited
	reqA2 := httptest.NewRequest(http.MethodPost, "/admin/v1/replay", nil)
	reqA2.RemoteAddr = "10.0.0.1:12346"
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, reqA2)
	if rec2.Code != http.StatusTooManyRequests {
		t.Errorf("IP-A second request: expected 429, got %d", rec2.Code)
	}

	// IP-B should still be allowed (independent limiter)
	reqB := httptest.NewRequest(http.MethodPost, "/admin/v1/replay", nil)
	reqB.RemoteAddr = "10.0.0.2:54321"
	rec3 := httptest.NewRecorder()
	handler.ServeHTTP(rec3, reqB)
	if rec3.Code != http.StatusOK {
		t.Errorf("IP-B first request: expected 200 (independent from IP-A), got %d", rec3.Code)
	}
}

func TestExtractClientIP_XForwardedFor_TrustedProxy(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.1:12345" // private IP → trusted proxy
	r.Header.Set("X-Forwarded-For", "203.0.113.50, 70.41.3.18, 150.172.238.178")

	ip := extractClientIP(r)
	if ip != "203.0.113.50" {
		t.Errorf("expected 203.0.113.50, got %s", ip)
	}
}

func TestExtractClientIP_XForwardedFor_UntrustedProxy(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "192.0.2.1:12345" // public IP → untrusted
	r.Header.Set("X-Forwarded-For", "203.0.113.50")

	ip := extractClientIP(r)
	if ip != "192.0.2.1" {
		t.Errorf("expected RemoteAddr 192.0.2.1 (untrusted proxy), got %s", ip)
	}
}

func TestExtractClientIP_XForwardedForSingle(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "127.0.0.1:9999" // loopback → trusted
	r.Header.Set("X-Forwarded-For", "203.0.113.50")

	ip := extractClientIP(r)
	if ip != "203.0.113.50" {
		t.Errorf("expected 203.0.113.50, got %s", ip)
	}
}

func TestExtractClientIP_XRealIP(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "172.16.0.1:8080" // private IP → trusted
	r.Header.Set("X-Real-IP", "198.51.100.10")

	ip := extractClientIP(r)
	if ip != "198.51.100.10" {
		t.Errorf("expected 198.51.100.10, got %s", ip)
	}
}

func TestExtractClientIP_RemoteAddr(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "192.168.1.100:8080"
	// Ensure no proxy headers are set
	r.Header.Del("X-Forwarded-For")
	r.Header.Del("X-Real-IP")

	ip := extractClientIP(r)
	if ip != "192.168.1.100" {
		t.Errorf("expected 192.168.1.100, got %s", ip)
	}
}

func TestExtractClientIP_XForwardedForPriority(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Forwarded-For", "1.1.1.1")
	r.Header.Set("X-Real-IP", "2.2.2.2")
	r.RemoteAddr = "10.0.0.1:1234" // private → trusted proxy

	ip := extractClientIP(r)
	if ip != "1.1.1.1" {
		t.Errorf("expected X-Forwarded-For to take priority, got %s", ip)
	}
}

func TestRateLimitMiddleware_PerIPKeys(t *testing.T) {
	rl := newTestRateLimiter(t)

	handler := rl.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Make requests from two different IPs via X-Forwarded-For (trusted proxy)
	req1 := httptest.NewRequest(http.MethodPost, "/admin/v1/replay", nil)
	req1.RemoteAddr = "10.0.0.1:12345"
	req1.Header.Set("X-Forwarded-For", "10.1.1.1")
	handler.ServeHTTP(httptest.NewRecorder(), req1)

	req2 := httptest.NewRequest(http.MethodPost, "/admin/v1/replay", nil)
	req2.RemoteAddr = "10.0.0.1:12346"
	req2.Header.Set("X-Forwarded-For", "10.2.2.2")
	handler.ServeHTTP(httptest.NewRecorder(), req2)

	// We should have at least 2 limiter entries (one per IP for the same endpoint)
	count := rl.LimiterCount()
	if count < 2 {
		t.Errorf("expected at least 2 limiter entries for different IPs, got %d", count)
	}
}

func TestRateLimitMiddleware_StaleCleanup(t *testing.T) {
	rl := newTestRateLimiter(t)

	// Override nowFunc so we can control time
	frozenNow := time.Now()
	rl.nowFunc = func() time.Time { return frozenNow }

	handler := rl.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Create limiter entries from two IPs
	req1 := httptest.NewRequest(http.MethodGet, "/admin/v1/health", nil)
	req1.RemoteAddr = "10.0.0.1:1111"
	handler.ServeHTTP(httptest.NewRecorder(), req1)

	req2 := httptest.NewRequest(http.MethodGet, "/admin/v1/health", nil)
	req2.RemoteAddr = "10.0.0.2:2222"
	handler.ServeHTTP(httptest.NewRecorder(), req2)

	if rl.LimiterCount() < 2 {
		t.Fatalf("expected at least 2 limiters before cleanup, got %d", rl.LimiterCount())
	}

	// Advance time past the TTL
	frozenNow = frozenNow.Add(staleLimiterTTL + time.Second)

	// Trigger eviction manually
	rl.evictStale()

	if rl.LimiterCount() != 0 {
		t.Errorf("expected 0 limiters after TTL expiry, got %d", rl.LimiterCount())
	}
}

func TestRateLimitMiddleware_StaleCleanupKeepsActive(t *testing.T) {
	rl := newTestRateLimiter(t)

	frozenNow := time.Now()
	rl.nowFunc = func() time.Time { return frozenNow }

	handler := rl.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Create entries from two IPs at t=0
	req1 := httptest.NewRequest(http.MethodGet, "/admin/v1/health", nil)
	req1.RemoteAddr = "10.0.0.1:1111"
	handler.ServeHTTP(httptest.NewRecorder(), req1)

	req2 := httptest.NewRequest(http.MethodGet, "/admin/v1/health", nil)
	req2.RemoteAddr = "10.0.0.2:2222"
	handler.ServeHTTP(httptest.NewRecorder(), req2)

	// Advance time to just before TTL expiry
	frozenNow = frozenNow.Add(staleLimiterTTL - time.Second)

	// Touch IP-1 only (refreshes its lastSeen)
	req1b := httptest.NewRequest(http.MethodGet, "/admin/v1/health", nil)
	req1b.RemoteAddr = "10.0.0.1:3333"
	handler.ServeHTTP(httptest.NewRecorder(), req1b)

	// Advance time past the original TTL (IP-2 is stale, IP-1 was refreshed)
	frozenNow = frozenNow.Add(2 * time.Second)

	rl.evictStale()

	if rl.LimiterCount() != 1 {
		t.Errorf("expected 1 limiter (active IP-1 kept, stale IP-2 evicted), got %d", rl.LimiterCount())
	}
}
