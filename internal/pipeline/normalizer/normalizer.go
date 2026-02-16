package normalizer

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
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
	coverageFloorMu  sync.Mutex
	coverageFloor    map[string]*sidecarv1.TransactionResult
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
			unexpectedResultBySignature[signatureKey] = reconcileDecodedResultCoverage(batch.Chain, unexpectedResultBySignature[signatureKey], result)
			continue
		}
		resultBySignature[signatureKey] = reconcileDecodedResultCoverage(batch.Chain, resultBySignature[signatureKey], result)
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
		result = n.reconcileCoverageRegressionFlap(log, batch, signatureKey, result)

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
	isBTCChain := batch.Chain == model.ChainBTC
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
	} else if isBTCChain {
		tx.BalanceEvents = buildCanonicalBTCBalanceEvents(
			batch.Chain,
			batch.Network,
			txHash,
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
