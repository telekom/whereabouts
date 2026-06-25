#!/bin/bash
# Copyright 2026 Deutsche Telekom AG
# SPDX-License-Identifier: Apache-2.0

set -euo pipefail

GITHUB_TAG=${GITHUB_TAG:-}
IMAGE_NAME=${IMAGE_NAME:-}
WAIT_FOR_RELEASE_IMAGE_TIMEOUT=${WAIT_FOR_RELEASE_IMAGE_TIMEOUT:-900}
WAIT_FOR_RELEASE_IMAGE_INTERVAL=${WAIT_FOR_RELEASE_IMAGE_INTERVAL:-15}

BASE=${PWD}

if [ -z "${GITHUB_TAG}" ]; then
    echo "ERROR: GITHUB_TAG must be provided as env var"
    exit 1
fi

if [ -z "${IMAGE_NAME}" ]; then
    echo "ERROR: IMAGE_NAME must be provided as env var"
    exit 1
fi

if [[ ! "${WAIT_FOR_RELEASE_IMAGE_TIMEOUT}" =~ ^[0-9]+$ ]]; then
    echo "ERROR: WAIT_FOR_RELEASE_IMAGE_TIMEOUT must be a non-negative integer"
    exit 1
fi

if [[ ! "${WAIT_FOR_RELEASE_IMAGE_INTERVAL}" =~ ^[0-9]+$ ]]; then
    echo "ERROR: WAIT_FOR_RELEASE_IMAGE_INTERVAL must be a non-negative integer"
    exit 1
fi

if ! command -v docker >/dev/null 2>&1; then
    echo "ERROR: docker must be installed"
    exit 1
fi

bash "${BASE}/hack/release/validate-tag.sh" "${GITHUB_TAG}"

image_ref="${IMAGE_NAME}:${GITHUB_TAG}"
deadline=$((SECONDS + WAIT_FOR_RELEASE_IMAGE_TIMEOUT))
attempt=1

while true; do
    if docker buildx imagetools inspect "${image_ref}" >/dev/null 2>&1; then
        echo "Release image is available: ${image_ref}"
        exit 0
    fi

    if (( SECONDS >= deadline )); then
        echo "ERROR: timed out waiting for release image ${image_ref}"
        exit 1
    fi

    echo "Release image is not available yet: ${image_ref} (attempt ${attempt}); retrying in ${WAIT_FOR_RELEASE_IMAGE_INTERVAL}s"
    sleep "${WAIT_FOR_RELEASE_IMAGE_INTERVAL}"
    attempt=$((attempt + 1))
done
