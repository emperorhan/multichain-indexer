package coordinator

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

type lagAwareJobSnapshot struct {
	Address        string
	CursorValue    string
	CursorSequence int64
}

type autoTuneHarness struct {
	coordinator *Coordinator
	configRepo  *inMemoryConfigRepo
	jobCh       chan event.FetchJob
}

// inMemoryConfigRepo implements store.WatermarkRepository for tests.
// It provides a mutable watermark for block-scan mode.
type inMemoryConfigRepo struct {
	watermark int64
}

func (r *inMemoryConfigRepo) UpdateWatermarkTx(context.Context, *sql.Tx, model.Chain, model.Network, int64) error {
	return nil
}

func (r *inMemoryConfigRepo) RewindWatermarkTx(context.Context, *sql.Tx, model.Chain, model.Network, int64) error {
	return nil
}

func (r *inMemoryConfigRepo) GetWatermark(_ context.Context, _ model.Chain, _ model.Network) (*model.PipelineWatermark, error) {
	if r.watermark <= 0 {
		return nil, nil
	}
	return &model.PipelineWatermark{
		IngestedSequence: r.watermark,
	}, nil
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

func (*scriptedWatchedAddressRepo) GetPendingBackfill(context.Context, model.Chain, model.Network) ([]model.WatchedAddress, error) {
	return nil, nil
}

func (*scriptedWatchedAddressRepo) ClearBackfill(context.Context, model.Chain, model.Network) error {
	return nil
}

func (*scriptedWatchedAddressRepo) SetBackfillFromBlock(context.Context, model.Chain, model.Network, []string, int64) error {
	return nil
}

func cloneAutoTuneRestartState(state *AutoTuneRestartState) *AutoTuneRestartState {
	if state == nil {
		return nil
	}
	cloned := *state
	return &cloned
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
			harness = newAutoTuneHarnessWithWarmStartAndHeadSeries(
				chain,
				network,
				address,
				harness.configRepo.watermark,
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

			harness = newAutoTuneHarnessWithWarmStartAndHeadSeries(
				chain,
				network,
				address,
				harness.configRepo.watermark,
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
	// Block-scan mode requires a configRepo for watermark reads.
	configRepo := &inMemoryConfigRepo{watermark: initialSequence}
	jobCh := make(chan event.FetchJob, tickCount+1)
	coord := New(chain, network, watchedRepo, 100, time.Second, jobCh, slog.Default()).
		WithHeadProvider(&stubHeadProvider{head: headSequence}).
		WithBlockScanMode(configRepo)
	if warmState != nil {
		coord = coord.WithAutoTuneWarmStart(autoTuneCfg, warmState)
	} else {
		coord = coord.WithAutoTune(autoTuneCfg)
	}

	return &autoTuneHarness{
		coordinator: coord,
		configRepo:  configRepo,
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

	// In block-scan mode, advance the watermark to StartBlock (net +1 per tick).
	// Since startBlock = watermark + 1, setting watermark = startBlock advances
	// the watermark by exactly 1 per tick, which mirrors the old per-address
	// cursor advancement pattern and ensures auto-tune batch size changes do not
	// affect the canonical watermark progression.
	if h.configRepo != nil && job.BlockScanMode {
		h.configRepo.watermark = job.StartBlock
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
	if job.BlockScanMode {
		addr := job.Address
		if addr == "" && len(job.WatchedAddresses) > 0 {
			addr = job.WatchedAddresses[0]
		}
		return lagAwareJobSnapshot{
			Address:        addr,
			CursorValue:    "",
			CursorSequence: job.StartBlock,
		}
	}
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
