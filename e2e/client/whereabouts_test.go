package client

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
)

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

func ptrTo[T any](v T) *T {
	return &v
}
