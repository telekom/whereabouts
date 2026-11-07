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

func TestCNIDaemonSetSecurityContextIsUnprivilegedAndConsistent(t *testing.T) {
	var values struct {
		SecurityContext map[string]interface{} `json:"securityContext"`
		SELinuxSpcT     bool                   `json:"seLinuxSpcT"`
	}
	readYAML(t, filepath.Join("..", "deployment", "whereabouts-chart", "values.yaml"), &values)

	var ds struct {
		Spec struct {
			Template struct {
				Spec struct {
					Containers []struct {
						SecurityContext map[string]interface{} `json:"securityContext"`
					} `json:"containers"`
				} `json:"spec"`
			} `json:"template"`
		} `json:"spec"`
	}
	readYAML(t, filepath.Join("..", "config", "daemonset", "daemonset.yaml"), &ds)
	if len(ds.Spec.Template.Spec.Containers) != 1 {
		t.Fatalf("expected one CNI DaemonSet container, got %d", len(ds.Spec.Template.Spec.Containers))
	}

	want := `{"allowPrivilegeEscalation":false,"capabilities":{"drop":["ALL"]},"privileged":false,` +
		`"readOnlyRootFilesystem":true,"runAsUser":0,"seccompProfile":{"type":"RuntimeDefault"}}`
	for name, sc := range map[string]map[string]interface{}{
		"helm values.yaml":           values.SecurityContext,
		"config/daemonset/daemonset": ds.Spec.Template.Spec.Containers[0].SecurityContext,
	} {
		got, err := json.Marshal(sc)
		if err != nil {
			t.Fatalf("marshal %s securityContext: %v", name, err)
		}
		if string(got) != want {
			t.Errorf("%s CNI securityContext = %s, want %s", name, got, want)
		}
	}
	if values.SELinuxSpcT {
		t.Errorf("seLinuxSpcT must default to false")
	}
}
