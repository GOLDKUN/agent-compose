#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
GUEST_IMAGE_DIR=${GUEST_IMAGE_DIR:-$ROOT_DIR/guest-images}
GUEST_IMAGE_DOCKERFILE=${GUEST_IMAGE_DOCKERFILE:-$GUEST_IMAGE_DIR/Dockerfile.agent-compose-guest}
IMAGE_TAG=${IMAGE_TAG:-agent-compose-guest:latest}

build_args=(
  -f "$GUEST_IMAGE_DOCKERFILE"
  -t "$IMAGE_TAG"
)

if [[ -n ${BUILD_PLATFORM:-} ]]; then
  build_args+=(--platform "$BUILD_PLATFORM")
fi

append_build_arg() {
  local name=$1
  local value=$2
  if [[ -n "$value" ]]; then
    build_args+=(--build-arg "$name=$value")
  fi
}

append_build_arg REGISTRY_MIRROR "${REGISTRY_MIRROR:-}"
append_build_arg GOPROXY "${GOPROXY:-}"
append_build_arg GO_VERSION "${GO_VERSION:-}"
append_build_arg GRPCURL_VERSION "${GRPCURL_VERSION:-}"
append_build_arg PYPI_INDEX_URL "${PYPI_INDEX_URL:-}"
append_build_arg PYPI_TRUSTED_HOST "${PYPI_TRUSTED_HOST:-}"
append_build_arg ARCHLINUX_TAG "${ARCHLINUX_TAG:-}"
append_build_arg ARCHLINUX_MIRROR "${ARCHLINUX_MIRROR:-}"
append_build_arg CODEX_VERSION "${CODEX_VERSION:-}"
append_build_arg CLAUDE_CODE_VERSION "${CLAUDE_CODE_VERSION:-}"
append_build_arg GEMINI_CLI_VERSION "${GEMINI_CLI_VERSION:-}"
append_build_arg OPENCODE_VERSION "${OPENCODE_VERSION:-}"
append_build_arg PI_AGENT_VERSION "${PI_AGENT_VERSION:-}"
append_build_arg PI_MCP_ADAPTER_VERSION "${PI_MCP_ADAPTER_VERSION:-}"

docker build "${build_args[@]}" "$ROOT_DIR"

echo "Built guest image: $IMAGE_TAG"
