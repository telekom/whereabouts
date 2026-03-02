# P6: Code Quality Review

**Date:** 2026-03-02  
**Reviewer Persona:** Go Code Quality Expert

## Summary

| Severity | Count |
|----------|-------|
| CRITICAL | 0 |
| HIGH | 0 |
| MEDIUM | 5 |
| LOW | 5 |
| INFO | 2 |

---

## Findings

### 1. MEDIUM — `mergo.Merge` error logged but not returned

**File:** [pkg/config/config.go](pkg/config/config.go#L71-L73)

If `mergo.Merge` fails, configuration `n` may be partially merged. Execution continues with potentially corrupt config.

### 2. MEDIUM — `OverlappingRanges` save/restore is fragile

**File:** [pkg/config/config.go](pkg/config/config.go#L69-L74)

Manual save/restore of `OverlappingRanges` before merge because Go booleans can't distinguish "false" from "unset." If inline config omits the field, the zero value silently overrides a flat-file `true`.

### 3. MEDIUM — No `RangeEnd >= RangeStart` validation

**File:** [pkg/config/config.go](pkg/config/config.go#L88-L107)

A config with `range_start > range_end` passes validation and produces undefined allocation behavior.

### 4. MEDIUM — `defer Close()` inside loop

**File:** [pkg/config/config.go](pkg/config/config.go#L203-L208)

File handle not closed until function returns. Currently safe (returns after first match), but fragile if iteration logic changes.

### 5. MEDIUM — `storageError()` confusing error message

**File:** [pkg/config/config.go](pkg/config/config.go#L267-L269)

References "invalid `kubernetes.kubeconfig`" — could be read as dotted-notation field when actual structure is nested JSON.

### 6. LOW — `ConfigFileNotFoundError` pointer sentinel instead of value

**File:** [pkg/config/config.go](pkg/config/config.go#L258-L264)

Struct with no fields using `errors.As` (pointer) when a sentinel `var` with `errors.Is` would be simpler.

### 7. LOW — Dead `Datastore` and etcd fields in `IPAMConfig`

**File:** [pkg/types/types.go](pkg/types/types.go#L83-L98)

`Datastore`, `EtcdHost`, `EtcdUsername`, etc. are vestigial. No etcd implementation exists.

### 8. LOW — `parsePodRef` duplicated between controller and validation

**Files:** [internal/controller/ippool_controller.go](internal/controller/ippool_controller.go#L203-L209), [internal/validation/validation.go](internal/validation/validation.go#L31-L42)

Both split on "/" with similar logic. Could share a utility.

### 9. LOW — `denormalizeIPName` IPv4 ambiguity with network-name prefix

**File:** [internal/controller/ippool_controller.go](internal/controller/ippool_controller.go#L175-L200)

`"mynet-192-168-1-1"` tries `"192-168-1-1"` → `"192:168:1:1"` which IS a valid IPv6 address. Returns wrong IP type. Edge case but could cause cleanup misses.

### 10. LOW — `newK8sIPAM` / `mutateK8sIPAM` return nil on error silently (test)

**File:** [cmd/whereabouts_test.go](cmd/whereabouts_test.go#L1562-L1573)

If IPAM creation fails, nil is returned with no logging or `Fail()`. Tests get confusing nil pointer panics.

### 11. INFO — `certrotator.Enable` direct client doesn't use manager scheme

**File:** [internal/webhook/certrotator/certrotator.go](internal/webhook/certrotator/certrotator.go#L83-L84)

### 12. INFO — Duplicate `getCounterValue` test helper in controller and webhook test packages
