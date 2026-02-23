package normalizer

import (
	"testing"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/circuitbreaker"
	"github.com/stretchr/testify/assert"
)

func TestWithRetryConfig_AppliesValues(t *testing.T) {
	n := &Normalizer{}
	opt := WithRetryConfig(7, 200*time.Millisecond, 5*time.Second)
	opt(n)
	assert.Equal(t, 7, n.retryMaxAttempts)
	assert.Equal(t, 200*time.Millisecond, n.retryDelayStart)
	assert.Equal(t, 5*time.Second, n.retryDelayMax)
}

func TestWithCircuitBreaker_AppliesValues(t *testing.T) {
	called := false
	cb := func(from, to circuitbreaker.State) { called = true }

	n := &Normalizer{}
	opt := WithCircuitBreaker(5, 3, 30*time.Second, cb)
	opt(n)

	assert.Equal(t, 5, n.cbFailureThreshold)
	assert.Equal(t, 3, n.cbSuccessThreshold)
	assert.Equal(t, 30*time.Second, n.cbOpenTimeout)
	assert.NotNil(t, n.cbOnStateChange)

	// Verify the callback is the one we passed.
	n.cbOnStateChange(circuitbreaker.StateClosed, circuitbreaker.StateOpen)
	assert.True(t, called)
}

func TestWithCircuitBreaker_NilCallback(t *testing.T) {
	n := &Normalizer{}
	opt := WithCircuitBreaker(3, 2, 15*time.Second, nil)
	opt(n)

	assert.Equal(t, 3, n.cbFailureThreshold)
	assert.Equal(t, 2, n.cbSuccessThreshold)
	assert.Equal(t, 15*time.Second, n.cbOpenTimeout)
	assert.Nil(t, n.cbOnStateChange)
}

func TestWithMaxMsgSizeMB_AppliesValue(t *testing.T) {
	n := &Normalizer{}
	opt := WithMaxMsgSizeMB(64)
	opt(n)
	assert.Equal(t, 64, n.maxMsgSizeMB)
}

func TestWithMaxMsgSizeMB_ZeroIgnored(t *testing.T) {
	n := &Normalizer{maxMsgSizeMB: 32}
	opt := WithMaxMsgSizeMB(0)
	opt(n)
	assert.Equal(t, 32, n.maxMsgSizeMB, "zero value should not overwrite existing")
}

func TestWithMaxMsgSizeMB_NegativeIgnored(t *testing.T) {
	n := &Normalizer{maxMsgSizeMB: 32}
	opt := WithMaxMsgSizeMB(-1)
	opt(n)
	assert.Equal(t, 32, n.maxMsgSizeMB, "negative value should not overwrite existing")
}

func TestWithTLS_AppliesValues(t *testing.T) {
	n := &Normalizer{}
	opt := WithTLS(true, "/ca.pem", "/cert.pem", "/key.pem")
	opt(n)
	assert.True(t, n.tlsEnabled)
	assert.Equal(t, "/ca.pem", n.tlsCA)
	assert.Equal(t, "/cert.pem", n.tlsCert)
	assert.Equal(t, "/key.pem", n.tlsKey)
}

func TestWithTLS_Disabled(t *testing.T) {
	n := &Normalizer{}
	opt := WithTLS(false, "", "", "")
	opt(n)
	assert.False(t, n.tlsEnabled)
	assert.Empty(t, n.tlsCA)
}
