# AGENTS.md - Whereabouts Development Quick Reference

See **CLAUDE.md** for comprehensive architecture, deallocation logic, and storage flow.

## OVERVIEW
Cluster-wide IPAM CNI plugin and operator using Kubernetes CRDs for state.

## STRUCTURE
```
whereabouts/
├── api/                    # CRD API definitions (v1alpha1)
├── cmd/
│   ├── whereabouts/        # CNI plugin binary
│   ├── operator/           # controller-runtime operator
│   └── install-cni/        # DaemonSet installer
├── config/                 # Kustomize manifests (CRD, RBAC, Webhook)
├── deployment/
│   └── whereabouts-chart/  # Helm chart (includes CRDs)
├── e2e/                    # Ginkgo e2e tests (requires Kind)
├── hack/                   # Codegen, setup, and release scripts
├── internal/
│   ├── controller/         # IPPool, NodeSlice, OverlappingRange reconcilers
│   └── webhook/            # Validating admission webhooks
├── pkg/
│   ├── allocate/           # IP allocation algorithms
│   ├── generated/          # clientset, informers, listers, applyconfig
│   ├── storage/            # IP pool store abstractions
│   └── version/            # Version info (injected via LDFLAGS)
└── vendor/                 # Committed dependencies
```

## WHERE TO LOOK
- **CRD Logic**: `api/whereabouts.cni.cncf.io/v1alpha1/`
- **Reconcilers**: `internal/controller/`
- **Admission**: `internal/webhook/` (uses `admission.Validator[T]`)
- **IP Math**: `pkg/iphelpers/` and `pkg/allocate/`
- **CNI Entry**: `cmd/whereabouts/main.go`
- **Generated**: `pkg/generated/` (clients, listers, etc.)

## CONVENTIONS
- **Go**: controller-runtime v0.23, Ginkgo v2, Cobra CLI.
- **Generated Code**: Always run `make generate-api` after editing `*_types.go`.
- **SPDX**: DT AGENT requires `SPDX-FileCopyrightText: 2026 Deutsche Telekom AG` in new files.
- **Lint**: Strictly enforced via `make lint`. No dot imports in production code.

## ANTI-PATTERNS
- **DO NOT** use `go mod` directly — use `make update-deps` (manages `vendor/`).
- **DO NOT** use `fmt.Printf` for logging — use `pkg/logging`.

## COMMANDS
- `make build`: Build `whereabouts`, `whereabouts-operator`, `install-cni`.
- `make generate-api`: Full codegen (manifests + deepcopy + clientsets).
- `make test`: Unit tests + build + vet + staticcheck.
- `make lint`: Run golangci-lint (10m timeout).
- `make update-deps`: `go mod tidy` + `vendor` + `verify`.
- `make kind`: Spin up local dev cluster with Whereabouts.
- `./hack/update-codegen.sh`: Refresh `pkg/generated/`.

## NOTES
- **Dual Runtime**: CNI binary (fast path) vs Operator (async cleanup/Fast IPAM).
- **Fast IPAM**: Experimental; uses `NodeSlicePool` CRDs.
- **Committed Vendor**: All dependencies are vendored; PRs must include vendor updates.
- **LDFLAGS**: Versioning is injected at build time (see `Makefile`).
