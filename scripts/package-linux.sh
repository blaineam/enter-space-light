#!/usr/bin/env bash
# Builds the .deb, .rpm and .tar.gz files from what scripts/build.sh left in
# dist/. Used by both CI and the release workflow.
#   scripts/package-linux.sh VERSION      → out/
set -euo pipefail
cd "$(dirname "$0")/.."
V="${1:?usage: package-linux.sh VERSION}"; V="${V#v}"
NFPM_VERSION=v2.48.0
GOBIN="$(mktemp -d)" ; export GOBIN
trap 'rm -rf "$GOBIN"' EXIT
GOFLAGS= GOOS= GOARCH= go install "github.com/goreleaser/nfpm/v2/cmd/nfpm@${NFPM_VERSION}"
mkdir -p out
for arch in amd64 arm64; do
  dir="dist/esl-$V-linux-$arch"
  [ -x "$dir/esl" ] || { echo "missing $dir/esl (run: OUT=dist scripts/build.sh $V linux/$arch)"; exit 1; }
  for fmt in deb rpm; do
    VERSION="$V" ARCH="$arch" BIN="$dir/esl" "$GOBIN/nfpm" package -f packaging/linux/nfpm.yaml -p "$fmt" -t out/
  done
  mkdir -p "$dir/icons"
  cp assets/icon-*.png "$dir/icons/"
  cp packaging/linux/install.sh packaging/linux/enter-space-light.desktop LICENSE NOTICE README.md "$dir/"
  tar -C dist -czf "out/EnterSpaceLight-$V-linux-$arch.tar.gz" "esl-$V-linux-$arch"
done
ls -l out
