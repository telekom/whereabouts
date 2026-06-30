// Copyright 2026 Deutsche Telekom AG
// SPDX-License-Identifier: Apache-2.0

package hack_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestE2ESetupPinsMultusManifestToCommit(t *testing.T) {
	scripts := []string{
		filepath.Join("..", "hack", "e2e-setup-kind-cluster.sh"),
		filepath.Join("..", "hack", "e2e-setup-kind-cluster-helm.sh"),
	}

	refPattern := regexp.MustCompile(`(?m)^MULTUS_DAEMONSET_REF="([0-9a-f]{40})"$`)
	for _, script := range scripts {
		t.Run(script, func(t *testing.T) {
			data, err := os.ReadFile(script)
			if err != nil {
				t.Fatalf("reading %s: %v", script, err)
			}
			contents := string(data)

			if strings.Contains(contents, "raw.githubusercontent.com/k8snetworkplumbingwg/multus-cni/master/") ||
				strings.Contains(contents, "raw.githubusercontent.com/k8snetworkplumbingwg/multus-cni/main/") {
				t.Fatalf("%s must not install Multus from a mutable branch", script)
			}
			if !refPattern.MatchString(contents) {
				t.Fatalf("%s must set MULTUS_DAEMONSET_REF to a 40-character commit SHA", script)
			}
			if !strings.Contains(contents, `raw.githubusercontent.com/k8snetworkplumbingwg/multus-cni/${MULTUS_DAEMONSET_REF}/deployments/multus-daemonset.yml`) {
				t.Fatalf("%s must build the Multus manifest URL from MULTUS_DAEMONSET_REF", script)
			}
		})
	}
}
