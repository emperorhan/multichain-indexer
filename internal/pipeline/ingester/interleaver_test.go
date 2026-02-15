package ingester

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type triChainIngesters struct {
	solana *Ingester
	base   *Ingester
	btc    *Ingester
}

type triChainSnapshot struct {
	tupleKeys       map[string]struct{}
	eventIDs        map[string]struct{}
	eventIDCounts   map[string]int
	categoryCounts  map[string]int
	btcTotalDelta   string
	cursorSeq       map[string]int64
	watermarks      map[string]int64
	cursorWrites    map[string][]int64
	watermarkWrites map[string][]int64
	insertedCount   int
	upsertAttempts  int
}

func TestTriChainInterleaving_CompletionOrderPermutationsConvergeDeterministically(t *testing.T) {
	ctx := context.Background()
	maxSkew := 40 * time.Millisecond

	runPermutation := func(delays map[model.Chain]time.Duration) triChainSnapshot {
		state := newInterleaveState(nil)
		ings := newTriChainIngesters(t, state, maxSkew)
		errs := runTriChainPermutation(ctx, ings, delays)
		require.Empty(t, errs)
		return snapshotTriChainState(state)
	}

	permutationA := runPermutation(map[model.Chain]time.Duration{
		model.ChainSolana: 8 * time.Millisecond,
		model.ChainBase:   0,
		model.ChainBTC:    4 * time.Millisecond,
	})
	permutationB := runPermutation(map[model.Chain]time.Duration{
		model.ChainSolana: 0,
		model.ChainBase:   7 * time.Millisecond,
		model.ChainBTC:    2 * time.Millisecond,
	})

	assert.Equal(t, 0, tupleDiffCount(permutationA.tupleKeys, permutationB.tupleKeys))
	assert.Equal(t, 0, tupleDiffCount(permutationA.eventIDs, permutationB.eventIDs))
	assert.Equal(t, permutationA.categoryCounts, permutationB.categoryCounts)
	assert.Equal(t, permutationA.cursorSeq, permutationB.cursorSeq)
	assert.Equal(t, permutationA.watermarks, permutationB.watermarks)

	assert.Equal(t, 8, len(permutationA.eventIDs))
	assert.Equal(t, 1, permutationA.categoryCounts[chainCategoryKey(model.ChainSolana, model.EventCategoryFee)])
	assert.Equal(t, 1, permutationA.categoryCounts[chainCategoryKey(model.ChainBase, model.EventCategoryFeeExecutionL2)])
	assert.Equal(t, 1, permutationA.categoryCounts[chainCategoryKey(model.ChainBase, model.EventCategoryFeeDataL1)])
	assert.Equal(t, "-1", permutationA.btcTotalDelta)

	solKey := fmt.Sprintf("%s|%s", interleaveKey(model.ChainSolana, model.NetworkDevnet), triChainAddress(model.ChainSolana))
	baseKey := fmt.Sprintf("%s|%s", interleaveKey(model.ChainBase, model.NetworkSepolia), triChainAddress(model.ChainBase))
	btcKey := fmt.Sprintf("%s|%s", interleaveKey(model.ChainBTC, model.NetworkTestnet), triChainAddress(model.ChainBTC))
	assert.Equal(t, int64(101), permutationA.cursorSeq[solKey])
	assert.Equal(t, int64(201), permutationA.cursorSeq[baseKey])
	assert.Equal(t, int64(301), permutationA.cursorSeq[btcKey])
	assert.Equal(t, int64(101), permutationA.watermarks[interleaveKey(model.ChainSolana, model.NetworkDevnet)])
	assert.Equal(t, int64(201), permutationA.watermarks[interleaveKey(model.ChainBase, model.NetworkSepolia)])
	assert.Equal(t, int64(301), permutationA.watermarks[interleaveKey(model.ChainBTC, model.NetworkTestnet)])

	assertNoDuplicateEventIDs(t, permutationA.eventIDCounts)
	assertMonotonicWrites(t, permutationA.cursorWrites, "tri-chain permutationA cursor writes")
	assertMonotonicWrites(t, permutationA.watermarkWrites, "tri-chain permutationA watermark writes")
	assertMonotonicWrites(t, permutationB.cursorWrites, "tri-chain permutationB cursor writes")
	assertMonotonicWrites(t, permutationB.watermarkWrites, "tri-chain permutationB watermark writes")
}

func TestTriChainInterleaving_BacklogRetryPressurePreservesIsolationAndReplayDeterminism(t *testing.T) {
	ctx := context.Background()
	btcChainKey := interleaveKey(model.ChainBTC, model.NetworkTestnet)
	state := newInterleaveState(map[string]error{
		btcChainKey: errors.New("btc backlog transient failure"),
	})

	ings := newTriChainIngesters(t, state, 20*time.Millisecond)
	failFirstErrs := runTriChainPermutation(ctx, ings, map[model.Chain]time.Duration{
		model.ChainSolana: 3 * time.Millisecond,
		model.ChainBase:   1 * time.Millisecond,
		model.ChainBTC:    0,
	})
	require.Len(t, failFirstErrs, 1)
	assert.Contains(t, failFirstErrs[0].Error(), "btc backlog transient failure")

	first := snapshotTriChainState(state)
	assert.Equal(t, 5, len(first.eventIDs), "btc failure should not remove solana/base logical events")
	assert.Equal(t, 1, first.categoryCounts[chainCategoryKey(model.ChainSolana, model.EventCategoryFee)])
	assert.Equal(t, 1, first.categoryCounts[chainCategoryKey(model.ChainBase, model.EventCategoryFeeExecutionL2)])
	assert.Equal(t, 1, first.categoryCounts[chainCategoryKey(model.ChainBase, model.EventCategoryFeeDataL1)])

	btcCursorKey := fmt.Sprintf("%s|%s", btcChainKey, triChainAddress(model.ChainBTC))
	_, btcCursorWritten := first.cursorSeq[btcCursorKey]
	assert.False(t, btcCursorWritten, "btc failed-path must not advance cursor")
	_, btcWatermarkWritten := first.watermarks[btcChainKey]
	assert.False(t, btcWatermarkWritten, "btc failed-path must not advance watermark")

	replay := newTriChainIngesters(t, state, 20*time.Millisecond)
	replayErrs := runTriChainPermutation(ctx, replay, map[model.Chain]time.Duration{
		model.ChainSolana: 0,
		model.ChainBase:   4 * time.Millisecond,
		model.ChainBTC:    2 * time.Millisecond,
	})
	require.Empty(t, replayErrs)

	second := snapshotTriChainState(state)
	assert.Equal(t, 8, len(second.eventIDs))
	assertNoDuplicateEventIDs(t, second.eventIDCounts)
	for eventID := range first.eventIDs {
		_, exists := second.eventIDs[eventID]
		assert.True(t, exists, "replay lost pre-existing logical event %s", eventID)
	}

	assert.Equal(t, "-1", second.btcTotalDelta)
	assert.Equal(t, int64(101), second.watermarks[interleaveKey(model.ChainSolana, model.NetworkDevnet)])
	assert.Equal(t, int64(201), second.watermarks[interleaveKey(model.ChainBase, model.NetworkSepolia)])
	assert.Equal(t, int64(301), second.watermarks[interleaveKey(model.ChainBTC, model.NetworkTestnet)])

	assert.GreaterOrEqual(t, second.upsertAttempts, second.insertedCount)
	assertMonotonicWrites(t, second.cursorWrites, "tri-chain backlog cursor writes")
	assertMonotonicWrites(t, second.watermarkWrites, "tri-chain backlog watermark writes")
}

func TestTriChainInterleaving_LateArrivalPermutationsConvergeDeterministically(t *testing.T) {
	ctx := context.Background()
	maxSkew := 25 * time.Millisecond

	runOnTime := func() triChainSnapshot {
		state := newInterleaveState(nil)
		ings := newTriChainIngesters(t, state, maxSkew)

		errs := runTriChainBatches(ctx, ings, map[model.Chain]event.NormalizedBatch{
			model.ChainSolana: buildTriChainInterleaveBatch(model.ChainSolana),
			model.ChainBase:   buildTriChainInterleaveBatch(model.ChainBase),
			model.ChainBTC:    buildTriChainInterleaveBatch(model.ChainBTC),
		}, map[model.Chain]time.Duration{
			model.ChainSolana: 2 * time.Millisecond,
			model.ChainBase:   0,
			model.ChainBTC:    5 * time.Millisecond,
		})
		require.Empty(t, errs)

		errs = runTriChainBatches(ctx, ings, map[model.Chain]event.NormalizedBatch{
			model.ChainSolana: buildTriChainClosureBatch(model.ChainSolana, "sig-tri-sol-101", 101),
			model.ChainBase:   buildTriChainClosureBatch(model.ChainBase, "0xtri_base_201", 201),
			model.ChainBTC:    buildTriChainClosureBatch(model.ChainBTC, "btc-tri-301", 301),
		}, map[model.Chain]time.Duration{
			model.ChainSolana: 1 * time.Millisecond,
			model.ChainBase:   3 * time.Millisecond,
			model.ChainBTC:    0,
		})
		require.Empty(t, errs)

		return snapshotTriChainState(state)
	}

	runDelayed := func() triChainSnapshot {
		state := newInterleaveState(nil)
		ings := newTriChainIngesters(t, state, maxSkew)

		errs := runTriChainBatches(ctx, ings, map[model.Chain]event.NormalizedBatch{
			model.ChainSolana: buildTriChainInterleaveBatch(model.ChainSolana),
			model.ChainBase:   buildTriChainInterleaveBatch(model.ChainBase),
			model.ChainBTC:    buildTriChainClosureBatch(model.ChainBTC, "btc-tri-300", 300),
		}, map[model.Chain]time.Duration{
			model.ChainSolana: 4 * time.Millisecond,
			model.ChainBase:   0,
			model.ChainBTC:    1 * time.Millisecond,
		})
		require.Empty(t, errs)

		errs = runTriChainBatches(ctx, ings, map[model.Chain]event.NormalizedBatch{
			model.ChainSolana: buildTriChainInterleaveBatch(model.ChainSolana),
			model.ChainBase:   buildTriChainInterleaveBatch(model.ChainBase),
			model.ChainBTC:    buildTriChainClosureBatch(model.ChainBTC, "btc-tri-300", 300),
		}, map[model.Chain]time.Duration{
			model.ChainSolana: 0,
			model.ChainBase:   5 * time.Millisecond,
			model.ChainBTC:    2 * time.Millisecond,
		})
		require.Empty(t, errs)

		errs = runTriChainBatches(ctx, ings, map[model.Chain]event.NormalizedBatch{
			model.ChainSolana: buildTriChainClosureBatch(model.ChainSolana, "sig-tri-sol-101", 101),
			model.ChainBase:   buildTriChainClosureBatch(model.ChainBase, "0xtri_base_201", 201),
			model.ChainBTC:    buildTriChainInterleaveBatch(model.ChainBTC),
		}, map[model.Chain]time.Duration{
			model.ChainSolana: 1 * time.Millisecond,
			model.ChainBase:   0,
			model.ChainBTC:    6 * time.Millisecond,
		})
		require.Empty(t, errs)

		return snapshotTriChainState(state)
	}

	onTime := runOnTime()
	delayed := runDelayed()

	assert.Equal(t, onTime.tupleKeys, delayed.tupleKeys)
	assert.Equal(t, onTime.eventIDs, delayed.eventIDs)
	assert.Equal(t, onTime.categoryCounts, delayed.categoryCounts)
	assert.Equal(t, onTime.cursorSeq, delayed.cursorSeq)
	assert.Equal(t, onTime.watermarks, delayed.watermarks)
	assert.Equal(t, onTime.btcTotalDelta, delayed.btcTotalDelta)

	assert.Equal(t, 8, len(delayed.eventIDs))
	assert.Equal(t, 1, delayed.categoryCounts[chainCategoryKey(model.ChainSolana, model.EventCategoryFee)])
	assert.Equal(t, 1, delayed.categoryCounts[chainCategoryKey(model.ChainBase, model.EventCategoryFeeExecutionL2)])
	assert.Equal(t, 1, delayed.categoryCounts[chainCategoryKey(model.ChainBase, model.EventCategoryFeeDataL1)])
	assert.Equal(t, "-1", delayed.btcTotalDelta)

	assertNoDuplicateEventIDs(t, delayed.eventIDCounts)
	assertMonotonicWrites(t, delayed.cursorWrites, "tri-chain late-arrival delayed cursor writes")
	assertMonotonicWrites(t, delayed.watermarkWrites, "tri-chain late-arrival delayed watermark writes")
}

func TestTriChainInterleaving_OneChainDelayedClosurePressureStaysIsolated(t *testing.T) {
	ctx := context.Background()
	maxSkew := 25 * time.Millisecond

	baselineState := newInterleaveState(nil)
	baselineIngesters := newTriChainIngesters(t, baselineState, maxSkew)
	baselineErrs := runTriChainBatches(ctx, baselineIngesters, map[model.Chain]event.NormalizedBatch{
		model.ChainSolana: buildTriChainInterleaveBatch(model.ChainSolana),
		model.ChainBase:   buildTriChainInterleaveBatch(model.ChainBase),
		model.ChainBTC:    buildTriChainInterleaveBatch(model.ChainBTC),
	}, map[model.Chain]time.Duration{
		model.ChainSolana: 0,
		model.ChainBase:   3 * time.Millisecond,
		model.ChainBTC:    1 * time.Millisecond,
	})
	require.Empty(t, baselineErrs)
	baseline := snapshotTriChainState(baselineState)

	state := newInterleaveState(nil)
	ings := newTriChainIngesters(t, state, maxSkew)

	errs := runTriChainBatches(ctx, ings, map[model.Chain]event.NormalizedBatch{
		model.ChainSolana: buildTriChainInterleaveBatch(model.ChainSolana),
		model.ChainBase:   buildTriChainInterleaveBatch(model.ChainBase),
		model.ChainBTC:    buildTriChainClosureBatch(model.ChainBTC, "btc-tri-300", 300),
	}, map[model.Chain]time.Duration{
		model.ChainSolana: 2 * time.Millisecond,
		model.ChainBase:   0,
		model.ChainBTC:    1 * time.Millisecond,
	})
	require.Empty(t, errs)

	errs = runTriChainBatches(ctx, ings, map[model.Chain]event.NormalizedBatch{
		model.ChainSolana: buildTriChainInterleaveBatch(model.ChainSolana),
		model.ChainBase:   buildTriChainInterleaveBatch(model.ChainBase),
		model.ChainBTC:    buildTriChainClosureBatch(model.ChainBTC, "btc-tri-300", 300),
	}, map[model.Chain]time.Duration{
		model.ChainSolana: 0,
		model.ChainBase:   4 * time.Millisecond,
		model.ChainBTC:    2 * time.Millisecond,
	})
	require.Empty(t, errs)

	beforeLateArrival := snapshotTriChainState(state)
	assert.Equal(t, 5, len(beforeLateArrival.eventIDs))
	assert.Equal(t, chainEventIDSubset(baseline.eventIDs, model.ChainSolana), chainEventIDSubset(beforeLateArrival.eventIDs, model.ChainSolana))
	assert.Equal(t, chainEventIDSubset(baseline.eventIDs, model.ChainBase), chainEventIDSubset(beforeLateArrival.eventIDs, model.ChainBase))
	assert.Empty(t, chainEventIDSubset(beforeLateArrival.eventIDs, model.ChainBTC))

	errs = runTriChainBatches(ctx, ings, map[model.Chain]event.NormalizedBatch{
		model.ChainSolana: buildTriChainClosureBatch(model.ChainSolana, "sig-tri-sol-101", 101),
		model.ChainBase:   buildTriChainClosureBatch(model.ChainBase, "0xtri_base_201", 201),
		model.ChainBTC:    buildTriChainInterleaveBatch(model.ChainBTC),
	}, map[model.Chain]time.Duration{
		model.ChainSolana: 0,
		model.ChainBase:   2 * time.Millisecond,
		model.ChainBTC:    6 * time.Millisecond,
	})
	require.Empty(t, errs)

	afterLateArrival := snapshotTriChainState(state)
	assert.Equal(t, baseline.eventIDs, afterLateArrival.eventIDs)
	assert.Equal(t, baseline.tupleKeys, afterLateArrival.tupleKeys)
	assert.Equal(t, baseline.categoryCounts, afterLateArrival.categoryCounts)
	assert.Equal(t, baseline.cursorSeq, afterLateArrival.cursorSeq)
	assert.Equal(t, baseline.watermarks, afterLateArrival.watermarks)
	assert.Equal(t, chainEventIDSubset(beforeLateArrival.eventIDs, model.ChainSolana), chainEventIDSubset(afterLateArrival.eventIDs, model.ChainSolana))
	assert.Equal(t, chainEventIDSubset(beforeLateArrival.eventIDs, model.ChainBase), chainEventIDSubset(afterLateArrival.eventIDs, model.ChainBase))

	assertNoDuplicateEventIDs(t, afterLateArrival.eventIDCounts)
	assertMonotonicWrites(t, afterLateArrival.cursorWrites, "tri-chain one-chain-late cursor writes")
	assertMonotonicWrites(t, afterLateArrival.watermarkWrites, "tri-chain one-chain-late watermark writes")
}

func TestDeterministicMandatoryChainInterleaver_FailedAttemptDoesNotAdvanceCheckpoint(t *testing.T) {
	ctx := context.Background()

	interleaver, ok := NewDeterministicMandatoryChainInterleaver(10 * time.Millisecond).(*deterministicMandatoryChainInterleaver)
	require.True(t, ok)

	releaseSol, err := interleaver.Acquire(ctx, model.ChainSolana, model.NetworkDevnet)
	require.NoError(t, err)
	releaseSol(false)

	interleaver.mu.Lock()
	nextAfterFailedSolana := interleaver.next
	interleaver.mu.Unlock()
	assert.Equal(t, 0, nextAfterFailedSolana, "failed attempt must not advance checkpoint")

	releaseSol, err = interleaver.Acquire(ctx, model.ChainSolana, model.NetworkDevnet)
	require.NoError(t, err)
	releaseSol(true)

	interleaver.mu.Lock()
	nextAfterCommittedSolana := interleaver.next
	interleaver.mu.Unlock()
	assert.Equal(t, 1, nextAfterCommittedSolana)

	releaseBase, err := interleaver.Acquire(ctx, model.ChainBase, model.NetworkSepolia)
	require.NoError(t, err)
	releaseBase(true)

	interleaver.mu.Lock()
	nextAfterCommittedBase := interleaver.next
	interleaver.mu.Unlock()
	assert.Equal(t, 2, nextAfterCommittedBase)

	releaseBTC, err := interleaver.Acquire(ctx, model.ChainBTC, model.NetworkTestnet)
	require.NoError(t, err)
	releaseBTC(true)

	interleaver.mu.Lock()
	nextAfterCommittedBTC := interleaver.next
	interleaver.mu.Unlock()
	assert.Equal(t, 0, nextAfterCommittedBTC)
}

func TestDeterministicMandatoryChainInterleaver_NoOpClosureDoesNotAdvanceCheckpoint(t *testing.T) {
	ctx := context.Background()

	state := newInterleaveState(nil)
	ings := newTriChainIngesters(t, state, 10*time.Millisecond)

	interleaver, ok := ings.solana.commitInterleaver.(*deterministicMandatoryChainInterleaver)
	require.True(t, ok)

	require.NoError(t, ings.solana.processBatch(ctx, buildTriChainClosureBatch(model.ChainSolana, "sig-tri-sol-100", 100)))

	interleaver.mu.Lock()
	nextAfterNoOpClosure := interleaver.next
	interleaver.mu.Unlock()
	assert.Equal(t, 0, nextAfterNoOpClosure, "closure no-op must not advance checkpoint")

	require.NoError(t, ings.solana.processBatch(ctx, buildTriChainInterleaveBatch(model.ChainSolana)))

	interleaver.mu.Lock()
	nextAfterMaterialCommit := interleaver.next
	interleaver.mu.Unlock()
	assert.Equal(t, 1, nextAfterMaterialCommit, "material commit should advance checkpoint")
}

func newTriChainIngesters(t *testing.T, state *interleaveState, maxSkew time.Duration) triChainIngesters {
	t.Helper()

	db := openFakeDB()
	t.Cleanup(func() {
		_ = db.Close()
	})

	txBeginner := &interleaveTxBeginner{db: db}
	txRepo := &interleaveTxRepo{state: state}
	beRepo := &interleaveBalanceEventRepo{state: state}
	balanceRepo := &interleaveBalanceRepo{state: state}
	tokenRepo := &interleaveTokenRepo{state: state}
	cursorRepo := &interleaveCursorRepo{state: state}
	configRepo := &interleaveConfigRepo{state: state}
	interleaver := NewDeterministicMandatoryChainInterleaver(maxSkew)

	newIngester := func() *Ingester {
		return New(
			txBeginner,
			txRepo,
			beRepo,
			balanceRepo,
			tokenRepo,
			cursorRepo,
			configRepo,
			nil,
			slog.Default(),
			WithCommitInterleaver(interleaver),
		)
	}

	return triChainIngesters{
		solana: newIngester(),
		base:   newIngester(),
		btc:    newIngester(),
	}
}

func runTriChainPermutation(ctx context.Context, ings triChainIngesters, delays map[model.Chain]time.Duration) []error {
	chains := []model.Chain{model.ChainSolana, model.ChainBase, model.ChainBTC}
	errCh := make(chan error, len(chains))
	var wg sync.WaitGroup

	for _, chainID := range chains {
		chainID := chainID
		wg.Add(1)
		go func() {
			defer wg.Done()
			time.Sleep(delays[chainID])
			errCh <- ingesterForChain(ings, chainID).processBatch(ctx, buildTriChainInterleaveBatch(chainID))
		}()
	}

	wg.Wait()
	close(errCh)

	errs := make([]error, 0, len(chains))
	for err := range errCh {
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

func runTriChainBatches(
	ctx context.Context,
	ings triChainIngesters,
	batches map[model.Chain]event.NormalizedBatch,
	delays map[model.Chain]time.Duration,
) []error {
	chains := []model.Chain{model.ChainSolana, model.ChainBase, model.ChainBTC}
	errCh := make(chan error, len(chains))
	var wg sync.WaitGroup

	for _, chainID := range chains {
		batch, exists := batches[chainID]
		if !exists {
			continue
		}
		delay := delays[chainID]
		chainID := chainID
		wg.Add(1)
		go func() {
			defer wg.Done()
			time.Sleep(delay)
			errCh <- ingesterForChain(ings, chainID).processBatch(ctx, batch)
		}()
	}

	wg.Wait()
	close(errCh)

	errs := make([]error, 0, len(chains))
	for err := range errCh {
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

func buildTriChainInterleaveBatch(chainID model.Chain) event.NormalizedBatch {
	switch chainID {
	case model.ChainSolana:
		address := triChainAddress(model.ChainSolana)
		txHash := "sig-tri-sol-101"
		cursor := txHash
		return event.NormalizedBatch{
			Chain:             model.ChainSolana,
			Network:           model.NetworkDevnet,
			Address:           address,
			NewCursorValue:    &cursor,
			NewCursorSequence: 101,
			Transactions: []event.NormalizedTransaction{
				{
					TxHash:      txHash,
					BlockCursor: 101,
					FeeAmount:   "2",
					FeePayer:    address,
					Status:      model.TxStatusSuccess,
					BalanceEvents: []event.NormalizedBalanceEvent{
						{
							EventCategory:       model.EventCategoryTransfer,
							EventAction:         "system_transfer",
							ProgramID:           "11111111111111111111111111111111",
							ContractAddress:     "11111111111111111111111111111111",
							Address:             address,
							CounterpartyAddress: "solana-counterparty-tri",
							Delta:               "-100",
							EventID:             "solana|devnet|sig-tri-sol-101|tx:outer:0:inner:-1|addr:tri-sol-addr|asset:SOL|cat:TRANSFER",
							TokenType:           model.TokenTypeNative,
						},
						{
							EventCategory:       model.EventCategoryFee,
							EventAction:         "transaction_fee",
							ProgramID:           "11111111111111111111111111111111",
							ContractAddress:     "11111111111111111111111111111111",
							Address:             address,
							CounterpartyAddress: "",
							Delta:               "-2",
							EventID:             "solana|devnet|sig-tri-sol-101|tx:outer:-1:inner:-1|addr:tri-sol-addr|asset:SOL|cat:FEE",
							TokenType:           model.TokenTypeNative,
						},
					},
				},
			},
		}
	case model.ChainBase:
		address := triChainAddress(model.ChainBase)
		txHash := "0xtri_base_201"
		cursor := txHash
		return event.NormalizedBatch{
			Chain:             model.ChainBase,
			Network:           model.NetworkSepolia,
			Address:           address,
			NewCursorValue:    &cursor,
			NewCursorSequence: 201,
			Transactions: []event.NormalizedTransaction{
				{
					TxHash:      txHash,
					BlockCursor: 201,
					FeeAmount:   "4",
					FeePayer:    address,
					Status:      model.TxStatusSuccess,
					BalanceEvents: []event.NormalizedBalanceEvent{
						{
							EventCategory:       model.EventCategoryTransfer,
							EventAction:         "native_transfer",
							ProgramID:           "0xbase-program-tri",
							ContractAddress:     "ETH",
							Address:             address,
							CounterpartyAddress: "0x2222222222222222222222222222222222222201",
							Delta:               "-50",
							EventID:             "base|sepolia|0xtri_base_201|tx:log:0|addr:0x1111111111111111111111111111111111112201|asset:ETH|cat:TRANSFER",
							TokenType:           model.TokenTypeNative,
						},
						{
							EventCategory:       model.EventCategoryFeeExecutionL2,
							EventAction:         "fee_execution_l2",
							ProgramID:           "0xbase-program-tri",
							ContractAddress:     "ETH",
							Address:             address,
							CounterpartyAddress: "",
							Delta:               "-3",
							EventID:             "base|sepolia|0xtri_base_201|tx:log:1|addr:0x1111111111111111111111111111111111112201|asset:ETH|cat:fee_execution_l2",
							TokenType:           model.TokenTypeNative,
						},
						{
							EventCategory:       model.EventCategoryFeeDataL1,
							EventAction:         "fee_data_l1",
							ProgramID:           "0xbase-program-tri",
							ContractAddress:     "ETH",
							Address:             address,
							CounterpartyAddress: "",
							Delta:               "-1",
							EventID:             "base|sepolia|0xtri_base_201|tx:log:2|addr:0x1111111111111111111111111111111111112201|asset:ETH|cat:fee_data_l1",
							TokenType:           model.TokenTypeNative,
						},
					},
				},
			},
		}
	default:
		address := triChainAddress(model.ChainBTC)
		txHash := "btc-tri-301"
		cursor := txHash
		return event.NormalizedBatch{
			Chain:             model.ChainBTC,
			Network:           model.NetworkTestnet,
			Address:           address,
			NewCursorValue:    &cursor,
			NewCursorSequence: 301,
			Transactions: []event.NormalizedTransaction{
				{
					TxHash:      txHash,
					BlockCursor: 301,
					FeeAmount:   "1",
					FeePayer:    address,
					Status:      model.TxStatusSuccess,
					BalanceEvents: []event.NormalizedBalanceEvent{
						{
							EventCategory:       model.EventCategoryTransfer,
							EventAction:         "vin_spend",
							ProgramID:           "btc",
							ContractAddress:     "BTC",
							Address:             address,
							CounterpartyAddress: "btc-counterparty-vin",
							Delta:               "-10",
							EventID:             "btc|testnet|btc-tri-301|tx:vin:0|addr:tri-btc-addr|asset:BTC|cat:TRANSFER",
							TokenType:           model.TokenTypeNative,
						},
						{
							EventCategory:       model.EventCategoryTransfer,
							EventAction:         "vout_receive",
							ProgramID:           "btc",
							ContractAddress:     "BTC",
							Address:             address,
							CounterpartyAddress: "btc-counterparty-vout",
							Delta:               "10",
							EventID:             "btc|testnet|btc-tri-301|tx:vout:0|addr:tri-btc-addr|asset:BTC|cat:TRANSFER",
							TokenType:           model.TokenTypeNative,
						},
						{
							EventCategory:       model.EventCategoryFee,
							EventAction:         "miner_fee",
							ProgramID:           "btc",
							ContractAddress:     "BTC",
							Address:             address,
							CounterpartyAddress: "",
							Delta:               "-1",
							EventID:             "btc|testnet|btc-tri-301|tx:fee|addr:tri-btc-addr|asset:BTC|cat:FEE",
							TokenType:           model.TokenTypeNative,
						},
					},
				},
			},
		}
	}
}

func buildTriChainClosureBatch(chainID model.Chain, cursor string, sequence int64) event.NormalizedBatch {
	previous := cursor
	next := cursor
	return event.NormalizedBatch{
		Chain:                  chainID,
		Network:                triChainNetwork(chainID),
		Address:                triChainAddress(chainID),
		PreviousCursorValue:    &previous,
		PreviousCursorSequence: sequence,
		NewCursorValue:         &next,
		NewCursorSequence:      sequence,
	}
}

func snapshotTriChainState(state *interleaveState) triChainSnapshot {
	tuples := state.snapshotTuples()
	tupleKeys := make(map[string]struct{}, len(tuples))
	eventIDs := make(map[string]struct{}, len(tuples))
	eventIDCounts := make(map[string]int, len(tuples))
	categoryCounts := make(map[string]int, len(tuples))
	btcTotalDelta := "0"

	for _, tuple := range tuples {
		tupleKeys[interleaveTupleKey(tuple)] = struct{}{}
		eventIDs[tuple.EventID] = struct{}{}
		eventIDCounts[tuple.EventID]++
		categoryCounts[chainCategoryKey(tuple.Chain, tuple.Category)]++
		if tuple.Chain == model.ChainBTC {
			next, err := addDecimalStringsForTest(btcTotalDelta, tuple.Delta)
			if err == nil {
				btcTotalDelta = next
			}
		}
	}

	cursors := state.snapshotCursors()
	cursorSeq := make(map[string]int64, len(cursors))
	for key, cursor := range cursors {
		if cursor == nil {
			continue
		}
		cursorSeq[key] = cursor.CursorSequence
	}

	watermarks := state.snapshotWatermarks()

	state.mu.Lock()
	insertedCount := len(state.insertedEvents)
	upsertAttempts := state.upsertAttempts
	cursorWrites := make(map[string][]int64, len(state.cursorWrites))
	for key, writes := range state.cursorWrites {
		copied := append([]int64(nil), writes...)
		cursorWrites[key] = copied
	}
	watermarkWrites := make(map[string][]int64, len(state.watermarkWrites))
	for key, writes := range state.watermarkWrites {
		copied := append([]int64(nil), writes...)
		watermarkWrites[key] = copied
	}
	state.mu.Unlock()

	return triChainSnapshot{
		tupleKeys:       tupleKeys,
		eventIDs:        eventIDs,
		eventIDCounts:   eventIDCounts,
		categoryCounts:  categoryCounts,
		btcTotalDelta:   btcTotalDelta,
		cursorSeq:       cursorSeq,
		watermarks:      watermarks,
		cursorWrites:    cursorWrites,
		watermarkWrites: watermarkWrites,
		insertedCount:   insertedCount,
		upsertAttempts:  upsertAttempts,
	}
}

func ingesterForChain(ings triChainIngesters, chainID model.Chain) *Ingester {
	switch chainID {
	case model.ChainSolana:
		return ings.solana
	case model.ChainBase:
		return ings.base
	case model.ChainBTC:
		return ings.btc
	default:
		return nil
	}
}

func triChainAddress(chainID model.Chain) string {
	switch chainID {
	case model.ChainSolana:
		return "tri-sol-addr"
	case model.ChainBase:
		return "0x1111111111111111111111111111111111112201"
	case model.ChainBTC:
		return "tri-btc-addr"
	default:
		return ""
	}
}

func triChainNetwork(chainID model.Chain) model.Network {
	switch chainID {
	case model.ChainSolana:
		return model.NetworkDevnet
	case model.ChainBase:
		return model.NetworkSepolia
	case model.ChainBTC:
		return model.NetworkTestnet
	default:
		return ""
	}
}

func chainCategoryKey(chainID model.Chain, category model.EventCategory) string {
	return fmt.Sprintf("%s|%s", chainID, category)
}

func tupleDiffCount(left, right map[string]struct{}) int {
	diff := 0
	for key := range left {
		if _, exists := right[key]; !exists {
			diff++
		}
	}
	for key := range right {
		if _, exists := left[key]; !exists {
			diff++
		}
	}
	return diff
}

func assertNoDuplicateEventIDs(t *testing.T, counts map[string]int) {
	t.Helper()
	for eventID, count := range counts {
		assert.Equal(t, 1, count, "duplicate canonical event id detected: %s", eventID)
	}
}

func chainEventIDSubset(eventIDs map[string]struct{}, chainID model.Chain) map[string]struct{} {
	subset := make(map[string]struct{})
	prefix := string(chainID) + "|"
	for eventID := range eventIDs {
		if strings.HasPrefix(eventID, prefix) {
			subset[eventID] = struct{}{}
		}
	}
	return subset
}
