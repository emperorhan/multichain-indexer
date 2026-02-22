# multichain-indexer

Multi-chain custody indexer: Go pipeline + Node.js sidecar (gRPC decoder).

## Architecture

- **Go pipeline**: Coordinator → Fetcher → Normalizer → Ingester
- **Node.js sidecar**: gRPC decoder with plugin-based balance event detection
- **Storage**: PostgreSQL (balance_events + JSONB)
- **Chains**: Solana, Base, Ethereum, BTC, Polygon, Arbitrum, BSC (7 chains)

## Quick Start

```bash
# Start infrastructure
docker-compose -f deployments/docker-compose.yaml up -d

# Run migrations
make migrate

# Generate protobuf code
make proto

# Run indexer
make run
```

## Project Structure

- `cmd/indexer/` — entry point
- `internal/config/` — configuration loading
- `internal/domain/model/` — DB models (unified tables)
- `internal/domain/event/` — pipeline event types
- `internal/chain/` — chain adapter interface + implementations
- `internal/pipeline/` — pipeline stages (coordinator, fetcher, normalizer, ingester)
- `internal/pipeline/identity/` — shared identity/canonicalization functions (dedup across pipeline stages)
- `internal/pipeline/health.go` — per-chain health monitoring (HEALTHY/UNHEALTHY/INACTIVE)
- `internal/pipeline/registry.go` — chain:network → Pipeline instance mapping
- `internal/pipeline/config_watcher.go` — hot config reload (polls runtime_configs every 30s)
- `internal/pipeline/finalizer/` — optional Finalizer stage for reorg-aware chains
- `internal/pipeline/reorgdetector/` — reorg detection (consecutive RPC error alerts)
- `internal/pipeline/replay/` — replay service
- `internal/pipeline/retry/` — retry classification
- `internal/circuitbreaker/` — circuit breaker (Fetcher RPC + Normalizer sidecar)
- `internal/cache/` — ShardedLRU cache
- `internal/tracing/` — OpenTelemetry tracing
- `internal/metrics/` — Prometheus metrics
- `internal/admin/` — admin API + embedded dashboard (auth, rate limiting, audit logging)
- `internal/alert/` — alert system (Slack, Webhook) with state-transition-aware cooldown
- `internal/reconciliation/` — balance reconciliation (on-chain vs DB)
- `internal/store/` — repository implementations (PostgreSQL)
- `proto/` — protobuf definitions
- `sidecar/` — Node.js gRPC decoder service
- `sidecar/src/decoder/solana/plugin/` — plugin system (EventPlugin interface, PluginDispatcher, builtin plugins)

## Key Commands

```bash
make build          # Build Go binary
make run            # Run indexer
make test           # Run tests
make migrate        # Run DB migrations
make migrate-down   # Rollback migrations
make proto          # Generate protobuf code
make sidecar-build  # Build sidecar Docker image
make lint           # Run linter
```

## Database

- PostgreSQL 16 on port 5433
- Migrations in `internal/store/postgres/migrations/`
- Core tables: `transactions`, `balance_events`, `balances`, `tokens`, `watched_addresses`, `indexer_configs`, `indexed_blocks`, `pipeline_watermarks`
- Operational tables: `address_books`, `balance_reconciliation_snapshots`, `runtime_configs`
- `balance_events` uses signed delta model (+deposit, -withdrawal) with UNIQUE INDEX on event_id for dedup
- `finality_rank()` PG function for finality ordering (replaces hardcoded CASE WHEN)
- 22 migrations (001–022)

## Configuration

The indexer uses a layered configuration system:

1. **YAML file** — base configuration (default path: `config.yaml`, override with `CONFIG_FILE` env var)
2. **Environment variables** — override any YAML value
3. **Built-in defaults** — applied for any remaining zero-value fields

If no YAML file is found, the indexer runs in env-only mode (backward compatible).
See `configs/config.example.yaml` for the full reference with all fields, defaults, and env var names.

## Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `CONFIG_FILE` | Path to YAML config file | `config.yaml` |
| `DB_URL` | PostgreSQL connection string | `postgres://indexer:indexer@localhost:5433/custody_indexer?sslmode=disable` |
| `SIDECAR_ADDR` | Sidecar gRPC address | `localhost:50051` |
| `WATCHED_ADDRESSES` | Comma-separated watched addresses | — |
| `LOG_LEVEL` | Log level (debug, info, warn, error) | `info` |
| `SOLANA_RPC_URL` | Solana RPC endpoint | `https://api.devnet.solana.com` |
| `BASE_RPC_URL` | Base RPC endpoint | — |
| `ETH_RPC_URL` | Ethereum RPC endpoint | — |
| `BTC_RPC_URL` | Bitcoin RPC endpoint | — |
| `POLYGON_RPC_URL` | Polygon RPC endpoint | — |
| `ARBITRUM_RPC_URL` | Arbitrum RPC endpoint | — |
| `BSC_RPC_URL` | BSC RPC endpoint | — |
| `FETCH_WORKERS` | Fetcher worker count | — |
| `NORMALIZER_WORKERS` | Normalizer worker count | — |
| `BATCH_SIZE` | Batch size (max 10000) | — |
| `BLOCK_SCAN_MAX_BATCH_TXS` | Block-scan batch chunking limit | `500` |
| `INTERLEAVE_MAX_SKEW_MS` | Commit interleaver max skew | — |
| `ADMIN_API_KEY` | Admin API authentication key | — |
| `ADMIN_LISTEN_ADDR` | Admin API listen address | — |
| `RECONCILIATION_ENABLED` | Enable balance reconciliation | — |
| `RECONCILIATION_INTERVAL_MS` | Reconciliation interval | — |
| `SLACK_WEBHOOK_URL` | Slack webhook for alerts | — |
| `ALERT_WEBHOOK_URL` | Generic webhook for alerts | — |
| `ALERT_COOLDOWN_MS` | Alert dedup cooldown | `1800000` (30 min) |
