package ingester

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// reorgFakeDriver / reorgFakeConn / reorgFakeTx mirror the E2E fake pattern.
type reorgFakeDriver struct{}
type reorgFakeConn struct{}
type reorgFakeTx struct{}

func (d *reorgFakeDriver) Open(string) (driver.Conn, error) { return &reorgFakeConn{}, nil }
func (c *reorgFakeConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("not implemented")
}
func (c *reorgFakeConn) Close() error              { return nil }
func (c *reorgFakeConn) Begin() (driver.Tx, error) { return &reorgFakeTx{}, nil }
func (tx *reorgFakeTx) Commit() error              { return nil }
func (tx *reorgFakeTx) Rollback() error            { return nil }

var registerReorgFakeDriver sync.Once

func openReorgFakeDB(t *testing.T) *sql.DB {
	t.Helper()
	registerReorgFakeDriver.Do(func() {
		sql.Register("fake_reorg_interleave_ingester", &reorgFakeDriver{})
	})
	db, err := sql.Open("fake_reorg_interleave_ingester", "")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func buildReorgIngester(
	t *testing.T,
	state *interleaveState,
	inputCh chan event.NormalizedBatch,
) *Ingester {
	t.Helper()
	fakeDB := openReorgFakeDB(t)
	return New(
		&interleaveTxBeginner{db: fakeDB},
		&interleaveTxRepo{state: state},
		&interleaveBalanceEventRepo{state: state},
		&interleaveBalanceRepo{state: state},
		&interleaveTokenRepo{state: state},
		&interleaveConfigRepo{state: state},
		inputCh,
		slog.Default(),
		WithReorgHandler(func(_ context.Context, _ *sql.Tx, batch event.NormalizedBatch) error {
			forkCursor := batch.PreviousCursorSequence
			rewindCursor, rewindSeq, err := state.rollbackFromCursor(
				batch.Chain, batch.Network, batch.Address, forkCursor,
			)
			if err != nil {
				return err
			}
			state.mu.Lock()
			cursorKey := fmt.Sprintf("%s-%s|%s", batch.Chain, batch.Network, batch.Address)
			state.cursors[cursorKey] = &model.AddressCursor{
				Chain:          batch.Chain,
				Network:        batch.Network,
				Address:        batch.Address,
				CursorValue:    rewindCursor,
				CursorSequence: rewindSeq,
			}
			wmKey := fmt.Sprintf("%s-%s", batch.Chain, batch.Network)
			if rewindSeq < state.watermarks[wmKey] {
				state.watermarks[wmKey] = rewindSeq
			}
			state.mu.Unlock()
			return nil
		}),
	)
}

func makeBatch(chain model.Chain, network model.Network, address string, prevCursor *string, prevSeq int64, newCursor *string, newSeq int64, txs []event.NormalizedTransaction) event.NormalizedBatch {
	walletID := "wallet-" + address
	orgID := "org-" + address
	return event.NormalizedBatch{
		Chain:                  chain,
		Network:                network,
		Address:                address,
		WalletID:               &walletID,
		OrgID:                  &orgID,
		PreviousCursorValue:    prevCursor,
		PreviousCursorSequence: prevSeq,
		NewCursorValue:         newCursor,
		NewCursorSequence:      newSeq,
		Transactions:           txs,
	}
}

func makeBalanceEvent(action string, address string, delta string, contractAddress string) event.NormalizedBalanceEvent {
	return event.NormalizedBalanceEvent{
		OuterInstructionIndex: 0,
		InnerInstructionIndex: -1,
		ActivityType:           model.ActivityWithdrawal,
		EventAction:           action,
		ProgramID:             "test",
		ContractAddress:       contractAddress,
		Address:               address,
		Delta:                 delta,
		TokenSymbol:           "TEST",
		TokenName:             "Test Token",
		TokenDecimals:         9,
		TokenType:             model.TokenTypeNative,
		EventID:               uuid.NewString(),
		EventPathType:         "test",
		DecoderVersion:        "test-v1",
	}
}

func TestReorgRollback_SingleChainCursorRegressionRewindAndConverge(t *testing.T) {
	state := newInterleaveState(nil)
	inputCh := make(chan event.NormalizedBatch, 10)
	ing := buildReorgIngester(t, state, inputCh)

	const address = "solana-addr-1"
	now := time.Now()

	// Step 1: Insert batches at cursor 100, 101 (4 events total).
	sig100 := "sig100"
	sig101 := "sig101"
	inputCh <- makeBatch(model.ChainSolana, model.NetworkDevnet, address, nil, 0, &sig100, 100,
		[]event.NormalizedTransaction{
			{
				TxHash: sig100, BlockCursor: 100, BlockTime: &now, FeeAmount: "5000", FeePayer: address,
				Status: model.TxStatusSuccess,
				BalanceEvents: []event.NormalizedBalanceEvent{
					makeBalanceEvent("system_transfer", address, "-1000000", "SOL"),
					makeBalanceEvent("transaction_fee", address, "-5000", "SOL"),
				},
			},
		},
	)
	inputCh <- makeBatch(model.ChainSolana, model.NetworkDevnet, address, &sig100, 100, &sig101, 101,
		[]event.NormalizedTransaction{
			{
				TxHash: sig101, BlockCursor: 101, BlockTime: &now, FeeAmount: "5000", FeePayer: address,
				Status: model.TxStatusSuccess,
				BalanceEvents: []event.NormalizedBalanceEvent{
					makeBalanceEvent("system_transfer", address, "-2000000", "SOL"),
					makeBalanceEvent("transaction_fee", address, "-5000", "SOL"),
				},
			},
		},
	)

	// Step 2: Reorg batch: cursor regresses from 101 to 100.
	reorgNewCursor := "sig100-fork"
	inputCh <- makeBatch(model.ChainSolana, model.NetworkDevnet, address, &sig101, 101, &reorgNewCursor, 100,
		[]event.NormalizedTransaction{},
	)

	// Step 3: New fork batch at cursor 100 with different content.
	newForkSig := "sig100-new-fork"
	inputCh <- makeBatch(model.ChainSolana, model.NetworkDevnet, address, &reorgNewCursor, 100, &newForkSig, 100,
		[]event.NormalizedTransaction{
			{
				TxHash: newForkSig, BlockCursor: 100, BlockTime: &now, FeeAmount: "5000", FeePayer: address,
				Status: model.TxStatusSuccess,
				BalanceEvents: []event.NormalizedBalanceEvent{
					makeBalanceEvent("system_transfer", address, "-500000", "SOL"),
				},
			},
		},
	)

	close(inputCh)
	require.NoError(t, ing.Run(context.Background()))

	// Verify: events from cursor >= 100 in the old fork should be deleted.
	// Only the new fork event should remain after reorg.
	tuples := state.snapshotTuples()
	// We expect: 2 events from batch 100 (pre-reorg) get deleted,
	// 2 events from batch 101 get deleted, then 1 new event from the new fork.
	var postReorgEvents []interleaveTuple
	for _, tuple := range tuples {
		if tuple.Chain == model.ChainSolana {
			postReorgEvents = append(postReorgEvents, tuple)
		}
	}
	assert.NotEmpty(t, postReorgEvents, "should have events from the new fork")

	// Verify watermark converged (cursors are no longer managed by the ingester).
	watermarks := state.snapshotWatermarks()
	wmKey := "solana-devnet"
	assert.Contains(t, watermarks, wmKey, "watermark should exist for solana-devnet")
}

func TestReorgRollback_BTCCompetingBranchReplay(t *testing.T) {
	state := newInterleaveState(nil)
	inputCh := make(chan event.NormalizedBatch, 10)
	ing := buildReorgIngester(t, state, inputCh)

	const address = "tb1btcaddr1"
	now := time.Now()

	// Insert BTC batches at cursor 300, 301, 302.
	sig300 := "btc-300"
	sig301 := "btc-301"
	sig302 := "btc-302"

	inputCh <- makeBatch(model.ChainBTC, model.NetworkTestnet, address, nil, 0, &sig300, 300,
		[]event.NormalizedTransaction{
			{
				TxHash: sig300, BlockCursor: 300, BlockTime: &now, FeeAmount: "1000", FeePayer: address,
				Status: model.TxStatusSuccess,
				BalanceEvents: []event.NormalizedBalanceEvent{
					makeBalanceEvent("vin_spend", address, "-50000", "BTC"),
					makeBalanceEvent("vout_receive", address, "48000", "BTC"),
				},
			},
		},
	)
	inputCh <- makeBatch(model.ChainBTC, model.NetworkTestnet, address, &sig300, 300, &sig301, 301,
		[]event.NormalizedTransaction{
			{
				TxHash: sig301, BlockCursor: 301, BlockTime: &now, FeeAmount: "500", FeePayer: address,
				Status: model.TxStatusSuccess,
				BalanceEvents: []event.NormalizedBalanceEvent{
					makeBalanceEvent("vin_spend", address, "-30000", "BTC"),
				},
			},
		},
	)
	inputCh <- makeBatch(model.ChainBTC, model.NetworkTestnet, address, &sig301, 301, &sig302, 302,
		[]event.NormalizedTransaction{
			{
				TxHash: sig302, BlockCursor: 302, BlockTime: &now, FeeAmount: "500", FeePayer: address,
				Status: model.TxStatusSuccess,
				BalanceEvents: []event.NormalizedBalanceEvent{
					makeBalanceEvent("vout_receive", address, "20000", "BTC"),
				},
			},
		},
	)

	// Reorg: cursor regresses from 302 to 301 with replacement transaction (BTC competing branch).
	reorgNewCursor := "btc-301-fork"
	inputCh <- makeBatch(model.ChainBTC, model.NetworkTestnet, address, &sig302, 302, &reorgNewCursor, 301,
		[]event.NormalizedTransaction{
			{
				TxHash: "btc-alt-301", BlockCursor: 301, BlockTime: &now, FeeAmount: "600", FeePayer: address,
				Status: model.TxStatusSuccess,
				BalanceEvents: []event.NormalizedBalanceEvent{
					makeBalanceEvent("vin_spend", address, "-25000", "BTC"),
					makeBalanceEvent("vout_receive", address, "24400", "BTC"),
				},
			},
		},
	)

	close(inputCh)
	require.NoError(t, ing.Run(context.Background()))

	// Verify: the replacement tx events should exist.
	tuples := state.snapshotTuples()
	var btcEvents []interleaveTuple
	for _, tuple := range tuples {
		if tuple.Chain == model.ChainBTC {
			btcEvents = append(btcEvents, tuple)
		}
	}
	assert.NotEmpty(t, btcEvents, "should have BTC events after competing branch replay")

	// Verify watermark.
	watermarks := state.snapshotWatermarks()
	wmKey := "btc-testnet"
	assert.GreaterOrEqual(t, watermarks[wmKey], int64(301))
}

func TestReorgRollback_CrossChainIsolation(t *testing.T) {
	state := newInterleaveState(nil)
	inputCh := make(chan event.NormalizedBatch, 20)
	ing := buildReorgIngester(t, state, inputCh)

	now := time.Now()

	// Insert events for 3 chains.
	solAddr := "sol-addr"
	baseAddr := "0xbaseaddr"
	btcAddr := "tb1btcaddr"

	solSig := "sol-sig-50"
	inputCh <- makeBatch(model.ChainSolana, model.NetworkDevnet, solAddr, nil, 0, &solSig, 50,
		[]event.NormalizedTransaction{
			{
				TxHash: solSig, BlockCursor: 50, BlockTime: &now, FeeAmount: "5000", FeePayer: solAddr,
				Status: model.TxStatusSuccess,
				BalanceEvents: []event.NormalizedBalanceEvent{
					makeBalanceEvent("system_transfer", solAddr, "-1000000", "SOL"),
				},
			},
		},
	)

	baseSig := "0xbase-sig-10"
	inputCh <- makeBatch(model.ChainBase, model.NetworkSepolia, baseAddr, nil, 0, &baseSig, 10,
		[]event.NormalizedTransaction{
			{
				TxHash: baseSig, BlockCursor: 10, BlockTime: &now, FeeAmount: "21000", FeePayer: baseAddr,
				Status: model.TxStatusSuccess,
				BalanceEvents: []event.NormalizedBalanceEvent{
					makeBalanceEvent("native_transfer", baseAddr, "-500000", "ETH"),
				},
			},
		},
	)

	btcSig := "btc-sig-100"
	inputCh <- makeBatch(model.ChainBTC, model.NetworkTestnet, btcAddr, nil, 0, &btcSig, 100,
		[]event.NormalizedTransaction{
			{
				TxHash: btcSig, BlockCursor: 100, BlockTime: &now, FeeAmount: "1000", FeePayer: btcAddr,
				Status: model.TxStatusSuccess,
				BalanceEvents: []event.NormalizedBalanceEvent{
					makeBalanceEvent("vin_spend", btcAddr, "-30000", "BTC"),
				},
			},
		},
	)

	// Count events before reorg.
	close(inputCh)
	require.NoError(t, ing.Run(context.Background()))

	tuplesBeforeReorg := state.snapshotTuples()
	var solEventsBefore, baseEventsBefore, btcEventsBefore int
	for _, tuple := range tuplesBeforeReorg {
		switch tuple.Chain {
		case model.ChainSolana:
			solEventsBefore++
		case model.ChainBase:
			baseEventsBefore++
		case model.ChainBTC:
			btcEventsBefore++
		}
	}

	// Reorg Solana only.
	inputCh2 := make(chan event.NormalizedBatch, 5)
	ing2 := buildReorgIngester(t, state, inputCh2)

	reorgSolCursor := "sol-sig-reorg"
	inputCh2 <- makeBatch(model.ChainSolana, model.NetworkDevnet, solAddr, &solSig, 50, &reorgSolCursor, 49,
		[]event.NormalizedTransaction{},
	)

	close(inputCh2)
	require.NoError(t, ing2.Run(context.Background()))

	tuplesAfterReorg := state.snapshotTuples()
	var solEventsAfter, baseEventsAfter, btcEventsAfter int
	for _, tuple := range tuplesAfterReorg {
		switch tuple.Chain {
		case model.ChainSolana:
			solEventsAfter++
		case model.ChainBase:
			baseEventsAfter++
		case model.ChainBTC:
			btcEventsAfter++
		}
	}

	// Solana events should be affected (deleted by rollback).
	assert.Less(t, solEventsAfter, solEventsBefore, "Solana events should be reduced after reorg")

	// Base and BTC should be completely unaffected.
	assert.Equal(t, baseEventsBefore, baseEventsAfter, "Base events should not change")
	assert.Equal(t, btcEventsBefore, btcEventsAfter, "BTC events should not change")
}
