#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
OUTPUT_DIR=${1:-"$ROOT_DIR/upload-v2-storage-migrator"}

[[ ! -e "$OUTPUT_DIR" && ! -L "$OUTPUT_DIR" ]] \
  || { printf 'build-v2-storage-migrator-binaries: output path already exists: %s\n' "$OUTPUT_DIR" >&2; exit 1; }
command -v go >/dev/null 2>&1 || { printf 'build-v2-storage-migrator-binaries: go is required\n' >&2; exit 1; }
command -v sha256sum >/dev/null 2>&1 || { printf 'build-v2-storage-migrator-binaries: sha256sum is required\n' >&2; exit 1; }

mkdir -p -- "$(dirname -- "$OUTPUT_DIR")"
OUTPUT_DIR=$(cd -- "$(dirname -- "$OUTPUT_DIR")" && pwd -P)/$(basename -- "$OUTPUT_DIR")
WORK_DIR=$(mktemp -d "$(dirname -- "$OUTPUT_DIR")/.v2-storage-migrator-binaries.XXXXXX")
cleanup() {
  rm -rf -- "$WORK_DIR"
}
trap cleanup EXIT

for arch in amd64 arm64; do
  asset="agent-compose-v2-storage-migrator-linux-$arch"
  CGO_ENABLED=0 GOOS=linux GOARCH="$arch" \
    go -C "$ROOT_DIR" build -trimpath -ldflags '-s -w' \
    -o "$WORK_DIR/$asset" ./cmd/agent-compose-migrate
  chmod 0755 "$WORK_DIR/$asset"
done
(
  cd "$WORK_DIR"
  sha256sum \
    agent-compose-v2-storage-migrator-linux-amd64 \
    agent-compose-v2-storage-migrator-linux-arm64 >SHASUMS256.txt
)
chmod 0644 "$WORK_DIR/SHASUMS256.txt"
mv -- "$WORK_DIR" "$OUTPUT_DIR"
trap - EXIT
printf 'Built V2 storage migrator release assets in %s\n' "$OUTPUT_DIR"
