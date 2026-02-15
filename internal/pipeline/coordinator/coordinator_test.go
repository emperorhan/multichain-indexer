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

func TestTick_AutoTunePolicyManifestPermutationsConvergeCanonicalTuplesAcrossMandatoryChains(t *testing.T) {
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
			address: "tb1qmanifestperm00000000000000000000000000",
		},
	}

	manifestV2aCfg := AutoTuneConfig{
		Enabled:                    true,
		MinBatchSize:               60,
		MaxBatchSize:               400,
		StepUp:                     20,
		StepDown:                   10,
		LagHighWatermark:           80,
		LagLowWatermark:            20,
		QueueHighWatermarkPct:      90,
		QueueLowWatermarkPct:       10,
		HysteresisTicks:            1,
		CooldownTicks:              1,
		PolicyVersion:              "policy-v2",
		PolicyManifestDigest:       "manifest-v2a",
		PolicyManifestRefreshEpoch: 1,
		PolicyActivationHoldTicks:  1,
	}
	manifestV2bCfg := manifestV2aCfg
	manifestV2bCfg.PolicyManifestDigest = "manifest-v2b"
	manifestV2bCfg.PolicyManifestRefreshEpoch = 2
	staleManifestCfg := manifestV2aCfg
	reapplyManifestCfg := manifestV2bCfg

	heads := []int64{260, 261, 262, 263, 264, 265, 266, 267, 268}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			baselineHarness := newAutoTuneHarnessWithHeadSeries(tc.chain, tc.network, tc.address, 100, heads, manifestV2aCfg)
			baselineSnapshots, baselineBatches := collectAutoTuneTrace(t, baselineHarness, len(heads))

			refreshHarness := newAutoTuneHarnessWithHeadSeries(tc.chain, tc.network, tc.address, 100, heads, manifestV2aCfg)
			refreshSnapshots := make([]lagAwareJobSnapshot, 0, len(heads))
			refreshBatches := make([]int, 0, len(heads))
			for i := 0; i < len(heads); i++ {
				if i == 2 {
					refreshHarness.coordinator.WithAutoTune(manifestV2bCfg)
				}
				job := refreshHarness.tickAndAdvance(t)
				refreshSnapshots = append(refreshSnapshots, snapshotFromFetchJob(job))
				refreshBatches = append(refreshBatches, job.BatchSize)
			}

			staleReapplyHarness := newAutoTuneHarnessWithHeadSeries(tc.chain, tc.network, tc.address, 100, heads, manifestV2aCfg)
			staleReapplySnapshots := make([]lagAwareJobSnapshot, 0, len(heads))
			staleReapplyBatches := make([]int, 0, len(heads))
			for i := 0; i < len(heads); i++ {
				if i == 2 {
					staleReapplyHarness.coordinator.WithAutoTune(manifestV2bCfg)
				}
				if i == 4 {
					staleReapplyHarness.coordinator.WithAutoTune(staleManifestCfg)
					state := staleReapplyHarness.coordinator.ExportAutoTuneRestartState()
					require.NotNil(t, state)
					assert.Equal(t, manifestV2bCfg.PolicyManifestDigest, state.PolicyManifestDigest, "stale refresh must pin last verified manifest digest")
					assert.Equal(t, manifestV2bCfg.PolicyManifestRefreshEpoch, state.PolicyEpoch, "stale refresh must preserve last verified manifest epoch")
				}
				if i == 6 {
					staleReapplyHarness.coordinator.WithAutoTune(reapplyManifestCfg)
					state := staleReapplyHarness.coordinator.ExportAutoTuneRestartState()
					require.NotNil(t, state)
					assert.Equal(t, manifestV2bCfg.PolicyManifestDigest, state.PolicyManifestDigest, "digest re-apply must remain deterministic")
					assert.Equal(t, manifestV2bCfg.PolicyManifestRefreshEpoch, state.PolicyEpoch, "digest re-apply must not fork lineage epoch")
				}
				job := staleReapplyHarness.tickAndAdvance(t)
				staleReapplySnapshots = append(staleReapplySnapshots, snapshotFromFetchJob(job))
				staleReapplyBatches = append(staleReapplyBatches, job.BatchSize)
			}

			assert.Equal(t, baselineSnapshots, refreshSnapshots, "manifest-v2a baseline and manifest-v2b refresh permutations must converge to one canonical tuple output set")
			assert.Equal(t, baselineSnapshots, staleReapplySnapshots, "manifest-v2a/v2b stale-refresh-reject/re-apply permutations must converge to one canonical tuple output set")
			assertNoDuplicateOrMissingLogicalSnapshots(t, baselineSnapshots, refreshSnapshots, "manifest-v2a baseline vs manifest-v2b refresh")
			assertNoDuplicateOrMissingLogicalSnapshots(t, baselineSnapshots, staleReapplySnapshots, "manifest-v2a baseline vs stale-refresh-reject/re-apply")
			assertCursorMonotonicByAddress(t, baselineSnapshots)
			assertCursorMonotonicByAddress(t, refreshSnapshots)
			assertCursorMonotonicByAddress(t, staleReapplySnapshots)

			assert.NotEqual(t, baselineBatches, refreshBatches, "manifest refresh should alter control decisions deterministically at transition boundary")
			assert.NotEqual(t, baselineBatches, staleReapplyBatches, "refresh permutation with stale-reject/re-apply should alter control decisions at refresh boundary only")
			assert.Equal(t, refreshBatches[1], refreshBatches[2], "manifest refresh boundary must apply deterministic activation hold")
			assert.Equal(t, staleReapplyBatches[1], staleReapplyBatches[2], "manifest-v2a->manifest-v2b boundary must hold deterministically")
			assert.NotEqual(t, staleReapplyBatches[3], staleReapplyBatches[4], "stale refresh reject must not open an additional hold")
			assert.NotEqual(t, staleReapplyBatches[5], staleReapplyBatches[6], "digest re-apply must not open an additional hold")
		})
	}
}

func TestTick_AutoTunePolicyManifestSequenceGapPermutationsConvergeCanonicalTuplesAcrossMandatoryChains(t *testing.T) {
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
			address: "7nYBpkEPkDD6m1JKBGwvftG7bHjJErJPjTH3VbKgapseq",
		},
		{
			name:    "base-sepolia",
			chain:   model.ChainBase,
			network: model.NetworkSepolia,
			address: "0x2222222222222222222222222222222222222222",
		},
		{
			name:    "btc-testnet",
			chain:   model.ChainBTC,
			network: model.NetworkTestnet,
			address: "tb1qmanifestseqgap000000000000000000000000",
		},
	}

	segment1Cfg := AutoTuneConfig{
		Enabled:                    true,
		MinBatchSize:               60,
		MaxBatchSize:               400,
		StepUp:                     20,
		StepDown:                   10,
		LagHighWatermark:           80,
		LagLowWatermark:            20,
		QueueHighWatermarkPct:      90,
		QueueLowWatermarkPct:       10,
		HysteresisTicks:            1,
		CooldownTicks:              1,
		PolicyVersion:              "policy-v2",
		PolicyManifestDigest:       "manifest-v2a",
		PolicyManifestRefreshEpoch: 1,
		PolicyActivationHoldTicks:  1,
	}
	segment2Cfg := segment1Cfg
	segment2Cfg.PolicyManifestDigest = "manifest-v2b"
	segment2Cfg.PolicyManifestRefreshEpoch = 2
	segment3Cfg := segment1Cfg
	segment3Cfg.PolicyManifestDigest = "manifest-v2c"
	segment3Cfg.PolicyManifestRefreshEpoch = 3

	heads := []int64{260, 261, 262, 263, 264, 265, 266, 267, 268, 269}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			baselineHarness := newAutoTuneHarnessWithHeadSeries(tc.chain, tc.network, tc.address, 100, heads, segment1Cfg)
			baselineSnapshots := make([]lagAwareJobSnapshot, 0, len(heads))
			baselineBatches := make([]int, 0, len(heads))
			for i := 0; i < len(heads); i++ {
				if i == 2 {
					baselineHarness.coordinator.WithAutoTune(segment2Cfg)
				}
				if i == 4 {
					baselineHarness.coordinator.WithAutoTune(segment3Cfg)
				}
				job := baselineHarness.tickAndAdvance(t)
				baselineSnapshots = append(baselineSnapshots, snapshotFromFetchJob(job))
				baselineBatches = append(baselineBatches, job.BatchSize)
			}

			sequenceGapHarness := newAutoTuneHarnessWithHeadSeries(tc.chain, tc.network, tc.address, 100, heads, segment1Cfg)
			sequenceGapSnapshots := make([]lagAwareJobSnapshot, 0, len(heads))
			sequenceGapBatches := make([]int, 0, len(heads))
			for i := 0; i < len(heads); i++ {
				if i == 2 {
					sequenceGapHarness.coordinator.WithAutoTune(segment3Cfg)
					state := sequenceGapHarness.coordinator.ExportAutoTuneRestartState()
					require.NotNil(t, state)
					assert.Equal(t, segment1Cfg.PolicyManifestDigest, state.PolicyManifestDigest, "non-contiguous segment apply must pin previously verified manifest digest")
					assert.Equal(t, segment1Cfg.PolicyManifestRefreshEpoch, state.PolicyEpoch, "non-contiguous segment apply must pin last contiguous epoch")
				}
				if i == 4 {
					sequenceGapHarness.coordinator.WithAutoTune(segment2Cfg)
					state := sequenceGapHarness.coordinator.ExportAutoTuneRestartState()
					require.NotNil(t, state)
					assert.Equal(t, segment2Cfg.PolicyManifestDigest, state.PolicyManifestDigest, "late contiguous gap-fill must become active deterministically")
					assert.Equal(t, segment2Cfg.PolicyManifestRefreshEpoch, state.PolicyEpoch)
				}
				if i == 6 {
					sequenceGapHarness.coordinator.WithAutoTune(segment3Cfg)
					state := sequenceGapHarness.coordinator.ExportAutoTuneRestartState()
					require.NotNil(t, state)
					assert.Equal(t, segment3Cfg.PolicyManifestDigest, state.PolicyManifestDigest, "duplicate segment re-apply after gap-fill must converge deterministically")
					assert.Equal(t, segment3Cfg.PolicyManifestRefreshEpoch, state.PolicyEpoch)
				}
				if i == 7 {
					sequenceGapHarness.coordinator.WithAutoTune(segment3Cfg)
				}
				job := sequenceGapHarness.tickAndAdvance(t)
				sequenceGapSnapshots = append(sequenceGapSnapshots, snapshotFromFetchJob(job))
				sequenceGapBatches = append(sequenceGapBatches, job.BatchSize)
			}

			assert.Equal(t, baselineSnapshots, sequenceGapSnapshots, "sequence-complete baseline and sequence-gap permutations must converge to one canonical tuple output set")
			assertNoDuplicateOrMissingLogicalSnapshots(t, baselineSnapshots, sequenceGapSnapshots, "sequence baseline vs sequence-gap hold/late-gap-fill/re-apply")
			assertCursorMonotonicByAddress(t, baselineSnapshots)
			assertCursorMonotonicByAddress(t, sequenceGapSnapshots)

			assert.NotEqual(t, baselineBatches, sequenceGapBatches, "sequence-gap permutations should alter control decisions only at sequence transition boundaries")
			assert.NotEqual(t, sequenceGapBatches[1], sequenceGapBatches[2], "non-contiguous segment transition must not open policy activation hold")
			assert.Equal(t, sequenceGapBatches[3], sequenceGapBatches[4], "late contiguous gap-fill must apply deterministic activation hold")
			assert.Equal(t, sequenceGapBatches[5], sequenceGapBatches[6], "contiguous re-apply after gap-fill must apply deterministic activation hold exactly once")
			assert.NotEqual(t, sequenceGapBatches[6], sequenceGapBatches[7], "duplicate segment re-apply at same epoch must not reopen activation hold")
		})
	}
}

func TestTick_AutoTuneOneChainPolicyManifestSequenceGapDoesNotBleedControlAcrossOtherMandatoryChains(t *testing.T) {
	segment1Cfg := AutoTuneConfig{
		Enabled:                    true,
		MinBatchSize:               60,
		MaxBatchSize:               320,
		StepUp:                     20,
		StepDown:                   10,
		LagHighWatermark:           80,
		LagLowWatermark:            20,
		QueueHighWatermarkPct:      90,
		QueueLowWatermarkPct:       10,
		HysteresisTicks:            1,
		CooldownTicks:              1,
		PolicyVersion:              "policy-v2",
		PolicyManifestDigest:       "manifest-v2a",
		PolicyManifestRefreshEpoch: 1,
		PolicyActivationHoldTicks:  1,
	}
	segment2Cfg := segment1Cfg
	segment2Cfg.PolicyManifestDigest = "manifest-v2b"
	segment2Cfg.PolicyManifestRefreshEpoch = 2
	segment3Cfg := segment1Cfg
	segment3Cfg.PolicyManifestDigest = "manifest-v2c"
	segment3Cfg.PolicyManifestRefreshEpoch = 3

	const tickCount = 10
	healthyBaseAddress := "0x3333333333333333333333333333333333333333"
	healthyBTCAddress := "tb1qmanifestseqhealthy0000000000000000000000"
	laggingSolanaAddress := "7nYBpkEPkDD6m1JKBGwvftG7bHjJErJPjTH3VbKgapseq"

	healthyHeads := []int64{130, 131, 132, 133, 134, 135, 136, 137, 138, 139}
	laggingHeads := []int64{260, 261, 262, 263, 264, 265, 266, 267, 268, 269}

	baseBaseline := newAutoTuneHarnessWithHeadSeries(
		model.ChainBase,
		model.NetworkSepolia,
		healthyBaseAddress,
		120,
		healthyHeads,
		segment1Cfg,
	)
	baseBaselineSnapshots, baseBaselineBatches := collectAutoTuneTrace(t, baseBaseline, tickCount)

	btcBaseline := newAutoTuneHarnessWithHeadSeries(
		model.ChainBTC,
		model.NetworkTestnet,
		healthyBTCAddress,
		120,
		healthyHeads,
		segment1Cfg,
	)
	btcBaselineSnapshots, btcBaselineBatches := collectAutoTuneTrace(t, btcBaseline, tickCount)

	laggingBaseline := newAutoTuneHarnessWithHeadSeries(
		model.ChainSolana,
		model.NetworkDevnet,
		laggingSolanaAddress,
		100,
		laggingHeads,
		segment1Cfg,
	)
	laggingBaselineSnapshots := make([]lagAwareJobSnapshot, 0, tickCount)
	laggingBaselineBatches := make([]int, 0, tickCount)
	for i := 0; i < tickCount; i++ {
		if i == 2 {
			laggingBaseline.coordinator.WithAutoTune(segment2Cfg)
		}
		if i == 4 {
			laggingBaseline.coordinator.WithAutoTune(segment3Cfg)
		}
		job := laggingBaseline.tickAndAdvance(t)
		laggingBaselineSnapshots = append(laggingBaselineSnapshots, snapshotFromFetchJob(job))
		laggingBaselineBatches = append(laggingBaselineBatches, job.BatchSize)
	}

	baseInterleaved := newAutoTuneHarnessWithHeadSeries(
		model.ChainBase,
		model.NetworkSepolia,
		healthyBaseAddress,
		120,
		healthyHeads,
		segment1Cfg,
	)
	btcInterleaved := newAutoTuneHarnessWithHeadSeries(
		model.ChainBTC,
		model.NetworkTestnet,
		healthyBTCAddress,
		120,
		healthyHeads,
		segment1Cfg,
	)
	laggingInterleaved := newAutoTuneHarnessWithHeadSeries(
		model.ChainSolana,
		model.NetworkDevnet,
		laggingSolanaAddress,
		100,
		laggingHeads,
		segment1Cfg,
	)

	baseSnapshots := make([]lagAwareJobSnapshot, 0, tickCount)
	baseBatches := make([]int, 0, tickCount)
	btcSnapshots := make([]lagAwareJobSnapshot, 0, tickCount)
	btcBatches := make([]int, 0, tickCount)
	laggingSnapshots := make([]lagAwareJobSnapshot, 0, tickCount)
	laggingBatches := make([]int, 0, tickCount)

	for i := 0; i < tickCount; i++ {
		if i == 2 {
			laggingInterleaved.coordinator.WithAutoTune(segment3Cfg)
			state := laggingInterleaved.coordinator.ExportAutoTuneRestartState()
			require.NotNil(t, state)
			assert.Equal(t, segment1Cfg.PolicyManifestDigest, state.PolicyManifestDigest, "one-chain sequence-gap apply must pin previous contiguous manifest digest")
			assert.Equal(t, segment1Cfg.PolicyManifestRefreshEpoch, state.PolicyEpoch, "one-chain sequence-gap apply must pin previous contiguous manifest epoch")
		}
		if i == 4 {
			laggingInterleaved.coordinator.WithAutoTune(segment2Cfg)
			state := laggingInterleaved.coordinator.ExportAutoTuneRestartState()
			require.NotNil(t, state)
			assert.Equal(t, segment2Cfg.PolicyManifestDigest, state.PolicyManifestDigest, "one-chain late contiguous gap-fill must become active deterministically")
			assert.Equal(t, segment2Cfg.PolicyManifestRefreshEpoch, state.PolicyEpoch)
		}
		if i == 6 {
			laggingInterleaved.coordinator.WithAutoTune(segment3Cfg)
			state := laggingInterleaved.coordinator.ExportAutoTuneRestartState()
			require.NotNil(t, state)
			assert.Equal(t, segment3Cfg.PolicyManifestDigest, state.PolicyManifestDigest, "one-chain contiguous re-apply after gap-fill must converge deterministically")
			assert.Equal(t, segment3Cfg.PolicyManifestRefreshEpoch, state.PolicyEpoch)
		}
		if i == 7 {
			laggingInterleaved.coordinator.WithAutoTune(segment3Cfg)
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

	assert.Equal(t, baseBaselineSnapshots, baseSnapshots, "one-chain sequence-gap recovery must not alter base canonical tuples")
	assert.Equal(t, baseBaselineBatches, baseBatches, "one-chain sequence-gap recovery must not alter base control decisions")
	assert.Equal(t, btcBaselineSnapshots, btcSnapshots, "one-chain sequence-gap recovery must not alter btc canonical tuples")
	assert.Equal(t, btcBaselineBatches, btcBatches, "one-chain sequence-gap recovery must not alter btc control decisions")
	assert.Equal(t, laggingBaselineSnapshots, laggingSnapshots, "sequence-gap recovery must preserve lagging-chain canonical tuples")
	assert.NotEqual(t, laggingBaselineBatches, laggingBatches, "sequence-gap recovery permutations should alter only lagging-chain control decisions at sequence boundaries")
	assert.NotEqual(t, laggingBatches[1], laggingBatches[2], "non-contiguous segment transition must not open policy activation hold")
	assert.Equal(t, laggingBatches[3], laggingBatches[4], "late contiguous gap-fill must apply deterministic activation hold")
	assert.Equal(t, laggingBatches[5], laggingBatches[6], "contiguous re-apply after gap-fill must apply deterministic activation hold exactly once")
	assert.NotEqual(t, laggingBatches[6], laggingBatches[7], "duplicate segment re-apply at same epoch must not reopen activation hold")
	assertNoDuplicateOrMissingLogicalSnapshots(t, baseBaselineSnapshots, baseSnapshots, "base baseline vs one-chain sequence-gap transition")
	assertNoDuplicateOrMissingLogicalSnapshots(t, btcBaselineSnapshots, btcSnapshots, "btc baseline vs one-chain sequence-gap transition")
	assertNoDuplicateOrMissingLogicalSnapshots(t, laggingBaselineSnapshots, laggingSnapshots, "lagging baseline vs one-chain sequence-gap transition")

	assertCursorMonotonicByAddress(t, baseSnapshots)
	assertCursorMonotonicByAddress(t, btcSnapshots)
	assertCursorMonotonicByAddress(t, laggingSnapshots)
}

func TestTick_AutoTunePolicyManifestSequenceGapReplayResumeConvergesAcrossMandatoryChains(t *testing.T) {
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
			address: "7nYBpkEPkDD6m1JKBGwvftG7bHjJErJPjTH3VbKseqreplay",
		},
		{
			name:    "base-sepolia",
			chain:   model.ChainBase,
			network: model.NetworkSepolia,
			address: "0x3333333333333333333333333333333333333333",
		},
		{
			name:    "btc-testnet",
			chain:   model.ChainBTC,
			network: model.NetworkTestnet,
			address: "tb1qmanifestseqreplay000000000000000000000",
		},
	}

	segment1Cfg := AutoTuneConfig{
		Enabled:                    true,
		MinBatchSize:               60,
		MaxBatchSize:               320,
		StepUp:                     20,
		StepDown:                   10,
		LagHighWatermark:           80,
		LagLowWatermark:            20,
		QueueHighWatermarkPct:      90,
		QueueLowWatermarkPct:       10,
		HysteresisTicks:            1,
		CooldownTicks:              1,
		PolicyVersion:              "policy-v2",
		PolicyManifestDigest:       "manifest-v2a",
		PolicyManifestRefreshEpoch: 1,
		PolicyActivationHoldTicks:  2,
	}
	segment2Cfg := segment1Cfg
	segment2Cfg.PolicyManifestDigest = "manifest-v2b"
	segment2Cfg.PolicyManifestRefreshEpoch = 2
	segment3Cfg := segment1Cfg
	segment3Cfg.PolicyManifestDigest = "manifest-v2c"
	segment3Cfg.PolicyManifestRefreshEpoch = 3

	heads := []int64{260, 261, 262, 263, 264, 265, 266, 267, 268, 269}
	const splitTick = 3

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			coldHarness := newAutoTuneHarnessWithHeadSeries(tc.chain, tc.network, tc.address, 100, heads, segment1Cfg)
			coldSnapshots := make([]lagAwareJobSnapshot, 0, len(heads))
			coldBatches := make([]int, 0, len(heads))
			for i := 0; i < len(heads); i++ {
				if i == 2 {
					coldHarness.coordinator.WithAutoTune(segment3Cfg)
				}
				if i == 4 {
					coldHarness.coordinator.WithAutoTune(segment2Cfg)
				}
				if i == 6 {
					coldHarness.coordinator.WithAutoTune(segment3Cfg)
				}
				if i == 7 {
					coldHarness.coordinator.WithAutoTune(segment3Cfg)
				}
				job := coldHarness.tickAndAdvance(t)
				coldSnapshots = append(coldSnapshots, snapshotFromFetchJob(job))
				coldBatches = append(coldBatches, job.BatchSize)
			}

			warmFirst := newAutoTuneHarnessWithHeadSeries(tc.chain, tc.network, tc.address, 100, heads[:splitTick], segment1Cfg)
			warmSnapshots := make([]lagAwareJobSnapshot, 0, len(heads))
			warmBatches := make([]int, 0, len(heads))
			for i := 0; i < splitTick; i++ {
				if i == 2 {
					warmFirst.coordinator.WithAutoTune(segment3Cfg)
				}
				job := warmFirst.tickAndAdvance(t)
				warmSnapshots = append(warmSnapshots, snapshotFromFetchJob(job))
				warmBatches = append(warmBatches, job.BatchSize)
			}

			restartState := warmFirst.coordinator.ExportAutoTuneRestartState()
			require.NotNil(t, restartState)
			assert.Equal(t, segment1Cfg.PolicyVersion, restartState.PolicyVersion)
			assert.Equal(t, segment1Cfg.PolicyManifestDigest, restartState.PolicyManifestDigest)
			assert.Equal(t, segment1Cfg.PolicyManifestRefreshEpoch, restartState.PolicyEpoch)
			assert.Equal(t, 0, restartState.PolicyActivationRemaining, "restart state must not open activation hold after rejected sequence-gap transition")
			resumeCursor := warmFirst.cursorRepo.GetByAddress(tc.address)
			require.NotNil(t, resumeCursor)

			warmSecond := newAutoTuneHarnessWithWarmStartAndHeadSeries(
				tc.chain,
				tc.network,
				tc.address,
				resumeCursor.CursorSequence,
				heads[splitTick:],
				segment3Cfg,
				restartState,
			)

			for i := splitTick; i < len(heads); i++ {
				if i == 4 {
					warmSecond.coordinator.WithAutoTune(segment2Cfg)
					state := warmSecond.coordinator.ExportAutoTuneRestartState()
					require.NotNil(t, state)
					assert.Equal(t, segment2Cfg.PolicyManifestDigest, state.PolicyManifestDigest, "replay late contiguous gap-fill must be accepted deterministically")
					assert.Equal(t, segment2Cfg.PolicyManifestRefreshEpoch, state.PolicyEpoch)
				}
				if i == 6 {
					warmSecond.coordinator.WithAutoTune(segment3Cfg)
					state := warmSecond.coordinator.ExportAutoTuneRestartState()
					require.NotNil(t, state)
					assert.Equal(t, segment3Cfg.PolicyManifestDigest, state.PolicyManifestDigest, "replay contiguous segment re-apply after gap-fill must converge deterministically")
					assert.Equal(t, segment3Cfg.PolicyManifestRefreshEpoch, state.PolicyEpoch)
				}
				if i == 7 {
					warmSecond.coordinator.WithAutoTune(segment3Cfg)
				}
				job := warmSecond.tickAndAdvance(t)
				warmSnapshots = append(warmSnapshots, snapshotFromFetchJob(job))
				warmBatches = append(warmBatches, job.BatchSize)
			}

			assert.Equal(t, coldSnapshots, warmSnapshots, "policy-manifest sequence-gap replay/resume must converge to deterministic canonical tuples")
			assert.Equal(t, coldBatches, warmBatches, "policy-manifest sequence-gap replay/resume must preserve deterministic control decisions")
			assertNoDuplicateOrMissingLogicalSnapshots(t, coldSnapshots, warmSnapshots, "cold vs warm sequence-gap replay")
			assertCursorMonotonicByAddress(t, warmSnapshots)
		})
	}
}

func TestTick_AutoTunePolicyManifestSnapshotCutoverPermutationsConvergeCanonicalTuplesAcrossMandatoryChains(t *testing.T) {
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
			address: "7nYBpkEPkDD6m1JKBGwvftG7bHjJErJPjTH3VbKsnapcut",
		},
		{
			name:    "base-sepolia",
			chain:   model.ChainBase,
			network: model.NetworkSepolia,
			address: "0x4444444444444444444444444444444444444444",
		},
		{
			name:    "btc-testnet",
			chain:   model.ChainBTC,
			network: model.NetworkTestnet,
			address: "tb1qmanifestsnapshotcut0000000000000000000",
		},
	}

	segment1Cfg := AutoTuneConfig{
		Enabled:                    true,
		MinBatchSize:               60,
		MaxBatchSize:               400,
		StepUp:                     20,
		StepDown:                   10,
		LagHighWatermark:           80,
		LagLowWatermark:            20,
		QueueHighWatermarkPct:      90,
		QueueLowWatermarkPct:       10,
		HysteresisTicks:            1,
		CooldownTicks:              1,
		PolicyVersion:              "policy-v2",
		PolicyManifestDigest:       "manifest-tail-v2a",
		PolicyManifestRefreshEpoch: 1,
		PolicyActivationHoldTicks:  1,
	}
	segment2Cfg := segment1Cfg
	segment2Cfg.PolicyManifestDigest = "manifest-tail-v2b"
	segment2Cfg.PolicyManifestRefreshEpoch = 2
	segment3Cfg := segment1Cfg
	segment3Cfg.PolicyManifestDigest = "manifest-tail-v2c"
	segment3Cfg.PolicyManifestRefreshEpoch = 3

	snapshotCutoverCfg := segment1Cfg
	snapshotCutoverCfg.PolicyManifestDigest = "manifest-snapshot-v2c|snapshot-base-seq=1|snapshot-tail-seq=3"
	snapshotCutoverCfg.PolicyManifestRefreshEpoch = 3
	staleSnapshotCfg := segment1Cfg
	staleSnapshotCfg.PolicyManifestDigest = "manifest-snapshot-stale|snapshot-base-seq=0|snapshot-tail-seq=2"
	staleSnapshotCfg.PolicyManifestRefreshEpoch = 2

	heads := []int64{260, 261, 262, 263, 264, 265, 266, 267, 268, 269}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			baselineHarness := newAutoTuneHarnessWithHeadSeries(tc.chain, tc.network, tc.address, 100, heads, segment1Cfg)
			baselineSnapshots := make([]lagAwareJobSnapshot, 0, len(heads))
			baselineBatches := make([]int, 0, len(heads))
			for i := 0; i < len(heads); i++ {
				if i == 2 {
					baselineHarness.coordinator.WithAutoTune(segment2Cfg)
				}
				if i == 4 {
					baselineHarness.coordinator.WithAutoTune(segment3Cfg)
				}
				job := baselineHarness.tickAndAdvance(t)
				baselineSnapshots = append(baselineSnapshots, snapshotFromFetchJob(job))
				baselineBatches = append(baselineBatches, job.BatchSize)
			}

			snapshotHarness := newAutoTuneHarnessWithHeadSeries(tc.chain, tc.network, tc.address, 100, heads, segment1Cfg)
			snapshotCutoverSnapshots := make([]lagAwareJobSnapshot, 0, len(heads))
			snapshotCutoverBatches := make([]int, 0, len(heads))
			for i := 0; i < len(heads); i++ {
				if i == 2 {
					snapshotHarness.coordinator.WithAutoTune(snapshotCutoverCfg)
					state := snapshotHarness.coordinator.ExportAutoTuneRestartState()
					require.NotNil(t, state)
					assert.Equal(t, snapshotCutoverCfg.PolicyManifestDigest, state.PolicyManifestDigest, "snapshot cutover must become active at deterministic boundary")
					assert.Equal(t, snapshotCutoverCfg.PolicyManifestRefreshEpoch, state.PolicyEpoch)
				}
				if i == 5 {
					snapshotHarness.coordinator.WithAutoTune(staleSnapshotCfg)
					state := snapshotHarness.coordinator.ExportAutoTuneRestartState()
					require.NotNil(t, state)
					assert.Equal(t, snapshotCutoverCfg.PolicyManifestDigest, state.PolicyManifestDigest, "stale snapshot must pin last verified snapshot+tail digest")
					assert.Equal(t, snapshotCutoverCfg.PolicyManifestRefreshEpoch, state.PolicyEpoch, "stale snapshot must preserve last verified snapshot+tail epoch")
				}
				if i == 7 {
					snapshotHarness.coordinator.WithAutoTune(snapshotCutoverCfg)
					state := snapshotHarness.coordinator.ExportAutoTuneRestartState()
					require.NotNil(t, state)
					assert.Equal(t, snapshotCutoverCfg.PolicyManifestDigest, state.PolicyManifestDigest, "snapshot+tail re-apply must be replay-stable")
					assert.Equal(t, snapshotCutoverCfg.PolicyManifestRefreshEpoch, state.PolicyEpoch)
				}
				job := snapshotHarness.tickAndAdvance(t)
				snapshotCutoverSnapshots = append(snapshotCutoverSnapshots, snapshotFromFetchJob(job))
				snapshotCutoverBatches = append(snapshotCutoverBatches, job.BatchSize)
			}

			assert.Equal(t, baselineSnapshots, snapshotCutoverSnapshots, "sequence-tail baseline and snapshot-cutover permutations must converge to one canonical tuple output set")
			assertNoDuplicateOrMissingLogicalSnapshots(t, baselineSnapshots, snapshotCutoverSnapshots, "sequence-tail baseline vs snapshot-cutover/stale-reject/re-apply")
			assertCursorMonotonicByAddress(t, baselineSnapshots)
			assertCursorMonotonicByAddress(t, snapshotCutoverSnapshots)

			assert.NotEqual(t, baselineBatches, snapshotCutoverBatches, "snapshot-cutover permutations should alter control decisions only at transition boundaries")
			assert.Equal(t, snapshotCutoverBatches[1], snapshotCutoverBatches[2], "snapshot-cutover boundary must apply deterministic activation hold")
			assert.NotEqual(t, snapshotCutoverBatches[4], snapshotCutoverBatches[5], "stale snapshot reject must not open an additional hold")
			assert.NotEqual(t, snapshotCutoverBatches[6], snapshotCutoverBatches[7], "snapshot+tail re-apply at same boundary must not reopen activation hold")
		})
	}
}

func TestTick_AutoTuneOneChainPolicyManifestSnapshotCutoverDoesNotBleedControlAcrossOtherMandatoryChains(t *testing.T) {
	segment1Cfg := AutoTuneConfig{
		Enabled:                    true,
		MinBatchSize:               60,
		MaxBatchSize:               320,
		StepUp:                     20,
		StepDown:                   10,
		LagHighWatermark:           80,
		LagLowWatermark:            20,
		QueueHighWatermarkPct:      90,
		QueueLowWatermarkPct:       10,
		HysteresisTicks:            1,
		CooldownTicks:              1,
		PolicyVersion:              "policy-v2",
		PolicyManifestDigest:       "manifest-tail-v2a",
		PolicyManifestRefreshEpoch: 1,
		PolicyActivationHoldTicks:  1,
	}
	segment2Cfg := segment1Cfg
	segment2Cfg.PolicyManifestDigest = "manifest-tail-v2b"
	segment2Cfg.PolicyManifestRefreshEpoch = 2
	segment3Cfg := segment1Cfg
	segment3Cfg.PolicyManifestDigest = "manifest-tail-v2c"
	segment3Cfg.PolicyManifestRefreshEpoch = 3
	snapshotCutoverCfg := segment1Cfg
	snapshotCutoverCfg.PolicyManifestDigest = "manifest-snapshot-v2c|snapshot-base-seq=1|snapshot-tail-seq=3"
	snapshotCutoverCfg.PolicyManifestRefreshEpoch = 3
	staleSnapshotCfg := segment1Cfg
	staleSnapshotCfg.PolicyManifestDigest = "manifest-snapshot-stale|snapshot-base-seq=0|snapshot-tail-seq=2"
	staleSnapshotCfg.PolicyManifestRefreshEpoch = 2

	const tickCount = 10
	healthyBaseAddress := "0x5555555555555555555555555555555555555555"
	healthyBTCAddress := "tb1qmanifestsnapshothealthy00000000000000000"
	laggingSolanaAddress := "7nYBpkEPkDD6m1JKBGwvftG7bHjJErJPjTH3VbKsnapcut"

	healthyHeads := []int64{130, 131, 132, 133, 134, 135, 136, 137, 138, 139}
	laggingHeads := []int64{260, 261, 262, 263, 264, 265, 266, 267, 268, 269}

	baseBaseline := newAutoTuneHarnessWithHeadSeries(
		model.ChainBase,
		model.NetworkSepolia,
		healthyBaseAddress,
		120,
		healthyHeads,
		segment1Cfg,
	)
	baseBaselineSnapshots, baseBaselineBatches := collectAutoTuneTrace(t, baseBaseline, tickCount)

	btcBaseline := newAutoTuneHarnessWithHeadSeries(
		model.ChainBTC,
		model.NetworkTestnet,
		healthyBTCAddress,
		120,
		healthyHeads,
		segment1Cfg,
	)
	btcBaselineSnapshots, btcBaselineBatches := collectAutoTuneTrace(t, btcBaseline, tickCount)

	laggingBaseline := newAutoTuneHarnessWithHeadSeries(
		model.ChainSolana,
		model.NetworkDevnet,
		laggingSolanaAddress,
		100,
		laggingHeads,
		segment1Cfg,
	)
	laggingBaselineSnapshots := make([]lagAwareJobSnapshot, 0, tickCount)
	laggingBaselineBatches := make([]int, 0, tickCount)
	for i := 0; i < tickCount; i++ {
		if i == 2 {
			laggingBaseline.coordinator.WithAutoTune(segment2Cfg)
		}
		if i == 4 {
			laggingBaseline.coordinator.WithAutoTune(segment3Cfg)
		}
		job := laggingBaseline.tickAndAdvance(t)
		laggingBaselineSnapshots = append(laggingBaselineSnapshots, snapshotFromFetchJob(job))
		laggingBaselineBatches = append(laggingBaselineBatches, job.BatchSize)
	}

	baseInterleaved := newAutoTuneHarnessWithHeadSeries(
		model.ChainBase,
		model.NetworkSepolia,
		healthyBaseAddress,
		120,
		healthyHeads,
		segment1Cfg,
	)
	btcInterleaved := newAutoTuneHarnessWithHeadSeries(
		model.ChainBTC,
		model.NetworkTestnet,
		healthyBTCAddress,
		120,
		healthyHeads,
		segment1Cfg,
	)
	laggingInterleaved := newAutoTuneHarnessWithHeadSeries(
		model.ChainSolana,
		model.NetworkDevnet,
		laggingSolanaAddress,
		100,
		laggingHeads,
		segment1Cfg,
	)

	baseSnapshots := make([]lagAwareJobSnapshot, 0, tickCount)
	baseBatches := make([]int, 0, tickCount)
	btcSnapshots := make([]lagAwareJobSnapshot, 0, tickCount)
	btcBatches := make([]int, 0, tickCount)
	laggingSnapshots := make([]lagAwareJobSnapshot, 0, tickCount)
	laggingBatches := make([]int, 0, tickCount)

	for i := 0; i < tickCount; i++ {
		if i == 2 {
			laggingInterleaved.coordinator.WithAutoTune(snapshotCutoverCfg)
			state := laggingInterleaved.coordinator.ExportAutoTuneRestartState()
			require.NotNil(t, state)
			assert.Equal(t, snapshotCutoverCfg.PolicyManifestDigest, state.PolicyManifestDigest, "one-chain snapshot cutover must become active deterministically")
			assert.Equal(t, snapshotCutoverCfg.PolicyManifestRefreshEpoch, state.PolicyEpoch)
		}
		if i == 5 {
			laggingInterleaved.coordinator.WithAutoTune(staleSnapshotCfg)
			state := laggingInterleaved.coordinator.ExportAutoTuneRestartState()
			require.NotNil(t, state)
			assert.Equal(t, snapshotCutoverCfg.PolicyManifestDigest, state.PolicyManifestDigest, "one-chain stale snapshot must pin verified snapshot+tail digest")
			assert.Equal(t, snapshotCutoverCfg.PolicyManifestRefreshEpoch, state.PolicyEpoch, "one-chain stale snapshot must preserve verified snapshot+tail epoch")
		}
		if i == 7 {
			laggingInterleaved.coordinator.WithAutoTune(snapshotCutoverCfg)
			state := laggingInterleaved.coordinator.ExportAutoTuneRestartState()
			require.NotNil(t, state)
			assert.Equal(t, snapshotCutoverCfg.PolicyManifestDigest, state.PolicyManifestDigest, "one-chain snapshot+tail re-apply must remain deterministic")
			assert.Equal(t, snapshotCutoverCfg.PolicyManifestRefreshEpoch, state.PolicyEpoch)
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

	assert.Equal(t, baseBaselineSnapshots, baseSnapshots, "solana snapshot-cutover transition must not alter base canonical tuples")
	assert.Equal(t, baseBaselineBatches, baseBatches, "solana snapshot-cutover transition must not alter base control decisions")
	assert.Equal(t, btcBaselineSnapshots, btcSnapshots, "solana snapshot-cutover transition must not alter btc canonical tuples")
	assert.Equal(t, btcBaselineBatches, btcBatches, "solana snapshot-cutover transition must not alter btc control decisions")
	assert.Equal(t, laggingBaselineSnapshots, laggingSnapshots, "snapshot-cutover transition must preserve lagging-chain canonical tuples")
	assert.NotEqual(t, laggingBaselineBatches, laggingBatches, "snapshot-cutover permutations should alter only lagging-chain control decisions at transition boundaries")
	assert.Equal(t, laggingBatches[1], laggingBatches[2], "one-chain snapshot-cutover boundary must apply deterministic activation hold")
	assert.NotEqual(t, laggingBatches[4], laggingBatches[5], "one-chain stale snapshot reject must not open additional hold")
	assert.NotEqual(t, laggingBatches[6], laggingBatches[7], "one-chain snapshot+tail re-apply must not reopen activation hold")
	assertNoDuplicateOrMissingLogicalSnapshots(t, baseBaselineSnapshots, baseSnapshots, "base baseline vs one-chain snapshot-cutover transition")
	assertNoDuplicateOrMissingLogicalSnapshots(t, btcBaselineSnapshots, btcSnapshots, "btc baseline vs one-chain snapshot-cutover transition")
	assertNoDuplicateOrMissingLogicalSnapshots(t, laggingBaselineSnapshots, laggingSnapshots, "lagging baseline vs one-chain snapshot-cutover transition")

	assertCursorMonotonicByAddress(t, baseSnapshots)
	assertCursorMonotonicByAddress(t, btcSnapshots)
	assertCursorMonotonicByAddress(t, laggingSnapshots)
}

func TestTick_AutoTunePolicyManifestSnapshotCutoverReplayResumeConvergesAcrossMandatoryChains(t *testing.T) {
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
			address: "7nYBpkEPkDD6m1JKBGwvftG7bHjJErJPjTH3VbKsnapreplay",
		},
		{
			name:    "base-sepolia",
			chain:   model.ChainBase,
			network: model.NetworkSepolia,
			address: "0x6666666666666666666666666666666666666666",
		},
		{
			name:    "btc-testnet",
			chain:   model.ChainBTC,
			network: model.NetworkTestnet,
			address: "tb1qmanifestsnapshotreplay00000000000000000",
		},
	}

	segment1Cfg := AutoTuneConfig{
		Enabled:                    true,
		MinBatchSize:               60,
		MaxBatchSize:               320,
		StepUp:                     20,
		StepDown:                   10,
		LagHighWatermark:           80,
		LagLowWatermark:            20,
		QueueHighWatermarkPct:      90,
		QueueLowWatermarkPct:       10,
		HysteresisTicks:            1,
		CooldownTicks:              1,
		PolicyVersion:              "policy-v2",
		PolicyManifestDigest:       "manifest-tail-v2a",
		PolicyManifestRefreshEpoch: 1,
		PolicyActivationHoldTicks:  2,
	}
	snapshotCutoverCfg := segment1Cfg
	snapshotCutoverCfg.PolicyManifestDigest = "manifest-snapshot-v2c|snapshot-base-seq=1|snapshot-tail-seq=3"
	snapshotCutoverCfg.PolicyManifestRefreshEpoch = 3
	staleSnapshotCfg := segment1Cfg
	staleSnapshotCfg.PolicyManifestDigest = "manifest-snapshot-stale|snapshot-base-seq=0|snapshot-tail-seq=2"
	staleSnapshotCfg.PolicyManifestRefreshEpoch = 2

	heads := []int64{260, 261, 262, 263, 264, 265, 266, 267, 268, 269}
	const splitTick = 3

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			coldHarness := newAutoTuneHarnessWithHeadSeries(tc.chain, tc.network, tc.address, 100, heads, segment1Cfg)
			coldSnapshots := make([]lagAwareJobSnapshot, 0, len(heads))
			coldBatches := make([]int, 0, len(heads))
			for i := 0; i < len(heads); i++ {
				if i == 2 {
					coldHarness.coordinator.WithAutoTune(snapshotCutoverCfg)
				}
				if i == 5 {
					coldHarness.coordinator.WithAutoTune(staleSnapshotCfg)
				}
				if i == 7 {
					coldHarness.coordinator.WithAutoTune(snapshotCutoverCfg)
				}
				job := coldHarness.tickAndAdvance(t)
				coldSnapshots = append(coldSnapshots, snapshotFromFetchJob(job))
				coldBatches = append(coldBatches, job.BatchSize)
			}

			warmFirst := newAutoTuneHarnessWithHeadSeries(tc.chain, tc.network, tc.address, 100, heads[:splitTick], segment1Cfg)
			warmSnapshots := make([]lagAwareJobSnapshot, 0, len(heads))
			warmBatches := make([]int, 0, len(heads))
			for i := 0; i < splitTick; i++ {
				if i == 2 {
					warmFirst.coordinator.WithAutoTune(snapshotCutoverCfg)
				}
				job := warmFirst.tickAndAdvance(t)
				warmSnapshots = append(warmSnapshots, snapshotFromFetchJob(job))
				warmBatches = append(warmBatches, job.BatchSize)
			}

			restartState := warmFirst.coordinator.ExportAutoTuneRestartState()
			require.NotNil(t, restartState)
			assert.Equal(t, snapshotCutoverCfg.PolicyVersion, restartState.PolicyVersion)
			assert.Equal(t, snapshotCutoverCfg.PolicyManifestDigest, restartState.PolicyManifestDigest)
			assert.Equal(t, snapshotCutoverCfg.PolicyManifestRefreshEpoch, restartState.PolicyEpoch)
			assert.Greater(t, restartState.PolicyActivationRemaining, 0, "restart state must preserve snapshot-cutover activation hold countdown at boundary")
			resumeCursor := warmFirst.cursorRepo.GetByAddress(tc.address)
			require.NotNil(t, resumeCursor)

			warmSecond := newAutoTuneHarnessWithWarmStartAndHeadSeries(
				tc.chain,
				tc.network,
				tc.address,
				resumeCursor.CursorSequence,
				heads[splitTick:],
				snapshotCutoverCfg,
				restartState,
			)

			for i := splitTick; i < len(heads); i++ {
				if i == 5 {
					warmSecond.coordinator.WithAutoTune(staleSnapshotCfg)
					state := warmSecond.coordinator.ExportAutoTuneRestartState()
					require.NotNil(t, state)
					assert.Equal(t, snapshotCutoverCfg.PolicyManifestDigest, state.PolicyManifestDigest, "replay stale snapshot must be rejected deterministically")
					assert.Equal(t, snapshotCutoverCfg.PolicyManifestRefreshEpoch, state.PolicyEpoch)
				}
				if i == 7 {
					warmSecond.coordinator.WithAutoTune(snapshotCutoverCfg)
					state := warmSecond.coordinator.ExportAutoTuneRestartState()
					require.NotNil(t, state)
					assert.Equal(t, snapshotCutoverCfg.PolicyManifestDigest, state.PolicyManifestDigest, "replay snapshot+tail re-apply must remain deterministic")
					assert.Equal(t, snapshotCutoverCfg.PolicyManifestRefreshEpoch, state.PolicyEpoch)
				}
				job := warmSecond.tickAndAdvance(t)
				warmSnapshots = append(warmSnapshots, snapshotFromFetchJob(job))
				warmBatches = append(warmBatches, job.BatchSize)
			}

			assert.Equal(t, coldSnapshots, warmSnapshots, "policy-manifest snapshot-cutover replay/resume must converge to deterministic canonical tuples")
			assert.Equal(t, coldBatches, warmBatches, "policy-manifest snapshot-cutover replay/resume must preserve deterministic control decisions")
			assertNoDuplicateOrMissingLogicalSnapshots(t, coldSnapshots, warmSnapshots, "cold vs warm snapshot-cutover replay")
			assertCursorMonotonicByAddress(t, warmSnapshots)
		})
	}
}

func TestTick_AutoTunePolicyManifestRollbackLineagePermutationsConvergeCanonicalTuplesAcrossMandatoryChains(t *testing.T) {
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
			address: "7nYBpkEPkDD6m1JKBGwvftG7bHjJErJPjTH3VbKrlbk01",
		},
		{
			name:    "base-sepolia",
			chain:   model.ChainBase,
			network: model.NetworkSepolia,
			address: "0x7777777777777777777777777777777777777777",
		},
		{
			name:    "btc-testnet",
			chain:   model.ChainBTC,
			network: model.NetworkTestnet,
			address: "tb1qmanifestrollbacklineage000000000000000",
		},
	}

	segment1Cfg := AutoTuneConfig{
		Enabled:                    true,
		MinBatchSize:               60,
		MaxBatchSize:               400,
		StepUp:                     20,
		StepDown:                   10,
		LagHighWatermark:           80,
		LagLowWatermark:            20,
		QueueHighWatermarkPct:      90,
		QueueLowWatermarkPct:       10,
		HysteresisTicks:            1,
		CooldownTicks:              1,
		PolicyVersion:              "policy-v2",
		PolicyManifestDigest:       "manifest-tail-v2a",
		PolicyManifestRefreshEpoch: 1,
		PolicyActivationHoldTicks:  1,
	}
	segment2Cfg := segment1Cfg
	segment2Cfg.PolicyManifestDigest = "manifest-tail-v2b"
	segment2Cfg.PolicyManifestRefreshEpoch = 2
	segment3Cfg := segment1Cfg
	segment3Cfg.PolicyManifestDigest = "manifest-tail-v2c"
	segment3Cfg.PolicyManifestRefreshEpoch = 3

	rollbackCfg := segment2Cfg
	rollbackCfg.PolicyManifestDigest = "manifest-tail-v2b|rollback-from-seq=3|rollback-to-seq=2|rollback-forward-seq=3"
	staleRollbackCfg := segment1Cfg
	staleRollbackCfg.PolicyManifestDigest = "manifest-tail-v2a|rollback-from-seq=3|rollback-to-seq=1|rollback-forward-seq=3"
	staleRollbackCfg.PolicyManifestRefreshEpoch = 1

	heads := []int64{260, 261, 262, 263, 264, 265, 266, 267, 268, 269, 270, 271, 272}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			baselineHarness := newAutoTuneHarnessWithHeadSeries(tc.chain, tc.network, tc.address, 100, heads, segment1Cfg)
			baselineSnapshots := make([]lagAwareJobSnapshot, 0, len(heads))
			baselineBatches := make([]int, 0, len(heads))
			for i := 0; i < len(heads); i++ {
				if i == 2 {
					baselineHarness.coordinator.WithAutoTune(segment2Cfg)
				}
				if i == 4 {
					baselineHarness.coordinator.WithAutoTune(segment3Cfg)
				}
				job := baselineHarness.tickAndAdvance(t)
				baselineSnapshots = append(baselineSnapshots, snapshotFromFetchJob(job))
				baselineBatches = append(baselineBatches, job.BatchSize)
			}

			rollbackHarness := newAutoTuneHarnessWithHeadSeries(tc.chain, tc.network, tc.address, 100, heads, segment1Cfg)
			rollbackSnapshots := make([]lagAwareJobSnapshot, 0, len(heads))
			rollbackBatches := make([]int, 0, len(heads))
			for i := 0; i < len(heads); i++ {
				if i == 2 {
					rollbackHarness.coordinator.WithAutoTune(segment2Cfg)
				}
				if i == 4 {
					rollbackHarness.coordinator.WithAutoTune(segment3Cfg)
				}
				if i == 6 {
					rollbackHarness.coordinator.WithAutoTune(rollbackCfg)
					state := rollbackHarness.coordinator.ExportAutoTuneRestartState()
					require.NotNil(t, state)
					assert.Equal(t, rollbackCfg.PolicyManifestDigest, state.PolicyManifestDigest, "rollback apply must activate deterministic rollback lineage boundary")
					assert.Equal(t, rollbackCfg.PolicyManifestRefreshEpoch, state.PolicyEpoch)
				}
				if i == 8 {
					rollbackHarness.coordinator.WithAutoTune(staleRollbackCfg)
					state := rollbackHarness.coordinator.ExportAutoTuneRestartState()
					require.NotNil(t, state)
					assert.Equal(t, rollbackCfg.PolicyManifestDigest, state.PolicyManifestDigest, "stale rollback must pin last verified rollback-safe digest")
					assert.Equal(t, rollbackCfg.PolicyManifestRefreshEpoch, state.PolicyEpoch, "stale rollback must preserve last verified rollback-safe epoch")
				}
				if i == 10 {
					rollbackHarness.coordinator.WithAutoTune(segment3Cfg)
					state := rollbackHarness.coordinator.ExportAutoTuneRestartState()
					require.NotNil(t, state)
					assert.Equal(t, segment3Cfg.PolicyManifestDigest, state.PolicyManifestDigest, "rollback+re-forward apply must deterministically restore forward lineage")
					assert.Equal(t, segment3Cfg.PolicyManifestRefreshEpoch, state.PolicyEpoch)
				}
				if i == 12 {
					rollbackHarness.coordinator.WithAutoTune(segment3Cfg)
					state := rollbackHarness.coordinator.ExportAutoTuneRestartState()
					require.NotNil(t, state)
					assert.Equal(t, segment3Cfg.PolicyManifestDigest, state.PolicyManifestDigest, "rollback+re-forward re-apply must remain replay-stable")
					assert.Equal(t, segment3Cfg.PolicyManifestRefreshEpoch, state.PolicyEpoch)
				}
				job := rollbackHarness.tickAndAdvance(t)
				rollbackSnapshots = append(rollbackSnapshots, snapshotFromFetchJob(job))
				rollbackBatches = append(rollbackBatches, job.BatchSize)
			}

			assert.Equal(t, baselineSnapshots, rollbackSnapshots, "forward-lineage baseline and rollback-lineage permutations must converge to one canonical tuple output set")
			assertNoDuplicateOrMissingLogicalSnapshots(t, baselineSnapshots, rollbackSnapshots, "forward baseline vs rollback-lineage apply/stale-reject/re-forward")
			assertCursorMonotonicByAddress(t, baselineSnapshots)
			assertCursorMonotonicByAddress(t, rollbackSnapshots)

			assert.NotEqual(t, baselineBatches, rollbackBatches, "rollback-lineage permutations should alter control decisions only at rollback boundaries")
			assert.Equal(t, rollbackBatches[1], rollbackBatches[2], "forward segment-2 boundary must apply deterministic activation hold")
			assert.Equal(t, rollbackBatches[3], rollbackBatches[4], "forward segment-3 boundary must apply deterministic activation hold")
			assert.Equal(t, rollbackBatches[5], rollbackBatches[6], "rollback boundary must apply deterministic activation hold")
			assert.NotEqual(t, rollbackBatches[7], rollbackBatches[8], "stale rollback reject must not open an additional hold")
			assert.Equal(t, rollbackBatches[9], rollbackBatches[10], "rollback+re-forward boundary must apply deterministic activation hold")
			assert.NotEqual(t, rollbackBatches[11], rollbackBatches[12], "rollback+re-forward re-apply must not reopen activation hold")
		})
	}
}

func TestTick_AutoTuneOneChainPolicyManifestRollbackLineageDoesNotBleedControlAcrossOtherMandatoryChains(t *testing.T) {
	segment1Cfg := AutoTuneConfig{
		Enabled:                    true,
		MinBatchSize:               60,
		MaxBatchSize:               320,
		StepUp:                     20,
		StepDown:                   10,
		LagHighWatermark:           80,
		LagLowWatermark:            20,
		QueueHighWatermarkPct:      90,
		QueueLowWatermarkPct:       10,
		HysteresisTicks:            1,
		CooldownTicks:              1,
		PolicyVersion:              "policy-v2",
		PolicyManifestDigest:       "manifest-tail-v2a",
		PolicyManifestRefreshEpoch: 1,
		PolicyActivationHoldTicks:  1,
	}
	segment2Cfg := segment1Cfg
	segment2Cfg.PolicyManifestDigest = "manifest-tail-v2b"
	segment2Cfg.PolicyManifestRefreshEpoch = 2
	segment3Cfg := segment1Cfg
	segment3Cfg.PolicyManifestDigest = "manifest-tail-v2c"
	segment3Cfg.PolicyManifestRefreshEpoch = 3
	rollbackCfg := segment2Cfg
	rollbackCfg.PolicyManifestDigest = "manifest-tail-v2b|rollback-from-seq=3|rollback-to-seq=2|rollback-forward-seq=3"
	staleRollbackCfg := segment1Cfg
	staleRollbackCfg.PolicyManifestDigest = "manifest-tail-v2a|rollback-from-seq=3|rollback-to-seq=1|rollback-forward-seq=3"
	staleRollbackCfg.PolicyManifestRefreshEpoch = 1

	const tickCount = 13
	healthyBaseAddress := "0x8888888888888888888888888888888888888888"
	healthyBTCAddress := "tb1qmanifestrollbackhealthy0000000000000000"
	laggingSolanaAddress := "7nYBpkEPkDD6m1JKBGwvftG7bHjJErJPjTH3VbKrlbk01"

	healthyHeads := []int64{130, 131, 132, 133, 134, 135, 136, 137, 138, 139, 140, 141, 142}
	laggingHeads := []int64{260, 261, 262, 263, 264, 265, 266, 267, 268, 269, 270, 271, 272}

	baseBaseline := newAutoTuneHarnessWithHeadSeries(
		model.ChainBase,
		model.NetworkSepolia,
		healthyBaseAddress,
		120,
		healthyHeads,
		segment1Cfg,
	)
	baseBaselineSnapshots, baseBaselineBatches := collectAutoTuneTrace(t, baseBaseline, tickCount)

	btcBaseline := newAutoTuneHarnessWithHeadSeries(
		model.ChainBTC,
		model.NetworkTestnet,
		healthyBTCAddress,
		120,
		healthyHeads,
		segment1Cfg,
	)
	btcBaselineSnapshots, btcBaselineBatches := collectAutoTuneTrace(t, btcBaseline, tickCount)

	laggingBaseline := newAutoTuneHarnessWithHeadSeries(
		model.ChainSolana,
		model.NetworkDevnet,
		laggingSolanaAddress,
		100,
		laggingHeads,
		segment1Cfg,
	)
	laggingBaselineSnapshots := make([]lagAwareJobSnapshot, 0, tickCount)
	laggingBaselineBatches := make([]int, 0, tickCount)
	for i := 0; i < tickCount; i++ {
		if i == 2 {
			laggingBaseline.coordinator.WithAutoTune(segment2Cfg)
		}
		if i == 4 {
			laggingBaseline.coordinator.WithAutoTune(segment3Cfg)
		}
		job := laggingBaseline.tickAndAdvance(t)
		laggingBaselineSnapshots = append(laggingBaselineSnapshots, snapshotFromFetchJob(job))
		laggingBaselineBatches = append(laggingBaselineBatches, job.BatchSize)
	}

	baseInterleaved := newAutoTuneHarnessWithHeadSeries(
		model.ChainBase,
		model.NetworkSepolia,
		healthyBaseAddress,
		120,
		healthyHeads,
		segment1Cfg,
	)
	btcInterleaved := newAutoTuneHarnessWithHeadSeries(
		model.ChainBTC,
		model.NetworkTestnet,
		healthyBTCAddress,
		120,
		healthyHeads,
		segment1Cfg,
	)
	laggingInterleaved := newAutoTuneHarnessWithHeadSeries(
		model.ChainSolana,
		model.NetworkDevnet,
		laggingSolanaAddress,
		100,
		laggingHeads,
		segment1Cfg,
	)

	baseSnapshots := make([]lagAwareJobSnapshot, 0, tickCount)
	baseBatches := make([]int, 0, tickCount)
	btcSnapshots := make([]lagAwareJobSnapshot, 0, tickCount)
	btcBatches := make([]int, 0, tickCount)
	laggingSnapshots := make([]lagAwareJobSnapshot, 0, tickCount)
	laggingBatches := make([]int, 0, tickCount)

	for i := 0; i < tickCount; i++ {
		if i == 2 {
			laggingInterleaved.coordinator.WithAutoTune(segment2Cfg)
		}
		if i == 4 {
			laggingInterleaved.coordinator.WithAutoTune(segment3Cfg)
		}
		if i == 6 {
			laggingInterleaved.coordinator.WithAutoTune(rollbackCfg)
			state := laggingInterleaved.coordinator.ExportAutoTuneRestartState()
			require.NotNil(t, state)
			assert.Equal(t, rollbackCfg.PolicyManifestDigest, state.PolicyManifestDigest, "one-chain rollback apply must activate deterministic rollback lineage boundary")
			assert.Equal(t, rollbackCfg.PolicyManifestRefreshEpoch, state.PolicyEpoch)
		}
		if i == 8 {
			laggingInterleaved.coordinator.WithAutoTune(staleRollbackCfg)
			state := laggingInterleaved.coordinator.ExportAutoTuneRestartState()
			require.NotNil(t, state)
			assert.Equal(t, rollbackCfg.PolicyManifestDigest, state.PolicyManifestDigest, "one-chain stale rollback must pin verified rollback-safe digest")
			assert.Equal(t, rollbackCfg.PolicyManifestRefreshEpoch, state.PolicyEpoch, "one-chain stale rollback must preserve verified rollback-safe epoch")
		}
		if i == 10 {
			laggingInterleaved.coordinator.WithAutoTune(segment3Cfg)
			state := laggingInterleaved.coordinator.ExportAutoTuneRestartState()
			require.NotNil(t, state)
			assert.Equal(t, segment3Cfg.PolicyManifestDigest, state.PolicyManifestDigest, "one-chain rollback+re-forward apply must restore forward lineage deterministically")
			assert.Equal(t, segment3Cfg.PolicyManifestRefreshEpoch, state.PolicyEpoch)
		}
		if i == 12 {
			laggingInterleaved.coordinator.WithAutoTune(segment3Cfg)
			state := laggingInterleaved.coordinator.ExportAutoTuneRestartState()
			require.NotNil(t, state)
			assert.Equal(t, segment3Cfg.PolicyManifestDigest, state.PolicyManifestDigest, "one-chain rollback+re-forward re-apply must remain deterministic")
			assert.Equal(t, segment3Cfg.PolicyManifestRefreshEpoch, state.PolicyEpoch)
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

	assert.Equal(t, baseBaselineSnapshots, baseSnapshots, "solana rollback-lineage transition must not alter base canonical tuples")
	assert.Equal(t, baseBaselineBatches, baseBatches, "solana rollback-lineage transition must not alter base control decisions")
	assert.Equal(t, btcBaselineSnapshots, btcSnapshots, "solana rollback-lineage transition must not alter btc canonical tuples")
	assert.Equal(t, btcBaselineBatches, btcBatches, "solana rollback-lineage transition must not alter btc control decisions")
	assert.Equal(t, laggingBaselineSnapshots, laggingSnapshots, "rollback-lineage transition must preserve lagging-chain canonical tuples")
	assert.NotEqual(t, laggingBaselineBatches, laggingBatches, "rollback-lineage permutations should remain chain-scoped and alter only lagging-chain control decisions")
	assert.Equal(t, laggingBatches[1], laggingBatches[2], "one-chain forward segment-2 boundary must apply deterministic activation hold")
	assert.Equal(t, laggingBatches[3], laggingBatches[4], "one-chain forward segment-3 boundary must apply deterministic activation hold")
	assert.Equal(t, laggingBatches[5], laggingBatches[6], "one-chain rollback boundary must apply deterministic activation hold")
	assert.NotEqual(t, laggingBatches[7], laggingBatches[8], "one-chain stale rollback reject must not open additional hold")
	assert.Equal(t, laggingBatches[9], laggingBatches[10], "one-chain rollback+re-forward boundary must apply deterministic activation hold")
	assert.NotEqual(t, laggingBatches[11], laggingBatches[12], "one-chain rollback+re-forward re-apply must not reopen activation hold")
	assertNoDuplicateOrMissingLogicalSnapshots(t, baseBaselineSnapshots, baseSnapshots, "base baseline vs one-chain rollback-lineage transition")
	assertNoDuplicateOrMissingLogicalSnapshots(t, btcBaselineSnapshots, btcSnapshots, "btc baseline vs one-chain rollback-lineage transition")
	assertNoDuplicateOrMissingLogicalSnapshots(t, laggingBaselineSnapshots, laggingSnapshots, "lagging baseline vs one-chain rollback-lineage transition")

	assertCursorMonotonicByAddress(t, baseSnapshots)
	assertCursorMonotonicByAddress(t, btcSnapshots)
	assertCursorMonotonicByAddress(t, laggingSnapshots)
}

func TestTick_AutoTunePolicyManifestRollbackLineageReplayResumeConvergesAcrossMandatoryChains(t *testing.T) {
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
			address: "7nYBpkEPkDD6m1JKBGwvftG7bHjJErJPjTH3VbKrlbkrpl",
		},
		{
			name:    "base-sepolia",
			chain:   model.ChainBase,
			network: model.NetworkSepolia,
			address: "0x9999999999999999999999999999999999999999",
		},
		{
			name:    "btc-testnet",
			chain:   model.ChainBTC,
			network: model.NetworkTestnet,
			address: "tb1qmanifestrollbackreplay0000000000000000",
		},
	}

	segment1Cfg := AutoTuneConfig{
		Enabled:                    true,
		MinBatchSize:               60,
		MaxBatchSize:               320,
		StepUp:                     20,
		StepDown:                   10,
		LagHighWatermark:           80,
		LagLowWatermark:            20,
		QueueHighWatermarkPct:      90,
		QueueLowWatermarkPct:       10,
		HysteresisTicks:            1,
		CooldownTicks:              1,
		PolicyVersion:              "policy-v2",
		PolicyManifestDigest:       "manifest-tail-v2a",
		PolicyManifestRefreshEpoch: 1,
		PolicyActivationHoldTicks:  2,
	}
	segment2Cfg := segment1Cfg
	segment2Cfg.PolicyManifestDigest = "manifest-tail-v2b"
	segment2Cfg.PolicyManifestRefreshEpoch = 2
	segment3Cfg := segment1Cfg
	segment3Cfg.PolicyManifestDigest = "manifest-tail-v2c"
	segment3Cfg.PolicyManifestRefreshEpoch = 3
	rollbackCfg := segment2Cfg
	rollbackCfg.PolicyManifestDigest = "manifest-tail-v2b|rollback-from-seq=3|rollback-to-seq=2|rollback-forward-seq=3"
	staleRollbackCfg := segment1Cfg
	staleRollbackCfg.PolicyManifestDigest = "manifest-tail-v2a|rollback-from-seq=3|rollback-to-seq=1|rollback-forward-seq=3"
	staleRollbackCfg.PolicyManifestRefreshEpoch = 1

	heads := []int64{260, 261, 262, 263, 264, 265, 266, 267, 268, 269, 270, 271, 272}
	const splitTick = 7

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			coldHarness := newAutoTuneHarnessWithHeadSeries(tc.chain, tc.network, tc.address, 100, heads, segment1Cfg)
			coldSnapshots := make([]lagAwareJobSnapshot, 0, len(heads))
			coldBatches := make([]int, 0, len(heads))
			for i := 0; i < len(heads); i++ {
				if i == 2 {
					coldHarness.coordinator.WithAutoTune(segment2Cfg)
				}
				if i == 4 {
					coldHarness.coordinator.WithAutoTune(segment3Cfg)
				}
				if i == 6 {
					coldHarness.coordinator.WithAutoTune(rollbackCfg)
				}
				if i == 8 {
					coldHarness.coordinator.WithAutoTune(staleRollbackCfg)
				}
				if i == 10 {
					coldHarness.coordinator.WithAutoTune(segment3Cfg)
				}
				if i == 12 {
					coldHarness.coordinator.WithAutoTune(segment3Cfg)
				}
				job := coldHarness.tickAndAdvance(t)
				coldSnapshots = append(coldSnapshots, snapshotFromFetchJob(job))
				coldBatches = append(coldBatches, job.BatchSize)
			}

			warmFirst := newAutoTuneHarnessWithHeadSeries(tc.chain, tc.network, tc.address, 100, heads[:splitTick], segment1Cfg)
			warmSnapshots := make([]lagAwareJobSnapshot, 0, len(heads))
			warmBatches := make([]int, 0, len(heads))
			for i := 0; i < splitTick; i++ {
				if i == 2 {
					warmFirst.coordinator.WithAutoTune(segment2Cfg)
				}
				if i == 4 {
					warmFirst.coordinator.WithAutoTune(segment3Cfg)
				}
				if i == 6 {
					warmFirst.coordinator.WithAutoTune(rollbackCfg)
				}
				job := warmFirst.tickAndAdvance(t)
				warmSnapshots = append(warmSnapshots, snapshotFromFetchJob(job))
				warmBatches = append(warmBatches, job.BatchSize)
			}

			restartState := warmFirst.coordinator.ExportAutoTuneRestartState()
			require.NotNil(t, restartState)
			assert.Equal(t, rollbackCfg.PolicyVersion, restartState.PolicyVersion)
			assert.Equal(t, rollbackCfg.PolicyManifestDigest, restartState.PolicyManifestDigest)
			assert.Equal(t, rollbackCfg.PolicyManifestRefreshEpoch, restartState.PolicyEpoch)
			assert.Greater(t, restartState.PolicyActivationRemaining, 0, "restart state must preserve rollback-lineage activation hold countdown at boundary")
			resumeCursor := warmFirst.cursorRepo.GetByAddress(tc.address)
			require.NotNil(t, resumeCursor)

			warmSecond := newAutoTuneHarnessWithWarmStartAndHeadSeries(
				tc.chain,
				tc.network,
				tc.address,
				resumeCursor.CursorSequence,
				heads[splitTick:],
				rollbackCfg,
				restartState,
			)

			for i := splitTick; i < len(heads); i++ {
				if i == 8 {
					warmSecond.coordinator.WithAutoTune(staleRollbackCfg)
					state := warmSecond.coordinator.ExportAutoTuneRestartState()
					require.NotNil(t, state)
					assert.Equal(t, rollbackCfg.PolicyManifestDigest, state.PolicyManifestDigest, "replay stale rollback must be rejected deterministically")
					assert.Equal(t, rollbackCfg.PolicyManifestRefreshEpoch, state.PolicyEpoch)
				}
				if i == 10 {
					warmSecond.coordinator.WithAutoTune(segment3Cfg)
					state := warmSecond.coordinator.ExportAutoTuneRestartState()
					require.NotNil(t, state)
					assert.Equal(t, segment3Cfg.PolicyManifestDigest, state.PolicyManifestDigest, "replay rollback+re-forward apply must be deterministic")
					assert.Equal(t, segment3Cfg.PolicyManifestRefreshEpoch, state.PolicyEpoch)
				}
				if i == 12 {
					warmSecond.coordinator.WithAutoTune(segment3Cfg)
					state := warmSecond.coordinator.ExportAutoTuneRestartState()
					require.NotNil(t, state)
					assert.Equal(t, segment3Cfg.PolicyManifestDigest, state.PolicyManifestDigest, "replay rollback+re-forward re-apply must remain deterministic")
					assert.Equal(t, segment3Cfg.PolicyManifestRefreshEpoch, state.PolicyEpoch)
				}
				job := warmSecond.tickAndAdvance(t)
				warmSnapshots = append(warmSnapshots, snapshotFromFetchJob(job))
				warmBatches = append(warmBatches, job.BatchSize)
			}

			assert.Equal(t, coldSnapshots, warmSnapshots, "policy-manifest rollback-lineage replay/resume must converge to deterministic canonical tuples")
			assert.Equal(t, coldBatches, warmBatches, "policy-manifest rollback-lineage replay/resume must preserve deterministic control decisions")
			assertNoDuplicateOrMissingLogicalSnapshots(t, coldSnapshots, warmSnapshots, "cold vs warm rollback-lineage replay")
			assertCursorMonotonicByAddress(t, warmSnapshots)
		})
	}
}

func TestTick_AutoTunePolicyManifestRollbackCrashpointPermutationsConvergeAcrossMandatoryChains(t *testing.T) {
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
			address: "7nYBpkEPkDD6m1JKBGwvftG7bHjJErJPjTH3VbKrcr01",
		},
		{
			name:    "base-sepolia",
			chain:   model.ChainBase,
			network: model.NetworkSepolia,
			address: "0x1234567890123456789012345678901234567890",
		},
		{
			name:    "btc-testnet",
			chain:   model.ChainBTC,
			network: model.NetworkTestnet,
			address: "tb1qmanifestrollbackcrash0000000000000000",
		},
	}

	segment1Cfg := AutoTuneConfig{
		Enabled:                    true,
		MinBatchSize:               60,
		MaxBatchSize:               320,
		StepUp:                     20,
		StepDown:                   10,
		LagHighWatermark:           80,
		LagLowWatermark:            20,
		QueueHighWatermarkPct:      90,
		QueueLowWatermarkPct:       10,
		HysteresisTicks:            1,
		CooldownTicks:              1,
		PolicyVersion:              "policy-v2",
		PolicyManifestDigest:       "manifest-tail-v2a",
		PolicyManifestRefreshEpoch: 1,
		PolicyActivationHoldTicks:  2,
	}
	segment2Cfg := segment1Cfg
	segment2Cfg.PolicyManifestDigest = "manifest-tail-v2b"
	segment2Cfg.PolicyManifestRefreshEpoch = 2
	segment3Cfg := segment1Cfg
	segment3Cfg.PolicyManifestDigest = "manifest-tail-v2c"
	segment3Cfg.PolicyManifestRefreshEpoch = 3
	rollbackCfg := segment2Cfg
	rollbackCfg.PolicyManifestDigest = "manifest-tail-v2b|rollback-from-seq=3|rollback-to-seq=2|rollback-forward-seq=3"
	staleRollbackCfg := segment1Cfg
	staleRollbackCfg.PolicyManifestDigest = "manifest-tail-v2a|rollback-from-seq=3|rollback-to-seq=1|rollback-forward-seq=3"
	staleRollbackCfg.PolicyManifestRefreshEpoch = 1

	policySchedule := map[int]AutoTuneConfig{
		2:  segment2Cfg,
		4:  segment3Cfg,
		6:  rollbackCfg,
		8:  staleRollbackCfg,
		10: segment3Cfg,
		12: segment3Cfg,
	}

	heads := []int64{260, 261, 262, 263, 264, 265, 266, 267, 268, 269, 270, 271, 272}
	permutations := []struct {
		name       string
		crashTicks []int
	}{
		{
			name:       "rollback-apply-crash-resume",
			crashTicks: []int{7},
		},
		{
			name:       "rollback-checkpoint-resume-crash-resume",
			crashTicks: []int{8},
		},
		{
			name:       "rollback-reforward-crash-resume",
			crashTicks: []int{7, 11},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			baselineSnapshots, baselineBatches := runAutoTuneTraceWithPolicyScheduleAndCrashpoints(
				t,
				tc.chain,
				tc.network,
				tc.address,
				100,
				heads,
				segment1Cfg,
				policySchedule,
				nil,
			)

			for _, permutation := range permutations {
				permutation := permutation
				t.Run(permutation.name, func(t *testing.T) {
					candidateSnapshots, candidateBatches := runAutoTuneTraceWithPolicyScheduleAndCrashpoints(
						t,
						tc.chain,
						tc.network,
						tc.address,
						100,
						heads,
						segment1Cfg,
						policySchedule,
						permutation.crashTicks,
					)

					assert.Equal(t, baselineSnapshots, candidateSnapshots, "rollback-crashpoint permutations must converge to deterministic canonical tuples")
					assert.Equal(t, baselineBatches, candidateBatches, "rollback-crashpoint permutations must preserve deterministic control decisions")
					assertNoDuplicateOrMissingLogicalSnapshots(t, baselineSnapshots, candidateSnapshots, "rollback-crashpoint baseline vs candidate")
					assertCursorMonotonicByAddress(t, candidateSnapshots)
				})
			}
		})
	}
}

func TestTick_AutoTuneOneChainPolicyManifestRollbackCrashpointDoesNotBleedAcrossOtherMandatoryChains(t *testing.T) {
	segment1Cfg := AutoTuneConfig{
		Enabled:                    true,
		MinBatchSize:               60,
		MaxBatchSize:               320,
		StepUp:                     20,
		StepDown:                   10,
		LagHighWatermark:           80,
		LagLowWatermark:            20,
		QueueHighWatermarkPct:      90,
		QueueLowWatermarkPct:       10,
		HysteresisTicks:            1,
		CooldownTicks:              1,
		PolicyVersion:              "policy-v2",
		PolicyManifestDigest:       "manifest-tail-v2a",
		PolicyManifestRefreshEpoch: 1,
		PolicyActivationHoldTicks:  2,
	}
	segment2Cfg := segment1Cfg
	segment2Cfg.PolicyManifestDigest = "manifest-tail-v2b"
	segment2Cfg.PolicyManifestRefreshEpoch = 2
	segment3Cfg := segment1Cfg
	segment3Cfg.PolicyManifestDigest = "manifest-tail-v2c"
	segment3Cfg.PolicyManifestRefreshEpoch = 3
	rollbackCfg := segment2Cfg
	rollbackCfg.PolicyManifestDigest = "manifest-tail-v2b|rollback-from-seq=3|rollback-to-seq=2|rollback-forward-seq=3"
	staleRollbackCfg := segment1Cfg
	staleRollbackCfg.PolicyManifestDigest = "manifest-tail-v2a|rollback-from-seq=3|rollback-to-seq=1|rollback-forward-seq=3"
	staleRollbackCfg.PolicyManifestRefreshEpoch = 1

	policySchedule := map[int]AutoTuneConfig{
		2:  segment2Cfg,
		4:  segment3Cfg,
		6:  rollbackCfg,
		8:  staleRollbackCfg,
		10: segment3Cfg,
		12: segment3Cfg,
	}

	const tickCount = 13
	healthyBaseAddress := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	healthyBTCAddress := "tb1qmanifestrollbackcrashhealthy000000000000"
	laggingSolanaAddress := "7nYBpkEPkDD6m1JKBGwvftG7bHjJErJPjTH3VbKrcr01"

	healthyHeads := []int64{130, 131, 132, 133, 134, 135, 136, 137, 138, 139, 140, 141, 142}
	laggingHeads := []int64{260, 261, 262, 263, 264, 265, 266, 267, 268, 269, 270, 271, 272}

	baseBaseline := newAutoTuneHarnessWithHeadSeries(
		model.ChainBase,
		model.NetworkSepolia,
		healthyBaseAddress,
		120,
		healthyHeads,
		segment1Cfg,
	)
	baseBaselineSnapshots, baseBaselineBatches := collectAutoTuneTrace(t, baseBaseline, tickCount)

	btcBaseline := newAutoTuneHarnessWithHeadSeries(
		model.ChainBTC,
		model.NetworkTestnet,
		healthyBTCAddress,
		120,
		healthyHeads,
		segment1Cfg,
	)
	btcBaselineSnapshots, btcBaselineBatches := collectAutoTuneTrace(t, btcBaseline, tickCount)

	laggingNoCrashSnapshots, laggingNoCrashBatches := runAutoTuneTraceWithPolicyScheduleAndCrashpoints(
		t,
		model.ChainSolana,
		model.NetworkDevnet,
		laggingSolanaAddress,
		100,
		laggingHeads,
		segment1Cfg,
		policySchedule,
		nil,
	)

	baseInterleaved := newAutoTuneHarnessWithHeadSeries(
		model.ChainBase,
		model.NetworkSepolia,
		healthyBaseAddress,
		120,
		healthyHeads,
		segment1Cfg,
	)
	btcInterleaved := newAutoTuneHarnessWithHeadSeries(
		model.ChainBTC,
		model.NetworkTestnet,
		healthyBTCAddress,
		120,
		healthyHeads,
		segment1Cfg,
	)
	laggingInterleaved := newAutoTuneHarnessWithHeadSeries(
		model.ChainSolana,
		model.NetworkDevnet,
		laggingSolanaAddress,
		100,
		laggingHeads,
		segment1Cfg,
	)

	baseSnapshots := make([]lagAwareJobSnapshot, 0, tickCount)
	baseBatches := make([]int, 0, tickCount)
	btcSnapshots := make([]lagAwareJobSnapshot, 0, tickCount)
	btcBatches := make([]int, 0, tickCount)
	laggingSnapshots := make([]lagAwareJobSnapshot, 0, tickCount)
	laggingBatches := make([]int, 0, tickCount)

	crashTicks := []int{7, 11}
	crashIndex := 0
	activeLaggingCfg := segment1Cfg

	for i := 0; i < tickCount; i++ {
		if crashIndex < len(crashTicks) && i == crashTicks[crashIndex] {
			restartState := laggingInterleaved.coordinator.ExportAutoTuneRestartState()
			require.NotNil(t, restartState)
			resumeCursor := laggingInterleaved.cursorRepo.GetByAddress(laggingSolanaAddress)
			require.NotNil(t, resumeCursor)
			laggingInterleaved = newAutoTuneHarnessWithWarmStartAndHeadSeries(
				model.ChainSolana,
				model.NetworkDevnet,
				laggingSolanaAddress,
				resumeCursor.CursorSequence,
				laggingHeads[i:],
				activeLaggingCfg,
				restartState,
			)
			crashIndex++
		}
		if cfg, ok := policySchedule[i]; ok {
			activeLaggingCfg = cfg
			laggingInterleaved.coordinator.WithAutoTune(cfg)
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

	assert.Equal(t, baseBaselineSnapshots, baseSnapshots, "solana rollback-crashpoint transition must not alter base canonical tuples")
	assert.Equal(t, baseBaselineBatches, baseBatches, "solana rollback-crashpoint transition must not alter base control decisions")
	assert.Equal(t, btcBaselineSnapshots, btcSnapshots, "solana rollback-crashpoint transition must not alter btc canonical tuples")
	assert.Equal(t, btcBaselineBatches, btcBatches, "solana rollback-crashpoint transition must not alter btc control decisions")

	assert.Equal(t, laggingNoCrashSnapshots, laggingSnapshots, "lagging rollback-crashpoint replay/resume must preserve canonical tuples")
	assert.Equal(t, laggingNoCrashBatches, laggingBatches, "lagging rollback-crashpoint replay/resume must preserve deterministic control decisions")
	assertNoDuplicateOrMissingLogicalSnapshots(t, laggingNoCrashSnapshots, laggingSnapshots, "lagging no-crash vs lagging crashpoint replay")

	assertCursorMonotonicByAddress(t, baseSnapshots)
	assertCursorMonotonicByAddress(t, btcSnapshots)
	assertCursorMonotonicByAddress(t, laggingSnapshots)
}

func TestTick_AutoTunePolicyManifestRollbackCheckpointFencePermutationsConvergeAcrossMandatoryChains(t *testing.T) {
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
			address: "7nYBpkEPkDD6m1JKBGwvftG7bHjJErJPjTH3VbKfence01",
		},
		{
			name:    "base-sepolia",
			chain:   model.ChainBase,
			network: model.NetworkSepolia,
			address: "0xabcdefabcdefabcdefabcdefabcdefabcdefabcd",
		},
		{
			name:    "btc-testnet",
			chain:   model.ChainBTC,
			network: model.NetworkTestnet,
			address: "tb1qmanifestrollbackfence0000000000000000",
		},
	}

	segment1Cfg := AutoTuneConfig{
		Enabled:                    true,
		MinBatchSize:               60,
		MaxBatchSize:               320,
		StepUp:                     20,
		StepDown:                   10,
		LagHighWatermark:           80,
		LagLowWatermark:            20,
		QueueHighWatermarkPct:      90,
		QueueLowWatermarkPct:       10,
		HysteresisTicks:            1,
		CooldownTicks:              1,
		PolicyVersion:              "policy-v2",
		PolicyManifestDigest:       "manifest-tail-v2a",
		PolicyManifestRefreshEpoch: 1,
		PolicyActivationHoldTicks:  2,
	}
	segment2Cfg := segment1Cfg
	segment2Cfg.PolicyManifestDigest = "manifest-tail-v2b"
	segment2Cfg.PolicyManifestRefreshEpoch = 2
	segment3Cfg := segment1Cfg
	segment3Cfg.PolicyManifestDigest = "manifest-tail-v2c"
	segment3Cfg.PolicyManifestRefreshEpoch = 3
	rollbackCfg := segment2Cfg
	rollbackCfg.PolicyManifestDigest = "manifest-tail-v2b|rollback-from-seq=3|rollback-to-seq=2|rollback-forward-seq=3"
	staleRollbackCfg := segment1Cfg
	staleRollbackCfg.PolicyManifestDigest = "manifest-tail-v2a|rollback-from-seq=3|rollback-to-seq=1|rollback-forward-seq=3"
	staleRollbackCfg.PolicyManifestRefreshEpoch = 1

	policySchedule := map[int]AutoTuneConfig{
		2:  segment2Cfg,
		4:  segment3Cfg,
		6:  rollbackCfg,
		8:  staleRollbackCfg,
		10: segment3Cfg,
		12: segment3Cfg,
	}

	heads := []int64{260, 261, 262, 263, 264, 265, 266, 267, 268, 269, 270, 271, 272}
	permutations := []struct {
		name                  string
		staleFenceCaptureTick map[int]struct{}
		crashpoints           []autoTuneCheckpointFenceCrashpoint
	}{
		{
			name: "no-fence-baseline",
		},
		{
			name:                  "crash-before-fence-flush",
			staleFenceCaptureTick: map[int]struct{}{6: {}},
			crashpoints: []autoTuneCheckpointFenceCrashpoint{
				{Tick: 7, UseStaleFenceState: true},
			},
		},
		{
			name:        "crash-after-fence-flush",
			crashpoints: []autoTuneCheckpointFenceCrashpoint{{Tick: 7}},
		},
		{
			name:                  "rollback-reforward-fence-resume",
			staleFenceCaptureTick: map[int]struct{}{6: {}},
			crashpoints: []autoTuneCheckpointFenceCrashpoint{
				{Tick: 7, UseStaleFenceState: true},
				{Tick: 11},
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			baselineSnapshots, baselineBatches := runAutoTuneTraceWithPolicyScheduleAndCheckpointFenceCrashpoints(
				t,
				tc.chain,
				tc.network,
				tc.address,
				100,
				heads,
				segment1Cfg,
				policySchedule,
				nil,
				nil,
			)

			for _, permutation := range permutations {
				permutation := permutation
				t.Run(permutation.name, func(t *testing.T) {
					candidateSnapshots, candidateBatches := runAutoTuneTraceWithPolicyScheduleAndCheckpointFenceCrashpoints(
						t,
						tc.chain,
						tc.network,
						tc.address,
						100,
						heads,
						segment1Cfg,
						policySchedule,
						permutation.staleFenceCaptureTick,
						permutation.crashpoints,
					)

					assert.Equal(t, baselineSnapshots, candidateSnapshots, "rollback checkpoint-fence permutations must converge to deterministic canonical tuples")
					assert.Equal(t, baselineBatches, candidateBatches, "rollback checkpoint-fence permutations must preserve deterministic control decisions")
					assertNoDuplicateOrMissingLogicalSnapshots(t, baselineSnapshots, candidateSnapshots, "rollback checkpoint-fence baseline vs candidate")
					assertCursorMonotonicByAddress(t, candidateSnapshots)
				})
			}
		})
	}
}

func TestTick_AutoTuneOneChainPolicyManifestRollbackCheckpointFenceDoesNotBleedAcrossOtherMandatoryChains(t *testing.T) {
	segment1Cfg := AutoTuneConfig{
		Enabled:                    true,
		MinBatchSize:               60,
		MaxBatchSize:               320,
		StepUp:                     20,
		StepDown:                   10,
		LagHighWatermark:           80,
		LagLowWatermark:            20,
		QueueHighWatermarkPct:      90,
		QueueLowWatermarkPct:       10,
		HysteresisTicks:            1,
		CooldownTicks:              1,
		PolicyVersion:              "policy-v2",
		PolicyManifestDigest:       "manifest-tail-v2a",
		PolicyManifestRefreshEpoch: 1,
		PolicyActivationHoldTicks:  2,
	}
	segment2Cfg := segment1Cfg
	segment2Cfg.PolicyManifestDigest = "manifest-tail-v2b"
	segment2Cfg.PolicyManifestRefreshEpoch = 2
	segment3Cfg := segment1Cfg
	segment3Cfg.PolicyManifestDigest = "manifest-tail-v2c"
	segment3Cfg.PolicyManifestRefreshEpoch = 3
	rollbackCfg := segment2Cfg
	rollbackCfg.PolicyManifestDigest = "manifest-tail-v2b|rollback-from-seq=3|rollback-to-seq=2|rollback-forward-seq=3"
	staleRollbackCfg := segment1Cfg
	staleRollbackCfg.PolicyManifestDigest = "manifest-tail-v2a|rollback-from-seq=3|rollback-to-seq=1|rollback-forward-seq=3"
	staleRollbackCfg.PolicyManifestRefreshEpoch = 1

	policySchedule := map[int]AutoTuneConfig{
		2:  segment2Cfg,
		4:  segment3Cfg,
		6:  rollbackCfg,
		8:  staleRollbackCfg,
		10: segment3Cfg,
		12: segment3Cfg,
	}

	const tickCount = 13
	healthyBaseAddress := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	healthyBTCAddress := "tb1qmanifestrollbackfencehealthy00000000000"
	laggingSolanaAddress := "7nYBpkEPkDD6m1JKBGwvftG7bHjJErJPjTH3VbKfence01"

	healthyHeads := []int64{130, 131, 132, 133, 134, 135, 136, 137, 138, 139, 140, 141, 142}
	laggingHeads := []int64{260, 261, 262, 263, 264, 265, 266, 267, 268, 269, 270, 271, 272}

	baseBaseline := newAutoTuneHarnessWithHeadSeries(
		model.ChainBase,
		model.NetworkSepolia,
		healthyBaseAddress,
		120,
		healthyHeads,
		segment1Cfg,
	)
	baseBaselineSnapshots, baseBaselineBatches := collectAutoTuneTrace(t, baseBaseline, tickCount)

	btcBaseline := newAutoTuneHarnessWithHeadSeries(
		model.ChainBTC,
		model.NetworkTestnet,
		healthyBTCAddress,
		120,
		healthyHeads,
		segment1Cfg,
	)
	btcBaselineSnapshots, btcBaselineBatches := collectAutoTuneTrace(t, btcBaseline, tickCount)

	laggingBaselineSnapshots, laggingBaselineBatches := runAutoTuneTraceWithPolicyScheduleAndCheckpointFenceCrashpoints(
		t,
		model.ChainSolana,
		model.NetworkDevnet,
		laggingSolanaAddress,
		100,
		laggingHeads,
		segment1Cfg,
		policySchedule,
		nil,
		nil,
	)

	baseInterleaved := newAutoTuneHarnessWithHeadSeries(
		model.ChainBase,
		model.NetworkSepolia,
		healthyBaseAddress,
		120,
		healthyHeads,
		segment1Cfg,
	)
	btcInterleaved := newAutoTuneHarnessWithHeadSeries(
		model.ChainBTC,
		model.NetworkTestnet,
		healthyBTCAddress,
		120,
		healthyHeads,
		segment1Cfg,
	)
	laggingInterleaved := newAutoTuneHarnessWithHeadSeries(
		model.ChainSolana,
		model.NetworkDevnet,
		laggingSolanaAddress,
		100,
		laggingHeads,
		segment1Cfg,
	)

	baseSnapshots := make([]lagAwareJobSnapshot, 0, tickCount)
	baseBatches := make([]int, 0, tickCount)
	btcSnapshots := make([]lagAwareJobSnapshot, 0, tickCount)
	btcBatches := make([]int, 0, tickCount)
	laggingSnapshots := make([]lagAwareJobSnapshot, 0, tickCount)
	laggingBatches := make([]int, 0, tickCount)

	activeLaggingCfg := segment1Cfg
	staleFenceCaptureTicks := map[int]struct{}{6: {}}
	crashpoints := map[int]bool{7: true, 11: false}
	var latestStaleFenceState *AutoTuneRestartState

	for i := 0; i < tickCount; i++ {
		if cfg, ok := policySchedule[i]; ok {
			activeLaggingCfg = cfg
			laggingInterleaved.coordinator.WithAutoTune(cfg)
			if _, capture := staleFenceCaptureTicks[i]; capture {
				latestStaleFenceState = cloneAutoTuneRestartState(laggingInterleaved.coordinator.ExportAutoTuneRestartState())
				require.NotNil(t, latestStaleFenceState)
			}
		}

		if useStaleFence, ok := crashpoints[i]; ok {
			var restartState *AutoTuneRestartState
			if useStaleFence {
				require.NotNil(t, latestStaleFenceState, "stale fence crashpoint requires captured pre-flush state")
				restartState = cloneAutoTuneRestartState(latestStaleFenceState)
			} else {
				restartState = laggingInterleaved.coordinator.ExportAutoTuneRestartState()
			}
			require.NotNil(t, restartState)
			resumeCursor := laggingInterleaved.cursorRepo.GetByAddress(laggingSolanaAddress)
			require.NotNil(t, resumeCursor)
			laggingInterleaved = newAutoTuneHarnessWithWarmStartAndHeadSeries(
				model.ChainSolana,
				model.NetworkDevnet,
				laggingSolanaAddress,
				resumeCursor.CursorSequence,
				laggingHeads[i:],
				activeLaggingCfg,
				restartState,
			)
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

	assert.Equal(t, baseBaselineSnapshots, baseSnapshots, "solana rollback checkpoint-fence transition must not alter base canonical tuples")
	assert.Equal(t, baseBaselineBatches, baseBatches, "solana rollback checkpoint-fence transition must not alter base control decisions")
	assert.Equal(t, btcBaselineSnapshots, btcSnapshots, "solana rollback checkpoint-fence transition must not alter btc canonical tuples")
	assert.Equal(t, btcBaselineBatches, btcBatches, "solana rollback checkpoint-fence transition must not alter btc control decisions")

	assert.Equal(t, laggingBaselineSnapshots, laggingSnapshots, "lagging rollback checkpoint-fence replay/resume must preserve canonical tuples")
	assert.Equal(t, laggingBaselineBatches, laggingBatches, "lagging rollback checkpoint-fence replay/resume must preserve deterministic control decisions")
	assertNoDuplicateOrMissingLogicalSnapshots(t, laggingBaselineSnapshots, laggingSnapshots, "lagging no-crash vs lagging checkpoint-fence replay")

	assertCursorMonotonicByAddress(t, baseSnapshots)
	assertCursorMonotonicByAddress(t, btcSnapshots)
	assertCursorMonotonicByAddress(t, laggingSnapshots)
}

func TestTick_AutoTunePolicyManifestRollbackCheckpointFenceEpochCompactionPermutationsConvergeAcrossMandatoryChains(t *testing.T) {
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
			address: "7nYBpkEPkDD6m1JKBGwvftG7bHjJErJPjTH3VbKcmpct01",
		},
		{
			name:    "base-sepolia",
			chain:   model.ChainBase,
			network: model.NetworkSepolia,
			address: "0xabcdefabcdefabcdefabcdefabcdefabcdefff01",
		},
		{
			name:    "btc-testnet",
			chain:   model.ChainBTC,
			network: model.NetworkTestnet,
			address: "tb1qmanifestrollbackcompact00000000000000",
		},
	}

	segment1Cfg := AutoTuneConfig{
		Enabled:                    true,
		MinBatchSize:               60,
		MaxBatchSize:               320,
		StepUp:                     20,
		StepDown:                   10,
		LagHighWatermark:           80,
		LagLowWatermark:            20,
		QueueHighWatermarkPct:      90,
		QueueLowWatermarkPct:       10,
		HysteresisTicks:            1,
		CooldownTicks:              1,
		PolicyVersion:              "policy-v2",
		PolicyManifestDigest:       "manifest-tail-v2a",
		PolicyManifestRefreshEpoch: 1,
		PolicyActivationHoldTicks:  2,
	}
	segment2Cfg := segment1Cfg
	segment2Cfg.PolicyManifestDigest = "manifest-tail-v2b"
	segment2Cfg.PolicyManifestRefreshEpoch = 2
	segment3Cfg := segment1Cfg
	segment3Cfg.PolicyManifestDigest = "manifest-tail-v2c"
	segment3Cfg.PolicyManifestRefreshEpoch = 3
	rollbackCfg := segment2Cfg
	rollbackCfg.PolicyManifestDigest = "manifest-tail-v2b|rollback-from-seq=3|rollback-to-seq=2|rollback-forward-seq=3"
	compactionCfg := rollbackCfg
	compactionCfg.PolicyManifestDigest = rollbackCfg.PolicyManifestDigest + "|rollback-fence-tombstone=1"

	noCompactionSchedule := map[int]AutoTuneConfig{
		2:  segment2Cfg,
		4:  segment3Cfg,
		6:  rollbackCfg,
		8:  rollbackCfg,
		10: rollbackCfg,
		11: segment3Cfg,
		12: segment3Cfg,
	}
	liveCompactionSchedule := map[int]AutoTuneConfig{
		2:  segment2Cfg,
		4:  segment3Cfg,
		6:  rollbackCfg,
		8:  compactionCfg,
		10: compactionCfg,
		11: segment3Cfg,
		12: segment3Cfg,
	}
	rollbackReforwardAfterCompactionSchedule := map[int]AutoTuneConfig{
		2:  segment2Cfg,
		4:  segment3Cfg,
		6:  rollbackCfg,
		8:  compactionCfg,
		10: rollbackCfg,
		11: segment3Cfg,
		12: segment3Cfg,
	}

	heads := []int64{260, 261, 262, 263, 264, 265, 266, 267, 268, 269, 270, 271, 272}
	permutations := []struct {
		name                  string
		policySchedule        map[int]AutoTuneConfig
		staleFenceCaptureTick map[int]struct{}
		crashpoints           []autoTuneCheckpointFenceCrashpoint
		assertControlParity   bool
	}{
		{
			name:                "live-compaction",
			policySchedule:      liveCompactionSchedule,
			assertControlParity: true,
		},
		{
			name:                  "crash-during-compaction-restart",
			policySchedule:        liveCompactionSchedule,
			staleFenceCaptureTick: map[int]struct{}{6: {}},
			crashpoints: []autoTuneCheckpointFenceCrashpoint{
				{Tick: 9, UseStaleFenceState: true},
			},
			assertControlParity: false,
		},
		{
			name:                "rollback-reforward-after-compaction",
			policySchedule:      rollbackReforwardAfterCompactionSchedule,
			assertControlParity: true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			baselineSnapshots, baselineBatches := runAutoTuneTraceWithPolicyScheduleAndCheckpointFenceCrashpoints(
				t,
				tc.chain,
				tc.network,
				tc.address,
				100,
				heads,
				segment1Cfg,
				noCompactionSchedule,
				nil,
				nil,
			)

			for _, permutation := range permutations {
				permutation := permutation
				t.Run(permutation.name, func(t *testing.T) {
					candidateSnapshots, candidateBatches := runAutoTuneTraceWithPolicyScheduleAndCheckpointFenceCrashpoints(
						t,
						tc.chain,
						tc.network,
						tc.address,
						100,
						heads,
						segment1Cfg,
						permutation.policySchedule,
						permutation.staleFenceCaptureTick,
						permutation.crashpoints,
					)

					assert.Equal(t, baselineSnapshots, candidateSnapshots, "rollback checkpoint-fence epoch-compaction permutations must converge to deterministic canonical tuples")
					if permutation.assertControlParity {
						assert.Equal(t, baselineBatches, candidateBatches, "rollback checkpoint-fence epoch-compaction permutations must preserve deterministic control decisions")
					}
					assertNoDuplicateOrMissingLogicalSnapshots(t, baselineSnapshots, candidateSnapshots, "rollback checkpoint-fence epoch-compaction baseline vs candidate")
					assertCursorMonotonicByAddress(t, candidateSnapshots)
				})
			}
		})
	}
}

func TestTick_AutoTuneOneChainPolicyManifestRollbackCheckpointFenceEpochCompactionDoesNotBleedAcrossOtherMandatoryChains(t *testing.T) {
	segment1Cfg := AutoTuneConfig{
		Enabled:                    true,
		MinBatchSize:               60,
		MaxBatchSize:               320,
		StepUp:                     20,
		StepDown:                   10,
		LagHighWatermark:           80,
		LagLowWatermark:            20,
		QueueHighWatermarkPct:      90,
		QueueLowWatermarkPct:       10,
		HysteresisTicks:            1,
		CooldownTicks:              1,
		PolicyVersion:              "policy-v2",
		PolicyManifestDigest:       "manifest-tail-v2a",
		PolicyManifestRefreshEpoch: 1,
		PolicyActivationHoldTicks:  2,
	}
	segment2Cfg := segment1Cfg
	segment2Cfg.PolicyManifestDigest = "manifest-tail-v2b"
	segment2Cfg.PolicyManifestRefreshEpoch = 2
	segment3Cfg := segment1Cfg
	segment3Cfg.PolicyManifestDigest = "manifest-tail-v2c"
	segment3Cfg.PolicyManifestRefreshEpoch = 3
	rollbackCfg := segment2Cfg
	rollbackCfg.PolicyManifestDigest = "manifest-tail-v2b|rollback-from-seq=3|rollback-to-seq=2|rollback-forward-seq=3"
	compactionCfg := rollbackCfg
	compactionCfg.PolicyManifestDigest = rollbackCfg.PolicyManifestDigest + "|rollback-fence-tombstone=1"

	noCompactionSchedule := map[int]AutoTuneConfig{
		2:  segment2Cfg,
		4:  segment3Cfg,
		6:  rollbackCfg,
		8:  rollbackCfg,
		10: rollbackCfg,
		11: segment3Cfg,
		12: segment3Cfg,
	}
	compactionReplaySchedule := map[int]AutoTuneConfig{
		2:  segment2Cfg,
		4:  segment3Cfg,
		6:  rollbackCfg,
		8:  compactionCfg,
		10: rollbackCfg,
		11: segment3Cfg,
		12: segment3Cfg,
	}

	const tickCount = 13
	healthyBaseAddress := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbcc01"
	healthyBTCAddress := "tb1qmanifestrollbackcompacthealthy00000000"
	laggingSolanaAddress := "7nYBpkEPkDD6m1JKBGwvftG7bHjJErJPjTH3VbKcmpct01"

	healthyHeads := []int64{130, 131, 132, 133, 134, 135, 136, 137, 138, 139, 140, 141, 142}
	laggingHeads := []int64{260, 261, 262, 263, 264, 265, 266, 267, 268, 269, 270, 271, 272}

	baseBaseline := newAutoTuneHarnessWithHeadSeries(
		model.ChainBase,
		model.NetworkSepolia,
		healthyBaseAddress,
		120,
		healthyHeads,
		segment1Cfg,
	)
	baseBaselineSnapshots, baseBaselineBatches := collectAutoTuneTrace(t, baseBaseline, tickCount)

	btcBaseline := newAutoTuneHarnessWithHeadSeries(
		model.ChainBTC,
		model.NetworkTestnet,
		healthyBTCAddress,
		120,
		healthyHeads,
		segment1Cfg,
	)
	btcBaselineSnapshots, btcBaselineBatches := collectAutoTuneTrace(t, btcBaseline, tickCount)

	laggingBaselineSnapshots, _ := runAutoTuneTraceWithPolicyScheduleAndCheckpointFenceCrashpoints(
		t,
		model.ChainSolana,
		model.NetworkDevnet,
		laggingSolanaAddress,
		100,
		laggingHeads,
		segment1Cfg,
		noCompactionSchedule,
		nil,
		nil,
	)

	baseInterleaved := newAutoTuneHarnessWithHeadSeries(
		model.ChainBase,
		model.NetworkSepolia,
		healthyBaseAddress,
		120,
		healthyHeads,
		segment1Cfg,
	)
	btcInterleaved := newAutoTuneHarnessWithHeadSeries(
		model.ChainBTC,
		model.NetworkTestnet,
		healthyBTCAddress,
		120,
		healthyHeads,
		segment1Cfg,
	)
	laggingInterleaved := newAutoTuneHarnessWithHeadSeries(
		model.ChainSolana,
		model.NetworkDevnet,
		laggingSolanaAddress,
		100,
		laggingHeads,
		segment1Cfg,
	)

	baseSnapshots := make([]lagAwareJobSnapshot, 0, tickCount)
	baseBatches := make([]int, 0, tickCount)
	btcSnapshots := make([]lagAwareJobSnapshot, 0, tickCount)
	btcBatches := make([]int, 0, tickCount)
	laggingSnapshots := make([]lagAwareJobSnapshot, 0, tickCount)

	activeLaggingCfg := segment1Cfg
	staleFenceCaptureTicks := map[int]struct{}{6: {}}
	crashpoints := map[int]bool{9: true}
	var latestStaleFenceState *AutoTuneRestartState

	for i := 0; i < tickCount; i++ {
		if cfg, ok := compactionReplaySchedule[i]; ok {
			activeLaggingCfg = cfg
			laggingInterleaved.coordinator.WithAutoTune(cfg)
			if _, capture := staleFenceCaptureTicks[i]; capture {
				latestStaleFenceState = cloneAutoTuneRestartState(laggingInterleaved.coordinator.ExportAutoTuneRestartState())
				require.NotNil(t, latestStaleFenceState)
			}
		}

		if useStaleFence, ok := crashpoints[i]; ok {
			var restartState *AutoTuneRestartState
			if useStaleFence {
				require.NotNil(t, latestStaleFenceState, "stale compaction crashpoint requires captured pre-compaction state")
				restartState = cloneAutoTuneRestartState(latestStaleFenceState)
			} else {
				restartState = laggingInterleaved.coordinator.ExportAutoTuneRestartState()
			}
			require.NotNil(t, restartState)
			resumeCursor := laggingInterleaved.cursorRepo.GetByAddress(laggingSolanaAddress)
			require.NotNil(t, resumeCursor)
			laggingInterleaved = newAutoTuneHarnessWithWarmStartAndHeadSeries(
				model.ChainSolana,
				model.NetworkDevnet,
				laggingSolanaAddress,
				resumeCursor.CursorSequence,
				laggingHeads[i:],
				activeLaggingCfg,
				restartState,
			)
		}

		laggingJob := laggingInterleaved.tickAndAdvance(t)
		laggingSnapshots = append(laggingSnapshots, snapshotFromFetchJob(laggingJob))

		baseJob := baseInterleaved.tickAndAdvance(t)
		baseSnapshots = append(baseSnapshots, snapshotFromFetchJob(baseJob))
		baseBatches = append(baseBatches, baseJob.BatchSize)

		btcJob := btcInterleaved.tickAndAdvance(t)
		btcSnapshots = append(btcSnapshots, snapshotFromFetchJob(btcJob))
		btcBatches = append(btcBatches, btcJob.BatchSize)
	}

	assert.Equal(t, baseBaselineSnapshots, baseSnapshots, "solana rollback checkpoint-fence compaction transition must not alter base canonical tuples")
	assert.Equal(t, baseBaselineBatches, baseBatches, "solana rollback checkpoint-fence compaction transition must not alter base control decisions")
	assert.Equal(t, btcBaselineSnapshots, btcSnapshots, "solana rollback checkpoint-fence compaction transition must not alter btc canonical tuples")
	assert.Equal(t, btcBaselineBatches, btcBatches, "solana rollback checkpoint-fence compaction transition must not alter btc control decisions")

	assert.Equal(t, laggingBaselineSnapshots, laggingSnapshots, "lagging rollback checkpoint-fence compaction replay/resume must preserve canonical tuples")
	assertNoDuplicateOrMissingLogicalSnapshots(t, laggingBaselineSnapshots, laggingSnapshots, "lagging no-compaction baseline vs lagging checkpoint-fence compaction replay")

	assertCursorMonotonicByAddress(t, baseSnapshots)
	assertCursorMonotonicByAddress(t, btcSnapshots)
	assertCursorMonotonicByAddress(t, laggingSnapshots)
}

func TestTick_AutoTunePolicyManifestRollbackCheckpointFenceTombstoneExpiryPermutationsConvergeAcrossMandatoryChains(t *testing.T) {
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
			address: "7nYBpkEPkDD6m1JKBGwvftG7bHjJErJPjTH3VbKexp01",
		},
		{
			name:    "base-sepolia",
			chain:   model.ChainBase,
			network: model.NetworkSepolia,
			address: "0xabcdefabcdefabcdefabcdefabcdefabcdefff02",
		},
		{
			name:    "btc-testnet",
			chain:   model.ChainBTC,
			network: model.NetworkTestnet,
			address: "tb1qmanifestrollbackexpiry000000000000000",
		},
	}

	segment1Cfg := AutoTuneConfig{
		Enabled:                    true,
		MinBatchSize:               60,
		MaxBatchSize:               320,
		StepUp:                     20,
		StepDown:                   10,
		LagHighWatermark:           80,
		LagLowWatermark:            20,
		QueueHighWatermarkPct:      90,
		QueueLowWatermarkPct:       10,
		HysteresisTicks:            1,
		CooldownTicks:              1,
		PolicyVersion:              "policy-v2",
		PolicyManifestDigest:       "manifest-tail-v2a",
		PolicyManifestRefreshEpoch: 1,
		PolicyActivationHoldTicks:  2,
	}
	segment2Cfg := segment1Cfg
	segment2Cfg.PolicyManifestDigest = "manifest-tail-v2b"
	segment2Cfg.PolicyManifestRefreshEpoch = 2
	segment3Cfg := segment1Cfg
	segment3Cfg.PolicyManifestDigest = "manifest-tail-v2c"
	segment3Cfg.PolicyManifestRefreshEpoch = 3
	rollbackCfg := segment2Cfg
	rollbackCfg.PolicyManifestDigest = "manifest-tail-v2b|rollback-from-seq=3|rollback-to-seq=2|rollback-forward-seq=3"
	compactionCfg := rollbackCfg
	compactionCfg.PolicyManifestDigest = rollbackCfg.PolicyManifestDigest + "|rollback-fence-tombstone=1"
	expiryCfg := rollbackCfg
	expiryCfg.PolicyManifestDigest = rollbackCfg.PolicyManifestDigest + "|rollback-fence-tombstone-expiry-epoch=4"

	tombstoneRetainedSchedule := map[int]AutoTuneConfig{
		2:  segment2Cfg,
		4:  segment3Cfg,
		6:  rollbackCfg,
		8:  compactionCfg,
		10: compactionCfg,
		11: segment3Cfg,
		12: segment3Cfg,
	}
	expirySweepSchedule := map[int]AutoTuneConfig{
		2:  segment2Cfg,
		4:  segment3Cfg,
		6:  rollbackCfg,
		8:  compactionCfg,
		10: expiryCfg,
		11: segment3Cfg,
		12: segment3Cfg,
	}
	rollbackReforwardAfterExpirySchedule := map[int]AutoTuneConfig{
		2:  segment2Cfg,
		4:  segment3Cfg,
		6:  rollbackCfg,
		8:  compactionCfg,
		10: expiryCfg,
		11: rollbackCfg,
		12: segment3Cfg,
	}

	heads := []int64{260, 261, 262, 263, 264, 265, 266, 267, 268, 269, 270, 271, 272}
	permutations := []struct {
		name                  string
		policySchedule        map[int]AutoTuneConfig
		staleFenceCaptureTick map[int]struct{}
		crashpoints           []autoTuneCheckpointFenceCrashpoint
		assertControlParity   bool
	}{
		{
			name:                "tombstone-expiry-sweep",
			policySchedule:      expirySweepSchedule,
			assertControlParity: true,
		},
		{
			name:                  "crash-during-expiry-restart",
			policySchedule:        expirySweepSchedule,
			staleFenceCaptureTick: map[int]struct{}{8: {}},
			crashpoints: []autoTuneCheckpointFenceCrashpoint{
				{Tick: 10, UseStaleFenceState: true},
			},
			assertControlParity: false,
		},
		{
			name:                "rollback-reforward-after-expiry",
			policySchedule:      rollbackReforwardAfterExpirySchedule,
			assertControlParity: false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			baselineSnapshots, baselineBatches := runAutoTuneTraceWithPolicyScheduleAndCheckpointFenceCrashpoints(
				t,
				tc.chain,
				tc.network,
				tc.address,
				100,
				heads,
				segment1Cfg,
				tombstoneRetainedSchedule,
				nil,
				nil,
			)

			for _, permutation := range permutations {
				permutation := permutation
				t.Run(permutation.name, func(t *testing.T) {
					candidateSnapshots, candidateBatches := runAutoTuneTraceWithPolicyScheduleAndCheckpointFenceCrashpoints(
						t,
						tc.chain,
						tc.network,
						tc.address,
						100,
						heads,
						segment1Cfg,
						permutation.policySchedule,
						permutation.staleFenceCaptureTick,
						permutation.crashpoints,
					)

					assert.Equal(t, baselineSnapshots, candidateSnapshots, "rollback checkpoint-fence tombstone-expiry permutations must converge to deterministic canonical tuples")
					if permutation.assertControlParity {
						assert.Equal(t, baselineBatches, candidateBatches, "rollback checkpoint-fence tombstone-expiry permutations must preserve deterministic control decisions")
					}
					assertNoDuplicateOrMissingLogicalSnapshots(t, baselineSnapshots, candidateSnapshots, "rollback checkpoint-fence tombstone-expiry baseline vs candidate")
					assertCursorMonotonicByAddress(t, candidateSnapshots)
				})
			}
		})
	}
}

func TestTick_AutoTuneOneChainPolicyManifestRollbackCheckpointFenceTombstoneExpiryDoesNotBleedAcrossOtherMandatoryChains(t *testing.T) {
	segment1Cfg := AutoTuneConfig{
		Enabled:                    true,
		MinBatchSize:               60,
		MaxBatchSize:               320,
		StepUp:                     20,
		StepDown:                   10,
		LagHighWatermark:           80,
		LagLowWatermark:            20,
		QueueHighWatermarkPct:      90,
		QueueLowWatermarkPct:       10,
		HysteresisTicks:            1,
		CooldownTicks:              1,
		PolicyVersion:              "policy-v2",
		PolicyManifestDigest:       "manifest-tail-v2a",
		PolicyManifestRefreshEpoch: 1,
		PolicyActivationHoldTicks:  2,
	}
	segment2Cfg := segment1Cfg
	segment2Cfg.PolicyManifestDigest = "manifest-tail-v2b"
	segment2Cfg.PolicyManifestRefreshEpoch = 2
	segment3Cfg := segment1Cfg
	segment3Cfg.PolicyManifestDigest = "manifest-tail-v2c"
	segment3Cfg.PolicyManifestRefreshEpoch = 3
	rollbackCfg := segment2Cfg
	rollbackCfg.PolicyManifestDigest = "manifest-tail-v2b|rollback-from-seq=3|rollback-to-seq=2|rollback-forward-seq=3"
	compactionCfg := rollbackCfg
	compactionCfg.PolicyManifestDigest = rollbackCfg.PolicyManifestDigest + "|rollback-fence-tombstone=1"
	expiryCfg := rollbackCfg
	expiryCfg.PolicyManifestDigest = rollbackCfg.PolicyManifestDigest + "|rollback-fence-tombstone-expiry-epoch=4"

	tombstoneRetainedSchedule := map[int]AutoTuneConfig{
		2:  segment2Cfg,
		4:  segment3Cfg,
		6:  rollbackCfg,
		8:  compactionCfg,
		10: compactionCfg,
		11: segment3Cfg,
		12: segment3Cfg,
	}
	expiryReplaySchedule := map[int]AutoTuneConfig{
		2:  segment2Cfg,
		4:  segment3Cfg,
		6:  rollbackCfg,
		8:  compactionCfg,
		10: expiryCfg,
		11: rollbackCfg,
		12: segment3Cfg,
	}

	const tickCount = 13
	healthyBaseAddress := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbcc02"
	healthyBTCAddress := "tb1qmanifestrollbackexpiryhealthy0000000000"
	laggingSolanaAddress := "7nYBpkEPkDD6m1JKBGwvftG7bHjJErJPjTH3VbKexp01"

	healthyHeads := []int64{130, 131, 132, 133, 134, 135, 136, 137, 138, 139, 140, 141, 142}
	laggingHeads := []int64{260, 261, 262, 263, 264, 265, 266, 267, 268, 269, 270, 271, 272}

	baseBaseline := newAutoTuneHarnessWithHeadSeries(
		model.ChainBase,
		model.NetworkSepolia,
		healthyBaseAddress,
		120,
		healthyHeads,
		segment1Cfg,
	)
	baseBaselineSnapshots, baseBaselineBatches := collectAutoTuneTrace(t, baseBaseline, tickCount)

	btcBaseline := newAutoTuneHarnessWithHeadSeries(
		model.ChainBTC,
		model.NetworkTestnet,
		healthyBTCAddress,
		120,
		healthyHeads,
		segment1Cfg,
	)
	btcBaselineSnapshots, btcBaselineBatches := collectAutoTuneTrace(t, btcBaseline, tickCount)

	laggingBaselineSnapshots, _ := runAutoTuneTraceWithPolicyScheduleAndCheckpointFenceCrashpoints(
		t,
		model.ChainSolana,
		model.NetworkDevnet,
		laggingSolanaAddress,
		100,
		laggingHeads,
		segment1Cfg,
		tombstoneRetainedSchedule,
		nil,
		nil,
	)

	baseInterleaved := newAutoTuneHarnessWithHeadSeries(
		model.ChainBase,
		model.NetworkSepolia,
		healthyBaseAddress,
		120,
		healthyHeads,
		segment1Cfg,
	)
	btcInterleaved := newAutoTuneHarnessWithHeadSeries(
		model.ChainBTC,
		model.NetworkTestnet,
		healthyBTCAddress,
		120,
		healthyHeads,
		segment1Cfg,
	)
	laggingInterleaved := newAutoTuneHarnessWithHeadSeries(
		model.ChainSolana,
		model.NetworkDevnet,
		laggingSolanaAddress,
		100,
		laggingHeads,
		segment1Cfg,
	)

	baseSnapshots := make([]lagAwareJobSnapshot, 0, tickCount)
	baseBatches := make([]int, 0, tickCount)
	btcSnapshots := make([]lagAwareJobSnapshot, 0, tickCount)
	btcBatches := make([]int, 0, tickCount)
	laggingSnapshots := make([]lagAwareJobSnapshot, 0, tickCount)

	activeLaggingCfg := segment1Cfg
	staleFenceCaptureTicks := map[int]struct{}{8: {}}
	crashpoints := map[int]bool{10: true}
	var latestStaleFenceState *AutoTuneRestartState

	for i := 0; i < tickCount; i++ {
		if cfg, ok := expiryReplaySchedule[i]; ok {
			activeLaggingCfg = cfg
			laggingInterleaved.coordinator.WithAutoTune(cfg)
			if _, capture := staleFenceCaptureTicks[i]; capture {
				latestStaleFenceState = cloneAutoTuneRestartState(laggingInterleaved.coordinator.ExportAutoTuneRestartState())
				require.NotNil(t, latestStaleFenceState)
			}
			if i == 11 {
				state := laggingInterleaved.coordinator.ExportAutoTuneRestartState()
				require.NotNil(t, state)
				assert.Equal(t, expiryCfg.PolicyManifestDigest, state.PolicyManifestDigest, "stale post-expiry rollback ownership must not reactivate")
				assert.Equal(t, expiryCfg.PolicyManifestRefreshEpoch, state.PolicyEpoch)
			}
		}

		if useStaleFence, ok := crashpoints[i]; ok {
			var restartState *AutoTuneRestartState
			if useStaleFence {
				require.NotNil(t, latestStaleFenceState, "stale expiry crashpoint requires captured pre-expiry state")
				restartState = cloneAutoTuneRestartState(latestStaleFenceState)
			} else {
				restartState = laggingInterleaved.coordinator.ExportAutoTuneRestartState()
			}
			require.NotNil(t, restartState)
			resumeCursor := laggingInterleaved.cursorRepo.GetByAddress(laggingSolanaAddress)
			require.NotNil(t, resumeCursor)
			laggingInterleaved = newAutoTuneHarnessWithWarmStartAndHeadSeries(
				model.ChainSolana,
				model.NetworkDevnet,
				laggingSolanaAddress,
				resumeCursor.CursorSequence,
				laggingHeads[i:],
				activeLaggingCfg,
				restartState,
			)
		}

		laggingJob := laggingInterleaved.tickAndAdvance(t)
		laggingSnapshots = append(laggingSnapshots, snapshotFromFetchJob(laggingJob))

		baseJob := baseInterleaved.tickAndAdvance(t)
		baseSnapshots = append(baseSnapshots, snapshotFromFetchJob(baseJob))
		baseBatches = append(baseBatches, baseJob.BatchSize)

		btcJob := btcInterleaved.tickAndAdvance(t)
		btcSnapshots = append(btcSnapshots, snapshotFromFetchJob(btcJob))
		btcBatches = append(btcBatches, btcJob.BatchSize)
	}

	assert.Equal(t, baseBaselineSnapshots, baseSnapshots, "solana rollback checkpoint-fence tombstone-expiry transition must not alter base canonical tuples")
	assert.Equal(t, baseBaselineBatches, baseBatches, "solana rollback checkpoint-fence tombstone-expiry transition must not alter base control decisions")
	assert.Equal(t, btcBaselineSnapshots, btcSnapshots, "solana rollback checkpoint-fence tombstone-expiry transition must not alter btc canonical tuples")
	assert.Equal(t, btcBaselineBatches, btcBatches, "solana rollback checkpoint-fence tombstone-expiry transition must not alter btc control decisions")

	assert.Equal(t, laggingBaselineSnapshots, laggingSnapshots, "lagging rollback checkpoint-fence tombstone-expiry replay/resume must preserve canonical tuples")
	assertNoDuplicateOrMissingLogicalSnapshots(t, laggingBaselineSnapshots, laggingSnapshots, "lagging tombstone-retained baseline vs lagging checkpoint-fence tombstone-expiry replay")

	assertCursorMonotonicByAddress(t, baseSnapshots)
	assertCursorMonotonicByAddress(t, btcSnapshots)
	assertCursorMonotonicByAddress(t, laggingSnapshots)
}

func TestTick_AutoTunePolicyManifestRollbackCheckpointFencePostQuarantineReleaseWindowPermutationsConvergeAcrossMandatoryChains(t *testing.T) {
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
			address: "7nYBpkEPkDD6m1JKBGwvftG7bHjJErJPjTH3VbKexp55",
		},
		{
			name:    "base-sepolia",
			chain:   model.ChainBase,
			network: model.NetworkSepolia,
			address: "0xabcdefabcdefabcdefabcdefabcdefabcdefff55",
		},
		{
			name:    "btc-testnet",
			chain:   model.ChainBTC,
			network: model.NetworkTestnet,
			address: "tb1qmanifestrollbacklatemarker00000000000",
		},
	}

	segment1Cfg := AutoTuneConfig{
		Enabled:                    true,
		MinBatchSize:               60,
		MaxBatchSize:               360,
		StepUp:                     20,
		StepDown:                   10,
		LagHighWatermark:           80,
		LagLowWatermark:            20,
		QueueHighWatermarkPct:      90,
		QueueLowWatermarkPct:       10,
		HysteresisTicks:            1,
		CooldownTicks:              1,
		PolicyVersion:              "policy-v2",
		PolicyManifestDigest:       "manifest-tail-v2a",
		PolicyManifestRefreshEpoch: 1,
		PolicyActivationHoldTicks:  2,
	}
	segment2Cfg := segment1Cfg
	segment2Cfg.PolicyManifestDigest = "manifest-tail-v2b"
	segment2Cfg.PolicyManifestRefreshEpoch = 2
	segment3Cfg := segment1Cfg
	segment3Cfg.PolicyManifestDigest = "manifest-tail-v2c"
	segment3Cfg.PolicyManifestRefreshEpoch = 3
	rollbackCfg := segment2Cfg
	rollbackCfg.PolicyManifestDigest = "manifest-tail-v2b|rollback-from-seq=3|rollback-to-seq=2|rollback-forward-seq=3"
	compactionCfg := rollbackCfg
	compactionCfg.PolicyManifestDigest = rollbackCfg.PolicyManifestDigest + "|rollback-fence-tombstone=1"
	expiryCfg := rollbackCfg
	expiryCfg.PolicyManifestDigest = rollbackCfg.PolicyManifestDigest + "|rollback-fence-tombstone-expiry-epoch=4"
	quarantineCfg := expiryCfg
	quarantineCfg.PolicyManifestDigest = expiryCfg.PolicyManifestDigest + "|rollback-fence-late-marker-hold-epoch=5"
	releaseCfg := quarantineCfg
	releaseCfg.PolicyManifestDigest = quarantineCfg.PolicyManifestDigest + "|rollback-fence-late-marker-release-epoch=6"
	releaseWindowCfg := quarantineCfg
	releaseWindowCfg.PolicyManifestDigest = quarantineCfg.PolicyManifestDigest + "|rollback-fence-late-marker-release-epoch=7"

	releaseOnlyBaselineSchedule := map[int]AutoTuneConfig{
		2:  segment2Cfg,
		4:  segment3Cfg,
		6:  rollbackCfg,
		8:  compactionCfg,
		10: expiryCfg,
		11: quarantineCfg,
		12: releaseCfg,
		13: segment3Cfg,
		14: segment3Cfg,
		15: segment3Cfg,
	}
	staggeredReleaseWindowSchedule := map[int]AutoTuneConfig{
		2:  segment2Cfg,
		4:  segment3Cfg,
		6:  rollbackCfg,
		8:  compactionCfg,
		10: expiryCfg,
		11: quarantineCfg,
		12: releaseCfg,
		13: releaseWindowCfg,
		14: segment3Cfg,
		15: segment3Cfg,
	}
	rollbackReforwardAfterReleaseWindowSchedule := map[int]AutoTuneConfig{
		2:  segment2Cfg,
		4:  segment3Cfg,
		6:  rollbackCfg,
		8:  compactionCfg,
		10: expiryCfg,
		11: quarantineCfg,
		12: releaseCfg,
		13: releaseWindowCfg,
		14: rollbackCfg,
		15: segment3Cfg,
	}

	heads := []int64{260, 261, 262, 263, 264, 265, 266, 267, 268, 269, 270, 271, 272, 273, 274, 275}
	permutations := []struct {
		name                  string
		policySchedule        map[int]AutoTuneConfig
		staleFenceCaptureTick map[int]struct{}
		crashpoints           []autoTuneCheckpointFenceCrashpoint
		assertControlParity   bool
	}{
		{
			name:                "staggered-release-window",
			policySchedule:      staggeredReleaseWindowSchedule,
			assertControlParity: false,
		},
		{
			name:                  "crash-during-release-restart",
			policySchedule:        staggeredReleaseWindowSchedule,
			staleFenceCaptureTick: map[int]struct{}{12: {}},
			crashpoints: []autoTuneCheckpointFenceCrashpoint{
				{Tick: 13, UseStaleFenceState: true},
			},
			assertControlParity: false,
		},
		{
			name:                "rollback-reforward-after-release-window",
			policySchedule:      rollbackReforwardAfterReleaseWindowSchedule,
			assertControlParity: false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			baselineSnapshots, baselineBatches := runAutoTuneTraceWithPolicyScheduleAndCheckpointFenceCrashpoints(
				t,
				tc.chain,
				tc.network,
				tc.address,
				100,
				heads,
				segment1Cfg,
				releaseOnlyBaselineSchedule,
				nil,
				nil,
			)

			for _, permutation := range permutations {
				permutation := permutation
				t.Run(permutation.name, func(t *testing.T) {
					candidateSnapshots, candidateBatches := runAutoTuneTraceWithPolicyScheduleAndCheckpointFenceCrashpoints(
						t,
						tc.chain,
						tc.network,
						tc.address,
						100,
						heads,
						segment1Cfg,
						permutation.policySchedule,
						permutation.staleFenceCaptureTick,
						permutation.crashpoints,
					)

					assert.Equal(t, baselineSnapshots, candidateSnapshots, "rollback checkpoint-fence post-quarantine release-window permutations must converge to deterministic canonical tuples")
					if permutation.assertControlParity {
						assert.Equal(t, baselineBatches, candidateBatches, "rollback checkpoint-fence post-quarantine release-window permutations must preserve deterministic control decisions")
					}
					assertNoDuplicateOrMissingLogicalSnapshots(t, baselineSnapshots, candidateSnapshots, "rollback checkpoint-fence post-quarantine release-window baseline vs candidate")
					assertCursorMonotonicByAddress(t, candidateSnapshots)
				})
			}
		})
	}
}

func TestTick_AutoTunePolicyManifestRollbackCheckpointFencePostReleaseWindowEpochRolloverPermutationsConvergeAcrossMandatoryChains(t *testing.T) {
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
			address: "7nYBpkEPkDD6m1JKBGwvftG7bHjJErJPjTH3VbKexp57",
		},
		{
			name:    "base-sepolia",
			chain:   model.ChainBase,
			network: model.NetworkSepolia,
			address: "0xabcdefabcdefabcdefabcdefabcdefabcdefff57",
		},
		{
			name:    "btc-testnet",
			chain:   model.ChainBTC,
			network: model.NetworkTestnet,
			address: "tb1qmanifestrollover000000000000000000000",
		},
	}

	segment1Cfg := AutoTuneConfig{
		Enabled:                    true,
		MinBatchSize:               60,
		MaxBatchSize:               360,
		StepUp:                     20,
		StepDown:                   10,
		LagHighWatermark:           80,
		LagLowWatermark:            20,
		QueueHighWatermarkPct:      90,
		QueueLowWatermarkPct:       10,
		HysteresisTicks:            1,
		CooldownTicks:              1,
		PolicyVersion:              "policy-v2",
		PolicyManifestDigest:       "manifest-tail-v2a",
		PolicyManifestRefreshEpoch: 1,
		PolicyActivationHoldTicks:  2,
	}
	segment2Cfg := segment1Cfg
	segment2Cfg.PolicyManifestDigest = "manifest-tail-v2b"
	segment2Cfg.PolicyManifestRefreshEpoch = 2
	segment3Cfg := segment1Cfg
	segment3Cfg.PolicyManifestDigest = "manifest-tail-v2c"
	segment3Cfg.PolicyManifestRefreshEpoch = 3
	rollbackCfg := segment2Cfg
	rollbackCfg.PolicyManifestDigest = "manifest-tail-v2b|rollback-from-seq=3|rollback-to-seq=2|rollback-forward-seq=3"
	compactionCfg := rollbackCfg
	compactionCfg.PolicyManifestDigest = rollbackCfg.PolicyManifestDigest + "|rollback-fence-tombstone=1"
	expiryCfg := rollbackCfg
	expiryCfg.PolicyManifestDigest = rollbackCfg.PolicyManifestDigest + "|rollback-fence-tombstone-expiry-epoch=4"
	quarantineCfg := expiryCfg
	quarantineCfg.PolicyManifestDigest = expiryCfg.PolicyManifestDigest + "|rollback-fence-late-marker-hold-epoch=5"
	releaseCfg := quarantineCfg
	releaseCfg.PolicyManifestDigest = quarantineCfg.PolicyManifestDigest + "|rollback-fence-late-marker-release-epoch=6"
	releaseWindowCfg := quarantineCfg
	releaseWindowCfg.PolicyManifestDigest = quarantineCfg.PolicyManifestDigest + "|rollback-fence-late-marker-release-epoch=7"
	staleRollbackAtRolloverCfg := rollbackCfg
	staleRollbackAtRolloverCfg.PolicyManifestRefreshEpoch = 3

	releaseOnlyRolloverBaselineSchedule := map[int]AutoTuneConfig{
		2:  segment2Cfg,
		4:  segment3Cfg,
		6:  rollbackCfg,
		8:  compactionCfg,
		10: expiryCfg,
		11: quarantineCfg,
		12: releaseCfg,
		13: segment3Cfg,
		14: segment3Cfg,
		15: segment3Cfg,
		16: segment3Cfg,
	}
	rolloverAdoptionSchedule := map[int]AutoTuneConfig{
		2:  segment2Cfg,
		4:  segment3Cfg,
		6:  rollbackCfg,
		8:  compactionCfg,
		10: expiryCfg,
		11: quarantineCfg,
		12: releaseCfg,
		13: releaseWindowCfg,
		14: segment3Cfg,
		15: segment3Cfg,
		16: segment3Cfg,
	}
	rollbackReforwardAfterRolloverSchedule := map[int]AutoTuneConfig{
		2:  segment2Cfg,
		4:  segment3Cfg,
		6:  rollbackCfg,
		8:  compactionCfg,
		10: expiryCfg,
		11: quarantineCfg,
		12: releaseCfg,
		13: releaseWindowCfg,
		14: staleRollbackAtRolloverCfg,
		15: segment3Cfg,
		16: segment3Cfg,
	}

	heads := []int64{260, 261, 262, 263, 264, 265, 266, 267, 268, 269, 270, 271, 272, 273, 274, 275, 276}
	permutations := []struct {
		name                  string
		policySchedule        map[int]AutoTuneConfig
		staleFenceCaptureTick map[int]struct{}
		crashpoints           []autoTuneCheckpointFenceCrashpoint
		assertControlParity   bool
	}{
		{
			name:                "epoch-rollover-adoption",
			policySchedule:      rolloverAdoptionSchedule,
			assertControlParity: false,
		},
		{
			name:           "crash-during-rollover-restart",
			policySchedule: rolloverAdoptionSchedule,
			staleFenceCaptureTick: map[int]struct{}{
				13: {},
			},
			crashpoints: []autoTuneCheckpointFenceCrashpoint{
				{Tick: 14, UseStaleFenceState: true},
			},
			assertControlParity: false,
		},
		{
			name:                "rollback-reforward-after-rollover",
			policySchedule:      rollbackReforwardAfterRolloverSchedule,
			assertControlParity: false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			baselineSnapshots, baselineBatches := runAutoTuneTraceWithPolicyScheduleAndCheckpointFenceCrashpoints(
				t,
				tc.chain,
				tc.network,
				tc.address,
				100,
				heads,
				segment1Cfg,
				releaseOnlyRolloverBaselineSchedule,
				nil,
				nil,
			)

			for _, permutation := range permutations {
				permutation := permutation
				t.Run(permutation.name, func(t *testing.T) {
					candidateSnapshots, candidateBatches := runAutoTuneTraceWithPolicyScheduleAndCheckpointFenceCrashpoints(
						t,
						tc.chain,
						tc.network,
						tc.address,
						100,
						heads,
						segment1Cfg,
						permutation.policySchedule,
						permutation.staleFenceCaptureTick,
						permutation.crashpoints,
					)

					assert.Equal(t, baselineSnapshots, candidateSnapshots, "rollback checkpoint-fence post-release-window epoch-rollover permutations must converge to deterministic canonical tuples")
					if permutation.assertControlParity {
						assert.Equal(t, baselineBatches, candidateBatches, "rollback checkpoint-fence post-release-window epoch-rollover permutations must preserve deterministic control decisions")
					}
					assertNoDuplicateOrMissingLogicalSnapshots(t, baselineSnapshots, candidateSnapshots, "rollback checkpoint-fence post-release-window epoch-rollover baseline vs candidate")
					assertCursorMonotonicByAddress(t, candidateSnapshots)
				})
			}
		})
	}
}

func TestTick_AutoTuneOneChainPolicyManifestRollbackCheckpointFencePostQuarantineReleaseWindowDoesNotBleedAcrossOtherMandatoryChains(t *testing.T) {
	segment1Cfg := AutoTuneConfig{
		Enabled:                    true,
		MinBatchSize:               60,
		MaxBatchSize:               360,
		StepUp:                     20,
		StepDown:                   10,
		LagHighWatermark:           80,
		LagLowWatermark:            20,
		QueueHighWatermarkPct:      90,
		QueueLowWatermarkPct:       10,
		HysteresisTicks:            1,
		CooldownTicks:              1,
		PolicyVersion:              "policy-v2",
		PolicyManifestDigest:       "manifest-tail-v2a",
		PolicyManifestRefreshEpoch: 1,
		PolicyActivationHoldTicks:  2,
	}
	segment2Cfg := segment1Cfg
	segment2Cfg.PolicyManifestDigest = "manifest-tail-v2b"
	segment2Cfg.PolicyManifestRefreshEpoch = 2
	segment3Cfg := segment1Cfg
	segment3Cfg.PolicyManifestDigest = "manifest-tail-v2c"
	segment3Cfg.PolicyManifestRefreshEpoch = 3
	rollbackCfg := segment2Cfg
	rollbackCfg.PolicyManifestDigest = "manifest-tail-v2b|rollback-from-seq=3|rollback-to-seq=2|rollback-forward-seq=3"
	compactionCfg := rollbackCfg
	compactionCfg.PolicyManifestDigest = rollbackCfg.PolicyManifestDigest + "|rollback-fence-tombstone=1"
	expiryCfg := rollbackCfg
	expiryCfg.PolicyManifestDigest = rollbackCfg.PolicyManifestDigest + "|rollback-fence-tombstone-expiry-epoch=4"
	quarantineCfg := expiryCfg
	quarantineCfg.PolicyManifestDigest = expiryCfg.PolicyManifestDigest + "|rollback-fence-late-marker-hold-epoch=5"
	releaseCfg := quarantineCfg
	releaseCfg.PolicyManifestDigest = quarantineCfg.PolicyManifestDigest + "|rollback-fence-late-marker-release-epoch=6"
	releaseWindowCfg := quarantineCfg
	releaseWindowCfg.PolicyManifestDigest = quarantineCfg.PolicyManifestDigest + "|rollback-fence-late-marker-release-epoch=7"

	releaseOnlyBaselineSchedule := map[int]AutoTuneConfig{
		2:  segment2Cfg,
		4:  segment3Cfg,
		6:  rollbackCfg,
		8:  compactionCfg,
		10: expiryCfg,
		11: quarantineCfg,
		12: releaseCfg,
		13: segment3Cfg,
		14: segment3Cfg,
		15: segment3Cfg,
		16: segment3Cfg,
	}
	releaseWindowReplaySchedule := map[int]AutoTuneConfig{
		2:  segment2Cfg,
		4:  segment3Cfg,
		6:  rollbackCfg,
		8:  compactionCfg,
		10: expiryCfg,
		11: quarantineCfg,
		12: releaseCfg,
		13: releaseWindowCfg,
		14: releaseCfg,
		15: rollbackCfg,
		16: segment3Cfg,
	}
	const tickCount = 17
	healthyBaseAddress := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbee55"
	healthyBTCAddress := "tb1qmanifestlatemarkerhealthy000000000000"
	laggingSolanaAddress := "7nYBpkEPkDD6m1JKBGwvftG7bHjJErJPjTH3VbKexp56"

	healthyHeads := []int64{130, 131, 132, 133, 134, 135, 136, 137, 138, 139, 140, 141, 142, 143, 144, 145, 146}
	laggingHeads := []int64{260, 261, 262, 263, 264, 265, 266, 267, 268, 269, 270, 271, 272, 273, 274, 275, 276}

	baseBaseline := newAutoTuneHarnessWithHeadSeries(
		model.ChainBase,
		model.NetworkSepolia,
		healthyBaseAddress,
		120,
		healthyHeads,
		segment1Cfg,
	)
	baseBaselineSnapshots, baseBaselineBatches := collectAutoTuneTrace(t, baseBaseline, tickCount)

	btcBaseline := newAutoTuneHarnessWithHeadSeries(
		model.ChainBTC,
		model.NetworkTestnet,
		healthyBTCAddress,
		120,
		healthyHeads,
		segment1Cfg,
	)
	btcBaselineSnapshots, btcBaselineBatches := collectAutoTuneTrace(t, btcBaseline, tickCount)

	laggingBaselineSnapshots, _ := runAutoTuneTraceWithPolicyScheduleAndCheckpointFenceCrashpoints(
		t,
		model.ChainSolana,
		model.NetworkDevnet,
		laggingSolanaAddress,
		100,
		laggingHeads,
		segment1Cfg,
		releaseOnlyBaselineSchedule,
		nil,
		nil,
	)

	baseInterleaved := newAutoTuneHarnessWithHeadSeries(
		model.ChainBase,
		model.NetworkSepolia,
		healthyBaseAddress,
		120,
		healthyHeads,
		segment1Cfg,
	)
	btcInterleaved := newAutoTuneHarnessWithHeadSeries(
		model.ChainBTC,
		model.NetworkTestnet,
		healthyBTCAddress,
		120,
		healthyHeads,
		segment1Cfg,
	)
	laggingInterleaved := newAutoTuneHarnessWithHeadSeries(
		model.ChainSolana,
		model.NetworkDevnet,
		laggingSolanaAddress,
		100,
		laggingHeads,
		segment1Cfg,
	)

	baseSnapshots := make([]lagAwareJobSnapshot, 0, tickCount)
	baseBatches := make([]int, 0, tickCount)
	btcSnapshots := make([]lagAwareJobSnapshot, 0, tickCount)
	btcBatches := make([]int, 0, tickCount)
	laggingSnapshots := make([]lagAwareJobSnapshot, 0, tickCount)

	activeLaggingCfg := segment1Cfg
	staleFenceCaptureTicks := map[int]struct{}{12: {}}
	crashpoints := map[int]bool{13: true}
	var latestStaleFenceState *AutoTuneRestartState

	for i := 0; i < tickCount; i++ {
		if cfg, ok := releaseWindowReplaySchedule[i]; ok {
			activeLaggingCfg = cfg
			laggingInterleaved.coordinator.WithAutoTune(cfg)
			if _, capture := staleFenceCaptureTicks[i]; capture {
				latestStaleFenceState = cloneAutoTuneRestartState(laggingInterleaved.coordinator.ExportAutoTuneRestartState())
				require.NotNil(t, latestStaleFenceState)
			}
			if i == 14 {
				state := laggingInterleaved.coordinator.ExportAutoTuneRestartState()
				require.NotNil(t, state)
				assert.Equal(t, releaseWindowCfg.PolicyManifestDigest, state.PolicyManifestDigest, "stale release-window marker must remain pinned to latest released ownership")
				assert.Equal(t, releaseWindowCfg.PolicyManifestRefreshEpoch, state.PolicyEpoch)
			}
			if i == 15 {
				state := laggingInterleaved.coordinator.ExportAutoTuneRestartState()
				require.NotNil(t, state)
				assert.Equal(t, releaseWindowCfg.PolicyManifestDigest, state.PolicyManifestDigest, "stale post-release rollback marker must remain pinned to latest release-window ownership")
				assert.Equal(t, releaseWindowCfg.PolicyManifestRefreshEpoch, state.PolicyEpoch)
			}
		}

		if useStaleFence, ok := crashpoints[i]; ok {
			var restartState *AutoTuneRestartState
			if useStaleFence {
				require.NotNil(t, latestStaleFenceState, "stale release crashpoint requires captured pre-release-window state")
				restartState = cloneAutoTuneRestartState(latestStaleFenceState)
			} else {
				restartState = laggingInterleaved.coordinator.ExportAutoTuneRestartState()
			}
			require.NotNil(t, restartState)
			resumeCursor := laggingInterleaved.cursorRepo.GetByAddress(laggingSolanaAddress)
			require.NotNil(t, resumeCursor)
			laggingInterleaved = newAutoTuneHarnessWithWarmStartAndHeadSeries(
				model.ChainSolana,
				model.NetworkDevnet,
				laggingSolanaAddress,
				resumeCursor.CursorSequence,
				laggingHeads[i:],
				activeLaggingCfg,
				restartState,
			)
		}

		laggingJob := laggingInterleaved.tickAndAdvance(t)
		laggingSnapshots = append(laggingSnapshots, snapshotFromFetchJob(laggingJob))

		baseJob := baseInterleaved.tickAndAdvance(t)
		baseSnapshots = append(baseSnapshots, snapshotFromFetchJob(baseJob))
		baseBatches = append(baseBatches, baseJob.BatchSize)

		btcJob := btcInterleaved.tickAndAdvance(t)
		btcSnapshots = append(btcSnapshots, snapshotFromFetchJob(btcJob))
		btcBatches = append(btcBatches, btcJob.BatchSize)
	}

	assert.Equal(t, baseBaselineSnapshots, baseSnapshots, "solana rollback checkpoint-fence post-quarantine release-window transition must not alter base canonical tuples")
	assert.Equal(t, baseBaselineBatches, baseBatches, "solana rollback checkpoint-fence post-quarantine release-window transition must not alter base control decisions")
	assert.Equal(t, btcBaselineSnapshots, btcSnapshots, "solana rollback checkpoint-fence post-quarantine release-window transition must not alter btc canonical tuples")
	assert.Equal(t, btcBaselineBatches, btcBatches, "solana rollback checkpoint-fence post-quarantine release-window transition must not alter btc control decisions")

	assert.Equal(t, laggingBaselineSnapshots, laggingSnapshots, "lagging rollback checkpoint-fence post-quarantine release-window replay/resume must preserve canonical tuples")
	assertNoDuplicateOrMissingLogicalSnapshots(t, laggingBaselineSnapshots, laggingSnapshots, "lagging release-only baseline vs lagging post-quarantine release-window replay")

	assertCursorMonotonicByAddress(t, baseSnapshots)
	assertCursorMonotonicByAddress(t, btcSnapshots)
	assertCursorMonotonicByAddress(t, laggingSnapshots)
}

func TestTick_AutoTuneOneChainPolicyManifestTransitionDoesNotBleedControlAcrossOtherMandatoryChains(t *testing.T) {
	manifestV2aCfg := AutoTuneConfig{
		Enabled:                    true,
		MinBatchSize:               60,
		MaxBatchSize:               320,
		StepUp:                     20,
		StepDown:                   10,
		LagHighWatermark:           80,
		LagLowWatermark:            20,
		QueueHighWatermarkPct:      90,
		QueueLowWatermarkPct:       10,
		HysteresisTicks:            1,
		CooldownTicks:              1,
		PolicyVersion:              "policy-v2",
		PolicyManifestDigest:       "manifest-v2a",
		PolicyManifestRefreshEpoch: 1,
		PolicyActivationHoldTicks:  1,
	}
	manifestV2bCfg := manifestV2aCfg
	manifestV2bCfg.PolicyManifestDigest = "manifest-v2b"
	manifestV2bCfg.PolicyManifestRefreshEpoch = 2
	staleManifestCfg := manifestV2aCfg

	const tickCount = 9
	healthyBaseAddress := "0x1111111111111111111111111111111111111111"
	healthyBTCAddress := "tb1qmanifesthealthy000000000000000000000000"
	laggingSolanaAddress := "7nYBpkEPkDD6m1JKBGwvftG7bHjJErJPjTH3VbKpump"

	healthyHeads := []int64{130, 131, 132, 133, 134, 135, 136, 137, 138}
	laggingHeads := []int64{260, 261, 262, 263, 264, 265, 266, 267, 268}

	baseBaseline := newAutoTuneHarnessWithHeadSeries(
		model.ChainBase,
		model.NetworkSepolia,
		healthyBaseAddress,
		120,
		healthyHeads,
		manifestV2aCfg,
	)
	baseBaselineSnapshots, baseBaselineBatches := collectAutoTuneTrace(t, baseBaseline, tickCount)

	btcBaseline := newAutoTuneHarnessWithHeadSeries(
		model.ChainBTC,
		model.NetworkTestnet,
		healthyBTCAddress,
		120,
		healthyHeads,
		manifestV2aCfg,
	)
	btcBaselineSnapshots, btcBaselineBatches := collectAutoTuneTrace(t, btcBaseline, tickCount)

	laggingBaseline := newAutoTuneHarnessWithHeadSeries(
		model.ChainSolana,
		model.NetworkDevnet,
		laggingSolanaAddress,
		100,
		laggingHeads,
		manifestV2aCfg,
	)
	laggingBaselineSnapshots, laggingBaselineBatches := collectAutoTuneTrace(t, laggingBaseline, tickCount)

	baseInterleaved := newAutoTuneHarnessWithHeadSeries(
		model.ChainBase,
		model.NetworkSepolia,
		healthyBaseAddress,
		120,
		healthyHeads,
		manifestV2aCfg,
	)
	btcInterleaved := newAutoTuneHarnessWithHeadSeries(
		model.ChainBTC,
		model.NetworkTestnet,
		healthyBTCAddress,
		120,
		healthyHeads,
		manifestV2aCfg,
	)
	laggingInterleaved := newAutoTuneHarnessWithHeadSeries(
		model.ChainSolana,
		model.NetworkDevnet,
		laggingSolanaAddress,
		100,
		laggingHeads,
		manifestV2aCfg,
	)

	baseSnapshots := make([]lagAwareJobSnapshot, 0, tickCount)
	baseBatches := make([]int, 0, tickCount)
	btcSnapshots := make([]lagAwareJobSnapshot, 0, tickCount)
	btcBatches := make([]int, 0, tickCount)
	laggingSnapshots := make([]lagAwareJobSnapshot, 0, tickCount)
	laggingBatches := make([]int, 0, tickCount)

	for i := 0; i < tickCount; i++ {
		if i == 2 {
			laggingInterleaved.coordinator.WithAutoTune(manifestV2bCfg)
		}
		if i == 5 {
			laggingInterleaved.coordinator.WithAutoTune(staleManifestCfg)
			state := laggingInterleaved.coordinator.ExportAutoTuneRestartState()
			require.NotNil(t, state)
			assert.Equal(t, manifestV2bCfg.PolicyManifestDigest, state.PolicyManifestDigest, "stale one-chain refresh must remain pinned to verified digest")
			assert.Equal(t, manifestV2bCfg.PolicyManifestRefreshEpoch, state.PolicyEpoch, "stale one-chain refresh must preserve verified epoch")
		}
		if i == 7 {
			laggingInterleaved.coordinator.WithAutoTune(manifestV2bCfg)
			state := laggingInterleaved.coordinator.ExportAutoTuneRestartState()
			require.NotNil(t, state)
			assert.Equal(t, manifestV2bCfg.PolicyManifestDigest, state.PolicyManifestDigest)
			assert.Equal(t, manifestV2bCfg.PolicyManifestRefreshEpoch, state.PolicyEpoch)
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

	assert.Equal(t, baseBaselineSnapshots, baseSnapshots, "solana manifest transition must not alter base canonical tuples")
	assert.Equal(t, baseBaselineBatches, baseBatches, "solana manifest transition must not alter base control decisions")
	assert.Equal(t, btcBaselineSnapshots, btcSnapshots, "solana manifest transition must not alter btc canonical tuples")
	assert.Equal(t, btcBaselineBatches, btcBatches, "solana manifest transition must not alter btc control decisions")
	assert.Equal(t, laggingBaselineSnapshots, laggingSnapshots, "manifest transition must preserve lagging-chain canonical tuples")
	assert.NotEqual(t, laggingBaselineBatches, laggingBatches, "manifest transition should remain chain-scoped and alter only lagging-chain control decisions")
	assert.Equal(t, laggingBatches[1], laggingBatches[2], "manifest refresh boundary hold should be chain-scoped to lagging chain")
	assert.NotEqual(t, laggingBatches[4], laggingBatches[5], "stale manifest refresh reject should not add lagging-chain hold")
	assert.NotEqual(t, laggingBatches[6], laggingBatches[7], "manifest digest re-apply should not add lagging-chain hold")
	assertNoDuplicateOrMissingLogicalSnapshots(t, baseBaselineSnapshots, baseSnapshots, "base baseline vs interleaved manifest transition")
	assertNoDuplicateOrMissingLogicalSnapshots(t, btcBaselineSnapshots, btcSnapshots, "btc baseline vs interleaved manifest transition")
	assertNoDuplicateOrMissingLogicalSnapshots(t, laggingBaselineSnapshots, laggingSnapshots, "lagging baseline vs interleaved manifest transition")

	assertCursorMonotonicByAddress(t, baseSnapshots)
	assertCursorMonotonicByAddress(t, btcSnapshots)
	assertCursorMonotonicByAddress(t, laggingSnapshots)
}

func TestTick_AutoTunePolicyManifestReplayResumeConvergesAcrossMandatoryChains(t *testing.T) {
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
			address: "tb1qmanifestreplay000000000000000000000000",
		},
	}

	manifestV2aCfg := AutoTuneConfig{
		Enabled:                    true,
		MinBatchSize:               60,
		MaxBatchSize:               320,
		StepUp:                     20,
		StepDown:                   10,
		LagHighWatermark:           80,
		LagLowWatermark:            20,
		QueueHighWatermarkPct:      90,
		QueueLowWatermarkPct:       10,
		HysteresisTicks:            1,
		CooldownTicks:              1,
		PolicyVersion:              "policy-v2",
		PolicyManifestDigest:       "manifest-v2a",
		PolicyManifestRefreshEpoch: 1,
		PolicyActivationHoldTicks:  2,
	}
	manifestV2bCfg := manifestV2aCfg
	manifestV2bCfg.PolicyManifestDigest = "manifest-v2b"
	manifestV2bCfg.PolicyManifestRefreshEpoch = 2
	staleManifestCfg := manifestV2aCfg

	heads := []int64{260, 261, 262, 263, 264, 265, 266, 267, 268, 269}
	const splitTick = 3

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			coldHarness := newAutoTuneHarnessWithHeadSeries(tc.chain, tc.network, tc.address, 100, heads, manifestV2aCfg)
			coldSnapshots := make([]lagAwareJobSnapshot, 0, len(heads))
			coldBatches := make([]int, 0, len(heads))
			for i := 0; i < len(heads); i++ {
				if i == 2 {
					coldHarness.coordinator.WithAutoTune(manifestV2bCfg)
				}
				if i == 4 {
					coldHarness.coordinator.WithAutoTune(staleManifestCfg)
				}
				if i == 6 {
					coldHarness.coordinator.WithAutoTune(manifestV2bCfg)
				}
				job := coldHarness.tickAndAdvance(t)
				coldSnapshots = append(coldSnapshots, snapshotFromFetchJob(job))
				coldBatches = append(coldBatches, job.BatchSize)
			}

			warmFirst := newAutoTuneHarnessWithHeadSeries(tc.chain, tc.network, tc.address, 100, heads[:splitTick], manifestV2aCfg)
			warmSnapshots := make([]lagAwareJobSnapshot, 0, len(heads))
			warmBatches := make([]int, 0, len(heads))
			for i := 0; i < splitTick; i++ {
				if i == 2 {
					warmFirst.coordinator.WithAutoTune(manifestV2bCfg)
				}
				job := warmFirst.tickAndAdvance(t)
				warmSnapshots = append(warmSnapshots, snapshotFromFetchJob(job))
				warmBatches = append(warmBatches, job.BatchSize)
			}

			restartState := warmFirst.coordinator.ExportAutoTuneRestartState()
			require.NotNil(t, restartState)
			assert.Equal(t, manifestV2bCfg.PolicyVersion, restartState.PolicyVersion)
			assert.Equal(t, manifestV2bCfg.PolicyManifestDigest, restartState.PolicyManifestDigest)
			assert.Equal(t, manifestV2bCfg.PolicyManifestRefreshEpoch, restartState.PolicyEpoch)
			assert.Greater(t, restartState.PolicyActivationRemaining, 0, "restart state must preserve policy-manifest activation hold countdown at refresh boundary")
			resumeCursor := warmFirst.cursorRepo.GetByAddress(tc.address)
			require.NotNil(t, resumeCursor)

			warmSecond := newAutoTuneHarnessWithWarmStartAndHeadSeries(
				tc.chain,
				tc.network,
				tc.address,
				resumeCursor.CursorSequence,
				heads[splitTick:],
				manifestV2bCfg,
				restartState,
			)

			for i := splitTick; i < len(heads); i++ {
				if i == 4 {
					warmSecond.coordinator.WithAutoTune(staleManifestCfg)
					state := warmSecond.coordinator.ExportAutoTuneRestartState()
					require.NotNil(t, state)
					assert.Equal(t, manifestV2bCfg.PolicyManifestDigest, state.PolicyManifestDigest, "replay stale refresh must be rejected deterministically")
					assert.Equal(t, manifestV2bCfg.PolicyManifestRefreshEpoch, state.PolicyEpoch)
				}
				if i == 6 {
					warmSecond.coordinator.WithAutoTune(manifestV2bCfg)
					state := warmSecond.coordinator.ExportAutoTuneRestartState()
					require.NotNil(t, state)
					assert.Equal(t, manifestV2bCfg.PolicyManifestDigest, state.PolicyManifestDigest)
					assert.Equal(t, manifestV2bCfg.PolicyManifestRefreshEpoch, state.PolicyEpoch)
				}
				job := warmSecond.tickAndAdvance(t)
				warmSnapshots = append(warmSnapshots, snapshotFromFetchJob(job))
				warmBatches = append(warmBatches, job.BatchSize)
			}

			assert.Equal(t, coldSnapshots, warmSnapshots, "policy-manifest replay/resume must converge to deterministic canonical tuples")
			assert.Equal(t, coldBatches, warmBatches, "policy-manifest replay/resume must preserve deterministic control decisions")
			assertNoDuplicateOrMissingLogicalSnapshots(t, coldSnapshots, warmSnapshots, "cold vs warm policy-manifest replay")
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

func cloneAutoTuneRestartState(state *AutoTuneRestartState) *AutoTuneRestartState {
	if state == nil {
		return nil
	}
	cloned := *state
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

func runAutoTuneTraceWithPolicyScheduleAndCrashpoints(
	t *testing.T,
	chain model.Chain,
	network model.Network,
	address string,
	initialSequence int64,
	heads []int64,
	initialCfg AutoTuneConfig,
	policySchedule map[int]AutoTuneConfig,
	crashTicks []int,
) ([]lagAwareJobSnapshot, []int) {
	t.Helper()

	for i, crashTick := range crashTicks {
		if crashTick <= 0 || crashTick >= len(heads) {
			t.Fatalf("crash tick must be within (0,%d), got %d", len(heads), crashTick)
		}
		if i > 0 && crashTick <= crashTicks[i-1] {
			t.Fatalf("crash ticks must be strictly increasing, got %v", crashTicks)
		}
	}

	harness := newAutoTuneHarnessWithHeadSeries(
		chain,
		network,
		address,
		initialSequence,
		heads,
		initialCfg,
	)
	activeCfg := initialCfg

	snapshots := make([]lagAwareJobSnapshot, 0, len(heads))
	batches := make([]int, 0, len(heads))

	crashIndex := 0
	for i := 0; i < len(heads); i++ {
		if crashIndex < len(crashTicks) && i == crashTicks[crashIndex] {
			restartState := harness.coordinator.ExportAutoTuneRestartState()
			require.NotNil(t, restartState)
			resumeCursor := harness.cursorRepo.GetByAddress(address)
			require.NotNil(t, resumeCursor)
			harness = newAutoTuneHarnessWithWarmStartAndHeadSeries(
				chain,
				network,
				address,
				resumeCursor.CursorSequence,
				heads[i:],
				activeCfg,
				restartState,
			)
			crashIndex++
		}
		if cfg, ok := policySchedule[i]; ok {
			activeCfg = cfg
			harness.coordinator.WithAutoTune(cfg)
		}

		job := harness.tickAndAdvance(t)
		snapshots = append(snapshots, snapshotFromFetchJob(job))
		batches = append(batches, job.BatchSize)
	}

	return snapshots, batches
}

type autoTuneCheckpointFenceCrashpoint struct {
	Tick               int
	UseStaleFenceState bool
}

func runAutoTuneTraceWithPolicyScheduleAndCheckpointFenceCrashpoints(
	t *testing.T,
	chain model.Chain,
	network model.Network,
	address string,
	initialSequence int64,
	heads []int64,
	initialCfg AutoTuneConfig,
	policySchedule map[int]AutoTuneConfig,
	staleFenceCaptureTicks map[int]struct{},
	crashpoints []autoTuneCheckpointFenceCrashpoint,
) ([]lagAwareJobSnapshot, []int) {
	t.Helper()

	for i, crashpoint := range crashpoints {
		if crashpoint.Tick <= 0 || crashpoint.Tick >= len(heads) {
			t.Fatalf("checkpoint-fence crash tick must be within (0,%d), got %d", len(heads), crashpoint.Tick)
		}
		if i > 0 && crashpoint.Tick <= crashpoints[i-1].Tick {
			t.Fatalf("checkpoint-fence crash ticks must be strictly increasing, got %+v", crashpoints)
		}
	}

	harness := newAutoTuneHarnessWithHeadSeries(
		chain,
		network,
		address,
		initialSequence,
		heads,
		initialCfg,
	)
	activeCfg := initialCfg
	var latestStaleFenceState *AutoTuneRestartState

	snapshots := make([]lagAwareJobSnapshot, 0, len(heads))
	batches := make([]int, 0, len(heads))

	crashIndex := 0
	for i := 0; i < len(heads); i++ {
		if cfg, ok := policySchedule[i]; ok {
			activeCfg = cfg
			harness.coordinator.WithAutoTune(cfg)
			if _, capture := staleFenceCaptureTicks[i]; capture {
				latestStaleFenceState = cloneAutoTuneRestartState(harness.coordinator.ExportAutoTuneRestartState())
				require.NotNil(t, latestStaleFenceState)
			}
		}

		if crashIndex < len(crashpoints) && i == crashpoints[crashIndex].Tick {
			var restartState *AutoTuneRestartState
			if crashpoints[crashIndex].UseStaleFenceState {
				require.NotNil(t, latestStaleFenceState, "stale fence crashpoint requires captured pre-flush restart state")
				restartState = cloneAutoTuneRestartState(latestStaleFenceState)
			} else {
				restartState = harness.coordinator.ExportAutoTuneRestartState()
				require.NotNil(t, restartState)
			}

			resumeCursor := harness.cursorRepo.GetByAddress(address)
			require.NotNil(t, resumeCursor)
			harness = newAutoTuneHarnessWithWarmStartAndHeadSeries(
				chain,
				network,
				address,
				resumeCursor.CursorSequence,
				heads[i:],
				activeCfg,
				restartState,
			)
			crashIndex++
		}

		job := harness.tickAndAdvance(t)
		snapshots = append(snapshots, snapshotFromFetchJob(job))
		batches = append(batches, job.BatchSize)
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
