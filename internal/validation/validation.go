// Copyright 2025 Deutsche Telekom
// SPDX-License-Identifier: Apache-2.0

// Package validation provides shared validation functions for Whereabouts CRD
// fields. These functions are used by both webhooks and reconcilers to ensure
// consistent validation across the codebase.
package validation

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// ValidateCIDR validates that cidr is a valid CIDR notation string (e.g. "10.0.0.0/8").
func ValidateCIDR(cidr string) error {
	if cidr == "" {
		return fmt.Errorf("CIDR is required")
	}
	_, _, err := net.ParseCIDR(cidr)
	if err != nil {
		return fmt.Errorf("invalid CIDR %q: %w", cidr, err)
	}
	return nil
}

// ValidateOmitRanges validates omitRanges entries as CIDRs or IP ranges.
func ValidateOmitRanges(omitRanges []string, poolCIDR string) error {
	for i, omitRange := range omitRanges {
		if err := validateOmitRange(omitRange); err != nil {
			return fmt.Errorf("omitRanges[%d] %q is not a valid CIDR or IP range: %w", i, omitRange, err)
		}
	}
	return nil
}

func validateOmitRange(omitRange string) error {
	if omitRange == "" {
		return fmt.Errorf("value is empty")
	}
	if _, _, err := net.ParseCIDR(omitRange); err == nil {
		return nil
	}
	if strings.Contains(omitRange, "-") {
		parts := strings.SplitN(omitRange, "-", 2)
		if len(parts) != 2 {
			return fmt.Errorf("malformed IP range")
		}
		start := net.ParseIP(strings.TrimSpace(parts[0]))
		end := net.ParseIP(strings.TrimSpace(parts[1]))
		if start == nil || end == nil {
			return fmt.Errorf("malformed IP range")
		}
		if (start.To4() == nil) != (end.To4() == nil) {
			return fmt.Errorf("IP range endpoints must use the same address family")
		}
		return nil
	}
	if ip := net.ParseIP(omitRange); ip != nil {
		return nil
	}
	return fmt.Errorf("must be a valid CIDR, IP, or IP range")
}

// ValidatePodRef validates that podRef is in "namespace/name" format.
// An empty podRef returns an error if required is true.
func ValidatePodRef(podRef string, required bool) error {
	if podRef == "" {
		if required {
			return fmt.Errorf("podRef is required")
		}
		return nil
	}
	parts := strings.SplitN(podRef, "/", 2)
	if len(parts) != 2 {
		return fmt.Errorf("podRef %q must contain a '/' separator (expected namespace/name format)", podRef)
	}
	if strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return fmt.Errorf("podRef %q has an empty namespace or name component (expected namespace/name format)", podRef)
	}
	return nil
}

// ValidateSliceSize validates and parses a slice size string like "/24" or "24".
// Returns the parsed prefix length (1-128).
func ValidateSliceSize(s string) (int, error) {
	if s == "" {
		return 0, fmt.Errorf("sliceSize is required")
	}
	trimmed := strings.TrimPrefix(s, "/")
	if strings.Contains(trimmed, "/") {
		// Only a single optional leading slash is allowed; any additional slash is invalid.
		return 0, fmt.Errorf("invalid sliceSize %q: must be a CIDR prefix length", s)
	}
	size, err := strconv.Atoi(trimmed)
	if err != nil {
		return 0, fmt.Errorf("invalid sliceSize %q: must be a CIDR prefix length", s)
	}
	if size < 1 || size > 128 {
		return 0, fmt.Errorf("invalid sliceSize %q: prefix length must be between 1 and 128", s)
	}
	return size, nil
}
