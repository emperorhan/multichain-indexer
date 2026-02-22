package normalizer

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	sidecarv1 "github.com/emperorhan/multichain-indexer/pkg/generated/sidecar/v1"

	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
)

// btcFeeChainData is a pre-computed JSON payload for BTC fee events,
// avoiding json.Marshal allocation on every fee event.
var btcFeeChainData = json.RawMessage(`{"event_path":"fee:miner"}`)

func buildCanonicalBTCBalanceEvents(
	chain model.Chain,
	network model.Network,
	txHash, txStatus, feePayer, feeAmount, finalityState string,
	rawEvents []*sidecarv1.BalanceEventInfo,
	watchedAddress string,
) []event.NormalizedBalanceEvent {
	return buildCanonicalBTCBalanceEventsMulti(chain, network, txHash, txStatus, feePayer, feeAmount, finalityState, rawEvents, map[string]struct{}{watchedAddress: {}})
}

func buildCanonicalBTCBalanceEventsMulti(
	chain model.Chain,
	network model.Network,
	txHash, txStatus, feePayer, feeAmount, finalityState string,
	rawEvents []*sidecarv1.BalanceEventInfo,
	watchedAddresses map[string]struct{},
) []event.NormalizedBalanceEvent {
	finalityState = normalizeFinalityStateOrDefault(chain, finalityState)
	normalizedEvents := buildRawBalanceEventsMulti(rawEvents, watchedAddresses, false)

	// Fee events: only emit if feePayer is one of the watched addresses
	if _, ok := watchedAddresses[feePayer]; ok {
		if shouldEmitBTCFeeEvent(txStatus, feePayer, feeAmount, normalizedEvents) && !hasBTCFeeEvent(normalizedEvents, feePayer) {
			normalizedEvents = append(normalizedEvents, buildBTCFeeBalanceEvent(feePayer, feeAmount))
		}
	}

	return canonicalizeBTCBalanceEvents(chain, network, txHash, finalityState, normalizedEvents)
}

// --- BTC canonicalization ---

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
		if be.ActivityType == model.ActivityFee {
			assetType = "fee"
		}

		be.ActorAddress = canonicalizeAddressIdentity(chain, be.Address)
		be.AssetType = assetType
		if be.ActivityType == model.ActivityFee {
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
			be.ActorAddress, be.AssetID, be.ActivityType,
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

// --- BTC event path resolution ---

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
		return utxoPathType + ":" + strconv.FormatInt(int64(outerInstructionIndex), 10)
	}
	return ""
}

// --- BTC dedup / replacement ---

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

// --- BTC fee helpers ---

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
		if be.ActivityType != model.ActivityDeposit && be.ActivityType != model.ActivityWithdrawal {
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
		if be.ActivityType != model.ActivityFee {
			continue
		}
		if strings.TrimSpace(be.Address) == feePayer {
			return true
		}
	}
	return false
}

func buildBTCFeeBalanceEvent(feePayer, feeAmount string) event.NormalizedBalanceEvent {
	return event.NormalizedBalanceEvent{
		OuterInstructionIndex: -1,
		InnerInstructionIndex: -1,
		ActivityType:          model.ActivityFee,
		EventAction:           "miner_fee",
		ProgramID:             "btc",
		ContractAddress:       "BTC",
		Address:               feePayer,
		CounterpartyAddress:   "",
		Delta:                 canonicalFeeDelta(feeAmount),
		ChainData:             btcFeeChainData,
		TokenSymbol:           "BTC",
		TokenName:             "Bitcoin",
		TokenDecimals:         8,
		TokenType:             model.TokenTypeNative,
		AssetID:               "BTC",
	}
}
