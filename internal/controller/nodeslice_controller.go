// Copyright 2025 Deutsche Telekom
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	nadv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	whereaboutsv1alpha1 "github.com/telekom/whereabouts/api/whereabouts.cni.cncf.io/v1alpha1"
	"github.com/telekom/whereabouts/pkg/iphelpers"
)

const staleIPPoolRequeueInterval = 30 * time.Second

var errNoWhereaboutsIPAMConfig = errors.New("no whereabouts IPAM config found")

// NodeSliceReconciler reconciles NetworkAttachmentDefinition resources by
// managing the corresponding NodeSlicePool CRDs. It assigns IP range slices
// to nodes and ensures node join/leave events are reflected in the allocations.
type NodeSliceReconciler struct {
	client   client.Client
	recorder events.EventRecorder
}

// SetupNodeSliceReconciler creates and registers the NodeSliceReconciler with
// the manager.
func SetupNodeSliceReconciler(mgr ctrl.Manager) error {
	r := &NodeSliceReconciler{
		client:   mgr.GetClient(),
		recorder: mgr.GetEventRecorder("nodeslice-controller"),
	}

	return ctrl.NewControllerManagedBy(mgr).
		// Primary: watch NADs — each NAD reconciliation manages its NodeSlicePool.
		For(&nadv1.NetworkAttachmentDefinition{}).
		// Secondary: when a Node is added/deleted, re-reconcile all NADs.
		WatchesRawSource(source.Kind(mgr.GetCache(), &corev1.Node{},
			handler.TypedEnqueueRequestsFromMapFunc(r.mapNodeToNADs),
			predicate.TypedFuncs[*corev1.Node]{
				UpdateFunc: func(_ event.TypedUpdateEvent[*corev1.Node]) bool {
					return false
				},
			},
		)).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 1,
		}).
		Named("nodeslice").
		WatchesRawSource(source.Kind(mgr.GetCache(), &whereaboutsv1alpha1.NodeSlicePool{},
			handler.TypedEnqueueRequestsFromMapFunc(func(_ context.Context, obj *whereaboutsv1alpha1.NodeSlicePool) []reconcile.Request {
				if !obj.DeletionTimestamp.IsZero() {
					deleteNodeSliceMetrics(obj.Name)
				}
				return nil
			}),
			predicate.TypedFuncs[*whereaboutsv1alpha1.NodeSlicePool]{
				DeleteFunc: func(e event.TypedDeleteEvent[*whereaboutsv1alpha1.NodeSlicePool]) bool {
					deleteNodeSliceMetrics(e.Object.GetName())
					return false
				},
			},
		)).
		Complete(r)
}

//+kubebuilder:rbac:groups=k8s.cni.cncf.io,resources=network-attachment-definitions,verbs=get;list;watch
//+kubebuilder:rbac:groups=whereabouts.cni.cncf.io,resources=ippools,verbs=get;delete
//+kubebuilder:rbac:groups=whereabouts.cni.cncf.io,resources=nodeslicepools,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=whereabouts.cni.cncf.io,resources=nodeslicepools/status,verbs=get;update;patch
//+kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch

// Reconcile processes a NAD and manages the corresponding NodeSlicePool.
func (r *NodeSliceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.V(1).Info("reconciling NAD for NodeSlicePool", "name", req.Name, "namespace", req.Namespace)

	// Fetch the NAD.
	var nad nadv1.NetworkAttachmentDefinition
	if err := r.client.Get(ctx, req.NamespacedName, &nad); err != nil {
		if apierrors.IsNotFound(err) {
			// NAD deleted — NodeSlicePool will be garbage collected via OwnerReference.
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("getting NAD: %w", err)
	}

	// Parse IPAM configuration from the NAD.
	ipamConf, err := parseNADIPAMConfig(nad.Spec.Config)
	if err != nil {
		if !errors.Is(err, errNoWhereaboutsIPAMConfig) {
			return ctrl.Result{}, fmt.Errorf("parsing NAD IPAM config: %w", err)
		}
		logger.V(1).Info("NAD has no whereabouts IPAM config, skipping", "error", err)
		return ctrl.Result{}, nil
	}

	// Skip NADs without node_slice_size.
	if ipamConf.NodeSliceSize == "" || ipamConf.Range == "" {
		return ctrl.Result{}, nil
	}

	// Check for multi-NAD config mismatch.
	if err := r.checkMultiNADMismatch(ctx, &nad, ipamConf); err != nil {
		return ctrl.Result{}, err
	}

	// Compute pool name and slices.
	poolName := nodeSlicePoolName(ipamConf)

	subnets, err := iphelpers.DivideRangeBySizeWithLimit(ipamConf.Range, ipamConf.NodeSliceSize, iphelpers.MaxNodeSliceSubnets)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("dividing range by size: %w", err)
	}

	// List all nodes.
	var nodeList corev1.NodeList
	if err := r.client.List(ctx, &nodeList); err != nil {
		return ctrl.Result{}, fmt.Errorf("listing nodes: %w", err)
	}
	nodes := make([]string, 0, len(nodeList.Items))
	for i := range nodeList.Items {
		nodes = append(nodes, nodeList.Items[i].Name)
	}
	sort.Strings(nodes)

	// Get or create the NodeSlicePool.
	poolKey := types.NamespacedName{Namespace: nad.Namespace, Name: poolName}
	var pool whereaboutsv1alpha1.NodeSlicePool
	exists := true
	if err := r.client.Get(ctx, poolKey, &pool); err != nil {
		if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("getting NodeSlicePool: %w", err)
		}
		exists = false
	}

	if err := r.cleanupStaleOwnedPools(ctx, &nad, poolName); err != nil {
		return ctrl.Result{}, fmt.Errorf("cleaning stale NodeSlicePools: %w", err)
	}

	if !exists {
		return r.createPool(ctx, &nad, poolName, ipamConf.Range, ipamConf.NodeSliceSize, subnets, nodes)
	}

	// Check if spec changed (range or sliceSize).
	specChanged := pool.Spec.Range != ipamConf.Range || pool.Spec.SliceSize != ipamConf.NodeSliceSize

	// Ensure this NAD is an OwnerReference.
	if err := r.ensureOwnerRef(ctx, &pool, &nad); err != nil {
		return ctrl.Result{}, fmt.Errorf("ensuring OwnerReference: %w", err)
	}

	if specChanged {
		return r.updatePoolSpec(ctx, &pool, ipamConf.Range, ipamConf.NodeSliceSize, subnets, nodes)
	}

	if allocationsNeedRebuild(pool.Status.Allocations, subnets) {
		if err := r.rebuildPoolStatus(ctx, &pool, subnets, nodes); err != nil {
			return ctrl.Result{}, err
		}
		return r.ensureNodeAssignments(ctx, &pool, nodes, ipamConf.NetworkName)
	}

	// Spec unchanged — just ensure node assignments are current.
	return r.ensureNodeAssignments(ctx, &pool, nodes, ipamConf.NetworkName)
}

// computeSliceStats populates the NodeSlicePool's status with computed
// slice counts derived from the allocations array.
func computeSliceStats(pool *whereaboutsv1alpha1.NodeSlicePool) {
	total := int32(len(pool.Status.Allocations))
	var assigned int32
	for _, a := range pool.Status.Allocations {
		if a.NodeName != "" {
			assigned++
		}
	}
	pool.Status.TotalSlices = total
	pool.Status.AssignedSlices = assigned
	pool.Status.FreeSlices = total - assigned
}

// createPool creates a new NodeSlicePool with initial allocations.
func (r *NodeSliceReconciler) createPool(ctx context.Context, nad *nadv1.NetworkAttachmentDefinition, name, rangeStr, sliceSize string, subnets, nodes []string) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	allocations := makeAllocations(subnets, nodes)

	pool := &whereaboutsv1alpha1.NodeSlicePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: nad.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(nad, nadv1.SchemeGroupVersion.WithKind("NetworkAttachmentDefinition")),
			},
		},
		Spec: whereaboutsv1alpha1.NodeSlicePoolSpec{
			Range:     rangeStr,
			SliceSize: sliceSize,
		},
		Status: whereaboutsv1alpha1.NodeSlicePoolStatus{
			Allocations: allocations,
		},
	}

	if err := r.client.Create(ctx, pool); err != nil {
		return ctrl.Result{}, fmt.Errorf("creating NodeSlicePool: %w", err)
	}

	// Snapshot after Create so we have the server-set fields, then patch status.
	patchHelper, err := NewPatchHelper(pool, r.client)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("creating patch helper: %w", err)
	}
	pool.Status.Allocations = allocations
	computeSliceStats(pool)
	markReady(pool, ReasonPoolCreated, fmt.Sprintf("created with range %s, slice size %s, %d node(s)", rangeStr, sliceSize, len(nodes)))
	if err := patchHelper.Patch(ctx, pool); err != nil {
		return ctrl.Result{}, fmt.Errorf("patching NodeSlicePool status: %w", err)
	}

	logger.Info("created NodeSlicePool", "name", name, "range", rangeStr,
		"sliceSize", sliceSize, "nodes", len(nodes))
	r.recorder.Eventf(pool, nil, corev1.EventTypeNormal, "Created", "Reconcile",
		"created NodeSlicePool with range %s, slice size %s, %d node(s)", rangeStr, sliceSize, len(nodes))
	recordNodeSliceMetrics(name, allocations)
	return ctrl.Result{}, nil
}

// updatePoolSpec updates the spec and recomputes all allocations.
func (r *NodeSliceReconciler) updatePoolSpec(ctx context.Context, pool *whereaboutsv1alpha1.NodeSlicePool, rangeStr, sliceSize string, subnets, nodes []string) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Snapshot before any changes — PatchHelper will handle both spec and status.
	patchHelper, err := NewPatchHelper(pool, r.client)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("creating patch helper: %w", err)
	}

	// Update spec.
	pool.Spec.Range = rangeStr
	pool.Spec.SliceSize = sliceSize

	// Recompute allocations.
	allocations := makeAllocations(subnets, nodes)
	pool.Status.Allocations = allocations
	computeSliceStats(pool)
	markReady(pool, ReasonPoolUpdated, fmt.Sprintf("updated range to %s, slice size %s", rangeStr, sliceSize))

	if err := patchHelper.Patch(ctx, pool); err != nil {
		return ctrl.Result{}, fmt.Errorf("patching NodeSlicePool: %w", err)
	}

	logger.Info("updated NodeSlicePool spec and re-sliced", "name", pool.Name,
		"range", rangeStr, "sliceSize", sliceSize)
	r.recorder.Eventf(pool, nil, corev1.EventTypeNormal, "SpecUpdated", "Reconcile",
		"updated range to %s, slice size to %s, re-sliced allocations", rangeStr, sliceSize)
	recordNodeSliceMetrics(pool.Name, allocations)
	return ctrl.Result{}, nil
}

// rebuildPoolStatus restores missing or stale status allocations when a prior
// create/update persisted spec but failed before the status patch completed.
func (r *NodeSliceReconciler) rebuildPoolStatus(ctx context.Context, pool *whereaboutsv1alpha1.NodeSlicePool, subnets, nodes []string) error {
	logger := log.FromContext(ctx)
	patchHelper, err := NewPatchHelper(pool, r.client)
	if err != nil {
		return fmt.Errorf("creating patch helper: %w", err)
	}

	allocations := repairAllocations(pool.Status.Allocations, subnets, nodes)
	pool.Status.Allocations = allocations
	computeSliceStats(pool)
	markReady(pool, ReasonReconciled, "rebuilt missing or stale node slice status")
	if err := patchHelper.Patch(ctx, pool); err != nil {
		return fmt.Errorf("patching rebuilt NodeSlicePool status: %w", err)
	}

	logger.Info("rebuilt NodeSlicePool status", "name", pool.Name, "slices", len(allocations), "nodes", len(nodes))
	r.recorder.Eventf(pool, nil, corev1.EventTypeNormal, "StatusRebuilt", "Reconcile",
		"rebuilt NodeSlicePool status with %d slice(s) and %d node(s)", len(allocations), len(nodes))
	recordNodeSliceMetrics(pool.Name, allocations)
	return nil
}

// ensureNodeAssignments checks that all current nodes have slice assignments
// and removes assignments for deleted nodes.
func (r *NodeSliceReconciler) ensureNodeAssignments(ctx context.Context, pool *whereaboutsv1alpha1.NodeSlicePool, nodes []string, networkName string) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Snapshot before mutations.
	patchHelper, err := NewPatchHelper(pool, r.client)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("creating patch helper: %w", err)
	}

	nodeSet := make(map[string]struct{}, len(nodes))
	for _, n := range nodes {
		nodeSet[n] = struct{}{}
	}

	allocations := pool.Status.Allocations

	// Remove assignments for nodes that no longer exist.
	staleIPPoolBlocking := false
	for i := range allocations {
		if allocations[i].NodeName != "" {
			if _, ok := nodeSet[allocations[i].NodeName]; !ok {
				released, err := r.releaseDeletedNodeSlice(ctx, pool.Namespace, networkName, allocations[i])
				if err != nil {
					return ctrl.Result{}, err
				}
				if !released {
					staleIPPoolBlocking = true
					continue
				}
				allocations[i].NodeName = ""
			}
		}
	}

	// Assign unassigned nodes to empty slots.
	assignedNodes := make(map[string]struct{})
	freeSlots := make([]int, 0)
	for i := range allocations {
		if allocations[i].NodeName != "" {
			assignedNodes[allocations[i].NodeName] = struct{}{}
		} else {
			freeSlots = append(freeSlots, i)
		}
	}
	nextFreeSlot := 0
	poolFullThisReconcile := false
	for _, nodeName := range nodes {
		if _, assigned := assignedNodes[nodeName]; !assigned {
			if nextFreeSlot < len(freeSlots) {
				allocations[freeSlots[nextFreeSlot]].NodeName = nodeName
				assignedNodes[nodeName] = struct{}{}
				nextFreeSlot++
			} else {
				if staleIPPoolBlocking {
					logger.Info("no available slot for node while stale per-node IPPool cleanup is pending",
						"pool", pool.Name, "node", nodeName)
					continue
				}
				// No slot available — pool is full.
				logger.Info("no available slot for node, pool is full",
					"pool", pool.Name, "node", nodeName)
				r.recorder.Eventf(pool, nil, corev1.EventTypeWarning, "PoolFull", "Reconcile",
					"no available IP slice for node %s — pool is full", nodeName)
				markStalled(pool, ReasonPoolFull,
					fmt.Sprintf("no available IP slice for node %s", nodeName))
				poolFullThisReconcile = true
			}
		}
	}

	pool.Status.Allocations = allocations
	computeSliceStats(pool)
	if staleIPPoolBlocking {
		markReconciling(pool, "waiting for stale per-node IPPools to be cleaned up")
	} else if !poolFullThisReconcile {
		markReady(pool, ReasonReconciled, "all nodes assigned to slices")
	}

	// PatchHelper will no-op if nothing actually changed.
	if err := patchHelper.Patch(ctx, pool); err != nil {
		return ctrl.Result{}, fmt.Errorf("patching NodeSlicePool status: %w", err)
	}

	recordNodeSliceMetrics(pool.Name, pool.Status.Allocations)
	if staleIPPoolBlocking {
		return ctrl.Result{RequeueAfter: staleIPPoolRequeueInterval}, nil
	}
	return ctrl.Result{}, nil
}

func (r *NodeSliceReconciler) releaseDeletedNodeSlice(ctx context.Context, namespace, networkName string, allocation whereaboutsv1alpha1.NodeSliceAllocation) (bool, error) {
	poolName := nodeSliceIPPoolName(networkName, allocation.NodeName, allocation.SliceRange)
	var pool whereaboutsv1alpha1.IPPool
	if err := r.client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: poolName}, &pool); err != nil {
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		return false, fmt.Errorf("getting stale IPPool %s/%s: %w", namespace, poolName, err)
	}

	if len(pool.Spec.Allocations) > 0 {
		return false, nil
	}

	if err := client.IgnoreNotFound(r.client.Delete(ctx, &pool)); err != nil {
		return false, fmt.Errorf("deleting empty stale IPPool %s/%s: %w", namespace, poolName, err)
	}
	return true, nil
}

func allocationsNeedRebuild(allocations []whereaboutsv1alpha1.NodeSliceAllocation, subnets []string) bool {
	if len(allocations) != len(subnets) {
		return true
	}

	for i := range allocations {
		if allocations[i].SliceRange != subnets[i] {
			return true
		}
	}
	return false
}

func repairAllocations(allocations []whereaboutsv1alpha1.NodeSliceAllocation, subnets, nodes []string) []whereaboutsv1alpha1.NodeSliceAllocation {
	if allocationsMatchSubnetPrefix(allocations, subnets) {
		repaired := make([]whereaboutsv1alpha1.NodeSliceAllocation, len(subnets))
		for i, subnet := range subnets {
			repaired[i] = whereaboutsv1alpha1.NodeSliceAllocation{SliceRange: subnet}
		}
		copy(repaired, allocations)
		return repaired
	}

	return makeAllocations(subnets, nodes)
}

func allocationsMatchSubnetPrefix(allocations []whereaboutsv1alpha1.NodeSliceAllocation, subnets []string) bool {
	if len(allocations) > len(subnets) {
		return false
	}
	for i := range allocations {
		if allocations[i].SliceRange != subnets[i] {
			return false
		}
	}
	return true
}

// ensureOwnerRef adds a non-controller OwnerReference for multi-NAD scenarios.
func (r *NodeSliceReconciler) ensureOwnerRef(ctx context.Context, pool *whereaboutsv1alpha1.NodeSlicePool, nad *nadv1.NetworkAttachmentDefinition) error {
	for _, ref := range pool.OwnerReferences {
		if ref.UID == nad.UID {
			return nil // Already has this OwnerReference.
		}
	}

	patchHelper, err := NewPatchHelper(pool, r.client)
	if err != nil {
		return fmt.Errorf("creating patch helper: %w", err)
	}
	pool.OwnerReferences = append(pool.OwnerReferences, metav1.OwnerReference{
		APIVersion: nadv1.SchemeGroupVersion.String(),
		Kind:       "NetworkAttachmentDefinition",
		Name:       nad.Name,
		UID:        nad.UID,
	})
	if err := patchHelper.Patch(ctx, pool); err != nil {
		return fmt.Errorf("patching OwnerReference on NodeSlicePool %s for NAD %s: %w", pool.Name, nad.Name, err)
	}
	return nil
}

// mapNodeToNADs maps a Node event to reconciliation of all NADs.
func (r *NodeSliceReconciler) mapNodeToNADs(ctx context.Context, _ *corev1.Node) []reconcile.Request {
	var nadList nadv1.NetworkAttachmentDefinitionList
	if err := r.client.List(ctx, &nadList); err != nil {
		log.FromContext(ctx).Error(err, "failed to list NADs for node event mapping")
		return nil
	}

	requests := make([]reconcile.Request, 0, len(nadList.Items))
	for i := range nadList.Items {
		requests = append(requests, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: nadList.Items[i].Namespace,
				Name:      nadList.Items[i].Name,
			},
		})
	}
	return requests
}

// checkMultiNADMismatch verifies that NADs in the same namespace sharing the
// same NodeSlicePool have consistent range and node_slice_size configurations.
func (r *NodeSliceReconciler) checkMultiNADMismatch(ctx context.Context, nad *nadv1.NetworkAttachmentDefinition, conf *nadIPAMConfig) error {
	var nadList nadv1.NetworkAttachmentDefinitionList
	if err := r.client.List(ctx, &nadList, client.InNamespace(nad.Namespace)); err != nil {
		return fmt.Errorf("listing NADs for mismatch check: %w", err)
	}

	for i := range nadList.Items {
		other := &nadList.Items[i]
		if other.UID == nad.UID {
			continue
		}

		otherConf, err := parseNADIPAMConfig(other.Spec.Config)
		if errors.Is(err, errNoWhereaboutsIPAMConfig) || (err == nil && otherConf.NodeSliceSize == "") {
			continue
		}
		if err != nil {
			return fmt.Errorf("parsing NAD %s/%s IPAM config for mismatch check: %w", other.Namespace, other.Name, err)
		}

		// Only compare NADs that resolve to the same NodeSlicePool.
		thisName := nodeSlicePoolName(conf)
		otherName := nodeSlicePoolName(otherConf)
		if thisName != otherName {
			continue
		}

		if conf.Range != otherConf.Range || conf.NodeSliceSize != otherConf.NodeSliceSize {
			return fmt.Errorf("NAD %s/%s and %s/%s share network name %q but have mismatched range or node_slice_size",
				nad.Namespace, nad.Name, other.Namespace, other.Name, thisName)
		}
	}

	return nil
}

func (r *NodeSliceReconciler) cleanupStaleOwnedPools(ctx context.Context, nad *nadv1.NetworkAttachmentDefinition, desiredName string) error {
	var pools whereaboutsv1alpha1.NodeSlicePoolList
	if err := r.client.List(ctx, &pools, client.InNamespace(nad.Namespace)); err != nil {
		return err
	}

	for i := range pools.Items {
		pool := &pools.Items[i]
		if pool.Name == desiredName {
			continue
		}

		ownerIdx := ownerRefIndex(pool.OwnerReferences, nad.UID)
		if ownerIdx < 0 {
			continue
		}

		remainingOwners := append([]metav1.OwnerReference{}, pool.OwnerReferences[:ownerIdx]...)
		remainingOwners = append(remainingOwners, pool.OwnerReferences[ownerIdx+1:]...)
		if len(remainingOwners) == 0 {
			deleteNodeSliceMetrics(pool.Name)
			if err := client.IgnoreNotFound(r.client.Delete(ctx, pool)); err != nil {
				return err
			}
			continue
		}

		pool.OwnerReferences = remainingOwners
		if err := r.client.Update(ctx, pool); err != nil {
			return err
		}
	}
	return nil
}

func ownerRefIndex(refs []metav1.OwnerReference, uid types.UID) int {
	for i := range refs {
		if refs[i].UID == uid {
			return i
		}
	}
	return -1
}

func nodeSlicePoolName(conf *nadIPAMConfig) string {
	if conf.NetworkName != "" {
		return conf.NetworkName
	}
	if conf.Range != "" {
		return iphelpers.NormalizeRangeForResourceName(conf.Range)
	}
	return conf.Name
}

func nodeSliceIPPoolName(networkName, nodeName, ipRange string) string {
	normalizedRange := iphelpers.NormalizeRangeForResourceName(ipRange)
	if networkName == "" {
		return fmt.Sprintf("%s-%s", nodeName, normalizedRange)
	}
	return fmt.Sprintf("%s-%s-%s", networkName, nodeName, normalizedRange)
}

// makeAllocations creates allocation entries from subnets and assigns nodes.
func makeAllocations(subnets, nodes []string) []whereaboutsv1alpha1.NodeSliceAllocation {
	allocations := make([]whereaboutsv1alpha1.NodeSliceAllocation, len(subnets))
	for i, subnet := range subnets {
		allocations[i] = whereaboutsv1alpha1.NodeSliceAllocation{
			SliceRange: subnet,
		}
	}

	// Assign nodes in order.
	for i, nodeName := range nodes {
		if i >= len(allocations) {
			break // Pool full.
		}
		allocations[i].NodeName = nodeName
	}

	return allocations
}

// nadIPAMConfig is a minimal representation of the whereabouts IPAM config
// extracted from a NAD's spec.config JSON.
type nadIPAMConfig struct {
	Name          string `json:"name"`
	NetworkName   string `json:"network_name"`
	Range         string `json:"range"`
	NodeSliceSize string `json:"node_slice_size"`
}

// parseNADIPAMConfig extracts whereabouts IPAM configuration from a NAD's
// spec.config JSON. Returns an error if the config doesn't contain whereabouts
// IPAM configuration.
func parseNADIPAMConfig(specConfig string) (*nadIPAMConfig, error) {
	if specConfig == "" {
		return nil, errNoWhereaboutsIPAMConfig
	}

	// Try parsing as a single plugin config.
	var singlePlugin struct {
		Name string `json:"name"`
		IPAM struct {
			Type          string `json:"type"`
			Name          string `json:"name"`
			NetworkName   string `json:"network_name"`
			Range         string `json:"range"`
			NodeSliceSize string `json:"node_slice_size"`
		} `json:"ipam"`
	}
	if err := json.Unmarshal([]byte(specConfig), &singlePlugin); err != nil {
		return nil, fmt.Errorf("parsing NAD spec.config JSON: %w", err)
	}
	if singlePlugin.IPAM.Type == "whereabouts" {
		return &nadIPAMConfig{
			Name:          singlePlugin.Name,
			NetworkName:   singlePlugin.IPAM.NetworkName,
			Range:         singlePlugin.IPAM.Range,
			NodeSliceSize: singlePlugin.IPAM.NodeSliceSize,
		}, nil
	}

	// Try parsing as a plugin list (conflist).
	var confList struct {
		Name    string `json:"name"`
		Plugins []struct {
			IPAM struct {
				Type          string `json:"type"`
				NetworkName   string `json:"network_name"`
				Range         string `json:"range"`
				NodeSliceSize string `json:"node_slice_size"`
			} `json:"ipam"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal([]byte(specConfig), &confList); err != nil {
		return nil, fmt.Errorf("parsing NAD spec.config conflist JSON: %w", err)
	}
	if len(confList.Plugins) > 0 {
		for _, p := range confList.Plugins {
			if p.IPAM.Type == "whereabouts" {
				return &nadIPAMConfig{
					Name:          confList.Name,
					NetworkName:   p.IPAM.NetworkName,
					Range:         p.IPAM.Range,
					NodeSliceSize: p.IPAM.NodeSliceSize,
				}, nil
			}
		}
	}

	return nil, errNoWhereaboutsIPAMConfig
}

var _ reconcile.Reconciler = &NodeSliceReconciler{}
