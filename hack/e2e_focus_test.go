// Copyright 2026 Deutsche Telekom AG
// SPDX-License-Identifier: Apache-2.0

package hack_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestE2EWorkflowRunsOperatorRestartRecovery(t *testing.T) {
	workflow, err := os.ReadFile(filepath.Join("..", ".github", "workflows", "test.yml"))
	if err != nil {
		t.Fatalf("reading test workflow: %v", err)
	}
	contents := string(workflow)

	if !strings.Contains(contents, `focus: "Pod cleanup|Node drain|Reconciler cleanup|Operator restart recovery"`) {
		t.Fatal("non-Helm core-lifecycle e2e shard must include Operator restart recovery")
	}
	if strings.Contains(contents, `e2e-helm`) &&
		strings.Contains(contents, `focus: "Pod cleanup|Node drain|Reconciler cleanup|Operator restart recovery"`+"\n"+`    env:`) {
		t.Fatal("operator restart recovery uses the kustomize deployment name and must not be enabled in the Helm shard without generalizing the test")
	}
}
