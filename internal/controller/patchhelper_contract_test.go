// SPDX-FileCopyrightText: 2026 Deutsche Telekom AG
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"errors"
	"reflect"
	"testing"

	fluxpatch "github.com/fluxcd/pkg/runtime/patch"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	whereaboutsv1alpha1 "github.com/telekom/whereabouts/api/whereabouts.cni.cncf.io/v1alpha1"
)

func snapshotPool() *whereaboutsv1alpha1.NodeSlicePool {
	return &whereaboutsv1alpha1.NodeSlicePool{
		ObjectMeta: metav1.ObjectMeta{Name: "slices", Namespace: "default"},
		Spec:       whereaboutsv1alpha1.NodeSlicePoolSpec{Range: "10.0.0.0/24", SliceSize: "25"},
		Status: whereaboutsv1alpha1.NodeSlicePoolStatus{
			Allocations: []whereaboutsv1alpha1.NodeSliceAllocation{{NodeName: "node-a", SliceRange: "10.0.0.0/25"}},
		},
	}
}

func snapshotClient(t *testing.T, pool *whereaboutsv1alpha1.NodeSlicePool, hooks interceptor.Funcs) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := whereaboutsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(pool).WithStatusSubresource(pool).WithInterceptorFuncs(hooks).Build()
}

func TestPatchHelperWriteCounts(t *testing.T) {
	for _, tc := range []struct {
		name           string
		object, status bool
		want           []string
	}{
		{"no-op", false, false, nil},
		{"object", true, false, []string{"object"}},
		{"status", false, true, []string{"status"}},
		{"both", true, true, []string{"object", "status"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			pool := snapshotPool()
			var writes []string
			c := snapshotClient(t, pool, interceptor.Funcs{
				Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
					writes = append(writes, "object")
					return c.Patch(ctx, obj, patch, opts...)
				},
				SubResourceUpdate: func(ctx context.Context, c client.Client, name string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
					writes = append(writes, name)
					return c.SubResource(name).Update(ctx, obj, opts...)
				},
			})
			if err := c.Get(ctx, client.ObjectKeyFromObject(pool), pool); err != nil {
				t.Fatal(err)
			}
			h, err := NewPatchHelper(pool, c)
			if err != nil {
				t.Fatal(err)
			}
			if tc.object {
				pool.Spec.Range = "10.1.0.0/24"
			}
			if tc.status {
				pool.Status.Allocations[0].SliceRange = "10.1.0.0/25"
			}
			desired := pool.DeepCopy().Status
			if err := h.Patch(ctx, pool); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(writes, tc.want) {
				t.Fatalf("writes: %v, want %v", writes, tc.want)
			}
			var stored whereaboutsv1alpha1.NodeSlicePool
			if err := c.Get(ctx, client.ObjectKeyFromObject(pool), &stored); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(stored.Status, desired) || !reflect.DeepEqual(pool.Status, desired) {
				t.Fatal("desired status lost to object patch response")
			}
		})
	}
}

func TestPatchHelperSpecFailureGatesStatus(t *testing.T) {
	for _, local := range []bool{true, false} {
		name := "Flux"
		if local {
			name = "local"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			pool := snapshotPool()
			specErr := errors.New("spec patch denied")
			statusWrites := 0
			c := snapshotClient(t, pool, interceptor.Funcs{
				Patch: func(context.Context, client.WithWatch, client.Object, client.Patch, ...client.PatchOption) error {
					return specErr
				},
				SubResourceUpdate: func(ctx context.Context, c client.Client, name string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
					statusWrites++
					return c.SubResource(name).Update(ctx, obj, opts...)
				},
				SubResourcePatch: func(ctx context.Context, c client.Client, name string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
					statusWrites++
					return c.SubResource(name).Patch(ctx, obj, patch, opts...)
				},
			})
			if err := c.Get(ctx, client.ObjectKeyFromObject(pool), pool); err != nil {
				t.Fatal(err)
			}
			var patch func(context.Context, client.Object) error
			if local {
				h, err := NewPatchHelper(pool, c)
				if err != nil {
					t.Fatal(err)
				}
				patch = h.Patch
			} else {
				h, err := fluxpatch.NewHelper(pool, c)
				if err != nil {
					t.Fatal(err)
				}
				patch = func(ctx context.Context, obj client.Object) error { return h.Patch(ctx, obj) }
			}
			pool.Spec.Range = "10.1.0.0/24"
			pool.Status.Allocations[0].SliceRange = "10.1.0.0/25"
			if err := patch(ctx, pool); !errors.Is(err, specErr) {
				t.Fatalf("lost spec failure: %v", err)
			}
			var stored whereaboutsv1alpha1.NodeSlicePool
			if err := c.Get(ctx, client.ObjectKeyFromObject(pool), &stored); err != nil {
				t.Fatal(err)
			}
			if stored.Spec.Range != "10.0.0.0/24" {
				t.Fatal("failed spec persisted")
			}
			if local {
				if statusWrites != 0 || stored.Status.Allocations[0].SliceRange != "10.0.0.0/25" {
					t.Fatal("failed spec published incompatible allocations")
				}
			} else if statusWrites == 0 || stored.Status.Allocations[0].SliceRange != "10.1.0.0/25" {
				t.Fatal("Flux comparison no longer demonstrates unconditional status patching; reconsider local helper")
			}
		})
	}
}
