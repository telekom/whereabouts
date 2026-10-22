// Copyright 2026 Deutsche Telekom AG
// SPDX-License-Identifier: Apache-2.0

package hack_test

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/yaml"
)

func TestE2ESetupUsesTrackedMultusManifest(t *testing.T) {
	scripts := []string{
		filepath.Join("..", "hack", "e2e-setup-kind-cluster.sh"),
		filepath.Join("..", "hack", "e2e-setup-kind-cluster-helm.sh"),
	}

	for _, script := range scripts {
		t.Run(script, func(t *testing.T) {
			data, err := os.ReadFile(script)
			if err != nil {
				t.Fatalf("reading %s: %v", script, err)
			}
			contents := string(data)

			if !strings.Contains(contents, `MULTUS_DAEMONSET_PATH="$ROOT/hack/multus-install.yml"`) ||
				!strings.Contains(contents, `kubectl create -f "${MULTUS_DAEMONSET_PATH}"`) {
				t.Fatalf("%s must install the local Dependabot-tracked Multus manifest", script)
			}
			if !strings.Contains(contents, "rollout status daemonset/kube-multus-ds") {
				t.Fatalf("%s must wait for Multus readiness on every node", script)
			}
			for _, check := range []string{
				`kind get nodes --name "$KIND_CLUSTER_NAME"`,
				`retry "$OCI_BIN" exec "$node" /bin/sh -ec`,
				"test -s /etc/cni/net.d/00-multus.conf",
				"test -s /etc/cni/net.d/multus.d/multus.kubeconfig",
				"test -x /opt/cni/bin/multus",
			} {
				if !strings.Contains(contents, check) {
					t.Fatalf("%s must check host Multus installation: %s", script, check)
				}
			}
		})
	}
}

func TestE2EMultusHasPinnedImagesAndMemoryHeadroom(t *testing.T) {
	data, err := os.ReadFile("multus-install.yml")
	if err != nil {
		t.Fatal(err)
	}
	imagePattern := regexp.MustCompile(`(?m)^\s+image: ghcr\.io/k8snetworkplumbingwg/multus-cni:v[0-9]+\.[0-9]+\.[0-9]+@sha256:[0-9a-f]{64}$`)
	if len(imagePattern.FindAll(data, -1)) != 2 {
		t.Fatal("both Multus images must use stable tags and immutable digests")
	}
	decoder := yaml.NewYAMLOrJSONDecoder(strings.NewReader(string(data)), 4096)
	for {
		var object struct {
			Kind string
			Spec appsv1.DaemonSetSpec
		}
		if err := decoder.Decode(&object); err == io.EOF {
			t.Fatal("Multus DaemonSet not found")
		} else if err != nil {
			t.Fatal(err)
		}
		if object.Kind != "DaemonSet" {
			continue
		}
		if len(object.Spec.Template.Spec.Containers) != 1 {
			t.Fatal("expected one Multus container")
		}
		container := object.Spec.Template.Spec.Containers[0]
		if container.Resources.Limits.Memory().Cmp(resource.MustParse("128Mi")) < 0 {
			t.Fatal("Multus needs at least 128Mi to avoid the observed 50Mi startup OOM")
		}
		if container.ReadinessProbe != nil && container.ReadinessProbe.Exec != nil {
			t.Fatal("the distroless Multus image cannot run shell-based readiness probes")
		}
		return
	}
}
