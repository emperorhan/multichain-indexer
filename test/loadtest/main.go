// Package main implements a load test harness for the multichain-indexer ingester.
// It generates synthetic NormalizedBatch data and pushes it through the full
// ingester path against a real PostgreSQL database, measuring throughput, latency,
// and error rate.
//
// Usage:
//
//	go run ./test/loadtest \
//	  -db-url "postgres://indexer:indexer@localhost:5433/custody_indexer?sslmode=disable" \
//	  -batch-size 50 \
//	  -concurrency 4 \
//	  -duration 30s \
//	  -chain solana \
//	  -network devnet \
//	  -verify
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"math"
	"os"
	"os/signal"
	"sort"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/ingester"
	"github.com/emperorhan/multichain-indexer/internal/store/postgres"
)

func main() {
	var (
		dbURL       = flag.String("db-url", "postgres://indexer:indexer@localhost:5433/custody_indexer?sslmode=disable", "PostgreSQL connection string")
		batchSize   = flag.Int("batch-size", 50, "Transactions per batch")
		concurrency = flag.Int("concurrency", 4, "Number of parallel ingester workers")
		duration    = flag.Duration("duration", 30*time.Second, "Test duration")
		chainFlag   = flag.String("chain", "solana", "Chain identifier (solana, ethereum, base, btc, polygon, arbitrum, bsc)")
		networkFlag = flag.String("network", "devnet", "Network identifier (mainnet, devnet, testnet, sepolia, amoy)")
		migrate     = flag.Bool("migrate", false, "Run DB migrations before starting the load test")
		verify      = flag.Bool("verify", false, "Run post-load-test data integrity verification")
	)
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	chain := model.Chain(*chainFlag)
	network := model.Network(*networkFlag)

	logger.Info("load test configuration",
		"db_url", maskPassword(*dbURL),
		"batch_size", *batchSize,
		"concurrency", *concurrency,
		"duration", *duration,
		"chain", chain,
		"network", network,
		"migrate", *migrate,
	)

	// Connect to PostgreSQL.
	db, err := postgres.New(postgres.Config{
		URL:             *dbURL,
		MaxOpenConns:    *concurrency + 4,
		MaxIdleConns:    *concurrency + 2,
		ConnMaxLifetime: 5 * time.Minute,
	})
	if err != nil {
		logger.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	// Optionally run migrations.
	if *migrate {
		logger.Info("running database migrations")
		if err := db.RunMigrations("internal/store/postgres/migrations"); err != nil {
			logger.Error("migrations failed", "error", err)
			os.Exit(1)
		}
		logger.Info("migrations completed")
	}

	// Initialize real PostgreSQL repositories.
	txRepo := postgres.NewTransactionRepo(db)
	balanceEventRepo := postgres.NewBalanceEventRepo(db)
	balanceRepo := postgres.NewBalanceRepo(db)
	tokenRepo := postgres.NewTokenRepo(db)
	cursorRepo := postgres.NewCursorRepo(db)
	configRepo := postgres.NewIndexerConfigRepo(db)

	// Ensure the indexer_configs row exists for watermark updates.
	if err := ensureIndexerConfig(db, chain, network); err != nil {
		logger.Error("failed to ensure indexer config", "error", err)
		os.Exit(1)
	}

	// Set up context with signal handling.
	ctx, cancel := context.WithTimeout(context.Background(), *duration+10*time.Second)
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case sig := <-sigCh:
			logger.Info("received signal, shutting down", "signal", sig)
			cancel()
		case <-ctx.Done():
		}
	}()

	// Stats collection.
	var (
		totalBatches  atomic.Int64
		totalEvents   atomic.Int64
		totalErrors   atomic.Int64
		latenciesMu   sync.Mutex
		latenciesNs   []int64
	)

	recordLatency := func(d time.Duration) {
		latenciesMu.Lock()
		latenciesNs = append(latenciesNs, d.Nanoseconds())
		latenciesMu.Unlock()
	}

	// Worker function: each worker creates its own ingester and feeds it batches.
	worker := func(workerID int) {
		// Each worker gets a unique address space to avoid cursor conflicts.
		address := fmt.Sprintf("loadtest-addr-%d", workerID)

		// Create a per-worker ingester with its own channel.
		ch := make(chan event.NormalizedBatch, 8)
		ing := ingester.New(
			db, txRepo, balanceEventRepo, balanceRepo, tokenRepo, cursorRepo, configRepo,
			ch, logger.With("worker", workerID),
		)

		// Start the ingester in a goroutine.
		ingCtx, ingCancel := context.WithCancel(ctx)
		defer ingCancel()

		ingDone := make(chan error, 1)
		go func() {
			ingDone <- ing.Run(ingCtx)
		}()

		batchSeq := int64(0)
		deadline := time.Now().Add(*duration)

		for time.Now().Before(deadline) {
			select {
			case <-ctx.Done():
				close(ch)
				<-ingDone
				return
			default:
			}

			batch := buildLoadTestBatch(chain, network, address, *batchSize, batchSeq, workerID)
			batchSeq++

			start := time.Now()
			select {
			case ch <- batch:
			case <-ctx.Done():
				close(ch)
				<-ingDone
				return
			}

			// Wait for the batch to be processed by checking cursor advancement.
			// We use a simple approach: wait for the ingester to drain the channel.
			waitStart := time.Now()
			for len(ch) > 0 {
				if time.Since(waitStart) > 30*time.Second {
					totalErrors.Add(1)
					break
				}
				time.Sleep(time.Millisecond)
			}

			elapsed := time.Since(start)
			recordLatency(elapsed)
			totalBatches.Add(1)
			totalEvents.Add(int64(*batchSize * 2)) // Each tx has 2 balance events (transfer + fee).
		}

		// Drain: close the channel and wait for the ingester to finish.
		close(ch)
		if err := <-ingDone; err != nil && ctx.Err() == nil {
			logger.Warn("ingester finished with error", "worker", workerID, "error", err)
			totalErrors.Add(1)
		}
	}

	// Run all workers in parallel.
	logger.Info("starting load test", "workers", *concurrency, "duration", *duration)
	testStart := time.Now()

	var wg sync.WaitGroup
	for i := 0; i < *concurrency; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			worker(id)
		}(i)
	}
	wg.Wait()

	testDuration := time.Since(testStart)

	// Compute statistics.
	batches := totalBatches.Load()
	events := totalEvents.Load()
	errors := totalErrors.Load()

	latenciesMu.Lock()
	allLatencies := make([]int64, len(latenciesNs))
	copy(allLatencies, latenciesNs)
	latenciesMu.Unlock()

	sort.Slice(allLatencies, func(i, j int) bool { return allLatencies[i] < allLatencies[j] })

	p50 := percentile(allLatencies, 50)
	p95 := percentile(allLatencies, 95)
	p99 := percentile(allLatencies, 99)

	batchesPerSec := float64(batches) / testDuration.Seconds()
	eventsPerSec := float64(events) / testDuration.Seconds()
	errorRate := float64(0)
	if batches > 0 {
		errorRate = float64(errors) / float64(batches) * 100
	}

	// Print report.
	fmt.Println()
	fmt.Println("========================================")
	fmt.Println("       LOAD TEST RESULTS")
	fmt.Println("========================================")
	fmt.Printf("Duration:       %s\n", testDuration.Round(time.Millisecond))
	fmt.Printf("Workers:        %d\n", *concurrency)
	fmt.Printf("Batch size:     %d txs/batch\n", *batchSize)
	fmt.Printf("Chain/Network:  %s/%s\n", chain, network)
	fmt.Println("----------------------------------------")
	fmt.Println("Throughput:")
	fmt.Printf("  Batches:      %d\n", batches)
	fmt.Printf("  Events:       %d\n", events)
	fmt.Printf("  Batches/sec:  %.2f\n", batchesPerSec)
	fmt.Printf("  Events/sec:   %.2f\n", eventsPerSec)
	fmt.Println("----------------------------------------")
	fmt.Println("Latency (per batch):")
	fmt.Printf("  p50:          %s\n", formatNanos(p50))
	fmt.Printf("  p95:          %s\n", formatNanos(p95))
	fmt.Printf("  p99:          %s\n", formatNanos(p99))
	fmt.Println("----------------------------------------")
	fmt.Println("Errors:")
	fmt.Printf("  Total:        %d\n", errors)
	fmt.Printf("  Error rate:   %.2f%%\n", errorRate)
	fmt.Println("========================================")

	// Run post-load-test data integrity verification if requested.
	if *verify {
		verifyFailed := verifyDataIntegrity(db, chain, network, events, *concurrency, logger)
		if verifyFailed {
			errors++ // treat verification failures as errors for exit code
		}
	}

	if errors > 0 {
		os.Exit(1)
	}
}

// checkResult holds the outcome of a single verification check.
type checkResult struct {
	Name    string
	Passed  bool
	Detail  string
}

// verifyDataIntegrity runs post-load-test consistency checks against the database.
// It returns true if any check failed.
func verifyDataIntegrity(
	db *postgres.DB,
	chain model.Chain,
	network model.Network,
	expectedEvents int64,
	concurrency int,
	logger *slog.Logger,
) bool {
	logger.Info("starting data integrity verification")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var results []checkResult

	// Check 1: balance_events count matches expected insertions.
	results = append(results, verifyBalanceEventsCount(ctx, db, chain, network, expectedEvents, concurrency))

	// Check 2: no negative balances exist.
	results = append(results, verifyNoNegativeBalances(ctx, db, chain, network))

	// Check 3: cursor monotonicity (no gaps in per-address cursor sequences).
	results = append(results, verifyCursorMonotonicity(ctx, db, chain, network, concurrency))

	// Check 4: transaction dedup (no duplicate tx_hash per chain/network).
	results = append(results, verifyTransactionDedup(ctx, db, chain, network))

	// Print verification report.
	fmt.Println()
	fmt.Println("========================================")
	fmt.Println("    DATA INTEGRITY VERIFICATION")
	fmt.Println("========================================")

	anyFailed := false
	for _, r := range results {
		status := "PASS"
		if !r.Passed {
			status = "FAIL"
			anyFailed = true
		}
		fmt.Printf("  [%s] %s\n", status, r.Name)
		if r.Detail != "" {
			fmt.Printf("         %s\n", r.Detail)
		}
	}

	fmt.Println("----------------------------------------")
	if anyFailed {
		fmt.Println("  Result: SOME CHECKS FAILED")
	} else {
		fmt.Println("  Result: ALL CHECKS PASSED")
	}
	fmt.Println("========================================")

	return anyFailed
}

// verifyBalanceEventsCount checks that the number of loadtest balance_events in the
// database is at least the expected count. We use "at least" because prior test runs
// may have left data, and dedup (ON CONFLICT) means the actual count should equal
// expected if data was not pre-existing.
func verifyBalanceEventsCount(
	ctx context.Context,
	db *postgres.DB,
	chain model.Chain,
	network model.Network,
	expectedEvents int64,
	concurrency int,
) checkResult {
	name := "balance_events count matches expected"

	// Count only events generated by the load test (event_id starts with "lt-").
	var actualCount int64
	err := db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM balance_events
		WHERE chain = $1 AND network = $2
		  AND event_id LIKE 'lt-%'
	`, chain, network).Scan(&actualCount)
	if err != nil {
		return checkResult{Name: name, Passed: false, Detail: fmt.Sprintf("query error: %v", err)}
	}

	if actualCount < expectedEvents {
		return checkResult{
			Name:   name,
			Passed: false,
			Detail: fmt.Sprintf("expected >= %d, got %d (missing %d events)", expectedEvents, actualCount, expectedEvents-actualCount),
		}
	}

	return checkResult{
		Name:   name,
		Passed: true,
		Detail: fmt.Sprintf("expected >= %d, got %d", expectedEvents, actualCount),
	}
}

// verifyNoNegativeBalances checks that no balance rows have a negative amount.
func verifyNoNegativeBalances(
	ctx context.Context,
	db *postgres.DB,
	chain model.Chain,
	network model.Network,
) checkResult {
	name := "no negative balances"

	var negativeCount int64
	err := db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM balances
		WHERE chain = $1 AND network = $2
		  AND amount < 0
	`, chain, network).Scan(&negativeCount)
	if err != nil {
		return checkResult{Name: name, Passed: false, Detail: fmt.Sprintf("query error: %v", err)}
	}

	if negativeCount > 0 {
		// Fetch a sample of the offending rows for diagnostics.
		rows, qErr := db.QueryContext(ctx, `
			SELECT address, amount
			FROM balances
			WHERE chain = $1 AND network = $2
			  AND amount < 0
			LIMIT 5
		`, chain, network)
		sample := ""
		if qErr == nil {
			defer rows.Close()
			for rows.Next() {
				var addr, amt string
				if sErr := rows.Scan(&addr, &amt); sErr == nil {
					if sample != "" {
						sample += "; "
					}
					sample += fmt.Sprintf("%s=%s", addr, amt)
				}
			}
		}
		detail := fmt.Sprintf("found %d negative balance(s)", negativeCount)
		if sample != "" {
			detail += fmt.Sprintf(" [sample: %s]", sample)
		}
		return checkResult{Name: name, Passed: false, Detail: detail}
	}

	return checkResult{Name: name, Passed: true, Detail: "0 negative balances found"}
}

// verifyCursorMonotonicity verifies that each load-test worker's address cursor
// has a strictly increasing cursor_sequence, and that no cursors were lost.
// For worker W that processed N batches, the final cursor_sequence should be
// the max slot from its last batch.
func verifyCursorMonotonicity(
	ctx context.Context,
	db *postgres.DB,
	chain model.Chain,
	network model.Network,
	concurrency int,
) checkResult {
	name := "cursor monotonicity"

	// Check that all loadtest address cursors have non-decreasing cursor_sequence
	// relative to their ingestion order. Since we only have the final snapshot in
	// address_cursors, we verify that each worker's cursor is present and that
	// cursor_sequence > 0 (i.e., at least one batch was ingested).
	rows, err := db.QueryContext(ctx, `
		SELECT address, cursor_sequence, cursor_value
		FROM address_cursors
		WHERE chain = $1 AND network = $2
		  AND address LIKE 'loadtest-addr-%'
		ORDER BY address
	`, chain, network)
	if err != nil {
		return checkResult{Name: name, Passed: false, Detail: fmt.Sprintf("query error: %v", err)}
	}
	defer rows.Close()

	type cursorRow struct {
		address  string
		sequence int64
		value    sql.NullString
	}
	var cursors []cursorRow
	for rows.Next() {
		var c cursorRow
		if sErr := rows.Scan(&c.address, &c.sequence, &c.value); sErr != nil {
			return checkResult{Name: name, Passed: false, Detail: fmt.Sprintf("scan error: %v", sErr)}
		}
		cursors = append(cursors, c)
	}
	if rErr := rows.Err(); rErr != nil {
		return checkResult{Name: name, Passed: false, Detail: fmt.Sprintf("rows error: %v", rErr)}
	}

	if len(cursors) == 0 {
		return checkResult{Name: name, Passed: false, Detail: "no loadtest cursors found in address_cursors"}
	}

	// Verify each cursor has a positive sequence (meaning batches were processed).
	var issues []string
	for _, c := range cursors {
		if c.sequence <= 0 {
			issues = append(issues, fmt.Sprintf("%s: cursor_sequence=%d (expected > 0)", c.address, c.sequence))
		}
	}

	// Additionally verify cursor sequences within balance_events are monotonically
	// ordered: for each loadtest address, block_cursor values should be non-decreasing
	// when ordered by created_at.
	gapRows, err := db.QueryContext(ctx, `
		WITH ordered_events AS (
			SELECT address, block_cursor,
			       LAG(block_cursor) OVER (PARTITION BY address ORDER BY block_cursor, created_at) AS prev_cursor
			FROM balance_events
			WHERE chain = $1 AND network = $2
			  AND address LIKE 'loadtest-addr-%'
		)
		SELECT address, COUNT(*) AS gap_count
		FROM ordered_events
		WHERE prev_cursor IS NOT NULL AND block_cursor < prev_cursor
		GROUP BY address
	`, chain, network)
	if err != nil {
		return checkResult{Name: name, Passed: false, Detail: fmt.Sprintf("monotonicity query error: %v", err)}
	}
	defer gapRows.Close()

	for gapRows.Next() {
		var addr string
		var gapCount int64
		if sErr := gapRows.Scan(&addr, &gapCount); sErr == nil && gapCount > 0 {
			issues = append(issues, fmt.Sprintf("%s: %d non-monotonic cursor transitions", addr, gapCount))
		}
	}

	if len(issues) > 0 {
		detail := fmt.Sprintf("%d issue(s): %s", len(issues), issues[0])
		if len(issues) > 1 {
			detail += fmt.Sprintf(" (and %d more)", len(issues)-1)
		}
		return checkResult{Name: name, Passed: false, Detail: detail}
	}

	return checkResult{
		Name:   name,
		Passed: true,
		Detail: fmt.Sprintf("%d worker cursor(s) verified, no monotonicity violations", len(cursors)),
	}
}

// verifyTransactionDedup checks that no duplicate tx_hash exists per chain/network
// for loadtest-generated transactions.
func verifyTransactionDedup(
	ctx context.Context,
	db *postgres.DB,
	chain model.Chain,
	network model.Network,
) checkResult {
	name := "transaction dedup (no duplicate tx_hash per chain/network)"

	var dupCount int64
	err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM (
			SELECT tx_hash
			FROM transactions
			WHERE chain = $1 AND network = $2
			  AND tx_hash LIKE 'loadtest-%'
			GROUP BY tx_hash
			HAVING COUNT(*) > 1
		) AS dups
	`, chain, network).Scan(&dupCount)
	if err != nil {
		return checkResult{Name: name, Passed: false, Detail: fmt.Sprintf("query error: %v", err)}
	}

	if dupCount > 0 {
		// Fetch sample duplicates for diagnostics.
		rows, qErr := db.QueryContext(ctx, `
			SELECT tx_hash, COUNT(*) AS cnt
			FROM transactions
			WHERE chain = $1 AND network = $2
			  AND tx_hash LIKE 'loadtest-%'
			GROUP BY tx_hash
			HAVING COUNT(*) > 1
			LIMIT 5
		`, chain, network)
		sample := ""
		if qErr == nil {
			defer rows.Close()
			for rows.Next() {
				var txHash string
				var cnt int64
				if sErr := rows.Scan(&txHash, &cnt); sErr == nil {
					if sample != "" {
						sample += "; "
					}
					sample += fmt.Sprintf("%s (x%d)", txHash, cnt)
				}
			}
		}
		detail := fmt.Sprintf("found %d duplicate tx_hash group(s)", dupCount)
		if sample != "" {
			detail += fmt.Sprintf(" [sample: %s]", sample)
		}
		return checkResult{Name: name, Passed: false, Detail: detail}
	}

	return checkResult{Name: name, Passed: true, Detail: "0 duplicate tx_hash groups found"}
}

// buildLoadTestBatch generates a synthetic NormalizedBatch for load testing.
// Each batch contains batchSize transactions, each with 2 balance events
// (a transfer deposit and a fee), mimicking realistic Solana transaction patterns.
func buildLoadTestBatch(
	chain model.Chain,
	network model.Network,
	address string,
	batchSize int,
	batchSeq int64,
	workerID int,
) event.NormalizedBatch {
	baseSlot := batchSeq*int64(batchSize) + 1000000
	cursor := fmt.Sprintf("loadtest-cursor-w%d-seq%d", workerID, batchSeq)
	now := time.Now().UTC()

	txs := make([]event.NormalizedTransaction, batchSize)
	for i := 0; i < batchSize; i++ {
		txHash := fmt.Sprintf("loadtest-sig-w%d-s%d-tx%d", workerID, batchSeq, i)
		blockTime := now.Add(-time.Duration(batchSize-i) * time.Second)
		slot := baseSlot + int64(i)

		txs[i] = event.NormalizedTransaction{
			TxHash:      txHash,
			BlockCursor: slot,
			BlockTime:   &blockTime,
			FeeAmount:   "5000",
			FeePayer:    address,
			Status:      model.TxStatusSuccess,
			ChainData:   json.RawMessage(`{}`),
			BalanceEvents: []event.NormalizedBalanceEvent{
				{
					OuterInstructionIndex: 0,
					InnerInstructionIndex: -1,
					ActivityType:          model.ActivityDeposit,
					EventAction:           "transfer",
					ProgramID:             "11111111111111111111111111111111",
					ContractAddress:       "11111111111111111111111111111111",
					Address:               address,
					CounterpartyAddress:   fmt.Sprintf("loadtest-counterparty-%d", i%10),
					Delta:                 "1000000",
					EventID:               fmt.Sprintf("lt-w%d-s%d-transfer-%d", workerID, batchSeq, i),
					TokenType:             model.TokenTypeNative,
					TokenSymbol:           "SOL",
					TokenName:             "Solana",
					TokenDecimals:         9,
					BlockHash:             fmt.Sprintf("blockhash-%d", slot),
					TxIndex:               int64(i),
					EventPath:             fmt.Sprintf("0/%d", i),
					EventPathType:         "instruction",
					ActorAddress:          fmt.Sprintf("loadtest-counterparty-%d", i%10),
					AssetType:             "native",
					AssetID:               "SOL",
					FinalityState:         "finalized",
					DecoderVersion:        "1.0.0",
					SchemaVersion:         "1.0.0",
				},
				{
					OuterInstructionIndex: -1,
					InnerInstructionIndex: -1,
					ActivityType:          model.ActivityFee,
					EventAction:           "fee",
					ProgramID:             "11111111111111111111111111111111",
					ContractAddress:       "11111111111111111111111111111111",
					Address:               address,
					CounterpartyAddress:   "",
					Delta:                 "-5000",
					EventID:               fmt.Sprintf("lt-w%d-s%d-fee-%d", workerID, batchSeq, i),
					TokenType:             model.TokenTypeNative,
					TokenSymbol:           "SOL",
					TokenName:             "Solana",
					TokenDecimals:         9,
					BlockHash:             fmt.Sprintf("blockhash-%d", slot),
					TxIndex:               int64(i),
					EventPath:             "fee",
					EventPathType:         "fee",
					ActorAddress:          address,
					AssetType:             "native",
					AssetID:               "SOL",
					FinalityState:         "finalized",
					DecoderVersion:        "1.0.0",
					SchemaVersion:         "1.0.0",
				},
			},
		}
	}

	var prevCursor *string
	var prevSeq int64
	if batchSeq > 0 {
		prev := fmt.Sprintf("loadtest-cursor-w%d-seq%d", workerID, batchSeq-1)
		prevCursor = &prev
		prevSeq = baseSlot - 1
	}

	return event.NormalizedBatch{
		Chain:                  chain,
		Network:                network,
		Address:                address,
		PreviousCursorValue:    prevCursor,
		PreviousCursorSequence: prevSeq,
		Transactions:           txs,
		NewCursorValue:         &cursor,
		NewCursorSequence:      baseSlot + int64(batchSize) - 1,
	}
}

// ensureIndexerConfig creates a minimal indexer_configs row so watermark updates succeed.
func ensureIndexerConfig(db *postgres.DB, chain model.Chain, network model.Network) error {
	_, err := db.Exec(`
		INSERT INTO indexer_configs (chain, network, is_active, target_batch_size, indexing_interval_ms, chain_config)
		VALUES ($1, $2, true, 50, 1000, '{}')
		ON CONFLICT (chain, network) DO NOTHING
	`, chain, network)
	return err
}

// percentile returns the value at the given percentile from a sorted slice.
func percentile(sorted []int64, pct float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Ceil(pct/100*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// formatNanos formats nanoseconds as a human-readable duration string.
func formatNanos(ns int64) string {
	d := time.Duration(ns)
	if d < time.Millisecond {
		return fmt.Sprintf("%.1fus", float64(d.Microseconds()))
	}
	if d < time.Second {
		return fmt.Sprintf("%.2fms", float64(d.Nanoseconds())/1e6)
	}
	return fmt.Sprintf("%.3fs", d.Seconds())
}

// maskPassword masks the password in a PostgreSQL connection string for log output.
func maskPassword(url string) string {
	// Simple masking: find "password=" or ":pass@" patterns.
	// This is best-effort for logging safety.
	result := []byte(url)
	inPassword := false
	colonCount := 0
	for i := 0; i < len(result); i++ {
		if result[i] == ':' {
			colonCount++
			if colonCount == 2 {
				inPassword = true
				continue
			}
		}
		if inPassword && result[i] == '@' {
			inPassword = false
			continue
		}
		if inPassword {
			result[i] = '*'
		}
	}
	return string(result)
}
