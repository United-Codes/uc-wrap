#!/usr/bin/env sh
# Build the plsqlwrap CLI into ./bin
set -eu

cd "$(dirname "$0")"
mkdir -p bin

go build -trimpath -ldflags "-s -w" -o bin/plsqlwrap ./cmd/plsqlwrap

echo "Built bin/plsqlwrap"
