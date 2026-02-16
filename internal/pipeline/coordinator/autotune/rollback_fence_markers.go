package autotune

import (
	"strconv"
	"strings"
)

func parseRollbackFenceSealDriftEpoch(digest string) (int64, bool) {
	const (
		sealDriftEpochKey       = "rollback-fence-seal-drift-epoch="
		steadySealDriftEpochKey = "rollback-fence-steady-seal-drift-epoch="
		postSteadySealDriftKey  = "rollback-fence-post-steady-seal-drift-epoch="
	)

	var (
		sealDriftEpoch int64
		seen           bool
	)

	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		var value string
		switch {
		case strings.HasPrefix(token, sealDriftEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, sealDriftEpochKey))
		case strings.HasPrefix(token, steadySealDriftEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, steadySealDriftEpochKey))
		case strings.HasPrefix(token, postSteadySealDriftKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postSteadySealDriftKey))
		default:
			continue
		}
		if value == "" {
			return 0, false
		}
		parsedEpoch, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsedEpoch < 0 {
			return 0, false
		}
		if !seen {
			sealDriftEpoch = parsedEpoch
			seen = true
			continue
		}
		if sealDriftEpoch != parsedEpoch {
			return 0, false
		}
	}

	if !seen {
		return 0, false
	}
	return sealDriftEpoch, true
}

func parseRollbackFenceDriftReanchorEpoch(digest string) (int64, bool) {
	const (
		driftReanchorEpochKey      = "rollback-fence-drift-reanchor-epoch="
		postDriftReanchorEpochKey  = "rollback-fence-post-drift-reanchor-epoch="
		postSteadyDriftReanchorKey = "rollback-fence-post-steady-drift-reanchor-epoch="
	)

	var (
		driftReanchorEpoch int64
		seen               bool
	)

	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		var value string
		switch {
		case strings.HasPrefix(token, driftReanchorEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, driftReanchorEpochKey))
		case strings.HasPrefix(token, postDriftReanchorEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postDriftReanchorEpochKey))
		case strings.HasPrefix(token, postSteadyDriftReanchorKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postSteadyDriftReanchorKey))
		default:
			continue
		}
		if value == "" {
			return 0, false
		}
		parsedEpoch, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsedEpoch < 0 {
			return 0, false
		}
		if !seen {
			driftReanchorEpoch = parsedEpoch
			seen = true
			continue
		}
		if driftReanchorEpoch != parsedEpoch {
			return 0, false
		}
	}

	if !seen {
		return 0, false
	}
	return driftReanchorEpoch, true
}

func parseRollbackFenceReanchorCompactionEpoch(digest string) (int64, bool) {
	const (
		reanchorCompactionEpochKey     = "rollback-fence-reanchor-compaction-epoch="
		postReanchorCompactionEpochKey = "rollback-fence-post-reanchor-compaction-epoch="
		postDriftReanchorCompactionKey = "rollback-fence-post-drift-reanchor-compaction-epoch="
	)

	var (
		reanchorCompactionEpoch int64
		seen                    bool
	)

	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		var value string
		switch {
		case strings.HasPrefix(token, reanchorCompactionEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, reanchorCompactionEpochKey))
		case strings.HasPrefix(token, postReanchorCompactionEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postReanchorCompactionEpochKey))
		case strings.HasPrefix(token, postDriftReanchorCompactionKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postDriftReanchorCompactionKey))
		default:
			continue
		}
		if value == "" {
			return 0, false
		}
		parsedEpoch, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsedEpoch < 0 {
			return 0, false
		}
		if !seen {
			reanchorCompactionEpoch = parsedEpoch
			seen = true
			continue
		}
		if reanchorCompactionEpoch != parsedEpoch {
			return 0, false
		}
	}

	if !seen {
		return 0, false
	}
	return reanchorCompactionEpoch, true
}

func parseRollbackFenceCompactionExpiryEpoch(digest string) (int64, bool) {
	const (
		compactionExpiryEpochKey   = "rollback-fence-compaction-expiry-epoch="
		lineageCompactionExpiryKey = "rollback-fence-lineage-compaction-expiry-epoch="
		postLineageCompactionKey   = "rollback-fence-post-lineage-compaction-expiry-epoch="
	)

	var (
		compactionExpiryEpoch int64
		seen                  bool
	)

	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		var value string
		switch {
		case strings.HasPrefix(token, compactionExpiryEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, compactionExpiryEpochKey))
		case strings.HasPrefix(token, lineageCompactionExpiryKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, lineageCompactionExpiryKey))
		case strings.HasPrefix(token, postLineageCompactionKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postLineageCompactionKey))
		default:
			continue
		}
		if value == "" {
			return 0, false
		}
		parsedEpoch, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsedEpoch < 0 {
			return 0, false
		}
		if !seen {
			compactionExpiryEpoch = parsedEpoch
			seen = true
			continue
		}
		if compactionExpiryEpoch != parsedEpoch {
			return 0, false
		}
	}

	if !seen {
		return 0, false
	}
	return compactionExpiryEpoch, true
}

func parseRollbackFenceResurrectionQuarantineEpoch(digest string) (int64, bool) {
	const (
		resurrectionQuarantineEpochKey         = "rollback-fence-resurrection-quarantine-epoch="
		lateResurrectionQuarantineEpochKey     = "rollback-fence-late-resurrection-quarantine-epoch="
		postExpiryResurrectionQuarantineEpoch  = "rollback-fence-post-expiry-late-resurrection-quarantine-epoch="
		postMarkerExpiryResurrectionQuarantine = "rollback-fence-post-marker-expiry-late-resurrection-quarantine-epoch="
	)

	var (
		resurrectionQuarantineEpoch int64
		seen                        bool
	)

	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		var value string
		switch {
		case strings.HasPrefix(token, resurrectionQuarantineEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, resurrectionQuarantineEpochKey))
		case strings.HasPrefix(token, lateResurrectionQuarantineEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, lateResurrectionQuarantineEpochKey))
		case strings.HasPrefix(token, postExpiryResurrectionQuarantineEpoch):
			value = strings.TrimSpace(strings.TrimPrefix(token, postExpiryResurrectionQuarantineEpoch))
		case strings.HasPrefix(token, postMarkerExpiryResurrectionQuarantine):
			value = strings.TrimSpace(strings.TrimPrefix(token, postMarkerExpiryResurrectionQuarantine))
		default:
			continue
		}
		if value == "" {
			return 0, false
		}
		parsedEpoch, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsedEpoch < 0 {
			return 0, false
		}
		if !seen {
			resurrectionQuarantineEpoch = parsedEpoch
			seen = true
			continue
		}
		if resurrectionQuarantineEpoch != parsedEpoch {
			return 0, false
		}
	}

	if !seen {
		return 0, false
	}
	return resurrectionQuarantineEpoch, true
}

func parseRollbackFenceResurrectionReintegrationEpoch(digest string) (int64, bool) {
	const (
		resurrectionReintegrationEpochKey     = "rollback-fence-resurrection-reintegration-epoch="
		lateResurrectionReintegrationEpochKey = "rollback-fence-late-resurrection-reintegration-epoch="
		postQuarantineReintegrationEpochKey   = "rollback-fence-post-late-resurrection-quarantine-reintegration-epoch="
		postMarkerReintegrationEpochKey       = "rollback-fence-post-marker-expiry-late-resurrection-reintegration-epoch="
	)

	var (
		resurrectionReintegrationEpoch int64
		seen                           bool
	)

	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		var value string
		switch {
		case strings.HasPrefix(token, resurrectionReintegrationEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, resurrectionReintegrationEpochKey))
		case strings.HasPrefix(token, lateResurrectionReintegrationEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, lateResurrectionReintegrationEpochKey))
		case strings.HasPrefix(token, postQuarantineReintegrationEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postQuarantineReintegrationEpochKey))
		case strings.HasPrefix(token, postMarkerReintegrationEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postMarkerReintegrationEpochKey))
		default:
			continue
		}
		if value == "" {
			return 0, false
		}
		parsedEpoch, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsedEpoch < 0 {
			return 0, false
		}
		if !seen {
			resurrectionReintegrationEpoch = parsedEpoch
			seen = true
			continue
		}
		if resurrectionReintegrationEpoch != parsedEpoch {
			return 0, false
		}
	}

	if !seen {
		return 0, false
	}
	return resurrectionReintegrationEpoch, true
}

func parseRollbackFenceResurrectionReintegrationSealEpoch(digest string) (int64, bool) {
	const (
		resurrectionReintegrationSealEpochKey     = "rollback-fence-resurrection-reintegration-seal-epoch="
		lateResurrectionReintegrationSealEpochKey = "rollback-fence-late-resurrection-reintegration-seal-epoch="
		postQuarantineReintegrationSealEpochKey   = "rollback-fence-post-late-resurrection-reintegration-seal-epoch="
		postMarkerReintegrationSealEpochKey       = "rollback-fence-post-marker-expiry-late-resurrection-reintegration-seal-epoch="
		postReintegrationSealEpochKey             = "rollback-fence-post-reintegration-seal-epoch="
	)

	var (
		resurrectionReintegrationSealEpoch int64
		seen                               bool
	)

	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		var value string
		switch {
		case strings.HasPrefix(token, resurrectionReintegrationSealEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, resurrectionReintegrationSealEpochKey))
		case strings.HasPrefix(token, lateResurrectionReintegrationSealEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, lateResurrectionReintegrationSealEpochKey))
		case strings.HasPrefix(token, postQuarantineReintegrationSealEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postQuarantineReintegrationSealEpochKey))
		case strings.HasPrefix(token, postMarkerReintegrationSealEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postMarkerReintegrationSealEpochKey))
		case strings.HasPrefix(token, postReintegrationSealEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postReintegrationSealEpochKey))
		default:
			continue
		}
		if value == "" {
			return 0, false
		}
		parsedEpoch, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsedEpoch < 0 {
			return 0, false
		}
		if !seen {
			resurrectionReintegrationSealEpoch = parsedEpoch
			seen = true
			continue
		}
		if resurrectionReintegrationSealEpoch != parsedEpoch {
			return 0, false
		}
	}

	if !seen {
		return 0, false
	}
	return resurrectionReintegrationSealEpoch, true
}

func parseRollbackFenceResurrectionReintegrationSealDriftEpoch(digest string) (int64, bool) {
	const (
		resurrectionReintegrationSealDriftEpochKey     = "rollback-fence-resurrection-reintegration-seal-drift-epoch="
		lateResurrectionReintegrationSealDriftEpochKey = "rollback-fence-late-resurrection-reintegration-seal-drift-epoch="
		postQuarantineReintegrationSealDriftEpochKey   = "rollback-fence-post-late-resurrection-reintegration-seal-drift-epoch="
		postMarkerReintegrationSealDriftEpochKey       = "rollback-fence-post-marker-expiry-late-resurrection-reintegration-seal-drift-epoch="
		postReintegrationSealDriftEpochKey             = "rollback-fence-post-reintegration-seal-drift-epoch="
	)

	var (
		resurrectionReintegrationSealDriftEpoch int64
		seen                                    bool
	)

	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		var value string
		switch {
		case strings.HasPrefix(token, resurrectionReintegrationSealDriftEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, resurrectionReintegrationSealDriftEpochKey))
		case strings.HasPrefix(token, lateResurrectionReintegrationSealDriftEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, lateResurrectionReintegrationSealDriftEpochKey))
		case strings.HasPrefix(token, postQuarantineReintegrationSealDriftEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postQuarantineReintegrationSealDriftEpochKey))
		case strings.HasPrefix(token, postMarkerReintegrationSealDriftEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postMarkerReintegrationSealDriftEpochKey))
		case strings.HasPrefix(token, postReintegrationSealDriftEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postReintegrationSealDriftEpochKey))
		default:
			continue
		}
		if value == "" {
			return 0, false
		}
		parsedEpoch, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsedEpoch < 0 {
			return 0, false
		}
		if !seen {
			resurrectionReintegrationSealDriftEpoch = parsedEpoch
			seen = true
			continue
		}
		if resurrectionReintegrationSealDriftEpoch != parsedEpoch {
			return 0, false
		}
	}

	if !seen {
		return 0, false
	}
	return resurrectionReintegrationSealDriftEpoch, true
}

func parseRollbackFenceResurrectionReintegrationSealDriftReanchorEpoch(digest string) (int64, bool) {
	const (
		resurrectionReintegrationSealDriftReanchorEpochKey     = "rollback-fence-resurrection-reintegration-seal-drift-reanchor-epoch="
		lateResurrectionReintegrationSealDriftReanchorEpochKey = "rollback-fence-late-resurrection-reintegration-seal-drift-reanchor-epoch="
		postQuarantineReintegrationSealDriftReanchorEpochKey   = "rollback-fence-post-late-resurrection-reintegration-seal-drift-reanchor-epoch="
		postMarkerReintegrationSealDriftReanchorEpochKey       = "rollback-fence-post-marker-expiry-late-resurrection-reintegration-seal-drift-reanchor-epoch="
		postReintegrationSealDriftReanchorEpochKey             = "rollback-fence-post-reintegration-seal-drift-reanchor-epoch="
	)

	var (
		resurrectionReintegrationSealDriftReanchorEpoch int64
		seen                                            bool
	)

	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		var value string
		switch {
		case strings.HasPrefix(token, resurrectionReintegrationSealDriftReanchorEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, resurrectionReintegrationSealDriftReanchorEpochKey))
		case strings.HasPrefix(token, lateResurrectionReintegrationSealDriftReanchorEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, lateResurrectionReintegrationSealDriftReanchorEpochKey))
		case strings.HasPrefix(token, postQuarantineReintegrationSealDriftReanchorEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postQuarantineReintegrationSealDriftReanchorEpochKey))
		case strings.HasPrefix(token, postMarkerReintegrationSealDriftReanchorEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postMarkerReintegrationSealDriftReanchorEpochKey))
		case strings.HasPrefix(token, postReintegrationSealDriftReanchorEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postReintegrationSealDriftReanchorEpochKey))
		default:
			continue
		}
		if value == "" {
			return 0, false
		}
		parsedEpoch, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsedEpoch < 0 {
			return 0, false
		}
		if !seen {
			resurrectionReintegrationSealDriftReanchorEpoch = parsedEpoch
			seen = true
			continue
		}
		if resurrectionReintegrationSealDriftReanchorEpoch != parsedEpoch {
			return 0, false
		}
	}

	if !seen {
		return 0, false
	}
	return resurrectionReintegrationSealDriftReanchorEpoch, true
}

func parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionEpoch(digest string) (int64, bool) {
	const (
		resurrectionReintegrationSealDriftReanchorCompactionEpochKey     = "rollback-fence-resurrection-reintegration-seal-drift-reanchor-compaction-epoch="
		lateResurrectionReintegrationSealDriftReanchorCompactionEpochKey = "rollback-fence-late-resurrection-reintegration-seal-drift-reanchor-compaction-epoch="
		postQuarantineReintegrationSealDriftReanchorCompactionEpochKey   = "rollback-fence-post-late-resurrection-reintegration-seal-drift-reanchor-compaction-epoch="
		postMarkerReintegrationSealDriftReanchorCompactionEpochKey       = "rollback-fence-post-marker-expiry-late-resurrection-reintegration-seal-drift-reanchor-compaction-epoch="
		postReintegrationSealDriftReanchorCompactionEpochKey             = "rollback-fence-post-reintegration-seal-drift-reanchor-compaction-epoch="
	)

	var (
		resurrectionReintegrationSealDriftReanchorCompactionEpoch int64
		seen                                                      bool
	)

	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		var value string
		switch {
		case strings.HasPrefix(token, resurrectionReintegrationSealDriftReanchorCompactionEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, resurrectionReintegrationSealDriftReanchorCompactionEpochKey))
		case strings.HasPrefix(token, lateResurrectionReintegrationSealDriftReanchorCompactionEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, lateResurrectionReintegrationSealDriftReanchorCompactionEpochKey))
		case strings.HasPrefix(token, postQuarantineReintegrationSealDriftReanchorCompactionEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postQuarantineReintegrationSealDriftReanchorCompactionEpochKey))
		case strings.HasPrefix(token, postMarkerReintegrationSealDriftReanchorCompactionEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postMarkerReintegrationSealDriftReanchorCompactionEpochKey))
		case strings.HasPrefix(token, postReintegrationSealDriftReanchorCompactionEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postReintegrationSealDriftReanchorCompactionEpochKey))
		default:
			continue
		}
		if value == "" {
			return 0, false
		}
		parsedEpoch, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsedEpoch < 0 {
			return 0, false
		}
		if !seen {
			resurrectionReintegrationSealDriftReanchorCompactionEpoch = parsedEpoch
			seen = true
			continue
		}
		if resurrectionReintegrationSealDriftReanchorCompactionEpoch != parsedEpoch {
			return 0, false
		}
	}

	if !seen {
		return 0, false
	}
	return resurrectionReintegrationSealDriftReanchorCompactionEpoch, true
}

func parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryEpoch(digest string) (int64, bool) {
	const (
		resurrectionReintegrationSealDriftReanchorCompactionExpiryEpochKey     = "rollback-fence-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-epoch="
		lateResurrectionReintegrationSealDriftReanchorCompactionExpiryEpochKey = "rollback-fence-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-epoch="
		postQuarantineReintegrationSealDriftReanchorCompactionExpiryEpochKey   = "rollback-fence-post-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-epoch="
		postMarkerReintegrationSealDriftReanchorCompactionExpiryEpochKey       = "rollback-fence-post-marker-expiry-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-epoch="
		postReintegrationSealDriftReanchorCompactionExpiryEpochKey             = "rollback-fence-post-reintegration-seal-drift-reanchor-compaction-expiry-epoch="
	)

	var (
		resurrectionReintegrationSealDriftReanchorCompactionExpiryEpoch int64
		seen                                                            bool
	)

	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		var value string
		switch {
		case strings.HasPrefix(token, resurrectionReintegrationSealDriftReanchorCompactionExpiryEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, resurrectionReintegrationSealDriftReanchorCompactionExpiryEpochKey))
		case strings.HasPrefix(token, lateResurrectionReintegrationSealDriftReanchorCompactionExpiryEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, lateResurrectionReintegrationSealDriftReanchorCompactionExpiryEpochKey))
		case strings.HasPrefix(token, postQuarantineReintegrationSealDriftReanchorCompactionExpiryEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postQuarantineReintegrationSealDriftReanchorCompactionExpiryEpochKey))
		case strings.HasPrefix(token, postMarkerReintegrationSealDriftReanchorCompactionExpiryEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postMarkerReintegrationSealDriftReanchorCompactionExpiryEpochKey))
		case strings.HasPrefix(token, postReintegrationSealDriftReanchorCompactionExpiryEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postReintegrationSealDriftReanchorCompactionExpiryEpochKey))
		default:
			continue
		}
		if value == "" {
			return 0, false
		}
		parsedEpoch, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsedEpoch < 0 {
			return 0, false
		}
		if !seen {
			resurrectionReintegrationSealDriftReanchorCompactionExpiryEpoch = parsedEpoch
			seen = true
			continue
		}
		if resurrectionReintegrationSealDriftReanchorCompactionExpiryEpoch != parsedEpoch {
			return 0, false
		}
	}

	if !seen {
		return 0, false
	}
	return resurrectionReintegrationSealDriftReanchorCompactionExpiryEpoch, true
}

func parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch(digest string) (int64, bool) {
	const (
		resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey     = "rollback-fence-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-epoch="
		lateResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey = "rollback-fence-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-epoch="
		postQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey   = "rollback-fence-post-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-epoch="
		postMarkerReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey       = "rollback-fence-post-marker-expiry-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-epoch="
		postReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey             = "rollback-fence-post-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-epoch="
	)

	var (
		resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch int64
		seen                                                                      bool
	)

	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		var value string
		switch {
		case strings.HasPrefix(token, resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey))
		case strings.HasPrefix(token, lateResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, lateResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey))
		case strings.HasPrefix(token, postQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey))
		case strings.HasPrefix(token, postMarkerReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postMarkerReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey))
		case strings.HasPrefix(token, postReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey))
		default:
			continue
		}
		if value == "" {
			return 0, false
		}
		parsedEpoch, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsedEpoch < 0 {
			return 0, false
		}
		if !seen {
			resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch = parsedEpoch
			seen = true
			continue
		}
		if resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch != parsedEpoch {
			return 0, false
		}
	}

	if !seen {
		return 0, false
	}
	return resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch, true
}

func parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch(digest string) (int64, bool) {
	const (
		resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey     = "rollback-fence-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-epoch="
		lateResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey = "rollback-fence-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-epoch="
		postQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey   = "rollback-fence-post-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-epoch="
		postMarkerReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey       = "rollback-fence-post-marker-expiry-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-epoch="
		postReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey             = "rollback-fence-post-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-epoch="
	)

	var (
		resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch int64
		seen                                                                                   bool
	)

	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		var value string
		switch {
		case strings.HasPrefix(token, resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey))
		case strings.HasPrefix(token, lateResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, lateResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey))
		case strings.HasPrefix(token, postQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey))
		case strings.HasPrefix(token, postMarkerReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postMarkerReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey))
		case strings.HasPrefix(token, postReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey))
		default:
			continue
		}
		if value == "" {
			return 0, false
		}
		parsedEpoch, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsedEpoch < 0 {
			return 0, false
		}
		if !seen {
			resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch = parsedEpoch
			seen = true
			continue
		}
		if resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch != parsedEpoch {
			return 0, false
		}
	}

	if !seen {
		return 0, false
	}
	return resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch, true
}

func parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpoch(digest string) (int64, bool) {
	const (
		resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpochKey     = "rollback-fence-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-epoch="
		lateResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpochKey = "rollback-fence-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-epoch="
		postQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpochKey   = "rollback-fence-post-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-epoch="
		postMarkerReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpochKey       = "rollback-fence-post-marker-expiry-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-epoch="
		postReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpochKey             = "rollback-fence-post-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-epoch="
	)

	var (
		resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpoch int64
		seen                                                                                       bool
	)

	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		var value string
		switch {
		case strings.HasPrefix(token, resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpochKey))
		case strings.HasPrefix(token, lateResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, lateResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpochKey))
		case strings.HasPrefix(token, postQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpochKey))
		case strings.HasPrefix(token, postMarkerReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postMarkerReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpochKey))
		case strings.HasPrefix(token, postReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpochKey))
		default:
			continue
		}
		if value == "" {
			return 0, false
		}
		parsedEpoch, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsedEpoch < 0 {
			return 0, false
		}
		if !seen {
			resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpoch = parsedEpoch
			seen = true
			continue
		}
		if resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpoch != parsedEpoch {
			return 0, false
		}
	}

	if !seen {
		return 0, false
	}
	return resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpoch, true
}

func parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpoch(digest string) (int64, bool) {
	const (
		resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpochKey     = "rollback-fence-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-epoch="
		lateResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpochKey = "rollback-fence-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-epoch="
		postQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpochKey   = "rollback-fence-post-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-epoch="
		postMarkerReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpochKey       = "rollback-fence-post-marker-expiry-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-epoch="
		postReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpochKey             = "rollback-fence-post-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-epoch="
	)

	var (
		resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpoch int64
		seen                                                                                            bool
	)

	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		var value string
		switch {
		case strings.HasPrefix(token, resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpochKey))
		case strings.HasPrefix(token, lateResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, lateResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpochKey))
		case strings.HasPrefix(token, postQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpochKey))
		case strings.HasPrefix(token, postMarkerReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postMarkerReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpochKey))
		case strings.HasPrefix(token, postReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpochKey))
		default:
			continue
		}
		if value == "" {
			return 0, false
		}
		parsedEpoch, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsedEpoch < 0 {
			return 0, false
		}
		if !seen {
			resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpoch = parsedEpoch
			seen = true
			continue
		}
		if resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpoch != parsedEpoch {
			return 0, false
		}
	}

	if !seen {
		return 0, false
	}
	return resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpoch, true
}

func parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpoch(digest string) (int64, bool) {
	const (
		resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpochKey     = "rollback-fence-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-epoch="
		lateResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpochKey = "rollback-fence-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-epoch="
		postQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpochKey   = "rollback-fence-post-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-epoch="
		postMarkerReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpochKey       = "rollback-fence-post-marker-expiry-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-epoch="
		postReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpochKey             = "rollback-fence-post-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-epoch="
	)

	var (
		resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpoch int64
		seen                                                                                                    bool
	)

	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		var value string
		switch {
		case strings.HasPrefix(token, resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpochKey))
		case strings.HasPrefix(token, lateResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, lateResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpochKey))
		case strings.HasPrefix(token, postQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpochKey))
		case strings.HasPrefix(token, postMarkerReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postMarkerReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpochKey))
		case strings.HasPrefix(token, postReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpochKey))
		default:
			continue
		}
		if value == "" {
			return 0, false
		}
		parsedEpoch, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsedEpoch < 0 {
			return 0, false
		}
		if !seen {
			resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpoch = parsedEpoch
			seen = true
			continue
		}
		if resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpoch != parsedEpoch {
			return 0, false
		}
	}

	if !seen {
		return 0, false
	}
	return resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpoch, true
}

func parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpoch(digest string) (int64, bool) {
	const (
		resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpochKey     = "rollback-fence-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-epoch="
		lateResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpochKey = "rollback-fence-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-epoch="
		postQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpochKey   = "rollback-fence-post-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-epoch="
		postMarkerReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpochKey       = "rollback-fence-post-marker-expiry-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-epoch="
		postReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpochKey             = "rollback-fence-post-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-epoch="
	)

	var (
		resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpoch int64
		seen                                                                                                              bool
	)

	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		var value string
		switch {
		case strings.HasPrefix(token, resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpochKey))
		case strings.HasPrefix(token, lateResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, lateResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpochKey))
		case strings.HasPrefix(token, postQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpochKey))
		case strings.HasPrefix(token, postMarkerReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postMarkerReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpochKey))
		case strings.HasPrefix(token, postReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpochKey))
		default:
			continue
		}
		if value == "" {
			return 0, false
		}
		parsedEpoch, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsedEpoch < 0 {
			return 0, false
		}
		if !seen {
			resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpoch = parsedEpoch
			seen = true
			continue
		}
		if resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpoch != parsedEpoch {
			return 0, false
		}
	}

	if !seen {
		return 0, false
	}
	return resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpoch, true
}

func parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpoch(digest string) (int64, bool) {
	const (
		resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpochKey     = "rollback-fence-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry-epoch="
		lateResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpochKey = "rollback-fence-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry-epoch="
		postQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpochKey   = "rollback-fence-post-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry-epoch="
		postMarkerReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpochKey       = "rollback-fence-post-marker-expiry-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry-epoch="
		postReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpochKey             = "rollback-fence-post-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry-epoch="
	)

	var (
		resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpoch int64
		seen                                                                                                                    bool
	)

	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		var value string
		switch {
		case strings.HasPrefix(token, resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpochKey))
		case strings.HasPrefix(token, lateResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, lateResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpochKey))
		case strings.HasPrefix(token, postQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpochKey))
		case strings.HasPrefix(token, postMarkerReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postMarkerReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpochKey))
		case strings.HasPrefix(token, postReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpochKey))
		default:
			continue
		}
		if value == "" {
			return 0, false
		}
		parsedEpoch, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsedEpoch < 0 {
			return 0, false
		}
		if !seen {
			resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpoch = parsedEpoch
			seen = true
			continue
		}
		if resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpoch != parsedEpoch {
			return 0, false
		}
	}

	if !seen {
		return 0, false
	}
	return resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpoch, true
}

func parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch(digest string) (int64, bool) {
	const (
		resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey     = "rollback-fence-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-epoch="
		lateResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey = "rollback-fence-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-epoch="
		postQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey   = "rollback-fence-post-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-epoch="
		postMarkerReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey       = "rollback-fence-post-marker-expiry-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-epoch="
		postReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey             = "rollback-fence-post-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-epoch="
	)

	var (
		resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch int64
		seen                                                                                                                              bool
	)

	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		var value string
		switch {
		case strings.HasPrefix(token, resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey))
		case strings.HasPrefix(token, lateResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, lateResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey))
		case strings.HasPrefix(token, postQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey))
		case strings.HasPrefix(token, postMarkerReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postMarkerReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey))
		case strings.HasPrefix(token, postReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey))
		default:
			continue
		}
		if value == "" {
			return 0, false
		}
		parsedEpoch, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsedEpoch < 0 {
			return 0, false
		}
		if !seen {
			resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch = parsedEpoch
			seen = true
			continue
		}
		if resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch != parsedEpoch {
			return 0, false
		}
	}

	if !seen {
		return 0, false
	}
	return resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch, true
}

func parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch(digest string) (int64, bool) {
	const (
		resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey     = "rollback-fence-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-epoch="
		lateResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey = "rollback-fence-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-epoch="
		postQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey   = "rollback-fence-post-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-epoch="
		postMarkerReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey       = "rollback-fence-post-marker-expiry-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-epoch="
		postReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey             = "rollback-fence-post-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-epoch="
	)

	var (
		resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch int64
		seen                                                                                                                                           bool
	)

	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		var value string
		switch {
		case strings.HasPrefix(token, resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey))
		case strings.HasPrefix(token, lateResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, lateResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey))
		case strings.HasPrefix(token, postQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey))
		case strings.HasPrefix(token, postMarkerReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postMarkerReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey))
		case strings.HasPrefix(token, postReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey))
		default:
			continue
		}
		if value == "" {
			return 0, false
		}
		parsedEpoch, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsedEpoch < 0 {
			return 0, false
		}
		if !seen {
			resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch = parsedEpoch
			seen = true
			continue
		}
		if resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch != parsedEpoch {
			return 0, false
		}
	}

	if !seen {
		return 0, false
	}
	return resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch, true
}
