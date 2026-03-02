# Storage & Concurrency Layer Review — Whereabouts IPAM

**Reviewer:** Kubernetes Storage & Concurrency Engineer  
**Scope:** `pkg/storage/kubernetes/ipam.go`, `pkg/storage/storage.go`, CRD update patterns  
**Date:** 2026-03-02

---

## Verified Known Issues (Still Present)

### P2-5: JSON Patch `test` operation uses uninitialized map — CONFIRMED STILL PRESENT

**File:** `pkg/storage/kubernetes/ipam.go:220-224`

```go
if o.Operation == "add" {
    var m map[string]interface{}  // nil → serializes as JSON null
    ops = append(ops, jsonpatch.Operation{Operation: "test", Path: o.Path, Value: m})
}
```

Still present at lines 220-224. The `test` precondition checks the target path against `null`. Per RFC 6902, if the path does not exist, a `test` operation should fail. This works only because the Kubernetes API server's json-patch implementation treats a missing key as equivalent to `null`. Semantically fragile — any server-side json-patch library change would break all new allocations.

### P2-6: Mutable global variables for retry config — CONFIRMED STILL PRESENT

**File:** `pkg/storage/storage.go:14-18`

```go
var (
    RequestTimeout    = 10 * time.Second
    DatastoreRetries  = 100
    PodRefreshRetries = 3
)
```

Still mutable `var` rather than `const` or injected config. Tests mutating these values can cause cross-test interference.

---

## New Findings

### Finding P2-7: Potential deadlock in `IPManagement` when `deposed` fires

**Severity:** HIGH  
**File:** `pkg/storage/kubernetes/ipam.go:477-519`

```go
go func() {  // goroutine 1
    defer wg.Done()
    for {
        select {
        case <-ctx.Done():
            err = fmt.Errorf("time limit exceeded while waiting to become leader")
            stopM <- struct{}{}   // ← signals goroutine 2
            return
        case <-leader:
            newips, err = IPManagementKubernetesUpdate(ctx, mode, client, ipamConf)
            stopM <- struct{}{}   // ← signals goroutine 2
            return
        case <-deposed:
            result <- nil         // ← does NOT signal stopM
            return
        }
    }
}()

go func() {  // goroutine 2
    defer wg.Done()
    // ...
    <-stopM       // ← blocks forever if deposed case fired
    leCancel()
    result <- (<-res)
}()
wg.Wait()   // ← blocks forever because goroutine 2 never finishes
```

**Description:** Goroutine 1 has three `select` cases. Two of them (`ctx.Done()`, `leader`) send to `stopM`, which unblocks goroutine 2. But the `deposed` case sends to `result` instead of `stopM`. If the `deposed` case fires, goroutine 2 blocks on `<-stopM` forever, and `wg.Wait()` never returns — **full deadlock**.

This can happen when `OnStartedLeading` and `OnStoppedLeading` fire in rapid succession. In client-go's `LeaderElector.Run()`:
```go
go le.config.Callbacks.OnStartedLeading(ctx)  // closes `leader` channel
le.renew(ctx)                                  // if renew fails immediately...
// ...defer calls OnStoppedLeading()            // closes `deposed` channel
```
If `renew` returns quickly (e.g., immediate lease loss), both `leader` and `deposed` channels are ready. Go's `select` is pseudorandom — if it picks `deposed`, the deadlock occurs.

**Recommendation:** The `deposed` case should also send to `stopM` (or close it), and set an appropriate error. Alternatively, restructure so `stopM` is the single coordination point:
```go
case <-deposed:
    logging.Debugf("Deposed as leader, shutting down")
    err = fmt.Errorf("deposed as leader before completing operation")
    stopM <- struct{}{}
    return
```

---

### Finding P2-8: `result` channel is never drained — leader election errors silently discarded

**Severity:** MEDIUM  
**File:** `pkg/storage/kubernetes/ipam.go:475, 492, 514, 519-520`

```go
result := make(chan error, 2)
// ... goroutine 1 sends: result <- nil (deposed case)
// ... goroutine 2 sends: result <- (<-res)

wg.Wait()
close(stopM)
logging.Debugf("IPManagement: %v, %v", newips, err)
return newips, err   // ← 'result' channel is never read
```

**Description:** The `result` channel receives error values from both goroutines but is never consumed. Any error from leader election teardown (goroutine 2 via `le.Run` sub-goroutine) is silently lost. If leader election encounters a real error during shutdown, the caller will never know.

**Recommendation:** Drain the `result` channel after `wg.Wait()` and aggregate any non-nil errors, or remove the channel entirely and use the shared `err` variable (which is already synchronized via `wg.Wait()`).

---

### Finding P2-9: Non-atomic IPPool + OverlappingRangeIPReservation update — data integrity gap

**Severity:** HIGH  
**File:** `pkg/storage/kubernetes/ipam.go:705-732`

```go
// Inside RETRYLOOP:
err = pool.Update(requestCtx, usereservelist)   // Step 1: IPPool updated
if err != nil { ... }
requestCancel()
break RETRYLOOP

// OUTSIDE RETRYLOOP — no retry protection:
if ipamConf.OverlappingRanges {
    if !skipOverlappingRangeUpdate {
        err = overlappingrangestore.UpdateOverlappingRangeAllocation(...)  // Step 2
        if err != nil {
            return newips, err   // ← IPPool update already committed!
        }
    }
}
```

**Description:** The IPPool CRD update (step 1) and the OverlappingRangeIPReservation creation (step 2) are two separate, non-transactional operations. If step 1 succeeds but step 2 fails (e.g., `AlreadyExists` because another pod concurrently claimed the same IP in a different overlapping range, or a transient network error), the system is left in an inconsistent state:
- The IPPool contains a reservation for the IP
- The OverlappingRangeIPReservation either doesn't exist or belongs to a different pod

This is particularly dangerous because step 2 is **outside** the retry loop and has **no rollback** of step 1. The orphaned IPPool reservation will persist until the reconciler cleans it up (which checks podRef against live pods — the pod IS live, so it won't be cleaned).

**Recommendation:** Either:
1. Move the overlapping range update inside the retry loop (before `pool.Update`), so that a failure can be retried atomically, or
2. On step 2 failure, perform a compensating `pool.Update` to remove the just-added reservation, or
3. Create the OverlappingRangeIPReservation first (step 2 before step 1). If it fails, no IPPool was modified. If step 1 then fails, clean up the reservation.

---

### Finding P2-10: `Update()` only treats `IsInvalid` errors as retryable — transient failures are fatal

**Severity:** MEDIUM  
**File:** `pkg/storage/kubernetes/ipam.go:232-239`

```go
_, err = p.client.WhereaboutsV1alpha1().IPPools(orig.GetNamespace()).Patch(
    ctx, orig.GetName(), types.JSONPatchType, patchData, metav1.PatchOptions{})
if err != nil {
    if errors.IsInvalid(err) {
        // expect "invalid" errors if any of the jsonpatch "test" Operations fail
        return &temporaryError{err}
    }
    return err   // ← network timeouts, 500s, etc. are fatal
}
```

**Description:** Only `422 Unprocessable Entity` (`IsInvalid`) is wrapped as a `temporaryError` for retry. Transient errors such as:
- Network timeouts / connection resets
- `500 Internal Server Error` (API server restart)
- `503 Service Unavailable` (API server overloaded)
- `429 Too Many Requests` (rate limiting)

…are returned as non-temporary errors, causing the retry loop to `break RETRYLOOP` immediately (line 710-713). In a large cluster with API server load, this makes allocations fragile.

**Recommendation:** Also treat `IsServerTimeout`, `IsServiceUnavailable`, `IsTooManyRequests`, `IsInternalError`, and connection-level errors as temporary:
```go
if errors.IsInvalid(err) || errors.IsServerTimeout(err) ||
   errors.IsServiceUnavailable(err) || errors.IsTooManyRequests(err) ||
   errors.IsInternalError(err) {
    return &temporaryError{err}
}
```

---

### Finding P2-11: `normalizeRange` panics on empty string input

**Severity:** LOW  
**File:** `pkg/storage/kubernetes/ipam.go:128`

```go
func normalizeRange(ipRange string) string {
    if ipRange[len(ipRange)-1] == ':' {   // ← panics if ipRange is ""
```

**Description:** If `ipRange` is an empty string, `len(ipRange)-1` is `-1`, causing an index-out-of-bounds panic. While `ipRange` is typically validated upstream, a defensive check is warranted since this is a string utility function called from `IPPoolName` which is also used externally (e.g., leader election lease name construction).

**Recommendation:** Add a guard:
```go
func normalizeRange(ipRange string) string {
    if ipRange == "" {
        return ""
    }
    // ...
```

---

### Finding P2-12: `getPool` shares a single timeout context between Get and Create operations

**Severity:** LOW  
**File:** `pkg/storage/kubernetes/ipam.go:138-148`

```go
func (i *KubernetesIPAM) getPool(ctx context.Context, name string, iprange string) (*whereaboutsv1alpha1.IPPool, error) {
    ctxWithTimeout, cancel := context.WithTimeout(ctx, storage.RequestTimeout)
    defer cancel()

    pool, err := i.client.WhereaboutsV1alpha1().IPPools(i.Namespace).Get(ctxWithTimeout, ...)
    if err != nil && errors.IsNotFound(err) {
        // ...
        _, err = i.client.WhereaboutsV1alpha1().IPPools(i.Namespace).Create(ctxWithTimeout, ...)
```

**Description:** A single `context.WithTimeout(ctx, 10s)` is created and shared between the `Get` call and the subsequent `Create` call. If the `Get` takes significant time before returning `NotFound` (e.g., slow API server), the remaining timeout for `Create` is reduced. In pathological cases (Get takes ~10s to timeout with NotFound), the Create will immediately fail with `context deadline exceeded`, producing a confusing error.

**Recommendation:** Create a fresh timeout context for the `Create` operation, or use the already-existing `requestCtx` from the caller's retry loop (passed as `ctx` here).

---

### Finding P2-13: `newip` variable scoped outside `ipRange` loop — stale value appended for Deallocate

**Severity:** LOW  
**File:** `pkg/storage/kubernetes/ipam.go:551, 687-691, 736`

```go
var newip net.IPNet           // ← declared once, outside the for loop (line 551)

// ... inside the loop, Deallocate case:
case whereaboutstypes.Deallocate:
    updatedreservelist, ipforoverlappingrangeupdate = allocate.DeallocateIP(...)
    // newip is NEVER assigned in this branch

// ... after RETRYLOOP:
newips = append(newips, newip)  // ← appends zero-value net.IPNet{} for Deallocate
```

**Description:** `newip` is declared at function scope (line 551) but only assigned in the `Allocate` branch (line 649). For `Deallocate`, `newip` retains its zero value (`net.IPNet{}`), and a zero-value `net.IPNet` is appended to `newips` at line 736 for each `ipRange`. Currently harmless because `cmdDel` discards the return value (`_, _ = kubernetes.IPManagement(...)`), but this is misleading and would break if a future caller inspects the returned IPs.

**Recommendation:** Move `var newip net.IPNet` inside the `for _, ipRange := range` loop, and only append to `newips` when `mode == Allocate`.

---

### Finding P2-14: `getNodeName` continues on read error — may return truncated/empty hostname

**Severity:** LOW  
**File:** `pkg/storage/kubernetes/ipam.go:378-381`

```go
n, err := file.Read(data)
if err != nil {
    logging.Errorf("Error reading file /etc/hostname: %v", err)
    // ← execution continues, returns potentially truncated hostname
}
hostname := string(data[:n])
```

**Description:** If `file.Read` returns a non-nil error (partial read, I/O error), the error is logged but execution continues. The function returns whatever partial data was read, potentially an empty string. This hostname is used for leader election lease names (`IPPoolName`) and node-slice lookups — an incorrect hostname could cause the CNI plugin to acquire the wrong lease or look up the wrong node slice.

**Recommendation:** Return the error when read fails:
```go
if err != nil {
    return "", fmt.Errorf("error reading hostname file: %v", err)
}
```

---

### Finding P2-15: `overlappingrangeallocations` accumulates across `ipRange` iterations

**Severity:** LOW  
**File:** `pkg/storage/kubernetes/ipam.go:572, 647, 671`

```go
var overlappingrangeallocations []whereaboutstypes.IPReservation  // ← outside for loop (line 572)

for _, ipRange := range ipamConf.IPRanges {  // ← iterates multiple ranges
    // ...
    RETRYLOOP:
        // ...
        reservelist = append(reservelist, overlappingrangeallocations...)  // line 647
        // ...
        overlappingrangeallocations = append(overlappingrangeallocations, ...)  // line 671
```

**Description:** `overlappingrangeallocations` accumulates "dummy" reservations (IPs that were found to be cluster-wide allocated) across both retries and across different `ipRange` iterations. The cross-retry accumulation is correct (avoids re-trying the same overlapping IP). The cross-`ipRange` accumulation is incorrect — dummy IPs from range A's CIDR are injected into range B's reserve list. While IPs from one CIDR typically won't fall within another CIDR, in edge cases (overlapping ranges, dual-stack ranges sharing a prefix) this could cause incorrect skip behavior.

**Recommendation:** Reset `overlappingrangeallocations` at the start of each `ipRange` iteration:
```go
for _, ipRange := range ipamConf.IPRanges {
    overlappingrangeallocations = nil  // reset per range
```

---

### Finding P2-16: Leader election uses `context.Background()` — survives parent context cancellation

**Severity:** LOW  
**File:** `pkg/storage/kubernetes/ipam.go:501`

```go
leCtx, leCancel := context.WithCancel(context.Background())

go func() {
    le.Run(leCtx)
    // ...
}()
```

**Description:** The leader election goroutine's context is derived from `context.Background()`, not from the parent `ctx`. This means leader election continues running (making API server calls for lease renewal) even after the parent context is canceled. It only stops when `leCancel()` is called, which happens after `stopM` is received. If the `deposed` deadlock (P2-7) occurs, `leCancel()` is never called and the leader election goroutine leaks — holding and renewing a lease indefinitely.

Even without the deadlock, there's a window between `ctx.Done()` firing and `leCancel()` completing where the leader election continues making unnecessary API calls.

**Recommendation:** Derive `leCtx` from `ctx` so it inherits cancellation:
```go
leCtx, leCancel := context.WithCancel(ctx)
```

---

### Finding P2-17: `cmdDel` silently swallows all errors from IP deallocation

**Severity:** MEDIUM  
**File:** `cmd/whereabouts.go:110-116`

```go
func cmdDel(client *kubernetes.KubernetesIPAM) error {
    ctx, cancel := context.WithTimeout(context.Background(), types.DelTimeLimit)
    defer cancel()

    _, _ = kubernetes.IPManagement(ctx, types.Deallocate, client.Config, client)

    return nil
}
```

**Description:** Both return values from `IPManagement` are discarded. If deallocation fails (e.g., API server unreachable, context timeout, leader election failure), the error is silently swallowed and `cmdDel` returns `nil`, making the CNI runtime believe the DEL succeeded. This can cause IP address leaks — the IP remains allocated in the IPPool CRD but the container is gone, and the runtime won't retry the DEL because it thinks it succeeded.

While the reconciler (`IPPoolReconciler`) eventually cleans up orphaned allocations, relying on eventual consistency for a synchronous operation is not ideal, especially if the reconciler is not running or has its own issues.

**Recommendation:** At minimum, log the error. Ideally, return the error to the CNI runtime so it can retry:
```go
_, err := kubernetes.IPManagement(ctx, types.Deallocate, client.Config, client)
if err != nil {
    logging.Errorf("WARNING: IP deallocation failed: %v", err)
    // CNI spec recommends DEL be idempotent and best-effort,
    // but the runtime should know about failures
}
return nil  // or return err based on policy
```

---

## Summary Table

| ID | Title | Severity | Category |
|----|-------|----------|----------|
| P2-5 | JSON Patch test with nil map (verified) | LOW | Fragility |
| P2-6 | Mutable global retry vars (verified) | LOW | Design |
| P2-7 | Deadlock when `deposed` case fires in `IPManagement` | **HIGH** | Concurrency |
| P2-8 | `result` channel never drained | MEDIUM | Error Handling |
| P2-9 | Non-atomic IPPool + OverlappingRange update | **HIGH** | Data Integrity |
| P2-10 | `Update()` only retries `IsInvalid`, not transient errors | MEDIUM | Resilience |
| P2-11 | `normalizeRange` panics on empty string | LOW | Robustness |
| P2-12 | Shared timeout context between Get and Create in `getPool` | LOW | Context Propagation |
| P2-13 | `newip` not scoped per `ipRange` — stale value for Deallocate | LOW | Correctness |
| P2-14 | `getNodeName` continues on read error | LOW | Error Handling |
| P2-15 | `overlappingrangeallocations` not reset between ipRanges | LOW | Correctness |
| P2-16 | Leader election context detached from parent | LOW | Resource Leak |
| P2-17 | `cmdDel` silently swallows deallocation errors | MEDIUM | Error Handling |
