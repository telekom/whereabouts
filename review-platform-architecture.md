# Platform Architecture Review — Whereabouts IPAM CRDs, Deployments & Helm Chart

**Reviewer:** Kubernetes Platform Architect  
**Date:** 2026-03-02  
**Scope:** CRD definitions, deployment manifests (`doc/crds/`), Helm chart (`deployment/whereabouts-chart/`)  
**Prior findings:** P9-2 (Helm test missing), P9-4 (Fixed — leader election), P9-5 (DaemonSet doc sync manual)

---

## P9-6: IPPool CRD stores mutable allocation state in `spec` — no status subresource

**Severity:** Medium  
**File:** [doc/crds/whereabouts.cni.cncf.io_ippools.yaml](doc/crds/whereabouts.cni.cncf.io_ippools.yaml#L81)  

**Description:**  
The IPPool CRD defines `subresources: {}` — explicitly no status subresource. All mutable allocation state lives in `spec.allocations`. This violates the Kubernetes API convention that `spec` represents desired state and `status` represents observed state. Consequences:

1. **No RBAC split between spec and status.** Any principal with `update` on `ippools` can modify both the range definition and the allocation map. With a status subresource, you could grant the CNI ServiceAccount `update` on `ippools/status` (for allocations) while restricting `update` on `ippools` (range changes) to administrators only.
2. **Optimistic locking covers the entire resource.** A metadata-only change (label update) conflicts with an allocation update because they share the same `resourceVersion`. With a status subresource, `/status` has an independent update path that doesn't conflict with spec changes.
3. **`kubectl edit` exposes allocations.** Administrators editing an IPPool see and can accidentally modify the allocations map.

This is inherited from upstream whereabouts and changing it would be a breaking API change requiring a new CRD version.

**Recommendation:**  
Plan a `v1beta1` CRD version that moves `allocations` to `status` with a status subresource. Provide a conversion webhook to migrate. In the interim, document that `spec.allocations` is a system-managed field not to be edited manually; consider a CEL `x-kubernetes-validations` rule that prevents non-system actors from modifying it (Kubernetes 1.28+).

---

## P9-7: CRDs lack structural validation — CIDR format and sliceSize type

**Severity:** Medium  
**Files:** [doc/crds/whereabouts.cni.cncf.io_ippools.yaml](doc/crds/whereabouts.cni.cncf.io_ippools.yaml#L73-L76), [doc/crds/whereabouts.cni.cncf.io_nodeslicepools.yaml](doc/crds/whereabouts.cni.cncf.io_nodeslicepools.yaml#L53-L56)

**Description:**  
Several CRD fields rely solely on webhook validation, which provides no protection when the webhook is unavailable (startup, outage, or `failurePolicy: Ignore`):

| CRD | Field | Schema | Issue |
|-----|-------|--------|-------|
| IPPool | `spec.range` | `type: string, minLength: 1` | No CIDR format validation. `"hello"` is accepted by the schema. |
| NodeSlicePool | `spec.sliceSize` | `type: string, minLength: 1` | Should be an integer 1–128 per webhook logic. `"banana"` passes schema. |
| IPPool | `spec.allocations.*.podref` | `type: string` | No format validation for `namespace/name` pattern. |
| OverlappingRange | `spec.podref` | `type: string, minLength: 1` | Same — no `namespace/name` pattern validation. |

The validating webhooks catch these at admission time, but during webhook outage windows (see P5-11), invalid data can be persisted and will cause runtime panics in the allocator (`net.ParseCIDR` on garbage data).

**Recommendation:**  
Add `x-kubernetes-validations` CEL rules (available since CRD `apiextensions.k8s.io/v1` on Kubernetes 1.25+):

```yaml
# IPPool spec.range
properties:
  range:
    type: string
    minLength: 1
    x-kubernetes-validations:
    - rule: "self.matches('^[0-9a-fA-F.:]+/[0-9]+$')"
      message: "range must be a valid CIDR notation (e.g., 10.0.0.0/24)"

# NodeSlicePool spec.sliceSize — change type to integer
properties:
  sliceSize:
    type: integer
    minimum: 1
    maximum: 128
```

Changing `sliceSize` from string to integer is a breaking change that requires a new CRD version (see P9-8), but the CEL validation can be added non-disruptively.

---

## P9-8: No CRD versioning strategy — single v1alpha1 with no conversion path

**Severity:** Medium  
**Files:** all three CRD YAMLs under [doc/crds/](doc/crds/)

**Description:**  
All three CRDs serve only `v1alpha1` with `storage: true`. There is no conversion webhook, no additional versions defined, and no documented plan for graduating to `v1beta1` or `v1`. Issues:

1. **`v1alpha1` signals instability.** Per Kubernetes API conventions, alpha APIs can be removed without notice. Platform teams are reluctant to adopt alpha CRDs in production. The CRDs have been stable for multiple releases — the version doesn't reflect maturity.
2. **No backward compatibility guarantee.** If a schema change is needed (e.g., P9-6 moving allocations to status, P9-7 changing sliceSize type), there's no multi-version serving strategy. The only option is a destructive CRD replace.
3. **No conversion webhook registered.** Even if a `v1beta1` version were added to the schema, there's no webhook to convert between storage and served versions.
4. **Helm `crds/` directory doesn't support upgrades.** Helm only installs CRDs on initial install, not on upgrade. Schema changes (adding CEL validation, new fields) won't be applied on `helm upgrade`.

**Recommendation:**  
1. Define a CRD graduation plan: `v1alpha1` → `v1alpha2` (with structural fixes from P9-6/P9-7) → `v1beta1` → `v1`.
2. Register a conversion webhook in the operator binary (controller-runtime supports this natively) for dual-serving during transition periods.
3. For Helm CRD lifecycle, either use a pre-upgrade hook Job that runs `kubectl apply` on CRDs, or move CRDs to regular templates with `{{- if .Values.installCRDs }}` guard (like cert-manager's approach).

---

## P9-9: Helm chart deploys deprecated architecture — not updated for operator split

**Severity:** High  
**Files:** [deployment/whereabouts-chart/templates/daemonset.yaml](deployment/whereabouts-chart/templates/daemonset.yaml#L51-L54), [deployment/whereabouts-chart/templates/node-slice-controller.yaml](deployment/whereabouts-chart/templates/node-slice-controller.yaml)

**Description:**  
The Helm chart deploys the **old** architecture while `doc/crds/` deploys the **new** architecture. They are fundamentally incompatible:

| Component | Helm Chart (old) | doc/crds (new) |
|-----------|------------------|----------------|
| DaemonSet command | `install-cni.sh` + `token-watcher.sh` + **`/ip-control-loop -log-level debug`** | `install-cni.sh` + `token-watcher.sh` (no ip-control-loop) |
| IP reconciliation | Cron-based `ip-control-loop` in DaemonSet | Operator Deployment with leader election |
| Node-slice controller | Standalone `/node-slice-controller` Deployment | Integrated in operator `controller` subcommand |
| Webhooks | **Not deployed** | Separate webhook Deployment + ValidatingWebhookConfiguration |
| ConfigMap | `cron-expression` for ip-control-loop schedule | Not needed (operator uses `--reconcile-interval`) |

A user installing via Helm gets:
- No validating webhooks (no data integrity protection)
- No leader-elected reconciler (ip-control-loop runs on every DaemonSet pod concurrently)  
- The deprecated standalone node-slice-controller
- A cron ConfigMap volume that wastes resources

The Helm chart also references the binary `/node-slice-controller` and `/ip-control-loop`, which may not exist if the image is built from the new codebase that produces `/whereabouts-operator` instead.

**Recommendation:**  
Update the Helm chart to deploy the new architecture:
1. Add templates for the operator Deployment, webhook Deployment, Service, and ValidatingWebhookConfiguration.
2. Add values for `operator.replicas`, `operator.resources`, `webhook.replicas`, `webhook.resources`, `webhook.failurePolicy`.
3. Strip `ip-control-loop` and `cron-scheduler-configmap` from the DaemonSet template.
4. Gate the deprecated node-slice-controller behind `nodeSliceController.legacy: true` (default `false`).
5. Remove the `cron-expression` ConfigMap or gate it behind the legacy flag.

---

## P9-10: Operator and webhook Deployments lack nodeSelector and tolerations

**Severity:** Medium  
**Files:** [doc/crds/operator-install.yaml](doc/crds/operator-install.yaml#L85-L128), [doc/crds/webhook-install.yaml](doc/crds/webhook-install.yaml#L73-L130)

**Description:**  
The DaemonSet specifies `tolerations: [{operator: Exists, effect: NoSchedule}]` to run on all nodes (including tainted ones), but neither the operator nor webhook Deployment has:
- **`nodeSelector`**: No `kubernetes.io/os: linux` — on mixed-OS clusters, pods could be scheduled on Windows nodes.
- **`tolerations`**: No tolerations at all. In managed Kubernetes clusters (EKS, AKS, GKE) where worker nodes commonly carry taints like `node.kubernetes.io/not-ready` during scaling events, the operator and webhook pods may become unschedulable.
- **`priorityClassName`**: Not set. During resource pressure, the operator and webhook can be preempted. Since IPAM is infrastructure-critical (pod creation depends on it), these should run at elevated priority.

The Helm chart's DaemonSet inherits `nodeSelector: kubernetes.io/os: linux` from values but uses neither for the node-slice-controller Deployment.

**Recommendation:**
```yaml
# Add to both operator-install.yaml and webhook-install.yaml pod specs
nodeSelector:
  kubernetes.io/os: linux
tolerations:
- key: node-role.kubernetes.io/control-plane
  operator: Exists
  effect: NoSchedule
priorityClassName: system-cluster-critical
```

The `system-cluster-critical` priority class ensures the IPAM operator and webhook are not preempted during resource pressure, consistent with other CNI components (e.g., Calico, Cilium operators).

---

## P9-11: No sidecar injection exclusion annotation — service mesh will break webhook

**Severity:** Low  
**Files:** [doc/crds/webhook-install.yaml](doc/crds/webhook-install.yaml#L73-L80)

**Description:**  
The webhook Deployment's pod template has no annotations to prevent service mesh sidecar injection. While `kube-system` is typically excluded from mesh injection by default, this is not guaranteed — platform teams may configure namespace-wide injection. If a sidecar proxy (Istio, Linkerd) is injected:

1. The API server sends webhook admission requests to the Service IP on port 443, expecting TLS with the CA from `caBundle`. The sidecar intercepts port 9443 and attempts its own mTLS handshake, which fails because the API server doesn't speak mesh mTLS.
2. The sidecar may interfere with cert-controller's secret watch connections.
3. Startup ordering: if the sidecar requires an init container that needs network (e.g., iptables setup), and the webhook pod is needed for other pod admissions, a circular dependency can form.

**Recommendation:**  
Add explicit sidecar exclusion annotations to both Deployments' pod templates:
```yaml
metadata:
  annotations:
    sidecar.istio.io/inject: "false"
    linkerd.io/inject: disabled
```

---

## P9-12: CNI ServiceAccount has cluster-wide CRD CRUD — no tenant boundary enforcement

**Severity:** Low  
**Files:** [doc/crds/daemonset-install.yaml](doc/crds/daemonset-install.yaml#L27-L48)

**Description:**  
The `whereabouts-cni` ClusterRole grants `get, list, watch, create, update, patch, delete` on all IPPools and OverlappingRangeIPReservations across all namespaces. The DaemonSet's ServiceAccount token is mounted into every CNI-executing pod's host filesystem (via `token-watcher.sh`). This means:

1. Any process with access to the host filesystem on any node can impersonate the CNI ServiceAccount and modify any IPPool — including those in other namespaces used by other tenants.
2. The CNI plugin writes to whichever namespace is configured in `WHEREABOUTS_NAMESPACE`, but the ClusterRole allows writes to all namespaces. A misconfigured `network_name` or namespace could cause cross-tenant IP pool corruption.
3. No admission control prevents a CNI invocation from writing to an IPPool in a different namespace than its pod.

IPAM is inherently cluster-scoped, so full restriction is impractical, but the blast radius should be minimized.

**Recommendation:**  
1. Consider namespace-scoped Roles where IPPools are constrained to `kube-system` (default) and the CNI SA only gets a RoleBinding in that namespace.
2. If multi-namespace IPPools are needed, document the security model and add a webhook validation rule that ensures `podref` namespace matches the pod's actual namespace (cross-reference check).
3. Use `automountServiceAccountToken: false` on the DaemonSet and mount the token explicitly with a projected volume for audience/expiry control.

---

## P9-13: Helm chart resource limits significantly lower than doc/crds manifests

**Severity:** Medium  
**Files:** [deployment/whereabouts-chart/values.yaml](deployment/whereabouts-chart/values.yaml#L35-L41)

**Description:**  
Resource allocations diverge between the Helm chart and the doc/crds manifests:

| Component | Helm chart | doc/crds |
|-----------|-----------|----------|
| DaemonSet memory request | 50Mi | 100Mi |
| DaemonSet memory limit | 50Mi | 200Mi |
| DaemonSet CPU limit | 100m (= request, no burst) | 100m (= request) |
| Node-slice-controller | Uses DaemonSet values (50Mi) | Deprecated (operator uses 128Mi/256Mi) |

The Helm chart's 50Mi memory limit is critically tight. The DaemonSet runs `ip-control-loop` which lists pods and IPPools — memory usage scales with cluster size. In a 500-pod cluster, the pod list alone can exceed 50Mi when deserialized. The container will be OOMKilled.

Additionally, the Helm chart applies the same resource block to both the DaemonSet and the node-slice-controller Deployment, but they have very different resource profiles. The node-slice-controller lists NADs and nodes and builds in-memory allocation maps — 50Mi is insufficient for clusters with many NADs.

**Recommendation:**  
1. Align Helm values with doc/crds manifests:
```yaml
resources:
  requests:
    cpu: "100m"
    memory: "100Mi"
  limits:
    cpu: "100m"
    memory: "200Mi"
```
2. Add separate resource values for the node-slice-controller:
```yaml
nodeSliceController:
  resources:
    requests:
      cpu: "100m"
      memory: "128Mi"
    limits:
      cpu: "200m"
      memory: "256Mi"
```
3. When migrating the Helm chart to the new architecture (P9-9), add dedicated resource blocks for operator and webhook.

---

## P9-14: No NetworkPolicy for operator or webhook — unrestricted network access

**Severity:** Low  
**Files:** [doc/crds/operator-install.yaml](doc/crds/operator-install.yaml), [doc/crds/webhook-install.yaml](doc/crds/webhook-install.yaml)

**Description:**  
Neither the operator nor webhook has a NetworkPolicy. In a zero-trust cluster:
- The webhook should only accept ingress on port 9443 from the API server (and port 8082/8083 from monitoring).
- The operator should only need egress to the API server on port 443/6443.
- Neither should accept arbitrary ingress or make arbitrary egress connections.

Without NetworkPolicies, a compromised operator pod can be used for lateral movement within the cluster network.

**Recommendation:**  
Add NetworkPolicies (and corresponding Helm templates):
```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: whereabouts-webhook
  namespace: kube-system
spec:
  podSelector:
    matchLabels:
      app: whereabouts-webhook
  policyTypes: [Ingress, Egress]
  ingress:
  - ports:
    - port: 9443    # webhook admission
    - port: 8082    # metrics (restrict to monitoring namespace)
    - port: 8083    # health probes
  egress:
  - ports:
    - port: 443     # API server
    - port: 6443    # API server (alternate)
```

---

## P9-15: IPPool CRD has no additionalPrinterColumns for allocation count — poor `kubectl` observability

**Severity:** Low  
**Files:** [doc/crds/whereabouts.cni.cncf.io_ippools.yaml](doc/crds/whereabouts.cni.cncf.io_ippools.yaml#L19-L24)

**Description:**  
`kubectl get ippools` shows only `Range`. There's no visibility into how many IPs are allocated vs. available, the age of the resource, or the namespace the pool serves. Operators must use `kubectl get ippool <name> -o json | jq '.spec.allocations | length'` for basic capacity information.

The NodeSlicePool and OverlappingRangeIPReservation CRDs have useful columns (SliceSize, PodRef), but IPPool — the most frequently inspected resource — lacks them.

**Recommendation:**  
Add printer columns to the IPPool CRD:
```yaml
additionalPrinterColumns:
- jsonPath: .spec.range
  name: Range
  type: string
- jsonPath: '.spec.allocations'
  name: Allocations
  type: string
  description: "Number of allocated IPs"
  # Note: JSONPath can't count map keys; use a custom column via controller
- jsonPath: .metadata.creationTimestamp
  name: Age
  type: date
```

Since JSONPath cannot count map keys, consider adding a `status.allocationCount` field (requires status subresource from P9-6) that the operator maintains, and use that as the column source.

---

## P9-16: CRD shortName `nsp` risks collision with common platform CRDs

**Severity:** Low  
**Files:** [doc/crds/whereabouts.cni.cncf.io_nodeslicepools.yaml](doc/crds/whereabouts.cni.cncf.io_nodeslicepools.yaml#L14)

**Description:**  
NodeSlicePool uses shortName `nsp`. In the broader Kubernetes ecosystem:
- Calico's `NetworkSecurityPolicy` historically used `nsp` in some configurations.
- Some policy engines use `nsp` for namespace policies.
- It's a common abbreviation that could collide as CRD adoption grows.

If a collision occurs, `kubectl get nsp` becomes ambiguous and users must use the fully qualified resource name.

**Recommendation:**  
Consider a more specific shortName like `wnsp` (whereabouts-namespaced-slice-pool) or keep the current name but document the potential collision. Not urgent but worth noting for the `v1beta1` graduation.

---

## P9-17: Webhook `matchConditions` CEL bypass is brittle — hardcoded ServiceAccount names

**Severity:** Medium  
**Files:** [doc/crds/validatingwebhookconfiguration.yaml](doc/crds/validatingwebhookconfiguration.yaml#L36-L38)

**Description:**  
The CEL bypass expressions hardcode ServiceAccount usernames:
```yaml
expression: >-
  !(request.userInfo.username == "system:serviceaccount:kube-system:whereabouts")
```

This breaks if:
1. Whereabouts is installed in a namespace other than `kube-system` (the Helm chart supports `namespaceOverride`).
2. The ServiceAccount is renamed (the Helm chart generates names from the release).
3. A Helm release named `my-release` produces a SA named `my-release-whereabouts-chart`, not `whereabouts`.

The Helm chart doesn't deploy webhooks (P9-9), but when it eventually does, these hardcoded names will cause the webhook to validate its own writes, leading to rejection loops.

**Recommendation:**  
1. Parameterize the SA names in the matchConditions when generating the ValidatingWebhookConfiguration. The Helm template should inject the actual SA name:
```yaml
expression: >-
  !(request.userInfo.username == "system:serviceaccount:{{ .Release.Namespace }}:{{ include "whereabouts.serviceAccountName" . }}")
```
2. For the static manifests in `doc/crds/`, add comments documenting that these names must be adjusted if the namespace or SA name changes.
3. Consider using a `system:serviceaccounts:kube-system` group check instead of exact username match for broader compatibility.

---

## P9-18: DaemonSet runs as `privileged: true` and `hostNetwork: true` — no capability scoping

**Severity:** Medium  
**Files:** [doc/crds/daemonset-install.yaml](doc/crds/daemonset-install.yaml#L86-L87)

**Description:**  
The DaemonSet container runs with `privileged: true` and `hostNetwork: true`. While `hostNetwork` is required for CNI binary installation (writing to `/opt/cni/bin` and `/etc/cni/net.d` on the host), full `privileged` mode grants all Linux capabilities (CAP_SYS_ADMIN, CAP_NET_ADMIN, etc.), device access, and host PID/IPC namespace access. The actual operations (copying a binary, watching a token file) only need:
- Write access to `/host/opt/cni/bin` and `/host/etc/cni/net.d` (provided by hostPath volumes)
- Read access to the ServiceAccount token (provided by volume mount)

`privileged: true` is far broader than necessary and violates the principle of least privilege. Clusters enforcing Pod Security Standards (PSS) at `baseline` or `restricted` level will reject this pod.

**Recommendation:**  
Replace `privileged: true` with minimal capabilities:
```yaml
securityContext:
  privileged: false
  runAsUser: 0  # needed for writing to host paths
  capabilities:
    drop: ["ALL"]
    add: ["DAC_OVERRIDE"]  # write to host-owned directories
```
Test thoroughly — if `install-cni.sh` uses `chmod` or `chown`, additional capabilities (`CHOWN`, `FOWNER`) may be needed. The current Helm chart also uses `privileged: true` and should be aligned.

---

## P9-19: Helm chart CRDs in `crds/` directory won't update on `helm upgrade`

**Severity:** Medium  
**Files:** [deployment/whereabouts-chart/crds/](deployment/whereabouts-chart/crds/)

**Description:**  
Helm's `crds/` directory is a special directory: CRDs in it are installed on `helm install` but **never** updated on `helm upgrade` and **never** deleted on `helm uninstall`. This is by design (Helm doesn't want to accidentally delete CRDs and all their instances). Consequences:

1. Schema changes (adding CEL validation from P9-7, adding printer columns from P9-15, adding new versions from P9-8) will not be applied when users upgrade the chart.
2. New fields added to CRD schemas in a chart version bump are silently ignored.
3. Users must manually `kubectl apply -f` the CRD YAMLs after every upgrade that changes CRD schemas.

**Recommendation:**  
Adopt one of these patterns:
1. **Pre-upgrade hook Job** (cert-manager pattern): Add a Job template with `helm.sh/hook: pre-upgrade` that runs `kubectl apply` on the CRDs.
2. **Move CRDs to templates** with a `installCRDs` value gate and `"helm.sh/resource-policy": keep` annotation to prevent deletion.
3. **Document prominently** that CRD updates require manual application after chart upgrades.

---

## Summary

| ID | Title | Severity |
|------|-------|----------|
| **P9-6** | IPPool allocations in `spec`, no status subresource | Medium |
| **P9-7** | CRDs lack structural validation (CIDR format, sliceSize type) | Medium |
| **P9-8** | No CRD versioning strategy — single v1alpha1 | Medium |
| **P9-9** | Helm chart deploys deprecated architecture, not operator split | **High** |
| **P9-10** | Operator/webhook lack nodeSelector, tolerations, priorityClass | Medium |
| **P9-11** | No sidecar injection exclusion annotation | Low |
| **P9-12** | CNI SA has cluster-wide CRD CRUD — no tenant boundaries | Low |
| **P9-13** | Helm resource limits (50Mi) too low vs. doc/crds (200Mi) | Medium |
| **P9-14** | No NetworkPolicy for operator or webhook | Low |
| **P9-15** | IPPool CRD lacks printer columns for allocation count | Low |
| **P9-16** | CRD shortName `nsp` risks collision | Low |
| **P9-17** | Webhook CEL bypass hardcodes SA names — breaks Helm installs | Medium |
| **P9-18** | DaemonSet `privileged: true` — no capability scoping | Medium |
| **P9-19** | Helm `crds/` directory won't update on `helm upgrade` | Medium |

**Priority Fix Order:** P9-9 → P9-17 → P9-10 → P9-13 → P9-7 → P9-18 → P9-19 → P9-6 → P9-8 → remaining
