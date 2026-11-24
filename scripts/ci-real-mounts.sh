#!/usr/bin/env bash
# Soren gate for what this Mac can't test: real WinFsp drives on Windows and
# real FUSE mounts on Linux. Passes only if GitHub Actions ran the CI workflow
# for the CURRENT commit and every job (both real-mount end-to-end jobs
# included) succeeded. Push first; it waits for a run in progress.
set -euo pipefail
cd "$(dirname "$0")/.."
sha="$(git rev-parse HEAD)"
if [ -n "$(git status --porcelain --untracked-files=no)" ]; then
  echo "uncommitted changes: CI can't have tested this tree"; exit 1
fi
for i in $(seq 1 90); do
  run="$(gh run list --workflow ci.yml --commit "$sha" --limit 1 --json databaseId,status,conclusion --jq '.[0] // empty')"
  if [ -z "$run" ]; then echo "no CI run for $sha yet (pushed?)"; sleep 20; continue; fi
  status="$(jq -r .status <<<"$run")"; id="$(jq -r .databaseId <<<"$run")"
  if [ "$status" != "completed" ]; then echo "CI run $id: $status…"; sleep 20; continue; fi
  gh run view "$id" --json jobs --jq '.jobs[] | "\(.conclusion)\t\(.name)"'
  [ "$(jq -r .conclusion <<<"$run")" = "success" ] && exit 0 || exit 1
done
echo "timed out waiting for CI"; exit 1
