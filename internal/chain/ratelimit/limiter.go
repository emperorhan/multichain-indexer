package ratelimit

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/metrics"
	"golang.org/x/time/rate"
)

// Limiter wraps a token-bucket rate limiter for RPC calls.
type Limiter struct {
	limiter *rate.Limiter
	chain   string
}

// NewLimiter creates a rate limiter that allows rps requests per second
// with a burst capacity of burst tokens.
func NewLimiter(rps float64, burst int, chain string) *Limiter {
	return &Limiter{
		limiter: rate.NewLimiter(rate.Limit(rps), burst),
		chain:   chain,
	}
}

// Wait blocks until the limiter allows one event, or ctx is done.
// Uses Reserve() to guarantee exactly one token is consumed per call.
func (l *Limiter) Wait(ctx context.Context) error {
	r := l.limiter.Reserve()
	if !r.OK() {
		return fmt.Errorf("rate: cannot reserve token")
	}
	delay := r.Delay()
	if delay > 0 {
		metrics.RPCRateLimitWaits.WithLabelValues(l.chain).Inc()
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			r.Cancel()
			return ctx.Err()
		}
	}
	return nil
}

// RecordRPCCall records an RPC call metric with status classification.
func RecordRPCCall(chain, method string, err error) {
	status := ClassifyRPCError(err)
	metrics.RPCCallsTotal.WithLabelValues(chain, method, status).Inc()
}

// ClassifyRPCError classifies an RPC error into a category.
func ClassifyRPCError(err error) string {
	if err == nil {
		return "ok"
	}
	lower := strings.ToLower(err.Error())
	switch {
	case strings.Contains(lower, "timeout") || strings.Contains(lower, "deadline exceeded"):
		return "timeout"
	case strings.Contains(lower, "rate limit") || strings.Contains(lower, "429") || strings.Contains(lower, "too many requests"):
		return "rate_limited"
	case strings.Contains(lower, "500") || strings.Contains(lower, "502") || strings.Contains(lower, "503") || strings.Contains(lower, "internal server error"):
		return "server_error"
	case strings.Contains(lower, "connection refused") || strings.Contains(lower, "connection reset") ||
		strings.Contains(lower, "network is unreachable") || strings.Contains(lower, "no such host") ||
		strings.Contains(lower, "broken pipe") || strings.Contains(lower, "eof"):
		return "network_error"
	default:
		return "client_error"
	}
}
