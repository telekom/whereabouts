# Extended configuration

Should you need to further configure Whereabouts, you might find these options valuable.

## IP Reconciliation

Whereabouts includes an IP reconciliation mechanism that continuously scans
allocated IP addresses, reconciles them against currently running pods, and
deallocates IP addresses which have been left stranded.

Stranded IP addresses can occur due to node failures (e.g. a sudden power off /
reboot event) or potentially from pods that have been force deleted
(e.g. `kubectl delete pod foo --grace-period=0 --force`)

The reconciler runs as part of the **whereabouts-operator** (see
`doc/crds/operator-install.yaml`). The reconciliation interval is configured
via the `--reconcile-interval` flag on the operator's `controller` subcommand
(default: `30s`).

## Installation options

The daemonset installation as shown on the README is for use with Kubernetes version 1.16 and later. It may also be useful with previous versions, however you'll need to change the `apiVersion` of the daemonset in the provided yaml, [see the deprecation notice](https://kubernetes.io/blog/2019/07/18/api-deprecations-in-1-16/).

You can compile from this repo (with `./hack/build-go.sh`) and copy the resulting binary onto each node in the `/opt/cni/bin` directory (by default).

Note that we're also including a Custom Resource Definition (CRD) to use the `kubernetes` datastore option. This installs the kubernetes CRD specification for the `ippools.whereabouts.cni.cncf.io/v1alpha1` type.

### Logging Parameters

There are two optional parameters for logging, they are:

* `log_file`: A file path to a logfile to log to.
* `log_level`: Set the logging verbosity, from most to least: `debug`,`verbose`,`error`,`panic`

## Flatfile configuration

During installation using the daemonset-style install, Whereabouts creates a configuration file @ `/etc/cni/net.d/whereabouts.d/whereabouts.conf`. Any parameter that you do not wish to repeatedly put into the `ipam` section of a CNI configuration can be put into this file (such as Kubernetes configuration parameters or logging).

There is one option for flat file configuration:

* `configuration_path`: A file path to a Whereabouts configuration file.

If you're using [Multus CNI](http://multus-cni.io/) or another meta-plugin, you may wish to reduce the number of parameters you need to specify in the IPAM section by putting commonly used options into a flat file -- primarily to make it simpler to type and to reduce having to copy and paste the same parameters repeatedly.

Whereabouts will look for the configuration in these locations, in this order:

* The location specified by the `configuration_path` option.
* `/etc/kubernetes/cni/net.d/whereabouts.d/whereabouts.conf`
* `/etc/cni/net.d/whereabouts.d/whereabouts.conf`

You may specify the `configuration_path` to point to another location should it be desired.

Any options added to the `whereabouts.conf` are overridden by configuration options that are in the primary CNI configuration (e.g. in a custom resource `NetworkAttachmentDefinition` used by Multus CNI or in the first file ASCII-betically in the CNI configuration directory -- which is `/etc/cni/net.d/` by default).


### Example flat file configuration

You can reduce the number of parameters used if you need to make more than one Whereabouts configuration (such as if you're using [Multus CNI](http://multus-cni.io/))

Create a file named `/etc/cni/net.d/whereabouts.d/whereabouts.conf`, with the contents:

```
{
  "kubernetes": {
    "kubeconfig": "/etc/cni/net.d/whereabouts.d/whereabouts.kubeconfig"
  },
  "log_file": "/tmp/whereabouts.log",
  "log_level": "debug"
}
```

With that in place, you can now create an IPAM configuration that has a lot less options, in this case we'll give an example using a `NetworkAttachmentDefinition` as used with Multus CNI (or other implementations of the [Network Plumbing Working Group specification](https://github.com/k8snetworkplumbingwg/multi-net-spec))

An example configuration using a `NetworkAttachmentDefinition`:

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

You'll note that in the `ipam` section there's a lot less parameters than are used in the previous examples.

## Reconciler Interval Configuration (optional)

The IP reconciler runs as part of the whereabouts-operator. The reconciliation
interval can be configured via the `--reconcile-interval` flag on the operator's
`controller` subcommand. The default interval is `30s`.

To change it, edit the operator Deployment's command args:
```yaml
        command:
        - /whereabouts-operator
        - controller
        - --reconcile-interval=60s
```

## IPAM Configuration Reference

Below is a complete reference of all IPAM configuration parameters. All parameters
are specified inside the `"ipam"` object in CNI configuration JSON.

### Core Parameters

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `type` | string | yes | Must be `"whereabouts"` |
| `range` | string | yes* | CIDR notation for the IP range (e.g., `"192.168.2.0/24"`, `"2001:db8::/64"`) |
| `range_start` | string | no | First IP to allocate within the range |
| `range_end` | string | no | Last IP to allocate within the range |
| `exclude` | string[] | no | CIDRs to exclude from allocation |
| `gateway` | string | no | Gateway IP address for the interface |

*\*Required unless using `ipRanges`.*

### Multi-Range / Dual-Stack

| Parameter | Type | Description |
|-----------|------|-------------|
| `ipRanges` | object[] | Array of range objects for multi-IP or dual-stack allocation. Each element supports `range`, `range_start`, `range_end`, and `exclude`. |

Example dual-stack configuration:
```json
{
  "type": "whereabouts",
  "ipRanges": [
    {"range": "192.168.2.0/24"},
    {"range": "2001:db8::/64", "range_start": "2001:db8::10", "range_end": "2001:db8::ff"}
  ]
}
```

### Network Isolation

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `network_name` | string | `""` | Logical network name. Creates separate IPPool CRs per network, allowing the same CIDR range to be used independently in multi-tenant scenarios. |
| `enable_overlapping_ranges` | bool | `true` | Enables cluster-wide IP uniqueness checks via OverlappingRangeIPReservation CRDs. Prevents the same IP from being allocated across different ranges. |

### Fast IPAM (Experimental)

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `node_slice_size` | string | `""` | Prefix length for per-node IP slices (e.g., `"28"` or `"/28"`). Enables the experimental Fast IPAM feature, which pre-allocates IP slices per node to reduce allocation contention in large clusters. Requires the operator's NodeSliceReconciler (deployed via `doc/crds/operator-install.yaml`). Valid range: 1–128. |

### Leader Election

These parameters configure the leader election used during IP allocation. All values
are in **milliseconds**. Defaults are suitable for most deployments.

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `leader_lease_duration` | int | `1500` | Leader election lease duration (ms) |
| `leader_renew_deadline` | int | `1000` | Leader election renew deadline (ms) |
| `leader_retry_period` | int | `500` | Leader election retry period (ms) |

### Logging

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `log_file` | string | `""` | Path to the whereabouts log file. If empty, logs go to stderr. |
| `log_level` | string | `""` | Logging verbosity: `"debug"`, `"verbose"`, `"error"`, or `"panic"`. |

### Kubernetes Configuration

| Parameter | Type | Description |
|-----------|------|-------------|
| `kubernetes.kubeconfig` | string | Path to a kubeconfig file. If empty, in-cluster configuration is used. |

### Other

| Parameter | Type | Description |
|-----------|------|-------------|
| `configuration_path` | string | Path to a flat file configuration (see [Flatfile configuration](#flatfile-configuration)). Must not contain path traversal (`..`). |
| `sleep_for_race` | int | Debug parameter: adds artificial delay (seconds) before pool updates to simulate race conditions. Do not use in production. |

## Operator Configuration Examples

The operator binary (`whereabouts-operator`) supports two subcommands:

### Controller (reconcilers)

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: whereabouts-controller
spec:
  template:
    spec:
      containers:
      - name: whereabouts-controller
        image: ghcr.io/telekom/whereabouts:latest
        command:
        - /whereabouts-operator
        - controller
        # Reconciliation interval (default: 30s)
        - --reconcile-interval=60s
        # Health and metrics endpoints
        - --health-probe-bind-address=:8081
        - --metrics-bind-address=:8443
```

### Webhook server

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: whereabouts-webhook
spec:
  template:
    spec:
      containers:
      - name: whereabouts-webhook
        image: ghcr.io/telekom/whereabouts:latest
        command:
        - /whereabouts-operator
        - webhook
        # Webhook server port (default: 9443)
        - --webhook-port=9443
        # Health probe (default: :8081)
        - --health-probe-bind-address=:8082
        # TLS certificates are auto-rotated by cert-controller
```

For complete installation manifests, see `doc/crds/operator-install.yaml` and
`doc/crds/webhook-install.yaml`.


