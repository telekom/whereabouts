# P8: CI/CD & Build Review

**Date:** 2026-03-02  
**Reviewer Persona:** CI/CD Expert & Documentation Specialist

## Summary

| Severity | Count |
|----------|-------|
| CRITICAL | 2 |
| HIGH | 4 |
| MEDIUM | 6 |
| LOW | 4 |
| INFO | 3 |

---

## Findings

### 1. CRITICAL — `Makefile` `generate-api` target calls verify instead of update

**Files:** [Makefile](Makefile), [hack/verify-codegen.sh](hack/verify-codegen.sh)

`make generate-api` calls `hack/verify-codegen.sh` which fails if code is out of date instead of regenerating it. Developers expecting "generate" behavior get "verify" behavior.

### 2. CRITICAL — `binaries-upload-release.yml` does NOT upload the operator binary

**File:** [.github/workflows/binaries-upload-release.yml](.github/workflows/binaries-upload-release.yml#L22-L36)

`hack/build-go.sh` builds both `whereabouts` and `whereabouts-operator`, but the release workflow only renames and uploads `whereabouts`. Users downloading release binaries cannot run the operator.

### 3. HIGH — No `-race` flag in test runs

**File:** [hack/test-go.sh](hack/test-go.sh#L30-L35)

The race detector is the primary tool for finding data races. The codebase has significant concurrency. Note: `covermode=count` would need to change to `covermode=atomic`.

### 4. HIGH — Documentation references etcd backend that was removed

**File:** [doc/extended-configuration.md](doc/extended-configuration.md#L22-L67)

Full section on etcd datastore with config parameters. No etcd storage implementation exists.

### 5. HIGH — Documentation uses wrong CRD API group name

**File:** [doc/extended-configuration.md](doc/extended-configuration.md#L26)

Says `whereabouts.cni.k8s.io` — actual group is `whereabouts.cni.cncf.io`. Also "Not that" typo (should be "Note that").

### 6. HIGH — Alert references non-existent `whereabouts_ippool_capacity` metric

**File:** [doc/metrics.md](doc/metrics.md#L95-L101)

The `IPPoolNearlyFull` alert uses `whereabouts_ippool_capacity` which is not defined anywhere. The alert will never fire.

### 7. MEDIUM — Duplicate test jobs across release workflows

**Files:** [.github/workflows/image-push-release.yml](.github/workflows/image-push-release.yml), [.github/workflows/chart-push-release.yml](.github/workflows/chart-push-release.yml)

Both trigger on `push: tags: v*` with identical `test` jobs. Full test suite runs twice per tag push.

### 8. MEDIUM — `install-kubebuilder-tools.sh` only installs `controller-gen`

**File:** [hack/install-kubebuilder-tools.sh](hack/install-kubebuilder-tools.sh)

Script name doesn't match content. Does not install kubebuilder or envtest binaries.

### 9. MEDIUM — Tests run with `sudo` without documentation

**File:** [.github/workflows/test.yml](.github/workflows/test.yml#L54)

`KUBEBUILDER_ASSETS` may be lost when using `sudo`. Root-mode testing masks permission issues.

### 10. MEDIUM — `docker-build` doesn't pass version build-args

**File:** [Makefile](Makefile#L14-L15)

Dockerfile accepts `VERSION`, `GIT_SHA`, etc. as `ARG`s but `make docker-build` doesn't pass `--build-arg` flags. Images always report empty version.

### 11. MEDIUM — `e2e-get-test-tools.sh` uses `readlink --canonicalize` (fails on macOS)

**File:** [hack/e2e-get-test-tools.sh](hack/e2e-get-test-tools.sh#L5-L6)

GNU extension not available on macOS.

### 12. MEDIUM — `e2e-get-test-tools.sh` hardcodes `linux/amd64` kubectl download

**File:** [hack/e2e-get-test-tools.sh](hack/e2e-get-test-tools.sh#L8-L20)

kubectl download hardcoded to `linux/amd64`. Unusable on Apple Silicon.

### 13. LOW — `build.yml` unnecessarily installs kubebuilder tools in build matrix

### 14. LOW — `test.yml` step named "Generate code" also verifies

### 15. LOW — `Makefile` `yq` target hardcodes `linux_amd64`

### 16. LOW — `test-go.sh` typo: "arguement" → "argument"

**File:** [hack/test-go.sh](hack/test-go.sh#L14)

### 17. INFO — Trivy installed via unpinned `curl | sh` from `main` branch

Supply chain risk. Should use `aquasecurity/trivy-action` with pinned hash.

### 18. INFO — `developer_notes.md` `.env` uses `:` instead of `=`

**File:** [doc/developer_notes.md](doc/developer_notes.md#L55)

### 19. INFO — `build.yml` and `test.yml` have overlapping triggers on `push` to `main`
