package coordinator

import (
	"log/slog"
	"testing"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/stretchr/testify/assert"
)

// ---------------------------------------------------------------------------
// UpdateBatchSize tests
// ---------------------------------------------------------------------------

func TestUpdateBatchSize_ValidValue(t *testing.T) {
	c := New(
		model.ChainSolana, model.NetworkDevnet,
		nil, // watchedAddrRepo not needed
		100, time.Second,
		nil, // jobCh not needed
		slog.Default(),
	)

	// Verify initial value.
	assert.Equal(t, int32(100), c.batchSize.Load())

	// Update to a new valid value.
	c.UpdateBatchSize(200)
	assert.Equal(t, int32(200), c.batchSize.Load())

	// Update again to another valid value.
	c.UpdateBatchSize(50)
	assert.Equal(t, int32(50), c.batchSize.Load())
}

func TestUpdateBatchSize_ZeroValue(t *testing.T) {
	c := New(
		model.ChainSolana, model.NetworkDevnet,
		nil,
		100, time.Second,
		nil,
		slog.Default(),
	)

	// Zero should be ignored — batch size stays at 100.
	c.UpdateBatchSize(0)
	assert.Equal(t, int32(100), c.batchSize.Load())
}

func TestUpdateBatchSize_NegativeValue(t *testing.T) {
	c := New(
		model.ChainSolana, model.NetworkDevnet,
		nil,
		100, time.Second,
		nil,
		slog.Default(),
	)

	// Negative should be ignored — batch size stays at 100.
	c.UpdateBatchSize(-5)
	assert.Equal(t, int32(100), c.batchSize.Load())
}

// ---------------------------------------------------------------------------
// UpdateInterval tests
// ---------------------------------------------------------------------------

func TestUpdateInterval_ValidValue(t *testing.T) {
	c := New(
		model.ChainSolana, model.NetworkDevnet,
		nil,
		100, time.Second,
		nil,
		slog.Default(),
	)

	// Verify initial value.
	assert.Equal(t, int64(time.Second), c.intervalNs.Load())

	// Update to 2 seconds.
	changed := c.UpdateInterval(2 * time.Second)
	assert.True(t, changed)
	assert.Equal(t, int64(2*time.Second), c.intervalNs.Load())
}

func TestUpdateInterval_ZeroValue(t *testing.T) {
	c := New(
		model.ChainSolana, model.NetworkDevnet,
		nil,
		100, time.Second,
		nil,
		slog.Default(),
	)

	// Zero should be ignored — interval stays at 1 second, returns false.
	changed := c.UpdateInterval(0)
	assert.False(t, changed)
	assert.Equal(t, int64(time.Second), c.intervalNs.Load())
}

func TestUpdateInterval_NegativeValue(t *testing.T) {
	c := New(
		model.ChainSolana, model.NetworkDevnet,
		nil,
		100, time.Second,
		nil,
		slog.Default(),
	)

	// Negative should be ignored — interval stays at 1 second, returns false.
	changed := c.UpdateInterval(-100 * time.Millisecond)
	assert.False(t, changed)
	assert.Equal(t, int64(time.Second), c.intervalNs.Load())
}

func TestUpdateInterval_SendsResetSignal(t *testing.T) {
	c := New(
		model.ChainSolana, model.NetworkDevnet,
		nil,
		100, time.Second,
		nil,
		slog.Default(),
	)

	// The channel should be empty initially.
	assert.Len(t, c.intervalResetCh, 0)

	// A valid update should send a signal on intervalResetCh.
	changed := c.UpdateInterval(2 * time.Second)
	assert.True(t, changed)

	select {
	case <-c.intervalResetCh:
		// Signal received — expected.
	default:
		t.Fatal("expected a signal on intervalResetCh but channel was empty")
	}
}

func TestUpdateInterval_SameValueReturnsFalse(t *testing.T) {
	c := New(
		model.ChainSolana, model.NetworkDevnet,
		nil,
		100, time.Second,
		nil,
		slog.Default(),
	)

	// Updating to the same value should return false and NOT send a reset signal.
	changed := c.UpdateInterval(time.Second)
	assert.False(t, changed)
	assert.Len(t, c.intervalResetCh, 0)
}
