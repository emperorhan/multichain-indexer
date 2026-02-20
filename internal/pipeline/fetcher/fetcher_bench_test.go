package fetcher

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/cache"
	"github.com/emperorhan/multichain-indexer/internal/chain"
	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
)

// benchAdapter is a minimal ChainAdapter that returns pre-built responses
// without any network I/O, so benchmarks measure pipeline overhead only.
type benchAdapter struct {
	signatures []chain.SignatureInfo
	rawTxs     []json.RawMessage
}

func (a *benchAdapter) Chain() string { return "solana" }

func (a *benchAdapter) GetHeadSequence(context.Context) (int64, error) {
	if len(a.signatures) == 0 {
		return 0, nil
	}
	return a.signatures[len(a.signatures)-1].Sequence, nil
}

func (a *benchAdapter) FetchNewSignatures(_ context.Context, _ string, _ *string, batchSize int) ([]chain.SignatureInfo, error) {
	if batchSize >= len(a.signatures) {
		return a.signatures, nil
	}
	return a.signatures[:batchSize], nil
}

func (a *benchAdapter) FetchTransactions(_ context.Context, hashes []string) ([]json.RawMessage, error) {
	if len(hashes) >= len(a.rawTxs) {
		return a.rawTxs, nil
	}
	return a.rawTxs[:len(hashes)], nil
}

// newBenchAdapter creates a benchAdapter with n pre-built signatures and transactions.
func newBenchAdapter(n int) *benchAdapter {
	now := time.Now()
	sigs := make([]chain.SignatureInfo, n)
	txs := make([]json.RawMessage, n)
	for i := 0; i < n; i++ {
		sigs[i] = chain.SignatureInfo{
			Hash:     fmt.Sprintf("sig-%06d", i),
			Sequence: int64(1000 + i),
			Time:     &now,
		}
		txs[i] = json.RawMessage(fmt.Sprintf(`{"tx":%d}`, i))
	}
	return &benchAdapter{signatures: sigs, rawTxs: txs}
}

// silentLogger returns a logger that discards all output.
func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(discard{}, nil))
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

// BenchmarkFetchSignatures measures the overhead of the signature-fetching path
// through processJob (canonicalization, dedup, boundary suppression) without
// real network I/O.
func BenchmarkFetchSignatures(b *testing.B) {
	for _, size := range []int{10, 100, 500} {
		b.Run(fmt.Sprintf("sigs=%d", size), func(b *testing.B) {
			adapter := newBenchAdapter(size)
			rawBatchCh := make(chan event.RawBatch, 1)
			logger := silentLogger()

			f := &Fetcher{
				adapter:          adapter,
				rawBatchCh:       rawBatchCh,
				logger:           logger,
				retryMaxAttempts: 1,
			}

			job := event.FetchJob{
				Chain:     model.ChainSolana,
				Network:   model.NetworkDevnet,
				Address:   "bench-addr",
				BatchSize: size,
			}

			ctx := context.Background()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = f.processJob(ctx, logger, job)
				<-rawBatchCh
			}
		})
	}
}

// BenchmarkFetchTransactions measures the full processJob path (signature fetch
// + transaction fetch + batch assembly) with varying batch sizes.
func BenchmarkFetchTransactions(b *testing.B) {
	for _, size := range []int{10, 100, 500} {
		b.Run(fmt.Sprintf("txs=%d", size), func(b *testing.B) {
			adapter := newBenchAdapter(size)
			rawBatchCh := make(chan event.RawBatch, 1)
			logger := silentLogger()

			cursor := "sig-000000"
			f := &Fetcher{
				adapter:          adapter,
				rawBatchCh:       rawBatchCh,
				logger:           logger,
				retryMaxAttempts: 1,
			}

			job := event.FetchJob{
				Chain:          model.ChainSolana,
				Network:        model.NetworkDevnet,
				Address:        "bench-addr",
				CursorValue:    &cursor,
				CursorSequence: 1000,
				BatchSize:      size,
			}

			ctx := context.Background()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = f.processJob(ctx, logger, job)
				<-rawBatchCh
			}
		})
	}
}

// BenchmarkCanonicalizeSignatures measures the canonicalization + dedup of
// signature lists for different chain types.
func BenchmarkCanonicalizeSignatures(b *testing.B) {
	now := time.Now()

	buildSigs := func(n int, prefix string) []chain.SignatureInfo {
		sigs := make([]chain.SignatureInfo, n)
		for i := 0; i < n; i++ {
			sigs[i] = chain.SignatureInfo{
				Hash:     fmt.Sprintf("%s%06d", prefix, i),
				Sequence: int64(i),
				Time:     &now,
			}
		}
		return sigs
	}

	buildEVMSigs := func(n int) []chain.SignatureInfo {
		sigs := make([]chain.SignatureInfo, n)
		for i := 0; i < n; i++ {
			sigs[i] = chain.SignatureInfo{
				Hash:     fmt.Sprintf("0x%040x", i),
				Sequence: int64(i),
				Time:     &now,
			}
		}
		return sigs
	}

	benchCases := []struct {
		name  string
		chain model.Chain
		sigs  []chain.SignatureInfo
	}{
		{"solana/100", model.ChainSolana, buildSigs(100, "sig-")},
		{"solana/500", model.ChainSolana, buildSigs(500, "sig-")},
		{"base/100", model.ChainBase, buildEVMSigs(100)},
		{"base/500", model.ChainBase, buildEVMSigs(500)},
	}

	for _, bc := range benchCases {
		b.Run(bc.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				canonicalizeSignatures(bc.chain, bc.sigs)
			}
		})
	}
}

// BenchmarkSuppressBoundaryCursorSignatures measures cursor overlap filtering.
func BenchmarkSuppressBoundaryCursorSignatures(b *testing.B) {
	sigs := make([]chain.SignatureInfo, 200)
	for i := range sigs {
		sigs[i] = chain.SignatureInfo{
			Hash:     fmt.Sprintf("sig-%06d", i),
			Sequence: int64(1000 + i),
		}
	}
	cursor := "sig-000000"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		suppressBoundaryCursorSignatures(model.ChainSolana, sigs, &cursor, 1000)
	}
}

// BenchmarkSuppressPostCutoffSignatures measures cutoff filtering with varying
// fractions of signatures beyond the cutoff.
func BenchmarkSuppressPostCutoffSignatures(b *testing.B) {
	sigs := make([]chain.SignatureInfo, 500)
	for i := range sigs {
		sigs[i] = chain.SignatureInfo{
			Hash:     fmt.Sprintf("sig-%06d", i),
			Sequence: int64(1000 + i),
		}
	}

	for _, cutoff := range []int64{1250, 1400, 1499} {
		name := fmt.Sprintf("cutoff=%d/500", cutoff)
		b.Run(name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				suppressPostCutoffSignatures(sigs, cutoff)
			}
		})
	}
}

// BenchmarkSuppressPreCursorSequenceCarryover benchmarks the seam carryover
// suppression logic that merges and partitions signatures around the cursor
// boundary.
func BenchmarkSuppressPreCursorSequenceCarryover(b *testing.B) {
	sigs := make([]chain.SignatureInfo, 200)
	for i := range sigs {
		seq := int64(1000)
		if i >= 100 {
			seq = int64(1001 + i - 100)
		}
		sigs[i] = chain.SignatureInfo{
			Hash:     fmt.Sprintf("sig-%06d", i),
			Sequence: seq,
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		suppressPreCursorSequenceCarryover(sigs, 1000, 50)
	}
}

// BenchmarkReduceBatchSize measures the adaptive batch halving logic.
func BenchmarkReduceBatchSize(b *testing.B) {
	f := &Fetcher{adaptiveMinBatch: 1}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = f.reduceBatchSize(256)
	}
}

// BenchmarkRetryDelay measures the exponential backoff delay computation.
func BenchmarkRetryDelay(b *testing.B) {
	f := &Fetcher{
		backoffInitial: 200 * time.Millisecond,
		backoffMax:     3 * time.Second,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for attempt := 1; attempt <= 10; attempt++ {
			_ = f.retryDelay(attempt)
		}
	}
}

// BenchmarkCanonicalizeWatchedAddressIdentity measures address canonicalization
// across different chain types (Solana pass-through vs EVM hex lowering).
func BenchmarkCanonicalizeWatchedAddressIdentity(b *testing.B) {
	cases := []struct {
		name    string
		chain   model.Chain
		address string
	}{
		{"solana", model.ChainSolana, "  7xKXtg2CW87d97TXJSDpbD5jBkheTqA83TZRuJosgAsU  "},
		{"base", model.ChainBase, " 0xABCDEF1234567890ABCDEF1234567890ABCDEF12 "},
		{"btc", model.ChainBTC, " tb1qw508d6qejxtdg4y5r3zarvary0c5xw7kxpjzsx "},
	}

	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				canonicalizeWatchedAddressIdentity(c.chain, c.address)
			}
		})
	}
}

// BenchmarkResolveBatchSize measures the adaptive batch size resolution under
// contention (sequential calls simulating repeated job dispatch).
func BenchmarkResolveBatchSize(b *testing.B) {
	f := &Fetcher{
		adaptiveMinBatch:   1,
		batchSizeByAddress: cache.NewLRU[string, int](10000, time.Hour),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f.resolveBatchSize(model.ChainSolana, model.NetworkDevnet, "bench-addr", 100)
	}
}
