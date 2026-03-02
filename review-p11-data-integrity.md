# P11: Data Integrity & CRD Review

**Date:** 2026-03-02  
**Reviewer Persona:** Data Integrity & Kubernetes API Expert

## Summary

| Severity | Count |
|----------|-------|
| CRITICAL | 1 |
| HIGH | 2 |
| MEDIUM | 5 |
| LOW | 5 |
| INFO | 2 |

---

## Findings

### 1. CRITICAL — IPPool stores mutable allocation data in `.spec` not `.status`

**File:** [pkg/api/whereabouts.cni.cncf.io/v1alpha1/ippool_types.go](pkg/api/whereabouts.cni.cncf.io/v1alpha1/ippool_types.go#L17)

`Allocations` in `IPPoolSpec` is observed/mutable state. Consequences: no RBAC separation between configuration and management, `kubectl edit` can corrupt live allocations, SSA field ownership conflicts between CNI, reconciler, and human editors. Inherited upstream debt; requires a breaking CRD version change to fix.

### 2. HIGH — IPPool CRD has no status subresource at all

**Files:** [ippool_types.go](pkg/api/whereabouts.cni.cncf.io/v1alpha1/ippool_types.go#L39-L48), [whereabouts.cni.cncf.io_ippools.yaml](doc/crds/whereabouts.cni.cncf.io_ippools.yaml#L86)

No `+kubebuilder:subresource:status` marker, no `Status` field. Cannot report operational status (utilization %, last-reconciled timestamp, error conditions). NodeSlicePool correctly has status; IPPool and ORIP do not.

### 3. HIGH — `failurePolicy: Fail` on webhooks without PDB blocks operations

Combined with no PodDisruptionBudget, webhook outage blocks all IPPool/NodeSlicePool/ORIP mutations including the operator's own reconciler patches.

### 4. MEDIUM — No CRD-level `Pattern` validation on `Range` fields

**Files:** [ippool_types.go L12-13](pkg/api/whereabouts.cni.cncf.io/v1alpha1/ippool_types.go#L12-L13), [nodeslicepool_types.go L13-14](pkg/api/whereabouts.cni.cncf.io/v1alpha1/nodeslicepool_types.go#L13-L14)

Only `MinLength=1`. A regex like `^[0-9a-fA-F.:]+/[0-9]+$` would catch invalid values at admission.

### 5. MEDIUM — `PodRef` has no format validation marker

**Files:** [ippool_types.go L31-32](pkg/api/whereabouts.cni.cncf.io/v1alpha1/ippool_types.go#L31-L32), [overlappingrangeipreservation_types.go L11-12](pkg/api/whereabouts.cni.cncf.io/v1alpha1/overlappingrangeipreservation_types.go#L11-L12)

Should have pattern like `^[a-z0-9-]+/[a-z0-9.-]+$`.

### 6. MEDIUM — `IPAllocation.ContainerID` required vs optional inconsistency

`ContainerID` is required in IPAllocation (no `omitempty`) but optional in OverlappingRangeIPReservationSpec (has `omitempty`). ContainerID can legitimately be empty in some CNI flows.

### 7. MEDIUM — `NodeSlicePool` short name `nsp` may clash with Calico

### 8. MEDIUM — No `x-kubernetes-map-type` annotation on `allocations` map

Without explicit `granular`/`atomic`, future kubebuilder changes could alter SSA behavior.

### 9. LOW — IPPool CRD lacks top-level `required: [spec]`

**File:** [whereabouts.cni.cncf.io_ippools.yaml](doc/crds/whereabouts.cni.cncf.io_ippools.yaml#L82-L85)

An IPPool can be created with no `.spec` at all. OverlappingRangeIPReservation correctly requires spec.

### 10. LOW — `Allocations` required forces empty map `{}` on creation

**File:** [ippool_types.go L17](pkg/api/whereabouts.cni.cncf.io/v1alpha1/ippool_types.go#L17)

Every new IPPool must include `"allocations": {}`. If `omitempty`, creation would be cleaner.

### 11. LOW — Missing `Age` printer column on all three CRDs

### 12. LOW — No `MaxLength` on string fields (etcd bloat risk)

Recommend: Range ~50, PodRef ~506, ContainerID ~64, IfName 15.

### 13. LOW — Operator RBAC uses ClusterRole for leader election leases

Leases are namespace-scoped. ClusterRole grants access in all namespaces.

### 14. INFO — `OverlappingRangeIPReservation` extremely long resource name (36 chars)

Mitigated by `orip` short name.

### 15. INFO — `register.go` uses hybrid code-generator + kubebuilder pattern
