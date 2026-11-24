#!/usr/bin/env bash
# go vet for every release target (and the macOS dev build), no cgo.
set -u
log="$(mktemp -d)"; trap 'rm -rf "$log"' EXIT
cd "$(dirname "$0")/.."
rc=0
for t in darwin/arm64 linux/amd64 linux/arm64 windows/amd64 windows/arm64; do
  os="${t%/*}"; arch="${t#*/}"; tags=(); [ "$os" = windows ] && tags=(-tags cmount)
  if CGO_ENABLED=0 GOOS=$os GOARCH=$arch go vet ${tags[@]+"${tags[@]}"} ./... > "$log/vet-$os-$arch.log" 2>&1; then echo "$t vet ok"; else echo "$t vet FAILED"; head -30 "$log/vet-$os-$arch.log"; rc=1; fi
done
exit $rc
