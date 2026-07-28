#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/../.."

generated_files=(
  proto/agentcompose/v2/agentcompose.pb.go
  proto/health/v1/health.pb.go
)

for generated_file in "${generated_files[@]}"; do
  if [[ ! -s "$generated_file" ]]; then
    echo "generated protobuf source is missing: $generated_file" >&2
    exit 1
  fi
  if ! git check-ignore --quiet "$generated_file"; then
    echo "generated protobuf source is not ignored: $generated_file" >&2
    exit 1
  fi
  if git ls-files --error-unmatch "$generated_file" >/dev/null 2>&1; then
    echo "generated protobuf source must not be tracked: $generated_file" >&2
    exit 1
  fi
done

echo "generated protobuf contract passed"
