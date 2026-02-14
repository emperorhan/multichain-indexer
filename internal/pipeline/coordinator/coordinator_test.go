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

func TestTick_CursorGetError_SkipsAddress(t *testing.T) {
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

	mockCursor.EXPECT().
		Get(gomock.Any(), model.ChainSolana, model.NetworkDevnet, "addr2").
		Return(nil, nil)

	err := c.tick(context.Background())
	require.NoError(t, err)

	// Only addr2 should have a job
	require.Len(t, jobCh, 1)
	job := <-jobCh
	assert.Equal(t, "addr2", job.Address)
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
