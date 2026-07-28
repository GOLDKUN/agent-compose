#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
WORKFLOW="$ROOT_DIR/.github/workflows/v2-storage-migrator.yml"
BUILDER="$ROOT_DIR/scripts/build-v2-storage-migrator-binaries.sh"

fail() {
  printf 'test-v2-storage-migrator-ci-contract: %s\n' "$*" >&2
  exit 1
}

[[ -f "$WORKFLOW" ]] || fail 'manual publishing workflow is missing'
[[ -x "$BUILDER" ]] || fail 'binary builder is missing or not executable'

workflow_source=$(<"$WORKFLOW")
grep -Eq '^[[:space:]]*workflow_dispatch:' <<<"$workflow_source" || fail 'workflow is not manually dispatchable'
if grep -Eq '^[[:space:]]*(push|pull_request|schedule):' <<<"$workflow_source"; then
  fail 'migrator publishing workflow must only run manually'
fi
grep -Eq '^[[:space:]]*contents:[[:space:]]+write[[:space:]]*$' <<<"$workflow_source" || fail 'contents write permission is missing'
grep -Eq '^[[:space:]]*release_tag:' <<<"$workflow_source" || fail 'release tag input is missing'
grep -Fq 'go test ./cmd/agent-compose-migrate/...' <<<"$workflow_source" || fail 'focused migrator tests are missing'
grep -Fq './scripts/build-v2-storage-migrator-binaries.sh ./upload-v2-storage-migrator' <<<"$workflow_source" || fail 'shared binary builder is not used'
grep -Fq 'gh release upload' <<<"$workflow_source" || fail 'existing release upload is missing'
grep -Fq 'gh release create' <<<"$workflow_source" || fail 'new release creation is missing'
grep -Fq -- '--prerelease' <<<"$workflow_source" || fail 'migrator release must be a prerelease'
grep -Fq -- '--clobber' <<<"$workflow_source" || fail 'manually republished assets are not replaceable'

for asset in \
  agent-compose-v2-storage-migrator-linux-amd64 \
  agent-compose-v2-storage-migrator-linux-arm64; do
  grep -Fq "$asset" "$BUILDER" || fail "$asset is missing from the builder"
  grep -Fq "$asset" "$WORKFLOW" || fail "$asset is not verified by the workflow"
done
grep -Fq 'SHASUMS256.txt' "$BUILDER" || fail 'checksum manifest is missing from the builder'
grep -Fq 'sha256sum -c SHASUMS256.txt' <<<"$workflow_source" || fail 'checksum verification is missing'

for daemon_file in Dockerfile Dockerfile.agent-compose-local scripts/verify-agent-compose-image.sh; do
  if grep -Fq 'agent-compose-migrate' "$ROOT_DIR/$daemon_file"; then
    fail "$daemon_file must not include the transitional migrator"
  fi
done

build_deps=$(awk '
  /^  build:$/ { in_build = 1; next }
  in_build && /^  [[:alnum:]_-]+:$/ { exit }
  in_build && /deps:/ { print }
' "$ROOT_DIR/Taskfile.yml")
if grep -Fq 'build:migrator' <<<"$build_deps"; then
  fail 'default build must not include the transitional migrator'
fi
grep -Fq '  build:migrator:' "$ROOT_DIR/Taskfile.yml" || fail 'explicit standalone migrator build is missing'

printf 'test-v2-storage-migrator-ci-contract: all checks passed\n'
