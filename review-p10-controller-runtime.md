# P10: Controller-Runtime Usage Review

**Date:** 2026-03-02  
**Reviewer Persona:** Controller-Runtime Expert

## Summary

| Severity | Count |
|----------|-------|
| CRITICAL | 1 |
| HIGH | 3 |
| MEDIUM | 7 |
| LOW | 3 |
| INFO | 2 |

---

## Findings

### 1. CRITICAL — Webhook registration race with server start

**File:** [internal/controller/setup.go](internal/controller/setup.go) (or webhook setup)

Webhook handlers may be registered after the webhook server has started accepting connections, potentially causing 500 errors for early requests. Should use `mgr.GetWebhookServer().Register()` before `mgr.Start()`.

### 2. HIGH — `isPodUsingIP` doesn't distinguish pools for multi-network pods

**File:** [internal/controller/ippool_controller.go](internal/controller/ippool_controller.go)

The orphan detection checks if a pod exists and has the IP, but doesn't distinguish which network the IP belongs to. A pod with the same IP on a different network could prevent legitimate orphan cleanup.

### 3. HIGH — `cleanupOverlappingReservations` drops errors; leak on partial failure

**File:** [internal/controller/ippool_controller.go](internal/controller/ippool_controller.go)

If overlapping reservation deletion partially fails, some reservations are orphaned. The error handling doesn't report which specific reservations failed.

### 4. HIGH — `mapNodeToNADs` O(N×M) + no node event predicates

**File:** [internal/controller/nodeslice_controller.go](internal/controller/nodeslice_controller.go)

Every Node event triggers a full NAD list. No predicates filter node updates (status changes, heartbeats), causing unnecessary reconciliations.

### 5. MEDIUM — No predicates on IPPool watch → self-triggered reconcile loops

**File:** [internal/controller/ippool_controller.go](internal/controller/ippool_controller.go)

Every IPPool update (including the reconciler's own patches) triggers re-reconcile. Consider `GenerationChangedPredicate` or similar filtering.

### 6. MEDIUM — Allocation gauge not updated on early `cleanupOverlappingReservations` failure

**File:** [internal/controller/ippool_controller.go](internal/controller/ippool_controller.go#L128)

If orphans are cleaned from the pool but overlapping reservation cleanup fails, the function returns early without updating the gauge to reflect post-cleanup count.

### 7. MEDIUM — Stale `ippoolAllocationsGauge` when IPPool is deleted

**File:** [internal/controller/ippool_controller.go](internal/controller/ippool_controller.go#L77-L82)

On NotFound, the reconciler returns without clearing the gauge. Uses `ippoolAllocationsGauge.DeleteLabelValues(req.Name)` to remove stale series.

### 8. MEDIUM — Status set during `Create` (ignored by API server)

**File:** [internal/controller/nodeslice_controller.go](internal/controller/nodeslice_controller.go#L127-L153)

Setting `Status` during `Create` is silently dropped when status subresource is enabled. If Create succeeds but `Status().Update()` fails, NodeSlicePool exists with empty status.

### 9. MEDIUM — `Status().Update()` conflict-prone vs `Status().Patch()`

**File:** [internal/controller/nodeslice_controller.go](internal/controller/nodeslice_controller.go#L172-L180)

Using `Status().Update()` requires sending the complete object. `Status().Patch()` with `MergeFrom` would be more robust against concurrent modifications.

### 10. MEDIUM — IPPool webhook allows empty `spec.range`

**File:** [internal/webhook/ippool_webhook.go](internal/webhook/ippool_webhook.go#L57-L62)

Validation skips when `pool.Spec.Range` is empty. An IPPool with no range passes validation but breaks controller logic.

### 11. MEDIUM — No Kubernetes Event recording in any controller

None of the three controllers emit Events. Significant actions (orphan cleanup, reservation deletion, pool creation) are only logged, not visible via `kubectl describe`.

### 12. LOW — `WatchesRawSource` unnecessary for cached watch

**File:** [internal/controller/nodeslice_controller.go](internal/controller/nodeslice_controller.go#L43-L46)

Simpler `Watches(&corev1.Node{}, ...)` achieves the same and is more idiomatic.

### 13. LOW — `ensureOwnerRef` may set empty APIVersion/Kind from cache

**File:** [internal/controller/nodeslice_controller.go](internal/controller/nodeslice_controller.go)

Objects from cache may have empty `TypeMeta`. The OwnerReference could have empty APIVersion/Kind fields.

### 14. LOW — NodeSliceReconciler doesn't requeue periodically

Unlike IPPool and OverlappingRange reconcilers, NodeSlice only reconciles on events. Drift from missed events isn't corrected until the next event.

### 15. INFO — Metrics registration via `init()` is correct ✅

### 16. INFO — Signal handler usage is correct (`mgr.Start(ctrl.SetupSignalHandler())`) ✅
