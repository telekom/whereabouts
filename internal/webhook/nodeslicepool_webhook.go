// Copyright 2025 Deutsche Telekom
// SPDX-License-Identifier: Apache-2.0

package webhook

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	whereaboutsv1alpha1 "github.com/telekom/whereabouts/pkg/api/whereabouts.cni.cncf.io/v1alpha1"
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
	if pool.Spec.Range == "" {
		return nil, fmt.Errorf("spec.range is required")
	}
	_, _, err := net.ParseCIDR(pool.Spec.Range)
	if err != nil {
		return nil, fmt.Errorf("invalid spec.range %q: %s", pool.Spec.Range, err)
	}

	// Validate SliceSize is parseable (format: "/24" or "24").
	if pool.Spec.SliceSize == "" {
		return nil, fmt.Errorf("spec.sliceSize is required")
	}
	s := strings.TrimPrefix(pool.Spec.SliceSize, "/")
	size, err := strconv.Atoi(s)
	if err != nil {
		return nil, fmt.Errorf("invalid spec.sliceSize %q: must be a CIDR prefix length", pool.Spec.SliceSize)
	}
	if size < 1 || size > 128 {
		return nil, fmt.Errorf("invalid spec.sliceSize %q: prefix length must be between 1 and 128", pool.Spec.SliceSize)
	}

	return nil, nil
}
