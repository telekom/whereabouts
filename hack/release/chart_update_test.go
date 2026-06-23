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

func TestChartUpdateDoesNotRequireGitHubToken(t *testing.T) {
	tempRoot := prepareChartUpdateWorkspace(t, true)

	output, err := runChartUpdate(t, tempRoot, map[string]string{
		"GITHUB_TAG":        "v1.2.3",
		"GITHUB_REPO_OWNER": "example-owner",
	})
	if err != nil {
		t.Fatalf("chart-update.sh returned error: %v\n%s", err, output)
	}
	if strings.Contains(output, "GITHUB_TOKEN") {
		t.Fatalf("chart-update.sh output mentioned GITHUB_TOKEN:\n%s", output)
	}

	yqArgs, err := os.ReadFile(filepath.Join(tempRoot, "yq.args"))
	if err != nil {
		t.Fatalf("reading fake yq args: %v", err)
	}
	args := string(yqArgs)
	for _, want := range []string{
		"ghcr.io/example-owner/whereabouts",
		"v1.2.3",
		"1.2.3",
	} {
		if !strings.Contains(args, want) {
			t.Fatalf("fake yq args did not contain %q:\n%s", want, args)
		}
	}
}

func TestChartUpdateStillRequiresTagAndOwner(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{
			name: "missing tag",
			env: map[string]string{
				"GITHUB_REPO_OWNER": "example-owner",
			},
			want: "GITHUB_TAG must be provided",
		},
		{
			name: "missing owner",
			env: map[string]string{
				"GITHUB_TAG": "v1.2.3",
			},
			want: "GITHUB_REPO_OWNER must be provided",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tempRoot := prepareChartUpdateWorkspace(t, true)
			output, err := runChartUpdate(t, tempRoot, tt.env)
			if err == nil {
				t.Fatalf("chart-update.sh unexpectedly succeeded:\n%s", output)
			}
			if !strings.Contains(output, tt.want) {
				t.Fatalf("chart-update.sh output did not contain %q:\n%s", tt.want, output)
			}
			if strings.Contains(output, "GITHUB_TOKEN") {
				t.Fatalf("chart-update.sh output mentioned GITHUB_TOKEN:\n%s", output)
			}
		})
	}
}

func TestChartUpdateReportsMissingYQAfterRequiredEnvIsValid(t *testing.T) {
	tempRoot := prepareChartUpdateWorkspace(t, false)

	output, err := runChartUpdate(t, tempRoot, map[string]string{
		"GITHUB_TAG":        "v1.2.3",
		"GITHUB_REPO_OWNER": "example-owner",
	})
	if err == nil {
		t.Fatalf("chart-update.sh unexpectedly succeeded without bin/yq:\n%s", output)
	}
	if !strings.Contains(output, "bin/yq") {
		t.Fatalf("chart-update.sh output did not mention missing bin/yq:\n%s", output)
	}
	if strings.Contains(output, "GITHUB_TOKEN") {
		t.Fatalf("chart-update.sh output mentioned GITHUB_TOKEN:\n%s", output)
	}
}

func prepareChartUpdateWorkspace(t *testing.T, includeFakeYQ bool) string {
	t.Helper()

	tempRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tempRoot, "bin"), 0755); err != nil {
		t.Fatalf("creating bin dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(tempRoot, "deployment", "whereabouts-chart"), 0755); err != nil {
		t.Fatalf("creating chart dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tempRoot, "deployment", "whereabouts-chart", "values.yaml"), []byte("image: {}\n"), 0644); err != nil {
		t.Fatalf("writing values.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tempRoot, "deployment", "whereabouts-chart", "Chart.yaml"), []byte("version: 0.0.0\nappVersion: v0.0.0\n"), 0644); err != nil {
		t.Fatalf("writing Chart.yaml: %v", err)
	}
	if includeFakeYQ {
		fakeYQ := "#!/bin/sh\nprintf '%s\\n' \"$@\" >> \"$PWD/yq.args\"\n"
		if err := os.WriteFile(filepath.Join(tempRoot, "bin", "yq"), []byte(fakeYQ), 0755); err != nil {
			t.Fatalf("writing fake yq: %v", err)
		}
	}

	return tempRoot
}

func runChartUpdate(t *testing.T, tempRoot string, env map[string]string) (string, error) {
	t.Helper()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getting working directory: %v", err)
	}
	scriptPath := filepath.Join(wd, "chart-update.sh")
	cmd := exec.Command("bash", scriptPath)
	cmd.Dir = tempRoot
	cmd.Env = []string{
		"PATH=/usr/bin:/bin",
		"HOME=" + tempRoot,
	}
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}

	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err = cmd.Run()
	return output.String(), err
}
