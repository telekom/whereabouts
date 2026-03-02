# CNI Specification Compliance Review — Whereabouts IPAM Plugin

**Date:** March 2, 2026
**Scope:** CNI binary (`cmd/whereabouts.go`, `cmd/whereabouts_test.go`) and the ADD/DEL/CHECK/VERSION flow
**Focus:** CNI spec compliance, error handling, config parsing, result formatting

---

## Known Issues — Updated Status

### Finding P1-1: `cmdCheck` not implemented (MEDIUM → should be HIGH)

**File:** [cmd/whereabouts.go](cmd/whereabouts.go#L72-L76)

```go
func cmdCheck(args *skel.CmdArgs) error {
	// TODO
	return fmt.Errorf("CNI CHECK method is not implemented")
}
```

**Status:** Still present. Returning a hard `fmt.Errorf` (not a structured CNI error) violates [CNI spec §6](https://www.cni.dev/docs/spec/#section-6-result-types) which expects CHECK to either succeed or return a structured error indicating *what* is wrong. Furthermore, the error is a plain `error`, not wrapped in `cnitypes.Error{Code, Msg, Details}`, meaning runtimes cannot programmatically distinguish "CHECK not implemented" from "allocation is invalid."

**Updated assessment:** Severity should be elevated to HIGH because:
1. CRI-O 1.28+ and containerd 2.0+ enable CHECK by default. Every pod on these runtimes generates spurious error logs per CHECK interval.
2. The `skel` framework wraps the returned error in the CNI error JSON envelope, but the error code defaults to `0` (no code), which some runtimes treat as success. Behavior is runtime-dependent and unpredictable.

**Recommendation:** At minimum, implement a no-op CHECK that returns `nil`. Ideally, verify the allocated IP still exists in the IPPool CRD.

---

### Finding P1-2: `cmdDel` swallows ALL errors — violates CNI spec DEL contract (CRITICAL)

**File:** [cmd/whereabouts.go](cmd/whereabouts.go#L110-L116)

```go
func cmdDel(client *kubernetes.KubernetesIPAM) error {
	ctx, cancel := context.WithTimeout(context.Background(), types.DelTimeLimit)
	defer cancel()

	_, _ = kubernetes.IPManagement(ctx, types.Deallocate, client.Config, client)

	return nil
}
```

**Status:** Still present and confirmed CRITICAL.

**Updated detail:** The CNI spec ([§4.2](https://www.cni.dev/docs/spec/#section-4-plugin-delegation)) states: *"Plugins should generally complete a DEL action without error even if some resources are missing."* However, it also says: *"If a DEL fails, the container runtime will generally retry the DEL operation."* The spec distinguishes between "resource already gone" (should succeed) and "can't reach backend to free resource" (should error to trigger retry).

By discarding both return values (`_, _ = ...`), the code:
- Returns `nil` when K8s API is unreachable → runtime never retries → permanent IP leak
- Returns `nil` when context times out → silent failure
- Returns `nil` when leader election fails → silent failure
- Correctly returns `nil` when allocation not found (idempotent) → but this is incidental, not by design

**Recommendation:**
```go
func cmdDel(client *kubernetes.KubernetesIPAM) error {
	ctx, cancel := context.WithTimeout(context.Background(), types.DelTimeLimit)
	defer cancel()

	_, err := kubernetes.IPManagement(ctx, types.Deallocate, client.Config, client)
	if err != nil {
		return fmt.Errorf("error deallocating IP: %s", err)
	}
	return nil
}
```

The `IPManagement` → `DeallocateIP` path already returns `nil, nil` when the allocation is not found, so the idempotency guarantee is preserved.

---

## New Findings

### Finding P1-4: `cmdAddFunc` error handling inconsistency — logs and returns different errors (LOW)

**File:** [cmd/whereabouts.go](cmd/whereabouts.go#L21-L26)

```go
func cmdAddFunc(args *skel.CmdArgs) error {
	ipamConf, confVersion, err := config.LoadIPAMConfig(args.StdinData, args.Args)
	if err != nil {
		logging.Errorf("IPAM configuration load failed: %s", err)
		return err   // ← returns the ORIGINAL error, not the one from logging.Errorf
	}
	// ...
	ipam, err := kubernetes.NewKubernetesIPAM(args.ContainerID, args.IfName, *ipamConf)
	if err != nil {
		return logging.Errorf("failed to create Kubernetes IPAM manager: %v", err)  // ← returns the WRAPPED error
	}
```

**Description:** There are two different error-return patterns within the same function:

1. **Lines 23-25:** `logging.Errorf(...)` is called (which logs AND creates a new error), but its return value is discarded. The *original* `err` from `LoadIPAMConfig` is returned. The log message says `"IPAM configuration load failed: ..."` but the error returned to the CNI runtime says something else entirely (e.g., `"LoadIPAMConfig - JSON Parsing Error: ..."`).

2. **Lines 29-30:** `return logging.Errorf(...)` correctly returns the error created by `logging.Errorf`, which matches what was logged.

The same inconsistency exists in `cmdDelFunc` at [lines 39-42](cmd/whereabouts.go#L39-L42).

**Impact:** The error seen by the container runtime differs from the error logged, making debugging harder. Not a functional bug, but a code quality issue that violates the codebase convention (per CLAUDE.md: *"Use `logging.Errorf` to both log AND return an error in one call"*).

**Recommendation:** Use consistent pattern:
```go
if err != nil {
	return logging.Errorf("IPAM configuration load failed: %s", err)
}
```

---

### Finding P1-5: `cmdAdd` uses `%w` error wrapping — violates codebase convention and leaks internal types (MEDIUM)

**File:** [cmd/whereabouts.go](cmd/whereabouts.go#L90-L92)

```go
	if err != nil {
		logging.Errorf("Error at storage engine: %s", err)
		return fmt.Errorf("error at storage engine: %w", err)
	}
```

**Description:** Two issues:

1. **Convention violation:** The codebase convention (per copilot-instructions.md) is: *"Wrap with `fmt.Errorf("context: %s", err)` — use `%s`, NOT `%w`"*. This is the only `%w` usage in the entire CNI binary.

2. **Internal type leakage:** Using `%w` makes the internal error (e.g., `allocate.AssignmentError`) unwrappable by callers. The container runtime or Multus could call `errors.As()` on the returned error and get a type from Whereabouts' internal packages. This creates an implicit API contract on error types that should be opaque.

3. **Same function also has the double-log pattern** from P1-4: `logging.Errorf(...)` creates and logs one error, then a *second* `fmt.Errorf` is created and returned.

**Recommendation:**
```go
if err != nil {
	return logging.Errorf("error at storage engine: %s", err)
}
```

---

### Finding P1-6: `cmdAdd` can return a successful result with zero IPs (MEDIUM)

**File:** [cmd/whereabouts.go](cmd/whereabouts.go#L77-L108)

```go
func cmdAdd(client *kubernetes.KubernetesIPAM, cniVersion string) error {
	result := &current.Result{}
	result.DNS = client.Config.DNS
	result.Routes = client.Config.Routes

	var newips []net.IPNet

	ctx, cancel := context.WithTimeout(context.Background(), types.AddTimeLimit)
	defer cancel()

	newips, err := kubernetes.IPManagement(ctx, types.Allocate, client.Config, client)
	if err != nil { /* ... */ }

	for _, newip := range newips {
		result.IPs = append(result.IPs, &current.IPConfig{
			Address: newip, Gateway: client.Config.Gateway})
	}

	for _, v := range client.Config.Addresses {
		result.IPs = append(result.IPs, &current.IPConfig{
			Address: v.Address, Gateway: v.Gateway})
	}

	return cnitypes.PrintResult(result, cniVersion)
}
```

**Description:** If `IPManagement` returns an empty `newips` slice (no error, no IPs) AND `client.Config.Addresses` is empty, the function returns a `Result` with `IPs: nil` — a successful response with zero IP addresses. This can happen when:

- `IPRanges` in the config is empty (the `for _, ipRange := range ipamConf.IPRanges` loop in `IPManagementKubernetesUpdate` executes zero iterations, returning `nil, nil`)
- Config parsing produces an empty `IPRanges` list (no `range` and no `ipRanges` fields)

Per the CNI spec, an IPAM plugin ADD is *supposed* to return at least one IP address. Returning zero IPs without an error is semantically wrong — the calling plugin (e.g., Multus) will configure an interface with no IP, which silently breaks network connectivity.

**Recommendation:** After IP allocation, validate that at least one IP was assigned:
```go
if len(result.IPs) == 0 {
	return logging.Errorf("no IP addresses were allocated")
}
return cnitypes.PrintResult(result, cniVersion)
```

---

### Finding P1-7: `cmdDelFunc` inconsistent error philosophy — strict on config, silent on deallocation (HIGH)

**File:** [cmd/whereabouts.go](cmd/whereabouts.go#L38-L54)

```go
func cmdDelFunc(args *skel.CmdArgs) error {
	ipamConf, _, err := config.LoadIPAMConfig(args.StdinData, args.Args)
	if err != nil {
		logging.Errorf("IPAM configuration load failed: %s", err)
		return err   // ← RETURNS errors from config parsing
	}
	// ...
	ipam, err := kubernetes.NewKubernetesIPAM(args.ContainerID, args.IfName, *ipamConf)
	if err != nil {
		return logging.Errorf("IPAM client initialization error: %v", err)  // ← RETURNS errors from init
	}
	// ...
	return cmdDel(ipam)  // ← cmdDel SWALLOWS all errors from actual deallocation
}
```

**Description:** `cmdDelFunc` has an internally contradictory error philosophy:
- Config parsing errors → **returned** (blocks pod deletion until config is fixed)
- K8s client creation errors → **returned** (blocks pod deletion)
- Actual IP deallocation errors → **silently swallowed** (the most consequential operation)

This means if the kubeconfig path changes between pod creation and deletion, DEL returns a hard error that blocks pod garbage collection indefinitely. But if the K8s API is temporarily unreachable (a transient condition the runtime would retry), DEL silently succeeds and the IP leaks.

The error handling is exactly backwards from what the CNI spec recommends for DEL:
- **Config errors on DEL** *should* be lenient (try best-effort cleanup, log warnings) because the pod is being destroyed regardless
- **Deallocation errors** *should* be returned so the runtime can retry

**Recommendation:** Invert the philosophy:
```go
func cmdDelFunc(args *skel.CmdArgs) error {
	ipamConf, _, err := config.LoadIPAMConfig(args.StdinData, args.Args)
	if err != nil {
		// Log but don't block deletion — config may be stale for deleted pods
		_ = logging.Errorf("IPAM configuration load failed (DEL may be incomplete): %s", err)
		return nil
	}
	// ...
	return cmdDel(ipam)  // ← cmdDel should return deallocation errors
}
```

---

### Finding P1-8: `IPManagementKubernetesUpdate` appends zero-value `net.IPNet{}` during deallocation (LOW)

**File:** [pkg/storage/kubernetes/ipam.go](pkg/storage/kubernetes/ipam.go#L550-L737)

```go
func IPManagementKubernetesUpdate(...) ([]net.IPNet, error) {
	var newips []net.IPNet
	var newip net.IPNet       // ← declared outside loop, zero-value: {IP:nil, Mask:nil}
	// ...
	for _, ipRange := range ipamConf.IPRanges {
		// ...
		// In Deallocate mode, `newip` is NEVER assigned
		case whereaboutstypes.Deallocate:
			updatedreservelist, ipforoverlappingrangeupdate = allocate.DeallocateIP(...)
		// ...
		newips = append(newips, newip)   // ← appends zero-value net.IPNet{}
	}
	return newips, nil
}
```

**Description:** During deallocation, the `newip` variable is never assigned (only the `Allocate` case sets it). Yet at the end of each range iteration, `newips = append(newips, newip)` appends a zero-value `net.IPNet{IP: nil, Mask: nil}` to the return slice.

Currently benign because `cmdDel` discards the return: `_, _ = kubernetes.IPManagement(...)`. However:
1. It's a latent bug — if `cmdDel` is fixed to inspect the return value (per P1-2 recommendation), this will cause confusion.
2. The `newip` variable's scope outside the range loop means it retains the value from the *previous* range's allocation in the Allocate path, which could mask bugs in multi-range scenarios.

**Recommendation:** Scope `newip` inside the loop and only append it in the Allocate case:
```go
for _, ipRange := range ipamConf.IPRanges {
	var newip net.IPNet
	// ...
	if mode == whereaboutstypes.Allocate {
		newips = append(newips, newip)
	}
}
```

---

### Finding P1-9: Config file not found is a fatal error even with complete inline IPAM config (MEDIUM)

**File:** [pkg/config/config.go](pkg/config/config.go#L56-L58) and [cmd/whereabouts.go](cmd/whereabouts.go#L22)

```go
// config.go line 56-58
flatipam, foundflatfile, err := GetFlatIPAM(false, n.IPAM, extraConfigPaths...)
if err != nil {
	return nil, "", err    // ← fatal return on ConfigFileNotFoundError
}
```

```go
// whereabouts.go line 22 — production entry point
ipamConf, confVersion, err := config.LoadIPAMConfig(args.StdinData, args.Args)
// NOTE: no extraConfigPaths passed!
```

**Description:** `LoadIPAMConfig` calls `GetFlatIPAM` which searches for a flat config file at three default paths:
- `/etc/kubernetes/cni/net.d/whereabouts.d/whereabouts.conf`
- `/etc/cni/net.d/whereabouts.d/whereabouts.conf`
- `/host/etc/cni/net.d/whereabouts.d/whereabouts.conf`

If none exist, `GetFlatIPAM` returns `ConfigFileNotFoundError`. `LoadIPAMConfig` treats this as fatal and returns the error.

The production entry point (`cmdAddFunc`/`cmdDelFunc`) calls `LoadIPAMConfig` without `extraConfigPaths`. The standard installer script (`script/install-cni.sh`) creates this flat file, so standard deployments work. However:

1. Non-standard deployments (custom daemonset, Helm with different paths, direct binary usage) that rely solely on inline IPAM config in the NetworkAttachmentDefinition fail with a confusing `"config file not found"` error.
2. The function comment says: *"let's look for our (optional) configuration file"* — but the code treats it as mandatory.
3. All tests pass because they provide `extraConfigPaths` explicitly, masking this production behavior.

**Recommendation:** Treat `ConfigFileNotFoundError` as non-fatal in `LoadIPAMConfig`:
```go
flatipam, foundflatfile, err := GetFlatIPAM(false, n.IPAM, extraConfigPaths...)
if err != nil {
	var notFound *ConfigFileNotFoundError
	if !errors.As(err, &notFound) {
		return nil, "", err  // only fatal for real errors
	}
	// config file not found is fine — inline config is sufficient
}
```

---

### Finding P1-10: `cmdCheck` error is not a proper CNI error type (LOW)

**File:** [cmd/whereabouts.go](cmd/whereabouts.go#L72-L76)

```go
func cmdCheck(args *skel.CmdArgs) error {
	// TODO
	return fmt.Errorf("CNI CHECK method is not implemented")
}
```

**Description:** The CNI library (`skel.PluginMainFuncs`) converts returned errors to the [CNI error result format](https://www.cni.dev/docs/spec/#error):
```json
{
  "cniVersion": "1.0.0",
  "code": 0,
  "msg": "CNI CHECK method is not implemented"
}
```

The error code `0` is used because `fmt.Errorf` produces a plain error, not a `*cnitypes.Error` with a specific code. CNI spec error code `0` is undefined. Some runtimes may interpret this as success (CRI-O), while others treat any non-nil error as failure (containerd).

**Recommendation:** If CHECK remains unimplemented, use a proper CNI error code:
```go
func cmdCheck(args *skel.CmdArgs) error {
	return &cnitypes.Error{
		Code: 100,
		Msg:  "CNI CHECK method is not implemented",
	}
}
```
Or better, return `nil` (no-op CHECK) to avoid spurious errors.

---

### Finding P1-11: Test suite uses deprecated Ginkgo API (INFO)

**File:** [cmd/whereabouts_test.go](cmd/whereabouts_test.go#L40-L43)

```go
func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecsWithDefaultAndCustomReporters(t,
		"Whereabouts Suite",
		[]Reporter{})
}
```

**Description:** `RunSpecsWithDefaultAndCustomReporters` is deprecated in Ginkgo v2. The vendored code at `vendor/github.com/onsi/ginkgo/v2/deprecated_dsl.go` shows: *"Custom Reporters have been removed in Ginkgo 2.0. RunSpecsWithDefaultAndCustomReporters will simply call RunSpecs()"*.

Not a CNI spec issue, but the empty `[]Reporter{}` argument is dead code and future Ginkgo versions may remove this function entirely.

**Recommendation:** Replace with `RunSpecs(t, "Whereabouts Suite")`.

---

### Finding P1-12: No `prevResult` handling in any CNI operation (INFO)

**File:** [cmd/whereabouts.go](cmd/whereabouts.go) (entire file)

**Description:** CNI spec 0.4.0+ passes a `prevResult` field in the config for chained plugins. For CHECK and DEL operations, the runtime includes the result from the prior ADD. Whereabouts does not parse or use `prevResult` anywhere.

For a pure IPAM plugin, this is technically acceptable — `prevResult` is primarily relevant for interface-managing plugins. However:

1. **CHECK** should ideally validate that the IPs in `prevResult` match the current state in the IPPool CRD. Without `prevResult`, CHECK cannot verify anything meaningful.
2. **DEL** could use `prevResult` to know which IPs to deallocate if the container ID or network state has changed, rather than relying solely on `containerID + ifName`.

**Impact:** Informational. No functional issue with current behavior, but implementing CHECK correctly will require parsing `prevResult`.

---

## Summary Table

| Finding | Title | Severity | Status |
|---------|-------|----------|--------|
| P1-1 | `cmdCheck` not implemented | **HIGH** (upgraded from MEDIUM) | Known, verified — worse than described due to CRI-O/containerd 2.0 defaults |
| P1-2 | `cmdDel` swallows all errors | **CRITICAL** | Known, verified — confirmed as primary IP leak source |
| P1-4 | `cmdAddFunc` logs and returns different errors | LOW | **NEW** |
| P1-5 | `cmdAdd` uses `%w` wrapping (convention violation + type leakage) | MEDIUM | **NEW** |
| P1-6 | `cmdAdd` can return success with zero IPs | MEDIUM | **NEW** |
| P1-7 | `cmdDelFunc` error philosophy is inverted (strict on config, silent on deallocation) | HIGH | **NEW** — extends P1-2 with config-error dimension |
| P1-8 | Zero-value `net.IPNet{}` appended during deallocation | LOW | **NEW** |
| P1-9 | Config file not found is fatal even with complete inline config | MEDIUM | **NEW** |
| P1-10 | `cmdCheck` error uses code 0 (undefined in CNI spec) | LOW | **NEW** — extends P1-1 |
| P1-11 | Test suite uses deprecated Ginkgo API | INFO | **NEW** |
| P1-12 | No `prevResult` handling in any CNI operation | INFO | **NEW** |

### Priority Order for Fixes

1. **P1-2 + P1-7** (CRITICAL/HIGH): Fix `cmdDel` to return deallocation errors; make `cmdDelFunc` lenient on config errors
2. **P1-1 + P1-10** (HIGH): Implement at least a no-op CHECK (return `nil`)
3. **P1-6** (MEDIUM): Validate that at least one IP was allocated before printing result
4. **P1-9** (MEDIUM): Make flat config file optional when inline config is complete
5. **P1-5** (MEDIUM): Fix `%w` to `%s` and use `logging.Errorf` return pattern
6. **P1-4, P1-8** (LOW): Consistency fixes
