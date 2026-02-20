package autotune

import (
	"strconv"
	"strings"
)

func isRollbackFenceTransition(epoch int64, digest string) bool {
	if epoch < 0 {
		return false
	}
	rollbackFromSeq, rollbackToSeq, _, ok := parseRollbackLineage(normalizePolicyManifestDigest(digest))
	if !ok {
		return false
	}
	if rollbackToSeq != epoch {
		return false
	}
	return rollbackFromSeq > rollbackToSeq
}

func isRollbackFenceEpochCompactionDigest(epoch int64, digest string) bool {
	if epoch < 0 {
		return false
	}
	normalized := normalizePolicyManifestDigest(digest)
	if !hasRollbackFenceEpochCompactionTombstone(normalized) {
		return false
	}
	rollbackFromSeq, rollbackToSeq, rollbackForwardSeq, ok := parseRollbackLineage(normalized)
	if !ok {
		return false
	}
	if rollbackToSeq != epoch {
		return false
	}
	if rollbackFromSeq <= rollbackToSeq {
		return false
	}
	if rollbackForwardSeq < rollbackFromSeq {
		return false
	}
	return true
}

func isDeterministicRollbackFenceEpochCompactionTransition(
	epoch int64,
	sourceDigest string,
	targetDigest string,
) bool {
	if epoch < 0 {
		return false
	}
	sourceNormalized := normalizePolicyManifestDigest(sourceDigest)
	targetNormalized := normalizePolicyManifestDigest(targetDigest)
	if !hasRollbackFenceEpochCompactionTombstone(targetNormalized) {
		return false
	}
	return validateLineagePair(epoch, sourceNormalized, targetNormalized)
}

func hasRollbackFenceEpochCompactionTombstone(digest string) bool {
	const (
		tombstoneKey      = "rollback-fence-tombstone"
		tombstoneValueKey = tombstoneKey + "="
	)
	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		switch {
		case token == tombstoneKey:
			return true
		case strings.HasPrefix(token, tombstoneValueKey):
			value := strings.TrimSpace(strings.TrimPrefix(token, tombstoneValueKey))
			switch value {
			case "1", "true", "yes", "on":
				return true
			}
		}
	}
	return false
}

func parseRollbackFenceTombstoneExpiryEpoch(digest string) (int64, bool) {
	return parseSingleKey(digest, "rollback-fence-tombstone-expiry-epoch=")
}

func parseRollbackFenceLateMarkerHoldEpoch(digest string) (int64, bool) {
	return parseSingleKey(digest, "rollback-fence-late-marker-hold-epoch=")
}

func parseRollbackFenceLateMarkerReleaseEpoch(digest string) (int64, bool) {
	return parseSingleKey(digest, "rollback-fence-late-marker-release-epoch=")
}

// parseSingleKey extracts a single int64 value from a pipe-delimited digest.
func parseSingleKey(digest, keyPrefix string) (int64, bool) {
	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		if !strings.HasPrefix(token, keyPrefix) {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(token, keyPrefix))
		if value == "" {
			return 0, false
		}
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsed < 0 {
			return 0, false
		}
		return parsed, true
	}
	return 0, false
}

func isRollbackFenceTombstoneExpiryDigest(epoch int64, digest string) bool {
	if epoch < 0 {
		return false
	}
	normalized := normalizePolicyManifestDigest(digest)
	if hasRollbackFenceEpochCompactionTombstone(normalized) {
		return false
	}
	if _, hasHold := parseRollbackFenceLateMarkerHoldEpoch(normalized); hasHold {
		return false
	}
	if _, hasRelease := parseRollbackFenceLateMarkerReleaseEpoch(normalized); hasRelease {
		return false
	}
	expiryEpoch, ok := parseRollbackFenceTombstoneExpiryEpoch(normalized)
	if !ok {
		return false
	}
	if !validateRollbackLineage(epoch, normalized) {
		return false
	}
	return expiryEpoch > epoch
}

func isRollbackFencePostExpiryLateMarkerQuarantineDigest(epoch int64, digest string) bool {
	if epoch < 0 {
		return false
	}
	normalized := normalizePolicyManifestDigest(digest)
	if hasRollbackFenceEpochCompactionTombstone(normalized) {
		return false
	}
	if _, hasRelease := parseRollbackFenceLateMarkerReleaseEpoch(normalized); hasRelease {
		return false
	}
	expiryEpoch, ok := parseRollbackFenceTombstoneExpiryEpoch(normalized)
	if !ok {
		return false
	}
	holdEpoch, ok := parseRollbackFenceLateMarkerHoldEpoch(normalized)
	if !ok {
		return false
	}
	if !validateRollbackLineage(epoch, normalized) {
		return false
	}
	if expiryEpoch <= epoch {
		return false
	}
	return holdEpoch > expiryEpoch
}

func isRollbackFencePostExpiryLateMarkerReleaseDigest(epoch int64, digest string) bool {
	if epoch < 0 {
		return false
	}
	normalized := normalizePolicyManifestDigest(digest)
	if hasRollbackFenceEpochCompactionTombstone(normalized) {
		return false
	}
	expiryEpoch, ok := parseRollbackFenceTombstoneExpiryEpoch(normalized)
	if !ok {
		return false
	}
	holdEpoch, ok := parseRollbackFenceLateMarkerHoldEpoch(normalized)
	if !ok {
		return false
	}
	releaseEpoch, ok := parseRollbackFenceLateMarkerReleaseEpoch(normalized)
	if !ok {
		return false
	}
	if !validateRollbackLineage(epoch, normalized) {
		return false
	}
	if expiryEpoch <= epoch {
		return false
	}
	if holdEpoch <= expiryEpoch {
		return false
	}
	return releaseEpoch > holdEpoch
}

func isDeterministicRollbackFenceTombstoneExpiryTransition(
	epoch int64,
	sourceDigest string,
	targetDigest string,
) bool {
	if epoch < 0 {
		return false
	}
	sourceNormalized := normalizePolicyManifestDigest(sourceDigest)
	targetNormalized := normalizePolicyManifestDigest(targetDigest)
	if !isRollbackFenceEpochCompactionDigest(epoch, sourceNormalized) {
		return false
	}
	if !isRollbackFenceTombstoneExpiryDigest(epoch, targetNormalized) {
		return false
	}
	return validateLineagePair(epoch, sourceNormalized, targetNormalized)
}

func isDeterministicRollbackFencePostExpiryLateMarkerQuarantineTransition(
	epoch int64,
	sourceDigest string,
	targetDigest string,
) bool {
	if epoch < 0 {
		return false
	}
	sourceNormalized := normalizePolicyManifestDigest(sourceDigest)
	targetNormalized := normalizePolicyManifestDigest(targetDigest)
	if !isRollbackFenceTombstoneExpiryDigest(epoch, sourceNormalized) {
		return false
	}
	if !isRollbackFencePostExpiryLateMarkerQuarantineDigest(epoch, targetNormalized) {
		return false
	}
	if !validateLineagePair(epoch, sourceNormalized, targetNormalized) {
		return false
	}
	sourceExpiryEpoch, ok := parseRollbackFenceTombstoneExpiryEpoch(sourceNormalized)
	if !ok {
		return false
	}
	targetExpiryEpoch, ok := parseRollbackFenceTombstoneExpiryEpoch(targetNormalized)
	if !ok {
		return false
	}
	return sourceExpiryEpoch == targetExpiryEpoch
}

func isDeterministicRollbackFencePostExpiryLateMarkerReleaseTransition(
	epoch int64,
	sourceDigest string,
	targetDigest string,
) bool {
	if epoch < 0 {
		return false
	}
	sourceNormalized := normalizePolicyManifestDigest(sourceDigest)
	targetNormalized := normalizePolicyManifestDigest(targetDigest)
	if !isRollbackFencePostExpiryLateMarkerQuarantineDigest(epoch, sourceNormalized) {
		return false
	}
	if !isRollbackFencePostExpiryLateMarkerReleaseDigest(epoch, targetNormalized) {
		return false
	}
	if !validateLineagePair(epoch, sourceNormalized, targetNormalized) {
		return false
	}
	sourceExpiryEpoch, ok := parseRollbackFenceTombstoneExpiryEpoch(sourceNormalized)
	if !ok {
		return false
	}
	targetExpiryEpoch, ok := parseRollbackFenceTombstoneExpiryEpoch(targetNormalized)
	if !ok {
		return false
	}
	if sourceExpiryEpoch != targetExpiryEpoch {
		return false
	}
	sourceHoldEpoch, ok := parseRollbackFenceLateMarkerHoldEpoch(sourceNormalized)
	if !ok {
		return false
	}
	targetHoldEpoch, ok := parseRollbackFenceLateMarkerHoldEpoch(targetNormalized)
	if !ok {
		return false
	}
	if sourceHoldEpoch != targetHoldEpoch {
		return false
	}
	targetReleaseEpoch, ok := parseRollbackFenceLateMarkerReleaseEpoch(targetNormalized)
	if !ok {
		return false
	}
	return targetReleaseEpoch > targetHoldEpoch
}

// stageProgressionMarkers lists the epoch marker indices that participate in
// stage-progression checks within the release-window transition. Each entry
// references an epochMarkers index via slotMarkerBase offset.
var stageProgressionMarkers = [...]int{
	13, // reintegration-seal (cycle 2)
	14, // reintegration-seal-drift (cycle 2)
	15, // reintegration-seal-drift-reanchor (cycle 2)
	16, // reintegration-seal-drift-reanchor-compaction (cycle 2)
	18, // reintegration-seal-drift-reanchor-compaction-expiry-quarantine (cycle 2)
	12, // reintegration (cycle 2 - checked after quarantine)
	19, // reintegration (cycle 3)
	20, // reintegration-seal (cycle 3)
	21, // reintegration-seal-drift (cycle 3)
}

func isDeterministicRollbackFencePostExpiryLateMarkerReleaseWindowTransition(
	epoch int64,
	sourceDigest string,
	targetDigest string,
) bool {
	if epoch < 0 {
		return false
	}
	sourceNormalized := normalizePolicyManifestDigest(sourceDigest)
	targetNormalized := normalizePolicyManifestDigest(targetDigest)
	if !isRollbackFencePostExpiryLateMarkerReleaseDigest(epoch, sourceNormalized) {
		return false
	}
	if !isRollbackFencePostExpiryLateMarkerReleaseDigest(epoch, targetNormalized) {
		return false
	}
	if !validateLineagePair(epoch, sourceNormalized, targetNormalized) {
		return false
	}
	sourceExpiryEpoch, ok := parseRollbackFenceTombstoneExpiryEpoch(sourceNormalized)
	if !ok {
		return false
	}
	targetExpiryEpoch, ok := parseRollbackFenceTombstoneExpiryEpoch(targetNormalized)
	if !ok {
		return false
	}
	if sourceExpiryEpoch != targetExpiryEpoch {
		return false
	}
	sourceHoldEpoch, ok := parseRollbackFenceLateMarkerHoldEpoch(sourceNormalized)
	if !ok {
		return false
	}
	targetHoldEpoch, ok := parseRollbackFenceLateMarkerHoldEpoch(targetNormalized)
	if !ok {
		return false
	}
	if sourceHoldEpoch != targetHoldEpoch {
		return false
	}
	sourceOwnership, ok := parseRollbackFenceOwnershipOrdering(epoch, sourceNormalized)
	if !ok {
		return false
	}
	targetOwnership, ok := parseRollbackFenceOwnershipOrdering(epoch, targetNormalized)
	if !ok {
		return false
	}

	// Stage-progression checks: for each marker, if the target has it but the
	// source does not, the target wins (stage advancement). If the source has
	// it but the target does not, reject (stage regression).
	for _, idx := range stageProgressionMarkers {
		_, sourceHas := parseEpochMarker(sourceNormalized, epochMarkers[idx])
		_, targetHas := parseEpochMarker(targetNormalized, epochMarkers[idx])
		if !sourceHas && targetHas {
			return true
		}
		if sourceHas && !targetHas {
			return false
		}
	}

	return compareRollbackFenceOwnershipOrdering(sourceOwnership, targetOwnership) < 0
}

func isDeterministicRollbackFencePostReleaseWindowEpochRolloverStaleTransition(
	previousEpoch int64,
	incomingEpoch int64,
	previousDigest string,
	incomingDigest string,
) bool {
	if previousEpoch < 0 {
		return false
	}
	if incomingEpoch != previousEpoch+1 {
		return false
	}
	previousNormalized := normalizePolicyManifestDigest(previousDigest)
	if !isRollbackFencePostExpiryLateMarkerReleaseDigest(previousEpoch, previousNormalized) {
		return false
	}
	return isRollbackFenceDigestAnchoredToEpoch(previousEpoch, incomingDigest)
}

func isRollbackFenceDigestAnchoredToEpoch(epoch int64, digest string) bool {
	if epoch < 0 {
		return false
	}
	normalized := normalizePolicyManifestDigest(digest)
	return validateRollbackLineage(epoch, normalized)
}

// validateRollbackLineage validates that a normalized digest has a valid
// rollback lineage anchored to the given epoch.
func validateRollbackLineage(epoch int64, normalized string) bool {
	rollbackFromSeq, rollbackToSeq, rollbackForwardSeq, ok := parseRollbackLineage(normalized)
	if !ok {
		return false
	}
	if rollbackToSeq != epoch {
		return false
	}
	if rollbackFromSeq <= rollbackToSeq {
		return false
	}
	if rollbackForwardSeq < rollbackFromSeq {
		return false
	}
	return true
}

// validateLineagePair validates that two normalized digests share the same
// rollback lineage anchored to the given epoch.
func validateLineagePair(epoch int64, sourceNormalized, targetNormalized string) bool {
	sourceFromSeq, sourceToSeq, sourceForwardSeq, ok := parseRollbackLineage(sourceNormalized)
	if !ok {
		return false
	}
	targetFromSeq, targetToSeq, targetForwardSeq, ok := parseRollbackLineage(targetNormalized)
	if !ok {
		return false
	}
	if sourceFromSeq != targetFromSeq || sourceToSeq != targetToSeq || sourceForwardSeq != targetForwardSeq {
		return false
	}
	if sourceToSeq != epoch || targetToSeq != epoch {
		return false
	}
	if sourceFromSeq <= sourceToSeq || targetFromSeq <= targetToSeq {
		return false
	}
	if sourceForwardSeq < sourceFromSeq || targetForwardSeq < targetFromSeq {
		return false
	}
	return true
}
