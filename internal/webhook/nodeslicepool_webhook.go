// Copyright 2025 Deutsche Telekom
// SPDX-License-Identifier: Apache-2.0

package webhook

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	whereaboutsv1alpha1 "github.com/telekom/whereabouts/pkg/api/whereabouts.cni.cncf.io/v1alpha1"

	"github.com/telekom/whereabouts/internal/validation"
)

// NodeSlicePoolValidator validates NodeSlicePool resources.
type NodeSlicePoolValidator struct{}

var _ admission.Validator[*whereaboutsv1alpha1.NodeSlicePool] = &NodeSlicePoolValidator{}

// SetupNodeSlicePoolWebhook registers the NodeSlicePool validating webhook.
func SetupNodeSlicePoolWebhook(mgr manager.Manager) error {
	return builder.WebhookManagedBy(mgr, &whereaboutsv1alpha1.NodeSlicePool{}).
		WithValidator(&NodeSlicePoolValidator{}).
		Complete()
}

//+kubebuilder:webhook:path=/validate-whereabouts-cni-cncf-io-v1alpha1-nodeslicepool,mutating=false,failurePolicy=Fail,sideEffects=None,groups=whereabouts.cni.cncf.io,resources=nodeslicepools,verbs=create;update,versions=v1alpha1,name=vnodeslicepool.whereabouts.cni.cncf.io,admissionReviewVersions=v1

// ValidateCreate validates a NodeSlicePool on creation.
func (v *NodeSlicePoolValidator) ValidateCreate(_ context.Context, pool *whereaboutsv1alpha1.NodeSlicePool) (admission.Warnings, error) {
	return validateNodeSlicePool(pool)
}

// ValidateUpdate validates a NodeSlicePool on update.
func (v *NodeSlicePoolValidator) ValidateUpdate(_ context.Context, _, pool *whereaboutsv1alpha1.NodeSlicePool) (admission.Warnings, error) {
	return validateNodeSlicePool(pool)
}

// ValidateDelete is a no-op.
func (v *NodeSlicePoolValidator) ValidateDelete(_ context.Context, _ *whereaboutsv1alpha1.NodeSlicePool) (admission.Warnings, error) {
	return nil, nil
}

func validateNodeSlicePool(pool *whereaboutsv1alpha1.NodeSlicePool) (admission.Warnings, error) {
	// Validate Range is a valid CIDR.
	if err := validation.ValidateCIDR(pool.Spec.Range); err != nil {
		return nil, fmt.Errorf("invalid spec.range: %s", err)
	}

	// Validate SliceSize is parseable.
	_, err := validation.ValidateSliceSize(pool.Spec.SliceSize)
	if err != nil {
		return nil, fmt.Errorf("invalid spec.sliceSize: %s", err)
	}

	return nil, nil
}
