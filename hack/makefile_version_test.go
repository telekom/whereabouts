// Copyright 2026 Deutsche Telekom AG
// SPDX-License-Identifier: Apache-2.0

package hack_test

import (
	"os/exec"
	"strings"
	"testing"
)

func TestMakefileMarksOnlyExactTagsAsReleased(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "version without exact tag is unreleased",
			args: []string{"GIT_TAG=", "VERSION=v1.2.3"},
			want: "unreleased",
		},
		{
			name: "exact tag is released",
			args: []string{"GIT_TAG=v1.2.3", "VERSION=v1.2.3"},
			want: "released",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := makefileReleaseStatus(t, tt.args...)
			if got != tt.want {
				t.Fatalf("RELEASE_STATUS = %q, want %q", got, tt.want)
			}
		})
	}
}

func makefileReleaseStatus(t *testing.T, args ...string) string {
	t.Helper()

	cmdArgs := append([]string{"-pn"}, args...)
	cmd := exec.Command("make", cmdArgs...)
	cmd.Dir = ".."
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("running make %s: %v", strings.Join(cmdArgs, " "), err)
	}

	for _, line := range strings.Split(string(output), "\n") {
		if strings.HasPrefix(line, "RELEASE_STATUS := ") {
			return strings.TrimPrefix(line, "RELEASE_STATUS := ")
		}
	}
	t.Fatalf("RELEASE_STATUS was not present in make output")
	return ""
}
