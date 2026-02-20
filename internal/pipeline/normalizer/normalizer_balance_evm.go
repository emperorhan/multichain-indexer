package normalizer

import (
	"encoding/json"
	"math/big"
	"sort"
	"strings"

	sidecarv1 "github.com/emperorhan/multichain-indexer/pkg/generated/sidecar/v1"

	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
)

func buildCanonicalBaseBalanceEvents(
	chain model.Chain,
	network model.Network,
	txHash, txStatus, feePayer, feeAmount, finalityState string,
	rawEvents []*sidecarv1.BalanceEventInfo,
	watchedAddress string,
) []event.NormalizedBalanceEvent {
	return buildCanonicalBaseBalanceEventsMulti(chain, network, txHash, txStatus, feePayer, feeAmount, finalityState, rawEvents, map[string]struct{}{watchedAddress: {}})
}

func buildCanonicalBaseBalanceEventsMulti(
	chain model.Chain,
	network model.Network,
	txHash, txStatus, feePayer, feeAmount, finalityState string,
	rawEvents []*sidecarv1.BalanceEventInfo,
	watchedAddresses map[string]struct{},
) []event.NormalizedBalanceEvent {
	finalityState = normalizeFinalityStateOrDefault(chain, finalityState)
	normalizedEvents := buildRawBalanceEventsMulti(rawEvents, watchedAddresses, true)
	meta := collectBaseMetadata(rawEvents)
	missingDataFee := !hasBaseDataFee(meta)
	eventPath := resolveBaseFeeEventPath(meta)

	_, feePayerIsWatched := watchedAddresses[feePayer]
	if feePayerIsWatched && shouldEmitBaseFeeEvent(txStatus, feePayer, feeAmount) {
		nativeToken := evmNativeToken(chain)
		if isEVML1Chain(chain) {
			normalizedEvents = append(normalizedEvents, buildEVML1FeeBalanceEvent(feePayer, feeAmount, eventPath, nativeToken))
		} else {
			executionFee := deriveBaseExecutionFee(feeAmount, meta)
			dataFee, hasDataFee := deriveBaseDataFee(meta)

			executionEvent := buildBaseFeeBalanceEvent(
				model.ActivityFeeExecutionL2,
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
						model.ActivityFeeDataL1,
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

// --- Base metadata collection ---

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

// --- EVM native token helpers ---

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

// --- EVM fee event builders ---

func buildEthL1FeeBalanceEvent(feePayer, feeAmount, eventPath string) event.NormalizedBalanceEvent {
	return buildEVML1FeeBalanceEvent(feePayer, feeAmount, eventPath, evmNativeToken(model.ChainEthereum))
}

func buildEVML1FeeBalanceEvent(feePayer, feeAmount, eventPath string, token evmNativeTokenInfo) event.NormalizedBalanceEvent {
	return event.NormalizedBalanceEvent{
		OuterInstructionIndex: -1,
		InnerInstructionIndex: -1,
		ActivityType:          model.ActivityFee,
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
	activityType model.ActivityType,
	feePayer, feeAmount string,
	eventPath, eventAction string,
) event.NormalizedBalanceEvent {
	return event.NormalizedBalanceEvent{
		OuterInstructionIndex: -1,
		InnerInstructionIndex: -1,
		ActivityType:          activityType,
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

// --- EVM fee derivation ---

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

// --- Base canonicalization ---

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
		if be.ActivityType == model.ActivityFeeExecutionL2 || be.ActivityType == model.ActivityFeeDataL1 {
			be.EventAction = string(be.ActivityType)
			be.Delta = canonicalFeeDelta(be.Delta)
		} else if be.ActivityType == model.ActivityFee {
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
		if isBaseFeeActivity(be.ActivityType) || be.ActivityType == model.ActivityFee {
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
			be.ActorAddress, be.AssetID, be.ActivityType,
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

func isBaseFeeActivity(activityType model.ActivityType) bool {
	switch activityType {
	case model.ActivityFeeExecutionL2, model.ActivityFeeDataL1:
		return true
	default:
		return false
	}
}
