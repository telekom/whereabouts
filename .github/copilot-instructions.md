# Copilot Instructions — Whereabouts (Deutsche Telekom T-CAAS Fork)

This is a fork of [k8snetworkplumbingwg/whereabouts](https://github.com/k8snetworkplumbingwg/whereabouts), maintained by the Deutsche Telekom T-CAAS team. It is a Kubernetes IPAM CNI plugin that assigns IP addresses cluster-wide using CRDs.

## Architecture Overview

- **CNI entry point** ([cmd/whereabouts.go](cmd/whereabouts.go)): Implements ADD/DEL/CHECK via `skel.PluginMainFuncs`. ADD allocates the lowest available IP; DEL releases it.
- **Allocation engine** (`pkg/allocate/`): `AssignIP` / `IterateForAssignment` find the lowest free IP, skipping `.0` addresses and exclude ranges. Idempotent — existing `podRef+ifName` allocations are returned as-is.
- **Storage layer** (`pkg/storage/`): `Store` and `IPPool` interfaces in `storage.go`; sole production implementation in `pkg/storage/kubernetes/` using IPPool CRDs with JSON Patch + optimistic locking (up to 100 retries).
- **Config** (`pkg/config/`): Merges inline IPAM JSON → flat file (`whereabouts.conf`) → defaults using `mergo.Merge`. JSON tags are **snake_case** (`range_start`, `enable_overlapping_ranges`).
- **Reconciler** (`pkg/reconciler/`): CronJob-driven cleanup of orphaned IP allocations by comparing IPPool entries against live pods.
- **Controllers** (`cmd/controlloop/`, `cmd/nodeslicecontroller/`): Pod watcher for reconciliation; experimental Fast IPAM node-slice pre-allocation.

## Build & Test Commands

```bash
./hack/build-go.sh                    # CGO_ENABLED=0 static binaries → bin/
make docker-build                     # Container image
make test                             # build + install tools + go vet + staticcheck + tests
make test-skip-static                 # Skip staticcheck (faster iteration)
go test --tags=test -v ./pkg/allocate/ # Single package (--tags=test required!)
make kind                             # Local kind cluster with whereabouts installed
make kind COMPUTE_NODES=3             # Custom worker count
```

**Critical**: Tests MUST be run with `--tags=test` — some test helper files use `//go:build test` (e.g., `pkg/controlloop/entity_generators.go`). The `make test` target handles this automatically.

## Code Conventions

### Error Handling
- Wrap with `fmt.Errorf("context: %s", err)` — use `%s`, NOT `%w` (codebase convention)
- Use `logging.Errorf("msg: %v", err)` to both log AND return an error in one call
- When discarding the returned error: `_ = logging.Errorf(...)`
- Custom error types use struct + `Error() string` + constructor: `NewInvalidPluginError()`
- Retryable errors implement `Temporary() bool` interface (checked via type assertion in retry loops)

### Logging
- Use `logging.Debugf` / `logging.Verbosef` / `logging.Errorf` from `pkg/logging/` — no third-party loggers
- `logging.Errorf` returns `error` — it's dual-purpose (log + return)

### Testing
- **Ginkgo v1** + Gomega with dot-imports: `. "github.com/onsi/ginkgo"`, `. "github.com/onsi/gomega"`
- Suite bootstrap: `RegisterFailHandler(Fail); RunSpecs(t, "Suite Name")`
- K8s fakes: `fake.NewSimpleClientset(...)` from `client-go/kubernetes/fake` and generated `versioned/fake`
- Some tests use standard `testing.T` table-driven style (e.g., `TestIPPoolName` in `pkg/storage/kubernetes/`)
- Test entity helpers live alongside production code with `//go:build test` tag

### CRD Types (`pkg/api/whereabouts.cni.cncf.io/v1alpha1/`)
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
5. **Three binaries**: `whereabouts` (CNI plugin), `ip-control-loop` (reconciler), `node-slice-controller` (Fast IPAM)
