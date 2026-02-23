package coordinator

import (
	"context"
	"fmt"
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

// buildWatchedAddresses creates n watched addresses with deterministic names.
func buildWatchedAddresses(n int) []model.WatchedAddress {
	addrs := make([]model.WatchedAddress, n)
	for i := range addrs {
		addrs[i] = model.WatchedAddress{Address: fmt.Sprintf("0x%040d", i)}
	}
	return addrs
}

// BenchmarkTickBlockScan measures a single tickBlockScan call with 10 watched
// addresses, watermark=9900, head=10000.
func BenchmarkTickBlockScan(b *testing.B) {
	addrs := buildWatchedAddresses(10)
	ticks := make([][]model.WatchedAddress, b.N)
	for i := range ticks {
		ticks[i] = addrs
	}

	watchedRepo := &scriptedWatchedAddressRepo{ticks: ticks}
	configRepo := &inMemoryConfigRepo{watermark: 9900}
	jobCh := make(chan event.FetchJob, b.N+1)

	c := New(
		model.ChainBase, model.NetworkMainnet,
		watchedRepo, 100, time.Second,
		jobCh, silentLogger(),
	).
		WithHeadProvider(&stubHeadProvider{head: 10000}).
		WithBlockScanMode(configRepo)

	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = c.tick(ctx)
	}
}

// BenchmarkTickBlockScan_LargeAddressSet measures tickBlockScan with 1000
// watched addresses to expose allocation scaling with address count.
func BenchmarkTickBlockScan_LargeAddressSet(b *testing.B) {
	addrs := buildWatchedAddresses(1000)
	ticks := make([][]model.WatchedAddress, b.N)
	for i := range ticks {
		ticks[i] = addrs
	}

	watchedRepo := &scriptedWatchedAddressRepo{ticks: ticks}
	configRepo := &inMemoryConfigRepo{watermark: 9900}
	jobCh := make(chan event.FetchJob, b.N+1)

	c := New(
		model.ChainBase, model.NetworkMainnet,
		watchedRepo, 100, time.Second,
		jobCh, silentLogger(),
	).
		WithHeadProvider(&stubHeadProvider{head: 10000}).
		WithBlockScanMode(configRepo)

	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = c.tick(ctx)
	}
}
