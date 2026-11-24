#!/usr/bin/env bash
# Cross-builds Enter Space Light. No cgo anywhere: Windows mounts load the
# WinFsp DLL at runtime, Linux mounts use pure-Go FUSE.
#
#   scripts/build.sh [VERSION] [targets...]     default targets: all
#   OUT=dist scripts/build.sh 0.1.0 linux/amd64
#
# Windows gets two executables from the same code:
#   esl.exe              console subsystem, for terminals and scripts
#   EnterSpaceLight.exe  GUI subsystem: no console window at sign-in
set -euo pipefail
cd "$(dirname "$0")/.."
VERSION="${1:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
shift || true
OUT="${OUT:-dist}"
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || true)"
TARGETS=("$@")
[ ${#TARGETS[@]} -eq 0 ] && TARGETS=(windows/amd64 windows/arm64 linux/amd64 linux/arm64)
RCLONE_VERSION="$(go list -m -f '{{.Version}}' github.com/rclone/rclone)"
LDFLAGS="-s -w -X github.com/blaineam/enter-space-light/internal/version.Version=${VERSION#v} -X github.com/blaineam/enter-space-light/internal/version.Commit=${COMMIT} -X github.com/rclone/rclone/fs.Version=${RCLONE_VERSION}"
mkdir -p "$OUT"
for t in "${TARGETS[@]}"; do
  os="${t%/*}"; arch="${t#*/}"
  dir="$OUT/esl-${VERSION#v}-${os}-${arch}"
  mkdir -p "$dir"
  if [ "$os" = windows ]; then
    CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -tags cmount -trimpath -ldflags "$LDFLAGS" -o "$dir/esl.exe" ./cmd/esl
    CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -tags cmount -trimpath -ldflags "$LDFLAGS -H=windowsgui" -o "$dir/EnterSpaceLight.exe" ./cmd/esl
  else
    CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -ldflags "$LDFLAGS" -o "$dir/esl" ./cmd/esl
  fi
  echo "built $dir"
done
