# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Configurable logging for every binary, documented per level in
  `doc/logging.md`. Defaults keep the current output.
  - Operator: `--log-format` (`json`, `console`; Helm `operator.logFormat`).
    Empty keeps the previous behavior (console for `--log-level=debug`, json
    otherwise); an invalid value stops the operator. The Helm value
    `operator.logLevel` (default `info`) sets `--log-level`.
  - install-cni: `LOG_LEVEL` (`debug`, `info`, `warn`, `error`; default `info`)
    and `LOG_FORMAT` (`json`, `text`; default `json`), Helm
    `installCNI.logLevel`/`installCNI.logFormat`. Invalid values stop
    install-cni.
  - CNI plugin: install-cni writes `CNI_LOG_LEVEL`/`CNI_LOG_FILE` as
    `log_level`/`log_file` into `whereabouts.conf` (Helm
    `cniConf.logLevel`/`cniConf.logFile`, empty by default). The
    NetworkAttachmentDefinition's IPAM config still takes precedence.
  - `doc/logging_test.go` fails `make test` when a log message is missing from
    `doc/logging.md`.
- Webhook server TLS hardening: minimum TLS 1.2, and for TLS 1.2 only ECDHE
  cipher suites with AES-GCM or ChaCha20-Poly1305. TLS 1.3 keeps Go's defaults.
  Opt out with `--webhook-tls-go-defaults=true` (Helm:
  `operator.webhookTLS.goDefaults: true`). The opt-out is transitional; may be
  removed in a future release.
- Webhook certificate rotation and revocation runbook in
  `doc/webhook-certificate-runbook.md`.
- Operator binary with single `controller` subcommand that runs both reconcilers
  and webhook server, replacing the legacy `ip-control-loop` and `node-slice-controller` binaries.
- Validating webhooks for IPPool, NodeSlicePool, and OverlappingRangeIPReservation
  CRDs with automatic TLS certificate rotation.
- `matchConditions` CEL expressions on webhooks to bypass validation for the CNI
  ServiceAccount.
- `EventRecorder` integration in all three reconcilers (IPPool, NodeSlice,
  OverlappingRange) for Kubernetes event emission on CRDs.
- `predicate.GenerationChangedPredicate` on IPPool and OverlappingRange watches
  to skip reconciliation on status-only updates.
- Path traversal validation on `configuration_path` in IPAM configuration.
- Operator feature flags for reconciler behavior:
  - `--cleanup-terminating-pods` (default `false`): Release IPs from pods with
    `DeletionTimestamp` set (graceful node shutdown / drain scenarios).
  - `--cleanup-disrupted-pods` (default `true`): Release IPs from pods with
    `DisruptionTarget` condition (taint-manager eviction).
  - `--verify-network-status` (default `true`): Verify allocated IPs against
    Multus `network-status` annotation on pods.
- Comprehensive e2e test coverage across IPv4, IPv6, and dual-stack for all
  features: gateway exclusion, L3 mode, optimistic IPAM, named networks,
  multi-pool, exclude ranges, preferred IP, small subnets, pool exhaustion,
  reallocation cycles, and allocation verification.
- Edge case e2e tests for operational scenarios: node cordon + eviction, pod
  eviction via Policy API, rapid pod churn, multi-interface cleanup, StatefulSet
  scale down/up, and concurrent burst creation.
- Comprehensive IPAM configuration reference in `doc/extended-configuration.md`.
- Architecture documentation in `doc/architecture.md`.
- Operator metrics documentation in `doc/metrics.md`.
- `CONTRIBUTING.md` with build, test, and code convention guidelines.
- `.dockerignore` to reduce Docker build context size.

### Fixed
- Empty or whitespace-only Multus `network-status` annotations no longer keep
  stale IPPool allocations marked as in use when verification is enabled.
- `denormalizeIPName` infinite loop when the last segment of a normalized IPPool
  name contained no dash separator.
- `pathExists()` in config.go now correctly returns `false` on any `os.Stat` error
  (previously returned `true` for non-`IsNotExist` errors).
- `AssignmentError` message is now actionable, suggesting pool exhaustion checks.

### Changed
- Removed unused `pkg/version.GetVersion` and `GetGitSHA` accessors and their
  helper-only tests. Build-time version variables and displayed version strings
  are unchanged; both binaries continue to use `GetFullVersionWithRuntimeInfo`.
- Removed unused CNI logging `Panicf`, `GetLoggingLevel`, and `SetLogStderr`
  helpers and their helper-only tests. The configured `panic` level, stderr/file
  output, timestamps, error wrapping, and log-file validation are unchanged.
- Updated all safe direct and indirect Go modules to current releases. The
  legacy `github.com/imdario/mergo` path is pinned to its latest compatible
  release (`v0.3.16`) because v1 moved to `dario.cat/mergo`, which is already
  used separately.
- `ReconcilerCronExpression` field in `IPAMConfig` is marked deprecated; the
  operator now uses `--reconcile-interval` instead.
- Helm webhook ClusterRole secrets access is now scoped to the specific webhook
  cert secret name (least-privilege).
- Improved error messages for `InvalidPluginError` and `storageError`.
- `doc/sample_config.json` is now valid JSON (removed malformed second object).

### Security
- The CNI install DaemonSet no longer runs privileged. It keeps `runAsUser: 0`
  but sets `privileged: false`, `allowPrivilegeEscalation: false`, drops all
  capabilities, uses a read-only root filesystem and the `RuntimeDefault`
  seccomp profile, in both the Kustomize manifest and the Helm chart. The Helm
  `securityContext` value remains overridable, and the new `seLinuxSpcT` value
  (default `false`) sets `seLinuxOptions.type: spc_t`.
  **Upgrade note:** on SELinux-enforcing nodes, set `seLinuxSpcT=true` (Helm)
  or add the Kustomize patch documented in the README, because the default
  `container_t` domain cannot write `/opt/cni/bin` and `/etc/cni/net.d`.
  Without capabilities, the host CNI directories must also be root-owned and
  owner-writable. The namespace still needs the `privileged` Pod Security
  level (hostNetwork and hostPath).
  **Opt-out:** set the new Helm value `cniPrivileged=true`, or apply the
  Kustomize patch documented in the README, to restore the previous
  `privileged: true`, `runAsUser: 0` security context. This is a transitional
  escape hatch and may be removed in a future release.
- Added `configuration_path` validation to reject paths containing `..` (path
  traversal prevention).
- Restricted Helm webhook ClusterRole: broad `secrets` access split into
  unrestricted `create` and resourceName-scoped `get/list/watch/update/patch`.
