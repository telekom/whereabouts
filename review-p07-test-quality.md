# P7: Test Quality Review

**Date:** 2026-03-02  
**Reviewer Persona:** Test Quality Expert

## Summary

| Severity | Count |
|----------|-------|
| CRITICAL | 0 |
| HIGH | 1 |
| MEDIUM | 5 |
| LOW | 5 |
| INFO | 2 |

---

## Findings

### 1. HIGH — CHECK tests don't exercise `cmdCheck`

**File:** [cmd/whereabouts_test.go](cmd/whereabouts_test.go#L1309-L1403)

Tests inline a reimplemented pool-lookup instead of calling `cmdCheck`. Real`cmdCheck` bugs (including missing `prevResult` handling) are invisible.

### 2. MEDIUM — Tests write to global `/tmp/whereabouts.conf`

**Files:** [cmd/whereabouts_test.go](cmd/whereabouts_test.go), [pkg/config/config_test.go](pkg/config/config_test.go#L103)

Multiple tests write to the same global path without cleanup. Breaks parallel test execution and leaves artifacts.

### 3. MEDIUM — `ipamConfig` leaks temp directories on failure

**File:** [cmd/whereabouts_test.go](cmd/whereabouts_test.go#L1589-L1618)

If any intermediate step fails, `Fail()` panics and `os.RemoveAll(tmpDir)` is never called. Should use `defer`.

### 4. MEDIUM — No `isPodUsingIP` tests with network-status annotation

**File:** [internal/controller/ippool_controller_test.go](internal/controller/ippool_controller_test.go)

Missing: pod exists but IP not in network-status annotation, pod with correct IP, pod with malformed annotation, multiple allocations (mix of valid and orphaned).

### 5. MEDIUM — No `denormalizeIPName` unit tests

**File:** [internal/controller/ippool_controller_test.go](internal/controller/ippool_controller_test.go)

Complex multi-path parsing logic (plain IP, dash→colon, iterative prefix stripping) with no dedicated tests. Needs table-driven tests.

### 6. MEDIUM — Test coverage gaps in allocate.go

Missing: `AssignIP` with invalid CIDR, `DeallocateIP` with nil/empty list, IPv6 single-IP exclude, `parseExcludedRange` standalone, `IterateForAssignment` with rangeStart==rangeEnd, full exhaustion without excludes.

### 7. LOW — No test for empty `IPRanges` config

No test verifying behavior when both `range` and `ipRanges` are omitted.

### 8. LOW — No test for `RangeEnd < RangeStart`

No test verifying behavior of inverted range.

### 9. LOW — Webhook tests don't use `envtest`

No integration tests verifying webhook registration paths, admission serialization, or matchConditions CEL.

### 10. LOW — No test for `cleanupOverlappingReservations` with matching ORIPs

### 11. LOW — `mustCIDR` nil deref before error check

**File:** [cmd/whereabouts_test.go](cmd/whereabouts_test.go#L1574-L1579)

`n.IP = ip` executes before `if err != nil`, panicking on invalid input instead of producing test failure.

### 12. INFO — Test suite names misleading (several packages use `"cmd"`)

### 13. INFO — Tests use 0755 permissions for config files (should be 0644)
