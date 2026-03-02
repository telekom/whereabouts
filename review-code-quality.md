# P6 — Go Code Quality Review

**Scope:** New controller-runtime migration code (`cmd/operator/`, `internal/controller/`, `internal/webhook/`, `internal/webhook/certrotator/`) plus spot-check of `pkg/allocate/`, `pkg/config/`, `pkg/logging/`.

## Summary Table

| ID | Title | Severity | File | Lines |
|----|-------|----------|------|-------|
| P6-1 | `defer` inside loop leaks file handles | HIGH | `pkg/config/config.go` | 238–255 |
| P6-2 | Global logging state is not thread-safe | MEDIUM | `pkg/logging/logging.go` | 39–41, 60–77 |
| P6-3 | `Panicf` does not panic | MEDIUM | `pkg/logging/logging.go` | 97–102 |
| P6-4 | Log file handle is never closed; missing `return` in error branch | LOW | `pkg/logging/logging.go` | 131–139 |
| P6-5 | `context.Background()` used instead of `ctrl.SetupSignalHandler()` | MEDIUM | `cmd/operator/controller.go`, `cmd/operator/webhook.go` | 63, 85 |
| P6-6 | `ctrl.GetConfigOrDie()` panics inside Cobra `RunE` | LOW | `cmd/operator/controller.go`, `cmd/operator/webhook.go` | 35, 40 |
| P6-7 | `setupLogger` silently swallows flag error | LOW | `cmd/operator/main.go` | 57 |
| P6-8 | `setupLogger` ignores `"error"` log level | LOW | `cmd/operator/main.go` | 59–64 |
| P6-9 | `denormalizeIPName` unbounded recursion | MEDIUM | `internal/controller/ippool_controller.go` | 241–261 |
| P6-10 | `ensureOwnerRef` uses potentially empty `APIVersion`/`Kind` from NAD | MEDIUM | `internal/controller/nodeslice_controller.go` | 271–274 |
| P6-11 | `ensureOwnerRef` error is silently logged, not returned | LOW | `internal/controller/nodeslice_controller.go` | 260–281 |
| P6-12 | Missing `NeedLeaderElection()` on webhook `Setup` runnable | LOW | `internal/webhook/setup.go` | 17–30 |
| P6-13 | `cleanupOverlappingReservations` lists all reservations per orphaned key (O(N×M)) | MEDIUM | `internal/controller/ippool_controller.go` | 166–196 |
| P6-14 | `cleanupOverlappingReservations` errors silently swallowed | LOW | `internal/controller/ippool_controller.go` | 128–131 |
| P6-15 | `addOffsetToIP` signed arithmetic — negative offsets produce wrong results | LOW | `internal/controller/ippool_controller.go` | 218–236 |
| P6-16 | `NodeSlicePoolValidator` accepts IPv4-invalid prefix lengths | INFO | `internal/webhook/nodeslicepool_webhook.go` | 66–70 |

---

## Verified Pre-existing Issues

### P6-1 — `defer` inside loop leaks file handles
**Severity:** HIGH  
**File:** [pkg/config/config.go](pkg/config/config.go#L238-L255)

```go
for _, confpath := range confdirs {
    if pathExists(confpath) {
        jsonFile, err := os.Open(confpath)
        if err != nil {
            return flatipam, foundflatfile, fmt.Errorf(...)
        }

        defer jsonFile.Close()  // ← deferred inside loop body
        // ...
        return flatipam, foundflatfile, nil  // early return on first match
    }
}
```

**Description:** **Confirmed.** `defer` is scoped to the enclosing *function*, not the loop iteration. Although the current logic returns on the first match (so only one file is opened), the `defer` would accumulate if the logic changes to continue iterating. It's misleading and a maintenance hazard.

**Recommendation:** Replace `defer jsonFile.Close()` with an explicit `jsonFile.Close()` before each return, or extract the loop body into a helper function where `defer` fires per call.

---

### P6-2 — Global logging state is not thread-safe
**Severity:** MEDIUM  
**File:** [pkg/logging/logging.go](pkg/logging/logging.go#L39-L77)

```go
var loggingStderr bool
var loggingFp *os.File
var loggingLevel Level
```

**Description:** **Confirmed.** `loggingStderr`, `loggingFp`, and `loggingLevel` are package-level variables read and written without synchronization. In the CNI binary (single-threaded short-lived process) this is safe, but any concurrent caller (tests, operator) risks data races. `Printf` reads all three globals on every call.

**Recommendation:** Guard with `sync.RWMutex` or use `atomic.Value` / `atomic.Int32` for `loggingLevel`. Alternatively, use a `sync.Once` pattern if configuration is set-once.

---

### P6-3 — `Panicf` doesn't panic
**Severity:** MEDIUM  
**File:** [pkg/logging/logging.go](pkg/logging/logging.go#L96-L101)

```go
func Panicf(format string, a ...interface{}) {
    Printf(PanicLevel, format, a...)
    Printf(PanicLevel, "========= Stack trace output ========")
    Printf(PanicLevel, "%+v", errors.New("Whereabouts Panic"))
    Printf(PanicLevel, "========= Stack trace output end ========")
}
```

**Description:** **Confirmed.** Despite the name `Panicf`, this function only logs a stack trace and returns normally. Callers expecting panic semantics (`recover`-based cleanup, guaranteed abort) will be surprised. Violates the convention set by `log.Panicf` in the standard library.

**Recommendation:** Add `panic(fmt.Sprintf(format, a...))` at the end, or rename the function to something like `LogStackTrace` to avoid confusion.

---

### P6-4 — Log file handle is never closed; missing `return` in error branch
**Severity:** LOW  
**File:** [pkg/logging/logging.go](pkg/logging/logging.go#L131-L139)

```go
func SetLogFile(filename string) {
    if filename == "" {
        return
    }
    fp, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
    if err != nil {
        loggingFp = nil
        fmt.Fprintf(os.Stderr, "Whereabouts logging: cannot open %s", filename)
    }
    loggingFp = fp  // ← always executed, even after error
}
```

**Description:** **Confirmed.** Two issues: (1) When `SetLogFile` is called multiple times, the previous `loggingFp` is overwritten without being closed, leaking a file descriptor. (2) When `os.OpenFile` fails, the code sets `loggingFp = nil` inside the `if err` block but then falls through to `loggingFp = fp` (which happens to also be `nil`, but the control flow is incorrect — there is no `return` after the error branch).

**Recommendation:**
```go
func SetLogFile(filename string) {
    if filename == "" {
        return
    }
    fp, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
    if err != nil {
        loggingFp = nil
        fmt.Fprintf(os.Stderr, "Whereabouts logging: cannot open %s\n", filename)
        return
    }
    if loggingFp != nil {
        loggingFp.Close()
    }
    loggingFp = fp
}
```

---

## New Findings

### P6-5 — `context.Background()` used instead of `ctrl.SetupSignalHandler()`
**Severity:** MEDIUM  
**File:** [cmd/operator/controller.go](cmd/operator/controller.go#L63), [cmd/operator/webhook.go](cmd/operator/webhook.go#L85)

```go
// controller.go:63
return mgr.Start(context.Background())

// webhook.go:85
return mgr.Start(context.Background())
```

**Description:** Both the controller and webhook commands start the controller-runtime manager with `context.Background()`. The idiomatic pattern is `ctrl.SetupSignalHandler()`, which returns a context that is cancelled on `SIGTERM`/`SIGINT`. Without this, the manager will not gracefully shut down on OS signals — `Start()` blocks indefinitely, and a `SIGTERM` will hard-kill the process without draining work queues or releasing the leader election lease. The controller-runtime documentation explicitly recommends `ctrl.SetupSignalHandler()`.

**Recommendation:** Replace `context.Background()` with `ctrl.SetupSignalHandler()`:

```go
return mgr.Start(ctrl.SetupSignalHandler())
```

---

### P6-6 — `ctrl.GetConfigOrDie()` panics inside Cobra `RunE`
**Severity:** LOW  
**File:** [cmd/operator/controller.go](cmd/operator/controller.go#L35), [cmd/operator/webhook.go](cmd/operator/webhook.go#L40)

```go
mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
```

**Description:** `GetConfigOrDie()` calls `os.Exit(1)` (via `klog.Fatal`) if kubeconfig is unavailable. Inside a Cobra `RunE` handler, this bypasses Cobra's error reporting and any deferred cleanup. The convention for `RunE`-based commands is to use `ctrl.GetConfig()` and propagate the error normally.

**Recommendation:** Use `ctrl.GetConfig()` and return the error:
```go
cfg, err := ctrl.GetConfig()
if err != nil {
    return fmt.Errorf("loading kubeconfig: %s", err)
}
mgr, err := ctrl.NewManager(cfg, ctrl.Options{...})
```

---

### P6-7 — `setupLogger` silently swallows flag error
**Severity:** LOW  
**File:** [cmd/operator/main.go](cmd/operator/main.go#L57)

```go
logLevel, _ := cmd.Flags().GetString("log-level")
```

**Description:** The error from `GetString` is discarded with `_`. While unlikely to fail in practice (the flag is registered as a PersistentFlag), silently swallowing the error means a misconfiguration would produce no diagnostic — `logLevel` would silently default to `""` and fall through to the `default` case.

**Recommendation:**
```go
logLevel, err := cmd.Flags().GetString("log-level")
if err != nil {
    ctrl.Log.WithName("setup").Error(err, "failed to read log-level flag, defaulting to info")
}
```

---

### P6-8 — `setupLogger` ignores `"error"` log level
**Severity:** LOW  
**File:** [cmd/operator/main.go](cmd/operator/main.go#L59-L64)

```go
switch logLevel {
case "debug":
    opts.Development = true
default:
    opts.Development = false
}
```

**Description:** The flag description says `Log level (debug, info, error)`, but only `"debug"` is handled as a distinct case. Both `"info"` and `"error"` produce the same configuration. Users setting `--log-level=error` would still see info-level logs, which is misleading.

**Recommendation:** Wire up the `opts.Level` field:
```go
switch logLevel {
case "debug":
    opts.Development = true
case "error":
    lvl := zap.NewAtomicLevelAt(zapcore.ErrorLevel)
    opts.Level = &lvl
default:
    opts.Development = false
}
```

---

### P6-9 — `denormalizeIPName` unbounded recursion
**Severity:** MEDIUM  
**File:** [internal/controller/ippool_controller.go](internal/controller/ippool_controller.go#L241-L261)

```go
func denormalizeIPName(name string) net.IP {
    if ip := net.ParseIP(name); ip != nil {
        return ip
    }
    denormalized := strings.ReplaceAll(name, "-", ":")
    if ip := net.ParseIP(denormalized); ip != nil {
        return ip
    }
    parts := strings.SplitN(name, "-", 2)
    if len(parts) == 2 {
        return denormalizeIPName(parts[1]) // ← recursive call
    }
    return nil
}
```

**Description:** The function calls itself recursively, stripping the first dash-delimited segment on each call. For a name like `"a-b-c-d-e-f-..."` with many dash-separated segments, this recurses proportionally to the number of dashes. While Go's stack is growable and Kubernetes names are limited to 253 chars, the recursion is unnecessary. More importantly, the `strings.ReplaceAll(name, "-", ":")` step in earlier recursion levels can produce false positives for names with network-name prefixes that contain dashes (e.g. `"my-net-10.0.0.1"` → tries parsing `"my:net:10.0.0.1"` as an IP on the first pass).

**Recommendation:** Convert to an iterative approach:
```go
func denormalizeIPName(name string) net.IP {
    candidate := name
    for {
        if ip := net.ParseIP(candidate); ip != nil {
            return ip
        }
        denormalized := strings.ReplaceAll(candidate, "-", ":")
        if ip := net.ParseIP(denormalized); ip != nil {
            return ip
        }
        parts := strings.SplitN(candidate, "-", 2)
        if len(parts) != 2 {
            return nil
        }
        candidate = parts[1]
    }
}
```

---

### P6-10 — `ensureOwnerRef` uses potentially empty `APIVersion`/`Kind` from NAD
**Severity:** MEDIUM  
**File:** [internal/controller/nodeslice_controller.go](internal/controller/nodeslice_controller.go#L270-L274)

```go
pool.OwnerReferences = append(pool.OwnerReferences, metav1.OwnerReference{
    APIVersion: nad.APIVersion,   // ← may be empty after Get
    Kind:       nad.Kind,         // ← may be empty after Get
    Name:       nad.Name,
    UID:        nad.UID,
})
```

**Description:** When a NAD is fetched via the controller-runtime client (typed `Get`), the `APIVersion` and `Kind` fields on the runtime object are typically empty — Go structs deserialized from API responses don't carry GVK metadata unless using `Unstructured`. The `createPool` method correctly uses `nadv1.SchemeGroupVersion.WithKind("NetworkAttachmentDefinition")` via `metav1.NewControllerRef`, but `ensureOwnerRef` reads from the struct's fields which are likely `""`. This creates an OwnerReference with empty `APIVersion` and `Kind`, which breaks garbage collection.

**Recommendation:** Use the GVK constant instead:
```go
pool.OwnerReferences = append(pool.OwnerReferences, metav1.OwnerReference{
    APIVersion: nadv1.SchemeGroupVersion.String(),
    Kind:       "NetworkAttachmentDefinition",
    Name:       nad.Name,
    UID:        nad.UID,
})
```

---

### P6-11 — `ensureOwnerRef` error is silently logged, not returned
**Severity:** LOW  
**File:** [internal/controller/nodeslice_controller.go](internal/controller/nodeslice_controller.go#L260-L281)

```go
func (r *NodeSliceReconciler) ensureOwnerRef(ctx context.Context, pool *..., nad *...) {
    // ...
    if err := r.client.Patch(ctx, pool, patch); err != nil {
        logger.Error(err, "failed to add OwnerReference to NodeSlicePool",
            "pool", pool.Name, "nad", nad.Name)
    }
}
```

**Description:** `ensureOwnerRef` is called from `Reconcile` (line 130) but its signature is `void` — patch failures are logged but not propagated. The caller cannot detect failure and won't trigger a re-queue. For a network failure or conflict, the OwnerReference will never be added, and orphaned NodeSlicePools may persist after NAD deletion.

**Recommendation:** Return `error` from `ensureOwnerRef` and handle it in the caller:
```go
func (r *NodeSliceReconciler) ensureOwnerRef(...) error {
    // ...
    return r.client.Patch(ctx, pool, patch)
}
```

---

### P6-12 — Missing `NeedLeaderElection()` on webhook `Setup` runnable
**Severity:** LOW  
**File:** [internal/webhook/setup.go](internal/webhook/setup.go#L17-L30)

```go
type Setup struct {
    mgr       manager.Manager
    certReady <-chan struct{}
    ready     atomic.Bool
}
```

**Description:** `Setup` implements `manager.Runnable` (via `Start`) but does not implement `manager.LeaderElectionRunnable` (`NeedLeaderElection() bool`). By default, controller-runtime treats runnables without that interface as *requiring* leader election — they only run after a lease is acquired. The webhook command disables leader election (`LeaderElection: false`), so this is not a bug today. However, if someone enables leader election for HA on the webhook manager, the `Setup` runnable would silently fail to start on non-leader replicas.

**Recommendation:** Add the interface method to make the intent explicit:
```go
func (s *Setup) NeedLeaderElection() bool { return false }
```

---

### P6-13 — `cleanupOverlappingReservations` issues a full List per orphaned key (O(N×M))
**Severity:** MEDIUM  
**File:** [internal/controller/ippool_controller.go](internal/controller/ippool_controller.go#L166-L196)

```go
func (r *IPPoolReconciler) cleanupOverlappingReservations(ctx context.Context, pool *..., keys []string) {
    for _, key := range keys {
        // ...
        var reservations whereaboutsv1alpha1.OverlappingRangeIPReservationList
        if err := r.client.List(ctx, &reservations, client.InNamespace(pool.Namespace)); err != nil {
            // ...
            continue
        }
        for i := range reservations.Items {
            res := &reservations.Items[i]
            resIP := denormalizeIPName(res.Name)
            if resIP != nil && resIP.Equal(ip) {
                // delete...
            }
        }
    }
}
```

**Description:** For each orphaned allocation key, a full `List` of all `OverlappingRangeIPReservation` resources in the namespace is issued. With `N` orphaned keys and `M` reservations, this is `O(N)` API calls with `O(N × M)` total comparisons. The listing should be hoisted out of the loop.

**Recommendation:** List once before the loop:
```go
var reservations whereaboutsv1alpha1.OverlappingRangeIPReservationList
if err := r.client.List(ctx, &reservations, client.InNamespace(pool.Namespace)); err != nil {
    logger.V(1).Info("failed to list overlapping reservations", "error", err)
    return
}
for _, key := range keys {
    ip := allocationKeyToIP(pool, key)
    if ip == nil { continue }
    for i := range reservations.Items {
        // match and delete
    }
}
```

---

### P6-14 — `cleanupOverlappingReservations` errors silently dropped
**Severity:** LOW  
**File:** [internal/controller/ippool_controller.go](internal/controller/ippool_controller.go#L128-L131)

```go
// Also clean up any corresponding OverlappingRangeIPReservation CRDs.
r.cleanupOverlappingReservations(ctx, &pool, orphanedKeys)
```

**Description:** `cleanupOverlappingReservations` returns `void` and logs errors internally. The caller in `Reconcile` invokes it as fire-and-forget. List/delete failures are not surfaced to the reconcile loop, so a transient API server error will leave ORIP CRDs orphaned until the next reconcile cycle (or the `OverlappingRangeReconciler` picks them up). This is defensible as "best-effort" cleanup, but it should be documented and ideally propagated.

**Recommendation:** Either return `error` for requeue, or add a comment at the call site documenting the design decision:
```go
// Best-effort cleanup — OverlappingRangeReconciler provides secondary GC path.
r.cleanupOverlappingReservations(ctx, &pool, orphanedKeys)
```

---

### P6-15 — `addOffsetToIP` signed arithmetic with negative offsets
**Severity:** LOW  
**File:** [internal/controller/ippool_controller.go](internal/controller/ippool_controller.go#L218-L236)

```go
func addOffsetToIP(ip net.IP, offset int64) net.IP {
    // ...
    carry := offset
    for i := len(result) - 1; i >= 0 && carry != 0; i-- {
        sum := int64(result[i]) + carry
        result[i] = byte(sum & 0xff)
        carry = sum >> 8  // arithmetic right-shift on signed int64
    }
    return result
}
```

**Description:** The right-shift `carry = sum >> 8` on a signed `int64` performs an *arithmetic* shift, preserving the sign bit. For negative offsets (which shouldn't normally occur but the `int64` type permits), this propagates `-1` carries indefinitely until `i < 0`, producing incorrect IP wrapping behavior. Additionally, `byte(sum & 0xff)` masks correctly, but the carry computation for negative sums is not standard two's-complement addition on a byte-by-byte basis.

**Recommendation:** Since offsets in allocation keys are always non-negative, either change the parameter type to `uint64` or add a guard:
```go
if offset < 0 {
    return nil
}
```

---

### P6-16 — `NodeSlicePoolValidator` accepts IPv4-invalid prefix lengths
**Severity:** INFO  
**File:** [internal/webhook/nodeslicepool_webhook.go](internal/webhook/nodeslicepool_webhook.go#L66-L70)

```go
if size < 1 || size > 128 {
    return nil, fmt.Errorf("invalid spec.sliceSize %q: prefix length must be between 1 and 128",
        pool.Spec.SliceSize)
}
```

**Description:** The upper bound of 128 is correct for IPv6 but allows a prefix like `/64` on an IPv4 range (`10.0.0.0/24`), which is nonsensical. The actual slicing logic in `iphelpers.DivideRangeBySize` would catch it at runtime, but the webhook could provide a better user experience by cross-checking against the range's address family (≤32 for IPv4, ≤128 for IPv6).

**Recommendation:** Validate that slice size is appropriate for the address family of the range.

---

## Positive Observations

The new controller-runtime migration code is well-structured:

1. **Error format convention (`%s` not `%w`)** — consistently applied across all new files. No `%w` usage found in any non-vendor code.
2. **Interface compliance guards** — all reconcilers have `var _ reconcile.Reconciler = &XReconciler{}` compile-time assertions.
3. **Context propagation** — `log.FromContext(ctx)` used consistently in reconcilers; `ctrl.Log.WithName()` used in non-reconcile paths (setup, cert rotator).
4. **RBAC markers** — comprehensive and well-scoped kubebuilder RBAC annotations on each reconciler, webhook, and the cert rotator.
5. **Webhook readiness gate** — `Setup.ReadyCheck()` correctly blocks readiness until webhooks are registered, using `atomic.Bool` for thread safety.
6. **Patch semantics** — `MergeFrom(pool.DeepCopy())` correctly captures pre-mutation state before patching.
7. **No dead imports, unused variables, or `%w` violations** detected in any of the new files.
8. **No type assertions without `ok` check** found in new code.
9. **Consistent naming** — reconcilers follow controller-runtime conventions (`SetupXReconciler`, `Reconcile`, typed builders).
