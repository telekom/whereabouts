# Data Integrity / IP Leak Review — Whereabouts IPAM

**Reviewer focus:** Every code path where an IP can be allocated in the CRD but not used by a live pod (leaked IP), or allocated to a live interface but missing from the CRD (double-allocation risk).

---

## Confirmed Known Issues (L1–L5)

### L1 — `cmdDel` unconditionally swallows errors → leaked IPs

| Field | Detail |
|---|---|
| **Severity** | **Critical** |
| **File** | [cmd/whereabouts.go](cmd/whereabouts.go#L117-L121) |
| **Code path** | `cmdDel` → `kubernetes.IPManagement(Deallocate)` → result discarded |

```go
func cmdDel(client *kubernetes.KubernetesIPAM) error {
    ctx, cancel := context.WithTimeout(context.Background(), types.DelTimeLimit)
    defer cancel()
    _, _ = kubernetes.IPManagement(ctx, types.Deallocate, client.Config, client) // ← BOTH returns discarded
    return nil
}
```

**Description:** Every possible failure inside `IPManagement`—K8s API unreachable, leader election timeout, 100 retry exhaustion, overlapping-range delete failure—is silently discarded. The container runtime considers DEL successful and will **never retry**. The allocation persists in the IPPool CRD indefinitely.

**Impact:** Any transient failure during pod teardown permanently leaks the IP. Under cluster instability (API server overloaded, etcd leader election), this can rapidly consume the entire IP range.

**Recommendation:** Return the error from `IPManagement`. The CNI spec allows DEL to return errors; the container runtime will retry. At minimum, return errors for non-`NotFound` failures:
```go
func cmdDel(client *kubernetes.KubernetesIPAM) error {
    ctx, cancel := context.WithTimeout(context.Background(), types.DelTimeLimit)
    defer cancel()
    _, err := kubernetes.IPManagement(ctx, types.Deallocate, client.Config, client)
    return err
}
```

---

### L2 — Partial multi-range allocation not rolled back → leaked IPs

| Field | Detail |
|---|---|
| **Severity** | **High** |
| **File** | [pkg/storage/kubernetes/ipam.go](pkg/storage/kubernetes/ipam.go#L510-L560) |
| **Code path** | `IPManagementKubernetesUpdate` → `for _, ipRange := range ipamConf.IPRanges` |

**Trace:**
1. `ipamConf.IPRanges` has two ranges: `[10.0.0.0/24, 10.1.0.0/24]`
2. Range 0: `pool.Update()` succeeds → IP `10.0.0.1` committed to IPPool CRD
3. Range 0: `UpdateOverlappingRangeAllocation(Allocate)` succeeds
4. Range 1: `pool.Update()` fails after 100 retries → error returned
5. Function returns `(newips=[10.0.0.1/24], err)` — error propagated
6. `cmdAdd` at [cmd/whereabouts.go#L97](cmd/whereabouts.go#L97) returns the error to the container runtime
7. **IP `10.0.0.1` is committed in the CRD but the pod never receives the CNI result**

**Impact:** For dual-stack or multi-range configurations, a failure on any range after the first leaves committed allocations as leaks. The reconciler is the only cleanup path, but it depends on the pod being fully gone (pod may never have started).

**Recommendation:** Implement compensating rollback: on failure after one or more successful range allocations, iterate the already-committed ranges and call `DeallocateIP` + `pool.Update()` + overlapping-range cleanup for each.

---

### L3 — Non-atomic IPPool + OverlappingRange update → orphaned reservation or unprotected IP

| Field | Detail |
|---|---|
| **Severity** | **High** |
| **File** | [pkg/storage/kubernetes/ipam.go](pkg/storage/kubernetes/ipam.go#L547-L558) |
| **Code path** | After `pool.Update()` succeeds → `UpdateOverlappingRangeAllocation()` may fail |

The two CRD writes are sequential and non-transactional:

```go
err = pool.Update(requestCtx, usereservelist)  // ① IPPool patched
// ... break RETRYLOOP ...
err = overlappingrangestore.UpdateOverlappingRangeAllocation(...)  // ② OverlappingRange created/deleted
```

**Failure on Allocate path:**
- ① succeeds: IP reserved in IPPool
- ② fails: `OverlappingRangeIPReservation` not created
- Error returned → `cmdAdd` fails → pod doesn't get the IP
- **But the IP is committed in the IPPool CRD** with a podRef for a pod that never started
- Another pod with an overlapping range will **not** be blocked from this IP → **double-allocation risk**

**Failure on Deallocate path:**
- ① succeeds: allocation removed from IPPool
- ② fails: `OverlappingRangeIPReservation` not deleted → orphan
- Error returned but **swallowed by `cmdDel` (L1)**
- IP is free in the IPPool but **blocked** in OverlappingRange → IP unusable in other overlapping ranges

**Recommendation:** For Allocate: create the `OverlappingRangeIPReservation` **before** updating the IPPool. If the overlapping range create fails, the pool is unchanged and no inconsistency exists. For Deallocate: delete the overlapping reservation first, then update the pool.

---

### L4 — DEL after container restart: `containerID` mismatch → leaked IP

| Field | Detail |
|---|---|
| **Severity** | **High** |
| **File** | [pkg/allocate/allocate.go](pkg/allocate/allocate.go#L56-L64) |
| **Code path** | `DeallocateIP` → `getMatchingIPReservationIndex(reservelist, containerID, ifName)` |

```go
func getMatchingIPReservationIndex(reservelist []types.IPReservation, id, ifName string) int {
    for idx, v := range reservelist {
        if v.ContainerID == id && v.IfName == ifName {   // ← matches on containerID
            return idx
        }
    }
    return -1
}
```

**Scenario:** Container crashes and kubelet recreates the sandbox with a **new** `containerID`. If `cmdDel` is then called with the **old** containerID (stale cleanup), the match fails. In `IPManagementKubernetesUpdate`:

```go
if ipforoverlappingrangeupdate == nil {
    // Do not fail if allocation was not found.
    logging.Debugf("Failed to find allocation for container ID: %s", ipam.ContainerID)
    return nil, nil   // ← returns success, doesn't clean up
}
```

The function returns `nil, nil` (no error), so `cmdDel` also returns `nil`. The allocation persists.

**Mitigating factor:** The idempotent ADD in `AssignIP` updates the `containerID` in the CRD to the new value when it matches by `podRef+ifName`. So a subsequent DEL with the **new** containerID would succeed. The leak is limited to cases where DEL is called with a stale containerID AND no subsequent ADD occurs (e.g., pod is deleted during restart).

**Impact:** IP leaked until reconciler cleanup.

**Recommendation:** Add a fallback match on `podRef + ifName` in `DeallocateIP` when `containerID + ifName` match fails.

---

### L5 — Idempotent ADD masks containerID change but creates DEL mismatch risk

| Field | Detail |
|---|---|
| **Severity** | **Medium** |
| **File** | [pkg/allocate/allocate.go](pkg/allocate/allocate.go#L30-L42) |
| **Code path** | `AssignIP` → podRef+ifName match → updates containerID in-place |

```go
for i, r := range reservelist {
    if r.PodRef == podRef && r.IfName == ifName {
        if r.ContainerID != containerID {
            logging.Debugf("updating container ID: %q", containerID)
            reservelist[i].ContainerID = containerID   // ← CRD updated to new containerID
        }
        return net.IPNet{IP: r.IP, Mask: ipnet.Mask}, reservelist, nil
    }
}
```

**Race window:** If `cmdAdd(containerID_B)` and `cmdDel(containerID_A)` execute concurrently for the same pod:
1. Both read the IPPool (allocation has `containerID_A`)
2. ADD: matches by podRef+ifName → updates to `containerID_B` in memory → `pool.Update()` succeeds first (CRD now has `containerID_B`)
3. DEL: matches by `containerID_A+ifName` → removes allocation → `pool.Update()` fails (resourceVersion mismatch) → retries → re-reads pool → CRD now has `containerID_B` → `containerID_A` doesn't match → `return nil, nil`
4. **IP is preserved correctly** (ADD won the race)

But in the reverse order:
1. DEL succeeds first → allocation removed
2. ADD retries → no existing allocation → allocates **new IP** (potentially different)
3. Old IP is free → another pod gets it → correct behavior

**Impact:** Mostly mitigated by optimistic concurrency. The remaining risk is the L4 scenario where a stale DEL fails silently.

**Recommendation:** Same as L4 — add podRef+ifName fallback match in `DeallocateIP`.

---

## New Findings (L6–L13)

### L6 — Multi-range deallocation aborts all remaining ranges on first miss

| Field | Detail |
|---|---|
| **Severity** | **Critical** |
| **File** | [pkg/storage/kubernetes/ipam.go](pkg/storage/kubernetes/ipam.go#L524-L530) |
| **Code path** | `IPManagementKubernetesUpdate` → Deallocate → first range → `DeallocateIP` returns nil → `return nil, nil` |

```go
case whereaboutstypes.Deallocate:
    updatedreservelist, ipforoverlappingrangeupdate = allocate.DeallocateIP(reservelist, ipam.ContainerID, ipam.IfName)
    if ipforoverlappingrangeupdate == nil {
        // Do not fail if allocation was not found.
        logging.Debugf("Failed to find allocation for container ID: %s", ipam.ContainerID)
        requestCancel()
        return nil, nil   // ← EXITS the entire function, not just this range
    }
```

**Trace:**
1. Pod has IPs from two ranges: `10.0.0.0/24` (IPPool-A) and `10.1.0.0/24` (IPPool-B)
2. `cmdDel` is called; `ipamConf.IPRanges` = `[range_A, range_B]`
3. Processing `range_A`: `DeallocateIP(pool_A.Allocations(), containerID, ifName)`:
   - If the containerID doesn't match (L4 scenario) or the allocation was already cleaned by the reconciler → returns `nil` IP
4. `return nil, nil` — **function exits immediately**
5. `range_B` is **never processed** → its allocation is permanently leaked

**Impact:** In any dual-stack or multi-range configuration, if the first range's deallocation can't find the allocation (containerID mismatch, already cleaned, wrong pool), all subsequent ranges are silently skipped. Combined with L1 (error swallowed), this is unrecoverable without the reconciler.

**Recommendation:** Replace `return nil, nil` with `continue` (to the next range) or `break RETRYLOOP` (to proceed to the next range after the inner loop):
```go
if ipforoverlappingrangeupdate == nil {
    logging.Debugf("Failed to find allocation for container ID: %s", ipam.ContainerID)
    requestCancel()
    break RETRYLOOP  // skip this range's overlapping cleanup, proceed to next range
}
```

---

### L7 — No backoff between retries → exhaustion under contention

| Field | Detail |
|---|---|
| **Severity** | **Medium** |
| **File** | [pkg/storage/kubernetes/ipam.go](pkg/storage/kubernetes/ipam.go#L485-L544) |
| **Code path** | `RETRYLOOP: for j := 0; j < storage.DatastoreRetries; j++` |

The retry loop has **no sleep or exponential backoff** between iterations. Each iteration:
1. Creates a context with 10s timeout
2. Reads the IPPool
3. Computes the allocation/deallocation
4. Attempts `pool.Update()` with JSON Patch + resourceVersion test
5. On conflict (`temporaryError`), immediately retries

Under high contention (many pods being allocated simultaneously using the same IPPool), all 100 retries can be consumed in under a second:
- Read pool → compute → patch → conflict → immediately retry
- Each cycle is dominated by 2 API calls (~5-20ms each)
- 100 retries × ~40ms ≈ 4 seconds of rapid-fire retries

**Impact:** For Allocate: `cmdAdd` fails → kubelet retries (recoverable). For Deallocate: the error is swallowed by L1 → **IP permanently leaked**. Under scale testing or burst scenarios, this can cause widespread IP leakage.

**Recommendation:** Add exponential backoff with jitter between retries. Even a simple `time.Sleep(time.Duration(j) * 10 * time.Millisecond)` would dramatically reduce contention.

---

### L8 — Reconciler false positive on partially-annotated multi-network pods

| Field | Detail |
|---|---|
| **Severity** | **High** |
| **File** | [internal/controller/ippool_controller.go](internal/controller/ippool_controller.go#L121-L127) |
| **Code path** | `Reconcile` → `isPodUsingIP` → pod has annotation but IP missing |

```go
// In Reconcile():
poolIP := allocationKeyToIP(&pool, key)
if poolIP != nil && !isPodUsingIP(&pod, poolIP) {
    orphanedKeys = append(orphanedKeys, key)   // ← false positive
}
```

[`isPodUsingIP`](internal/controller/ippool_controller.go#L280-L302) correctly returns `true` when the annotation is absent entirely (safe default). But when the annotation **exists** and does **not** contain the specific IP, it returns `false`.

**Scenario:**
1. Pod with Multus has two network attachments: `net-A` (IPPool-A) and `net-B` (IPPool-B)
2. `cmdAdd` for `net-A` completes → annotation updated with `net-A`'s IP
3. `cmdAdd` for `net-B` is still running → IPPool-B has the allocation, but annotation doesn't yet include `net-B`'s IP
4. Reconciler processes IPPool-B → sees allocation → pod exists → annotation exists (from `net-A`) → `net-B`'s IP **not in annotation** → marked orphaned → **deleted**
5. `cmdAdd` for `net-B` eventually succeeds but the allocation was just removed → IP conflict or allocation failure on next retry

**Impact:** Under multi-network configurations with Multus, the reconciler can race with `cmdAdd` and prematurely delete valid allocations. This produces either a leaked IP (allocation removed, cmdAdd retry allocates a different IP, original IP is orphaned in any overlapping range reservation) or worse, a double allocation.

**Recommendation:** Add a grace period check: skip `isPodUsingIP` for allocations whose pods were created within the last N seconds, or only run this check for pods in `Running` phase with `Ready` condition `True`. Alternatively, check if the allocation's `containerID` matches any of the pod's container statuses.

---

### L9 — Reconciler reclaims DisruptionTarget IPs before grace period expires → potential double allocation

| Field | Detail |
|---|---|
| **Severity** | **Medium** |
| **File** | [internal/controller/ippool_controller.go](internal/controller/ippool_controller.go#L108-L113) |
| **Code path** | `Reconcile` → `isPodMarkedForDeletion` → immediately orphaned |

```go
if isPodMarkedForDeletion(pod.Status.Conditions) {
    orphanedKeys = append(orphanedKeys, key)
    continue
}
```

[`isPodMarkedForDeletion`](internal/controller/ippool_controller.go#L263-L272) checks for `DisruptionTarget` with `DeletionByTaintManager`. When the taint manager marks a pod for eviction, the pod may still be running within its termination grace period (default 30s). The reconciler immediately removes the allocation.

**Trace:**
1. Node gets tainted (e.g., `node.kubernetes.io/not-ready`)
2. Taint manager sets `DisruptionTarget` condition on the pod
3. Reconciler fires → sees `DisruptionTarget` → removes allocation from IPPool
4. Pod is still running (grace period not expired) → still has the IP on its interface
5. Another pod is scheduled → gets the same IP from the IPPool → **IP conflict on the wire**
6. Original pod eventually terminates (grace period expires)

**Mitigating factor:** If the node is truly not-ready, the pod is likely unreachable anyway, and the IP conflict is theoretical rather than practical. But for taints like `PreferNoSchedule` upgraded to `NoSchedule`, the node may still be reachable.

**Impact:** Brief window of IP conflict (typically 30s) during taint-based evictions on reachable nodes.

**Recommendation:** Also check `pod.DeletionTimestamp` and, if set, verify that the grace period has expired before treating the allocation as orphaned. Or defer cleanup to after the pod is actually `NotFound`.

---

### L10 — Allocate: IPPool committed before OverlappingRange → double-allocation window

| Field | Detail |
|---|---|
| **Severity** | **High** |
| **File** | [pkg/storage/kubernetes/ipam.go](pkg/storage/kubernetes/ipam.go#L538-L558) |
| **Code path** | `pool.Update()` succeeds → `UpdateOverlappingRangeAllocation(Allocate)` not yet called |

```go
// Step 1: IPPool patched — allocation committed
err = pool.Update(requestCtx, usereservelist)
// ... break RETRYLOOP ...

// Step 2: Overlapping range reservation created
if ipamConf.OverlappingRanges {
    if !skipOverlappingRangeUpdate {
        err = overlappingrangestore.UpdateOverlappingRangeAllocation(...)  // ← may fail
```

Between Step 1 and Step 2, a concurrent `cmdAdd` from a **different overlapping range** can:
1. Read its own IPPool (different pool, same IP subnet)
2. Check `GetOverlappingRangeIPReservation` → **reservation doesn't exist yet** (Step 2 hasn't run)
3. Allocate the **same IP** → commit to its own IPPool → create its own overlapping reservation

**Result:** Two pods have the same IP from two different (overlapping) ranges. Both have allocations in their respective IPPools and both may have OverlappingRangeIPReservations (the second create would fail with `AlreadyExists`, but the IPPool is already committed).

**Impact:** Double allocation. This race window exists every time an IP is allocated with `enable_overlapping_ranges: true`. The window is small (milliseconds) but non-zero and increases with API server latency.

**Recommendation:** Reverse the order: create the `OverlappingRangeIPReservation` first (acts as a distributed lock), then update the IPPool. If pool.Update fails, delete the reservation to compensate.

---

### L11 — Leader election timeout on DEL → silent IP leak

| Field | Detail |
|---|---|
| **Severity** | **Medium** |
| **File** | [pkg/storage/kubernetes/ipam.go](pkg/storage/kubernetes/ipam.go#L414-L425) |
| **Code path** | `IPManagement` → goroutine → `ctx.Done()` → error set → `cmdDel` swallows it |

```go
go func() {
    defer wg.Done()
    for {
        select {
        case <-ctx.Done():
            err = fmt.Errorf("time limit exceeded while waiting to become leader")
            stopM <- struct{}{}
            return
        case <-leader:
            // ... do work
```

`DelTimeLimit = 1 * time.Minute`. If leader election takes longer (e.g., lease held by another node doing a long allocation, or API server slow), the context expires. The error `"time limit exceeded"` is set, but `cmdDel` discards it (L1).

**Trace:**
1. Pod deleted → kubelet calls `cmdDel`
2. `IPManagement` starts leader election with 1-minute timeout
3. Leader lease is held by another instance for >1 minute (e.g., slow allocation with retries)
4. Context expires → error returned
5. `cmdDel` discards error → returns `nil` → kubelet won't retry
6. Allocation persists in IPPool CRD → leaked IP

**Impact:** Under contention or when leader election is slow, DEL operations silently fail. This compounds with scale: more pods = more contention = more timeouts = more leaks.

**Recommendation:** In addition to fixing L1, consider making DEL not require leader election (deallocation of a specific containerID+ifName is idempotent and less conflict-prone than allocation).

---

### L12 — `cleanupOverlappingReservations` swallows errors → orphaned OverlappingRange CRDs

| Field | Detail |
|---|---|
| **Severity** | **Low** |
| **File** | [internal/controller/ippool_controller.go](internal/controller/ippool_controller.go#L140-L141) |
| **Code path** | `Reconcile` → `removeAllocations` succeeds → `cleanupOverlappingReservations` errors logged only |

```go
if len(orphanedKeys) > 0 {
    if err := r.removeAllocations(ctx, &pool, orphanedKeys); err != nil {
        return ctrl.Result{}, fmt.Errorf("removing orphaned allocations: %s", err)
    }
    // Also clean up any corresponding OverlappingRangeIPReservation CRDs.
    r.cleanupOverlappingReservations(ctx, &pool, orphanedKeys)  // ← fire-and-forget
}
```

Inside [`cleanupOverlappingReservations`](internal/controller/ippool_controller.go#L149-L176):
```go
if err := r.client.List(ctx, &reservations, ...); err != nil {
    logger.V(1).Info("failed to list overlapping reservations", "error", err)
    continue  // ← swallowed
}
// ...
if err := r.client.Delete(ctx, res); err != nil && !errors.IsNotFound(err) {
    logger.Error(err, "failed to delete overlapping reservation", ...)
    // ← no return, just logged
}
```

**Impact:** After the IPPool allocation is removed, the corresponding `OverlappingRangeIPReservation` may persist. This IP would be free in the IPPool but **blocked** in all other overlapping ranges. The `OverlappingRangeReconciler` provides a secondary cleanup path but depends on the pod being gone.

**Mitigating factor:** The `OverlappingRangeReconciler` independently watches these CRDs and deletes them when the referenced pod no longer exists. So this is eventually consistent.

**Recommendation:** Return errors from `cleanupOverlappingReservations` and requeue the reconciliation if cleanup fails, rather than logging and moving on.

---

### L13 — `newLeaderElector` returns nil on multiple error paths → unrecoverable DEL failure

| Field | Detail |
|---|---|
| **Severity** | **Medium** |
| **File** | [pkg/storage/kubernetes/ipam.go](pkg/storage/kubernetes/ipam.go#L378-L408) |
| **Code path** | `newLeaderElector` → returns `nil, leaderOK, deposed` on error → `IPManagement` → `le == nil` → error → cmdDel swallows |

```go
func newLeaderElector(...) (*leaderelection.LeaderElector, chan struct{}, chan struct{}) {
    // ...
    hostname, err := getNodeName(ipamConf)
    if err != nil {
        logging.Errorf("Failed to create leader elector: %v", err)
        return nil, leaderOK, deposed    // ← nil leader elector
    }
    // ...
    le, err := leaderelection.NewLeaderElector(...)
    if err != nil {
        logging.Errorf("Failed to create leader elector: %v", err)
        return nil, leaderOK, deposed    // ← nil leader elector
    }
```

In `IPManagement`:
```go
le, leader, deposed := newLeaderElector(...)
if le == nil {
    return newips, fmt.Errorf("failed to create leader elector")
}
```

**Impact:** If hostname resolution fails, or `LeaderElector` construction fails, the DEL operation cannot proceed. Combined with L1, the error is swallowed and the IP is permanently leaked. This is especially problematic on nodes where `/etc/hostname` is misconfigured or the `NODENAME` env var is missing in the `node_slice_size` path.

**Recommendation:** For `Deallocate` mode, bypass leader election entirely since deallocation by `containerID+ifName` is inherently idempotent and safe for concurrent execution (unlike allocation which must find the lowest free IP).

---

## Summary Table

| ID | Severity | Category | Root Cause | Reconciler Recoverable? |
|----|----------|----------|------------|------------------------|
| L1 | Critical | Leak | `cmdDel` discards all errors | Yes, eventually |
| L2 | High | Leak | No rollback on partial multi-range alloc | Depends on pod state |
| L3 | High | Leak / Double-alloc | Non-atomic IPPool + OverlappingRange writes | Partially |
| L4 | High | Leak | DEL matches on containerID, not podRef | Yes, eventually |
| L5 | Medium | Race | ADD updates containerID, stale DEL misses | Yes, eventually |
| L6 | Critical | Leak | `return nil,nil` aborts multi-range DEL | Yes, eventually |
| L7 | Medium | Leak | No retry backoff → exhaustion | Yes, eventually |
| L8 | High | Double-alloc | Reconciler races with multi-net cmdAdd | No — allocation deleted |
| L9 | Medium | Double-alloc | DisruptionTarget cleaned before grace period | Transient (grace period) |
| L10 | High | Double-alloc | OverlappingRange created after IPPool commit | No — two allocations exist |
| L11 | Medium | Leak | Leader election timeout on DEL, error swallowed | Yes, eventually |
| L12 | Low | Soft leak | Overlapping cleanup errors swallowed in reconciler | Yes (OverlappingRangeReconciler) |
| L13 | Medium | Leak | Leader elector nil on DEL, error swallowed | Yes, eventually |

**"Reconciler Recoverable"** means the `IPPoolReconciler` or `OverlappingRangeReconciler` will eventually clean up the leaked resource once the pod is confirmed gone. This is a safety net with a latency of `reconcileInterval` (configurable), not a fix.

---

## Systemic Observations

1. **The reconciler is load-bearing for correctness.** L1, L4, L6, L7, L11, and L13 all depend on the reconciler as the sole recovery path. If the operator is not deployed, not running, or misconfigured, every one of these leaks is permanent.

2. **The non-atomic two-phase write pattern** (IPPool update → OverlappingRange update) is the root cause of L3 and L10. Reversing the write order (OverlappingRange first as a lock, then IPPool) would eliminate the double-allocation risk at the cost of potential orphaned reservations (lower severity, reconciler-recoverable).

3. **`cmdDel` returning `nil` unconditionally** (L1) is the single highest-leverage fix. Propagating the error would allow the container runtime to retry, converting most "permanent leak" scenarios into "transient delay" scenarios.

4. **ContainerID-based deallocation** (L4) is fragile by design. CNI DEL can be called with a stale containerID after sandbox recreation. Adding a `podRef+ifName` fallback match would make deallocation robust against container restarts.
