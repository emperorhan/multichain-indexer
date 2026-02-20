package autotune

import (
	"strconv"
	"strings"
)

// orderingFieldCount is the total number of fields in the ownership ordering:
// 12 prefix fields + 22 epoch marker fields = 34.
const orderingFieldCount = 12 + numEpochMarkers

// rollbackFenceOwnershipOrdering holds 34 int64 fields as a fixed-size array
// for lexicographic comparison. Slot layout:
//
//	[0]  epoch
//	[1]  bridgeSequence
//	[2]  drainWatermark
//	[3]  liveHead
//	[4]  steadyStateWatermark
//	[5]  steadyGeneration
//	[6]  generationFloor
//	[7]  floorLiftEpoch
//	[8]  settleWindowEpoch
//	[9]  spilloverEpoch
//	[10] spilloverRejoinEpoch
//	[11] rejoinSealEpoch
//	[12..33] epochMarkers[0..21]
type rollbackFenceOwnershipOrdering struct {
	fields [orderingFieldCount]int64
}

// Prefix field indices.
const (
	slotEpoch                = 0
	slotBridgeSequence       = 1
	slotDrainWatermark       = 2
	slotLiveHead             = 3
	slotSteadyStateWatermark = 4
	slotSteadyGeneration     = 5
	slotGenerationFloor      = 6
	slotFloorLiftEpoch       = 7
	slotSettleWindowEpoch    = 8
	slotSpilloverEpoch       = 9
	slotSpilloverRejoinEpoch = 10
	slotRejoinSealEpoch      = 11
	slotMarkerBase           = 12
)

type rollbackFenceOwnershipPrefixFields struct {
	releaseEpoch            int64
	hasReleaseEpoch         bool
	bridgeSequence          int64
	hasBridgeSequence       bool
	releaseWatermark        int64
	hasReleaseWatermark     bool
	drainWatermark          int64
	hasDrainWatermark       bool
	liveHead                int64
	hasLiveHead             bool
	steadyStateWatermark    int64
	hasSteadyStateWatermark bool
	steadyGeneration        int64
	hasSteadyGeneration     bool
	generationFloor         int64
	hasGenerationFloor      bool
	floorLiftEpoch          int64
	hasFloorLiftEpoch       bool
	settleWindowEpoch       int64
	hasSettleWindowEpoch    bool
	spilloverEpoch          int64
	hasSpilloverEpoch       bool
	spilloverRejoinEpoch    int64
	hasSpilloverRejoinEpoch bool
	rejoinSealEpoch         int64
	hasRejoinSealEpoch      bool
}

func setOwnershipPrefixField(value string, target *int64, seen *bool) bool {
	if *seen {
		return true
	}
	if value == "" {
		return false
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		return false
	}
	*target = parsed
	*seen = true
	return true
}

func parseRollbackFenceOwnershipPrefixFields(digest string) (rollbackFenceOwnershipPrefixFields, bool) {
	var fields rollbackFenceOwnershipPrefixFields

	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		key, value, hasValue := strings.Cut(token, "=")
		if !hasValue {
			continue
		}
		value = strings.TrimSpace(value)
		switch key {
		case "rollback-fence-late-marker-release-epoch":
			if !setOwnershipPrefixField(value, &fields.releaseEpoch, &fields.hasReleaseEpoch) {
				return rollbackFenceOwnershipPrefixFields{}, false
			}
		case "rollback-fence-late-bridge-sequence", "rollback-fence-late-bridge-seq":
			if !setOwnershipPrefixField(value, &fields.bridgeSequence, &fields.hasBridgeSequence) {
				return rollbackFenceOwnershipPrefixFields{}, false
			}
		case "rollback-fence-late-bridge-release-watermark", "rollback-fence-release-watermark":
			if !setOwnershipPrefixField(value, &fields.releaseWatermark, &fields.hasReleaseWatermark) {
				return rollbackFenceOwnershipPrefixFields{}, false
			}
		case "rollback-fence-late-bridge-drain-watermark", "rollback-fence-backlog-drain-watermark", "rollback-fence-drain-watermark":
			if !setOwnershipPrefixField(value, &fields.drainWatermark, &fields.hasDrainWatermark) {
				return rollbackFenceOwnershipPrefixFields{}, false
			}
		case "rollback-fence-live-head", "rollback-fence-live-catchup-head", "rollback-fence-live-watermark":
			if !setOwnershipPrefixField(value, &fields.liveHead, &fields.hasLiveHead) {
				return rollbackFenceOwnershipPrefixFields{}, false
			}
		case "rollback-fence-steady-state-watermark", "rollback-fence-steady-watermark", "rollback-fence-rebaseline-watermark":
			if !setOwnershipPrefixField(value, &fields.steadyStateWatermark, &fields.hasSteadyStateWatermark) {
				return rollbackFenceOwnershipPrefixFields{}, false
			}
		case "rollback-fence-steady-generation", "rollback-fence-baseline-generation":
			if !setOwnershipPrefixField(value, &fields.steadyGeneration, &fields.hasSteadyGeneration) {
				return rollbackFenceOwnershipPrefixFields{}, false
			}
		case "rollback-fence-generation-retention-floor", "rollback-fence-retention-floor":
			if !setOwnershipPrefixField(value, &fields.generationFloor, &fields.hasGenerationFloor) {
				return rollbackFenceOwnershipPrefixFields{}, false
			}
		case "rollback-fence-floor-lift-epoch", "rollback-fence-retention-floor-lift-epoch":
			if !setOwnershipPrefixField(value, &fields.floorLiftEpoch, &fields.hasFloorLiftEpoch) {
				return rollbackFenceOwnershipPrefixFields{}, false
			}
		case "rollback-fence-settle-window-epoch", "rollback-fence-floor-lift-settle-window-epoch":
			if !setOwnershipPrefixField(value, &fields.settleWindowEpoch, &fields.hasSettleWindowEpoch) {
				return rollbackFenceOwnershipPrefixFields{}, false
			}
		case "rollback-fence-spillover-epoch", "rollback-fence-late-spillover-epoch", "rollback-fence-settle-window-spillover-epoch":
			if !setOwnershipPrefixField(value, &fields.spilloverEpoch, &fields.hasSpilloverEpoch) {
				return rollbackFenceOwnershipPrefixFields{}, false
			}
		case "rollback-fence-spillover-rejoin-epoch", "rollback-fence-rejoin-window-epoch", "rollback-fence-late-spillover-rejoin-epoch":
			if !setOwnershipPrefixField(value, &fields.spilloverRejoinEpoch, &fields.hasSpilloverRejoinEpoch) {
				return rollbackFenceOwnershipPrefixFields{}, false
			}
		case "rollback-fence-rejoin-seal-epoch", "rollback-fence-steady-seal-epoch", "rollback-fence-post-rejoin-seal-epoch":
			if !setOwnershipPrefixField(value, &fields.rejoinSealEpoch, &fields.hasRejoinSealEpoch) {
				return rollbackFenceOwnershipPrefixFields{}, false
			}
		}
	}

	return fields, true
}

// prefixHasFlags returns the "has" flags for the 12 prefix fields in dependency
// order, plus the epoch marker "has" flags at indices [12..33].
type parsedEpochs struct {
	values [numEpochMarkers]int64
	has    [numEpochMarkers]bool
}

func parseAllEpochMarkers(digest string) parsedEpochs {
	var p parsedEpochs
	for i := range epochMarkers {
		p.values[i], p.has[i] = parseEpochMarker(digest, epochMarkers[i])
	}
	return p
}

func parseRollbackFenceOwnershipOrdering(epoch int64, digest string) (rollbackFenceOwnershipOrdering, bool) {
	if epoch < 0 {
		return rollbackFenceOwnershipOrdering{}, false
	}
	normalized := normalizePolicyManifestDigest(digest)
	if !isRollbackFencePostExpiryLateMarkerReleaseDigest(epoch, normalized) {
		return rollbackFenceOwnershipOrdering{}, false
	}
	prefixFields, ok := parseRollbackFenceOwnershipPrefixFields(normalized)
	if !ok || !prefixFields.hasReleaseEpoch {
		return rollbackFenceOwnershipOrdering{}, false
	}

	// Parse all 22 epoch markers.
	em := parseAllEpochMarkers(normalized)

	// Prefix field dependency chain: each "has" flag requires its predecessor.
	prefixHas := [12]bool{
		true, // releaseEpoch (always required; checked above)
		prefixFields.hasBridgeSequence,
		prefixFields.hasReleaseWatermark,
		prefixFields.hasDrainWatermark,
		prefixFields.hasLiveHead,
		prefixFields.hasSteadyStateWatermark,
		prefixFields.hasSteadyGeneration,
		prefixFields.hasGenerationFloor,
		prefixFields.hasFloorLiftEpoch,
		prefixFields.hasSettleWindowEpoch,
		prefixFields.hasSpilloverEpoch,
		prefixFields.hasSpilloverRejoinEpoch,
	}

	// Special prefix dependency rules.
	if prefixFields.hasBridgeSequence != prefixFields.hasReleaseWatermark {
		return rollbackFenceOwnershipOrdering{}, false
	}
	if prefixFields.hasDrainWatermark && !prefixFields.hasBridgeSequence {
		return rollbackFenceOwnershipOrdering{}, false
	}
	if prefixFields.hasLiveHead && !prefixFields.hasBridgeSequence {
		return rollbackFenceOwnershipOrdering{}, false
	}
	if prefixFields.hasLiveHead && !prefixFields.hasDrainWatermark {
		return rollbackFenceOwnershipOrdering{}, false
	}
	if prefixFields.hasSteadyStateWatermark && !prefixFields.hasLiveHead {
		return rollbackFenceOwnershipOrdering{}, false
	}
	if prefixFields.hasSteadyGeneration && !prefixFields.hasSteadyStateWatermark {
		return rollbackFenceOwnershipOrdering{}, false
	}
	if prefixFields.hasGenerationFloor && !prefixFields.hasSteadyGeneration {
		return rollbackFenceOwnershipOrdering{}, false
	}
	if prefixFields.hasFloorLiftEpoch && !prefixFields.hasGenerationFloor {
		return rollbackFenceOwnershipOrdering{}, false
	}
	if prefixFields.hasSettleWindowEpoch && !prefixFields.hasFloorLiftEpoch {
		return rollbackFenceOwnershipOrdering{}, false
	}
	if prefixFields.hasSpilloverEpoch && !prefixFields.hasSettleWindowEpoch {
		return rollbackFenceOwnershipOrdering{}, false
	}
	if prefixFields.hasSpilloverRejoinEpoch && !prefixFields.hasSpilloverEpoch {
		return rollbackFenceOwnershipOrdering{}, false
	}
	if prefixFields.hasRejoinSealEpoch && !prefixFields.hasSpilloverRejoinEpoch {
		return rollbackFenceOwnershipOrdering{}, false
	}

	// Epoch marker dependency chain: each marker requires the previous one.
	if em.has[0] && !prefixFields.hasRejoinSealEpoch {
		return rollbackFenceOwnershipOrdering{}, false
	}
	for i := 1; i < numEpochMarkers; i++ {
		if em.has[i] && !em.has[i-1] {
			return rollbackFenceOwnershipOrdering{}, false
		}
	}

	// Assign prefix field values with defaults.
	bridgeSequence := prefixFields.bridgeSequence
	releaseWatermark := prefixFields.releaseWatermark
	if !prefixFields.hasBridgeSequence {
		bridgeSequence = 0
		releaseWatermark = prefixFields.releaseEpoch
	}
	if releaseWatermark < prefixFields.releaseEpoch {
		return rollbackFenceOwnershipOrdering{}, false
	}

	drainWatermark := prefixFields.drainWatermark
	if !prefixFields.hasDrainWatermark {
		drainWatermark = releaseWatermark
	}
	if drainWatermark < releaseWatermark {
		return rollbackFenceOwnershipOrdering{}, false
	}

	liveHead := prefixFields.liveHead
	if !prefixFields.hasLiveHead {
		liveHead = drainWatermark
	}
	if liveHead < drainWatermark {
		return rollbackFenceOwnershipOrdering{}, false
	}

	steadyStateWatermark := prefixFields.steadyStateWatermark
	if !prefixFields.hasSteadyStateWatermark {
		steadyStateWatermark = liveHead
	}
	if steadyStateWatermark < liveHead {
		return rollbackFenceOwnershipOrdering{}, false
	}

	_ = prefixHas // used above in dependency checks

	// Zero out unset prefix fields.
	steadyGeneration := prefixFields.steadyGeneration
	if !prefixFields.hasSteadyGeneration {
		steadyGeneration = 0
	}
	generationFloor := prefixFields.generationFloor
	if !prefixFields.hasGenerationFloor {
		generationFloor = 0
	}
	floorLiftEpoch := prefixFields.floorLiftEpoch
	if !prefixFields.hasFloorLiftEpoch {
		floorLiftEpoch = 0
	}
	settleWindowEpoch := prefixFields.settleWindowEpoch
	if !prefixFields.hasSettleWindowEpoch {
		settleWindowEpoch = 0
	}
	spilloverEpoch := prefixFields.spilloverEpoch
	if !prefixFields.hasSpilloverEpoch {
		spilloverEpoch = 0
	}
	spilloverRejoinEpoch := prefixFields.spilloverRejoinEpoch
	if !prefixFields.hasSpilloverRejoinEpoch {
		spilloverRejoinEpoch = 0
	}
	rejoinSealEpoch := prefixFields.rejoinSealEpoch
	if !prefixFields.hasRejoinSealEpoch {
		rejoinSealEpoch = 0
	}

	// Zero out unset epoch markers.
	var markerValues [numEpochMarkers]int64
	for i := range epochMarkers {
		if em.has[i] {
			markerValues[i] = em.values[i]
		}
	}

	// Strict monotonicity: each resurrection-cycle epoch must advance beyond
	// the previous one.
	// Markers 4+ (resurrection-quarantine onward) form strictly increasing pairs.
	if em.has[4] && markerValues[4] <= markerValues[3] {
		return rollbackFenceOwnershipOrdering{}, false
	}
	for i := 5; i < numEpochMarkers; i++ {
		if em.has[i] && markerValues[i] <= markerValues[i-1] {
			return rollbackFenceOwnershipOrdering{}, false
		}
	}

	if generationFloor > steadyGeneration {
		return rollbackFenceOwnershipOrdering{}, false
	}

	var ordering rollbackFenceOwnershipOrdering
	ordering.fields[slotEpoch] = epoch
	ordering.fields[slotBridgeSequence] = bridgeSequence
	ordering.fields[slotDrainWatermark] = drainWatermark
	ordering.fields[slotLiveHead] = liveHead
	ordering.fields[slotSteadyStateWatermark] = steadyStateWatermark
	ordering.fields[slotSteadyGeneration] = steadyGeneration
	ordering.fields[slotGenerationFloor] = generationFloor
	ordering.fields[slotFloorLiftEpoch] = floorLiftEpoch
	ordering.fields[slotSettleWindowEpoch] = settleWindowEpoch
	ordering.fields[slotSpilloverEpoch] = spilloverEpoch
	ordering.fields[slotSpilloverRejoinEpoch] = spilloverRejoinEpoch
	ordering.fields[slotRejoinSealEpoch] = rejoinSealEpoch
	for i := range epochMarkers {
		ordering.fields[slotMarkerBase+i] = markerValues[i]
	}
	return ordering, true
}

func compareRollbackFenceOwnershipOrdering(
	left rollbackFenceOwnershipOrdering,
	right rollbackFenceOwnershipOrdering,
) int {
	for i := 0; i < orderingFieldCount; i++ {
		if left.fields[i] < right.fields[i] {
			return -1
		}
		if left.fields[i] > right.fields[i] {
			return 1
		}
	}
	return 0
}
