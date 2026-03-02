# P12: Configuration & Documentation Review

**Date:** 2026-03-02  
**Reviewer Persona:** Configuration & Documentation Specialist

## Summary

| Severity | Count |
|----------|-------|
| CRITICAL | 0 |
| HIGH | 3 |
| MEDIUM | 2 |
| LOW | 2 |
| INFO | 2 |

---

## Findings

### 1. HIGH — Extended configuration documents removed etcd backend

**File:** [doc/extended-configuration.md](doc/extended-configuration.md#L22-L67)

Full section on etcd datastore with config parameters (`etcd_host`, `etcd_username`, `etcd_password`, etc.). No etcd storage implementation exists in the codebase. Misleads users into thinking etcd is supported.

### 2. HIGH — Wrong CRD API group name in documentation

**File:** [doc/extended-configuration.md](doc/extended-configuration.md#L26)

Says `whereabouts.cni.k8s.io` — actual group is `whereabouts.cni.cncf.io`. Also "Not that" typo.

### 3. HIGH — Metrics doc alert references non-existent metric

**File:** [doc/metrics.md](doc/metrics.md#L95-L101)

`IPPoolNearlyFull` alert uses `whereabouts_ippool_capacity` which is not defined. Alert will never fire.

### 4. MEDIUM — `e2e-get-test-tools.sh` uses GNU-only `readlink --canonicalize`

**File:** [hack/e2e-get-test-tools.sh](hack/e2e-get-test-tools.sh#L5-L6)

Breaks on macOS. Other scripts use POSIX-compatible path resolution.

### 5. MEDIUM — `docker-build` doesn't pass version build-args to Dockerfile

**File:** [Makefile](Makefile#L14-L15)

Images always have empty version info despite Dockerfile accepting `VERSION`, `GIT_SHA`, etc.

### 6. LOW — `developer_notes.md` `.env` uses `:` instead of `=`

**File:** [doc/developer_notes.md](doc/developer_notes.md#L55)

`godotenv` uses `KEY=VALUE` format, not `KEY: VALUE`.

### 7. LOW — `Makefile` `yq` download hardcodes `linux_amd64`

**File:** [Makefile](Makefile#L41)

Unusable on macOS or ARM machines.

### 8. INFO — Helm `node-slice-controller.yaml` is misnamed (actually deploys operator)

**File:** [deployment/whereabouts-chart/templates/node-slice-controller.yaml](deployment/whereabouts-chart/templates/node-slice-controller.yaml)

File should be renamed to `operator.yaml` to avoid confusion.

### 9. INFO — Chart name `whereabouts-chart` is non-idiomatic

Causes awkward resource names like `<release>-whereabouts-chart-operator`.
