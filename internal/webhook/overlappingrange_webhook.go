// Copyright 2025 Deutsche Telekom
// SPDX-License-Identifier: Apache-2.0

package webhook

import (
	"context"
	"fmt"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	whereaboutsv1alpha1 "github.com/telekom/whereabouts/pkg/api/whereabouts.cni.cncf.io/v1alpha1"
)

// OverlappingRangeValidator validates OverlappingRangeIPReservation resources.
type OverlappingRangeValidator struct{}

var _ admission.Validator[*whereaboutsv1alpha1.OverlappingRangeIPReservation] = &OverlappingRangeValidator{}

// SetupOverlappingRangeWebhook registers the ORIP validating webhook.
func SetupOverlappingRangeWebhook(mgr manager.Manager) error {
	return builder.WebhookManagedBy(mgr, &whereaboutsv1alpha1.OverlappingRangeIPReservation{}).
		WithValidator(&OverlappingRangeValidator{}).
		Complete()
}

//+kubebuilder:webhook:path=/validate-whereabouts-cni-cncf-io-v1alpha1-overlappingrangeipreservation,mutating=false,failurePolicy=Fail,sideEffects=None,groups=whereabouts.cni.cncf.io,resources=overlappingrangeipreservations,verbs=create;update,versions=v1alpha1,name=voverlappingrangeipreservation.whereabouts.cni.cncf.io,admissionReviewVersions=v1

// ValidateCreate validates an OverlappingRangeIPReservation on creation.
func (v *OverlappingRangeValidator) ValidateCreate(_ context.Context, res *whereaboutsv1alpha1.OverlappingRangeIPReservation) (admission.Warnings, error) {
	return validateOverlappingRange(res)
}

// ValidateUpdate validates an OverlappingRangeIPReservation on update.
func (v *OverlappingRangeValidator) ValidateUpdate(_ context.Context, _, res *whereaboutsv1alpha1.OverlappingRangeIPReservation) (admission.Warnings, error) {
	return validateOverlappingRange(res)
}

// ValidateDelete is a no-op.
func (v *OverlappingRangeValidator) ValidateDelete(_ context.Context, _ *whereaboutsv1alpha1.OverlappingRangeIPReservation) (admission.Warnings, error) {
	return nil, nil
}

func validateOverlappingRange(res *whereaboutsv1alpha1.OverlappingRangeIPReservation) (admission.Warnings, error) {
	// PodRef is required (enforced by MinLength=1 CRD validation, but double-check).
	if res.Spec.PodRef == "" {
		return nil, fmt.Errorf("spec.podref is required")
	}

	parts := strings.SplitN(res.Spec.PodRef, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, fmt.Errorf("spec.podref %q must be in namespace/name format", res.Spec.PodRef)
	}

	return nil, nil
}
