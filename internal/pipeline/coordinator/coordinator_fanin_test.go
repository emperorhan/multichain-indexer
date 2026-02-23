package coordinator

import (
	"context"
	"log/slog"
	"sort"
	"testing"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	storemocks "github.com/emperorhan/multichain-indexer/internal/store/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestTick_FanInOverlapDedupesAcrossMandatoryChains(t *testing.T) {
	// In block-scan mode, the coordinator does not perform per-address dedup.
	// It emits a single job per tick containing all watched addresses from the repo.
	type testCase struct {
		name              string
		chain             model.Chain
		network           model.Network
		addresses         []model.WatchedAddress
		expectedAddresses []string
	}

	walletA := "wallet-a"
	orgA := "org-a"
	walletB := "wallet-b"
	orgB := "org-b"

	baseLower := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	baseUpper := "0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

	solanaAddr := "7nYBpkEPkDD6m1JKBGwvftG7bHjJErJPjTH3VbKpump"
	solanaAddrLag := " " + solanaAddr

	tests := []testCase{
		{
			name:    "base-sepolia-canonical-overlap",
			chain:   model.ChainBase,
			network: model.NetworkSepolia,
			addresses: []model.WatchedAddress{
				{Address: baseUpper, WalletID: &walletA, OrganizationID: &orgA},
				{Address: baseLower, WalletID: &walletB, OrganizationID: &orgB},
			},
			expectedAddresses: []string{baseUpper, baseLower},
		},
		{
			name:    "solana-devnet-lagging-overlap",
			chain:   model.ChainSolana,
			network: model.NetworkDevnet,
			addresses: []model.WatchedAddress{
				{Address: solanaAddr, WalletID: &walletA, OrganizationID: &orgA},
				{Address: solanaAddrLag, WalletID: &walletB, OrganizationID: &orgB},
			},
			expectedAddresses: []string{solanaAddr, solanaAddrLag},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockWatchedAddr := storemocks.NewMockWatchedAddressRepository(ctrl)
			jobCh := make(chan event.FetchJob, 10)
			c := New(
				tc.chain, tc.network,
				mockWatchedAddr,
				100, time.Second,
				jobCh, slog.Default(),
			)

			mockWatchedAddr.EXPECT().
				GetActive(gomock.Any(), tc.chain, tc.network).
				Return(tc.addresses, nil)

			require.NoError(t, c.tick(context.Background()))
			require.Len(t, jobCh, 1)

			job := <-jobCh
			assert.Equal(t, tc.chain, job.Chain)
			assert.Equal(t, tc.network, job.Network)
			assert.True(t, job.BlockScanMode)
			assert.Equal(t, 100, job.BatchSize)
			assert.ElementsMatch(t, tc.expectedAddresses, job.WatchedAddresses)
		})
	}
}

func TestTick_FanInRepresentativeAliasCarryoverDeterministicAcrossMandatoryChains(t *testing.T) {
	// In block-scan mode, per-address alias dedup and representative selection
	// do not apply at the coordinator level. The single block-scan job always
	// contains all watched addresses as returned by the repo. This test verifies
	// that both permutations produce the same set of watched addresses.
	type testCase struct {
		name              string
		chain             model.Chain
		network           model.Network
		permA             []model.WatchedAddress
		permB             []model.WatchedAddress
		expectedAddresses []string
	}

	baseCanonical := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	baseAlias := " 0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	solCanonical := "7nYBpkEPkDD6m1JKBGwvftG7bHjJErJPjTH3VbKpump"
	solAlias := " " + solCanonical + " "
	btcCanonical := "tb1qcanonicalaliasaddress000000000000000000"
	btcAlias := " " + btcCanonical
	wallet := "wallet-carryover"
	org := "org-carryover"

	tests := []testCase{
		{
			name:              "base-sepolia",
			chain:             model.ChainBase,
			network:           model.NetworkSepolia,
			permA:             []model.WatchedAddress{{Address: baseAlias, WalletID: &wallet, OrganizationID: &org}, {Address: baseCanonical, WalletID: &wallet, OrganizationID: &org}},
			permB:             []model.WatchedAddress{{Address: baseCanonical, WalletID: &wallet, OrganizationID: &org}, {Address: baseAlias, WalletID: &wallet, OrganizationID: &org}},
			expectedAddresses: []string{baseAlias, baseCanonical},
		},
		{
			name:              "solana-devnet",
			chain:             model.ChainSolana,
			network:           model.NetworkDevnet,
			permA:             []model.WatchedAddress{{Address: solAlias, WalletID: &wallet, OrganizationID: &org}, {Address: solCanonical, WalletID: &wallet, OrganizationID: &org}},
			permB:             []model.WatchedAddress{{Address: solCanonical, WalletID: &wallet, OrganizationID: &org}, {Address: solAlias, WalletID: &wallet, OrganizationID: &org}},
			expectedAddresses: []string{solAlias, solCanonical},
		},
		{
			name:              "btc-testnet",
			chain:             model.ChainBTC,
			network:           model.NetworkTestnet,
			permA:             []model.WatchedAddress{{Address: btcAlias, WalletID: &wallet, OrganizationID: &org}, {Address: btcCanonical, WalletID: &wallet, OrganizationID: &org}},
			permB:             []model.WatchedAddress{{Address: btcCanonical, WalletID: &wallet, OrganizationID: &org}, {Address: btcAlias, WalletID: &wallet, OrganizationID: &org}},
			expectedAddresses: []string{btcAlias, btcCanonical},
		},
	}

	run := func(t *testing.T, tc testCase, addresses []model.WatchedAddress) event.FetchJob {
		t.Helper()
		ctrl := gomock.NewController(t)
		mockWatchedAddr := storemocks.NewMockWatchedAddressRepository(ctrl)
		jobCh := make(chan event.FetchJob, 10)

		c := New(
			tc.chain, tc.network,
			mockWatchedAddr,
			100, time.Second,
			jobCh, slog.Default(),
		)

		mockWatchedAddr.EXPECT().
			GetActive(gomock.Any(), tc.chain, tc.network).
			Return(addresses, nil)

		require.NoError(t, c.tick(context.Background()))
		require.Len(t, jobCh, 1)
		return <-jobCh
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			jobA := run(t, tc, tc.permA)
			jobB := run(t, tc, tc.permB)

			assert.True(t, jobA.BlockScanMode)
			assert.True(t, jobB.BlockScanMode)
			assert.ElementsMatch(t, tc.expectedAddresses, jobA.WatchedAddresses)
			assert.ElementsMatch(t, tc.expectedAddresses, jobB.WatchedAddresses)
		})
	}
}

func TestTick_FanInLagAwareMembershipChurnReplayResumeAcrossMandatoryChains(t *testing.T) {
	// In block-scan mode, per-address lag-aware cursor dedup does not apply.
	// The coordinator produces a single block-scan job per tick with whatever
	// addresses the watched-address repo returns. This test verifies that
	// changing address membership across ticks produces the expected set of
	// watched addresses in each job, and that both permutations produce
	// block-scan jobs with the same address sets.
	type testCase struct {
		name    string
		chain   model.Chain
		network model.Network
		ticksA  [][]model.WatchedAddress
		ticksB  [][]model.WatchedAddress
	}

	baseUpper := "0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	baseLower := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	basePlain := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	solBase := "7nYBpkEPkDD6m1JKBGwvftG7bHjJErJPjTH3VbKpump"
	solLag := " " + solBase
	solNew := solBase + " "

	tests := []testCase{
		{
			name:    "base-sepolia",
			chain:   model.ChainBase,
			network: model.NetworkSepolia,
			ticksA: [][]model.WatchedAddress{
				{{Address: baseUpper}, {Address: baseLower}},
				{{Address: baseUpper}, {Address: baseLower}, {Address: basePlain}},
				{{Address: baseLower}, {Address: basePlain}},
				{{Address: basePlain}, {Address: baseLower}},
			},
			ticksB: [][]model.WatchedAddress{
				{{Address: baseLower}, {Address: baseUpper}},
				{{Address: basePlain}, {Address: baseUpper}, {Address: baseLower}},
				{{Address: basePlain}, {Address: baseLower}},
				{{Address: baseLower}, {Address: basePlain}},
			},
		},
		{
			name:    "solana-devnet",
			chain:   model.ChainSolana,
			network: model.NetworkDevnet,
			ticksA: [][]model.WatchedAddress{
				{{Address: solBase}, {Address: solLag}},
				{{Address: solBase}, {Address: solLag}, {Address: solNew}},
				{{Address: solLag}, {Address: solNew}},
				{{Address: solNew}, {Address: solLag}},
			},
			ticksB: [][]model.WatchedAddress{
				{{Address: solLag}, {Address: solBase}},
				{{Address: solNew}, {Address: solBase}, {Address: solLag}},
				{{Address: solNew}, {Address: solLag}},
				{{Address: solLag}, {Address: solNew}},
			},
		},
	}

	runBlockScanTicks := func(t *testing.T, chain model.Chain, network model.Network, ticks [][]model.WatchedAddress) [][]string {
		t.Helper()
		watchedRepo := &scriptedWatchedAddressRepo{ticks: ticks}
		jobCh := make(chan event.FetchJob, len(ticks)+1)
		c := New(chain, network, watchedRepo, 100, time.Second, jobCh, slog.Default())

		result := make([][]string, 0, len(ticks))
		for range ticks {
			require.NoError(t, c.tick(context.Background()))
			require.Len(t, jobCh, 1)
			job := <-jobCh
			assert.True(t, job.BlockScanMode)
			// Sort addresses for deterministic comparison.
			addrs := append([]string(nil), job.WatchedAddresses...)
			sort.Strings(addrs)
			result = append(result, addrs)
		}
		return result
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			resultA := runBlockScanTicks(t, tc.chain, tc.network, tc.ticksA)
			resultB := runBlockScanTicks(t, tc.chain, tc.network, tc.ticksB)
			// Both permutations must produce the same address sets per tick.
			require.Len(t, resultA, len(resultB))
			for i := range resultA {
				assert.Equal(t, resultA[i], resultB[i], "tick %d address sets must match regardless of input order", i)
			}
		})
	}
}

func TestTick_FanInDoesNotCollapseDistinctSolanaAddresses(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockWatchedAddr := storemocks.NewMockWatchedAddressRepository(ctrl)
	jobCh := make(chan event.FetchJob, 10)
	c := New(
		model.ChainSolana, model.NetworkDevnet,
		mockWatchedAddr,
		100, time.Second,
		jobCh, slog.Default(),
	)

	addrA := "AbCdEfGh123456789ABCDEFGH123456789"
	addrB := "aBcDeFgH123456789ABCDEFGH123456789"

	mockWatchedAddr.EXPECT().
		GetActive(gomock.Any(), model.ChainSolana, model.NetworkDevnet).
		Return([]model.WatchedAddress{
			{Address: addrA},
			{Address: addrB},
		}, nil)

	require.NoError(t, c.tick(context.Background()))
	require.Len(t, jobCh, 1)

	job := <-jobCh
	assert.True(t, job.BlockScanMode)
	// Both distinct addresses must appear in the single block-scan job.
	assert.ElementsMatch(t, []string{addrA, addrB}, job.WatchedAddresses)
}
