package coordinator

import (
	"log/slog"
	"testing"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/coordinator/autotune"
	"github.com/stretchr/testify/assert"
)

func TestWithMaxInitialLookbackBlocks(t *testing.T) {
	c := New(
		model.ChainSolana, model.NetworkDevnet,
		nil, 100, time.Second,
		make(chan<- event.FetchJob, 1), slog.Default(),
	)
	c.WithMaxInitialLookbackBlocks(1000)
	assert.Equal(t, int64(1000), c.maxInitialLookbackBlocks)
}

func TestWithMaxInitialLookbackBlocks_Zero(t *testing.T) {
	c := New(
		model.ChainSolana, model.NetworkDevnet,
		nil, 100, time.Second,
		make(chan<- event.FetchJob, 1), slog.Default(),
	)
	c.WithMaxInitialLookbackBlocks(0)
	assert.Equal(t, int64(0), c.maxInitialLookbackBlocks)
}

func TestWithAutoTune_SetsController(t *testing.T) {
	c := New(
		model.ChainSolana, model.NetworkDevnet,
		nil, 100, time.Second,
		make(chan<- event.FetchJob, 1), slog.Default(),
	)
	cfg := AutoTuneConfig{
		Enabled:      true,
		MinBatchSize: 10,
		MaxBatchSize: 500,
		StepUp:       10,
		StepDown:     5,
	}
	result := c.WithAutoTune(cfg)
	assert.Same(t, c, result, "WithAutoTune should return the same coordinator for chaining")
	assert.NotNil(t, c.autoTune)
}

func TestWithAutoTuneWarmStart_ChainMismatch(t *testing.T) {
	c := New(
		model.ChainSolana, model.NetworkDevnet,
		nil, 100, time.Second,
		make(chan<- event.FetchJob, 1), slog.Default(),
	)
	cfg := AutoTuneConfig{
		Enabled:      true,
		MinBatchSize: 10,
		MaxBatchSize: 500,
		StepUp:       10,
		StepDown:     5,
	}
	state := &AutoTuneRestartState{
		Chain:     model.ChainBase,
		Network:   model.NetworkDevnet,
		BatchSize: 200,
	}
	result := c.WithAutoTuneWarmStart(cfg, state)
	assert.Same(t, c, result)
	assert.NotNil(t, c.autoTune)
}

func TestWithAutoTuneWarmStart_ValidState(t *testing.T) {
	c := New(
		model.ChainSolana, model.NetworkDevnet,
		nil, 100, time.Second,
		make(chan<- event.FetchJob, 1), slog.Default(),
	)
	cfg := AutoTuneConfig{
		Enabled:      true,
		MinBatchSize: 10,
		MaxBatchSize: 500,
		StepUp:       10,
		StepDown:     5,
	}
	state := &AutoTuneRestartState{
		Chain:     model.ChainSolana,
		Network:   model.NetworkDevnet,
		BatchSize: 200,
	}
	result := c.WithAutoTuneWarmStart(cfg, state)
	assert.Same(t, c, result)
	assert.NotNil(t, c.autoTune)
}

func TestExportAutoTuneRestartState_NilAutoTune(t *testing.T) {
	c := New(
		model.ChainSolana, model.NetworkDevnet,
		nil, 100, time.Second,
		make(chan<- event.FetchJob, 1), slog.Default(),
	)
	state := c.ExportAutoTuneRestartState()
	assert.Nil(t, state, "ExportAutoTuneRestartState should return nil when autoTune is nil")
}

func TestExportAutoTuneRestartState_WithAutoTune(t *testing.T) {
	c := New(
		model.ChainSolana, model.NetworkDevnet,
		nil, 100, time.Second,
		make(chan<- event.FetchJob, 1), slog.Default(),
	)
	cfg := AutoTuneConfig{
		Enabled:      true,
		MinBatchSize: 10,
		MaxBatchSize: 500,
		StepUp:       10,
		StepDown:     5,
	}
	c.WithAutoTune(cfg)

	state := c.ExportAutoTuneRestartState()
	assert.NotNil(t, state)
	assert.Equal(t, model.ChainSolana, state.Chain)
	assert.Equal(t, model.NetworkDevnet, state.Network)
}

func TestWithAutoTuneSignalSource(t *testing.T) {
	c := New(
		model.ChainSolana, model.NetworkDevnet,
		nil, 100, time.Second,
		make(chan<- event.FetchJob, 1), slog.Default(),
	)
	source := autotune.NewRuntimeSignalRegistry()
	result := c.WithAutoTuneSignalSource(source)
	assert.Same(t, c, result)
	assert.NotNil(t, c.autoTuneSignals)
}
