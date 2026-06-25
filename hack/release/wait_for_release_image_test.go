// Copyright 2026 Deutsche Telekom AG
// SPDX-License-Identifier: Apache-2.0

package release_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWaitForReleaseImageSucceedsAfterRetry(t *testing.T) {
	repoRoot := repoRoot(t)
	binDir := t.TempDir()
	dockerLog := filepath.Join(t.TempDir(), "docker.log")
	dockerState := filepath.Join(t.TempDir(), "docker.state")
	writeFakeDocker(t, filepath.Join(binDir, "docker"))

	output, err := runWaitForReleaseImage(t, repoRoot, map[string]string{
		"PATH":                            binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"DOCKER_LOG":                      dockerLog,
		"DOCKER_STATE":                    dockerState,
		"DOCKER_SUCCEED_AFTER":            "2",
		"GITHUB_TAG":                      "v1.2.3",
		"IMAGE_NAME":                      "ghcr.io/test-owner/whereabouts",
		"WAIT_FOR_RELEASE_IMAGE_TIMEOUT":  "5",
		"WAIT_FOR_RELEASE_IMAGE_INTERVAL": "0",
	})
	if err != nil {
		t.Fatalf("wait-for-release-image.sh returned error: %v\n%s", err, output)
	}
	if !strings.Contains(output, "Release image is available: ghcr.io/test-owner/whereabouts:v1.2.3") {
		t.Fatalf("wait output did not report the available image:\n%s", output)
	}

	logBytes, err := os.ReadFile(dockerLog)
	if err != nil {
		t.Fatalf("read fake docker log: %v", err)
	}
	log := string(logBytes)
	if got := strings.Count(log, "buildx imagetools inspect ghcr.io/test-owner/whereabouts:v1.2.3"); got != 2 {
		t.Fatalf("fake docker inspect count = %d, want 2:\n%s", got, log)
	}
}

func TestWaitForReleaseImageTimesOut(t *testing.T) {
	repoRoot := repoRoot(t)
	binDir := t.TempDir()
	writeFakeDocker(t, filepath.Join(binDir, "docker"))

	output, err := runWaitForReleaseImage(t, repoRoot, map[string]string{
		"PATH":                            binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"DOCKER_LOG":                      filepath.Join(t.TempDir(), "docker.log"),
		"DOCKER_STATE":                    filepath.Join(t.TempDir(), "docker.state"),
		"DOCKER_SUCCEED_AFTER":            "99",
		"GITHUB_TAG":                      "v1.2.3",
		"IMAGE_NAME":                      "ghcr.io/test-owner/whereabouts",
		"WAIT_FOR_RELEASE_IMAGE_TIMEOUT":  "0",
		"WAIT_FOR_RELEASE_IMAGE_INTERVAL": "0",
	})
	if err == nil {
		t.Fatalf("wait-for-release-image.sh unexpectedly succeeded:\n%s", output)
	}
	if !strings.Contains(output, "timed out waiting for release image ghcr.io/test-owner/whereabouts:v1.2.3") {
		t.Fatalf("wait output did not explain the timeout:\n%s", output)
	}
}

func TestWaitForReleaseImageValidatesInputs(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{
			name: "missing tag",
			env: map[string]string{
				"IMAGE_NAME": "ghcr.io/test-owner/whereabouts",
			},
			want: "GITHUB_TAG must be provided",
		},
		{
			name: "missing image",
			env: map[string]string{
				"GITHUB_TAG": "v1.2.3",
			},
			want: "IMAGE_NAME must be provided",
		},
		{
			name: "invalid timeout",
			env: map[string]string{
				"GITHUB_TAG":                      "v1.2.3",
				"IMAGE_NAME":                      "ghcr.io/test-owner/whereabouts",
				"WAIT_FOR_RELEASE_IMAGE_TIMEOUT":  "soon",
				"WAIT_FOR_RELEASE_IMAGE_INTERVAL": "0",
			},
			want: "WAIT_FOR_RELEASE_IMAGE_TIMEOUT must be a non-negative integer",
		},
		{
			name: "invalid tag",
			env: map[string]string{
				"GITHUB_TAG":                      "v1.2.3;",
				"IMAGE_NAME":                      "ghcr.io/test-owner/whereabouts",
				"WAIT_FOR_RELEASE_IMAGE_TIMEOUT":  "0",
				"WAIT_FOR_RELEASE_IMAGE_INTERVAL": "0",
			},
			want: "release tag must match",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repoRoot := repoRoot(t)
			binDir := t.TempDir()
			writeFakeDocker(t, filepath.Join(binDir, "docker"))

			env := map[string]string{
				"PATH":                            binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
				"DOCKER_LOG":                      filepath.Join(t.TempDir(), "docker.log"),
				"DOCKER_STATE":                    filepath.Join(t.TempDir(), "docker.state"),
				"DOCKER_SUCCEED_AFTER":            "1",
				"WAIT_FOR_RELEASE_IMAGE_TIMEOUT":  "0",
				"WAIT_FOR_RELEASE_IMAGE_INTERVAL": "0",
			}
			for key, value := range tt.env {
				env[key] = value
			}

			output, err := runWaitForReleaseImage(t, repoRoot, env)
			if err == nil {
				t.Fatalf("wait-for-release-image.sh unexpectedly succeeded:\n%s", output)
			}
			if !strings.Contains(output, tt.want) {
				t.Fatalf("wait output did not contain %q:\n%s", tt.want, output)
			}
		})
	}
}

func runWaitForReleaseImage(t *testing.T, repoRoot string, env map[string]string) (string, error) {
	t.Helper()

	cmd := exec.Command("bash", "hack/release/wait-for-release-image.sh")
	cmd.Dir = repoRoot
	cmd.Env = []string{
		"HOME=" + repoRoot,
	}
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}

	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	return output.String(), err
}

func writeFakeDocker(t *testing.T, path string) {
	t.Helper()

	const script = `#!/bin/sh
set -eu

printf '%s\n' "$*" >> "$DOCKER_LOG"

if [ "$1" != "buildx" ] || [ "$2" != "imagetools" ] || [ "$3" != "inspect" ]; then
    echo "unexpected docker arguments: $*" >&2
    exit 2
fi

count=0
if [ -f "$DOCKER_STATE" ]; then
    count=$(cat "$DOCKER_STATE")
fi
count=$((count + 1))
printf '%s' "$count" > "$DOCKER_STATE"

if [ "$count" -ge "${DOCKER_SUCCEED_AFTER:-1}" ]; then
    printf 'Name: %s\nDigest: sha256:abc123\n' "$4"
    exit 0
fi

echo "not found" >&2
exit 1
`

	if err := os.WriteFile(path, []byte(script), 0755); err != nil {
		t.Fatalf("write fake docker: %v", err)
	}
}
