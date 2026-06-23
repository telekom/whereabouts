// SPDX-FileCopyrightText: 2026 Deutsche Telekom AG
// SPDX-License-Identifier: Apache-2.0

package release_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestChartPushUsesPasswordStdinWithoutLeakingToken(t *testing.T) {
	t.Parallel()

	repoRoot := repoRoot(t)
	binDir := t.TempDir()
	helmLog := filepath.Join(t.TempDir(), "helm.log")
	secretToken := "secret-token-should-not-leak"

	writeFakeHelm(t, filepath.Join(binDir, "helm"))

	cmd := exec.Command("bash", "hack/release/chart-push.sh")
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"HELM_LOG="+helmLog,
		"GITHUB_REPO_OWNER=test-owner",
		"GITHUB_TOKEN="+secretToken,
		"GITHUB_TAG=v1.2.3",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("chart-push.sh failed: %v\n%s", err, output)
	}
	if strings.Contains(string(output), secretToken) {
		t.Fatalf("chart-push.sh output leaked token: %s", output)
	}

	logBytes, err := os.ReadFile(helmLog)
	if err != nil {
		t.Fatalf("read fake helm log: %v", err)
	}
	log := string(logBytes)
	if strings.Contains(log, secretToken) {
		t.Fatalf("helm command arguments leaked token: %s", log)
	}
	if !strings.Contains(log, "registry login ghcr.io -u test-owner --password-stdin") {
		t.Fatalf("expected registry login to use --password-stdin, got log:\n%s", log)
	}
	if !strings.Contains(log, "stdin-bytes=28") {
		t.Fatalf("expected helm login to receive token on stdin, got log:\n%s", log)
	}
	if !strings.Contains(log, "push whereabouts-chart-1.2.3.tgz oci://ghcr.io/test-owner") {
		t.Fatalf("expected chart push invocation, got log:\n%s", log)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repository root")
		}
		dir = parent
	}
}

func writeFakeHelm(t *testing.T, path string) {
	t.Helper()

	const script = `#!/bin/sh
set -eu

printf '%s\n' "$*" >> "$HELM_LOG"

if [ "$1" = "registry" ] && [ "$2" = "login" ]; then
    password=$(cat)
    printf 'stdin-bytes=%s\n' "${#password}" >> "$HELM_LOG"
fi
`

	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake helm: %v", err)
	}
}
