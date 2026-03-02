// Copyright 2025 Deutsche Telekom
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"strings"
	"time"

	nadv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	whereaboutsv1alpha1 "github.com/telekom/whereabouts/pkg/api/whereabouts.cni.cncf.io/v1alpha1"
	"github.com/telekom/whereabouts/pkg/iphelpers"
)

// IPPoolReconciler reconciles IPPool resources by removing allocations whose
// pods no longer exist. It replaces the legacy CronJob-based reconciler and
// the DaemonSet pod controller.
type IPPoolReconciler struct {
	client            client.Client
	reconcileInterval time.Duration
}

const (
	// retryRequeueInterval is the interval to retry when transient errors
	// occur (e.g. overlapping reservation cleanup failure).
	retryRequeueInterval = 5 * time.Second

	// pendingPodRequeueInterval is the interval to recheck allocations for
	// pods still in the Pending phase.
	pendingPodRequeueInterval = 5 * time.Second
)

// SetupIPPoolReconciler creates and registers the IPPoolReconciler with the
// manager. The reconcileInterval controls the periodic re-queue interval.
func SetupIPPoolReconciler(mgr ctrl.Manager, reconcileInterval time.Duration) error {
	r := &IPPoolReconciler{
		client:            mgr.GetClient(),
		reconcileInterval: reconcileInterval,
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&whereaboutsv1alpha1.IPPool{}).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 1,
		}).
		Named("ippool").
		Complete(r)
}

//+kubebuilder:rbac:groups=whereabouts.cni.cncf.io,resources=ippools,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=whereabouts.cni.cncf.io,resources=overlappingrangeipreservations,verbs=get;list;watch;delete
//+kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch

// Reconcile checks all allocations in the IPPool against live pods and removes
// orphaned entries.
func (r *IPPoolReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var pool whereaboutsv1alpha1.IPPool
	if err := r.client.Get(ctx, req.NamespacedName, &pool); err != nil {
		if errors.IsNotFound(err) {
			ippoolAllocationsGauge.DeleteLabelValues(req.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("getting IPPool: %s", err)
	}

	// Skip pools being deleted.
	if !pool.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	// Report current allocation count.
	ippoolAllocationsGauge.WithLabelValues(pool.Name).Set(float64(len(pool.Spec.Allocations)))

	if len(pool.Spec.Allocations) == 0 {
		return ctrl.Result{RequeueAfter: r.reconcileInterval}, nil
	}

	// Collect orphaned allocation keys.
	var orphanedKeys []string
	var hasPending bool

	for key, alloc := range pool.Spec.Allocations {
		if alloc.PodRef == "" {
			logger.Info("allocation missing podRef, marking orphaned", "key", key)
			orphanedKeys = append(orphanedKeys, key)
			continue
		}

		podNS, podName, ok := parsePodRef(alloc.PodRef)
		if !ok {
			logger.Info("invalid podRef format, marking orphaned", "key", key, "podRef", alloc.PodRef)
			orphanedKeys = append(orphanedKeys, key)
			continue
		}

		var pod corev1.Pod
		err := r.client.Get(ctx, types.NamespacedName{Namespace: podNS, Name: podName}, &pod)
		if errors.IsNotFound(err) {
			logger.V(1).Info("pod not found, marking allocation orphaned",
				"key", key, "podRef", alloc.PodRef)
			orphanedKeys = append(orphanedKeys, key)
			continue
		}
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("getting pod %s: %s", alloc.PodRef, err)
		}

		// Pod marked for deletion by taint manager — treat as orphaned.
		if isPodMarkedForDeletion(pod.Status.Conditions) {
			logger.V(1).Info("pod marked for deletion, marking allocation orphaned",
				"key", key, "podRef", alloc.PodRef)
			orphanedKeys = append(orphanedKeys, key)
			continue
		}

		// Pending pods may not have network-status annotation yet.
		if pod.Status.Phase == corev1.PodPending {
			hasPending = true
			continue
		}

		// Verify the IP is actually present on the pod (Multus network-status).
		poolIP := allocationKeyToIP(&pool, key)
		if poolIP != nil && !isPodUsingIP(&pod, poolIP) {
			logger.V(1).Info("IP not found on pod, marking allocation orphaned",
				"key", key, "podRef", alloc.PodRef, "ip", poolIP)
			orphanedKeys = append(orphanedKeys, key)
			continue
		}
	}

	// Remove orphaned allocations.
	if len(orphanedKeys) > 0 {
		if err := r.removeAllocations(ctx, &pool, orphanedKeys); err != nil {
			return ctrl.Result{}, fmt.Errorf("removing orphaned allocations: %s", err)
		}
		ippoolOrphansCleaned.WithLabelValues(pool.Name).Add(float64(len(orphanedKeys)))
		logger.Info("cleaned up orphaned allocations",
			"pool", pool.Name, "count", len(orphanedKeys))

		// Also clean up any corresponding OverlappingRangeIPReservation CRDs.
		if err := r.cleanupOverlappingReservations(ctx, &pool, orphanedKeys); err != nil {
			logger.Error(err, "failed to clean up some overlapping reservations, will retry")
			return ctrl.Result{RequeueAfter: retryRequeueInterval}, nil
		}
	}

	// Requeue sooner if pending pods exist.
	if hasPending {
		return ctrl.Result{RequeueAfter: pendingPodRequeueInterval}, nil
	}

	// Update allocation gauge after cleanup.
	ippoolAllocationsGauge.WithLabelValues(pool.Name).Set(float64(len(pool.Spec.Allocations)))

	return ctrl.Result{RequeueAfter: r.reconcileInterval}, nil
}

// removeAllocations patches the IPPool to remove the specified allocation keys.
func (r *IPPoolReconciler) removeAllocations(ctx context.Context, pool *whereaboutsv1alpha1.IPPool, keys []string) error {
	newAllocations := make(map[string]whereaboutsv1alpha1.IPAllocation, len(pool.Spec.Allocations)-len(keys))
	removeSet := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		removeSet[k] = struct{}{}
	}
	for k, v := range pool.Spec.Allocations {
		if _, remove := removeSet[k]; !remove {
			newAllocations[k] = v
		}
	}

	patch := client.MergeFrom(pool.DeepCopy())
	pool.Spec.Allocations = newAllocations

	return r.client.Patch(ctx, pool, patch)
}

// cleanupOverlappingReservations deletes OverlappingRangeIPReservation CRDs
// for IPs that were in the orphaned allocations. Returns an error if any
// deletion fails (excluding NotFound).
func (r *IPPoolReconciler) cleanupOverlappingReservations(ctx context.Context, pool *whereaboutsv1alpha1.IPPool, keys []string) error {
	logger := log.FromContext(ctx)
	var lastErr error

	// List all overlapping reservations in the pool namespace once and reuse for all keys.
	var reservations whereaboutsv1alpha1.OverlappingRangeIPReservationList
	if err := r.client.List(ctx, &reservations, client.InNamespace(pool.Namespace)); err != nil {
		logger.V(1).Info("failed to list overlapping reservations", "error", err)
		return err
	}

	for _, key := range keys {
		ip := allocationKeyToIP(pool, key)
		if ip == nil {
			continue
		}

		for i := range reservations.Items {
			res := &reservations.Items[i]
			resIP := denormalizeIPName(res.Name)
			if resIP != nil && resIP.Equal(ip) {
				if err := r.client.Delete(ctx, res); err != nil && !errors.IsNotFound(err) {
					logger.Error(err, "failed to delete overlapping reservation",
						"name", res.Name)
					lastErr = err
				} else if err == nil {
					overlappingReservationsCleaned.Inc()
					logger.V(1).Info("deleted overlapping reservation", "name", res.Name)
				}
			}
		}
	}

	return lastErr
}

// allocationKeyToIP converts an allocation map key (decimal offset) to an IP
// address using the pool's CIDR range. Supports arbitrarily large offsets via
// big.Int to handle wide IPv6 ranges (e.g. /64 or wider).
func allocationKeyToIP(pool *whereaboutsv1alpha1.IPPool, key string) net.IP {
	_, ipNet, err := net.ParseCIDR(pool.Spec.Range)
	if err != nil {
		return nil
	}

	// Parse offset as big.Int — must be non-negative for a valid allocation key.
	offset, ok := new(big.Int).SetString(key, 10)
	if !ok || offset.Sign() < 0 {
		return nil
	}

	return iphelpers.IPAddOffset(ipNet.IP, offset)
}

// denormalizeIPName converts a normalized IP name (dashes for colons) back to
// a net.IP. Handles optional network-name prefix.
//
// Names may have a network-name prefix: "mynet-10.0.0.5" or just "10.0.0.5".
// For IPv6, colons are replaced with dashes: "fd00--1" for "fd00::1".
func denormalizeIPName(name string) net.IP {
	// Try parsing as-is first (IPv4 with dots preserved).
	if ip := net.ParseIP(name); ip != nil {
		return ip
	}

	// Try full dash→colon replacement (IPv6 normalization).
	if ip := net.ParseIP(strings.ReplaceAll(name, "-", ":")); ip != nil {
		return ip
	}

	// Iteratively strip leading dash-separated prefix segments.
	// e.g. "mynet-10.0.0.5" → try "10.0.0.5",
	// "mynet-fd00--1" → try "fd00--1" → replace → "fd00::1".
	for i := strings.IndexByte(name, '-'); i >= 0; i = strings.IndexByte(name[i+1:], '-') + i + 1 {
		suffix := name[i+1:]
		if ip := net.ParseIP(suffix); ip != nil {
			return ip
		}
		if ip := net.ParseIP(strings.ReplaceAll(suffix, "-", ":")); ip != nil {
			return ip
		}
	}

	return nil
}

// parsePodRef splits "namespace/name" into its components.
func parsePodRef(podRef string) (namespace, name string, ok bool) {
	parts := strings.SplitN(podRef, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// isPodMarkedForDeletion returns true if the pod has a DisruptionTarget
// condition indicating it's being evicted.
func isPodMarkedForDeletion(conditions []corev1.PodCondition) bool {
	for _, c := range conditions {
		if c.Type == corev1.DisruptionTarget &&
			c.Status == corev1.ConditionTrue &&
			c.Reason == "DeletionByTaintManager" {
			return true
		}
	}
	return false
}

// isPodUsingIP checks whether the pod's Multus network-status annotation
// contains the given IP address. Uses net.IP.Equal for proper IPv6 comparison.
func isPodUsingIP(pod *corev1.Pod, ip net.IP) bool {
	annotation, ok := pod.Annotations[nadv1.NetworkStatusAnnot]
	if !ok || annotation == "" {
		// No annotation — cannot confirm; assume still valid to avoid
		// false-positive cleanup.
		return true
	}

	var statuses []nadv1.NetworkStatus
	if err := json.Unmarshal([]byte(annotation), &statuses); err != nil {
		// Malformed annotation — skip this pod, don't treat as orphan (P11-2).
		return true
	}

	for _, status := range statuses {
		if status.Default {
			continue
		}
		for _, ipStr := range status.IPs {
			podIP := net.ParseIP(ipStr)
			if podIP != nil && podIP.Equal(ip) {
				return true
			}
		}
	}

	return false
}

var _ reconcile.Reconciler = &IPPoolReconciler{}
