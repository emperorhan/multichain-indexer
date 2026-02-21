package normalizer

import (
	"fmt"
	"log/slog"
	"testing"

	sidecarv1 "github.com/emperorhan/multichain-indexer/pkg/generated/sidecar/v1"

	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/identity"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
)

// makeSolanaBalanceEventInfos builds N sidecar BalanceEventInfo protos for benchmark input.
func makeSolanaBalanceEventInfos(watchedAddr string, n int) []*sidecarv1.BalanceEventInfo {
	events := make([]*sidecarv1.BalanceEventInfo, 0, n)
	for i := 0; i < n; i++ {
		events = append(events, &sidecarv1.BalanceEventInfo{
			OuterInstructionIndex: int32(i),
			InnerInstructionIndex: -1,
			EventCategory:         "TRANSFER",
			EventAction:           "system_transfer",
			ProgramId:             "11111111111111111111111111111111",
			Address:               watchedAddr,
			ContractAddress:       "11111111111111111111111111111111",
			Delta:                 fmt.Sprintf("-%d", (i+1)*1000),
			CounterpartyAddress:   "counterparty_addr",
			TokenSymbol:           "SOL",
			TokenName:             "Solana",
			TokenDecimals:         9,
			TokenType:             "NATIVE",
			Metadata:              map[string]string{"commitment": "finalized"},
		})
	}
	return events
}

// makeEVMBalanceEventInfos builds N sidecar BalanceEventInfo protos for EVM benchmark input.
func makeEVMBalanceEventInfos(watchedAddr string, n int) []*sidecarv1.BalanceEventInfo {
	events := make([]*sidecarv1.BalanceEventInfo, 0, n)
	for i := 0; i < n; i++ {
		events = append(events, &sidecarv1.BalanceEventInfo{
			OuterInstructionIndex: int32(i),
			InnerInstructionIndex: -1,
			EventCategory:         "TRANSFER",
			EventAction:           "erc20_transfer",
			ProgramId:             "",
			Address:               watchedAddr,
			ContractAddress:       "0xdead000000000000000000000000000000000001",
			Delta:                 fmt.Sprintf("-%d", (i+1)*50000),
			CounterpartyAddress:   "0xbeef000000000000000000000000000000000002",
			TokenSymbol:           "ETH",
			TokenName:             "Ether",
			TokenDecimals:         18,
			TokenType:             "NATIVE",
			Metadata: map[string]string{
				"finality_state":   "finalized",
				"fee_execution_l2": "21000",
				"fee_data_l1":      "5000",
				"event_path":       "0",
			},
		})
	}
	return events
}

// makeSignatureInfos builds N SignatureInfo entries for benchmark input.
func makeSignatureInfos(n int) []event.SignatureInfo {
	sigs := make([]event.SignatureInfo, 0, n)
	for i := 0; i < n; i++ {
		sigs = append(sigs, event.SignatureInfo{
			Hash:     fmt.Sprintf("sig_%d_abcdef1234567890", i),
			Sequence: int64(100 + i),
		})
	}
	return sigs
}

// BenchmarkBuildCanonicalSolanaBalanceEvents benchmarks the Solana balance event
// construction and canonicalization path. This is the core normalization logic
// for Solana transactions.
func BenchmarkBuildCanonicalSolanaBalanceEvents(b *testing.B) {
	const watchedAddr = "SoLAddr1111111111111111111111111111111111111"

	for _, count := range []int{1, 5, 20} {
		rawEvents := makeSolanaBalanceEventInfos(watchedAddr, count)
		b.Run(fmt.Sprintf("events_%d", count), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = buildCanonicalSolanaBalanceEvents(
					model.ChainSolana,
					model.NetworkDevnet,
					"txhash_bench_solana",
					"SUCCESS",
					watchedAddr,
					"5000",
					"finalized",
					rawEvents,
					watchedAddr,
				)
			}
		})
	}
}

// BenchmarkBuildCanonicalBaseBalanceEvents benchmarks the EVM (Base chain) balance
// event construction and canonicalization path including L2 execution/data fee split.
func BenchmarkBuildCanonicalBaseBalanceEvents(b *testing.B) {
	const watchedAddr = "0xdead000000000000000000000000000000000001"

	for _, count := range []int{1, 5, 20} {
		rawEvents := makeEVMBalanceEventInfos(watchedAddr, count)
		b.Run(fmt.Sprintf("events_%d", count), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = buildCanonicalBaseBalanceEvents(
					model.ChainBase,
					model.NetworkMainnet,
					"0xdeadbeef0000000000000000000000000000000000000000000000000000abcd",
					"SUCCESS",
					watchedAddr,
					"26000",
					"finalized",
					rawEvents,
					watchedAddr,
				)
			}
		})
	}
}

// BenchmarkNormalizedTxFromResult benchmarks the full normalizedTxFromResult method
// which converts a sidecar TransactionResult into a NormalizedTransaction including
// balance event building, finality resolution, and field mapping.
func BenchmarkNormalizedTxFromResult(b *testing.B) {
	const watchedAddr = "SoLAddr1111111111111111111111111111111111111"

	n := &Normalizer{
		logger: slog.Default(),
	}

	for _, count := range []int{1, 5, 20} {
		rawEvents := makeSolanaBalanceEventInfos(watchedAddr, count)
		result := &sidecarv1.TransactionResult{
			TxHash:        "txhash_bench_norm",
			BlockCursor:   12345,
			BlockTime:     1700000000,
			FeeAmount:     "5000",
			FeePayer:      watchedAddr,
			Status:        "SUCCESS",
			BalanceEvents: rawEvents,
		}
		batch := event.RawBatch{
			Chain:   model.ChainSolana,
			Network: model.NetworkDevnet,
			Address: watchedAddr,
		}

		watchedAddrs := resolveWatchedAddressSet(batch)
		b.Run(fmt.Sprintf("events_%d", count), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = n.normalizedTxFromResult(batch, result, nil, watchedAddrs)
			}
		})
	}
}

// BenchmarkCanonicalizeBatchSignatures benchmarks the signature deduplication and
// ordering logic that runs on every incoming batch.
func BenchmarkCanonicalizeBatchSignatures(b *testing.B) {
	for _, count := range []int{1, 10, 50} {
		sigs := makeSignatureInfos(count)
		b.Run(fmt.Sprintf("sigs_%d", count), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = canonicalizeBatchSignatures(model.ChainSolana, sigs)
			}
		})
	}
}

// BenchmarkCanonicalizeBatchSignatures_EVM benchmarks signature canonicalization
// for EVM chains where hex normalization (0x prefix, lowercase) is applied.
func BenchmarkCanonicalizeBatchSignatures_EVM(b *testing.B) {
	evmSigs := make([]event.SignatureInfo, 50)
	for i := range evmSigs {
		evmSigs[i] = event.SignatureInfo{
			Hash:     fmt.Sprintf("0xABCDEF%040d", i),
			Sequence: int64(i),
		}
	}

	b.Run("sigs_50_evm", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = canonicalizeBatchSignatures(model.ChainEthereum, evmSigs)
		}
	})
}

// BenchmarkBuildCanonicalSolanaBalanceEvents_SelfTransfer benchmarks the
// self-transfer detection path where counterparty == watched address.
func BenchmarkBuildCanonicalSolanaBalanceEvents_SelfTransfer(b *testing.B) {
	const watchedAddr = "SoLAddr1111111111111111111111111111111111111"

	rawEvents := []*sidecarv1.BalanceEventInfo{
		{
			OuterInstructionIndex: 0,
			InnerInstructionIndex: -1,
			EventCategory:         "TRANSFER",
			EventAction:           "system_transfer",
			ProgramId:             "11111111111111111111111111111111",
			Address:               watchedAddr,
			ContractAddress:       "11111111111111111111111111111111",
			Delta:                 "-500000",
			CounterpartyAddress:   watchedAddr, // self-transfer
			TokenSymbol:           "SOL",
			TokenName:             "Solana",
			TokenDecimals:         9,
			TokenType:             "NATIVE",
			Metadata:              map[string]string{"commitment": "finalized"},
		},
	}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = buildCanonicalSolanaBalanceEvents(
			model.ChainSolana,
			model.NetworkDevnet,
			"txhash_bench_self",
			"SUCCESS",
			watchedAddr,
			"5000",
			"finalized",
			rawEvents,
			watchedAddr,
		)
	}
}

// BenchmarkNormalizedTxFromResult_EVM benchmarks normalizedTxFromResult for an
// EVM chain (Base) to exercise the EVM-specific fee splitting and canonicalization.
func BenchmarkNormalizedTxFromResult_EVM(b *testing.B) {
	const watchedAddr = "0xdead000000000000000000000000000000000001"

	n := &Normalizer{
		logger: slog.Default(),
	}

	rawEvents := makeEVMBalanceEventInfos(watchedAddr, 5)
	result := &sidecarv1.TransactionResult{
		TxHash:        "0xdeadbeef00000000000000000000000000000000000000000000000000001234",
		BlockCursor:   99999,
		BlockTime:     1700000000,
		FeeAmount:     "26000",
		FeePayer:      watchedAddr,
		Status:        "SUCCESS",
		BalanceEvents: rawEvents,
	}
	batch := event.RawBatch{
		Chain:   model.ChainBase,
		Network: model.NetworkMainnet,
		Address: watchedAddr,
	}

	evmWatchedAddrs := resolveWatchedAddressSet(batch)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = n.normalizedTxFromResult(batch, result, nil, evmWatchedAddrs)
	}
}

// BenchmarkBuildCanonicalSolanaBalanceEvents_MixedEvents benchmarks with a
// realistic mix of event types (transfers, fees, stakes) in a single transaction.
func BenchmarkBuildCanonicalSolanaBalanceEvents_MixedEvents(b *testing.B) {
	const watchedAddr = "SoLAddr1111111111111111111111111111111111111"

	rawEvents := []*sidecarv1.BalanceEventInfo{
		{
			OuterInstructionIndex: 0,
			InnerInstructionIndex: -1,
			EventCategory:         "TRANSFER",
			EventAction:           "system_transfer",
			ProgramId:             "11111111111111111111111111111111",
			Address:               watchedAddr,
			ContractAddress:       "11111111111111111111111111111111",
			Delta:                 "-1000000",
			CounterpartyAddress:   "counterparty_1",
			TokenSymbol:           "SOL",
			TokenName:             "Solana",
			TokenDecimals:         9,
			TokenType:             "NATIVE",
			Metadata:              map[string]string{"commitment": "finalized"},
		},
		{
			OuterInstructionIndex: 1,
			InnerInstructionIndex: -1,
			EventCategory:         "TRANSFER",
			EventAction:           "spl_transfer",
			ProgramId:             "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA",
			Address:               watchedAddr,
			ContractAddress:       "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v",
			Delta:                 "5000000",
			CounterpartyAddress:   "counterparty_2",
			TokenSymbol:           "USDC",
			TokenName:             "USD Coin",
			TokenDecimals:         6,
			TokenType:             "FUNGIBLE",
			Metadata:              map[string]string{"commitment": "finalized"},
		},
		{
			OuterInstructionIndex: 2,
			InnerInstructionIndex: -1,
			EventCategory:         "STAKE",
			EventAction:           "stake_delegate",
			ProgramId:             "Stake11111111111111111111111111111111111111",
			Address:               watchedAddr,
			ContractAddress:       "11111111111111111111111111111111",
			Delta:                 "-2000000000",
			CounterpartyAddress:   "",
			TokenSymbol:           "SOL",
			TokenName:             "Solana",
			TokenDecimals:         9,
			TokenType:             "NATIVE",
			Metadata:              map[string]string{"commitment": "finalized"},
		},
		{
			OuterInstructionIndex: 3,
			InnerInstructionIndex: -1,
			EventCategory:         "TRANSFER",
			EventAction:           "system_transfer",
			ProgramId:             "11111111111111111111111111111111",
			Address:               "other_addr",
			ContractAddress:       "11111111111111111111111111111111",
			Delta:                 "1000000",
			CounterpartyAddress:   watchedAddr,
			TokenSymbol:           "SOL",
			TokenName:             "Solana",
			TokenDecimals:         9,
			TokenType:             "NATIVE",
			Metadata:              map[string]string{},
		},
	}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = buildCanonicalSolanaBalanceEvents(
			model.ChainSolana,
			model.NetworkDevnet,
			"txhash_bench_mixed",
			"SUCCESS",
			watchedAddr,
			"5000",
			"finalized",
			rawEvents,
			watchedAddr,
		)
	}
}

// BenchmarkCanonicalSignatureIdentity_Solana benchmarks the signature identity
// function for Solana (no hex normalization).
func BenchmarkCanonicalSignatureIdentity_Solana(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = identity.CanonicalSignatureIdentity(model.ChainSolana, "5VERv8NMvzbJMEkV8xnrLkEaWRtSz9CosKDYjCJjBRnbJLgp8uirBgmQpjKhoR4tJ")
	}
}

// BenchmarkCanonicalSignatureIdentity_EVM benchmarks the signature identity
// function for EVM chains (hex lowercase + 0x prefix normalization).
func BenchmarkCanonicalSignatureIdentity_EVM(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = identity.CanonicalSignatureIdentity(model.ChainEthereum, "0xABCDEF1234567890ABCDEF1234567890ABCDEF1234567890ABCDEF1234567890")
	}
}
