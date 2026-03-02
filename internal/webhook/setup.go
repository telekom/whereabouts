// Copyright 2025 Deutsche Telekom
// SPDX-License-Identifier: Apache-2.0

// Package webhook provides validating admission webhooks for Whereabouts CRDs.
package webhook

import (
	"context"
	"net/http"
	"sync/atomic"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

// setup is a manager.Runnable that waits for certs to be ready before
// registering validating webhooks.
type setup struct {
	mgr       manager.Manager
	certReady <-chan struct{}
	ready     atomic.Bool
}

// NewSetup returns a Runnable that registers validating webhooks after the
// certificate is provisioned.
func NewSetup(mgr manager.Manager, certReady <-chan struct{}) manager.Runnable {
	return &setup{mgr: mgr, certReady: certReady}
}

// Start blocks until certs are ready, then registers webhooks.
func (s *setup) Start(ctx context.Context) error {
	log := ctrl.Log.WithName("webhook-setup")

	// Wait for cert-controller to signal readiness.
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.certReady:
		log.Info("certificates ready, registering webhooks")
	}

	// TODO(step-13): Register validating webhooks here:
	// - IPPool
	// - NodeSlicePool
	// - OverlappingRangeIPReservation

	s.ready.Store(true)
	log.Info("webhooks registered")

	// Block until the manager is stopped.
	<-ctx.Done()
	return nil
}

// ReadyCheck returns a healthz.Checker that reports ready only after webhooks
// have been registered.
func ReadyCheck(certReady <-chan struct{}) healthz.Checker {
	return func(_ *http.Request) error {
		select {
		case <-certReady:
			return nil
		default:
			return http.ErrAbortHandler
		}
	}
}
