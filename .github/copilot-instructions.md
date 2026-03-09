# Copilot Instructions — Whereabouts (Deutsche Telekom T-CAAS Fork)

This is a fork of [k8snetworkplumbingwg/whereabouts](https://github.com/k8snetworkplumbingwg/whereabouts), maintained by the Deutsche Telekom T-CAAS team. It is a Kubernetes IPAM CNI plugin that assigns IP addresses cluster-wide using CRDs.

## Architecture Overview

- **CNI entry point** ([cmd/whereabouts/main.go](cmd/whereabouts/main.go)): Implements ADD/DEL/CHECK via `skel.PluginMainFuncs`. ADD allocates the lowest available IP; DEL releases it.
- **Allocation engine** (`pkg/allocate/`): `AssignIP` / `IterateForAssignment` find the lowest free IP, skipping `.0` addresses and exclude ranges. Idempotent — existing `podRef+ifName` allocations are returned as-is.
- **Storage layer** (`pkg/storage/`): `Store` and `IPPool` interfaces in `storage.go`; sole production implementation in `pkg/storage/kubernetes/` using IPPool CRDs with JSON Patch + optimistic locking (up to 100 retries).
- **Config** (`pkg/config/`): Merges inline IPAM JSON → flat file (`whereabouts.conf`) → defaults using `mergo.Merge`. JSON tags are **snake_case** (`range_start`, `enable_overlapping_ranges`).
- **Operator** (`cmd/operator/`): Cobra-based entry point with `controller` subcommand. Built on controller-runtime v0.23. Runs reconcilers (leader-elected) and webhook server (all replicas) from a single Deployment.
- **Reconcilers** (`internal/controller/`): `IPPoolReconciler` (orphaned allocation cleanup), `NodeSliceReconciler` (NAD+Node → NodeSlicePool), `OverlappingRangeReconciler` (orphaned reservation cleanup).
- **Webhooks** (`internal/webhook/`): Typed `admission.Validator[T]` implementations for IPPool, NodeSlicePool, OverlappingRangeIPReservation with matchConditions CEL bypass for CNI ServiceAccount.

## Build & Test Commands

```bash
make build
make docker-build                     # Container image
make test                             # build + install tools + go vet + staticcheck + tests
make test-skip-static                 # Skip staticcheck (faster iteration)
go test -v ./pkg/allocate/             # Single package
make kind                             # Local kind cluster with whereabouts installed
make kind COMPUTE_NODES=3             # Custom worker count
```

## Code Conventions

### Error Handling
- Wrap with `fmt.Errorf("context: %w", err)` — use `%w` for proper error wrapping everywhere
- Use `logging.Errorf("msg: %v", err)` to both log AND return an error in one call (note: `logging.Errorf` internally calls `fmt.Errorf` for the returned error and replaces `%w` with `%v` for the log line, so `%w` also works but `%v` is clearer)
- When discarding the returned error: `_ = logging.Errorf(...)`
- Custom error types use struct + `Error() string` + constructor: `NewInvalidPluginError()`
- Retryable errors implement `Temporary() bool` interface (checked via type assertion in retry loops)

### Logging
- Use `logging.Debugf` / `logging.Verbosef` / `logging.Errorf` from `pkg/logging/` — no third-party loggers
- `logging.Errorf` returns `error` — it's dual-purpose (log + return)

### Testing
- **Ginkgo v2** + Gomega with dot-imports: `. "github.com/onsi/ginkgo/v2"`, `. "github.com/onsi/gomega"`
- Suite bootstrap: `RegisterFailHandler(Fail); RunSpecs(t, "Suite Name")`
- K8s fakes: `fake.NewClientset(...)` from `client-go/kubernetes/fake` and generated `versioned/fake`
- controller-runtime `envtest` used for reconciler and webhook tests
- Some tests use standard `testing.T` table-driven style (e.g., `TestIPPoolName` in `pkg/storage/kubernetes/`)
- Test entity helpers live alongside production code with `//go:build test` tag

### CRD Types (`api/whereabouts.cni.cncf.io/v1alpha1/`)
- Standard kubebuilder markers: `+genclient`, `+kubebuilder:object:root=true`
- Auto-generated code in `pkg/generated/` — regenerate with `hack/update-codegen.sh`, verify with `hack/verify-codegen.sh`
- Never edit `zz_generated.deepcopy.go` manually

### Package Import Aliases
- `whereaboutstypes` for `pkg/types`
- `whereaboutsv1alpha1` for CRD v1alpha1 types

## Key Design Decisions

1. **Lowest-available-IP allocation**: Always assigns the lowest free IP in range — deterministic, not random
2. **Optimistic concurrency**: IPPool updates use K8s resource version checks with retry (100 attempts, exponential backoff)
3. **Overlapping range protection**: `OverlappingRangeIPReservation` CRDs prevent duplicate IPs across ranges (enabled by default)
4. **Idempotent ADD**: Re-running CNI ADD for the same pod+interface returns the existing allocation
5. **Three binaries**: `whereabouts` (CNI plugin), `whereabouts-operator` (reconcilers + webhooks via single `controller` subcommand), `install-cni` (DaemonSet entry-point: copies CNI binary, generates kubeconfig/conf, watches token rotation)

## IPAM Features

- **L3/Routed mode** (`enable_l3`): All IPs allocatable including network/broadcast; for BGP-routed, /31, /32
- **Gateway exclusion** (`exclude_gateway`): Auto-exclude gateway IP from allocation (opt-in)
- **Optimistic IPAM** (`optimistic_ipam`): Bypass leader election for faster allocation at scale
- **Preferred/Sticky IP**: Pod annotation `whereabouts.cni.cncf.io/preferred-ip`; falls back to lowest-available
- **Small subnets**: /32, /31, /127, /128 supported
- **Dual-stack**: `ipRanges` array with IPv4 + IPv6 CIDRs
- **Named networks** (`network_name`): Separate IPPool CRs per logical network
- **Fast IPAM** (`node_slice_size`): Per-node IP slices to reduce contention at scale

## Operator Feature Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--cleanup-terminating-pods` | `false` | Release IPs from pods with `DeletionTimestamp` set |
| `--cleanup-disrupted-pods` | `true` | Release IPs from pods with `DisruptionTarget` condition |
| `--verify-network-status` | `true` | Check Multus network-status annotation for IP presence |
| `--reconcile-interval` | `30s` | How often to re-check IP pools for orphaned allocations |
