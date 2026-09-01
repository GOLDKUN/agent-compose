#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
WORKFLOW="$ROOT_DIR/.github/workflows/installer.yml"
INSTALLER_MODULE="$ROOT_DIR/cmd/installer/go.mod"

fail() {
  printf 'test-installer-ci-contract: %s\n' "$*" >&2
  exit 1
}

source_text=$(<"$WORKFLOW")
grep -Eq '^[[:space:]]*workflow_dispatch:' <<<"$source_text" || fail 'workflow is not manually dispatchable'
if grep -Eq '^[[:space:]]*(push|pull_request|schedule):' <<<"$source_text"; then
  fail 'installer publishing workflow must only run manually'
fi
grep -Fq './scripts/build-installer-binaries.sh ./upload-installer' <<<"$source_text" || fail 'shared binary builder is not used'
grep -Fq 'RELEASE_TAG: installer-latest' <<<"$source_text" || fail 'fixed installer release tag is missing'
grep -Fq -- '--prerelease' <<<"$source_text" || fail 'installer release must remain a prerelease'
grep -Fq -- '--clobber' <<<"$source_text" || fail 'existing fixed release assets are not replaceable'
grep -Fq 'agent-compose-installer-linux-amd64' "$ROOT_DIR/scripts/build-installer-binaries.sh" || fail 'amd64 asset is missing'
grep -Fq 'agent-compose-installer-linux-arm64' "$ROOT_DIR/scripts/build-installer-binaries.sh" || fail 'arm64 asset is missing'
[[ -f "$INSTALLER_MODULE" ]] || fail 'standalone installer module is missing'
grep -Fq 'module github.com/chaitin/agent-compose/cmd/installer' "$INSTALLER_MODULE" || fail 'installer module path is incorrect'
grep -Fq 'go -C "$ROOT_DIR/cmd/installer" build' "$ROOT_DIR/scripts/build-installer-binaries.sh" || fail 'binary builder does not use the installer module'
grep -Fq 'go -C cmd/installer test ./...' "$WORKFLOW" || fail 'publishing workflow does not test the installer module'
if grep -Fq 'github.com/charmbracelet/' "$ROOT_DIR/go.mod"; then
  fail 'installer TUI dependencies leaked into the root module'
fi

printf 'test-installer-ci-contract: all checks passed\n'
