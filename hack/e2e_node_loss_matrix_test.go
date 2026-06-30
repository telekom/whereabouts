// Copyright 2026 Deutsche Telekom AG
// SPDX-License-Identifier: Apache-2.0

package hack_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestE2EWorkflowRunsNodeLossInDedicatedShard(t *testing.T) {
	workflow, err := os.ReadFile(filepath.Join("..", ".github", "workflows", "test.yml"))
	if err != nil {
		t.Fatalf("read test workflow: %v", err)
	}
	workflowText := string(workflow)

	for _, want := range []string{
		"- suite: node-loss",
		`test-pkg: "."`,
		`focus: "Node loss"`,
	} {
		if !strings.Contains(workflowText, want) {
			t.Fatalf("test workflow missing dedicated node-loss shard entry %q", want)
		}
	}

	if strings.Contains(workflowText, `focus: "Pod cleanup|Node drain|Reconciler cleanup|Node loss"`) {
		t.Fatal("node loss removes a kind worker and must not run in the shared core-lifecycle shard")
	}

	if strings.Contains(workflowText, `e2e-helm / ${{ matrix.suite }}`+"\n") &&
		strings.Contains(workflowText, `focus: "Node loss"`+"\n"+`    env:`) {
		t.Fatal("node loss is kind-node destructive and must not run in the Helm shard")
	}
}
