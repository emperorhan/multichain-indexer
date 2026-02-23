# AGENTS.md

## Purpose
This repository runs a multi-chain on-chain indexer (Solana, Base, Ethereum, BTC, Polygon, Arbitrum, BSC).

## Required Validation
- `go build ./cmd/indexer` — verify Go compilation
- `cd sidecar && npm run build` — verify sidecar compilation
- `make test`
- `make test-sidecar`
- `make lint`
