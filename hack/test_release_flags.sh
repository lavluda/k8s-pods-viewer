#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

expect_success() {
  local description="$1"
  shift

  echo "PASS: $description"
  "$@" >/dev/null
}

expect_failure_contains() {
  local description="$1"
  local expected="$2"
  shift 2

  echo "PASS: $description"
  set +e
  local output
  output="$("$@" 2>&1)"
  local status=$?
  set -e

  if [[ $status -eq 0 ]]; then
    echo "expected failure but command succeeded: $description" >&2
    exit 1
  fi
  if [[ "$output" != *"$expected"* ]]; then
    echo "expected output to contain: $expected" >&2
    echo "$output" >&2
    exit 1
  fi
}

go test ./cmd/... ./pkg/...
go test -cover ./cmd/k8s-pods-viewer >/dev/null

expect_success "help output" go run ./cmd/k8s-pods-viewer --help
expect_success "version output" go run ./cmd/k8s-pods-viewer -version
expect_success "attribution output" go run ./cmd/k8s-pods-viewer -attribution

expect_success "version with combined parse-only flags" \
  go run ./cmd/k8s-pods-viewer \
  -version \
  --context demo \
  --namespace production \
  --node-selector role=worker \
  --pod-selector app=api \
  --pod-sort creation=asc \
  --resources memory,cpu \
  --style '#04B575,#FFFF00,#FF0000'

expect_failure_contains "invalid style rejected before client startup" "creating style:" \
  go run ./cmd/k8s-pods-viewer \
  --kubeconfig /tmp/does-not-exist \
  --style '#04B575,#FFFF00'

expect_failure_contains "invalid node selector rejected before client startup" "parsing node selector:" \
  go run ./cmd/k8s-pods-viewer \
  --kubeconfig /tmp/does-not-exist \
  --style '#04B575,#FFFF00,#FF0000' \
  --node-selector 'bad selector'

expect_failure_contains "invalid pod selector rejected before client startup" "parsing pod selector:" \
  go run ./cmd/k8s-pods-viewer \
  --kubeconfig /tmp/does-not-exist \
  --style '#04B575,#FFFF00,#FF0000' \
  --pod-selector 'bad selector'

expect_failure_contains "valid runtime flag combination reaches client creation" "creating client" \
  go run ./cmd/k8s-pods-viewer \
  --kubeconfig /tmp/does-not-exist \
  --context demo \
  --namespace production \
  --node-selector role=worker \
  --pod-selector app=api \
  --pod-sort memory=dsc \
  --resources cpu,memory \
  --style '#04B575,#FFFF00,#FF0000'

echo "release flag checks completed successfully"
