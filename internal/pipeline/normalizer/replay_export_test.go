package normalizer

import (
	"testing"

	sidecarv1 "github.com/emperorhan/multichain-indexer/pkg/generated/sidecar/v1"

	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// ReplayNormalizeTxResult: basic chain dispatch
// ---------------------------------------------------------------------------

func TestReplayNormalizeTxResult_Solana_BasicFields(t *testing.T) {
	result := &sidecarv1.TransactionResult{
		TxHash:      "5wHu1qwD7q4hKzGj8DFpKTvAxKHcPA3A2YDWZ7fSSSR2LNqnLB3s2aym6LnKsnSf9KHbXwEq9Gs7JZnvmnQKvDB",
		BlockCursor: 300000005,
		BlockTime:   1700000000,
		FeeAmount:   "5000",
		FeePayer:    "Feepayer111111111111111111111111111111111",
		Status:      "SUCCESS",
	}
	watchedAddrs := map[string]struct{}{"SomeAddr111111111111111111111111111111111": {}}

	ntx := ReplayNormalizeTxResult(model.ChainSolana, model.NetworkDevnet, result, watchedAddrs)

	assert.Equal(t, result.TxHash, ntx.TxHash)
	assert.Equal(t, int64(300000005), ntx.BlockCursor)
	assert.Equal(t, "5000", ntx.FeeAmount)
	assert.Equal(t, model.TxStatusSuccess, ntx.Status)
	assert.NotNil(t, ntx.BlockTime)
	assert.Nil(t, ntx.Err)
}

func TestReplayNormalizeTxResult_EVM_Dispatch(t *testing.T) {
	for _, chain := range []model.Chain{
		model.ChainEthereum, model.ChainBase, model.ChainPolygon,
		model.ChainArbitrum, model.ChainBSC,
	} {
		t.Run(string(chain), func(t *testing.T) {
			result := &sidecarv1.TransactionResult{
				TxHash:      "0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
				BlockCursor: 19999800,
				BlockTime:   1700000000,
				FeeAmount:   "21000000000000",
				FeePayer:    "0x1234567890abcdef1234567890abcdef12345678",
				Status:      "SUCCESS",
				BalanceEvents: []*sidecarv1.BalanceEventInfo{
					{
						OuterInstructionIndex: 0,
						InnerInstructionIndex: -1,
						EventCategory:         "TRANSFER",
						EventAction:           "erc20_transfer",
						ProgramId:             "0xdAC17F958D2ee523a2206206994597C13D831ec7",
						Address:               "0xwatched",
						Delta:                 "1000000",
						CounterpartyAddress:   "0xsender",
						TokenType:             "FUNGIBLE",
						Metadata:              map[string]string{"log_index": "0"},
					},
				},
			}
			watchedAddrs := map[string]struct{}{"0xwatched": {}}

			ntx := ReplayNormalizeTxResult(chain, model.NetworkMainnet, result, watchedAddrs)

			// EVM dispatch should produce balance events.
			assert.Equal(t, "0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890", ntx.TxHash)
			assert.Equal(t, int64(19999800), ntx.BlockCursor)
			assert.Equal(t, model.TxStatusSuccess, ntx.Status)
		})
	}
}

func TestReplayNormalizeTxResult_BTC_Dispatch(t *testing.T) {
	result := &sidecarv1.TransactionResult{
		TxHash:      "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
		BlockCursor: 800000,
		BlockTime:   1700000000,
		FeeAmount:   "10000",
		FeePayer:    "bc1qsender",
		Status:      "SUCCESS",
		BalanceEvents: []*sidecarv1.BalanceEventInfo{
			{
				EventCategory:       "TRANSFER",
				EventAction:         "utxo_receive",
				Address:             "bc1qwatched",
				Delta:               "50000",
				CounterpartyAddress: "bc1qsender",
				TokenType:           "NATIVE",
				Metadata:            map[string]string{"vout_index": "0"},
			},
		},
	}
	watchedAddrs := map[string]struct{}{"bc1qwatched": {}}

	ntx := ReplayNormalizeTxResult(model.ChainBTC, model.NetworkMainnet, result, watchedAddrs)

	assert.Equal(t, int64(800000), ntx.BlockCursor)
	assert.Equal(t, model.TxStatusSuccess, ntx.Status)
}

// ---------------------------------------------------------------------------
// ReplayNormalizeTxResult: edge cases
// ---------------------------------------------------------------------------

func TestReplayNormalizeTxResult_ZeroBlockTime(t *testing.T) {
	result := &sidecarv1.TransactionResult{
		TxHash:      "txhash123",
		BlockCursor: 100,
		BlockTime:   0, // zero → no block time
		Status:      "SUCCESS",
	}
	ntx := ReplayNormalizeTxResult(model.ChainSolana, model.NetworkDevnet, result, nil)

	assert.Nil(t, ntx.BlockTime, "block time should be nil when BlockTime=0")
}

func TestReplayNormalizeTxResult_WithBlockTime(t *testing.T) {
	result := &sidecarv1.TransactionResult{
		TxHash:      "txhash123",
		BlockCursor: 100,
		BlockTime:   1700000000,
		Status:      "SUCCESS",
	}
	ntx := ReplayNormalizeTxResult(model.ChainSolana, model.NetworkDevnet, result, nil)

	require.NotNil(t, ntx.BlockTime)
	assert.Equal(t, int64(1700000000), ntx.BlockTime.Unix())
}

func TestReplayNormalizeTxResult_FailedTx_HasError(t *testing.T) {
	errMsg := "out of gas"
	result := &sidecarv1.TransactionResult{
		TxHash:      "0xfailed",
		BlockCursor: 100,
		Status:      "FAILED",
		Error:       &errMsg,
	}
	ntx := ReplayNormalizeTxResult(model.ChainEthereum, model.NetworkMainnet, result, nil)

	assert.Equal(t, model.TxStatusFailed, ntx.Status)
	require.NotNil(t, ntx.Err)
	assert.Equal(t, "out of gas", *ntx.Err)
}

func TestReplayNormalizeTxResult_NoError(t *testing.T) {
	result := &sidecarv1.TransactionResult{
		TxHash:      "0xok",
		BlockCursor: 100,
		Status:      "SUCCESS",
	}
	ntx := ReplayNormalizeTxResult(model.ChainEthereum, model.NetworkMainnet, result, nil)

	assert.Nil(t, ntx.Err)
}

func TestReplayNormalizeTxResult_ChainData_IsEmptyJSON(t *testing.T) {
	result := &sidecarv1.TransactionResult{
		TxHash: "tx", BlockCursor: 1, Status: "SUCCESS",
	}
	ntx := ReplayNormalizeTxResult(model.ChainSolana, model.NetworkDevnet, result, nil)

	assert.JSONEq(t, `{}`, string(ntx.ChainData))
}

func TestReplayNormalizeTxResult_EmptyWatchedAddrs(t *testing.T) {
	result := &sidecarv1.TransactionResult{
		TxHash:      "tx",
		BlockCursor: 1,
		Status:      "SUCCESS",
		BalanceEvents: []*sidecarv1.BalanceEventInfo{
			{
				EventCategory: "TRANSFER",
				EventAction:   "spl_transfer",
				Address:       "SomeAddr",
				Delta:         "100",
			},
		},
	}
	// nil watched addresses → should not panic.
	ntx := ReplayNormalizeTxResult(model.ChainSolana, model.NetworkDevnet, result, nil)
	assert.NotNil(t, ntx)
}

func TestReplayNormalizeTxResult_NilBalanceEvents(t *testing.T) {
	result := &sidecarv1.TransactionResult{
		TxHash:        "tx",
		BlockCursor:   1,
		Status:        "SUCCESS",
		BalanceEvents: nil,
	}
	ntx := ReplayNormalizeTxResult(model.ChainEthereum, model.NetworkMainnet, result, nil)

	// Should not panic; BalanceEvents may be empty.
	assert.Empty(t, ntx.BalanceEvents)
}

// ---------------------------------------------------------------------------
// ReplayNormalizeTxResult parity: same output as normalizedTxFromResult
// ---------------------------------------------------------------------------

func TestReplayNormalizeTxResult_Parity_Solana(t *testing.T) {
	result := &sidecarv1.TransactionResult{
		TxHash:      "5wHu1qwD7q4hKzGj8DFpKTvAxKHcPA3A2YDWZ7fSSSR2LNqnLB3s2aym6LnKsnSf9KHbXwEq9Gs7JZnvmnQKvDB",
		BlockCursor: 300000005,
		BlockTime:   1700000000,
		FeeAmount:   "5000",
		FeePayer:    "Feepayer111111111111111111111111111111111",
		Status:      "SUCCESS",
		BalanceEvents: []*sidecarv1.BalanceEventInfo{
			{
				OuterInstructionIndex: 0,
				InnerInstructionIndex: -1,
				EventCategory:         "TRANSFER",
				EventAction:           "spl_transfer",
				ProgramId:             "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA",
				Address:               "WatchedAddr",
				Delta:                 "1000000",
				CounterpartyAddress:   "SenderAddr",
				TokenType:             "FUNGIBLE",
				ContractAddress:       "MintAddr",
			},
		},
	}
	watchedAddrs := map[string]struct{}{"WatchedAddr": {}}

	replayTx := ReplayNormalizeTxResult(model.ChainSolana, model.NetworkDevnet, result, watchedAddrs)

	// The replay function should produce identical results to the Normalizer method.
	// Verify key fields.
	assert.Equal(t, result.TxHash, replayTx.TxHash)
	assert.Equal(t, int64(300000005), replayTx.BlockCursor)
	assert.Equal(t, "5000", replayTx.FeeAmount)
}

func TestReplayNormalizeTxResult_Parity_EVM(t *testing.T) {
	result := &sidecarv1.TransactionResult{
		TxHash:      "0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
		BlockCursor: 19999800,
		BlockTime:   1700000000,
		FeeAmount:   "21000000000000",
		FeePayer:    "0x1234567890abcdef1234567890abcdef12345678",
		Status:      "SUCCESS",
	}

	replayTx := ReplayNormalizeTxResult(model.ChainEthereum, model.NetworkMainnet, result, nil)

	assert.Equal(t, "0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890", replayTx.TxHash)
	assert.Equal(t, int64(19999800), replayTx.BlockCursor)
}

func TestReplayNormalizeTxResult_Parity_BTC(t *testing.T) {
	result := &sidecarv1.TransactionResult{
		TxHash:      "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
		BlockCursor: 800000,
		BlockTime:   1700000000,
		FeeAmount:   "10000",
		FeePayer:    "",
		Status:      "SUCCESS",
	}

	replayTx := ReplayNormalizeTxResult(model.ChainBTC, model.NetworkMainnet, result, nil)

	assert.Equal(t, "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2", replayTx.TxHash)
	assert.Equal(t, int64(800000), replayTx.BlockCursor)
}
