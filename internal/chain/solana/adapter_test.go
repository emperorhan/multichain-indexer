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
		client: mockClient,
		logger: slog.Default(),
	}
	return adapter, mockClient
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

func TestAdapter_FetchTransactions_BatchPreferred(t *testing.T) {
	client := &batchCapableClient{
		results: []json.RawMessage{
			json.RawMessage(`{"slot":100}`),
			json.RawMessage(`{"slot":200}`),
		},
	}
	adapter := &Adapter{
		client: client,
		logger: slog.Default(),
	}

	results, err := adapter.FetchTransactions(context.Background(), []string{"sig1", "sig2"})
	require.NoError(t, err)
	require.Len(t, results, 2)
	assert.True(t, client.batchCalled)
	assert.False(t, client.singleCalled)
}
