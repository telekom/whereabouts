#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT}"

fail() {
  echo "$1" >&2
  exit 1
}

if grep -Fq "IMG=" README.md; then
  fail "README.md must not document make deploy IMG=...; deploy does not consume IMG"
fi

if grep -Fq "<WHEREABOUTS_VERSION>" README.md; then
  fail "README.md must use <CHART_VERSION> for Helm chart versions"
fi

grep -Fq "<CHART_VERSION>" README.md || fail "README.md must document Helm <CHART_VERSION>"
grep -Fq "operator.serviceCIDRs" README.md || fail "README.md must document operator.serviceCIDRs"
grep -Fq -- "--service-cidr" README.md || fail "README.md must document --service-cidr"
grep -Fq "ServiceCIDROverlap" README.md || fail "README.md must document ServiceCIDROverlap warning events"

bad_env_refs="$(grep -RInF --exclude-dir=.git --exclude-dir=vendor -- "-f ../e2e/.env" README.md CONTRIBUTING.md doc .github CLAUDE.md 2>/dev/null || true)"
if [ -n "${bad_env_refs}" ]; then
  echo "${bad_env_refs}" >&2
  fail "NodeSlice E2E docs must use ../../e2e/.env from e2e/e2e_node_slice"
fi

grep -Fq "helm lint deployment/whereabouts-chart" .github/workflows/chart-push-release.yml ||
  fail "release chart workflow must lint after chart-prepare-release"
grep -Fq "helm template whereabouts deployment/whereabouts-chart" .github/workflows/chart-push-release.yml ||
  fail "release chart workflow must template after chart-prepare-release"
grep -Fq "helm template whereabouts deployment/whereabouts-chart" .github/workflows/build.yml ||
  fail "build workflow must template the Helm chart"

helm_matrix="$(awk '/^  e2e-helm:/,/^    env:/' .github/workflows/test.yml)"
echo "${helm_matrix}" | grep -Fq "suite: node-slice" ||
  fail "Helm E2E matrix must include NodeSlice coverage"
echo "${helm_matrix}" | grep -Fq './e2e_node_slice/...' ||
  fail "Helm NodeSlice E2E coverage must run ./e2e_node_slice/..."
