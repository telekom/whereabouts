# Architecture

This document describes the high-level architecture of the Whereabouts IPAM
CNI plugin.

## Binaries

Whereabouts ships two binaries:

| Binary | Role |
|--------|------|
| `whereabouts` | CNI plugin binary, called by the container runtime (via Multus) on pod create/delete |
| `whereabouts-operator` | Operator binary with `controller` and `webhook` subcommands |

## CNI Plugin (`cmd/whereabouts.go`)

Implements the standard CNI interface:

* **ADD** — allocates the lowest available IP from the configured range(s),
  persists the reservation in an IPPool CRD, and returns it to the runtime.
  Idempotent: re-running ADD for the same pod+interface returns the existing
  allocation.
* **DEL** — releases the IP reservation for the given container ID + interface.
* **CHECK** — verifies that the previously allocated IP is still present on the
  interface (CNI spec compliance).

## Operator (`cmd/operator/`)

Built on [controller-runtime](https://github.com/kubernetes-sigs/controller-runtime)
with Cobra subcommands:

* `controller` — runs reconcilers as a leader-elected Deployment.
* `webhook` — runs the validating webhook server with automatic TLS rotation
  (via `cert-controller`).

### Reconcilers (`internal/controller/`)

| Reconciler | Watches | Purpose |
|-----------|---------|---------|
| `IPPoolReconciler` | IPPool CRDs | Removes orphaned allocations by checking podRef against live pods |
| `NodeSliceReconciler` | NetworkAttachmentDefinitions + Nodes | Manages NodeSlicePool CRDs for Fast IPAM |
| `OverlappingRangeReconciler` | OverlappingRangeIPReservation CRDs | Deletes orphaned reservations |

### Webhooks (`internal/webhook/`)

Typed `admission.Validator[T]` implementations:

| Validator | Validates |
|-----------|-----------|
| `IPPoolValidator` | Range CIDR format, podRef `"namespace/name"` format |
| `NodeSlicePoolValidator` | Range CIDR, SliceSize integer 1–128 |
| `OverlappingRangeValidator` | podRef `"namespace/name"` format |

Webhook manifests include `matchConditions` CEL expressions to bypass
validation for the CNI plugin's own ServiceAccount.

## IP Allocation Flow

```
Pod Create → kubelet → Multus → whereabouts CNI ADD
                                      │
                                      ├─ Parse IPAM config (inline + flat-file merge)
                                      ├─ Acquire leader election lease
                                      ├─ Get/create IPPool CRD for range
                                      ├─ Check for existing allocation (idempotent)
                                      ├─ Find lowest available IP (IterateForAssignment)
                                      ├─ Update IPPool CRD (with retries, up to 100)
                                      ├─ Create OverlappingRangeIPReservation (if enabled)
                                      └─ Return IP to runtime
```

## Storage Layer (`pkg/storage/`)

* `Store` and `IPPool` interfaces defined in `storage.go`.
* Sole production implementation: `pkg/storage/kubernetes/` using IPPool CRDs
  with JSON Patch + optimistic locking (resource version checks with retry).

### Custom Resource Definitions

| CRD | Purpose |
|-----|---------|
| `IPPool` | Stores IP allocations per range. Key format: `<namespace>-<network-name>-<normalized-range>` |
| `OverlappingRangeIPReservation` | Ensures IP uniqueness across overlapping ranges (enabled by default) |
| `NodeSlicePool` | Tracks per-node IP slice allocations for Fast IPAM (experimental) |

## Key Packages

| Package | Responsibility |
|---------|---------------|
| `pkg/allocate` | IP assignment algorithm — finds lowest available IP |
| `pkg/config` | Merges inline IPAM JSON → flat file → defaults |
| `pkg/iphelpers` | IP arithmetic: offsets, ranges, CIDR splitting |
| `pkg/storage/kubernetes` | CRD-based storage with retry logic |
| `pkg/types` | Core data structures (IPAMConfig, IPReservation) |
| `pkg/logging` | Structured logging with configurable levels |

## Configuration Hierarchy

Whereabouts merges configuration from multiple sources (high → low priority):

1. Inline IPAM config in the NetworkAttachmentDefinition
2. CNI config file parameters
3. Flat file at `configuration_path` or default locations

See [extended-configuration.md](extended-configuration.md) for the full
parameter reference.
