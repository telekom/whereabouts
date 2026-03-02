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

Not that we're also including a Custom Resource Definition (CRD) to use the `kubernetes` datastore option. This installs the kubernetes CRD specification for the `ippools.whereabouts.cni.k8s.io/v1alpha1` type.

### Example etcd datastore configuration

If you'll use the etcd datastore option, you'll likely want to install etcd first. Etcd installation suggestions follow below.

*NOTE*: You'll almost certainly want to change `etcd_host`.

```
{
      "cniVersion": "0.3.0",
      "name": "whereaboutsexample",
      "type": "macvlan",
      "master": "eth0",
      "mode": "bridge",
      "ipam": {
        "type": "whereabouts",
        "etcd_host": "example-etcd-cluster-client.cluster.local:2379",
        "range": "192.168.2.225/28",
        "exclude": [
           "192.168.2.229/30",
           "192.168.2.236/32"
        ],
        "log_file" : "/tmp/whereabouts.log",
        "log_level" : "debug",
        "gateway": "192.168.2.1"
      }
}
```


### etcd Parameters

**Required:**
* `etcd_host`: This is a connection string for your etcd hosts. It can take a single address or a list, or any other valid etcd connection string.

**Optional:**
* `etcd_username`: Basic Auth username to use when accessing the etcd API.
* `etcd_password`: Basic Auth password to use when accessing the etcd API.
* `etcd_key_file`: Path to the file containing the etcd private key matching the CNI plugin’s client certificate.
* `etcd_cert_file`: Path to the file containing the etcd client certificate issued to the CNI plugin.
* `etcd_ca_cert_file`: Path to the file containing the root certificate of the certificate authority (CA) that issued the etcd server certificate.

### Logging Parameters

There are two optional parameters for logging, they are:

* `log_file`: A file path to a logfile to log to.
* `log_level`: Set the logging verbosity, from most to least: `debug`,`error`,`panic`

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


