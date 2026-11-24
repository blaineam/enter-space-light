#!/usr/bin/env bash
# Regenerates internal/rclonecli/commands.go from rclone's cmd/all after an
# rclone upgrade, leaving out selfupdate (it would overwrite esl with rclone).
set -euo pipefail
cd "$(dirname "$0")/.."
ver=$(go list -m -f '{{.Version}}' github.com/rclone/rclone)
go mod download github.com/rclone/rclone
all="$(go env GOMODCACHE)/github.com/rclone/rclone@${ver}/cmd/all/all.go"
{
  printf '// Package rclonecli embeds the full rclone command line as `esl rclone …`.\n//\n'
  printf "// Generated from rclone's cmd/all by scripts/gen-rclonecli.sh, minus\n"
  printf '// selfupdate (it would replace the esl binary with plain rclone).\npackage rclonecli\n\nimport (\n'
  grep '_ "github.com/rclone/rclone/cmd/' "$all" | grep -v selfupdate
  printf ')\n'
} > internal/rclonecli/commands.go
gofmt -w internal/rclonecli/commands.go
echo "regenerated from rclone ${ver}"
