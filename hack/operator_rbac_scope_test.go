// Copyright 2026 Deutsche Telekom AG
// SPDX-License-Identifier: Apache-2.0

package hack_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"k8s.io/apimachinery/pkg/util/yaml"
)

type rbacManifest struct {
	Kind     string `json:"kind"`
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Rules []rbacRule `json:"rules"`
}

type rbacRule struct {
	APIGroups     []string `json:"apiGroups"`
	Resources     []string `json:"resources"`
	ResourceNames []string `json:"resourceNames"`
	Verbs         []string `json:"verbs"`
}

func TestOperatorRBACScopesSecretAndWebhookPermissions(t *testing.T) {
	manifests := readRBACManifests(t,
		filepath.Join("..", "config", "rbac", "role.yaml"),
		filepath.Join("..", "config", "rbac", "webhook_secret_role.yaml"),
	)
	manifests = append(manifests, renderHelmRBAC(t)...)

	var secretRoleCount int
	for _, manifest := range manifests {
		if manifest.Kind == "ClusterRole" {
			assertNoClusterSecretAccess(t, manifest)
			assertWebhookUpdatesAreNamed(t, manifest)
		}
		if manifest.Kind == "Role" && roleHasSecretRotationRules(manifest) {
			secretRoleCount++
		}
	}

	if secretRoleCount < 2 {
		t.Fatalf("expected kustomize and Helm to render namespaced Secret rotation Roles, got %d", secretRoleCount)
	}
}

func assertNoClusterSecretAccess(t *testing.T, manifest rbacManifest) {
	t.Helper()
	for _, rule := range manifest.Rules {
		if has(rule.APIGroups, "") && has(rule.Resources, "secrets") {
			t.Fatalf("ClusterRole %s must not grant Secret access: %#v", manifest.Metadata.Name, rule)
		}
	}
}

func assertWebhookUpdatesAreNamed(t *testing.T, manifest rbacManifest) {
	t.Helper()
	for _, rule := range manifest.Rules {
		if !has(rule.APIGroups, "admissionregistration.k8s.io") || !has(rule.Resources, "validatingwebhookconfigurations") {
			continue
		}
		if (has(rule.Verbs, "update") || has(rule.Verbs, "patch")) && len(rule.ResourceNames) == 0 {
			t.Fatalf("ClusterRole %s updates validatingwebhookconfigurations without resourceNames: %#v", manifest.Metadata.Name, rule)
		}
	}
}

func roleHasSecretRotationRules(manifest rbacManifest) bool {
	if manifest.Kind != "Role" {
		return false
	}

	var hasWatchRule, hasNamedWriteRule bool
	for _, rule := range manifest.Rules {
		if !has(rule.APIGroups, "") || !has(rule.Resources, "secrets") {
			continue
		}
		if hasAll(rule.Verbs, "create", "list", "watch") {
			hasWatchRule = true
		}
		if len(rule.ResourceNames) > 0 && hasAll(rule.Verbs, "get", "update", "patch") {
			hasNamedWriteRule = true
		}
	}
	return hasWatchRule && hasNamedWriteRule
}

func readRBACManifests(t *testing.T, paths ...string) []rbacManifest {
	t.Helper()

	manifests := make([]rbacManifest, 0, len(paths)*2)
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		manifests = append(manifests, decodeRBACManifests(t, data)...)
	}
	return manifests
}

func renderHelmRBAC(t *testing.T) []rbacManifest {
	t.Helper()

	cmd := exec.Command("helm", "template", "whereabouts", filepath.Join("..", "deployment", "whereabouts-chart"), "--namespace", "kube-system")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helm template failed: %v\n%s", err, output)
	}
	return decodeRBACManifests(t, output)
}

func decodeRBACManifests(t *testing.T, data []byte) []rbacManifest {
	t.Helper()

	docs := bytes.Split(data, []byte("\n---"))
	manifests := make([]rbacManifest, 0, len(docs))
	for _, doc := range docs {
		doc = bytes.TrimSpace(doc)
		if len(doc) == 0 {
			continue
		}

		var manifest rbacManifest
		jsonDoc, err := yaml.ToJSON(doc)
		if err != nil {
			t.Fatalf("converting RBAC manifest to JSON:\n%s\nerror: %v", doc, err)
		}
		if err := json.Unmarshal(jsonDoc, &manifest); err != nil {
			t.Fatalf("decoding RBAC manifest:\n%s\nerror: %v", doc, err)
		}
		if manifest.Kind == "ClusterRole" || manifest.Kind == "Role" {
			manifests = append(manifests, manifest)
		}
	}
	return manifests
}

func has(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func hasAll(values []string, wants ...string) bool {
	for _, want := range wants {
		if !has(values, want) {
			return false
		}
	}
	return true
}
