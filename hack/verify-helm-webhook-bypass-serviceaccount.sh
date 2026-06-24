#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CHART_DIR="${ROOT}/deployment/whereabouts-chart"

rendered="$(mktemp)"
trap 'rm -f "${rendered}"' EXIT

assert_bypass_service_account() {
  local release="$1"
  local expected_service_account="$2"
  local unexpected_service_account="$3"
  shift 3

  helm template "${release}" "${CHART_DIR}" --namespace kube-system "$@" >"${rendered}"

  local expected_count
  expected_count="$(awk -v pat="system:serviceaccount:kube-system:${expected_service_account}\")" 'index($0, pat) { count++ } END { print count + 0 }' "${rendered}")"
  if [ "${expected_count}" -ne 3 ]; then
    echo "expected all 3 webhooks to bypass ${expected_service_account}, rendered ${expected_count}" >&2
    grep -F "system:serviceaccount:kube-system:" "${rendered}" >&2 || true
    exit 1
  fi

  if grep -Fq "system:serviceaccount:kube-system:${unexpected_service_account}\")" "${rendered}"; then
    echo "webhook bypass unexpectedly rendered ${unexpected_service_account}" >&2
    grep -F "system:serviceaccount:kube-system:" "${rendered}" >&2
    exit 1
  fi

  if ! grep -Fq "serviceAccountName: ${expected_service_account}" "${rendered}"; then
    echo "DaemonSet did not render serviceAccountName: ${expected_service_account}" >&2
    exit 1
  fi

  local binding_count
  binding_count="$(awk -v name="${expected_service_account}" '
    $1 == "kind:" && ($2 == "ClusterRoleBinding" || $2 == "RoleBinding") {
      in_binding = 1
      saw_service_account = 0
      found_subject = 0
      next
    }
    in_binding && $1 == "---" {
      if (found_subject) { count++ }
      in_binding = 0
    }
    in_binding && $1 == "-" && $2 == "kind:" && $3 == "ServiceAccount" {
      saw_service_account = 1
      next
    }
    in_binding && saw_service_account && $1 == "name:" && $2 == name {
      found_subject = 1
    }
    END {
      if (in_binding && found_subject) { count++ }
      print count + 0
    }
  ' "${rendered}")"
  if [ "${binding_count}" -lt 2 ]; then
    echo "expected CNI RoleBinding and ClusterRoleBinding subjects for ${expected_service_account}, rendered ${binding_count}" >&2
    awk '/kind: (ClusterRoleBinding|RoleBinding)|name: / { print }' "${rendered}" >&2
    exit 1
  fi
}

assert_bypass_service_account customrel customrel-whereabouts-chart whereabouts
assert_bypass_service_account customrel custom-cni whereabouts --set serviceAccount.create=false --set serviceAccount.name=custom-cni
