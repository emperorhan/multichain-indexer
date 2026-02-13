#!/bin/bash
set -euo pipefail

PROTO_DIR="proto"
OUT_DIR="pkg/generated"

mkdir -p "${OUT_DIR}/sidecar/v1"

protoc \
  --proto_path="${PROTO_DIR}" \
  --go_out="${OUT_DIR}" \
  --go_opt=paths=source_relative \
  --go-grpc_out="${OUT_DIR}" \
  --go-grpc_opt=paths=source_relative \
  "${PROTO_DIR}/sidecar/v1/decoder.proto"

echo "Proto generation complete: ${OUT_DIR}/sidecar/v1/"
