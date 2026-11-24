#!/usr/bin/env bash
# Proves backup manifests are interchangeable with Enter Space for Apple
# platforms, using the Apple app's real codec. Run on a Mac with the Enter
# Space repo checked out next to this one:
#   scripts/check-apple-compat.sh [path/to/EnterSpaceShared/BackupManifest.swift]
# It (1) regenerates testdata/apple-written.json.deflate with Swift and checks
# Go reads it, and (2) has Swift decode a Go-written manifest.
set -euo pipefail
cd "$(dirname "$0")/.."
src="${1:-../Enter Space/EnterSpaceShared/BackupManifest.swift}"
work="$(mktemp -d)"; trap 'rm -rf "$work"' EXIT
cp scripts/apple-compat-main.swift "$work/main.swift"   # swiftc only allows top-level code in main.swift
swiftc -O "$src" "$work/main.swift" -o "$work/compat"
"$work/compat" write internal/backup/testdata/apple-written.json.deflate
go test -count=1 -run TestReadsAManifestWrittenByTheAppleApp ./internal/backup
ESL_WRITE_GO_MANIFEST="$work/go.json.deflate" go test -count=1 -run TestWritesWhatTheAppleAppReads ./internal/backup >/dev/null
"$work/compat" read "$work/go.json.deflate" | grep -q '^ok ' && echo "Apple app reads Go manifests: ok"
