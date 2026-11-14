# Logging

Whereabouts ships three binaries. Each has its own logger, level setting and
destination:

| Binary | Runs as | Logger | Destination | Level setting | Format setting | Default |
|--------|---------|--------|-------------|---------------|----------------|---------|
| `whereabouts` (CNI plugin) | Executed on the node by Multus / the container runtime for every CNI call | `pkg/logging` (text) | stderr of the calling process, plus an optional host file | IPAM `log_level` (Helm `cniConf.logLevel`) | none (text only) | `error` |
| `whereabouts-operator controller` | Deployment | controller-runtime zap | container stderr | `--log-level` (Helm `operator.logLevel`) | `--log-format` (Helm `operator.logFormat`) | `info`, `json` |
| `install-cni` | DaemonSet container | `log/slog` | container stdout | `LOG_LEVEL` (Helm `installCNI.logLevel`) | `LOG_FORMAT` (Helm `installCNI.logFormat`) | `info`, `json` |

The defaults produce the same output as releases before these settings
existed.

The [log reference](#log-reference) at the end of each section lists every
message the binary logs, generated from the code. A unit test fails when the
code and this document differ (see [Keeping this document current](#keeping-this-document-current)).

## Personal data

Pod names and namespaces (`podRef`), pod UIDs, container IDs, interface names
and allocated IP addresses identify customer workloads and are personal data.
They appear in:

- CNI plugin: every `verbose` and most `debug` messages, and the `error`
  messages about allocation, rollback and overlapping reservations.
- Operator: the `info` messages about orphaned allocations and reservations,
  the webhook `rejected` message (`reason` can quote the rejected IP range or
  podRef), and most `debug` messages.
- install-cni: none. It logs file paths and errors only.

Credentials are never logged. install-cni logs that the ServiceAccount token
changed, not its content.

## CNI plugin (`whereabouts`)

### Setting the level

The plugin reads its level from the IPAM configuration:

| Key | Values | Default | Effect |
|-----|--------|---------|--------|
| `log_level` | `debug`, `verbose`, `error`, `panic` | `error` | Lowest level written |
| `log_file` | absolute path under `/var/log` or `/tmp` on the node | none | Also append every line to this file |

Both keys can be set in two places. A value in the NetworkAttachmentDefinition's
IPAM configuration wins over the flat file:

1. Per network, in the NetworkAttachmentDefinition:

   ```json
   { "type": "whereabouts", "range": "192.168.2.0/24", "log_level": "verbose" }
   ```

2. For all networks on all nodes, in the flat file
   `whereabouts.d/whereabouts.conf`. install-cni writes this file and takes the
   values from its `CNI_LOG_LEVEL` and `CNI_LOG_FILE` environment variables,
   which the Helm chart sets from:

   ```yaml
   cniConf:
     logLevel: verbose          # debug, verbose, error or panic; "" = plugin default (error)
     logFile: /var/log/whereabouts.log
   ```

   `CNI_LOG_LEVEL` is case-insensitive. An unknown `CNI_LOG_LEVEL`, or a
   `CNI_LOG_FILE` that is not an absolute path under `/var/log` or `/tmp`, stops
   install-cni at startup. Empty values are not written, so the file is
   unchanged unless you set them.

An unknown `log_level` in the IPAM configuration is ignored: the plugin keeps
`error` and writes `Whereabouts logging: cannot set logging level to …` to
stderr. A `log_file` outside `/var/log` and `/tmp`, a symlink or a non-regular
file is rejected the same way, and the plugin logs to stderr only.

### Levels

Each level includes the levels above it.

| Level | What is logged |
|-------|----------------|
| `panic` | Nothing from `pkg/logging`: the plugin has no panic-level messages, so this silences its own log lines. It does not silence the client-go lines described in [Lines from client-go](#lines-from-client-go). |
| `error` (default) | Every failed CNI ADD, DEL and CHECK with its cause: configuration that cannot be loaded, Kubernetes client creation, IPPool and OverlappingRangeIPReservation reads and updates (with the retry attempt), exhausted ranges, node slice lookup, leader election setup, and rollbacks of partial multi-range allocations. DEL failures are logged per retry attempt. |
| `verbose` | One line per successful allocation (`ADD: allocated …`) and release (`DEL: released …`) with container ID and podRef. |
| `debug` | The full allocation trace: loaded configuration (a summary line with counts and flags, plus the configured ranges when a range has an invalid CIDR), the pod and interface of each call, flat file used, leader election steps, pool identifiers, every IP considered, reserved, skipped or already allocated, preferred-IP handling, overlapping reservation lookups (new and legacy names), stale reservation cleanup, retries and rollbacks, and CHECK results. |

### Output

Every line has the form

```text
2026-11-13T10:15:04Z [verbose] ADD: allocated 1 IP(s) for containerID "3f2a…" podRef "team-a/web-0"
```

with an RFC 3339 timestamp, the level in brackets and the message. Error
messages are logged with `%w` replaced by the error text.

The plugin writes each line to stderr and, with `log_file`, appends it to the
file. stderr belongs to the process that executes the plugin (Multus or the
container runtime); whether and where it is kept depends on that caller. Use
`log_file` for a dependable record on the node. The file is not rotated by
Whereabouts.

Independently of the level, a failed CNI call returns its error to the runtime,
which reports it as a pod event (for example `FailedCreatePodSandBox`).

The `stderr` rows below are written directly to stderr, regardless of
`log_level`, while the logging configuration is applied.

### Lines from client-go

Without `optimistic_ipam`, every ADD and DEL takes a Kubernetes Lease through
client-go leader election. client-go logs through klog, not `pkg/logging`, so
these lines ignore `log_level` (including `panic`), do not use the format
above and are not written to `log_file`. klog writes them to the plugin's
stderr in its own format, for example:

```text
I1113 10:15:04.123456   4711 leaderelection.go:258] "Attempting to acquire leader lease..." lock="<namespace>/whereabouts"
I1113 10:15:04.234567   4711 leaderelection.go:272] "Successfully acquired lease" lock="<namespace>/whereabouts"
```

Lease failures (for example `Error retrieving lease lock`, `Failed to update
lease`, `Failed to release lease`) appear the same way, at klog error level,
and other client-go warnings can too. The plugin's own leader election errors
are logged through `pkg/logging` as listed below. With `optimistic_ipam: true`
no lease is taken and the leader election lines do not appear. The lease is
`whereabouts`, or the node slice pool name with `node_slice_size`.

### Log reference

<!-- BEGIN GENERATED LOG TABLE: cni-plugin -->
| Level | Message | Values / fields | Source |
|---|---|---|---|
| stderr | `` Whereabouts logging: cannot open <filename> `` | - | pkg/logging/logging.go |
| stderr | `` Whereabouts logging: cannot set logging level to %s `` | `levelStr` | pkg/logging/logging.go |
| stderr | `` Whereabouts logging: error closing previous log file: %v `` | `closeErr` | pkg/logging/logging.go |
| stderr | `` Whereabouts logging: rejected log_file %q: %v `` | `filename`, `err` | pkg/logging/logging.go |
| error | `` CHECK: IP config %s from prevResult is not expected for containerID %q ifName %q `` | `key`, `args.ContainerID`, `args.IfName` | cmd/whereabouts/main.go |
| error | `` CHECK: could not parse prevResult: %w `` | `err` | cmd/whereabouts/main.go |
| error | `` CHECK: error reading pool %s: %w `` | `poolIdentifier.IPRange`, `err` | cmd/whereabouts/main.go |
| error | `` CHECK: error resolving pool for range %s: %w `` | `ipRange.Range`, `err` | cmd/whereabouts/main.go |
| error | `` CHECK: expected IP config %s for containerID %q ifName %q is missing from prevResult `` | `key`, `args.ContainerID`, `args.IfName` | cmd/whereabouts/main.go |
| error | `` CHECK: invalid pool range %s: %w `` | `poolIdentifier.IPRange`, `err` | cmd/whereabouts/main.go |
| error | `` CHECK: no allocation found for containerID %q ifName %q in range %s `` | `args.ContainerID`, `args.IfName`, `poolIdentifier.IPRange` | cmd/whereabouts/main.go |
| error | `` CHECK: transient error reading pool %s: %w `` | `poolIdentifier.IPRange`, `err` | cmd/whereabouts/main.go |
| error | `` Could not determine nodename and could not open /etc/hostname: %w `` | `err` | pkg/storage/kubernetes/ipam.go |
| error | `` DEL attempt %d/%d failed: %w `` | `attempt + 1`, `delMaxRetries`, `lastErr` | cmd/whereabouts/main.go |
| error | `` DEL failed after %d attempts: %w `` | `delMaxRetries`, `lastErr` | cmd/whereabouts/main.go |
| error | `` Error assigning IP: %w `` | `err` | pkg/storage/kubernetes/ipam.go |
| error | `` Error decoding ip offset (backend: kubernetes): invalid offset %q `` | `offset` | pkg/storage/kubernetes/ipam.go |
| error | `` Error getting cluster wide IP allocation: %w `` | `err` | pkg/storage/kubernetes/ipam.go |
| error | `` Error parsing node slice cidr to net.IPNet: %w `` | `err` | pkg/storage/kubernetes/ipam.go |
| error | `` Error parsing node slice cidr to range end: %w `` | `err` | pkg/storage/kubernetes/ipam.go |
| error | `` Error parsing node slice cidr to range start: %w `` | `err` | pkg/storage/kubernetes/ipam.go |
| error | `` Error performing UpdateOverlappingRangeAllocation (attempt: %d): %w `` | `j`, `err` | pkg/storage/kubernetes/ipam.go |
| error | `` Error reading file: %w `` | `err` | pkg/storage/kubernetes/ipam.go |
| error | `` Failed to create leader elector: %w `` | `err` | pkg/storage/kubernetes/ipam.go |
| error | `` Failed to get node hostname: %w `` | `err` | pkg/storage/kubernetes/ipam.go |
| error | `` GetIPRange request failed with: %w `` | `err` | pkg/allocate/allocate.go |
| error | `` IP allocation failed for range %s after %d attempts: %w `` | `configuredRange`, `attempts`, `err` | pkg/storage/kubernetes/ipam.go |
| error | `` IPAM client initialization error (attempt %d/%d): %w `` | `attempt + 1`, `delMaxRetries`, `err` | cmd/whereabouts/main.go |
| error | `` IPAM configuration load failed (DEL tolerant): %w `` | `err` | cmd/whereabouts/main.go |
| error | `` IPAM configuration load failed: %w `` | `err` | cmd/whereabouts/main.go |
| error | `` IPAM error getting OverlappingRangeStore: %w `` | `err` | pkg/storage/kubernetes/ipam.go |
| error | `` IPAM error reading pool allocations (attempt: %d): %w `` | `j`, `err` | pkg/storage/kubernetes/ipam.go |
| error | `` IPAM error updating pool (attempt: %d): %w `` | `j`, `err` | pkg/storage/kubernetes/ipam.go |
| error | `` Multi-range rollback failed for IP %s after %d attempt(s): %v `` | `c.ip`, `attempts`, `lastErr` | pkg/storage/kubernetes/ipam.go |
| error | `` ORIP rollback failed for IP %s after %d attempt(s): %v `` | `c.overlapIP`, `attempts`, `lastErr` | pkg/storage/kubernetes/ipam.go |
| error | `` Rollback re-read failed for IP %s (attempt %d): %v `` | `c.ip`, `attempt + 1`, `err` | pkg/storage/kubernetes/ipam.go |
| error | `` error at storage engine: %w `` | `err` | cmd/whereabouts/main.go |
| error | `` error deallocating IP: %w `` | `err` | cmd/whereabouts/main.go |
| error | `` error finding node within node slice allocations `` | - | pkg/storage/kubernetes/ipam.go |
| error | `` error getting node slice %s/%s %w `` | `ipam.Namespace`, `getNodeSliceName(ipam)`, `err` | pkg/storage/kubernetes/ipam.go |
| error | `` failed to close the connection to the K8s backend: %w `` | `err` | cmd/whereabouts/main.go |
| error | `` failed to create Kubernetes IPAM manager: %w `` | `err` | cmd/whereabouts/main.go |
| error | `` failed to delete stale ORIP for %v: %w `` | `newip.IP`, `delErr` | pkg/storage/kubernetes/ipam.go |
| error | `` invalid static address configuration: %w `` | `err` | cmd/whereabouts/main.go |
| error | `` k8s get OverlappingRangeIPReservation error (legacy): %w `` | `err` | pkg/storage/kubernetes/ipam.go |
| error | `` k8s get OverlappingRangeIPReservation error: %w `` | `err` | pkg/storage/kubernetes/ipam.go |
| error | `` merge error with flat file: %w `` | `err` | pkg/config/config.go |
| error | `` no IP addresses allocated - check IPAM configuration (ipRanges may be empty) `` | - | cmd/whereabouts/main.go |
| error | `` preferredIPInBounds: IsIPInRange failed: %v `` | `err` | pkg/allocate/allocate.go |
| error | `` preferredIPInBounds: effectiveIPRange failed: %v `` | `err` | pkg/allocate/allocate.go |
| verbose | `` ADD: allocated %d IP(s) for containerID %q podRef %q `` | `len(newips)`, `client.ContainerID`, `client.Config.GetPodRef()` | cmd/whereabouts/main.go |
| verbose | `` DEL: released IP(s) for containerID %q podRef %q `` | `client.ContainerID`, `client.Config.GetPodRef()` | cmd/whereabouts/main.go |
| debug | `` %s - IPAM configuration read: ranges=%d staticAddresses=%d routes=%d dnsNameservers=%d gatewayConfigured=%t networkNameSet=%t nodeSliceEnabled=%t overlappingRanges=%t optimisticIPAM=%t `` | `operation`, `len(ipamConf.IPRanges)`, `len(ipamConf.Addresses)`, `len(ipamConf.Routes)`, `len(ipamConf.DNS.Nameservers)`, `ipamConf.Gateway != nil`, `ipamConf.NetworkName != ""`, `ipamConf.NodeSliceSize != ""`, `ipamConf.OverlappingRanges`, `ipamConf.OptimisticIPAM` | cmd/whereabouts/main.go |
| debug | `` Assigning preferred IP: %q - container ID %q - podRef: %q - ifName: %q `` | `ipamConf.PreferredIP`, `containerID`, `podRef`, `ifName` | pkg/allocate/allocate.go |
| debug | `` Beginning IPAM for ContainerID: %q - podRef: %q - ifName: %q `` | `args.ContainerID`, `ipamConf.GetPodRef()`, `args.IfName` | cmd/whereabouts/main.go |
| debug | `` Beginning delete for ContainerID: %q - podRef: %q - ifName: %q `` | `args.ContainerID`, `ipamConf.GetPodRef()`, `args.IfName` | cmd/whereabouts/main.go |
| debug | `` CHECK: allocation verified for containerID %q ifName %q in range %s `` | `args.ContainerID`, `args.IfName`, `poolIdentifier.IPRange` | cmd/whereabouts/main.go |
| debug | `` CHECK: expected IP config %s confirmed in prevResult `` | `key` | cmd/whereabouts/main.go |
| debug | `` CHECK: prevResult IP config %s matches expected IPAM result `` | `key` | cmd/whereabouts/main.go |
| debug | `` Capping sleep_for_race from %d to %d seconds `` | `sleepSec`, `whereaboutstypes.MaxSleepForRace` | pkg/storage/kubernetes/ipam.go |
| debug | `` Continuing loop, IP is already allocated (possibly from another range): %v `` | `newip` | pkg/storage/kubernetes/ipam.go |
| debug | `` Continuing loop, IP is already allocated to podRef %q on ifName %q: %v `` | `ipamConf.GetPodRef()`, `overlappingRangeIPReservation.Spec.IfName`, `newip` | pkg/storage/kubernetes/ipam.go |
| debug | `` Deallocating given previously used IP: %v `` | `ip.String()` | pkg/allocate/allocate.go |
| debug | `` Deposed as leader, shutting down `` | - | pkg/storage/kubernetes/ipam.go |
| debug | `` Elected as leader, do processing `` | - | pkg/storage/kubernetes/ipam.go |
| debug | `` Finished leader election `` | - | pkg/storage/kubernetes/ipam.go |
| debug | `` Gateway %s excluded from allocation in all IP ranges `` | `n.IPAM.Gateway` | pkg/config/config.go |
| debug | `` Get overlappingRangewide allocation; normalized IP: %q, IP: %q, networkName: %q `` | `normalizedIP`, `ip`, `networkName` | pkg/storage/kubernetes/ipam.go |
| debug | `` IP already allocated for podRef: %q - ifName:%q - IP: %s `` | `podRef`, `ifName`, `r.IP.String()` | pkg/allocate/allocate.go |
| debug | `` IPManagement -- mode: %d / containerID: %q / podRef: %q / ifName: %q  `` | `mode`, `ipam.ContainerID`, `ipamConf.GetPodRef()`, `ipam.IfName` | pkg/storage/kubernetes/ipam.go |
| debug | `` IPManagement: %v, %v `` | `newips`, `err` | pkg/storage/kubernetes/ipam.go |
| debug | `` IPPool %s not found for deallocation, treating as already released `` | `IPPoolName(poolIdentifier)` | pkg/storage/kubernetes/ipam.go |
| debug | `` IterateForAssignment input >> range_start: %v \| range_end: %v \| ipnet: %v \| first IP: %v \| last IP: %v \| l3: %v `` | `rangeStart`, `rangeEnd`, `ipnet.String()`, `firstIP`, `lastIP`, `l3` | pkg/allocate/allocate.go |
| debug | `` K8s UpdateOverlappingRangeAllocation success on %v: %+v `` | `verb`, `clusteripres` | pkg/storage/kubernetes/ipam.go |
| debug | `` Legacy-format name %q is reserved; IP: %q, networkName: %q `` | `legacyName`, `ip`, `networkName` | pkg/storage/kubernetes/ipam.go |
| debug | `` New-format name %q not found on delete, trying legacy name %q `` | `normalizedIP`, `legacyName` | pkg/storage/kubernetes/ipam.go |
| debug | `` New-format name %q not found, trying legacy name %q `` | `normalizedIP`, `legacyName` | pkg/storage/kubernetes/ipam.go |
| debug | `` No allocation found for container ID %q in range %s, continuing to next range `` | `ipam.ContainerID`, `ipRange.Range` | pkg/storage/kubernetes/ipam.go |
| debug | `` No pool allocation found for container ID %q in range %s; retrying overlapping reservation cleanup for IP %s `` | `ipam.ContainerID`, `ipRange.Range`, `ipforoverlappingrangeupdate` | pkg/storage/kubernetes/ipam.go |
| debug | `` Normalized IP is reserved; normalized IP: %q, IP: %q, networkName: %q `` | `normalizedIP`, `ip`, `networkName` | pkg/storage/kubernetes/ipam.go |
| debug | `` ORIP rollback transient error for IP %s (attempt %d), retrying `` | `c.overlapIP`, `attempt + 1` | pkg/storage/kubernetes/ipam.go |
| debug | `` OnStartedLeading() called `` | - | pkg/storage/kubernetes/ipam.go |
| debug | `` OnStoppedLeading() called `` | - | pkg/storage/kubernetes/ipam.go |
| debug | `` PickForAssignment input >> range_start: %v \| range_end: %v \| ipnet: %v \| first IP: %v \| last IP: %v \| pick_addresses: %v \| l3: %v `` | `rangeStart`, `rangeEnd`, `ipnet.String()`, `firstIP`, `lastIP`, `pickAddresses`, `l3` | pkg/allocate/allocate.go |
| debug | `` Pod %s has invalid preferred-ip annotation: %q `` | `ipamConf.GetPodRef()`, `preferred` | pkg/storage/kubernetes/ipam.go |
| debug | `` Pod %s has preferred IP annotation: %s `` | `ipamConf.GetPodRef()`, `preferredIP` | pkg/storage/kubernetes/ipam.go |
| debug | `` Preferred IP %s not available, falling back to lowest-available `` | `ipamConf.PreferredIP` | pkg/allocate/allocate.go |
| debug | `` Reserving IP: %q - container ID %q - podRef: %q - ifName: %q `` | `ip.String()`, `containerID`, `podRef`, `ifName` | pkg/allocate/allocate.go |
| debug | `` Reserving picked IP: %q - container ID %q - podRef: %q - ifName: %q `` | `candidate.String()`, `containerID`, `podRef`, `ifName` | pkg/allocate/allocate.go |
| debug | `` Retrying DEL (attempt %d/%d) after %s `` | `attempt + 1`, `delMaxRetries`, `backoff` | cmd/whereabouts/main.go |
| debug | `` Rollback pool for IP %s no longer exists `` | `c.ip` | pkg/storage/kubernetes/ipam.go |
| debug | `` Rollback retry for IP %s skipping pool re-read: no IPAM reference available `` | `c.ip` | pkg/storage/kubernetes/ipam.go |
| debug | `` Rollback skipping IP %s: allocation no longer belongs to container %q podRef %q ifName %q `` | `c.ip`, `c.containerID`, `c.podRef`, `c.ifName` | pkg/storage/kubernetes/ipam.go |
| debug | `` Rollback transient error for IP %s (attempt %d), retrying `` | `c.ip`, `attempt + 1` | pkg/storage/kubernetes/ipam.go |
| debug | `` Rolled back allocation for IP %s `` | `c.ip` | pkg/storage/kubernetes/ipam.go |
| debug | `` Rolled back overlapping reservation for IP %s `` | `c.overlapIP` | pkg/storage/kubernetes/ipam.go |
| debug | `` Skipping delete of ORIP %q: stored UID %q differs from caller UID %q (stale reservation of another pod lifecycle) `` | `normalizedIP`, `existing.Spec.PodUID`, `podUID` | pkg/storage/kubernetes/ipam.go |
| debug | `` Skipping delete of legacy ORIP %q: stored UID %q differs from caller UID %q (stale reservation of another pod lifecycle) `` | `legacyName`, `existing.Spec.PodUID`, `podUID` | pkg/storage/kubernetes/ipam.go |
| debug | `` Stale ORIP for %v: stored UID %q differs from current UID %q; deleting stale reservation `` | `newip.IP`, `overlappingRangeIPReservation.Spec.PodUID`, `ipamConf.PodUID` | pkg/storage/kubernetes/ipam.go |
| debug | `` Started leader election `` | - | pkg/storage/kubernetes/ipam.go |
| debug | `` Used defaults from parsed flat file config @ %s `` | `foundflatfile` | pkg/config/config.go |
| debug | `` Using optimistic IPAM (no leader election) `` | - | cmd/whereabouts/main.go |
| debug | `` Using optimistic IPAM for deallocation (no leader election) `` | - | cmd/whereabouts/main.go |
| debug | `` discovered current hostname as: %s `` | `hostname` | pkg/storage/kubernetes/ipam.go |
| debug | `` excluding %v and moving to the end of the excluded range: %v `` | `subnet`, `broadcastIP` | pkg/allocate/allocate.go |
| debug | `` found matching node slice allocation for hostname %v: %v `` | `nodeName`, `allocation` | pkg/storage/kubernetes/ipam.go |
| debug | `` invalid cidr error on range %v, within ranges %v `` | `n.IPAM.IPRanges[idx].Range`, `n.IPAM.IPRanges` | pkg/config/config.go |
| debug | `` ipam namespace is %v `` | `ipam.Namespace` | pkg/storage/kubernetes/ipam.go |
| debug | `` listing IP pools `` | - | pkg/storage/kubernetes/client.go |
| debug | `` listing Pods `` | - | pkg/storage/kubernetes/client.go |
| debug | `` updating container ID: %q `` | `containerID` | pkg/allocate/allocate.go |
| debug | `` using lease with name: %v `` | `leaseName` | pkg/storage/kubernetes/ipam.go |
| debug | `` using pool identifier: %v `` | `poolIdentifier` | pkg/storage/kubernetes/ipam.go |
<!-- END GENERATED LOG TABLE: cni-plugin -->

## Operator (`whereabouts-operator controller`)

### Setting the level and format

| Flag | Values | Default | Helm value |
|------|--------|---------|------------|
| `--log-level` | `debug`, `info`, `warn`, `error` | `info` | `operator.logLevel` (`info`) |
| `--log-format` | `json`, `console` | `console` for `--log-level=debug`, `json` otherwise | `operator.logFormat` (`""`) |

```yaml
operator:
  logLevel: debug
  logFormat: json   # debug lines as JSON
```

An unknown `--log-level` falls back to `info` and prints
`unrecognized log-level …` to stderr, as in earlier releases. An unknown
`--log-format` stops the operator at startup.

- `json` is zap's production profile: one JSON object per line, stack traces on
  `error` lines. Repeated identical messages are sampled: the first 100 per
  second, then every 100th.
- `console` is zap's development profile: tab-separated, human-readable lines,
  stack traces from `warn` on. Do not use it for log collection; its layout is
  not a stable interface.

Without `--log-format`, `--log-level=debug` selects `console`, exactly as
before; set `--log-format=json` to get debug lines as JSON.

### Levels

Each level includes the levels above it. The operator's own messages use
`error`, `info` and `debug` (logr `V(1)`); `warn` only filters messages of the
libraries below.

| Level | What is logged |
|-------|----------------|
| `error` | Failed Kubernetes API calls the reconcilers cannot complete: status patches, deletion of orphaned overlapping reservations, finalization cleanup, NAD listing. controller-runtime adds `Reconciler error` for every failed reconcile. |
| `warn` | Nothing from the operator itself. |
| `info` (default) | State changes: orphaned allocations released (`cleaned up orphaned allocations` with count), orphaned overlapping reservations deleted, NodeSlicePools created, re-sliced or rebuilt, full node slice pools, IPPool ranges overlapping the service CIDR, IPPool finalization, webhook rejections (`rejected` with object name, operation and reason), certificate rotation setup, and startup. |
| `debug` | Every reconcile (`reconciling …` with name and namespace) and the reason each allocation or reservation is treated as orphaned (pod not found, UID mismatch, terminating, completed, lost with its node, IP missing from the network-status annotation), plus skipped and concurrent deletions and non-fatal lookup failures. |

### Output

With `json`, each line is a JSON object:

| Field | Content |
|-------|---------|
| `level` | `debug`, `info`, `warn`, `error` |
| `ts` | RFC 3339 time, for example `2026-11-13T10:15:04Z` |
| `logger` | Logger name, for example `controller`, `webhook.ippool`, `certrotator`, `cert-rotation`; absent on reconciler lines |
| `msg` | The message |
| `error` | Error text, on lines that carry an error |
| `stacktrace` | Stack trace, on `error` lines |

All other keys are the fields listed in the reference, plus the
controller-runtime fields `controller`, `controllerGroup`, `controllerKind`,
`name`, `namespace` and `reconcileID` on reconciler lines. Example:

```json
{"level":"debug","ts":"2026-11-13T10:15:04Z","msg":"reconciling IPPool","controller":"ippool","controllerGroup":"whereabouts.cni.cncf.io","controllerKind":"IPPool","name":"192.168.2.0-24","namespace":"kube-system","reconcileID":"5b1c…"}
```

The operator writes to stderr. Besides its own messages, the same logger and
level carry the messages of controller-runtime (manager, controllers, webhook
server, leader election state) and of
[`t-caas-go-library/pkg/certrotation`](https://github.com/telekom/t-caas-go-library/blob/main/docs/logging.md)
with cert-controller (`cert-rotation` logger). client-go writes a few lines
through klog (for example leader lease acquisition) in klog's text format; they
are not affected by `--log-level` or `--log-format`.

### Log reference

<!-- BEGIN GENERATED LOG TABLE: operator -->
| Level | Message | Values / fields | Source |
|---|---|---|---|
| stderr | `` error: %v `` | `err` | cmd/operator/main.go |
| stderr | `` unrecognized log-level %q, defaulting to info `` | `logLevel` | cmd/operator/main.go |
| error | `` failed to clean up overlapping reservations during finalization `` | `error` | internal/controller/ippool_controller.go |
| error | `` failed to clean up some overlapping reservations, will retry `` | `error` | internal/controller/ippool_controller.go |
| error | `` failed to delete overlapping reservation `` | `error`, `name` | internal/controller/ippool_controller.go |
| error | `` failed to list NADs for node event mapping `` | `error` | internal/controller/nodeslice_controller.go |
| error | `` failed to patch ready status `` | `error` | internal/controller/overlappingrange_controller.go |
| error | `` failed to patch status `` | `error` | internal/controller/ippool_controller.go |
| error | `` failed to re-fetch overlapping reservation before delete `` | `error`, `name` | internal/controller/ippool_controller.go |
| info | `` IPPool being deleted, cleaning up overlapping reservations `` | `pool` | internal/controller/ippool_controller.go |
| info | `` IPPool range overlaps Kubernetes service CIDR `` | `pool`, `range`, `serviceCIDR` | internal/controller/ippool_controller.go |
| info | `` TLS secret ensured `` | `secret` | internal/webhook/certrotator/certrotator.go |
| info | `` allocation missing podRef, marking orphaned `` | `key` | internal/controller/ippool_controller.go |
| info | `` cleaned up orphaned allocations `` | `pool`, `count` | internal/controller/ippool_controller.go |
| info | `` created NodeSlicePool `` | `name`, `range`, `sliceSize`, `nodes` | internal/controller/nodeslice_controller.go |
| info | `` deleted orphaned overlapping reservation `` | `name`, `podRef` | internal/controller/overlappingrange_controller.go |
| info | `` enabling certificate rotation `` | `namespace`, `secret`, `webhookConfig` | internal/webhook/certrotator/certrotator.go |
| info | `` finalizer removed, IPPool can be deleted `` | `pool` | internal/controller/ippool_controller.go |
| info | `` invalid podRef format, deleting reservation `` | `name`, `podRef` | internal/controller/overlappingrange_controller.go |
| info | `` invalid podRef format, marking orphaned `` | `key`, `podRef` | internal/controller/ippool_controller.go |
| info | `` no available slot for node while stale per-node IPPool cleanup is pending `` | `pool`, `node` | internal/controller/nodeslice_controller.go |
| info | `` no available slot for node, pool is full `` | `pool`, `node` | internal/controller/nodeslice_controller.go |
| info | `` rebuilt NodeSlicePool status `` | `name`, `slices`, `nodes` | internal/controller/nodeslice_controller.go |
| info | `` rejected `` | `name`, `operation`, `reason` | internal/webhook/ippool_webhook.go, internal/webhook/nodeslicepool_webhook.go, internal/webhook/overlappingrange_webhook.go |
| info | `` starting controller manager `` | - | cmd/operator/controller.go |
| info | `` updated NodeSlicePool spec and re-sliced `` | `name`, `range`, `sliceSize` | internal/controller/nodeslice_controller.go |
| debug | `` IP not found on pod, marking allocation orphaned `` | `key`, `podRef`, `ip` | internal/controller/ippool_controller.go |
| debug | `` NAD has no whereabouts IPAM config, skipping `` | `error` | internal/controller/nodeslice_controller.go |
| debug | `` deleted overlapping reservation `` | `name` | internal/controller/ippool_controller.go |
| debug | `` failed to check service CIDR overlap `` | `pool`, `range`, `error` | internal/controller/ippool_controller.go |
| debug | `` failed to count usable IPs `` | `range`, `error` | internal/controller/ippool_controller.go |
| debug | `` failed to list overlapping reservations `` | `error` | internal/controller/ippool_controller.go |
| debug | `` failed to list overlapping reservations for stats `` | `error` | internal/controller/ippool_controller.go |
| debug | `` overlapping reservation already gone, skipping `` | `name` | internal/controller/ippool_controller.go |
| debug | `` overlapping reservation deleted concurrently, treating as success `` | `name` | internal/controller/ippool_controller.go |
| debug | `` overlapping reservation podRef/ifName changed between list and delete, skipping to avoid TOCTOU delete `` | `name`, `expectedPodRef`, `currentPodRef`, `expectedIfName`, `currentIfName` | internal/controller/ippool_controller.go |
| debug | `` pod UID mismatch, deleting stale overlapping reservation `` | `name`, `podRef`, `reservationPodUID`, `podUID` | internal/controller/overlappingrange_controller.go |
| debug | `` pod is completed, deleting overlapping reservation `` | `name`, `podRef`, `phase` | internal/controller/overlappingrange_controller.go |
| debug | `` pod is completed, marking allocation orphaned `` | `key`, `podRef`, `phase` | internal/controller/ippool_controller.go |
| debug | `` pod is lost with its node, deleting overlapping reservation `` | `name`, `podRef`, `phase`, `reason` | internal/controller/overlappingrange_controller.go |
| debug | `` pod is lost with its node, marking allocation orphaned `` | `key`, `podRef`, `phase`, `reason` | internal/controller/ippool_controller.go |
| debug | `` pod is terminating, deleting overlapping reservation `` | `name`, `podRef` | internal/controller/overlappingrange_controller.go |
| debug | `` pod is terminating, marking allocation orphaned `` | `key`, `podRef` | internal/controller/ippool_controller.go |
| debug | `` pod marked for deletion, deleting overlapping reservation `` | `name`, `podRef` | internal/controller/overlappingrange_controller.go |
| debug | `` pod marked for deletion, marking allocation orphaned `` | `key`, `podRef` | internal/controller/ippool_controller.go |
| debug | `` pod not found, deleting overlapping reservation `` | `name`, `podRef` | internal/controller/overlappingrange_controller.go |
| debug | `` pod not found, marking allocation orphaned `` | `key`, `podRef` | internal/controller/ippool_controller.go |
| debug | `` reconciling IPPool `` | `name`, `namespace` | internal/controller/ippool_controller.go |
| debug | `` reconciling NAD for NodeSlicePool `` | `name`, `namespace` | internal/controller/nodeslice_controller.go |
| debug | `` reconciling OverlappingRangeIPReservation `` | `name`, `namespace` | internal/controller/overlappingrange_controller.go |
| debug | `` skipping overlapping reservation: podRef/ifName unverifiable or mismatch `` | `name`, `allocPodRef`, `resPodRef`, `allocIfName`, `resIfName` | internal/controller/ippool_controller.go |
<!-- END GENERATED LOG TABLE: operator -->

## install-cni

### Setting the level and format

| Environment variable | Values | Default | Helm value |
|----------------------|--------|---------|------------|
| `LOG_LEVEL` | `debug`, `info`, `warn`, `error` | `info` | `installCNI.logLevel` |
| `LOG_FORMAT` | `json`, `text` | `json` | `installCNI.logFormat` |

Both are case-insensitive, and `warning` is accepted for `warn`. An unknown
`LOG_LEVEL` falls back to `info` and logs `unknown LOG_LEVEL, using info`; an
unknown `LOG_FORMAT` stops install-cni at startup. install-cni has no `debug`
messages yet, so `debug` logs the same as `info`.

### Levels

| Level | What is logged |
|-------|----------------|
| `error` | A fatal setup error (the container then restarts) and failed kubeconfig regeneration after a credential change. |
| `warn` | `NODENAME` missing, and fsnotify problems that switch credential watching to polling. |
| `info` (default) | Startup, each file written (kubeconfig, `whereabouts.conf`, nodename, the copied plugin binary, with their paths), how credentials are watched, each detected credential change, and shutdown. |
| `debug` | Nothing more than `info`. |

### Output

install-cni writes to stdout through `log/slog`. `json` lines have the keys
`time`, `level` (`DEBUG`, `INFO`, `WARN`, `ERROR`), `msg` and the fields below;
`text` writes the same as `key=value` pairs:

```json
{"time":"2026-11-13T10:15:04.123Z","level":"INFO","msg":"wrote whereabouts.conf","path":"/host/etc/cni/net.d/whereabouts.d/whereabouts.conf"}
```

The `stderr` row is written when `LOG_FORMAT` is invalid, before the logger
exists.

### Log reference

<!-- BEGIN GENERATED LOG TABLE: install-cni -->
| Level | Message | Values / fields | Source |
|---|---|---|---|
| stderr | `` install-cni: `` | `err` | cmd/install-cni/main.go |
| error | `` failed to regenerate kubeconfig `` | `error` | cmd/install-cni/main.go |
| error | `` fatal error `` | `error` | cmd/install-cni/main.go |
| warn | `` NODENAME not set; skipping nodename file generation `` | - | cmd/install-cni/main.go |
| warn | `` cannot watch SA directory, falling back to polling `` | `error` | cmd/install-cni/main.go |
| warn | `` fsnotify error `` | `error` | cmd/install-cni/main.go |
| warn | `` fsnotify unavailable, falling back to polling only `` | `error` | cmd/install-cni/main.go |
| warn | `` unknown LOG_LEVEL, using info `` | `logLevel` | cmd/install-cni/main.go |
| info | `` CNI installer setup complete `` | - | cmd/install-cni/main.go |
| info | `` copied whereabouts binary `` | `src`, `dst` | cmd/install-cni/main.go |
| info | `` detected credential change, regenerating kubeconfig `` | - | cmd/install-cni/main.go |
| info | `` shutting down `` | - | cmd/install-cni/main.go |
| info | `` starting CNI installer `` | - | cmd/install-cni/main.go |
| info | `` using polling-only mode for credential changes `` | `interval` | cmd/install-cni/main.go |
| info | `` watching for credential changes `` | `path` | cmd/install-cni/main.go |
| info | `` wrote kubeconfig `` | `path` | cmd/install-cni/main.go |
| info | `` wrote nodename `` | `path` | cmd/install-cni/main.go |
| info | `` wrote whereabouts.conf `` | `path` | cmd/install-cni/main.go |
<!-- END GENERATED LOG TABLE: install-cni -->

## Keeping this document current

The log reference tables are generated from the code by `doc/logging_test.go`.
It parses the non-test sources of every module package each binary links
(`go list -deps`) and collects every log call with its level, message, logged
values (format arguments for the CNI plugin, structured keys for the operator
and install-cni) and source file. The test runs in `make test` and fails when:

- a table differs from the code, so a new, changed or removed log message must
  update this document in the same pull request;
- a log call has no literal message (a constant string or a concatenation with
  one), because it could not be documented; or
- code writes output past the documented loggers: `fmt.Print*`, writes to
  stdout, the standard `log` package, klog, zap, or `print`/`println`.
Regenerate the tables with

```sh
go test ./doc -run TestLoggingReference -update
```

and update the level descriptions above when a change alters what a level
logs. A change to a log level default or a log setting updates this document
too.
