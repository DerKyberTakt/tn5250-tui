#!/bin/sh
# Build tn5250-tui for the local OS/arch into dist/, version from `git describe`.
set -eu
cd "$(dirname "$0")/.."
ver=$(git describe --tags --always --dirty 2>/dev/null || echo dev)
exe=tn5250-tui
if [ "$(go env GOOS)" = windows ]; then exe=tn5250-tui.exe; fi
mkdir -p dist
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$ver" -o "dist/$exe" ./cmd/tn5250-tui
echo "built dist/$exe ($ver)"
