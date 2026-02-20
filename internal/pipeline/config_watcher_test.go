package pipeline

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/stretchr/testify/assert"
)

type fakeRuntimeConfigRepo struct {
	mu      sync.Mutex
	configs map[string]string
	err     error
}

func (f *fakeRuntimeConfigRepo) GetActive(_ context.Context, _ model.Chain, _ model.Network) (map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	result := make(map[string]string, len(f.configs))
	for k, v := range f.configs {
		result[k] = v
	}
	return result, nil
}

func (f *fakeRuntimeConfigRepo) SetConfig(key, value string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.configs[key] = value
}

type fakeCoordinatorUpdater struct {
	mu        sync.Mutex
	batchSize int
	interval  time.Duration
}

func (f *fakeCoordinatorUpdater) UpdateBatchSize(newBatchSize int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.batchSize = newBatchSize
}

func (f *fakeCoordinatorUpdater) UpdateInterval(newInterval time.Duration) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.interval = newInterval
	return true
}

func (f *fakeCoordinatorUpdater) getBatchSize() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.batchSize
}

func (f *fakeCoordinatorUpdater) getInterval() time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.interval
}

func TestConfigWatcher_AppliesBatchSizeChange(t *testing.T) {
	t.Parallel()

	repo := &fakeRuntimeConfigRepo{
		configs: map[string]string{
			ConfigKeyBatchSize: "200",
		},
	}
	coord := &fakeCoordinatorUpdater{}
	watcher := NewConfigWatcher(
		model.ChainBase, model.NetworkSepolia,
		repo, coord, slog.Default(), 0,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	watcher.poll(ctx)

	assert.Equal(t, 200, coord.getBatchSize())
}

func TestConfigWatcher_AppliesIntervalChange(t *testing.T) {
	t.Parallel()

	repo := &fakeRuntimeConfigRepo{
		configs: map[string]string{
			ConfigKeyIndexingInterval: "3000",
		},
	}
	coord := &fakeCoordinatorUpdater{}
	watcher := NewConfigWatcher(
		model.ChainBase, model.NetworkSepolia,
		repo, coord, slog.Default(), 0,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	watcher.poll(ctx)

	assert.Equal(t, 3*time.Second, coord.getInterval())
}

func TestConfigWatcher_SkipsDuplicateUpdates(t *testing.T) {
	t.Parallel()

	repo := &fakeRuntimeConfigRepo{
		configs: map[string]string{
			ConfigKeyBatchSize: "300",
		},
	}
	coord := &fakeCoordinatorUpdater{}
	watcher := NewConfigWatcher(
		model.ChainBase, model.NetworkSepolia,
		repo, coord, slog.Default(), 0,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	watcher.poll(ctx)
	assert.Equal(t, 300, coord.getBatchSize())

	// Set to different value and verify it changes
	coord.UpdateBatchSize(0) // reset
	watcher.poll(ctx)
	// Should not update since the repo value hasn't changed
	assert.Equal(t, 0, coord.getBatchSize())

	// Now change the repo value
	repo.SetConfig(ConfigKeyBatchSize, "500")
	watcher.poll(ctx)
	assert.Equal(t, 500, coord.getBatchSize())
}

func TestConfigWatcher_IgnoresInvalidValues(t *testing.T) {
	t.Parallel()

	repo := &fakeRuntimeConfigRepo{
		configs: map[string]string{
			ConfigKeyBatchSize:        "not_a_number",
			ConfigKeyIndexingInterval: "-100",
		},
	}
	coord := &fakeCoordinatorUpdater{}
	watcher := NewConfigWatcher(
		model.ChainBase, model.NetworkSepolia,
		repo, coord, slog.Default(), 0,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	watcher.poll(ctx)

	assert.Equal(t, 0, coord.getBatchSize())
	assert.Equal(t, time.Duration(0), coord.getInterval())
}
