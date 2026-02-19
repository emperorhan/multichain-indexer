package ratelimit

import (
	"context"

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
func (l *Limiter) Wait(ctx context.Context) error {
	if !l.limiter.Allow() {
		metrics.RPCRateLimitWaits.WithLabelValues(l.chain).Inc()
		return l.limiter.Wait(ctx)
	}
	return nil
}
