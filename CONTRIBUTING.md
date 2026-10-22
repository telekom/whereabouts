# Contributing to Whereabouts

Thank you for your interest in contributing to the Whereabouts IPAM CNI plugin!

## Getting Started

1. **Fork** the repository and clone your fork.
2. **Install Go** (see `go.mod` for the required version).
3. **Build** the binary:
   ```bash
   make build
   ```
4. **Run tests**:
   ```bash
   make test              # Full: build + vet + staticcheck + tests
   make test-skip-static  # Faster iteration (skip staticcheck)
   make test-envtest      # IPPool/NodeSlice API-server characterization (Kubernetes 1.37.0)
   go test -v ./pkg/allocate/  # Single package
   ```

## Development Workflow

### Local Kubernetes Cluster

Create a local [kind](https://kind.sigs.k8s.io/) cluster with Whereabouts installed:

```bash
make kind                 # Default: 2 worker nodes
make kind COMPUTE_NODES=3 # Custom worker count
```

### Code Generation

After modifying CRD types in `api/`:

```bash
make generate-api         # Regenerate deepcopy + manifests
./hack/update-codegen.sh  # Regenerate clientsets, informers, listers
./hack/verify-codegen.sh  # Verify generated code is up-to-date
```

### Dependencies

```bash
make update-deps  # go mod tidy && go mod vendor && go mod verify
```

Dependabot checks daily for direct and indirect Go modules (including the vendor refresh),
workflow actions, root Dockerfile bases, Kubernetes image references in `hack/`
and `yamls/`, and the chart in `deployment/whereabouts-chart/`.
The chart's own `ghcr.io/telekom/whereabouts` image is intentionally excluded:
its version belongs to the release process, as do `controller:latest`
deployment placeholders.

The kind suites use `hack/multus-install.yml`, not a remote quickstart with a
mutable snapshot image. Dependabot tracks both digest-pinned Multus images there.
Multus readiness checks the installed host CNI configuration, and setup waits for
the full DaemonSet rollout before creating networked test pods.

Dependabot does not update arbitrary tool versions, checksums, shell-script
image references, or package pins inside Dockerfile `RUN` instructions.
Maintain these together with their consumers and validate the relevant build
and integration jobs.

The OPA constraint dependency is a nested Go module without its own release tags.
If Dependabot proposes the parent repository's `v0.20.0` tag and reports
`dependency_file_not_resolvable`, resolve the nested module's actual latest
pseudo-version with `go list -m -json github.com/open-policy-agent/frameworks/constraint@latest`,
then update that version and regenerate vendoring. Do not substitute the parent
module's unrelated release or suppress all updates for this dependency.

When a dependency raises the Go requirement, update the Go patch version and
the Staticcheck and golangci-lint pins together. Regenerate the vendor directory
with that Go toolchain so vendored build constraints match CI.
Standalone Staticcheck follows golangci-lint's existing deprecation policy
(`SA1019` is excluded, including for upstream-generated Kubernetes clients).

### Advisory security reports

Vulnerability scanners and dependency review retain their findings but do not
block PRs. One security report comment per PR is updated with the current commit,
scanner warnings and links to complete logs, including the image scan.
Builds, tests, lint, vendor consistency, REUSE and license checks remain blocking.

The reporting workflow runs from the default branch with permission to comment,
so Dependabot and fork PRs do not need a write token. It reads GitHub job metadata
and annotations only; it never checks out or executes PR code with that token.
The reporter becomes active after its workflow is merged into the default branch.

## Code Conventions

### Error Handling

- Wrap errors with `fmt.Errorf("context: %w", err)` — use `%w` for proper error wrapping in new code (operator, webhooks, controllers).
- Legacy CNI plugin code (`pkg/`, `cmd/whereabouts/main.go`) still uses `%s` — migrate to `%w` opportunistically.
- Use `logging.Errorf("msg: %v", err)` to both log AND return an error in one call.
- When discarding the returned error: `_ = logging.Errorf(...)`.
- Custom error types use struct + `Error() string` + constructor (e.g. `NewInvalidPluginError()`).

### Logging

- Use `logging.Debugf` / `logging.Verbosef` / `logging.Errorf` from `pkg/logging/`.
- Do **not** introduce third-party loggers.

### Testing

- **Ginkgo v2** + Gomega with dot-imports:
  ```go
  . "github.com/onsi/ginkgo/v2"
  . "github.com/onsi/gomega"
  ```
- Suite bootstrap: `RegisterFailHandler(Fail); RunSpecs(t, "Suite Name")`
- Use `fake.NewClientset(...)` from `client-go/kubernetes/fake` for unit tests.
- controller-runtime `envtest` is used for reconciler and webhook integration tests.
- Never edit `zz_generated.deepcopy.go` manually.

### Import Aliases

| Alias                    | Package                                              |
|--------------------------|------------------------------------------------------|
| `whereaboutstypes`       | `pkg/types`                                          |
| `whereaboutsv1alpha1`    | `api/whereabouts.cni.cncf.io/v1alpha1` |

### JSON Tags

All configuration struct JSON tags use **snake_case** (e.g., `range_start`, `enable_overlapping_ranges`).

## Running E2E Tests

End-to-end tests verify the full CNI plugin lifecycle against a real Kubernetes cluster.

1. Install [godotenv](https://github.com/joho/godotenv): `go install github.com/joho/godotenv/cmd/godotenv@latest`
2. Create a kind cluster: `make kind` (or `make kind COMPUTE_NODES=3` for multi-node tests)
3. Create `e2e/.env` with: `KUBECONFIG=$HOME/.kube/config`
4. Run the tests:
   ```bash
   cd e2e && godotenv -f .env go test -v . -timeout=1h
   ```

For Fast IPAM (NodeSlice) e2e tests:
```bash
cd e2e/e2e_node_slice && godotenv -f ../../e2e/.env go test -v . -timeout=1h
```

## Pull Requests

- Keep PRs focused — one logical change per PR.
- Include tests for new functionality.
- Run `make test` before submitting.
- Update documentation if you change configuration or behavior.

## Reporting Issues

Open an issue on GitHub with:
- Whereabouts version / image tag
- Kubernetes version
- Network configuration (NAD spec, IPAM config)
- CNI log output (usually `/var/log/whereabouts.log`)

## Dependency updates

GitHub Dependabot is the only dependency updater. It checks Go modules, including
indirect dependencies, Actions, configured Docker/Kubernetes manifests and Helm
dependencies daily. No module ownership is handed to an external updater.

Dependabot does not update arbitrary Makefile tools, installer versions, workflow
inputs, envtest assets or images embedded in Go source. Review these pins manually
alongside supported manifest updates, keep duplicate references consistent and
preserve checksum verification. The untagged OPA constraint submodule may require
a manual Go-proxy pseudo-version update when Dependabot selects an unrelated
parent-module release; regenerate module and vendor artifacts together.
Release-owned self versions, protocol examples and compatibility fixtures are
not latest-version targets.

Issues are never closed for inactivity. The stale workflow was disabled while
this policy change is reviewed; after merging, maintainers may re-enable it for
labeling only. Reopened reports remain open until explicit triage confirms a
merged fix or another deliberate resolution.

## License

By contributing, you agree that your contributions will be licensed under the Apache License 2.0.
