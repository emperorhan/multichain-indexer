#!/usr/bin/env bash
set -euo pipefail

# ============================================================
# Testbed Setup Script — Mainnet 3-Chain
# Solana, Ethereum, BTC 메인넷 동시 인덱싱
# ============================================================

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
COMPOSE_FILE="$PROJECT_ROOT/deployments/docker-compose.yaml"
CONFIG_FILE="$PROJECT_ROOT/configs/testbed.yaml"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m'

log()  { echo -e "${GREEN}[testbed]${NC} $*"; }
warn() { echo -e "${YELLOW}[testbed]${NC} $*"; }
err()  { echo -e "${RED}[testbed]${NC} $*" >&2; }
info() { echo -e "${CYAN}[testbed]${NC} $*"; }

# ============================================================
# Commands
# ============================================================

cmd_up() {
    log "Starting infrastructure (PostgreSQL, Redis, Prometheus, Grafana)..."
    docker compose -f "$COMPOSE_FILE" up -d postgres redis prometheus grafana

    log "Waiting for PostgreSQL to be healthy..."
    local retries=0
    until docker compose -f "$COMPOSE_FILE" exec -T postgres pg_isready -U indexer -d custody_indexer >/dev/null 2>&1; do
        retries=$((retries + 1))
        if [ "$retries" -ge 30 ]; then
            err "PostgreSQL failed to start"
            exit 1
        fi
        sleep 1
    done
    log "PostgreSQL ready."

    log "Running migrations..."
    docker compose -f "$COMPOSE_FILE" up migrate
    log "Migrations complete."

    log "Starting sidecar..."
    docker compose -f "$COMPOSE_FILE" up -d sidecar

    log "Waiting for sidecar to be healthy..."
    retries=0
    until docker compose -f "$COMPOSE_FILE" exec -T sidecar node -e "
        const g=require('@grpc/grpc-js');
        const c=new g.Client('localhost:50051',g.credentials.createInsecure());
        c.waitForReady(Date.now()+2000,e=>{process.exit(e?1:0)})
    " >/dev/null 2>&1; do
        retries=$((retries + 1))
        if [ "$retries" -ge 30 ]; then
            warn "Sidecar health check timed out (may still be starting)"
            break
        fi
        sleep 2
    done

    echo ""
    log "Infrastructure ready!"
    echo ""
    echo -e "  ${BLUE}PostgreSQL${NC}  localhost:5433  (indexer/indexer)"
    echo -e "  ${BLUE}Redis${NC}       localhost:6380"
    echo -e "  ${BLUE}Prometheus${NC}  http://localhost:9090"
    echo -e "  ${BLUE}Grafana${NC}     http://localhost:3000  (admin/admin)"
    echo -e "  ${BLUE}Sidecar${NC}     localhost:50051 (gRPC)"
    echo ""
}

cmd_run() {
    local chain="${1:-all}"

    log "Building indexer..."
    (cd "$PROJECT_ROOT" && go build -o bin/indexer ./cmd/indexer)

    case "$chain" in
        all)
            cmd_run_all
            ;;
        solana)
            cmd_run_solana
            ;;
        ethereum|eth)
            cmd_run_ethereum
            ;;
        btc|bitcoin)
            cmd_run_btc
            ;;
        *)
            err "Unknown chain: $chain"
            echo "  Usage: $0 run [all|solana|ethereum|btc]"
            exit 1
            ;;
    esac
}

cmd_run_all() {
    echo ""
    log "=== 3-Chain Mainnet Simultaneous Run ==="
    echo ""

    # Check RPC availability
    local chains_available=()
    local chains_missing=()

    # Solana: always available (free public RPC or custom)
    chains_available+=("solana-mainnet")
    if [ -n "${SOLANA_RPC_URL:-}" ]; then
        info "Solana mainnet: ${SOLANA_RPC_URL:0:40}..."
    else
        info "Solana mainnet: public RPC (https://api.mainnet-beta.solana.com)"
    fi

    # Ethereum: requires ETH_RPC_URL or ETHEREUM_RPC_URL
    local eth_rpc="${ETH_RPC_URL:-${ETHEREUM_RPC_URL:-}}"
    if [ -n "$eth_rpc" ]; then
        export ETH_RPC_URL="$eth_rpc"
        export ETHEREUM_RPC_URL="$eth_rpc"
        chains_available+=("ethereum-mainnet")
        info "Ethereum mainnet: ${eth_rpc:0:40}..."
    else
        chains_missing+=("ethereum-mainnet")
        warn "Ethereum mainnet: SKIPPED (set ETH_RPC_URL or ETHEREUM_RPC_URL to enable)"
    fi

    # BTC: requires BTC_RPC_URL
    if [ -n "${BTC_RPC_URL:-}" ]; then
        chains_available+=("btc-mainnet")
        info "BTC mainnet: ${BTC_RPC_URL:0:40}..."
    else
        chains_missing+=("btc-mainnet")
        warn "BTC mainnet: SKIPPED (set BTC_RPC_URL to enable)"
    fi

    echo ""

    if [ ${#chains_available[@]} -eq 0 ]; then
        err "No chains available. At minimum, infrastructure must be running."
        exit 1
    fi

    # Build chain_targets CSV
    local targets
    targets=$(IFS=,; echo "${chains_available[*]}")

    log "Starting indexer with chains: $targets"
    echo ""
    print_start_positions "$chains_available"
    echo ""
    print_watched_addresses "$chains_available"
    echo ""

    CONFIG_FILE="$CONFIG_FILE" \
    RUNTIME_LIKE_GROUP="" \
    RUNTIME_CHAIN_TARGETS="$targets" \
    ADMIN_REQUIRE_AUTH=false \
    SOLANA_RPC_URL="${SOLANA_RPC_URL:-}" \
    ETH_RPC_URL="${ETH_RPC_URL:-}" \
    ETHEREUM_RPC_URL="${ETHEREUM_RPC_URL:-}" \
    BTC_RPC_URL="${BTC_RPC_URL:-}" \
        "$PROJECT_ROOT/bin/indexer"
}

cmd_run_solana() {
    log "Starting indexer (Solana mainnet)..."
    echo ""
    info "Start position (from chain tip):"
    echo "  Solana:   head - 5,000 slots   (~33 min behind tip)"
    echo ""
    warn "Watched addresses (Solana mainnet):"
    echo "  5Q544fKrFoe6tsEbD7S8EmxGTJYAKtTVhAW5Q5pge4j1  (Raydium Authority)"
    echo "  JUP6LkbZbjS1jKKwapdHNy74zcZ3tLUZoi5QNyVTaV4    (Jupiter V6)"
    echo "  MarBmsSgKXdrN1egZf5sqe1TMai9K1rChYNDJgjq7aD     (Marinade Finance)"
    echo "  CuieVDEDtLo7FypA9SbLM9saXFdb1dsshEkyErMqkRQq    (Phantom whale)"
    echo "  7VHUFJHWu2CuExkJcJrzhQPJ2oygupd2fDnjThLk64aR    (USDC reserve)"
    echo ""

    CONFIG_FILE="$CONFIG_FILE" \
    RUNTIME_LIKE_GROUP="" \
    RUNTIME_CHAIN_TARGETS="solana-mainnet" \
    ADMIN_REQUIRE_AUTH=false \
    SOLANA_RPC_URL="${SOLANA_RPC_URL:-}" \
        "$PROJECT_ROOT/bin/indexer"
}

cmd_run_ethereum() {
    local eth_rpc="${ETH_RPC_URL:-${ETHEREUM_RPC_URL:-}}"
    if [ -z "$eth_rpc" ]; then
        err "ETH_RPC_URL or ETHEREUM_RPC_URL is required for Ethereum mainnet"
        echo ""
        echo "  Free RPC options:"
        echo "    Alchemy:  https://eth-mainnet.g.alchemy.com/v2/YOUR_KEY"
        echo "    Infura:   https://mainnet.infura.io/v3/YOUR_KEY"
        echo ""
        echo "  Usage: ETHEREUM_RPC_URL=https://... $0 run ethereum"
        exit 1
    fi
    export ETH_RPC_URL="$eth_rpc"
    export ETHEREUM_RPC_URL="$eth_rpc"
    log "Starting indexer (Ethereum mainnet)..."
    echo ""
    info "Start position (from chain tip):"
    echo "  Ethereum: head - 300 blocks    (~60 min behind tip)"
    echo ""
    warn "Watched addresses (Ethereum mainnet):"
    echo "  0x3fC91A3afd70395Cd496C647d5a6CC9D4B2b7FAD  (Uniswap Universal Router)"
    echo "  0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48  (USDC)"
    echo "  0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045  (vitalik.eth)"
    echo "  0xA9D1e08C7793af67e9d92fe308d5697FB81d3E43  (Coinbase Hot Wallet)"
    echo "  0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2  (WETH)"
    echo ""

    CONFIG_FILE="$CONFIG_FILE" \
    RUNTIME_LIKE_GROUP="" \
    RUNTIME_CHAIN_TARGETS="ethereum-mainnet" \
    ADMIN_REQUIRE_AUTH=false \
    ETH_RPC_URL="$ETH_RPC_URL" \
    ETHEREUM_RPC_URL="$ETHEREUM_RPC_URL" \
        "$PROJECT_ROOT/bin/indexer"
}

cmd_run_btc() {
    if [ -z "${BTC_RPC_URL:-}" ]; then
        err "BTC_RPC_URL is required for BTC mainnet"
        echo ""
        echo "  Free RPC options:"
        echo "    QuickNode: BTC mainnet free tier"
        echo "    GetBlock:  https://go.getblock.io/YOUR_KEY"
        echo ""
        echo "  Usage: BTC_RPC_URL=https://... $0 run btc"
        exit 1
    fi
    log "Starting indexer (BTC mainnet)..."
    echo ""
    info "Start position (from chain tip):"
    echo "  BTC:      head - 50 blocks     (~8 hrs behind tip)"
    echo ""
    warn "Watched addresses (BTC mainnet):"
    echo "  34xp4vRoCGJym3xR7yCVPFHoCNxv4Twseo              (Binance Cold Wallet)"
    echo "  3JZq4atUahhuA9rLhXLMhhTo133J9rF97j              (Bitfinex Hot Wallet)"
    echo "  bc1qxy2kgdygjrsqtzq2n0yrf2493p83kkfjhx0wlh      (Binance Hot Wallet)"
    echo "  bc1qa5wkgaew2dkv56kc6hp24cc2nkej6ece2grary      (Kraken Hot Wallet)"
    echo "  bc1qm34lsc65zpw79lxes69zkqmk6ee3ewf0j77s3h      (SegWit bech32)"
    echo ""

    CONFIG_FILE="$CONFIG_FILE" \
    RUNTIME_LIKE_GROUP="" \
    RUNTIME_CHAIN_TARGETS="btc-mainnet" \
    ADMIN_REQUIRE_AUTH=false \
    BTC_RPC_URL="$BTC_RPC_URL" \
        "$PROJECT_ROOT/bin/indexer"
}

print_start_positions() {
    local chains=("$@")
    info "Start positions (from chain tip):"
    for chain in "${chains[@]}"; do
        case "$chain" in
            solana-mainnet)
                echo "  Solana:   head - 5,000 slots   (~33 min behind tip)"
                ;;
            ethereum-mainnet)
                echo "  Ethereum: head - 300 blocks    (~60 min behind tip)"
                ;;
            btc-mainnet)
                echo "  BTC:      head - 50 blocks     (~8 hrs behind tip)"
                ;;
        esac
    done
}

print_watched_addresses() {
    local chains=("$@")
    for chain in "${chains[@]}"; do
        case "$chain" in
            solana-mainnet)
                info "Solana mainnet watched addresses:"
                echo "  5Q544fKrFoe6tsEbD7S8EmxGTJYAKtTVhAW5Q5pge4j1  (Raydium Authority)"
                echo "  JUP6LkbZbjS1jKKwapdHNy74zcZ3tLUZoi5QNyVTaV4    (Jupiter V6)"
                echo "  MarBmsSgKXdrN1egZf5sqe1TMai9K1rChYNDJgjq7aD     (Marinade Finance)"
                echo "  CuieVDEDtLo7FypA9SbLM9saXFdb1dsshEkyErMqkRQq    (Phantom whale)"
                echo "  7VHUFJHWu2CuExkJcJrzhQPJ2oygupd2fDnjThLk64aR    (USDC reserve)"
                ;;
            ethereum-mainnet)
                info "Ethereum mainnet watched addresses:"
                echo "  0x3fC91A3afd70395Cd496C647d5a6CC9D4B2b7FAD  (Uniswap Universal Router)"
                echo "  0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48  (USDC)"
                echo "  0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045  (vitalik.eth)"
                echo "  0xA9D1e08C7793af67e9d92fe308d5697FB81d3E43  (Coinbase Hot Wallet)"
                echo "  0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2  (WETH)"
                ;;
            btc-mainnet)
                info "BTC mainnet watched addresses:"
                echo "  34xp4vRoCGJym3xR7yCVPFHoCNxv4Twseo              (Binance Cold Wallet)"
                echo "  3JZq4atUahhuA9rLhXLMhhTo133J9rF97j              (Bitfinex Hot Wallet)"
                echo "  bc1qxy2kgdygjrsqtzq2n0yrf2493p83kkfjhx0wlh      (Binance Hot Wallet)"
                echo "  bc1qa5wkgaew2dkv56kc6hp24cc2nkej6ece2grary      (Kraken Hot Wallet)"
                echo "  bc1qm34lsc65zpw79lxes69zkqmk6ee3ewf0j77s3h      (SegWit bech32)"
                ;;
        esac
    done
}

cmd_status() {
    echo ""
    log "Infrastructure Status"
    echo "  ─────────────────────────────────────────"

    # PostgreSQL
    if docker compose -f "$COMPOSE_FILE" exec -T postgres pg_isready -U indexer -d custody_indexer >/dev/null 2>&1; then
        echo -e "  ${GREEN}●${NC} PostgreSQL    localhost:5433"
        # Show table counts
        COUNTS=$(docker compose -f "$COMPOSE_FILE" exec -T postgres psql -U indexer -d custody_indexer -t -c "
            SELECT 'transactions: ' || COUNT(*) FROM transactions
            UNION ALL SELECT 'balance_events: ' || COUNT(*) FROM balance_events
            UNION ALL SELECT 'balances: ' || COUNT(*) FROM balances
            UNION ALL SELECT 'tokens: ' || COUNT(*) FROM tokens
            UNION ALL SELECT 'watched_addresses: ' || COUNT(*) FROM watched_addresses
        " 2>/dev/null | tr -d ' ')
        if [ -n "$COUNTS" ]; then
            echo "$COUNTS" | while read -r line; do
                [ -n "$line" ] && echo "    $line"
            done
        fi
    else
        echo -e "  ${RED}●${NC} PostgreSQL    NOT RUNNING"
    fi

    # Redis
    if docker compose -f "$COMPOSE_FILE" exec -T redis redis-cli ping >/dev/null 2>&1; then
        echo -e "  ${GREEN}●${NC} Redis         localhost:6380"
    else
        echo -e "  ${RED}●${NC} Redis         NOT RUNNING"
    fi

    # Prometheus
    if curl -s http://localhost:9090/-/healthy >/dev/null 2>&1; then
        echo -e "  ${GREEN}●${NC} Prometheus    http://localhost:9090"
    else
        echo -e "  ${RED}●${NC} Prometheus    NOT RUNNING"
    fi

    # Grafana
    if curl -s http://localhost:3000/api/health >/dev/null 2>&1; then
        echo -e "  ${GREEN}●${NC} Grafana       http://localhost:3000"
    else
        echo -e "  ${RED}●${NC} Grafana       NOT RUNNING"
    fi

    # Sidecar
    if docker compose -f "$COMPOSE_FILE" ps sidecar 2>/dev/null | grep -q "running"; then
        echo -e "  ${GREEN}●${NC} Sidecar       localhost:50051"
    else
        echo -e "  ${RED}●${NC} Sidecar       NOT RUNNING"
    fi

    # Indexer (local process)
    if curl -s http://localhost:8080/healthz >/dev/null 2>&1; then
        echo -e "  ${GREEN}●${NC} Indexer       http://localhost:8080"
        echo -e "    Admin API:  http://localhost:9091"
        echo -e "    Metrics:    http://localhost:8080/metrics"
    else
        echo -e "  ${YELLOW}●${NC} Indexer       NOT RUNNING (start with: $0 run [all|solana|ethereum|btc])"
    fi

    echo ""
}

cmd_query() {
    log "Database contents:"
    docker compose -f "$COMPOSE_FILE" exec -T postgres psql -U indexer -d custody_indexer -c "
        SELECT 'transactions' AS table_name, COUNT(*) AS rows FROM transactions
        UNION ALL SELECT 'balance_events', COUNT(*) FROM balance_events
        UNION ALL SELECT 'balances', COUNT(*) FROM balances
        UNION ALL SELECT 'tokens', COUNT(*) FROM tokens
        UNION ALL SELECT 'watched_addresses', COUNT(*) FROM watched_addresses
        UNION ALL SELECT 'pipeline_watermarks', COUNT(*) FROM pipeline_watermarks
        UNION ALL SELECT 'indexed_blocks', COUNT(*) FROM indexed_blocks
        ORDER BY table_name;
    "

    echo ""
    log "Recent balance events (last 10):"
    docker compose -f "$COMPOSE_FILE" exec -T postgres psql -U indexer -d custody_indexer -c "
        SELECT
            be.chain, be.network,
            SUBSTRING(be.address, 1, 12) || '...' AS address,
            be.activity_type,
            be.delta,
            COALESCE(t.symbol, '?') AS token,
            be.finality_state,
            be.created_at
        FROM balance_events be
        LEFT JOIN tokens t ON be.token_id = t.id
        ORDER BY be.created_at DESC
        LIMIT 10;
    "

    echo ""
    log "Current balances:"
    docker compose -f "$COMPOSE_FILE" exec -T postgres psql -U indexer -d custody_indexer -c "
        SELECT
            b.chain, b.network,
            SUBSTRING(b.address, 1, 12) || '...' AS address,
            b.amount,
            COALESCE(t.symbol, '?') AS token,
            b.last_updated_cursor
        FROM balances b
        LEFT JOIN tokens t ON b.token_id = t.id
        ORDER BY b.chain, b.address
        LIMIT 20;
    "

    echo ""
    log "Pipeline watermarks (indexing progress):"
    docker compose -f "$COMPOSE_FILE" exec -T postgres psql -U indexer -d custody_indexer -c "
        SELECT chain, network, ingested_sequence, head_sequence, last_heartbeat_at
        FROM pipeline_watermarks
        ORDER BY chain;
    "
}

cmd_down() {
    log "Stopping all services..."
    docker compose -f "$COMPOSE_FILE" down
    log "Done."
}

cmd_reset() {
    warn "This will delete all data. Continue? [y/N]"
    read -r answer
    if [ "$answer" != "y" ] && [ "$answer" != "Y" ]; then
        log "Cancelled."
        exit 0
    fi
    log "Stopping and removing volumes..."
    docker compose -f "$COMPOSE_FILE" down -v
    log "All data deleted."
}

# ============================================================
# Usage
# ============================================================

usage() {
    echo ""
    echo "Usage: $0 <command> [options]"
    echo ""
    echo "Commands:"
    echo "  up                  Start infrastructure (PostgreSQL, Redis, Prometheus, Grafana, Sidecar)"
    echo "  run [chain]         Build and run indexer"
    echo "                        all       — 3-chain simultaneous (default)"
    echo "                        solana    — Solana mainnet only (free RPC)"
    echo "                        ethereum  — Ethereum mainnet only (requires ETH_RPC_URL)"
    echo "                        btc       — BTC mainnet only (requires BTC_RPC_URL)"
    echo "  status              Show status of all services and DB counts"
    echo "  query               Query database for indexed data"
    echo "  down                Stop all services"
    echo "  reset               Stop all services and delete all data"
    echo ""
    echo "Quick start (3-chain simultaneous):"
    echo "  $0 up"
    echo "  ETH_RPC_URL=https://... BTC_RPC_URL=https://... $0 run"
    echo ""
    echo "Solana only (no API key needed):"
    echo "  $0 up"
    echo "  $0 run solana"
    echo ""
    echo "Individual chains:"
    echo "  ETH_RPC_URL=https://eth-mainnet.g.alchemy.com/v2/KEY $0 run ethereum"
    echo "  BTC_RPC_URL=https://go.getblock.io/KEY $0 run btc"
    echo ""
}

# ============================================================
# Main
# ============================================================

case "${1:-}" in
    up)     cmd_up ;;
    run)    cmd_run "${2:-all}" ;;
    status) cmd_status ;;
    query)  cmd_query ;;
    down)   cmd_down ;;
    reset)  cmd_reset ;;
    *)      usage ;;
esac
