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

func TestHelmChartDefaultImageTagMatchesAppVersion(t *testing.T) {
	var chart struct {
		Version    string `json:"version"`
		AppVersion string `json:"appVersion"`
	}
	readYAML(t, filepath.Join("..", "deployment", "whereabouts-chart", "Chart.yaml"), &chart)

	var values struct {
		Image struct {
			Repository string `json:"repository"`
			Tag        string `json:"tag"`
		} `json:"image"`
	}
	readYAML(t, filepath.Join("..", "deployment", "whereabouts-chart", "values.yaml"), &values)

	if chart.AppVersion == "" || !strings.HasPrefix(chart.AppVersion, "v") {
		t.Fatalf("chart appVersion should use release tag form, got %q", chart.AppVersion)
	}
	if chart.Version != strings.TrimPrefix(chart.AppVersion, "v") {
		t.Fatalf("chart version %q should match appVersion %q without the leading v", chart.Version, chart.AppVersion)
	}
	if values.Image.Repository != "ghcr.io/telekom/whereabouts" {
		t.Fatalf("unexpected default image repository %q", values.Image.Repository)
	}
	if values.Image.Tag != chart.AppVersion {
		t.Fatalf("default image tag %q should match chart appVersion %q", values.Image.Tag, chart.AppVersion)
	}
}

func readYAML(t *testing.T, path string, into interface{}) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	jsonData, err := yaml.ToJSON(data)
	if err != nil {
		t.Fatalf("convert %s to JSON: %v", path, err)
	}
	if err := json.Unmarshal(jsonData, into); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
}
