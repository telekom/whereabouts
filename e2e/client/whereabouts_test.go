package client

import (
	stderrors "errors"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
)

func TestPodDeleteTimeoutAllowsSlowCNITeardown(t *testing.T) {
	if podDeleteTimeout < 2*time.Minute {
		t.Fatalf("podDeleteTimeout = %s, want at least 2m for slow CI CNI teardown", podDeleteTimeout)
	}
}

func TestStatefulSetReplicasOrDefault(t *testing.T) {
	tests := []struct {
		name     string
		replicas *int32
		want     int32
	}{
		{
			name: "defaults nil replicas to one",
			want: 1,
		},
		{
			name:     "uses configured replicas",
			replicas: ptrTo[int32](4),
			want:     4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			statefulSet := &appsv1.StatefulSet{
				Spec: appsv1.StatefulSetSpec{
					Replicas: tt.replicas,
				},
			}

			if got := statefulSetReplicasOrDefault(statefulSet); got != tt.want {
				t.Fatalf("statefulSetReplicasOrDefault() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestSetStatefulSetReplicasRetriesConflicts(t *testing.T) {
	clientset := fake.NewClientset(&appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web",
			Namespace: "default",
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: ptrTo[int32](1),
		},
	})

	updateCalls := 0
	clientset.PrependReactor("update", "statefulsets", func(clienttesting.Action) (bool, runtime.Object, error) {
		updateCalls++
		if updateCalls == 1 {
			return true, nil, apierrors.NewConflict(
				schema.GroupResource{Group: "apps", Resource: "statefulsets"},
				"web",
				stderrors.New("stale resource version"),
			)
		}
		return false, nil, nil
	})

	err := updateStatefulSetReplicas(clientset.AppsV1().StatefulSets("default"), "web", func(int32) int32 {
		return 3
	})
	if err != nil {
		t.Fatalf("updateStatefulSetReplicas returned error: %v", err)
	}
	if updateCalls != 2 {
		t.Fatalf("StatefulSet update calls = %d, want 2", updateCalls)
	}

	statefulSet, err := clientset.AppsV1().StatefulSets("default").Get(t.Context(), "web", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get statefulset: %v", err)
	}
	if got := statefulSetReplicasOrDefault(statefulSet); got != 3 {
		t.Fatalf("StatefulSet replicas = %d, want 3", got)
	}
}

func ptrTo[T any](v T) *T {
	return &v
}
