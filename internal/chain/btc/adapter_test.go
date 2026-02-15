package btc

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
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

func (f *fakeRPCClient) GetRawTransactionVerbose(_ context.Context, txid string) (*rpc.Transaction, error) {
	if f.txErr != nil {
		return nil, f.txErr
	}
	return f.txsByID[canonicalTxID(txid)], nil
}

func newTestAdapter(client rpc.RPCClient) *Adapter {
	return &Adapter{
		client: client,
		logger: slog.Default(),
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
