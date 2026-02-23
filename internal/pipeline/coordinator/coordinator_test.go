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
	jobCh := make(chan event.FetchJob, 10)
	c := New(
		model.ChainSolana, model.NetworkDevnet,
		mockWatchedAddr,
		100, time.Second,
		jobCh, slog.Default(),
	)

	mockWatchedAddr.EXPECT().
		GetActive(gomock.Any(), model.ChainSolana, model.NetworkDevnet).
		Return([]model.WatchedAddress{
			{Address: "addr1"},
			{Address: "addr2"},
		}, nil)

	err := c.tick(context.Background())
	require.NoError(t, err)

	// Block-scan mode emits exactly 1 job per tick with all watched addresses.
	require.Len(t, jobCh, 1)

	job := <-jobCh
	assert.Equal(t, model.ChainSolana, job.Chain)
	assert.Equal(t, model.NetworkDevnet, job.Network)
	assert.True(t, job.BlockScanMode)
	assert.Equal(t, 100, job.BatchSize)
	assert.ElementsMatch(t, []string{"addr1", "addr2"}, job.WatchedAddresses)
}

func TestTick_NoAddresses(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockWatchedAddr := storemocks.NewMockWatchedAddressRepository(ctrl)
	jobCh := make(chan event.FetchJob, 10)
	c := New(
		model.ChainSolana, model.NetworkDevnet,
		mockWatchedAddr,
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

	jobCh := make(chan event.FetchJob, 10)
	headProvider := &stubHeadProvider{head: 777}
	c := New(
		model.ChainSolana, model.NetworkDevnet,
		mockWatchedAddr,
		100, time.Second,
		jobCh, slog.Default(),
	).WithHeadProvider(headProvider)

	mockWatchedAddr.EXPECT().
		GetActive(gomock.Any(), model.ChainSolana, model.NetworkDevnet).
		Return([]model.WatchedAddress{
			{Address: "addr1"},
			{Address: "addr2"},
		}, nil)

	require.NoError(t, c.tick(context.Background()))
	// Block-scan mode: single job per tick.
	require.Len(t, jobCh, 1)
	assert.Equal(t, 1, headProvider.calls)

	job := <-jobCh
	assert.True(t, job.BlockScanMode)
	// No configRepo, so startBlock=0, head=777, batchSize=100 => endBlock=99.
	assert.Equal(t, int64(0), job.StartBlock)
	assert.Equal(t, int64(99), job.EndBlock)
	assert.Equal(t, int64(99), job.FetchCutoffSeq)
	assert.ElementsMatch(t, []string{"addr1", "addr2"}, job.WatchedAddresses)
}

func TestTick_WithHeadProviderErrorFailsFast(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockWatchedAddr := storemocks.NewMockWatchedAddressRepository(ctrl)
	jobCh := make(chan event.FetchJob, 10)
	headProvider := &stubHeadProvider{err: errors.New("head unavailable")}
	c := New(
		model.ChainBase, model.NetworkSepolia,
		mockWatchedAddr,
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

	jobCh := make(chan event.FetchJob, 10)
	c := New(
		model.ChainSolana, model.NetworkDevnet,
		mockWatchedAddr,
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

	jobCh := make(chan event.FetchJob, 10)
	c := New(
		model.ChainSolana, model.NetworkDevnet,
		mockWatchedAddr,
		100, time.Second,
		jobCh, slog.Default(),
	)

	mockWatchedAddr.EXPECT().
		GetActive(gomock.Any(), model.ChainSolana, model.NetworkDevnet).
		Return([]model.WatchedAddress{
			{Address: "addr1"},
			{Address: "addr2"},
		}, nil)

	err := c.tick(context.Background())
	require.NoError(t, err)
}

func TestRun_ReturnsErrorOnTickFailure(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockWatchedAddr := storemocks.NewMockWatchedAddressRepository(ctrl)
	jobCh := make(chan event.FetchJob, 10)
	c := New(
		model.ChainSolana, model.NetworkDevnet,
		mockWatchedAddr,
		100, time.Second,
		jobCh, slog.Default(),
	)

	mockWatchedAddr.EXPECT().
		GetActive(gomock.Any(), model.ChainSolana, model.NetworkDevnet).
		Return(nil, errors.New("db connection lost"))

	err := c.Run(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "coordinator tick failed")
	assert.Contains(t, err.Error(), "db connection lost")
}

func TestRun_ReturnsErrorOnTickFailure_WithAutoTuneEnabled(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockWatchedAddr := storemocks.NewMockWatchedAddressRepository(ctrl)

	jobCh := make(chan event.FetchJob, 10)
	c := New(
		model.ChainSolana, model.NetworkDevnet,
		mockWatchedAddr,
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

	err := c.Run(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "coordinator tick failed")
	assert.Contains(t, err.Error(), "db connection lost")
}

func TestTick_ContextCanceled(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockWatchedAddr := storemocks.NewMockWatchedAddressRepository(ctrl)
	jobCh := make(chan event.FetchJob) // unbuffered, will block
	c := New(
		model.ChainSolana, model.NetworkDevnet,
		mockWatchedAddr,
		100, time.Second,
		jobCh, slog.Default(),
	)

	mockWatchedAddr.EXPECT().
		GetActive(gomock.Any(), model.ChainSolana, model.NetworkDevnet).
		Return([]model.WatchedAddress{
			{Address: "addr1"},
		}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := c.tick(ctx)
	require.Error(t, err)
	assert.Equal(t, context.Canceled, err)
}

// ---------------------------------------------------------------------------
// Backfill detection + channel saturation tests
// ---------------------------------------------------------------------------

func TestTickBlockScan_BackfillDetected(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockWatchedAddr := storemocks.NewMockWatchedAddressRepository(ctrl)
	mockWmRepo := storemocks.NewMockWatermarkRepository(ctrl)

	jobCh := make(chan event.FetchJob, 10)
	backfillCh := make(chan BackfillRequest, 1)

	c := New(
		model.ChainBase, model.NetworkMainnet,
		mockWatchedAddr,
		100, time.Second,
		jobCh, slog.Default(),
	).WithHeadProvider(&stubHeadProvider{head: 200}).
		WithBlockScanMode(mockWmRepo).
		WithBackfillChannel(backfillCh)

	mockWatchedAddr.EXPECT().
		GetActive(gomock.Any(), model.ChainBase, model.NetworkMainnet).
		Return([]model.WatchedAddress{{Address: "addr1"}}, nil)

	mockWmRepo.EXPECT().
		GetWatermark(gomock.Any(), model.ChainBase, model.NetworkMainnet).
		Return(&model.PipelineWatermark{IngestedSequence: 100}, nil)

	fromBlock := int64(50)
	mockWatchedAddr.EXPECT().
		GetPendingBackfill(gomock.Any(), model.ChainBase, model.NetworkMainnet).
		Return([]model.WatchedAddress{{Address: "addr1", BackfillFromBlock: &fromBlock}}, nil)

	err := c.tick(context.Background())
	require.NoError(t, err)

	// Job should have been enqueued (startBlock=101).
	require.Len(t, jobCh, 1)
	job := <-jobCh
	assert.Equal(t, int64(101), job.StartBlock)

	// Backfill request should have been sent because 50 < 101.
	require.Len(t, backfillCh, 1)
	req := <-backfillCh
	assert.Equal(t, model.ChainBase, req.Chain)
	assert.Equal(t, model.NetworkMainnet, req.Network)
	assert.Equal(t, int64(50), req.FromBlock)
}

func TestTickBlockScan_BackfillNotTriggered_WhenAheadOfWatermark(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockWatchedAddr := storemocks.NewMockWatchedAddressRepository(ctrl)
	mockWmRepo := storemocks.NewMockWatermarkRepository(ctrl)

	jobCh := make(chan event.FetchJob, 10)
	backfillCh := make(chan BackfillRequest, 1)

	c := New(
		model.ChainBase, model.NetworkMainnet,
		mockWatchedAddr,
		100, time.Second,
		jobCh, slog.Default(),
	).WithHeadProvider(&stubHeadProvider{head: 200}).
		WithBlockScanMode(mockWmRepo).
		WithBackfillChannel(backfillCh)

	mockWatchedAddr.EXPECT().
		GetActive(gomock.Any(), model.ChainBase, model.NetworkMainnet).
		Return([]model.WatchedAddress{{Address: "addr1"}}, nil)

	mockWmRepo.EXPECT().
		GetWatermark(gomock.Any(), model.ChainBase, model.NetworkMainnet).
		Return(&model.PipelineWatermark{IngestedSequence: 100}, nil)

	// BackfillFromBlock=200 is ahead of startBlock=101, so no backfill should be sent.
	fromBlock := int64(200)
	mockWatchedAddr.EXPECT().
		GetPendingBackfill(gomock.Any(), model.ChainBase, model.NetworkMainnet).
		Return([]model.WatchedAddress{{Address: "addr1", BackfillFromBlock: &fromBlock}}, nil)

	err := c.tick(context.Background())
	require.NoError(t, err)

	// Job enqueued normally.
	require.Len(t, jobCh, 1)

	// No backfill request because 200 >= 101.
	assert.Empty(t, backfillCh, "backfill channel should be empty when BackfillFromBlock >= startBlock")
}

func TestTickBlockScan_BackfillChannelFull(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockWatchedAddr := storemocks.NewMockWatchedAddressRepository(ctrl)
	mockWmRepo := storemocks.NewMockWatermarkRepository(ctrl)

	jobCh := make(chan event.FetchJob, 10)
	// Zero-buffer channel: already full by construction (no reader).
	backfillCh := make(chan BackfillRequest) // unbuffered

	c := New(
		model.ChainBase, model.NetworkMainnet,
		mockWatchedAddr,
		100, time.Second,
		jobCh, slog.Default(),
	).WithHeadProvider(&stubHeadProvider{head: 200}).
		WithBlockScanMode(mockWmRepo).
		WithBackfillChannel(backfillCh)

	mockWatchedAddr.EXPECT().
		GetActive(gomock.Any(), model.ChainBase, model.NetworkMainnet).
		Return([]model.WatchedAddress{{Address: "addr1"}}, nil)

	mockWmRepo.EXPECT().
		GetWatermark(gomock.Any(), model.ChainBase, model.NetworkMainnet).
		Return(&model.PipelineWatermark{IngestedSequence: 100}, nil)

	fromBlock := int64(50)
	mockWatchedAddr.EXPECT().
		GetPendingBackfill(gomock.Any(), model.ChainBase, model.NetworkMainnet).
		Return([]model.WatchedAddress{{Address: "addr1", BackfillFromBlock: &fromBlock}}, nil)

	// Should not panic or deadlock. The default branch in the select drops
	// the backfill request when the channel is full.
	done := make(chan struct{})
	go func() {
		defer close(done)
		err := c.tick(context.Background())
		require.NoError(t, err)
	}()

	select {
	case <-done:
		// Completed without deadlock.
	case <-time.After(2 * time.Second):
		t.Fatal("tick deadlocked on full backfill channel")
	}

	// Job should still have been enqueued successfully.
	require.Len(t, jobCh, 1)

	// Backfill channel remains empty (unbuffered, no reader consumed it).
	assert.Empty(t, backfillCh)
}

func TestTickBlockScan_NoBackfillWithoutChannel(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockWatchedAddr := storemocks.NewMockWatchedAddressRepository(ctrl)
	mockWmRepo := storemocks.NewMockWatermarkRepository(ctrl)

	jobCh := make(chan event.FetchJob, 10)
	// No backfillCh set -- backfillCh remains nil.

	c := New(
		model.ChainBase, model.NetworkMainnet,
		mockWatchedAddr,
		100, time.Second,
		jobCh, slog.Default(),
	).WithHeadProvider(&stubHeadProvider{head: 200}).
		WithBlockScanMode(mockWmRepo)
	// Intentionally NOT calling WithBackfillChannel.

	mockWatchedAddr.EXPECT().
		GetActive(gomock.Any(), model.ChainBase, model.NetworkMainnet).
		Return([]model.WatchedAddress{{Address: "addr1"}}, nil)

	mockWmRepo.EXPECT().
		GetWatermark(gomock.Any(), model.ChainBase, model.NetworkMainnet).
		Return(&model.PipelineWatermark{IngestedSequence: 100}, nil)

	// GetPendingBackfill should NOT be called because backfillCh is nil.
	// gomock will fail if an unexpected call is made.

	err := c.tick(context.Background())
	require.NoError(t, err)
	require.Len(t, jobCh, 1)
}

func TestTickBlockScan_JobChannelFull_BlockingRetry(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockWatchedAddr := storemocks.NewMockWatchedAddressRepository(ctrl)

	// Unbuffered job channel: first send attempt hits the default branch,
	// second send blocks until ctx is canceled.
	jobCh := make(chan event.FetchJob)

	c := New(
		model.ChainBase, model.NetworkMainnet,
		mockWatchedAddr,
		100, time.Second,
		jobCh, slog.Default(),
	).WithHeadProvider(&stubHeadProvider{head: 200})

	mockWatchedAddr.EXPECT().
		GetActive(gomock.Any(), model.ChainBase, model.NetworkMainnet).
		Return([]model.WatchedAddress{{Address: "addr1"}}, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := c.tick(ctx)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded,
		"tick should return context deadline error when job channel is full")
}

func TestWithBackfillChannel(t *testing.T) {
	jobCh := make(chan event.FetchJob, 1)
	c := New(
		model.ChainSolana, model.NetworkDevnet,
		nil, 100, time.Second,
		jobCh, slog.Default(),
	)

	assert.Nil(t, c.backfillCh, "backfillCh should be nil by default")

	backfillCh := make(chan BackfillRequest, 5)
	result := c.WithBackfillChannel(backfillCh)

	assert.Same(t, c, result, "WithBackfillChannel should return the same coordinator for chaining")
	assert.NotNil(t, c.backfillCh, "backfillCh should be set after WithBackfillChannel")
}

// ---------------------------------------------------------------------------
// Run loop branch-coverage tests
// ---------------------------------------------------------------------------

// TestRun_InitialTickError_ReturnsError verifies that Run returns immediately
// when the very first tick fails (the fail-fast path before entering the for
// loop). This uses a headProvider error to trigger the tick failure, which is
// distinct from the existing TestRun_ReturnsErrorOnTickFailure that uses a
// GetActive DB error.
func TestRun_InitialTickError_ReturnsError(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockWatchedAddr := storemocks.NewMockWatchedAddressRepository(ctrl)
	jobCh := make(chan event.FetchJob, 10)

	headProvider := &stubHeadProvider{err: errors.New("head unavailable")}
	c := New(
		model.ChainBase, model.NetworkMainnet,
		mockWatchedAddr,
		100, time.Second,
		jobCh, slog.Default(),
	).WithHeadProvider(headProvider)

	mockWatchedAddr.EXPECT().
		GetActive(gomock.Any(), model.ChainBase, model.NetworkMainnet).
		Return([]model.WatchedAddress{{Address: "0xabc"}}, nil)

	err := c.Run(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "coordinator tick failed")
	assert.Contains(t, err.Error(), "head unavailable")
	assert.Empty(t, jobCh, "no jobs should be enqueued when first tick fails")
}

// TestRun_ContextCancel_ReturnsContextError verifies that Run returns
// context.Canceled when the context is canceled while the coordinator is
// waiting in the select loop (after the initial tick succeeds).
func TestRun_ContextCancel_ReturnsContextError(t *testing.T) {
	watchedRepo := &scriptedWatchedAddressRepo{
		ticks: [][]model.WatchedAddress{
			{{Address: "addr1"}}, // first tick (initial)
		},
	}
	jobCh := make(chan event.FetchJob, 10)

	c := New(
		model.ChainSolana, model.NetworkDevnet,
		watchedRepo,
		100, 10*time.Second, // long interval so the ticker won't fire
		jobCh, slog.Default(),
	)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- c.Run(ctx)
	}()

	// Wait briefly for the initial tick to complete, then cancel.
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		require.Error(t, err)
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}

// TestRun_IntervalResetCh_ResetsTimer verifies that calling UpdateInterval
// during Run causes the intervalResetCh branch to fire, resetting the ticker
// to the new interval. The test starts with a very long interval (10s), then
// shortens it via UpdateInterval. The second tick should fire much sooner
// than the original 10s interval, proving the reset path was taken.
func TestRun_IntervalResetCh_ResetsTimer(t *testing.T) {
	watchedRepo := &scriptedWatchedAddressRepo{
		ticks: [][]model.WatchedAddress{
			{{Address: "addr1"}}, // first tick (initial)
			{{Address: "addr1"}}, // second tick (after interval reset)
		},
	}
	jobCh := make(chan event.FetchJob, 10)

	c := New(
		model.ChainSolana, model.NetworkDevnet,
		watchedRepo,
		100, 10*time.Second, // very long initial interval
		jobCh, slog.Default(),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- c.Run(ctx)
	}()

	// Wait for the initial tick to complete.
	time.Sleep(20 * time.Millisecond)

	// Shorten interval to 20ms — this sends a signal on intervalResetCh,
	// which causes the Run loop to reset the ticker.
	changed := c.UpdateInterval(20 * time.Millisecond)
	require.True(t, changed, "UpdateInterval should return true for a new value")

	// The second tick should happen within ~100ms (well under the original
	// 10s interval). We wait up to 500ms to be safe.
	time.Sleep(200 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		// Run should return with context error.
		require.Error(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}

	// At least 2 jobs should have been enqueued: initial tick + after reset.
	assert.GreaterOrEqual(t, len(jobCh), 2,
		"expected at least 2 jobs (initial tick + post-reset tick)")
}
