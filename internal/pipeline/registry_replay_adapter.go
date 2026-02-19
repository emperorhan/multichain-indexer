package pipeline

import (
	"context"

	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/replay"
	"github.com/emperorhan/multichain-indexer/internal/store"
)

// RegistryReplayAdapter adapts a Registry + replay.Service to satisfy
// the admin.ReplayRequester interface.
type RegistryReplayAdapter struct {
	registry      *Registry
	replayService *replay.Service
	configRepo    store.IndexerConfigRepository
}

// NewRegistryReplayAdapter creates a new adapter.
func NewRegistryReplayAdapter(registry *Registry, replayService *replay.Service, configRepo store.IndexerConfigRepository) *RegistryReplayAdapter {
	return &RegistryReplayAdapter{
		registry:      registry,
		replayService: replayService,
		configRepo:    configRepo,
	}
}

// HasPipeline returns true if a pipeline exists for the given chain/network.
func (a *RegistryReplayAdapter) HasPipeline(chain model.Chain, network model.Network) bool {
	return a.registry.Get(chain, network) != nil
}

// RequestReplay delegates to the pipeline's RequestReplay method, which stops
// the pipeline, runs the purge, and restarts it.
func (a *RegistryReplayAdapter) RequestReplay(ctx context.Context, req replay.PurgeRequest) (*replay.PurgeResult, error) {
	p := a.registry.Get(req.Chain, req.Network)
	if p == nil {
		return nil, nil
	}
	return p.RequestReplay(ctx, req)
}

// DryRunPurge runs a dry-run purge directly on the replay service without
// stopping the pipeline.
func (a *RegistryReplayAdapter) DryRunPurge(ctx context.Context, req replay.PurgeRequest) (*replay.PurgeResult, error) {
	req.DryRun = true
	return a.replayService.PurgeFromBlock(ctx, req)
}

// GetWatermark returns the current watermark for the given chain/network.
func (a *RegistryReplayAdapter) GetWatermark(ctx context.Context, chain model.Chain, network model.Network) (*model.PipelineWatermark, error) {
	return a.configRepo.GetWatermark(ctx, chain, network)
}
