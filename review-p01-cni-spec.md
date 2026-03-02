# P1: CNI Spec Compliance Review

**Date:** 2026-03-02  
**Reviewer Persona:** CNI Specification Expert

## Summary

| Severity | Count |
|----------|-------|
| CRITICAL | 0 |
| HIGH | 3 |
| MEDIUM | 5 |
| LOW | 6 |
| INFO | 3 |

---

## Findings

### 1. HIGH — `cmdCheck` does not validate `prevResult`

**File:** [cmd/whereabouts.go](cmd/whereabouts.go#L103-L140)  
**Category:** CNI Spec Compliance

CNI spec 0.4.0+ requires CHECK to parse `prevResult` from stdin and verify the IPAM state matches what `prevResult` claims. The current `cmdCheck` only verifies that *some* allocation exists for the `containerID+ifName` pair but never parses `prevResult` or verifies the allocated IP matches the runtime's expected IP.

### 2. HIGH — `mustCIDR` nil pointer dereference before error check (test)

**File:** [cmd/whereabouts_test.go](cmd/whereabouts_test.go#L1574-L1579)

```go
func mustCIDR(s string) net.IPNet {
    ip, n, err := net.ParseCIDR(s)
    n.IP = ip        // panics if ParseCIDR fails (n is nil)
    if err != nil {
        Fail(err.Error())
    }
}
```

### 3. HIGH — CHECK tests don't actually call `cmdCheck`

**File:** [cmd/whereabouts_test.go](cmd/whereabouts_test.go#L1309-L1403)

The CHECK tests inline a reimplementation of the pool-lookup logic instead of calling `cmdCheck` directly. Any bug in the real `cmdCheck` (including the missing `prevResult` handling) is invisible to these tests.

### 4. MEDIUM — `cmdAdd` double-logs and discards `logging.Errorf` return

**File:** [cmd/whereabouts.go](cmd/whereabouts.go#L155-L159)

`logging.Errorf` returns `error` (codebase convention), but `cmdAdd` creates a second `fmt.Errorf` with the same message. Should use `return logging.Errorf(...)`.

### 5. MEDIUM — `cmdCheck` doesn't verify allocated IP value

**File:** [cmd/whereabouts.go](cmd/whereabouts.go#L122-L135)

Even without `prevResult`, CHECK should validate the allocated IP is still within the configured range and not in an exclude list.

### 6. MEDIUM — `cmdDelFunc` swallows all errors including infra failures

**File:** [cmd/whereabouts.go](cmd/whereabouts.go#L80-L85)

CNI DEL should be idempotent for missing resources, but the implementation swallows every error (API server connectivity, RBAC, internal bugs). IP leases are silently leaked on genuine infrastructure failures.

### 7. MEDIUM — No partial allocation check on multi-IP result

**File:** [cmd/whereabouts.go](cmd/whereabouts.go#L175)

If `IPManagement` returns a partial result (e.g., 1 of 2 dual-stack IPs), the partial result is silently returned. No validation that allocated IP count matches `len(ipamConf.IPRanges)`.

### 8. MEDIUM — `cmdAddFunc` inconsistent error return pattern

**File:** [cmd/whereabouts.go](cmd/whereabouts.go#L25-L28)

Logs with `logging.Errorf` but returns the original unwrapped `err`, discarding the contextualized error. Elsewhere the pattern `return logging.Errorf(...)` is used consistently.

### 9. LOW — `cmdDelFunc` swallows config parse errors

**File:** [cmd/whereabouts.go](cmd/whereabouts.go#L51-L56)

If IPAM config is genuinely malformed, DEL silently succeeds and the IP is never deallocated.

### 10. LOW — Missing `Interface` index on `IPConfig` in ADD result

**File:** [cmd/whereabouts.go](cmd/whereabouts.go#L162-L165)

CNI Result spec 1.0.0 expects `IPConfig.Interface` to reference an interface index. IPAM-only plugins commonly omit this but it's technically incomplete.

### 11. LOW — `LoadIPAMConfiguration` discards CNI version

**File:** [pkg/config/config.go](pkg/config/config.go#L213-L232)

The version string from `LoadIPAMConfig` is explicitly discarded.

### 12. LOW — `IPRanges` can be empty after config loading

**File:** [pkg/config/config.go](pkg/config/config.go#L134)

If both `range` and `ipRanges` are omitted, a valid config with empty `IPRanges` is returned. The error only surfaces much later during allocation.

### 13. LOW — `pathExists` returns true on permission errors

**File:** [pkg/config/config.go](pkg/config/config.go#L144-L151)

Non-ENOENT errors (like EACCES) cause `pathExists` to return `true`, leading to confusing errors when `os.Open` subsequently fails.

### 14. LOW — Unnecessary closure wrapping `defer`

**File:** [cmd/whereabouts.go](cmd/whereabouts.go#L35)

`defer func() { safeCloseKubernetesBackendConnection(ipam) }()` — the closure is unnecessary.

### 15. INFO — Test file permissions use 0755 for config files

Should be `0644` — config files don't need execute permission.

### 16. INFO — `ipamConfig` helper typo: "wherebouts.conf" (missing 'a')

**File:** [cmd/whereabouts_test.go](cmd/whereabouts_test.go#L1601)

### 17. INFO — Config test suite name misleading: `TestAllocate` / `"cmd"` in `pkg/config`

**File:** [pkg/config/config_test.go](pkg/config/config_test.go#L16)
