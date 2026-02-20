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
	"github.com/emperorhan/multichain-indexer/internal/pipeline/identity"
)

// buildRawBalanceEvents converts sidecar raw events into NormalizedBalanceEvents,
// filtering by watchedAddress. When handleSelfTransfer is true, transfer events
// where counterparty == watchedAddress are emitted as delta=0 SELF_TRANSFER events.
func buildRawBalanceEvents(
	rawEvents []*sidecarv1.BalanceEventInfo,
	watchedAddress string,
	handleSelfTransfer bool,
) []event.NormalizedBalanceEvent {
	return buildRawBalanceEventsMulti(rawEvents, map[string]struct{}{watchedAddress: {}}, handleSelfTransfer)
}

// buildRawBalanceEventsMulti converts sidecar raw events into NormalizedBalanceEvents,
// filtering by a set of watched addresses.
func buildRawBalanceEventsMulti(
	rawEvents []*sidecarv1.BalanceEventInfo,
	watchedAddresses map[string]struct{},
	handleSelfTransfer bool,
) []event.NormalizedBalanceEvent {
	normalizedEvents := make([]event.NormalizedBalanceEvent, 0, len(rawEvents)+2)

	for _, be := range rawEvents {
		if be == nil {
			continue
		}
		if _, ok := watchedAddresses[be.Address]; !ok {
			continue
		}
		category := model.EventCategory(be.EventCategory)

		_, counterpartyIsWatched := watchedAddresses[be.CounterpartyAddress]
		if handleSelfTransfer && category == model.EventCategoryTransfer && be.CounterpartyAddress == be.Address {
			chainData, _ := json.Marshal(be.Metadata)
			normalizedEvents = append(normalizedEvents, event.NormalizedBalanceEvent{
				OuterInstructionIndex: int(be.OuterInstructionIndex),
				InnerInstructionIndex: int(be.InnerInstructionIndex),
				ActivityType:          model.ActivitySelfTransfer,
				EventAction:           "self_transfer",
				ProgramID:             be.ProgramId,
				ContractAddress:       be.ContractAddress,
				Address:               be.Address,
				CounterpartyAddress:   be.CounterpartyAddress,
				Delta:                 "0",
				ChainData:             chainData,
				TokenSymbol:           be.TokenSymbol,
				TokenName:             be.TokenName,
				TokenDecimals:         int(be.TokenDecimals),
				TokenType:             model.TokenType(be.TokenType),
				AssetID:               be.ContractAddress,
			})
			continue
		}
		_ = counterpartyIsWatched

		activityType := model.ClassifyActivity(category, be.Delta, true)
		chainData, _ := json.Marshal(be.Metadata)

		normalizedEvents = append(normalizedEvents, event.NormalizedBalanceEvent{
			OuterInstructionIndex: int(be.OuterInstructionIndex),
			InnerInstructionIndex: int(be.InnerInstructionIndex),
			ActivityType:          activityType,
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

	return normalizedEvents
}

// --- Solana ---

func buildCanonicalSolanaBalanceEvents(
	chain model.Chain,
	network model.Network,
	txHash, txStatus, feePayer, feeAmount, finalityState string,
	rawEvents []*sidecarv1.BalanceEventInfo,
	watchedAddress string,
) []event.NormalizedBalanceEvent {
	return buildCanonicalSolanaBalanceEventsMulti(chain, network, txHash, txStatus, feePayer, feeAmount, finalityState, rawEvents, map[string]struct{}{watchedAddress: {}})
}

func buildCanonicalSolanaBalanceEventsMulti(
	chain model.Chain,
	network model.Network,
	txHash, txStatus, feePayer, feeAmount, finalityState string,
	rawEvents []*sidecarv1.BalanceEventInfo,
	watchedAddresses map[string]struct{},
) []event.NormalizedBalanceEvent {
	finalityState = normalizeFinalityStateOrDefault(chain, finalityState)
	normalizedEvents := buildRawBalanceEventsMulti(rawEvents, watchedAddresses, true)

	// Fee events: only emit if feePayer is one of the watched addresses
	if _, ok := watchedAddresses[feePayer]; ok {
		if shouldEmitSolanaFeeEvent(txStatus, feePayer, feeAmount) && !hasSolanaFeeEvent(normalizedEvents, feePayer) {
			normalizedEvents = append(normalizedEvents, buildSolanaFeeBalanceEvent(feePayer, feeAmount))
		}
	}

	return canonicalizeSolanaBalanceEvents(chain, network, txHash, finalityState, normalizedEvents)
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
		if be.ActivityType != model.ActivityFee {
			continue
		}
		if strings.TrimSpace(be.Address) == feePayer {
			return true
		}
	}
	return false
}

func buildSolanaFeeBalanceEvent(feePayer, feeAmount string) event.NormalizedBalanceEvent {
	return event.NormalizedBalanceEvent{
		OuterInstructionIndex: -1,
		InnerInstructionIndex: -1,
		ActivityType:          model.ActivityFee,
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
		ActivityType     model.ActivityType
	}

	for idx := range normalizedEvents {
		normalizedEvents[idx].Address = canonicalizeAddressIdentity(chain, normalizedEvents[idx].Address)
		normalizedEvents[idx].CounterpartyAddress = canonicalizeAddressIdentity(chain, normalizedEvents[idx].CounterpartyAddress)
		normalizedEvents[idx].ContractAddress = canonicalizeAddressIdentity(chain, normalizedEvents[idx].ContractAddress)
		normalizedEvents[idx].AssetID = canonicalizeAddressIdentity(chain, normalizedEvents[idx].AssetID)
		if normalizedEvents[idx].AssetID == "" {
			normalizedEvents[idx].AssetID = normalizedEvents[idx].ContractAddress
		}
		if normalizedEvents[idx].ActivityType == model.ActivityFee {
			continue
		}
		if outer, inner, ok := solanaInstructionPathFromChainData(normalizedEvents[idx].ChainData); ok {
			normalizedEvents[idx].OuterInstructionIndex = outer
			normalizedEvents[idx].InnerInstructionIndex = inner
		}
	}

	outerHasOwner := make(map[solanaInstructionOwnerKey]struct{})
	for _, be := range normalizedEvents {
		if be.ActivityType != model.ActivityFee && be.OuterInstructionIndex >= 0 && be.InnerInstructionIndex == -1 {
			outerHasOwner[solanaInstructionOwnerKey{
				OuterInstruction: be.OuterInstructionIndex,
				Address:          be.Address,
				AssetID:          be.AssetID,
				ActivityType:     be.ActivityType,
			}] = struct{}{}
		}
	}

	sort.SliceStable(normalizedEvents, func(i, j int) bool {
		if normalizedEvents[i].ActivityType != normalizedEvents[j].ActivityType {
			return normalizedEvents[i].ActivityType < normalizedEvents[j].ActivityType
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
		if be.ActivityType == model.ActivityFee {
			be.OuterInstructionIndex = -1
			be.InnerInstructionIndex = -1
			be.Delta = canonicalFeeDelta(be.Delta)
			be.EventAction = "transaction_fee"
		}
		selection := solanaCanonicalSelection{
			FromOuterInstruction: be.InnerInstructionIndex == -1,
			OriginalInnerIndex:   int32(be.InnerInstructionIndex),
		}
		if be.ActivityType != model.ActivityFee && be.OuterInstructionIndex >= 0 && be.InnerInstructionIndex > -1 {
			key := solanaInstructionOwnerKey{
				OuterInstruction: be.OuterInstructionIndex,
				Address:          be.Address,
				AssetID:          be.AssetID,
				ActivityType:     be.ActivityType,
			}
			if _, ok := outerHasOwner[key]; ok {
				be.InnerInstructionIndex = -1
				selection.FromOuterInstruction = false
			}
		}

		assetType := mapTokenTypeToAssetType(be.TokenType)
		if be.ActivityType == model.ActivityFee {
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
			be.ActorAddress, be.AssetID, be.ActivityType,
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
	if existing.ActivityType == model.ActivityFee || incoming.ActivityType == model.ActivityFee {
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

// --- Shared helpers ---

func buildCanonicalEventID(chain model.Chain, network model.Network, txHash, eventPath, actorAddress, assetID string, activityType model.ActivityType) string {
	canonicalTxHash := strings.TrimSpace(txHash)
	canonicalTxHash = identity.CanonicalSignatureIdentity(chain, canonicalTxHash)
	if canonicalTxHash == "" {
		canonicalTxHash = strings.TrimSpace(txHash)
	}
	canonical := fmt.Sprintf("chain=%s|network=%s|tx=%s|path=%s|actor=%s|asset=%s|activity=%s", chain, network, canonicalTxHash, eventPath, actorAddress, assetID, activityType)
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
