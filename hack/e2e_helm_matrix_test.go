// Copyright 2026 Deutsche Telekom AG
// SPDX-License-Identifier: Apache-2.0

package hack_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHelmE2EMatrixCoversIPv6AndDualStack(t *testing.T) {
	workflow, err := os.ReadFile(filepath.Join("..", ".github", "workflows", "test.yml"))
	if err != nil {
		t.Fatalf("read test workflow: %v", err)
	}

	helmJob := workflowJob(t, string(workflow), "e2e-helm")
	for _, tc := range []struct {
		suite string
		focus string
	}{
		{suite: "v6", focus: `"IPv6"`},
		{suite: "dualstack-ext", focus: `"DS:|Multi-pool:|Failure:"`},
	} {
		t.Run(tc.suite, func(t *testing.T) {
			if !strings.Contains(helmJob, "- suite: "+tc.suite) {
				t.Fatalf("e2e-helm matrix does not include suite %q", tc.suite)
			}
			if !strings.Contains(helmJob, "focus: "+tc.focus) {
				t.Fatalf("e2e-helm suite %q does not use focus %s", tc.suite, tc.focus)
			}
		})
	}
}

func workflowJob(t *testing.T, workflow, jobName string) string {
	t.Helper()

	lines := strings.Split(workflow, "\n")
	startLine := -1
	for i, line := range lines {
		if line == "  "+jobName+":" {
			startLine = i
			break
		}
	}
	if startLine == -1 {
		t.Fatalf("workflow does not define job %q", jobName)
	}

	var job strings.Builder
	for i := startLine; i < len(lines); i++ {
		line := lines[i]
		if i > startLine && strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "    ") &&
			strings.HasSuffix(strings.TrimSpace(line), ":") {
			break
		}
		job.WriteString(line)
		job.WriteByte('\n')
	}
	return job.String()
}
