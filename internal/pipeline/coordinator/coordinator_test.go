package coordinator

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	storemocks "github.com/emperorhan/multichain-indexer/internal/store/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

type stubHeadProvider struct {
	head  int64
	heads []int64
	err   error
	calls int
	idx   int
}

func (s *stubHeadProvider) GetHeadSequence(context.Context) (int64, error) {
	s.calls++
	if s.err != nil {
		return 0, s.err
	}
	if len(s.heads) > 0 {
		if s.idx >= len(s.heads) {
			return s.heads[len(s.heads)-1], nil
		}
		head := s.heads[s.idx]
		s.idx++
		return head, nil
	}
	return s.head, nil
}

func TestTick_HappyPath(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockWatchedAddr := storemocks.NewMockWatchedAddressRepository(ctrl)
	mockCursor := storemocks.NewMockCursorRepository(ctrl)

	jobCh := make(chan event.FetchJob, 10)
	c := New(
		model.ChainSolana, model.NetworkDevnet,
		mockWatchedAddr, mockCursor,
		100, time.Second,
		jobCh, slog.Default(),
	)

	walletID := "wallet-1"
	orgID := "org-1"
	cursorVal := "lastSig"

	mockWatchedAddr.EXPECT().
		GetActive(gomock.Any(), model.ChainSolana, model.NetworkDevnet).
		Return([]model.WatchedAddress{
			{Address: "addr1", WalletID: &walletID, OrganizationID: &orgID},
			{Address: "addr2"},
		}, nil)

	mockCursor.EXPECT().
		Get(gomock.Any(), model.ChainSolana, model.NetworkDevnet, "addr1").
		Return(&model.AddressCursor{
			CursorValue:    &cursorVal,
			CursorSequence: 100,
		}, nil)

	mockCursor.EXPECT().
		Get(gomock.Any(), model.ChainSolana, model.NetworkDevnet, "addr2").
		Return(nil, nil)

	err := c.tick(context.Background())
	require.NoError(t, err)

	require.Len(t, jobCh, 2)

	job1 := <-jobCh
	assert.Equal(t, model.ChainSolana, job1.Chain)
	assert.Equal(t, model.NetworkDevnet, job1.Network)
	assert.Equal(t, "addr1", job1.Address)
	assert.Equal(t, &cursorVal, job1.CursorValue)
	assert.Equal(t, 100, job1.BatchSize)
	assert.Equal(t, int64(100), job1.CursorSequence)
	assert.Equal(t, &walletID, job1.WalletID)
	assert.Equal(t, &orgID, job1.OrgID)

	job2 := <-jobCh
	assert.Equal(t, "addr2", job2.Address)
	assert.Nil(t, job2.CursorValue)
}

func TestTick_NoAddresses(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockWatchedAddr := storemocks.NewMockWatchedAddressRepository(ctrl)
	mockCursor := storemocks.NewMockCursorRepository(ctrl)

	jobCh := make(chan event.FetchJob, 10)
	c := New(
		model.ChainSolana, model.NetworkDevnet,
		mockWatchedAddr, mockCursor,
		100, time.Second,
		jobCh, slog.Default(),
	)

	mockWatchedAddr.EXPECT().
		GetActive(gomock.Any(), model.ChainSolana, model.NetworkDevnet).
		Return([]model.WatchedAddress{}, nil)

	err := c.tick(context.Background())
	require.NoError(t, err)
	assert.Empty(t, jobCh)
}

func TestTick_WithHeadProviderPinsSingleCutoffAcrossJobs(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockWatchedAddr := storemocks.NewMockWatchedAddressRepository(ctrl)
	mockCursor := storemocks.NewMockCursorRepository(ctrl)

	jobCh := make(chan event.FetchJob, 10)
	headProvider := &stubHeadProvider{head: 777}
	c := New(
		model.ChainSolana, model.NetworkDevnet,
		mockWatchedAddr, mockCursor,
		100, time.Second,
		jobCh, slog.Default(),
	).WithHeadProvider(headProvider)

	mockWatchedAddr.EXPECT().
		GetActive(gomock.Any(), model.ChainSolana, model.NetworkDevnet).
		Return([]model.WatchedAddress{
			{Address: "addr1"},
			{Address: "addr2"},
		}, nil)

	mockCursor.EXPECT().
		Get(gomock.Any(), model.ChainSolana, model.NetworkDevnet, "addr1").
		Return(nil, nil)
	mockCursor.EXPECT().
		Get(gomock.Any(), model.ChainSolana, model.NetworkDevnet, "addr2").
		Return(nil, nil)

	require.NoError(t, c.tick(context.Background()))
	require.Len(t, jobCh, 2)
	assert.Equal(t, 1, headProvider.calls)

	job1 := <-jobCh
	job2 := <-jobCh
	assert.Equal(t, int64(777), job1.FetchCutoffSeq)
	assert.Equal(t, int64(777), job2.FetchCutoffSeq)
}

func TestTick_WithHeadProviderErrorFailsFast(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockWatchedAddr := storemocks.NewMockWatchedAddressRepository(ctrl)
	mockCursor := storemocks.NewMockCursorRepository(ctrl)

	jobCh := make(chan event.FetchJob, 10)
	headProvider := &stubHeadProvider{err: errors.New("head unavailable")}
	c := New(
		model.ChainBase, model.NetworkSepolia,
		mockWatchedAddr, mockCursor,
		100, time.Second,
		jobCh, slog.Default(),
	).WithHeadProvider(headProvider)

	mockWatchedAddr.EXPECT().
		GetActive(gomock.Any(), model.ChainBase, model.NetworkSepolia).
		Return([]model.WatchedAddress{
			{Address: "0x1111111111111111111111111111111111111111"},
		}, nil)

	err := c.tick(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "resolve tick cutoff head")
	assert.Empty(t, jobCh)
}

func TestTick_GetActiveError(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockWatchedAddr := storemocks.NewMockWatchedAddressRepository(ctrl)
	mockCursor := storemocks.NewMockCursorRepository(ctrl)

	jobCh := make(chan event.FetchJob, 10)
	c := New(
		model.ChainSolana, model.NetworkDevnet,
		mockWatchedAddr, mockCursor,
		100, time.Second,
		jobCh, slog.Default(),
	)

	mockWatchedAddr.EXPECT().
		GetActive(gomock.Any(), model.ChainSolana, model.NetworkDevnet).
		Return(nil, errors.New("db connection lost"))

	err := c.tick(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "db connection lost")
}

func TestTick_CursorGetError_FailsFast(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockWatchedAddr := storemocks.NewMockWatchedAddressRepository(ctrl)
	mockCursor := storemocks.NewMockCursorRepository(ctrl)

	jobCh := make(chan event.FetchJob, 10)
	c := New(
		model.ChainSolana, model.NetworkDevnet,
		mockWatchedAddr, mockCursor,
		100, time.Second,
		jobCh, slog.Default(),
	)

	mockWatchedAddr.EXPECT().
		GetActive(gomock.Any(), model.ChainSolana, model.NetworkDevnet).
		Return([]model.WatchedAddress{
			{Address: "addr1"},
			{Address: "addr2"},
		}, nil)

	mockCursor.EXPECT().
		Get(gomock.Any(), model.ChainSolana, model.NetworkDevnet, "addr1").
		Return(nil, errors.New("cursor db error"))

	err := c.tick(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cursor db error")
	assert.Empty(t, jobCh)
}

func TestRun_PanicsOnTickError(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockWatchedAddr := storemocks.NewMockWatchedAddressRepository(ctrl)
	mockCursor := storemocks.NewMockCursorRepository(ctrl)

	jobCh := make(chan event.FetchJob, 10)
	c := New(
		model.ChainSolana, model.NetworkDevnet,
		mockWatchedAddr, mockCursor,
		100, time.Second,
		jobCh, slog.Default(),
	)

	mockWatchedAddr.EXPECT().
		GetActive(gomock.Any(), model.ChainSolana, model.NetworkDevnet).
		Return(nil, errors.New("db connection lost"))

	require.Panics(t, func() {
		_ = c.Run(context.Background())
	})
}

func TestRun_PanicsOnTickError_WithAutoTuneEnabled(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockWatchedAddr := storemocks.NewMockWatchedAddressRepository(ctrl)
	mockCursor := storemocks.NewMockCursorRepository(ctrl)

	jobCh := make(chan event.FetchJob, 10)
	c := New(
		model.ChainSolana, model.NetworkDevnet,
		mockWatchedAddr, mockCursor,
		100, time.Second,
		jobCh, slog.Default(),
	).WithAutoTune(AutoTuneConfig{
		Enabled:               true,
		MinBatchSize:          50,
		MaxBatchSize:          200,
		StepUp:                10,
		StepDown:              10,
		LagHighWatermark:      100,
		LagLowWatermark:       20,
		QueueHighWatermarkPct: 80,
		QueueLowWatermarkPct:  30,
		HysteresisTicks:       1,
	})

	mockWatchedAddr.EXPECT().
		GetActive(gomock.Any(), model.ChainSolana, model.NetworkDevnet).
		Return(nil, errors.New("db connection lost"))

	require.Panics(t, func() {
		_ = c.Run(context.Background())
	})
}

func TestTick_ContextCanceled(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockWatchedAddr := storemocks.NewMockWatchedAddressRepository(ctrl)
	mockCursor := storemocks.NewMockCursorRepository(ctrl)

	jobCh := make(chan event.FetchJob) // unbuffered, will block
	c := New(
		model.ChainSolana, model.NetworkDevnet,
		mockWatchedAddr, mockCursor,
		100, time.Second,
		jobCh, slog.Default(),
	)

	mockWatchedAddr.EXPECT().
		GetActive(gomock.Any(), model.ChainSolana, model.NetworkDevnet).
		Return([]model.WatchedAddress{
			{Address: "addr1"},
		}, nil)

	mockCursor.EXPECT().
		Get(gomock.Any(), model.ChainSolana, model.NetworkDevnet, "addr1").
		Return(nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := c.tick(ctx)
	require.Error(t, err)
	assert.Equal(t, context.Canceled, err)
}

func TestTick_FanInOverlapDedupesAcrossMandatoryChains(t *testing.T) {
	type testCase struct {
		name        string
		chain       model.Chain
		network     model.Network
		addresses   []model.WatchedAddress
		cursorByKey map[string]*model.AddressCursor
		expectedJob event.FetchJob
	}

	walletA := "wallet-a"
	orgA := "org-a"
	walletB := "wallet-b"
	orgB := "org-b"

	baseLower := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	baseUpper := "0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	baseCursorA := "0xabcDEF"
	baseCursorB := "abcdef"

	solanaAddr := "7nYBpkEPkDD6m1JKBGwvftG7bHjJErJPjTH3VbKpump"
	solanaAddrLag := " " + solanaAddr
	solCursorA := "sig-sol-12"
	solCursorB := " sig-sol-10 "

	tests := []testCase{
		{
			name:    "base-sepolia-canonical-overlap",
			chain:   model.ChainBase,
			network: model.NetworkSepolia,
			addresses: []model.WatchedAddress{
				{Address: baseUpper, WalletID: &walletA, OrganizationID: &orgA},
				{Address: baseLower, WalletID: &walletB, OrganizationID: &orgB},
			},
			cursorByKey: map[string]*model.AddressCursor{
				baseUpper: {
					Address:        baseUpper,
					CursorValue:    &baseCursorA,
					CursorSequence: 10,
				},
				baseLower: {
					Address:        baseLower,
					CursorValue:    &baseCursorB,
					CursorSequence: 12,
				},
			},
			expectedJob: event.FetchJob{
				Chain:          model.ChainBase,
				Network:        model.NetworkSepolia,
				Address:        baseUpper,
				CursorValue:    strPtr("0xabcdef"),
				CursorSequence: 10,
				BatchSize:      100,
				WalletID:       &walletA,
				OrgID:          &orgA,
			},
		},
		{
			name:    "solana-devnet-lagging-overlap",
			chain:   model.ChainSolana,
			network: model.NetworkDevnet,
			addresses: []model.WatchedAddress{
				{Address: solanaAddr, WalletID: &walletA, OrganizationID: &orgA},
				{Address: solanaAddrLag, WalletID: &walletB, OrganizationID: &orgB},
			},
			cursorByKey: map[string]*model.AddressCursor{
				solanaAddr: {
					Address:        solanaAddr,
					CursorValue:    &solCursorA,
					CursorSequence: 12,
				},
				solanaAddrLag: {
					Address:        solanaAddrLag,
					CursorValue:    &solCursorB,
					CursorSequence: 10,
				},
			},
			expectedJob: event.FetchJob{
				Chain:          model.ChainSolana,
				Network:        model.NetworkDevnet,
				Address:        solanaAddrLag,
				CursorValue:    strPtr("sig-sol-10"),
				CursorSequence: 10,
				BatchSize:      100,
				WalletID:       &walletB,
				OrgID:          &orgB,
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockWatchedAddr := storemocks.NewMockWatchedAddressRepository(ctrl)
			mockCursor := storemocks.NewMockCursorRepository(ctrl)

			jobCh := make(chan event.FetchJob, 10)
			c := New(
				tc.chain, tc.network,
				mockWatchedAddr, mockCursor,
				100, time.Second,
				jobCh, slog.Default(),
			)

			mockWatchedAddr.EXPECT().
				GetActive(gomock.Any(), tc.chain, tc.network).
				Return(tc.addresses, nil)

			mockCursor.EXPECT().
				Get(gomock.Any(), tc.chain, tc.network, gomock.Any()).
				DoAndReturn(func(_ context.Context, _ model.Chain, _ model.Network, address string) (*model.AddressCursor, error) {
					cursor, ok := tc.cursorByKey[address]
					if !ok {
						return nil, nil
					}
					return cursor, nil
				}).
				Times(len(tc.addresses))

			require.NoError(t, c.tick(context.Background()))
			require.Len(t, jobCh, 1)

			job := <-jobCh
			assert.Equal(t, tc.expectedJob.Chain, job.Chain)
			assert.Equal(t, tc.expectedJob.Network, job.Network)
			assert.Equal(t, tc.expectedJob.Address, job.Address)
			assert.Equal(t, tc.expectedJob.CursorSequence, job.CursorSequence)
			assert.Equal(t, tc.expectedJob.BatchSize, job.BatchSize)
			assert.Equal(t, tc.expectedJob.WalletID, job.WalletID)
			assert.Equal(t, tc.expectedJob.OrgID, job.OrgID)
			if tc.expectedJob.CursorValue == nil {
				assert.Nil(t, job.CursorValue)
			} else {
				require.NotNil(t, job.CursorValue)
				assert.Equal(t, *tc.expectedJob.CursorValue, *job.CursorValue)
			}
		})
	}
}

func TestTick_FanInOrderVarianceDeterministicAcrossMandatoryChains(t *testing.T) {
	type testCase struct {
		name        string
		chain       model.Chain
		network     model.Network
		addressesA  []model.WatchedAddress
		addressesB  []model.WatchedAddress
		cursorByKey map[string]*model.AddressCursor
	}

	walletA := "wallet-a"
	walletB := "wallet-b"

	baseAliasUpper := "0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	baseAliasLower := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	baseOther := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	baseAliasCursor := "abcdef"
	baseOtherCursor := "0x1234"

	solAlias := "7nYBpkEPkDD6m1JKBGwvftG7bHjJErJPjTH3VbKpump"
	solOther := "9xQeWvG816bUx9EPf7R3mNq8K6V3A8wH2fJ9Q9q5Y8V"
	solAliasCursor := "sig-sol-200"
	solOtherCursor := "sig-sol-201"

	tests := []testCase{
		{
			name:    "base-sepolia",
			chain:   model.ChainBase,
			network: model.NetworkSepolia,
			addressesA: []model.WatchedAddress{
				{Address: baseAliasUpper, WalletID: &walletA},
				{Address: baseOther, WalletID: &walletB},
				{Address: baseAliasLower, WalletID: &walletB},
			},
			addressesB: []model.WatchedAddress{
				{Address: baseAliasLower, WalletID: &walletB},
				{Address: baseAliasUpper, WalletID: &walletA},
				{Address: baseOther, WalletID: &walletB},
			},
			cursorByKey: map[string]*model.AddressCursor{
				baseAliasUpper: {Address: baseAliasUpper, CursorValue: strPtr(baseAliasCursor), CursorSequence: 300},
				baseAliasLower: {Address: baseAliasLower, CursorValue: strPtr("0xABCDEF"), CursorSequence: 299},
				baseOther:      {Address: baseOther, CursorValue: strPtr(baseOtherCursor), CursorSequence: 301},
			},
		},
		{
			name:    "solana-devnet",
			chain:   model.ChainSolana,
			network: model.NetworkDevnet,
			addressesA: []model.WatchedAddress{
				{Address: solOther, WalletID: &walletA},
				{Address: solAlias, WalletID: &walletB},
				{Address: solAlias, WalletID: &walletA},
			},
			addressesB: []model.WatchedAddress{
				{Address: solAlias, WalletID: &walletA},
				{Address: solOther, WalletID: &walletA},
				{Address: solAlias, WalletID: &walletB},
			},
			cursorByKey: map[string]*model.AddressCursor{
				solAlias: {Address: solAlias, CursorValue: strPtr(solAliasCursor), CursorSequence: 200},
				solOther: {Address: solOther, CursorValue: strPtr(solOtherCursor), CursorSequence: 201},
			},
		},
	}

	type jobSnapshot struct {
		Address        string
		CursorValue    string
		CursorSequence int64
	}

	run := func(t *testing.T, tc testCase, addresses []model.WatchedAddress) []jobSnapshot {
		t.Helper()
		ctrl := gomock.NewController(t)
		mockWatchedAddr := storemocks.NewMockWatchedAddressRepository(ctrl)
		mockCursor := storemocks.NewMockCursorRepository(ctrl)
		jobCh := make(chan event.FetchJob, 10)

		c := New(
			tc.chain, tc.network,
			mockWatchedAddr, mockCursor,
			100, time.Second,
			jobCh, slog.Default(),
		)

		mockWatchedAddr.EXPECT().
			GetActive(gomock.Any(), tc.chain, tc.network).
			Return(addresses, nil)

		mockCursor.EXPECT().
			Get(gomock.Any(), tc.chain, tc.network, gomock.Any()).
			DoAndReturn(func(_ context.Context, _ model.Chain, _ model.Network, address string) (*model.AddressCursor, error) {
				cursor, ok := tc.cursorByKey[address]
				if !ok {
					return nil, nil
				}
				return cursor, nil
			}).
			Times(len(addresses))

		require.NoError(t, c.tick(context.Background()))

		snapshots := make([]jobSnapshot, 0, len(jobCh))
		for len(jobCh) > 0 {
			job := <-jobCh
			cursorValue := ""
			if job.CursorValue != nil {
				cursorValue = *job.CursorValue
			}
			snapshots = append(snapshots, jobSnapshot{
				Address:        job.Address,
				CursorValue:    cursorValue,
				CursorSequence: job.CursorSequence,
			})
		}
		return snapshots
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			jobsA := run(t, tc, tc.addressesA)
			jobsB := run(t, tc, tc.addressesB)
			assert.Equal(t, jobsA, jobsB)
		})
	}
}

func TestTick_FanInLagAwareMembershipChurnReplayResumeAcrossMandatoryChains(t *testing.T) {
	type testCase struct {
		name         string
		chain        model.Chain
		network      model.Network
		ticksA       [][]model.WatchedAddress
		ticksB       [][]model.WatchedAddress
		initialByKey map[string]*model.AddressCursor
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
			initialByKey: map[string]*model.AddressCursor{
				baseUpper: {Address: baseUpper, CursorValue: strPtr("0x0000000000000000000000000000000000000000000000000000000000000040"), CursorSequence: 40},
				baseLower: {Address: baseLower, CursorValue: strPtr("0x0000000000000000000000000000000000000000000000000000000000000035"), CursorSequence: 35},
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
			initialByKey: map[string]*model.AddressCursor{
				solBase: {Address: solBase, CursorValue: strPtr("sig-sol-40"), CursorSequence: 40},
				solLag:  {Address: solLag, CursorValue: strPtr("sig-sol-35"), CursorSequence: 35},
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			fullA, _ := runLagAwareTickScenario(t, tc.chain, tc.network, tc.ticksA, tc.initialByKey)
			fullB, _ := runLagAwareTickScenario(t, tc.chain, tc.network, tc.ticksB, tc.initialByKey)
			assert.Equal(t, fullA, fullB)

			seen := make(map[lagAwareJobSnapshot]struct{}, len(fullA))
			for _, snapshot := range fullA {
				_, exists := seen[snapshot]
				assert.False(t, exists, "duplicate lag-aware cursor tuple: %+v", snapshot)
				seen[snapshot] = struct{}{}
			}

			require.Len(t, tc.ticksA, 4)
			partA, stateAfterPartA := runLagAwareTickScenario(t, tc.chain, tc.network, tc.ticksA[:2], tc.initialByKey)
			partB, _ := runLagAwareTickScenario(t, tc.chain, tc.network, tc.ticksA[2:], stateAfterPartA)
			assert.Equal(t, fullA[:2], partA)
			assert.Equal(t, fullA[2:], partB)
		})
	}
}

func TestTick_FanInDoesNotCollapseDistinctSolanaAddresses(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockWatchedAddr := storemocks.NewMockWatchedAddressRepository(ctrl)
	mockCursor := storemocks.NewMockCursorRepository(ctrl)

	jobCh := make(chan event.FetchJob, 10)
	c := New(
		model.ChainSolana, model.NetworkDevnet,
		mockWatchedAddr, mockCursor,
		100, time.Second,
		jobCh, slog.Default(),
	)

	addrA := "AbCdEfGh123456789ABCDEFGH123456789"
	addrB := "aBcDeFgH123456789ABCDEFGH123456789"
	cursorA := "sig-A"
	cursorB := "sig-B"

	mockWatchedAddr.EXPECT().
		GetActive(gomock.Any(), model.ChainSolana, model.NetworkDevnet).
		Return([]model.WatchedAddress{
			{Address: addrA},
			{Address: addrB},
		}, nil)

	mockCursor.EXPECT().
		Get(gomock.Any(), model.ChainSolana, model.NetworkDevnet, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ model.Chain, _ model.Network, address string) (*model.AddressCursor, error) {
			switch address {
			case addrA:
				return &model.AddressCursor{Address: addrA, CursorValue: &cursorA, CursorSequence: 10}, nil
			case addrB:
				return &model.AddressCursor{Address: addrB, CursorValue: &cursorB, CursorSequence: 11}, nil
			default:
				return nil, nil
			}
		}).
		Times(2)

	require.NoError(t, c.tick(context.Background()))
	require.Len(t, jobCh, 2)

	job1 := <-jobCh
	job2 := <-jobCh
	assert.NotEqual(t, job1.Address, job2.Address)
}

func TestTick_CheckpointIntegrityCorruptionRecoveryConvergesAcrossMandatoryChains(t *testing.T) {
	type testCase struct {
		name            string
		chain           model.Chain
		network         model.Network
		address         string
		validCursorHint string
	}

	tests := []testCase{
		{
			name:            "solana-devnet",
			chain:           model.ChainSolana,
			network:         model.NetworkDevnet,
			address:         "7nYBpkEPkDD6m1JKBGwvftG7bHjJErJPjTH3VbKpump",
			validCursorHint: "sig-sol-55",
		},
		{
			name:            "base-sepolia",
			chain:           model.ChainBase,
			network:         model.NetworkSepolia,
			address:         "0x1111111111111111111111111111111111111111",
			validCursorHint: "ABCDEF55",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ticks := [][]model.WatchedAddress{
				{{Address: tc.address}},
				{{Address: tc.address}},
				{{Address: tc.address}},
				{{Address: tc.address}},
			}

			baseline := runCheckpointIntegrityTickScenario(t, tc.chain, tc.network, ticks, map[string]*model.AddressCursor{})

			truncated := runCheckpointIntegrityTickScenario(t, tc.chain, tc.network, ticks, map[string]*model.AddressCursor{
				tc.address: {
					Address:        tc.address,
					CursorValue:    strPtr("   "),
					CursorSequence: 55,
					ItemsProcessed: 3,
				},
			})

			stale := runCheckpointIntegrityTickScenario(t, tc.chain, tc.network, ticks, map[string]*model.AddressCursor{
				tc.address: {
					Address:        tc.address,
					CursorValue:    strPtr(tc.validCursorHint),
					CursorSequence: 55,
					ItemsProcessed: 8,
					LastFetchedAt:  nil,
				},
			})

			assert.Equal(t, baseline, truncated)
			assert.Equal(t, baseline, stale)

			for i := 1; i < len(stale); i++ {
				assert.GreaterOrEqual(t, stale[i].CursorSequence, stale[i-1].CursorSequence)
			}
		})
	}
}

func TestTick_CheckpointIntegrityCrossChainMixupFailsFastAcrossMandatoryChains(t *testing.T) {
	type testCase struct {
		name    string
		chain   model.Chain
		network model.Network
		address string
		cursor  model.AddressCursor
	}

	tests := []testCase{
		{
			name:    "solana-devnet-value-shape-mixup",
			chain:   model.ChainSolana,
			network: model.NetworkDevnet,
			address: "7nYBpkEPkDD6m1JKBGwvftG7bHjJErJPjTH3VbKpump",
			cursor: model.AddressCursor{
				CursorValue:    strPtr("0xabc123"),
				CursorSequence: 11,
			},
		},
		{
			name:    "base-sepolia-value-shape-mixup",
			chain:   model.ChainBase,
			network: model.NetworkSepolia,
			address: "0x1111111111111111111111111111111111111111",
			cursor: model.AddressCursor{
				CursorValue:    strPtr("3N5Y7jA1vB2qK8mL9pQ4tU6wX1zC5dE2fG7hJ3kL9mN"),
				CursorSequence: 22,
			},
		},
		{
			name:    "chain-scope-mismatch",
			chain:   model.ChainSolana,
			network: model.NetworkDevnet,
			address: "scope-mismatch-addr",
			cursor: model.AddressCursor{
				Chain:          model.ChainBase,
				Network:        model.NetworkSepolia,
				Address:        "0x2222222222222222222222222222222222222222",
				CursorValue:    strPtr("0xdef456"),
				CursorSequence: 33,
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockWatchedAddr := storemocks.NewMockWatchedAddressRepository(ctrl)
			mockCursor := storemocks.NewMockCursorRepository(ctrl)

			jobCh := make(chan event.FetchJob, 2)
			c := New(tc.chain, tc.network, mockWatchedAddr, mockCursor, 100, time.Second, jobCh, slog.Default())

			mockWatchedAddr.EXPECT().
				GetActive(gomock.Any(), tc.chain, tc.network).
				Return([]model.WatchedAddress{{Address: tc.address}}, nil)

			mockCursor.EXPECT().
				Get(gomock.Any(), tc.chain, tc.network, tc.address).
				Return(&tc.cursor, nil)

			err := c.tick(context.Background())
			require.Error(t, err)
			assert.Contains(t, err.Error(), "checkpoint_integrity_failure")
			assert.Contains(t, err.Error(), "cross_chain_checkpoint_mixup")
			assert.Empty(t, jobCh)
		})
	}
}

func TestTick_AutoTuneOnOffPreservesCanonicalLagAwareTuplesAcrossMandatoryChains(t *testing.T) {
	type testCase struct {
		name         string
		chain        model.Chain
		network      model.Network
		ticks        [][]model.WatchedAddress
		initialByKey map[string]*model.AddressCursor
	}

	baseUpper := "0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	baseLower := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	solBase := "7nYBpkEPkDD6m1JKBGwvftG7bHjJErJPjTH3VbKpump"
	solLag := " " + solBase

	tests := []testCase{
		{
			name:    "base-sepolia",
			chain:   model.ChainBase,
			network: model.NetworkSepolia,
			ticks: [][]model.WatchedAddress{
				{{Address: baseUpper}, {Address: baseLower}},
				{{Address: baseLower}, {Address: baseUpper}},
				{{Address: baseUpper}, {Address: baseLower}},
				{{Address: baseLower}, {Address: baseUpper}},
			},
			initialByKey: map[string]*model.AddressCursor{
				baseUpper: {Address: baseUpper, CursorValue: strPtr("0x0000000000000000000000000000000000000000000000000000000000000010"), CursorSequence: 16},
				baseLower: {Address: baseLower, CursorValue: strPtr("0x000000000000000000000000000000000000000000000000000000000000000a"), CursorSequence: 10},
			},
		},
		{
			name:    "solana-devnet",
			chain:   model.ChainSolana,
			network: model.NetworkDevnet,
			ticks: [][]model.WatchedAddress{
				{{Address: solBase}, {Address: solLag}},
				{{Address: solLag}, {Address: solBase}},
				{{Address: solBase}, {Address: solLag}},
				{{Address: solLag}, {Address: solBase}},
			},
			initialByKey: map[string]*model.AddressCursor{
				solBase: {Address: solBase, CursorValue: strPtr("sig-sol-16"), CursorSequence: 16},
				solLag:  {Address: solLag, CursorValue: strPtr("sig-sol-10"), CursorSequence: 10},
			},
		},
	}

	autoTuneCfg := AutoTuneConfig{
		Enabled:               true,
		MinBatchSize:          50,
		MaxBatchSize:          180,
		StepUp:                20,
		StepDown:              10,
		LagHighWatermark:      100,
		LagLowWatermark:       20,
		QueueHighWatermarkPct: 90,
		QueueLowWatermarkPct:  10,
		HysteresisTicks:       1,
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			baselineSnapshots, baselineBatches := runAutoTuneTickScenario(t, tc.chain, tc.network, tc.ticks, tc.initialByKey, 2000, nil)
			autoTuneSnapshots, autoTuneBatches := runAutoTuneTickScenario(t, tc.chain, tc.network, tc.ticks, tc.initialByKey, 2000, &autoTuneCfg)

			assert.Equal(t, baselineSnapshots, autoTuneSnapshots, "auto-tune must not change canonical lag-aware tuple selection")
			assert.NotEqual(t, baselineBatches, autoTuneBatches, "auto-tune should change envelope knobs under sustained lag")
		})
	}
}

func TestTick_AutoTuneChainScopedOneChainLagDoesNotThrottleHealthyChain(t *testing.T) {
	autoTuneCfg := AutoTuneConfig{
		Enabled:               true,
		MinBatchSize:          60,
		MaxBatchSize:          160,
		StepUp:                20,
		StepDown:              10,
		LagHighWatermark:      80,
		LagLowWatermark:       20,
		QueueHighWatermarkPct: 90,
		QueueLowWatermarkPct:  10,
		HysteresisTicks:       1,
	}

	const tickCount = 5
	healthyAddress := "0x1111111111111111111111111111111111111111"
	laggingAddress := "7nYBpkEPkDD6m1JKBGwvftG7bHjJErJPjTH3VbKpump"

	healthyBaseline := newAutoTuneHarness(
		model.ChainBase,
		model.NetworkSepolia,
		healthyAddress,
		120,
		130,
		tickCount,
		autoTuneCfg,
	)
	baselineBatches := make([]int, 0, tickCount)
	for i := 0; i < tickCount; i++ {
		job := healthyBaseline.tickAndAdvance(t)
		baselineBatches = append(baselineBatches, job.BatchSize)
	}

	healthyInterleaved := newAutoTuneHarness(
		model.ChainBase,
		model.NetworkSepolia,
		healthyAddress,
		120,
		130,
		tickCount,
		autoTuneCfg,
	)
	lagging := newAutoTuneHarness(
		model.ChainSolana,
		model.NetworkDevnet,
		laggingAddress,
		100,
		2000,
		tickCount,
		autoTuneCfg,
	)

	interleavedHealthyBatches := make([]int, 0, tickCount)
	laggingBatches := make([]int, 0, tickCount)
	for i := 0; i < tickCount; i++ {
		laggingJob := lagging.tickAndAdvance(t)
		laggingBatches = append(laggingBatches, laggingJob.BatchSize)

		healthyJob := healthyInterleaved.tickAndAdvance(t)
		interleavedHealthyBatches = append(interleavedHealthyBatches, healthyJob.BatchSize)
	}

	assert.Equal(t, baselineBatches, interleavedHealthyBatches, "healthy chain knobs must be independent from lagging chain pressure")
	assert.Greater(t, maxIntSlice(laggingBatches), maxIntSlice(interleavedHealthyBatches), "lagging chain should scale independently without throttling healthy chain")
}

func TestTick_AutoTuneProfileTransitionPreservesBatchAcrossMandatoryChains(t *testing.T) {
	type testCase struct {
		name    string
		chain   model.Chain
		network model.Network
		address string
	}

	tests := []testCase{
		{
			name:    "solana-devnet",
			chain:   model.ChainSolana,
			network: model.NetworkDevnet,
			address: "7nYBpkEPkDD6m1JKBGwvftG7bHjJErJPjTH3VbKpump",
		},
		{
			name:    "base-sepolia",
			chain:   model.ChainBase,
			network: model.NetworkSepolia,
			address: "0x1111111111111111111111111111111111111111",
		},
		{
			name:    "btc-testnet",
			chain:   model.ChainBTC,
			network: model.NetworkTestnet,
			address: "tb1qprofiletransition000000000000000000000000",
		},
	}

	rampCfg := AutoTuneConfig{
		Enabled:               true,
		MinBatchSize:          60,
		MaxBatchSize:          200,
		StepUp:                20,
		StepDown:              10,
		LagHighWatermark:      80,
		LagLowWatermark:       20,
		QueueHighWatermarkPct: 90,
		QueueLowWatermarkPct:  10,
		HysteresisTicks:       1,
	}

	transitionCfg := AutoTuneConfig{
		Enabled:               true,
		MinBatchSize:          60,
		MaxBatchSize:          200,
		StepUp:                20,
		StepDown:              10,
		LagHighWatermark:      5_000,
		LagLowWatermark:       0,
		QueueHighWatermarkPct: 90,
		QueueLowWatermarkPct:  10,
		HysteresisTicks:       1,
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			harness := newAutoTuneHarness(tc.chain, tc.network, tc.address, 100, 2000, 4, rampCfg)

			first := harness.tickAndAdvance(t)
			second := harness.tickAndAdvance(t)
			require.Equal(t, 120, first.BatchSize)
			require.Equal(t, 140, second.BatchSize)

			harness.coordinator.WithAutoTune(transitionCfg)

			third := harness.tickAndAdvance(t)
			assert.Equal(t, second.BatchSize, third.BatchSize, "profile transition must preserve chain-local batch state")
			assert.GreaterOrEqual(t, third.CursorSequence, second.CursorSequence, "cursor monotonicity must hold through profile transitions")
		})
	}
}

func TestTick_AutoTuneRestartPermutationsConvergeCanonicalTuplesAcrossMandatoryChains(t *testing.T) {
	type testCase struct {
		name         string
		chain        model.Chain
		network      model.Network
		address      string
		headSequence int64
	}

	tests := []testCase{
		{
			name:         "solana-devnet",
			chain:        model.ChainSolana,
			network:      model.NetworkDevnet,
			address:      "7nYBpkEPkDD6m1JKBGwvftG7bHjJErJPjTH3VbKpump",
			headSequence: 2_000,
		},
		{
			name:         "base-sepolia",
			chain:        model.ChainBase,
			network:      model.NetworkSepolia,
			address:      "0x1111111111111111111111111111111111111111",
			headSequence: 2_000,
		},
		{
			name:         "btc-testnet",
			chain:        model.ChainBTC,
			network:      model.NetworkTestnet,
			address:      "tb1qrestartprofile000000000000000000000000",
			headSequence: 2_000,
		},
	}

	baseCfg := AutoTuneConfig{
		Enabled:               true,
		MinBatchSize:          60,
		MaxBatchSize:          200,
		StepUp:                20,
		StepDown:              10,
		LagHighWatermark:      80,
		LagLowWatermark:       20,
		QueueHighWatermarkPct: 90,
		QueueLowWatermarkPct:  10,
		HysteresisTicks:       1,
	}
	transitionCfg := AutoTuneConfig{
		Enabled:               true,
		MinBatchSize:          60,
		MaxBatchSize:          200,
		StepUp:                20,
		StepDown:              10,
		LagHighWatermark:      5_000,
		LagLowWatermark:       0,
		QueueHighWatermarkPct: 90,
		QueueLowWatermarkPct:  10,
		HysteresisTicks:       1,
	}

	const tickCount = 6
	const splitTick = 3

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			coldHarness := newAutoTuneHarness(tc.chain, tc.network, tc.address, 100, tc.headSequence, tickCount, baseCfg)
			coldSnapshots, _ := collectAutoTuneTrace(t, coldHarness, tickCount)

			warmFirst := newAutoTuneHarness(tc.chain, tc.network, tc.address, 100, tc.headSequence, splitTick, baseCfg)
			warmSnapshots, _ := collectAutoTuneTrace(t, warmFirst, splitTick)
			restartState := warmFirst.coordinator.ExportAutoTuneRestartState()
			require.NotNil(t, restartState)
			resumeCursor := warmFirst.cursorRepo.GetByAddress(tc.address)
			require.NotNil(t, resumeCursor)

			warmSecond := newAutoTuneHarnessWithWarmStart(
				tc.chain,
				tc.network,
				tc.address,
				resumeCursor.CursorSequence,
				tc.headSequence,
				tickCount-splitTick,
				baseCfg,
				restartState,
			)
			warmTailSnapshots, _ := collectAutoTuneTrace(t, warmSecond, tickCount-splitTick)
			warmSnapshots = append(warmSnapshots, warmTailSnapshots...)

			profileHarness := newAutoTuneHarness(tc.chain, tc.network, tc.address, 100, tc.headSequence, tickCount, baseCfg)
			profileSnapshots := make([]lagAwareJobSnapshot, 0, tickCount)
			for i := 0; i < tickCount; i++ {
				if i == splitTick {
					profileHarness.coordinator.WithAutoTune(transitionCfg)
				}
				job := profileHarness.tickAndAdvance(t)
				profileSnapshots = append(profileSnapshots, snapshotFromFetchJob(job))
			}

			assert.Equal(t, coldSnapshots, warmSnapshots, "cold-start and warm-start permutations must converge to one canonical tuple output set")
			assert.Equal(t, coldSnapshots, profileSnapshots, "cold-start and profile-transition permutations must converge to one canonical tuple output set")
		})
	}
}

func TestTick_AutoTuneOneChainRestartUnderLagPressureNoCrossChainBleed(t *testing.T) {
	autoTuneCfg := AutoTuneConfig{
		Enabled:               true,
		MinBatchSize:          60,
		MaxBatchSize:          200,
		StepUp:                20,
		StepDown:              10,
		LagHighWatermark:      80,
		LagLowWatermark:       20,
		QueueHighWatermarkPct: 90,
		QueueLowWatermarkPct:  10,
		HysteresisTicks:       1,
	}

	const tickCount = 6
	const restartTick = 3

	healthyAddress := "0x1111111111111111111111111111111111111111"
	laggingAddress := "7nYBpkEPkDD6m1JKBGwvftG7bHjJErJPjTH3VbKpump"

	healthyBaseline := newAutoTuneHarness(
		model.ChainBase,
		model.NetworkSepolia,
		healthyAddress,
		120,
		130,
		tickCount,
		autoTuneCfg,
	)
	healthyBaselineSnapshots, healthyBaselineBatches := collectAutoTuneTrace(t, healthyBaseline, tickCount)

	laggingBaseline := newAutoTuneHarness(
		model.ChainSolana,
		model.NetworkDevnet,
		laggingAddress,
		100,
		2_000,
		tickCount,
		autoTuneCfg,
	)
	laggingBaselineSnapshots, _ := collectAutoTuneTrace(t, laggingBaseline, tickCount)

	healthyInterleaved := newAutoTuneHarness(
		model.ChainBase,
		model.NetworkSepolia,
		healthyAddress,
		120,
		130,
		tickCount,
		autoTuneCfg,
	)
	laggingRestarted := newAutoTuneHarness(
		model.ChainSolana,
		model.NetworkDevnet,
		laggingAddress,
		100,
		2_000,
		tickCount,
		autoTuneCfg,
	)

	healthyInterleavedSnapshots := make([]lagAwareJobSnapshot, 0, tickCount)
	healthyInterleavedBatches := make([]int, 0, tickCount)
	laggingRestartSnapshots := make([]lagAwareJobSnapshot, 0, tickCount)

	for i := 0; i < tickCount; i++ {
		if i == restartTick {
			restartState := laggingRestarted.coordinator.ExportAutoTuneRestartState()
			require.NotNil(t, restartState)
			resumeCursor := laggingRestarted.cursorRepo.GetByAddress(laggingAddress)
			require.NotNil(t, resumeCursor)

			laggingRestarted = newAutoTuneHarnessWithWarmStart(
				model.ChainSolana,
				model.NetworkDevnet,
				laggingAddress,
				resumeCursor.CursorSequence,
				2_000,
				tickCount-i,
				autoTuneCfg,
				restartState,
			)
		}

		laggingJob := laggingRestarted.tickAndAdvance(t)
		laggingRestartSnapshots = append(laggingRestartSnapshots, snapshotFromFetchJob(laggingJob))

		healthyJob := healthyInterleaved.tickAndAdvance(t)
		healthyInterleavedSnapshots = append(healthyInterleavedSnapshots, snapshotFromFetchJob(healthyJob))
		healthyInterleavedBatches = append(healthyInterleavedBatches, healthyJob.BatchSize)
	}

	assert.Equal(t, healthyBaselineSnapshots, healthyInterleavedSnapshots, "lagging-chain restart must not change healthy-chain canonical tuples")
	assert.Equal(t, healthyBaselineBatches, healthyInterleavedBatches, "lagging-chain restart must not alter healthy-chain control decisions")
	assert.Equal(t, laggingBaselineSnapshots, laggingRestartSnapshots, "restart/resume under lag pressure must preserve lagging-chain canonical tuples")

	assertCursorMonotonicByAddress(t, healthyInterleavedSnapshots)
	assertCursorMonotonicByAddress(t, laggingRestartSnapshots)
}

func TestTick_AutoTuneWarmStartRejectsCrossChainState(t *testing.T) {
	autoTuneCfg := AutoTuneConfig{
		Enabled:               true,
		MinBatchSize:          60,
		MaxBatchSize:          200,
		StepUp:                20,
		StepDown:              10,
		LagHighWatermark:      80,
		LagLowWatermark:       20,
		QueueHighWatermarkPct: 90,
		QueueLowWatermarkPct:  10,
		HysteresisTicks:       1,
	}

	solanaHarness := newAutoTuneHarness(
		model.ChainSolana,
		model.NetworkDevnet,
		"7nYBpkEPkDD6m1JKBGwvftG7bHjJErJPjTH3VbKpump",
		100,
		2_000,
		2,
		autoTuneCfg,
	)
	_ = solanaHarness.tickAndAdvance(t)
	foreignState := solanaHarness.coordinator.ExportAutoTuneRestartState()
	require.NotNil(t, foreignState)

	coldStart := newAutoTuneHarness(
		model.ChainBase,
		model.NetworkSepolia,
		"0x1111111111111111111111111111111111111111",
		100,
		2_000,
		1,
		autoTuneCfg,
	)
	coldJob := coldStart.tickAndAdvance(t)

	rejectedWarmStart := newAutoTuneHarnessWithWarmStart(
		model.ChainBase,
		model.NetworkSepolia,
		"0x1111111111111111111111111111111111111111",
		100,
		2_000,
		1,
		autoTuneCfg,
		foreignState,
	)
	rejectedJob := rejectedWarmStart.tickAndAdvance(t)

	assert.Equal(t, coldJob.BatchSize, rejectedJob.BatchSize, "cross-chain warm-start state must be rejected and behave as deterministic cold-start")
	assert.Equal(t, snapshotFromFetchJob(coldJob), snapshotFromFetchJob(rejectedJob))
}

func TestTick_AutoTuneSignalFlapPermutationsConvergeCanonicalTuplesAcrossMandatoryChains(t *testing.T) {
	type testCase struct {
		name    string
		chain   model.Chain
		network model.Network
		address string
	}

	tests := []testCase{
		{
			name:    "solana-devnet",
			chain:   model.ChainSolana,
			network: model.NetworkDevnet,
			address: "7nYBpkEPkDD6m1JKBGwvftG7bHjJErJPjTH3VbKpump",
		},
		{
			name:    "base-sepolia",
			chain:   model.ChainBase,
			network: model.NetworkSepolia,
			address: "0x1111111111111111111111111111111111111111",
		},
		{
			name:    "btc-testnet",
			chain:   model.ChainBTC,
			network: model.NetworkTestnet,
			address: "tb1qflapperm000000000000000000000000000000",
		},
	}

	autoTuneCfg := AutoTuneConfig{
		Enabled:               true,
		MinBatchSize:          60,
		MaxBatchSize:          200,
		StepUp:                20,
		StepDown:              10,
		LagHighWatermark:      80,
		LagLowWatermark:       20,
		QueueHighWatermarkPct: 90,
		QueueLowWatermarkPct:  10,
		HysteresisTicks:       1,
		CooldownTicks:         2,
	}

	steadyHeads := []int64{260, 261, 262, 263, 264, 265}
	jitterHeads := []int64{260, 115, 260, 115, 260, 115}
	recoveryHeads := []int64{260, 115, 260, 115, 260, 261}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			steadyHarness := newAutoTuneHarnessWithHeadSeries(tc.chain, tc.network, tc.address, 100, steadyHeads, autoTuneCfg)
			steadySnapshots, steadyBatches := collectAutoTuneTrace(t, steadyHarness, len(steadyHeads))

			jitterHarness := newAutoTuneHarnessWithHeadSeries(tc.chain, tc.network, tc.address, 100, jitterHeads, autoTuneCfg)
			jitterSnapshots, jitterBatches := collectAutoTuneTrace(t, jitterHarness, len(jitterHeads))

			recoveryHarness := newAutoTuneHarnessWithHeadSeries(tc.chain, tc.network, tc.address, 100, recoveryHeads, autoTuneCfg)
			recoverySnapshots, _ := collectAutoTuneTrace(t, recoveryHarness, len(recoveryHeads))

			assert.Equal(t, steadySnapshots, jitterSnapshots, "steady-state and threshold-jitter permutations must converge to one canonical tuple output set")
			assert.Equal(t, steadySnapshots, recoverySnapshots, "steady-state and recovery permutations must converge to one canonical tuple output set")
			assertNoDuplicateOrMissingLogicalSnapshots(t, steadySnapshots, jitterSnapshots, "steady-state vs threshold-jitter")
			assertNoDuplicateOrMissingLogicalSnapshots(t, steadySnapshots, recoverySnapshots, "steady-state vs recovery")
			assert.NotEqual(t, steadyBatches, jitterBatches, "signal-flap jitter should exercise different control decisions without changing canonical tuples")
			assertNoImmediateDirectionFlip(t, jitterBatches)

			assertCursorMonotonicByAddress(t, steadySnapshots)
			assertCursorMonotonicByAddress(t, jitterSnapshots)
			assertCursorMonotonicByAddress(t, recoverySnapshots)
		})
	}
}

func TestTick_AutoTuneOneChainOscillationDoesNotBleedControlAcrossOtherMandatoryChains(t *testing.T) {
	autoTuneCfg := AutoTuneConfig{
		Enabled:               true,
		MinBatchSize:          60,
		MaxBatchSize:          200,
		StepUp:                20,
		StepDown:              10,
		LagHighWatermark:      80,
		LagLowWatermark:       20,
		QueueHighWatermarkPct: 90,
		QueueLowWatermarkPct:  10,
		HysteresisTicks:       1,
		CooldownTicks:         2,
	}

	const tickCount = 6
	healthyBaseAddress := "0x1111111111111111111111111111111111111111"
	healthyBTCAddress := "tb1qoscillationhealthy000000000000000000000"
	laggingSolanaAddress := "7nYBpkEPkDD6m1JKBGwvftG7bHjJErJPjTH3VbKpump"

	healthyHeads := []int64{130, 130, 130, 130, 130, 130}
	laggingFlapHeads := []int64{260, 115, 260, 115, 260, 115}

	baseBaseline := newAutoTuneHarnessWithHeadSeries(
		model.ChainBase,
		model.NetworkSepolia,
		healthyBaseAddress,
		120,
		healthyHeads,
		autoTuneCfg,
	)
	baseBaselineSnapshots, baseBaselineBatches := collectAutoTuneTrace(t, baseBaseline, tickCount)

	btcBaseline := newAutoTuneHarnessWithHeadSeries(
		model.ChainBTC,
		model.NetworkTestnet,
		healthyBTCAddress,
		120,
		healthyHeads,
		autoTuneCfg,
	)
	btcBaselineSnapshots, btcBaselineBatches := collectAutoTuneTrace(t, btcBaseline, tickCount)

	baseInterleaved := newAutoTuneHarnessWithHeadSeries(
		model.ChainBase,
		model.NetworkSepolia,
		healthyBaseAddress,
		120,
		healthyHeads,
		autoTuneCfg,
	)
	btcInterleaved := newAutoTuneHarnessWithHeadSeries(
		model.ChainBTC,
		model.NetworkTestnet,
		healthyBTCAddress,
		120,
		healthyHeads,
		autoTuneCfg,
	)
	laggingInterleaved := newAutoTuneHarnessWithHeadSeries(
		model.ChainSolana,
		model.NetworkDevnet,
		laggingSolanaAddress,
		100,
		laggingFlapHeads,
		autoTuneCfg,
	)

	baseSnapshots := make([]lagAwareJobSnapshot, 0, tickCount)
	baseBatches := make([]int, 0, tickCount)
	btcSnapshots := make([]lagAwareJobSnapshot, 0, tickCount)
	btcBatches := make([]int, 0, tickCount)
	laggingSnapshots := make([]lagAwareJobSnapshot, 0, tickCount)
	laggingBatches := make([]int, 0, tickCount)

	for i := 0; i < tickCount; i++ {
		laggingJob := laggingInterleaved.tickAndAdvance(t)
		laggingSnapshots = append(laggingSnapshots, snapshotFromFetchJob(laggingJob))
		laggingBatches = append(laggingBatches, laggingJob.BatchSize)

		baseJob := baseInterleaved.tickAndAdvance(t)
		baseSnapshots = append(baseSnapshots, snapshotFromFetchJob(baseJob))
		baseBatches = append(baseBatches, baseJob.BatchSize)

		btcJob := btcInterleaved.tickAndAdvance(t)
		btcSnapshots = append(btcSnapshots, snapshotFromFetchJob(btcJob))
		btcBatches = append(btcBatches, btcJob.BatchSize)
	}

	assert.Equal(t, baseBaselineSnapshots, baseSnapshots, "solana oscillation pressure must not alter base canonical tuples")
	assert.Equal(t, baseBaselineBatches, baseBatches, "solana oscillation pressure must not alter base control decisions")
	assert.Equal(t, btcBaselineSnapshots, btcSnapshots, "solana oscillation pressure must not alter btc canonical tuples")
	assert.Equal(t, btcBaselineBatches, btcBatches, "solana oscillation pressure must not alter btc control decisions")
	assertNoDuplicateOrMissingLogicalSnapshots(t, baseBaselineSnapshots, baseSnapshots, "base baseline vs interleaved oscillation")
	assertNoDuplicateOrMissingLogicalSnapshots(t, btcBaselineSnapshots, btcSnapshots, "btc baseline vs interleaved oscillation")
	assertNoImmediateDirectionFlip(t, laggingBatches)

	assertCursorMonotonicByAddress(t, baseSnapshots)
	assertCursorMonotonicByAddress(t, btcSnapshots)
	assertCursorMonotonicByAddress(t, laggingSnapshots)
}

func TestTick_AutoTuneSaturationPermutationsConvergeCanonicalTuplesAcrossMandatoryChains(t *testing.T) {
	type testCase struct {
		name    string
		chain   model.Chain
		network model.Network
		address string
	}

	tests := []testCase{
		{
			name:    "solana-devnet",
			chain:   model.ChainSolana,
			network: model.NetworkDevnet,
			address: "7nYBpkEPkDD6m1JKBGwvftG7bHjJErJPjTH3VbKpump",
		},
		{
			name:    "base-sepolia",
			chain:   model.ChainBase,
			network: model.NetworkSepolia,
			address: "0x1111111111111111111111111111111111111111",
		},
		{
			name:    "btc-testnet",
			chain:   model.ChainBTC,
			network: model.NetworkTestnet,
			address: "tb1qsaturationperm000000000000000000000000",
		},
	}

	autoTuneCfg := AutoTuneConfig{
		Enabled:               true,
		MinBatchSize:          60,
		MaxBatchSize:          140,
		StepUp:                20,
		StepDown:              10,
		LagHighWatermark:      80,
		LagLowWatermark:       20,
		QueueHighWatermarkPct: 90,
		QueueLowWatermarkPct:  10,
		HysteresisTicks:       1,
		CooldownTicks:         2,
	}

	saturationEntryHeads := []int64{300, 110, 110, 110, 110, 110}
	sustainedSaturationHeads := []int64{300, 300, 300, 110, 110, 110}
	deSaturationRecoveryHeads := []int64{300, 300, 300, 300, 110, 110}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			entryHarness := newAutoTuneHarnessWithHeadSeries(
				tc.chain,
				tc.network,
				tc.address,
				100,
				saturationEntryHeads,
				autoTuneCfg,
			)
			entrySnapshots, entryBatches := collectAutoTuneTrace(t, entryHarness, len(saturationEntryHeads))

			sustainedHarness := newAutoTuneHarnessWithHeadSeries(
				tc.chain,
				tc.network,
				tc.address,
				100,
				sustainedSaturationHeads,
				autoTuneCfg,
			)
			sustainedSnapshots, sustainedBatches := collectAutoTuneTrace(t, sustainedHarness, len(sustainedSaturationHeads))

			recoveryHarness := newAutoTuneHarnessWithHeadSeries(
				tc.chain,
				tc.network,
				tc.address,
				100,
				deSaturationRecoveryHeads,
				autoTuneCfg,
			)
			recoverySnapshots, recoveryBatches := collectAutoTuneTrace(t, recoveryHarness, len(deSaturationRecoveryHeads))

			assert.Equal(t, entrySnapshots, sustainedSnapshots, "saturation-entry and sustained-saturation permutations must converge to one canonical tuple output set")
			assert.Equal(t, entrySnapshots, recoverySnapshots, "saturation-entry and de-saturation-recovery permutations must converge to one canonical tuple output set")
			assertNoDuplicateOrMissingLogicalSnapshots(t, entrySnapshots, sustainedSnapshots, "saturation-entry vs sustained-saturation")
			assertNoDuplicateOrMissingLogicalSnapshots(t, entrySnapshots, recoverySnapshots, "saturation-entry vs de-saturation-recovery")
			assert.NotEqual(t, entryBatches, sustainedBatches, "saturation permutations must exercise different control decisions")
			assert.NotEqual(t, entryBatches, recoveryBatches, "saturation permutations must exercise different control decisions")

			assertCursorMonotonicByAddress(t, entrySnapshots)
			assertCursorMonotonicByAddress(t, sustainedSnapshots)
			assertCursorMonotonicByAddress(t, recoverySnapshots)
		})
	}
}

func TestTick_AutoTuneOneChainSustainedSaturationDoesNotBleedControlAcrossOtherMandatoryChains(t *testing.T) {
	autoTuneCfg := AutoTuneConfig{
		Enabled:               true,
		MinBatchSize:          60,
		MaxBatchSize:          140,
		StepUp:                20,
		StepDown:              10,
		LagHighWatermark:      80,
		LagLowWatermark:       20,
		QueueHighWatermarkPct: 90,
		QueueLowWatermarkPct:  10,
		HysteresisTicks:       1,
		CooldownTicks:         2,
	}

	const tickCount = 6
	healthyBaseAddress := "0x1111111111111111111111111111111111111111"
	healthyBTCAddress := "tb1qsaturationhealthy00000000000000000000000"
	laggingSolanaAddress := "7nYBpkEPkDD6m1JKBGwvftG7bHjJErJPjTH3VbKpump"

	healthyHeads := []int64{130, 130, 130, 130, 130, 130}
	sustainedSaturationHeads := []int64{300, 300, 300, 300, 110, 110}

	baseBaseline := newAutoTuneHarnessWithHeadSeries(
		model.ChainBase,
		model.NetworkSepolia,
		healthyBaseAddress,
		120,
		healthyHeads,
		autoTuneCfg,
	)
	baseBaselineSnapshots, baseBaselineBatches := collectAutoTuneTrace(t, baseBaseline, tickCount)

	btcBaseline := newAutoTuneHarnessWithHeadSeries(
		model.ChainBTC,
		model.NetworkTestnet,
		healthyBTCAddress,
		120,
		healthyHeads,
		autoTuneCfg,
	)
	btcBaselineSnapshots, btcBaselineBatches := collectAutoTuneTrace(t, btcBaseline, tickCount)

	baseInterleaved := newAutoTuneHarnessWithHeadSeries(
		model.ChainBase,
		model.NetworkSepolia,
		healthyBaseAddress,
		120,
		healthyHeads,
		autoTuneCfg,
	)
	btcInterleaved := newAutoTuneHarnessWithHeadSeries(
		model.ChainBTC,
		model.NetworkTestnet,
		healthyBTCAddress,
		120,
		healthyHeads,
		autoTuneCfg,
	)
	laggingInterleaved := newAutoTuneHarnessWithHeadSeries(
		model.ChainSolana,
		model.NetworkDevnet,
		laggingSolanaAddress,
		100,
		sustainedSaturationHeads,
		autoTuneCfg,
	)

	baseSnapshots := make([]lagAwareJobSnapshot, 0, tickCount)
	baseBatches := make([]int, 0, tickCount)
	btcSnapshots := make([]lagAwareJobSnapshot, 0, tickCount)
	btcBatches := make([]int, 0, tickCount)
	laggingSnapshots := make([]lagAwareJobSnapshot, 0, tickCount)
	laggingBatches := make([]int, 0, tickCount)

	for i := 0; i < tickCount; i++ {
		laggingJob := laggingInterleaved.tickAndAdvance(t)
		laggingSnapshots = append(laggingSnapshots, snapshotFromFetchJob(laggingJob))
		laggingBatches = append(laggingBatches, laggingJob.BatchSize)

		baseJob := baseInterleaved.tickAndAdvance(t)
		baseSnapshots = append(baseSnapshots, snapshotFromFetchJob(baseJob))
		baseBatches = append(baseBatches, baseJob.BatchSize)

		btcJob := btcInterleaved.tickAndAdvance(t)
		btcSnapshots = append(btcSnapshots, snapshotFromFetchJob(btcJob))
		btcBatches = append(btcBatches, btcJob.BatchSize)
	}

	assert.Equal(t, baseBaselineSnapshots, baseSnapshots, "sustained solana saturation pressure must not alter base canonical tuples")
	assert.Equal(t, baseBaselineBatches, baseBatches, "sustained solana saturation pressure must not alter base control decisions")
	assert.Equal(t, btcBaselineSnapshots, btcSnapshots, "sustained solana saturation pressure must not alter btc canonical tuples")
	assert.Equal(t, btcBaselineBatches, btcBatches, "sustained solana saturation pressure must not alter btc control decisions")
	assert.Equal(t, autoTuneCfg.MaxBatchSize, maxIntSlice(laggingBatches), "lagging chain should independently reach deterministic saturation cap under sustained pressure")
	assertNoDuplicateOrMissingLogicalSnapshots(t, baseBaselineSnapshots, baseSnapshots, "base baseline vs interleaved sustained saturation")
	assertNoDuplicateOrMissingLogicalSnapshots(t, btcBaselineSnapshots, btcSnapshots, "btc baseline vs interleaved sustained saturation")

	assertCursorMonotonicByAddress(t, baseSnapshots)
	assertCursorMonotonicByAddress(t, btcSnapshots)
	assertCursorMonotonicByAddress(t, laggingSnapshots)
}

func TestTick_AutoTuneSaturationReplayResumeConvergesAcrossMandatoryChains(t *testing.T) {
	type testCase struct {
		name    string
		chain   model.Chain
		network model.Network
		address string
	}

	tests := []testCase{
		{
			name:    "solana-devnet",
			chain:   model.ChainSolana,
			network: model.NetworkDevnet,
			address: "7nYBpkEPkDD6m1JKBGwvftG7bHjJErJPjTH3VbKpump",
		},
		{
			name:    "base-sepolia",
			chain:   model.ChainBase,
			network: model.NetworkSepolia,
			address: "0x1111111111111111111111111111111111111111",
		},
		{
			name:    "btc-testnet",
			chain:   model.ChainBTC,
			network: model.NetworkTestnet,
			address: "tb1qsaturationreplay0000000000000000000000",
		},
	}

	autoTuneCfg := AutoTuneConfig{
		Enabled:               true,
		MinBatchSize:          60,
		MaxBatchSize:          140,
		StepUp:                20,
		StepDown:              10,
		LagHighWatermark:      80,
		LagLowWatermark:       20,
		QueueHighWatermarkPct: 90,
		QueueLowWatermarkPct:  10,
		HysteresisTicks:       1,
		CooldownTicks:         2,
	}

	heads := []int64{300, 300, 300, 110, 110, 110}
	const splitTick = 3

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			coldHarness := newAutoTuneHarnessWithHeadSeries(tc.chain, tc.network, tc.address, 100, heads, autoTuneCfg)
			coldSnapshots, _ := collectAutoTuneTrace(t, coldHarness, len(heads))

			warmFirst := newAutoTuneHarnessWithHeadSeries(tc.chain, tc.network, tc.address, 100, heads[:splitTick], autoTuneCfg)
			warmSnapshots, _ := collectAutoTuneTrace(t, warmFirst, splitTick)
			restartState := warmFirst.coordinator.ExportAutoTuneRestartState()
			require.NotNil(t, restartState)
			resumeCursor := warmFirst.cursorRepo.GetByAddress(tc.address)
			require.NotNil(t, resumeCursor)

			warmSecond := newAutoTuneHarnessWithWarmStartAndHeadSeries(
				tc.chain,
				tc.network,
				tc.address,
				resumeCursor.CursorSequence,
				heads[splitTick:],
				autoTuneCfg,
				restartState,
			)
			warmTailSnapshots, _ := collectAutoTuneTrace(t, warmSecond, len(heads)-splitTick)
			warmSnapshots = append(warmSnapshots, warmTailSnapshots...)

			assert.Equal(t, coldSnapshots, warmSnapshots, "saturation-boundary replay/resume must converge to deterministic canonical tuples")
			assertNoDuplicateOrMissingLogicalSnapshots(t, coldSnapshots, warmSnapshots, "cold vs warm saturation replay")
			assertCursorMonotonicByAddress(t, warmSnapshots)
		})
	}
}

func TestTick_AutoTuneTelemetryStalenessPermutationsConvergeCanonicalTuplesAcrossMandatoryChains(t *testing.T) {
	type testCase struct {
		name    string
		chain   model.Chain
		network model.Network
		address string
	}

	tests := []testCase{
		{
			name:    "solana-devnet",
			chain:   model.ChainSolana,
			network: model.NetworkDevnet,
			address: "7nYBpkEPkDD6m1JKBGwvftG7bHjJErJPjTH3VbKpump",
		},
		{
			name:    "base-sepolia",
			chain:   model.ChainBase,
			network: model.NetworkSepolia,
			address: "0x1111111111111111111111111111111111111111",
		},
		{
			name:    "btc-testnet",
			chain:   model.ChainBTC,
			network: model.NetworkTestnet,
			address: "tb1qtelemetrystale000000000000000000000000",
		},
	}

	autoTuneCfg := AutoTuneConfig{
		Enabled:                true,
		MinBatchSize:           60,
		MaxBatchSize:           200,
		StepUp:                 20,
		StepDown:               10,
		LagHighWatermark:       80,
		LagLowWatermark:        20,
		QueueHighWatermarkPct:  90,
		QueueLowWatermarkPct:   10,
		HysteresisTicks:        1,
		CooldownTicks:          1,
		TelemetryStaleTicks:    2,
		TelemetryRecoveryTicks: 2,
	}

	freshHeads := []int64{260, 261, 262, 263, 264, 265, 266}
	staleHeads := []int64{260, 95, 95, 95, 95, 95, 95}
	recoveryHeads := []int64{260, 95, 95, 95, 300, 301, 302}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			freshHarness := newAutoTuneHarnessWithHeadSeries(tc.chain, tc.network, tc.address, 100, freshHeads, autoTuneCfg)
			freshSnapshots, freshBatches := collectAutoTuneTrace(t, freshHarness, len(freshHeads))

			staleHarness := newAutoTuneHarnessWithHeadSeries(tc.chain, tc.network, tc.address, 100, staleHeads, autoTuneCfg)
			staleSnapshots, staleBatches := collectAutoTuneTrace(t, staleHarness, len(staleHeads))

			recoveryHarness := newAutoTuneHarnessWithHeadSeries(tc.chain, tc.network, tc.address, 100, recoveryHeads, autoTuneCfg)
			recoverySnapshots, recoveryBatches := collectAutoTuneTrace(t, recoveryHarness, len(recoveryHeads))

			assert.Equal(t, freshSnapshots, staleSnapshots, "fresh and stale-fallback permutations must converge to one canonical tuple output set")
			assert.Equal(t, freshSnapshots, recoverySnapshots, "fresh and telemetry-recovery permutations must converge to one canonical tuple output set")
			assertNoDuplicateOrMissingLogicalSnapshots(t, freshSnapshots, staleSnapshots, "fresh vs stale-fallback")
			assertNoDuplicateOrMissingLogicalSnapshots(t, freshSnapshots, recoverySnapshots, "fresh vs telemetry-recovery")
			assert.NotEqual(t, freshBatches, staleBatches, "telemetry staleness fallback should deterministically alter control decisions")
			assert.NotEqual(t, staleBatches, recoveryBatches, "telemetry recovery should deterministically release fallback hold")

			assertCursorMonotonicByAddress(t, freshSnapshots)
			assertCursorMonotonicByAddress(t, staleSnapshots)
			assertCursorMonotonicByAddress(t, recoverySnapshots)
		})
	}
}

func TestTick_AutoTuneOneChainTelemetryBlackoutDoesNotBleedControlAcrossOtherMandatoryChains(t *testing.T) {
	autoTuneCfg := AutoTuneConfig{
		Enabled:                true,
		MinBatchSize:           60,
		MaxBatchSize:           200,
		StepUp:                 20,
		StepDown:               10,
		LagHighWatermark:       80,
		LagLowWatermark:        20,
		QueueHighWatermarkPct:  90,
		QueueLowWatermarkPct:   10,
		HysteresisTicks:        1,
		CooldownTicks:          1,
		TelemetryStaleTicks:    2,
		TelemetryRecoveryTicks: 2,
	}

	const tickCount = 6
	healthyBaseAddress := "0x1111111111111111111111111111111111111111"
	healthyBTCAddress := "tb1qtelemetryhealthy00000000000000000000000"
	laggingSolanaAddress := "7nYBpkEPkDD6m1JKBGwvftG7bHjJErJPjTH3VbKpump"

	healthyHeads := []int64{260, 261, 262, 263, 264, 265}
	blackoutHeads := []int64{260, 95, 95, 95, 95, 95}

	baseBaseline := newAutoTuneHarnessWithHeadSeries(
		model.ChainBase,
		model.NetworkSepolia,
		healthyBaseAddress,
		100,
		healthyHeads,
		autoTuneCfg,
	)
	baseBaselineSnapshots, baseBaselineBatches := collectAutoTuneTrace(t, baseBaseline, tickCount)

	btcBaseline := newAutoTuneHarnessWithHeadSeries(
		model.ChainBTC,
		model.NetworkTestnet,
		healthyBTCAddress,
		100,
		healthyHeads,
		autoTuneCfg,
	)
	btcBaselineSnapshots, btcBaselineBatches := collectAutoTuneTrace(t, btcBaseline, tickCount)

	baseInterleaved := newAutoTuneHarnessWithHeadSeries(
		model.ChainBase,
		model.NetworkSepolia,
		healthyBaseAddress,
		100,
		healthyHeads,
		autoTuneCfg,
	)
	btcInterleaved := newAutoTuneHarnessWithHeadSeries(
		model.ChainBTC,
		model.NetworkTestnet,
		healthyBTCAddress,
		100,
		healthyHeads,
		autoTuneCfg,
	)
	laggingInterleaved := newAutoTuneHarnessWithHeadSeries(
		model.ChainSolana,
		model.NetworkDevnet,
		laggingSolanaAddress,
		100,
		blackoutHeads,
		autoTuneCfg,
	)

	baseSnapshots := make([]lagAwareJobSnapshot, 0, tickCount)
	baseBatches := make([]int, 0, tickCount)
	btcSnapshots := make([]lagAwareJobSnapshot, 0, tickCount)
	btcBatches := make([]int, 0, tickCount)
	laggingSnapshots := make([]lagAwareJobSnapshot, 0, tickCount)

	for i := 0; i < tickCount; i++ {
		laggingJob := laggingInterleaved.tickAndAdvance(t)
		laggingSnapshots = append(laggingSnapshots, snapshotFromFetchJob(laggingJob))

		baseJob := baseInterleaved.tickAndAdvance(t)
		baseSnapshots = append(baseSnapshots, snapshotFromFetchJob(baseJob))
		baseBatches = append(baseBatches, baseJob.BatchSize)

		btcJob := btcInterleaved.tickAndAdvance(t)
		btcSnapshots = append(btcSnapshots, snapshotFromFetchJob(btcJob))
		btcBatches = append(btcBatches, btcJob.BatchSize)
	}

	assert.Equal(t, baseBaselineSnapshots, baseSnapshots, "solana telemetry blackout must not alter base canonical tuples")
	assert.Equal(t, baseBaselineBatches, baseBatches, "solana telemetry blackout must not alter base control decisions")
	assert.Equal(t, btcBaselineSnapshots, btcSnapshots, "solana telemetry blackout must not alter btc canonical tuples")
	assert.Equal(t, btcBaselineBatches, btcBatches, "solana telemetry blackout must not alter btc control decisions")
	assertNoDuplicateOrMissingLogicalSnapshots(t, baseBaselineSnapshots, baseSnapshots, "base baseline vs interleaved telemetry blackout")
	assertNoDuplicateOrMissingLogicalSnapshots(t, btcBaselineSnapshots, btcSnapshots, "btc baseline vs interleaved telemetry blackout")

	assertCursorMonotonicByAddress(t, baseSnapshots)
	assertCursorMonotonicByAddress(t, btcSnapshots)
	assertCursorMonotonicByAddress(t, laggingSnapshots)
}

func TestTick_AutoTuneTelemetryFallbackReplayResumeConvergesAcrossMandatoryChains(t *testing.T) {
	type testCase struct {
		name    string
		chain   model.Chain
		network model.Network
		address string
	}

	tests := []testCase{
		{
			name:    "solana-devnet",
			chain:   model.ChainSolana,
			network: model.NetworkDevnet,
			address: "7nYBpkEPkDD6m1JKBGwvftG7bHjJErJPjTH3VbKpump",
		},
		{
			name:    "base-sepolia",
			chain:   model.ChainBase,
			network: model.NetworkSepolia,
			address: "0x1111111111111111111111111111111111111111",
		},
		{
			name:    "btc-testnet",
			chain:   model.ChainBTC,
			network: model.NetworkTestnet,
			address: "tb1qtelemetryreplay00000000000000000000000",
		},
	}

	autoTuneCfg := AutoTuneConfig{
		Enabled:                true,
		MinBatchSize:           60,
		MaxBatchSize:           200,
		StepUp:                 20,
		StepDown:               10,
		LagHighWatermark:       80,
		LagLowWatermark:        20,
		QueueHighWatermarkPct:  90,
		QueueLowWatermarkPct:   10,
		HysteresisTicks:        1,
		CooldownTicks:          1,
		TelemetryStaleTicks:    2,
		TelemetryRecoveryTicks: 2,
	}

	heads := []int64{260, 95, 95, 95, 300, 301, 302}
	const splitTick = 4

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			coldHarness := newAutoTuneHarnessWithHeadSeries(tc.chain, tc.network, tc.address, 100, heads, autoTuneCfg)
			coldSnapshots, _ := collectAutoTuneTrace(t, coldHarness, len(heads))

			warmFirst := newAutoTuneHarnessWithHeadSeries(tc.chain, tc.network, tc.address, 100, heads[:splitTick], autoTuneCfg)
			warmSnapshots, _ := collectAutoTuneTrace(t, warmFirst, splitTick)
			restartState := warmFirst.coordinator.ExportAutoTuneRestartState()
			require.NotNil(t, restartState)
			resumeCursor := warmFirst.cursorRepo.GetByAddress(tc.address)
			require.NotNil(t, resumeCursor)

			warmSecond := newAutoTuneHarnessWithWarmStartAndHeadSeries(
				tc.chain,
				tc.network,
				tc.address,
				resumeCursor.CursorSequence,
				heads[splitTick:],
				autoTuneCfg,
				restartState,
			)
			warmTailSnapshots, _ := collectAutoTuneTrace(t, warmSecond, len(heads)-splitTick)
			warmSnapshots = append(warmSnapshots, warmTailSnapshots...)

			assert.Equal(t, coldSnapshots, warmSnapshots, "telemetry-fallback replay/resume must converge to deterministic canonical tuples")
			assertNoDuplicateOrMissingLogicalSnapshots(t, coldSnapshots, warmSnapshots, "cold vs warm telemetry-fallback replay")
			assertCursorMonotonicByAddress(t, warmSnapshots)
		})
	}
}

func TestTick_AutoTuneOperatorOverridePermutationsConvergeCanonicalTuplesAcrossMandatoryChains(t *testing.T) {
	type testCase struct {
		name    string
		chain   model.Chain
		network model.Network
		address string
	}

	tests := []testCase{
		{
			name:    "solana-devnet",
			chain:   model.ChainSolana,
			network: model.NetworkDevnet,
			address: "7nYBpkEPkDD6m1JKBGwvftG7bHjJErJPjTH3VbKpump",
		},
		{
			name:    "base-sepolia",
			chain:   model.ChainBase,
			network: model.NetworkSepolia,
			address: "0x1111111111111111111111111111111111111111",
		},
		{
			name:    "btc-testnet",
			chain:   model.ChainBTC,
			network: model.NetworkTestnet,
			address: "tb1qoverrideperm00000000000000000000000000",
		},
	}

	autoTuneCfg := AutoTuneConfig{
		Enabled:                  true,
		MinBatchSize:             60,
		MaxBatchSize:             200,
		StepUp:                   20,
		StepDown:                 10,
		LagHighWatermark:         80,
		LagLowWatermark:          20,
		QueueHighWatermarkPct:    90,
		QueueLowWatermarkPct:     10,
		HysteresisTicks:          1,
		CooldownTicks:            1,
		OperatorReleaseHoldTicks: 2,
	}
	manualOverrideCfg := autoTuneCfg
	manualOverrideCfg.OperatorOverrideBatchSize = 70
	releaseCfg := autoTuneCfg

	heads := []int64{260, 261, 262, 263, 264, 265, 266, 267}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			autoHarness := newAutoTuneHarnessWithHeadSeries(tc.chain, tc.network, tc.address, 100, heads, autoTuneCfg)
			autoSnapshots, autoBatches := collectAutoTuneTrace(t, autoHarness, len(heads))

			overrideHarness := newAutoTuneHarnessWithHeadSeries(tc.chain, tc.network, tc.address, 100, heads, autoTuneCfg)
			overrideSnapshots := make([]lagAwareJobSnapshot, 0, len(heads))
			overrideBatches := make([]int, 0, len(heads))
			for i := 0; i < len(heads); i++ {
				if i == 2 {
					overrideHarness.coordinator.WithAutoTune(manualOverrideCfg)
				}
				if i == 5 {
					overrideHarness.coordinator.WithAutoTune(releaseCfg)
				}
				job := overrideHarness.tickAndAdvance(t)
				overrideSnapshots = append(overrideSnapshots, snapshotFromFetchJob(job))
				overrideBatches = append(overrideBatches, job.BatchSize)
			}

			assert.Equal(t, autoSnapshots, overrideSnapshots, "auto and operator-override permutations must converge to one canonical tuple output set")
			assertNoDuplicateOrMissingLogicalSnapshots(t, autoSnapshots, overrideSnapshots, "auto vs operator-override permutations")
			assert.NotEqual(t, autoBatches, overrideBatches, "operator override should deterministically alter control decisions without changing canonical tuples")
			assert.Contains(t, overrideBatches, 70, "manual override profile should pin deterministic batch during override hold")
			assertCursorMonotonicByAddress(t, autoSnapshots)
			assertCursorMonotonicByAddress(t, overrideSnapshots)
		})
	}
}

func TestTick_AutoTuneOneChainOperatorOverrideDoesNotBleedControlAcrossOtherMandatoryChains(t *testing.T) {
	autoTuneCfg := AutoTuneConfig{
		Enabled:                  true,
		MinBatchSize:             60,
		MaxBatchSize:             200,
		StepUp:                   20,
		StepDown:                 10,
		LagHighWatermark:         80,
		LagLowWatermark:          20,
		QueueHighWatermarkPct:    90,
		QueueLowWatermarkPct:     10,
		HysteresisTicks:          1,
		CooldownTicks:            1,
		OperatorReleaseHoldTicks: 2,
	}
	manualOverrideCfg := autoTuneCfg
	manualOverrideCfg.OperatorOverrideBatchSize = 70
	releaseCfg := autoTuneCfg

	const tickCount = 8
	healthyBaseAddress := "0x1111111111111111111111111111111111111111"
	healthyBTCAddress := "tb1qoverridehealthy000000000000000000000000"
	laggingSolanaAddress := "7nYBpkEPkDD6m1JKBGwvftG7bHjJErJPjTH3VbKpump"

	healthyHeads := []int64{130, 131, 132, 133, 134, 135, 136, 137}
	laggingHeads := []int64{260, 261, 262, 263, 264, 265, 266, 267}

	baseBaseline := newAutoTuneHarnessWithHeadSeries(
		model.ChainBase,
		model.NetworkSepolia,
		healthyBaseAddress,
		120,
		healthyHeads,
		autoTuneCfg,
	)
	baseBaselineSnapshots, baseBaselineBatches := collectAutoTuneTrace(t, baseBaseline, tickCount)

	btcBaseline := newAutoTuneHarnessWithHeadSeries(
		model.ChainBTC,
		model.NetworkTestnet,
		healthyBTCAddress,
		120,
		healthyHeads,
		autoTuneCfg,
	)
	btcBaselineSnapshots, btcBaselineBatches := collectAutoTuneTrace(t, btcBaseline, tickCount)

	laggingBaseline := newAutoTuneHarnessWithHeadSeries(
		model.ChainSolana,
		model.NetworkDevnet,
		laggingSolanaAddress,
		100,
		laggingHeads,
		autoTuneCfg,
	)
	_, laggingBaselineBatches := collectAutoTuneTrace(t, laggingBaseline, tickCount)

	baseInterleaved := newAutoTuneHarnessWithHeadSeries(
		model.ChainBase,
		model.NetworkSepolia,
		healthyBaseAddress,
		120,
		healthyHeads,
		autoTuneCfg,
	)
	btcInterleaved := newAutoTuneHarnessWithHeadSeries(
		model.ChainBTC,
		model.NetworkTestnet,
		healthyBTCAddress,
		120,
		healthyHeads,
		autoTuneCfg,
	)
	laggingInterleaved := newAutoTuneHarnessWithHeadSeries(
		model.ChainSolana,
		model.NetworkDevnet,
		laggingSolanaAddress,
		100,
		laggingHeads,
		autoTuneCfg,
	)

	baseSnapshots := make([]lagAwareJobSnapshot, 0, tickCount)
	baseBatches := make([]int, 0, tickCount)
	btcSnapshots := make([]lagAwareJobSnapshot, 0, tickCount)
	btcBatches := make([]int, 0, tickCount)
	laggingSnapshots := make([]lagAwareJobSnapshot, 0, tickCount)
	laggingBatches := make([]int, 0, tickCount)

	for i := 0; i < tickCount; i++ {
		if i == 2 {
			laggingInterleaved.coordinator.WithAutoTune(manualOverrideCfg)
		}
		if i == 5 {
			laggingInterleaved.coordinator.WithAutoTune(releaseCfg)
		}

		laggingJob := laggingInterleaved.tickAndAdvance(t)
		laggingSnapshots = append(laggingSnapshots, snapshotFromFetchJob(laggingJob))
		laggingBatches = append(laggingBatches, laggingJob.BatchSize)

		baseJob := baseInterleaved.tickAndAdvance(t)
		baseSnapshots = append(baseSnapshots, snapshotFromFetchJob(baseJob))
		baseBatches = append(baseBatches, baseJob.BatchSize)

		btcJob := btcInterleaved.tickAndAdvance(t)
		btcSnapshots = append(btcSnapshots, snapshotFromFetchJob(btcJob))
		btcBatches = append(btcBatches, btcJob.BatchSize)
	}

	assert.Equal(t, baseBaselineSnapshots, baseSnapshots, "solana operator override must not alter base canonical tuples")
	assert.Equal(t, baseBaselineBatches, baseBatches, "solana operator override must not alter base control decisions")
	assert.Equal(t, btcBaselineSnapshots, btcSnapshots, "solana operator override must not alter btc canonical tuples")
	assert.Equal(t, btcBaselineBatches, btcBatches, "solana operator override must not alter btc control decisions")
	assert.NotEqual(t, laggingBaselineBatches, laggingBatches, "operator override should be chain-scoped and only alter lagging chain control decisions")
	assert.Contains(t, laggingBatches, 70, "lagging chain override path should pin deterministic manual batch")
	assertNoDuplicateOrMissingLogicalSnapshots(t, baseBaselineSnapshots, baseSnapshots, "base baseline vs interleaved operator override")
	assertNoDuplicateOrMissingLogicalSnapshots(t, btcBaselineSnapshots, btcSnapshots, "btc baseline vs interleaved operator override")

	assertCursorMonotonicByAddress(t, baseSnapshots)
	assertCursorMonotonicByAddress(t, btcSnapshots)
	assertCursorMonotonicByAddress(t, laggingSnapshots)
}

func TestTick_AutoTuneOperatorOverrideReplayResumeConvergesAcrossMandatoryChains(t *testing.T) {
	type testCase struct {
		name    string
		chain   model.Chain
		network model.Network
		address string
	}

	tests := []testCase{
		{
			name:    "solana-devnet",
			chain:   model.ChainSolana,
			network: model.NetworkDevnet,
			address: "7nYBpkEPkDD6m1JKBGwvftG7bHjJErJPjTH3VbKpump",
		},
		{
			name:    "base-sepolia",
			chain:   model.ChainBase,
			network: model.NetworkSepolia,
			address: "0x1111111111111111111111111111111111111111",
		},
		{
			name:    "btc-testnet",
			chain:   model.ChainBTC,
			network: model.NetworkTestnet,
			address: "tb1qoverridereplay000000000000000000000000",
		},
	}

	autoTuneCfg := AutoTuneConfig{
		Enabled:                  true,
		MinBatchSize:             60,
		MaxBatchSize:             200,
		StepUp:                   20,
		StepDown:                 10,
		LagHighWatermark:         80,
		LagLowWatermark:          20,
		QueueHighWatermarkPct:    90,
		QueueLowWatermarkPct:     10,
		HysteresisTicks:          1,
		CooldownTicks:            1,
		OperatorReleaseHoldTicks: 3,
	}
	manualOverrideCfg := autoTuneCfg
	manualOverrideCfg.OperatorOverrideBatchSize = 70
	releaseCfg := autoTuneCfg

	heads := []int64{260, 261, 262, 263, 264, 265, 266, 267}
	const splitTick = 5

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			coldHarness := newAutoTuneHarnessWithHeadSeries(tc.chain, tc.network, tc.address, 100, heads, autoTuneCfg)
			coldSnapshots := make([]lagAwareJobSnapshot, 0, len(heads))
			coldBatches := make([]int, 0, len(heads))
			for i := 0; i < len(heads); i++ {
				if i == 2 {
					coldHarness.coordinator.WithAutoTune(manualOverrideCfg)
				}
				if i == 4 {
					coldHarness.coordinator.WithAutoTune(releaseCfg)
				}
				job := coldHarness.tickAndAdvance(t)
				coldSnapshots = append(coldSnapshots, snapshotFromFetchJob(job))
				coldBatches = append(coldBatches, job.BatchSize)
			}

			warmFirst := newAutoTuneHarnessWithHeadSeries(tc.chain, tc.network, tc.address, 100, heads[:splitTick], autoTuneCfg)
			warmSnapshots := make([]lagAwareJobSnapshot, 0, len(heads))
			warmBatches := make([]int, 0, len(heads))
			for i := 0; i < splitTick; i++ {
				if i == 2 {
					warmFirst.coordinator.WithAutoTune(manualOverrideCfg)
				}
				if i == 4 {
					warmFirst.coordinator.WithAutoTune(releaseCfg)
				}
				job := warmFirst.tickAndAdvance(t)
				warmSnapshots = append(warmSnapshots, snapshotFromFetchJob(job))
				warmBatches = append(warmBatches, job.BatchSize)
			}

			restartState := warmFirst.coordinator.ExportAutoTuneRestartState()
			require.NotNil(t, restartState)
			assert.False(t, restartState.OverrideManualActive)
			assert.Greater(t, restartState.OverrideReleaseRemaining, 0, "restart state must preserve release-hold countdown at override boundary")
			resumeCursor := warmFirst.cursorRepo.GetByAddress(tc.address)
			require.NotNil(t, resumeCursor)

			warmSecond := newAutoTuneHarnessWithWarmStartAndHeadSeries(
				tc.chain,
				tc.network,
				tc.address,
				resumeCursor.CursorSequence,
				heads[splitTick:],
				releaseCfg,
				restartState,
			)
			warmTailSnapshots, warmTailBatches := collectAutoTuneTrace(t, warmSecond, len(heads)-splitTick)
			warmSnapshots = append(warmSnapshots, warmTailSnapshots...)
			warmBatches = append(warmBatches, warmTailBatches...)

			assert.Equal(t, coldSnapshots, warmSnapshots, "operator-override replay/resume must converge to deterministic canonical tuples")
			assert.Equal(t, coldBatches, warmBatches, "operator-override replay/resume must preserve deterministic control decisions")
			assertNoDuplicateOrMissingLogicalSnapshots(t, coldSnapshots, warmSnapshots, "cold vs warm operator-override replay")
			assertCursorMonotonicByAddress(t, warmSnapshots)
		})
	}
}

func TestTick_AutoTunePolicyVersionPermutationsConvergeCanonicalTuplesAcrossMandatoryChains(t *testing.T) {
	type testCase struct {
		name    string
		chain   model.Chain
		network model.Network
		address string
	}

	tests := []testCase{
		{
			name:    "solana-devnet",
			chain:   model.ChainSolana,
			network: model.NetworkDevnet,
			address: "7nYBpkEPkDD6m1JKBGwvftG7bHjJErJPjTH3VbKpump",
		},
		{
			name:    "base-sepolia",
			chain:   model.ChainBase,
			network: model.NetworkSepolia,
			address: "0x1111111111111111111111111111111111111111",
		},
		{
			name:    "btc-testnet",
			chain:   model.ChainBTC,
			network: model.NetworkTestnet,
			address: "tb1qpolicyperm0000000000000000000000000000",
		},
	}

	policyV1Cfg := AutoTuneConfig{
		Enabled:                   true,
		MinBatchSize:              60,
		MaxBatchSize:              400,
		StepUp:                    20,
		StepDown:                  10,
		LagHighWatermark:          80,
		LagLowWatermark:           20,
		QueueHighWatermarkPct:     90,
		QueueLowWatermarkPct:      10,
		HysteresisTicks:           1,
		CooldownTicks:             1,
		PolicyVersion:             "policy-v1",
		PolicyActivationHoldTicks: 1,
	}
	policyV2Cfg := policyV1Cfg
	policyV2Cfg.PolicyVersion = "policy-v2"

	heads := []int64{260, 261, 262, 263, 264, 265, 266, 267, 268}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			baselineHarness := newAutoTuneHarnessWithHeadSeries(tc.chain, tc.network, tc.address, 100, heads, policyV1Cfg)
			baselineSnapshots, baselineBatches := collectAutoTuneTrace(t, baselineHarness, len(heads))

			rolloutHarness := newAutoTuneHarnessWithHeadSeries(tc.chain, tc.network, tc.address, 100, heads, policyV1Cfg)
			rolloutSnapshots := make([]lagAwareJobSnapshot, 0, len(heads))
			rolloutBatches := make([]int, 0, len(heads))
			for i := 0; i < len(heads); i++ {
				if i == 2 {
					rolloutHarness.coordinator.WithAutoTune(policyV2Cfg)
				}
				job := rolloutHarness.tickAndAdvance(t)
				rolloutSnapshots = append(rolloutSnapshots, snapshotFromFetchJob(job))
				rolloutBatches = append(rolloutBatches, job.BatchSize)
			}

			rollbackHarness := newAutoTuneHarnessWithHeadSeries(tc.chain, tc.network, tc.address, 100, heads, policyV1Cfg)
			rollbackSnapshots := make([]lagAwareJobSnapshot, 0, len(heads))
			rollbackBatches := make([]int, 0, len(heads))
			for i := 0; i < len(heads); i++ {
				if i == 2 {
					rollbackHarness.coordinator.WithAutoTune(policyV2Cfg)
				}
				if i == 4 {
					rollbackHarness.coordinator.WithAutoTune(policyV1Cfg)
				}
				if i == 6 {
					rollbackHarness.coordinator.WithAutoTune(policyV2Cfg)
				}
				job := rollbackHarness.tickAndAdvance(t)
				rollbackSnapshots = append(rollbackSnapshots, snapshotFromFetchJob(job))
				rollbackBatches = append(rollbackBatches, job.BatchSize)
			}

			assert.Equal(t, baselineSnapshots, rolloutSnapshots, "policy-v1 baseline and policy-v2 rollout permutations must converge to one canonical tuple output set")
			assert.Equal(t, baselineSnapshots, rollbackSnapshots, "policy rollback/re-apply permutations must converge to one canonical tuple output set")
			assertNoDuplicateOrMissingLogicalSnapshots(t, baselineSnapshots, rolloutSnapshots, "policy-v1 baseline vs policy-v2 rollout")
			assertNoDuplicateOrMissingLogicalSnapshots(t, baselineSnapshots, rollbackSnapshots, "policy-v1 baseline vs rollback/re-apply")
			assertCursorMonotonicByAddress(t, baselineSnapshots)
			assertCursorMonotonicByAddress(t, rolloutSnapshots)
			assertCursorMonotonicByAddress(t, rollbackSnapshots)

			assert.NotEqual(t, baselineBatches, rolloutBatches, "policy rollout should alter control decisions deterministically at transition boundaries")
			assert.NotEqual(t, baselineBatches, rollbackBatches, "rollback/re-apply should alter control decisions deterministically at transition boundaries")
			assert.Equal(t, rolloutBatches[1], rolloutBatches[2], "policy rollout boundary must apply deterministic activation hold")
			assert.Equal(t, rollbackBatches[1], rollbackBatches[2], "policy-v1->policy-v2 boundary must hold deterministically")
			assert.Equal(t, rollbackBatches[3], rollbackBatches[4], "policy-v2->policy-v1 boundary must hold deterministically")
			assert.Equal(t, rollbackBatches[5], rollbackBatches[6], "policy-v1->policy-v2 re-apply boundary must hold deterministically")
		})
	}
}

func TestTick_AutoTuneOneChainPolicyVersionTransitionDoesNotBleedControlAcrossOtherMandatoryChains(t *testing.T) {
	policyV1Cfg := AutoTuneConfig{
		Enabled:                   true,
		MinBatchSize:              60,
		MaxBatchSize:              320,
		StepUp:                    20,
		StepDown:                  10,
		LagHighWatermark:          80,
		LagLowWatermark:           20,
		QueueHighWatermarkPct:     90,
		QueueLowWatermarkPct:      10,
		HysteresisTicks:           1,
		CooldownTicks:             1,
		PolicyVersion:             "policy-v1",
		PolicyActivationHoldTicks: 1,
	}
	policyV2Cfg := policyV1Cfg
	policyV2Cfg.PolicyVersion = "policy-v2"

	const tickCount = 8
	healthyBaseAddress := "0x1111111111111111111111111111111111111111"
	healthyBTCAddress := "tb1qpolicyhealthy00000000000000000000000000"
	laggingSolanaAddress := "7nYBpkEPkDD6m1JKBGwvftG7bHjJErJPjTH3VbKpump"

	healthyHeads := []int64{130, 131, 132, 133, 134, 135, 136, 137}
	laggingHeads := []int64{260, 261, 262, 263, 264, 265, 266, 267}

	baseBaseline := newAutoTuneHarnessWithHeadSeries(
		model.ChainBase,
		model.NetworkSepolia,
		healthyBaseAddress,
		120,
		healthyHeads,
		policyV1Cfg,
	)
	baseBaselineSnapshots, baseBaselineBatches := collectAutoTuneTrace(t, baseBaseline, tickCount)

	btcBaseline := newAutoTuneHarnessWithHeadSeries(
		model.ChainBTC,
		model.NetworkTestnet,
		healthyBTCAddress,
		120,
		healthyHeads,
		policyV1Cfg,
	)
	btcBaselineSnapshots, btcBaselineBatches := collectAutoTuneTrace(t, btcBaseline, tickCount)

	laggingBaseline := newAutoTuneHarnessWithHeadSeries(
		model.ChainSolana,
		model.NetworkDevnet,
		laggingSolanaAddress,
		100,
		laggingHeads,
		policyV1Cfg,
	)
	laggingBaselineSnapshots, laggingBaselineBatches := collectAutoTuneTrace(t, laggingBaseline, tickCount)

	baseInterleaved := newAutoTuneHarnessWithHeadSeries(
		model.ChainBase,
		model.NetworkSepolia,
		healthyBaseAddress,
		120,
		healthyHeads,
		policyV1Cfg,
	)
	btcInterleaved := newAutoTuneHarnessWithHeadSeries(
		model.ChainBTC,
		model.NetworkTestnet,
		healthyBTCAddress,
		120,
		healthyHeads,
		policyV1Cfg,
	)
	laggingInterleaved := newAutoTuneHarnessWithHeadSeries(
		model.ChainSolana,
		model.NetworkDevnet,
		laggingSolanaAddress,
		100,
		laggingHeads,
		policyV1Cfg,
	)

	baseSnapshots := make([]lagAwareJobSnapshot, 0, tickCount)
	baseBatches := make([]int, 0, tickCount)
	btcSnapshots := make([]lagAwareJobSnapshot, 0, tickCount)
	btcBatches := make([]int, 0, tickCount)
	laggingSnapshots := make([]lagAwareJobSnapshot, 0, tickCount)
	laggingBatches := make([]int, 0, tickCount)

	for i := 0; i < tickCount; i++ {
		if i == 2 {
			laggingInterleaved.coordinator.WithAutoTune(policyV2Cfg)
		}
		if i == 5 {
			laggingInterleaved.coordinator.WithAutoTune(policyV1Cfg)
		}

		laggingJob := laggingInterleaved.tickAndAdvance(t)
		laggingSnapshots = append(laggingSnapshots, snapshotFromFetchJob(laggingJob))
		laggingBatches = append(laggingBatches, laggingJob.BatchSize)

		baseJob := baseInterleaved.tickAndAdvance(t)
		baseSnapshots = append(baseSnapshots, snapshotFromFetchJob(baseJob))
		baseBatches = append(baseBatches, baseJob.BatchSize)

		btcJob := btcInterleaved.tickAndAdvance(t)
		btcSnapshots = append(btcSnapshots, snapshotFromFetchJob(btcJob))
		btcBatches = append(btcBatches, btcJob.BatchSize)
	}

	assert.Equal(t, baseBaselineSnapshots, baseSnapshots, "solana policy transition must not alter base canonical tuples")
	assert.Equal(t, baseBaselineBatches, baseBatches, "solana policy transition must not alter base control decisions")
	assert.Equal(t, btcBaselineSnapshots, btcSnapshots, "solana policy transition must not alter btc canonical tuples")
	assert.Equal(t, btcBaselineBatches, btcBatches, "solana policy transition must not alter btc control decisions")
	assert.Equal(t, laggingBaselineSnapshots, laggingSnapshots, "policy transition must preserve lagging-chain canonical tuples")
	assert.NotEqual(t, laggingBaselineBatches, laggingBatches, "policy transition should remain chain-scoped and alter only lagging-chain control decisions")
	assert.Equal(t, laggingBatches[1], laggingBatches[2], "policy rollout boundary hold should be chain-scoped to lagging chain")
	assert.Equal(t, laggingBatches[4], laggingBatches[5], "policy rollback boundary hold should be chain-scoped to lagging chain")
	assertNoDuplicateOrMissingLogicalSnapshots(t, baseBaselineSnapshots, baseSnapshots, "base baseline vs interleaved policy transition")
	assertNoDuplicateOrMissingLogicalSnapshots(t, btcBaselineSnapshots, btcSnapshots, "btc baseline vs interleaved policy transition")
	assertNoDuplicateOrMissingLogicalSnapshots(t, laggingBaselineSnapshots, laggingSnapshots, "lagging baseline vs interleaved policy transition")

	assertCursorMonotonicByAddress(t, baseSnapshots)
	assertCursorMonotonicByAddress(t, btcSnapshots)
	assertCursorMonotonicByAddress(t, laggingSnapshots)
}

func TestTick_AutoTunePolicyVersionReplayResumeConvergesAcrossMandatoryChains(t *testing.T) {
	type testCase struct {
		name    string
		chain   model.Chain
		network model.Network
		address string
	}

	tests := []testCase{
		{
			name:    "solana-devnet",
			chain:   model.ChainSolana,
			network: model.NetworkDevnet,
			address: "7nYBpkEPkDD6m1JKBGwvftG7bHjJErJPjTH3VbKpump",
		},
		{
			name:    "base-sepolia",
			chain:   model.ChainBase,
			network: model.NetworkSepolia,
			address: "0x1111111111111111111111111111111111111111",
		},
		{
			name:    "btc-testnet",
			chain:   model.ChainBTC,
			network: model.NetworkTestnet,
			address: "tb1qpolicyreplay0000000000000000000000000",
		},
	}

	policyV1Cfg := AutoTuneConfig{
		Enabled:                   true,
		MinBatchSize:              60,
		MaxBatchSize:              320,
		StepUp:                    20,
		StepDown:                  10,
		LagHighWatermark:          80,
		LagLowWatermark:           20,
		QueueHighWatermarkPct:     90,
		QueueLowWatermarkPct:      10,
		HysteresisTicks:           1,
		CooldownTicks:             1,
		PolicyVersion:             "policy-v1",
		PolicyActivationHoldTicks: 2,
	}
	policyV2Cfg := policyV1Cfg
	policyV2Cfg.PolicyVersion = "policy-v2"

	heads := []int64{260, 261, 262, 263, 264, 265, 266, 267}
	const splitTick = 3

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			coldHarness := newAutoTuneHarnessWithHeadSeries(tc.chain, tc.network, tc.address, 100, heads, policyV1Cfg)
			coldSnapshots := make([]lagAwareJobSnapshot, 0, len(heads))
			coldBatches := make([]int, 0, len(heads))
			for i := 0; i < len(heads); i++ {
				if i == 2 {
					coldHarness.coordinator.WithAutoTune(policyV2Cfg)
				}
				job := coldHarness.tickAndAdvance(t)
				coldSnapshots = append(coldSnapshots, snapshotFromFetchJob(job))
				coldBatches = append(coldBatches, job.BatchSize)
			}

			warmFirst := newAutoTuneHarnessWithHeadSeries(tc.chain, tc.network, tc.address, 100, heads[:splitTick], policyV1Cfg)
			warmSnapshots := make([]lagAwareJobSnapshot, 0, len(heads))
			warmBatches := make([]int, 0, len(heads))
			for i := 0; i < splitTick; i++ {
				if i == 2 {
					warmFirst.coordinator.WithAutoTune(policyV2Cfg)
				}
				job := warmFirst.tickAndAdvance(t)
				warmSnapshots = append(warmSnapshots, snapshotFromFetchJob(job))
				warmBatches = append(warmBatches, job.BatchSize)
			}

			restartState := warmFirst.coordinator.ExportAutoTuneRestartState()
			require.NotNil(t, restartState)
			assert.Equal(t, "policy-v2", restartState.PolicyVersion)
			assert.Equal(t, int64(1), restartState.PolicyEpoch)
			assert.Greater(t, restartState.PolicyActivationRemaining, 0, "restart state must preserve policy activation hold countdown at rollout boundary")
			resumeCursor := warmFirst.cursorRepo.GetByAddress(tc.address)
			require.NotNil(t, resumeCursor)

			warmSecond := newAutoTuneHarnessWithWarmStartAndHeadSeries(
				tc.chain,
				tc.network,
				tc.address,
				resumeCursor.CursorSequence,
				heads[splitTick:],
				policyV2Cfg,
				restartState,
			)
			warmTailSnapshots, warmTailBatches := collectAutoTuneTrace(t, warmSecond, len(heads)-splitTick)
			warmSnapshots = append(warmSnapshots, warmTailSnapshots...)
			warmBatches = append(warmBatches, warmTailBatches...)

			assert.Equal(t, coldSnapshots, warmSnapshots, "policy-version replay/resume must converge to deterministic canonical tuples")
			assert.Equal(t, coldBatches, warmBatches, "policy-version replay/resume must preserve deterministic control decisions")
			assertNoDuplicateOrMissingLogicalSnapshots(t, coldSnapshots, warmSnapshots, "cold vs warm policy-version replay")
			assertCursorMonotonicByAddress(t, warmSnapshots)
		})
	}
}

type lagAwareJobSnapshot struct {
	Address        string
	CursorValue    string
	CursorSequence int64
}

type autoTuneHarness struct {
	coordinator *Coordinator
	cursorRepo  *inMemoryCursorRepo
	jobCh       chan event.FetchJob
}

type scriptedWatchedAddressRepo struct {
	ticks [][]model.WatchedAddress
	index int
}

func (r *scriptedWatchedAddressRepo) GetActive(context.Context, model.Chain, model.Network) ([]model.WatchedAddress, error) {
	if r.index >= len(r.ticks) {
		return []model.WatchedAddress{}, nil
	}
	active := make([]model.WatchedAddress, len(r.ticks[r.index]))
	copy(active, r.ticks[r.index])
	r.index++
	return active, nil
}

func (*scriptedWatchedAddressRepo) Upsert(context.Context, *model.WatchedAddress) error {
	return nil
}

func (*scriptedWatchedAddressRepo) FindByAddress(context.Context, model.Chain, model.Network, string) (*model.WatchedAddress, error) {
	return nil, nil
}

type inMemoryCursorRepo struct {
	state map[string]*model.AddressCursor
}

func (r *inMemoryCursorRepo) Get(_ context.Context, _ model.Chain, _ model.Network, address string) (*model.AddressCursor, error) {
	return r.GetByAddress(address), nil
}

func (r *inMemoryCursorRepo) GetByAddress(address string) *model.AddressCursor {
	cursor, ok := r.state[address]
	if !ok || cursor == nil {
		return nil
	}
	return cloneAddressCursor(cursor)
}

func (r *inMemoryCursorRepo) UpsertTx(context.Context, *sql.Tx, model.Chain, model.Network, string, *string, int64, int64) error {
	return nil
}

func (r *inMemoryCursorRepo) EnsureExists(context.Context, model.Chain, model.Network, string) error {
	return nil
}

func runLagAwareTickScenario(
	t *testing.T,
	chain model.Chain,
	network model.Network,
	ticks [][]model.WatchedAddress,
	initialByKey map[string]*model.AddressCursor,
) ([]lagAwareJobSnapshot, map[string]*model.AddressCursor) {
	t.Helper()

	watchedRepo := &scriptedWatchedAddressRepo{ticks: ticks}
	cursorRepo := &inMemoryCursorRepo{state: cloneCursorState(initialByKey)}
	jobCh := make(chan event.FetchJob, len(ticks)+1)
	c := New(chain, network, watchedRepo, cursorRepo, 100, time.Second, jobCh, slog.Default())

	snapshots := make([]lagAwareJobSnapshot, 0, len(ticks))
	lastByAddress := make(map[string]int64, len(ticks))

	for _, active := range ticks {
		expectedMinSeq := lagAwareMinSequence(active, cursorRepo)

		require.NoError(t, c.tick(context.Background()))
		require.Len(t, jobCh, 1)

		job := <-jobCh
		assert.Equal(t, expectedMinSeq, job.CursorSequence)

		if last, ok := lastByAddress[job.Address]; ok {
			assert.GreaterOrEqual(t, job.CursorSequence, last)
		}
		lastByAddress[job.Address] = job.CursorSequence

		cursorValue := ""
		if job.CursorValue != nil {
			cursorValue = *job.CursorValue
		}
		snapshots = append(snapshots, lagAwareJobSnapshot{
			Address:        job.Address,
			CursorValue:    cursorValue,
			CursorSequence: job.CursorSequence,
		})

		nextSeq := job.CursorSequence + 5
		nextCursor := syntheticCursorValue(chain, nextSeq)
		cursorRepo.state[job.Address] = &model.AddressCursor{
			Address:        job.Address,
			CursorValue:    &nextCursor,
			CursorSequence: nextSeq,
		}
	}

	return snapshots, cloneCursorState(cursorRepo.state)
}

func runCheckpointIntegrityTickScenario(
	t *testing.T,
	chain model.Chain,
	network model.Network,
	ticks [][]model.WatchedAddress,
	initialByKey map[string]*model.AddressCursor,
) []lagAwareJobSnapshot {
	t.Helper()

	watchedRepo := &scriptedWatchedAddressRepo{ticks: ticks}
	cursorRepo := &inMemoryCursorRepo{state: cloneCursorState(initialByKey)}
	jobCh := make(chan event.FetchJob, len(ticks)+1)
	c := New(chain, network, watchedRepo, cursorRepo, 100, time.Second, jobCh, slog.Default())

	snapshots := make([]lagAwareJobSnapshot, 0, len(ticks))
	for i := range ticks {
		require.NoError(t, c.tick(context.Background()))
		require.Len(t, jobCh, 1)

		job := <-jobCh
		cursorValue := ""
		if job.CursorValue != nil {
			cursorValue = *job.CursorValue
		}
		snapshots = append(snapshots, lagAwareJobSnapshot{
			Address:        job.Address,
			CursorValue:    cursorValue,
			CursorSequence: job.CursorSequence,
		})

		nextSeq := job.CursorSequence + 5
		nextCursor := syntheticCursorValue(chain, nextSeq)
		lastFetched := time.Unix(1700000000+int64(i), 0)
		cursorRepo.state[job.Address] = &model.AddressCursor{
			Address:        job.Address,
			CursorValue:    &nextCursor,
			CursorSequence: nextSeq,
			ItemsProcessed: int64(i + 1),
			LastFetchedAt:  &lastFetched,
		}
	}

	return snapshots
}

func lagAwareMinSequence(active []model.WatchedAddress, cursorRepo *inMemoryCursorRepo) int64 {
	var (
		minSeq int64
		set    bool
	)
	for _, watched := range active {
		cursor := cursorRepo.GetByAddress(watched.Address)
		seq := int64(0)
		if cursor != nil && cursor.CursorSequence > 0 {
			seq = cursor.CursorSequence
		}
		if !set || seq < minSeq {
			minSeq = seq
			set = true
		}
	}
	if !set {
		return 0
	}
	return minSeq
}

func syntheticCursorValue(chain model.Chain, seq int64) string {
	if isEVMChain(chain) {
		return fmt.Sprintf("0x%064x", seq)
	}
	return fmt.Sprintf("sig-%d", seq)
}

func cloneCursorState(state map[string]*model.AddressCursor) map[string]*model.AddressCursor {
	cloned := make(map[string]*model.AddressCursor, len(state))
	for address, cursor := range state {
		cloned[address] = cloneAddressCursor(cursor)
	}
	return cloned
}

func cloneAddressCursor(cursor *model.AddressCursor) *model.AddressCursor {
	if cursor == nil {
		return nil
	}
	cloned := *cursor
	if cursor.CursorValue != nil {
		value := *cursor.CursorValue
		cloned.CursorValue = &value
	}
	return &cloned
}

func runAutoTuneTickScenario(
	t *testing.T,
	chain model.Chain,
	network model.Network,
	ticks [][]model.WatchedAddress,
	initialByKey map[string]*model.AddressCursor,
	headSequence int64,
	autoTuneCfg *AutoTuneConfig,
) ([]lagAwareJobSnapshot, []int) {
	t.Helper()

	watchedRepo := &scriptedWatchedAddressRepo{ticks: ticks}
	cursorRepo := &inMemoryCursorRepo{state: cloneCursorState(initialByKey)}
	jobCh := make(chan event.FetchJob, len(ticks)+1)
	c := New(chain, network, watchedRepo, cursorRepo, 100, time.Second, jobCh, slog.Default()).
		WithHeadProvider(&stubHeadProvider{head: headSequence})
	if autoTuneCfg != nil {
		c = c.WithAutoTune(*autoTuneCfg)
	}

	snapshots := make([]lagAwareJobSnapshot, 0, len(ticks))
	batches := make([]int, 0, len(ticks))
	for i := range ticks {
		require.NoError(t, c.tick(context.Background()))
		require.Len(t, jobCh, 1)
		job := <-jobCh

		cursorValue := ""
		if job.CursorValue != nil {
			cursorValue = *job.CursorValue
		}
		snapshots = append(snapshots, lagAwareJobSnapshot{
			Address:        job.Address,
			CursorValue:    cursorValue,
			CursorSequence: job.CursorSequence,
		})
		batches = append(batches, job.BatchSize)

		nextSeq := job.CursorSequence + 5
		nextCursor := syntheticCursorValue(chain, nextSeq)
		lastFetched := time.Unix(1700001000+int64(i), 0)
		cursorRepo.state[job.Address] = &model.AddressCursor{
			Address:        job.Address,
			CursorValue:    &nextCursor,
			CursorSequence: nextSeq,
			ItemsProcessed: int64(i + 1),
			LastFetchedAt:  &lastFetched,
		}
	}
	return snapshots, batches
}

func newAutoTuneHarness(
	chain model.Chain,
	network model.Network,
	address string,
	initialSequence int64,
	headSequence int64,
	tickCount int,
	autoTuneCfg AutoTuneConfig,
) *autoTuneHarness {
	return newAutoTuneHarnessWithWarmStart(
		chain,
		network,
		address,
		initialSequence,
		headSequence,
		tickCount,
		autoTuneCfg,
		nil,
	)
}

func newAutoTuneHarnessWithWarmStart(
	chain model.Chain,
	network model.Network,
	address string,
	initialSequence int64,
	headSequence int64,
	tickCount int,
	autoTuneCfg AutoTuneConfig,
	warmState *AutoTuneRestartState,
) *autoTuneHarness {
	ticks := make([][]model.WatchedAddress, tickCount)
	for i := range ticks {
		ticks[i] = []model.WatchedAddress{{Address: address}}
	}
	watchedRepo := &scriptedWatchedAddressRepo{ticks: ticks}
	cursorValue := syntheticCursorValue(chain, initialSequence)
	cursorRepo := &inMemoryCursorRepo{
		state: map[string]*model.AddressCursor{
			address: {
				Address:        address,
				CursorValue:    &cursorValue,
				CursorSequence: initialSequence,
			},
		},
	}
	jobCh := make(chan event.FetchJob, tickCount+1)
	coord := New(chain, network, watchedRepo, cursorRepo, 100, time.Second, jobCh, slog.Default()).
		WithHeadProvider(&stubHeadProvider{head: headSequence})
	if warmState != nil {
		coord = coord.WithAutoTuneWarmStart(autoTuneCfg, warmState)
	} else {
		coord = coord.WithAutoTune(autoTuneCfg)
	}

	return &autoTuneHarness{
		coordinator: coord,
		cursorRepo:  cursorRepo,
		jobCh:       jobCh,
	}
}

func newAutoTuneHarnessWithHeadSeries(
	chain model.Chain,
	network model.Network,
	address string,
	initialSequence int64,
	headSeries []int64,
	autoTuneCfg AutoTuneConfig,
) *autoTuneHarness {
	harness := newAutoTuneHarness(
		chain,
		network,
		address,
		initialSequence,
		0,
		len(headSeries),
		autoTuneCfg,
	)
	harness.coordinator.WithHeadProvider(&stubHeadProvider{heads: append([]int64(nil), headSeries...)})
	return harness
}

func newAutoTuneHarnessWithWarmStartAndHeadSeries(
	chain model.Chain,
	network model.Network,
	address string,
	initialSequence int64,
	headSeries []int64,
	autoTuneCfg AutoTuneConfig,
	warmState *AutoTuneRestartState,
) *autoTuneHarness {
	harness := newAutoTuneHarnessWithWarmStart(
		chain,
		network,
		address,
		initialSequence,
		0,
		len(headSeries),
		autoTuneCfg,
		warmState,
	)
	harness.coordinator.WithHeadProvider(&stubHeadProvider{heads: append([]int64(nil), headSeries...)})
	return harness
}

func (h *autoTuneHarness) tickAndAdvance(t *testing.T) event.FetchJob {
	t.Helper()
	require.NoError(t, h.coordinator.tick(context.Background()))
	require.Len(t, h.jobCh, 1)
	job := <-h.jobCh

	nextSeq := job.CursorSequence + 1
	nextCursor := syntheticCursorValue(job.Chain, nextSeq)
	h.cursorRepo.state[job.Address] = &model.AddressCursor{
		Address:        job.Address,
		CursorValue:    &nextCursor,
		CursorSequence: nextSeq,
	}
	return job
}

func collectAutoTuneTrace(t *testing.T, harness *autoTuneHarness, tickCount int) ([]lagAwareJobSnapshot, []int) {
	t.Helper()
	snapshots := make([]lagAwareJobSnapshot, 0, tickCount)
	batches := make([]int, 0, tickCount)
	for i := 0; i < tickCount; i++ {
		job := harness.tickAndAdvance(t)
		snapshots = append(snapshots, snapshotFromFetchJob(job))
		batches = append(batches, job.BatchSize)
	}
	return snapshots, batches
}

func snapshotFromFetchJob(job event.FetchJob) lagAwareJobSnapshot {
	cursorValue := ""
	if job.CursorValue != nil {
		cursorValue = *job.CursorValue
	}
	return lagAwareJobSnapshot{
		Address:        job.Address,
		CursorValue:    cursorValue,
		CursorSequence: job.CursorSequence,
	}
}

func assertCursorMonotonicByAddress(t *testing.T, snapshots []lagAwareJobSnapshot) {
	t.Helper()
	seen := make(map[string]int64, len(snapshots))
	for _, snapshot := range snapshots {
		if last, ok := seen[snapshot.Address]; ok {
			assert.GreaterOrEqual(t, snapshot.CursorSequence, last, "cursor sequence must remain monotonic per address")
		}
		seen[snapshot.Address] = snapshot.CursorSequence
	}
}

func assertNoDuplicateOrMissingLogicalSnapshots(
	t *testing.T,
	baseline []lagAwareJobSnapshot,
	candidate []lagAwareJobSnapshot,
	comparison string,
) {
	t.Helper()
	assert.Len(t, candidate, len(baseline), "%s must preserve logical snapshot cardinality", comparison)

	expected := make(map[string]int, len(baseline))
	for _, snapshot := range baseline {
		expected[snapshotIdentityKey(snapshot)]++
	}

	for _, snapshot := range candidate {
		key := snapshotIdentityKey(snapshot)
		count := expected[key]
		assert.Greater(t, count, 0, "%s produced duplicate/unexpected logical snapshot %s", comparison, key)
		if count == 0 {
			continue
		}
		expected[key] = count - 1
	}

	for key, count := range expected {
		assert.Equal(t, 0, count, "%s is missing logical snapshot %s", comparison, key)
	}
}

func snapshotIdentityKey(snapshot lagAwareJobSnapshot) string {
	return fmt.Sprintf("%s|%d|%s", snapshot.Address, snapshot.CursorSequence, snapshot.CursorValue)
}

func maxIntSlice(values []int) int {
	maxValue := 0
	for i, value := range values {
		if i == 0 || value > maxValue {
			maxValue = value
		}
	}
	return maxValue
}

func assertNoImmediateDirectionFlip(t *testing.T, batches []int) {
	t.Helper()
	lastDirection := 0
	for i := 1; i < len(batches); i++ {
		delta := batches[i] - batches[i-1]
		direction := 0
		if delta > 0 {
			direction = 1
		}
		if delta < 0 {
			direction = -1
		}
		if direction == 0 {
			continue
		}
		if lastDirection != 0 {
			assert.NotEqual(t, -lastDirection, direction, "batch control direction must not flip on adjacent ticks under cooldown")
		}
		lastDirection = direction
	}
}

func strPtr(v string) *string {
	return &v
}
