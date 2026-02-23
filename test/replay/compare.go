package main

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
)

// CompareResult holds the outcome of comparing replayed events against DB events.
type CompareResult struct {
	Matching  []string        `json:"matching"`
	Missing   []string        `json:"missing"`   // in replay but not in DB
	Extra     []string        `json:"extra"`      // in DB but not in replay
	Divergent []DivergentEvent `json:"divergent"` // event_id matches but fields differ
}

// DivergentEvent records a field-level mismatch between replay and DB.
type DivergentEvent struct {
	EventID     string `json:"event_id"`
	Field       string `json:"field"`
	ReplayValue string `json:"replay_value"`
	DBValue     string `json:"db_value"`
}

// HasMismatch returns true if there are any missing, extra, or divergent events.
func (r *CompareResult) HasMismatch() bool {
	return len(r.Missing) > 0 || len(r.Extra) > 0 || len(r.Divergent) > 0
}

// compareEvents compares replayed NormalizedBatch transactions against DB BalanceEvents.
// The comparison is keyed on event_id.
func compareEvents(replayBatches []event.NormalizedTransaction, dbEvents []model.BalanceEvent) CompareResult {
	// Build replay event map: event_id → normalized balance event
	type replayEntry struct {
		ActivityType        string
		Delta               string
		ActorAddress        string
		AssetID             string
		CounterpartyAddress string
		ContractAddress     string
	}
	replayMap := make(map[string]replayEntry)
	for _, tx := range replayBatches {
		for _, be := range tx.BalanceEvents {
			if be.EventID == "" {
				continue
			}
			replayMap[be.EventID] = replayEntry{
				ActivityType:        string(be.ActivityType),
				Delta:               be.Delta,
				ActorAddress:        be.ActorAddress,
				AssetID:             be.AssetID,
				CounterpartyAddress: be.CounterpartyAddress,
				ContractAddress:     be.ContractAddress,
			}
		}
	}

	// Build DB event map
	type dbEntry struct {
		ActivityType        string
		Delta               string
		ActorAddress        string
		AssetID             string
		CounterpartyAddress string
		ContractAddress     string // mapped from ProgramID in DB
	}
	dbMap := make(map[string]dbEntry, len(dbEvents))
	for _, ev := range dbEvents {
		dbMap[ev.EventID] = dbEntry{
			ActivityType:        string(ev.ActivityType),
			Delta:               ev.Delta,
			ActorAddress:        ev.ActorAddress,
			AssetID:             ev.AssetID,
			CounterpartyAddress: ev.CounterpartyAddress,
			ContractAddress:     ev.ProgramID,
		}
	}

	var result CompareResult

	// Check replay events against DB
	for eid, re := range replayMap {
		de, found := dbMap[eid]
		if !found {
			result.Missing = append(result.Missing, eid)
			continue
		}
		// Check field divergence
		checkField := func(field, replayVal, dbVal string) {
			if replayVal != dbVal {
				result.Divergent = append(result.Divergent, DivergentEvent{
					EventID:     eid,
					Field:       field,
					ReplayValue: replayVal,
					DBValue:     dbVal,
				})
			}
		}
		checkField("activity_type", re.ActivityType, de.ActivityType)
		checkField("delta", re.Delta, de.Delta)
		checkField("actor_address", re.ActorAddress, de.ActorAddress)
		checkField("asset_id", re.AssetID, de.AssetID)
		checkField("counterparty_address", re.CounterpartyAddress, de.CounterpartyAddress)
		checkField("contract_address", re.ContractAddress, de.ContractAddress)
		if re.ActivityType == de.ActivityType && re.Delta == de.Delta &&
			re.ActorAddress == de.ActorAddress && re.AssetID == de.AssetID &&
			re.CounterpartyAddress == de.CounterpartyAddress &&
			re.ContractAddress == de.ContractAddress {
			result.Matching = append(result.Matching, eid)
		}
	}

	// Check for extra events in DB not in replay
	for eid := range dbMap {
		if _, found := replayMap[eid]; !found {
			result.Extra = append(result.Extra, eid)
		}
	}

	// Sort for deterministic output
	sort.Strings(result.Matching)
	sort.Strings(result.Missing)
	sort.Strings(result.Extra)
	sort.Slice(result.Divergent, func(i, j int) bool {
		if result.Divergent[i].EventID == result.Divergent[j].EventID {
			return result.Divergent[i].Field < result.Divergent[j].Field
		}
		return result.Divergent[i].EventID < result.Divergent[j].EventID
	})

	return result
}

// printTextReport writes a human-readable report to w.
func printTextReport(w io.Writer, chain, network string, startBlock, endBlock int64, replayCount, dbCount int, result CompareResult) {
	fmt.Fprintln(w, "=== Replay Verification Report ===")
	fmt.Fprintf(w, "Chain: %s / %s\n", chain, network)
	fmt.Fprintf(w, "Block range: %d - %d\n", startBlock, endBlock)
	fmt.Fprintf(w, "Replay events: %d\n", replayCount)
	fmt.Fprintf(w, "DB events: %d\n", dbCount)
	fmt.Fprintf(w, "Matching: %d\n", len(result.Matching))
	fmt.Fprintf(w, "Missing: %d\n", len(result.Missing))
	fmt.Fprintf(w, "Extra: %d\n", len(result.Extra))
	fmt.Fprintf(w, "Divergent: %d\n", len(result.Divergent))

	if len(result.Missing) > 0 {
		fmt.Fprintln(w, "\n--- Missing (in replay but not in DB) ---")
		for _, eid := range result.Missing {
			fmt.Fprintf(w, "  %s\n", eid)
		}
	}
	if len(result.Extra) > 0 {
		fmt.Fprintln(w, "\n--- Extra (in DB but not in replay) ---")
		for _, eid := range result.Extra {
			fmt.Fprintf(w, "  %s\n", eid)
		}
	}
	if len(result.Divergent) > 0 {
		fmt.Fprintln(w, "\n--- Divergent (field mismatches) ---")
		for _, d := range result.Divergent {
			fmt.Fprintf(w, "  %s: %s replay=%q db=%q\n", d.EventID, d.Field, d.ReplayValue, d.DBValue)
		}
	}

	fmt.Fprintln(w)
	if !result.HasMismatch() {
		fmt.Fprintln(w, "Result: MATCH")
	} else {
		fmt.Fprintln(w, "Result: MISMATCH")
	}
}

// printJSONReport writes a JSON report to w.
func printJSONReport(w io.Writer, chain, network string, startBlock, endBlock int64, replayCount, dbCount int, result CompareResult) error {
	report := struct {
		Chain       string        `json:"chain"`
		Network     string        `json:"network"`
		StartBlock  int64         `json:"start_block"`
		EndBlock    int64         `json:"end_block"`
		ReplayCount int           `json:"replay_events"`
		DBCount     int           `json:"db_events"`
		Result      string        `json:"result"`
		Compare     CompareResult `json:"compare"`
	}{
		Chain:       chain,
		Network:     network,
		StartBlock:  startBlock,
		EndBlock:    endBlock,
		ReplayCount: replayCount,
		DBCount:     dbCount,
		Compare:     result,
	}
	if result.HasMismatch() {
		report.Result = "MISMATCH"
	} else {
		report.Result = "MATCH"
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}
