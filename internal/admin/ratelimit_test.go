package admin

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestRateLimitMiddleware_AllowsNormalRequests(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	rl := NewRateLimitMiddleware(logger)

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
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	rl := NewRateLimitMiddleware(logger)

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

	// Second request should be rate limited
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
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	rl := NewRateLimitMiddleware(logger)

	handler := rl.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Exhaust replay limit
	req := httptest.NewRequest(http.MethodPost, "/admin/v1/replay", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	// Reconcile should still work (different limiter)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/admin/v1/reconcile", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("reconcile request: expected 200, got %d", rec.Code)
	}
}
