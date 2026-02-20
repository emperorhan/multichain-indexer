package normalizer

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	sidecarv1 "github.com/emperorhan/multichain-indexer/pkg/generated/sidecar/v1"

	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/emperorhan/multichain-indexer/internal/metrics"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/retry"
	"github.com/emperorhan/multichain-indexer/internal/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	otelTrace "go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
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
	tlsEnabled       bool
	tlsCA            string
	tlsCert          string
	tlsKey           string
}

type Option func(*Normalizer)

// WithTLS configures TLS for the gRPC connection to the sidecar.
// If enabled is false, insecure credentials are used (suitable for local dev).
// caPath is required when enabled. certPath and keyPath are optional (for mTLS).
func WithTLS(enabled bool, caPath, certPath, keyPath string) Option {
	return func(n *Normalizer) {
		n.tlsEnabled = enabled
		n.tlsCA = caPath
		n.tlsCert = certPath
		n.tlsKey = keyPath
	}
}

func New(
	sidecarAddr string,
	sidecarTimeout time.Duration,
	rawBatchCh <-chan event.RawBatch,
	normalizedCh chan<- event.NormalizedBatch,
	workerCount int,
	logger *slog.Logger,
	opts ...Option,
) *Normalizer {
	n := &Normalizer{
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
	for _, opt := range opts {
		if opt != nil {
			opt(n)
		}
	}
	return n
}

func (n *Normalizer) Run(ctx context.Context) error {
	n.logger.Info("normalizer started", "sidecar_addr", n.sidecarAddr, "workers", n.workerCount, "tls", n.tlsEnabled)

	transportCreds, err := n.buildTransportCredentials()
	if err != nil {
		return fmt.Errorf("build transport credentials: %w", err)
	}

	conn, err := grpc.NewClient(
		n.sidecarAddr,
		grpc.WithTransportCredentials(transportCreds),
	)
	if err != nil {
		return fmt.Errorf("connect sidecar: %w", err)
	}
	defer conn.Close()

	client := sidecarv1.NewChainDecoderClient(conn)

	// Use errgroup so that a worker error propagates up and cancels all
	// sibling workers via gCtx, achieving fail-fast without panic.
	g, gCtx := errgroup.WithContext(ctx)
	for i := 0; i < n.workerCount; i++ {
		workerID := i
		g.Go(func() error {
			return n.worker(gCtx, workerID, client)
		})
	}

	waitErr := g.Wait()
	n.logger.Info("normalizer stopped")
	return waitErr
}

func (n *Normalizer) worker(ctx context.Context, workerID int, client sidecarv1.ChainDecoderClient) error {
	log := n.logger.With("worker", workerID)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case batch, ok := <-n.rawBatchCh:
			if !ok {
				return nil
			}
			spanCtx, span := tracing.Tracer("normalizer").Start(ctx, "normalizer.processBatch",
				otelTrace.WithAttributes(
					attribute.String("chain", batch.Chain.String()),
					attribute.String("network", batch.Network.String()),
					attribute.String("address", batch.Address),
				),
			)
			start := time.Now()
			if err := n.processBatchWithRetry(spanCtx, log, client, batch); err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
				span.End()
				metrics.NormalizerErrors.WithLabelValues(batch.Chain.String(), batch.Network.String()).Inc()
				metrics.NormalizerLatency.WithLabelValues(batch.Chain.String(), batch.Network.String()).Observe(time.Since(start).Seconds())
				log.Error("process batch failed",
					"address", batch.Address,
					"error", err,
				)
				// Fail-fast: return error so errgroup cancels all workers and
				// the pipeline restarts from the last committed cursor.
				return fmt.Errorf("normalizer process batch failed: address=%s: %w", batch.Address, err)
			}
			span.End()
			metrics.NormalizerBatchesProcessed.WithLabelValues(batch.Chain.String(), batch.Network.String()).Inc()
			metrics.NormalizerLatency.WithLabelValues(batch.Chain.String(), batch.Network.String()).Observe(time.Since(start).Seconds())
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

	grpcCtx, grpcSpan := tracing.Tracer("normalizer").Start(ctx, "normalizer.grpcDecode",
		otelTrace.WithAttributes(
			attribute.String("chain", batch.Chain.String()),
			attribute.Int("tx_count", len(batch.RawTransactions)),
		),
	)

	callCtx, cancel := context.WithTimeout(grpcCtx, n.sidecarTimeout)
	defer cancel()

	resp, err := client.DecodeSolanaTransactionBatch(callCtx, &sidecarv1.DecodeSolanaTransactionBatchRequest{
		Transactions:     rawTxs,
		WatchedAddresses: []string{batch.Address},
	})
	if err != nil {
		grpcSpan.RecordError(err)
		grpcSpan.SetStatus(codes.Error, err.Error())
		grpcSpan.End()
		return fmt.Errorf("sidecar decode: %w", err)
	}
	grpcSpan.SetAttributes(attribute.Int("results_count", len(resp.GetResults())))
	grpcSpan.End()

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
		BlockScanMode:          batch.BlockScanMode,
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

	isBaseChain := isEVMChain(batch.Chain)
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
			batch.Address,
		)
	} else if isBTCChain {
		tx.BalanceEvents = buildCanonicalBTCBalanceEvents(
			batch.Chain,
			batch.Network,
			txHash,
			result.Status,
			result.FeePayer,
			result.FeeAmount,
			finalityState,
			result.BalanceEvents,
			batch.Address,
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
			batch.Address,
		)
	}

	return tx
}

func resolveResultFinalityState(chainID model.Chain, result *sidecarv1.TransactionResult) string {
	if result == nil {
		return defaultFinalityState(chainID)
	}
	metadataKeys := finalityMetadataKeys(chainID)

	best := ""
	bestRank := 0
	bestKeyOrder := len(metadataKeys)
	for _, be := range result.BalanceEvents {
		if be == nil {
			continue
		}
		for keyOrder, key := range metadataKeys {
			candidate := canonicalizeFinalityState(be.Metadata[key])
			if candidate == "" {
				continue
			}
			candidateRank := finalityStateRank(candidate)
			if candidateRank > bestRank || (candidateRank == bestRank && keyOrder < bestKeyOrder) {
				best = candidate
				bestRank = candidateRank
				bestKeyOrder = keyOrder
			}
		}
	}
	if best != "" {
		return best
	}
	return defaultFinalityState(chainID)
}

func finalityMetadataKeys(chainID model.Chain) []string {
	if chainID == model.ChainSolana {
		return []string{"commitment", "confirmation_status", "finality_state", "finality"}
	}
	if chainID == model.ChainBTC {
		return []string{"finality_state", "finality", "confirmation_status", "commitment"}
	}
	if isEVMChain(chainID) {
		return []string{"finality_state", "finality", "confirmation_status", "commitment"}
	}
	return []string{"finality_state", "finality", "confirmation_status", "commitment"}
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

func (n *Normalizer) buildTransportCredentials() (credentials.TransportCredentials, error) {
	if !n.tlsEnabled {
		return insecure.NewCredentials(), nil
	}

	caCert, err := os.ReadFile(n.tlsCA)
	if err != nil {
		return nil, fmt.Errorf("read CA cert %s: %w", n.tlsCA, err)
	}
	certPool := x509.NewCertPool()
	if !certPool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("failed to parse CA cert %s", n.tlsCA)
	}

	tlsCfg := &tls.Config{
		RootCAs:    certPool,
		MinVersion: tls.VersionTLS12,
	}

	// mTLS: load client certificate if provided.
	if n.tlsCert != "" && n.tlsKey != "" {
		clientCert, err := tls.LoadX509KeyPair(n.tlsCert, n.tlsKey)
		if err != nil {
			return nil, fmt.Errorf("load client cert/key: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{clientCert}
	}

	return credentials.NewTLS(tlsCfg), nil
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
