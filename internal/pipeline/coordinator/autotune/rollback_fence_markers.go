package autotune

import (
	"strconv"
	"strings"
)

// epochMarkerDef describes a single rollback-fence epoch marker: its logical
// name and the set of digest key prefixes that carry the epoch value.
type epochMarkerDef struct {
	name string
	keys []string
}

// epochMarkers is the ordered registry of all 22 rollback-fence epoch markers.
// The order matters: it defines the slot indices used by
// rollbackFenceOwnershipOrdering and the dependency chain validated during
// parsing.
var epochMarkers = [...]epochMarkerDef{
	{name: "seal-drift", keys: []string{
		"rollback-fence-seal-drift-epoch=",
		"rollback-fence-steady-seal-drift-epoch=",
		"rollback-fence-post-steady-seal-drift-epoch=",
	}},
	{name: "drift-reanchor", keys: []string{
		"rollback-fence-drift-reanchor-epoch=",
		"rollback-fence-post-drift-reanchor-epoch=",
		"rollback-fence-post-steady-drift-reanchor-epoch=",
	}},
	{name: "reanchor-compaction", keys: []string{
		"rollback-fence-reanchor-compaction-epoch=",
		"rollback-fence-post-reanchor-compaction-epoch=",
		"rollback-fence-post-drift-reanchor-compaction-epoch=",
	}},
	{name: "compaction-expiry", keys: []string{
		"rollback-fence-compaction-expiry-epoch=",
		"rollback-fence-lineage-compaction-expiry-epoch=",
		"rollback-fence-post-lineage-compaction-expiry-epoch=",
	}},
	{name: "resurrection-quarantine", keys: []string{
		"rollback-fence-resurrection-quarantine-epoch=",
		"rollback-fence-late-resurrection-quarantine-epoch=",
		"rollback-fence-post-expiry-late-resurrection-quarantine-epoch=",
		"rollback-fence-post-marker-expiry-late-resurrection-quarantine-epoch=",
	}},
	{name: "resurrection-reintegration", keys: []string{
		"rollback-fence-resurrection-reintegration-epoch=",
		"rollback-fence-late-resurrection-reintegration-epoch=",
		"rollback-fence-post-late-resurrection-quarantine-reintegration-epoch=",
		"rollback-fence-post-marker-expiry-late-resurrection-reintegration-epoch=",
	}},
	{name: "resurrection-reintegration-seal", keys: []string{
		"rollback-fence-resurrection-reintegration-seal-epoch=",
		"rollback-fence-late-resurrection-reintegration-seal-epoch=",
		"rollback-fence-post-late-resurrection-reintegration-seal-epoch=",
		"rollback-fence-post-marker-expiry-late-resurrection-reintegration-seal-epoch=",
		"rollback-fence-post-reintegration-seal-epoch=",
	}},
	{name: "resurrection-reintegration-seal-drift", keys: []string{
		"rollback-fence-resurrection-reintegration-seal-drift-epoch=",
		"rollback-fence-late-resurrection-reintegration-seal-drift-epoch=",
		"rollback-fence-post-late-resurrection-reintegration-seal-drift-epoch=",
		"rollback-fence-post-marker-expiry-late-resurrection-reintegration-seal-drift-epoch=",
		"rollback-fence-post-reintegration-seal-drift-epoch=",
	}},
	{name: "resurrection-reintegration-seal-drift-reanchor", keys: []string{
		"rollback-fence-resurrection-reintegration-seal-drift-reanchor-epoch=",
		"rollback-fence-late-resurrection-reintegration-seal-drift-reanchor-epoch=",
		"rollback-fence-post-late-resurrection-reintegration-seal-drift-reanchor-epoch=",
		"rollback-fence-post-marker-expiry-late-resurrection-reintegration-seal-drift-reanchor-epoch=",
		"rollback-fence-post-reintegration-seal-drift-reanchor-epoch=",
	}},
	{name: "resurrection-reintegration-seal-drift-reanchor-compaction", keys: []string{
		"rollback-fence-resurrection-reintegration-seal-drift-reanchor-compaction-epoch=",
		"rollback-fence-late-resurrection-reintegration-seal-drift-reanchor-compaction-epoch=",
		"rollback-fence-post-late-resurrection-reintegration-seal-drift-reanchor-compaction-epoch=",
		"rollback-fence-post-marker-expiry-late-resurrection-reintegration-seal-drift-reanchor-compaction-epoch=",
		"rollback-fence-post-reintegration-seal-drift-reanchor-compaction-epoch=",
	}},
	{name: "resurrection-reintegration-seal-drift-reanchor-compaction-expiry", keys: []string{
		"rollback-fence-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-epoch=",
		"rollback-fence-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-epoch=",
		"rollback-fence-post-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-epoch=",
		"rollback-fence-post-marker-expiry-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-epoch=",
		"rollback-fence-post-reintegration-seal-drift-reanchor-compaction-expiry-epoch=",
	}},
	{name: "resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine", keys: []string{
		"rollback-fence-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-epoch=",
		"rollback-fence-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-epoch=",
		"rollback-fence-post-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-epoch=",
		"rollback-fence-post-marker-expiry-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-epoch=",
		"rollback-fence-post-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-epoch=",
	}},
	{name: "resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration", keys: []string{
		"rollback-fence-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-epoch=",
		"rollback-fence-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-epoch=",
		"rollback-fence-post-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-epoch=",
		"rollback-fence-post-marker-expiry-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-epoch=",
		"rollback-fence-post-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-epoch=",
	}},
	{name: "resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal", keys: []string{
		"rollback-fence-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-epoch=",
		"rollback-fence-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-epoch=",
		"rollback-fence-post-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-epoch=",
		"rollback-fence-post-marker-expiry-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-epoch=",
		"rollback-fence-post-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-epoch=",
	}},
	{name: "resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift", keys: []string{
		"rollback-fence-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-epoch=",
		"rollback-fence-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-epoch=",
		"rollback-fence-post-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-epoch=",
		"rollback-fence-post-marker-expiry-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-epoch=",
		"rollback-fence-post-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-epoch=",
	}},
	{name: "resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor", keys: []string{
		"rollback-fence-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-epoch=",
		"rollback-fence-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-epoch=",
		"rollback-fence-post-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-epoch=",
		"rollback-fence-post-marker-expiry-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-epoch=",
		"rollback-fence-post-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-epoch=",
	}},
	{name: "resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction", keys: []string{
		"rollback-fence-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-epoch=",
		"rollback-fence-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-epoch=",
		"rollback-fence-post-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-epoch=",
		"rollback-fence-post-marker-expiry-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-epoch=",
		"rollback-fence-post-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-epoch=",
	}},
	{name: "resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry", keys: []string{
		"rollback-fence-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry-epoch=",
		"rollback-fence-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry-epoch=",
		"rollback-fence-post-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry-epoch=",
		"rollback-fence-post-marker-expiry-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry-epoch=",
		"rollback-fence-post-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry-epoch=",
	}},
	{name: "resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry-quarantine", keys: []string{
		"rollback-fence-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-epoch=",
		"rollback-fence-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-epoch=",
		"rollback-fence-post-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-epoch=",
		"rollback-fence-post-marker-expiry-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-epoch=",
		"rollback-fence-post-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-epoch=",
	}},
	{name: "resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration", keys: []string{
		"rollback-fence-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-epoch=",
		"rollback-fence-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-epoch=",
		"rollback-fence-post-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-epoch=",
		"rollback-fence-post-marker-expiry-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-epoch=",
		"rollback-fence-post-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-epoch=",
	}},
	{name: "resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal", keys: []string{
		"rollback-fence-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-epoch=",
		"rollback-fence-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-epoch=",
		"rollback-fence-post-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-epoch=",
		"rollback-fence-post-marker-expiry-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-epoch=",
		"rollback-fence-post-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-epoch=",
	}},
	{name: "resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift", keys: []string{
		"rollback-fence-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-epoch=",
		"rollback-fence-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-epoch=",
		"rollback-fence-post-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-epoch=",
		"rollback-fence-post-marker-expiry-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-epoch=",
		"rollback-fence-post-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-epoch=",
	}},
}

// numEpochMarkers is the count of epoch markers in the registry.
const numEpochMarkers = len(epochMarkers)

// parseEpochMarker parses a single epoch marker from a pipe-delimited digest.
// All matching key prefixes must agree on the same value; a mismatch returns false.
func parseEpochMarker(digest string, def epochMarkerDef) (int64, bool) {
	var (
		epoch int64
		seen  bool
	)
	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		var value string
		for _, key := range def.keys {
			if strings.HasPrefix(token, key) {
				value = strings.TrimSpace(strings.TrimPrefix(token, key))
				break
			}
		}
		if value == "" {
			continue
		}
		parsedEpoch, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsedEpoch < 0 {
			return 0, false
		}
		if !seen {
			epoch = parsedEpoch
			seen = true
			continue
		}
		if epoch != parsedEpoch {
			return 0, false
		}
	}
	if !seen {
		return 0, false
	}
	return epoch, true
}
