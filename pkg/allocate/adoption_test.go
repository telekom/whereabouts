// SPDX-FileCopyrightText: 2026 Deutsche Telekom AG
// SPDX-License-Identifier: Apache-2.0

package allocate

import (
	"net"
	"testing"

	whereaboutstypes "github.com/telekom/whereabouts/pkg/types"
)

func TestAllocationPolicyCharacterization(t *testing.T) {
	for _, tc := range []struct {
		name, cidr, start, end, preferred string
		l3                                bool
		exclude, want                     []string
	}{
		{"IPv4 ordinary", "10.0.0.0/30", "", "", "", false, nil, []string{"10.0.0.1", "10.0.0.2"}},
		{"IPv4 point-to-point", "10.0.0.0/31", "", "", "", false, nil, []string{"10.0.0.0", "10.0.0.1"}},
		{"IPv4 single host", "10.0.0.1/32", "", "", "", false, nil, []string{"10.0.0.1"}},
		{"IPv6 ordinary", "fd00::/126", "", "", "", false, nil, []string{"fd00::1", "fd00::2"}},
		{"IPv6 point-to-point", "fd00::/127", "", "", "", false, nil, []string{"fd00::", "fd00::1"}},
		{"IPv6 single host", "fd00::1/128", "", "", "", false, nil, []string{"fd00::1"}},
		{"IPv4 routed endpoints", "10.0.0.0/30", "", "", "", true, nil, []string{"10.0.0.0", "10.0.0.1", "10.0.0.2", "10.0.0.3"}},
		{"IPv6 routed endpoints", "fd00::/126", "", "", "", true, nil, []string{"fd00::", "fd00::1", "fd00::2", "fd00::3"}},
		{"inclusive bounds", "10.0.0.0/29", "10.0.0.2", "10.0.0.3", "", false, nil, []string{"10.0.0.2", "10.0.0.3"}},
		{"IPv6 inclusive bounds", "fd00::/125", "fd00::2", "fd00::3", "", false, nil, []string{"fd00::2", "fd00::3"}},
		{"excluded CIDR", "10.0.0.0/29", "", "", "", false, []string{"10.0.0.0/30"}, []string{"10.0.0.4", "10.0.0.5", "10.0.0.6"}},
		{"excluded IPv6", "fd00::/125", "", "", "", false, []string{"fd00::/126"}, []string{"fd00::4", "fd00::5", "fd00::6"}},
		{"preferred before lowest", "10.0.0.0/30", "", "", "10.0.0.2", false, nil, []string{"10.0.0.2", "10.0.0.1"}},
		{"preferred excluded fallback", "10.0.0.0/30", "", "", "10.0.0.2", false, []string{"10.0.0.2/32"}, []string{"10.0.0.1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conf := whereaboutstypes.RangeConfiguration{
				Range: tc.cidr, L3: tc.l3, OmitRanges: tc.exclude,
				RangeStart: net.ParseIP(tc.start), RangeEnd: net.ParseIP(tc.end), PreferredIP: net.ParseIP(tc.preferred),
			}
			var reservations []whereaboutstypes.IPReservation
			for i, want := range tc.want {
				pod := string(rune('a' + i))
				ip, updated, err := AssignIP(conf, reservations, pod, "ns/"+pod, "net1")
				if err != nil || !ip.IP.Equal(net.ParseIP(want)) || len(updated) != i+1 {
					t.Fatalf("allocation %d: got %v, %v, %v; want %s", i, ip, updated, err, want)
				}
				reservations = updated
				again, unchanged, err := AssignIP(conf, reservations, pod+"-restart", "ns/"+pod, "net1")
				if err != nil || !again.IP.Equal(ip.IP) || len(unchanged) != len(updated) {
					t.Fatalf("idempotence failed: %v, %v, %v", again, unchanged, err)
				}
			}
			if _, _, err := AssignIP(conf, reservations, "overflow", "ns/overflow", "net1"); err == nil {
				t.Fatal("exhausted pool allocated another address")
			}
		})
	}
}
