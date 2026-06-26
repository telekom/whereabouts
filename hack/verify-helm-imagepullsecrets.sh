#!/usr/bin/env bash
set -euo pipefail

tmpdir="$(mktemp -d)"
trap 'rm -rf "${tmpdir}"' EXIT

helm template whereabouts deployment/whereabouts-chart \
  --namespace kube-system \
  --set imagePullSecrets[0].name=registry-credentials \
  --show-only templates/daemonset.yaml \
  > "${tmpdir}/daemonset.yaml"

helm template whereabouts deployment/whereabouts-chart \
  --namespace kube-system \
  --set imagePullSecrets[0].name=registry-credentials \
  --show-only templates/operator.yaml \
  > "${tmpdir}/operator.yaml"

grep -q "imagePullSecrets:" "${tmpdir}/daemonset.yaml"
grep -q "name: registry-credentials" "${tmpdir}/daemonset.yaml"
grep -q "imagePullSecrets:" "${tmpdir}/operator.yaml"
grep -q "name: registry-credentials" "${tmpdir}/operator.yaml"
