package ingester

import (
	"context"
	"testing"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/addressindex"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/coordinator/autotune"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/replay"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	storemocks "github.com/emperorhan/multichain-indexer/internal/store/mocks"
)

// applyOptions creates a bare Ingester and applies the given options.
func applyOptions(opts ...Option) *Ingester {
	ing := &Ingester{}
	for _, opt := range opts {
		opt(ing)
	}
	return ing
}

// ---------------------------------------------------------------------------
// Option function application tests
// ---------------------------------------------------------------------------

func TestWithReplayService_SetsField(t *testing.T) {
	t.Parallel()
	svc := &replay.Service{}
	ing := applyOptions(WithReplayService(svc))
	assert.Same(t, svc, ing.replayService)
}

func TestWithAddressIndex_SetsField(t *testing.T) {
	t.Parallel()
	var idx addressindex.Index = stubAddressIndex{}
	ing := applyOptions(WithAddressIndex(idx))
	assert.Equal(t, idx, ing.addressIndex)
}

func TestWithAutoTuneSignalSink_SetsField(t *testing.T) {
	t.Parallel()
	sink := autotune.NewRuntimeSignalRegistry()
	ing := applyOptions(WithAutoTuneSignalSink(sink))
	assert.Same(t, sink, ing.autoTuneSignals)
}

func TestWithWatchedAddressRepo_SetsField(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	repo := storemocks.NewMockWatchedAddressRepository(ctrl)
	ing := applyOptions(WithWatchedAddressRepo(repo))
	assert.Same(t, repo, ing.watchedAddrRepo)
}

func TestWithBlockScanAddrCacheTTL_SetsField(t *testing.T) {
	t.Parallel()
	ttl := 45 * time.Second
	ing := applyOptions(WithBlockScanAddrCacheTTL(ttl))
	assert.Equal(t, ttl, ing.blockScanAddrCacheTTL)
}

// stubAddressIndex is a minimal no-op implementation of addressindex.Index.
type stubAddressIndex struct{}

func (stubAddressIndex) Contains(_ context.Context, _ model.Chain, _ model.Network, _ string) bool {
	return false
}
func (stubAddressIndex) Lookup(_ context.Context, _ model.Chain, _ model.Network, _ string) *model.WatchedAddress {
	return nil
}
func (stubAddressIndex) Reload(_ context.Context, _ model.Chain, _ model.Network) error {
	return nil
}

// ---------------------------------------------------------------------------
// Native token helper table tests
// ---------------------------------------------------------------------------

func TestNativeTokenHelpers(t *testing.T) {
	t.Parallel()

	type expected struct {
		contract string
		symbol   string
		name     string
		decimals int
	}

	tests := []struct {
		chain model.Chain
		want  expected
	}{
		{
			chain: model.ChainSolana,
			want: expected{
				contract: "So11111111111111111111111111111111111111112",
				symbol:   "SOL",
				name:     "Solana",
				decimals: 9,
			},
		},
		{
			chain: model.ChainEthereum,
			want: expected{
				contract: "0x0000000000000000000000000000000000000000",
				symbol:   "ETH",
				name:     "Ether",
				decimals: 18,
			},
		},
		{
			chain: model.ChainBase,
			want: expected{
				contract: "0x0000000000000000000000000000000000000000",
				symbol:   "ETH",
				name:     "Ether",
				decimals: 18,
			},
		},
		{
			chain: model.ChainArbitrum,
			want: expected{
				contract: "0x0000000000000000000000000000000000000000",
				symbol:   "ETH",
				name:     "Ether",
				decimals: 18,
			},
		},
		{
			chain: model.ChainPolygon,
			want: expected{
				contract: "0x0000000000000000000000000000000000000000",
				symbol:   "MATIC",
				name:     "MATIC",
				decimals: 18,
			},
		},
		{
			chain: model.ChainBSC,
			want: expected{
				contract: "0x0000000000000000000000000000000000000000",
				symbol:   "BNB",
				name:     "BNB",
				decimals: 18,
			},
		},
		{
			chain: model.ChainBTC,
			want: expected{
				contract: "btc_native",
				symbol:   "BTC",
				name:     "Bitcoin",
				decimals: 8,
			},
		},
		{
			chain: model.Chain("unknown"),
			want: expected{
				contract: "0x0000000000000000000000000000000000000000",
				symbol:   "NATIVE",
				name:     "Native Token",
				decimals: 18,
			},
		},
	}

	for _, tc := range tests {
		t.Run(string(tc.chain), func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want.contract, nativeTokenContract(tc.chain), "contract")
			assert.Equal(t, tc.want.symbol, nativeTokenSymbol(tc.chain), "symbol")
			assert.Equal(t, tc.want.name, nativeTokenName(tc.chain), "name")
			assert.Equal(t, tc.want.decimals, nativeTokenDecimals(tc.chain), "decimals")
		})
	}
}

// ---------------------------------------------------------------------------
// cancelWaiter test
// ---------------------------------------------------------------------------

func TestCancelWaiter_DecrementsAndSignals(t *testing.T) {
	t.Parallel()

	d := newDeterministicCommitInterleaver(
		[]interleaveTarget{
			{chain: model.ChainSolana, network: model.NetworkDevnet},
		},
		100*time.Millisecond,
	).(*deterministicMandatoryChainInterleaver)

	key := commitInterleaveKey(model.ChainSolana, model.NetworkDevnet)

	// Seed the waiting counter.
	d.mu.Lock()
	d.waiting[key] = 2
	d.mu.Unlock()

	// First decrement: 2 → 1
	d.cancelWaiter(key)
	d.mu.Lock()
	require.Equal(t, 1, d.waiting[key])
	d.mu.Unlock()

	// Second decrement: 1 → 0
	d.cancelWaiter(key)
	d.mu.Lock()
	require.Equal(t, 0, d.waiting[key])
	d.mu.Unlock()

	// Third call is a no-op: still 0
	d.cancelWaiter(key)
	d.mu.Lock()
	require.Equal(t, 0, d.waiting[key])
	d.mu.Unlock()
}

// ---------------------------------------------------------------------------
// derefStr test
// ---------------------------------------------------------------------------

func TestDerefStr(t *testing.T) {
	t.Parallel()

	t.Run("nil", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, "", derefStr(nil))
	})

	t.Run("non-nil", func(t *testing.T) {
		t.Parallel()
		s := "hello"
		assert.Equal(t, "hello", derefStr(&s))
	})
}

