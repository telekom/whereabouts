#!/bin/bash
set -euo pipefail

# github repo owner: e.g k8snetworkplumbingwg
GITHUB_REPO_OWNER=${GITHUB_REPO_OWNER:-}
# github api token with package:write permissions
GITHUB_TOKEN=${GITHUB_TOKEN:-}
# github tag e.g v1.2.3
GITHUB_TAG=${GITHUB_TAG:-}

BASE=${PWD}
HELM_CHART=${BASE}/deployment/whereabouts-chart
HELM_CHART_VERSION=${GITHUB_TAG#"v"}
HELM_CHART_TARBALL="whereabouts-chart-${HELM_CHART_VERSION}.tgz"

if ! command -v helm >/dev/null 2>&1; then
    echo "ERROR: helm must be installed"
    exit 1
fi

if [ -z "$GITHUB_REPO_OWNER" ]; then
    echo "ERROR: GITHUB_REPO_OWNER must be provided as env var"
    exit 1
fi

if [ -z "$GITHUB_TOKEN" ]; then
    echo "ERROR: GITHUB_TOKEN must be provided as env var"
    exit 1
fi

if [ -z "$GITHUB_TAG" ]; then
    echo "ERROR: GITHUB_TAG must be provided as env var"
    exit 1
fi

helm package "${HELM_CHART}"
printf '%s' "${GITHUB_TOKEN}" | helm registry login ghcr.io -u "${GITHUB_REPO_OWNER}" --password-stdin
helm push "${HELM_CHART_TARBALL}" "oci://ghcr.io/${GITHUB_REPO_OWNER}"
