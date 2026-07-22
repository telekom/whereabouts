#!/bin/bash
set -eo pipefail

NUMBER_OF_COMPUTE_NODES=""

while [ $# -gt 0 ]; do
  case "$1" in
    -n|--number-of-compute)
      if [ -z "${2:-}" ]; then
        echo "option '$1' requires an argument: -n <number-of-compute-nodes>" >&2
        exit 1
      fi
      NUMBER_OF_COMPUTE_NODES=$2
      shift 2
      ;;
    --)
      shift
      break
      ;;
    *)
      echo "Usage: $0 -n <number-of-compute-nodes>" >&2
      exit 1
  esac
done

if [ -z "${NUMBER_OF_COMPUTE_NODES:-}" ]; then
  echo "Missing required argument: -n <number-of-compute-nodes>" >&2
  exit 1
fi

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$HERE/.." && pwd)"
MULTUS_DAEMONSET_REF="db30741512795c258b3ba383a571a66850e8baac"
MULTUS_DAEMONSET_URL="https://raw.githubusercontent.com/k8snetworkplumbingwg/multus-cni/${MULTUS_DAEMONSET_REF}/deployments/multus-daemonset.yml"
CNIS_DAEMONSET_PATH="$ROOT/hack/cni-install.yml"
RETRY_MAX=10
INTERVAL=10
TIMEOUT=120
TIMEOUT_K8="${TIMEOUT}s"
KIND_CLUSTER_NAME="whereabouts"
OCI_BIN="${OCI_BIN:-"docker"}"
IMG_PROJECT="whereabouts"
IMG_REGISTRY="ghcr.io/telekom"
IMG_TAG="latest"
IMG_NAME="$IMG_REGISTRY/$IMG_PROJECT:$IMG_TAG"

create_cluster() {
workers="$(for _ in $(seq "$NUMBER_OF_COMPUTE_NODES"); do echo "  - role: worker"; done)"
  # deploy cluster with kind
  cat <<EOF | kind create cluster --name $KIND_CLUSTER_NAME --config=-
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
  - role: control-plane
$workers
EOF
}


check_requirements() {
  for cmd in "$OCI_BIN" kind kubectl; do
    if ! command -v "$cmd" &> /dev/null; then
      echo "$cmd is not available"
      exit 1
    fi
  done
  # Use GNU timeout (gtimeout on macOS via coreutils)
  if command -v timeout &> /dev/null; then
    TIMEOUT_CMD="timeout"
  elif command -v gtimeout &> /dev/null; then
    TIMEOUT_CMD="gtimeout"
  else
    echo "timeout (or gtimeout) is not available; install coreutils"
    exit 1
  fi
}

retry() {
  local status=0
  local retries=${RETRY_MAX:-5}
  local delay=${INTERVAL:-5}
  local to=${TIMEOUT:-20}

  while [ "$retries" -gt 0 ]
  do
    status=0
    printf '+'
    printf ' %q' "$@"
    printf '\n'
    "$TIMEOUT_CMD" "$to" "$@" || status=$?
    if [ $status -eq 0 ]; then
      break;
    fi
    echo "Exit code: '$status'. Sleeping '$delay' seconds before retrying"
    sleep "$delay"
    retries=$((retries - 1))
  done
  return $status
}

echo "## checking requirements"
check_requirements
echo "## delete existing KinD cluster if it exists"
kind delete clusters $KIND_CLUSTER_NAME
echo "## start KinD cluster"
create_cluster
kind export kubeconfig --name $KIND_CLUSTER_NAME
echo "## wait for coreDNS"
kubectl -n kube-system wait --for=condition=available deploy/coredns --timeout=$TIMEOUT_K8
echo "## install multus"
retry kubectl create -f "${MULTUS_DAEMONSET_URL}"
retry kubectl -n kube-system wait --for=condition=ready -l name="multus" pod --timeout=$TIMEOUT_K8
echo "## install CNIs"
retry kubectl create -f "${CNIS_DAEMONSET_PATH}"
retry kubectl -n kube-system wait --for=condition=ready -l name="cni-plugins" pod --timeout=$TIMEOUT_K8
echo "## build whereabouts"
pushd "$ROOT"
$OCI_BIN build --load -t "$IMG_NAME" -f Dockerfile .
popd

echo "## load image into KinD"
trap "rm -f /tmp/whereabouts-img.tar" EXIT
"$OCI_BIN" save -o /tmp/whereabouts-img.tar "$IMG_NAME"
kind load image-archive --name "$KIND_CLUSTER_NAME" /tmp/whereabouts-img.tar

<<<<<<< HEAD
echo "## install CRDs"
for crd in "$ROOT/config/crd/bases/whereabouts.cni.cncf.io_"*.yaml; do
  retry kubectl apply -f "$crd"
done

echo "## install whereabouts"
# Build kustomize output, substitute the locally built image, and add imagePullPolicy: Never
pushd "$ROOT"
make kustomize
rendered_manifest="$(mktemp -t whereabouts-manifest.XXXXXX)"
trap 'rm -f /tmp/whereabouts-img.tar "$rendered_manifest"' EXIT
bin/kustomize build config/default | \
  sed "s|ghcr.io/telekom/whereabouts:[^ \"]*|$IMG_NAME|g" | \
  awk '/^[[:space:]]*image:/{print; indent=substr($0,1,match($0,/image:/)-1); print indent "imagePullPolicy: Never"; next}1' \
  > "$rendered_manifest"
retry kubectl apply -f "$rendered_manifest"
popd
retry kubectl wait -n kube-system --for=condition=ready -l name=whereabouts pod --timeout=$TIMEOUT_K8
retry kubectl wait -n kube-system --for=condition=ready -l control-plane=controller-manager pod --timeout=$TIMEOUT_K8
=======
echo "## install whereabouts"
for file in "daemonset-install.yaml" "whereabouts.cni.cncf.io_ippools.yaml" "whereabouts.cni.cncf.io_overlappingrangeipreservations.yaml" "whereabouts.cni.cncf.io_nodeslicepools.yaml" "reconciler-deployment.yaml"; do
  # insert 'imagePullPolicy: Never' under the container 'image' so it is certain that the image used
  # by the daemonset is the one loaded into KinD and not one pulled from a repo
  sed '/        image:/a\        imagePullPolicy: Never' "$ROOT/doc/crds/$file" | retry kubectl apply -f -
done
# deployment has an extra tab for the sed so doing out of the loop
sed '/          image:/a\          imagePullPolicy: Never' "$ROOT/doc/crds/node-slice-controller.yaml" | retry kubectl apply -f -
retry kubectl wait -n kube-system --for=condition=ready -l app=whereabouts pod --timeout=$TIMEOUT_K8
retry kubectl wait -n kube-system --for=condition=ready -l app=whereabouts-controller pod --timeout=$TIMEOUT_K8
retry kubectl wait -n kube-system --for=condition=ready -l app=whereabouts-reconciler pod --timeout=$TIMEOUT_K8
>>>>>>> upstream/master
echo "## done"
