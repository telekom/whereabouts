#!/usr/bin/env bash
set -u

OUTPUT_DIR="${1:-/tmp/e2e-diagnostics}"
mkdir -p "${OUTPUT_DIR}"

capture() {
  local name="$1"
  shift
  local output="${OUTPUT_DIR}/${name}.txt"

  {
    echo "=== ${name} ==="
    "$@"
  } 2>&1 | tee "${output}" || true
}

capture_shell() {
  local name="$1"
  shift
  capture "${name}" bash -c "$*"
}

capture nodes "kubectl" get nodes -o wide
capture pods-all "kubectl" get pods -A -o wide
capture whereabouts-daemonset-pods "kubectl" -n kube-system get pods -l name=whereabouts -o wide
capture whereabouts-operator-pods "kubectl" -n kube-system get pods -l control-plane=controller-manager -o wide
capture ippools "kubectl" get ippools.whereabouts.cni.cncf.io -A -o yaml
capture nodeslicepools "kubectl" get nodeslicepools.whereabouts.cni.cncf.io -A -o yaml
capture overlappingrangeipreservations "kubectl" get overlappingrangeipreservations.whereabouts.cni.cncf.io -A -o yaml
capture network-attachment-definitions "kubectl" get net-attach-def -A -o yaml
capture_shell events "kubectl get events -A --sort-by='.lastTimestamp' | tail -100"
capture_shell pod-descriptions "kubectl describe pods -A"

pods="$(
  kubectl -n kube-system get pods -l name=whereabouts -o name 2>/dev/null || true
  kubectl -n kube-system get pods -l control-plane=controller-manager -o name 2>/dev/null || true
)"

mkdir -p "${OUTPUT_DIR}/pod-logs"
for pod in ${pods}; do
  safe_name="${pod//\//-}"
  {
    echo "=== ${pod} ==="
    kubectl -n kube-system logs "${pod}" --all-containers --tail=500
  } >"${OUTPUT_DIR}/pod-logs/${safe_name}.log" 2>&1 || true
done

if command -v kind >/dev/null 2>&1; then
  kind export logs "${OUTPUT_DIR}/kind-logs" --name whereabouts || true
fi
