// Copyright 2026 Deutsche Telekom AG
// SPDX-License-Identifier: Apache-2.0

package hack_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestE2EDiagnosticsArePersistedAsArtifacts(t *testing.T) {
	workflow, err := os.ReadFile(filepath.Join("..", ".github", "workflows", "test.yml"))
	if err != nil {
		t.Fatalf("reading test workflow: %v", err)
	}
	workflowText := string(workflow)

	for _, want := range []string{
		"hack/e2e-collect-diagnostics.sh /tmp/e2e-diagnostics",
		"path: /tmp/e2e-diagnostics/",
		"retention-days: 30",
		"if-no-files-found: warn",
		"name: e2e-diagnostics-${{ matrix.suite }}",
		"name: e2e-helm-diagnostics-${{ matrix.suite }}",
	} {
		if !strings.Contains(workflowText, want) {
			t.Fatalf("test workflow missing diagnostics artifact setting %q", want)
		}
	}
	for _, legacy := range []string{"/tmp/kind-logs/", "retention-days: 7"} {
		if strings.Contains(workflowText, legacy) {
			t.Fatalf("test workflow still uses legacy diagnostics artifact setting %q", legacy)
		}
	}

	script, err := os.ReadFile(filepath.Join("e2e-collect-diagnostics.sh"))
	if err != nil {
		t.Fatalf("reading diagnostics script: %v", err)
	}
	scriptText := string(script)
	for _, want := range []string{
		"ippools.whereabouts.cni.cncf.io",
		"nodeslicepools.whereabouts.cni.cncf.io",
		"overlappingrangeipreservations.whereabouts.cni.cncf.io",
		"net-attach-def",
		"kubectl describe pods -A",
		"kubectl -n kube-system logs",
		"kind export logs",
		"${OUTPUT_DIR}/kind-logs",
	} {
		if !strings.Contains(scriptText, want) {
			t.Fatalf("diagnostics script missing %q", want)
		}
	}
}
