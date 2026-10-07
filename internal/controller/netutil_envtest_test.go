//go:build integration

// SPDX-FileCopyrightText: 2026 Deutsche Telekom AG
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	nadv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	whereaboutsv1alpha1 "github.com/telekom/whereabouts/api/whereabouts.cni.cncf.io/v1alpha1"
)

func TestNetutilNodeSlicePersistence(t *testing.T) {
	assets, err := filepath.Abs("../../bin/envtest")
	if err != nil {
		t.Fatal(err)
	}
	preserve := true
	testEnv := &envtest.Environment{
		CRDDirectoryPaths:           []string{"../../config/crd/bases"},
		ErrorIfCRDPathMissing:       true,
		BinaryAssetsDirectory:       assets,
		DownloadBinaryAssets:        true,
		DownloadBinaryAssetsVersion: "1.37.0",
		CRDs: []*apiextensionsv1.CustomResourceDefinition{{
			ObjectMeta: metav1.ObjectMeta{Name: "network-attachment-definitions.k8s.cni.cncf.io"},
			Spec: apiextensionsv1.CustomResourceDefinitionSpec{
				Group: "k8s.cni.cncf.io", Scope: apiextensionsv1.NamespaceScoped,
				Names: apiextensionsv1.CustomResourceDefinitionNames{Plural: "network-attachment-definitions", Kind: "NetworkAttachmentDefinition"},
				Versions: []apiextensionsv1.CustomResourceDefinitionVersion{{
					Name: "v1", Served: true, Storage: true,
					Schema: &apiextensionsv1.CustomResourceValidation{OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
						Type: "object", Properties: map[string]apiextensionsv1.JSONSchemaProps{
							"spec": {Type: "object", XPreserveUnknownFields: &preserve},
						},
					}},
				}},
			},
		}},
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
	for _, add := range []func(*runtime.Scheme) error{corev1.AddToScheme, nadv1.AddToScheme, whereaboutsv1alpha1.AddToScheme} {
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
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "subdivision-"}}
	if err := c.Create(ctx, ns); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"node-b", "node-a"} {
		if err := c.Create(ctx, &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name}}); err != nil {
			t.Fatal(err)
		}
	}
	r := &NodeSliceReconciler{client: c, recorder: events.NewFakeRecorder(20)}
	for i, tc := range []struct {
		cidr, bits string
		want       []string
	}{
		{"10.0.0.0/30", "31", []string{"10.0.0.0/31", "10.0.0.2/31"}},
		{"fd00::/126", "127", []string{"fd00::/127", "fd00::2/127"}},
		{"10.0.0.1/32", "32", []string{"10.0.0.1/32"}},
		{"fd00::1/128", "128", []string{"fd00::1/128"}},
	} {
		t.Run(tc.cidr, func(t *testing.T) {
			name := fmt.Sprintf("slices-%d", i)
			config, err := json.Marshal(map[string]any{
				"cniVersion": "0.3.1", "name": name, "type": "macvlan",
				"ipam": map[string]string{"type": "whereabouts", "range": tc.cidr, "node_slice_size": tc.bits},
			})
			if err != nil {
				t.Fatal(err)
			}
			nad := &nadv1.NetworkAttachmentDefinition{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns.Name},
				Spec:       nadv1.NetworkAttachmentDefinitionSpec{Config: string(config)},
			}
			if err := c.Create(ctx, nad); err != nil {
				t.Fatal(err)
			}
			req := ctrl.Request{NamespacedName: types.NamespacedName{Name: name, Namespace: ns.Name}}
			for range 2 {
				if _, err := r.Reconcile(ctx, req); err != nil {
					t.Fatal(err)
				}
				conf, err := parseNADIPAMConfig(nad.Spec.Config)
				if err != nil {
					t.Fatal(err)
				}
				var pool whereaboutsv1alpha1.NodeSlicePool
				if err := c.Get(ctx, types.NamespacedName{Name: nodeSlicePoolName(conf), Namespace: ns.Name}, &pool); err != nil {
					t.Fatal(err)
				}
				if len(pool.Status.Allocations) != len(tc.want) || pool.Status.TotalSlices != int32(len(tc.want)) {
					t.Fatalf("unexpected slice status: %+v", pool.Status)
				}
				for j, want := range tc.want {
					got := pool.Status.Allocations[j]
					if got.SliceRange != want || got.NodeName != []string{"node-a", "node-b"}[j] {
						t.Fatalf("slice %d: got %+v, want %s", j, got, want)
					}
				}
			}
		})
	}
	t.Run("IPPool resolved allocations and overlap statistics", func(t *testing.T) {
		pool := &whereaboutsv1alpha1.IPPool{
			ObjectMeta: metav1.ObjectMeta{Name: "wide-ipv6", Namespace: ns.Name},
			Spec: whereaboutsv1alpha1.IPPoolSpec{
				Range: "fd00::/63",
				Allocations: map[string]whereaboutsv1alpha1.IPAllocation{
					"1":                    {PodRef: ns.Name + "/first", ContainerID: "first", IfName: "net1"},
					"18446744073709551617": {PodRef: ns.Name + "/wide", ContainerID: "wide", IfName: "net1"},
				},
			},
		}
		if err := c.Create(ctx, pool); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"first", "wide"} {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns.Name},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "pause", Image: "registry.k8s.io/pause:3.10"}}},
			}
			if err := c.Create(ctx, pod); err != nil {
				t.Fatal(err)
			}
			pod.Status.Phase = corev1.PodRunning
			if err := c.Status().Update(ctx, pod); err != nil {
				t.Fatal(err)
			}
		}
		reservation := &whereaboutsv1alpha1.OverlappingRangeIPReservation{
			ObjectMeta: metav1.ObjectMeta{Name: "fd00--1", Namespace: ns.Name},
			Spec:       whereaboutsv1alpha1.OverlappingRangeIPReservationSpec{PodRef: ns.Name + "/first", ContainerID: "first", IfName: "net1"},
		}
		if err := c.Create(ctx, reservation); err != nil {
			t.Fatal(err)
		}
		reconciler := newIPPoolReconciler(c, events.NewFakeRecorder(20), time.Minute, ReconcilerOptions{})
		for range 2 {
			if _, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(pool)}); err != nil {
				t.Fatal(err)
			}
		}
		if err := c.Get(ctx, client.ObjectKeyFromObject(pool), pool); err != nil {
			t.Fatal(err)
		}
		if pool.Status.UsedIPs != 2 || pool.Status.OverlappingReservations != 1 || len(pool.Status.AllocatedIPs) != 2 {
			t.Fatalf("unexpected IPPool status: %+v", pool.Status)
		}
		for _, allocation := range pool.Status.AllocatedIPs {
			want := map[string]string{ns.Name + "/first": "fd00::1", ns.Name + "/wide": "fd00:0:0:1::1"}[allocation.PodRef]
			if allocation.IP != want {
				t.Fatalf("resolved %s as %s, want %s", allocation.PodRef, allocation.IP, want)
			}
		}
	})
}
