// Copyright 2026 Deutsche Telekom AG
// SPDX-License-Identifier: Apache-2.0

package hack_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitHubActionsDoNotInstallLatestToolVersions(t *testing.T) {
	workflowPaths, err := filepath.Glob(filepath.Join("..", ".github", "workflows", "*.yml"))
	if err != nil {
		t.Fatalf("glob workflow files: %v", err)
	}
	for _, path := range workflowPaths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read workflow %s: %v", path, err)
		}
		if strings.Contains(string(data), "version: latest") {
			t.Fatalf("%s installs a tool with version: latest", path)
		}
	}
}
