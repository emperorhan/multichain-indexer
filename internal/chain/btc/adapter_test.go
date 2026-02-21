package btc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"testing"

	"github.com/emperorhan/multichain-indexer/internal/chain/btc/rpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeRPCClient struct {
	head              int64
	blockHashByHeight map[int64]string
	blocksByHash      map[string]*rpc.Block
	txsByID           map[string]*rpc.Transaction
	headErr           error
	blockHashErr      error
	blockErr          error
	txErr             error
}

func (f *fakeRPCClient) GetBlockCount(context.Context) (int64, error) {
	if f.headErr != nil {
		return 0, f.headErr
	}
	return f.head, nil
}

func (f *fakeRPCClient) GetBlockHash(_ context.Context, height int64) (string, error) {
	if f.blockHashErr != nil {
		return "", f.blockHashErr
	}
	return f.blockHashByHeight[height], nil
}

func (f *fakeRPCClient) GetBlock(_ context.Context, hash string, _ int) (*rpc.Block, error) {
	if f.blockErr != nil {
		return nil, f.blockErr
	}
	return f.blocksByHash[hash], nil
}

func (f *fakeRPCClient) GetBlockHeader(_ context.Context, hash string) (*rpc.BlockHeader, error) {
	block := f.blocksByHash[hash]
	if block == nil {
		return nil, nil
	}
	return &rpc.BlockHeader{
		Hash:              block.Hash,
		Height:            block.Height,
		PreviousBlockHash: block.PreviousBlockHash,
		Time:              block.Time,
	}, nil
}

func (f *fakeRPCClient) GetRawTransactionVerbose(_ context.Context, txid string) (*rpc.Transaction, error) {
	if f.txErr != nil {
		return nil, f.txErr
	}
	return f.txsByID[canonicalTxID(txid)], nil
}

func (f *fakeRPCClient) GetRawTransactionsVerbose(_ context.Context, txids []string) ([]*rpc.Transaction, error) {
	if f.txErr != nil {
		return nil, f.txErr
	}
	results := make([]*rpc.Transaction, len(txids))
	for i, txid := range txids {
		results[i] = f.txsByID[canonicalTxID(txid)]
	}
	return results, nil
}

func newTestAdapter(client rpc.RPCClient) *Adapter {
	return &Adapter{
		client:                   client,
		logger:                   slog.Default(),
		maxInitialLookbackBlocks: maxInitialLookbackBlocks,
	}
}

func TestAdapter_RPCClientContractParity(t *testing.T) {
	t.Parallel()

	var _ rpc.RPCClient = (*rpc.Client)(nil)
	var _ rpc.RPCClient = (*fakeRPCClient)(nil)
}

func TestAdapter_Chain(t *testing.T) {
	adapter := newTestAdapter(&fakeRPCClient{})
	assert.Equal(t, "btc", adapter.Chain())
}

func TestAdapter_GetHeadSequence(t *testing.T) {
	adapter := newTestAdapter(&fakeRPCClient{head: 128})
	seq, err := adapter.GetHeadSequence(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(128), seq)
}

func TestAdapter_GetHeadSequence_Error(t *testing.T) {
	adapter := newTestAdapter(&fakeRPCClient{headErr: errors.New("rpc down")})
	_, err := adapter.GetHeadSequence(context.Background())
	require.Error(t, err)
}

func TestAdapter_FetchNewSignaturesWithCutoff_InputAndOutputMatches(t *testing.T) {
	const watched = "tb1watched"

	client := &fakeRPCClient{
		head: 102,
		blockHashByHeight: map[int64]string{
			100: "block100",
			101: "block101",
			102: "block102",
		},
		blocksByHash: map[string]*rpc.Block{
			"block100": {
				Hash:   "block100",
				Height: 100,
				Time:   1_700_000_100,
				Tx: []*rpc.Transaction{
					{
						Txid:      "txa",
						Blockhash: "block100",
						Vin: []*rpc.Vin{
							{Txid: "prev1", Vout: 0},
						},
						Vout: []*rpc.Vout{
							{
								N:     0,
								Value: json.Number("0.00019000"),
								ScriptPubKey: rpc.ScriptPubKey{
									Address: "tb1external",
								},
							},
						},
					},
				},
			},
			"block101": {
				Hash:   "block101",
				Height: 101,
				Time:   1_700_000_101,
				Tx: []*rpc.Transaction{
					{
						Txid:      "txb",
						Blockhash: "block101",
						Vin: []*rpc.Vin{
							{Coinbase: "coinbase"},
						},
						Vout: []*rpc.Vout{
							{
								N:     0,
								Value: json.Number("0.00005000"),
								ScriptPubKey: rpc.ScriptPubKey{
									Address: watched,
								},
							},
						},
					},
				},
			},
			"block102": {
				Hash:   "block102",
				Height: 102,
				Time:   1_700_000_102,
				Tx: []*rpc.Transaction{
					{
						Txid:      "txc",
						Blockhash: "block102",
						Vin: []*rpc.Vin{
							{Coinbase: "coinbase"},
						},
						Vout: []*rpc.Vout{
							{
								N:     0,
								Value: json.Number("0.00001000"),
								ScriptPubKey: rpc.ScriptPubKey{
									Address: watched,
								},
							},
						},
					},
				},
			},
		},
		txsByID: map[string]*rpc.Transaction{
			"txa": {
				Txid:      "txa",
				Blockhash: "block100",
			},
			"prev1": {
				Txid: "prev1",
				Vout: []*rpc.Vout{
					{
						N:     0,
						Value: json.Number("0.00020000"),
						ScriptPubKey: rpc.ScriptPubKey{
							Address: watched,
						},
					},
				},
			},
		},
	}

	adapter := newTestAdapter(client)

	cursor := "txa"
	sigs, err := adapter.FetchNewSignaturesWithCutoff(context.Background(), watched, &cursor, 10, 101)
	require.NoError(t, err)
	require.Len(t, sigs, 1)
	assert.Equal(t, "txb", sigs[0].Hash)
	assert.Equal(t, int64(101), sigs[0].Sequence)
}

func TestAdapter_FetchTransactions_BuildsBTCEnvelopeDeterministically(t *testing.T) {
	client := &fakeRPCClient{
		blocksByHash: map[string]*rpc.Block{
			"block77": {
				Hash:   "block77",
				Height: 77,
			},
		},
		txsByID: map[string]*rpc.Transaction{
			"txspend": {
				Txid:          "txspend",
				Blockhash:     "block77",
				Blocktime:     1_700_000_777,
				Confirmations: 5,
				Vin: []*rpc.Vin{
					{Txid: "prevouttx", Vout: 0},
				},
				Vout: []*rpc.Vout{
					{
						N:     0,
						Value: json.Number("0.00009000"),
						ScriptPubKey: rpc.ScriptPubKey{
							Address: "tb1dest",
						},
					},
					{
						N:     1,
						Value: json.Number("0.00000900"),
						ScriptPubKey: rpc.ScriptPubKey{
							Address: "tb1change",
						},
					},
				},
			},
			"prevouttx": {
				Txid: "prevouttx",
				Vout: []*rpc.Vout{
					{
						N:     0,
						Value: json.Number("0.00010000"),
						ScriptPubKey: rpc.ScriptPubKey{
							Address: "tb1payer",
						},
					},
				},
			},
		},
	}

	adapter := newTestAdapter(client)

	payloads, err := adapter.FetchTransactions(context.Background(), []string{"txspend"})
	require.NoError(t, err)
	require.Len(t, payloads, 1)

	var decoded map[string]interface{}
	require.NoError(t, json.Unmarshal(payloads[0], &decoded))

	assert.Equal(t, "btc", decoded["chain"])
	assert.Equal(t, "txspend", decoded["txid"])
	assert.Equal(t, "100", decoded["fee_sat"])
	assert.Equal(t, "tb1payer", decoded["fee_payer"])

	vin, ok := decoded["vin"].([]interface{})
	require.True(t, ok)
	require.Len(t, vin, 1)
	vin0, ok := vin[0].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "tb1payer", vin0["address"])
	assert.Equal(t, "10000", vin0["value_sat"])
}

func TestAdapter_FetchTransactions_DeterministicFeePayerUnderMultiInputPermutations(t *testing.T) {
	client := &fakeRPCClient{
		blocksByHash: map[string]*rpc.Block{
			"block88": {
				Hash:   "block88",
				Height: 88,
			},
		},
		txsByID: map[string]*rpc.Transaction{
			"tx-multi-input": {
				Txid:      "tx-multi-input",
				Blockhash: "block88",
				Vin: []*rpc.Vin{
					{Txid: "prev-zeta", Vout: 0},
					{Txid: "prev-alpha", Vout: 0},
					{Txid: "prev-beta", Vout: 0},
					{Txid: "prev-gone", Vout: 0},
				},
				Vout: []*rpc.Vout{
					{
						N:            0,
						Value:        json.Number("0.00001000"),
						ScriptPubKey: rpc.ScriptPubKey{Address: "tb1change"},
					},
				},
			},
			"prev-zeta": {
				Txid: "prev-zeta",
				Vout: []*rpc.Vout{
					{
						N:            0,
						Value:        json.Number("0.00004000"),
						ScriptPubKey: rpc.ScriptPubKey{Address: "tb1payer-b"},
					},
				},
			},
			"prev-alpha": {
				Txid: "prev-alpha",
				Vout: []*rpc.Vout{
					{
						N:            0,
						Value:        json.Number("0.00003000"),
						ScriptPubKey: rpc.ScriptPubKey{Address: "tb1payer-a"},
					},
				},
			},
			"prev-beta": {
				Txid: "prev-beta",
				Vout: []*rpc.Vout{
					{
						N:            0,
						Value:        json.Number("0.00005000"),
						ScriptPubKey: rpc.ScriptPubKey{Address: "tb1payer-b"},
					},
				},
			},
		},
	}

	adapter := newTestAdapter(client)

	canonicalOrder, err := adapter.FetchTransactions(context.Background(), []string{"tx-multi-input"})
	replayedOrder, err2 := adapter.FetchTransactions(context.Background(), []string{"tx-multi-input"})
	require.NoError(t, err)
	require.NoError(t, err2)
	require.Len(t, canonicalOrder, 1)
	require.Len(t, replayedOrder, 1)

	canonicalEnvelope := decodeBTCEnvelope(t, canonicalOrder[0])
	replayedEnvelope := decodeBTCEnvelope(t, replayedOrder[0])
	assert.Equal(t, canonicalEnvelope, replayedEnvelope)

	assert.Equal(t, 4, len(canonicalEnvelope.Vin))
	assert.Equal(t, "tb1payer-a", canonicalEnvelope.FeePayer)

	assert.Equal(t, "4000", canonicalEnvelope.Vin[0].ValueSat)
	assert.Equal(t, "3000", canonicalEnvelope.Vin[1].ValueSat)
	assert.Equal(t, "5000", canonicalEnvelope.Vin[2].ValueSat)
	assert.Empty(t, canonicalEnvelope.Vin[3].ValueSat)

	assert.Equal(t, "11000", canonicalEnvelope.FeeSat)
}

func TestAdapter_FetchTransactions_DeterministicVoutOrdering_WhenIndexesOutOfOrder(t *testing.T) {
	client := &fakeRPCClient{
		blocksByHash: map[string]*rpc.Block{
			"block77": {
				Hash:   "block77",
				Height: 77,
			},
		},
		txsByID: map[string]*rpc.Transaction{
			"tx-vout-order": {
				Txid:      "tx-vout-order",
				Blockhash: "block77",
				Vout: []*rpc.Vout{
					{
						N:            2,
						Value:        json.Number("0.00030000"),
						ScriptPubKey: rpc.ScriptPubKey{Address: "tb1vout-2"},
					},
					{
						N:            0,
						Value:        json.Number("0.00010000"),
						ScriptPubKey: rpc.ScriptPubKey{Address: "tb1vout-0"},
					},
					{
						N:            1,
						Value:        json.Number("0.00020000"),
						ScriptPubKey: rpc.ScriptPubKey{Address: "tb1vout-1"},
					},
				},
			},
		},
	}

	adapter := newTestAdapter(client)

	payloads, err := adapter.FetchTransactions(context.Background(), []string{"tx-vout-order"})
	require.NoError(t, err)
	require.Len(t, payloads, 1)

	envelope := decodeBTCEnvelope(t, payloads[0])
	require.Len(t, envelope.Vout, 3)
	assert.Equal(t, 0, envelope.Vout[0].Index)
	assert.Equal(t, "10000", envelope.Vout[0].ValueSat)
	assert.Equal(t, "tb1vout-0", envelope.Vout[0].Address)

	assert.Equal(t, 1, envelope.Vout[1].Index)
	assert.Equal(t, "20000", envelope.Vout[1].ValueSat)
	assert.Equal(t, "tb1vout-1", envelope.Vout[1].Address)

	assert.Equal(t, 2, envelope.Vout[2].Index)
	assert.Equal(t, "30000", envelope.Vout[2].ValueSat)
	assert.Equal(t, "tb1vout-2", envelope.Vout[2].Address)
}

func TestAdapter_FetchNewSignaturesWithCutoff_AdaptiveBoundaryCarryoverDeterministic(t *testing.T) {
	const watched = "tb1watched"

	client := &fakeRPCClient{
		head: 12,
		blockHashByHeight: map[int64]string{
			10: "block10",
			11: "block11",
			12: "block12",
		},
		blocksByHash: map[string]*rpc.Block{
			"block10": {
				Hash:   "block10",
				Height: 10,
				Time:   1_700_000_010,
				Tx: []*rpc.Transaction{
					{
						Txid:      "tx-10-a",
						Blockhash: "block10",
						Vout: []*rpc.Vout{
							{
								N:            0,
								Value:        json.Number("0.00000100"),
								ScriptPubKey: rpc.ScriptPubKey{Address: watched},
							},
						},
					},
				},
			},
			"block11": {
				Hash:   "block11",
				Height: 11,
				Time:   1_700_000_011,
				Tx: []*rpc.Transaction{
					{
						Txid:      "tx-11-a",
						Blockhash: "block11",
						Vout: []*rpc.Vout{
							{
								N:            0,
								Value:        json.Number("0.00000200"),
								ScriptPubKey: rpc.ScriptPubKey{Address: watched},
							},
						},
					},
					{
						Txid:      "tx-11-b",
						Blockhash: "block11",
						Vout: []*rpc.Vout{
							{
								N:            0,
								Value:        json.Number("0.00000300"),
								ScriptPubKey: rpc.ScriptPubKey{Address: watched},
							},
						},
					},
				},
			},
			"block12": {
				Hash:   "block12",
				Height: 12,
				Time:   1_700_000_012,
				Tx: []*rpc.Transaction{
					{
						Txid:      "tx-12-a",
						Blockhash: "block12",
						Vout: []*rpc.Vout{
							{
								N:            0,
								Value:        json.Number("0.00000400"),
								ScriptPubKey: rpc.ScriptPubKey{Address: watched},
							},
						},
					},
				},
			},
		},
		txsByID: map[string]*rpc.Transaction{
			"tx-11-a": {
				Txid:      "tx-11-a",
				Blockhash: "block11",
			},
			"tx-12-a": {
				Txid:      "tx-12-a",
				Blockhash: "block12",
			},
		},
	}

	adapter := newTestAdapter(client)

	pageA, err := adapter.FetchNewSignaturesWithCutoff(context.Background(), watched, nil, 2, 12)
	require.NoError(t, err)
	require.Len(t, pageA, 2)

	pageAfirst := pageA[0].Hash
	pageAsecond := pageA[1].Hash
	assert.Equal(t, "tx-10-a", pageAfirst)
	assert.Equal(t, "tx-11-a", pageAsecond)

	restartCursor := pageA[1].Hash
	pageB, err := adapter.FetchNewSignaturesWithCutoff(context.Background(), watched, &restartCursor, 2, 12)
	require.NoError(t, err)
	require.Len(t, pageB, 2)
	assert.Equal(t, "tx-11-b", pageB[0].Hash)
	assert.Equal(t, "tx-12-a", pageB[1].Hash)

	restartCursor = pageB[1].Hash
	pageC, err := adapter.FetchNewSignaturesWithCutoff(context.Background(), watched, &restartCursor, 2, 12)
	require.NoError(t, err)
	require.Len(t, pageC, 0)
}

func TestAdapter_FetchNewSignaturesWithCutoff_CursorExclusiveAndRestart(t *testing.T) {
	const watched = "tb1watched"

	client := &fakeRPCClient{
		head: 101,
		blockHashByHeight: map[int64]string{
			100: "block100",
			101: "block101",
		},
		blocksByHash: map[string]*rpc.Block{
			"block100": {
				Hash:   "block100",
				Height: 100,
				Time:   1_700_000_100,
				Tx: []*rpc.Transaction{
					{
						Txid:      "tx-before",
						Blockhash: "block100",
						Vout: []*rpc.Vout{
							{N: 0, Value: json.Number("0.00100000"), ScriptPubKey: rpc.ScriptPubKey{Address: "tb1ignored"}},
						},
					},
					{
						Txid:      "tx-cursor",
						Blockhash: "block100",
						Vout: []*rpc.Vout{
							{N: 0, Value: json.Number("0.00005000"), ScriptPubKey: rpc.ScriptPubKey{Address: watched}},
						},
					},
					{
						Txid:      "tx-after-same-block",
						Blockhash: "block100",
						Vout: []*rpc.Vout{
							{N: 0, Value: json.Number("0.00007000"), ScriptPubKey: rpc.ScriptPubKey{Address: watched}},
						},
					},
				},
			},
			"block101": {
				Hash:   "block101",
				Height: 101,
				Time:   1_700_000_101,
				Tx: []*rpc.Transaction{
					{
						Txid:      "tx-after-next-block",
						Blockhash: "block101",
						Vout:      []*rpc.Vout{{N: 0, Value: json.Number("0.00003000"), ScriptPubKey: rpc.ScriptPubKey{Address: watched}}},
					},
				},
			},
		},
		txsByID: map[string]*rpc.Transaction{
			"tx-cursor": {
				Txid:      "tx-cursor",
				Blockhash: "block100",
			},
			"tx-after-same-block": {
				Txid:      "tx-after-same-block",
				Blockhash: "block100",
			},
		},
	}

	adapter := newTestAdapter(client)

	cursor := "tx-cursor"
	sigs, err := adapter.FetchNewSignaturesWithCutoff(context.Background(), watched, &cursor, 10, 101)
	require.NoError(t, err)
	require.Len(t, sigs, 2)
	assert.Equal(t, "tx-after-same-block", sigs[0].Hash)
	assert.Equal(t, int64(100), sigs[0].Sequence)
	assert.Equal(t, "tx-after-next-block", sigs[1].Hash)
	assert.Equal(t, int64(101), sigs[1].Sequence)

	restartCursor := sigs[0].Hash
	replayed, err := adapter.FetchNewSignaturesWithCutoff(context.Background(), watched, &restartCursor, 10, 101)
	require.NoError(t, err)
	require.Len(t, replayed, 1)
	assert.Equal(t, "tx-after-next-block", replayed[0].Hash)
	assert.Equal(t, int64(101), replayed[0].Sequence)
}

func TestAdapter_FetchNewSignaturesWithCutoff_UnknownCursorIndexFallsToNextSlot(t *testing.T) {
	const watched = "tb1watched"

	client := &fakeRPCClient{
		head: 101,
		blockHashByHeight: map[int64]string{
			100: "block100",
			101: "block101",
		},
		blocksByHash: map[string]*rpc.Block{
			"block100": {
				Hash:   "block100",
				Height: 100,
				Time:   1_700_000_100,
				Tx: []*rpc.Transaction{
					{
						Txid:      "tx-before",
						Blockhash: "block100",
						Vout: []*rpc.Vout{
							{
								N:            0,
								Value:        json.Number("0.00001000"),
								ScriptPubKey: rpc.ScriptPubKey{Address: watched},
							},
						},
					},
				},
			},
			"block101": {
				Hash:   "block101",
				Height: 101,
				Time:   1_700_000_101,
				Tx: []*rpc.Transaction{
					{
						Txid:      "tx-after",
						Blockhash: "block101",
						Vout: []*rpc.Vout{
							{
								N:            0,
								Value:        json.Number("0.00002000"),
								ScriptPubKey: rpc.ScriptPubKey{Address: watched},
							},
						},
					},
				},
			},
		},
		txsByID: map[string]*rpc.Transaction{
			"cursor-missing-index": {
				Txid:      "cursor-missing-index",
				Blockhash: "block100",
			},
		},
	}

	adapter := newTestAdapter(client)

	cursor := "cursor-missing-index"
	sigs, err := adapter.FetchNewSignaturesWithCutoff(context.Background(), watched, &cursor, 10, 101)
	require.NoError(t, err)
	require.Len(t, sigs, 1)
	assert.Equal(t, "tx-after", sigs[0].Hash)
	assert.Equal(t, int64(101), sigs[0].Sequence)
}

func TestAdapter_FetchNewSignaturesWithCutoff_BatchChunkingDeterministic(t *testing.T) {
	const watched = "tb1watched"

	client := &fakeRPCClient{
		head: 11,
		blockHashByHeight: map[int64]string{
			10: "block10",
			11: "block11",
		},
		blocksByHash: map[string]*rpc.Block{
			"block10": {
				Hash:   "block10",
				Height: 10,
				Time:   1_700_000_010,
				Tx: []*rpc.Transaction{
					{
						Txid:      "tx-10-a",
						Blockhash: "block10",
						Vout: []*rpc.Vout{
							{
								N:            0,
								Value:        json.Number("0.00001000"),
								ScriptPubKey: rpc.ScriptPubKey{Address: watched},
							},
						},
					},
					{
						Txid:      "tx-10-b",
						Blockhash: "block10",
						Vout: []*rpc.Vout{
							{
								N:            0,
								Value:        json.Number("0.00002000"),
								ScriptPubKey: rpc.ScriptPubKey{Address: watched},
							},
						},
					},
					{
						Txid:      "tx-10-c",
						Blockhash: "block10",
						Vout: []*rpc.Vout{
							{
								N:            0,
								Value:        json.Number("0.00003000"),
								ScriptPubKey: rpc.ScriptPubKey{Address: watched},
							},
						},
					},
					{
						Txid:      "tx-10-d",
						Blockhash: "block10",
						Vout: []*rpc.Vout{
							{
								N:            0,
								Value:        json.Number("0.00004000"),
								ScriptPubKey: rpc.ScriptPubKey{Address: watched},
							},
						},
					},
				},
			},
			"block11": {
				Hash:   "block11",
				Height: 11,
				Time:   1_700_000_011,
				Tx: []*rpc.Transaction{
					{
						Txid:      "tx-11-a",
						Blockhash: "block11",
						Vout: []*rpc.Vout{
							{
								N:            0,
								Value:        json.Number("0.00005000"),
								ScriptPubKey: rpc.ScriptPubKey{Address: watched},
							},
						},
					},
				},
			},
		},
		txsByID: map[string]*rpc.Transaction{
			"tx-10-b": {
				Txid:      "tx-10-b",
				Blockhash: "block10",
			},
			"tx-10-d": {
				Txid:      "tx-10-d",
				Blockhash: "block10",
			},
		},
	}

	adapter := newTestAdapter(client)

	firstPage, err := adapter.FetchNewSignaturesWithCutoff(context.Background(), watched, nil, 2, 11)
	require.NoError(t, err)
	require.Len(t, firstPage, 2)
	assert.Equal(t, "tx-10-a", firstPage[0].Hash)
	assert.Equal(t, int64(10), firstPage[0].Sequence)
	assert.Equal(t, "tx-10-b", firstPage[1].Hash)
	assert.Equal(t, int64(10), firstPage[1].Sequence)

	restartCursor := firstPage[1].Hash
	secondPage, err := adapter.FetchNewSignaturesWithCutoff(context.Background(), watched, &restartCursor, 2, 11)
	require.NoError(t, err)
	require.Len(t, secondPage, 2)
	assert.Equal(t, "tx-10-c", secondPage[0].Hash)
	assert.Equal(t, int64(10), secondPage[0].Sequence)
	assert.Equal(t, "tx-10-d", secondPage[1].Hash)
	assert.Equal(t, int64(10), secondPage[1].Sequence)

	restartCursor = secondPage[1].Hash
	thirdPage, err := adapter.FetchNewSignaturesWithCutoff(context.Background(), watched, &restartCursor, 2, 11)
	require.NoError(t, err)
	require.Len(t, thirdPage, 1)
	assert.Equal(t, "tx-11-a", thirdPage[0].Hash)
	assert.Equal(t, int64(11), thirdPage[0].Sequence)
}

func TestAdapter_FetchTransactions_ResolvesPrevoutsDeterministically_WhenMissingOrMalformed(t *testing.T) {
	client := &fakeRPCClient{
		blocksByHash: map[string]*rpc.Block{
			"block99": {
				Hash:   "block99",
				Height: 99,
			},
		},
		txsByID: map[string]*rpc.Transaction{
			"tx-spend": {
				Txid:      "tx-spend",
				Blockhash: "block99",
				Vin: []*rpc.Vin{
					{Txid: "prev-good", Vout: 0},
					{Txid: "prev-missing", Vout: 0},
					{Txid: "prev-malformed", Vout: 0},
				},
				Vout: []*rpc.Vout{
					{
						N:            0,
						Value:        json.Number("0.00012000"),
						ScriptPubKey: rpc.ScriptPubKey{Address: "tb1receiver"},
					},
				},
			},
			"prev-good": {
				Txid: "prev-good",
				Vout: []*rpc.Vout{
					{
						N:            0,
						Value:        json.Number("0.00050000"),
						ScriptPubKey: rpc.ScriptPubKey{Address: "tb1payer-c"},
					},
				},
			},
			"prev-malformed": {
				Txid: "prev-malformed",
				Vout: []*rpc.Vout{
					{
						N:            0,
						Value:        json.Number("not-a-number"),
						ScriptPubKey: rpc.ScriptPubKey{Address: "tb1payer-a"},
					},
				},
			},
		},
	}

	adapter := newTestAdapter(client)

	payloads, err := adapter.FetchTransactions(context.Background(), []string{"tx-spend"})
	require.NoError(t, err)
	require.Len(t, payloads, 1)

	envelope := decodeBTCEnvelope(t, payloads[0])
	require.Len(t, envelope.Vin, 3)
	assert.Equal(t, "tb1payer-c", envelope.Vin[0].Address)
	assert.Equal(t, "50000", envelope.Vin[0].ValueSat)
	assert.Empty(t, envelope.Vin[1].Address)
	assert.Empty(t, envelope.Vin[1].ValueSat)
	assert.Equal(t, "tb1payer-a", envelope.Vin[2].Address)
	assert.Equal(t, "0", envelope.Vin[2].ValueSat)

	assert.Equal(t, "38000", envelope.FeeSat)
	assert.Equal(t, "tb1payer-a", envelope.FeePayer)
}

func TestAdapter_FetchTransactions_EventIdentityStableUnderReplayPermutation(t *testing.T) {
	client := &fakeRPCClient{
		blocksByHash: map[string]*rpc.Block{
			"block88": {
				Hash:   "block88",
				Height: 88,
			},
		},
		txsByID: map[string]*rpc.Transaction{
			"tx-one": {
				Txid:      "tx-one",
				Blockhash: "block88",
				Vin: []*rpc.Vin{
					{Txid: "prev-one", Vout: 0},
					{Txid: "prev-two", Vout: 0},
				},
				Vout: []*rpc.Vout{
					{
						N:            0,
						Value:        json.Number("0.00001000"),
						ScriptPubKey: rpc.ScriptPubKey{Address: "tb1watch-1"},
					},
					{
						N:            1,
						Value:        json.Number("0.00000500"),
						ScriptPubKey: rpc.ScriptPubKey{Address: "tb1watch-2"},
					},
				},
			},
			"tx-two": {
				Txid:      "tx-two",
				Blockhash: "block88",
				Vin: []*rpc.Vin{
					{Txid: "prev-two", Vout: 0},
				},
				Vout: []*rpc.Vout{
					{
						N:            0,
						Value:        json.Number("0.00002000"),
						ScriptPubKey: rpc.ScriptPubKey{Address: "tb1watch-3"},
					},
				},
			},
			"prev-one": {
				Txid: "prev-one",
				Vout: []*rpc.Vout{
					{
						N:            0,
						Value:        json.Number("0.00003000"),
						ScriptPubKey: rpc.ScriptPubKey{Address: "tb1payer-1"},
					},
				},
			},
			"prev-two": {
				Txid: "prev-two",
				Vout: []*rpc.Vout{
					{
						N:            0,
						Value:        json.Number("0.00004000"),
						ScriptPubKey: rpc.ScriptPubKey{Address: "tb1payer-2"},
					},
				},
			},
		},
	}

	adapter := newTestAdapter(client)

	canonicalOrder, err := adapter.FetchTransactions(context.Background(), []string{"tx-one", "tx-two"})
	require.NoError(t, err)
	require.Len(t, canonicalOrder, 2)

	replayedOrder, err := adapter.FetchTransactions(context.Background(), []string{"tx-two", "tx-one"})
	require.NoError(t, err)
	require.Len(t, replayedOrder, 2)

	canonicalIDs := make([]string, 0, len(canonicalOrder)*2)
	for _, raw := range canonicalOrder {
		envelope := decodeBTCEnvelope(t, raw)
		canonicalIDs = append(canonicalIDs, collectBTCEventIDs(envelope)...)
	}

	replayedIDs := make([]string, 0, len(replayedOrder)*2)
	for _, raw := range replayedOrder {
		envelope := decodeBTCEnvelope(t, raw)
		replayedIDs = append(replayedIDs, collectBTCEventIDs(envelope)...)
	}

	sort.Strings(canonicalIDs)
	sort.Strings(replayedIDs)
	assert.Equal(t, canonicalIDs, replayedIDs)

	unique := map[string]int{}
	for _, id := range canonicalIDs {
		unique[id]++
	}
	for _, id := range canonicalIDs {
		assert.Equal(t, 1, unique[id])
	}

	hasVin := false
	hasVout := false
	hasFee := false
	for _, id := range canonicalIDs {
		switch {
		case strings.Contains(id, "|class=TRANSFER:vin|"):
			hasVin = true
		case strings.Contains(id, "|class=TRANSFER:vout|"):
			hasVout = true
		case strings.Contains(id, "|class=FEE:miner_fee|"):
			hasFee = true
		}
	}

	assert.True(t, hasVin, "expected deterministic event id coverage for TRANSFER:vin")
	assert.True(t, hasVout, "expected deterministic event id coverage for TRANSFER:vout")
	assert.True(t, hasFee, "expected deterministic event id coverage for miner_fee")
}

type btcEnvelope struct {
	Txid          string          `json:"txid"`
	FeeSat        string          `json:"fee_sat"`
	FeePayer      string          `json:"fee_payer"`
	Vin           []txEnvelopeIn  `json:"vin"`
	Vout          []txEnvelopeOut `json:"vout"`
	Chain         string          `json:"chain"`
	BlockHeight   int64           `json:"block_height"`
	BlockHash     string          `json:"block_hash,omitempty"`
	BlockTime     int64           `json:"block_time,omitempty"`
	Confirmations int64           `json:"confirmations,omitempty"`
}

func decodeBTCEnvelope(t *testing.T, payload json.RawMessage) btcEnvelope {
	var envelope btcEnvelope
	require.NoError(t, json.Unmarshal(payload, &envelope))
	return envelope
}

func collectBTCEventIDs(envelope btcEnvelope) []string {
	eventIDs := make([]string, 0, len(envelope.Vin)+len(envelope.Vout)+1)
	for _, vin := range envelope.Vin {
		eventIDs = append(eventIDs, fmt.Sprintf("chain=btc|tx=%s|class=TRANSFER:vin|path=vin:%d|actor=%s|asset=BTC", envelope.Txid, vin.Index, vin.Txid))
	}
	for _, vout := range envelope.Vout {
		eventIDs = append(eventIDs, fmt.Sprintf("chain=btc|tx=%s|class=TRANSFER:vout|path=vout:%d|actor=%s|asset=BTC", envelope.Txid, vout.Index, vout.Address))
	}
	if strings.TrimSpace(envelope.FeeSat) != "" && envelope.FeeSat != "0" && envelope.FeePayer != "" {
		eventIDs = append(
			eventIDs,
			fmt.Sprintf("chain=btc|tx=%s|class=FEE:miner_fee|path=fee:miner|actor=%s|asset=BTC|delta=%s", envelope.Txid, envelope.FeePayer, envelope.FeeSat),
		)
	}
	return eventIDs
}
