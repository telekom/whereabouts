// Copyright 2025 Deutsche Telekom
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"
)

func TestParseServiceCIDRs(t *testing.T) {
	t.Run("empty input returns nil without error", func(t *testing.T) {
		got, err := parseServiceCIDRs("")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != nil {
			t.Fatalf("expected nil, got %v", got)
		}
	})

	t.Run("valid single CIDR", func(t *testing.T) {
		got, err := parseServiceCIDRs("10.96.0.0/12")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 1 || got[0] != "10.96.0.0/12" {
			t.Fatalf("expected [10.96.0.0/12], got %v", got)
		}
	})

	t.Run("valid multiple CIDRs with spaces", func(t *testing.T) {
		got, err := parseServiceCIDRs("10.96.0.0/12, 192.168.0.0/16")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("expected 2 CIDRs, got %v", got)
		}
		if got[0] != "10.96.0.0/12" || got[1] != "192.168.0.0/16" {
			t.Fatalf("unexpected CIDRs: %v", got)
		}
	})

	t.Run("invalid CIDR returns error", func(t *testing.T) {
		_, err := parseServiceCIDRs("not-a-cidr")
		if err == nil {
			t.Fatal("expected error for invalid CIDR, got nil")
		}
	})

	t.Run("invalid CIDR among valid ones returns error", func(t *testing.T) {
		_, err := parseServiceCIDRs("10.96.0.0/12,bad-cidr,192.168.0.0/16")
		if err == nil {
			t.Fatal("expected error for invalid CIDR, got nil")
		}
	})

	t.Run("comma-separated entries with empty parts ignored", func(t *testing.T) {
		got, err := parseServiceCIDRs("10.96.0.0/12,,192.168.0.0/16")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("expected 2 CIDRs, got %v", got)
		}
	})
}
