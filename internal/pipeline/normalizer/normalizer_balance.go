package normalizer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strings"

	sidecarv1 "github.com/emperorhan/multichain-indexer/pkg/generated/sidecar/v1"

	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
)

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
		nativeToken := evmNativeToken(chain)
		if isEVML1Chain(chain) {
			// L1: single FEE event (no execution/data split)
			normalizedEvents = append(normalizedEvents, buildEVML1FeeBalanceEvent(feePayer, feeAmount, eventPath, nativeToken))
		} else {
			// L2: dual fee events (fee_execution_l2 + fee_data_l1)
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
	}

	return canonicalizeBaseBalanceEvents(chain, network, txHash, finalityState, normalizedEvents)
}

func buildCanonicalBTCBalanceEvents(
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

	if shouldEmitBTCFeeEvent(txStatus, feePayer, feeAmount, normalizedEvents) && !hasBTCFeeEvent(normalizedEvents, feePayer) {
		normalizedEvents = append(normalizedEvents, buildBTCFeeBalanceEvent(feePayer, feeAmount))
	}

	return canonicalizeBTCBalanceEvents(chain, network, txHash, finalityState, normalizedEvents)
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
	reconcileBaseFeeComponentMetadata(out, rawEvents)
	return out
}

var baseFeePathMetadataKeys = [...]string{
	"event_path",
	"log_path",
	"log_index",
	"base_log_index",
	"base_event_path",
}

var baseFeeComponentMetadataKeys = [...]string{
	"fee_execution_l2",
	"fee_data_l1",
	"data_fee_l1",
	"l1_data_fee",
	"l1_fee",
}

type baseFeeMetadataCandidate struct {
	metadata      map[string]string
	hasExecution  bool
	execution     *big.Int
	hasData       bool
	data          *big.Int
	pathHint      bool
	metadataCount int
	fingerprint   string
}

func newBaseFeeMetadataCandidate(be *sidecarv1.BalanceEventInfo) *baseFeeMetadataCandidate {
	if be == nil || len(be.Metadata) == 0 {
		return nil
	}
	execution, hasExecution := deriveBaseExecutionFeeFromMetadata(be.Metadata)
	data, hasData := deriveBaseDataFee(be.Metadata)
	return &baseFeeMetadataCandidate{
		metadata:      be.Metadata,
		hasExecution:  hasExecution,
		execution:     execution,
		hasData:       hasData,
		data:          data,
		pathHint:      resolveBaseMetadataEventPath(be.Metadata) != "",
		metadataCount: len(be.Metadata),
		fingerprint:   decodedEventFingerprint(model.ChainBase, be),
	}
}

func shouldPreferBaseFeeMetadataCandidate(existing, incoming *baseFeeMetadataCandidate) bool {
	if existing == nil {
		return incoming != nil
	}
	if incoming == nil {
		return false
	}
	if existing.pathHint != incoming.pathHint {
		return incoming.pathHint
	}
	if existing.metadataCount != incoming.metadataCount {
		return incoming.metadataCount > existing.metadataCount
	}
	if existing.fingerprint != incoming.fingerprint {
		return incoming.fingerprint < existing.fingerprint
	}
	return false
}

func pickBestBaseFeeMetadataCandidate(
	candidates []*baseFeeMetadataCandidate,
	predicate func(*baseFeeMetadataCandidate) bool,
) *baseFeeMetadataCandidate {
	var best *baseFeeMetadataCandidate
	for _, candidate := range candidates {
		if candidate == nil {
			continue
		}
		if predicate != nil && !predicate(candidate) {
			continue
		}
		if best == nil || shouldPreferBaseFeeMetadataCandidate(best, candidate) {
			best = candidate
		}
	}
	return best
}

func applyBasePathMetadata(out map[string]string, metadata map[string]string) {
	for _, key := range baseFeePathMetadataKeys {
		delete(out, key)
	}
	if len(metadata) == 0 {
		return
	}
	for _, key := range baseFeePathMetadataKeys {
		value := strings.TrimSpace(metadata[key])
		if value == "" {
			continue
		}
		out[key] = value
	}
}

func reconcileBaseFeeComponentMetadata(out map[string]string, rawEvents []*sidecarv1.BalanceEventInfo) {
	candidates := make([]*baseFeeMetadataCandidate, 0, len(rawEvents))
	for _, be := range rawEvents {
		candidate := newBaseFeeMetadataCandidate(be)
		if candidate != nil {
			candidates = append(candidates, candidate)
		}
	}
	if len(candidates) == 0 {
		return
	}

	bestBoth := pickBestBaseFeeMetadataCandidate(candidates, func(candidate *baseFeeMetadataCandidate) bool {
		return candidate.hasExecution && candidate.hasData
	})
	bestExecution := pickBestBaseFeeMetadataCandidate(candidates, func(candidate *baseFeeMetadataCandidate) bool {
		return candidate.hasExecution
	})
	bestData := pickBestBaseFeeMetadataCandidate(candidates, func(candidate *baseFeeMetadataCandidate) bool {
		return candidate.hasData
	})

	selected := bestBoth
	if selected == nil {
		selected = bestExecution
	}
	if selected == nil {
		selected = bestData
	}
	if selected == nil {
		return
	}

	for _, key := range baseFeeComponentMetadataKeys {
		delete(out, key)
	}
	executionSource := bestExecution
	dataSource := bestData
	if bestBoth != nil {
		executionSource = bestBoth
		dataSource = bestBoth
	}
	if executionSource != nil && executionSource.execution != nil {
		out["fee_execution_l2"] = executionSource.execution.String()
	}
	if dataSource != nil && dataSource.data != nil {
		out["fee_data_l1"] = dataSource.data.String()
	}

	pathSource := selected
	if resolveBaseMetadataEventPath(pathSource.metadata) == "" {
		bestPath := pickBestBaseFeeMetadataCandidate(candidates, func(candidate *baseFeeMetadataCandidate) bool {
			return candidate.pathHint
		})
		if bestPath != nil {
			pathSource = bestPath
		}
	}
	applyBasePathMetadata(out, pathSource.metadata)
}

type evmNativeTokenInfo struct {
	Symbol   string
	Name     string
	Decimals int
}

func evmNativeToken(chain model.Chain) evmNativeTokenInfo {
	switch chain {
	case model.ChainPolygon:
		return evmNativeTokenInfo{Symbol: "POL", Name: "POL", Decimals: 18}
	case model.ChainBSC:
		return evmNativeTokenInfo{Symbol: "BNB", Name: "BNB", Decimals: 18}
	default:
		return evmNativeTokenInfo{Symbol: "ETH", Name: "Ether", Decimals: 18}
	}
}

func isEVML1Chain(chain model.Chain) bool {
	switch chain {
	case model.ChainEthereum, model.ChainPolygon, model.ChainBSC:
		return true
	default:
		return false
	}
}

func evmDecoderVersion(chain model.Chain) string {
	if isEVML1Chain(chain) {
		return "evm-l1-decoder-v1"
	}
	return "base-decoder-v1"
}

func buildEthL1FeeBalanceEvent(feePayer, feeAmount, eventPath string) event.NormalizedBalanceEvent {
	return buildEVML1FeeBalanceEvent(feePayer, feeAmount, eventPath, evmNativeToken(model.ChainEthereum))
}

func buildEVML1FeeBalanceEvent(feePayer, feeAmount, eventPath string, token evmNativeTokenInfo) event.NormalizedBalanceEvent {
	return event.NormalizedBalanceEvent{
		OuterInstructionIndex: -1,
		InnerInstructionIndex: -1,
		EventCategory:         model.EventCategoryFee,
		EventAction:           "net_eth",
		ProgramID:             "0x0000000000000000000000000000000000000000000000000000000000000000",
		ContractAddress:       token.Symbol,
		Address:               feePayer,
		CounterpartyAddress:   "",
		Delta:                 canonicalFeeDelta(feeAmount),
		ChainData:             json.RawMessage("{}"),
		TokenSymbol:           token.Symbol,
		TokenName:             token.Name,
		TokenDecimals:         token.Decimals,
		TokenType:             model.TokenTypeNative,
		AssetID:               token.Symbol,
		EventPath:             eventPath,
	}
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
	if p := resolveBaseMetadataEventPath(metadata); p != "" {
		return p
	}
	return "log:0"
}

func resolveBaseMetadataEventPath(metadata map[string]string) string {
	if len(metadata) == 0 {
		return ""
	}
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
	return ""
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

func parseSignedBigInt(value string) (*big.Int, bool) {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return nil, false
	}
	parsed := new(big.Int)
	if _, ok := parsed.SetString(raw, 10); !ok {
		return nil, false
	}
	return parsed, true
}

func compareCanonicalDeltas(left, right string) int {
	leftValue, leftOK := parseSignedBigInt(left)
	rightValue, rightOK := parseSignedBigInt(right)
	if !leftOK || !rightOK {
		return strings.Compare(strings.TrimSpace(left), strings.TrimSpace(right))
	}
	return leftValue.Cmp(rightValue)
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
	decoderVersion := evmDecoderVersion(chain)

	for _, be := range normalizedEvents {
		if be.EventCategory == model.EventCategoryFeeExecutionL2 || be.EventCategory == model.EventCategoryFeeDataL1 {
			be.EventAction = string(be.EventCategory)
			be.Delta = canonicalFeeDelta(be.Delta)
		} else if be.EventCategory == model.EventCategoryFee {
			be.Delta = canonicalFeeDelta(be.Delta)
		}
		if metadataPath := resolveBaseMetadataEventPath(metadataFromChainData(be.ChainData)); metadataPath != "" {
			be.EventPath = metadataPath
		}
		be.EventPath = strings.TrimSpace(be.EventPath)
		if be.EventPath == "" {
			be.EventPath = balanceEventPath(int32(be.OuterInstructionIndex), int32(be.InnerInstructionIndex))
		}

		be.Address = canonicalizeAddressIdentity(chain, be.Address)
		be.CounterpartyAddress = canonicalizeAddressIdentity(chain, be.CounterpartyAddress)
		be.ContractAddress = canonicalizeAddressIdentity(chain, be.ContractAddress)
		be.AssetID = canonicalizeAddressIdentity(chain, be.AssetID)
		if be.AssetID == "" {
			be.AssetID = be.ContractAddress
		}

		assetType := mapTokenTypeToAssetType(be.TokenType)
		if isBaseFeeCategory(be.EventCategory) || be.EventCategory == model.EventCategoryFee {
			assetType = "fee"
		}

		be.ActorAddress = canonicalizeAddressIdentity(chain, be.Address)
		be.AssetType = assetType
		be.EventPathType = "base_log"
		be.FinalityState = finalityState
		be.DecoderVersion = decoderVersion
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

func canonicalizeBTCBalanceEvents(
	chain model.Chain,
	network model.Network,
	txHash string,
	finalityState string,
	normalizedEvents []event.NormalizedBalanceEvent,
) []event.NormalizedBalanceEvent {
	finalityState = normalizeFinalityStateOrDefault(chain, finalityState)
	eventsByID := make(map[string]event.NormalizedBalanceEvent, len(normalizedEvents))

	for _, be := range normalizedEvents {
		metadata := metadataFromChainData(be.ChainData)
		be.EventPath = resolveBTCEventPathFromMetadata(metadata, int32(be.OuterInstructionIndex))
		if be.EventPath == "" {
			be.EventPath = balanceEventPath(int32(be.OuterInstructionIndex), int32(be.InnerInstructionIndex))
		}

		be.Address = canonicalizeAddressIdentity(chain, be.Address)
		be.CounterpartyAddress = canonicalizeAddressIdentity(chain, be.CounterpartyAddress)
		be.ContractAddress = canonicalizeBTCAssetID(be.ContractAddress)
		be.AssetID = canonicalizeBTCAssetID(be.AssetID)
		if be.AssetID == "" {
			be.AssetID = be.ContractAddress
		}

		assetType := mapTokenTypeToAssetType(be.TokenType)
		if be.EventCategory == model.EventCategoryFee {
			assetType = "fee"
		}

		be.ActorAddress = canonicalizeAddressIdentity(chain, be.Address)
		be.AssetType = assetType
		if be.EventCategory == model.EventCategoryFee {
			be.EventPathType = "btc_fee"
		} else {
			be.EventPathType = "btc_utxo"
		}
		be.FinalityState = finalityState
		be.DecoderVersion = "btc-decoder-v1"
		be.SchemaVersion = "v2"
		be.EventID = buildCanonicalEventID(
			chain, network,
			txHash, be.EventPath,
			be.ActorAddress, be.AssetID, be.EventCategory,
		)

		existing, ok := eventsByID[be.EventID]
		if !ok || shouldReplaceCanonicalBTCEvent(existing, be) {
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

func resolveBTCEventPathFromMetadata(metadata map[string]string, outerInstructionIndex int32) string {
	if metadata == nil {
		return ""
	}
	eventPath := strings.TrimSpace(metadata["event_path"])
	if eventPath != "" {
		return eventPath
	}
	utxoPathType := strings.ToLower(strings.TrimSpace(metadata["utxo_path_type"]))
	if (utxoPathType == "vin" || utxoPathType == "vout") && outerInstructionIndex >= 0 {
		return fmt.Sprintf("%s:%d", utxoPathType, outerInstructionIndex)
	}
	return ""
}

func shouldReplaceCanonicalBTCEvent(existing, incoming event.NormalizedBalanceEvent) bool {
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

func canonicalizeBTCAssetID(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "BTC"
	}
	if strings.EqualFold(trimmed, "btc") {
		return "BTC"
	}
	return trimmed
}

func shouldEmitBTCFeeEvent(txStatus, feePayer, feeAmount string, rawEvents []event.NormalizedBalanceEvent) bool {
	if !shouldEmitSolanaFeeEvent(txStatus, feePayer, feeAmount) {
		return false
	}
	return !hasBTCFeeRepresentedAsChangeOutput(rawEvents, feePayer, feeAmount)
}

func hasBTCFeeRepresentedAsChangeOutput(events []event.NormalizedBalanceEvent, feePayer, feeAmount string) bool {
	feePayer = canonicalizeAddressIdentity(model.ChainBTC, feePayer)
	if feePayer == "" {
		return false
	}

	fee, ok := parseBigInt(feeAmount)
	if !ok || fee.Sign() == 0 {
		return false
	}

	hasInputSpend := false
	hasRecipient := false
	hasEqualFeeChangeOutput := false

	for _, be := range events {
		if be.EventCategory != model.EventCategoryTransfer {
			continue
		}
		action := strings.TrimSpace(be.EventAction)
		if action == "vin_spend" {
			hasInputSpend = true
			continue
		}
		if action != "vout_receive" {
			continue
		}

		beDelta, ok := parseBigInt(be.Delta)
		if !ok || beDelta.Sign() == 0 {
			continue
		}

		address := canonicalizeAddressIdentity(model.ChainBTC, be.Address)
		if address == feePayer {
			if beDelta.Cmp(fee) == 0 {
				hasEqualFeeChangeOutput = true
			}
			continue
		}

		hasRecipient = true
	}

	return hasInputSpend && hasRecipient && hasEqualFeeChangeOutput
}

func hasBTCFeeEvent(events []event.NormalizedBalanceEvent, feePayer string) bool {
	feePayer = strings.TrimSpace(feePayer)
	if feePayer == "" {
		return false
	}
	for _, be := range events {
		if be.EventCategory != model.EventCategoryFee {
			continue
		}
		if strings.TrimSpace(be.Address) == feePayer {
			return true
		}
	}
	return false
}

func buildBTCFeeBalanceEvent(feePayer, feeAmount string) event.NormalizedBalanceEvent {
	chainData, _ := json.Marshal(map[string]string{"event_path": "fee:miner"})
	return event.NormalizedBalanceEvent{
		OuterInstructionIndex: -1,
		InnerInstructionIndex: -1,
		EventCategory:         model.EventCategoryFee,
		EventAction:           "miner_fee",
		ProgramID:             "btc",
		ContractAddress:       "BTC",
		Address:               feePayer,
		CounterpartyAddress:   "",
		Delta:                 canonicalFeeDelta(feeAmount),
		ChainData:             chainData,
		TokenSymbol:           "BTC",
		TokenName:             "Bitcoin",
		TokenDecimals:         8,
		TokenType:             model.TokenTypeNative,
		AssetID:               "BTC",
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

	for idx := range normalizedEvents {
		normalizedEvents[idx].Address = canonicalizeAddressIdentity(chain, normalizedEvents[idx].Address)
		normalizedEvents[idx].CounterpartyAddress = canonicalizeAddressIdentity(chain, normalizedEvents[idx].CounterpartyAddress)
		normalizedEvents[idx].ContractAddress = canonicalizeAddressIdentity(chain, normalizedEvents[idx].ContractAddress)
		normalizedEvents[idx].AssetID = canonicalizeAddressIdentity(chain, normalizedEvents[idx].AssetID)
		if normalizedEvents[idx].AssetID == "" {
			normalizedEvents[idx].AssetID = normalizedEvents[idx].ContractAddress
		}
		if normalizedEvents[idx].EventCategory == model.EventCategoryFee {
			continue
		}
		if outer, inner, ok := solanaInstructionPathFromChainData(normalizedEvents[idx].ChainData); ok {
			normalizedEvents[idx].OuterInstructionIndex = outer
			normalizedEvents[idx].InnerInstructionIndex = inner
		}
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
		return compareCanonicalDeltas(existing.Delta, incoming.Delta) > 0
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
	return compareCanonicalDeltas(existing.Delta, incoming.Delta) > 0
}

func buildCanonicalEventID(chain model.Chain, network model.Network, txHash, eventPath, actorAddress, assetID string, category model.EventCategory) string {
	canonicalTxHash := strings.TrimSpace(txHash)
	canonicalTxHash = canonicalSignatureIdentity(chain, canonicalTxHash)
	if canonicalTxHash == "" {
		canonicalTxHash = strings.TrimSpace(txHash)
	}
	canonical := fmt.Sprintf("chain=%s|network=%s|tx=%s|path=%s|actor=%s|asset=%s|category=%s", chain, network, canonicalTxHash, eventPath, actorAddress, assetID, category)
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
