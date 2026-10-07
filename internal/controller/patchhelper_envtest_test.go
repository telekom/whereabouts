//go:build integration

// SPDX-FileCopyrightText: 2026 Deutsche Telekom AG
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	whereaboutsv1alpha1 "github.com/telekom/whereabouts/api/whereabouts.cni.cncf.io/v1alpha1"
)

func TestPatchHelperAPIContract(t *testing.T) {
	assets, err := filepath.Abs("../../bin/envtest")
	if err != nil {
		t.Fatal(err)
	}
	testEnv := &envtest.Environment{
		CRDDirectoryPaths: []string{"../../config/crd/bases"}, ErrorIfCRDPathMissing: true,
		BinaryAssetsDirectory: assets, DownloadBinaryAssets: true, DownloadBinaryAssetsVersion: "1.37.0",
	}
	cfg, err := testEnv.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := testEnv.Stop(); err != nil {
			t.Error(err)
		}
	})
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{corev1.AddToScheme, whereaboutsv1alpha1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "patch-contract-"}}
	if err := c.Create(ctx, ns); err != nil {
		t.Fatal(err)
	}
	pool := snapshotPool()
	pool.Namespace = ns.Name
	initialStatus := pool.Status
	if err := c.Create(ctx, pool); err != nil {
		t.Fatal(err)
	}
	pool.Status = initialStatus
	if err := c.Status().Update(ctx, pool); err != nil {
		t.Fatal(err)
	}

	t.Run("stale status conflicts without clobbering concurrent status", func(t *testing.T) {
		stale := pool.DeepCopy()
		h, err := NewPatchHelper(stale, c)
		if err != nil {
			t.Fatal(err)
		}
		pool.Status.TotalSlices = 2
		if err := c.Status().Update(ctx, pool); err != nil {
			t.Fatal(err)
		}
		stale.Status.TotalSlices = 99
		err = h.Patch(ctx, stale)
		aggregate, ok := err.(kerrors.Aggregate)
		if !ok || len(aggregate.Errors()) != 1 || !apierrors.IsConflict(aggregate.Errors()[0]) {
			t.Fatalf("expected aggregated real resourceVersion conflict, got %v", err)
		}
		var stored whereaboutsv1alpha1.NodeSlicePool
		if err := c.Get(ctx, client.ObjectKeyFromObject(pool), &stored); err != nil {
			t.Fatal(err)
		}
		if stored.Status.TotalSlices != 2 {
			t.Fatal("stale status clobbered concurrent writer")
		}
	})
	t.Run("object response restores desired status and preserves unrelated metadata", func(t *testing.T) {
		if err := c.Get(ctx, client.ObjectKeyFromObject(pool), pool); err != nil {
			t.Fatal(err)
		}
		h, err := NewPatchHelper(pool, c)
		if err != nil {
			t.Fatal(err)
		}
		concurrent := pool.DeepCopy()
		concurrent.Labels = map[string]string{"unrelated": "keep"}
		concurrent.Annotations = map[string]string{"other-controller": "keep"}
		if err := c.Update(ctx, concurrent); err != nil {
			t.Fatal(err)
		}
		pool.Spec.Range = "10.1.0.0/24"
		pool.Status.Allocations[0].SliceRange = "10.1.0.0/25"
		desired := pool.DeepCopy().Status
		if err := h.Patch(ctx, pool); err != nil {
			t.Fatal(err)
		}
		var stored whereaboutsv1alpha1.NodeSlicePool
		if err := c.Get(ctx, client.ObjectKeyFromObject(pool), &stored); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(stored.Status, desired) || !reflect.DeepEqual(pool.Status, desired) {
			t.Fatal("desired status lost after spec PATCH")
		}
		if stored.Spec.Range != "10.1.0.0/24" || stored.Labels["unrelated"] != "keep" || stored.Annotations["other-controller"] != "keep" {
			t.Fatal("spec update removed unrelated metadata")
		}
		if pool.ResourceVersion != stored.ResourceVersion || pool.ResourceVersion == concurrent.ResourceVersion {
			t.Fatal("caller did not receive latest server resourceVersion")
		}
	})
}
