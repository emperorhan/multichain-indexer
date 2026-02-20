package admin

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"time"
)

const maxAuditBodyBytes = 1024 // 1KB summary limit

// AuditMiddleware logs all mutating (POST/DELETE) requests for operational audit trails.
func AuditMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	auditLogger := logger.With("component", "admin_audit")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost && r.Method != http.MethodDelete {
			next.ServeHTTP(w, r)
			return
		}

		start := time.Now()

		// Capture body summary (up to 1KB)
		var bodySummary string
		if r.Body != nil {
			bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, maxAuditBodyBytes+1))
			if err == nil {
				if len(bodyBytes) > maxAuditBodyBytes {
					bodySummary = string(bodyBytes[:maxAuditBodyBytes]) + "...(truncated)"
				} else {
					bodySummary = string(bodyBytes)
				}
				// Restore body for downstream handlers
				r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			}
		}

		// Wrap response writer to capture status code
		sw := &statusWriter{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(sw, r)

		auditLogger.Warn("admin API audit",
			"timestamp", start.UTC().Format(time.RFC3339),
			"remote_addr", r.RemoteAddr,
			"method", r.Method,
			"path", r.URL.Path,
			"body_summary", bodySummary,
			"response_status", sw.statusCode,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	})
}

type statusWriter struct {
	http.ResponseWriter
	statusCode int
	written    bool
}

func (sw *statusWriter) WriteHeader(code int) {
	if !sw.written {
		sw.statusCode = code
		sw.written = true
	}
	sw.ResponseWriter.WriteHeader(code)
}

func (sw *statusWriter) Write(b []byte) (int, error) {
	if !sw.written {
		sw.written = true
	}
	return sw.ResponseWriter.Write(b)
}
