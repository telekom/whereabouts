# Whereabouts — Comprehensive Code Review

**Date:** 2026-03-02 (Rev 2 — post controller-runtime migration)  
**Reviewers:** 13 specialist personas via automated deep review  
**Scope:** Full repository — CNI binary, storage layer, IP math, operator, webhooks, deployment manifests, CI/CD, documentation  
**Branch:** `feat/controller-runtime-migration` (15 commits ahead of `main`)

> **Rev 2 changes:** The controller-runtime migration replaced hand-rolled informers/workqueues with controller-runtime v0.23.2 reconcilers and typed validating webhooks. Old code in `cmd/controlloop/`, `cmd/nodeslicecontroller/`, `pkg/controlloop/`, `pkg/node-controller/`, `pkg/reconciler/` was removed. This review covers the **current state** of all code.

---

## Executive Summary

| Severity | Open | Fixed (this PR) | N/A (old code removed) |
|----------|------|-----------------|------------------------|
| CRITICAL | 3 | 0 | 0 |
| HIGH | 16 | 4 | 2 |
| MEDIUM | 36 | 2 | 1 |
| LOW | 30 | 0 | 0 |
| INFO | 3 | 0 | 0 |
| **Total** | **88** | **6** | **3** |

**Top 5 issues requiring immediate attention:**
1. **P5-7 (CRITICAL):** Webhook VWC name mismatch — cert-controller will fail to inject CA bundle
2. **L6 (CRITICAL):** Multi-range dealloc aborts on first miss — remaining IPs leak
3. **L1 (CRITICAL):** `cmdDel` swallows ALL errors — primary IP leak source
4. **P3-14 (HIGH):** `byteSliceSub` borrow bug → duplicate IP allocation for non-zero-aligned ranges
5. **P8-15/P8-16 (HIGH):** Helm chart references deleted binaries (`/ip-control-loop`, `/node-slice-controller`)

---

## Items Fixed by Controller-Runtime Migration

| ID | Title | Status |
|----|-------|--------|
| P2-1 | `requestCtx` 10s timeout defeats retry loop | **FIXED** — timeout now per-retry |
| P2-2 | Nil leader elector panic | **FIXED** — nil guard before goroutine launch |
| P2-3 | Shared `err` variable across ipRange loops | **FIXED** — `err` scoped per iteration |
| P2-4 | `skipOverlappingRangeUpdate` not reset per ipRange | **FIXED** — reset at loop top |
| P5-2 | No health/readiness probes | **FIXED** — operator has healthz/readyz at `:8081`/`:8083` |
| P5-3 | No metrics endpoints | **FIXED** — operator exposes metrics at `:8080`/`:8082` |
| P5-6 | Reconciler runs without context/timeout | **FIXED** — controller-runtime provides context |
| P9-4 | No leader election in node-slice-controller | **FIXED** — operator controller subcommand uses leader election |
| P11-3 | IPv6 normalization mismatch in reconciler | **FIXED** — `net.IP.Equal()` used in new IPPoolReconciler |
| P11-4 | Reconciler snapshot goes stale | **FIXED** — watch-based triggers via controller-runtime |
| P11-5 | `close(errorChan)` race | **N/A** — old code removed |
| P10-2 | `checkForMultiNadMismatch` lists ALL NADs | **N/A** — old code removed |
| P10-3 | Reconciler loads ALL pods | **PARTIALLY FIXED** — filtered watches, but still `Get` per pod in orphan cleanup |

---

## Persona 1 — CNI Specification Expert

### P1-1: `cmdCheck` not implemented [HIGH] *(upgraded from MEDIUM)*
**File:** `cmd/whereabouts.go:72-76`  
CNI CHECK is a no-op returning `fmt.Errorf(...)`. CRI-O defaults to calling CHECK periodically; containerd 2.0 enables it by default. An unimplemented CHECK generates constant error noise in the kubelet logs and may cause container scheduling delays on runtimes that treat CHECK failure as degraded.  
**Recommendation:** Return `nil` (no-op) or implement basic validation against IPPool CRD state.

### P1-2: `cmdDel` swallows all errors [CRITICAL]
**File:** `cmd/whereabouts.go:110-116`  
```go
_, _ = kubernetes.IPManagement(ctx, types.Deallocate, client.Config, client)
return nil
```
Both return values from `IPManagement` are unconditionally discarded. If deallocation fails, the CNI runtime considers DEL successful and won't retry. The IP remains allocated in the CRD but the container is gone.  
**Recommendation:** At minimum return the error so the runtime can retry. The reconciler is a safety net, not a primary mechanism.

### P1-4: `cmdAddFunc` logs and returns different errors [LOW]
**File:** `cmd/whereabouts.go:22-43`  
The function logs `err` from `config.LoadIPAMConfig` but then returns a completely different error wrapping a different message.

### P1-5: `cmdAdd` uses `%w` wrapping (convention violation) [MEDIUM]
**File:** `cmd/whereabouts.go:30`  
Uses `fmt.Errorf("error loading config: %w", err)` — project convention is `%s` not `%w`. Also leaks internal error types to the CNI runtime.

### P1-6: `cmdAdd` can return success with zero IPs [MEDIUM]
**File:** `cmd/whereabouts.go:48-60`  
If `ipamConf.IPRanges` is empty (e.g., misconfiguration), the loop body never runs, `newips` is empty, and `cmdAdd` returns a CNI result with no IPs — silent misconfiguration.

### P1-7: `cmdDelFunc` error philosophy is inverted [HIGH]
**File:** `cmd/whereabouts.go:78-108`  
`cmdDelFunc` returns fatal errors for config parsing failures but silently succeeds when the actual IP deallocation fails. This is backwards — CNI spec says DEL should be lenient on preconditions and strict on resource cleanup.

### P1-8: Zero-value `net.IPNet{}` appended during deallocation [LOW]
**File:** `pkg/storage/kubernetes/ipam.go:736`  
During Deallocate mode, `newip` is never assigned, but `newips = append(newips, newip)` appends a zero-value. Currently benign (cmdDel discards the return), but latent.

### P1-9: Config file not found is fatal even with complete inline config [MEDIUM]
**File:** `pkg/config/config.go:56-58`  
`GetFlatIPAM` returns `ConfigFileNotFoundError` if no flat config file exists, and `LoadIPAMConfig` treats this as fatal. Non-standard deployments without the flat file fail even when inline IPAM config is complete.

### P1-10: `cmdCheck` error uses undefined CNI error code 0 [LOW]
**File:** `cmd/whereabouts.go:72-76`  
`fmt.Errorf(...)` produces error code `0` which is undefined in the CNI spec.

### P1-11: Test suite uses deprecated `RunSpecsWithDefaultAndCustomReporters` [INFO]
**File:** `cmd/whereabouts_test.go:40-43`

### P1-12: No `prevResult` handling in any CNI operation [INFO]
**File:** `cmd/whereabouts.go`  
CNI spec 0.4.0+ passes `prevResult` for chained plugins. Not required for pure IPAM, but needed for a correct CHECK implementation.

---

## Persona 2 — Kubernetes Storage & Concurrency Engineer

### P2-5: JSON Patch test operation uses uninitialized map [LOW] *(verified)*
**File:** `pkg/storage/kubernetes/ipam.go` — `formatPatch()`

### P2-6: Mutable global variables for retry config [LOW] *(verified)*
**File:** `pkg/storage/kubernetes/ipam.go:30-33`

### P2-7: Deadlock when `deposed` case fires in `IPManagement` [HIGH]
**File:** `pkg/storage/kubernetes/ipam.go:520-540`  
If the leader elector loses the lease, the `deposed` signal triggers `leCancel()`, but the main goroutine may be blocked on unbuffered `result` channel which is never drained in the deposed path.

### P2-8: `result` channel never drained on deposed [MEDIUM]
**File:** `pkg/storage/kubernetes/ipam.go:520`  
Related to P2-7.

### P2-9: Non-atomic IPPool + OverlappingRange update [HIGH]
**File:** `pkg/storage/kubernetes/ipam.go:649-680`  
IPPool CRD is updated first, then OverlappingRangeIPReservation is created. If the second step fails, the IP is allocated in IPPool but unprotected from cross-range duplication. The retry loop re-reads the IPPool but doesn't clean up the orphaned ORIP.

### P2-10: `Update()` only retries `IsInvalid`, not transient errors [MEDIUM]
**File:** `pkg/storage/kubernetes/ipam.go`

### P2-11: `normalizeRange` panics on empty string [LOW]

### P2-12: Shared timeout context between Get and Create in `getPool` [LOW]

### P2-13: `newip` not scoped per `ipRange` [LOW]
**File:** `pkg/storage/kubernetes/ipam.go:551,736`

### P2-14: `getNodeName` continues on read error [LOW]
**File:** `pkg/storage/kubernetes/ipam.go:378-381`

### P2-15: `overlappingrangeallocations` not reset between ipRanges [LOW]
**File:** `pkg/storage/kubernetes/ipam.go:572`

### P2-16: Leader election context detached from parent [LOW]
**File:** `pkg/storage/kubernetes/ipam.go:501`

---

## Persona 3 — Network / IPAM Specialist

### P3-1: `byteSliceSub` incorrect borrow formula [MEDIUM] *(verified)*
**File:** `pkg/iphelpers/iphelpers.go:293-313`

### P3-14: `byteSliceSub` borrow bug → round-trip CRD data corruption [HIGH]
**File:** `pkg/iphelpers/iphelpers.go:293-313` + `pkg/storage/kubernetes/ipam.go:244-270`  
Extension of P3-1 showing end-to-end impact. Write path: `IPGetOffset` computes wrong offset. Read path: `IPAddOffset` reconstructs a *different* IP. Result: the original IP appears free → duplicate allocation on the network. Trigger: any range where `range_start` doesn't end in `.0`.  
**Recommendation:** Single-line fix: change borrow formula to `sum = 0x100 + int(ar1[n]) - int(ar2[n])`.

### P3-2: Negative offset validation missing in `toIPReservationList` [MEDIUM] *(verified)*
### P3-3: Invalid CRD offsets silently skipped [LOW] *(verified)*
### P3-4: `ParseCIDR` error ignored in `AssignIP` [MEDIUM] *(verified)*
**File:** `pkg/allocate/allocate.go:30`

### P3-5: `DivideRangeBySize` panics on IPv6 input [MEDIUM] *(verified)*

### P3-6: `IncIP`/`DecIP` wrap around silently [LOW] *(verified)*

### P3-7: `ipAddrToUint64` truncates IPv6 offsets > 64 bits [MEDIUM]

### P3-8: `DivideRangeBySize` swallows `Atoi` error [MEDIUM]

### P3-9: `/31` and `/127` point-to-point ranges rejected [LOW]

### P3-10: `IPAddOffset` has no overflow protection for IPv6 [MEDIUM]

### P3-11: `normalizeRange` panics on empty string [LOW]

### P3-12: `DivideRangeBySize` unbounded slice allocation — OOM risk [LOW]

### P3-13: Documentation incorrectly claims `.0` address skipping [LOW]
**File:** `CLAUDE.md`, `.github/copilot-instructions.md`

### P3-15: `IPAddOffset` boundary guard off-by-one for IPv4 [LOW]

### P3-16: `toIPReservationList` does not check for nil IP from `IPAddOffset` [LOW]

---

## Persona 4 — Security Engineer

### P4-4: Webhook ClusterRole grants unrestricted Secrets access cluster-wide [HIGH]
**File:** `doc/crds/webhook-install.yaml`

### P4-5: DaemonSet container runs fully privileged with host access [HIGH]
**File:** `doc/crds/daemonset-install.yaml`

### P4-6: Container image runs as root — no USER directive [MEDIUM]
**File:** `Dockerfile`

### P4-7: CEL matchConditions bypass uses hardcoded SA names [MEDIUM]
**File:** `doc/crds/validatingwebhookconfiguration.yaml`

### P4-8: Operator/webhook Deployments missing `capabilities.drop: [ALL]` [MEDIUM]

### P4-9: Metrics endpoints exposed without authentication [MEDIUM]

### P4-10: All deployment manifests use `:latest` image tag [MEDIUM]

### P4-11: SA token written to world-readable host filesystem [MEDIUM]

### P4-12: `SKIP_TLS_VERIFY` allows disabling API server TLS [LOW]
### P4-13: TLS certificates stored in `/tmp/` directory [LOW]
### P4-14: Deprecated node-slice-controller has empty `securityContext` [LOW]
### P4-15: No NetworkPolicy for webhook or operator [LOW]
### P4-16: `replace` directive pins gogo/protobuf — may mask security patches [LOW]
### P4-17: Webhook `failurePolicy: Fail` creates a DoS vector [LOW]
### P4-18: CNI ClusterRole has unnecessary `delete` on IPPools [LOW]

---

## Persona 5 — Site Reliability Engineer

### P5-7: Webhook VWC name mismatch — cert-controller will fail to inject CA [CRITICAL]
**File:** `cmd/operator/webhook.go:67` vs `doc/crds/validatingwebhookconfiguration.yaml:12`  
```go
WebhookName: "whereabouts-validating-webhook",   // singular in Go code
```
```yaml
name: whereabouts-validating-webhooks             # plural in YAML
```
cert-controller looks up the VWC by name to inject the CA bundle. The name mismatch means it will never find it → webhooks get no CA → API server can't verify webhook TLS → all validating webhook calls fail with TLS errors.  
**Recommendation:** Align both to the same name (either singular or plural).

### P5-8: `context.Background()` instead of signal-aware context [HIGH]
**File:** `cmd/operator/controller.go:91`, `cmd/operator/webhook.go:89`  
`mgr.Start(context.Background())` ignores SIGTERM/SIGINT. Use `ctrl.SetupSignalHandler()`.

### P5-9: No PodDisruptionBudget for operator or webhook [HIGH]

### P5-10: No pod anti-affinity or topology spread [MEDIUM]
### P5-11: All webhook `failurePolicy: Fail` — outage blocks CNI operations [HIGH]
### P5-12: No Deployment `strategy` defined [MEDIUM]
### P5-13: `:latest` image tag — no rollback safety [HIGH]
### P5-14: No `terminationGracePeriodSeconds` configured [MEDIUM]
### P5-15: No custom application metrics [MEDIUM]
### P5-16: No metrics Service for Prometheus scraping [MEDIUM]
### P5-17: Log level only supports "debug" vs non-debug [LOW]
### P5-18: Webhook cert volume `optional: true` [MEDIUM]
### P5-19: NodeSliceReconciler silently drops nodes when pool is full [MEDIUM]
### P5-20: `mapNodeToNADs` enqueues ALL NADs on every node event — O(N×M) [MEDIUM]
### P5-21: Missing seccomp/runAsUser in pod securityContext [LOW]
### P5-22: Leader election defaults not tuned, no `ReleaseOnCancel` [LOW]
### P5-23: `cleanupOverlappingReservations` errors logged but not returned [LOW]
### P5-24: Webhook cert directory uses `/tmp` path [LOW]
### P5-25: `ensureNodeAssignments` uses full Status Update, not Patch [LOW]

---

## Persona 6 — Go Code Quality Expert

### P6-1: `defer` inside loop leaks file handles [HIGH] *(verified)*
**File:** `pkg/config/config.go`

### P6-2: Global logging state is not thread-safe [MEDIUM] *(verified)*
### P6-3: `Panicf` does not panic [MEDIUM] *(verified)*
### P6-4: Log file handle never closed; missing return in error branch [LOW] *(verified)*

### P6-5: `context.Background()` used instead of `ctrl.SetupSignalHandler()` [MEDIUM]
*(Same underlying issue as P5-8)*

### P6-6: `ctrl.GetConfigOrDie()` panics inside Cobra `RunE` [LOW]

### P6-7: `setupLogger` silently swallows flag error [LOW]
### P6-8: `setupLogger` ignores `"error"` log level [LOW]

### P6-9: `denormalizeIPName` unbounded recursion [MEDIUM]
**File:** `internal/controller/ippool_controller.go`

### P6-10: `ensureOwnerRef` uses potentially empty `APIVersion`/`Kind` from NAD [MEDIUM]
**File:** `internal/controller/nodeslice_controller.go`

### P6-11: `ensureOwnerRef` error logged not returned [LOW]
### P6-12: Missing `NeedLeaderElection()` on webhook Setup runnable [LOW]

### P6-13: `cleanupOverlappingReservations` O(N×M) API calls [MEDIUM]

### P6-14: `cleanupOverlappingReservations` errors silently swallowed [LOW]
### P6-15: `addOffsetToIP` signed arithmetic — negative offsets produce wrong results [LOW]
### P6-16: `NodeSlicePoolValidator` accepts IPv4-invalid prefix lengths [INFO]

---

## Persona 7 — Test Quality Expert

### P7-5: No concurrent allocation test [MEDIUM] *(verified)*
### P7-7: Test writes to `/tmp/whereabouts.conf` [LOW] *(verified)*

### P7-8: NodeSliceReconciler has ZERO test coverage [HIGH]
**File:** Missing `internal/controller/nodeslice_controller_test.go`  
435 lines of the most complex reconciler with no tests.

### P7-9: IPPoolReconciler tests don't cover multi-range pools [MEDIUM]
### P7-10: No webhook test for invalid CIDR ranges [MEDIUM]
### P7-11: OverlappingRange webhook test missing malformed podRef edge cases [LOW]
### P7-12: No envtest test for webhook cert bootstrapping [LOW]
### P7-13: No test for `denormalizeIPName` edge cases [LOW]

---

## Persona 8 — CI/CD & DevOps Expert

### P8-3: Near-identical image build jobs [HIGH] *(verified)*
### P8-7: E2E depends on moving targets [MEDIUM] *(verified)*
### P8-10: Chart version not committed [LOW] *(verified)*

### P8-11: Dockerfile drops version/git metadata [HIGH]

### P8-12: No race detection in CI test pipeline [MEDIUM]
### P8-13: Release workflow re-runs full test suite redundantly [MEDIUM]
### P8-14: `binaries-upload-release` only uploads `whereabouts`, not operator [LOW]

### P8-15: Helm chart DaemonSet references non-existent `/ip-control-loop` binary [HIGH]
**File:** `deployment/whereabouts-chart/templates/`  
The Helm chart still deploys containers with `/ip-control-loop` as the entrypoint. This binary was removed in the migration. Helm installs will produce CrashLoopBackOff.

### P8-16: Helm chart Deployment references non-existent `/node-slice-controller` [HIGH]
**File:** `deployment/whereabouts-chart/templates/`  
Same issue — deleted binary.

### P8-17: Node-slice-controller Deployment ignores `values.yaml` volume paths [LOW]
### P8-18: `verify-codegen.sh` compares generated code backwards [MEDIUM]
### P8-19: Makefile `generate-api` target calls verify instead of generate [MEDIUM]
### P8-20: DaemonSet runs as `privileged: true` without justification [MEDIUM]
### P8-21: `e2e-get-test-tools.sh` only downloads amd64 binaries [LOW]
### P8-22: `test-go.sh` runs `sudo` in CI [LOW]
### P8-23: `install-kubebuilder-tools.sh` name misleading [LOW]
### P8-24: Trivy install via unpinned curl-pipe-sh [LOW]
### P8-25: `license-audit` job swallows failures with `|| true` [LOW]

---

## Persona 9 — Kubernetes Platform Architect

### P9-6: IPPool CRD stores mutable allocation state in `spec` — no status subresource [MEDIUM]
**File:** `pkg/api/whereabouts.cni.cncf.io/v1alpha1/ippool_types.go`

### P9-7: CRDs lack structural validation — CIDR format and sliceSize type [MEDIUM]
### P9-8: No CRD versioning strategy — single v1alpha1 with no conversion path [MEDIUM]

### P9-9: Helm chart deploys deprecated architecture [HIGH]
**File:** `deployment/whereabouts-chart/`  
*(Same as P8-15/P8-16)*

### P9-10: Operator/webhook Deployments lack nodeSelector, tolerations, priorityClass [MEDIUM]
### P9-11: No sidecar injection exclusion annotation [LOW]
### P9-12: CNI SA has cluster-wide CRD CRUD — no tenant boundary [LOW]
### P9-13: Helm chart resource limits (50Mi) much lower than doc/crds (200Mi) [MEDIUM]
### P9-14: No NetworkPolicy for operator or webhook [LOW]
### P9-15: IPPool CRD has no additionalPrinterColumns for allocation count [LOW]
### P9-16: CRD shortName `nsp` risks collision [LOW]
### P9-17: Webhook `matchConditions` CEL bypass hardcodes SA names [MEDIUM]
### P9-18: DaemonSet `privileged: true` without capability scoping [MEDIUM]
### P9-19: Helm chart CRDs in `crds/` directory won't update on `helm upgrade` [MEDIUM]

---

## Persona 10 — Controller-Runtime Expert

### P13-1: `context.Background()` instead of `ctrl.SetupSignalHandler()` [HIGH]
*(Same as P5-8/P6-5)*

### P13-2: Webhook registration after manager starts — race with webhook server [HIGH]
**File:** `internal/webhook/setup.go`  
Webhooks are registered via `Runnable.Start()` after the manager (and its webhook server) have already started. If a request arrives between server start and webhook registration, it gets a 404.

### P13-3: All reconcilers read pods from cache, not API server [MEDIUM]
Stale cache could cause false-positive orphan detection.

### P13-4: `cleanupOverlappingReservations` silently swallows errors [MEDIUM]
*(Same as P6-14/P5-23)*

### P13-5: No event recording in any reconciler [MEDIUM]

### P13-6: `NodeSliceReconciler` uses `WatchesRawSource` instead of typed `Watches` [LOW]
### P13-7: `mapNodeToNADs` lists all NADs cluster-wide on every node event [MEDIUM]
*(Same as P5-20)*
### P13-8: `checkMultiNADMismatch` permanent error causes infinite requeue [LOW]
### P13-9: `createPool` sets status in Create body (ignored by API server) [LOW]
### P13-10: `ensureOwnerRef` silently swallows patch errors [MEDIUM]
### P13-11: Webhook server disabled via port 0 in controller — opens unused socket [INFO]
### P13-12: No predicates to filter resource update events [MEDIUM]
### P13-13: `ensureNodeAssignments` uses `Status().Update` instead of `Status().Patch` [LOW]

---

## Persona 11 — Data Integrity / IP Leak Specialist

### L1: `cmdDel` unconditionally swallows errors → leaked IPs [CRITICAL] *(verified)*
**Code path:** `cmdDelFunc` → `cmdDel` → `kubernetes.IPManagement` → `_, _ =` discards error → returns `nil`  
**Impact:** Primary source of IP leaks. Any failure in deallocation is invisible.

### L2: Partial multi-range allocation not rolled back [HIGH] *(verified)*
**Code path:** `IPManagement Allocate` → range 1 succeeds → range 2 fails → range 1's IP remains allocated

### L3: Non-atomic IPPool + OverlappingRange update → orphaned reservation or unprotected IP [HIGH] *(verified)*
*(Same as P2-9)*

### L4: DEL after container restart: `containerID` mismatch → leaked IP [HIGH] *(verified)*
**Code path:** Pod created → IP allocated with containerID-A → container restarts (new containerID-B) → DEL with containerID-B → lookup fails → IP leaks.

### L5: Idempotent ADD masks containerID change [MEDIUM] *(verified)*

### L6: Multi-range deallocation aborts all remaining ranges on first miss [CRITICAL]
**File:** `pkg/storage/kubernetes/ipam.go`  
**Code path:** `IPManagement Deallocate` → range 1 deallocation misses → function returns error → ranges 2..N never attempted → those IPs leak.  
**Recommendation:** Log the error and continue to remaining ranges; return an aggregate error at the end.

### L7: No backoff between retries → exhaustion under contention [MEDIUM]

### L8: Reconciler false positive on partially-annotated multi-network pods [HIGH]
**File:** `internal/controller/ippool_controller.go`

### L9: Reconciler reclaims DisruptionTarget IPs before grace period expires [MEDIUM]

### L10: Allocate: IPPool committed before OverlappingRange creates danger window [HIGH]
*(Same as L3/P2-9)*

### L11: Leader election timeout on DEL → silent IP leak [MEDIUM]

### L12: `cleanupOverlappingReservations` swallows errors → orphaned ORIP CRDs [LOW]

### L13: `newLeaderElector` returns nil on multiple error paths → unrecoverable DEL failure [MEDIUM]

---

## Persona 12 — Configuration & Documentation Expert

### P12-4: Etcd configuration fields parsed but silently discarded [MEDIUM] *(verified)*
### P12-5: OverlappingRanges merge logic silently overrides flat-file setting [MEDIUM] *(verified)*
### P12-6: `SleepForRace` exposed as production config without validation [LOW] *(verified)*
### P12-7: `node_slice_size` not validated during config loading [MEDIUM]
### P12-8: `leader_lease_duration/renew_deadline/retry_period` not validated [LOW]
### P12-9: `log_level` documentation lists incorrect valid values [LOW]

### P12-10: Extended configuration documents etcd as supported backend [MEDIUM]
**File:** `doc/extended-configuration.md`

### P12-11: README references etcd as a current storage option [MEDIUM]

### P12-12: README Fast IPAM section references deprecated `node-slice-controller.yaml` [MEDIUM]

### P12-13: Helm chart DaemonSet references non-existent `/ip-control-loop` binary [HIGH]
*(Same as P8-15)*

### P12-14: Helm chart Deployment references non-existent `/node-slice-controller` binary [HIGH]
*(Same as P8-16)*

### P12-15: No migration guide for operator architecture transition [MEDIUM]
### P12-16: README installation instructions don't mention NodeSlicePool CRD [LOW]
### P12-17: `mergo.Merge` error silently logged but not returned [LOW]
### P12-18: `GetFlatIPAM` defers `Close()` inside a loop [LOW]
### P12-19: 11 undocumented configuration options [MEDIUM]
### P12-20: `doc/extended-configuration.md` references non-existent `ip-reconcilier-job.yaml` [LOW]
### P12-21: Reconciler section describes obsolete CronJob architecture [MEDIUM]
### P12-22: `doc/developer_notes.md` only shows etcd-based examples [LOW]
### P12-23: Known limitations section references etcd and omits current limitations [LOW]

---

## Deduplicated Cross-Cutting Issues

Several findings were independently discovered by multiple personas. These represent the highest-confidence issues:

| Core Issue | Found By | Unified ID |
|-----------|----------|------------|
| `context.Background()` instead of signal handler | P5-8, P6-5, P13-1 | **P5-8** |
| `cmdDel` swallows errors / IP leaks | P1-2, L1 | **L1** |
| Non-atomic IPPool + ORIP update | P2-9, L3, L10 | **P2-9** |
| `cleanupOverlappingReservations` swallows errors | P5-23, P6-14, P13-4, L12 | **P6-14** |
| `ensureOwnerRef` error handling | P6-11, P13-10 | **P6-11** |
| Helm chart references deleted binaries | P8-15, P8-16, P9-9, P12-13, P12-14 | **P8-15/P8-16** |
| `:latest` image tags | P4-10, P5-13 | **P4-10** |
| `mapNodeToNADs` O(N×M) | P5-20, P13-7 | **P5-20** |
| Webhook `failurePolicy: Fail` DoS risk | P4-17, P5-11 | **P5-11** |
| Metrics unauthenticated | P4-9, P5-16 | **P4-9** |
| DaemonSet privileged | P4-5, P8-20, P9-18 | **P4-5** |
| No NetworkPolicy | P4-15, P9-14 | **P4-15** |

---

## Priority Matrix (Top 20)

| Priority | ID | Title | Severity | Effort |
|----------|----|-------|----------|--------|
| 1 | P5-7 | VWC name mismatch — webhooks completely broken | CRITICAL | **5 min** |
| 2 | L1 | `cmdDel` swallows errors — primary IP leak source | CRITICAL | 30 min |
| 3 | L6 | Multi-range dealloc aborts on first miss | CRITICAL | 30 min |
| 4 | P3-14 | `byteSliceSub` borrow bug → duplicate IPs | HIGH | 15 min |
| 5 | P8-15/16 | Helm chart references deleted binaries | HIGH | 2 hr |
| 6 | P5-8 | `context.Background()` → no graceful shutdown | HIGH | 10 min |
| 7 | P2-9 | Non-atomic IPPool + ORIP update | HIGH | 4 hr |
| 8 | P2-7 | Leader election deposed → deadlock | HIGH | 2 hr |
| 9 | L4 | containerID mismatch on DEL → leaked IP | HIGH | 2 hr |
| 10 | P1-7 | `cmdDelFunc` strict on config, silent on dealloc | HIGH | 30 min |
| 11 | P5-9 | No PodDisruptionBudget | HIGH | 30 min |
| 12 | P5-11 | `failurePolicy: Fail` blocks CNI on webhook outage | HIGH | 15 min |
| 13 | P4-4 | Webhook ClusterRole — cluster-wide Secrets access | HIGH | 15 min |
| 14 | L8 | Reconciler false positive on multi-network pods | HIGH | 2 hr |
| 15 | P7-8 | NodeSliceReconciler has zero tests | HIGH | 4 hr |
| 16 | P6-1 | `defer` in loop leaks file handles | HIGH | 15 min |
| 17 | P1-1 | `cmdCheck` not implemented | HIGH | 1 hr |
| 18 | P8-11 | No version/git info in binaries | HIGH | 30 min |
| 19 | L2 | Partial multi-range alloc not rolled back | HIGH | 4 hr |
| 20 | P12-15 | No migration guide for operator transition | MEDIUM | 2 hr |

---

## Appendix: Per-Persona Detail Files

Full findings with code snippets, line numbers, and recommendations are in the individual review files:

- `review-cni-spec.md` — Persona 1 (CNI Spec Expert)
- `review-storage-concurrency.md` — Persona 2 (Storage & Concurrency)
- `review-ip-allocation.md` — Persona 3 (Network/IPAM)
- `review-security.md` — Persona 4 (Security)
- `review-sre-operator.md` — Persona 5 (SRE Operations)
- `review-code-quality.md` — Persona 6 (Go Code Quality)
- `review-test-quality.md` — Persona 7 (Test Quality)
- `review-cicd.md` — Persona 8 (CI/CD & DevOps)
- `review-platform-architecture.md` — Persona 9 (Platform Architecture)
- `review-controller-runtime.md` — Persona 10 (controller-runtime Expert)
- `review-data-integrity.md` — Persona 11 (Data Integrity / IP Leaks)
- `review-config-docs.md` — Persona 12 (Configuration & Documentation)
