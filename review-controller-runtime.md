# Controller-Runtime / Operator Review — Whereabouts IPAM

**Scope**: `cmd/operator/`, `internal/controller/`, `internal/webhook/`, `internal/webhook/certrotator/`  
**controller-runtime version**: v0.23.2  
**Date**: 2026-03-02

---

## P13-1 — `context.Background()` instead of `ctrl.SetupSignalHandler()` (High)

**File**: [cmd/operator/controller.go](cmd/operator/controller.go#L63), [cmd/operator/webhook.go](cmd/operator/webhook.go#L85)

```go
// controller.go:63
return mgr.Start(context.Background())

// webhook.go:85
return mgr.Start(context.Background())
```

**Description**: Both subcommands pass `context.Background()` to `mgr.Start()`. The canonical controller-runtime pattern is `ctrl.SetupSignalHandler()`, which installs a SIGTERM/SIGINT handler and returns a context that is cancelled on signal delivery. Without it, the manager never learns about a shutdown signal — the pod must be hard-killed by the kubelet after `terminationGracePeriodSeconds`. This prevents graceful leader-election lease release, informer drain, and clean webhook server shutdown. For the controller subcommand, this is especially dangerous: the lease will survive the full lease duration (default 15 s) after the pod dies, blocking a new leader from starting.

**Recommendation**: Replace with `mgr.Start(ctrl.SetupSignalHandler())` in both files.

---

## P13-2 — Webhook registration after manager starts — race with webhook server (High)

**File**: [internal/webhook/setup.go](internal/webhook/setup.go#L35-L56)

```go
func (s *Setup) Start(ctx context.Context) error {
    // ...
    select {
    case <-ctx.Done():
        return ctx.Err()
    case <-s.certReady:
        log.Info("certificates ready, registering webhooks")
    }

    if err := SetupIPPoolWebhook(s.mgr); err != nil { ... }
    // ...
}
```

**Description**: `Setup` is added to the manager as a `Runnable`, so it runs concurrently with the webhook server (which is also a `Runnable`). The webhook server starts serving TLS immediately when the manager starts. `builder.WebhookManagedBy()` calls `mgr.GetWebhookServer().Register()` under the hood, which mutates the server's mux **after** the server is already listening. In controller-runtime v0.23, the webhook server's `Start()` method is called before non-leader-election runnables finish. If a request arrives between server startup and `Setup.Start()` completing, the path returns 404.

This is a narrow window in practice since cert provisioning takes time, and the readyz check blocks traffic until `s.ready` is set. However, the architecture is fragile — the webhook server starts accepting connections before paths are registered. If the readyz gate isn't wired correctly in the Kubernetes Service (e.g. no readinessProbe configured in the Deployment), requests can reach unregistered paths.

**Recommendation**: Register webhook handlers **before** `mgr.Start()` and use the cert-ready channel only to block the TLS listener. Alternatively, register all webhook paths eagerly (returning 503 until certs are ready) so that the mux is populated before the server starts. The current readyz-gated approach works but is implicitly relying on correct Deployment readiness configuration.

---

## P13-3 — All reconcilers read Pods from cache, not API server (Medium)

**File**: [internal/controller/ippool_controller.go](internal/controller/ippool_controller.go#L92), [internal/controller/overlappingrange_controller.go](internal/controller/overlappingrange_controller.go#L76)

```go
// ippool_controller.go:92
var pod corev1.Pod
err := r.client.Get(ctx, types.NamespacedName{Namespace: podNS, Name: podName}, &pod)

// overlappingrange_controller.go:76
var pod corev1.Pod
err := r.client.Get(ctx, types.NamespacedName{Namespace: podNS, Name: podName}, &pod)
```

**Description**: `mgr.GetClient()` returns a delegating client that reads from the informer cache by default. Pod caches can be stale for seconds after a pod is deleted. This means the reconciler may observe a pod as still existing when it has already been deleted from etcd, delaying orphan cleanup. More critically, it may also observe a pod as deleted when the informer simply hasn't synced the create yet (a new pod can be missed if the informer hasn't populated it). For the orphan-cleanup use case, false negatives (missing a still-live pod) risk **data loss** — deleting a still-valid IP allocation.

The reconciler's `RequeueAfter` mitigates this (it will re-check), but the first reconciliation after a rapid pod create-and-allocate sequence could incorrectly identify a freshly allocated IP as orphaned.

**Recommendation**: Use `mgr.GetAPIReader()` (which performs direct API server reads) for the pod existence check in orphan-cleanup paths. Store it as a separate field on the reconciler struct:

```go
type IPPoolReconciler struct {
    client            client.Client
    reader            client.Reader // direct API reader
    reconcileInterval time.Duration
}
```

---

## P13-4 — `cleanupOverlappingReservations` silently swallows errors (Medium)

**File**: [internal/controller/ippool_controller.go](internal/controller/ippool_controller.go#L168-L196)

```go
func (r *IPPoolReconciler) cleanupOverlappingReservations(ctx context.Context, pool *whereaboutsv1alpha1.IPPool, keys []string) {
    // ...
    if err := r.client.List(ctx, &reservations, client.InNamespace(pool.Namespace)); err != nil {
        logger.V(1).Info("failed to list overlapping reservations", "error", err)
        continue
    }
    // ...
}
```

**Description**: The method has no return value and logs errors at `V(1)` (debug level). If the List call or Delete call fails (e.g. RBAC, API server unavailable), the overlapping reservation is silently leaked. Since this runs after the IPPool has already been patched (allocations removed), the cleanup is a best-effort side effect with no retry guarantee.

**Recommendation**: Either (a) return an error from `cleanupOverlappingReservations` and propagate it from `Reconcile` to trigger a requeue, or (b) accept the best-effort design but log at a higher level (warn/error) so operators can detect cleanup failures in monitoring. The `OverlappingRangeReconciler` provides a secondary cleanup path, so option (b) is acceptable if documented.

---

## P13-5 — No event recording in any reconciler (Medium)

**File**: [internal/controller/ippool_controller.go](internal/controller/ippool_controller.go#L30-L33), [internal/controller/nodeslice_controller.go](internal/controller/nodeslice_controller.go#L33-L35), [internal/controller/overlappingrange_controller.go](internal/controller/overlappingrange_controller.go#L23-L26)

```go
type IPPoolReconciler struct {
    client            client.Client
    reconcileInterval time.Duration
}
```

**Description**: None of the three reconcilers use `mgr.GetEventRecorderFor()` to emit Kubernetes Events on the objects they manage. When allocations are cleaned up, when a multi-NAD mismatch is detected, or when a NodeSlicePool runs out of slots, there is no Kubernetes Event to surface this to users via `kubectl describe`. The RBAC markers in `setup.go` already declare `events` permissions, but they're unused. This makes operational debugging harder — cluster administrators have to parse controller logs instead of inspecting Events on the resource.

**Recommendation**: Add `record.EventRecorder` to each reconciler struct and record events for:
- IPPool: orphaned allocations cleaned up, N allocations removed
- NodeSlice: pool created/resized, pool full (no available slot for node), multi-NAD mismatch
- OverlappingRange: reservation deleted due to orphaned pod

---

## P13-6 — `NodeSliceReconciler` uses `WatchesRawSource` instead of `Watches` (Low)

**File**: [internal/controller/nodeslice_controller.go](internal/controller/nodeslice_controller.go#L49-L51)

```go
WatchesRawSource(source.Kind(mgr.GetCache(), &corev1.Node{},
    handler.TypedEnqueueRequestsFromMapFunc(r.mapNodeToNADs),
)).
```

**Description**: `WatchesRawSource` is the low-level API; controller-runtime v0.23 provides the typed `Watches()` builder method that wraps `source.Kind` and handles type inference automatically:

```go
Watches(&corev1.Node{}, handler.EnqueueRequestsFromMapFunc(r.mapNodeToNADs))
```

Using `WatchesRawSource` is not incorrect but bypasses builder-level validation and is less idiomatic. It also embeds a direct cache dependency (`mgr.GetCache()`) that the builder normally abstracts away.

**Recommendation**: Migrate to `Watches()` for consistency with controller-runtime conventions, unless there's a specific reason for `WatchesRawSource` (e.g., custom source with backoff).

---

## P13-7 — `mapNodeToNADs` lists all NADs cluster-wide on every node event (Medium)

**File**: [internal/controller/nodeslice_controller.go](internal/controller/nodeslice_controller.go#L283-L297)

```go
func (r *NodeSliceReconciler) mapNodeToNADs(ctx context.Context, _ *corev1.Node) []reconcile.Request {
    var nadList nadv1.NetworkAttachmentDefinitionList
    if err := r.client.List(ctx, &nadList); err != nil {
        log.FromContext(ctx).Error(err, "failed to list NADs for node event mapping")
        return nil
    }
    requests := make([]reconcile.Request, 0, len(nadList.Items))
    for i := range nadList.Items {
        requests = append(requests, reconcile.Request{...})
    }
    return requests
}
```

**Description**: Every node create/update/delete event triggers a full, unfiltered List of **all NADs across all namespaces**. In a large cluster with many NADs (only a fraction of which use whereabouts + `node_slice_size`), this creates a burst of unnecessary reconciliation work. With N nodes and M NADs, a node scale event produces N×M reconcile requests, most of which will be no-ops (NADs without `node_slice_size` are skipped at the top of `Reconcile`). There's also no predicate filter on the Node watch to skip update events that don't affect node readiness or existence.

**Recommendation**:
1. Add a `predicate.GenerationChangedPredicate` or custom predicate on the Node watch to only trigger on create/delete events (not every status update).
2. In `mapNodeToNADs`, filter NADs to only those with whereabouts IPAM config containing `node_slice_size`, or maintain a local cache/index.

---

## P13-8 — `checkMultiNADMismatch` lists all NADs on every reconcile (Low)

**File**: [internal/controller/nodeslice_controller.go](internal/controller/nodeslice_controller.go#L305-L340)

```go
func (r *NodeSliceReconciler) checkMultiNADMismatch(ctx context.Context, ...) error {
    var nadList nadv1.NetworkAttachmentDefinitionList
    if err := r.client.List(ctx, &nadList); err != nil {
        return fmt.Errorf("listing NADs for mismatch check: %s", err)
    }
    // ...
}
```

**Description**: Every single NAD reconcile call lists all NADs cluster-wide to check for configuration mismatches. Combined with `mapNodeToNADs` also listing all NADs, a single node event can produce two full NAD List calls per NAD. For cache-backed reads this is O(1) lookup but still allocates and iterates all items. More importantly, the mismatch check returns a hard error that requeues the NAD indefinitely — a permanent misconfiguration will produce infinite retries with exponential backoff capped at ~17 min.

**Recommendation**: Consider making the mismatch check produce a status condition or event rather than returning an error, so that the reconciler converges to a steady state. Alternatively, rate-limit or cap retries for configuration errors.

---

## P13-9 — `NodeSliceReconciler.createPool` sets status in a separate call after create (Low)

**File**: [internal/controller/nodeslice_controller.go](internal/controller/nodeslice_controller.go#L149-L180)

```go
pool := &whereaboutsv1alpha1.NodeSlicePool{
    // ...
    Status: whereaboutsv1alpha1.NodeSlicePoolStatus{
        Allocations: allocations,
    },
}

if err := r.client.Create(ctx, pool); err != nil { ... }

// Update status separately (subresource).
pool.Status.Allocations = allocations
if err := r.client.Status().Update(ctx, pool); err != nil { ... }
```

**Description**: The `Create()` call includes Status in the object, but if the Status subresource is enabled (which kubebuilder CRDs have by default), the API server ignores the status field on Create. The subsequent `Status().Update()` is correct. However, setting status in the Create body is dead code and slightly misleading.

**Recommendation**: Remove the inline `Status` field from the Create call to avoid confusion. The two-step create-then-update-status pattern is correct for kubebuilder status subresources.

---

## P13-10 — `ensureOwnerRef` silently swallows patch errors (Medium)

**File**: [internal/controller/nodeslice_controller.go](internal/controller/nodeslice_controller.go#L260-L280)

```go
func (r *NodeSliceReconciler) ensureOwnerRef(ctx context.Context, ...) {
    // ...
    if err := r.client.Patch(ctx, pool, patch); err != nil {
        logger.Error(err, "failed to add OwnerReference to NodeSlicePool",
            "pool", pool.Name, "nad", nad.Name)
    }
}
```

**Description**: `ensureOwnerRef` logs errors but doesn't return them. A failed Patch means the OwnerReference wasn't set, so the NodeSlicePool won't be garbage collected if this NAD is deleted. Since this is called from `Reconcile`, the error should propagate to trigger a retry. Additionally, the in-memory `pool` object has already been mutated (OwnerReferences appended) before the Patch call — if the Patch fails, subsequent code in the same Reconcile iteration operates on a mutated but not persisted copy.

**Recommendation**: Return `error` from `ensureOwnerRef` and propagate it:

```go
func (r *NodeSliceReconciler) ensureOwnerRef(...) error {
    // ...
    return r.client.Patch(ctx, pool, patch)
}
```

---

## P13-11 — Webhook server disabled via port 0 in controller subcommand (Informational)

**File**: [cmd/operator/controller.go](cmd/operator/controller.go#L46)

```go
WebhookServer: webhook.NewServer(webhook.Options{Port: 0}),
```

**Description**: The controller subcommand disables the webhook server by binding to port 0 (OS-assigned ephemeral). This works but is unconventional — port 0 still opens a listening socket on a random port. In controller-runtime v0.23, if no webhook handlers are registered, the server is a no-op even if configured. The unused open port may raise flags in security scanners.

**Recommendation**: Consider removing the `WebhookServer` field entirely since no webhooks are registered in the controller binary, or document the reason for port 0.

---

## P13-12 — No predicates to filter resource update events (Medium)

**File**: [internal/controller/ippool_controller.go](internal/controller/ippool_controller.go#L39-L50), [internal/controller/overlappingrange_controller.go](internal/controller/overlappingrange_controller.go#L34-L44)

```go
return ctrl.NewControllerManagedBy(mgr).
    For(&whereaboutsv1alpha1.IPPool{}).
    WithOptions(controller.Options{
        MaxConcurrentReconciles: 1,
    }).
    Named("ippool").
    Complete(r)
```

**Description**: The IPPool and OverlappingRange controllers watch their primary resources without any predicates. Every metadata update (labels, annotations, finalizers) or status update triggers a full reconciliation. For IPPool, every CNI ADD/DEL updates the pool's `spec.allocations`, triggering the reconciler, which then iterates all allocations and calls `Get` on every referenced pod. This is the intended behavior for detecting orphans, but it means the reconciler runs for **every** IP allocation event, not just periodically. With `MaxConcurrentReconciles: 1`, these reconcile calls are serialized, creating a potential bottleneck under high pod churn.

The `RequeueAfter` mechanism provides periodic reconciliation even without events, so the event-driven path is redundant for orphan cleanup (it's a "scan everything" approach, not a targeted reconcile).

**Recommendation**: Add `builder.WithPredicates(predicate.GenerationChangedPredicate{})` to skip status-only updates. Alternatively, accept the current behavior but increase `MaxConcurrentReconciles` or add rate limiting to prevent the queue from growing unboundedly during pod churn.

---

## P13-13 — `NodeSliceReconciler.ensureNodeAssignments` uses `Status().Update` instead of `Status().Patch` (Low)

**File**: [internal/controller/nodeslice_controller.go](internal/controller/nodeslice_controller.go#L248-L255)

```go
if changed {
    pool.Status.Allocations = allocations
    if err := r.client.Status().Update(ctx, pool); err != nil {
        return ctrl.Result{}, fmt.Errorf("updating NodeSlicePool status: %s", err)
    }
}
```

**Description**: `Status().Update()` sends the entire object and requires the current `resourceVersion` to match. If another writer has modified the object since this reconciler's `Get`, the update will fail with a conflict. `Status().Patch(ctx, pool, client.MergeFrom(original))` only sends the diff and is more conflict-resistant. The same pattern appears in `createPool` and `updatePoolSpec`. The IPPoolReconciler correctly uses `Patch` for allocations, but the NodeSliceReconciler uses `Update` for status.

Since `MaxConcurrentReconciles: 1` and there's only one leader, conflicts are unlikely in practice but can occur during leader transitions.

**Recommendation**: Use `Status().Patch()` with `client.MergeFrom()` for consistency and conflict resistance:

```go
original := pool.DeepCopy()
pool.Status.Allocations = allocations
if err := r.client.Status().Patch(ctx, pool, client.MergeFrom(original)); err != nil { ... }
```

---

## Summary

| ID | Severity | Category | File | Summary |
|----|----------|----------|------|---------|
| P13-1 | **High** | Signal handling | controller.go, webhook.go | `context.Background()` prevents graceful shutdown |
| P13-2 | **High** | Webhook setup | webhook/setup.go | Webhook paths registered after server starts listening |
| P13-3 | **Medium** | Cache staleness | ippool_controller.go, overlappingrange_controller.go | Pod reads from cache risk false-positive orphan detection |
| P13-4 | **Medium** | Error handling | ippool_controller.go | Overlapping reservation cleanup swallows errors |
| P13-5 | **Medium** | Observability | All reconcilers | No Kubernetes Events emitted |
| P13-6 | **Low** | Builder API | nodeslice_controller.go | `WatchesRawSource` instead of typed `Watches` |
| P13-7 | **Medium** | Performance | nodeslice_controller.go | `mapNodeToNADs` lists all NADs on every node event |
| P13-8 | **Low** | Performance / Error handling | nodeslice_controller.go | `checkMultiNADMismatch` lists all NADs; permanent error causes infinite requeue |
| P13-9 | **Low** | Dead code | nodeslice_controller.go | Status set in Create body is ignored by API server |
| P13-10 | **Medium** | Error handling | nodeslice_controller.go | `ensureOwnerRef` swallows patch errors |
| P13-11 | **Info** | Configuration | controller.go | Port 0 opens an unused socket |
| P13-12 | **Medium** | Performance | ippool_controller.go, overlappingrange_controller.go | No predicates; every spec/status change triggers full reconcile |
| P13-13 | **Low** | Concurrency | nodeslice_controller.go | `Status().Update` instead of `Status().Patch` |

**Overall assessment**: The migration to controller-runtime is structurally sound. The Builder API usage is correct, RBAC markers are in place, webhook registration uses the typed `admission.Validator[T]` API properly, and the reconcile loops follow the expected get-check-patch pattern. The two high-severity findings (P13-1 signal handling, P13-2 webhook registration timing) should be addressed before production. The medium-severity items around cache reads for orphan detection (P13-3) and missing event recording (P13-5) are important for operational reliability.
