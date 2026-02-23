package finalizer

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
)

// silentLogger returns a logger that discards all output.
func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(benchDiscard{}, nil))
}

type benchDiscard struct{}

func (benchDiscard) Write(p []byte) (int, error) { return len(p), nil }

// BenchmarkFinalizerCheck measures a single check() call where the finalized
// block has advanced (promotion is sent, no pruning).
func BenchmarkFinalizerCheck(b *testing.B) {
	adapter := &fakeReorgAwareAdapter{
		chainName:      "base",
		finalizedBlock: 10000,
	}
	blockRepo := &fakeBlockRepo{}
	finalityCh := make(chan event.FinalityPromotion, b.N+1)

	f := New(
		model.ChainBase, model.NetworkMainnet,
		adapter, blockRepo, finalityCh,
		time.Second, silentLogger(),
	)

	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		adapter.finalizedBlock = int64(10000 + i)
		_ = f.check(ctx)
	}
}

// BenchmarkFinalizerCheck_WithPruning measures check() when retention-based
// pruning fires after each promotion.
func BenchmarkFinalizerCheck_WithPruning(b *testing.B) {
	adapter := &fakeReorgAwareAdapter{
		chainName:      "base",
		finalizedBlock: 20000,
	}
	blockRepo := &fakeBlockRepo{purgeCount: 100}
	finalityCh := make(chan event.FinalityPromotion, b.N+1)

	f := New(
		model.ChainBase, model.NetworkMainnet,
		adapter, blockRepo, finalityCh,
		time.Second, silentLogger(),
		WithRetentionBlocks(10000),
	)

	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		adapter.finalizedBlock = int64(20000 + i)
		_ = f.check(ctx)
	}
}
