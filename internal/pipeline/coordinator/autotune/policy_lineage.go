package autotune

import (
	"strconv"
	"strings"
)

func resolveSameEpochPolicyLineage(
	epoch int64,
	previousDigest string,
	incomingDigest string,
) (adoptIncoming bool, handled bool) {
	for _, rule := range sameEpochPolicyLineageRules {
		if !rule.match(epoch, previousDigest, incomingDigest) {
			continue
		}
		return rule.adoptIncoming, true
	}
	return false, false
}

func (a *autoTuneController) reconcilePolicyTransition(transition autoTunePolicyTransition) {
	if !transition.HasState {
		return
	}
	normalizedActivationHold := normalizeTransitionActivationHold(transition)
	previousVersion := normalizePolicyVersion(transition.Version)
	previousDigest := normalizePolicyManifestDigest(transition.ManifestDigest)
	previousEpoch := transition.Epoch
	if previousEpoch < 0 {
		previousEpoch = 0
	}

	incomingVersion := a.policyVersion
	incomingDigest := normalizePolicyManifestDigest(a.policyManifestDigest)
	incomingEpoch := a.policyEpoch
	if incomingEpoch < 0 {
		incomingEpoch = 0
	}

	if incomingVersion == previousVersion && incomingDigest == previousDigest {
		if incomingEpoch == previousEpoch+1 {
			a.policyManifestDigest = incomingDigest
			a.policyEpoch = incomingEpoch
			a.policyActivationLeft = a.policyActivationHold
			a.resetAdaptiveControlState()
		} else {
			// Reject duplicate/stale/sequence-gap transitions for identical digest lineage.
			a.policyManifestDigest = previousDigest
			a.policyEpoch = previousEpoch
		}
		if normalizedActivationHold > a.policyActivationLeft {
			a.policyActivationLeft = normalizedActivationHold
			a.resetAdaptiveControlState()
		}
		return
	}

	if incomingVersion != previousVersion {
		nextEpoch := maxInt64(previousEpoch+1, incomingEpoch)
		a.policyManifestDigest = incomingDigest
		a.policyEpoch = nextEpoch
		a.policyActivationLeft = a.policyActivationHold
		a.resetAdaptiveControlState()
		return
	}

	if incomingEpoch > previousEpoch {
		if isDeterministicRollbackFencePostReleaseWindowEpochRolloverStaleTransition(
			previousEpoch,
			incomingEpoch,
			previousDigest,
			incomingDigest,
		) {
			// Reject delayed prior-epoch rollback-fence ownership during
			// post-release-window epoch rollover; keep verified ownership until
			// current-epoch lineage is observed.
			a.policyManifestDigest = previousDigest
			a.policyEpoch = previousEpoch
			if normalizedActivationHold > a.policyActivationLeft {
				a.policyActivationLeft = normalizedActivationHold
				a.resetAdaptiveControlState()
			}
			return
		}
		if incomingEpoch == previousEpoch+1 {
			a.policyManifestDigest = incomingDigest
			a.policyEpoch = incomingEpoch
			a.policyActivationLeft = a.policyActivationHold
			a.resetAdaptiveControlState()
			return
		}
		if isDeterministicSnapshotCutover(previousEpoch, incomingEpoch, incomingDigest) {
			a.policyManifestDigest = incomingDigest
			a.policyEpoch = incomingEpoch
			a.policyActivationLeft = a.policyActivationHold
			a.resetAdaptiveControlState()
			return
		}
		// Reject sequence-gap transitions: pin previously verified contiguous lineage.
		a.policyManifestDigest = previousDigest
		a.policyEpoch = previousEpoch
		if normalizedActivationHold > a.policyActivationLeft {
			a.policyActivationLeft = normalizedActivationHold
			a.resetAdaptiveControlState()
		}
		return
	}

	if incomingEpoch < previousEpoch {
		if isDeterministicRollbackLineage(previousEpoch, incomingEpoch, incomingDigest) {
			a.policyManifestDigest = incomingDigest
			a.policyEpoch = incomingEpoch
			a.policyActivationLeft = a.policyActivationHold
			a.resetAdaptiveControlState()
			return
		}
		// Reject stale/ambiguous rollback: pin previously verified rollback-safe lineage.
		a.policyManifestDigest = previousDigest
		a.policyEpoch = previousEpoch
		if normalizedActivationHold > a.policyActivationLeft {
			a.policyActivationLeft = normalizedActivationHold
			a.resetAdaptiveControlState()
		}
		return
	}

	if incomingEpoch == previousEpoch {
		if adoptIncoming, handled := resolveSameEpochPolicyLineage(previousEpoch, previousDigest, incomingDigest); handled {
			if adoptIncoming {
				a.policyManifestDigest = incomingDigest
				a.policyEpoch = incomingEpoch
			} else {
				a.policyManifestDigest = previousDigest
				a.policyEpoch = previousEpoch
			}
			return
		}
	}

	// Reject stale/ambiguous refresh: pin previously verified manifest lineage.
	a.policyManifestDigest = previousDigest
	a.policyEpoch = previousEpoch
	if normalizedActivationHold > a.policyActivationLeft {
		a.policyActivationLeft = normalizedActivationHold
		a.resetAdaptiveControlState()
	}
}

func normalizeTransitionActivationHold(transition autoTunePolicyTransition) int {
	remaining := maxInt(transition.ActivationHoldRemaining, 0)
	if remaining == 0 || !transition.FromWarmCheckpoint {
		return remaining
	}
	if isRollbackFenceEpochCompactionDigest(transition.Epoch, transition.ManifestDigest) {
		// Compaction restores can replay pre-compaction hold payloads; collapse to
		// deterministic no-hold because compaction is same-epoch metadata ownership.
		return 0
	}
	if !isRollbackFenceTransition(transition.Epoch, transition.ManifestDigest) {
		return remaining
	}
	// Warm-restore snapshots can race with rollback fence flush timing and
	// persist either pre- or post-flush hold counts. Collapse to one
	// deterministic restore hold tick to avoid replay drift.
	if remaining > 1 {
		return 1
	}
	return remaining
}

func isDeterministicSnapshotCutover(previousEpoch, incomingEpoch int64, incomingDigest string) bool {
	if incomingEpoch <= previousEpoch+1 {
		return false
	}
	baseSeq, tailSeq, ok := parseSnapshotCutoverLineage(incomingDigest)
	if !ok {
		return false
	}
	if baseSeq != previousEpoch {
		return false
	}
	if tailSeq != incomingEpoch {
		return false
	}
	return tailSeq > baseSeq
}

func isDeterministicRollbackLineage(previousEpoch, incomingEpoch int64, incomingDigest string) bool {
	if incomingEpoch >= previousEpoch {
		return false
	}
	rollbackFromSeq, rollbackToSeq, rollbackForwardSeq, ok := parseRollbackLineage(incomingDigest)
	if !ok {
		return false
	}
	if rollbackFromSeq != previousEpoch {
		return false
	}
	if rollbackToSeq != incomingEpoch {
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

func parseRollbackLineage(digest string) (int64, int64, int64, bool) {
	const fromKey = "rollback-from-seq="
	const toKey = "rollback-to-seq="
	const forwardKey = "rollback-forward-seq="

	var (
		fromValue    string
		toValue      string
		forwardValue string
	)
	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		switch {
		case strings.HasPrefix(token, fromKey):
			fromValue = strings.TrimSpace(strings.TrimPrefix(token, fromKey))
		case strings.HasPrefix(token, toKey):
			toValue = strings.TrimSpace(strings.TrimPrefix(token, toKey))
		case strings.HasPrefix(token, forwardKey):
			forwardValue = strings.TrimSpace(strings.TrimPrefix(token, forwardKey))
		}
	}
	if fromValue == "" || toValue == "" || forwardValue == "" {
		return 0, 0, 0, false
	}
	fromSeq, err := strconv.ParseInt(fromValue, 10, 64)
	if err != nil || fromSeq < 0 {
		return 0, 0, 0, false
	}
	toSeq, err := strconv.ParseInt(toValue, 10, 64)
	if err != nil || toSeq < 0 {
		return 0, 0, 0, false
	}
	forwardSeq, err := strconv.ParseInt(forwardValue, 10, 64)
	if err != nil || forwardSeq < 0 {
		return 0, 0, 0, false
	}
	return fromSeq, toSeq, forwardSeq, true
}

func parseSnapshotCutoverLineage(digest string) (int64, int64, bool) {
	const baseKey = "snapshot-base-seq="
	const tailKey = "snapshot-tail-seq="

	var (
		baseValue string
		tailValue string
	)
	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		switch {
		case strings.HasPrefix(token, baseKey):
			baseValue = strings.TrimSpace(strings.TrimPrefix(token, baseKey))
		case strings.HasPrefix(token, tailKey):
			tailValue = strings.TrimSpace(strings.TrimPrefix(token, tailKey))
		}
	}
	if baseValue == "" || tailValue == "" {
		return 0, 0, false
	}
	baseSeq, err := strconv.ParseInt(baseValue, 10, 64)
	if err != nil || baseSeq < 0 {
		return 0, 0, false
	}
	tailSeq, err := strconv.ParseInt(tailValue, 10, 64)
	if err != nil || tailSeq < 0 {
		return 0, 0, false
	}
	return baseSeq, tailSeq, true
}
