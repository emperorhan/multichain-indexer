package solana

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	"github.com/emperorhan/multichain-indexer/internal/chain/solana/rpc"
	rpcmocks "github.com/emperorhan/multichain-indexer/internal/chain/solana/rpc/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func newTestAdapter(ctrl *gomock.Controller) (*Adapter, *rpcmocks.MockRPCClient) {
	mockClient := rpcmocks.NewMockRPCClient(ctrl)
	adapter := &Adapter{
		client:           mockClient,
		logger:           slog.Default(),
		maxPageSize:      maxPageSize,
		maxConcurrentTxs: maxConcurrentTxs,
		headCommitment:   "confirmed",
	}
	return adapter, mockClient
}

func TestAdapter_RPCClientContractParity(t *testing.T) {
	t.Parallel()

	var _ rpc.RPCClient = (*rpc.Client)(nil)
	var _ rpc.RPCClient = (*rpcmocks.MockRPCClient)(nil)
}

func TestAdapter_Chain(t *testing.T) {
	ctrl := gomock.NewController(t)
	adapter, _ := newTestAdapter(ctrl)
	assert.Equal(t, "solana", adapter.Chain())
}

func TestAdapter_GetHeadSequence(t *testing.T) {
	ctrl := gomock.NewController(t)
	adapter, mockClient := newTestAdapter(ctrl)

	mockClient.EXPECT().
		GetSlot(gomock.Any(), "confirmed").
		Return(int64(123456), nil)

	slot, err := adapter.GetHeadSequence(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(123456), slot)
}

func TestAdapter_GetHeadSequence_Error(t *testing.T) {
	ctrl := gomock.NewController(t)
	adapter, mockClient := newTestAdapter(ctrl)

	mockClient.EXPECT().
		GetSlot(gomock.Any(), "confirmed").
		Return(int64(0), errors.New("rpc error"))

	_, err := adapter.GetHeadSequence(context.Background())
	require.Error(t, err)
}

func TestAdapter_FetchNewSignatures_HappyPath(t *testing.T) {
	ctrl := gomock.NewController(t)
	adapter, mockClient := newTestAdapter(ctrl)

	bt1 := int64(1000)
	bt2 := int64(1001)
	bt3 := int64(1002)

	// RPC returns newest-first
	mockClient.EXPECT().
		GetSignaturesForAddress(gomock.Any(), "addr1", gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, opts *rpc.GetSignaturesOpts) ([]rpc.SignatureInfo, error) {
			assert.Equal(t, 100, opts.Limit)
			return []rpc.SignatureInfo{
				{Signature: "sig3", Slot: 300, BlockTime: &bt3},
				{Signature: "sig2", Slot: 200, BlockTime: &bt2},
				{Signature: "sig1", Slot: 100, BlockTime: &bt1},
			}, nil
		})

	sigs, err := adapter.FetchNewSignatures(context.Background(), "addr1", nil, 100)
	require.NoError(t, err)
	require.Len(t, sigs, 3)

	// Should be oldest-first
	assert.Equal(t, "sig1", sigs[0].Hash)
	assert.Equal(t, int64(100), sigs[0].Sequence)
	assert.Equal(t, "sig2", sigs[1].Hash)
	assert.Equal(t, int64(200), sigs[1].Sequence)
	assert.Equal(t, "sig3", sigs[2].Hash)
	assert.Equal(t, int64(300), sigs[2].Sequence)
}

func TestAdapter_FetchNewSignatures_WithCursor(t *testing.T) {
	ctrl := gomock.NewController(t)
	adapter, mockClient := newTestAdapter(ctrl)

	cursor := "lastSig"

	mockClient.EXPECT().
		GetSignaturesForAddress(gomock.Any(), "addr1", gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, opts *rpc.GetSignaturesOpts) ([]rpc.SignatureInfo, error) {
			assert.Equal(t, "lastSig", opts.Until)
			return []rpc.SignatureInfo{}, nil
		})

	sigs, err := adapter.FetchNewSignatures(context.Background(), "addr1", &cursor, 100)
	require.NoError(t, err)
	assert.Empty(t, sigs)
}

func TestAdapter_FetchNewSignatures_Pagination(t *testing.T) {
	ctrl := gomock.NewController(t)
	adapter, mockClient := newTestAdapter(ctrl)

	// batchSize = 1500 → first page 1000, second page 500
	gomock.InOrder(
		mockClient.EXPECT().
			GetSignaturesForAddress(gomock.Any(), "addr1", gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, opts *rpc.GetSignaturesOpts) ([]rpc.SignatureInfo, error) {
				assert.Equal(t, 1000, opts.Limit)
				assert.Empty(t, opts.Before)
				sigs := make([]rpc.SignatureInfo, 1000)
				for i := range sigs {
					sigs[i] = rpc.SignatureInfo{Signature: "sig_page1", Slot: int64(2000 - i)}
				}
				return sigs, nil
			}),
		mockClient.EXPECT().
			GetSignaturesForAddress(gomock.Any(), "addr1", gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, opts *rpc.GetSignaturesOpts) ([]rpc.SignatureInfo, error) {
				assert.Equal(t, 500, opts.Limit)
				assert.Equal(t, "sig_page1", opts.Before) // before = last sig from page 1
				return []rpc.SignatureInfo{
					{Signature: "sig_page2", Slot: 500},
				}, nil
			}),
	)

	sigs, err := adapter.FetchNewSignatures(context.Background(), "addr1", nil, 1500)
	require.NoError(t, err)
	assert.Len(t, sigs, 1001)
}

func TestAdapter_FetchNewSignaturesWithCutoff_HeadAdvanceDuringPagination(t *testing.T) {
	ctrl := gomock.NewController(t)
	adapter, mockClient := newTestAdapter(ctrl)

	gomock.InOrder(
		mockClient.EXPECT().
			GetSignaturesForAddress(gomock.Any(), "addr1", gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, opts *rpc.GetSignaturesOpts) ([]rpc.SignatureInfo, error) {
				assert.Equal(t, 3, opts.Limit)
				assert.Empty(t, opts.Before)
				return []rpc.SignatureInfo{
					{Signature: "sig106", Slot: 106},
					{Signature: "sig105", Slot: 105},
					{Signature: "sig104", Slot: 104},
				}, nil
			}),
		mockClient.EXPECT().
			GetSignaturesForAddress(gomock.Any(), "addr1", gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, opts *rpc.GetSignaturesOpts) ([]rpc.SignatureInfo, error) {
				assert.Equal(t, 3, opts.Limit)
				assert.Equal(t, "sig104", opts.Before)
				return []rpc.SignatureInfo{
					{Signature: "sig103", Slot: 103},
					{Signature: "sig102", Slot: 102},
					{Signature: "sig101", Slot: 101},
				}, nil
			}),
	)

	sigs, err := adapter.FetchNewSignaturesWithCutoff(context.Background(), "addr1", nil, 3, 103)
	require.NoError(t, err)
	require.Len(t, sigs, 3)
	assert.Equal(t, []string{"sig101", "sig102", "sig103"}, []string{sigs[0].Hash, sigs[1].Hash, sigs[2].Hash})
	assert.Equal(t, []int64{101, 102, 103}, []int64{sigs[0].Sequence, sigs[1].Sequence, sigs[2].Sequence})
}

func TestAdapter_FetchNewSignatures_EmptyResult(t *testing.T) {
	ctrl := gomock.NewController(t)
	adapter, mockClient := newTestAdapter(ctrl)

	mockClient.EXPECT().
		GetSignaturesForAddress(gomock.Any(), "addr1", gomock.Any()).
		Return([]rpc.SignatureInfo{}, nil)

	sigs, err := adapter.FetchNewSignatures(context.Background(), "addr1", nil, 100)
	require.NoError(t, err)
	assert.Empty(t, sigs)
}

func TestAdapter_FetchNewSignatures_RPCError(t *testing.T) {
	ctrl := gomock.NewController(t)
	adapter, mockClient := newTestAdapter(ctrl)

	mockClient.EXPECT().
		GetSignaturesForAddress(gomock.Any(), "addr1", gomock.Any()).
		Return(nil, errors.New("connection refused"))

	_, err := adapter.FetchNewSignatures(context.Background(), "addr1", nil, 100)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connection refused")
}

func TestAdapter_FetchTransactions_HappyPath(t *testing.T) {
	ctrl := gomock.NewController(t)
	adapter, mockClient := newTestAdapter(ctrl)

	sigs := []string{"sig1", "sig2", "sig3"}

	mockClient.EXPECT().
		GetTransaction(gomock.Any(), "sig1").
		Return(json.RawMessage(`{"slot":100}`), nil)
	mockClient.EXPECT().
		GetTransaction(gomock.Any(), "sig2").
		Return(json.RawMessage(`{"slot":200}`), nil)
	mockClient.EXPECT().
		GetTransaction(gomock.Any(), "sig3").
		Return(json.RawMessage(`{"slot":300}`), nil)

	results, err := adapter.FetchTransactions(context.Background(), sigs)
	require.NoError(t, err)
	require.Len(t, results, 3)
	// Order is preserved by index
	assert.JSONEq(t, `{"slot":100}`, string(results[0]))
	assert.JSONEq(t, `{"slot":200}`, string(results[1]))
	assert.JSONEq(t, `{"slot":300}`, string(results[2]))
}

func TestAdapter_FetchTransactions_Error(t *testing.T) {
	ctrl := gomock.NewController(t)
	adapter, mockClient := newTestAdapter(ctrl)

	mockClient.EXPECT().
		GetTransaction(gomock.Any(), "sig1").
		Return(nil, errors.New("tx not found"))
	mockClient.EXPECT().
		GetTransaction(gomock.Any(), "sig2").
		Return(json.RawMessage(`{}`), nil).AnyTimes()

	_, err := adapter.FetchTransactions(context.Background(), []string{"sig1", "sig2"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tx not found")
}

func TestAdapter_FetchTransactions_EmptySignatures(t *testing.T) {
	ctrl := gomock.NewController(t)
	adapter, _ := newTestAdapter(ctrl)

	results, err := adapter.FetchTransactions(context.Background(), []string{})
	require.NoError(t, err)
	assert.Empty(t, results)
}

type batchCapableClient struct {
	results      []json.RawMessage
	batchCalled  bool
	singleCalled bool
}

func (b *batchCapableClient) GetSlot(context.Context, string) (int64, error) {
	return 0, nil
}

func (b *batchCapableClient) GetSignaturesForAddress(context.Context, string, *rpc.GetSignaturesOpts) ([]rpc.SignatureInfo, error) {
	return []rpc.SignatureInfo{}, nil
}

func (b *batchCapableClient) GetTransaction(context.Context, string) (json.RawMessage, error) {
	b.singleCalled = true
	return nil, errors.New("single path should not be used")
}

func (b *batchCapableClient) GetTransactions(_ context.Context, _ []string) ([]json.RawMessage, error) {
	b.batchCalled = true
	return b.results, nil
}

func (b *batchCapableClient) GetBlock(context.Context, int64, *rpc.GetBlockOpts) (*rpc.BlockResult, error) {
	return nil, nil
}

func TestAdapter_FetchTransactions_BatchPreferred(t *testing.T) {
	client := &batchCapableClient{
		results: []json.RawMessage{
			json.RawMessage(`{"slot":100}`),
			json.RawMessage(`{"slot":200}`),
		},
	}
	adapter := &Adapter{
		client:           client,
		logger:           slog.Default(),
		maxPageSize:      maxPageSize,
		maxConcurrentTxs: maxConcurrentTxs,
	}

	results, err := adapter.FetchTransactions(context.Background(), []string{"sig1", "sig2"})
	require.NoError(t, err)
	require.Len(t, results, 2)
	assert.True(t, client.batchCalled)
	assert.False(t, client.singleCalled)
}

// --- ScanBlocks tests ---

func makeBlockTx(sig string, accountKeys []string, preOwners, postOwners []string) rpc.BlockTransaction {
	keysJSON := "["
	for i, k := range accountKeys {
		if i > 0 {
			keysJSON += ","
		}
		keysJSON += `"` + k + `"`
	}
	keysJSON += "]"

	txJSON := `{"signatures":["` + sig + `"],"message":{"accountKeys":` + keysJSON + `}}`

	preBal := "["
	for i, o := range preOwners {
		if i > 0 {
			preBal += ","
		}
		preBal += `{"owner":"` + o + `","accountIndex":0,"mint":"m","uiTokenAmount":{"uiAmount":1,"decimals":6,"amount":"1000000"},"programId":"p"}`
	}
	preBal += "]"

	postBal := "["
	for i, o := range postOwners {
		if i > 0 {
			postBal += ","
		}
		postBal += `{"owner":"` + o + `","accountIndex":0,"mint":"m","uiTokenAmount":{"uiAmount":1,"decimals":6,"amount":"1000000"},"programId":"p"}`
	}
	postBal += "]"

	metaJSON := `{"preTokenBalances":` + preBal + `,"postTokenBalances":` + postBal + `}`

	return rpc.BlockTransaction{
		Transaction: json.RawMessage(txJSON),
		Meta:        json.RawMessage(metaJSON),
	}
}

func TestAdapter_ScanBlocks_HappyPath(t *testing.T) {
	ctrl := gomock.NewController(t)
	adapter, mockClient := newTestAdapter(ctrl)

	bt := int64(1700000000)
	// Slot 100: tx with watched address in accountKeys
	mockClient.EXPECT().GetBlock(gomock.Any(), int64(100), gomock.Any()).Return(&rpc.BlockResult{
		Blockhash: "hash100",
		BlockTime: &bt,
		Transactions: []rpc.BlockTransaction{
			makeBlockTx("sig100a", []string{"watched1", "other"}, nil, nil),
			makeBlockTx("sig100b", []string{"other1", "other2"}, nil, nil),
		},
	}, nil)

	// Slot 101: tx with watched address in postTokenBalances owner
	mockClient.EXPECT().GetBlock(gomock.Any(), int64(101), gomock.Any()).Return(&rpc.BlockResult{
		Blockhash: "hash101",
		BlockTime: &bt,
		Transactions: []rpc.BlockTransaction{
			makeBlockTx("sig101a", []string{"ata_addr"}, nil, []string{"watched2"}),
		},
	}, nil)

	// Slot 102: no watched addresses
	mockClient.EXPECT().GetBlock(gomock.Any(), int64(102), gomock.Any()).Return(&rpc.BlockResult{
		Blockhash: "hash102",
		BlockTime: &bt,
		Transactions: []rpc.BlockTransaction{
			makeBlockTx("sig102a", []string{"unrelated"}, nil, nil),
		},
	}, nil)

	sigs, err := adapter.ScanBlocks(context.Background(), 100, 102, []string{"watched1", "watched2"})
	require.NoError(t, err)
	require.Len(t, sigs, 2)
	assert.Equal(t, "sig100a", sigs[0].Hash)
	assert.Equal(t, int64(100), sigs[0].Sequence)
	assert.Equal(t, "sig101a", sigs[1].Hash)
	assert.Equal(t, int64(101), sigs[1].Sequence)
}

func TestAdapter_ScanBlocks_SkippedSlots(t *testing.T) {
	ctrl := gomock.NewController(t)
	adapter, mockClient := newTestAdapter(ctrl)

	bt := int64(1700000000)
	// Slot 100: skipped (nil)
	mockClient.EXPECT().GetBlock(gomock.Any(), int64(100), gomock.Any()).Return(nil, nil)
	// Slot 101: has a tx
	mockClient.EXPECT().GetBlock(gomock.Any(), int64(101), gomock.Any()).Return(&rpc.BlockResult{
		Blockhash: "hash101",
		BlockTime: &bt,
		Transactions: []rpc.BlockTransaction{
			makeBlockTx("sig101", []string{"watched1"}, nil, nil),
		},
	}, nil)
	// Slot 102: skipped
	mockClient.EXPECT().GetBlock(gomock.Any(), int64(102), gomock.Any()).Return(nil, nil)

	sigs, err := adapter.ScanBlocks(context.Background(), 100, 102, []string{"watched1"})
	require.NoError(t, err)
	require.Len(t, sigs, 1)
	assert.Equal(t, "sig101", sigs[0].Hash)
}

func TestAdapter_ScanBlocks_SPLTokenOwnerMatch(t *testing.T) {
	ctrl := gomock.NewController(t)
	adapter, mockClient := newTestAdapter(ctrl)

	bt := int64(1700000000)
	// accountKeys has only ATA, but postTokenBalances.owner has the watched address
	mockClient.EXPECT().GetBlock(gomock.Any(), int64(200), gomock.Any()).Return(&rpc.BlockResult{
		Blockhash: "hash200",
		BlockTime: &bt,
		Transactions: []rpc.BlockTransaction{
			makeBlockTx("sigSPL", []string{"ata_address", "token_program"}, []string{"sender_owner"}, []string{"watched_wallet"}),
		},
	}, nil)

	sigs, err := adapter.ScanBlocks(context.Background(), 200, 200, []string{"watched_wallet"})
	require.NoError(t, err)
	require.Len(t, sigs, 1)
	assert.Equal(t, "sigSPL", sigs[0].Hash)
}

func TestAdapter_ScanBlocks_NativeSOLTransfer(t *testing.T) {
	ctrl := gomock.NewController(t)
	adapter, mockClient := newTestAdapter(ctrl)

	bt := int64(1700000000)
	mockClient.EXPECT().GetBlock(gomock.Any(), int64(300), gomock.Any()).Return(&rpc.BlockResult{
		Blockhash: "hash300",
		BlockTime: &bt,
		Transactions: []rpc.BlockTransaction{
			makeBlockTx("sigNative", []string{"sender", "watched_recipient", "system_program"}, nil, nil),
		},
	}, nil)

	sigs, err := adapter.ScanBlocks(context.Background(), 300, 300, []string{"watched_recipient"})
	require.NoError(t, err)
	require.Len(t, sigs, 1)
	assert.Equal(t, "sigNative", sigs[0].Hash)
}

func TestAdapter_ScanBlocks_NoWatchedAddresses(t *testing.T) {
	ctrl := gomock.NewController(t)
	adapter, _ := newTestAdapter(ctrl)

	sigs, err := adapter.ScanBlocks(context.Background(), 100, 200, []string{})
	require.NoError(t, err)
	assert.Empty(t, sigs)
}

func TestAdapter_ScanBlocks_EmptyRange(t *testing.T) {
	ctrl := gomock.NewController(t)
	adapter, _ := newTestAdapter(ctrl)

	sigs, err := adapter.ScanBlocks(context.Background(), 200, 100, []string{"addr1"})
	require.NoError(t, err)
	assert.Empty(t, sigs)
}

func TestAdapter_ScanBlocks_RPCError(t *testing.T) {
	ctrl := gomock.NewController(t)
	adapter, mockClient := newTestAdapter(ctrl)

	mockClient.EXPECT().GetBlock(gomock.Any(), int64(100), gomock.Any()).
		Return(nil, errors.New("rpc unavailable"))

	_, err := adapter.ScanBlocks(context.Background(), 100, 100, []string{"addr1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rpc unavailable")
}
