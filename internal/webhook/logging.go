// Copyright 2026 Deutsche Telekom AG
// SPDX-License-Identifier: Apache-2.0

package webhook

import (
	"strings"

	"github.com/go-logr/logr"
)

func logValidationRejection(logger logr.Logger, operation string, err error) {
	if err == nil {
		return
	}
	logger.Info("rejected", "operation", operation, "reason", validationRejectionReason(err))
}

func validationRejectionReason(err error) string {
	if err == nil {
		return "none"
	}

	msg := err.Error()
	switch {
	case strings.Contains(msg, "immutable"):
		return "immutable field changed"
	case strings.Contains(msg, "old object is nil"):
		return "invalid update request"
	case strings.Contains(msg, "podRef"):
		return "invalid pod reference"
	case strings.Contains(msg, "sliceSize"):
		return "invalid slice size"
	case strings.Contains(msg, "CIDR") || strings.Contains(msg, "spec.range"):
		return "invalid CIDR"
	default:
		return "validation failed"
	}
}
