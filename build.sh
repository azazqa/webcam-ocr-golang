#!/usr/bin/env bash
# Build Windows executables from WSL2. Requires Go 1.27.4+.
#   ./build.sh          → dist/webcam-ocr.exe
#   ./build.sh test     → go test ./...
#   ./build.sh race     → go test -race ./internal/capture/...
#   ./build.sh deploy /mnt/c/Users/<name>/Desktop/webcam-ocr
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
mkdir -p dist

# GUI needs Common Controls v6 + DPI manifest embedded as .syso
if [[ ! -f cmd/webcam-ocr/rsrc_windows_amd64.syso ]]; then
  GOOS= GOARCH= go install github.com/akavel/rsrc@v0.10.2
  "$(go env GOPATH)/bin/rsrc" -arch amd64 -manifest cmd/webcam-ocr/webcam-ocr.manifest \
    -o cmd/webcam-ocr/rsrc_windows_amd64.syso
fi

go build -trimpath -ldflags "-H windowsgui -s -w" -o dist/webcam-ocr.exe ./cmd/webcam-ocr
ls -lh dist/

if [[ "${1:-}" == "deploy" && -n "${2:-}" ]]; then
  mkdir -p "$2"
  cp dist/*.exe "$2/"
  echo "Copied → $2"
fi
