// Copyright 2026 Deutsche Telekom AG
// SPDX-License-Identifier: Apache-2.0

package hack_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestE2EToolBootstrapPinsVersionsAndVerifiesChecksums(t *testing.T) {
	path := filepath.Join("..", "hack", "e2e-get-test-tools.sh")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	contents := string(data)

	if strings.Contains(contents, "stable.txt") {
		t.Fatalf("%s must not resolve kubectl through the mutable stable.txt channel", path)
	}
	for name, pattern := range map[string]string{
		"KIND_VERSION":    `(?m)^KIND_VERSION="v[0-9]+\.[0-9]+\.[0-9]+"$`,
		"KUBECTL_VERSION": `(?m)^KUBECTL_VERSION="v[0-9]+\.[0-9]+\.[0-9]+"$`,
	} {
		if !regexp.MustCompile(pattern).MatchString(contents) {
			t.Fatalf("%s must set a pinned %s", path, name)
		}
	}
	if !strings.Contains(contents, `KIND_CHECKSUM_URL="${KIND_BINARY_URL}.sha256sum"`) {
		t.Fatalf("%s must download kind's published checksum", path)
	}
	if strings.Count(contents, "verify_sha256 ") < 2 {
		t.Fatalf("%s must verify both kind and kubectl checksums", path)
	}
}
