# Configuration & Documentation Review — Whereabouts IPAM CNI Plugin

**Reviewer focus:** Configuration handling correctness, documentation accuracy, and completeness after the operator migration.

---

## Previously Identified (Still Present)

| ID | Title | Status |
|----|-------|--------|
| P12-1 | Silently discarded etcd config | Confirmed — see P12-4 below for details |
| P12-2 | OverlappingRanges defaults to true | Confirmed — see P12-5 below for details |
| P12-3 | SleepForRace in production config | Confirmed — see P12-6 below for details |

---

## New Findings

### P12-4: Etcd configuration fields parsed but silently discarded (MEDIUM)

**Severity:** MEDIUM  
**Files:** [pkg/types/types.go](pkg/types/types.go#L93-L98), [pkg/config/config.go](pkg/config/config.go#L37-L165)  

**Description:**  
The `UnmarshalJSON` method in `IPAMConfigAlias` still accepts six etcd-related fields (`etcd_host`, `etcd_username`, `etcd_password`, `etcd_key_file`, `etcd_cert_file`, `etcd_ca_cert_file`) and the `datastore` field. These fields are parsed during JSON unmarshalling but are **never** transferred to the `IPAMConfig` struct — the assignment block at [lines 122–150](pkg/types/types.go#L122-L150) simply omits them. The storage layer only has a Kubernetes implementation; there is no etcd backend in the codebase.

A user configuring etcd (following the extensive [extended-configuration.md](doc/extended-configuration.md#L27-L65) instructions) gets zero feedback that their config is ignored. The CNI plugin silently falls through to Kubernetes storage.

**Recommendation:**  
1. Log a warning in `LoadIPAMConfig` if `Datastore` is `"etcd"` or `EtcdHost` is non-empty: *"etcd backend is no longer supported; using Kubernetes CRDs"*.
2. Remove etcd parameters from `IPAMConfigAlias` or mark them explicitly deprecated with warning propagation.
3. Update documentation (see P12-10).

---

### P12-5: OverlappingRanges merge logic silently overrides flat-file setting (MEDIUM)

**Severity:** MEDIUM  
**Files:** [pkg/config/config.go](pkg/config/config.go#L62-L66)  

**Description:**  
The merge logic saves the inline config's `OverlappingRanges` value *before* calling `mergo.Merge`, then unconditionally restores it afterward:

```go
var OverlappingRanges = n.IPAM.OverlappingRanges  // line 62
if err := mergo.Merge(&n, flatipam); err != nil { ... }
n.IPAM.OverlappingRanges = OverlappingRanges       // line 66
```

Because `OverlappingRanges` is a `bool` with `omitempty`, and the `UnmarshalJSON` defaults it to `true`, the flat-file value for `enable_overlapping_ranges` is **always** overridden by the inline config default. If a user sets `"enable_overlapping_ranges": false` in the flat file but doesn't repeat it in every NAD config, overlay ranges remain enabled. This contradicts the documented merge precedence in [extended-configuration.md](doc/extended-configuration.md#L92) — *"options added to whereabouts.conf are overridden by configuration options that are in the primary CNI configuration"* — because a user who doesn't specify the option in the primary config expects the flat-file value to apply.

The test at [config_test.go](pkg/config/config_test.go#L127-L159) only checks cases where the inline config explicitly sets the field, never the case where it's omitted.

**Recommendation:**  
Distinguish "user explicitly set `false`" from "field absent/defaulted" using a `*bool` pointer type, or track whether the field was present in the inline JSON before applying the merge.

---

### P12-6: `SleepForRace` exposed as production config without validation (LOW)

**Severity:** LOW  
**Files:** [pkg/types/types.go](pkg/types/types.go#L69), [pkg/storage/kubernetes/ipam.go](pkg/storage/kubernetes/ipam.go#L701-L702)  

**Description:**  
`sleep_for_race` is a JSON-configurable integer that directly calls `time.Sleep(time.Duration(ipamConf.SleepForRace) * time.Second)` in the allocation hot path. There is no upper bound, no documentation, and no validation. A misconfiguration (e.g., `"sleep_for_race": 300`) blocks every IP allocation for 5 minutes. This is a testing-only knob that should not be user-facing.

**Recommendation:**  
Either remove the field from the public `IPAMConfig` struct (inject it only in test builds), or add validation capping it at a safe maximum (e.g., 5 seconds) with a log warning.

---

### P12-7: `node_slice_size` not validated during config loading (MEDIUM)

**Severity:** MEDIUM  
**Files:** [pkg/config/config.go](pkg/config/config.go#L37-L165), [pkg/types/types.go](pkg/types/types.go#L58)  

**Description:**  
The `node_slice_size` field is accepted as a freeform `string` (e.g., `"/22"`) and passed through without any validation in `LoadIPAMConfig`. There is no check that:
- The value is a valid CIDR prefix (e.g., must start with `/`)
- The slice size is reasonable relative to the range
- The value is an integer between 1 and 128 (the webhook validates 1–128 for the CRD, but the CNI config path has no such check)

An invalid `node_slice_size` will only surface as a runtime error deep in the node-slice controller, not at config-loading time.

**Recommendation:**  
Add validation in `LoadIPAMConfig` to verify `NodeSliceSize` parses as a valid CIDR prefix when non-empty.

---

### P12-8: `leader_lease_duration`, `leader_renew_deadline`, `leader_retry_period` not validated for correctness (LOW)

**Severity:** LOW  
**Files:** [pkg/config/config.go](pkg/config/config.go#L149-L161)  

**Description:**  
These three integer fields (milliseconds) are defaulted if zero but never validated for sanity. Invalid configurations are silently accepted:
- `leader_renew_deadline` ≥ `leader_lease_duration` (should always be `<`)
- Negative values
- `leader_retry_period` > `leader_renew_deadline`

The Kubernetes leader election library will panic or produce deadlocks with invalid lease parameters.

**Recommendation:**  
Add post-default validation: `LeaderRenewDeadline < LeaderLeaseDuration` and `LeaderRetryPeriod < LeaderRenewDeadline`, returning an error with a clear message.

---

### P12-9: `log_level` documentation lists incorrect valid values (LOW)

**Severity:** LOW  
**Files:** [doc/extended-configuration.md](doc/extended-configuration.md#L72)  

**Description:**  
The documentation states:
> `log_level`: Set the logging verbosity, from most to least: `debug`, `error`, `panic`

The actual logging implementation in [pkg/logging/logging.go](pkg/logging/logging.go#L30-L34) supports four levels: `panic`, `error`, `verbose`, `debug`. The `verbose` level is missing from the documentation. The ordering is also wrong — from most to least verbose: `debug` → `verbose` → `error` → `panic`.

**Recommendation:**  
Update documentation to: *"from most to least verbose: `debug`, `verbose`, `error`, `panic`"*.

---

### P12-10: Extended configuration documents etcd as supported backend (MEDIUM)

**Severity:** MEDIUM  
**Files:** [doc/extended-configuration.md](doc/extended-configuration.md#L27-L65)  

**Description:**  
A large section of the extended configuration document provides detailed etcd configuration instructions, including a full example config, required/optional parameters, and an etcd installation guide using the deprecated etcd-operator. The etcd storage backend has been entirely removed from the codebase — only the Kubernetes CRD backend exists. Users following these instructions will get silent failures (see P12-4).

Additionally, line 24 references a wrong API group: `ippools.whereabouts.cni.k8s.io/v1alpha1` — the actual group is `whereabouts.cni.cncf.io`.

**Recommendation:**  
1. Remove or clearly mark the etcd sections as deprecated/unsupported.
2. Fix the CRD API group reference from `whereabouts.cni.k8s.io` to `whereabouts.cni.cncf.io`.

---

### P12-11: README references etcd as a current storage option (MEDIUM)

**Severity:** MEDIUM  
**Files:** [README.md](README.md#L28), [README.md](README.md#L39), [README.md](README.md#L367)  

**Description:**  
Three locations in the README still present etcd as a supported storage backend:

1. Line 28: *"Whereabouts uses etcd or a Kubernetes Custom Resource as a backend"*
2. Line 39: *"Further installation options (including etcd usage)"*
3. Line 367 (Known limitations): *"The etcd method has a number of limitations..."*

Etcd support has been removed. These references mislead users.

**Recommendation:**  
Remove etcd references. State that Kubernetes CRDs are the sole storage backend.

---

### P12-12: README Fast IPAM section references deprecated `node-slice-controller.yaml` (MEDIUM)

**Severity:** MEDIUM  
**Files:** [README.md](README.md#L223-L224)  

**Description:**  
The Fast IPAM documentation states:
> *"Please note, you must run a whereabouts controller for this to work. Manifest can be found in doc/crds/node-slice-controller.yaml."*

The `node-slice-controller.yaml` manifest is itself marked `DEPRECATED` (header comment says it's superseded by `operator-install.yaml`), and the binary it references (`/node-slice-controller`) is no longer built by the Dockerfile. Users should be directed to `operator-install.yaml` instead.

**Recommendation:**  
Update the README to reference `doc/crds/operator-install.yaml` for the Fast IPAM controller requirement.

---

### P12-13: Helm chart DaemonSet references non-existent `/ip-control-loop` binary (HIGH)

**Severity:** HIGH  
**Files:** [deployment/whereabouts-chart/templates/daemonset.yaml](deployment/whereabouts-chart/templates/daemonset.yaml#L49)  

**Description:**  
The Helm chart DaemonSet template runs:
```
/ip-control-loop -log-level debug
```

The Dockerfile only produces two binaries: `/whereabouts` and `/whereabouts-operator`. There is no `/ip-control-loop` binary in the container image. Deploying this Helm chart will cause the DaemonSet pods to crash with `exec: "/ip-control-loop": not found`.

**Recommendation:**  
Remove the `/ip-control-loop` invocation from the DaemonSet template. The DaemonSet should only run `install-cni.sh` + `token-watcher.sh`. IP reconciliation is handled by the operator deployment.

---

### P12-14: Helm chart node-slice-controller references non-existent `/node-slice-controller` binary (HIGH)

**Severity:** HIGH  
**Files:** [deployment/whereabouts-chart/templates/node-slice-controller.yaml](deployment/whereabouts-chart/templates/node-slice-controller.yaml#L23)  

**Description:**  
The Helm chart's node-slice-controller template uses `command: ["/node-slice-controller"]`. This binary is no longer produced by the Dockerfile. Like P12-13, deploying with `nodeSliceController.enabled: true` (the default per `values.yaml`) will crash.

**Recommendation:**  
Replace the standalone `node-slice-controller` template with an operator Deployment template using `/whereabouts-operator controller`, or disable it by default and add operator templates to the Helm chart.

---

### P12-15: No migration guide for operator architecture transition (MEDIUM)

**Severity:** MEDIUM  
**Files:** Project-wide  

**Description:**  
The codebase has undergone significant architectural changes:
- `ip-control-loop` binary → `whereabouts-operator controller` subcommand
- Standalone `node-slice-controller` binary → integrated into operator
- New webhook server (`whereabouts-operator webhook`)
- New CRD manifests (`operator-install.yaml`, `webhook-install.yaml`, `validatingwebhookconfiguration.yaml`)

There is no migration guide explaining:
- Which old manifests to remove (`node-slice-controller.yaml` is deprecated)
- What new manifests to apply (`operator-install.yaml`, `webhook-install.yaml`)
- RBAC changes (new ServiceAccounts, ClusterRoles)
- Breaking Helm chart changes

The [doc/crds/daemonset-install.yaml](doc/crds/daemonset-install.yaml) has been updated with accurate comments, and `operator-install.yaml` has a brief header, but there is no user-facing migration document.

**Recommendation:**  
Create a `MIGRATION.md` or a section in the README documenting the transition from the old architecture to the new operator-based architecture, including step-by-step instructions.

---

### P12-16: README installation instructions don't mention NodeSlicePool CRD (LOW)

**Severity:** LOW  
**Files:** [README.md](README.md#L44-L50)  

**Description:**  
The quick-start installation command applies only two CRDs:
```
kubectl apply \
    -f doc/crds/daemonset-install.yaml \
    -f doc/crds/whereabouts.cni.cncf.io_ippools.yaml \
    -f doc/crds/whereabouts.cni.cncf.io_overlappingrangeipreservations.yaml
```

The `whereabouts.cni.cncf.io_nodeslicepools.yaml` CRD is missing. Users who later enable Fast IPAM will get API errors because the NodeSlicePool CRD doesn't exist. The operator and webhook manifests are also not mentioned anywhere in the installation flow.

**Recommendation:**  
Add the NodeSlicePool CRD to the installation command. Add a separate "Operator Installation" section for users who want reconciliation and webhooks.

---

### P12-17: `mergo.Merge` error silently logged but not returned (LOW)

**Severity:** LOW  
**Files:** [pkg/config/config.go](pkg/config/config.go#L63-L65)  

**Description:**  
```go
if err := mergo.Merge(&n, flatipam); err != nil {
    logging.Errorf("Merge error with flat file: %s", err)
}
```

If `mergo.Merge` fails, the error is logged but execution continues with a partially-merged configuration. The function doesn't return the error. This means the plugin proceeds with an unpredictable config state — some fields from the flat file may have been applied, others not.

**Recommendation:**  
Return the error from `LoadIPAMConfig` on merge failure rather than continuing with a broken config.

---

### P12-18: `GetFlatIPAM` defers `jsonFile.Close()` inside a loop — potential resource leak (LOW)

**Severity:** LOW  
**Files:** [pkg/config/config.go](pkg/config/config.go#L237-L255)  

**Description:**  
```go
for _, confpath := range confdirs {
    if pathExists(confpath) {
        jsonFile, err := os.Open(confpath)
        // ...
        defer jsonFile.Close()  // deferred until function returns, not loop iteration
```

The `defer` is inside a loop. While in practice the function returns on the first successful file open (so only one file is opened), if the code path were modified to try multiple files, file handles would leak. This is a minor code quality issue.

**Recommendation:**  
Close the file explicitly after `io.ReadAll` instead of using `defer` inside the loop, or extract the file-reading logic into a separate function.

---

### P12-19: Undocumented configuration options (MEDIUM)

**Severity:** MEDIUM  
**Files:** [doc/extended-configuration.md](doc/extended-configuration.md), [README.md](README.md)  

**Description:**  
The following `IPAMConfig` fields are accepted in JSON but never documented in user-facing docs:

| Field | JSON key | Documentation |
|-------|----------|---------------|
| `LeaderLeaseDuration` | `leader_lease_duration` | None |
| `LeaderRenewDeadline` | `leader_renew_deadline` | None |
| `LeaderRetryPeriod` | `leader_retry_period` | None |
| `NodeSliceSize` | `node_slice_size` | Only in README Fast IPAM example, no parameter description |
| `ReconcilerCronExpression` | `reconciler_cron_expression` | Only in extended-configuration.md via env var, not as IPAM JSON field |
| `ConfigurationPath` | `configuration_path` | Mentioned in extended-config but not in the Core Parameters table |
| `SleepForRace` | `sleep_for_race` | None (should not be documented — should be removed) |
| `Addresses` | `addresses` | None |
| `Routes` | `routes` | None |
| `DNS` | `dns` | None |
| `NetworkName` | `network_name` | Only in README, not in extended-configuration.md |

**Recommendation:**  
Create a comprehensive configuration reference table documenting all accepted IPAM JSON fields, their types, defaults, and valid values.

---

### P12-20: `doc/extended-configuration.md` references non-existent `ip-reconcilier-job.yaml` (LOW)

**Severity:** LOW  
**Files:** [doc/extended-configuration.md](doc/extended-configuration.md#L16)  

**Description:**  
The IP Reconciliation section states:
> *"A reference deployment of this tool is available in the `/docs/ip-reconcilier-job.yaml` file."*

This file does not exist in the repository (neither at `/docs/ip-reconcilier-job.yaml` nor at any similar path). The path also has a typo (`reconcilier` → `reconciler`). IP reconciliation is now handled by the operator, making this reference doubly obsolete.

**Recommendation:**  
Remove the reference to the non-existent file and update the section to describe reconciliation via the operator Deployment.

---

### P12-21: `doc/extended-configuration.md` reconciler section describes obsolete CronJob architecture (MEDIUM)

**Severity:** MEDIUM  
**Files:** [doc/extended-configuration.md](doc/extended-configuration.md#L7-L17), [doc/extended-configuration.md](doc/extended-configuration.md#L133-L148)  

**Description:**  
The document describes IP reconciliation as a standalone CronJob tool, and provides instructions for configuring the cron expression via `WHEREABOUTS_RECONCILER_CRON` environment variable or ConfigMap. The current architecture uses the operator's `IPPoolReconciler` running as a continuous controller-runtime reconciliation loop (watch-based), not a CronJob.

The ConfigMap-based cron schedule configuration in the extended docs corresponds to the old `ip-control-loop` binary, which no longer exists.

**Recommendation:**  
Rewrite the reconciliation section to describe the operator-based reconciliation model. Remove CronJob references or clearly mark them as legacy.

---

### P12-22: `doc/developer_notes.md` only shows etcd-based examples (LOW)

**Severity:** LOW  
**Files:** [doc/developer_notes.md](doc/developer_notes.md#L25-L46)  

**Description:**  
The "Running in Kube" section shows an example NAD config that includes `"etcd_host": "10.107.83.18:2379"`. This is the only example in the developer notes, and it uses a backend that no longer exists. The Kubernetes CRD backend isn't shown.

**Recommendation:**  
Replace the etcd example with a Kubernetes-native example using `"kubernetes": {"kubeconfig": "..."}`.

---

### P12-23: Known limitations section references etcd and omits actual current limitations (LOW)

**Severity:** LOW  
**Files:** [README.md](README.md#L362-L370)  

**Description:**  
The "Known limitations" section includes:
> *"The etcd method has a number of limitations..."*

This references a non-existent backend. Meanwhile, actual current limitations are not listed:
- Operator must run in the same namespace as NADs (for node-slice mode)
- Webhooks require cert-controller for TLS
- No HA for the CronJob-based reconciliation (now operator handles this, but not documented)

**Recommendation:**  
Remove the etcd limitation. Add current architecture limitations.

---

## Summary Table

| ID | Severity | Category | Description |
|----|----------|----------|-------------|
| P12-4 | MEDIUM | Config | Etcd config fields parsed but silently discarded |
| P12-5 | MEDIUM | Config | OverlappingRanges merge bypasses flat-file value |
| P12-6 | LOW | Config | `sleep_for_race` unbounded in production |
| P12-7 | MEDIUM | Config | `node_slice_size` not validated at config load time |
| P12-8 | LOW | Config | Leader election params not validated for correctness |
| P12-9 | LOW | Docs | `log_level` docs missing "verbose", wrong ordering |
| P12-10 | MEDIUM | Docs | Extended config documents etcd as supported; wrong API group |
| P12-11 | MEDIUM | Docs | README presents etcd as current storage option |
| P12-12 | MEDIUM | Docs | README Fast IPAM references deprecated manifest |
| P12-13 | HIGH | Helm | DaemonSet runs non-existent `/ip-control-loop` binary |
| P12-14 | HIGH | Helm | node-slice-controller runs non-existent binary |
| P12-15 | MEDIUM | Docs | No migration guide for operator transition |
| P12-16 | LOW | Docs | Installation omits NodeSlicePool CRD |
| P12-17 | LOW | Config | `mergo.Merge` error logged but not returned |
| P12-18 | LOW | Code | `defer Close()` inside loop in `GetFlatIPAM` |
| P12-19 | MEDIUM | Docs | 11 config options undocumented |
| P12-20 | LOW | Docs | Reference to non-existent `ip-reconcilier-job.yaml` |
| P12-21 | MEDIUM | Docs | Reconciler section describes obsolete CronJob model |
| P12-22 | LOW | Docs | Developer notes only show etcd examples |
| P12-23 | LOW | Docs | Known limitations reference etcd, omit current issues |
