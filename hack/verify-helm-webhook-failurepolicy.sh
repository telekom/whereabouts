#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CHART_DIR="${ROOT}/deployment/whereabouts-chart"

rendered="$(mktemp)"
trap 'rm -f "${rendered}"' EXIT

helm template whereabouts "${CHART_DIR}" --namespace kube-system >"${rendered}"

webhook_count="$(awk '
  /^kind: ValidatingWebhookConfiguration$/ { in_vwc = 1; next }
  in_vwc && /^kind:/ { in_vwc = 0 }
  in_vwc && /^- admissionReviewVersions:/ { count++ }
  END { print count + 0 }
' "${rendered}")"

if [ "${webhook_count}" -ne 3 ]; then
  echo "expected 3 validating webhooks, rendered ${webhook_count}" >&2
  exit 1
fi

failure_policy_count="$(awk '
  /^kind: ValidatingWebhookConfiguration$/ { in_vwc = 1; next }
  in_vwc && /^kind:/ { in_vwc = 0 }
  in_vwc && /^[[:space:]]*failurePolicy:/ { count++ }
  END { print count + 0 }
' "${rendered}")"

ignore_count="$(awk '
  /^kind: ValidatingWebhookConfiguration$/ { in_vwc = 1; next }
  in_vwc && /^kind:/ { in_vwc = 0 }
  in_vwc && /^[[:space:]]*failurePolicy:[[:space:]]+Ignore$/ { count++ }
  END { print count + 0 }
' "${rendered}")"

if [ "${failure_policy_count}" -ne "${webhook_count}" ] || [ "${ignore_count}" -ne "${webhook_count}" ]; then
  echo "all validating webhooks must render failurePolicy: Ignore by default" >&2
  awk '
    /^kind: ValidatingWebhookConfiguration$/ { in_vwc = 1; next }
    in_vwc && /^kind:/ { in_vwc = 0 }
    in_vwc && /^[[:space:]]*failurePolicy:/ { print }
  ' "${rendered}" >&2
  exit 1
fi
