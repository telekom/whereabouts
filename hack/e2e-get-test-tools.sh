#!/bin/bash
set -o errexit
# ensure this file is sourced to add required components to PATH

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd "$here/.." && pwd)"
VERSION="v0.32.0"
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m | sed 's/x86_64/amd64/' | sed 's/aarch64/arm64/')
KIND_BINARY_URL="https://github.com/kubernetes-sigs/kind/releases/download/${VERSION}/kind-${OS}-${ARCH}"
K8_STABLE_RELEASE_URL="https://dl.k8s.io/release/stable.txt"

if [ ! -d "${root}/bin" ]; then
    mkdir "${root}/bin"
fi

echo "retrieving kind"
curl -fL --max-time 10 --retry 10 --retry-delay 5 --retry-max-time 60 -o "${root}/bin/kind" "${KIND_BINARY_URL}"
chmod +x "${root}/bin/kind"

echo "retrieving kubectl"
K8_RELEASE="$(curl -fsSL --max-time 10 --retry 10 --retry-delay 5 --retry-max-time 60 "${K8_STABLE_RELEASE_URL}")"
KUBECTL_BASE_URL="https://dl.k8s.io/release/${K8_RELEASE}/bin/${OS}/${ARCH}"
curl -fL --max-time 10 --retry 10 --retry-delay 5 --retry-max-time 60 -o "${root}/bin/kubectl" "${KUBECTL_BASE_URL}/kubectl"
curl -fL --max-time 10 --retry 10 --retry-delay 5 --retry-max-time 60 -o "${root}/bin/kubectl.sha256" "${KUBECTL_BASE_URL}/kubectl.sha256"
if command -v sha256sum >/dev/null 2>&1; then
    (cd "${root}/bin" && echo "$(cat kubectl.sha256)  kubectl" | sha256sum --check)
elif command -v shasum >/dev/null 2>&1; then
    expected="$(cat "${root}/bin/kubectl.sha256")"
    actual="$(shasum -a 256 "${root}/bin/kubectl" | awk '{print $1}')"
    if [ "${actual}" != "${expected}" ]; then
        echo "kubectl checksum verification failed" >&2
        exit 1
    fi
else
    echo "missing sha256sum or shasum for kubectl checksum verification" >&2
    exit 1
fi
rm -f "${root}/bin/kubectl.sha256"
chmod +x "${root}/bin/kubectl"

export PATH="$PATH:$root/bin"
