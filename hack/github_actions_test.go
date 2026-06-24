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

func TestReleaseImageWorkflowPassesVersionBuildArgs(t *testing.T) {
	workflow, err := os.ReadFile(filepath.Join("..", ".github", "workflows", "image-push-release.yml"))
	if err != nil {
		t.Fatalf("read release image workflow: %v", err)
	}
	workflowText := string(workflow)

	dockerfile, err := os.ReadFile(filepath.Join("..", "Dockerfile"))
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}
	dockerfileText := string(dockerfile)

	for _, arg := range []string{"VERSION", "GIT_SHA", "GIT_TREE_STATE", "RELEASE_STATUS"} {
		if !strings.Contains(dockerfileText, "ARG "+arg+"=") {
			t.Fatalf("Dockerfile no longer declares build arg %s", arg)
		}
		if !strings.Contains(workflowText, arg+"=") {
			t.Fatalf("release image workflow does not pass Dockerfile build arg %s", arg)
		}
	}

	for _, want := range []string{
		"build-args: |",
		"VERSION=${{ github.ref_name }}",
		"GIT_SHA=${{ github.sha }}",
		"GIT_TREE_STATE=clean",
		"RELEASE_STATUS=released",
	} {
		if !strings.Contains(workflowText, want) {
			t.Fatalf("release image workflow missing %q", want)
		}
	}
}
