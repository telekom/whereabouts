# CI/CD & Build Process Review — Whereabouts

**Reviewer:** CI/CD and DevOps Expert  
**Date:** 2026-03-02  
**Scope:** Makefile, `hack/` scripts, Dockerfile, `.github/workflows/`, Helm chart  

---

## Verification of Known Issues

### P8-3: 9 near-identical image build jobs — CONFIRMED (HIGH)

Three workflow files duplicate the same Docker build logic:

| Workflow | Jobs | Trigger |
|---|---|---|
| `image-build.yml` | 1 (build + Trivy) | `pull_request` |
| `image-push-main.yml` | 2 (build-push × 2 platforms) + 1 assemble | `push` to `main` |
| `image-push-release.yml` | 1 test + 2 (build-push × 2) + 1 assemble + 1 sign | `push` tag `v*` |

Total: 7 job definitions (not 9 — the PR workflow has a single job, not per-platform) sharing nearly identical Docker build steps. The `build-push` jobs in `image-push-main.yml` and `image-push-release.yml` are copy-pasted with minor differences (labels, signing). This will diverge over time.

**Recommendation:** Extract a reusable workflow (`.github/workflows/reusable-image-build.yml`) with `workflow_call` parameterised by push mode, tag format, platforms, and sign toggle.

### P8-7: E2E depends on moving targets — CONFIRMED (MEDIUM)

**File:** [hack/e2e-setup-kind-cluster.sh](hack/e2e-setup-kind-cluster.sh#L18)

```bash
MULTUS_DAEMONSET_URL="https://raw.githubusercontent.com/k8snetworkplumbingwg/multus-cni/master/deployments/multus-daemonset.yml"
```

Additionally, [hack/e2e-get-test-tools.sh](hack/e2e-get-test-tools.sh#L21) fetches kubectl at whatever `stable.txt` resolves to:

```bash
curl -Lo "${root}/bin/kubectl" "https://storage.googleapis.com/kubernetes-release/release/$(curl -s ${K8_STABLE_RELEASE_URL})/bin/linux/amd64/kubectl"
```

Both are moving targets. A Multus breaking change on `master` or a kubectl stable bump can break CI without any code change in this repo.

**Recommendation:** Pin Multus manifest to a release tag URL (e.g., `v4.1.3`) and pin kubectl to a specific version constant.

### P8-10: Chart version not committed back — CONFIRMED (LOW)

**File:** [hack/release/chart-update.sh](hack/release/chart-update.sh)

The release workflow modifies `Chart.yaml` and `values.yaml` in-CI via `yq` but never commits the changes. The published OCI chart has the correct version, but the repository source perpetually shows stale values (`version: 0.1.1`, `appVersion: v0.8.0`).

**Recommendation:** Either commit + push the version bump from CI (requires `contents: write`), or generate the version at `helm package` time using `--version` / `--app-version` flags without modifying files.

---

## New Findings

### P8-11: Dockerfile drops version/git metadata — build is not reproducible (HIGH)

**Severity:** High  
**File:** [Dockerfile](Dockerfile#L7)

The Dockerfile builds with:

```dockerfile
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/whereabouts ./cmd/
```

This does **not** inject the `-X` ldflags that [hack/build-go.sh](hack/build-go.sh#L42-L47) uses (`Version`, `GitSHA`, `GitTreeState`, `ReleaseStatus`). Every container image binary will report `UNKNOWN` for its version.

Additionally, `-s -w` strips the symbol table and DWARF info (good for size) but the local `build-go.sh` does not, creating a behavioral difference between `make docker-build` and `./hack/build-go.sh`.

**Recommendation:** Pass build args for version metadata into the Dockerfile and use them in ldflags:

```dockerfile
ARG VERSION=unknown
ARG GIT_SHA=unknown
ARG GIT_TREE_STATE=unknown
ARG RELEASE_STATUS=unreleased
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags="-s -w \
      -X github.com/telekom/whereabouts/pkg/version.Version=${VERSION} \
      -X github.com/telekom/whereabouts/pkg/version.GitSHA=${GIT_SHA} \
      -X github.com/telekom/whereabouts/pkg/version.GitTreeState=${GIT_TREE_STATE} \
      -X github.com/telekom/whereabouts/pkg/version.ReleaseStatus=${RELEASE_STATUS}" \
    -o bin/whereabouts ./cmd/
```

### P8-12: No race detection in CI test pipeline (MEDIUM)

**Severity:** Medium  
**File:** [hack/test-go.sh](hack/test-go.sh#L35-L40)

The test script runs with `-covermode=count` but never uses `-race`. The code has complex concurrent access patterns (IPPool retry loops, overlapping range stores) that could harbor data races only detectable by Go's race detector.

Note: `-race` is incompatible with `CGO_ENABLED=0`, so it cannot coexist with the current build. But tests should run with CGO enabled + race detector.

**Recommendation:** Add a `-race` flag to the test invocation (which implicitly sets `CGO_ENABLED=1` and `-covermode=atomic`):

```bash
${GO} test -race -v -covermode=atomic -coverprofile=coverage.out ...
```

### P8-13: Release workflow re-runs full test suite redundantly (MEDIUM)

**Severity:** Medium  
**Files:** [image-push-release.yml](image-push-release.yml#L19-L38), [chart-push-release.yml](chart-push-release.yml#L15-L33), [binaries-upload-release.yml](binaries-upload-release.yml)

All three release workflows (`image-push-release`, `chart-push-release`, `binaries-upload-release`) are triggered by tag push events. The first two each independently run a full `test` job (checkout → install Go → build → test) before proceeding. That's **2 redundant test runs** on every release, plus the binary upload runs a build per architecture.

The tag is cut from `main`, which has already passed all CI checks. Re-testing the same commit wastes ~10 minutes of CI time per release and creates a risk of flaky-test-blocks-release.

**Recommendation:** Either gate release workflows on the test workflow via `workflow_run`, or consolidate into a single release workflow that tests once and then fans out to image push, chart push, and binary upload in parallel.

### P8-14: `binaries-upload-release` only uploads `whereabouts`, not `whereabouts-operator` (LOW)

**Severity:** Low  
**File:** [.github/workflows/binaries-upload-release.yml](binaries-upload-release.yml#L23-L26)

```yaml
run: |
  ./hack/build-go.sh
  mv ./bin/whereabouts ./bin/whereabouts-${{ matrix.arch }}
```

`build-go.sh` produces **two** binaries (`whereabouts` and `whereabouts-operator`), but only `whereabouts` is renamed/uploaded. The operator binary is silently discarded.

**Recommendation:** Upload both binaries (and their checksums) to the release.

### P8-15: Helm chart DaemonSet references stale binary `/ip-control-loop` (HIGH)

**Severity:** High  
**File:** [deployment/whereabouts-chart/templates/daemonset.yaml](deployment/whereabouts-chart/templates/daemonset.yaml#L49-L52)

```yaml
args:
  - -c
  - |
    SLEEP=false source /install-cni.sh
    /token-watcher.sh &
    /ip-control-loop -log-level debug
```

The codebase has been refactored to produce a single `whereabouts-operator` binary with `controller` and `webhook` subcommands. There is no `/ip-control-loop` binary in the Dockerfile — only `/whereabouts` and `/whereabouts-operator` are copied. Deploying this chart will fail with `exec: "/ip-control-loop": not found`.

**Recommendation:** Update the command to use the new operator binary:

```yaml
/whereabouts-operator controller
```

### P8-16: Helm chart node-slice-controller references stale binary `/node-slice-controller` (HIGH)

**Severity:** High  
**File:** [deployment/whereabouts-chart/templates/node-slice-controller.yaml](deployment/whereabouts-chart/templates/node-slice-controller.yaml#L24-L25)

```yaml
containers:
  - command:
      - /node-slice-controller
```

Same issue as P8-15 — the Dockerfile only produces `/whereabouts-operator`. The `node-slice-controller` entry point no longer exists as a separate binary.

**Recommendation:** Change to:

```yaml
containers:
  - command:
      - /whereabouts-operator
      - controller
```

(The old `node-slice-controller` functionality is now part of the unified operator `controller` subcommand.)

### P8-17: Node-slice-controller Deployment ignores `values.yaml` volume paths (LOW)

**Severity:** Low  
**File:** [deployment/whereabouts-chart/templates/node-slice-controller.yaml](deployment/whereabouts-chart/templates/node-slice-controller.yaml#L56-L65)

The node-slice-controller template hardcodes volume paths:

```yaml
volumes:
  - hostPath:
      path: /opt/cni/bin     # Ignores .Values.cniConf.binDir
  - hostPath:
      path: /etc/cni/net.d   # Ignores .Values.cniConf.confDir
```

While the DaemonSet template correctly uses `{{ .Values.cniConf.binDir }}` and `{{ .Values.cniConf.confDir }}`, the Deployment template hardcodes the paths, meaning custom `cniConf` overrides only apply to one of the two workloads.

**Recommendation:** Use `{{ .Values.cniConf.binDir }}` and `{{ .Values.cniConf.confDir }}` consistently.

### P8-18: `verify-codegen.sh` compares generated code backwards (MEDIUM)

**Severity:** Medium  
**File:** [hack/verify-codegen.sh](hack/verify-codegen.sh#L22-L25)

```bash
cp -a "${DIFFROOT}"/* "${TMP_DIFFROOT}"   # Copy current pkg/ to tmp
"${SCRIPT_ROOT}/hack/update-codegen.sh"   # Regenerate in-place in pkg/
diff -Naupr "${DIFFROOT}" "${TMP_DIFFROOT}" || ret=$?  # Compare
cp -a "${TMP_DIFFROOT}"/* "${DIFFROOT}"   # Restore original
```

The logic saves the current state to tmp, regenerates into the working tree, diffs, then restores. But the diff direction is inverted: it compares `DIFFROOT` (freshly generated) against `TMP_DIFFROOT` (original). If there's a difference, the `-` lines are the *new* generated code and `+` lines are the *old* committed code — confusing for developers reading the error output.

More critically, if the script is interrupted between `update-codegen.sh` and the final `cp -a` restore, the working tree's `pkg/` is left in the regenerated state — a dirty working tree for the developer.

**Recommendation:** Reverse the diff or, better, regenerate into the `TMP_DIFFROOT` instead of the working tree:

```bash
cp -a "${DIFFROOT}"/* "${TMP_DIFFROOT}"
SCRIPT_ROOT="${TMP_DIFFROOT}/.." "${SCRIPT_ROOT}/hack/update-codegen.sh"
diff -Naupr "${TMP_DIFFROOT}" "${DIFFROOT}"
```

### P8-19: `Makefile` `generate-api` target calls `verify-codegen.sh` instead of `update-codegen.sh` (MEDIUM)

**Severity:** Medium  
**File:** [Makefile](Makefile#L19-L20)

```makefile
generate-api:
	hack/verify-codegen.sh
	rm -rf github.com
```

The comment and target name suggest it should *generate* code, but it calls `verify-codegen.sh` (which regenerates, diffs, and restores). This means:
1. If the codegen is already up-to-date, it does nothing — correct but misleading.
2. If the codegen is stale, it fails with an error instead of generating.
3. The `rm -rf github.com` cleanup is for artifacts from `generate-code.sh` (controller-gen), not `update-codegen.sh`.

It should call `hack/update-codegen.sh` (for client-gen) and `hack/generate-code.sh` (for controller-gen).

**Recommendation:**

```makefile
generate-api:
	hack/generate-code.sh
	hack/update-codegen.sh
	rm -rf github.com
```

### P8-20: DaemonSet runs as `privileged: true` without justification (MEDIUM)

**Severity:** Medium  
**File:** [deployment/whereabouts-chart/values.yaml](deployment/whereabouts-chart/values.yaml#L32-L33)

```yaml
securityContext:
  privileged: true
```

The whereabouts DaemonSet only needs to copy the CNI binary and config file to host paths (via `hostPath` volumes). Full `privileged` mode grants far more than necessary (all Linux capabilities, device access, host PID namespace visibility, etc.).

**Recommendation:** Replace with the minimum required capabilities:

```yaml
securityContext:
  privileged: false
  capabilities:
    drop: ["ALL"]
  readOnlyRootFilesystem: true
  runAsNonRoot: true
```

Host path writes can be achieved through volume mount permissions without full privileged mode.

### P8-21: `e2e-get-test-tools.sh` only downloads `amd64` binaries (LOW)

**Severity:** Low  
**File:** [hack/e2e-get-test-tools.sh](hack/e2e-get-test-tools.sh#L8-L21)

```bash
KIND_BINARY_URL="https://github.com/kubernetes-sigs/kind/releases/download/${VERSION}/kind-$(uname)-amd64"
```

Both `kind` and `kubectl` URLs are hardcoded to `amd64`. This will fail on ARM-based CI runners (GitHub's `ubuntu-latest-arm64` or self-hosted ARM runners).

**Recommendation:** Detect architecture dynamically:

```bash
ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
```

### P8-22: `test-go.sh` runs `sudo` in CI — tests require root unnecessarily (LOW)

**Severity:** Low  
**File:** [.github/workflows/test.yml](test.yml#L53)

```yaml
- name: Test
  run: sudo PATH=${PATH}:./bin ./hack/test-go.sh
```

Unit tests should not require root. If `sudo` is needed because of path permissions for `KUBEBUILDER_ASSETS`, the proper fix is to set correct permissions on the binaries, not escalate the entire test run.

**Recommendation:** Drop `sudo` or isolate the specific operation that needs elevated privileges.

### P8-23: `install-kubebuilder-tools.sh` only installs `controller-gen`, name is misleading (LOW)

**Severity:** Low  
**File:** [hack/install-kubebuilder-tools.sh](hack/install-kubebuilder-tools.sh)

```bash
#!/bin/bash
BASEDIR=$(pwd)
GOBIN=${BASEDIR}/bin go install sigs.k8s.io/controller-tools/cmd/controller-gen@v0.20.0
```

The script is called "install-kubebuilder-tools" but only installs `controller-gen`. Kubebuilder tools typically include `kube-apiserver`, `etcd`, and `kubectl` binaries for `envtest`. In the CI workflow `build.yml`, this is called before `generate-code.sh` — which uses `controller-gen`. The name is misleading; actual envtest binaries are needed for webhook/reconciler tests but aren't installed here.

**Recommendation:** Rename to `install-controller-gen.sh` or expand to actually install envtest binaries.

### P8-24: Trivy install via curl-pipe-sh is unpinned (LOW)

**Severity:** Low  
**Files:** [.github/workflows/image-build.yml](image-build.yml#L27-L28), [.github/workflows/security-scan.yml](security-scan.yml#L42-L43)

```yaml
- name: Install Trivy
  run: |
    curl -sfL https://raw.githubusercontent.com/aquasecurity/trivy/main/contrib/install.sh | sh -s -- -b /usr/local/bin
```

The Trivy install script is fetched from `main` — a moving target. A breaking change in the installer or a supply chain attack would affect CI. All other actions are correctly pinned to SHA.

**Recommendation:** Use the official `aquasecurity/trivy-action` GitHub Action (pinned to a SHA), or pin the Trivy version in the curl command:

```bash
curl -sfL https://raw.githubusercontent.com/aquasecurity/trivy/main/contrib/install.sh | sh -s -- -b /usr/local/bin v0.58.0
```

### P8-25: `license-audit` job swallows failures with `|| true` (LOW)

**Severity:** Low  
**File:** [.github/workflows/security-scan.yml](security-scan.yml#L100-L104)

```yaml
- name: Check licenses
  run: go-licenses check ./... --disallowed_types=forbidden,restricted 2>&1 || true
- name: Generate license report
  run: go-licenses report ./... 2>/dev/null > licenses.csv || true
```

Both license steps suppress all errors. A dependency with a forbidden license will pass CI silently. The check step is security-relevant but toothless.

**Recommendation:** Remove `|| true` from the check step (keep it for the report step if desired). If known false positives exist, configure an allow-list instead.

---

## Summary

| ID | Severity | Area | Summary |
|---|---|---|---|
| P8-3 | HIGH | CI Workflows | Near-identical image build/push jobs across 3 workflow files |
| P8-7 | MEDIUM | E2E | Multus manifest and kubectl fetched from unpinned moving targets |
| P8-10 | LOW | Helm/Release | Chart version bumped in CI but never committed to repo |
| P8-11 | HIGH | Dockerfile | Version/git metadata not injected — binary reports `UNKNOWN` |
| P8-12 | MEDIUM | Testing | No `-race` flag — data races in allocation code go undetected |
| P8-13 | MEDIUM | CI Workflows | 3 release workflows each re-run full test suite redundantly |
| P8-14 | LOW | Release | `binaries-upload-release` omits `whereabouts-operator` binary |
| P8-15 | HIGH | Helm Chart | DaemonSet references non-existent `/ip-control-loop` binary |
| P8-16 | HIGH | Helm Chart | Deployment references non-existent `/node-slice-controller` binary |
| P8-17 | LOW | Helm Chart | Node-slice-controller hardcodes volume paths, ignores values |
| P8-18 | MEDIUM | Codegen | `verify-codegen.sh` diffs in confusing direction, can leave dirty tree |
| P8-19 | MEDIUM | Makefile | `generate-api` target calls verify instead of generate |
| P8-20 | MEDIUM | Helm Chart | Full `privileged: true` without justification |
| P8-21 | LOW | E2E | Test tools hardcoded to `amd64` — won't work on ARM runners |
| P8-22 | LOW | CI Testing | Unit tests run under `sudo` unnecessarily |
| P8-23 | LOW | Build Scripts | `install-kubebuilder-tools.sh` only installs `controller-gen`, misleading name |
| P8-24 | LOW | CI Security | Trivy installed via unpinned curl-pipe-sh from `main` branch |
| P8-25 | LOW | CI Security | License audit job swallows all errors silently |
