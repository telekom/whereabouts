#!/bin/bash
# Copyright 2026 Deutsche Telekom AG
# SPDX-License-Identifier: Apache-2.0

set -euo pipefail

tag="${1:-}"

if [ -z "${tag}" ]; then
    echo "ERROR: release tag must be provided"
    exit 1
fi

if [[ ! "${tag}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?$ ]]; then
    echo "ERROR: release tag must match vMAJOR.MINOR.PATCH or vMAJOR.MINOR.PATCH-PRERELEASE"
    exit 1
fi
