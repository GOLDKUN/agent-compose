#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
CHART_DIR="$ROOT_DIR/charts/agent-compose"

fail() {
  printf 'test-k8s-manifests: %s\n' "$*" >&2
  exit 1
}

assert_contains() {
  local file=$1
  local pattern=$2
  grep -Fq -- "$pattern" "$file" || fail "$file does not contain: $pattern"
}

for file in \
  Chart.yaml values.yaml values.schema.json README.md \
  templates/_helpers.tpl templates/serviceaccount.yaml \
  templates/clusterrole.yaml templates/clusterrolebinding.yaml \
  templates/pvc.yaml templates/service.yaml templates/deployment.yaml \
  templates/NOTES.txt; do
  [[ -f "$CHART_DIR/$file" ]] || fail "missing charts/agent-compose/$file"
done

assert_contains "$CHART_DIR/Chart.yaml" 'apiVersion: v2'
assert_contains "$CHART_DIR/Chart.yaml" 'name: agent-compose'
assert_contains "$CHART_DIR/values.yaml" 'sandboxNamespace: ""'
assert_contains "$CHART_DIR/templates/_helpers.tpl" '.Release.Namespace'
assert_contains "$CHART_DIR/templates/clusterrolebinding.yaml" 'namespace: {{ .Release.Namespace }}'
assert_contains "$CHART_DIR/templates/pvc.yaml" 'helm.sh/resource-policy: keep'
assert_contains "$CHART_DIR/README.md" '--kube-context prod-cluster'

if grep -R -n -E 'hostPath:|privileged:|/dev/kvm' "$CHART_DIR/templates" >/dev/null; then
  fail 'in-cluster daemon chart templates must not add sandbox hostPath, privilege, or KVM requirements'
fi

if command -v helm >/dev/null 2>&1; then
  rendered=$(mktemp)
  trap 'rm -f "$rendered"' EXIT
  helm lint "$CHART_DIR" >/dev/null \
    || fail 'helm lint rejected charts/agent-compose'
  helm template agent-compose "$CHART_DIR" --namespace team-a >"$rendered" \
    || fail 'helm template rejected charts/agent-compose'
  assert_contains "$rendered" 'value: "team-a"'
  assert_contains "$rendered" 'value: "http://agent-compose.team-a.svc.cluster.local:7410"'
  assert_contains "$rendered" 'namespace: team-a'
fi

printf 'Kubernetes deployment manifests passed\n'
