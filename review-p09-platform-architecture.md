# P9: Platform Architecture Review

**Date:** 2026-03-02  
**Reviewer Persona:** Kubernetes Platform Architect

## Summary

| Severity | Count |
|----------|-------|
| CRITICAL | 1 |
| HIGH | 2 |
| MEDIUM | 3 |
| LOW | 3 |
| INFO | 1 |

---

## Findings

### 1. CRITICAL — IPPool stores mutable allocation data in `.spec` not `.status`

**File:** [pkg/api/whereabouts.cni.cncf.io/v1alpha1/ippool_types.go](pkg/api/whereabouts.cni.cncf.io/v1alpha1/ippool_types.go#L17)

`IPAllocation` data (live pod→IP mappings) stored in `spec.allocations` is observed/mutable state, not desired state. Consequences: no RBAC separation, `kubectl edit` can corrupt state, SSA field ownership conflicts, no status subresource protection. This is inherited upstream design debt requiring a breaking CRD version change.

### 2. HIGH — v1alpha1 with no conversion webhook — future migration risk

All three CRDs are `v1alpha1` only with no conversion webhook infrastructure. When promoting to `v1beta1`/`v1`, need conversion webhook, dual-version serving, and migration tooling. Already deployed at production scale, so `v1alpha1` understates the API's actual stability contract.

### 3. HIGH — Webhook `failurePolicy: Fail` blocks pod creation during outage

**File:** [doc/crds/validatingwebhookconfiguration.yaml](doc/crds/validatingwebhookconfiguration.yaml#L23)

Combined with no PDB, cluster drain could evict all webhook pods, causing cluster-wide pod scheduling outage for whereabouts workloads. The matchConditions bypass only helps the CNI ServiceAccount — the operator's own reconciler is also blocked.

### 4. MEDIUM — Webhook does not bypass operator's own ServiceAccount

**File:** [doc/crds/validatingwebhookconfiguration.yaml](doc/crds/validatingwebhookconfiguration.yaml#L37-L39)

Only CNI SA `whereabouts` is bypassed. Operator SA `whereabouts-operator` patches IPPool CRDs for orphan cleanup — these go through the webhook. If webhook is down, reconciler fails.

### 5. MEDIUM — `NodeSlicePool` short name `nsp` may clash

**File:** [pkg/api/whereabouts.cni.cncf.io/v1alpha1/nodeslicepool_types.go](pkg/api/whereabouts.cni.cncf.io/v1alpha1/nodeslicepool_types.go#L53)

`nsp` is commonly used by Calico's `NetworkSecurityPolicy`. Hard conflict at API server level in mixed clusters.

### 6. MEDIUM — No `x-kubernetes-map-type` on `allocations`

**File:** [doc/crds/whereabouts.cni.cncf.io_ippools.yaml](doc/crds/whereabouts.cni.cncf.io_ippools.yaml#L49-L52)

Should explicitly declare `granular` for SSA to prevent future kubebuilder defaults from changing behavior. Add `// +mapType=granular` to Go type.

### 7. LOW — No CRD-level validation on `Range` fields (CIDR format)

**Files:** [pkg/api/whereabouts.cni.cncf.io/v1alpha1/ippool_types.go](pkg/api/whereabouts.cni.cncf.io/v1alpha1/ippool_types.go#L12-L13), [nodeslicepool_types.go](pkg/api/whereabouts.cni.cncf.io/v1alpha1/nodeslicepool_types.go#L13-L14)

Only `MinLength=1`. No `Pattern` marker for CIDR format. CRD schema should catch obviously invalid values.

### 8. LOW — No `MaxLength` validation on string fields

None of the CRDs set `MaxLength`. Large values could bloat etcd. Key fields: Range (~50), PodRef (~506), ContainerID (~64), IfName (15 — Linux limit).

### 9. LOW — Missing `Age` printer column on all three CRDs

Standard `metadata.creationTimestamp` column not added.

### 10. INFO — `register.go` uses hybrid code-generator + kubebuilder pattern (correct but unusual)
