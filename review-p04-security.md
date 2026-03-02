# P4: Security & Deployment Review

**Date:** 2026-03-02  
**Reviewer Persona:** Security Engineer & Kubernetes Platform Architect

## Summary

| Severity | Count |
|----------|-------|
| CRITICAL | 3 |
| HIGH | 3 |
| MEDIUM | 6 |
| LOW | 4 |
| INFO | 2 |

---

## Findings

### 1. CRITICAL — Helm chart dynamic names don't match Go hardcoded names

**Files:** [cmd/operator/webhook.go](cmd/operator/webhook.go#L63-L67), [deployment/whereabouts-chart/templates/](deployment/whereabouts-chart/templates/)

The Go code hardcodes `whereabouts-validating-webhooks`, `whereabouts-webhook-cert`, `whereabouts-webhook.<ns>.svc`. The Helm chart generates dynamic names via `{{ include "whereabouts.fullname" . }}` — e.g., `myrelease-whereabouts-chart-webhooks`. Unless `fullnameOverride` is set to `"whereabouts"`, cert-controller looks up wrong VWC/Secret/Service DNS names, causing TLS failures on all webhook calls.

### 2. CRITICAL — Static YAMLs use `:latest` image tag

**Files:** [doc/crds/daemonset-install.yaml](doc/crds/daemonset-install.yaml#L80), [doc/crds/operator-install.yaml](doc/crds/operator-install.yaml#L81), [doc/crds/webhook-install.yaml](doc/crds/webhook-install.yaml#L74), [doc/crds/node-slice-controller.yaml](doc/crds/node-slice-controller.yaml#L27)

Non-reproducible deployments. Rolling updates may run mixed versions.

### 3. CRITICAL — Deprecated `node-slice-controller.yaml` references deleted binary

**File:** [doc/crds/node-slice-controller.yaml](doc/crds/node-slice-controller.yaml#L18-L19)

The `/node-slice-controller` binary no longer exists (replaced by `/whereabouts-operator controller`). Deploying produces `CrashLoopBackOff`.

### 4. HIGH — DaemonSet runs `privileged: true` as root

**Files:** [doc/crds/daemonset-install.yaml](doc/crds/daemonset-install.yaml#L93-L95), [deployment/whereabouts-chart/values.yaml](deployment/whereabouts-chart/values.yaml#L33-L35)

Missing `capabilities.drop: [ALL]`, `readOnlyRootFilesystem: true`, `seccompProfile: RuntimeDefault`, `allowPrivilegeEscalation: false`.

### 5. HIGH — `failurePolicy: Fail` blocks pod creation on webhook outage

**Files:** [doc/crds/validatingwebhookconfiguration.yaml](doc/crds/validatingwebhookconfiguration.yaml#L22), Helm template

If the webhook is down (rolling update, crash, cert renewal), all IPPool/NodeSlicePool/ORIP CREATE/UPDATE operations are rejected. Since CNI creates IPPools during pod network setup, no pods with whereabouts IPAM can start during webhook downtime. No PodDisruptionBudget exists.

### 6. HIGH — Webhook ClusterRole has cluster-wide secrets access

**Files:** [doc/crds/webhook-install.yaml](doc/crds/webhook-install.yaml#L22-L23), Helm webhook-clusterrole.yaml

`get, list, watch, create, update, patch` on **all secrets in all namespaces**. Cert-controller only needs one specific secret. Should use a namespaced Role with `resourceNames` scoping.

### 7. MEDIUM — Stale cron ConfigMap and volume mount from deleted `ip-control-loop`

**File:** [deployment/whereabouts-chart/templates/daemonset.yaml](deployment/whereabouts-chart/templates/daemonset.yaml#L74-L82), Helm configmap.yaml

The `cron-expression` was consumed by the deleted `/ip-control-loop` binary. Dead code.

### 8. MEDIUM — CEL matchConditions bypass is fragile

**File:** [doc/crds/validatingwebhookconfiguration.yaml](doc/crds/validatingwebhookconfiguration.yaml#L36-L38)

ServiceAccount name `whereabouts` in `kube-system` is hardcoded in static YAML. Namespace/SA rename silently breaks the bypass.

### 9. MEDIUM — No PodDisruptionBudget for webhook or operator

Neither static YAMLs nor Helm chart define PDBs. Combined with `failurePolicy: Fail`, cluster drain/maintenance could evict all webhook pods simultaneously.

### 10. MEDIUM — No NetworkPolicy defined

Webhook port 9443 and metrics ports 8080/8082 are exposed to all pods in the cluster.

### 11. MEDIUM — TLS certificates default to `/tmp/` path

**File:** [cmd/operator/webhook.go](cmd/operator/webhook.go#L94)

### 12. MEDIUM — Misleading `cert-controller.io/inject-ca-from` annotation

**File:** [doc/crds/validatingwebhookconfiguration.yaml](doc/crds/validatingwebhookconfiguration.yaml#L9-L10)

The project uses `open-policy-agent/cert-controller` which injects CA programmatically. The annotation is cosmetic and implies cert-manager is required.

### 13. LOW — Helm DaemonSet `hostNetwork: true`

Standard for CNI installer pods but should be documented.

### 14. LOW — DaemonSet tolerates ALL NoSchedule taints

Intentional for CNI but undocumented.

### 15. LOW — Helm `NOTES.txt` references wrong namespace with `namespaceOverride`

### 16. LOW — Operator/webhook templates missing `imagePullSecrets`

Private registry deployments fail for operator/webhook pods.

### 17. INFO — Chart named `whereabouts-chart` instead of `whereabouts`

Non-idiomatic `-chart` suffix causes awkward resource names.

### 18. INFO — Static `daemonset-install.yaml` missing explicit `imagePullPolicy`
