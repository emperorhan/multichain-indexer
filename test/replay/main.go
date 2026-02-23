// Package main implements a history replay verifier for the multichain-indexer.
// It re-scans a block range through the same adapter → sidecar → canonicalization
// path as the live pipeline, then compares the resulting event_ids against the
// balance_events stored in the database.
//
// Usage:
//
//	go run ./test/replay \
//	  -chain ethereum -network mainnet \
//	  -start-block 19999800 -end-block 19999810 \
//	  -rpc-url https://eth.example.com \
//	  -db-url postgres://indexer:indexer@localhost:5433/custody_indexer?sslmode=disable \
//	  -sidecar-addr localhost:50051 \
//	  -addresses 0xABC...,0xDEF...
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	sidecarv1 "github.com/emperorhan/multichain-indexer/pkg/generated/sidecar/v1"

	chainpkg "github.com/emperorhan/multichain-indexer/internal/chain"
	"github.com/emperorhan/multichain-indexer/internal/chain/base"
	"github.com/emperorhan/multichain-indexer/internal/chain/btc"
	"github.com/emperorhan/multichain-indexer/internal/chain/solana"
	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/normalizer"
	"github.com/emperorhan/multichain-indexer/internal/store/postgres"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	exitMatch   = 0
	exitMismatch = 1
	exitFatal   = 2
)

func main() {
	var (
		chainFlag     = flag.String("chain", "", "Chain name (solana, ethereum, base, btc, polygon, arbitrum, bsc)")
		networkFlag   = flag.String("network", "", "Network (mainnet, devnet, testnet, sepolia, ...)")
		startBlock    = flag.Int64("start-block", 0, "Start block (inclusive)")
		endBlock      = flag.Int64("end-block", 0, "End block (inclusive)")
		rpcURL        = flag.String("rpc-url", "", "RPC endpoint URL")
		dbURL         = flag.String("db-url", "", "PostgreSQL connection string")
		sidecarAddr   = flag.String("sidecar-addr", "", "Sidecar gRPC address (host:port)")
		addressesFlag = flag.String("addresses", "", "Comma-separated watched addresses")
		outputFlag    = flag.String("output", "text", "Output format (text / json)")
	)
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// Validate required flags.
	var missing []string
	if *chainFlag == "" {
		missing = append(missing, "-chain")
	}
	if *networkFlag == "" {
		missing = append(missing, "-network")
	}
	if *startBlock == 0 {
		missing = append(missing, "-start-block")
	}
	if *endBlock == 0 {
		missing = append(missing, "-end-block")
	}
	if *rpcURL == "" {
		missing = append(missing, "-rpc-url")
	}
	if *dbURL == "" {
		missing = append(missing, "-db-url")
	}
	if *sidecarAddr == "" {
		missing = append(missing, "-sidecar-addr")
	}
	if *addressesFlag == "" {
		missing = append(missing, "-addresses")
	}
	if len(missing) > 0 {
		fmt.Fprintf(os.Stderr, "missing required flags: %s\n", strings.Join(missing, ", "))
		flag.Usage()
		os.Exit(exitFatal)
	}
	if *startBlock > *endBlock {
		fmt.Fprintln(os.Stderr, "-start-block must be <= -end-block")
		os.Exit(exitFatal)
	}

	chain := model.Chain(*chainFlag)
	network := model.Network(*networkFlag)
	addresses := strings.Split(*addressesFlag, ",")
	for i := range addresses {
		addresses[i] = strings.TrimSpace(addresses[i])
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	logger.Info("replay verifier starting",
		"chain", chain,
		"network", network,
		"start_block", *startBlock,
		"end_block", *endBlock,
		"addresses", len(addresses),
		"sidecar_addr", *sidecarAddr,
	)

	// 1. Connect DB (read-only pool).
	db, err := postgres.New(postgres.Config{
		URL:             *dbURL,
		MaxOpenConns:    5,
		MaxIdleConns:    3,
		ConnMaxLifetime: 5 * time.Minute,
	})
	if err != nil {
		logger.Error("failed to connect to database", "error", err)
		os.Exit(exitFatal)
	}
	defer db.Close()

	beRepo := postgres.NewBalanceEventRepo(db)

	// 2. Connect sidecar (gRPC).
	const maxMsgSize = 64 * 1024 * 1024 // 64 MB
	conn, err := grpc.NewClient(
		*sidecarAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(maxMsgSize),
			grpc.MaxCallSendMsgSize(maxMsgSize),
		),
	)
	if err != nil {
		logger.Error("failed to dial sidecar", "error", err)
		os.Exit(exitFatal)
	}
	defer conn.Close()

	sidecarClient := sidecarv1.NewChainDecoderClient(conn)

	// 3. Build chain adapter.
	adapter := buildAdapter(chain, *rpcURL, logger)

	// 4. ScanBlocks.
	logger.Info("scanning blocks", "start", *startBlock, "end", *endBlock)
	sigs, err := scanBlocks(ctx, adapter, *startBlock, *endBlock, addresses)
	if err != nil {
		logger.Error("scan blocks failed", "error", err)
		os.Exit(exitFatal)
	}
	logger.Info("scan complete", "signatures", len(sigs))

	// 5. FetchTransactions.
	hashes := make([]string, len(sigs))
	for i, s := range sigs {
		hashes[i] = s.Hash
	}

	var rawTxs []json.RawMessage
	if len(hashes) > 0 {
		rawTxs, err = adapter.FetchTransactions(ctx, hashes)
		if err != nil {
			logger.Error("fetch transactions failed", "error", err)
			os.Exit(exitFatal)
		}
	}
	logger.Info("fetched transactions", "count", len(rawTxs))

	// 6. Sidecar decode + normalise + canonicalise.
	replayTxs, err := decodeAndCanonicalize(ctx, sidecarClient, chain, network, addresses, sigs, rawTxs)
	if err != nil {
		logger.Error("decode/canonicalize failed", "error", err)
		os.Exit(exitFatal)
	}

	replayEventCount := 0
	for _, tx := range replayTxs {
		replayEventCount += len(tx.BalanceEvents)
	}
	logger.Info("replay events generated", "count", replayEventCount)

	// 7. Query DB for existing events.
	dbEvents, err := beRepo.GetByBlockRange(ctx, string(chain), string(network), *startBlock, *endBlock)
	if err != nil {
		logger.Error("db query failed", "error", err)
		os.Exit(exitFatal)
	}
	logger.Info("db events fetched", "count", len(dbEvents))

	// 8. Compare.
	result := compareEvents(replayTxs, dbEvents)

	// 9. Report.
	switch *outputFlag {
	case "json":
		if err := printJSONReport(os.Stdout, string(chain), string(network), *startBlock, *endBlock, replayEventCount, len(dbEvents), result); err != nil {
			logger.Error("json report failed", "error", err)
			os.Exit(exitFatal)
		}
	default:
		printTextReport(os.Stdout, string(chain), string(network), *startBlock, *endBlock, replayEventCount, len(dbEvents), result)
	}

	if result.HasMismatch() {
		os.Exit(exitMismatch)
	}
	os.Exit(exitMatch)
}

// buildAdapter constructs the appropriate chain adapter.
func buildAdapter(chain model.Chain, rpcURL string, logger *slog.Logger) chainpkg.ChainAdapter {
	switch chain {
	case model.ChainSolana:
		return solana.NewAdapter(rpcURL, logger)
	case model.ChainBTC:
		return btc.NewAdapter(rpcURL, logger)
	default: // EVM chains: ethereum, base, polygon, arbitrum, bsc
		return base.NewAdapterWithChain(string(chain), rpcURL, logger)
	}
}

// scanBlocks uses the adapter's ScanBlocks if available, otherwise falls back
// to per-address FetchNewSignatures.
func scanBlocks(ctx context.Context, adapter chainpkg.ChainAdapter, startBlock, endBlock int64, addresses []string) ([]chainpkg.SignatureInfo, error) {
	if bsa, ok := adapter.(chainpkg.BlockScanAdapter); ok {
		return bsa.ScanBlocks(ctx, startBlock, endBlock, addresses)
	}
	// Fallback: per-address signature fetch (Solana cursor-based adapters
	// won't normally reach here since Solana also implements ScanBlocks,
	// but handle gracefully).
	var all []chainpkg.SignatureInfo
	for _, addr := range addresses {
		startCursor := fmt.Sprintf("%d", startBlock)
		sigs, err := adapter.FetchNewSignatures(ctx, addr, &startCursor, 1000)
		if err != nil {
			return nil, fmt.Errorf("fetch signatures for %s: %w", addr, err)
		}
		for _, s := range sigs {
			if s.Sequence >= startBlock && s.Sequence <= endBlock {
				all = append(all, s)
			}
		}
	}
	return all, nil
}

// decodeAndCanonicalize sends raw transactions through the sidecar decoder,
// then applies the normalizer's canonicalization to generate event_ids.
func decodeAndCanonicalize(
	ctx context.Context,
	client sidecarv1.ChainDecoderClient,
	chain model.Chain,
	network model.Network,
	addresses []string,
	sigs []chainpkg.SignatureInfo,
	rawTxs []json.RawMessage,
) ([]event.NormalizedTransaction, error) {
	if len(rawTxs) == 0 {
		return nil, nil
	}

	// Build gRPC request.
	protoTxs := make([]*sidecarv1.RawTransaction, len(rawTxs))
	for i, raw := range rawTxs {
		hash := ""
		if i < len(sigs) {
			hash = sigs[i].Hash
		}
		protoTxs[i] = &sidecarv1.RawTransaction{
			Signature: hash,
			RawJson:   raw,
		}
	}

	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	resp, err := client.DecodeSolanaTransactionBatch(callCtx, &sidecarv1.DecodeSolanaTransactionBatchRequest{
		Transactions:     protoTxs,
		WatchedAddresses: addresses,
	})
	if err != nil {
		return nil, fmt.Errorf("sidecar decode: %w", err)
	}

	// Build watched address set for the normalizer.
	watchedSet := make(map[string]struct{}, len(addresses))
	for _, a := range addresses {
		watchedSet[a] = struct{}{}
	}

	// Convert results to NormalizedTransactions.
	var txs []event.NormalizedTransaction
	for _, result := range resp.GetResults() {
		if result == nil {
			continue
		}
		ntx := normalizer.ReplayNormalizeTxResult(chain, network, result, watchedSet)
		txs = append(txs, ntx)
	}

	return txs, nil
}
