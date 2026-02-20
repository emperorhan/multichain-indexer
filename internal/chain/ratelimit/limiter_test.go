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

func TestLimiter_ExactTokenConsumption(t *testing.T) {
	// Verify exactly N tokens are consumed for N Wait calls.
	const (
		rps   = 1000.0 // high rate so all calls succeed quickly
		burst = 20
		calls = 10
	)
	l := NewLimiter(rps, burst, "test")
	ctx := context.Background()

	for i := 0; i < calls; i++ {
		err := l.Wait(ctx)
		require.NoError(t, err, "call %d should succeed", i)
	}

	// After consuming `calls` tokens, the remaining burst should be burst - calls
	// (modulo any tokens replenished). Allow should reflect this.
	remaining := 0
	for l.limiter.Allow() {
		remaining++
	}
	// We consumed 10, burst was 20, some may have been replenished but
	// at 1000 RPS the test runs in <1ms so at most 1 token replenished.
	assert.LessOrEqual(t, remaining, burst-calls+2,
		"remaining tokens should reflect exact consumption")
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
