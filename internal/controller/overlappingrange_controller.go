// Copyright 2025 Deutsche Telekom
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	whereaboutsv1alpha1 "github.com/telekom/whereabouts/pkg/api/whereabouts.cni.cncf.io/v1alpha1"
)

// OverlappingRangeReconciler reconciles OverlappingRangeIPReservation CRDs by
// deleting reservations whose pods no longer exist. This provides a secondary
// cleanup path in addition to the IPPoolReconciler's inline cleanup.
type OverlappingRangeReconciler struct {
	client            client.Client
	reconcileInterval time.Duration
}

// SetupOverlappingRangeReconciler creates and registers the reconciler.
func SetupOverlappingRangeReconciler(mgr ctrl.Manager, reconcileInterval time.Duration) error {
	r := &OverlappingRangeReconciler{
		client:            mgr.GetClient(),
		reconcileInterval: reconcileInterval,
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&whereaboutsv1alpha1.OverlappingRangeIPReservation{}).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 1,
		}).
		Named("overlappingrange").
		Complete(r)
}

//+kubebuilder:rbac:groups=whereabouts.cni.cncf.io,resources=overlappingrangeipreservations,verbs=get;list;watch;delete
//+kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch

// Reconcile checks whether the pod referenced by the OverlappingRangeIPReservation
// still exists. If not, the reservation is deleted.
func (r *OverlappingRangeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var reservation whereaboutsv1alpha1.OverlappingRangeIPReservation
	if err := r.client.Get(ctx, req.NamespacedName, &reservation); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("getting OverlappingRangeIPReservation: %s", err)
	}

	// Skip if no podRef — nothing to check.
	if reservation.Spec.PodRef == "" {
		return ctrl.Result{RequeueAfter: r.reconcileInterval}, nil
	}

	podNS, podName, ok := parsePodRef(reservation.Spec.PodRef)
	if !ok {
		logger.Info("invalid podRef format, deleting reservation",
			"name", reservation.Name, "podRef", reservation.Spec.PodRef)
		return r.deleteReservation(ctx, &reservation)
	}

	var pod corev1.Pod
	err := r.client.Get(ctx, types.NamespacedName{Namespace: podNS, Name: podName}, &pod)
	if errors.IsNotFound(err) {
		logger.V(1).Info("pod not found, deleting overlapping reservation",
			"name", reservation.Name, "podRef", reservation.Spec.PodRef)
		return r.deleteReservation(ctx, &reservation)
	}
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("getting pod %s: %s", reservation.Spec.PodRef, err)
	}

	// Pod marked for deletion.
	if isPodMarkedForDeletion(pod.Status.Conditions) {
		logger.V(1).Info("pod marked for deletion, deleting overlapping reservation",
			"name", reservation.Name, "podRef", reservation.Spec.PodRef)
		return r.deleteReservation(ctx, &reservation)
	}

	return ctrl.Result{RequeueAfter: r.reconcileInterval}, nil
}

// deleteReservation removes the ORIP CR.
func (r *OverlappingRangeReconciler) deleteReservation(ctx context.Context, reservation *whereaboutsv1alpha1.OverlappingRangeIPReservation) (ctrl.Result, error) {
	if err := r.client.Delete(ctx, reservation); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("deleting OverlappingRangeIPReservation %s: %s", reservation.Name, err)
	}
	return ctrl.Result{}, nil
}

var _ reconcile.Reconciler = &OverlappingRangeReconciler{}
