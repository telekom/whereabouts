# P2: Storage & Concurrency Review

**Date:** 2026-03-02  
**Reviewer Persona:** Kubernetes Storage & Concurrency Expert

## Summary

| Severity | Count |
|----------|-------|
| CRITICAL | 2 |
| HIGH | 4 |
| MEDIUM | 6 |
| LOW | 3 |
| INFO | 2 |

---

## Findings

### 1. CRITICAL — Non-atomic IPPool + OverlappingRangeIPReservation updates

**File:** [pkg/storage/kubernetes/ipam.go](pkg/storage/kubernetes/ipam.go#L726-L746)

IPPool and OverlappingRangeIPReservation are updated in separate API calls with no transaction. If the IPPool update succeeds but the ORIP update fails, the IP is allocated without overlap protection (allocate path) or the overlap reservation is orphaned (deallocate path). Only the reconciler can clean up orphaned ORIPs.

### 2. CRITICAL — Rollback after failed multi-range allocation uses stale snapshot

**File:** [pkg/storage/kubernetes/ipam.go](pkg/storage/kubernetes/ipam.go#L776-L793)

The rollback path after a partial multi-range allocation attempts to deallocate using the original pool snapshots from before the allocation. These snapshots have stale `resourceVersion`s, so the rollback's JSON Patch `test` operation on the resource version always fails, making rollback effectively impossible. Partial allocations are leaked.

### 3. HIGH — No backoff in 100-iteration retry loop

**File:** [pkg/storage/kubernetes/ipam.go](pkg/storage/kubernetes/ipam.go#L594-L730)

The main allocation retry loop runs up to `DatastoreRetries` (100) times with no exponential backoff, only a fixed sleep. Under contention (many CNI ADD calls simultaneously), this creates a thundering herd on the API server.

### 4. HIGH — Global mutable `var` with no synchronization

**File:** [pkg/storage/storage.go](pkg/storage/storage.go#L14-L20)

`DatastoreRetries` and `RequestTimeout` are global mutable variables read by concurrent goroutines. No `sync.Mutex` or `atomic` protection.

### 5. HIGH — Data race on shared `err`/`newips` between goroutines

**File:** [pkg/storage/kubernetes/ipam.go](pkg/storage/kubernetes/ipam.go#L461-L510)

The leader election code shares `err` and `newips` variables between the goroutine doing allocation and the goroutine handling context/deposed signals. These are written/read without synchronization.

### 6. HIGH — DEL with stale containerID silently no-ops; IP leak

**File:** [pkg/allocate/allocate.go](pkg/allocate/allocate.go#L55-L67)

If a pod is recreated with a new containerID and idempotent ADD runs (updating the stored containerID), a delayed DEL from the old container arrives with the old containerID — the deallocation silently fails and the IP is never released (classic CNI ADD/DEL race).

### 7. MEDIUM — Leader election on `context.Background()` survives parent cancel

**File:** [pkg/storage/kubernetes/ipam.go](pkg/storage/kubernetes/ipam.go#L496-L505)

The leader election runs on `context.Background()`, not derived from parent `ctx`. Parent context cancellation (e.g., CNI timeout) does not immediately stop the leader election.

### 8. MEDIUM — JSON Patch `test` against nil has incorrect RFC 6902 semantics

**File:** [pkg/storage/kubernetes/ipam.go](pkg/storage/kubernetes/ipam.go#L216-L225)

The `test` operation checks that a path equals nil/empty map, but per RFC 6902 §4.6, testing a non-existent path always fails. The logic works in practice but for the wrong reason.

### 9. MEDIUM — OverlappingRange update not retried; orphan on transient failure

**File:** [pkg/storage/kubernetes/ipam.go](pkg/storage/kubernetes/ipam.go#L736-L752)

The OverlappingRange update is outside the retry loop with no retries. A single transient failure orphans the reservation permanently.

### 10. MEDIUM — Create-then-retry wastes an API round-trip

**File:** [pkg/storage/kubernetes/ipam.go](pkg/storage/kubernetes/ipam.go#L138-L163)

After successfully creating a pool, the code returns a `temporaryError` to force a re-GET. The Create response already contains the full object with resourceVersion.

### 11. MEDIUM — `normalizeRange` panics on empty string

**File:** [pkg/storage/kubernetes/ipam.go](pkg/storage/kubernetes/ipam.go#L121-L131)

`ipRange[len(ipRange)-1]` causes an index out of range panic if `ipRange` is empty.

### 12. MEDIUM — `getNodeName` ignores file read error

**File:** [pkg/storage/kubernetes/ipam.go](pkg/storage/kubernetes/ipam.go#L364-L370)

If `/etc/hostname` read fails, execution continues with empty or truncated hostname, causing incorrect pool lookups.

### 13. LOW — `Close()` is a no-op

**File:** [pkg/storage/kubernetes/ipam.go](pkg/storage/kubernetes/ipam.go#L172-L175)

HTTP transport connections held by `KubernetesIPAM` are never released.

### 14. LOW — `stopM` channel buffer=1 is fragile

**File:** [pkg/storage/kubernetes/ipam.go](pkg/storage/kubernetes/ipam.go#L471-L474)

### 15. LOW — `temporaryError` unexported; limits external testability

### 16. INFO — Overlapping range dummy list grows unboundedly per retry

**File:** [pkg/storage/kubernetes/ipam.go](pkg/storage/kubernetes/ipam.go#L688-L694)

### 17. INFO — `overlappingrangestore` unnecessarily recreated each iteration

**File:** [pkg/storage/kubernetes/ipam.go](pkg/storage/kubernetes/ipam.go#L608-L612)
