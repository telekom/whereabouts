// Copyright 2026 Deutsche Telekom AG
// SPDX-License-Identifier: Apache-2.0

package release_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateTagAcceptsReleaseTags(t *testing.T) {
	for _, tag := range []string{"v1.2.3", "v0.0.1", "v10.20.30-rc.1", "v1.2.3-alpha-1"} {
		t.Run(tag, func(t *testing.T) {
			output, err := runValidateTag(t, tag)
			if err != nil {
				t.Fatalf("validate-tag.sh rejected %q: %v\n%s", tag, err, output)
			}
		})
	}
}

func TestValidateTagRejectsInjectionShapedTags(t *testing.T) {
	for _, tag := range []string{
		"vbad",
		"v1.2",
		"v1.2.3$(touch /tmp/pwn)",
		"v1.2.3`touch /tmp/pwn`",
		"v1.2.3\"",
		"v1.2.3;",
		"v1.2.3\nnext",
		"v1.2.3-",
		"v1.2.3-rc.",
	} {
		t.Run(strings.ReplaceAll(tag, "\n", "\\n"), func(t *testing.T) {
			output, err := runValidateTag(t, tag)
			if err == nil {
				t.Fatalf("validate-tag.sh accepted invalid tag %q:\n%s", tag, output)
			}
			if !strings.Contains(output, "release tag must match") {
				t.Fatalf("validate-tag.sh output for %q did not explain the format:\n%s", tag, output)
			}
		})
	}
}

func TestMakefileReleaseTargetsDoNotInlineTagValues(t *testing.T) {
	makefile, err := os.ReadFile(filepath.Join("..", "..", "Makefile"))
	if err != nil {
		t.Fatalf("reading Makefile: %v", err)
	}
	text := string(makefile)

	for _, want := range []string{
		`GITHUB_TAG="$${GITHUB_TAG}" GITHUB_REPO_OWNER="$${GITHUB_REPO_OWNER}" hack/release/chart-update.sh`,
		`GITHUB_TAG="$${GITHUB_TAG}" GITHUB_TOKEN="$${GITHUB_TOKEN}" GITHUB_REPO_OWNER="$${GITHUB_REPO_OWNER}" hack/release/chart-push.sh`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("Makefile release target missing safe env forwarding %q", want)
		}
	}
	if strings.Contains(text, "GITHUB_TAG=$(GITHUB_TAG)") {
		t.Fatalf("Makefile still inlines GITHUB_TAG into shell recipes")
	}
}

func TestManifestReleaseWorkflowUsesEnvTag(t *testing.T) {
	workflow, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "binaries-upload-release.yml"))
	if err != nil {
		t.Fatalf("reading binaries-upload-release.yml: %v", err)
	}
	text := string(workflow)

	if !strings.Contains(text, "TAG: ${{ github.event.release.tag_name }}") {
		t.Fatalf("workflow does not pass release tag through step env")
	}
	if !strings.Contains(text, `bash hack/release/validate-tag.sh "${TAG}"`) {
		t.Fatalf("workflow does not validate the env release tag before manifest generation")
	}
	if strings.Contains(text, `TAG="${{ github.event.release.tag_name }}"`) {
		t.Fatalf("workflow still inlines the release tag inside shell script text")
	}
}

func runValidateTag(t *testing.T, tag string) (string, error) {
	t.Helper()

	cmd := exec.Command("bash", "validate-tag.sh", tag)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
