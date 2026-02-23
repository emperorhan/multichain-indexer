package coordinator

import (
	"testing"

	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/coordinator/autotune"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

func TestTick_AutoTuneSignalPerturbationIsChainScopedByRuntimePair(t *testing.T) {
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

	const tickCount = 6
	healthyAddress := "0x2222222222222222222222222222222222222222"

	baselineRegistry := autotune.NewRuntimeSignalRegistry()
	baselineHarness := newAutoTuneHarness(
		model.ChainBase,
		model.NetworkSepolia,
		healthyAddress,
		120,
		130,
		tickCount,
		autoTuneCfg,
	)
	baselineHarness.coordinator.WithAutoTuneSignalSource(baselineRegistry)

	perturbedRegistry := autotune.NewRuntimeSignalRegistry()
	perturbedHarness := newAutoTuneHarness(
		model.ChainBase,
		model.NetworkSepolia,
		healthyAddress,
		120,
		130,
		tickCount,
		autoTuneCfg,
	)
	perturbedHarness.coordinator.WithAutoTuneSignalSource(perturbedRegistry)

	for i := 0; i < tickCount; i++ {
		perturbedRegistry.RecordRPCResult(model.ChainSolana.String(), model.NetworkDevnet.String(), true)
		perturbedRegistry.RecordDBCommitLatencyMs(model.ChainSolana.String(), model.NetworkDevnet.String(), int64(500*(i+1)))
	}

	baselineBatches := make([]int, 0, tickCount)
	perturbedBatches := make([]int, 0, tickCount)
	for i := 0; i < tickCount; i++ {
		baselineBatches = append(baselineBatches, baselineHarness.tickAndAdvance(t).BatchSize)
		perturbedBatches = append(perturbedBatches, perturbedHarness.tickAndAdvance(t).BatchSize)
	}

	assert.Equal(t, baselineBatches, perturbedBatches, "peer-chain runtime perturbations must not alter chain-local auto-tune outputs")
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

			warmSecond := newAutoTuneHarnessWithWarmStart(
				tc.chain,
				tc.network,
				tc.address,
				warmFirst.configRepo.watermark,
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

			laggingRestarted = newAutoTuneHarnessWithWarmStart(
				model.ChainSolana,
				model.NetworkDevnet,
				laggingAddress,
				laggingRestarted.configRepo.watermark,
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

func TestTick_TopologyABCOneChainRestartReplayIsolationAcrossMandatoryChains_NoCrossChainControlOrCursorBleed(t *testing.T) {
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

	topologyModes := []struct {
		name       string
		crashTicks []int
	}{
		{name: "A", crashTicks: nil},
		{name: "B", crashTicks: []int{3}},
		{name: "C", crashTicks: []int{2, 4}},
	}

	const tickCount = 6
	healthyBaseAddress := "0x1111111111111111111111111111111111111111"
	healthyBTCAddress := "tb1qtopologymatrixpeer0000000000000000000000"
	laggingSolanaAddress := "7nYBpkEPkDD6m1JKBGwvftG7bHjJErJPjTH3VbKtopology"

	healthyHeads := []int64{130, 131, 132, 133, 134, 135}
	laggingHeads := []int64{2000, 2001, 2002, 2003, 2004, 2005}

	baseBaselineHarness := newAutoTuneHarnessWithHeadSeries(
		model.ChainBase,
		model.NetworkSepolia,
		healthyBaseAddress,
		120,
		healthyHeads,
		autoTuneCfg,
	)
	baseBaselineSnapshots, baseBaselineBatches := collectAutoTuneTrace(t, baseBaselineHarness, tickCount)

	btcBaselineHarness := newAutoTuneHarnessWithHeadSeries(
		model.ChainBTC,
		model.NetworkTestnet,
		healthyBTCAddress,
		120,
		healthyHeads,
		autoTuneCfg,
	)
	btcBaselineSnapshots, btcBaselineBatches := collectAutoTuneTrace(t, btcBaselineHarness, tickCount)

	for _, topology := range topologyModes {
		topology := topology
		t.Run("topology_"+topology.name, func(t *testing.T) {
			laggingBaselineSnapshots, laggingBaselineBatches := runAutoTuneTraceWithPolicyScheduleAndCrashpoints(
				t,
				model.ChainSolana,
				model.NetworkDevnet,
				laggingSolanaAddress,
				100,
				laggingHeads,
				autoTuneCfg,
				nil,
				topology.crashTicks,
			)

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

			crashByTick := make(map[int]struct{}, len(topology.crashTicks))
			for _, tick := range topology.crashTicks {
				crashByTick[tick] = struct{}{}
			}

			baseSnapshots := make([]lagAwareJobSnapshot, 0, tickCount)
			baseBatches := make([]int, 0, tickCount)
			btcSnapshots := make([]lagAwareJobSnapshot, 0, tickCount)
			btcBatches := make([]int, 0, tickCount)
			laggingSnapshots := make([]lagAwareJobSnapshot, 0, tickCount)
			laggingBatches := make([]int, 0, tickCount)

			for i := 0; i < tickCount; i++ {
				if _, restart := crashByTick[i]; restart {
					restartState := laggingInterleaved.coordinator.ExportAutoTuneRestartState()
					require.NotNil(t, restartState)
					laggingInterleaved = newAutoTuneHarnessWithWarmStartAndHeadSeries(
						model.ChainSolana,
						model.NetworkDevnet,
						laggingSolanaAddress,
						laggingInterleaved.configRepo.watermark,
						laggingHeads[i:],
						autoTuneCfg,
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

			assert.Equal(t, baseBaselineSnapshots, baseSnapshots, "topology %s lagging-chain restart/replay must not bleed cursor progression into base", topology.name)
			assert.Equal(t, baseBaselineBatches, baseBatches, "topology %s lagging-chain restart/replay must not bleed control decisions into base", topology.name)
			assert.Equal(t, btcBaselineSnapshots, btcSnapshots, "topology %s lagging-chain restart/replay must not bleed cursor progression into btc", topology.name)
			assert.Equal(t, btcBaselineBatches, btcBatches, "topology %s lagging-chain restart/replay must not bleed control decisions into btc", topology.name)
			assert.Equal(t, laggingBaselineSnapshots, laggingSnapshots, "topology %s lagging-chain restart/replay must preserve lagging-chain canonical tuples", topology.name)
			assert.Equal(t, laggingBaselineBatches, laggingBatches, "topology %s lagging-chain restart/replay must preserve lagging-chain control decisions", topology.name)

			assertNoDuplicateOrMissingLogicalSnapshots(t, baseBaselineSnapshots, baseSnapshots, "topology "+topology.name+" base baseline vs interleaved")
			assertNoDuplicateOrMissingLogicalSnapshots(t, btcBaselineSnapshots, btcSnapshots, "topology "+topology.name+" btc baseline vs interleaved")
			assertNoDuplicateOrMissingLogicalSnapshots(t, laggingBaselineSnapshots, laggingSnapshots, "topology "+topology.name+" lagging baseline vs interleaved")

			assertCursorMonotonicByAddress(t, baseSnapshots)
			assertCursorMonotonicByAddress(t, btcSnapshots)
			assertCursorMonotonicByAddress(t, laggingSnapshots)
		})
	}
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

			warmSecond := newAutoTuneHarnessWithWarmStartAndHeadSeries(
				tc.chain,
				tc.network,
				tc.address,
				warmFirst.configRepo.watermark,
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
			// Use initialSequence=50 so that startBlock (51+) stays below the minimum
			// head in the stale series (95). In block-scan mode the coordinator skips
			// job creation when startBlock > head, and lowering the initial watermark
			// avoids that guard while preserving the telemetry-staleness signal.
			freshHarness := newAutoTuneHarnessWithHeadSeries(tc.chain, tc.network, tc.address, 50, freshHeads, autoTuneCfg)
			freshSnapshots, freshBatches := collectAutoTuneTrace(t, freshHarness, len(freshHeads))

			staleHarness := newAutoTuneHarnessWithHeadSeries(tc.chain, tc.network, tc.address, 50, staleHeads, autoTuneCfg)
			staleSnapshots, staleBatches := collectAutoTuneTrace(t, staleHarness, len(staleHeads))

			recoveryHarness := newAutoTuneHarnessWithHeadSeries(tc.chain, tc.network, tc.address, 50, recoveryHeads, autoTuneCfg)
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

	// Use initialSequence=50 so that startBlock (51+) stays below the minimum
	// head in the blackout series (95). In block-scan mode the coordinator skips
	// job creation when startBlock > head.
	baseBaseline := newAutoTuneHarnessWithHeadSeries(
		model.ChainBase,
		model.NetworkSepolia,
		healthyBaseAddress,
		50,
		healthyHeads,
		autoTuneCfg,
	)
	baseBaselineSnapshots, baseBaselineBatches := collectAutoTuneTrace(t, baseBaseline, tickCount)

	btcBaseline := newAutoTuneHarnessWithHeadSeries(
		model.ChainBTC,
		model.NetworkTestnet,
		healthyBTCAddress,
		50,
		healthyHeads,
		autoTuneCfg,
	)
	btcBaselineSnapshots, btcBaselineBatches := collectAutoTuneTrace(t, btcBaseline, tickCount)

	baseInterleaved := newAutoTuneHarnessWithHeadSeries(
		model.ChainBase,
		model.NetworkSepolia,
		healthyBaseAddress,
		50,
		healthyHeads,
		autoTuneCfg,
	)
	btcInterleaved := newAutoTuneHarnessWithHeadSeries(
		model.ChainBTC,
		model.NetworkTestnet,
		healthyBTCAddress,
		50,
		healthyHeads,
		autoTuneCfg,
	)
	laggingInterleaved := newAutoTuneHarnessWithHeadSeries(
		model.ChainSolana,
		model.NetworkDevnet,
		laggingSolanaAddress,
		50,
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
			// Use initialSequence=50 so that startBlock (51+) stays below the minimum
			// head in the series (95). In block-scan mode the coordinator skips
			// job creation when startBlock > head.
			coldHarness := newAutoTuneHarnessWithHeadSeries(tc.chain, tc.network, tc.address, 50, heads, autoTuneCfg)
			coldSnapshots, _ := collectAutoTuneTrace(t, coldHarness, len(heads))

			warmFirst := newAutoTuneHarnessWithHeadSeries(tc.chain, tc.network, tc.address, 50, heads[:splitTick], autoTuneCfg)
			warmSnapshots, _ := collectAutoTuneTrace(t, warmFirst, splitTick)
			restartState := warmFirst.coordinator.ExportAutoTuneRestartState()
			require.NotNil(t, restartState)

			warmSecond := newAutoTuneHarnessWithWarmStartAndHeadSeries(
				tc.chain,
				tc.network,
				tc.address,
				warmFirst.configRepo.watermark,
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

			warmSecond := newAutoTuneHarnessWithWarmStartAndHeadSeries(
				tc.chain,
				tc.network,
				tc.address,
				warmFirst.configRepo.watermark,
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

			warmSecond := newAutoTuneHarnessWithWarmStartAndHeadSeries(
				tc.chain,
				tc.network,
				tc.address,
				warmFirst.configRepo.watermark,
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
