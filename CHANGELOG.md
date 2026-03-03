# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
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
- Comprehensive IPAM configuration reference in `doc/extended-configuration.md`.
- Architecture documentation in `doc/architecture.md`.
- `CONTRIBUTING.md` with build, test, and code convention guidelines.
- `.dockerignore` to reduce Docker build context size.

### Fixed
- `denormalizeIPName` infinite loop when the last segment of a normalized IPPool
  name contained no dash separator.
- `GetVersion()` no longer panics when the build-time `Version` variable is empty;
  returns a zero-value `semver.Version` instead.
- `pathExists()` in config.go now correctly returns `false` on any `os.Stat` error
  (previously returned `true` for non-`IsNotExist` errors).
- `AssignmentError` message is now actionable, suggesting pool exhaustion checks.

### Changed
- `ReconcilerCronExpression` field in `IPAMConfig` is marked deprecated; the
  operator now uses `--reconcile-interval` instead.
- Helm webhook ClusterRole secrets access is now scoped to the specific webhook
  cert secret name (least-privilege).
- Improved error messages for `InvalidPluginError` and `storageError`.
- `doc/sample_config.json` is now valid JSON (removed malformed second object).

### Security
- Added `configuration_path` validation to reject paths containing `..` (path
  traversal prevention).
- Restricted Helm webhook ClusterRole: broad `secrets` access split into
  unrestricted `create` and resourceName-scoped `get/list/watch/update/patch`.
