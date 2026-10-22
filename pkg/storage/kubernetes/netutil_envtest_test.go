//go:build integration

// SPDX-FileCopyrightText: 2026 Deutsche Telekom AG
// SPDX-License-Identifier: Apache-2.0

package kubernetes

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	whereaboutsv1alpha1 "github.com/telekom/whereabouts/api/whereabouts.cni.cncf.io/v1alpha1"
	"github.com/telekom/whereabouts/pkg/allocate"
	wbclient "github.com/telekom/whereabouts/pkg/generated/clientset/versioned"
	whereaboutstypes "github.com/telekom/whereabouts/pkg/types"
)

func TestNetutilIPPoolRoundTrip(t *testing.T) {
	assets, err := filepath.Abs("../../../bin/envtest")
	if err != nil {
		t.Fatal(err)
	}
	testEnv := &envtest.Environment{
		CRDDirectoryPaths:           []string{"../../../config/crd/bases"},
		ErrorIfCRDPathMissing:       true,
		BinaryAssetsDirectory:       assets,
		DownloadBinaryAssets:        true,
		DownloadBinaryAssetsVersion: "1.37.0",
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
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	client, err := wbclient.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ns, err := cs.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "netutil-"}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}

	for i, tc := range []struct {
		cidr, first, last string
		l3                bool
	}{
		{"10.0.0.0/30", "10.0.0.1", "10.0.0.2", false},
		{"10.0.0.0/31", "10.0.0.0", "10.0.0.1", false},
		{"10.0.0.1/32", "10.0.0.1", "10.0.0.1", false},
		{"fd00::/126", "fd00::1", "fd00::2", false},
		{"fd00::/127", "fd00::", "fd00::1", false},
		{"fd00::1/128", "fd00::1", "fd00::1", false},
		{"10.0.0.0/30", "10.0.0.0", "10.0.0.3", true},
		{"fd00::/126", "fd00::", "fd00::3", true},
		{"::/63", "::1", "0:0:0:1::1", false},
	} {
		t.Run(fmt.Sprintf("%s/l3=%t", tc.cidr, tc.l3), func(t *testing.T) {
			pool, err := client.WhereaboutsV1alpha1().IPPools(ns.Name).Create(ctx, &whereaboutsv1alpha1.IPPool{
				ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("pool-%d", i)},
				Spec:       whereaboutsv1alpha1.IPPoolSpec{Range: tc.cidr, EnableL3: tc.l3, Allocations: map[string]whereaboutsv1alpha1.IPAllocation{}},
			}, metav1.CreateOptions{})
			if err != nil {
				t.Fatal(err)
			}
			base, _, err := pool.ParseCIDR()
			if err != nil {
				t.Fatal(err)
			}
			store := &KubernetesIPPool{client: client, firstIP: base, pool: pool}
			reservations := []whereaboutstypes.IPReservation{{IP: net.ParseIP(tc.last), ContainerID: "last", PodRef: "ns/last", IfName: "net1"}}
			if tc.first != tc.last {
				reservations = append(reservations, whereaboutstypes.IPReservation{IP: net.ParseIP(tc.first), ContainerID: "first", PodRef: "ns/first", IfName: "net1"})
			}
			if err := store.Update(ctx, reservations); err != nil {
				t.Fatal(err)
			}
			persisted, err := client.WhereaboutsV1alpha1().IPPools(ns.Name).Get(ctx, pool.Name, metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			store.pool = persisted
			decoded := store.Allocations()
			if len(decoded) != len(reservations) {
				t.Fatalf("lost reservations: %v", decoded)
			}
			for _, want := range reservations {
				found := false
				for _, got := range decoded {
					if got.IP.Equal(want.IP) && got.PodRef == want.PodRef && got.ContainerID == want.ContainerID && got.IfName == want.IfName {
						found = true
					}
				}
				if !found {
					t.Fatalf("reservation %v not preserved: %v", want, decoded)
				}
			}
			ip, updated, err := allocate.AssignIP(whereaboutstypes.RangeConfiguration{Range: tc.cidr, L3: tc.l3}, decoded, "new-container", "ns/last", "net1")
			if err != nil || !ip.IP.Equal(net.ParseIP(tc.last)) || len(updated) != len(decoded) {
				t.Fatalf("idempotent allocation changed: %v, %v, %v", ip, updated, err)
			}
		})
	}
}
