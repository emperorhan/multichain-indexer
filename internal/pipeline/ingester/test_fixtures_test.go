package ingester

import (
	"context"
	"database/sql"
	"fmt"
	"math/big"
	"sync"
	"testing"

	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/emperorhan/multichain-indexer/internal/store"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

type interleaveTuple struct {
	Chain          model.Chain
	Network        model.Network
	EventID        string
	TxHash         string
	Address        string
	WatchedAddress string
	Category       model.ActivityType
	Delta          string
	BlockCursor    int64
	TokenID        uuid.UUID
}

type interleaveStoredEvent struct {
	Tuple interleaveTuple
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
	eventRecords   map[string]interleaveStoredEvent
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
		eventRecords:    make(map[string]interleaveStoredEvent),
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

func (*interleaveTokenRepo) IsDeniedTx(context.Context, *sql.Tx, model.Chain, model.Network, string) (bool, error) {
	return false, nil
}

func (*interleaveTokenRepo) DenyTokenTx(context.Context, *sql.Tx, model.Chain, model.Network, string, string, string, int16, []string) error {
	return nil
}

func (*interleaveTokenRepo) AllowTokenTx(context.Context, *sql.Tx, model.Chain, model.Network, string, string) error {
	return nil
}

type interleaveBalanceEventRepo struct {
	state *interleaveState
}

func (r *interleaveBalanceEventRepo) UpsertTx(_ context.Context, _ *sql.Tx, be *model.BalanceEvent) (store.UpsertResult, error) {
	r.state.mu.Lock()
	defer r.state.mu.Unlock()

	r.state.upsertAttempts++
	if _, exists := r.state.insertedEvents[be.EventID]; exists {
		return store.UpsertResult{Inserted: false}, nil
	}

	watchedAddress := be.Address
	if be.WatchedAddress != nil {
		watchedAddress = *be.WatchedAddress
	}

	tuple := interleaveTuple{
		Chain:          be.Chain,
		Network:        be.Network,
		EventID:        be.EventID,
		TxHash:         be.TxHash,
		Address:        be.Address,
		WatchedAddress: watchedAddress,
		Category:       be.ActivityType,
		Delta:          be.Delta,
		BlockCursor:    be.BlockCursor,
		TokenID:        be.TokenID,
	}
	r.state.insertedEvents[be.EventID] = struct{}{}
	r.state.eventRecords[be.EventID] = interleaveStoredEvent{Tuple: tuple}
	r.state.tuples = append(r.state.tuples, tuple)
	return store.UpsertResult{Inserted: true}, nil
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
	_ string,
) error {
	primaryKey := interleaveBalanceKey(chain, network, address, tokenID)

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
	_ string,
) (string, error) {
	primaryKey := interleaveBalanceKey(chain, network, address, tokenID)
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	amount, exists := r.state.balances[primaryKey]
	if !exists || amount == "" {
		return "0", nil
	}
	return amount, nil
}

func (r *interleaveBalanceRepo) GetAmountWithExistsTx(
	_ context.Context,
	_ *sql.Tx,
	chain model.Chain,
	network model.Network,
	address string,
	tokenID uuid.UUID,
	_ string,
) (string, bool, error) {
	primaryKey := interleaveBalanceKey(chain, network, address, tokenID)
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	amount, exists := r.state.balances[primaryKey]
	if !exists || amount == "" {
		return "0", false, nil
	}
	return amount, true, nil
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

func (s *interleaveState) rollbackFromCursor(
	chain model.Chain,
	network model.Network,
	watchedAddress string,
	forkCursor int64,
) (*string, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	toDelete := make(map[string]interleaveStoredEvent)
	for eventID, record := range s.eventRecords {
		tuple := record.Tuple
		if tuple.Chain != chain || tuple.Network != network {
			continue
		}
		if tuple.WatchedAddress != watchedAddress {
			continue
		}
		if tuple.BlockCursor < forkCursor {
			continue
		}
		toDelete[eventID] = record
	}

	for eventID, record := range toDelete {
		tuple := record.Tuple
		invertedDelta, err := negateDecimalString(tuple.Delta)
		if err != nil {
			return nil, 0, err
		}
		balanceKey := interleaveBalanceKey(tuple.Chain, tuple.Network, tuple.Address, tuple.TokenID)
		current := s.balances[balanceKey]
		next, err := addDecimalStringsForTest(current, invertedDelta)
		if err != nil {
			return nil, 0, err
		}
		s.balances[balanceKey] = next
		delete(s.insertedEvents, eventID)
		delete(s.eventRecords, eventID)
	}

	filtered := make([]interleaveTuple, 0, len(s.tuples))
	for _, tuple := range s.tuples {
		if _, exists := s.insertedEvents[tuple.EventID]; !exists {
			continue
		}
		filtered = append(filtered, tuple)
	}
	s.tuples = filtered

	var rewindSeq int64
	var rewindCursor string
	for _, tuple := range s.tuples {
		if tuple.Chain != chain || tuple.Network != network {
			continue
		}
		if tuple.WatchedAddress != watchedAddress {
			continue
		}
		if tuple.BlockCursor >= forkCursor {
			continue
		}
		if tuple.BlockCursor > rewindSeq || (tuple.BlockCursor == rewindSeq && tuple.TxHash > rewindCursor) {
			rewindSeq = tuple.BlockCursor
			rewindCursor = tuple.TxHash
		}
	}
	if rewindSeq == 0 {
		return nil, 0, nil
	}

	return &rewindCursor, rewindSeq, nil
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

func interleaveKey(chain model.Chain, network model.Network) string {
	return fmt.Sprintf("%s-%s", chain, network)
}

func interleaveBalanceKey(chain model.Chain, network model.Network, address string, tokenID uuid.UUID) string {
	return fmt.Sprintf("%s|%s|%s", interleaveKey(chain, network), address, tokenID.String())
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
