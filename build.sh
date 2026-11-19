#!/usr/bin/env bash
# Build / test from WSL2. Requires Go 1.27.4+.
#   ./build.sh          → compile packages for windows/amd64 (smoke)
#   ./build.sh test     → go test ./...
#   ./build.sh race     → go test -race ./internal/capture/...
set -euo pipefail
cd "$(dirname "$0")"

GO_VER=$(go version | awk '{print $3}')
echo "using $GO_VER"

if [[ "${1:-}" == "test" ]]; then
  go test ./...
  exit 0
fi

if [[ "${1:-}" == "race" ]]; then
  go test -race ./internal/capture/...
  exit 0
fi

go mod tidy
export GOOS=windows GOARCH=amd64 CGO_ENABLED=0
# Package compile smoke (no GUI binary in this phase)
go test -c -o /dev/null ./internal/imgconv
go test -c -o /dev/null ./internal/extract
go test -c -o /dev/null ./internal/ocr
go test -c -o /dev/null ./internal/capture
go test -c -o /dev/null ./internal/config
# camera is windows-only implementation; build the package
go build -o /dev/null ./internal/camera
echo "windows/amd64 package build ok"
