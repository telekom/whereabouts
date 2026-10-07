// SPDX-FileCopyrightText: 2026 Deutsche Telekom AG
// SPDX-License-Identifier: Apache-2.0

package iphelpers

import (
	"math/big"
	"net"
	"reflect"
	"testing"
)

func TestCheckedOffsetCharacterization(t *testing.T) {
	for _, tc := range []struct {
		name, ip, offset, want string
		length                 int
	}{
		{"IPv4 zero", "0.0.0.0", "0", "0.0.0.0", 4},
		{"IPv4 carry", "10.0.0.255", "1", "10.0.1.0", 4},
		{"mapped IPv4", "::ffff:10.0.0.255", "1", "10.0.1.0", 4},
		{"IPv4 maximum", "0.0.0.0", "4294967295", "255.255.255.255", 4},
		{"IPv4 overflow", "0.0.0.0", "4294967296", "", 0},
		{"IPv4 overflow from maximum", "255.255.255.255", "1", "", 0},
		{"negative", "10.0.0.1", "-1", "", 0},
		{"IPv6 carry", "fd00::ffff", "1", "fd00::1:0", 16},
		{"IPv6 wide", "::", "18446744073709551616", "0:0:0:1::", 16},
		{"IPv6 maximum", "::", "340282366920938463463374607431768211455", "ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff", 16},
		{"IPv6 overflow", "::", "340282366920938463463374607431768211456", "", 0},
		{"invalid IP", "invalid", "1", "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			offset, _ := new(big.Int).SetString(tc.offset, 10)
			got := IPAddOffset(net.ParseIP(tc.ip), offset)
			if len(got) != tc.length || (tc.want != "" && !got.Equal(net.ParseIP(tc.want))) {
				t.Fatalf("got %v (%d bytes), want %s (%d bytes)", got, len(got), tc.want, tc.length)
			}
			if offset.String() != tc.offset {
				t.Fatal("offset was mutated")
			}
		})
	}
	if IPAddOffset(net.ParseIP("10.0.0.1"), nil) != nil || IPAddOffset(net.IP{1, 2, 3}, big.NewInt(1)) != nil {
		t.Fatal("nil offset and malformed IP must be rejected")
	}
}

func TestBoundedSubdivisionCharacterization(t *testing.T) {
	for _, tc := range []struct {
		name, cidr, bits string
		limit            int64
		want             []string
		wantError        bool
	}{
		{"IPv4 hosts", "10.0.0.0/30", "/32", 4, []string{"10.0.0.0/32", "10.0.0.1/32", "10.0.0.2/32", "10.0.0.3/32"}, false},
		{"IPv6 hosts", "fd00::/126", "128", 4, []string{"fd00::/128", "fd00::1/128", "fd00::2/128", "fd00::3/128"}, false},
		{"IPv4 maximum", "255.255.255.254/31", "32", 2, []string{"255.255.255.254/32", "255.255.255.255/32"}, false},
		{"IPv6 maximum", "ffff:ffff:ffff:ffff:ffff:ffff:ffff:fffe/127", "128", 2, []string{"ffff:ffff:ffff:ffff:ffff:ffff:ffff:fffe/128", "ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff/128"}, false},
		{"same prefix", "10.0.0.0/24", "24", 1, []string{"10.0.0.0/24"}, false},
		{"one over budget", "10.0.0.0/30", "32", 3, nil, true},
		{"IPv6 huge count", "::/0", "128", 16384, nil, true},
		{"host bits", "10.0.0.1/24", "26", 4, nil, true},
		{"IPv6 host bits", "fd00::1/124", "126", 4, nil, true},
		{"wider slice", "10.0.0.0/24", "23", 4, nil, true},
		{"invalid bits", "10.0.0.0/24", "invalid", 4, nil, true},
		{"IPv4 bits overflow", "10.0.0.0/24", "33", 4, nil, true},
		{"IPv6 bits overflow", "fd00::/124", "129", 4, nil, true},
		{"invalid CIDR", "invalid", "24", 4, nil, true},
		{"zero means unlimited", "10.0.0.0/31", "32", 0, []string{"10.0.0.0/32", "10.0.0.1/32"}, false},
		{"negative means unlimited", "10.0.0.0/31", "32", -1, []string{"10.0.0.0/32", "10.0.0.1/32"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DivideRangeBySizeWithLimit(tc.cidr, tc.bits, tc.limit)
			if (err != nil) != tc.wantError || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, %v; want %v, error=%v", got, err, tc.want, tc.wantError)
			}
		})
	}
}
func TestOverlapCharacterization(t *testing.T) {
	for _, tc := range []struct {
		a, b            string
		want, wantError bool
	}{
		{"10.0.0.0/24", "10.0.0.0/24", true, false},
		{"10.0.0.0/24", "10.0.0.128/25", true, false},
		{"10.0.0.0/25", "10.0.0.128/25", false, false},
		{"10.0.0.1/32", "10.0.0.0/31", true, false},
		{"255.255.255.255/32", "0.0.0.0/0", true, false},
		{"fd00::/64", "fd00::1/128", true, false},
		{"fd00::/127", "fd00::2/127", false, false},
		{"ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff/128", "::/0", true, false},
		{"10.0.0.0/24", "fd00::/64", false, false},
		{"10.0.0.0/24", "::ffff:10.0.0.0/120", false, false},
		{"10.0.0.1/24", "10.0.0.128/25", true, false},
		{"invalid", "10.0.0.0/24", false, true},
	} {
		t.Run(tc.a+" and "+tc.b, func(t *testing.T) {
			for _, pair := range [][2]string{{tc.a, tc.b}, {tc.b, tc.a}} {
				got, err := CIDRsOverlap(pair[0], pair[1])
				if got != tc.want || (err != nil) != tc.wantError {
					t.Fatalf("overlap(%q, %q) = %t, %v", pair[0], pair[1], got, err)
				}
			}
		})
	}
}
