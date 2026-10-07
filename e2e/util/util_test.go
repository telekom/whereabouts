// SPDX-FileCopyrightText: 2026 Deutsche Telekom AG
// SPDX-License-Identifier: Apache-2.0

package util

import "testing"

func TestInIPRange(t *testing.T) {
	for _, tt := range []struct {
		name, start, end, ip string
		wantErr              bool
	}{
		{"IPv4 start included", "10.0.0.1", "10.0.0.3", "10.0.0.1", false},
		{"IPv4 end included", "10.0.0.1", "10.0.0.3", "10.0.0.3", false},
		{"IPv4 outside", "10.0.0.1", "10.0.0.3", "10.0.0.4", true},
		{"IPv6 inside", "fd00::ff", "fd00::101", "fd00::100", false},
		{"IPv6 outside", "fd00::ff", "fd00::101", "fd00::fe", true},
		{"mapped IPv4 equivalent", "10.0.0.1", "10.0.0.3", "::ffff:10.0.0.2", false},
		{"mixed family retains byte ordering", "::", "::1:0:0:0", "10.0.0.2", false},
		{"reversed range", "10.0.0.3", "10.0.0.1", "10.0.0.2", true},
		{"invalid start", "bad", "10.0.0.3", "10.0.0.2", true},
		{"invalid end", "10.0.0.1", "bad", "10.0.0.2", true},
		{"invalid IP", "10.0.0.1", "10.0.0.3", "bad", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := InIPRange(tt.start, tt.end, tt.ip); (err != nil) != tt.wantErr {
				t.Fatalf("InIPRange(%q, %q, %q) = %v, wantErr %t", tt.start, tt.end, tt.ip, err, tt.wantErr)
			}
		})
	}
}
