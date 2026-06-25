// Copyright 2026 Deutsche Telekom AG
// SPDX-License-Identifier: Apache-2.0

package hack_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestKustomizeDefaultOverlayRendersCustomWebhookNamespace(t *testing.T) {
	ensureKustomize(t)

	rendered := renderKustomizeDefaultOverlay(t, "custom-ns")
	assertContainsAll(t, rendered,
		"cert-controller.cert-rotation/inject-ca-from: custom-ns/whereabouts-webhook-cert",
		"namespace: custom-ns",
		`system:serviceaccount:custom-ns:whereabouts-cni`,
		`system:serviceaccount:custom-ns:whereabouts-whereabouts-operator`,
	)
	assertContainsNone(t, rendered,
		"cert-controller.cert-rotation/inject-ca-from: kube-system/whereabouts-webhook-cert",
		`system:serviceaccount:kube-system:whereabouts-cni`,
		`system:serviceaccount:kube-system:whereabouts-whereabouts-operator`,
	)
}

func TestHelmEmptyNamespaceOverrideRendersReleaseNamespace(t *testing.T) {
	rendered := renderHelmChart(t, "custom-ns", "--set", "namespaceOverride=")
	assertContainsAll(t, rendered,
		"cert-controller.cert-rotation/inject-ca-from: custom-ns/whereabouts-whereabouts-chart-webhook-cert",
		"namespace: custom-ns",
		"- --namespace=custom-ns",
		`system:serviceaccount:custom-ns:whereabouts-whereabouts-chart`,
		`system:serviceaccount:custom-ns:whereabouts-whereabouts-chart-operator`,
	)
	assertContainsNone(t, rendered,
		"namespace: null",
		"system:serviceaccount::",
	)
}

func TestHelmCustomCNIPathsWireInstallCNIEnvironment(t *testing.T) {
	rendered := renderHelmChart(t, "custom-ns",
		"--set", "namespaceOverride=",
		"--set", "cniConf.confDir=/var/lib/cni/net.d",
		"--set", "cniConf.binDir=/var/lib/cni/bin",
	)

	assertContainsAll(t, rendered,
		`- name: CNI_BIN_DIR`,
		`value: "/host/var/lib/cni/bin"`,
		`- name: CNI_CONF_DIR`,
		`value: "/host/var/lib/cni/net.d"`,
		`mountPath: "/host/var/lib/cni/bin"`,
		`mountPath: "/host/var/lib/cni/net.d"`,
		`path: "/var/lib/cni/bin"`,
		`path: "/var/lib/cni/net.d"`,
	)
	assertContainsNone(t, rendered,
		"mountPath: /host/opt/cni/bin",
		"mountPath: /host/etc/cni/net.d",
	)
}

func ensureKustomize(t *testing.T) {
	t.Helper()

	cmd := exec.Command("make", "kustomize")
	cmd.Dir = ".."
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("installing kustomize: %v\n%s", err, output)
	}
}

func renderKustomizeDefaultOverlay(t *testing.T, namespace string) string {
	t.Helper()

	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolving repo root: %v", err)
	}

	overlayDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolving overlay temp directory: %v", err)
	}
	repoRoot, err = filepath.EvalSymlinks(repoRoot)
	if err != nil {
		t.Fatalf("resolving repo root symlinks: %v", err)
	}
	resourcePath, err := filepath.Rel(overlayDir, filepath.Join(repoRoot, "config", "default"))
	if err != nil {
		t.Fatalf("resolving relative default overlay path: %v", err)
	}

	kustomization := "apiVersion: kustomize.config.k8s.io/v1beta1\n" +
		"kind: Kustomization\n" +
		"namespace: " + namespace + "\n" +
		"resources:\n" +
		"- " + resourcePath + "\n"
	if err := os.WriteFile(filepath.Join(overlayDir, "kustomization.yaml"), []byte(kustomization), 0600); err != nil {
		t.Fatalf("writing test kustomization: %v", err)
	}

	cmd := exec.Command(filepath.Join("..", "bin", "kustomize"), "build", overlayDir, "--load-restrictor=LoadRestrictionsNone")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rendering custom namespace kustomize overlay: %v\n%s", err, output)
	}
	return string(output)
}

func renderHelmChart(t *testing.T, namespace string, args ...string) string {
	t.Helper()

	baseArgs := make([]string, 0, 5+len(args))
	baseArgs = append(baseArgs, "template", "whereabouts", filepath.Join("..", "deployment", "whereabouts-chart"), "--namespace", namespace)
	cmd := exec.Command("helm", append(baseArgs, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rendering Helm chart: %v\n%s", err, output)
	}
	return string(output)
}

func assertContainsAll(t *testing.T, text string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(text, want) {
			t.Fatalf("rendered output missing %q", want)
		}
	}
}

func assertContainsNone(t *testing.T, text string, disallowed ...string) {
	t.Helper()
	for _, value := range disallowed {
		if strings.Contains(text, value) {
			t.Fatalf("rendered output unexpectedly contains %q", value)
		}
	}
}
