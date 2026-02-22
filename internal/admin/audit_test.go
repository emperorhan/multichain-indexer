package admin

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAuditMiddleware_LogsMutatingRequests(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	handler := AuditMiddleware(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))

	body := `{"chain":"solana","network":"devnet","address":"abc123"}`
	req := httptest.NewRequest(http.MethodPost, "/admin/v1/watched-addresses", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d", rec.Code)
	}

	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "admin API audit") {
		t.Error("expected audit log entry")
	}
	if !strings.Contains(logOutput, "POST") {
		t.Error("expected method in audit log")
	}
	if !strings.Contains(logOutput, "/admin/v1/watched-addresses") {
		t.Error("expected path in audit log")
	}
}

func TestAuditMiddleware_SkipsGETRequests(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	handler := AuditMiddleware(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/admin/v1/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if logBuf.Len() > 0 {
		t.Error("expected no audit log for GET request")
	}
}

func TestAuditMiddleware_LogsBodySize(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	handler := AuditMiddleware(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	largeBody := strings.Repeat("x", 2000)
	req := httptest.NewRequest(http.MethodPost, "/admin/v1/replay", strings.NewReader(largeBody))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	logOutput := logBuf.String()
	// Body content must NOT be logged; only body_size should appear.
	if strings.Contains(logOutput, "body_summary") {
		t.Error("body_summary should not appear in audit log")
	}
	if !strings.Contains(logOutput, "body_size") {
		t.Error("expected body_size in audit log")
	}
	if !strings.Contains(logOutput, "2000") {
		t.Error("expected body_size value of 2000")
	}
}

func TestAuditMiddleware_CapturesResponseStatus(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	handler := AuditMiddleware(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))

	req := httptest.NewRequest(http.MethodDelete, "/admin/v1/address-books?chain=solana&network=devnet&address=abc", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "400") {
		t.Error("expected response status 400 in audit log")
	}
}
