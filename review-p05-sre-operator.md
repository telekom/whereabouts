# P5: SRE & Operator Patterns Review

**Date:** 2026-03-02  
**Reviewer Persona:** SRE & Go Code Quality Expert

## Summary

| Severity | Count |
|----------|-------|
| CRITICAL | 0 |
| HIGH | 2 |
| MEDIUM | 5 |
| LOW | 3 |
| INFO | 2 |

---

## Findings

### 1. HIGH — `Panicf` does NOT panic

**File:** [pkg/logging/logging.go](pkg/logging/logging.go#L93-L98)

Despite its name, `Panicf` never calls `panic()` or `os.Exit()`. Execution continues after the call. Any caller relying on `Panicf` to halt after detecting data corruption will continue running in an invalid state.

### 2. HIGH — Global logging state has data races

**File:** [pkg/logging/logging.go](pkg/logging/logging.go#L38-L40)

`loggingStderr`, `loggingFp`, and `loggingLevel` are read by `Printf` (any goroutine) and written by setters with no `sync.Mutex`, `sync.RWMutex`, or `atomic` protection. Under Go's memory model, this is undefined behavior. The race detector would flag this.

### 3. MEDIUM — `GetConfigOrDie()` panics inside Cobra `RunE`

**Files:** [cmd/operator/controller.go](cmd/operator/controller.go#L33), [cmd/operator/webhook.go](cmd/operator/webhook.go#L39)

Bypasses Cobra's clean error-reporting path, producing stack traces instead of user-friendly messages. Should use `ctrl.GetConfig()` and return the error.

### 4. MEDIUM — `setupLogger` silently discards flag-lookup error

**File:** [cmd/operator/main.go](cmd/operator/main.go#L56-L65)

The error from `cmd.Flags().GetString("log-level")` is discarded. Also, `--log-level` advertises `"debug, info, error"` but only `"debug"` is differentiated. `"info"` and `"error"` both produce the same production logger.

### 5. MEDIUM — TOCTOU race in `ensureSecret`

**File:** [internal/webhook/certrotator/certrotator.go](internal/webhook/certrotator/certrotator.go#L49-L63)

Two replicas starting simultaneously both see `IsNotFound`, both attempt `Create`, one gets `AlreadyExists` which is not handled — it's returned as fatal startup error.

### 6. MEDIUM — No timeout on bootstrap API calls

**File:** [internal/webhook/certrotator/certrotator.go](internal/webhook/certrotator/certrotator.go#L83-L88)

`ctx` is derived from `cmd.Context()` (= `context.Background()` by default). If the API server is unreachable, the call blocks indefinitely.

### 7. MEDIUM — `Printf` log lines not atomically written

**File:** [pkg/logging/logging.go](pkg/logging/logging.go#L59-L73)

Each log line uses three separate `fmt.Fprintf` calls. Under concurrent access, output interleaves mid-line, producing garbled logs.

### 8. MEDIUM — `SetLogFile` missing `return` after error + file handle leak

**File:** [pkg/logging/logging.go](pkg/logging/logging.go#L123-L131)

After error, falls through to `loggingFp = fp` (happens to be nil). Also, calling `SetLogFile` twice leaks the first file descriptor.

### 9. LOW — `cmd.Context()` vs `ctrl.SetupSignalHandler()` — two distinct contexts

**File:** [cmd/operator/webhook.go](cmd/operator/webhook.go#L62)

Cert rotation uses `cmd.Context()` (`context.Background()`) while the manager gets a signal-aware context. Bootstrap calls block through SIGTERM.

### 10. LOW — `Errorf` double-formats the message

**File:** [pkg/logging/logging.go](pkg/logging/logging.go#L87-L90)

Format string and arguments processed twice. Minor performance waste.

### 11. LOW — Log file created with 0644 permissions (world-readable)

**File:** [pkg/logging/logging.go](pkg/logging/logging.go#L129)

Logs may contain pod names, IPs, namespace names. `0600` is more secure.

### 12. INFO — No `GracefulShutdownTimeout` set on managers

Default is 30s. If pod's grace period is also 30s, kubelet may SIGKILL before graceful shutdown completes.

### 13. INFO — `init()` scheme registration panics are unrecoverable but idiomatic
