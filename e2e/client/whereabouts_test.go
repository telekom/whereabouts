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

	whereaboutsv1alpha1 "github.com/telekom/whereabouts/api/whereabouts.cni.cncf.io/v1alpha1"
	wbfake "github.com/telekom/whereabouts/pkg/generated/clientset/versioned/fake"
	kubeClient "github.com/telekom/whereabouts/pkg/storage/kubernetes"
	whereaboutstypes "github.com/telekom/whereabouts/pkg/types"
)

func TestPodDeleteTimeoutAllowsSlowCNITeardown(t *testing.T) {
	if podDeleteTimeout < 2*time.Minute {
		t.Fatalf("podDeleteTimeout = %s, want at least 2m for slow CI CNI teardown", podDeleteTimeout)
	}
}

func TestPodCreateTimeoutAllowsHostedRunnerStartupLatency(t *testing.T) {
	if podCreateTimeout < 90*time.Second {
		t.Fatalf("podCreateTimeout = %s, want at least 90s for slow CI pod startup", podCreateTimeout)
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

func TestNodeSliceAllocationCleanupChecksActualSliceIPPools(t *testing.T) {
	const (
		namespace      = "kube-system"
		networkName    = "fast-net"
		nodeName       = "node-a"
		parentRange    = "10.0.0.0/16"
		nodeSliceRange = "10.0.1.0/24"
	)

	nodeSlicePool := &whereaboutsv1alpha1.NodeSlicePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      networkName,
			Namespace: namespace,
		},
		Spec: whereaboutsv1alpha1.NodeSlicePoolSpec{
			Range:     parentRange,
			SliceSize: "24",
		},
		Status: whereaboutsv1alpha1.NodeSlicePoolStatus{
			Allocations: []whereaboutsv1alpha1.NodeSliceAllocation{
				{
					NodeName:   nodeName,
					SliceRange: nodeSliceRange,
				},
			},
		},
	}
	slicePoolID := kubeClient.PoolIdentifier{
		NodeName:    nodeName,
		IPRange:     nodeSliceRange,
		NetworkName: networkName,
	}
	sliceIPPool := &whereaboutsv1alpha1.IPPool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      kubeClient.IPPoolName(slicePoolID),
			Namespace: namespace,
		},
		Spec: whereaboutsv1alpha1.IPPoolSpec{
			Range: nodeSliceRange,
			Allocations: map[string]whereaboutsv1alpha1.IPAllocation{
				"1": {PodRef: "default/pod-a", IfName: "net1"},
			},
		},
	}

	wbClient := wbfake.NewClientset(nodeSlicePool, sliceIPPool)
	coreClient := fake.NewClientset()
	clientInfo := &ClientInfo{
		WbClient: wbClient,
	}
	k8sIPAM := &kubeClient.KubernetesIPAM{
		Client:    *kubeClient.NewKubernetesClient(wbClient, coreClient),
		Namespace: namespace,
		Config: whereaboutstypes.IPAMConfig{
			NetworkName: networkName,
		},
	}

	empty, err := isIPPoolAllocationsEmptyForNodeSlices(k8sIPAM, parentRange, clientInfo)(t.Context())
	if err != nil {
		t.Fatalf("isIPPoolAllocationsEmptyForNodeSlices returned error: %v", err)
	}
	if empty {
		t.Fatal("expected lingering allocation in node-slice IPPool to be detected")
	}
}

func TestNodeSliceAllocationCleanupSkipsUnassignedSlices(t *testing.T) {
	const (
		namespace          = "kube-system"
		networkName        = "fast-net"
		nodeName           = "node-a"
		parentRange        = "10.0.0.0/16"
		freeNodeSliceRange = "10.0.0.0/24"
		nodeSliceRange     = "10.0.1.0/24"
	)

	nodeSlicePool := &whereaboutsv1alpha1.NodeSlicePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      networkName,
			Namespace: namespace,
		},
		Spec: whereaboutsv1alpha1.NodeSlicePoolSpec{
			Range:     parentRange,
			SliceSize: "24",
		},
		Status: whereaboutsv1alpha1.NodeSlicePoolStatus{
			Allocations: []whereaboutsv1alpha1.NodeSliceAllocation{
				{
					SliceRange: freeNodeSliceRange,
				},
				{
					NodeName:   nodeName,
					SliceRange: nodeSliceRange,
				},
			},
		},
	}
	slicePoolID := kubeClient.PoolIdentifier{
		NodeName:    nodeName,
		IPRange:     nodeSliceRange,
		NetworkName: networkName,
	}
	sliceIPPool := &whereaboutsv1alpha1.IPPool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      kubeClient.IPPoolName(slicePoolID),
			Namespace: namespace,
		},
		Spec: whereaboutsv1alpha1.IPPoolSpec{
			Range:       nodeSliceRange,
			Allocations: map[string]whereaboutsv1alpha1.IPAllocation{},
		},
	}

	wbClient := wbfake.NewClientset(nodeSlicePool, sliceIPPool)
	var gotIPPoolNames []string
	wbClient.PrependReactor("get", "ippools", func(action clienttesting.Action) (bool, runtime.Object, error) {
		getAction := action.(clienttesting.GetAction)
		gotIPPoolNames = append(gotIPPoolNames, getAction.GetName())
		return false, nil, nil
	})

	coreClient := fake.NewClientset()
	clientInfo := &ClientInfo{
		WbClient: wbClient,
	}
	k8sIPAM := &kubeClient.KubernetesIPAM{
		Client:    *kubeClient.NewKubernetesClient(wbClient, coreClient),
		Namespace: namespace,
		Config: whereaboutstypes.IPAMConfig{
			NetworkName: networkName,
		},
	}

	empty, err := isIPPoolAllocationsEmptyForNodeSlices(k8sIPAM, parentRange, clientInfo)(t.Context())
	if err != nil {
		t.Fatalf("isIPPoolAllocationsEmptyForNodeSlices returned error: %v", err)
	}
	if !empty {
		t.Fatal("expected no lingering allocations")
	}

	wantIPPoolName := kubeClient.IPPoolName(slicePoolID)
	if len(gotIPPoolNames) != 1 || gotIPPoolNames[0] != wantIPPoolName {
		t.Fatalf("GET IPPools = %v, want only %q", gotIPPoolNames, wantIPPoolName)
	}
}

func ptrTo[T any](v T) *T {
	return &v
}
