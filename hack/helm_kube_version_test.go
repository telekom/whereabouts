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

func TestHelmChartEnforcesSupportedKubernetesVersion(t *testing.T) {
	chart, err := os.ReadFile(filepath.Join("..", "deployment", "whereabouts-chart", "Chart.yaml"))
	if err != nil {
		t.Fatalf("read Chart.yaml: %v", err)
	}
	if !strings.Contains(string(chart), `kubeVersion: ">=1.28.0-0"`) {
		t.Fatalf("Chart.yaml must reject Kubernetes versions below 1.28")
	}

	oldCluster := exec.Command("helm", "template", "whereabouts", filepath.Join("..", "deployment", "whereabouts-chart"), "--namespace", "kube-system", "--kube-version", "1.27.0")
	oldOutput, oldErr := oldCluster.CombinedOutput()
	if oldErr == nil {
		t.Fatalf("helm template unexpectedly accepted Kubernetes 1.27:\n%s", oldOutput)
	}
	if !strings.Contains(string(oldOutput), "kubeVersion") && !strings.Contains(string(oldOutput), "1.28") {
		t.Fatalf("helm rejection for Kubernetes 1.27 did not explain kubeVersion floor:\n%s", oldOutput)
	}

	supportedCluster := exec.Command("helm", "template", "whereabouts", filepath.Join("..", "deployment", "whereabouts-chart"), "--namespace", "kube-system", "--kube-version", "1.28.0")
	supportedOutput, supportedErr := supportedCluster.CombinedOutput()
	if supportedErr != nil {
		t.Fatalf("helm template rejected supported Kubernetes 1.28: %v\n%s", supportedErr, supportedOutput)
	}
	if !strings.Contains(string(supportedOutput), "matchConditions:") {
		t.Fatalf("supported render should include webhook matchConditions:\n%s", supportedOutput)
	}
}
