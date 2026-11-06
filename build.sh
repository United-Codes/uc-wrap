#!/usr/bin/env sh
# Build the uc-wrap CLI into ./bin
set -eu

cd "$(dirname "$0")"
mkdir -p bin

go build -trimpath -ldflags "-s -w" -o bin/uc-wrap ./cmd/uc-wrap

echo "Built bin/uc-wrap"
