# Whereabouts — Comprehensive Code Review

**Date:** 2026-03-02  
**Branch:** `feat/controller-runtime-migration`  
**Commit:** `e4fc8dcc` (HEAD)

## Executive Summary

This review covers the full codebase of the Deutsche Telekom T-CAAS fork of whereabouts after the controller-runtime migration, Prometheus metrics addition, and CI fixes. Twelve persona reviews were conducted covering CNI spec compliance, storage/concurrency, IP allocation math, security, SRE/operator patterns, code quality, test quality, CI/CD, platform architecture, controller-runtime usage, data integrity, and configuration/documentation.

### Severity Distribution

| Severity | Count | Description |
|----------|-------|-------------|
| CRITICAL | 6 | Broken deployments, data integrity risks, spec violations |
| HIGH | 18 | Significant bugs, race conditions, missing validation |
| MEDIUM | 41 | Design issues, convention violations, missing guards |
| LOW | 38 | Code smells, minor issues, minor test gaps |
| INFO | 18 | Cosmetic items, documentation notes |

---

### Top 10 Critical/High Issues

| # | Sev | Issue | Persona |
|---|-----|-------|---------|
| 1 | CRITICAL | Non-atomic IPPool + OverlappingRange updates — IP allocated without overlap protection | [P2 §1](review-p02-storage-concurrency.md) |
| 2 | CRITICAL | Helm chart names don't match Go hardcoded VWC/Secret/Service names → TLS breaks | [P4 §1](review-p04-security.md) |
| 3 | CRITICAL | Release workflow doesn't upload operator binary | [P8 §2](review-p08-cicd.md) |
| 4 | CRITICAL | Static YAMLs use `:latest` image tag — non-reproducible | [P4 §2](review-p04-security.md) |
| 5 | CRITICAL | IPPool stores allocations in `.spec` not `.status` — no RBAC separation | [P11 §1](review-p11-data-integrity.md) |
| 6 | CRITICAL | Multi-range rollback uses stale resourceVersion — always fails | [P2 §2](review-p02-storage-concurrency.md) |
| 7 | HIGH | No backoff in 100-iteration retry loop → thundering herd | [P2 §3](review-p02-storage-concurrency.md) |
| 8 | HIGH | Global logging state has data races | [P5 §2](review-p05-sre-operator.md) |
| 9 | HIGH | `cmdCheck` does not validate `prevResult` per CNI spec | [P1 §1](review-p01-cni-spec.md) |
| 10 | HIGH | `failurePolicy: Fail` blocks pod creation during webhook outage | [P4 §5](review-p04-security.md) |

---

### Review Files

| Persona | Focus Area | Findings | File |
|---------|-----------|----------|------|
| P1 | CNI Spec Compliance | 3H 5M 6L 3I | [review-p01-cni-spec.md](review-p01-cni-spec.md) |
| P2 | Storage & Concurrency | 2C 4H 6M 3L 2I | [review-p02-storage-concurrency.md](review-p02-storage-concurrency.md) |
| P3 | IP Allocation & Math | 4M 5L 3I | [review-p03-ip-allocation.md](review-p03-ip-allocation.md) |
| P4 | Security & Deployment | 3C 3H 6M 4L 2I | [review-p04-security.md](review-p04-security.md) |
| P5 | SRE & Operator Patterns | 2H 5M 3L 2I | [review-p05-sre-operator.md](review-p05-sre-operator.md) |
| P6 | Code Quality | 5M 5L 2I | [review-p06-code-quality.md](review-p06-code-quality.md) |
| P7 | Test Quality | 1H 5M 5L 2I | [review-p07-test-quality.md](review-p07-test-quality.md) |
| P8 | CI/CD & Build | 2C 4H 6M 4L 3I | [review-p08-cicd.md](review-p08-cicd.md) |
| P9 | Platform Architecture | 1C 2H 3M 3L 1I | [review-p09-platform-architecture.md](review-p09-platform-architecture.md) |
| P10 | Controller-Runtime Usage | 1C 3H 7M 3L 2I | [review-p10-controller-runtime.md](review-p10-controller-runtime.md) |
| P11 | Data Integrity & CRDs | 1C 2H 5M 5L 2I | [review-p11-data-integrity.md](review-p11-data-integrity.md) |
| P12 | Config & Documentation | 3H 2M 2L 2I | [review-p12-config-docs.md](review-p12-config-docs.md) |

---

### Key Themes

#### 1. Upstream Design Debt (Inherited)
- IPPool `.spec.allocations` should be `.status` (P9, P11)
- Non-atomic IPPool + ORIP updates (P2)
- No backoff in retry loop (P2)
- `v1alpha1` API with production deployments (P9)

#### 2. Deployment & Security Hardening
- Helm/Go name mismatch breaks webhooks (P4)
- `:latest` tags in static manifests (P4)
- Missing PDBs for webhook availability (P4, P9)
- `failurePolicy: Fail` risk (P4, P9, P10)
- Overly broad secrets RBAC (P4)

#### 3. Controller-Runtime Patterns
- No Kubernetes Events emitted (P10)
- No predicates on IPPool watch → self-triggered loops (P10)
- `mapNodeToNADs` O(N×M) without node event filtering (P10)
- Stale metrics gauge when IPPool deleted (P10)
- Status update during Create ignored (P10)

#### 4. Testing Gaps
- CHECK tests don't call `cmdCheck` (P1, P7)
- No `denormalizeIPName` unit tests (P7)
- No race detector in CI (P8)
- Tests write to global paths without cleanup (P7)
- Missing edge case tests for IP math (P3, P7)

#### 5. Documentation Drift
- etcd backend documented but removed (P8, P12)
- Wrong API group name in docs (P8, P12)
- Alert references non-existent metric (P8, P12)
- Deprecated binaries still referenced (P4, P8)

---

### Recommended Priority Order

**Phase 1 — Safety (blocks production)**
1. Fix Helm chart naming to match Go hardcoded names
2. Pin image tags in static manifests
3. Remove deprecated `node-slice-controller.yaml`
4. Add operator binary to release workflow
5. Fix `doc/metrics.md` alert referencing non-existent metric

**Phase 2 — Reliability**
6. Add exponential backoff to retry loop
7. Add PDB for webhook deployment
8. Add node event predicates to NodeSliceReconciler
9. Fix `cleanupOverlappingReservations` error handling
10. Clear gauge on IPPool deletion
11. Add Kubernetes Events to controllers

**Phase 3 — Correctness**
12. Implement `cmdCheck` `prevResult` validation
13. Fix multi-range rollback stale resourceVersion
14. Add `-race` to CI test runs
15. Fix logging data races (`sync.Mutex`)
16. Add `denormalizeIPName` tests
17. Fix `mustCIDR` nil deref before error check

**Phase 4 — Documentation**
18. Remove etcd documentation
19. Fix API group name in docs
20. Update README for new operator architecture
21. Add missing CRD validations (Pattern, MaxLength)
