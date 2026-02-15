package coordinator

import (
	"context"
	"errors"
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
	solCursorB := "sig-sol-12"

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
				Address:        baseLower,
				CursorValue:    strPtr("0xabcdef"),
				CursorSequence: 12,
				BatchSize:      100,
				WalletID:       &walletB,
				OrgID:          &orgB,
			},
		},
		{
			name:    "solana-devnet-duplicate-row-fanin",
			chain:   model.ChainSolana,
			network: model.NetworkDevnet,
			addresses: []model.WatchedAddress{
				{Address: solanaAddr, WalletID: &walletA, OrganizationID: &orgA},
				{Address: solanaAddr, WalletID: &walletB, OrganizationID: &orgB},
			},
			cursorByKey: map[string]*model.AddressCursor{
				solanaAddr: {
					Address:        solanaAddr,
					CursorValue:    &solCursorB,
					CursorSequence: 12,
				},
			},
			expectedJob: event.FetchJob{
				Chain:          model.ChainSolana,
				Network:        model.NetworkDevnet,
				Address:        solanaAddr,
				CursorValue:    &solCursorB,
				CursorSequence: 12,
				BatchSize:      100,
				WalletID:       &walletA,
				OrgID:          &orgA,
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

func strPtr(v string) *string {
	return &v
}
