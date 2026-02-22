package normalizer

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"

	sidecarv1 "github.com/emperorhan/multichain-indexer/pkg/generated/sidecar/v1"

	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/identity"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/retry"
)

func canonicalizeBatchSignatures(chainID model.Chain, sigs []event.SignatureInfo) []event.SignatureInfo {
	if len(sigs) == 0 {
		return []event.SignatureInfo{}
	}

	byIdentity := make(map[string]event.SignatureInfo, len(sigs))
	for _, sig := range sigs {
		identity := identity.CanonicalSignatureIdentity(chainID, sig.Hash)
		if identity == "" {
			continue
		}
		candidate := event.SignatureInfo{
			Hash:     identity,
			Sequence: sig.Sequence,
			Time:     sig.Time,
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

func canonicalizeAddressIdentity(chainID model.Chain, value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	if !identity.IsEVMChain(chainID) {
		return trimmed
	}
	identity := identity.CanonicalSignatureIdentity(chainID, trimmed)
	if identity == "" {
		return trimmed
	}
	return identity
}

func metadataFromChainData(chainData json.RawMessage) map[string]string {
	metadata := map[string]string{}
	if len(chainData) == 0 || string(chainData) == "null" {
		return metadata
	}
	_ = json.Unmarshal(chainData, &metadata)
	return metadata
}

func solanaInstructionPathFromChainData(chainData json.RawMessage) (int, int, bool) {
	metadata := metadataFromChainData(chainData)
	if len(metadata) == 0 {
		return 0, 0, false
	}
	for _, key := range []string{"event_path", "instruction_path", "solana_event_path"} {
		if outer, inner, ok := parseSolanaInstructionPath(metadata[key]); ok {
			return outer, inner, true
		}
	}
	return 0, 0, false
}

func parseSolanaInstructionPath(path string) (int, int, bool) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return 0, 0, false
	}
	compact := strings.ReplaceAll(strings.ToLower(trimmed), " ", "")
	parts := strings.Split(compact, "|")
	if len(parts) != 2 {
		return 0, 0, false
	}

	outer, okOuter := parsePathIndexPart(parts[0], "outer")
	inner, okInner := parsePathIndexPart(parts[1], "inner")
	if !okOuter || !okInner {
		return 0, 0, false
	}
	return outer, inner, true
}

func parsePathIndexPart(part, key string) (int, bool) {
	if part == "" {
		return 0, false
	}
	var raw string
	switch {
	case strings.HasPrefix(part, key+":"):
		raw = strings.TrimPrefix(part, key+":")
	case strings.HasPrefix(part, key+"="):
		raw = strings.TrimPrefix(part, key+"=")
	default:
		return 0, false
	}
	if raw == "" {
		return 0, false
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false
	}
	return parsed, true
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

	existingPayer := identity.CanonicalSignatureIdentity(chainID, existing.FeePayer)
	incomingPayer := identity.CanonicalSignatureIdentity(chainID, incoming.FeePayer)
	if existingPayer != incomingPayer {
		return incomingPayer < existingPayer
	}

	incomingHash := identity.CanonicalSignatureIdentity(chainID, incoming.TxHash)
	existingHash := identity.CanonicalSignatureIdentity(chainID, existing.TxHash)
	if incomingHash != existingHash {
		return incomingHash < existingHash
	}

	existingScore := decodedResultSelectionScore(chainID, existing)
	incomingScore := decodedResultSelectionScore(chainID, incoming)
	if existingScore.eventCount != incomingScore.eventCount {
		return incomingScore.eventCount > existingScore.eventCount
	}
	if existingScore.pathHintCount != incomingScore.pathHintCount {
		return incomingScore.pathHintCount > existingScore.pathHintCount
	}
	if existingScore.metadataCount != incomingScore.metadataCount {
		return incomingScore.metadataCount > existingScore.metadataCount
	}
	if existingScore.fingerprint != incomingScore.fingerprint {
		return incomingScore.fingerprint < existingScore.fingerprint
	}
	return false
}

func reconcileDecodedResultCoverage(chainID model.Chain, existing, incoming *sidecarv1.TransactionResult) *sidecarv1.TransactionResult {
	if existing == nil {
		return incoming
	}
	if incoming == nil {
		return existing
	}

	preferred := existing
	secondary := incoming
	if shouldReplaceDecodedResult(chainID, existing, incoming) {
		preferred = incoming
		secondary = existing
	}

	out := cloneDecodedResult(preferred)
	out.BalanceEvents = mergeDecodedBalanceEvents(chainID, preferred.BalanceEvents, secondary.BalanceEvents)
	return out
}

func (n *Normalizer) reconcileCoverageRegressionFlap(
	log *slog.Logger,
	batch event.RawBatch,
	signatureKey string,
	incoming *sidecarv1.TransactionResult,
) *sidecarv1.TransactionResult {
	if incoming == nil || signatureKey == "" {
		return incoming
	}
	if n.coverageFloor == nil {
		return incoming
	}

	floorKey := coverageFloorKey(batch.Chain, batch.Network, batch.Address, signatureKey)

	existing, _ := n.coverageFloor.Get(floorKey)
	missingFromIncoming, newlyObserved := decodedCoverageDelta(batch.Chain, existing, incoming)
	reconciled := reconcileDecodedResultCoverage(batch.Chain, existing, incoming)
	n.coverageFloor.Put(floorKey, cloneDecodedResult(reconciled))

	if missingFromIncoming > 0 {
		log.Warn("decode coverage regression floor applied",
			"stage", "normalizer.decode_batch",
			"address", batch.Address,
			"signature", signatureKey,
			"missing_events", missingFromIncoming,
			"new_events", newlyObserved,
		)
	}

	return reconciled
}

func coverageFloorKey(chainID model.Chain, network model.Network, address, signature string) string {
	canonicalAddress := canonicalizeAddressIdentity(chainID, address)
	if canonicalAddress == "" {
		canonicalAddress = strings.TrimSpace(address)
	}
	return string(chainID) + "|" + string(network) + "|" + canonicalAddress + "|" + signature
}

func decodedCoverageDelta(chainID model.Chain, floor, incoming *sidecarv1.TransactionResult) (missingFromIncoming, newlyObserved int) {
	floorEvents := decodedCoverageEventSet(chainID, floor)
	incomingEvents := decodedCoverageEventSet(chainID, incoming)

	for key := range floorEvents {
		if _, ok := incomingEvents[key]; !ok {
			missingFromIncoming++
		}
	}
	for key := range incomingEvents {
		if _, ok := floorEvents[key]; !ok {
			newlyObserved++
		}
	}
	return missingFromIncoming, newlyObserved
}

func decodedCoverageEventSet(chainID model.Chain, result *sidecarv1.TransactionResult) map[string]struct{} {
	if result == nil || len(result.BalanceEvents) == 0 {
		return map[string]struct{}{}
	}

	out := make(map[string]struct{}, len(result.BalanceEvents))
	for _, be := range result.BalanceEvents {
		if be == nil {
			continue
		}
		key := decodedCoverageEventKey(chainID, be)
		if key == "" {
			key = decodedEventFingerprint(chainID, be)
		}
		if key == "" {
			continue
		}
		out[key] = struct{}{}
	}
	return out
}

func mergeDecodedBalanceEvents(chainID model.Chain, primary, secondary []*sidecarv1.BalanceEventInfo) []*sidecarv1.BalanceEventInfo {
	if len(primary) == 0 && len(secondary) == 0 {
		return nil
	}

	byKey := make(map[string]*sidecarv1.BalanceEventInfo, len(primary)+len(secondary))
	addEvent := func(be *sidecarv1.BalanceEventInfo) {
		if be == nil {
			return
		}
		key := decodedCoverageEventKey(chainID, be)
		if key == "" {
			key = decodedEventFingerprint(chainID, be)
		}
		if key == "" {
			return
		}
		existing, ok := byKey[key]
		if !ok || shouldReplaceDecodedCoverageEvent(chainID, existing, be) {
			byKey[key] = cloneDecodedBalanceEvent(be)
		}
	}

	for _, be := range primary {
		addEvent(be)
	}
	for _, be := range secondary {
		addEvent(be)
	}

	reconcileDecodedCoverageLineageAliases(chainID, byKey)

	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	out := make([]*sidecarv1.BalanceEventInfo, 0, len(keys))
	for _, key := range keys {
		out = append(out, byKey[key])
	}
	return out
}

func reconcileDecodedCoverageLineageAliases(chainID model.Chain, byKey map[string]*sidecarv1.BalanceEventInfo) {
	if len(byKey) < 2 {
		return
	}

	lineageGroups := make(map[string][]string, len(byKey))
	for key, be := range byKey {
		lineageKey := decodedCoverageEventLineageKey(chainID, be)
		if lineageKey == "" {
			continue
		}
		lineageGroups[lineageKey] = append(lineageGroups[lineageKey], key)
	}

	lineageKeys := make([]string, 0, len(lineageGroups))
	for lineageKey := range lineageGroups {
		lineageKeys = append(lineageKeys, lineageKey)
	}
	sort.Strings(lineageKeys)

	for _, lineageKey := range lineageKeys {
		groupKeys := lineageGroups[lineageKey]
		if len(groupKeys) < 2 {
			continue
		}
		sort.Strings(groupKeys)

		pathHintedKeys := make([]string, 0, len(groupKeys))
		pathlessKeys := make([]string, 0, len(groupKeys))
		for _, key := range groupKeys {
			be := byKey[key]
			if be == nil {
				continue
			}
			if decodedCoverageEventHasPathHint(chainID, be) {
				pathHintedKeys = append(pathHintedKeys, key)
			} else {
				pathlessKeys = append(pathlessKeys, key)
			}
		}
		// Only collapse aliases when there is a single path-hinted representative
		// and one or more lower-fidelity pathless variants.
		if len(pathHintedKeys) != 1 || len(pathlessKeys) == 0 {
			continue
		}

		keeperKey := pathHintedKeys[0]
		keeper := byKey[keeperKey]
		for _, key := range groupKeys {
			if key == keeperKey {
				continue
			}
			candidate := byKey[key]
			if candidate == nil {
				continue
			}
			if shouldReplaceDecodedCoverageEvent(chainID, keeper, candidate) {
				keeper = candidate
				keeperKey = key
			}
		}
		if !decodedCoverageEventHasPathHint(chainID, keeper) {
			continue
		}

		for _, key := range pathlessKeys {
			if key == keeperKey {
				continue
			}
			mergeDecodedCoverageEventMetadata(chainID, keeper, byKey[key])
			delete(byKey, key)
		}
		byKey[keeperKey] = keeper
	}
}

func mergeDecodedCoverageEventMetadata(chainID model.Chain, preferred, additional *sidecarv1.BalanceEventInfo) {
	if preferred == nil || additional == nil || len(additional.Metadata) == 0 {
		return
	}
	preferredHadCompleteFeeSplit := false
	additionalHasCompleteFeeSplit := false
	if identity.IsEVMChain(chainID) {
		_, preferredHasExecution := deriveBaseExecutionFeeFromMetadata(preferred.Metadata)
		_, preferredHasData := deriveBaseDataFee(preferred.Metadata)
		preferredHadCompleteFeeSplit = preferredHasExecution && preferredHasData

		_, additionalHasExecution := deriveBaseExecutionFeeFromMetadata(additional.Metadata)
		_, additionalHasData := deriveBaseDataFee(additional.Metadata)
		additionalHasCompleteFeeSplit = additionalHasExecution && additionalHasData
	}

	if preferred.Metadata == nil {
		preferred.Metadata = make(map[string]string, len(additional.Metadata))
	}
	for key, value := range additional.Metadata {
		if _, exists := preferred.Metadata[key]; exists {
			continue
		}
		preferred.Metadata[key] = value
	}

	if !identity.IsEVMChain(chainID) {
		return
	}
	// If alias metadata contributes a complete fee split while the preferred
	// event does not, copy the component keys verbatim to preserve fee coverage.
	if additionalHasCompleteFeeSplit && !preferredHadCompleteFeeSplit {
		for _, key := range baseFeeComponentMetadataKeys {
			value := strings.TrimSpace(additional.Metadata[key])
			if value == "" {
				continue
			}
			preferred.Metadata[key] = value
		}
	}
}

func shouldReplaceDecodedCoverageEvent(chainID model.Chain, existing, incoming *sidecarv1.BalanceEventInfo) bool {
	if existing == nil {
		return incoming != nil
	}
	if incoming == nil {
		return false
	}

	existingScore := decodedCoverageEventSelectionScore(chainID, existing)
	incomingScore := decodedCoverageEventSelectionScore(chainID, incoming)
	if existingScore.pathHint != incomingScore.pathHint {
		return incomingScore.pathHint
	}
	if existingScore.metadataCount != incomingScore.metadataCount {
		return incomingScore.metadataCount > existingScore.metadataCount
	}
	if existingScore.populatedFieldCount != incomingScore.populatedFieldCount {
		return incomingScore.populatedFieldCount > existingScore.populatedFieldCount
	}
	if existingScore.fingerprint != incomingScore.fingerprint {
		return incomingScore.fingerprint < existingScore.fingerprint
	}
	return false
}

type decodedCoverageEventScore struct {
	pathHint            bool
	metadataCount       int
	populatedFieldCount int
	fingerprint         string
}

func decodedCoverageEventSelectionScore(chainID model.Chain, be *sidecarv1.BalanceEventInfo) decodedCoverageEventScore {
	if be == nil {
		return decodedCoverageEventScore{}
	}
	score := decodedCoverageEventScore{
		metadataCount: len(be.Metadata),
		fingerprint:   decodedEventFingerprint(chainID, be),
	}
	score.pathHint = decodedCoverageEventHasPathHint(chainID, be)

	eventCategory := strings.TrimSpace(be.EventCategory)
	eventAction := strings.TrimSpace(be.EventAction)
	programID := strings.TrimSpace(be.ProgramId)
	address := strings.TrimSpace(be.Address)
	contractAddress := strings.TrimSpace(be.ContractAddress)
	counterparty := strings.TrimSpace(be.CounterpartyAddress)
	tokenSymbol := strings.TrimSpace(be.TokenSymbol)
	tokenName := strings.TrimSpace(be.TokenName)
	delta := strings.TrimSpace(be.Delta)
	tokenType := strings.TrimSpace(be.TokenType)

	for _, value := range []string{
		eventCategory,
		eventAction,
		programID,
		address,
		contractAddress,
		counterparty,
		tokenSymbol,
		tokenName,
		delta,
	} {
		if value != "" {
			score.populatedFieldCount++
		}
	}
	if be.TokenDecimals != 0 {
		score.populatedFieldCount++
	}
	if tokenType != "" {
		score.populatedFieldCount++
	}
	return score
}

func decodedCoverageEventHasPathHint(chainID model.Chain, be *sidecarv1.BalanceEventInfo) bool {
	if be == nil {
		return false
	}
	if identity.IsEVMChain(chainID) {
		return resolveBaseMetadataEventPath(be.Metadata) != ""
	}
	if chainID == model.ChainSolana {
		return hasSolanaInstructionPathHint(be.Metadata)
	}
	return strings.TrimSpace(be.Metadata["event_path"]) != ""
}

func decodedCoverageEventLineageKey(chainID model.Chain, be *sidecarv1.BalanceEventInfo) string {
	if be == nil {
		return ""
	}

	trimmedAddress := strings.TrimSpace(be.Address)
	trimmedContract := strings.TrimSpace(be.ContractAddress)
	trimmedCounterparty := strings.TrimSpace(be.CounterpartyAddress)
	trimmedCategory := strings.TrimSpace(be.EventCategory)
	trimmedAction := strings.TrimSpace(be.EventAction)
	trimmedDelta := strings.TrimSpace(be.Delta)

	address := canonicalizeAddressIdentity(chainID, trimmedAddress)
	if address == "" {
		address = trimmedAddress
	}
	assetID := canonicalizeAddressIdentity(chainID, trimmedContract)
	if assetID == "" {
		assetID = trimmedContract
	}
	counterparty := canonicalizeAddressIdentity(chainID, trimmedCounterparty)
	if counterparty == "" {
		counterparty = trimmedCounterparty
	}

	category := strings.ToUpper(trimmedCategory)
	if category == "" {
		category = strings.ToUpper(trimmedAction)
	}

	delta := trimmedDelta
	if strings.EqualFold(category, string(model.EventCategoryFee)) ||
		strings.EqualFold(category, string(model.EventCategoryFeeExecutionL2)) ||
		strings.EqualFold(category, string(model.EventCategoryFeeDataL1)) {
		delta = canonicalFeeDelta(delta)
	}

	return category + "|" + address + "|" + assetID + "|" + counterparty + "|" + delta
}

func decodedCoverageEventKey(chainID model.Chain, be *sidecarv1.BalanceEventInfo) string {
	if be == nil {
		return ""
	}
	path := decodedEventPath(chainID, be)
	address := canonicalizeAddressIdentity(chainID, be.Address)
	if address == "" {
		address = strings.TrimSpace(be.Address)
	}
	assetID := canonicalizeAddressIdentity(chainID, be.ContractAddress)
	if assetID == "" {
		assetID = strings.TrimSpace(be.ContractAddress)
	}
	category := strings.ToUpper(strings.TrimSpace(be.EventCategory))
	if category == "" {
		category = strings.ToUpper(strings.TrimSpace(be.EventAction))
	}
	return path + "|" + address + "|" + assetID + "|" + category
}

func cloneDecodedResult(result *sidecarv1.TransactionResult) *sidecarv1.TransactionResult {
	if result == nil {
		return nil
	}
	cloned := *result // shallow copy

	// Deep copy mutable pointer field
	if result.Error != nil {
		errCopy := *result.Error
		cloned.Error = &errCopy
	}

	// Deep copy mutable repeated fields
	if len(result.BalanceEvents) > 0 {
		cloned.BalanceEvents = make([]*sidecarv1.BalanceEventInfo, len(result.BalanceEvents))
		for i, be := range result.BalanceEvents {
			cloned.BalanceEvents[i] = cloneDecodedBalanceEvent(be)
		}
	}

	return &cloned
}

func cloneDecodedBalanceEvent(be *sidecarv1.BalanceEventInfo) *sidecarv1.BalanceEventInfo {
	if be == nil {
		return nil
	}
	cloned := *be // shallow copy

	// Deep copy the Metadata map (mutable)
	if len(be.Metadata) > 0 {
		cloned.Metadata = make(map[string]string, len(be.Metadata))
		for k, v := range be.Metadata {
			cloned.Metadata[k] = v
		}
	}

	return &cloned
}

type decodedResultScore struct {
	eventCount    int
	metadataCount int
	pathHintCount int
	fingerprint   string
}

func decodedResultSelectionScore(chainID model.Chain, result *sidecarv1.TransactionResult) decodedResultScore {
	if result == nil {
		return decodedResultScore{}
	}
	score := decodedResultScore{}
	eventFingerprints := make([]string, 0, len(result.BalanceEvents))
	for _, be := range result.BalanceEvents {
		if be == nil {
			continue
		}
		score.eventCount++
		score.metadataCount += len(be.Metadata)
		if resolveBaseMetadataEventPath(be.Metadata) != "" || hasSolanaInstructionPathHint(be.Metadata) {
			score.pathHintCount++
		}
		eventFingerprints = append(eventFingerprints, decodedEventFingerprint(chainID, be))
	}
	sort.Strings(eventFingerprints)
	score.fingerprint = strings.Join(eventFingerprints, ";")
	return score
}

func hasSolanaInstructionPathHint(metadata map[string]string) bool {
	if len(metadata) == 0 {
		return false
	}
	for _, key := range []string{"event_path", "instruction_path", "solana_event_path"} {
		if _, _, ok := parseSolanaInstructionPath(metadata[key]); ok {
			return true
		}
	}
	return false
}

func decodedEventFingerprint(chainID model.Chain, be *sidecarv1.BalanceEventInfo) string {
	if be == nil {
		return ""
	}
	address := canonicalizeAddressIdentity(chainID, be.Address)
	assetID := canonicalizeAddressIdentity(chainID, be.ContractAddress)
	if assetID == "" {
		assetID = strings.TrimSpace(be.ContractAddress)
	}
	path := decodedEventPath(chainID, be)
	category := strings.ToUpper(strings.TrimSpace(be.EventCategory))
	delta := strings.TrimSpace(be.Delta)
	if strings.EqualFold(category, string(model.EventCategoryFee)) ||
		strings.EqualFold(category, string(model.EventCategoryFeeExecutionL2)) ||
		strings.EqualFold(category, string(model.EventCategoryFeeDataL1)) {
		delta = canonicalFeeDelta(delta)
	}
	return path + "|" + address + "|" + assetID + "|" + delta
}

func decodedEventPath(chainID model.Chain, be *sidecarv1.BalanceEventInfo) string {
	if be == nil {
		return ""
	}
	if identity.IsEVMChain(chainID) {
		if metadataPath := resolveBaseMetadataEventPath(be.Metadata); metadataPath != "" {
			return metadataPath
		}
	} else {
		for _, key := range []string{"event_path", "instruction_path", "solana_event_path"} {
			if outer, inner, ok := parseSolanaInstructionPath(be.Metadata[key]); ok {
				return balanceEventPath(int32(outer), int32(inner))
			}
		}
	}
	return balanceEventPath(be.OuterInstructionIndex, be.InnerInstructionIndex)
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
		signatureKey := identity.CanonicalSignatureIdentity(chainID, decErr.Signature)
		if signatureKey == "" {
			signatureKey = fmt.Sprintf("<unknown:%03d>", idx)
		}
		reason := strings.TrimSpace(decErr.Error)
		if reason == "" {
			reason = "decode failed"
		}
		decision := retry.Classify(errors.New("sidecar decode error: " + reason))
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
		diagnostics = append(diagnostics, signature+"="+errorBySignature[signature])
	}
	return strings.Join(diagnostics, ",")
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
	return compareCanonicalDeltas(existing.Delta, incoming.Delta) > 0
}
