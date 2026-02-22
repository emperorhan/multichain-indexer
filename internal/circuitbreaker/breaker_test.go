package circuitbreaker

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew_Defaults(t *testing.T) {
	b := New(Config{})
	assert.Equal(t, StateClosed, b.GetState())
	assert.Equal(t, 5, b.failureThreshold)
	assert.Equal(t, 2, b.successThreshold)
	assert.Equal(t, 30*time.Second, b.openTimeout)
}

func TestNew_CustomConfig(t *testing.T) {
	b := New(Config{
		FailureThreshold: 3,
		SuccessThreshold: 1,
		OpenTimeout:      10 * time.Second,
	})
	assert.Equal(t, 3, b.failureThreshold)
	assert.Equal(t, 1, b.successThreshold)
	assert.Equal(t, 10*time.Second, b.openTimeout)
}

func TestBreaker_ClosedAllowsRequests(t *testing.T) {
	b := New(Config{FailureThreshold: 3})
	require.NoError(t, b.Allow())
}

func TestBreaker_OpensAfterThreshold(t *testing.T) {
	b := New(Config{FailureThreshold: 3, OpenTimeout: 1 * time.Hour})

	// Record failures below threshold
	b.RecordFailure()
	b.RecordFailure()
	require.NoError(t, b.Allow(), "should still be closed below threshold")

	// Third failure triggers open
	b.RecordFailure()
	assert.Equal(t, StateOpen, b.GetState())
	assert.ErrorIs(t, b.Allow(), ErrCircuitOpen)
}

func TestBreaker_SuccessResetsFailureCount(t *testing.T) {
	b := New(Config{FailureThreshold: 3, OpenTimeout: 1 * time.Hour})

	b.RecordFailure()
	b.RecordFailure()
	b.RecordSuccess() // should reset failure count
	b.RecordFailure()
	b.RecordFailure()
	// Only 2 failures since last success, should still be closed
	require.NoError(t, b.Allow())
	assert.Equal(t, StateClosed, b.GetState())
}

func TestBreaker_TransitionsToHalfOpenAfterTimeout(t *testing.T) {
	b := New(Config{FailureThreshold: 1, OpenTimeout: 1 * time.Millisecond})

	b.RecordFailure()
	assert.Equal(t, StateOpen, b.GetState())

	// Wait for open timeout to expire
	time.Sleep(5 * time.Millisecond)

	// Allow should transition to half-open
	require.NoError(t, b.Allow())
	assert.Equal(t, StateHalfOpen, b.GetState())
}

func TestBreaker_HalfOpenClosesAfterSuccessThreshold(t *testing.T) {
	b := New(Config{
		FailureThreshold: 1,
		SuccessThreshold: 2,
		OpenTimeout:      1 * time.Millisecond,
	})

	b.RecordFailure()
	time.Sleep(5 * time.Millisecond)
	require.NoError(t, b.Allow()) // transition to half-open

	b.RecordSuccess()
	assert.Equal(t, StateHalfOpen, b.GetState(), "not yet at success threshold")

	b.RecordSuccess()
	assert.Equal(t, StateClosed, b.GetState(), "should close after success threshold")
}

func TestBreaker_HalfOpenReopensOnFailure(t *testing.T) {
	b := New(Config{
		FailureThreshold: 1,
		SuccessThreshold: 2,
		OpenTimeout:      1 * time.Millisecond,
	})

	b.RecordFailure()
	time.Sleep(5 * time.Millisecond)
	require.NoError(t, b.Allow()) // transition to half-open

	b.RecordFailure()
	assert.Equal(t, StateOpen, b.GetState(), "should reopen on failure in half-open")
}

func TestBreaker_StateChangeCallback(t *testing.T) {
	var transitions []struct{ from, to State }
	b := New(Config{
		FailureThreshold: 2,
		SuccessThreshold: 1,
		OpenTimeout:      1 * time.Millisecond,
		OnStateChange: func(from, to State) {
			transitions = append(transitions, struct{ from, to State }{from, to})
		},
	})

	// closed -> open
	b.RecordFailure()
	b.RecordFailure()
	require.Len(t, transitions, 1)
	assert.Equal(t, StateClosed, transitions[0].from)
	assert.Equal(t, StateOpen, transitions[0].to)

	// open -> half-open
	time.Sleep(5 * time.Millisecond)
	_ = b.Allow()
	require.Len(t, transitions, 2)
	assert.Equal(t, StateOpen, transitions[1].from)
	assert.Equal(t, StateHalfOpen, transitions[1].to)

	// half-open -> closed
	b.RecordSuccess()
	require.Len(t, transitions, 3)
	assert.Equal(t, StateHalfOpen, transitions[2].from)
	assert.Equal(t, StateClosed, transitions[2].to)
}

func TestBreaker_GetStateTransitionsOpenToHalfOpen(t *testing.T) {
	b := New(Config{FailureThreshold: 1, OpenTimeout: 1 * time.Millisecond})

	b.RecordFailure()
	assert.Equal(t, StateOpen, b.state) // direct field, not GetState

	time.Sleep(5 * time.Millisecond)

	// GetState should notice timeout expired and transition
	assert.Equal(t, StateHalfOpen, b.GetState())
}

func TestState_String(t *testing.T) {
	assert.Equal(t, "closed", StateClosed.String())
	assert.Equal(t, "open", StateOpen.String())
	assert.Equal(t, "half-open", StateHalfOpen.String())
	assert.Equal(t, "unknown", State(99).String())
}

func TestBreaker_OpenDoesNotAllowBeforeTimeout(t *testing.T) {
	b := New(Config{FailureThreshold: 1, OpenTimeout: 1 * time.Hour})

	b.RecordFailure()
	assert.ErrorIs(t, b.Allow(), ErrCircuitOpen)
}

func TestBreaker_ConcurrentRecordSuccessFailure(t *testing.T) {
	// This test verifies there are no race conditions when RecordSuccess,
	// RecordFailure, Allow, and GetState are called concurrently.
	// Run with: go test -race ./internal/circuitbreaker/
	b := New(Config{
		FailureThreshold: 10,
		SuccessThreshold: 5,
		OpenTimeout:      1 * time.Millisecond,
	})

	const goroutines = 20
	const iterations = 500

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				switch id % 4 {
				case 0:
					b.RecordSuccess()
				case 1:
					b.RecordFailure()
				case 2:
					_ = b.Allow()
				case 3:
					_ = b.GetState()
				}
			}
		}(i)
	}
	wg.Wait()

	// The breaker should be in a valid state after concurrent access.
	state := b.GetState()
	assert.Contains(t, []State{StateClosed, StateOpen, StateHalfOpen}, state)
}
