#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Deutsche Telekom AG
# SPDX-License-Identifier: Apache-2.0

set -euo pipefail

# This deliberately removes certificates; never run against a shared cluster.
if [[ "$(kubectl config current-context)" != "kind-whereabouts" ]]; then
  echo "certificate lifecycle test requires the isolated kind-whereabouts cluster" >&2
  exit 1
fi

namespace=kube-system
deployment=whereabouts-controller-manager
secret=whereabouts-webhook-cert
webhooks=whereabouts-validating-webhook-configuration
selector=control-plane=controller-manager
original_replicas=$(kubectl -n "$namespace" get deployment "$deployment" -o jsonpath='{.spec.replicas}')
trap 'kubectl -n "$namespace" scale deployment "$deployment" --replicas="$original_replicas"' EXIT

kubectl -n "$namespace" scale deployment "$deployment" --replicas=0
kubectl -n "$namespace" wait --for=delete pod -l "$selector" --timeout=120s
kubectl -n "$namespace" delete secret "$secret"
if kubectl -n "$namespace" get secret "$secret" >/dev/null 2>&1; then
  echo "Secret must be absent before bootstrap" >&2
  exit 1
fi
kubectl -n "$namespace" scale deployment "$deployment" --replicas=2
kubectl -n "$namespace" rollout status deployment "$deployment" --timeout=300s
kubectl -n "$namespace" wait --for=condition=ready pod -l "$selector" --timeout=300s
ready=$(kubectl -n "$namespace" get deployment "$deployment" -o jsonpath='{.status.readyReplicas}')
[[ "$ready" == 2 ]]

ca=$(kubectl -n "$namespace" get secret "$secret" -o jsonpath='{.data.ca\.crt}')
kubectl get validatingwebhookconfiguration "$webhooks" -o json |
  jq -e --arg ca "$ca" '(.webhooks | length) > 0 and all(.webhooks[]; .clientConfig.caBundle == $ca)'
old_cert=$(kubectl -n "$namespace" get secret "$secret" -o jsonpath='{.data.tls\.crt}')
restart_count() {
  kubectl -n "$namespace" get pod -l "$selector" -o json |
    jq '[.items[].status.containerStatuses[]?.restartCount] | add // 0'
}
before=$(restart_count)
# Secret watch events trigger an immediate refresh. Invalidate only the
# leaf/key and require a rotator-driven restart, not a manual rollout.
kubectl -n "$namespace" patch secret "$secret" --type=merge -p '{"data":{"tls.crt":"","tls.key":""}}'
deadline=$((SECONDS + 300))
while (( SECONDS < deadline )); do
  new_cert=$(kubectl -n "$namespace" get secret "$secret" -o jsonpath='{.data.tls\.crt}')
  if [[ -n "$new_cert" && "$new_cert" != "$old_cert" ]] && (( $(restart_count) > before )); then
    kubectl -n "$namespace" wait --for=condition=ready pod -l "$selector" --timeout=300s
    echo "absent-Secret bootstrap, two-replica readiness, CA injection and restart-on-refresh passed"
    exit 0
  fi
  sleep 2
done
echo "certificate refresh did not regenerate the leaf and restart a replica" >&2
exit 1
