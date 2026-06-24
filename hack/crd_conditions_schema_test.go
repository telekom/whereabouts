// Copyright 2026 Deutsche Telekom AG
// SPDX-License-Identifier: Apache-2.0

package hack_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/util/yaml"
)

type customResourceDefinition struct {
	Spec struct {
		Versions []struct {
			Name   string `json:"name"`
			Schema struct {
				OpenAPIV3Schema map[string]interface{} `json:"openAPIV3Schema"`
			} `json:"schema"`
		} `json:"versions"`
	} `json:"spec"`
}

func TestCRDConditionsAreListMaps(t *testing.T) {
	crdFiles := []string{
		"whereabouts.cni.cncf.io_ippools.yaml",
		"whereabouts.cni.cncf.io_nodeslicepools.yaml",
		"whereabouts.cni.cncf.io_overlappingrangeipreservations.yaml",
	}
	crdDirs := []string{
		filepath.Join("..", "config", "crd", "bases"),
		filepath.Join("..", "deployment", "whereabouts-chart", "crds"),
	}

	for _, dir := range crdDirs {
		for _, file := range crdFiles {
			path := filepath.Join(dir, file)
			t.Run(path, func(t *testing.T) {
				crd := readCRD(t, path)
				for _, version := range crd.Spec.Versions {
					conditions := schemaPath(t, version.Schema.OpenAPIV3Schema, "properties", "status", "properties", "conditions")
					if got := conditions["x-kubernetes-list-type"]; got != "map" {
						t.Fatalf("%s version %s status.conditions x-kubernetes-list-type = %v, want map", path, version.Name, got)
					}
					keys, ok := conditions["x-kubernetes-list-map-keys"].([]interface{})
					if !ok {
						t.Fatalf("%s version %s status.conditions missing x-kubernetes-list-map-keys", path, version.Name)
					}
					if len(keys) != 1 || keys[0] != "type" {
						t.Fatalf("%s version %s status.conditions x-kubernetes-list-map-keys = %#v, want [type]", path, version.Name, keys)
					}
				}
			})
		}
	}
}

func TestOpenAPIViolationsDoNotAllowStatusConditionsListTypeFailures(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "hack", "openapi-violations.list"))
	if err != nil {
		t.Fatalf("reading openapi violations: %v", err)
	}

	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, "list_type_missing") && strings.Contains(line, ",Status,Conditions") {
			t.Fatalf("status.conditions list-type violation must not be allowed: %s", line)
		}
	}
}

func readCRD(t *testing.T, path string) customResourceDefinition {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	jsonData, err := yaml.ToJSON(data)
	if err != nil {
		t.Fatalf("converting %s to JSON: %v", path, err)
	}

	var crd customResourceDefinition
	if err := json.Unmarshal(jsonData, &crd); err != nil {
		t.Fatalf("decoding %s: %v", path, err)
	}
	if len(crd.Spec.Versions) == 0 {
		t.Fatalf("%s has no spec.versions", path)
	}
	return crd
}

func schemaPath(t *testing.T, root map[string]interface{}, path ...string) map[string]interface{} {
	t.Helper()

	current := root
	for _, key := range path {
		next, ok := current[key].(map[string]interface{})
		if !ok {
			t.Fatalf("schema path %v missing at %q", path, key)
		}
		current = next
	}
	return current
}
