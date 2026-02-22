package circuitbreaker

import (
	"errors"
	"sync"
	"time"
)

// ErrCircuitOpen is returned when the circuit breaker is open.
var ErrCircuitOpen = errors.New("circuit breaker is open")

// State represents the circuit breaker state.
type State int

const (
	StateClosed   State = iota // Normal operation
	StateOpen                  // Failing, rejecting requests
	StateHalfOpen              // Testing if service recovered
)

// Breaker implements a simple circuit breaker.
type Breaker struct {
	mu               sync.Mutex
	state            State
	failureCount     int
	successCount     int
	failureThreshold int
	successThreshold int // successes needed in half-open to close
	openTimeout      time.Duration
	lastFailureAt    time.Time
	onStateChange    func(from, to State)
}

// Config configures a circuit breaker.
type Config struct {
	FailureThreshold int           // failures before opening (default: 5)
	SuccessThreshold int           // successes in half-open before closing (default: 2)
	OpenTimeout      time.Duration // how long to stay open before half-open (default: 30s)
	OnStateChange    func(from, to State)
}

func New(cfg Config) *Breaker {
	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = 5
	}
	if cfg.SuccessThreshold <= 0 {
		cfg.SuccessThreshold = 2
	}
	if cfg.OpenTimeout <= 0 {
		cfg.OpenTimeout = 30 * time.Second
	}
	return &Breaker{
		state:            StateClosed,
		failureThreshold: cfg.FailureThreshold,
		successThreshold: cfg.SuccessThreshold,
		openTimeout:      cfg.OpenTimeout,
		onStateChange:    cfg.OnStateChange,
	}
}

// Allow checks if a request should be allowed.
func (b *Breaker) Allow() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case StateClosed:
		return nil
	case StateOpen:
		if time.Since(b.lastFailureAt) > b.openTimeout {
			b.setState(StateHalfOpen)
			return nil
		}
		return ErrCircuitOpen
	case StateHalfOpen:
		return nil
	}
	return nil
}

// RecordSuccess records a successful call.
func (b *Breaker) RecordSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failureCount = 0
	if b.state == StateHalfOpen {
		b.successCount++
		if b.successCount >= b.successThreshold {
			b.setState(StateClosed)
		}
	}
}

// RecordFailure records a failed call.
func (b *Breaker) RecordFailure() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failureCount++
	b.successCount = 0
	b.lastFailureAt = time.Now()
	if b.state == StateHalfOpen {
		b.setState(StateOpen)
	} else if b.state == StateClosed && b.failureCount >= b.failureThreshold {
		b.setState(StateOpen)
	}
}

// GetState returns the current state.
func (b *Breaker) GetState() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	// Check if open should transition to half-open
	if b.state == StateOpen && time.Since(b.lastFailureAt) > b.openTimeout {
		b.setState(StateHalfOpen)
	}
	return b.state
}

func (b *Breaker) setState(to State) {
	from := b.state
	if from == to {
		return
	}
	b.state = to
	b.successCount = 0
	if to == StateClosed {
		b.failureCount = 0
	}
	if b.onStateChange != nil {
		b.onStateChange(from, to)
	}
}

func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}
