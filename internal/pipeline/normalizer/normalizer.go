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
	"github.com/emperorhan/multichain-indexer/internal/pipeline/retry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	defaultProcessRetryMaxAttempts = 3
	defaultRetryDelayInitial       = 100 * time.Millisecond
	defaultRetryDelayMax           = 1 * time.Second
)

// Normalizer receives RawBatches, calls sidecar gRPC for decoding,
// and produces NormalizedBatches.
type Normalizer struct {
	sidecarAddr      string
	sidecarTimeout   time.Duration
	rawBatchCh       <-chan event.RawBatch
	normalizedCh     chan<- event.NormalizedBatch
	workerCount      int
	logger           *slog.Logger
	retryMaxAttempts int
	retryDelayStart  time.Duration
	retryDelayMax    time.Duration
	sleepFn          func(context.Context, time.Duration) error
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
		sidecarAddr:      sidecarAddr,
		sidecarTimeout:   sidecarTimeout,
		rawBatchCh:       rawBatchCh,
		normalizedCh:     normalizedCh,
		workerCount:      workerCount,
		logger:           logger.With("component", "normalizer"),
		retryMaxAttempts: defaultProcessRetryMaxAttempts,
		retryDelayStart:  defaultRetryDelayInitial,
		retryDelayMax:    defaultRetryDelayMax,
		sleepFn:          sleepContext,
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
			if err := n.processBatchWithRetry(ctx, log, client, batch); err != nil {
				log.Error("process batch failed",
					"address", batch.Address,
					"error", err,
				)
				panic(fmt.Sprintf("normalizer process batch failed: address=%s err=%v", batch.Address, err))
			}
		}
	}
}

func (n *Normalizer) processBatchWithRetry(
	ctx context.Context,
	log *slog.Logger,
	client sidecarv1.ChainDecoderClient,
	batch event.RawBatch,
) error {
	const stage = "normalizer.decode_batch"

	maxAttempts := n.effectiveRetryMaxAttempts()
	var lastErr error
	lastDecision := retry.Decision{
		Class:  retry.ClassTerminal,
		Reason: "unset",
	}

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err := n.processBatch(ctx, log, client, batch); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			lastErr = err
			lastDecision = retry.Classify(err)
			if !lastDecision.IsTransient() {
				return fmt.Errorf("terminal_failure stage=%s attempt=%d reason=%s: %w", stage, attempt, lastDecision.Reason, err)
			}
			if attempt == maxAttempts {
				break
			}
			log.Warn("process batch attempt failed; retrying",
				"stage", stage,
				"classification", lastDecision.Class,
				"classification_reason", lastDecision.Reason,
				"address", batch.Address,
				"attempt", attempt,
				"max_attempts", maxAttempts,
				"error", err,
			)
			if err := n.sleep(ctx, n.retryDelay(attempt)); err != nil {
				return err
			}
			continue
		}
		return nil
	}

	return fmt.Errorf("transient_recovery_exhausted stage=%s attempts=%d reason=%s: %w", stage, maxAttempts, lastDecision.Reason, lastErr)
}

func (n *Normalizer) processBatch(ctx context.Context, log *slog.Logger, client sidecarv1.ChainDecoderClient, batch event.RawBatch) error {
	const decodeStage = "normalizer.decode_batch"

	if len(batch.RawTransactions) != len(batch.Signatures) {
		return fmt.Errorf("raw/signature length mismatch: raw=%d signatures=%d", len(batch.RawTransactions), len(batch.Signatures))
	}
	canonicalSignatures := canonicalizeBatchSignatures(batch.Chain, batch.Signatures)
	if len(canonicalSignatures) == 0 && len(batch.Signatures) > 0 {
		return fmt.Errorf("decode collapse stage=%s no canonical signatures in batch", decodeStage)
	}
	canonicalPrevCursor := canonicalizeCursorValue(batch.Chain, batch.PreviousCursorValue)
	canonicalNewCursor := canonicalizeCursorValue(batch.Chain, batch.NewCursorValue)

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

	terminalDecodeErrors, transientDecodeErrors := classifyDecodeErrors(batch.Chain, resp.Errors, log, decodeStage)
	if len(transientDecodeErrors) > 0 {
		return retry.Transient(fmt.Errorf(
			"decode transient stage=%s signatures=%d diagnostics=%s",
			decodeStage,
			len(transientDecodeErrors),
			formatDecodeDiagnostics(transientDecodeErrors),
		))
	}

	expectedSignatures := make(map[string]struct{}, len(canonicalSignatures))
	for _, sig := range canonicalSignatures {
		signatureKey := canonicalSignatureIdentity(batch.Chain, sig.Hash)
		if signatureKey == "" {
			continue
		}
		expectedSignatures[signatureKey] = struct{}{}
	}

	resultBySignature := make(map[string]*sidecarv1.TransactionResult, len(resp.Results))
	unexpectedResults := make(map[string]struct{})
	unexpectedResultBySignature := make(map[string]*sidecarv1.TransactionResult)
	for _, result := range resp.Results {
		if result == nil {
			continue
		}

		signatureKey := canonicalSignatureIdentity(batch.Chain, result.TxHash)
		if signatureKey == "" {
			unexpectedResults["<empty>"] = struct{}{}
			continue
		}
		if _, ok := expectedSignatures[signatureKey]; !ok {
			unexpectedResults[signatureKey] = struct{}{}
			existing, exists := unexpectedResultBySignature[signatureKey]
			if !exists || shouldReplaceDecodedResult(batch.Chain, existing, result) {
				unexpectedResultBySignature[signatureKey] = result
			}
			continue
		}
		existing, ok := resultBySignature[signatureKey]
		if !ok || shouldReplaceDecodedResult(batch.Chain, existing, result) {
			resultBySignature[signatureKey] = result
		}
	}
	unexpectedSignatures := sortedSignatureKeys(unexpectedResults)
	if len(unexpectedSignatures) > 0 {
		log.Warn("decode recovery collision isolated",
			"stage", decodeStage,
			"address", batch.Address,
			"unexpected_signatures", strings.Join(unexpectedSignatures, ","),
		)
	}

	errorBySignature := terminalDecodeErrors

	// Convert to NormalizedBatch
	normalized := event.NormalizedBatch{
		Chain:                  batch.Chain,
		Network:                batch.Network,
		Address:                batch.Address,
		WalletID:               batch.WalletID,
		OrgID:                  batch.OrgID,
		PreviousCursorValue:    canonicalPrevCursor,
		PreviousCursorSequence: batch.PreviousCursorSequence,
		NewCursorValue:         canonicalPrevCursor,
		NewCursorSequence:      batch.PreviousCursorSequence,
	}
	if normalized.NewCursorValue == nil {
		normalized.NewCursorValue = canonicalNewCursor
		normalized.NewCursorSequence = batch.NewCursorSequence
	}

	processed := 0
	type decodeFailure struct {
		signature string
		reason    string
	}
	decodeFailures := make([]decodeFailure, 0, len(canonicalSignatures))

	for _, sig := range canonicalSignatures {
		signature := strings.TrimSpace(sig.Hash)
		signatureKey := canonicalSignatureIdentity(batch.Chain, signature)
		if signatureKey == "" {
			decodeFailures = append(decodeFailures, decodeFailure{
				signature: "<empty>",
				reason:    "empty signature",
			})
			continue
		}
		if errMsg, hasErr := errorBySignature[signatureKey]; hasErr {
			if errMsg == "" {
				errMsg = "decode failed"
			}
			log.Warn("signature decode isolated", "stage", decodeStage, "signature", signature, "error", errMsg)
			decodeFailures = append(decodeFailures, decodeFailure{
				signature: signature,
				reason:    errMsg,
			})
			continue
		}

		var (
			result *sidecarv1.TransactionResult
			ok     bool
		)
		if result, ok = resultBySignature[signatureKey]; ok {
			delete(resultBySignature, signatureKey)
		}
		if result == nil && len(canonicalSignatures) == 1 {
			result = singleSignatureFallbackResult(unexpectedResultBySignature)
			if result != nil {
				delete(unexpectedResultBySignature, canonicalSignatureIdentity(batch.Chain, result.TxHash))
				log.Warn("decode single-signature fallback applied",
					"stage", decodeStage,
					"address", batch.Address,
					"signature", signature,
					"fallback_tx_hash", result.TxHash,
				)
			}
		}
		if result == nil {
			reason := "missing decode result"
			if len(unexpectedSignatures) > 0 {
				reason = fmt.Sprintf("missing decode result (unexpected=%s)", strings.Join(unexpectedSignatures, "|"))
			}
			log.Warn("signature decode isolated", "stage", decodeStage, "signature", signature, "error", reason)
			decodeFailures = append(decodeFailures, decodeFailure{
				signature: signature,
				reason:    reason,
			})
			continue
		}

		normalized.Transactions = append(normalized.Transactions, n.normalizedTxFromResult(batch, result))
		if shouldAdvanceCanonicalCursor(normalized.NewCursorSequence, normalized.NewCursorValue, sig.Sequence, signatureKey) {
			cursorValue := signatureKey
			normalized.NewCursorValue = &cursorValue
			normalized.NewCursorSequence = sig.Sequence
		}
		processed++
	}

	if processed == 0 && len(canonicalSignatures) > 0 {
		diagnostics := make([]string, 0, len(decodeFailures))
		for _, failure := range decodeFailures {
			diagnostics = append(diagnostics, fmt.Sprintf("%s=%s", failure.signature, failure.reason))
		}
		if len(diagnostics) == 0 {
			diagnostics = append(diagnostics, "<unknown>=missing decode result")
		}
		return fmt.Errorf(
			"decode collapse stage=%s no decodable transactions in batch (diagnostics=%s)",
			decodeStage,
			strings.Join(diagnostics, ","),
		)
	}
	if len(decodeFailures) > 0 {
		log.Warn("decode isolation continued batch",
			"stage", decodeStage,
			"address", batch.Address,
			"processed", processed,
			"failed", len(decodeFailures),
		)
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

func (n *Normalizer) normalizedTxFromResult(batch event.RawBatch, result *sidecarv1.TransactionResult) event.NormalizedTransaction {
	txHash := canonicalSignatureIdentity(batch.Chain, result.TxHash)
	if txHash == "" {
		txHash = strings.TrimSpace(result.TxHash)
	}
	finalityState := resolveResultFinalityState(batch.Chain, result)
	tx := event.NormalizedTransaction{
		TxHash:      txHash,
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
			txHash,
			result.Status,
			result.FeePayer,
			result.FeeAmount,
			finalityState,
			result.BalanceEvents,
		)
	} else {
		tx.BalanceEvents = buildCanonicalSolanaBalanceEvents(
			batch.Chain,
			batch.Network,
			txHash,
			result.Status,
			result.FeePayer,
			result.FeeAmount,
			finalityState,
			result.BalanceEvents,
		)
	}

	return tx
}

func buildCanonicalSolanaBalanceEvents(
	chain model.Chain,
	network model.Network,
	txHash, txStatus, feePayer, feeAmount, finalityState string,
	rawEvents []*sidecarv1.BalanceEventInfo,
) []event.NormalizedBalanceEvent {
	finalityState = normalizeFinalityStateOrDefault(chain, finalityState)
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

	if shouldEmitSolanaFeeEvent(txStatus, feePayer, feeAmount) && !hasSolanaFeeEvent(normalizedEvents, feePayer) {
		normalizedEvents = append(normalizedEvents, buildSolanaFeeBalanceEvent(feePayer, feeAmount))
	}

	return canonicalizeSolanaBalanceEvents(chain, network, txHash, finalityState, normalizedEvents)
}

func buildCanonicalBaseBalanceEvents(
	chain model.Chain,
	network model.Network,
	txHash, txStatus, feePayer, feeAmount, finalityState string,
	rawEvents []*sidecarv1.BalanceEventInfo,
) []event.NormalizedBalanceEvent {
	finalityState = normalizeFinalityStateOrDefault(chain, finalityState)
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

	if shouldEmitBaseFeeEvent(txStatus, feePayer, feeAmount) {
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

	return canonicalizeBaseBalanceEvents(chain, network, txHash, finalityState, normalizedEvents)
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

func shouldEmitBaseFeeEvent(txStatus, feePayer, feeAmount string) bool {
	return shouldEmitSolanaFeeEvent(txStatus, feePayer, feeAmount)
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
	finalityState string,
	normalizedEvents []event.NormalizedBalanceEvent,
) []event.NormalizedBalanceEvent {
	finalityState = normalizeFinalityStateOrDefault(chain, finalityState)
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
		be.FinalityState = finalityState
		be.DecoderVersion = "base-decoder-v1"
		be.SchemaVersion = "v2"
		be.EventID = buildCanonicalEventID(
			chain, network,
			txHash, be.EventPath,
			be.ActorAddress, be.AssetID, be.EventCategory,
		)

		existing, ok := eventsByID[be.EventID]
		if !ok || shouldReplaceCanonicalBaseEvent(existing, be) {
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

func isBaseFeeCategory(category model.EventCategory) bool {
	switch category {
	case model.EventCategoryFeeExecutionL2, model.EventCategoryFeeDataL1:
		return true
	default:
		return false
	}
}

func shouldEmitSolanaFeeEvent(txStatus, feePayer, feeAmount string) bool {
	if !isFeeEligibleStatus(txStatus) {
		return false
	}
	if strings.TrimSpace(feePayer) == "" {
		return false
	}
	feeAmountInt, ok := parseBigInt(feeAmount)
	if !ok || feeAmountInt.Sign() == 0 {
		return false
	}
	return true
}

func isFeeEligibleStatus(txStatus string) bool {
	status := strings.TrimSpace(txStatus)
	return strings.EqualFold(status, string(model.TxStatusSuccess)) ||
		strings.EqualFold(status, string(model.TxStatusFailed))
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

type solanaCanonicalSelection struct {
	FromOuterInstruction bool
	OriginalInnerIndex   int32
}

func canonicalizeSolanaBalanceEvents(
	chain model.Chain,
	network model.Network,
	txHash string,
	finalityState string,
	normalizedEvents []event.NormalizedBalanceEvent,
) []event.NormalizedBalanceEvent {
	finalityState = normalizeFinalityStateOrDefault(chain, finalityState)
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
	selectionByEventID := make(map[string]solanaCanonicalSelection, len(normalizedEvents))
	for _, be := range normalizedEvents {
		if be.EventCategory == model.EventCategoryFee {
			be.OuterInstructionIndex = -1
			be.InnerInstructionIndex = -1
			be.Delta = canonicalFeeDelta(be.Delta)
			be.EventAction = "transaction_fee"
		}
		selection := solanaCanonicalSelection{
			FromOuterInstruction: be.InnerInstructionIndex == -1,
			OriginalInnerIndex:   int32(be.InnerInstructionIndex),
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
				selection.FromOuterInstruction = false
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
		be.FinalityState = finalityState
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
			selectionByEventID[be.EventID] = selection
			continue
		}
		if shouldReplaceCanonicalSolanaEvent(existing, be, selectionByEventID[be.EventID], selection) {
			eventsByID[be.EventID] = be
			selectionByEventID[be.EventID] = selection
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

func shouldReplaceCanonicalSolanaEvent(
	existing, incoming event.NormalizedBalanceEvent,
	existingSelection, incomingSelection solanaCanonicalSelection,
) bool {
	if cmp := compareFinalityStateStrength(existing.FinalityState, incoming.FinalityState); cmp != 0 {
		return cmp < 0
	}
	if existing.EventCategory == model.EventCategoryFee || incoming.EventCategory == model.EventCategoryFee {
		if existing.EventAction != incoming.EventAction {
			return incoming.EventAction < existing.EventAction
		}
		return existing.Delta > incoming.Delta
	}
	if existingSelection.FromOuterInstruction != incomingSelection.FromOuterInstruction {
		return incomingSelection.FromOuterInstruction
	}
	if existingSelection.OriginalInnerIndex != incomingSelection.OriginalInnerIndex {
		return incomingSelection.OriginalInnerIndex < existingSelection.OriginalInnerIndex
	}
	if existing.EventAction != incoming.EventAction {
		return incoming.EventAction < existing.EventAction
	}
	if existing.ContractAddress != incoming.ContractAddress {
		return incoming.ContractAddress < existing.ContractAddress
	}
	return existing.Delta > incoming.Delta
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

func canonicalizeBatchSignatures(chainID model.Chain, sigs []event.SignatureInfo) []event.SignatureInfo {
	if len(sigs) == 0 {
		return []event.SignatureInfo{}
	}

	byIdentity := make(map[string]event.SignatureInfo, len(sigs))
	for _, sig := range sigs {
		identity := canonicalSignatureIdentity(chainID, sig.Hash)
		if identity == "" {
			continue
		}
		candidate := event.SignatureInfo{
			Hash:     identity,
			Sequence: sig.Sequence,
		}
		existing, ok := byIdentity[identity]
		if !ok || shouldReplaceCanonicalSignature(existing, candidate) {
			byIdentity[identity] = candidate
		}
	}

	ordered := make([]event.SignatureInfo, 0, len(byIdentity))
	for _, sig := range byIdentity {
		ordered = append(ordered, sig)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Sequence != ordered[j].Sequence {
			return ordered[i].Sequence < ordered[j].Sequence
		}
		return ordered[i].Hash < ordered[j].Hash
	})
	return ordered
}

func shouldReplaceCanonicalSignature(existing, incoming event.SignatureInfo) bool {
	if existing.Sequence != incoming.Sequence {
		return incoming.Sequence > existing.Sequence
	}
	return incoming.Hash < existing.Hash
}

func canonicalizeCursorValue(chainID model.Chain, cursor *string) *string {
	if cursor == nil {
		return nil
	}
	identity := canonicalSignatureIdentity(chainID, *cursor)
	if identity == "" {
		return nil
	}
	value := identity
	return &value
}

func canonicalSignatureIdentity(chainID model.Chain, hash string) string {
	trimmed := strings.TrimSpace(hash)
	if trimmed == "" {
		return ""
	}
	if !isEVMChain(chainID) {
		return trimmed
	}

	withoutPrefix := strings.TrimPrefix(strings.TrimPrefix(trimmed, "0x"), "0X")
	if withoutPrefix == "" {
		return ""
	}
	if isHexString(withoutPrefix) {
		return "0x" + strings.ToLower(withoutPrefix)
	}
	if strings.HasPrefix(trimmed, "0x") || strings.HasPrefix(trimmed, "0X") {
		return "0x" + strings.ToLower(withoutPrefix)
	}
	return trimmed
}

func shouldReplaceDecodedResult(chainID model.Chain, existing, incoming *sidecarv1.TransactionResult) bool {
	if existing == nil {
		return incoming != nil
	}
	if incoming == nil {
		return false
	}

	if existing.BlockCursor != incoming.BlockCursor {
		return incoming.BlockCursor > existing.BlockCursor
	}
	existingFinality := resolveResultFinalityState(chainID, existing)
	incomingFinality := resolveResultFinalityState(chainID, incoming)
	if cmp := compareFinalityStateStrength(existingFinality, incomingFinality); cmp != 0 {
		return cmp < 0
	}

	existingStatus := strings.TrimSpace(existing.Status)
	incomingStatus := strings.TrimSpace(incoming.Status)
	existingSuccess := strings.EqualFold(existingStatus, string(model.TxStatusSuccess))
	incomingSuccess := strings.EqualFold(incomingStatus, string(model.TxStatusSuccess))
	if existingSuccess != incomingSuccess {
		return incomingSuccess
	}
	if existingStatus != incomingStatus {
		return incomingStatus < existingStatus
	}

	existingFee := strings.TrimSpace(existing.FeeAmount)
	incomingFee := strings.TrimSpace(incoming.FeeAmount)
	if existingFee != incomingFee {
		return incomingFee < existingFee
	}

	existingPayer := canonicalSignatureIdentity(chainID, existing.FeePayer)
	incomingPayer := canonicalSignatureIdentity(chainID, incoming.FeePayer)
	if existingPayer != incomingPayer {
		return incomingPayer < existingPayer
	}

	incomingHash := canonicalSignatureIdentity(chainID, incoming.TxHash)
	existingHash := canonicalSignatureIdentity(chainID, existing.TxHash)
	return incomingHash < existingHash
}

func sortedSignatureKeys(values map[string]struct{}) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for key := range values {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func singleSignatureFallbackResult(results map[string]*sidecarv1.TransactionResult) *sidecarv1.TransactionResult {
	if len(results) != 1 {
		return nil
	}
	for _, result := range results {
		return result
	}
	return nil
}

func shouldAdvanceCanonicalCursor(currentSequence int64, currentValue *string, candidateSequence int64, candidateValue string) bool {
	if candidateSequence > currentSequence {
		return true
	}
	if candidateSequence < currentSequence {
		return false
	}
	candidate := strings.TrimSpace(candidateValue)
	if candidate == "" {
		return false
	}
	if currentValue == nil {
		return true
	}
	current := strings.TrimSpace(*currentValue)
	if current == "" {
		return true
	}
	return candidate > current
}

func classifyDecodeErrors(
	chainID model.Chain,
	decodeErrors []*sidecarv1.DecodeError,
	log *slog.Logger,
	stage string,
) (map[string]string, map[string]string) {
	terminalBySignature := make(map[string]string, len(decodeErrors))
	transientBySignature := make(map[string]string)
	for idx, decErr := range decodeErrors {
		if decErr == nil {
			continue
		}
		signatureKey := canonicalSignatureIdentity(chainID, decErr.Signature)
		if signatureKey == "" {
			signatureKey = fmt.Sprintf("<unknown:%03d>", idx)
		}
		reason := strings.TrimSpace(decErr.Error)
		if reason == "" {
			reason = "decode failed"
		}
		decision := retry.Classify(fmt.Errorf("sidecar decode error: %s", reason))
		log.Warn("sidecar decode error",
			"stage", stage,
			"signature", decErr.Signature,
			"error", reason,
			"classification", decision.Class,
			"classification_reason", decision.Reason,
		)
		if decision.IsTransient() {
			existing, exists := transientBySignature[signatureKey]
			if !exists || reason < existing {
				transientBySignature[signatureKey] = reason
			}
			delete(terminalBySignature, signatureKey)
			continue
		}
		if _, transient := transientBySignature[signatureKey]; transient {
			continue
		}
		existing, exists := terminalBySignature[signatureKey]
		if !exists || reason < existing {
			terminalBySignature[signatureKey] = reason
		}
	}
	return terminalBySignature, transientBySignature
}

func formatDecodeDiagnostics(errorBySignature map[string]string) string {
	if len(errorBySignature) == 0 {
		return "<none>"
	}
	signatures := make([]string, 0, len(errorBySignature))
	for signature := range errorBySignature {
		signatures = append(signatures, signature)
	}
	sort.Strings(signatures)
	diagnostics := make([]string, 0, len(signatures))
	for _, signature := range signatures {
		diagnostics = append(diagnostics, fmt.Sprintf("%s=%s", signature, errorBySignature[signature]))
	}
	return strings.Join(diagnostics, ",")
}

func isEVMChain(chainID model.Chain) bool {
	return chainID == model.ChainBase || chainID == model.ChainEthereum
}

func shouldReplaceCanonicalBaseEvent(existing, incoming event.NormalizedBalanceEvent) bool {
	if cmp := compareFinalityStateStrength(existing.FinalityState, incoming.FinalityState); cmp != 0 {
		return cmp < 0
	}
	if existing.EventAction != incoming.EventAction {
		return incoming.EventAction < existing.EventAction
	}
	if existing.ContractAddress != incoming.ContractAddress {
		return incoming.ContractAddress < existing.ContractAddress
	}
	if existing.CounterpartyAddress != incoming.CounterpartyAddress {
		return incoming.CounterpartyAddress < existing.CounterpartyAddress
	}
	return existing.Delta > incoming.Delta
}

func resolveResultFinalityState(chainID model.Chain, result *sidecarv1.TransactionResult) string {
	if result == nil {
		return defaultFinalityState(chainID)
	}
	keys := [...]string{"finality_state", "finality", "commitment", "confirmation_status"}

	best := ""
	for _, be := range result.BalanceEvents {
		if be == nil {
			continue
		}
		for _, key := range keys {
			candidate := canonicalizeFinalityState(be.Metadata[key])
			if candidate == "" {
				continue
			}
			if best == "" || compareFinalityStateStrength(best, candidate) < 0 {
				best = candidate
			}
		}
	}
	if best != "" {
		return best
	}
	return defaultFinalityState(chainID)
}

func normalizeFinalityStateOrDefault(chainID model.Chain, state string) string {
	normalized := canonicalizeFinalityState(state)
	if normalized != "" {
		return normalized
	}
	return defaultFinalityState(chainID)
}

func defaultFinalityState(chainID model.Chain) string {
	if chainID == model.ChainSolana {
		return "finalized"
	}
	if isEVMChain(chainID) {
		return "finalized"
	}
	return "finalized"
}

func canonicalizeFinalityState(state string) string {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "":
		return ""
	case "processed", "pending", "latest", "unsafe":
		return "processed"
	case "confirmed", "accepted":
		return "confirmed"
	case "safe":
		return "safe"
	case "finalized", "finalised":
		return "finalized"
	default:
		return ""
	}
}

func compareFinalityStateStrength(left, right string) int {
	leftRank := finalityStateRank(left)
	rightRank := finalityStateRank(right)
	if leftRank != rightRank {
		if leftRank < rightRank {
			return -1
		}
		return 1
	}
	leftNorm := canonicalizeFinalityState(left)
	rightNorm := canonicalizeFinalityState(right)
	if leftNorm < rightNorm {
		return -1
	}
	if leftNorm > rightNorm {
		return 1
	}
	return 0
}

func finalityStateRank(state string) int {
	switch canonicalizeFinalityState(state) {
	case "processed":
		return 1
	case "confirmed":
		return 2
	case "safe":
		return 3
	case "finalized":
		return 4
	default:
		return 0
	}
}

func isHexString(v string) bool {
	for _, ch := range v {
		switch {
		case ch >= '0' && ch <= '9':
		case ch >= 'a' && ch <= 'f':
		case ch >= 'A' && ch <= 'F':
		default:
			return false
		}
	}
	return true
}

func (n *Normalizer) effectiveRetryMaxAttempts() int {
	if n.retryMaxAttempts <= 0 {
		return 1
	}
	return n.retryMaxAttempts
}

func (n *Normalizer) retryDelay(attempt int) time.Duration {
	delay := n.retryDelayStart
	if delay <= 0 {
		return 0
	}
	if attempt <= 1 {
		if n.retryDelayMax > 0 && delay > n.retryDelayMax {
			return n.retryDelayMax
		}
		return delay
	}
	for i := 1; i < attempt; i++ {
		delay *= 2
		if n.retryDelayMax > 0 && delay >= n.retryDelayMax {
			return n.retryDelayMax
		}
	}
	return delay
}

func (n *Normalizer) sleep(ctx context.Context, delay time.Duration) error {
	if n.sleepFn == nil {
		n.sleepFn = sleepContext
	}
	return n.sleepFn(ctx, delay)
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
