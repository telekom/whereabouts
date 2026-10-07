package entities

import (
	"strings"
	"testing"
)

func TestGeneratedWorkloadsUsePinnedTestImage(t *testing.T) {
	if !strings.Contains(testImage, "@sha256:") || strings.Contains(testImage, ":latest") {
		t.Fatalf("test image must be digest-pinned and must not use latest: %s", testImage)
	}

	pod := PodObject("samplepod", "default", nil, nil)
	if got := pod.Spec.Containers[0].Image; got != testImage {
		t.Fatalf("pod image = %s, want %s", got, testImage)
	}

	replicaSet := ReplicaSetObject(1, "samplers", "default", map[string]string{"app": "sample"}, nil)
	if got := replicaSet.Spec.Template.Spec.Containers[0].Image; got != testImage {
		t.Fatalf("replicaset image = %s, want %s", got, testImage)
	}

	statefulSet := StatefulSetSpec("sampless", "default", "samplesvc", 1, nil)
	if got := statefulSet.Spec.Template.Spec.Containers[0].Image; got != testImage {
		t.Fatalf("statefulset image = %s, want %s", got, testImage)
	}
}
