// Copyright 2025 Deutsche Telekom
// SPDX-License-Identifier: Apache-2.0

package validation

import (
	"strings"
	"testing"
)

func TestValidateCIDR(t *testing.T) {
	tests := []struct {
		name    string
		cidr    string
		wantErr bool
	}{
		{name: "valid IPv4 CIDR", cidr: "10.0.0.0/8", wantErr: false},
		{name: "valid IPv4 /32", cidr: "192.168.1.1/32", wantErr: false},
		{name: "valid IPv6 CIDR", cidr: "fd00::/48", wantErr: false},
		{name: "empty string", cidr: "", wantErr: true},
		{name: "not a CIDR", cidr: "not-a-cidr", wantErr: true},
		{name: "IP without mask", cidr: "10.0.0.1", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCIDR(tt.cidr)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateCIDR(%q) error = %v, wantErr %v", tt.cidr, err, tt.wantErr)
			}
		})
	}
}

func TestValidatePodRef(t *testing.T) {
	tests := []struct {
		name     string
		podRef   string
		required bool
		wantErr  bool
	}{
		{name: "valid podRef", podRef: "default/my-pod", required: true, wantErr: false},
		{name: "empty required", podRef: "", required: true, wantErr: true},
		{name: "empty optional", podRef: "", required: false, wantErr: false},
		{name: "no slash", podRef: "justpodname", required: true, wantErr: true},
		{name: "empty namespace", podRef: "/pod", required: true, wantErr: true},
		{name: "empty name", podRef: "ns/", required: true, wantErr: true},
		// Intentionally lenient: SplitN(podRef, "/", 2) yields ["ns", "pod/extra"],
		// so "pod/extra" is accepted as the pod name. This matches Kubernetes behavior
		// where pod names cannot contain slashes, but we don't reject at this layer.
		{name: "multiple slashes", podRef: "ns/pod/extra", required: true, wantErr: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePodRef(tt.podRef, tt.required)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidatePodRef(%q, %v) error = %v, wantErr %v", tt.podRef, tt.required, err, tt.wantErr)
			}
		})
	}
}

func TestValidateSliceSize(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    int
		wantErr bool
	}{
		{name: "valid /24", input: "/24", want: 24, wantErr: false},
		{name: "valid 24 (no leading slash)", input: "24", want: 24, wantErr: false},
		{name: "valid /1", input: "/1", want: 1, wantErr: false},
		{name: "valid /128", input: "/128", want: 128, wantErr: false},
		{name: "zero", input: "0", want: 0, wantErr: true},
		{name: "too large", input: "129", want: 0, wantErr: true},
		{name: "empty", input: "", want: 0, wantErr: true},
		{name: "not a number", input: "abc", want: 0, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ValidateSliceSize(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateSliceSize(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("ValidateSliceSize(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestValidateOmitRanges(t *testing.T) {
	tests := []struct {
		name       string
		omitRanges []string
		poolCIDR   string
		wantErr    bool
		errMsg     string
	}{
		{name: "empty slice", omitRanges: nil, poolCIDR: "10.0.0.0/24", wantErr: false},
		{name: "valid cidr in pool", omitRanges: []string{"10.0.0.0/28"}, poolCIDR: "10.0.0.0/24", wantErr: false},
		{name: "valid cidr outside pool", omitRanges: []string{"192.168.0.0/24"}, poolCIDR: "10.0.0.0/24", wantErr: false},
		{name: "valid ip range", omitRanges: []string{"10.0.0.1-10.0.0.5"}, poolCIDR: "10.0.0.0/24", wantErr: false},
		{name: "not a cidr", omitRanges: []string{"not-a-cidr"}, poolCIDR: "10.0.0.0/24", wantErr: true, errMsg: "not-a-cidr"},
		{name: "invalid cidr", omitRanges: []string{"999.999.999.999/32"}, poolCIDR: "10.0.0.0/24", wantErr: true, errMsg: "999.999.999.999/32"},
		{name: "mixed valid and invalid", omitRanges: []string{"10.0.0.0/28", "bad-entry"}, poolCIDR: "10.0.0.0/24", wantErr: true, errMsg: "omitRanges[1]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateOmitRanges(tt.omitRanges, tt.poolCIDR)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateOmitRanges(%v, %q) error = %v, wantErr %v", tt.omitRanges, tt.poolCIDR, err, tt.wantErr)
			}
			if tt.errMsg != "" && err != nil && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("ValidateOmitRanges(%v, %q) error = %q, want substring %q", tt.omitRanges, tt.poolCIDR, err.Error(), tt.errMsg)
			}
		})
	}
}
