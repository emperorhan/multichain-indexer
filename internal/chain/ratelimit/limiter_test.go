package ratelimit

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewLimiter(t *testing.T) {
	l := NewLimiter(10.0, 5, "solana")

	require.NotNil(t, l)
	require.NotNil(t, l.limiter)
	assert.Equal(t, "solana", l.chain)

	// The underlying rate.Limiter should reflect the configured RPS and burst.
	assert.InDelta(t, 10.0, float64(l.limiter.Limit()), 0.001)
	assert.Equal(t, 5, l.limiter.Burst())
}

func TestLimiter_AllowWithinBurst(t *testing.T) {
	const burst = 5
	l := NewLimiter(100, burst, "solana")

	ctx := context.Background()

	// All requests within the burst capacity should succeed immediately.
	for i := 0; i < burst; i++ {
		start := time.Now()
		err := l.Wait(ctx)
		elapsed := time.Since(start)

		require.NoError(t, err, "request %d should not error", i)
		assert.Less(t, elapsed, 50*time.Millisecond,
			"request %d should complete immediately, took %v", i, elapsed)
	}
}

func TestLimiter_WaitWhenExhausted(t *testing.T) {
	// Use a very low RPS so that after burst is exhausted, the next request
	// must wait a noticeable amount of time.
	const (
		rps   = 10.0 // 1 token every 100ms
		burst = 1
	)
	l := NewLimiter(rps, burst, "ethereum")

	ctx := context.Background()

	// First request consumes the only burst token — should be immediate.
	err := l.Wait(ctx)
	require.NoError(t, err)

	// Second request: burst is exhausted so Allow() returns false, triggering
	// the Wait path. It should block until a new token is available (~100ms).
	start := time.Now()
	err = l.Wait(ctx)
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.GreaterOrEqual(t, elapsed, 50*time.Millisecond,
		"should have waited for a token, but only took %v", elapsed)
}

func TestLimiter_ContextCancellation(t *testing.T) {
	// Create a limiter with very low RPS and small burst so we can exhaust tokens.
	const (
		rps   = 1.0 // 1 token per second
		burst = 1
	)
	l := NewLimiter(rps, burst, "solana")

	// Exhaust the burst token.
	err := l.Wait(context.Background())
	require.NoError(t, err)

	// Now cancel the context before the next token becomes available.
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err = l.Wait(ctx)
	require.Error(t, err, "should return error when context is cancelled")
}
