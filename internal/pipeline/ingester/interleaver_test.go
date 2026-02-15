package ingester

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type interleaveTuple struct {
	Chain    model.Chain
	Network  model.Network
	EventID  string
	TxHash   string
	Address  string
	Category model.EventCategory
	Delta    string
}

type interleaveTxBeginner struct {
	db *sql.DB
}

func (b *interleaveTxBeginner) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	return b.db.BeginTx(ctx, opts)
}

type interleaveState struct {
	mu sync.Mutex

	txFailures map[string]error

	txIDs          map[string]uuid.UUID
	tokenIDs       map[string]uuid.UUID
	insertedEvents map[string]struct{}
	tuples         []interleaveTuple
	upsertAttempts int

	balances map[string]string
	cursors  map[string]*model.AddressCursor

	watermarks map[string]int64

	cursorWrites    map[string][]int64
	watermarkWrites map[string][]int64
}

func newInterleaveState(txFailures map[string]error) *interleaveState {
	clonedFailures := make(map[string]error, len(txFailures))
	for key, err := range txFailures {
		clonedFailures[key] = err
	}
	return &interleaveState{
		txFailures:      clonedFailures,
		txIDs:           make(map[string]uuid.UUID),
		tokenIDs:        make(map[string]uuid.UUID),
		insertedEvents:  make(map[string]struct{}),
		balances:        make(map[string]string),
		cursors:         make(map[string]*model.AddressCursor),
		watermarks:      make(map[string]int64),
		cursorWrites:    make(map[string][]int64),
		watermarkWrites: make(map[string][]int64),
	}
}

func (s *interleaveState) snapshotTuples() []interleaveTuple {
	s.mu.Lock()
	defer s.mu.Unlock()
	clone := make([]interleaveTuple, len(s.tuples))
	copy(clone, s.tuples)
	return clone
}

func (s *interleaveState) snapshotCursors() map[string]*model.AddressCursor {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]*model.AddressCursor, len(s.cursors))
	for key, cursor := range s.cursors {
		if cursor == nil {
			out[key] = nil
			continue
		}
		cloned := *cursor
		if cursor.CursorValue != nil {
			value := *cursor.CursorValue
			cloned.CursorValue = &value
		}
		out[key] = &cloned
	}
	return out
}

func (s *interleaveState) snapshotWatermarks() map[string]int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]int64, len(s.watermarks))
	for key, value := range s.watermarks {
		out[key] = value
	}
	return out
}

func (s *interleaveState) snapshotBalances() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(s.balances))
	for key, value := range s.balances {
		out[key] = value
	}
	return out
}

type interleaveTxRepo struct {
	state *interleaveState
}

func (r *interleaveTxRepo) UpsertTx(_ context.Context, _ *sql.Tx, t *model.Transaction) (uuid.UUID, error) {
	chainKey := interleaveKey(t.Chain, t.Network)
	primaryKey := fmt.Sprintf("%s|%s", chainKey, t.TxHash)

	r.state.mu.Lock()
	defer r.state.mu.Unlock()

	if err, exists := r.state.txFailures[chainKey]; exists {
		delete(r.state.txFailures, chainKey)
		return uuid.Nil, err
	}

	if id, exists := r.state.txIDs[primaryKey]; exists {
		return id, nil
	}

	id := uuid.New()
	r.state.txIDs[primaryKey] = id
	return id, nil
}

type interleaveTokenRepo struct {
	state *interleaveState
}

func (r *interleaveTokenRepo) UpsertTx(_ context.Context, _ *sql.Tx, t *model.Token) (uuid.UUID, error) {
	primaryKey := fmt.Sprintf("%s|%s|%s", interleaveKey(t.Chain, t.Network), t.ContractAddress, t.TokenType)

	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	if id, exists := r.state.tokenIDs[primaryKey]; exists {
		return id, nil
	}

	id := uuid.New()
	r.state.tokenIDs[primaryKey] = id
	return id, nil
}

func (*interleaveTokenRepo) FindByContractAddress(context.Context, model.Chain, model.Network, string) (*model.Token, error) {
	return nil, nil
}

type interleaveBalanceEventRepo struct {
	state *interleaveState
}

func (r *interleaveBalanceEventRepo) UpsertTx(_ context.Context, _ *sql.Tx, be *model.BalanceEvent) (bool, error) {
	r.state.mu.Lock()
	defer r.state.mu.Unlock()

	r.state.upsertAttempts++
	if _, exists := r.state.insertedEvents[be.EventID]; exists {
		return false, nil
	}

	r.state.insertedEvents[be.EventID] = struct{}{}
	r.state.tuples = append(r.state.tuples, interleaveTuple{
		Chain:    be.Chain,
		Network:  be.Network,
		EventID:  be.EventID,
		TxHash:   be.TxHash,
		Address:  be.Address,
		Category: be.EventCategory,
		Delta:    be.Delta,
	})
	return true, nil
}

type interleaveBalanceRepo struct {
	state *interleaveState
}

func (r *interleaveBalanceRepo) AdjustBalanceTx(
	_ context.Context,
	_ *sql.Tx,
	chain model.Chain,
	network model.Network,
	address string,
	tokenID uuid.UUID,
	_ *string,
	_ *string,
	delta string,
	_ int64,
	_ string,
) error {
	primaryKey := fmt.Sprintf("%s|%s|%s", interleaveKey(chain, network), address, tokenID.String())

	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	current := r.state.balances[primaryKey]
	next, err := addDecimalStringsForTest(current, delta)
	if err != nil {
		return err
	}
	r.state.balances[primaryKey] = next
	return nil
}

func (r *interleaveBalanceRepo) GetAmountTx(
	_ context.Context,
	_ *sql.Tx,
	chain model.Chain,
	network model.Network,
	address string,
	tokenID uuid.UUID,
) (string, error) {
	primaryKey := fmt.Sprintf("%s|%s|%s", interleaveKey(chain, network), address, tokenID.String())
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	amount, exists := r.state.balances[primaryKey]
	if !exists || amount == "" {
		return "0", nil
	}
	return amount, nil
}

func (*interleaveBalanceRepo) GetByAddress(context.Context, model.Chain, model.Network, string) ([]model.Balance, error) {
	return nil, nil
}

type interleaveCursorRepo struct {
	state *interleaveState
}

func (r *interleaveCursorRepo) Get(_ context.Context, chain model.Chain, network model.Network, address string) (*model.AddressCursor, error) {
	key := fmt.Sprintf("%s|%s", interleaveKey(chain, network), address)
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	cursor, exists := r.state.cursors[key]
	if !exists || cursor == nil {
		return nil, nil
	}
	cloned := *cursor
	if cursor.CursorValue != nil {
		value := *cursor.CursorValue
		cloned.CursorValue = &value
	}
	return &cloned, nil
}

func (r *interleaveCursorRepo) UpsertTx(
	_ context.Context,
	_ *sql.Tx,
	chain model.Chain,
	network model.Network,
	address string,
	cursorValue *string,
	cursorSequence int64,
	_ int64,
) error {
	key := fmt.Sprintf("%s|%s", interleaveKey(chain, network), address)
	r.state.mu.Lock()
	defer r.state.mu.Unlock()

	cloned := &model.AddressCursor{
		Chain:          chain,
		Network:        network,
		Address:        address,
		CursorSequence: cursorSequence,
	}
	if cursorValue != nil {
		value := *cursorValue
		cloned.CursorValue = &value
	}
	r.state.cursors[key] = cloned
	r.state.cursorWrites[key] = append(r.state.cursorWrites[key], cursorSequence)
	return nil
}

func (*interleaveCursorRepo) EnsureExists(context.Context, model.Chain, model.Network, string) error {
	return nil
}

type interleaveConfigRepo struct {
	state *interleaveState
}

func (*interleaveConfigRepo) Get(context.Context, model.Chain, model.Network) (*model.IndexerConfig, error) {
	return nil, nil
}

func (*interleaveConfigRepo) Upsert(context.Context, *model.IndexerConfig) error {
	return nil
}

func (r *interleaveConfigRepo) UpdateWatermarkTx(
	_ context.Context,
	_ *sql.Tx,
	chain model.Chain,
	network model.Network,
	ingestedSequence int64,
) error {
	key := interleaveKey(chain, network)
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	if ingestedSequence > r.state.watermarks[key] {
		r.state.watermarks[key] = ingestedSequence
	}
	r.state.watermarkWrites[key] = append(r.state.watermarkWrites[key], r.state.watermarks[key])
	return nil
}

func addDecimalStringsForTest(current, delta string) (string, error) {
	baseValue := current
	if baseValue == "" {
		baseValue = "0"
	}
	left := new(big.Int)
	if _, ok := left.SetString(baseValue, 10); !ok {
		return "", fmt.Errorf("invalid decimal value: %s", current)
	}
	right := new(big.Int)
	if _, ok := right.SetString(delta, 10); !ok {
		return "", fmt.Errorf("invalid decimal value: %s", delta)
	}
	left.Add(left, right)
	return left.String(), nil
}

func buildInterleaveBatch(chain model.Chain, network model.Network) event.NormalizedBatch {
	var (
		address         string
		txHash          string
		cursorSequence  int64
		eventID         string
		contractAddress string
		programID       string
		counterparty    string
	)

	switch chain {
	case model.ChainSolana:
		address = "solana-addr-1"
		txHash = "sig-interleave-sol-1"
		cursorSequence = 101
		eventID = "solana|devnet|sig-interleave-sol-1|tx:outer:0:inner:-1|addr:solana-addr-1|asset:11111111111111111111111111111111|cat:TRANSFER"
		contractAddress = "11111111111111111111111111111111"
		programID = "11111111111111111111111111111111"
		counterparty = "solana-counterparty"
	default:
		address = "0x1111111111111111111111111111111111111111"
		txHash = "0xinterleave_base_1"
		cursorSequence = 201
		eventID = "base|sepolia|0xinterleave_base_1|tx:log:7|addr:0x1111111111111111111111111111111111111111|asset:ETH|cat:TRANSFER"
		contractAddress = "ETH"
		programID = "0xbase-program"
		counterparty = "0x2222222222222222222222222222222222222222"
	}

	cursor := txHash
	return event.NormalizedBatch{
		Chain:             chain,
		Network:           network,
		Address:           address,
		NewCursorValue:    &cursor,
		NewCursorSequence: cursorSequence,
		Transactions: []event.NormalizedTransaction{
			{
				TxHash:      txHash,
				BlockCursor: cursorSequence,
				FeeAmount:   "0",
				FeePayer:    address,
				Status:      model.TxStatusSuccess,
				ChainData:   json.RawMessage("{}"),
				BalanceEvents: []event.NormalizedBalanceEvent{
					{
						OuterInstructionIndex: 0,
						InnerInstructionIndex: -1,
						EventCategory:         model.EventCategoryTransfer,
						EventAction:           "transfer",
						ProgramID:             programID,
						ContractAddress:       contractAddress,
						Address:               address,
						CounterpartyAddress:   counterparty,
						Delta:                 "-1",
						EventID:               eventID,
						TokenType:             model.TokenTypeNative,
					},
				},
			},
		},
	}
}

func newInterleaveIngesters(
	t *testing.T,
	state *interleaveState,
	maxSkew time.Duration,
) (*Ingester, *Ingester) {
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

	interleaver := NewDeterministicDualChainInterleaver(maxSkew)

	solIng := New(
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
	baseIng := New(
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

	return solIng, baseIng
}

func runTwoChainPermutation(
	ctx context.Context,
	solIng *Ingester,
	baseIng *Ingester,
	solDelay time.Duration,
	baseDelay time.Duration,
) []error {
	var wg sync.WaitGroup
	errCh := make(chan error, 2)

	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(solDelay)
		errCh <- solIng.processBatch(ctx, buildInterleaveBatch(model.ChainSolana, model.NetworkDevnet))
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(baseDelay)
		errCh <- baseIng.processBatch(ctx, buildInterleaveBatch(model.ChainBase, model.NetworkSepolia))
	}()

	wg.Wait()
	close(errCh)

	errs := make([]error, 0, 2)
	for err := range errCh {
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

func TestDualChainInterleaving_CompletionOrderPermutationsConverge(t *testing.T) {
	ctx := context.Background()
	maxSkew := 40 * time.Millisecond

	run := func(solDelay, baseDelay time.Duration) ([]interleaveTuple, map[string]int64) {
		state := newInterleaveState(nil)
		solIng, baseIng := newInterleaveIngesters(t, state, maxSkew)
		errs := runTwoChainPermutation(ctx, solIng, baseIng, solDelay, baseDelay)
		require.Empty(t, errs)
		return state.snapshotTuples(), state.snapshotWatermarks()
	}

	// Base starts first but Solana arrives inside skew budget; order should still converge.
	permutationA, watermarksA := run(10*time.Millisecond, 0)
	permutationB, watermarksB := run(0, 10*time.Millisecond)

	require.Len(t, permutationA, 2)
	require.Len(t, permutationB, 2)
	assert.Equal(t, permutationA, permutationB)
	assert.Equal(t, model.ChainSolana, permutationA[0].Chain)
	assert.Equal(t, model.ChainBase, permutationA[1].Chain)
	assert.Equal(t, int64(101), watermarksA[interleaveKey(model.ChainSolana, model.NetworkDevnet)])
	assert.Equal(t, int64(201), watermarksA[interleaveKey(model.ChainBase, model.NetworkSepolia)])
	assert.Equal(t, watermarksA, watermarksB)
}

func TestDualChainInterleaving_OneChainLagFailurePreservesCursorIsolation(t *testing.T) {
	ctx := context.Background()
	solKey := interleaveKey(model.ChainSolana, model.NetworkDevnet)
	state := newInterleaveState(map[string]error{
		solKey: errors.New("solana transient failure"),
	})
	solIng, baseIng := newInterleaveIngesters(t, state, 20*time.Millisecond)

	errs := runTwoChainPermutation(ctx, solIng, baseIng, 35*time.Millisecond, 0)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), "solana transient failure")

	tuples := state.snapshotTuples()
	require.Len(t, tuples, 1)
	assert.Equal(t, model.ChainBase, tuples[0].Chain)

	cursors := state.snapshotCursors()
	baseCursorKey := fmt.Sprintf("%s|%s", interleaveKey(model.ChainBase, model.NetworkSepolia), "0x1111111111111111111111111111111111111111")
	_, solCursorExists := cursors[fmt.Sprintf("%s|%s", solKey, "solana-addr-1")]
	require.False(t, solCursorExists, "solana failure should not write base cursor or solana cursor")

	baseCursor, baseCursorExists := cursors[baseCursorKey]
	require.True(t, baseCursorExists)
	require.NotNil(t, baseCursor)
	assert.Equal(t, int64(201), baseCursor.CursorSequence)

	watermarks := state.snapshotWatermarks()
	_, solWatermarkExists := watermarks[solKey]
	require.False(t, solWatermarkExists, "solana failure should not advance watermark")
	assert.Equal(t, int64(201), watermarks[interleaveKey(model.ChainBase, model.NetworkSepolia)])
}

func TestDualChainInterleaving_ReplayResumeIdempotentAcrossMixedPermutations(t *testing.T) {
	ctx := context.Background()
	state := newInterleaveState(nil)
	solIng, baseIng := newInterleaveIngesters(t, state, 40*time.Millisecond)

	firstPassErrs := runTwoChainPermutation(ctx, solIng, baseIng, 10*time.Millisecond, 0)
	require.Empty(t, firstPassErrs)

	secondPassErrs := runTwoChainPermutation(ctx, solIng, baseIng, 0, 10*time.Millisecond)
	require.Empty(t, secondPassErrs)

	tuples := state.snapshotTuples()
	require.Len(t, tuples, 2)
	assert.Equal(t, model.ChainSolana, tuples[0].Chain)
	assert.Equal(t, model.ChainBase, tuples[1].Chain)

	state.mu.Lock()
	upsertAttempts := state.upsertAttempts
	insertedCount := len(state.insertedEvents)
	state.mu.Unlock()

	assert.Equal(t, 4, upsertAttempts)
	assert.Equal(t, 2, insertedCount)

	balances := state.snapshotBalances()
	for _, amount := range balances {
		assert.Equal(t, "-1", amount, "balance double-apply detected after replay")
	}

	watermarks := state.snapshotWatermarks()
	assert.Equal(t, int64(101), watermarks[interleaveKey(model.ChainSolana, model.NetworkDevnet)])
	assert.Equal(t, int64(201), watermarks[interleaveKey(model.ChainBase, model.NetworkSepolia)])
}

func TestDeterministicDualChainInterleaver_FailedAttemptDoesNotAdvanceCheckpoint(t *testing.T) {
	ctx := context.Background()
	interleaver, ok := NewDeterministicDualChainInterleaver(10 * time.Millisecond).(*deterministicDualChainInterleaver)
	require.True(t, ok)

	releaseSol, err := interleaver.Acquire(ctx, model.ChainSolana, model.NetworkDevnet)
	require.NoError(t, err)
	releaseSol(false)

	interleaver.mu.Lock()
	nextAfterFailedAttempt := interleaver.next
	interleaver.mu.Unlock()
	assert.Equal(t, 0, nextAfterFailedAttempt, "failed attempt must not advance next-turn checkpoint")

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
	assert.Equal(t, 0, nextAfterCommittedBase)
}

type crashCutpoint string

const (
	cutpointAfterFetch        crashCutpoint = "after_fetch"
	cutpointAfterNormalize    crashCutpoint = "after_normalize"
	cutpointIngestPreCommit   crashCutpoint = "ingest_pre_commit"
	cutpointAfterCursorCommit crashCutpoint = "after_cursor_commit"
)

type crashPermutationResult struct {
	tupleKeys       []string
	eventIDs        []string
	cursorSeq       map[string]int64
	watermarks      map[string]int64
	balancesByActor map[string]string
	cursorWrites    map[string][]int64
	watermarkWrites map[string][]int64
	insertedCount   int
	upsertAttempts  int
}

func TestDualChainInterleaving_CrashCutpointPermutationsConverge(t *testing.T) {
	baseline := runCrashPermutationBaseline(t)
	expectedEventIDs := expectedInterleaveEventIDs()

	cutpoints := []crashCutpoint{
		cutpointAfterFetch,
		cutpointAfterNormalize,
		cutpointIngestPreCommit,
		cutpointAfterCursorCommit,
	}
	chains := []model.Chain{model.ChainSolana, model.ChainBase}
	orderings := []bool{false, true}

	for _, cutpoint := range cutpoints {
		for _, crashChain := range chains {
			for _, otherChainFirst := range orderings {
				result := runCrashPermutationScenario(t, cutpoint, crashChain, otherChainFirst)
				scenario := fmt.Sprintf("cutpoint=%s crash_chain=%s other_chain_first=%t", cutpoint, crashChain, otherChainFirst)

				assert.Equal(t, baseline.tupleKeys, result.tupleKeys, "%s canonical tuples drifted", scenario)
				assert.Equal(t, baseline.cursorSeq, result.cursorSeq, "%s cursor state drifted", scenario)
				assert.Equal(t, baseline.watermarks, result.watermarks, "%s watermark state drifted", scenario)
				assert.Equal(t, baseline.balancesByActor, result.balancesByActor, "%s balance state drifted", scenario)

				assert.Equal(t, expectedEventIDs, result.eventIDs, "%s missing logical events after resume", scenario)
				assert.Equal(t, len(expectedEventIDs), result.insertedCount, "%s duplicate canonical IDs detected", scenario)
				assert.GreaterOrEqual(t, result.upsertAttempts, result.insertedCount, "%s replay did not execute expected duplicate suppression path", scenario)

				assertMonotonicWrites(t, result.cursorWrites, scenario+" cursor writes")
				assertMonotonicWrites(t, result.watermarkWrites, scenario+" watermark writes")
			}
		}
	}
}

func runCrashPermutationBaseline(t *testing.T) crashPermutationResult {
	t.Helper()

	ctx := context.Background()
	state := newInterleaveState(nil)
	for i := 0; i < 4; i++ {
		solIng, baseIng := newInterleaveIngesters(t, state, 5*time.Millisecond)
		errs := runTwoChainPermutation(ctx, solIng, baseIng, delayForIteration(i, true), delayForIteration(i, false))
		require.Empty(t, errs)
	}

	return snapshotCrashPermutationResult(state)
}

func runCrashPermutationScenario(
	t *testing.T,
	cutpoint crashCutpoint,
	crashChain model.Chain,
	otherChainFirst bool,
) crashPermutationResult {
	t.Helper()

	ctx := context.Background()
	state := newInterleaveState(nil)
	solIng, baseIng := newInterleaveIngesters(t, state, 5*time.Millisecond)
	otherChain := counterpartChain(crashChain)

	if otherChainFirst {
		require.NoError(t, processInterleaveChain(ctx, solIng, baseIng, otherChain))
	}

	switch cutpoint {
	case cutpointAfterFetch, cutpointAfterNormalize:
		// Crash before normalized output reaches ingest; resume must replay deterministically.
	case cutpointIngestPreCommit:
		chainKey := interleaveKey(crashChain, networkForInterleaveChain(crashChain))
		state.mu.Lock()
		state.txFailures[chainKey] = errors.New("crash cutpoint pre-commit")
		state.mu.Unlock()
		err := processInterleaveChain(ctx, solIng, baseIng, crashChain)
		require.Error(t, err)
	case cutpointAfterCursorCommit:
		require.NoError(t, processInterleaveChain(ctx, solIng, baseIng, crashChain))
	default:
		t.Fatalf("unknown crash cutpoint: %s", cutpoint)
	}

	for i := 0; i < 4; i++ {
		solResume, baseResume := newInterleaveIngesters(t, state, 5*time.Millisecond)
		errs := runTwoChainPermutation(ctx, solResume, baseResume, delayForIteration(i, true), delayForIteration(i, false))
		require.Empty(t, errs)
	}

	return snapshotCrashPermutationResult(state)
}

func processInterleaveChain(ctx context.Context, solIng, baseIng *Ingester, chain model.Chain) error {
	switch chain {
	case model.ChainSolana:
		return solIng.processBatch(ctx, buildInterleaveBatch(model.ChainSolana, model.NetworkDevnet))
	case model.ChainBase:
		return baseIng.processBatch(ctx, buildInterleaveBatch(model.ChainBase, model.NetworkSepolia))
	default:
		return fmt.Errorf("unsupported chain for interleave scenario: %s", chain)
	}
}

func networkForInterleaveChain(chain model.Chain) model.Network {
	switch chain {
	case model.ChainSolana:
		return model.NetworkDevnet
	case model.ChainBase:
		return model.NetworkSepolia
	default:
		return model.Network("")
	}
}

func counterpartChain(chain model.Chain) model.Chain {
	if chain == model.ChainSolana {
		return model.ChainBase
	}
	return model.ChainSolana
}

func delayForIteration(iteration int, isSolana bool) time.Duration {
	if iteration%2 == 0 {
		if isSolana {
			return 2 * time.Millisecond
		}
		return 0
	}
	if isSolana {
		return 0
	}
	return 2 * time.Millisecond
}

func expectedInterleaveEventIDs() []string {
	ids := []string{
		buildInterleaveBatch(model.ChainSolana, model.NetworkDevnet).Transactions[0].BalanceEvents[0].EventID,
		buildInterleaveBatch(model.ChainBase, model.NetworkSepolia).Transactions[0].BalanceEvents[0].EventID,
	}
	sort.Strings(ids)
	return ids
}

func snapshotCrashPermutationResult(state *interleaveState) crashPermutationResult {
	state.mu.Lock()
	defer state.mu.Unlock()

	tuples := make([]interleaveTuple, len(state.tuples))
	copy(tuples, state.tuples)
	sort.Slice(tuples, func(i, j int) bool {
		return interleaveTupleKey(tuples[i]) < interleaveTupleKey(tuples[j])
	})
	tupleKeys := make([]string, len(tuples))
	for i, tuple := range tuples {
		tupleKeys[i] = interleaveTupleKey(tuple)
	}

	eventIDs := make([]string, 0, len(state.insertedEvents))
	for eventID := range state.insertedEvents {
		eventIDs = append(eventIDs, eventID)
	}
	sort.Strings(eventIDs)

	cursorSeq := make(map[string]int64, len(state.cursors))
	for key, cursor := range state.cursors {
		if cursor == nil {
			cursorSeq[key] = 0
			continue
		}
		cursorSeq[key] = cursor.CursorSequence
	}

	watermarks := make(map[string]int64, len(state.watermarks))
	for key, value := range state.watermarks {
		watermarks[key] = value
	}

	balancesByActor := make(map[string]string, len(state.balances))
	for key, value := range state.balances {
		parts := strings.Split(key, "|")
		actorKey := key
		if len(parts) >= 2 {
			actorKey = parts[0] + "|" + parts[1]
		}
		if current, exists := balancesByActor[actorKey]; exists {
			next, err := addDecimalStringsForTest(current, value)
			if err != nil {
				// Should not happen in this fixture, keep the raw current value for determinism.
				continue
			}
			balancesByActor[actorKey] = next
			continue
		}
		balancesByActor[actorKey] = value
	}

	cursorWrites := make(map[string][]int64, len(state.cursorWrites))
	for key, writes := range state.cursorWrites {
		copied := make([]int64, len(writes))
		copy(copied, writes)
		cursorWrites[key] = copied
	}

	watermarkWrites := make(map[string][]int64, len(state.watermarkWrites))
	for key, writes := range state.watermarkWrites {
		copied := make([]int64, len(writes))
		copy(copied, writes)
		watermarkWrites[key] = copied
	}

	return crashPermutationResult{
		tupleKeys:       tupleKeys,
		eventIDs:        eventIDs,
		cursorSeq:       cursorSeq,
		watermarks:      watermarks,
		balancesByActor: balancesByActor,
		cursorWrites:    cursorWrites,
		watermarkWrites: watermarkWrites,
		insertedCount:   len(state.insertedEvents),
		upsertAttempts:  state.upsertAttempts,
	}
}

func interleaveTupleKey(tuple interleaveTuple) string {
	return fmt.Sprintf("%s|%s|%s|%s|%s|%s|%s", tuple.Chain, tuple.Network, tuple.EventID, tuple.TxHash, tuple.Address, tuple.Category, tuple.Delta)
}

func assertMonotonicWrites(t *testing.T, writes map[string][]int64, label string) {
	t.Helper()

	for key, seq := range writes {
		for i := 1; i < len(seq); i++ {
			assert.GreaterOrEqual(t, seq[i], seq[i-1], "%s key=%s sequence regressed", label, key)
		}
	}
}
