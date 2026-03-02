// Copyright 2025 Deutsche Telekom
// SPDX-License-Identifier: Apache-2.0

package webhook

import (
	"context"
	"fmt"
	"net"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	whereaboutsv1alpha1 "github.com/telekom/whereabouts/pkg/api/whereabouts.cni.cncf.io/v1alpha1"
)

// IPPoolValidator validates IPPool resources.
type IPPoolValidator struct{}

var _ admission.Validator[*whereaboutsv1alpha1.IPPool] = &IPPoolValidator{}

// SetupIPPoolWebhook registers the IPPool validating webhook with the manager.
func SetupIPPoolWebhook(mgr manager.Manager) error {
	return builder.WebhookManagedBy(mgr, &whereaboutsv1alpha1.IPPool{}).
		WithValidator(&IPPoolValidator{}).
		Complete()
}

//+kubebuilder:webhook:path=/validate-whereabouts-cni-cncf-io-v1alpha1-ippool,mutating=false,failurePolicy=Fail,sideEffects=None,groups=whereabouts.cni.cncf.io,resources=ippools,verbs=create;update,versions=v1alpha1,name=vippool.whereabouts.cni.cncf.io,admissionReviewVersions=v1

// ValidateCreate validates an IPPool on creation.
func (v *IPPoolValidator) ValidateCreate(_ context.Context, pool *whereaboutsv1alpha1.IPPool) (admission.Warnings, error) {
	return validateIPPool(pool)
}

// ValidateUpdate validates an IPPool on update.
func (v *IPPoolValidator) ValidateUpdate(_ context.Context, _, pool *whereaboutsv1alpha1.IPPool) (admission.Warnings, error) {
	return validateIPPool(pool)
}

// ValidateDelete is a no-op — deletes are always allowed.
func (v *IPPoolValidator) ValidateDelete(_ context.Context, _ *whereaboutsv1alpha1.IPPool) (admission.Warnings, error) {
	return nil, nil
}

func validateIPPool(pool *whereaboutsv1alpha1.IPPool) (admission.Warnings, error) {
	var warnings admission.Warnings

	// Validate Range is a valid CIDR.
	if pool.Spec.Range != "" {
		_, _, err := net.ParseCIDR(pool.Spec.Range)
		if err != nil {
			return nil, fmt.Errorf("invalid spec.range %q: %s", pool.Spec.Range, err)
		}
	}

	// Validate allocation podRefs.
	for key, alloc := range pool.Spec.Allocations {
		if alloc.PodRef == "" {
			warnings = append(warnings, fmt.Sprintf("allocation %s has empty podRef", key))
			continue
		}
		parts := strings.SplitN(alloc.PodRef, "/", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return nil, fmt.Errorf("allocation %s has invalid podRef %q: expected namespace/name", key, alloc.PodRef)
		}
	}

	return warnings, nil
}
