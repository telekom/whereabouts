#!/bin/bash
# Copyright 2026 Deutsche Telekom AG
# SPDX-License-Identifier: Apache-2.0

set -euo pipefail

GITHUB_TAG=${GITHUB_TAG:-}
IMAGE_NAME=${IMAGE_NAME:-}
# commit the release image must be built from (org.opencontainers.image.revision)
EXPECTED_REVISION=${EXPECTED_REVISION:-}
WAIT_FOR_RELEASE_IMAGE_TIMEOUT=${WAIT_FOR_RELEASE_IMAGE_TIMEOUT:-1800}
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

if [[ ! "${EXPECTED_REVISION}" =~ ^[0-9a-f]{40}$ ]]; then
    echo "ERROR: EXPECTED_REVISION must be a full git commit SHA, got '${EXPECTED_REVISION}'"
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

for cmd in docker jq; do
    if ! command -v "${cmd}" >/dev/null 2>&1; then
        echo "ERROR: ${cmd} must be installed"
        exit 1
    fi
done

bash "${BASE}/hack/release/validate-tag.sh" "${GITHUB_TAG}"

image_ref="${IMAGE_NAME}:${GITHUB_TAG}"
deadline=$((SECONDS + WAIT_FOR_RELEASE_IMAGE_TIMEOUT))
attempt=1

while true; do
    # The tag may already point at an older image (re-pushed git tag, re-run),
    # so only accept the multi-arch index whose platform images were all built
    # from EXPECTED_REVISION; anything else is treated as not yet available.
    if inspect_json=$(docker buildx imagetools inspect "${image_ref}" --format '{{json .}}' 2>/dev/null); then
        media_type=$(jq -r '.manifest.mediaType // ""' <<< "${inspect_json}")
        digest=$(jq -r '.manifest.digest // ""' <<< "${inspect_json}")
        revisions=""
        case "${media_type}" in
            application/vnd.oci.image.index.v1+json|application/vnd.docker.distribution.manifest.list.v2+json)
                revisions=$(jq -r '[.image // {} | .[] | .config.Labels["org.opencontainers.image.revision"] // ""] | unique | join(" ")' <<< "${inspect_json}")
                ;;
        esac
        if [ "${revisions}" = "${EXPECTED_REVISION}" ]; then
            if [[ ! "${digest}" =~ ^sha256:[0-9a-f]{64}$ ]]; then
                echo "ERROR: could not resolve a sha256 digest for ${image_ref} (got '${digest}')"
                exit 1
            fi
            echo "Release image is available: ${image_ref}@${digest} (revision ${EXPECTED_REVISION})"
            if [ -n "${GITHUB_OUTPUT:-}" ]; then
                echo "digest=${digest}" >> "${GITHUB_OUTPUT}"
            fi
            exit 0
        fi
        echo "Release image ${image_ref} is not the multi-arch build of ${EXPECTED_REVISION} yet (media type '${media_type}', revisions '${revisions}')"
    fi

    if (( SECONDS >= deadline )); then
        echo "ERROR: timed out waiting for release image ${image_ref} built from ${EXPECTED_REVISION}"
        exit 1
    fi

    echo "Release image is not available yet: ${image_ref} (attempt ${attempt}); retrying in ${WAIT_FOR_RELEASE_IMAGE_INTERVAL}s"
    sleep "${WAIT_FOR_RELEASE_IMAGE_INTERVAL}"
    attempt=$((attempt + 1))
done
