#!/bin/bash
set -euo pipefail

# github tag e.g v1.2.3
GITHUB_TAG=${GITHUB_TAG:-}
# github repo owner e.g k8snetworkplumbingwg
GITHUB_REPO_OWNER=${GITHUB_REPO_OWNER:-}

BASE=${PWD}
YQ_CMD="${BASE}/bin/yq"
HELM_VALUES=${BASE}/deployment/whereabouts-chart/values.yaml
HELM_CHART=${BASE}/deployment/whereabouts-chart/Chart.yaml


if [ -z "$GITHUB_TAG" ]; then
    echo "ERROR: GITHUB_TAG must be provided as env var"
    exit 1
fi

if [ -z "$GITHUB_REPO_OWNER" ]; then
    echo "ERROR: GITHUB_REPO_OWNER must be provided as env var"
    exit 1
fi

# tag provided via env var
WHEREABOUTS_TAG=${GITHUB_TAG}

# patch values.yaml in-place

# whereabouts image:
WHEREABOUTS_REPO=${GITHUB_REPO_OWNER} # this is used to allow to release whereabouts from forks
"$YQ_CMD" -i ".image.repository = \"ghcr.io/${WHEREABOUTS_REPO}/whereabouts\"" "$HELM_VALUES"
"$YQ_CMD" -i ".image.tag = \"${WHEREABOUTS_TAG}\"" "$HELM_VALUES"

# patch Chart.yaml in-place
"$YQ_CMD" -i ".version = \"${WHEREABOUTS_TAG#"v"}\"" "$HELM_CHART"
"$YQ_CMD" -i ".appVersion = \"${WHEREABOUTS_TAG}\"" "$HELM_CHART"
