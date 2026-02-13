# multichain-indexer

Multi-chain custody indexer: Go pipeline + Node.js sidecar (gRPC decoder).

## Architecture

- **Go pipeline**: Coordinator → Fetcher → Normalizer → Ingester
- **Node.js sidecar**: gRPC service for chain-specific transaction decoding
- **Storage**: PostgreSQL (unified tables + JSONB), Redis (future process separation)
- **MVP target**: Solana devnet (SOL + SPL Token)

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
- `internal/store/` — repository implementations (PostgreSQL, Redis)
- `proto/` — protobuf definitions
- `sidecar/` — Node.js gRPC decoder service

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
- Unified table strategy: common columns + `chain_data JSONB`

## Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `SOLANA_RPC_URL` | Solana RPC endpoint | `https://api.devnet.solana.com` |
| `DB_URL` | PostgreSQL connection string | `postgres://indexer:indexer@localhost:5433/custody_indexer?sslmode=disable` |
| `REDIS_URL` | Redis connection string | `redis://localhost:6380` |
| `SIDECAR_ADDR` | Sidecar gRPC address | `localhost:50051` |
| `WATCHED_ADDRESSES` | Comma-separated Solana addresses | — |
| `LOG_LEVEL` | Log level (debug, info, warn, error) | `info` |
