// Copyright 2025 Deutsche Telekom
// SPDX-License-Identifier: Apache-2.0

// Package controller registers reconcilers with a controller-runtime Manager.
package controller

import (
	"time"

	ctrl "sigs.k8s.io/controller-runtime"
)

// SetupWithManager registers all reconcilers with the given manager. The
// reconcileInterval controls how often periodic re-checks of IP pools and
// related resources are triggered.
func SetupWithManager(mgr ctrl.Manager, reconcileInterval time.Duration) error {
	if err := SetupIPPoolReconciler(mgr, reconcileInterval); err != nil {
		return err
	}

	if err := SetupNodeSliceReconciler(mgr); err != nil {
		return err
	}

	if err := SetupOverlappingRangeReconciler(mgr, reconcileInterval); err != nil {
		return err
	}

	return nil
}
