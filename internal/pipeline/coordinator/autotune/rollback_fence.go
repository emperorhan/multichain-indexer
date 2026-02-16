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
	const expiryKey = "rollback-fence-tombstone-expiry-epoch="

	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		if !strings.HasPrefix(token, expiryKey) {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(token, expiryKey))
		if value == "" {
			return 0, false
		}
		expiryEpoch, err := strconv.ParseInt(value, 10, 64)
		if err != nil || expiryEpoch < 0 {
			return 0, false
		}
		return expiryEpoch, true
	}

	return 0, false
}

func parseRollbackFenceLateMarkerHoldEpoch(digest string) (int64, bool) {
	const holdKey = "rollback-fence-late-marker-hold-epoch="

	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		if !strings.HasPrefix(token, holdKey) {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(token, holdKey))
		if value == "" {
			return 0, false
		}
		holdEpoch, err := strconv.ParseInt(value, 10, 64)
		if err != nil || holdEpoch < 0 {
			return 0, false
		}
		return holdEpoch, true
	}

	return 0, false
}

func parseRollbackFenceLateMarkerReleaseEpoch(digest string) (int64, bool) {
	const releaseKey = "rollback-fence-late-marker-release-epoch="

	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		if !strings.HasPrefix(token, releaseKey) {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(token, releaseKey))
		if value == "" {
			return 0, false
		}
		releaseEpoch, err := strconv.ParseInt(value, 10, 64)
		if err != nil || releaseEpoch < 0 {
			return 0, false
		}
		return releaseEpoch, true
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
	// Expiry must be explicitly post-fence to avoid accepting ambiguous
	// same-epoch markers that can re-open stale ownership.
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
	if expiryEpoch <= epoch {
		return false
	}
	// Quarantine hold epochs must be explicitly post-expiry to avoid ambiguous
	// same-epoch marker ownership.
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
	if expiryEpoch <= epoch {
		return false
	}
	if holdEpoch <= expiryEpoch {
		return false
	}
	// Release boundaries must be explicitly post-hold to avoid accepting
	// ambiguous release ownership.
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
	_, sourceHasReintegrationSealBoundaryEpoch := parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpoch(sourceNormalized)
	_, targetHasReintegrationSealBoundaryEpoch := parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpoch(targetNormalized)
	if !sourceHasReintegrationSealBoundaryEpoch && targetHasReintegrationSealBoundaryEpoch {
		// Once late-resurrection-quarantine reintegration-seal ownership is
		// verified for a lineage, explicit reintegration-seal progression must
		// win deterministically over reintegration-only ownership.
		return true
	}
	if sourceHasReintegrationSealBoundaryEpoch && !targetHasReintegrationSealBoundaryEpoch {
		// Reject stage-regression from verified reintegration-seal ownership
		// back to reintegration-only ownership for the same lineage.
		return false
	}
	_, sourceHasReintegrationSealDriftBoundaryEpoch := parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpoch(sourceNormalized)
	_, targetHasReintegrationSealDriftBoundaryEpoch := parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpoch(targetNormalized)
	if !sourceHasReintegrationSealDriftBoundaryEpoch && targetHasReintegrationSealDriftBoundaryEpoch {
		// Once late-resurrection-quarantine reintegration-seal-drift ownership
		// is verified for a lineage, explicit reintegration-seal-drift
		// progression must win deterministically over reintegration-seal-only
		// ownership.
		return true
	}
	if sourceHasReintegrationSealDriftBoundaryEpoch && !targetHasReintegrationSealDriftBoundaryEpoch {
		// Reject stage-regression from verified reintegration-seal-drift
		// ownership back to reintegration-seal-only ownership for the same
		// lineage.
		return false
	}
	_, sourceHasReintegrationSealDriftReanchorBoundaryEpoch := parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpoch(sourceNormalized)
	_, targetHasReintegrationSealDriftReanchorBoundaryEpoch := parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpoch(targetNormalized)
	if !sourceHasReintegrationSealDriftReanchorBoundaryEpoch && targetHasReintegrationSealDriftReanchorBoundaryEpoch {
		// Once reintegration-seal-drift-reanchor ownership is verified for a
		// lineage, delayed reintegration-seal-drift echoes without explicit
		// reanchor ownership must not reclaim control even if drift epochs are
		// numerically larger.
		return true
	}
	if sourceHasReintegrationSealDriftReanchorBoundaryEpoch && !targetHasReintegrationSealDriftReanchorBoundaryEpoch {
		// Reject stage-regression from verified reintegration-seal-drift-reanchor
		// ownership back to reintegration-seal-drift-only ownership.
		return false
	}
	_, sourceHasReintegrationSealDriftReanchorCompactionBoundaryEpoch := parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpoch(sourceNormalized)
	_, targetHasReintegrationSealDriftReanchorCompactionBoundaryEpoch := parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpoch(targetNormalized)
	if !sourceHasReintegrationSealDriftReanchorCompactionBoundaryEpoch && targetHasReintegrationSealDriftReanchorCompactionBoundaryEpoch {
		// Once reintegration-seal-drift-reanchor-lineage-compaction ownership is
		// verified for a lineage, explicit compaction ownership progression must
		// win deterministically over prior reanchor-only ownership.
		return true
	}
	if sourceHasReintegrationSealDriftReanchorCompactionBoundaryEpoch && !targetHasReintegrationSealDriftReanchorCompactionBoundaryEpoch {
		// Reject stage-regression from verified reintegration-seal-drift-reanchor
		// lineage-compaction ownership back to reanchor-only ownership.
		return false
	}
	_, sourceHasReintegrationSealDriftReanchorCompactionExpiryQuarantineBoundaryEpoch := parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch(sourceNormalized)
	_, targetHasReintegrationSealDriftReanchorCompactionExpiryQuarantineBoundaryEpoch := parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch(targetNormalized)
	if !sourceHasReintegrationSealDriftReanchorCompactionExpiryQuarantineBoundaryEpoch && targetHasReintegrationSealDriftReanchorCompactionExpiryQuarantineBoundaryEpoch {
		// Once reintegration-seal-drift-reanchor-lineage-compaction-marker-expiry
		// ownership is verified for a lineage, explicit late-resurrection-quarantine
		// progression must win deterministically over prior marker-expiry ownership.
		return true
	}
	if sourceHasReintegrationSealDriftReanchorCompactionExpiryQuarantineBoundaryEpoch && !targetHasReintegrationSealDriftReanchorCompactionExpiryQuarantineBoundaryEpoch {
		// Reject stage-regression from verified reintegration-seal-drift-reanchor
		// lineage-compaction marker-expiry late-resurrection-quarantine ownership
		// back to marker-expiry-only ownership.
		return false
	}
	_, sourceHasReintegrationBoundaryEpoch := parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch(sourceNormalized)
	_, targetHasReintegrationBoundaryEpoch := parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch(targetNormalized)
	if !sourceHasReintegrationBoundaryEpoch && targetHasReintegrationBoundaryEpoch {
		// Once late-resurrection-quarantine reintegration ownership is verified for
		// a lineage, explicit reintegration progression must win deterministically
		// over pre-reintegration quarantine ownership.
		return true
	}
	if sourceHasReintegrationBoundaryEpoch && !targetHasReintegrationBoundaryEpoch {
		// Reject stage-regression from verified reintegration ownership back to
		// late-resurrection-quarantine ownership.
		return false
	}
	_, sourceHasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationBoundaryEpoch := parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch(sourceNormalized)
	_, targetHasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationBoundaryEpoch := parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch(targetNormalized)
	if !sourceHasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationBoundaryEpoch && targetHasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationBoundaryEpoch {
		// Once reintegration-seal-drift-reanchor-lineage-compaction-marker-expiry
		// late-resurrection-quarantine-reintegration ownership is verified for a
		// lineage, explicit reintegration progression must win deterministically
		// over pre-reintegration late-resurrection-quarantine ownership.
		return true
	}
	if sourceHasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationBoundaryEpoch && !targetHasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationBoundaryEpoch {
		// Reject stage-regression from verified reintegration ownership back to
		// late-resurrection-quarantine ownership for the same lineage.
		return false
	}
	// Ownership progression is strictly monotonic under explicit
	// (epoch, bridge_sequence, drain_watermark, live_head,
	// steady_state_watermark, steady_generation, generation_retention_floor,
	// floor_lift_epoch, settle_window_epoch, spillover_epoch,
	// spillover_rejoin_epoch, rejoin_seal_epoch, seal_drift_epoch,
	// drift_reanchor_epoch, reanchor_compaction_epoch,
	// compaction_expiry_epoch, resurrection_quarantine_epoch,
	// resurrection_reintegration_epoch, resurrection_reintegration_seal_epoch,
	// resurrection_reintegration_seal_drift_epoch,
	// resurrection_reintegration_seal_drift_reanchor_epoch,
	// resurrection_reintegration_seal_drift_reanchor_compaction_epoch,
	// resurrection_reintegration_seal_drift_reanchor_compaction_expiry_epoch,
	// resurrection_reintegration_seal_drift_reanchor_compaction_expiry_quarantine_epoch,
	// resurrection_reintegration_seal_drift_reanchor_compaction_expiry_quarantine_reintegration_epoch,
	// resurrection_reintegration_seal_drift_reanchor_compaction_expiry_quarantine_reintegration_seal_epoch,
	// resurrection_reintegration_seal_drift_reanchor_compaction_expiry_quarantine_reintegration_seal_drift_epoch,
	// resurrection_reintegration_seal_drift_reanchor_compaction_expiry_quarantine_reintegration_seal_drift_reanchor_epoch,
	// resurrection_reintegration_seal_drift_reanchor_compaction_expiry_quarantine_reintegration_seal_drift_reanchor_compaction_epoch,
	// resurrection_reintegration_seal_drift_reanchor_compaction_expiry_quarantine_reintegration_seal_drift_reanchor_compaction_expiry_epoch,
	// resurrection_reintegration_seal_drift_reanchor_compaction_expiry_quarantine_reintegration_seal_drift_reanchor_compaction_expiry_quarantine_epoch,
	// resurrection_reintegration_seal_drift_reanchor_compaction_expiry_quarantine_reintegration_seal_drift_reanchor_compaction_expiry_quarantine_reintegration_epoch)
	// ordering.
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
	// Once post-release-window ownership is verified, any digest still anchored
	// to the prior rollback-fence epoch is stale at rollover.
	return isRollbackFenceDigestAnchoredToEpoch(previousEpoch, incomingDigest)
}

func isRollbackFenceDigestAnchoredToEpoch(epoch int64, digest string) bool {
	if epoch < 0 {
		return false
	}
	normalized := normalizePolicyManifestDigest(digest)
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
