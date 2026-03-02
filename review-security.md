# Security Review — Whereabouts IPAM CNI Plugin

**Date:** March 2, 2026  
**Scope:** RBAC, webhooks, cert rotation, container security, supply chain, privilege escalation  
**Repository:** `telekom/whereabouts`  
**Reviewer Role:** Security Engineer (Persona 4, continued)

**Known acknowledged issues (not re-listed):**
- P4-1: No image signing or SBOM generation
- P4-3: No govulncheck in CI

---

## Findings

---

### P4-4: Webhook ClusterRole grants unrestricted Secrets access cluster-wide

**Severity:** HIGH  
**File:** [doc/crds/webhook-install.yaml](doc/crds/webhook-install.yaml#L21-L24)

```yaml
rules:
- apiGroups: [""]
  resources: [secrets]
  verbs: [get, list, watch, create, update, patch]
```

The `whereabouts-webhook` ClusterRole grants `get`, `list`, `watch`, `create`, `update`, `patch` on **all** Secrets in **every** namespace.  The cert-controller only needs access to a single Secret (`whereabouts-webhook-cert`) in a single namespace (`kube-system`).

**Impact:** If the webhook pod is compromised (RCE via deserialization, dependency CVE, etc.), the attacker can read every Secret in the cluster — including other TLS keys, database credentials, cloud provider tokens, and every ServiceAccount token stored as a Secret.

**Recommendation:** Scope the Secret rule to the specific namespace and resource name:

```yaml
# Use a Role (not ClusterRole) in kube-system, with resourceNames
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: whereabouts-webhook
  namespace: kube-system
rules:
- apiGroups: [""]
  resources: [secrets]
  resourceNames: [whereabouts-webhook-cert]
  verbs: [get, list, watch, update, patch]
- apiGroups: [""]
  resources: [secrets]
  verbs: [create]   # create can't use resourceNames
```

The `validatingwebhookconfigurations` rule can remain a ClusterRole (webhooks are cluster-scoped), but should also use `resourceNames: [whereabouts-validating-webhooks]`.

---

### P4-5: DaemonSet container runs fully privileged with host access

**Severity:** HIGH  
**Files:** [doc/crds/daemonset-install.yaml](doc/crds/daemonset-install.yaml#L68-L100),
[deployment/whereabouts-chart/values.yaml](deployment/whereabouts-chart/values.yaml#L32-L33)

```yaml
# daemonset-install.yaml
hostNetwork: true
securityContext:
  privileged: true
volumeMounts:
- name: cnibin
  mountPath: /host/opt/cni/bin
- name: cni-net-dir
  mountPath: /host/etc/cni/net.d
```

The DaemonSet container runs with `privileged: true`, `hostNetwork: true`, and mounts the host CNI directories read-write. This combination gives the container **full root access to the host**:  
- All capabilities granted  
- Access to all host devices  
- No seccomp / AppArmor restrictions  
- Access to the host network namespace  
- Write access to `/opt/cni/bin` and `/etc/cni/net.d` on the host

Privilege is needed to install the CNI binary and kubeconfig, but should be minimized:

**Recommendation:**
1. Use an init container for the privileged binary copy, then run the main container (token-watcher) unprivileged.
2. At minimum, replace `privileged: true` with the specific capabilities needed:
   ```yaml
   securityContext:
     privileged: false
     capabilities:
       drop: [ALL]
       # No extra caps needed — file copy to hostPath volume doesn't require capabilities
   ```
3. Add `readOnlyRootFilesystem: true` and `runAsNonRoot: true` for the token-watcher container.

---

### P4-6: Container image runs as root — no USER directive in Dockerfile

**Severity:** MEDIUM  
**File:** [Dockerfile](Dockerfile#L10-L19)

```dockerfile
FROM alpine:3.23.3@sha256:...
WORKDIR /
COPY --from=builder ... /whereabouts .
COPY --from=builder ... /whereabouts-operator .
COPY script/install-cni.sh .
COPY script/lib.sh .
COPY script/token-watcher.sh .
CMD ["/install-cni.sh"]
```

No `USER` directive, no `adduser`, no `RUN chown`. The container runs as UID 0 (root) by default. While the operator and webhook deployments set `runAsNonRoot: true` in their pod specs (which prevents the container from actually running as root there), the DaemonSet has `privileged: true` and runs as root.

The binaries themselves (the statically compiled Go binaries) don't need root. Only the `install-cni.sh` script needs write access to host-mounted directories.

**Recommendation:**
1. Create a non-root user in the Dockerfile:
   ```dockerfile
   RUN adduser -D -u 65532 nonroot
   USER 65532:65532
   ```
2. Set appropriate ownership on copied binaries.
3. For the DaemonSet, the init container approach from P4-5 would run the privileged install as root, then the main container as non-root.

---

### P4-7: CEL matchConditions bypass uses hardcoded ServiceAccount names — impersonation risk

**Severity:** MEDIUM  
**File:** [doc/crds/validatingwebhookconfiguration.yaml](doc/crds/validatingwebhookconfiguration.yaml#L34-L36)

```yaml
matchConditions:
- name: bypass-cni-plugin
  expression: >-
    !(request.userInfo.username == "system:serviceaccount:kube-system:whereabouts")
```

The webhook bypass relies on the Kubernetes username string matching a specific ServiceAccount. Risks:

1. **SA in wrong namespace:** If someone creates a ServiceAccount named `whereabouts` in a different namespace but gains the ability to impersonate `system:serviceaccount:kube-system:whereabouts` (via RBAC impersonation grants), they bypass all webhook validation.

2. **Namespace assumption:** The SA name is hardcoded to `kube-system`. If whereabouts is deployed in a different namespace, the bypass doesn't work and the CNI binary is blocked by its own webhook.

3. **No group-based check:** The CEL expression doesn't verify group membership (`system:serviceaccounts:kube-system`), which would add a second factor.

**Recommendation:**
- Add a group check alongside the username match:
  ```yaml
  expression: >-
    !(request.userInfo.username == "system:serviceaccount:kube-system:whereabouts"
      && request.userInfo.groups.exists(g, g == "system:serviceaccounts:kube-system"))
  ```
- Document that the namespace is hardcoded and must be updated if deploying outside `kube-system`.

---

### P4-8: Operator and webhook Deployments missing `capabilities.drop: [ALL]` and seccomp profile

**Severity:** MEDIUM  
**Files:** [doc/crds/operator-install.yaml](doc/crds/operator-install.yaml#L124-L127),
[doc/crds/webhook-install.yaml](doc/crds/webhook-install.yaml#L117-L120)

Both Deployments have good baseline hardening:
```yaml
securityContext:
  allowPrivilegeEscalation: false
  readOnlyRootFilesystem: true
  runAsNonRoot: true
```

But are missing:

- `capabilities: { drop: [ALL] }` — without this, the container retains default Linux capabilities (e.g., `NET_RAW`, `CHOWN`, `DAC_OVERRIDE`). These could be abused if the container is compromised.
- `seccompProfile: { type: RuntimeDefault }` — without a seccomp profile, the container can make any syscall. Pod Security Standards "restricted" profile requires both.

**Recommendation:**
```yaml
securityContext:
  allowPrivilegeEscalation: false
  readOnlyRootFilesystem: true
  runAsNonRoot: true
  runAsUser: 65532
  runAsGroup: 65532
  capabilities:
    drop: [ALL]
  seccompProfile:
    type: RuntimeDefault
```

---

### P4-9: Metrics endpoints exposed without authentication

**Severity:** MEDIUM  
**Files:** [cmd/operator/controller.go](cmd/operator/controller.go#L37-L39),
[cmd/operator/webhook.go](cmd/operator/webhook.go#L42-L44)

```go
Metrics: server.Options{
    BindAddress: metricsAddr,  // ":8080" or ":8082"
},
```

The controller-runtime metrics server binds to `0.0.0.0:8080` (controller) and `0.0.0.0:8082` (webhook) with no TLS, no authentication, and no authorization. The Prometheus `/metrics` endpoint exposes:

- Go runtime internals (goroutine counts, GC stats, memory)
- controller-runtime work queue depths and latencies
- Reconciler error counts
- Kubernetes client request latencies and counts

This information helps an attacker understand workload patterns, timing, and internal state.

**Recommendation:**
1. Use controller-runtime's `SecureServing` or `FilterProvider` options to require authentication.
2. Alternatively, bind to `127.0.0.1` and use a sidecar for Prometheus scraping, or set `BindAddress: "0"` to disable if not needed.
3. At minimum, add a NetworkPolicy to restrict access to the metrics ports.

---

### P4-10: All deployment manifests use `:latest` image tag

**Severity:** MEDIUM  
**Files:** [doc/crds/operator-install.yaml](doc/crds/operator-install.yaml#L89),
[doc/crds/webhook-install.yaml](doc/crds/webhook-install.yaml#L81),
[doc/crds/daemonset-install.yaml](doc/crds/daemonset-install.yaml#L81),
[doc/crds/node-slice-controller.yaml](doc/crds/node-slice-controller.yaml#L35)

```yaml
image: ghcr.io/telekom/whereabouts:latest
```

All four deployment manifests reference `:latest`. This tag is mutable — a supply chain attacker who gains push access to the registry can replace the image silently. Even without malice, `:latest` makes deployments non-reproducible and rollbacks impossible because the previous image is overwritten.

Note: The Dockerfile itself pins base images by digest (good), but the deployment manifests undo that protection.

**Recommendation:** Use immutable image references in manifests — either a version tag (`v0.8.0`) or full digest (`@sha256:...`). The Helm chart's `values.yaml` correctly uses `tag: v0.8.0` but the standalone manifests do not.

---

### P4-11: SA token written to world-readable host filesystem

**Severity:** MEDIUM  
**File:** [script/lib.sh](script/lib.sh#L38-L77)

```bash
SERVICE_ACCOUNT_TOKEN=$(cat $SERVICE_ACCOUNT_TOKEN_PATH)
# ...
cat > $WHEREABOUTS_KUBECONFIG <<EOF
# ...
    token: "${SERVICE_ACCOUNT_TOKEN}"
# ...
EOF
```

The DaemonSet install script reads the ServiceAccount token and writes it into a kubeconfig file at `/host/etc/cni/net.d/whereabouts.d/whereabouts.kubeconfig` on the **host filesystem**. The file permission is set to `chmod 600`, but:

1. The file is on hostPath, so any other container mounting `/etc/cni/net.d` can read the token.
2. Any process running as root on the host can read the token.
3. The `whereabouts` ServiceAccount has powerful RBAC (create/update/delete on IPPools, OverlappingRangeIPReservation, and Leases). A token leak grants cluster-wide IPAM control.

**Recommendation:**
1. Explore using projected volume token mounts (audience-scoped, time-limited) instead of the long-lived SA token.
2. Ensure the kubeconfig path is not shared with untrusted containers.
3. Consider Kubernetes `TokenRequest` API for short-lived tokens.

---

### P4-12: `SKIP_TLS_VERIFY` allows disabling API server TLS verification

**Severity:** LOW  
**File:** [script/lib.sh](script/lib.sh#L16-L48)

```bash
SKIP_TLS_VERIFY=${SKIP_TLS_VERIFY:-false}
# ...
if [ "$SKIP_TLS_VERIFY" == "true" ]; then
    TLS_CFG="insecure-skip-tls-verify: true"
```

Setting the `SKIP_TLS_VERIFY` environment variable to `true` generates a kubeconfig that does not verify the API server's TLS certificate. This opens the CNI plugin to man-in-the-middle attacks — an attacker on the pod network could intercept CNI→API server traffic and steal the SA token or manipulate IPAM responses.

The default is `false` (correct), but the option exists and could be set accidentally or by convention in dev environments and left in production.

**Recommendation:** Log a prominent warning when `SKIP_TLS_VERIFY=true` is set. Consider removing the option entirely or gating it behind an additional "I-know-what-I-am-doing" flag.

---

### P4-13: TLS certificates stored in `/tmp/` directory

**Severity:** LOW  
**File:** [cmd/operator/webhook.go](cmd/operator/webhook.go#L93)

```go
cmd.Flags().StringVar(&certDir, "cert-dir", "/tmp/k8s-webhook-server/serving-certs",
    "Directory for TLS certificates")
```

The default certificate directory is under `/tmp/`. While the webhook deployment mounts a read-only secret volume at this path (overriding the default), the underlying default in the Go code points to `/tmp/` which:

1. Is world-readable on most systems (1777 permissions).
2. Could leak key material if the volume mount is misconfigured.
3. The cert-controller writes the cert/key here initially before the volume mount is active.

The deployment manifest mitigates this by mounting the Secret volume read-only:
```yaml
volumeMounts:
- name: cert
  mountPath: /tmp/k8s-webhook-server/serving-certs
  readOnly: true
```

**Recommendation:** Change the default to a non-`/tmp` path such as `/var/run/webhook-certs/` or `/etc/webhook/certs/`. This is the established controller-runtime convention, but it goes against security best practices for key material.

---

### P4-14: Node-slice-controller (deprecated) has empty `securityContext`

**Severity:** LOW  
**File:** [doc/crds/node-slice-controller.yaml](doc/crds/node-slice-controller.yaml#L57)

```yaml
securityContext: {}
```

Unlike the operator and webhook deployments which set `allowPrivilegeEscalation: false`, `readOnlyRootFilesystem: true`, and `runAsNonRoot: true`, the deprecated node-slice-controller has an empty security context. It runs as root with default capabilities and a writable root filesystem.

While marked as deprecated (superseded by the operator), this manifest may still be in use by deployments that haven't migrated.

**Recommendation:** Add the same security context as the operator deployment, or add a prominent deprecation notice with migration instructions.

---

### P4-15: No NetworkPolicy definitions for webhook or operator

**Severity:** LOW  
**Files:** [doc/crds/](doc/crds/) (all deployment manifests)

No NetworkPolicy resources are defined. The webhook server (port 9443), metrics endpoints (8080, 8082), and health probe endpoints (8081, 8083) are reachable from any pod in the cluster.

An attacker with a foothold in any namespace can:
- Probe the webhook for information disclosure via error messages
- Scrape metrics for internal state information (see P4-9)
- Potentially DOS the webhook by flooding it with invalid requests (though `failurePolicy: Fail` means this would block all CRD mutations)

**Recommendation:** Ship a NetworkPolicy that:
- Allows ingress to port 9443 only from the API server
- Allows ingress to metrics ports only from the monitoring namespace
- Denies all other ingress

---

### P4-16: `replace` directive pins gogo/protobuf — may mask security patches

**Severity:** LOW  
**File:** [go.mod](go.mod#L96)

```
replace github.com/gogo/protobuf => github.com/gogo/protobuf v1.3.2
```

The `replace` directive forces `gogo/protobuf` to v1.3.2 regardless of what transitive dependencies request. This library is deprecated and has had security issues. The `replace` directive prevents `go mod tidy` from picking up newer versions if any dependency updates its requirement.

**Recommendation:** Evaluate whether this replace is still necessary. If it is (for build compatibility), add a comment documenting why and set a reminder to audit when `gogo/protobuf` is fully removed from the dependency tree.

---

### P4-17: Webhook `failurePolicy: Fail` creates a DoS vector

**Severity:** LOW  
**File:** [doc/crds/validatingwebhookconfiguration.yaml](doc/crds/validatingwebhookconfiguration.yaml#L24)

```yaml
failurePolicy: Fail
```

All three webhooks use `failurePolicy: Fail`. If the webhook server is down (crash loop, cert rotation in progress, node failure), **all** IPPool, NodeSlicePool, and OverlappingRangeIPReservation CREATE/UPDATE operations are blocked cluster-wide. This means:

- The CNI plugin cannot allocate or update IPs (even though the CEL bypass exists — if the API server can't reach the webhook at all, `Fail` policy applies before matchConditions are evaluated)
- The operator cannot reconcile IPPools
- Pod creation is blocked for any pod using Whereabouts

The `certReady` gate in `Setup.Start()` means the webhook returns 503 during cert bootstrap, triggering the Fail policy.

**Recommendation:** Consider `failurePolicy: Ignore` for the IPPool webhook specifically (since the CNI SA is already bypassed via CEL), or ensure HA (the current `replicas: 2` helps but doesn't cover complete outage). At minimum, add a PodDisruptionBudget.

---

### P4-18: CNI ClusterRole has `delete` on OverlappingRangeIPReservation — broader than needed

**Severity:** LOW  
**File:** [doc/crds/daemonset-install.yaml](doc/crds/daemonset-install.yaml#L32-L34)

```yaml
- apiGroups: [whereabouts.cni.cncf.io]
  resources: [ippools, overlappingrangeipreservations]
  verbs: [get, list, watch, create, update, patch, delete]
```

The CNI ServiceAccount `whereabouts` has full `create, update, patch, delete` on both `ippools` and `overlappingrangeipreservations`. Compromise of any node running the DaemonSet (which runs on **every** node, including workers, due to the `Exists` toleration) gives the attacker the ability to:

- Delete all IPPool CRDs (mass IP deallocation → all pods lose their IPs on next reconciliation)
- Create bogus IP reservations (DoS by exhausting the pool)
- Modify allocations to point to attacker-controlled pods

This is an inherent risk of CNI plugins that need CRUD access, but the `delete` verb on `ippools` is suspicious — the CNI plugin should only add/remove individual allocations (via `update`/`patch`), not delete entire pools.

**Recommendation:** Remove `delete` from `ippools` in the CNI ClusterRole. The CNI binary never deletes an entire IPPool — it only updates allocations within one. Only the operator/reconciler should have `delete` on IPPools.

---

## Summary

| ID | Title | Severity | Category |
|---|---|---|---|
| P4-4 | Webhook ClusterRole grants unrestricted Secrets access | HIGH | RBAC |
| P4-5 | DaemonSet runs fully privileged | HIGH | Container Security |
| P4-6 | Dockerfile has no non-root USER | MEDIUM | Container Security |
| P4-7 | CEL matchConditions bypass — impersonation risk | MEDIUM | Webhook Security |
| P4-8 | Missing `capabilities.drop` and seccomp profile | MEDIUM | Container Security |
| P4-9 | Metrics endpoints unauthenticated | MEDIUM | Network Exposure |
| P4-10 | `:latest` image tag in deployment manifests | MEDIUM | Supply Chain |
| P4-11 | SA token on host filesystem | MEDIUM | Secrets |
| P4-12 | `SKIP_TLS_VERIFY` option exists | LOW | TLS |
| P4-13 | TLS certs default to `/tmp/` | LOW | Cert Handling |
| P4-14 | Deprecated controller has empty securityContext | LOW | Container Security |
| P4-15 | No NetworkPolicy for webhook/operator | LOW | Network Exposure |
| P4-16 | `replace` directive may mask security patches | LOW | Supply Chain |
| P4-17 | `failurePolicy: Fail` creates DoS vector | LOW | Availability |
| P4-18 | CNI ClusterRole has unnecessary `delete` on IPPools | LOW | RBAC |

### Priority Actions

1. **Immediate:** Scope the webhook Secret RBAC to a namespaced Role with `resourceNames` (P4-4).
2. **Short-term:** Add `capabilities.drop: [ALL]` and `seccompProfile` to operator and webhook (P4-8). Remove `delete` from CNI IPPool RBAC (P4-18). Add non-root USER to Dockerfile (P4-6).
3. **Medium-term:** Split DaemonSet into privileged init + unprivileged main (P4-5). Add NetworkPolicy (P4-15). Pin image tags in manifests (P4-10).
4. **Ongoing:** Audit CEL bypass expressions when namespace changes (P4-7). Monitor cert-controller for CVEs (P4-16).
