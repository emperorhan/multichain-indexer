package base

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	"github.com/emperorhan/multichain-indexer/internal/chain/base/rpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeRPCClient struct {
	head      int64
	blocks    map[int64]*rpc.Block
	txs       map[string]*rpc.Transaction
	receipts  map[string]*rpc.TransactionReceipt
	headErr   error
	blockErr  error
	txErr     error
	receiptErr error
}

func (f *fakeRPCClient) GetBlockNumber(_ context.Context) (int64, error) {
	if f.headErr != nil {
		return 0, f.headErr
	}
	return f.head, nil
}

func (f *fakeRPCClient) GetBlockByNumber(_ context.Context, blockNumber int64, _ bool) (*rpc.Block, error) {
	if f.blockErr != nil {
		return nil, f.blockErr
	}
	return f.blocks[blockNumber], nil
}

func (f *fakeRPCClient) GetTransactionByHash(_ context.Context, hash string) (*rpc.Transaction, error) {
	if f.txErr != nil {
		return nil, f.txErr
	}
	return f.txs[hash], nil
}

func (f *fakeRPCClient) GetTransactionReceipt(_ context.Context, hash string) (*rpc.TransactionReceipt, error) {
	if f.receiptErr != nil {
		return nil, f.receiptErr
	}
	return f.receipts[hash], nil
}

func newTestAdapter(client rpc.RPCClient) *Adapter {
	return &Adapter{
		client: client,
		logger: slog.Default(),
	}
}

func TestAdapter_Chain(t *testing.T) {
	a := newTestAdapter(&fakeRPCClient{})
	assert.Equal(t, "base", a.Chain())
}

func TestAdapter_GetHeadSequence(t *testing.T) {
	a := newTestAdapter(&fakeRPCClient{head: 123})
	seq, err := a.GetHeadSequence(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(123), seq)
}

func TestAdapter_FetchNewSignatures_WithCursor(t *testing.T) {
	watched := "0xabc"
	cursorHash := "0xcursor"

	client := &fakeRPCClient{
		head: 12,
		txs: map[string]*rpc.Transaction{
			cursorHash: {
				Hash:             cursorHash,
				BlockNumber:      "0xa",
				TransactionIndex: "0x1",
			},
		},
		blocks: map[int64]*rpc.Block{
			10: {
				Number:    "0xa",
				Timestamp: "0x64",
				Transactions: []*rpc.Transaction{
					{Hash: "0xold", TransactionIndex: "0x0", From: watched, To: "0xdef"},
					{Hash: cursorHash, TransactionIndex: "0x1", From: watched, To: "0xdef"},
					{Hash: "0xnew1", TransactionIndex: "0x2", From: watched, To: "0xdef"},
				},
			},
			11: {
				Number:    "0xb",
				Timestamp: "0x65",
				Transactions: []*rpc.Transaction{
					{Hash: "0xnew2", TransactionIndex: "0x0", From: "0xdef", To: watched},
				},
			},
			12: {
				Number:    "0xc",
				Timestamp: "0x66",
				Transactions: []*rpc.Transaction{
					{Hash: "0xignore", TransactionIndex: "0x0", From: "0xdef", To: "0x123"},
				},
			},
		},
	}
	a := newTestAdapter(client)

	sigs, err := a.FetchNewSignatures(context.Background(), watched, &cursorHash, 10)
	require.NoError(t, err)
	require.Len(t, sigs, 2)

	assert.Equal(t, "0xnew1", sigs[0].Hash)
	assert.Equal(t, int64(10), sigs[0].Sequence)
	assert.Equal(t, "0xnew2", sigs[1].Hash)
	assert.Equal(t, int64(11), sigs[1].Sequence)
}

func TestAdapter_FetchTransactions(t *testing.T) {
	client := &fakeRPCClient{
		txs: map[string]*rpc.Transaction{
			"0x1": {
				Hash:             "0x1",
				BlockNumber:      "0x10",
				TransactionIndex: "0x0",
				From:             "0xaaa",
				To:               "0xbbb",
				Value:            "0x1",
				GasPrice:         "0x10",
			},
			"0x2": {
				Hash:             "0x2",
				BlockNumber:      "0x11",
				TransactionIndex: "0x1",
				From:             "0xbbb",
				To:               "0xaaa",
				Value:            "0x2",
				GasPrice:         "0x20",
			},
		},
		receipts: map[string]*rpc.TransactionReceipt{
			"0x1": {
				TransactionHash:   "0x1",
				BlockNumber:       "0x10",
				TransactionIndex:  "0x0",
				Status:            "0x1",
				From:              "0xaaa",
				To:                "0xbbb",
				GasUsed:           "0x5208",
				EffectiveGasPrice: "0x10",
			},
			"0x2": {
				TransactionHash:   "0x2",
				BlockNumber:       "0x11",
				TransactionIndex:  "0x1",
				Status:            "0x1",
				From:              "0xbbb",
				To:                "0xaaa",
				GasUsed:           "0x5208",
				EffectiveGasPrice: "0x20",
			},
		},
	}
	a := newTestAdapter(client)

	payloads, err := a.FetchTransactions(context.Background(), []string{"0x1", "0x2"})
	require.NoError(t, err)
	require.Len(t, payloads, 2)

	var decoded map[string]interface{}
	require.NoError(t, json.Unmarshal(payloads[0], &decoded))
	assert.Equal(t, "base", decoded["chain"])

	txMap, ok := decoded["tx"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "0x1", txMap["hash"])

	receiptMap, ok := decoded["receipt"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "0x1", receiptMap["transactionHash"])
}

func TestAdapter_FetchTransactions_Error(t *testing.T) {
	client := &fakeRPCClient{
		txErr: errors.New("rpc unavailable"),
	}
	a := newTestAdapter(client)

	_, err := a.FetchTransactions(context.Background(), []string{"0x1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rpc unavailable")
}
