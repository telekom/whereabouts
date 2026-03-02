// Copyright 2025 Deutsche Telekom
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"encoding/json"
	"fmt"
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
)

// IPPoolReconciler reconciles IPPool resources by removing allocations whose
// pods no longer exist. It replaces the legacy CronJob-based reconciler and
// the DaemonSet pod controller.
type IPPoolReconciler struct {
	client            client.Client
	reconcileInterval time.Duration
}

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
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("getting IPPool: %s", err)
	}

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
		logger.Info("cleaned up orphaned allocations",
			"pool", pool.Name, "count", len(orphanedKeys))

		// Also clean up any corresponding OverlappingRangeIPReservation CRDs.
		if err := r.cleanupOverlappingReservations(ctx, &pool, orphanedKeys); err != nil {
			logger.Error(err, "failed to clean up some overlapping reservations, will retry")
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}
	}

	// Requeue sooner if pending pods exist.
	if hasPending {
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

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

	for _, key := range keys {
		ip := allocationKeyToIP(pool, key)
		if ip == nil {
			continue
		}

		// Try to find matching overlapping reservation by listing with podRef.
		var reservations whereaboutsv1alpha1.OverlappingRangeIPReservationList
		if err := r.client.List(ctx, &reservations, client.InNamespace(pool.Namespace)); err != nil {
			logger.V(1).Info("failed to list overlapping reservations", "error", err)
			lastErr = err
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
				} else {
					logger.V(1).Info("deleted overlapping reservation", "name", res.Name)
				}
			}
		}
	}

	return lastErr
}

// allocationKeyToIP converts an allocation map key (decimal offset) to an IP
// address using the pool's CIDR range.
func allocationKeyToIP(pool *whereaboutsv1alpha1.IPPool, key string) net.IP {
	_, ipNet, err := net.ParseCIDR(pool.Spec.Range)
	if err != nil {
		return nil
	}

	// Parse offset.
	var offset int64
	if _, err := fmt.Sscanf(key, "%d", &offset); err != nil {
		return nil
	}

	return addOffsetToIP(ipNet.IP, offset)
}

// addOffsetToIP adds an integer offset to a base IP address.
func addOffsetToIP(ip net.IP, offset int64) net.IP {
	// Normalize to 16-byte representation.
	ip = ip.To16()
	if ip == nil {
		return nil
	}

	result := make(net.IP, len(ip))
	copy(result, ip)

	carry := offset
	for i := len(result) - 1; i >= 0 && carry != 0; i-- {
		sum := int64(result[i]) + carry
		result[i] = byte(sum & 0xff)
		carry = sum >> 8
	}

	return result
}

// denormalizeIPName converts a normalized IP name (dashes for colons) back to
// a net.IP. Handles optional network-name prefix.
func denormalizeIPName(name string) net.IP {
	// Names may have a network-name prefix: "mynet-10.0.0.5" or just "10.0.0.5"
	// For IPv6, colons are replaced with dashes: "fd00--1" for "fd00::1"
	// Try parsing as-is first (IPv4 with dots preserved).
	if ip := net.ParseIP(name); ip != nil {
		return ip
	}

	// Try replacing dashes with colons (IPv6 normalization).
	denormalized := strings.ReplaceAll(name, "-", ":")
	if ip := net.ParseIP(denormalized); ip != nil {
		return ip
	}

	// Try removing network-name prefix (everything before first segment that
	// looks like an IP).
	parts := strings.SplitN(name, "-", 2)
	if len(parts) == 2 {
		return denormalizeIPName(parts[1])
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
