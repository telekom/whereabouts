package kubernetes

import (
	"context"
	"net"
	"testing"

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
				IpRange:     "10.0.0.0/8",
			},
			expectedResult: "10.0.0.0-8",
		},
		{
			name: "No node name, named network",
			poolIdentifier: PoolIdentifier{
				NetworkName: "test",
				IpRange:     "10.0.0.0/8",
			},
			expectedResult: "test-10.0.0.0-8",
		},
		{
			name: "Node name, unnamed network",
			poolIdentifier: PoolIdentifier{
				NetworkName: UnnamedNetwork,
				NodeName:    "testnode",
				IpRange:     "10.0.0.0/8",
			},
			expectedResult: "testnode-10.0.0.0-8",
		},
		{
			name: "Node name, named network",
			poolIdentifier: PoolIdentifier{
				NetworkName: "testnetwork",
				NodeName:    "testnode",
				IpRange:     "10.0.0.0/8",
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
