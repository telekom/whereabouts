#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CHART_DIR="${ROOT}/deployment/whereabouts-chart"

rendered="$(mktemp)"
trap 'rm -f "${rendered}"' EXIT

helm template whereabouts "${CHART_DIR}" --namespace kube-system >"${rendered}"

if grep -Fq -- "--service-cidr=" "${rendered}"; then
  echo "default chart values must not render --service-cidr" >&2
  grep -F -- "--service-cidr=" "${rendered}" >&2
  exit 1
fi

helm template whereabouts "${CHART_DIR}" --namespace kube-system \
  --set 'operator.serviceCIDRs[0]=10.96.0.0/12' \
  --set 'operator.serviceCIDRs[1]=fd00:1234::/108' \
  >"${rendered}"

service_cidr_count="$(grep -Fc -- "- --service-cidr=10.96.0.0/12,fd00:1234::/108" "${rendered}")"
if [ "${service_cidr_count}" -ne 1 ]; then
  echo "expected one rendered dual-stack --service-cidr argument, found ${service_cidr_count}" >&2
  grep -F -- "--service-cidr=" "${rendered}" >&2 || true
  exit 1
fi
