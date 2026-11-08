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

	// One core-lifecycle shard each in the e2e (kustomize) and e2e-helm jobs.
	const focus = `focus: "Pod cleanup|Node drain|Reconciler cleanup|Operator restart recovery|Webhook TLS"`
	if got := strings.Count(contents, focus); got != 2 {
		t.Fatalf("expected the kustomize and Helm core-lifecycle shards to use %s, found it %d times", focus, got)
	}
}
