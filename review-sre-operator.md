# SRE Review — Whereabouts Operator Architecture

**Reviewer:** SRE  
**Date:** 2026-03-02  
**Scope:** `cmd/operator/`, `internal/controller/`, `internal/webhook/`, deployment manifests  

---

## P5-7: Webhook CA bundle name mismatch — cert-controller will fail to inject CA

**Severity:** Critical  
**Files:** [cmd/operator/webhook.go](cmd/operator/webhook.go#L65), [doc/crds/validatingwebhookconfiguration.yaml](doc/crds/validatingwebhookconfiguration.yaml#L12)

**Description:**  
The webhook command configures cert-controller with `WebhookName: "whereabouts-validating-webhook"` (singular), but the `ValidatingWebhookConfiguration` resource is named `whereabouts-validating-webhooks` (plural). cert-controller uses this name to find the `ValidatingWebhookConfiguration` and inject the CA bundle. The name mismatch means cert-controller will never find the webhook configuration, leaving `caBundle` empty and causing all webhook calls to fail with TLS verification errors.

**Recommendation:**  
Align the names. Either change the code to `"whereabouts-validating-webhooks"` or rename the VWC resource to `whereabouts-validating-webhook`. The code reference should be the source of truth since it's programmatic — recommend changing the YAML:

```yaml
metadata:
  name: whereabouts-validating-webhook
```

---

## P5-8: `context.Background()` instead of signal-aware context — no graceful shutdown

**Severity:** High  
**Files:** [cmd/operator/controller.go](cmd/operator/controller.go#L63), [cmd/operator/webhook.go](cmd/operator/webhook.go#L85)

**Description:**  
Both `mgr.Start()` calls use `context.Background()`. controller-runtime's `ctrl.SetupSignalHandler()` returns a context that's cancelled on SIGTERM/SIGINT, enabling graceful shutdown of leader election, informers, and work queues. With `context.Background()`, the manager never receives a shutdown signal — the process must be hard-killed by the kubelet after `terminationGracePeriodSeconds`, which risks:
- Leader election lease not being released (standby replica waits full lease duration before acquiring)
- In-flight reconcile loops potentially partially completing

**Recommendation:**
```go
return mgr.Start(ctrl.SetupSignalHandler())
```

---

## P5-9: No PodDisruptionBudget for operator or webhook Deployments

**Severity:** High  
**Files:** [doc/crds/operator-install.yaml](doc/crds/operator-install.yaml), [doc/crds/webhook-install.yaml](doc/crds/webhook-install.yaml)

**Description:**  
Both Deployments have `replicas: 2` but no `PodDisruptionBudget`. During voluntary disruptions (node drain, cluster upgrade), both replicas can be evicted simultaneously. For the operator, this causes a leadership gap. For the webhook, all three webhooks use `failurePolicy: Fail`, so simultaneous eviction of both replicas blocks all IPPool, NodeSlicePool, and OverlappingRangeIPReservation creates/updates cluster-wide — effectively blocking all CNI IPAM operations for any pod that doesn't match the CEL bypass.

**Recommendation:**  
Add PDBs for both:
```yaml
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: whereabouts-webhook
  namespace: kube-system
spec:
  minAvailable: 1
  selector:
    matchLabels:
      app: whereabouts-webhook
```

---

## P5-10: No pod anti-affinity or topology spread — replicas may co-locate

**Severity:** Medium  
**Files:** [doc/crds/operator-install.yaml](doc/crds/operator-install.yaml#L85), [doc/crds/webhook-install.yaml](doc/crds/webhook-install.yaml#L73)

**Description:**  
Neither Deployment specifies `topologySpreadConstraints` or `podAntiAffinity`. Both replicas can land on the same node. If that node fails, the operator loses its leader and standby simultaneously, and the webhook becomes fully unavailable — nullifying the benefit of `replicas: 2`.

**Recommendation:**  
Add preferred anti-affinity:
```yaml
affinity:
  podAntiAffinity:
    preferredDuringSchedulingIgnoredDuringExecution:
    - weight: 100
      podAffinityTerm:
        labelSelector:
          matchLabels:
            app: whereabouts-webhook
        topologyKey: kubernetes.io/hostname
```

---

## P5-11: All webhook `failurePolicy: Fail` — webhook outage blocks CNI operations

**Severity:** High  
**Files:** [doc/crds/validatingwebhookconfiguration.yaml](doc/crds/validatingwebhookconfiguration.yaml#L20-L63)

**Description:**  
All three webhooks use `failurePolicy: Fail`. The CEL `matchConditions` bypass the CNI ServiceAccount (`system:serviceaccount:kube-system:whereabouts`) and the operator SA for NodeSlicePool, but does not bypass the operator for IPPool patch operations. More critically, if the webhook pods are down (startup, upgrade, node failure), any other controller or admin modifying these CRDs will be blocked. The IPPool webhook fires on every IPPool CREATE/UPDATE — during outage, even reconciler patches from the operator will fail unless the operator SA is also bypassed for IPPool.

**Recommendation:**  
1. Add a `bypass-operator` matchCondition to the IPPool webhook (the reconciler patches IPPool).
2. Consider `failurePolicy: Ignore` for the IPPool webhook specifically — the CNI SA is already bypassed, and the reconciler is the only other writer. Validation is defense-in-depth, not a hard gate.
3. At minimum, the PDB from P5-9 partially mitigates this.

---

## P5-12: No Deployment `strategy` defined — defaults to 25% maxUnavailable

**Severity:** Medium  
**Files:** [doc/crds/operator-install.yaml](doc/crds/operator-install.yaml#L76), [doc/crds/webhook-install.yaml](doc/crds/webhook-install.yaml#L68)

**Description:**  
Neither Deployment specifies a `strategy`. With `replicas: 2`, the default `RollingUpdate` with `maxUnavailable: 25%` rounds to 1, meaning during a rollout one replica goes down immediately. Combined with no PDB, this can cause brief webhook outages on every image update.

**Recommendation:**  
```yaml
strategy:
  type: RollingUpdate
  rollingUpdate:
    maxUnavailable: 0
    maxSurge: 1
```
This ensures a new pod is ready before the old one is terminated, maintaining availability during upgrades.

---

## P5-13: Image tag `:latest` in all deployment manifests — no rollback safety

**Severity:** High  
**Files:** [doc/crds/operator-install.yaml](doc/crds/operator-install.yaml#L89), [doc/crds/webhook-install.yaml](doc/crds/webhook-install.yaml#L81), [doc/crds/daemonset-install.yaml](doc/crds/daemonset-install.yaml#L81)

**Description:**  
All three manifests use `image: ghcr.io/telekom/whereabouts:latest`. This causes:
1. No `imagePullPolicy` is set, so it defaults to `Always` for `:latest`, generating unnecessary registry pulls on every pod restart.
2. Rollbacks are impossible — `kubectl rollout undo` replays the same `:latest` tag, pulling the same (broken) image.
3. No auditability of which version is running. `kubectl get pods -o jsonpath='{.spec.containers[0].image}'` just says `:latest`.

**Recommendation:**  
Use a placeholder like `VERSION` or a Helm value, and document the release process. At minimum add `imagePullPolicy: IfNotPresent` as a hint to override in production.

---

## P5-14: No `terminationGracePeriodSeconds` for operator or webhook Deployments

**Severity:** Medium  
**Files:** [doc/crds/operator-install.yaml](doc/crds/operator-install.yaml#L85), [doc/crds/webhook-install.yaml](doc/crds/webhook-install.yaml#L73)

**Description:**  
Neither pod spec sets `terminationGracePeriodSeconds`. The default is 30s. Combined with P5-8 (`context.Background()`), the process never initiates graceful shutdown — it runs for 30s doing nothing useful, then gets SIGKILL'd. The leader election lease (default 15s duration, 10s renew) may not expire before the standby acquires it, causing a brief dual-leader scenario if the lease is held by the dying pod.

Even with P5-8 fixed, the default 30s is appropriate for the controller but may be tight for the webhook if long-running admission requests are in-flight.

**Recommendation:**  
Fix P5-8 first, then explicitly set `terminationGracePeriodSeconds: 30` for documentation clarity.

---

## P5-15: No custom application metrics — only framework metrics available

**Severity:** Medium  
**Files:** [internal/controller/ippool_controller.go](internal/controller/ippool_controller.go), [internal/controller/nodeslice_controller.go](internal/controller/nodeslice_controller.go), [internal/controller/overlappingrange_controller.go](internal/controller/overlappingrange_controller.go)

**Description:**  
While the operator exposes Prometheus metrics endpoints (`:8080` controller, `:8082` webhook), there are zero application-specific metrics registered. The only available metrics are controller-runtime's built-in `controller_runtime_reconcile_total`, `controller_runtime_reconcile_errors_total`, and `controller_runtime_reconcile_time_seconds`. Missing operational metrics include:
- Orphaned allocations cleaned per reconcile
- IPPool utilization (allocated/total)
- NodeSlicePool slot exhaustion (the `TODO: fire an event` at line 246)
- Webhook validation rejection rate
- Cert rotation events/errors

Without these, operators can't build meaningful alerts beyond "reconciler is erroring."

**Recommendation:**  
Register at minimum:
```go
var orphanedAllocsCleaned = prometheus.NewCounterVec(
    prometheus.CounterOpts{
        Name: "whereabouts_orphaned_allocations_cleaned_total",
        Help: "Total orphaned IP allocations removed by reconciler",
    },
    []string{"pool"},
)
```
And a gauge for NodeSlicePool capacity exhaustion.

---

## P5-16: No metrics Service for operator — Prometheus cannot scrape

**Severity:** Medium  
**Files:** [doc/crds/operator-install.yaml](doc/crds/operator-install.yaml)

**Description:**  
The webhook deployment has a `Service` (for webhook traffic), but the operator controller deployment has no `Service` at all. Without a Service or ServiceMonitor, Prometheus (via `kubernetes-service-endpoints` or PodMonitor) cannot discover the metrics endpoint on `:8080`. The metrics port is exposed on the container but not reachable via any cluster service.

**Recommendation:**  
Add a headless Service for the operator metrics port, and ideally a `ServiceMonitor` for both the operator and webhook:
```yaml
apiVersion: v1
kind: Service
metadata:
  name: whereabouts-operator-metrics
  namespace: kube-system
  labels:
    app: whereabouts-operator
spec:
  selector:
    app: whereabouts-operator
  ports:
  - port: 8080
    targetPort: 8080
    name: metrics
  clusterIP: None
```

---

## P5-17: Log level  configuration only supports "debug" vs. non-debug — no granularity

**Severity:** Low  
**Files:** [cmd/operator/main.go](cmd/operator/main.go#L56-L68)

**Description:**  
The `setupLogger` function accepts a `--log-level` flag but only distinguishes "debug" from everything else. The `zap.Options.Development = true` switch enables `DPanicLevel` and stacktraces, but doesn't actually set log levels. The controller-runtime logger V-levels (used throughout reconcilers as `logger.V(1).Info(...)`) are not configurable at runtime. An "error" log level option is silently treated the same as "info", which is misleading.

**Recommendation:**  
Use zap's `Level` field properly:
```go
switch logLevel {
case "debug":
    opts.Development = true
case "error":
    lvl := zapcore.ErrorLevel
    opts.Level = &lvl
default:
    // info level is the default
}
```
Or better, use controller-runtime's `zap.Options.BindFlags` for full zap flag integration.

---

## P5-18: Webhook cert volume `optional: true` — silent startup with no TLS

**Severity:** Medium  
**Files:** [doc/crds/webhook-install.yaml](doc/crds/webhook-install.yaml#L125-L129)

**Description:**  
The cert secret volume is mounted with `optional: true`. This means the webhook pod will start even if the secret `whereabouts-webhook-cert` doesn't exist yet. While cert-controller will eventually create it, there's a window where the webhook server binds port 9443 without TLS certs. The readiness probe (which gates on `webhookSetup.ReadyCheck()`) should prevent traffic, but the liveness probe (`/healthz`) will pass — meaning the pod appears healthy but isn't serving.

In the worst case, if cert-controller fails silently (RBAC misconfiguration, P5-7 name mismatch), the pod stays "live" forever but never becomes "ready", creating a confusing operational state with no clear error surfaced.

**Recommendation:**  
Keep `optional: true` for initial startup ordering, but add a startup probe with a higher failure threshold so Kubernetes kills the pod if it never becomes ready:
```yaml
startupProbe:
  httpGet:
    path: /readyz
    port: 8083
  failureThreshold: 30
  periodSeconds: 10
```
This gives cert-controller 5 minutes to provision certs before the pod is killed and restarted.

---

## P5-19: NodeSliceReconciler silently drops nodes when pool is full

**Severity:** Medium  
**Files:** [internal/controller/nodeslice_controller.go](internal/controller/nodeslice_controller.go#L244-L246)

**Description:**  
When there are more nodes than available slice slots, the code hits:
```go
// No slot available — pool is full. TODO: fire an event.
```
No error is returned, no event is emitted, no metric is incremented, and no log is written. The node simply gets no IP slice. This is a silent data loss scenario — pods on that node will fail IPAM allocation with no operator-visible indication of why.

**Recommendation:**  
At minimum, emit a Kubernetes Event on the NodeSlicePool resource and log at `Info` level:
```go
logger.Info("no slice slot available for node — pool full",
    "node", nodeName, "pool", pool.Name, "totalSlots", len(allocations))
// TODO: emit Event
```
Also register a gauge metric `whereabouts_nodeslice_unassigned_nodes` for alerting.

---

## P5-20: `mapNodeToNADs` enqueues ALL NADs on every node event — O(N*M) reconciles

**Severity:** Medium  
**Files:** [internal/controller/nodeslice_controller.go](internal/controller/nodeslice_controller.go#L284-L296)

**Description:**  
When a node is added, removed, or updated, `mapNodeToNADs` lists all NADs across all namespaces and enqueues a reconcile for each one. In a cluster with 100 nodes and 50 NADs, a single node event triggers 50 reconcile runs. Each reconcile then lists all nodes (another API call). Node update events fire frequently (heartbeats, conditions, etc.).

The `MaxConcurrentReconciles: 1` prevents concurrent runs, but the queue depth grows linearly. Since most NADs don't have `node_slice_size`, the reconcile exits early, but the API server load (list all NADs) is paid on every node event.

**Recommendation:**  
1. Filter the node watch to only enqueue on node-lifecycle events (Create, Delete), not Updates. Use `builder.WithPredicates(predicate.GenerationChangedPredicate{})` or a custom predicate.
2. In `mapNodeToNADs`, filter to only NADs that have whereabouts IPAM config with `node_slice_size` to reduce queue depth.

---

## P5-21: No pod-level `securityContext` — missing `seccompProfile` and `runAsUser`

**Severity:** Low  
**Files:** [doc/crds/operator-install.yaml](doc/crds/operator-install.yaml#L124-L127), [doc/crds/webhook-install.yaml](doc/crds/webhook-install.yaml#L117-L120)

**Description:**  
Both Deployments set container-level `securityContext` (`allowPrivilegeEscalation: false`, `readOnlyRootFilesystem: true`, `runAsNonRoot: true`) but lack pod-level `securityContext`. Missing:
- `seccompProfile: { type: RuntimeDefault }` — required by Pod Security Standards "restricted" profile
- `runAsUser` / `runAsGroup` — `runAsNonRoot` without explicit UID means the container image must set a non-root user
- `capabilities: { drop: ["ALL"] }` — required by PSS "restricted"

Clusters with Pod Security Admission in `restricted` mode will reject these pods.

**Recommendation:**
```yaml
spec:
  securityContext:
    runAsNonRoot: true
    seccompProfile:
      type: RuntimeDefault
  containers:
  - securityContext:
      allowPrivilegeEscalation: false
      readOnlyRootFilesystem: true
      capabilities:
        drop: ["ALL"]
```

---

## P5-22: Leader election uses controller-runtime defaults — not tuned for IPAM criticality

**Severity:** Low  
**Files:** [cmd/operator/controller.go](cmd/operator/controller.go#L37-L47)

**Description:**  
The controller manager uses controller-runtime's default leader election parameters (15s lease duration, 10s renew deadline, 2s retry period). These are reasonable for general controllers but may cause a 15-second reconciliation gap when the leader pod is terminated during upgrades or failures. The reconcile interval is 30s, so the effective gap could be up to 45s.

No `LeaderElectionReleaseOnCancel` is set, which means the leader doesn't proactively release the lease on shutdown (compounding P5-8).

**Recommendation:**  
Consider setting:
```go
LeaderElectionReleaseOnCancel: true,
```
This requires P5-8 to be fixed first (signal-aware context). With both fixes, leadership transfer during rolling updates becomes near-instant.

---

## P5-23: `cleanupOverlappingReservations` errors are logged but not returned

**Severity:** Low  
**Files:** [internal/controller/ippool_controller.go](internal/controller/ippool_controller.go#L170-L195)

**Description:**  
The `cleanupOverlappingReservations` method is called after orphaned allocations are removed from the IPPool. If listing or deleting overlapping reservations fails, the errors are logged at V(1) but not returned to the reconciler. This means the reconcile is reported as successful, so no retry is scheduled, and the orphaned `OverlappingRangeIPReservation` CRDs are never cleaned up until the next periodic reconcile (30s default).

While the `OverlappingRangeReconciler` provides a secondary cleanup path, depending on two reconcilers to compensate for each other's error handling is fragile.

**Recommendation:**  
Return an error from `cleanupOverlappingReservations` or accumulate errors and return them:
```go
if err := r.cleanupOverlappingReservations(ctx, &pool, orphanedKeys); err != nil {
    logger.Error(err, "failed to clean up overlapping reservations, will retry")
    return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}
```

---

## P5-24: Webhook cert directory uses `/tmp` — potential ephemeral storage pressure

**Severity:** Low  
**Files:** [cmd/operator/webhook.go](cmd/operator/webhook.go#L92), [doc/crds/webhook-install.yaml](doc/crds/webhook-install.yaml#L123-L124)

**Description:**  
The default cert directory is `/tmp/k8s-webhook-server/serving-certs`, mounted from a Secret volume. Since `readOnlyRootFilesystem: true` is set, the mount works because it's a volume, not a tmpfs write. However, if the cert-controller writes directly to `/tmp` before the volume mount is ready (race condition), it would fail. The path is also shared with controller-runtime's default, which is fine, but `/tmp` in containers without `emptyDir` mounts is backed by the container's writable layer, which may have restricted disk space.

**Recommendation:**  
This is low-risk because the Secret volume mount overrides `/tmp/k8s-webhook-server/serving-certs`. No action required, but consider using a non-`/tmp` path like `/certs` for clarity.

---

## P5-25: NodeSliceReconciler `ensureNodeAssignments` uses full `Status().Update` — not SSA or patch

**Severity:** Low  
**Files:** [internal/controller/nodeslice_controller.go](internal/controller/nodeslice_controller.go#L249-L252)

**Description:**  
`ensureNodeAssignments` reads the current status, modifies allocations in-memory, and does a full `Status().Update()`. If another actor modifies the NodeSlicePool status between the read and the update, the update will fail with a conflict (resource version mismatch). Unlike the IPPool reconciler which uses `MergeFrom` patch, this is a blind overwrite. Since `MaxConcurrentReconciles: 1`, self-conflicts are unlikely, but external modifications (e.g., manual operator intervention) could cause repeated requeue loops.

**Recommendation:**  
Use `client.MergeFrom(pool.DeepCopy())` + `client.Status().Patch()` pattern, consistent with the IPPool reconciler.

---

## Summary

| ID | Title | Severity |
|------|-------|----------|
| P5-7 | Webhook CA bundle name mismatch | **Critical** |
| P5-8 | `context.Background()` — no graceful shutdown | **High** |
| P5-9 | No PodDisruptionBudget | **High** |
| P5-10 | No anti-affinity — replicas may co-locate | Medium |
| P5-11 | `failurePolicy: Fail` blocks CNI during webhook outage | **High** |
| P5-12 | No Deployment strategy defined | Medium |
| P5-13 | Image tag `:latest` — no rollback safety | **High** |
| P5-14 | No `terminationGracePeriodSeconds` explicit | Medium |
| P5-15 | No custom application metrics | Medium |
| P5-16 | No metrics Service for operator | Medium |
| P5-17 | Log level configuration lacks granularity | Low |
| P5-18 | Webhook cert volume `optional: true` — silent TLS failure | Medium |
| P5-19 | NodeSliceReconciler silently drops nodes | Medium |
| P5-20 | `mapNodeToNADs` O(N*M) fan-out on node events | Medium |
| P5-21 | Missing pod-level securityContext / PSS restricted | Low |
| P5-22 | Leader election not tuned, no `ReleaseOnCancel` | Low |
| P5-23 | `cleanupOverlappingReservations` swallows errors | Low |
| P5-24 | Cert directory uses `/tmp` path | Low |
| P5-25 | `ensureNodeAssignments` uses full Update, not Patch | Low |

**Priority Fix Order:** P5-7 → P5-8 → P5-11 → P5-9 → P5-13 → P5-10 → P5-12 → remaining
