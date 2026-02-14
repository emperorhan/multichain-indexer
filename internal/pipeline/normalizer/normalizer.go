package normalizer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"sort"
	"strings"
	"sync"
	"time"

	sidecarv1 "github.com/emperorhan/multichain-indexer/pkg/generated/sidecar/v1"

	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Normalizer receives RawBatches, calls sidecar gRPC for decoding,
// and produces NormalizedBatches.
type Normalizer struct {
	sidecarAddr    string
	sidecarTimeout time.Duration
	rawBatchCh     <-chan event.RawBatch
	normalizedCh   chan<- event.NormalizedBatch
	workerCount    int
	logger         *slog.Logger
}

func New(
	sidecarAddr string,
	sidecarTimeout time.Duration,
	rawBatchCh <-chan event.RawBatch,
	normalizedCh chan<- event.NormalizedBatch,
	workerCount int,
	logger *slog.Logger,
) *Normalizer {
	return &Normalizer{
		sidecarAddr:    sidecarAddr,
		sidecarTimeout: sidecarTimeout,
		rawBatchCh:     rawBatchCh,
		normalizedCh:   normalizedCh,
		workerCount:    workerCount,
		logger:         logger.With("component", "normalizer"),
	}
}

func (n *Normalizer) Run(ctx context.Context) error {
	n.logger.Info("normalizer started", "sidecar_addr", n.sidecarAddr, "workers", n.workerCount)

	conn, err := grpc.NewClient(
		n.sidecarAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return fmt.Errorf("connect sidecar: %w", err)
	}
	defer conn.Close()

	client := sidecarv1.NewChainDecoderClient(conn)

	var wg sync.WaitGroup
	for i := 0; i < n.workerCount; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			n.worker(ctx, workerID, client)
		}(i)
	}

	wg.Wait()
	n.logger.Info("normalizer stopped")
	return ctx.Err()
}

func (n *Normalizer) worker(ctx context.Context, workerID int, client sidecarv1.ChainDecoderClient) {
	log := n.logger.With("worker", workerID)

	for {
		select {
		case <-ctx.Done():
			return
		case batch, ok := <-n.rawBatchCh:
			if !ok {
				return
			}
			if err := n.processBatch(ctx, log, client, batch); err != nil {
				log.Error("process batch failed",
					"address", batch.Address,
					"error", err,
				)
			}
		}
	}
}

func (n *Normalizer) processBatch(ctx context.Context, log *slog.Logger, client sidecarv1.ChainDecoderClient, batch event.RawBatch) error {
	// Build gRPC request
	rawTxs := make([]*sidecarv1.RawTransaction, len(batch.RawTransactions))
	for i, rawJSON := range batch.RawTransactions {
		rawTxs[i] = &sidecarv1.RawTransaction{
			Signature: batch.Signatures[i].Hash,
			RawJson:   rawJSON,
		}
	}

	callCtx, cancel := context.WithTimeout(ctx, n.sidecarTimeout)
	defer cancel()

	resp, err := client.DecodeSolanaTransactionBatch(callCtx, &sidecarv1.DecodeSolanaTransactionBatchRequest{
		Transactions:     rawTxs,
		WatchedAddresses: []string{batch.Address},
	})
	if err != nil {
		return fmt.Errorf("sidecar decode: %w", err)
	}

	// Log decode errors
	for _, decErr := range resp.Errors {
		log.Warn("sidecar decode error", "signature", decErr.Signature, "error", decErr.Error)
	}

	// Convert to NormalizedBatch
	normalized := event.NormalizedBatch{
		Chain:             batch.Chain,
		Network:           batch.Network,
		Address:           batch.Address,
		WalletID:          batch.WalletID,
		OrgID:             batch.OrgID,
		NewCursorValue:    batch.NewCursorValue,
		NewCursorSequence: batch.NewCursorSequence,
	}

	for _, result := range resp.Results {
		tx := event.NormalizedTransaction{
			TxHash:      result.TxHash,
			BlockCursor: result.BlockCursor,
			FeeAmount:   result.FeeAmount,
			FeePayer:    result.FeePayer,
			Status:      model.TxStatus(result.Status),
			ChainData:   json.RawMessage("{}"),
		}

		if result.BlockTime != 0 {
			bt := time.Unix(result.BlockTime, 0)
			tx.BlockTime = &bt
		}
		if result.Error != nil {
			tx.Err = result.Error
		}

		isBaseChain := batch.Chain == model.ChainBase || (batch.Chain == model.ChainEthereum && batch.Network == model.NetworkSepolia)
		if isBaseChain {
			tx.BalanceEvents = buildCanonicalBaseBalanceEvents(
				batch.Chain,
				batch.Network,
				result.TxHash,
				result.Status,
				result.FeePayer,
				result.FeeAmount,
				result.BalanceEvents,
			)
		} else {
			tx.BalanceEvents = buildCanonicalSolanaBalanceEvents(
				batch.Chain,
				batch.Network,
				result.TxHash,
				result.Status,
				result.FeePayer,
				result.FeeAmount,
				result.BalanceEvents,
			)
		}

		normalized.Transactions = append(normalized.Transactions, tx)
	}

	select {
	case n.normalizedCh <- normalized:
		log.Info("normalized batch sent",
			"address", batch.Address,
			"tx_count", len(normalized.Transactions),
		)
	case <-ctx.Done():
		return ctx.Err()
	}

	return nil
}

func buildCanonicalSolanaBalanceEvents(
	chain model.Chain,
	network model.Network,
	txHash, txStatus, feePayer, feeAmount string,
	rawEvents []*sidecarv1.BalanceEventInfo,
) []event.NormalizedBalanceEvent {
	normalizedEvents := make([]event.NormalizedBalanceEvent, 0, len(rawEvents)+1)

	for _, be := range rawEvents {
		if be == nil {
			continue
		}
		chainData, _ := json.Marshal(be.Metadata)

		normalizedEvents = append(normalizedEvents, event.NormalizedBalanceEvent{
			OuterInstructionIndex: int(be.OuterInstructionIndex),
			InnerInstructionIndex: int(be.InnerInstructionIndex),
			EventCategory:         model.EventCategory(be.EventCategory),
			EventAction:           be.EventAction,
			ProgramID:             be.ProgramId,
			ContractAddress:       be.ContractAddress,
			Address:               be.Address,
			CounterpartyAddress:   be.CounterpartyAddress,
			Delta:                 be.Delta,
			ChainData:             chainData,
			TokenSymbol:           be.TokenSymbol,
			TokenName:             be.TokenName,
			TokenDecimals:         int(be.TokenDecimals),
			TokenType:             model.TokenType(be.TokenType),
			AssetID:               be.ContractAddress,
		})
	}

	if shouldEmitSolanaFeeEvent(txStatus, feePayer) && !hasSolanaFeeEvent(normalizedEvents, feePayer) {
		normalizedEvents = append(normalizedEvents, buildSolanaFeeBalanceEvent(feePayer, feeAmount))
	}

	return canonicalizeSolanaBalanceEvents(chain, network, txHash, normalizedEvents)
}

func buildCanonicalBaseBalanceEvents(
	chain model.Chain,
	network model.Network,
	txHash, txStatus, feePayer, feeAmount string,
	rawEvents []*sidecarv1.BalanceEventInfo,
) []event.NormalizedBalanceEvent {
	normalizedEvents := make([]event.NormalizedBalanceEvent, 0, len(rawEvents)+2)
	meta := collectBaseMetadata(rawEvents)
	missingDataFee := !hasBaseDataFee(meta)
	eventPath := resolveBaseFeeEventPath(meta)

	for _, be := range rawEvents {
		if be == nil {
			continue
		}
		chainData, _ := json.Marshal(be.Metadata)

		normalizedEvents = append(normalizedEvents, event.NormalizedBalanceEvent{
			OuterInstructionIndex: int(be.OuterInstructionIndex),
			InnerInstructionIndex: int(be.InnerInstructionIndex),
			EventCategory:         model.EventCategory(be.EventCategory),
			EventAction:           be.EventAction,
			ProgramID:             be.ProgramId,
			ContractAddress:       be.ContractAddress,
			Address:               be.Address,
			CounterpartyAddress:   be.CounterpartyAddress,
			Delta:                 be.Delta,
			ChainData:             chainData,
			TokenSymbol:           be.TokenSymbol,
			TokenName:             be.TokenName,
			TokenDecimals:         int(be.TokenDecimals),
			TokenType:             model.TokenType(be.TokenType),
			AssetID:               be.ContractAddress,
		})
	}

	if shouldEmitBaseFeeEvent(txStatus, feePayer) {
		executionFee := deriveBaseExecutionFee(feeAmount, meta)
		dataFee, hasDataFee := deriveBaseDataFee(meta)

		executionEvent := buildBaseFeeBalanceEvent(
			model.EventCategoryFeeExecutionL2,
			feePayer,
			executionFee.String(),
			eventPath,
			"net_base",
		)
		marker := map[string]string{}
		if hasBaseFeeComponentMetaMismatch(feeAmount, meta) {
			marker["fee_total_mismatch"] = "true"
			execAmount, hasExecution := deriveBaseExecutionFeeFromMetadata(meta)
			dataAmount, hasData := deriveBaseDataFee(meta)
			if !hasExecution {
				execAmount = big.NewInt(0)
			}
			if !hasData {
				dataAmount = big.NewInt(0)
			}
			marker["fee_execution_total_from_components"] = execAmount.String()
			marker["fee_data_total_from_components"] = dataAmount.String()
			marker["fee_amount_total"] = feeAmount
		}
		if missingDataFee {
			marker["data_fee_l1_unavailable"] = "true"
		}
		if len(marker) > 0 {
			executionEvent.ChainData = mergeMetadataJSON(executionEvent.ChainData, marker)
		}

		normalizedEvents = append(normalizedEvents, executionEvent)

		if hasDataFee {
			normalizedEvents = append(
				normalizedEvents,
				buildBaseFeeBalanceEvent(
					model.EventCategoryFeeDataL1,
					feePayer,
					dataFee.String(),
					eventPath,
					"net_base_data",
				),
			)
		}
	}

	return canonicalizeBaseBalanceEvents(chain, network, txHash, normalizedEvents)
}

func collectBaseMetadata(rawEvents []*sidecarv1.BalanceEventInfo) map[string]string {
	out := make(map[string]string, len(rawEvents))
	for _, be := range rawEvents {
		if be == nil {
			continue
		}
		for k, v := range be.Metadata {
			if _, exists := out[k]; exists {
				continue
			}
			out[k] = v
		}
	}
	return out
}

func buildBaseFeeBalanceEvent(
	category model.EventCategory,
	feePayer, feeAmount string,
	eventPath, eventAction string,
) event.NormalizedBalanceEvent {
	return event.NormalizedBalanceEvent{
		OuterInstructionIndex: -1,
		InnerInstructionIndex: -1,
		EventCategory:         category,
		EventAction:           eventAction,
		ProgramID:             "0x0000000000000000000000000000000000000000000000000000000000000000",
		ContractAddress:       "ETH",
		Address:               feePayer,
		CounterpartyAddress:   "",
		Delta:                 canonicalFeeDelta(feeAmount),
		ChainData:             json.RawMessage("{}"),
		TokenSymbol:           "ETH",
		TokenName:             "Ether",
		TokenDecimals:         18,
		TokenType:             model.TokenTypeNative,
		AssetID:               "ETH",
		EventPath:             eventPath,
	}
}

func resolveBaseFeeEventPath(metadata map[string]string) string {
	if p := metadata["event_path"]; p != "" {
		return p
	}
	if p := metadata["log_path"]; p != "" {
		return p
	}
	if idx := metadata["log_index"]; idx != "" {
		return "log:" + idx
	}
	if idx := metadata["base_log_index"]; idx != "" {
		return "log:" + idx
	}
	if p := metadata["base_event_path"]; p != "" {
		return p
	}
	return "log:0"
}

func shouldEmitBaseFeeEvent(txStatus, feePayer string) bool {
	return shouldEmitSolanaFeeEvent(txStatus, feePayer)
}

func deriveBaseExecutionFee(totalFee string, metadata map[string]string) *big.Int {
	if fee, ok := deriveBaseExecutionFeeFromMetadata(metadata); ok {
		return fee
	}
	if fee, ok := parseBigInt(totalFee); ok {
		return fee
	}
	return big.NewInt(0)
}

func deriveBaseExecutionFeeFromMetadata(metadata map[string]string) (*big.Int, bool) {
	if fee, ok := parseBigInt(metadata["fee_execution_l2"]); ok {
		return fee, true
	}
	if gasUsed, ok1 := parseBigInt(metadata["gas_used"]); ok1 {
		if gasPrice, ok2 := parseBigInt(metadata["effective_gas_price"]); ok2 {
			return gasUsed.Mul(gasUsed, gasPrice), true
		}
	}
	if gasUsed, ok1 := parseBigInt(metadata["base_gas_used"]); ok1 {
		if gasPrice, ok2 := parseBigInt(metadata["base_effective_gas_price"]); ok2 {
			return gasUsed.Mul(gasUsed, gasPrice), true
		}
	}
	return nil, false
}

func deriveBaseDataFee(metadata map[string]string) (*big.Int, bool) {
	keys := []string{
		"fee_data_l1",
		"data_fee_l1",
		"l1_data_fee",
		"l1_fee",
	}
	for _, key := range keys {
		if fee, ok := parseBigInt(metadata[key]); ok {
			return fee, true
		}
	}
	return big.NewInt(0), false
}

func hasBaseDataFee(metadata map[string]string) bool {
	_, has := deriveBaseDataFee(metadata)
	return has
}

func hasBaseFeeComponentMetaMismatch(totalFee string, metadata map[string]string) bool {
	total, ok := parseBigInt(totalFee)
	if !ok || total.Sign() == 0 {
		return false
	}

	exec, hasExecutionFee := deriveBaseExecutionFeeFromMetadata(metadata)
	data, hasDataFee := deriveBaseDataFee(metadata)
	if !hasExecutionFee || !hasDataFee {
		return false
	}

	sum := new(big.Int).Add(exec, data)
	return sum.Cmp(total) != 0
}

func parseBigInt(value string) (*big.Int, bool) {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return nil, false
	}
	if raw[0] == '+' || raw[0] == '-' {
		raw = raw[1:]
	}
	if raw == "" {
		return nil, false
	}
	parsed := new(big.Int)
	if _, ok := parsed.SetString(raw, 10); !ok {
		return nil, false
	}
	return parsed, true
}

func mergeMetadataJSON(original json.RawMessage, additions map[string]string) json.RawMessage {
	base := map[string]string{}
	if len(original) > 0 && string(original) != "null" {
		_ = json.Unmarshal(original, &base)
	}
	for k, v := range additions {
		base[k] = v
	}
	marshaled, _ := json.Marshal(base)
	return marshaled
}

func canonicalizeBaseBalanceEvents(
	chain model.Chain,
	network model.Network,
	txHash string,
	normalizedEvents []event.NormalizedBalanceEvent,
) []event.NormalizedBalanceEvent {
	eventsByID := make(map[string]event.NormalizedBalanceEvent, len(normalizedEvents))
	for _, be := range normalizedEvents {
		if be.EventCategory == model.EventCategoryFeeExecutionL2 || be.EventCategory == model.EventCategoryFeeDataL1 {
			be.EventAction = string(be.EventCategory)
			be.Delta = canonicalFeeDelta(be.Delta)
		}
		if be.EventPath == "" {
			be.EventPath = balanceEventPath(int32(be.OuterInstructionIndex), int32(be.InnerInstructionIndex))
		}

		assetType := mapTokenTypeToAssetType(be.TokenType)
		if isBaseFeeCategory(be.EventCategory) {
			assetType = "fee"
		}

		be.ActorAddress = be.Address
		be.AssetType = assetType
		be.EventPathType = "base_log"
		be.FinalityState = "finalized"
		be.DecoderVersion = "base-decoder-v1"
		be.SchemaVersion = "v2"
		be.EventID = buildCanonicalEventID(
			chain, network,
			txHash, be.EventPath,
			be.ActorAddress, be.AssetID, be.EventCategory,
		)

		eventsByID[be.EventID] = be
	}

	events := make([]event.NormalizedBalanceEvent, 0, len(eventsByID))
	for _, be := range eventsByID {
		events = append(events, be)
	}

	sort.Slice(events, func(i, j int) bool {
		return events[i].EventID < events[j].EventID
	})

	return events
}

func isBaseFeeCategory(category model.EventCategory) bool {
	switch category {
	case model.EventCategoryFeeExecutionL2, model.EventCategoryFeeDataL1:
		return true
	default:
		return false
	}
}

func shouldEmitSolanaFeeEvent(txStatus, feePayer string) bool {
	if !strings.EqualFold(txStatus, string(model.TxStatusSuccess)) {
		return false
	}
	if strings.TrimSpace(feePayer) == "" {
		return false
	}
	return true
}

func hasSolanaFeeEvent(events []event.NormalizedBalanceEvent, feePayer string) bool {
	feePayer = strings.TrimSpace(feePayer)
	if feePayer == "" {
		return false
	}
	for _, be := range events {
		if !strings.EqualFold(string(be.EventCategory), string(model.EventCategoryFee)) {
			continue
		}
		if strings.TrimSpace(be.Address) == feePayer {
			return true
		}
	}
	return false
}

func canonicalFeeDelta(amount string) string {
	clean := strings.TrimSpace(amount)
	if clean == "" {
		return "-0"
	}
	trimmed := clean
	if strings.HasPrefix(trimmed, "+") || strings.HasPrefix(trimmed, "-") {
		trimmed = trimmed[1:]
	}
	if trimmed == "" {
		return "-0"
	}
	amountInt := new(big.Int)
	if _, ok := amountInt.SetString(trimmed, 10); !ok {
		return "-0"
	}
	if amountInt.Sign() == 0 {
		return "-0"
	}
	return "-" + amountInt.String()
}

func buildSolanaFeeBalanceEvent(feePayer, feeAmount string) event.NormalizedBalanceEvent {
	return event.NormalizedBalanceEvent{
		OuterInstructionIndex: -1,
		InnerInstructionIndex: -1,
		EventCategory:         model.EventCategoryFee,
		EventAction:           "transaction_fee",
		ProgramID:             "11111111111111111111111111111111",
		ContractAddress:       "11111111111111111111111111111111",
		Address:               feePayer,
		CounterpartyAddress:   "",
		Delta:                 canonicalFeeDelta(feeAmount),
		ChainData:             json.RawMessage("{}"),
		TokenSymbol:           "SOL",
		TokenName:             "Solana",
		TokenDecimals:         9,
		TokenType:             model.TokenTypeNative,
		AssetID:               "11111111111111111111111111111111",
	}
}

func canonicalizeSolanaBalanceEvents(
	chain model.Chain,
	network model.Network,
	txHash string,
	normalizedEvents []event.NormalizedBalanceEvent,
) []event.NormalizedBalanceEvent {
	type solanaInstructionOwnerKey struct {
		OuterInstruction int
		Address          string
		AssetID          string
		Category         model.EventCategory
	}

	outerHasOwner := make(map[solanaInstructionOwnerKey]struct{})
	for _, be := range normalizedEvents {
		if be.EventCategory != model.EventCategoryFee && be.OuterInstructionIndex >= 0 && be.InnerInstructionIndex == -1 {
			outerHasOwner[solanaInstructionOwnerKey{
				OuterInstruction: be.OuterInstructionIndex,
				Address:          be.Address,
				AssetID:          be.AssetID,
				Category:         be.EventCategory,
			}] = struct{}{}
		}
	}

	sort.SliceStable(normalizedEvents, func(i, j int) bool {
		if normalizedEvents[i].EventCategory != normalizedEvents[j].EventCategory {
			return normalizedEvents[i].EventCategory < normalizedEvents[j].EventCategory
		}
		if normalizedEvents[i].OuterInstructionIndex != normalizedEvents[j].OuterInstructionIndex {
			return normalizedEvents[i].OuterInstructionIndex < normalizedEvents[j].OuterInstructionIndex
		}
		if normalizedEvents[i].InnerInstructionIndex != normalizedEvents[j].InnerInstructionIndex {
			return normalizedEvents[i].InnerInstructionIndex < normalizedEvents[j].InnerInstructionIndex
		}
		if normalizedEvents[i].Address != normalizedEvents[j].Address {
			return normalizedEvents[i].Address < normalizedEvents[j].Address
		}
		return normalizedEvents[i].ContractAddress < normalizedEvents[j].ContractAddress
	})

	eventsByID := make(map[string]event.NormalizedBalanceEvent, len(normalizedEvents))
	for _, be := range normalizedEvents {
		if be.EventCategory == model.EventCategoryFee {
			be.OuterInstructionIndex = -1
			be.InnerInstructionIndex = -1
			be.Delta = canonicalFeeDelta(be.Delta)
			be.EventAction = "transaction_fee"
		}
		if be.EventCategory != model.EventCategoryFee && be.OuterInstructionIndex >= 0 && be.InnerInstructionIndex > -1 {
			key := solanaInstructionOwnerKey{
				OuterInstruction: be.OuterInstructionIndex,
				Address:          be.Address,
				AssetID:          be.AssetID,
				Category:         be.EventCategory,
			}
			if _, ok := outerHasOwner[key]; ok {
				be.InnerInstructionIndex = -1
			}
		}

		assetType := mapTokenTypeToAssetType(be.TokenType)
		if be.EventCategory == model.EventCategoryFee {
			assetType = "fee"
		}

		be.ActorAddress = be.Address
		be.AssetType = assetType
		be.EventPath = balanceEventPath(int32(be.OuterInstructionIndex), int32(be.InnerInstructionIndex))
		be.EventPathType = "solana_instruction"
		be.FinalityState = "finalized"
		be.DecoderVersion = "solana-decoder-v1"
		be.SchemaVersion = "v2"
		be.EventID = buildCanonicalEventID(
			chain, network,
			txHash, be.EventPath,
			be.ActorAddress, be.AssetID, be.EventCategory,
		)

		existing, ok := eventsByID[be.EventID]
		if !ok {
			eventsByID[be.EventID] = be
			continue
		}
		if shouldReplaceCanonicalSolanaEvent(existing, be) {
			eventsByID[be.EventID] = be
		}
	}

	events := make([]event.NormalizedBalanceEvent, 0, len(eventsByID))
	for _, be := range eventsByID {
		events = append(events, be)
	}

	sort.Slice(events, func(i, j int) bool {
		return events[i].EventID < events[j].EventID
	})

	return events
}

func shouldReplaceCanonicalSolanaEvent(existing, incoming event.NormalizedBalanceEvent) bool {
	if existing.EventCategory == model.EventCategoryFee || incoming.EventCategory == model.EventCategoryFee {
		return false
	}
	return incoming.InnerInstructionIndex == -1 && existing.InnerInstructionIndex > incoming.InnerInstructionIndex
}

func buildCanonicalEventID(chain model.Chain, network model.Network, txHash, eventPath, actorAddress, assetID string, category model.EventCategory) string {
	canonical := fmt.Sprintf("chain=%s|network=%s|tx=%s|path=%s|actor=%s|asset=%s|category=%s", chain, network, txHash, eventPath, actorAddress, assetID, category)
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}

func mapTokenTypeToAssetType(tokenType model.TokenType) string {
	switch tokenType {
	case model.TokenTypeNative:
		return "native"
	case model.TokenTypeNFT:
		return "nft"
	case model.TokenTypeFungible:
		return "fungible_token"
	default:
		return "unknown"
	}
}

func balanceEventPath(outerInstructionIndex, innerInstructionIndex int32) string {
	return fmt.Sprintf("outer:%d|inner:%d", outerInstructionIndex, innerInstructionIndex)
}
