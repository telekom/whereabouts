// Copyright 2026 Deutsche Telekom AG
// SPDX-License-Identifier: Apache-2.0

package hack_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUndeployPreservesCRDs(t *testing.T) {
	makefile := readRepoFile(t, "Makefile")
	if !strings.Contains(makefile, "$(KUSTOMIZE) build config/undeploy | kubectl delete --ignore-not-found -f -") {
		t.Fatalf("undeploy target must delete config/undeploy instead of config/default")
	}

	defaultKustomization := readRepoFile(t, "config", "default", "kustomization.yaml")
	if !strings.Contains(defaultKustomization, "- ../crd") {
		t.Fatalf("config/default should keep installing CRDs")
	}

	undeployKustomization := readRepoFile(t, "config", "undeploy", "kustomization.yaml")
	if strings.Contains(undeployKustomization, "../crd") {
		t.Fatalf("config/undeploy must not include CRDs")
	}
	for _, resource := range []string{"../rbac", "../manager", "../webhook", "../daemonset"} {
		if !strings.Contains(undeployKustomization, "- "+resource) {
			t.Fatalf("config/undeploy missing runtime resource %s", resource)
		}
	}
}

func readRepoFile(t *testing.T, parts ...string) string {
	t.Helper()

	path := filepath.Join(append([]string{".."}, parts...)...)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
