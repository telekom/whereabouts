// Copyright 2025 Deutsche Telekom
// SPDX-License-Identifier: Apache-2.0

package webhook

import (
	"context"
	"fmt"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	whereaboutsv1alpha1 "github.com/telekom/whereabouts/api/whereabouts.cni.cncf.io/v1alpha1"

	"github.com/telekom/whereabouts/internal/validation"
	"github.com/telekom/whereabouts/pkg/iphelpers"
)

// IPPoolValidator validates IPPool resources.
type IPPoolValidator struct {
	// Reader is used to list existing IPPools for overlap detection.
	// When nil, pool-to-pool overlap checks are skipped (safe default for tests).
	Reader client.Reader
}

var ippoolLog = ctrl.Log.WithName("webhook").WithName("ippool")

var _ admission.Validator[*whereaboutsv1alpha1.IPPool] = &IPPoolValidator{}

// SetupIPPoolWebhook registers the IPPool validating webhook with the manager.
func SetupIPPoolWebhook(mgr manager.Manager) error {
	return builder.WebhookManagedBy(mgr, &whereaboutsv1alpha1.IPPool{}).
		WithValidator(&IPPoolValidator{Reader: mgr.GetClient()}).
		Complete()
}

//+kubebuilder:webhook:path=/validate-whereabouts-cni-cncf-io-v1alpha1-ippool,mutating=false,failurePolicy=Fail,sideEffects=None,groups=whereabouts.cni.cncf.io,resources=ippools,verbs=create;update,versions=v1alpha1,name=vippool.whereabouts.cni.cncf.io,admissionReviewVersions=v1

// ValidateCreate validates an IPPool on creation.
func (v *IPPoolValidator) ValidateCreate(ctx context.Context, pool *whereaboutsv1alpha1.IPPool) (admission.Warnings, error) {
	w, err := validateIPPool(pool)
	if err != nil {
		ippoolLog.Info("rejected", "name", pool.Name, "operation", "create", "reason", err.Error())
		recordValidation("ippool", "create", err)
		return w, err
	}
	if err := v.checkPoolOverlap(ctx, pool); err != nil {
		ippoolLog.Info("rejected", "name", pool.Name, "operation", "create", "reason", err.Error())
		recordValidation("ippool", "create", err)
		return w, err
	}
	recordValidation("ippool", "create", nil)
	return w, nil
}

// ValidateUpdate validates an IPPool on update.
func (v *IPPoolValidator) ValidateUpdate(ctx context.Context, oldPool, pool *whereaboutsv1alpha1.IPPool) (admission.Warnings, error) {
	var warnings admission.Warnings
	// Warn (but allow) range changes to support expansion/resizing.
	if oldPool != nil && oldPool.Spec.Range != pool.Spec.Range {
		warnings = append(warnings, fmt.Sprintf(
			"spec.range changed from %q to %q - existing allocations outside the new range will be orphaned",
			oldPool.Spec.Range, pool.Spec.Range))
	}
	w, err := validateIPPool(pool)
	if err != nil {
		ippoolLog.Info("rejected", "name", pool.Name, "operation", "update", "reason", err.Error())
		recordValidation("ippool", "update", err)
		return append(warnings, w...), err
	}
	if err := v.checkPoolOverlap(ctx, pool); err != nil {
		ippoolLog.Info("rejected", "name", pool.Name, "operation", "update", "reason", err.Error())
		recordValidation("ippool", "update", err)
		return append(warnings, w...), err
	}
	recordValidation("ippool", "update", nil)
	return append(warnings, w...), nil
}

// ValidateDelete is a no-op — deletes are always allowed.
func (v *IPPoolValidator) ValidateDelete(_ context.Context, _ *whereaboutsv1alpha1.IPPool) (admission.Warnings, error) {
	recordValidation("ippool", "delete", nil)
	return nil, nil
}

// checkPoolOverlap returns an error if pool's spec.range overlaps any existing IPPool
// (excluding the pool itself, identified by name+namespace). When v.Reader is nil,
// the check is skipped (safe for unit tests without a live client).
func (v *IPPoolValidator) checkPoolOverlap(ctx context.Context, pool *whereaboutsv1alpha1.IPPool) error {
	if v.Reader == nil {
		return nil
	}
	var poolList whereaboutsv1alpha1.IPPoolList
	if err := v.Reader.List(ctx, &poolList, client.InNamespace(pool.Namespace)); err != nil {
		return fmt.Errorf("listing existing IPPools: %w", err)
	}
	for i := range poolList.Items {
		existing := &poolList.Items[i]
		// Skip self-comparison (same name+namespace = update of the same pool).
		if existing.Name == pool.Name && existing.Namespace == pool.Namespace {
			continue
		}
		overlaps, err := iphelpers.CIDRsOverlap(pool.Spec.Range, existing.Spec.Range)
		if err != nil {
			// Invalid CIDR in an existing pool — skip it (don't block on others' misconfiguration).
			ippoolLog.Error(err, "skipping overlap check for existing pool with invalid CIDR",
				"pool", existing.Name, "range", existing.Spec.Range)
			continue
		}
		if overlaps {
			return fmt.Errorf("spec.range %q overlaps with existing IPPool %q (%s)",
				pool.Spec.Range, existing.Name, existing.Spec.Range)
		}
	}
	return nil
}

func validateIPPool(pool *whereaboutsv1alpha1.IPPool) (admission.Warnings, error) {
	var warnings admission.Warnings

	// Validate Range is a valid CIDR.
	if err := validation.ValidateCIDR(pool.Spec.Range); err != nil {
		return nil, fmt.Errorf("invalid spec.range: %w", err)
	}

	// Validate allocation podRefs.
	for key, alloc := range pool.Spec.Allocations {
		if alloc.PodRef == "" {
			warnings = append(warnings, fmt.Sprintf("allocation %s has empty podRef", key))
			continue
		}
		if err := validation.ValidatePodRef(alloc.PodRef, false); err != nil {
			return nil, fmt.Errorf("allocation %s: %w", key, err)
		}
	}

	return warnings, nil
}
