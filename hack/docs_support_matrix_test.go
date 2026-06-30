// Copyright 2026 Deutsche Telekom AG
// SPDX-License-Identifier: Apache-2.0

package hack_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDocumentationSupportMatrixMatchesSourceTruth(t *testing.T) {
	read := func(path string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join("..", path))
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		return string(data)
	}

	readme := read("README.md")
	extended := read(filepath.Join("doc", "extended-configuration.md"))
	architecture := read(filepath.Join("doc", "architecture.md"))
	buildWorkflow := read(filepath.Join(".github", "workflows", "build.yml"))
	mainImageWorkflow := read(filepath.Join(".github", "workflows", "image-push-main.yml"))
	releaseImageWorkflow := read(filepath.Join(".github", "workflows", "image-push-release.yml"))
	releaseBinaryWorkflow := read(filepath.Join(".github", "workflows", "binaries-upload-release.yml"))
	helmValues := read(filepath.Join("deployment", "whereabouts-chart", "values.yaml"))

	for _, doc := range []struct {
		name string
		body string
	}{
		{name: "README.md", body: readme},
		{name: "doc/extended-configuration.md", body: extended},
		{name: "doc/architecture.md", body: architecture},
	} {
		if !strings.Contains(doc.body, "Kubernetes") || !strings.Contains(doc.body, "1.28+") {
			t.Fatalf("%s must document the Kubernetes 1.28+ support floor", doc.name)
		}
		if strings.Contains(doc.body, "Kubernetes version 1.16") || strings.Contains(doc.body, "Kubernetes Version 1.16") {
			t.Fatalf("%s still documents the stale Kubernetes 1.16 support floor", doc.name)
		}
		if strings.Contains(doc.body, "only the first /65 range is addressable") {
			t.Fatalf("%s still documents the stale wide IPv6 /65 limitation", doc.name)
		}
	}

	for _, doc := range []struct {
		name string
		body string
	}{
		{name: "README.md", body: readme},
		{name: "doc/extended-configuration.md", body: extended},
	} {
		if !strings.Contains(doc.body, "Fast IPAM") || !strings.Contains(doc.body, "top-level `range`") || !strings.Contains(doc.body, "`ipRanges`") {
			t.Fatalf("%s must document the Fast IPAM top-level range limitation", doc.name)
		}
	}

	if !strings.Contains(readme, "Kubernetes CRDs") || !strings.Contains(extended, "Kubernetes CRDs") || !strings.Contains(architecture, "Kubernetes CRDs") {
		t.Fatalf("docs must identify Kubernetes CRDs as the supported storage backend")
	}
	if !strings.Contains(helmValues, "kubernetes.io/os: linux") {
		t.Fatalf("Helm values must keep the Linux node selector documented by the support matrix")
	}
	if !strings.Contains(buildWorkflow, "goarch: [amd64, arm64]") || !strings.Contains(buildWorkflow, "os: [linux]") {
		t.Fatalf("build workflow no longer matches the documented Linux amd64/arm64 build matrix")
	}
	for path, body := range map[string]string{
		".github/workflows/image-push-main.yml":    mainImageWorkflow,
		".github/workflows/image-push-release.yml": releaseImageWorkflow,
	} {
		if !strings.Contains(body, "platform: [linux/amd64, linux/arm64]") {
			t.Fatalf("%s no longer matches the documented image platform matrix", path)
		}
	}
	if !strings.Contains(releaseBinaryWorkflow, "arch: [amd64, arm64, arm]") {
		t.Fatalf("release binary workflow no longer matches the documented binary architecture matrix")
	}
}
