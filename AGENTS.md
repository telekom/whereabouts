# Reuse upstream libraries before writing helpers

This rule applies to humans and AI agents, even when an adoption PR has not
merged. Before adding helper code, check in this order:

1. Go standard library.
2. Kubernetes libraries: `k8s.io/*`, `sigs.k8s.io/controller-runtime`.
3. Flux libraries: `github.com/fluxcd/pkg`.
4. Other well-known, maintained upstream libraries.
5. Available packages in [`telekom/t-caas-go-library`](https://github.com/telekom/t-caas-go-library/blob/main/docs/upstream-libraries.md)
   (public, pinned to v0.1.0, including its released lifecycle helpers).
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
| Webhook certificate lifecycle | `github.com/telekom/t-caas-go-library/pkg/certrotation` (cert-controller underneath) |

The merged shared-library packages most relevant here are
`github.com/telekom/t-caas-go-library/pkg/netutil` for checked arithmetic and
budgeted subnet subdivision, and `github.com/telekom/t-caas-go-library/pkg/patch`
only when its fresh-read/retry composition matches the use case.
`github.com/telekom/t-caas-go-library/pkg/certrotation` supplies certificate
rotation setup and certificate-gated webhook registration/readiness. See the
upstream library guide for availability and semantic caveats.

Write convenience wrappers only when the same glue demonstrably repeats across
multiple repositories. In that case, contribute it to
`telekom/t-caas-go-library` rather than duplicating it here. Keep IPAM policy
and behavior that differs from upstream local.

## Existing integrations and remaining candidates

Preserve these local policies when considering further upstream adoption:

- `pkg/iphelpers/iphelpers.go`: already uses `net/netip` and shared
  `pkg/netutil` for checked addition and bounded subdivision. Keep unsigned
  offsets, mapped IPv4 unmapping, host-bit rejection, and allocation endpoint
  policy local; shared first-usable/broadcast conventions are not drop-in.
- `e2e/client/{pod,replicaset,statefulset}.go`: already use apimachinery
  context-aware waits. Retain workload predicates and dependent-Pod deletion
  checks: a Running pod is not equivalent to e2e-framework's PodReady.
  StatefulSet scaling uses client-go conflict retries with fresh reads.
- `e2e/util/`: reuse these test helpers rather than copying them into suites.
  Inclusive range assertions use `bytes.Compare` on 16-byte IPs to retain
  mapped-IPv4 equivalence and legacy cross-family numeric ordering, not
  `netip.Addr.Compare`'s family-first order.
- `internal/webhook/metrics.go`: prefer direct Prometheus `CounterVec` APIs;
  preserve labels and zero-value series.
- `internal/webhook/certrotator/` and webhook setup: use the public v0.1.0
  `pkg/certrotation` lifecycle helpers around cert-controller. Keep the
  single-consumer Secret bootstrap local; gate readiness on completed webhook
  registration, not just certificate provisioning. Preserve all-replica
  rotation, explicit Whereabouts CA identity/DNS and restart-on-refresh.
- `pkg/storage/kubernetes/ipam.go`: already uses client-go leader election;
  retain its one-shot election semantics rather than adding a wrapper.
