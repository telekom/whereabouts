# Reuse upstream libraries before writing helpers

This rule applies to humans and AI agents, even when an adoption PR has not
merged. Before adding helper code, check in this order:

1. Go standard library.
2. Kubernetes libraries: `k8s.io/*`, `sigs.k8s.io/controller-runtime`.
3. Flux libraries: `github.com/fluxcd/pkg`.
4. Other well-known, maintained upstream libraries.
5. Available packages in [`telekom/t-caas-go-library`](https://github.com/telekom/t-caas-go-library/blob/main/docs/upstream-libraries.md)
   (the repository is currently private and planned to become public).
6. Custom code only when no suitable upstream fits.

These recommendations are not dependencies added by this document. Check
version compatibility and preserve Whereabouts-specific semantics.

| Concern | Prefer this import path |
| --- | --- |
| IP parsing, comparison, and prefixes | `net/netip` |
| IP ranges and sets | `go4.org/netipx` |
| Kubernetes legacy IP operations | `k8s.io/utils/net` |
| Checked IP arithmetic and bounded subnet division | `github.com/telekom/t-caas-go-library/pkg/netutil` |
| Conflict retries | `k8s.io/client-go/util/retry` |
| Optimistic-lock patches | `sigs.k8s.io/controller-runtime/pkg/client` |
| Context-aware polling | `k8s.io/apimachinery/pkg/util/wait` |
| E2E Kubernetes waits | `sigs.k8s.io/e2e-framework/klient/wait` |
| Leader election | `k8s.io/client-go/tools/leaderelection` |
| Owner references and finalizers | `sigs.k8s.io/controller-runtime/pkg/controller/controllerutil` |
| API-server integration tests | `sigs.k8s.io/controller-runtime/pkg/envtest` |
| Prometheus metrics | `github.com/prometheus/client_golang/prometheus` |
| Webhook certificate rotation | `github.com/open-policy-agent/cert-controller/pkg/rotator` |

The merged shared-library packages most relevant here are
`github.com/telekom/t-caas-go-library/pkg/netutil` for checked arithmetic and
budgeted subnet subdivision, and `github.com/telekom/t-caas-go-library/pkg/patch`
only when its fresh-read/retry composition matches the use case. See the
upstream library guide for availability and semantic caveats; the link alone
may not be accessible until that repository becomes public.

Write convenience wrappers only when the same glue demonstrably repeats across
multiple repositories. In that case, contribute it to
`telekom/t-caas-go-library` rather than duplicating it here. Keep IPAM policy
and behavior that differs from upstream local.

## Existing migration candidates

These are review candidates, not changes in this documentation update:

- `pkg/iphelpers/iphelpers.go`: use `net/netip` / `go4.org/netipx` where
  equivalent; use shared `pkg/netutil` for checked arithmetic or bounded
  subdivision. Preserve single-host and IPv6 endpoint behavior.
- `e2e/client/{pod,replicaset,statefulset}.go`: consider upstream
  context-aware waits while retaining workload predicates and dependent-Pod
  deletion checks.
- `internal/webhook/metrics.go`: prefer direct Prometheus `CounterVec` APIs;
  preserve labels and zero-value series.
- `internal/webhook/certrotator/` and webhook setup: retain the existing
  cert-controller rotator and compare only repeated setup/readiness glue with
  the library's optional, not-yet-merged cert-rotation proposal.
- `pkg/storage/kubernetes/ipam.go`: already uses client-go leader election;
  retain its one-shot election semantics rather than adding a wrapper.
