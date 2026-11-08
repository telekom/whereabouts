// Copyright 2025 Deutsche Telekom
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"crypto/tls"
	"slices"
	"strings"
	"testing"
)

func TestWebhookTLSOpts(t *testing.T) {
	if opts := webhookTLSOpts(true); opts != nil {
		t.Fatalf("expected no TLS options with Go defaults, got %d", len(opts))
	}

	cfg := &tls.Config{}
	for _, opt := range webhookTLSOpts(false) {
		opt(cfg)
	}
	if cfg.MinVersion != tls.VersionTLS12 {
		t.Fatalf("expected MinVersion TLS 1.2, got %x", cfg.MinVersion)
	}
	if len(cfg.CipherSuites) == 0 {
		t.Fatal("expected pinned cipher suites")
	}
	for _, id := range cfg.CipherSuites {
		name := tls.CipherSuiteName(id)
		aead := strings.Contains(name, "_GCM_") || strings.Contains(name, "CHACHA20_POLY1305")
		if !strings.HasPrefix(name, "TLS_ECDHE_") || !aead {
			t.Errorf("cipher suite %s is not ECDHE with AES-GCM or ChaCha20-Poly1305", name)
		}
		if slices.ContainsFunc(tls.InsecureCipherSuites(), func(s *tls.CipherSuite) bool { return s.ID == id }) {
			t.Errorf("cipher suite %s is insecure", name)
		}
	}
}

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
