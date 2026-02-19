package pipeline

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/emperorhan/multichain-indexer/internal/store"
)

const (
	configWatcherDefaultInterval = 30 * time.Second

	// Known runtime config keys.
	ConfigKeyBatchSize        = "batch_size"
	ConfigKeyIndexingInterval = "indexing_interval_ms"
	ConfigKeyRPCURL           = "rpc_url"
	ConfigKeyIsActive         = "is_active"
)

// CoordinatorUpdater is the interface the config watcher uses to push
// parameter changes to a running coordinator.
type CoordinatorUpdater interface {
	UpdateBatchSize(newBatchSize int)
	UpdateInterval(newInterval time.Duration) bool
}

// ConfigWatcher polls the runtime_configs table and applies changes
// to the running pipeline coordinator without requiring a restart.
type ConfigWatcher struct {
	chain       model.Chain
	network     model.Network
	repo        store.RuntimeConfigRepository
	coordinator CoordinatorUpdater
	logger      *slog.Logger
	interval    time.Duration

	// Track last-seen values to avoid redundant updates.
	lastSeen map[string]string
}

func NewConfigWatcher(
	chain model.Chain,
	network model.Network,
	repo store.RuntimeConfigRepository,
	coordinator CoordinatorUpdater,
	logger *slog.Logger,
) *ConfigWatcher {
	return &ConfigWatcher{
		chain:       chain,
		network:     network,
		repo:        repo,
		coordinator: coordinator,
		logger:      logger.With("component", "config_watcher"),
		interval:    configWatcherDefaultInterval,
		lastSeen:    make(map[string]string),
	}
}

// Run starts the config watcher loop. It blocks until the context is cancelled.
func (w *ConfigWatcher) Run(ctx context.Context) error {
	w.logger.Info("config watcher started",
		"chain", w.chain,
		"network", w.network,
		"poll_interval", w.interval,
	)

	// Initial load.
	w.poll(ctx)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("config watcher stopping")
			return ctx.Err()
		case <-ticker.C:
			w.poll(ctx)
		}
	}
}

func (w *ConfigWatcher) poll(ctx context.Context) {
	configs, err := w.repo.GetActive(ctx, w.chain, w.network)
	if err != nil {
		w.logger.Warn("config watcher poll failed", "error", err)
		return
	}

	for key, value := range configs {
		if w.lastSeen[key] == value {
			continue
		}

		w.logger.Info("runtime config changed",
			"key", key,
			"old_value", w.lastSeen[key],
			"new_value", value,
		)

		w.applyConfig(key, value)
		w.lastSeen[key] = value
	}
}

func (w *ConfigWatcher) applyConfig(key, value string) {
	switch strings.TrimSpace(key) {
	case ConfigKeyBatchSize:
		if v, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && v > 0 {
			w.coordinator.UpdateBatchSize(v)
		} else {
			w.logger.Warn("invalid batch_size value", "value", value)
		}

	case ConfigKeyIndexingInterval:
		if v, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && v > 0 {
			w.coordinator.UpdateInterval(time.Duration(v) * time.Millisecond)
		} else {
			w.logger.Warn("invalid indexing_interval_ms value", "value", value)
		}

	default:
		w.logger.Debug("unhandled runtime config key", "key", key, "value", value)
	}
}
