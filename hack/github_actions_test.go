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

func TestChartReleaseWorkflowWaitsForReleaseImageBeforePushingChart(t *testing.T) {
	workflow, err := os.ReadFile(filepath.Join("..", ".github", "workflows", "chart-push-release.yml"))
	if err != nil {
		t.Fatalf("read release chart workflow: %v", err)
	}
	workflowText := string(workflow)

	for _, want := range []string{
		"docker/setup-buildx-action@d7f5e7f509e45cec5c76c4d5afdd7de93d0b3df5",
		"run: bash hack/release/wait-for-release-image.sh",
		"IMAGE_NAME: ${{ env.IMAGE_NAME }}",
		"GITHUB_TAG: ${{ github.ref_name }}",
	} {
		if !strings.Contains(workflowText, want) {
			t.Fatalf("release chart workflow missing %q", want)
		}
	}

	waitIndex := strings.Index(workflowText, "run: bash hack/release/wait-for-release-image.sh")
	pushIndex := strings.Index(workflowText, "run: make chart-push-release")
	if waitIndex == -1 || pushIndex == -1 {
		t.Fatalf("release chart workflow must include wait and push steps")
	}
	if waitIndex > pushIndex {
		t.Fatalf("release chart workflow waits for the release image after pushing the chart")
	}
}
