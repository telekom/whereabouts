package kubernetes

import (
	"context"
	"net"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fake "k8s.io/client-go/kubernetes/fake"

	whereaboutsv1alpha1 "github.com/telekom/whereabouts/api/whereabouts.cni.cncf.io/v1alpha1"
	wbfake "github.com/telekom/whereabouts/pkg/generated/clientset/versioned/fake"
	"github.com/telekom/whereabouts/pkg/types"
)

func TestUpdateOverlappingRangeAllocationStoresUID(t *testing.T) {
	wbClient := wbfake.NewClientset()
	store := &KubernetesOverlappingRangeStore{client: wbClient, namespace: "default"}

	err := store.UpdateOverlappingRangeAllocation(
		context.Background(), types.Allocate, net.ParseIP("10.0.0.1"), "default/pod1", "eth0", "", "uid-aaa")
	if err != nil {
		t.Fatalf("Allocate error: %v", err)
	}

	res, err := wbClient.WhereaboutsV1alpha1().OverlappingRangeIPReservations("default").Get(
		context.Background(), "10.0.0.1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	if res.Spec.PodUID != "uid-aaa" {
		t.Errorf("expected PodUID %q, got %q", "uid-aaa", res.Spec.PodUID)
	}
}

func TestUpdateOverlappingRangeDeallocationSkipsOnUIDMismatch(t *testing.T) {
	orip := &whereaboutsv1alpha1.OverlappingRangeIPReservation{
		ObjectMeta: metav1.ObjectMeta{Name: "10.0.0.2", Namespace: "default"},
		Spec: whereaboutsv1alpha1.OverlappingRangeIPReservationSpec{
			PodRef: "default/pod1",
			IfName: "eth0",
			PodUID: "uid-old",
		},
	}

	wbClient := wbfake.NewClientset(orip)
	store := &KubernetesOverlappingRangeStore{client: wbClient, namespace: "default"}

	err := store.UpdateOverlappingRangeAllocation(
		context.Background(), types.Deallocate, net.ParseIP("10.0.0.2"), "default/pod1", "eth0", "", "uid-new")
	if err != nil {
		t.Fatalf("Deallocate error: %v", err)
	}

	res, err := wbClient.WhereaboutsV1alpha1().OverlappingRangeIPReservations("default").Get(
		context.Background(), "10.0.0.2", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get after dealloc error: %v", err)
	}
	if res.Spec.PodUID != "uid-old" {
		t.Errorf("stale reservation should not have been deleted; expected UID %q, got %q", "uid-old", res.Spec.PodUID)
	}
}

func TestUpdateOverlappingRangeDeallocationProceedsOnUIDMatch(t *testing.T) {
	orip := &whereaboutsv1alpha1.OverlappingRangeIPReservation{
		ObjectMeta: metav1.ObjectMeta{Name: "10.0.0.3", Namespace: "default"},
		Spec: whereaboutsv1alpha1.OverlappingRangeIPReservationSpec{
			PodRef: "default/pod1",
			IfName: "eth0",
			PodUID: "uid-aaa",
		},
	}

	wbClient := wbfake.NewClientset(orip)
	store := &KubernetesOverlappingRangeStore{client: wbClient, namespace: "default"}

	err := store.UpdateOverlappingRangeAllocation(
		context.Background(), types.Deallocate, net.ParseIP("10.0.0.3"), "default/pod1", "eth0", "", "uid-aaa")
	if err != nil {
		t.Fatalf("Deallocate error: %v", err)
	}

	res, err := store.GetOverlappingRangeIPReservation(context.Background(), net.ParseIP("10.0.0.3"), "default/pod1", "")
	if err != nil {
		t.Fatalf("GetOverlappingRangeIPReservation error: %v", err)
	}
	if res != nil {
		t.Error("reservation should have been deleted when UIDs match")
	}
}

func TestUpdateOverlappingRangeDeallocationProceedsWhenNoUID(t *testing.T) {
	orip := &whereaboutsv1alpha1.OverlappingRangeIPReservation{
		ObjectMeta: metav1.ObjectMeta{Name: "10.0.0.4", Namespace: "default"},
		Spec: whereaboutsv1alpha1.OverlappingRangeIPReservationSpec{
			PodRef: "default/pod1",
			IfName: "eth0",
		},
	}

	wbClient := wbfake.NewClientset(orip)
	store := &KubernetesOverlappingRangeStore{client: wbClient, namespace: "default"}

	err := store.UpdateOverlappingRangeAllocation(
		context.Background(), types.Deallocate, net.ParseIP("10.0.0.4"), "default/pod1", "eth0", "", "")
	if err != nil {
		t.Fatalf("Deallocate error: %v", err)
	}

	res, err := store.GetOverlappingRangeIPReservation(context.Background(), net.ParseIP("10.0.0.4"), "default/pod1", "")
	if err != nil {
		t.Fatalf("GetOverlappingRangeIPReservation error: %v", err)
	}
	if res != nil {
		t.Error("reservation should have been deleted when caller has no UID (backward compat)")
	}
}

func TestUpdateOverlappingRangeDeallocationProceedsWhenStoredUIDEmpty(t *testing.T) {
	orip := &whereaboutsv1alpha1.OverlappingRangeIPReservation{
		ObjectMeta: metav1.ObjectMeta{Name: "10.0.0.5", Namespace: "default"},
		Spec: whereaboutsv1alpha1.OverlappingRangeIPReservationSpec{
			PodRef: "default/pod1",
			IfName: "eth0",
		},
	}

	wbClient := wbfake.NewClientset(orip)
	store := &KubernetesOverlappingRangeStore{client: wbClient, namespace: "default"}

	err := store.UpdateOverlappingRangeAllocation(
		context.Background(), types.Deallocate, net.ParseIP("10.0.0.5"), "default/pod1", "eth0", "", "uid-new")
	if err != nil {
		t.Fatalf("Deallocate error: %v", err)
	}

	res, err := store.GetOverlappingRangeIPReservation(context.Background(), net.ParseIP("10.0.0.5"), "default/pod1", "")
	if err != nil {
		t.Fatalf("GetOverlappingRangeIPReservation error: %v", err)
	}
	if res != nil {
		t.Error("reservation with empty stored UID should be deleted (old reservation, backward compat)")
	}
}

// TestIPManagementKubernetesUpdateStaleORIPDeleteAndReallocate tests the end-to-end
// stale-UID path in IPManagementKubernetesUpdate: when a pod restarts with the same
// name but a different UID, the stale ORIP reservation must be deleted and a fresh
// one created with the new UID.
func TestIPManagementKubernetesUpdateStaleORIPDeleteAndReallocate(t *testing.T) {
	const (
		ipRange     = "10.0.6.0/24"
		poolName    = "10.0.6.0-24" // normalizeRange("10.0.6.0/24")
		podRef      = "ns/pod-a"
		podName     = "pod-a"
		podNS       = "ns"
		containerID = "ctr-new"
		ifName      = "eth0"
		oldUID      = "old-uid"
		newUID      = "new-uid"
		// 10.0.6.1 is the first usable IP in 10.0.6.0/24; key is offset 1.
		oripName = "10.0.6.1"
	)

	// Pre-existing ORIP for the same PodRef but with the old pod UID.
	staleORIP := &whereaboutsv1alpha1.OverlappingRangeIPReservation{
		ObjectMeta: metav1.ObjectMeta{Name: oripName, Namespace: "default"},
		Spec: whereaboutsv1alpha1.OverlappingRangeIPReservationSpec{
			PodRef: podRef,
			IfName: ifName,
			PodUID: oldUID,
		},
	}

	// Pre-existing empty IP pool so AssignIP can allocate the first available IP.
	pool := &whereaboutsv1alpha1.IPPool{
		ObjectMeta: metav1.ObjectMeta{
			Name:            poolName,
			Namespace:       "default",
			ResourceVersion: "1",
		},
		Spec: whereaboutsv1alpha1.IPPoolSpec{
			Range:       ipRange,
			Allocations: map[string]whereaboutsv1alpha1.IPAllocation{},
		},
	}

	wbClient := wbfake.NewClientset(staleORIP, pool)
	k8sClient := fake.NewClientset()

	ipam := &KubernetesIPAM{
		Client:      *NewKubernetesClient(wbClient, k8sClient),
		Namespace:   "default",
		ContainerID: containerID,
		IfName:      ifName,
	}

	conf := types.IPAMConfig{
		PodName:           podName,
		PodNamespace:      podNS,
		PodUID:            newUID,
		OverlappingRanges: true,
		IPRanges: []types.RangeConfiguration{
			{Range: ipRange},
		},
	}

	newips, err := IPManagementKubernetesUpdate(context.Background(), types.Allocate, ipam, conf)
	if err != nil {
		t.Fatalf("IPManagementKubernetesUpdate() error: %v", err)
	}
	if len(newips) != 1 {
		t.Fatalf("expected 1 allocated IP, got %d", len(newips))
	}

	allocatedIP := newips[0].IP
	if !allocatedIP.Equal(net.ParseIP("10.0.6.1")) {
		t.Errorf("expected 10.0.6.1, got %s", allocatedIP)
	}

	// The stale ORIP should have been replaced: the new record must carry the new UID.
	updatedORIP, getErr := wbClient.WhereaboutsV1alpha1().OverlappingRangeIPReservations("default").Get(
		context.Background(), oripName, metav1.GetOptions{})
	if getErr != nil {
		t.Fatalf("Get ORIP error: %v", getErr)
	}
	if updatedORIP.Spec.PodUID != newUID {
		t.Errorf("expected ORIP PodUID %q after re-allocation, got %q", newUID, updatedORIP.Spec.PodUID)
	}
	if updatedORIP.Spec.PodRef != podRef {
		t.Errorf("expected ORIP PodRef %q, got %q", podRef, updatedORIP.Spec.PodRef)
	}
}
