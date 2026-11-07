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

// cniDaemonSet holds the fields of the CNI DaemonSet the security tests read.
type cniDaemonSet struct {
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

func (ds *cniDaemonSet) securityContextJSON(t *testing.T, name string) string {
	t.Helper()
	if len(ds.Spec.Template.Spec.Containers) != 1 {
		t.Fatalf("%s: expected one CNI DaemonSet container, got %d", name, len(ds.Spec.Template.Spec.Containers))
	}
	got, err := json.Marshal(ds.Spec.Template.Spec.Containers[0].SecurityContext)
	if err != nil {
		t.Fatalf("marshal %s securityContext: %v", name, err)
	}
	return string(got)
}

func renderHelmCNIDaemonSet(t *testing.T, args ...string) string {
	t.Helper()
	rendered := renderHelmChart(t, "kube-system", append([]string{"--show-only", "templates/daemonset.yaml"}, args...)...)
	jsonData, err := yaml.ToJSON([]byte(rendered))
	if err != nil {
		t.Fatalf("convert rendered Helm DaemonSet to JSON: %v", err)
	}
	var ds cniDaemonSet
	if err := json.Unmarshal(jsonData, &ds); err != nil {
		t.Fatalf("parse rendered Helm DaemonSet: %v", err)
	}
	return ds.securityContextJSON(t, "helm "+strings.Join(args, " "))
}

const unprivilegedCNISecurityContext = `{"allowPrivilegeEscalation":false,"capabilities":{"drop":["ALL"]},"privileged":false,` +
	`"readOnlyRootFilesystem":true,"runAsUser":0,"seccompProfile":{"type":"RuntimeDefault"}}`

func TestCNIDaemonSetSecurityContextIsUnprivilegedAndConsistent(t *testing.T) {
	var ds cniDaemonSet
	readYAML(t, filepath.Join("..", "config", "daemonset", "daemonset.yaml"), &ds)
	if got := ds.securityContextJSON(t, "config/daemonset/daemonset.yaml"); got != unprivilegedCNISecurityContext {
		t.Errorf("kustomize CNI securityContext = %s, want %s", got, unprivilegedCNISecurityContext)
	}

	// Render the chart rather than reading values.yaml so the template wiring
	// (and the seLinuxSpcT default) is covered too.
	if got := renderHelmCNIDaemonSet(t); got != unprivilegedCNISecurityContext {
		t.Errorf("helm default CNI securityContext = %s, want %s", got, unprivilegedCNISecurityContext)
	}
}

func TestHelmSELinuxSpcTAddsTypeAndKeepsSecurityContext(t *testing.T) {
	withSpcT := strings.TrimSuffix(unprivilegedCNISecurityContext, `,"seccompProfile":{"type":"RuntimeDefault"}}`) +
		`,"seLinuxOptions":{"type":"spc_t"},"seccompProfile":{"type":"RuntimeDefault"}}`
	if got := renderHelmCNIDaemonSet(t, "--set", "seLinuxSpcT=true"); got != withSpcT {
		t.Errorf("seLinuxSpcT=true CNI securityContext = %s, want %s", got, withSpcT)
	}

	got := renderHelmCNIDaemonSet(t, "--set", "seLinuxSpcT=true", "--set", "securityContext.seLinuxOptions.level=s0:c1")
	if !strings.Contains(got, `"seLinuxOptions":{"level":"s0:c1","type":"spc_t"}`) {
		t.Errorf("seLinuxSpcT=true must keep user seLinuxOptions, got %s", got)
	}

	want := `{"seLinuxOptions":{"type":"spc_t"}}`
	if got := renderHelmCNIDaemonSet(t, "--set", "seLinuxSpcT=true", "--set", "securityContext=null"); got != want {
		t.Errorf("seLinuxSpcT=true with null securityContext = %s, want %s", got, want)
	}
}

func TestHelmCNIPrivilegedOptOutRestoresPreviousSecurityContext(t *testing.T) {
	want := `{"privileged":true,"runAsUser":0}`
	if got := renderHelmCNIDaemonSet(t, "--set", "cniPrivileged=true"); got != want {
		t.Errorf("cniPrivileged=true CNI securityContext = %s, want %s", got, want)
	}
}
