#!/bin/bash
set -o errexit
# ensure this file is sourced to add required components to PATH

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd "$here/.." && pwd)"
KIND_VERSION="v0.32.0"
KUBECTL_VERSION="v1.36.2"
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m | sed 's/x86_64/amd64/' | sed 's/aarch64/arm64/')
KIND_BINARY_URL="https://github.com/kubernetes-sigs/kind/releases/download/${KIND_VERSION}/kind-${OS}-${ARCH}"
KIND_CHECKSUM_URL="${KIND_BINARY_URL}.sha256sum"
KUBECTL_BASE_URL="https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/${OS}/${ARCH}"

verify_sha256() {
    local binary_path="$1"
    local checksum_path="$2"
    local label="$3"
    local expected
    local actual

    expected="$(awk '{print $1}' "${checksum_path}")"
    if command -v sha256sum >/dev/null 2>&1; then
        actual="$(sha256sum "${binary_path}" | awk '{print $1}')"
    elif command -v shasum >/dev/null 2>&1; then
        actual="$(shasum -a 256 "${binary_path}" | awk '{print $1}')"
    else
        echo "missing sha256sum or shasum for ${label} checksum verification" >&2
        exit 1
    fi
    if [ "${actual}" != "${expected}" ]; then
        echo "${label} checksum verification failed" >&2
        exit 1
    fi
}

if [ ! -d "${root}/bin" ]; then
    mkdir "${root}/bin"
fi

echo "retrieving kind"
curl -fL --max-time 10 --retry 10 --retry-delay 5 --retry-max-time 60 -o "${root}/bin/kind" "${KIND_BINARY_URL}"
curl -fL --max-time 10 --retry 10 --retry-delay 5 --retry-max-time 60 -o "${root}/bin/kind.sha256" "${KIND_CHECKSUM_URL}"
verify_sha256 "${root}/bin/kind" "${root}/bin/kind.sha256" "kind"
rm -f "${root}/bin/kind.sha256"
chmod +x "${root}/bin/kind"

echo "retrieving kubectl"
curl -fL --max-time 10 --retry 10 --retry-delay 5 --retry-max-time 60 -o "${root}/bin/kubectl" "${KUBECTL_BASE_URL}/kubectl"
curl -fL --max-time 10 --retry 10 --retry-delay 5 --retry-max-time 60 -o "${root}/bin/kubectl.sha256" "${KUBECTL_BASE_URL}/kubectl.sha256"
verify_sha256 "${root}/bin/kubectl" "${root}/bin/kubectl.sha256" "kubectl"
rm -f "${root}/bin/kubectl.sha256"
chmod +x "${root}/bin/kubectl"

export PATH="$PATH:$root/bin"
