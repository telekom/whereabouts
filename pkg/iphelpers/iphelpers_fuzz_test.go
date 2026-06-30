// SPDX-FileCopyrightText: 2026 Deutsche Telekom AG
//
// SPDX-License-Identifier: Apache-2.0

package iphelpers

import (
	"net"
	"net/netip"
	"strconv"
	"strings"
	"testing"
)

func FuzzCIDRParsing(f *testing.F) {
	f.Add("10.0.0.0/24", "/28", "10.0.0.10", "10.0.0.100")
	f.Add("10.0.0.4/31", "31", "10.0.0.4", "10.0.0.5")
	f.Add("10.0.0.5/32", "/32", "10.0.0.5", "10.0.0.5")
	f.Add("fd00::/124", "/126", "fd00::2", "fd00::c")
	f.Add("2001:db8:abcd:12::/64", "/120", "2001:db8:abcd:12::50", "2001:db8:abcd:12::100")
	f.Add("not-a-cidr", "abc", "not-an-ip", "")

	f.Fuzz(func(t *testing.T, cidr, sliceSize, rangeStart, rangeEnd string) {
		skipOversizedIPHelpersFuzzInput(t, cidr, sliceSize, rangeStart, rangeEnd)

		_, _ = CountUsableIPs(cidr)
		if canFuzzDivideRangeBySize(cidr, sliceSize) {
			_, _ = DivideRangeBySize(cidr, sliceSize)
		}

		_, ipnet, err := net.ParseCIDR(cidr)
		if err != nil {
			return
		}

		first, last, err := GetIPRange(*ipnet, net.ParseIP(rangeStart), net.ParseIP(rangeEnd))
		if err != nil {
			return
		}
		if first == nil || last == nil {
			t.Fatalf("GetIPRange(%q, %q, %q) returned nil range without error", cidr, rangeStart, rangeEnd)
		}
	})
}

func canFuzzDivideRangeBySize(inputNetwork, sliceSizeString string) bool {
	sliceSize, err := strconv.Atoi(strings.TrimPrefix(sliceSizeString, "/"))
	if err != nil {
		return true
	}

	prefix, err := netip.ParsePrefix(inputNetwork)
	if err != nil {
		return true
	}
	if prefix.Addr() != prefix.Masked().Addr() {
		return true
	}
	if prefix.Bits() > sliceSize {
		return true
	}

	addrLen := 32
	if prefix.Addr().Is6() {
		addrLen = 128
	}
	if sliceSize > addrLen {
		return true
	}

	return sliceSize-prefix.Bits() <= 8
}

func skipOversizedIPHelpersFuzzInput(t *testing.T, values ...string) {
	t.Helper()

	for _, value := range values {
		if len(value) > 256 {
			t.Skip()
		}
	}
}
