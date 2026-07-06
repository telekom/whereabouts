# whereabouts
[![Build](https://github.com/telekom/whereabouts/actions/workflows/build.yml/badge.svg)](https://github.com/telekom/whereabouts/actions/workflows/build.yml) [![Test](https://github.com/telekom/whereabouts/actions/workflows/test.yml/badge.svg)](https://github.com/telekom/whereabouts/actions/workflows/test.yml) ![Go report card](https://goreportcard.com/badge/github.com/telekom/whereabouts)

![whereabouts-logo](doc/logo.png)

> **Fork notice:** This is a fork of [k8snetworkplumbingwg/whereabouts](https://github.com/k8snetworkplumbingwg/whereabouts), maintained by the **Deutsche Telekom T-CAAS team**. At this point, this fork is effectively an API-compatible rewrite — the operator, reconciliation, webhook validation, storage backend, and overall architecture have been redesigned from scratch to address a number of fundamental design and implementation shortcomings in the upstream project. These changes are unlikely to be backported, as they would constitute a near-complete replacement of the original codebase. For the upstream project, see the link above.

An IP Address Management (IPAM) CNI plugin that assigns IP addresses cluster-wide.

If you need a way to assign IP addresses dynamically across your cluster -- Whereabouts is the tool for you. If you've found that you like how the [host-local](https://github.com/containernetworking/plugins/tree/master/plugins/ipam/host-local) CNI plugin works, but, you need something that works across all the nodes in your cluster (`host-local` only knows how to assign IPs to pods on the same node) -- Whereabouts is just what you're looking for.

Whereabouts can be used for both IPv4 & IPv6 addressing.

## Introduction

CNI (Container Network Interface) plugins typically have a configuration element named `ipam`. CNI IPAM plugins can assign IP addresses, and Whereabouts assigns IP addresses within a range -- without having to use a DHCP server. 

Whereabouts takes an address range, like `192.168.2.0/24` in CIDR notation, and will assign IP addresses within that range. In this case, it will assign IP addresses from `192.168.2.1` to `192.168.2.254`. When an IP address is assigned to a pod, Whereabouts tracks that IP address in a data store for the lifetime of that pod. When the pod is removed, Whereabouts then frees the address and makes it available to assign on subsequent requests. Whereabouts always assigns the lowest value address that's available in the range.

You can also specify ranges to exclude from assignment, so if for example you'd like to assign IP addresses within the range `192.168.2.0/24`, you can exclude IP addresses within it by adding them to an exclude list. For example, if you decide to exclude the range `192.168.2.0/28`, the first IP address assigned in the range will be `192.168.2.16`.

In respect to the old equipment out there that doesn't think that IP addresses that end in `.0` are valid -- Whereabouts will not assign addresses that end in `.0`.

The original inspiration for Whereabouts comes from when users have tried to use the samples from [Multus CNI](https://github.com/intel/multus-cni) (a CNI plugin that attaches multiple network interfaces to your pods), which includes examples that use the `host-local` plugin, and they find that it's... Almost the right thing. Sometimes people even assume it'll work across nodes -- and then wind up with IP address collisions.

This fork is designed and tested for Kubernetes clusters.

To track which IP addresses are in use between nodes, Whereabouts uses Kubernetes [Custom Resources](https://kubernetes.io/docs/concepts/extend-kubernetes/api-extension/custom-resources/#custom-resources) (CRDs) as its supported storage backend. No external dependencies like etcd are required.

Issues and PRs are welcome! Some of the known limitations are found at the bottom of the README.

## Prerequisites

* Kubernetes 1.28+
* [Multus CNI](https://github.com/k8snetworkplumbingwg/multus-cni) installed on the cluster (Whereabouts is used as a secondary IPAM plugin via Multus `NetworkAttachmentDefinition`s)
* A secondary CNI plugin (e.g., macvlan, ipvlan, bridge) configured through Multus
* `kubectl` configured with cluster-admin access

## Support Matrix

| Area | Supported | Not supported / not CI-covered |
|------|-----------|--------------------------------|
| Kubernetes | 1.28+ | Older clusters are unsupported. |
| Node operating system | Linux Kubernetes nodes | Windows nodes and non-Linux CNI installs are not built or tested. |
| Container images | `linux/amd64`, `linux/arm64` | Other image platforms are not published. |
| Release binary architectures | `amd64`, `arm64`, `arm` Linux builds | macOS and Windows binaries are not part of the release workflows. |
| Install methods | Kustomize via `make deploy`; Helm 3 OCI chart | Other package managers are not maintained here. |
| Storage backend | Kubernetes CRDs | External etcd or flat-file datastores are not supported by this fork. |
| IPAM modes | IPv4, IPv6, multi-IP, and dual-stack through `range` and `ipRanges` | Fast IPAM with `ipRanges` or dual-stack node slices is not supported. |
| Fast IPAM | Experimental, top-level `range` plus `node_slice_size` | Multi-range and dual-stack Fast IPAM configurations should use standard IPAM instead. |

## Installation

There's three steps to installing Whereabouts:

* Installing Whereabouts itself (it's just a binary on disk).
* Installing the Whereabouts operator (handles IP reconciliation, node-slice management, and webhooks).
* Creating IPAM CNI configurations.

Further installation options and configuration parameters can be found in the [extended configuration document](doc/extended-configuration.md).

### Installing Whereabouts.

You can install the full stack (CRDs + DaemonSet + operator + webhooks) with:

```
git clone https://github.com/telekom/whereabouts && cd whereabouts
make deploy
```

Or install components individually:

```
make install    # CRDs only
make deploy     # CRDs + DaemonSet + operator + webhooks
```

### Installing the Whereabouts Operator

The operator runs reconcilers (IP pool cleanup, node-slice management) and
serves validating webhooks from the same deployment. Leader election ensures
only one replica runs reconcilers, while all replicas serve webhooks.

The operator is included in `make deploy`. No separate installation is needed.

The DaemonSet and operator manifests are supported on Kubernetes 1.28+ Linux nodes.

### Installing with helm 3
You can also install whereabouts with helm 3:

Helm chart versions do not include the release tag's leading `v`. For example,
use chart version `0.0.7` for the `v0.0.7` application release.

```
# Install from OCI registry
helm install whereabouts oci://ghcr.io/telekom/whereabouts-chart --version <CHART_VERSION>

# Or use template to render manifests locally
helm template whereabouts oci://ghcr.io/telekom/whereabouts-chart --version <CHART_VERSION>
```

Helm will install the CRDs as well as the daemonset, operator, and webhooks.

### Upgrading

For **kubectl-based** installations, re-apply the manifests with the new version:

```
git pull
make kustomize
(cd config/manager && ../../bin/kustomize edit set image controller=ghcr.io/telekom/whereabouts:v<APP_VERSION>)
(cd config/daemonset && ../../bin/kustomize edit set image controller=ghcr.io/telekom/whereabouts:v<APP_VERSION>)
make deploy
```

The static Kustomize manifests default to the `latest` image tag. `make deploy`
does not consume an `IMG` variable; set the image in the manager and daemonset
Kustomize components, or use Helm when installing a packaged release.

For **Helm**:
```
helm upgrade whereabouts oci://ghcr.io/telekom/whereabouts-chart --version <CHART_VERSION>
```

### Uninstalling

For **kubectl-based** installations:
```
make undeploy
```

> **Note:** CRDs are not deleted by default to prevent data loss. Remove them manually with `make uninstall` if no longer needed.

For **Helm**:
```
helm uninstall whereabouts
```

## Example IPAM Config

Included here is an entire CNI configuration. Whereabouts only cares about the `ipam` section of the CNI config. In particular this example uses the `macvlan` CNI plugin. (If you decide to copy this block and try it too, make sure that the `master` setting is set to a network interface name that exists on your nodes). Typically, you'll already have a CNI configuration for an existing CNI plugin in your cluster, and you'll just copy the `ipam` section and modify the values there.

```
{
      "cniVersion": "0.3.0",
      "name": "whereaboutsexample",
      "type": "macvlan",
      "master": "eth0",
      "mode": "bridge",
      "ipam": {
        "type": "whereabouts",
        "range": "192.168.2.225/28",
        "exclude": [
           "192.168.2.229/30",
           "192.168.2.236/32"
        ]
      }
}
```

### An example configuration using a `NetworkAttachmentDefinition`

Whereabouts is particularly useful in scenarios where you're using additional network interfaces for Kubernetes. A `NetworkAttachmentDefinition` custom resource can be used with a CNI meta plugin such as [Multus CNI](https://github.com/intel/multus-cni) to attach multiple interfaces to your pods in Kubernetes.

In short, a `NetworkAttachmentDefinition` contains a CNI configuration packaged into a custom resource. Here's an example of a `NetworkAttachmentDefinition` containing a CNI configuration which uses Whereabouts for IPAM:

```
apiVersion: "k8s.cni.cncf.io/v1"
kind: NetworkAttachmentDefinition
metadata:
  name: whereabouts-conf
spec:
  config: '{
      "cniVersion": "0.3.0",
      "name": "whereaboutsexample",
      "type": "macvlan",
      "master": "eth0",
      "mode": "bridge",
      "ipam": {
        "type": "whereabouts",
        "range": "192.168.2.225/28"
      }
    }'
```

### Example IPv6 Config

The same applies for the usage of IPv6:

```
{
      "cniVersion": "0.3.0",
      "name": "whereaboutsexample",
      "type": "macvlan",
      "master": "eth0",
      "mode": "bridge",
      "ipam": {
        "type": "whereabouts",
        "range": "2001::0/116",
        "gateway": "2001::f:1"
      }
}
```

### Example IPAM config for assigning multiple IP addresses

`ipRanges` field can be used to provide a list of range configurations for assigning multiple IP addresses.

```
{
      "cniVersion": "0.3.0",
      "name": "whereaboutsexample",
      "type": "macvlan",
      "master": "eth0",
      "mode": "bridge",
      "ipam": {
        "type": "whereabouts",
        "ipRanges": [{
            "range": "192.168.10.1/24"
          }, {
            "range": "176.168.10.1/16"
        }]
      }
}
```

The above can also be used in combination with basic `range` field as below:

```
{
      "cniVersion": "0.3.0",
      "name": "whereaboutsexample",
      "type": "macvlan",
      "master": "eth0",
      "mode": "bridge",
      "ipam": {
        "type": "whereabouts",
        "ipRanges": [{
            "range": "192.168.10.1/24"
          }, {
            "range": "176.168.10.1/16"
        }],
        "range": "abcd::1/64"
      }
}
```

### Example DualStack config

Similar to above, `ipRanges` can be used for configuring DualStack

```
{
      "cniVersion": "0.3.0",
      "name": "whereaboutsexample",
      "type": "macvlan",
      "master": "eth0",
      "mode": "bridge",
      "ipam": {
        "type": "whereabouts",
        "ipRanges": [{
            "range": "192.168.10.1/24"
          }, {
            "range": "abcd::1/64"
        }]
      }
}
```

## Fast IPAM by Using Preallocated Node Slices [Experimental]

**Enhance IPAM performance in large-scale Kubernetes environments by reducing IP allocation contention through node-based IP slicing.**

### Fast IPAM Configuration

```
apiVersion: "k8s.cni.cncf.io/v1"
kind: NetworkAttachmentDefinition
metadata:
  name: whereabouts-fast-ipam
spec:
  config: '{
    "cniVersion": "0.3.0",
    "name": "whereaboutsexample",
    "type": "macvlan",
    "master": "eth0",
    "mode": "bridge",
    "ipam": {
      "type": "whereabouts",
      "range": "192.168.0.0/16",
      "node_slice_size": "/24"
    }
  }'
```

This setup enables the fast IPAM feature to optimize IP allocation for nodes, improving network performance in clusters with high pod density.
Please note, you must run the whereabouts operator for this to work. The operator is deployed automatically with `make deploy`.
You must run your whereabouts daemonset and whereabouts operator in the same namespaces as your network-attachment-definitions.
The field in the example `node_slice_size` determines how large of a CIDR to allocate per node and the existence of the field is what triggers
`Fast IPAM` mode.

Fast IPAM currently supports a single top-level `range` only. Do not combine
`node_slice_size` with `ipRanges`; use standard IPAM for multi-IP or dual-stack
allocations.

When `network_name` is set, all matching NetworkAttachmentDefinitions share the
same NodeSlicePool. When it is not set, the NodeSlicePool name is derived from
the configured range so different NADs can use the same embedded CNI `name`
without colliding.

A single NodeSlicePool supports up to 16,384 slices. Configurations that would
produce more slices are rejected before the controller creates or updates the
pool, because very large allocation lists can exceed Kubernetes object-size
limits.

### Service CIDR collision warnings

The operator can warn when an `IPPool` range overlaps a Kubernetes Service CIDR.
By default this check is disabled: `--service-cidr` is empty, and Helm renders no
operator flag because `operator.serviceCIDRs` defaults to `[]`.

For Helm installs, configure one or more service CIDRs with chart values:

```
helm upgrade --install whereabouts oci://ghcr.io/telekom/whereabouts-chart \
  --version <CHART_VERSION> \
  --set 'operator.serviceCIDRs[0]=10.96.0.0/12'
```

For static or Kustomize installs, add the equivalent comma-separated flag to the
operator deployment args, for example `--service-cidr=10.96.0.0/12`.

When configured, each IPPool reconciliation compares `spec.range` with the
configured service CIDRs. If a range overlaps, the operator logs the overlap,
emits a Kubernetes Warning event with reason `ServiceCIDROverlap`, and stores an
internal `whereabouts.cni.cncf.io/service-cidr-overlap` annotation so repeated
reconciles do not spam duplicate warnings. It does not reject the IPPool or
change IP allocation data.


## Core Parameters

**Required**

These parameters are required:

* `type`: This should be set to `whereabouts`.
* `range`: This specifies the range in which IP addresses will be allocated.

If for example the `range` is set to `192.168.2.225/28`, this will allocate IP addresses in the range excluding the first network address and the last broadcast address.

If you need a tool to figure out the range of a given CIDR address, try this online tool, [subnet-calculator.com](http://www.subnet-calculator.com/) or an [IPv6 subnet calculator](https://www.vultr.com/resources/subnet-calculator-ipv6/).

**Range end syntax**

Additionally, the `range` parameter can support a CIDR notation that includes the last IP to use. Example: `range: "192.168.2.225-192.168.2.230/28"`.

**Optional**

The following parameters are optional:

* `range_start` : First IP to use when allocating from the `range`. Optional, if unset is inferred from the `range`.
* `range_end` : Last IP to use when allocating from the `range`. Optional, if unset the last ip within the range is determined.
* `pick_addresses`: Ordered list of candidate IPs to allocate from within the `range`. Candidates outside the effective range, excluded ranges, or existing reservations are skipped.
* `exclude`: This is a list of CIDRs to be excluded from being allocated. 

In the example, we exclude IP addresses in the range `192.168.2.229/30` from being allocated (in this case it's 3 addresses, `.229, .230, .231`), as well as `192.168.2.236/32` (just a single address).

*Note 1*: It's up to you to properly set exclusion ranges that are within your subnet, there's no double checking for you (other than that the CIDR notation parses).
*Note 2*: Wide IPv6 CIDRs are addressable. For operationally large pools, use `range_start`, `range_end`, or `exclude` to keep allocation searches bounded to the addresses you intend to hand out.

Additionally -- you can set the route, gateway and DNS using anything from the configurations for the [static IPAM plugin](https://github.com/containernetworking/plugins/tree/master/plugins/ipam/static) (as well as additional static IP addresses). Static addresses must be outside the managed Whereabouts ranges.

### Overlapping Ranges

The overlapping ranges feature is enabled by default, and will not allow an IP address to be re-assigned across two different ranges which overlap. However, this can be disabled.

* `enable_overlapping_ranges`: *(boolean)* Checks to see if an IP has been allocated across another range before assigning it (defaults to `true`).

Please note: This feature is only implemented for the Kubernetes storage backend.

### Network names

By default, it is not possible to configure the same CIDR range twice and have whereabouts assign from the ranges
independently. However, this is useful in multi-tenant situations where more than one group is responsible for
selecting CIDR ranges.

By using parameter `network_name` *(string)*, administrators can tell whereabouts to assign IP addresses for the same
CIDR range multiple times.

Parameter `enable_overlapping_ranges` (see above) is scoped per network name.

```
(...)
    "network_name": "network-with-independent-allocation",
    "enable_overlapping_ranges": true,
(...)
```

## Building

Run the build command from the `./hack` directory:

```
make build
```

## Running whereabouts CNI in a local kind cluster

You can start a kind cluster to run local changes with:
```
make kind
# or make kind COMPUTE_NODES=<desired number of worker nodes>
```

You can then create a NetworkAttachmentDefinition with:
```
cat <<'EOF' | kubectl apply -f -
apiVersion: "k8s.cni.cncf.io/v1"
kind: NetworkAttachmentDefinition
metadata:
  name: whereabouts-conf
spec:
  config: '{
      "cniVersion": "0.3.0",
      "name": "whereaboutsexample",
      "type": "macvlan",
      "master": "eth0",
      "mode": "bridge",
      "ipam": {
        "type": "whereabouts",
        "range": "192.168.2.225/28"
      }
    }'
EOF
```

Create a deployment that uses the NetworkAttachmentDefinition, for example:
```
cat <<'EOF' | kubectl apply -f -
apiVersion: apps/v1
kind: Deployment
metadata:
  name: netshoot-deployment
  labels:
    app: netshoot-deployment
spec:
  replicas: 1
  selector:
    matchLabels:
      app: netshoot-pod
  template:
    metadata:
      annotations:
        k8s.v1.cni.cncf.io/networks: whereabouts-conf
      labels:
        app: netshoot-pod
    spec:
      containers:
      - name: netshoot
        image: nicolaka/netshoot
        command:
          - sleep
          - "3600"
        imagePullPolicy: IfNotPresent
EOF
```

## Acknowledgements

Thanks big time to [Tomofumi Hayashi](https://github.com/s1061123), I utilized his [static CNI IPAM plugin](https://github.com/containernetworking/plugins/tree/master/plugins/ipam/static) as a basis for this project to give me a head start!

The typeface used in the logo is [AZONIX](https://www.dafont.com/azonix.font), by [MixoFX](https://twitter.com/MixoFX).

## Known limitations

* Unlikely to work in Canada, apparently it would have to be "where aboots?" for Canadians to be able to operate it.

## API Stability

The IPAM configuration format (JSON `ipam` block) is stable and follows semver. CRD API types in `whereabouts.cni.cncf.io/v1alpha1` are pre-GA: while we avoid breaking changes, the `v1alpha1` designation means the schema may evolve. The internal Go packages (`pkg/allocate`, `pkg/storage`, etc.) are not part of the public API.

## Troubleshooting

| Symptom | Likely Cause | Resolution |
|---------|-------------|------------|
| `could not allocate IP in range` | IP pool exhausted | Expand the CIDR range or check for orphaned allocations: `kubectl get ippools -A -o yaml` |
| `IPAM type must be 'whereabouts'` | Wrong IPAM type in NAD config | Verify `"type": "whereabouts"` in the `ipam` block of your `NetworkAttachmentDefinition` |
| `kubernetes.kubeconfig path is required` | Missing CNI kubeconfig path | Provide `kubeconfig` in the IPAM config or in `whereabouts.conf`; the DaemonSet install generates `/etc/cni/net.d/whereabouts.d/whereabouts.kubeconfig` for in-cluster use |
| Duplicate IPs across pods | CNI binary crash during allocation | The operator's IPPool reconciler automatically cleans up orphaned allocations within its reconcile interval |
| Webhook rejects valid CRDs | Webhook outage or misconfiguration | Check webhook pod logs; the static manifest uses `failurePolicy: Ignore` to avoid blocking CNI operations |
| Slow IP allocation in large clusters | Contention on IPPool CRD | Enable Fast IPAM with `node_slice_size` to pre-allocate per-node IP slices (see [extended configuration](doc/extended-configuration.md)) |
