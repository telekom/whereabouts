// Copyright 2025 Deutsche Telekom
// SPDX-License-Identifier: Apache-2.0

// Package webhook provides validating admission webhooks for Whereabouts CRDs.
package webhook

import (
	"context"
	"fmt"

	"github.com/telekom/t-caas-go-library/pkg/certrotation"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

// SetupWithManager registers certificate-gated webhooks on every replica and
// returns a readiness checker for completed registration, not just certificates.
// Cancellation before certificate readiness is a graceful shutdown.
func SetupWithManager(mgr manager.Manager, certReady <-chan struct{}) (healthz.Checker, error) {
	done, err := certrotation.SetupWhenReady(mgr, certReady,
		func(context.Context) error {
			if err := SetupIPPoolWebhook(mgr); err != nil {
				return fmt.Errorf("registering IPPool webhook: %w", err)
			}
			return nil
		},
		func(context.Context) error {
			if err := SetupNodeSlicePoolWebhook(mgr); err != nil {
				return fmt.Errorf("registering NodeSlicePool webhook: %w", err)
			}
			return nil
		},
		func(context.Context) error {
			if err := SetupOverlappingRangeWebhook(mgr); err != nil {
				return fmt.Errorf("registering OverlappingRangeIPReservation webhook: %w", err)
			}
			return nil
		},
	)
	if err != nil {
		return nil, err
	}
	return certrotation.ReadyChecker(done), nil
}
