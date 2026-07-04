# Proposal: Migrate Whereabouts to controller-runtime with SSA

## Status

Implemented in the Deutsche Telekom fork. The current implementation uses
controller-runtime v0.24.1, Kubernetes libraries v0.36.2, and a single
`whereabouts-operator controller` command that runs leader-elected reconcilers
and serves validating webhooks from the same Deployment.

## TL;DR

The hand-rolled `ip-control-loop` and `node-slice-controller` have been
replaced by the controller-runtime based `whereabouts-operator` binary. The
DaemonSet now installs the CNI binary only, while the operator Deployment owns
IPPool cleanup, NodeSlicePool management, overlapping-range cleanup, validating
webhooks, health and readiness probes, metrics, leader election, and webhook
certificate rotation. Validating webhooks exist for all three CRDs and use CEL
`matchConditions` to bypass validation for the CNI and operator service
accounts. Tests use Ginkgo v2.

## Architecture

| Component | Before | After |
|-----------|--------|-------|
| CNI binary | `/whereabouts` on host | Unchanged |
| Pod cleanup + reconciler | `/ip-control-loop` in DaemonSet | `/whereabouts-operator controller` in Deployment |
| Fast IPAM controller | `/node-slice-controller` in Deployment | Merged into `/whereabouts-operator controller` |
| Webhook validation | None | Served by `/whereabouts-operator controller` in the operator Deployment |
| DaemonSet | `install-cni.sh` + `token-watcher.sh` + `ip-control-loop` | `/install-cni` only |
| Cert management | N/A | `cert-controller/pkg/rotator` (self-signed, auto-rotation) |

## Review.md Issues Resolved

| Finding | How |
|---------|-----|
| **P2-1** 10s `requestCtx` defeats 100-retry loop | Per-retry `context.WithTimeout` inside RETRYLOOP |
| **P2-2** `nil` leader elector panic | Nil guard before goroutine |
| **P2-3** Shared `err` across loop iterations | Scope with `:=` per ipRange |
| **P2-4** `skipOverlappingRangeUpdate` not reset | Reset at top of each ipRange |
| **P5-2** No health/readiness/liveness probes | controller-runtime Manager `/healthz` `/readyz` |
| **P5-3** No metrics or observability | controller-runtime Prometheus metrics at `/metrics` |
| **P5-6** Reconciler runs without context/timeout | controller-runtime passes ctx to `Reconcile()` |
| **P6-2** Global logging state not thread-safe | `logr.Logger` — structured, per-reconciler |
| **P9-4** Node-slice-controller has no leader election | controller-runtime Manager handles it |
| **P10-2** `checkForMultiNadMismatch` O(n²) | Reduced to a single NAD list pass for shared NodeSlicePool validation |
| **P10-3** Reconciler loads ALL pods & pools | Controller-runtime cache and direct object lookups replace the legacy batch loop |
| **P11-1** Partial multi-range not rolled back | Compensating deallocations on failure |
| **P11-2** Corrupt annotation causes mass cleanup | Skip pod on parse error, don't treat as orphan |
| **P11-3** IPv6 normalization mismatch | `net.IP.Equal()` instead of string comparison |
| **P11-4** Reconciler snapshot stale by cleanup time | Event-driven reconciliation replaces batch snapshot |
| **P11-5** `close(errorChan)` race | Eliminated — controller-runtime lifecycle |

## Current Implementation

### Dependencies and Generated Clients

- `go.mod` uses Kubernetes libraries v0.36.2, controller-runtime v0.24.1,
  cert-controller v0.16.0, Cobra v1.10.2, and Ginkgo v2.
- ApplyConfiguration types are generated under `pkg/generated/applyconfiguration/`.
- `cmd/operator/main.go` registers Kubernetes core types, Whereabouts CRDs, and
  NetworkAttachmentDefinition types into the controller-runtime scheme.

### Operator Entry Point

`cmd/operator/main.go` creates the `whereabouts-operator` Cobra root command
with a `controller` subcommand. There is no separate `webhook` subcommand in the
current implementation. `cmd/operator/controller.go` starts a controller-runtime
manager with:

- leader election enabled by default,
- Prometheus metrics on `:8080`,
- health and readiness probes on `:8081`,
- a webhook server on `:9443`,
- cert-controller based TLS rotation,
- `--reconcile-interval`, `--metrics-bind-address`,
  `--health-probe-bind-address`, `--leader-elect-namespace`,
  `--webhook-port`, `--cert-dir`, `--namespace`, `--service-cidr`, and cleanup
  behavior flags.

### Reconcilers

`internal/controller/setup.go` registers three reconcilers with the manager:

- `internal/controller/ippool_controller.go` removes orphaned IPPool
  allocations, updates status, emits Kubernetes events, checks service-CIDR
  overlap, and uses JSON Patch for allocation cleanup because the CNI binary
  also writes the allocation map. It watches IPPool resources and uses periodic
  requeue plus direct pod and reservation lookups for cleanup.
- `internal/controller/nodeslice_controller.go` watches
  NetworkAttachmentDefinitions and Nodes, manages NodeSlicePool CRDs, repairs
  stale or inconsistent node-slice allocations, sets owner references, records
  metrics, and deletes stale node-slice IPPools when nodes leave.
- `internal/controller/overlappingrange_controller.go` deletes
  OverlappingRangeIPReservation CRDs whose referenced pods no longer exist.

### Webhooks and Certificates

`internal/webhook` registers validating webhooks for IPPool, NodeSlicePool, and
OverlappingRangeIPReservation. `internal/webhook/certrotator` wraps
cert-controller for self-signed webhook certificate rotation.

The generated webhook manifests default to `failurePolicy: Ignore`. Kustomize
and Helm add CEL `matchConditions` that bypass validation for the rendered CNI
and operator service accounts so CNI operations and cert rotation are not
blocked by the webhooks.

### Manifests and Helm Chart

- `config/daemonset/daemonset.yaml` runs only `/install-cni`.
- `config/manager/manager.yaml` deploys two operator replicas; all replicas
  serve webhooks, while leader election gates reconcilers.
- `config/webhook` contains generated webhook manifests plus Kustomize patches
  for CA injection and service-account bypass.
- The Helm chart installs the DaemonSet, operator, webhook Service, webhook
  configuration, RBAC, and CRDs. Webhook `failurePolicy` is configurable and
  defaults to `Ignore`.

### Removed Legacy Code

The legacy command/package trees `cmd/controlloop`, `cmd/nodeslicecontroller`,
`pkg/controlloop`, `pkg/node-controller`, and `pkg/reconciler` are no longer
present. The CNI binary still uses `pkg/generated/`.

## Verification

1. `go build ./cmd/...` — all binaries compile
2. `go test ./internal/... ./pkg/... -v -count=1` — all tests pass
3. `make test` — full suite passes
4. `make kind && cd e2e && go test -v . -timeout=1h`
5. Operator probes, metrics, leader election, orphan cleanup, webhook validation, CNI/operator bypass, cert rotation

## Key Decisions

- **Cobra subcommand** (`controller`): one manager serves reconcilers and webhooks
- **cert-controller**: Self-signed, auto-rotating, no cert-manager dependency
- **`matchConditions` CEL bypass**: bypasses the CNI and operator service accounts
- **DaemonSet slim-down**: CNI install only
- **One operator Deployment**: leader-elected reconcilers plus Service-backed webhooks
- **Webhooks fail open by default**: generated and Helm manifests default to `failurePolicy: Ignore`
- **`internal/` layout**: kubebuilder convention, private to operator binary
- **Controller-runtime cache**: reconcilers use cached reads, direct object
  lookups, and periodic requeue instead of the legacy batch snapshot loop
- **SSA for NodeSlicePool, JSON Patch for IPPool**: SSA unsafe for shared allocations map
- **RBAC split**: DaemonSet SA minimal, operator SA comprehensive
- **`RequeueAfter` default 30s**: Replaces gocron, fast enough for e2e tests
