.PHONY: build run test test-sidecar lint proto migrate migrate-down sidecar-build sidecar-run docker-up docker-down mock-gen

# Go
BINARY_NAME=koda-custody-indexer
MAIN_PATH=./cmd/indexer

# Database
DB_URL ?= postgres://indexer:indexer@localhost:5433/custody_indexer?sslmode=disable
MIGRATIONS_PATH=internal/store/postgres/migrations

# Proto
PROTO_DIR=proto
PROTO_OUT=pkg/generated

build:
	go build -o bin/$(BINARY_NAME) $(MAIN_PATH)

run:
	go run $(MAIN_PATH)

test:
	go test ./... -v -race -count=1

test-sidecar:
	cd sidecar && npm test

mock-gen:
	mockgen -source=internal/store/repository.go -destination=internal/store/mocks/mock_repository.go -package=mocks
	mockgen -source=internal/chain/adapter.go -destination=internal/chain/mocks/mock_adapter.go -package=mocks
	mockgen -source=internal/chain/solana/rpc/client.go -destination=internal/chain/solana/rpc/mocks/mock_client.go -package=mocks
	mockgen -destination=internal/pipeline/normalizer/mocks/mock_decoder_client.go -package=mocks github.com/kodax/koda-custody-indexer/pkg/generated/sidecar/v1 ChainDecoderClient

lint:
	golangci-lint run ./...

# Database migrations
migrate:
	migrate -path $(MIGRATIONS_PATH) -database "$(DB_URL)" up

migrate-down:
	migrate -path $(MIGRATIONS_PATH) -database "$(DB_URL)" down 1

migrate-create:
	@read -p "Migration name: " name; \
	migrate create -ext sql -dir $(MIGRATIONS_PATH) -seq $$name

# Protobuf
proto:
	./scripts/generate_proto.sh

# Sidecar
sidecar-build:
	docker build -t koda-custody-sidecar:latest ./sidecar

sidecar-run:
	cd sidecar && npm run start

sidecar-dev:
	cd sidecar && npm run dev

# Docker
docker-up:
	docker-compose -f deployments/docker-compose.yaml up -d

docker-down:
	docker-compose -f deployments/docker-compose.yaml down

docker-logs:
	docker-compose -f deployments/docker-compose.yaml logs -f
