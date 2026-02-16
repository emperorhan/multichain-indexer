package autotune

import (
	"strconv"
	"strings"
)

type rollbackFenceOwnershipOrdering struct {
	epoch                                                                                                                              int64
	bridgeSequence                                                                                                                     int64
	drainWatermark                                                                                                                     int64
	liveHead                                                                                                                           int64
	steadyStateWatermark                                                                                                               int64
	steadyGeneration                                                                                                                   int64
	generationFloor                                                                                                                    int64
	floorLiftEpoch                                                                                                                     int64
	settleWindowEpoch                                                                                                                  int64
	spilloverEpoch                                                                                                                     int64
	spilloverRejoinEpoch                                                                                                               int64
	rejoinSealEpoch                                                                                                                    int64
	sealDriftEpoch                                                                                                                     int64
	driftReanchorEpoch                                                                                                                 int64
	reanchorCompactionEpoch                                                                                                            int64
	compactionExpiryEpoch                                                                                                              int64
	resurrectionEpoch                                                                                                                  int64
	reintegrationEpoch                                                                                                                 int64
	reintegrationSealEpoch                                                                                                             int64
	reintegrationSealDriftEpoch                                                                                                        int64
	reintegrationSealDriftReanchorEpoch                                                                                                int64
	reintegrationSealDriftReanchorCompactionEpoch                                                                                      int64
	reintegrationSealDriftReanchorCompactionExpiryEpoch                                                                                int64
	reintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch                                                                      int64
	reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch                                                         int64
	reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpoch                                                     int64
	reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpoch                                                int64
	reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpoch                                        int64
	reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpoch                              int64
	reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpoch                        int64
	reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch              int64
	reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch int64
}

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
	releaseEpoch := prefixFields.releaseEpoch
	bridgeSequence := prefixFields.bridgeSequence
	hasBridgeSequence := prefixFields.hasBridgeSequence
	releaseWatermark := prefixFields.releaseWatermark
	hasExplicitWatermark := prefixFields.hasReleaseWatermark
	drainWatermark := prefixFields.drainWatermark
	hasDrainWatermark := prefixFields.hasDrainWatermark
	liveHead := prefixFields.liveHead
	hasLiveHead := prefixFields.hasLiveHead
	steadyStateWatermark := prefixFields.steadyStateWatermark
	hasSteadyStateWatermark := prefixFields.hasSteadyStateWatermark
	steadyGeneration := prefixFields.steadyGeneration
	hasSteadyGeneration := prefixFields.hasSteadyGeneration
	generationFloor := prefixFields.generationFloor
	hasGenerationFloor := prefixFields.hasGenerationFloor
	floorLiftEpoch := prefixFields.floorLiftEpoch
	hasFloorLiftEpoch := prefixFields.hasFloorLiftEpoch
	settleWindowEpoch := prefixFields.settleWindowEpoch
	hasSettleWindowEpoch := prefixFields.hasSettleWindowEpoch
	spilloverEpoch := prefixFields.spilloverEpoch
	hasSpilloverEpoch := prefixFields.hasSpilloverEpoch
	spilloverRejoinEpoch := prefixFields.spilloverRejoinEpoch
	hasSpilloverRejoinEpoch := prefixFields.hasSpilloverRejoinEpoch
	rejoinSealEpoch := prefixFields.rejoinSealEpoch
	hasRejoinSealEpoch := prefixFields.hasRejoinSealEpoch
	sealDriftEpoch, hasSealDriftEpoch := parseRollbackFenceSealDriftEpoch(normalized)
	driftReanchorEpoch, hasDriftReanchorEpoch := parseRollbackFenceDriftReanchorEpoch(normalized)
	reanchorCompactionEpoch, hasReanchorCompactionEpoch := parseRollbackFenceReanchorCompactionEpoch(normalized)
	compactionExpiryEpoch, hasCompactionExpiryEpoch := parseRollbackFenceCompactionExpiryEpoch(normalized)
	resurrectionEpoch, hasResurrectionEpoch := parseRollbackFenceResurrectionQuarantineEpoch(normalized)
	reintegrationEpoch, hasReintegrationEpoch := parseRollbackFenceResurrectionReintegrationEpoch(normalized)
	reintegrationSealEpoch, hasReintegrationSealEpoch := parseRollbackFenceResurrectionReintegrationSealEpoch(normalized)
	reintegrationSealDriftEpoch, hasReintegrationSealDriftEpoch := parseRollbackFenceResurrectionReintegrationSealDriftEpoch(normalized)
	reintegrationSealDriftReanchorEpoch, hasReintegrationSealDriftReanchorEpoch := parseRollbackFenceResurrectionReintegrationSealDriftReanchorEpoch(normalized)
	reintegrationSealDriftReanchorCompactionEpoch, hasReintegrationSealDriftReanchorCompactionEpoch := parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionEpoch(normalized)
	reintegrationSealDriftReanchorCompactionExpiryEpoch, hasReintegrationSealDriftReanchorCompactionExpiryEpoch := parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryEpoch(normalized)
	reintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch, hasReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch := parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch(normalized)
	reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch, hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch := parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch(normalized)
	reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpoch, hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpoch := parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpoch(normalized)
	reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpoch, hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpoch := parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpoch(normalized)
	reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpoch, hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpoch := parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpoch(normalized)
	reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpoch, hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpoch := parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpoch(normalized)
	reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpoch, hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpoch := parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpoch(normalized)
	reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch, hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch := parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch(normalized)
	reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch, hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch := parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch(normalized)
	if hasBridgeSequence != hasExplicitWatermark {
		// Quarantine ambiguous late-bridge markers until both sequence and
		// release watermark are present for deterministic ordering.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasDrainWatermark && !hasBridgeSequence {
		// Quarantine ambiguous backlog-drain markers until the corresponding
		// late-bridge ownership tuple is complete.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasLiveHead && !hasBridgeSequence {
		// Quarantine ambiguous live-catchup markers until the corresponding
		// late-bridge ownership tuple is complete.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasLiveHead && !hasDrainWatermark {
		// Quarantine drain-to-live handoff markers until an explicit
		// backlog-drain watermark is present in the ownership tuple.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasSteadyStateWatermark && !hasLiveHead {
		// Quarantine steady-state rebaseline markers until the corresponding
		// live-catchup ownership tuple is complete.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasSteadyGeneration && !hasSteadyStateWatermark {
		// Quarantine baseline-rotation generation markers until explicit
		// steady-state ownership is present for deterministic cross-generation
		// ordering.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasGenerationFloor && !hasSteadyGeneration {
		// Quarantine generation-prune markers until the corresponding
		// steady-generation ownership tuple is explicit.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasFloorLiftEpoch && !hasGenerationFloor {
		// Quarantine retention-floor-lift markers until explicit
		// generation-retention-floor ownership is present.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasSettleWindowEpoch && !hasFloorLiftEpoch {
		// Quarantine settle-window markers until explicit retention-floor-lift
		// ownership is present.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasSpilloverEpoch && !hasSettleWindowEpoch {
		// Quarantine late-spillover markers until explicit settle-window
		// ownership is present.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasSpilloverRejoinEpoch && !hasSpilloverEpoch {
		// Quarantine spillover-rejoin markers until explicit spillover
		// ownership is present.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasRejoinSealEpoch && !hasSpilloverRejoinEpoch {
		// Quarantine steady-seal markers until explicit spillover-rejoin
		// ownership is present.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasSealDriftEpoch && !hasRejoinSealEpoch {
		// Quarantine post-steady-seal drift markers until explicit steady-seal
		// ownership is present.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasDriftReanchorEpoch && !hasSealDriftEpoch {
		// Quarantine post-drift reanchor markers until explicit post-drift
		// ownership is present.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReanchorCompactionEpoch && !hasDriftReanchorEpoch {
		// Quarantine post-reanchor lineage-compaction markers until explicit
		// post-drift reanchor ownership is present.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasCompactionExpiryEpoch && !hasReanchorCompactionEpoch {
		// Quarantine post-lineage-compaction marker-expiry markers until
		// explicit post-reanchor compaction ownership is present.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasResurrectionEpoch && !hasCompactionExpiryEpoch {
		// Quarantine post-marker-expiry late-resurrection markers until
		// explicit post-lineage-compaction marker-expiry ownership is present.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationEpoch && !hasResurrectionEpoch {
		// Quarantine reintegration markers until explicit post-late-resurrection
		// quarantine ownership is present.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationSealEpoch && !hasReintegrationEpoch {
		// Quarantine post-reintegration seal markers until explicit reintegration
		// ownership is present.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationSealDriftEpoch && !hasReintegrationSealEpoch {
		// Quarantine post-reintegration-seal drift markers until explicit
		// reintegration-seal ownership is present.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationSealDriftReanchorEpoch && !hasReintegrationSealDriftEpoch {
		// Quarantine post-reintegration-seal drift-reanchor markers until
		// explicit reintegration-seal drift ownership is present.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationSealDriftReanchorCompactionEpoch && !hasReintegrationSealDriftReanchorEpoch {
		// Quarantine post-reintegration-seal drift-reanchor lineage-compaction
		// markers until explicit reintegration-seal drift-reanchor ownership is
		// present.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationSealDriftReanchorCompactionExpiryEpoch && !hasReintegrationSealDriftReanchorCompactionEpoch {
		// Quarantine post-reintegration-seal drift-reanchor lineage-compaction
		// marker-expiry candidates until explicit reintegration-seal
		// drift-reanchor lineage-compaction ownership is present.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch && !hasReintegrationSealDriftReanchorCompactionExpiryEpoch {
		// Quarantine post-reintegration-seal drift-reanchor lineage-compaction
		// marker-expiry late-resurrection quarantine candidates until explicit
		// reintegration-seal drift-reanchor lineage-compaction marker-expiry
		// ownership is present.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch &&
		!hasReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch {
		// Quarantine post-reintegration-seal drift-reanchor lineage-compaction
		// marker-expiry late-resurrection quarantine reintegration candidates
		// until explicit quarantine ownership is present.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpoch &&
		!hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch {
		// Quarantine post-reintegration-seal drift-reanchor lineage-compaction
		// marker-expiry late-resurrection quarantine reintegration-seal
		// candidates until explicit reintegration ownership is present.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpoch &&
		!hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpoch {
		// Quarantine post-reintegration-seal drift-reanchor lineage-compaction
		// marker-expiry late-resurrection quarantine reintegration-seal-drift
		// candidates until explicit reintegration-seal ownership is present.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpoch &&
		!hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpoch {
		// Quarantine post-reintegration-seal drift-reanchor lineage-compaction
		// marker-expiry late-resurrection quarantine reintegration-seal-drift-reanchor
		// candidates until explicit reintegration-seal-drift ownership is present.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpoch &&
		!hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpoch {
		// Quarantine post-reintegration-seal drift-reanchor lineage-compaction
		// marker-expiry late-resurrection quarantine reintegration-seal-drift-reanchor-lineage-compaction
		// candidates until explicit reintegration-seal-drift-reanchor ownership is present.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpoch &&
		!hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpoch {
		// Quarantine post-reintegration-seal drift-reanchor lineage-compaction
		// marker-expiry late-resurrection quarantine reintegration-seal-drift-reanchor-lineage-compaction
		// marker-expiry candidates until explicit reintegration-seal-drift-reanchor-lineage-compaction
		// ownership is present.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch &&
		!hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpoch {
		// Quarantine post-reintegration-seal drift-reanchor lineage-compaction
		// marker-expiry late-resurrection quarantine reintegration-seal-drift-reanchor-lineage-compaction
		// marker-expiry-late-resurrection-quarantine candidates until explicit
		// reintegration-seal-drift-reanchor-lineage-compaction marker-expiry
		// ownership is present.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch &&
		!hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch {
		// Quarantine post-reintegration-seal drift-reanchor lineage-compaction
		// marker-expiry late-resurrection quarantine reintegration-seal-drift-reanchor-lineage-compaction
		// marker-expiry-late-resurrection-quarantine-reintegration candidates
		// until explicit late-resurrection-quarantine ownership is present.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if !hasBridgeSequence {
		bridgeSequence = 0
		releaseWatermark = releaseEpoch
	}
	if releaseWatermark < releaseEpoch {
		return rollbackFenceOwnershipOrdering{}, false
	}
	if !hasDrainWatermark {
		drainWatermark = releaseWatermark
	}
	if drainWatermark < releaseWatermark {
		return rollbackFenceOwnershipOrdering{}, false
	}
	if !hasLiveHead {
		liveHead = drainWatermark
	}
	if liveHead < drainWatermark {
		return rollbackFenceOwnershipOrdering{}, false
	}
	if !hasSteadyStateWatermark {
		steadyStateWatermark = liveHead
	}
	if steadyStateWatermark < liveHead {
		return rollbackFenceOwnershipOrdering{}, false
	}
	if !hasSteadyGeneration {
		steadyGeneration = 0
	}
	if !hasGenerationFloor {
		generationFloor = 0
	}
	if !hasFloorLiftEpoch {
		floorLiftEpoch = 0
	}
	if !hasSettleWindowEpoch {
		settleWindowEpoch = 0
	}
	if !hasSpilloverEpoch {
		spilloverEpoch = 0
	}
	if !hasSpilloverRejoinEpoch {
		spilloverRejoinEpoch = 0
	}
	if !hasRejoinSealEpoch {
		rejoinSealEpoch = 0
	}
	if !hasSealDriftEpoch {
		sealDriftEpoch = 0
	}
	if !hasDriftReanchorEpoch {
		driftReanchorEpoch = 0
	}
	if !hasReanchorCompactionEpoch {
		reanchorCompactionEpoch = 0
	}
	if !hasCompactionExpiryEpoch {
		compactionExpiryEpoch = 0
	}
	if !hasResurrectionEpoch {
		resurrectionEpoch = 0
	}
	if !hasReintegrationEpoch {
		reintegrationEpoch = 0
	}
	if !hasReintegrationSealEpoch {
		reintegrationSealEpoch = 0
	}
	if !hasReintegrationSealDriftEpoch {
		reintegrationSealDriftEpoch = 0
	}
	if !hasReintegrationSealDriftReanchorEpoch {
		reintegrationSealDriftReanchorEpoch = 0
	}
	if !hasReintegrationSealDriftReanchorCompactionEpoch {
		reintegrationSealDriftReanchorCompactionEpoch = 0
	}
	if !hasReintegrationSealDriftReanchorCompactionExpiryEpoch {
		reintegrationSealDriftReanchorCompactionExpiryEpoch = 0
	}
	if !hasReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch {
		reintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch = 0
	}
	if !hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch {
		reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch = 0
	}
	if !hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpoch {
		reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpoch = 0
	}
	if !hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpoch {
		reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpoch = 0
	}
	if !hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpoch {
		reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpoch = 0
	}
	if !hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpoch {
		reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpoch = 0
	}
	if !hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpoch {
		reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpoch = 0
	}
	if !hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch {
		reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch = 0
	}
	if !hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch {
		reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch = 0
	}
	if hasResurrectionEpoch && resurrectionEpoch <= compactionExpiryEpoch {
		// Quarantine ambiguous late-resurrection markers that do not advance
		// strictly beyond verified marker-expiry ownership.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationEpoch && reintegrationEpoch <= resurrectionEpoch {
		// Quarantine ambiguous reintegration markers that do not advance
		// strictly beyond verified late-resurrection ownership.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationSealEpoch && reintegrationSealEpoch <= reintegrationEpoch {
		// Quarantine ambiguous reintegration seal markers that do not advance
		// strictly beyond verified reintegration ownership.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationSealDriftEpoch && reintegrationSealDriftEpoch <= reintegrationSealEpoch {
		// Quarantine ambiguous reintegration-seal drift markers that do not
		// advance strictly beyond verified reintegration-seal ownership.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationSealDriftReanchorEpoch && reintegrationSealDriftReanchorEpoch <= reintegrationSealDriftEpoch {
		// Quarantine ambiguous reintegration-seal drift-reanchor markers that
		// do not advance strictly beyond verified reintegration-seal drift
		// ownership.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationSealDriftReanchorCompactionEpoch && reintegrationSealDriftReanchorCompactionEpoch <= reintegrationSealDriftReanchorEpoch {
		// Quarantine ambiguous reintegration-seal drift-reanchor
		// lineage-compaction markers that do not advance strictly beyond
		// verified reintegration-seal drift-reanchor ownership.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationSealDriftReanchorCompactionExpiryEpoch && reintegrationSealDriftReanchorCompactionExpiryEpoch <= reintegrationSealDriftReanchorCompactionEpoch {
		// Quarantine ambiguous reintegration-seal drift-reanchor
		// lineage-compaction marker-expiry candidates that do not advance
		// strictly beyond verified reintegration-seal drift-reanchor
		// lineage-compaction ownership.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch &&
		reintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch <= reintegrationSealDriftReanchorCompactionExpiryEpoch {
		// Quarantine ambiguous reintegration-seal drift-reanchor
		// lineage-compaction marker-expiry late-resurrection quarantine
		// candidates that do not advance strictly beyond verified
		// reintegration-seal drift-reanchor lineage-compaction marker-expiry
		// ownership.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch &&
		reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch <= reintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch {
		// Quarantine ambiguous reintegration-seal drift-reanchor
		// lineage-compaction marker-expiry late-resurrection quarantine
		// reintegration candidates that do not advance strictly beyond verified
		// quarantine ownership.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpoch &&
		reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpoch <= reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch {
		// Quarantine ambiguous reintegration-seal drift-reanchor
		// lineage-compaction marker-expiry late-resurrection quarantine
		// reintegration-seal candidates that do not advance strictly beyond
		// verified reintegration ownership.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpoch &&
		reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpoch <= reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpoch {
		// Quarantine ambiguous reintegration-seal drift-reanchor
		// lineage-compaction marker-expiry late-resurrection quarantine
		// reintegration-seal-drift candidates that do not advance strictly
		// beyond verified reintegration-seal ownership.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpoch &&
		reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpoch <= reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpoch {
		// Quarantine ambiguous reintegration-seal drift-reanchor
		// lineage-compaction marker-expiry late-resurrection quarantine
		// reintegration-seal-drift-reanchor candidates that do not advance
		// strictly beyond verified reintegration-seal-drift ownership.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpoch &&
		reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpoch <= reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpoch {
		// Quarantine ambiguous reintegration-seal drift-reanchor
		// lineage-compaction marker-expiry late-resurrection quarantine
		// reintegration-seal-drift-reanchor-lineage-compaction candidates that
		// do not advance strictly beyond verified reintegration-seal-drift-reanchor
		// ownership.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpoch &&
		reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpoch <= reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpoch {
		// Quarantine ambiguous reintegration-seal drift-reanchor
		// lineage-compaction marker-expiry late-resurrection quarantine
		// reintegration-seal-drift-reanchor-lineage-compaction marker-expiry
		// candidates that do not advance strictly beyond verified
		// reintegration-seal-drift-reanchor-lineage-compaction ownership.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch &&
		reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch <= reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpoch {
		// Quarantine ambiguous reintegration-seal drift-reanchor
		// lineage-compaction marker-expiry late-resurrection quarantine
		// reintegration-seal-drift-reanchor-lineage-compaction marker-expiry
		// late-resurrection-quarantine candidates that do not advance strictly
		// beyond verified marker-expiry ownership.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch &&
		reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch <= reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch {
		// Quarantine ambiguous reintegration-seal drift-reanchor
		// lineage-compaction marker-expiry late-resurrection quarantine
		// reintegration-seal-drift-reanchor-lineage-compaction marker-expiry
		// late-resurrection-quarantine-reintegration candidates that do not
		// advance strictly beyond verified late-resurrection-quarantine
		// ownership.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if generationFloor > steadyGeneration {
		// Quarantine unresolved retired-generation markers whose ownership
		// points below the active retention floor.
		return rollbackFenceOwnershipOrdering{}, false
	}
	return rollbackFenceOwnershipOrdering{
		epoch:                               epoch,
		bridgeSequence:                      bridgeSequence,
		drainWatermark:                      drainWatermark,
		liveHead:                            liveHead,
		steadyStateWatermark:                steadyStateWatermark,
		steadyGeneration:                    steadyGeneration,
		generationFloor:                     generationFloor,
		floorLiftEpoch:                      floorLiftEpoch,
		settleWindowEpoch:                   settleWindowEpoch,
		spilloverEpoch:                      spilloverEpoch,
		spilloverRejoinEpoch:                spilloverRejoinEpoch,
		rejoinSealEpoch:                     rejoinSealEpoch,
		sealDriftEpoch:                      sealDriftEpoch,
		driftReanchorEpoch:                  driftReanchorEpoch,
		reanchorCompactionEpoch:             reanchorCompactionEpoch,
		compactionExpiryEpoch:               compactionExpiryEpoch,
		resurrectionEpoch:                   resurrectionEpoch,
		reintegrationEpoch:                  reintegrationEpoch,
		reintegrationSealEpoch:              reintegrationSealEpoch,
		reintegrationSealDriftEpoch:         reintegrationSealDriftEpoch,
		reintegrationSealDriftReanchorEpoch: reintegrationSealDriftReanchorEpoch,
		reintegrationSealDriftReanchorCompactionEpoch:                                                                                      reintegrationSealDriftReanchorCompactionEpoch,
		reintegrationSealDriftReanchorCompactionExpiryEpoch:                                                                                reintegrationSealDriftReanchorCompactionExpiryEpoch,
		reintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch:                                                                      reintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch,
		reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch:                                                         reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch,
		reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpoch:                                                     reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpoch,
		reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpoch:                                                reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpoch,
		reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpoch:                                        reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpoch,
		reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpoch:                              reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpoch,
		reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpoch:                        reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpoch,
		reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch:              reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch,
		reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch: reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch,
	}, true
}

func compareRollbackFenceOwnershipOrdering(
	left rollbackFenceOwnershipOrdering,
	right rollbackFenceOwnershipOrdering,
) int {
	switch {
	case left.epoch < right.epoch:
		return -1
	case left.epoch > right.epoch:
		return 1
	}
	switch {
	case left.bridgeSequence < right.bridgeSequence:
		return -1
	case left.bridgeSequence > right.bridgeSequence:
		return 1
	}
	switch {
	case left.drainWatermark < right.drainWatermark:
		return -1
	case left.drainWatermark > right.drainWatermark:
		return 1
	}
	switch {
	case left.liveHead < right.liveHead:
		return -1
	case left.liveHead > right.liveHead:
		return 1
	}
	switch {
	case left.steadyStateWatermark < right.steadyStateWatermark:
		return -1
	case left.steadyStateWatermark > right.steadyStateWatermark:
		return 1
	}
	switch {
	case left.steadyGeneration < right.steadyGeneration:
		return -1
	case left.steadyGeneration > right.steadyGeneration:
		return 1
	}
	switch {
	case left.generationFloor < right.generationFloor:
		return -1
	case left.generationFloor > right.generationFloor:
		return 1
	}
	switch {
	case left.floorLiftEpoch < right.floorLiftEpoch:
		return -1
	case left.floorLiftEpoch > right.floorLiftEpoch:
		return 1
	}
	switch {
	case left.settleWindowEpoch < right.settleWindowEpoch:
		return -1
	case left.settleWindowEpoch > right.settleWindowEpoch:
		return 1
	}
	switch {
	case left.spilloverEpoch < right.spilloverEpoch:
		return -1
	case left.spilloverEpoch > right.spilloverEpoch:
		return 1
	}
	switch {
	case left.spilloverRejoinEpoch < right.spilloverRejoinEpoch:
		return -1
	case left.spilloverRejoinEpoch > right.spilloverRejoinEpoch:
		return 1
	}
	switch {
	case left.rejoinSealEpoch < right.rejoinSealEpoch:
		return -1
	case left.rejoinSealEpoch > right.rejoinSealEpoch:
		return 1
	default:
	}
	switch {
	case left.sealDriftEpoch < right.sealDriftEpoch:
		return -1
	case left.sealDriftEpoch > right.sealDriftEpoch:
		return 1
	}
	switch {
	case left.driftReanchorEpoch < right.driftReanchorEpoch:
		return -1
	case left.driftReanchorEpoch > right.driftReanchorEpoch:
		return 1
	}
	switch {
	case left.reanchorCompactionEpoch < right.reanchorCompactionEpoch:
		return -1
	case left.reanchorCompactionEpoch > right.reanchorCompactionEpoch:
		return 1
	}
	switch {
	case left.compactionExpiryEpoch < right.compactionExpiryEpoch:
		return -1
	case left.compactionExpiryEpoch > right.compactionExpiryEpoch:
		return 1
	}
	switch {
	case left.resurrectionEpoch < right.resurrectionEpoch:
		return -1
	case left.resurrectionEpoch > right.resurrectionEpoch:
		return 1
	}
	switch {
	case left.reintegrationEpoch < right.reintegrationEpoch:
		return -1
	case left.reintegrationEpoch > right.reintegrationEpoch:
		return 1
	}
	switch {
	case left.reintegrationSealEpoch < right.reintegrationSealEpoch:
		return -1
	case left.reintegrationSealEpoch > right.reintegrationSealEpoch:
		return 1
	}
	switch {
	case left.reintegrationSealDriftEpoch < right.reintegrationSealDriftEpoch:
		return -1
	case left.reintegrationSealDriftEpoch > right.reintegrationSealDriftEpoch:
		return 1
	}
	switch {
	case left.reintegrationSealDriftReanchorEpoch < right.reintegrationSealDriftReanchorEpoch:
		return -1
	case left.reintegrationSealDriftReanchorEpoch > right.reintegrationSealDriftReanchorEpoch:
		return 1
	}
	switch {
	case left.reintegrationSealDriftReanchorCompactionEpoch < right.reintegrationSealDriftReanchorCompactionEpoch:
		return -1
	case left.reintegrationSealDriftReanchorCompactionEpoch > right.reintegrationSealDriftReanchorCompactionEpoch:
		return 1
	}
	switch {
	case left.reintegrationSealDriftReanchorCompactionExpiryEpoch < right.reintegrationSealDriftReanchorCompactionExpiryEpoch:
		return -1
	case left.reintegrationSealDriftReanchorCompactionExpiryEpoch > right.reintegrationSealDriftReanchorCompactionExpiryEpoch:
		return 1
	}
	switch {
	case left.reintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch < right.reintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch:
		return -1
	case left.reintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch > right.reintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch:
		return 1
	}
	switch {
	case left.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch < right.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch:
		return -1
	case left.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch > right.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch:
		return 1
	}
	switch {
	case left.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpoch < right.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpoch:
		return -1
	case left.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpoch > right.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpoch:
		return 1
	}
	switch {
	case left.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpoch < right.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpoch:
		return -1
	case left.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpoch > right.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpoch:
		return 1
	}
	switch {
	case left.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpoch < right.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpoch:
		return -1
	case left.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpoch > right.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpoch:
		return 1
	}
	switch {
	case left.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpoch < right.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpoch:
		return -1
	case left.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpoch > right.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpoch:
		return 1
	}
	switch {
	case left.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpoch < right.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpoch:
		return -1
	case left.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpoch > right.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpoch:
		return 1
	}
	switch {
	case left.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch < right.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch:
		return -1
	case left.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch > right.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch:
		return 1
	}
	switch {
	case left.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch < right.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch:
		return -1
	case left.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch > right.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch:
		return 1
	default:
		return 0
	}
}
