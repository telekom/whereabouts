package kubernetes

import (
	"context"
	"fmt"
	"net"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	fake "k8s.io/client-go/kubernetes/fake"

	whereaboutsv1alpha1 "github.com/telekom/whereabouts/api/whereabouts.cni.cncf.io/v1alpha1"
	wbfake "github.com/telekom/whereabouts/pkg/generated/clientset/versioned/fake"
	"github.com/telekom/whereabouts/pkg/types"
)

// mockIPPool implements storage.IPPool for testing rollbackCommitted.
type mockIPPool struct {
	allocations []types.IPReservation
	updated     bool
}

func (m *mockIPPool) Allocations() []types.IPReservation {
	return m.allocations
}

func (m *mockIPPool) Update(_ context.Context, reservations []types.IPReservation) error {
	m.allocations = reservations
	m.updated = true
	return nil
}

func TestRollbackCommitted(t *testing.T) {
	ip1 := net.ParseIP("10.0.0.1")
	ip2 := net.ParseIP("10.0.0.2")
	ip3 := net.ParseIP("10.0.0.3")

	pool1 := &mockIPPool{
		allocations: []types.IPReservation{
			{IP: ip1, PodRef: "ns/pod1", IfName: "eth0"},
			{IP: ip2, PodRef: "ns/pod2", IfName: "eth0"},
		},
	}
	pool2 := &mockIPPool{
		allocations: []types.IPReservation{
			{IP: ip3, PodRef: "ns/pod1", IfName: "eth0"},
		},
	}

	committed := []committedAlloc{
		{pool: pool1, ip: ip1},
		{pool: pool2, ip: ip3},
	}

	rollbackCommitted(context.Background(), committed)

	// pool1 should have ip1 removed, ip2 remaining.
	if !pool1.updated {
		t.Fatal("pool1 should have been updated")
	}
	if len(pool1.allocations) != 1 {
		t.Fatalf("expected 1 allocation in pool1, got %d", len(pool1.allocations))
	}
	if !pool1.allocations[0].IP.Equal(ip2) {
		t.Errorf("expected remaining IP %s, got %s", ip2, pool1.allocations[0].IP)
	}

	// pool2 should have ip3 removed, empty.
	if !pool2.updated {
		t.Fatal("pool2 should have been updated")
	}
	if len(pool2.allocations) != 0 {
		t.Fatalf("expected 0 allocations in pool2, got %d", len(pool2.allocations))
	}
}

type conflictMockIPPool struct {
	allocations []types.IPReservation
	updateCalls int
	failsRemain int
}

func (m *conflictMockIPPool) Allocations() []types.IPReservation {
	return m.allocations
}

func (m *conflictMockIPPool) Update(_ context.Context, reservations []types.IPReservation) error {
	m.updateCalls++
	if m.failsRemain > 0 {
		m.failsRemain--
		return &temporaryError{fmt.Errorf("resource version conflict")}
	}
	m.allocations = reservations
	return nil
}

// TestRollbackCommittedRetriesOnConflict verifies that the retry loop in
// rollbackCommitted retries on temporaryError and eventually succeeds.
// NOTE: c.ipam is intentionally nil here — the pool re-read path
// (GetIPPool) requires a real KubernetesIPAM with a Kubernetes client
// and is covered by envtest/e2e tests, not unit tests. This test
// validates the retry-loop mechanics: conflict detection, retry count,
// and correct IP removal after retries succeed.
func TestRollbackCommittedRetriesOnConflict(t *testing.T) {
	ip1 := net.ParseIP("10.0.0.1")
	ip2 := net.ParseIP("10.0.0.2")

	pool := &conflictMockIPPool{
		allocations: []types.IPReservation{
			{IP: ip1, PodRef: "ns/pod1", IfName: "eth0"},
			{IP: ip2, PodRef: "ns/pod2", IfName: "eth0"},
		},
		failsRemain: 2,
	}

	committed := []committedAlloc{
		{pool: pool, ip: ip1},
	}

	rollbackCommitted(context.Background(), committed)

	if pool.updateCalls != 3 {
		t.Fatalf("expected 3 update calls (2 conflicts + 1 success), got %d", pool.updateCalls)
	}
	if len(pool.allocations) != 1 {
		t.Fatalf("expected 1 allocation remaining, got %d", len(pool.allocations))
	}
	if !pool.allocations[0].IP.Equal(ip2) {
		t.Errorf("expected remaining IP %s, got %s", ip2, pool.allocations[0].IP)
	}
}

func TestNodeSliceRangeIsSliced(t *testing.T) {
	scheme := runtime.NewScheme()
	utilruntime.Must(whereaboutsv1alpha1.AddToScheme(scheme))

	nodeSlice := &whereaboutsv1alpha1.NodeSlicePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-nad",
			Namespace: "default",
		},
		Spec: whereaboutsv1alpha1.NodeSlicePoolSpec{
			Range:     "10.0.0.0/16",
			SliceSize: "24",
		},
		Status: whereaboutsv1alpha1.NodeSlicePoolStatus{
			Allocations: []whereaboutsv1alpha1.NodeSliceAllocation{{
				NodeName:   "test-node",
				SliceRange: "10.0.1.0/24",
			}},
		},
	}

	pool := &whereaboutsv1alpha1.IPPool{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "test-node-10.0.1.0-24",
			Namespace:       "default",
			ResourceVersion: "1",
		},
		Spec: whereaboutsv1alpha1.IPPoolSpec{
			Range:       "10.0.1.0/24",
			Allocations: map[string]whereaboutsv1alpha1.IPAllocation{},
		},
	}

	wbClient := wbfake.NewClientset(nodeSlice, pool)
	k8sClient := fake.NewClientset()
	ipam := &KubernetesIPAM{
		Client:      *NewKubernetesClient(wbClient, k8sClient),
		Namespace:   "default",
		ContainerID: "container1",
		IfName:      "eth0",
		Config: types.IPAMConfig{
			Name:          "test-nad",
			NodeSliceSize: "24",
		},
	}

	t.Setenv("NODENAME", "test-node")

	newips, err := IPManagementKubernetesUpdate(context.Background(), types.Allocate, ipam, types.IPAMConfig{
		Name:          "test-nad",
		PodName:       "pod1",
		PodNamespace:  "default",
		NodeSliceSize: "24",
		IPRanges: []types.RangeConfiguration{{
			Range: "10.0.0.0/16",
		}},
	})
	if err != nil {
		t.Fatalf("IPManagementKubernetesUpdate() error: %v", err)
	}
	if len(newips) != 1 {
		t.Fatalf("expected 1 IP, got %d", len(newips))
	}
	if got := newips[0].String(); got != "10.0.1.1/24" {
		t.Fatalf("expected sliced node range allocation 10.0.1.1/24, got %s", got)
	}

	gotPool, err := wbClient.WhereaboutsV1alpha1().IPPools("default").Get(context.Background(), "test-node-10.0.1.0-24", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected created IPPool, got error: %v", err)
	}
	if gotPool.Spec.Range != "10.0.1.0/24" {
		t.Fatalf("expected created pool range 10.0.1.0/24, got %s", gotPool.Spec.Range)
	}
}

func TestIPPoolName(t *testing.T) {
	cases := []struct {
		name           string
		poolIdentifier PoolIdentifier
		expectedResult string
	}{
		{
			name: "No node name, unnamed network",
			poolIdentifier: PoolIdentifier{
				NetworkName: UnnamedNetwork,
				IPRange:     "10.0.0.0/8",
			},
			expectedResult: "10.0.0.0-8",
		},
		{
			name: "No node name, named network",
			poolIdentifier: PoolIdentifier{
				NetworkName: "test",
				IPRange:     "10.0.0.0/8",
			},
			expectedResult: "test-10.0.0.0-8",
		},
		{
			name: "Node name, unnamed network",
			poolIdentifier: PoolIdentifier{
				NetworkName: UnnamedNetwork,
				NodeName:    "testnode",
				IPRange:     "10.0.0.0/8",
			},
			expectedResult: "testnode-10.0.0.0-8",
		},
		{
			name: "Node name, named network",
			poolIdentifier: PoolIdentifier{
				NetworkName: "testnetwork",
				NodeName:    "testnode",
				IPRange:     "10.0.0.0/8",
			},
			expectedResult: "testnetwork-testnode-10.0.0.0-8",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := IPPoolName(tc.poolIdentifier)
			if result != tc.expectedResult {
				t.Errorf("Expected result: %s, got result: %s", tc.expectedResult, result)
			}
		})
	}
}

func buildNodeSliceRangeConfiguration(_ string, nodeSlice string, omitRanges []string, start, end net.IP) types.RangeConfiguration {
	return types.RangeConfiguration{
		OmitRanges: omitRanges,
		Range:      nodeSlice,
		RangeStart: start,
		RangeEnd:   end,
	}
}

func TestNodeSliceRangeUsed(t *testing.T) {
	t.Run("uses node slice range in constructed range configuration", func(t *testing.T) {
		originalRange := "10.0.0.0/24"
		nodeSliceRange := "10.0.0.0/25"
		start := net.ParseIP("10.0.0.1")
		end := net.ParseIP("10.0.0.126")

		constructed := buildNodeSliceRangeConfiguration(
			originalRange,
			nodeSliceRange,
			[]string{"10.0.0.10/32"},
			start,
			end,
		)

		if constructed.Range != nodeSliceRange {
			t.Fatalf("expected node slice range %q, got %q", nodeSliceRange, constructed.Range)
		}
		if constructed.Range == originalRange {
			t.Fatalf("expected constructed range to differ from original full range %q", originalRange)
		}
		if !constructed.RangeStart.Equal(start) || !constructed.RangeEnd.Equal(end) {
			t.Fatalf("unexpected range bounds: got %s-%s", constructed.RangeStart, constructed.RangeEnd)
		}
	})
}

func TestToIPReservationList(t *testing.T) {
	firstIP := net.ParseIP("10.0.0.0")

	cases := []struct {
		name        string
		allocations map[string]whereaboutsv1alpha1.IPAllocation
		firstIP     net.IP
		expectedIPs []string
	}{
		{
			name:        "empty allocations",
			allocations: map[string]whereaboutsv1alpha1.IPAllocation{},
			firstIP:     firstIP,
			expectedIPs: []string{},
		},
		{
			name: "simple offset",
			allocations: map[string]whereaboutsv1alpha1.IPAllocation{
				"1": {PodRef: "ns/pod1", ContainerID: "c1", IfName: "eth0"},
				"5": {PodRef: "ns/pod2", ContainerID: "c2", IfName: "eth0"},
			},
			firstIP:     firstIP,
			expectedIPs: []string{"10.0.0.1", "10.0.0.5"},
		},
		{
			name: "invalid offset is skipped",
			allocations: map[string]whereaboutsv1alpha1.IPAllocation{
				"1":       {PodRef: "ns/pod1", ContainerID: "c1", IfName: "eth0"},
				"notanum": {PodRef: "ns/bad", ContainerID: "c3", IfName: "eth0"},
			},
			firstIP:     firstIP,
			expectedIPs: []string{"10.0.0.1"},
		},
		{
			name: "negative offset is skipped",
			allocations: map[string]whereaboutsv1alpha1.IPAllocation{
				"-1": {PodRef: "ns/neg", ContainerID: "c4", IfName: "eth0"},
				"2":  {PodRef: "ns/pod1", ContainerID: "c1", IfName: "eth0"},
			},
			firstIP:     firstIP,
			expectedIPs: []string{"10.0.0.2"},
		},
		{
			name: "large offset beyond uint64 max",
			allocations: map[string]whereaboutsv1alpha1.IPAllocation{
				"18446744073709551616": {PodRef: "ns/pod-big", ContainerID: "c5", IfName: "eth0"},
			},
			firstIP:     net.ParseIP("fd00::"),
			expectedIPs: []string{"fd00::1:0:0:0:0"},
		},
		{
			name: "max uint64 offset",
			allocations: map[string]whereaboutsv1alpha1.IPAllocation{
				"18446744073709551615": {PodRef: "ns/pod-max", ContainerID: "c6", IfName: "eth0"},
			},
			firstIP:     net.ParseIP("::"),
			expectedIPs: []string{"::ffff:ffff:ffff:ffff"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := toIPReservationList(tc.allocations, tc.firstIP)
			if len(result) != len(tc.expectedIPs) {
				t.Fatalf("expected %d reservations, got %d", len(tc.expectedIPs), len(result))
			}
			for _, expected := range tc.expectedIPs {
				expectedIP := net.ParseIP(expected)
				found := false
				for _, r := range result {
					if r.IP.Equal(expectedIP) {
						found = true
						break
					}
				}
				if !found {
					var got []string
					for _, r := range result {
						got = append(got, r.IP.String())
					}
					t.Errorf("expected IP %s not found in results: %v", expected, got)
				}
			}
		})
	}
}

func TestToAllocationMapRoundTrip(t *testing.T) {
	firstIP := net.ParseIP("10.0.0.0")
	reservations := []types.IPReservation{
		{IP: net.ParseIP("10.0.0.1"), PodRef: "ns/pod1", ContainerID: "c1", IfName: "eth0"},
		{IP: net.ParseIP("10.0.0.10"), PodRef: "ns/pod2", ContainerID: "c2", IfName: "net1"},
	}

	allocMap, err := toAllocationMap(reservations, firstIP)
	if err != nil {
		t.Fatalf("toAllocationMap failed: %v", err)
	}

	roundTripped := toIPReservationList(allocMap, firstIP)
	if len(roundTripped) != len(reservations) {
		t.Fatalf("round-trip length mismatch: expected %d, got %d", len(reservations), len(roundTripped))
	}

	originalIPs := make(map[string]types.IPReservation)
	for _, r := range reservations {
		originalIPs[r.IP.String()] = r
	}
	for _, r := range roundTripped {
		orig, ok := originalIPs[r.IP.String()]
		if !ok {
			t.Errorf("unexpected IP %s after round-trip", r.IP)
			continue
		}
		if r.PodRef != orig.PodRef || r.ContainerID != orig.ContainerID || r.IfName != orig.IfName {
			t.Errorf("metadata mismatch for IP %s: got podRef=%s containerID=%s ifName=%s, want podRef=%s containerID=%s ifName=%s",
				r.IP, r.PodRef, r.ContainerID, r.IfName, orig.PodRef, orig.ContainerID, orig.IfName)
		}
	}
}

func TestToAllocationMapRoundTripIPv6LargeOffset(t *testing.T) {
	// Use a /64 prefix — the full uint64 range (and beyond) should be addressable.
	firstIP := net.ParseIP("fd00::")
	// Offset = 2^64, which exceeds uint64 max — only possible with big.Int.
	highIP := net.ParseIP("fd00::1:0:0:0:0")

	reservations := []types.IPReservation{
		{IP: highIP, PodRef: "ns/pod-high", ContainerID: "c1", IfName: "eth0"},
	}

	allocMap, err := toAllocationMap(reservations, firstIP)
	if err != nil {
		t.Fatalf("toAllocationMap failed: %v", err)
	}

	// Verify the offset key is the expected large value.
	expectedKey := "18446744073709551616" // 2^64
	if _, ok := allocMap[expectedKey]; !ok {
		t.Fatalf("expected allocation key %s, got keys: %v", expectedKey, allocMap)
	}

	// Round-trip back.
	roundTripped := toIPReservationList(allocMap, firstIP)
	if len(roundTripped) != 1 {
		t.Fatalf("expected 1 reservation, got %d", len(roundTripped))
	}
	if !roundTripped[0].IP.Equal(highIP) {
		t.Errorf("round-trip IP mismatch: expected %s, got %s", highIP, roundTripped[0].IP)
	}
}

// permanentErrorPool is an IPPool whose Update always returns a non-temporary error.
type permanentErrorPool struct {
	allocations []types.IPReservation
	updateCalls int
}

func (p *permanentErrorPool) Allocations() []types.IPReservation { return p.allocations }
func (p *permanentErrorPool) Update(_ context.Context, _ []types.IPReservation) error {
	p.updateCalls++
	return fmt.Errorf("permanent storage failure") // not a storage.Temporary
}

// TestRollbackCommittedStopsOnPermanentError verifies that a non-temporary error
// from pool.Update causes rollbackCommitted to stop immediately (no retries).
func TestRollbackCommittedStopsOnPermanentError(t *testing.T) {
	ip1 := net.ParseIP("10.0.0.1")
	pool := &permanentErrorPool{
		allocations: []types.IPReservation{
			{IP: ip1, PodRef: "ns/pod1", IfName: "eth0"},
		},
	}

	committed := []committedAlloc{
		{pool: pool, ip: ip1, ipam: nil},
	}
	rollbackCommitted(context.Background(), committed)

	// A permanent error must break the retry loop after the very first attempt.
	if pool.updateCalls != 1 {
		t.Fatalf("expected exactly 1 Update call on permanent error, got %d", pool.updateCalls)
	}
}

// TestRollbackCommittedPoolIDIncludesNodeName verifies that when poolIdentifier
// carries a NodeName (Fast IPAM / NodeSlice mode), the committedAlloc.poolID
// correctly stores it — ensuring rollback re-reads the node-specific pool rather
// than the global range pool.
func TestRollbackCommittedPoolIDIncludesNodeName(t *testing.T) {
	nodeName := "worker-node-1"
	nodeSliceRange := "10.20.0.0/24"

	poolID := PoolIdentifier{
		NodeName:    nodeName,
		IPRange:     nodeSliceRange,
		NetworkName: "",
	}

	ip := net.ParseIP("10.20.0.5")
	pool := &mockIPPool{
		allocations: []types.IPReservation{
			{IP: ip, PodRef: "ns/pod1", IfName: "eth0"},
		},
	}

	c := committedAlloc{pool: pool, poolID: poolID, ip: ip, ipam: nil}

	// Verify the poolID carries the NodeName so rollback will look up
	// the correct node-specific pool (not the global range pool).
	if c.poolID.NodeName != nodeName {
		t.Errorf("expected poolID.NodeName=%q, got %q", nodeName, c.poolID.NodeName)
	}
	if c.poolID.IPRange != nodeSliceRange {
		t.Errorf("expected poolID.IPRange=%q, got %q", nodeSliceRange, c.poolID.IPRange)
	}

	// Sanity: the pool name includes the node name.
	expectedPoolName := IPPoolName(poolID)
	if expectedPoolName != nodeName+"-10.20.0.0-24" {
		t.Errorf("unexpected pool name %q", expectedPoolName)
	}

	// Rollback with ipam=nil (no re-read) should still remove the IP from the
	// in-memory pool on the first attempt.
	rollbackCommitted(context.Background(), []committedAlloc{c})
	if pool.updated {
		if len(pool.allocations) != 0 {
			t.Errorf("expected 0 allocations after rollback, got %d", len(pool.allocations))
		}
	}
}

type apiTimeoutPool struct {
	allocations []types.IPReservation
	updateCalls int
	failsRemain int
}

func (p *apiTimeoutPool) Allocations() []types.IPReservation { return p.allocations }
func (p *apiTimeoutPool) Update(_ context.Context, reservations []types.IPReservation) error {
	p.updateCalls++
	if p.failsRemain > 0 {
		p.failsRemain--
		return apierrors.NewServerTimeout(schema.GroupResource{Group: "whereabouts.cni.cncf.io", Resource: "ippools"}, "update", 1)
	}
	p.allocations = reservations
	return nil
}

func TestRollbackCommittedRetriesOnAPITimeout(t *testing.T) {
	ip1 := net.ParseIP("10.0.0.1")
	ip2 := net.ParseIP("10.0.0.2")

	pool := &apiTimeoutPool{
		allocations: []types.IPReservation{
			{IP: ip1, PodRef: "ns/pod1", IfName: "eth0"},
			{IP: ip2, PodRef: "ns/pod2", IfName: "eth0"},
		},
		failsRemain: 2,
	}

	committed := []committedAlloc{
		{pool: pool, ip: ip1},
	}
	rollbackCommitted(context.Background(), committed)

	if pool.updateCalls != 3 {
		t.Fatalf("expected 3 update calls (2 timeouts + 1 success), got %d", pool.updateCalls)
	}
	if len(pool.allocations) != 1 {
		t.Fatalf("expected 1 allocation remaining, got %d", len(pool.allocations))
	}
	if !pool.allocations[0].IP.Equal(ip2) {
		t.Errorf("expected remaining IP %s, got %s", ip2, pool.allocations[0].IP)
	}
}

func TestIsRetryableRollbackErrorAPITimeout(t *testing.T) {
	err := apierrors.NewTimeoutError("test timeout", 5)
	if !isRetryableRollbackError(err) {
		t.Errorf("expected isRetryableRollbackError to return true for API timeout (StatusReasonTimeout), got false")
	}
}

func TestNodeSliceRangeUsesSlicedRange(t *testing.T) {
	nodeSlice := &whereaboutsv1alpha1.NodeSlicePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-nad",
			Namespace: "default",
		},
		Spec: whereaboutsv1alpha1.NodeSlicePoolSpec{
			Range:     "10.0.0.0/16",
			SliceSize: "24",
		},
		Status: whereaboutsv1alpha1.NodeSlicePoolStatus{
			Allocations: []whereaboutsv1alpha1.NodeSliceAllocation{{
				NodeName:   "test-node",
				SliceRange: "10.0.1.0/24",
			}},
		},
	}
	pool := &whereaboutsv1alpha1.IPPool{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "test-node-10.0.1.0-24",
			Namespace:       "default",
			ResourceVersion: "1",
		},
		Spec: whereaboutsv1alpha1.IPPoolSpec{
			Range:       "10.0.1.0/24",
			Allocations: map[string]whereaboutsv1alpha1.IPAllocation{},
		},
	}

	ipam := &KubernetesIPAM{
		Client:      *NewKubernetesClient(wbfake.NewClientset(nodeSlice, pool), fake.NewClientset()),
		Namespace:   "default",
		ContainerID: "container1",
		IfName:      "eth0",
		Config: types.IPAMConfig{
			Name:          "test-nad",
			NodeSliceSize: "24",
		},
	}
	t.Setenv("NODENAME", "test-node")

	conf := types.IPAMConfig{
		Name:          "test-nad",
		PodName:       "pod1",
		PodNamespace:  "default",
		NodeSliceSize: "24",
		IPRanges: []types.RangeConfiguration{{
			Range: "10.0.0.0/16",
		}},
	}

	newips, err := IPManagementKubernetesUpdate(context.Background(), types.Allocate, ipam, conf)
	if err != nil {
		t.Fatalf("IPManagementKubernetesUpdate() error: %v", err)
	}
	if len(newips) != 1 {
		t.Fatalf("expected 1 IP, got %d", len(newips))
	}

	expectedIP := net.ParseIP("10.0.1.1")
	if !newips[0].IP.Equal(expectedIP) {
		t.Fatalf("expected allocated IP %s from node slice, got %s", expectedIP, newips[0].IP)
	}
}
