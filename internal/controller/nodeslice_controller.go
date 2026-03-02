// Copyright 2025 Deutsche Telekom
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	nadv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	whereaboutsv1alpha1 "github.com/telekom/whereabouts/pkg/api/whereabouts.cni.cncf.io/v1alpha1"
	"github.com/telekom/whereabouts/pkg/iphelpers"
)

const (
	// fieldOwnerNodeSlice is the SSA field manager for node slice status updates.
	fieldOwnerNodeSlice = "whereabouts-nodeslice"
)

// NodeSliceReconciler reconciles NetworkAttachmentDefinition resources by
// managing the corresponding NodeSlicePool CRDs. It assigns IP range slices
// to nodes and ensures node join/leave events are reflected in the allocations.
type NodeSliceReconciler struct {
	client client.Client
}

// SetupNodeSliceReconciler creates and registers the NodeSliceReconciler with
// the manager.
func SetupNodeSliceReconciler(mgr ctrl.Manager) error {
	r := &NodeSliceReconciler{
		client: mgr.GetClient(),
	}

	return ctrl.NewControllerManagedBy(mgr).
		// Primary: watch NADs — each NAD reconciliation manages its NodeSlicePool.
		For(&nadv1.NetworkAttachmentDefinition{}).
		// Secondary: when a Node is added/deleted, re-reconcile all NADs.
		WatchesRawSource(source.Kind(mgr.GetCache(), &corev1.Node{},
			handler.TypedEnqueueRequestsFromMapFunc(r.mapNodeToNADs),
		)).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 1,
		}).
		Named("nodeslice").
		Complete(r)
}

//+kubebuilder:rbac:groups=k8s.cni.cncf.io,resources=network-attachment-definitions,verbs=get;list;watch
//+kubebuilder:rbac:groups=whereabouts.cni.cncf.io,resources=nodeslicepools,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=whereabouts.cni.cncf.io,resources=nodeslicepools/status,verbs=get;update;patch
//+kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch

// Reconcile processes a NAD and manages the corresponding NodeSlicePool.
func (r *NodeSliceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the NAD.
	var nad nadv1.NetworkAttachmentDefinition
	if err := r.client.Get(ctx, req.NamespacedName, &nad); err != nil {
		if errors.IsNotFound(err) {
			// NAD deleted — NodeSlicePool will be garbage collected via OwnerReference.
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("getting NAD: %s", err)
	}

	// Parse IPAM configuration from the NAD.
	ipamConf, err := parseNADIPAMConfig(nad.Spec.Config)
	if err != nil {
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
	poolName := ipamConf.NetworkName
	if poolName == "" {
		poolName = ipamConf.Name
	}

	subnets, err := iphelpers.DivideRangeBySize(ipamConf.Range, ipamConf.NodeSliceSize)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("dividing range by size: %s", err)
	}

	// List all nodes.
	var nodeList corev1.NodeList
	if err := r.client.List(ctx, &nodeList); err != nil {
		return ctrl.Result{}, fmt.Errorf("listing nodes: %s", err)
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
		if !errors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("getting NodeSlicePool: %s", err)
		}
		exists = false
	}

	if !exists {
		return r.createPool(ctx, &nad, poolName, ipamConf.Range, ipamConf.NodeSliceSize, subnets, nodes)
	}

	// Check if spec changed (range or sliceSize).
	specChanged := pool.Spec.Range != ipamConf.Range || pool.Spec.SliceSize != ipamConf.NodeSliceSize

	// Ensure this NAD is an OwnerReference.
	r.ensureOwnerRef(ctx, &pool, &nad)

	if specChanged {
		return r.updatePoolSpec(ctx, &pool, ipamConf.Range, ipamConf.NodeSliceSize, subnets, nodes)
	}

	// Spec unchanged — just ensure node assignments are current.
	return r.ensureNodeAssignments(ctx, &pool, nodes)
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
		return ctrl.Result{}, fmt.Errorf("creating NodeSlicePool: %s", err)
	}

	// Update status separately (subresource).
	pool.Status.Allocations = allocations
	if err := r.client.Status().Update(ctx, pool); err != nil {
		return ctrl.Result{}, fmt.Errorf("updating NodeSlicePool status: %s", err)
	}

	logger.Info("created NodeSlicePool", "name", name, "range", rangeStr,
		"sliceSize", sliceSize, "nodes", len(nodes))
	return ctrl.Result{}, nil
}

// updatePoolSpec updates the spec and recomputes all allocations.
func (r *NodeSliceReconciler) updatePoolSpec(ctx context.Context, pool *whereaboutsv1alpha1.NodeSlicePool, rangeStr, sliceSize string, subnets, nodes []string) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Update spec.
	patch := client.MergeFrom(pool.DeepCopy())
	pool.Spec.Range = rangeStr
	pool.Spec.SliceSize = sliceSize
	if err := r.client.Patch(ctx, pool, patch); err != nil {
		return ctrl.Result{}, fmt.Errorf("patching NodeSlicePool spec: %s", err)
	}

	// Recompute allocations.
	allocations := makeAllocations(subnets, nodes)
	pool.Status.Allocations = allocations
	if err := r.client.Status().Update(ctx, pool); err != nil {
		return ctrl.Result{}, fmt.Errorf("updating NodeSlicePool status: %s", err)
	}

	logger.Info("updated NodeSlicePool spec and re-sliced", "name", pool.Name,
		"range", rangeStr, "sliceSize", sliceSize)
	return ctrl.Result{}, nil
}

// ensureNodeAssignments checks that all current nodes have slice assignments
// and removes assignments for deleted nodes.
func (r *NodeSliceReconciler) ensureNodeAssignments(ctx context.Context, pool *whereaboutsv1alpha1.NodeSlicePool, nodes []string) (ctrl.Result, error) {
	nodeSet := make(map[string]struct{}, len(nodes))
	for _, n := range nodes {
		nodeSet[n] = struct{}{}
	}

	allocations := pool.Status.Allocations
	changed := false

	// Remove assignments for nodes that no longer exist.
	for i := range allocations {
		if allocations[i].NodeName != "" {
			if _, ok := nodeSet[allocations[i].NodeName]; !ok {
				allocations[i].NodeName = ""
				changed = true
			}
		}
	}

	// Assign unassigned nodes to empty slots.
	assignedNodes := make(map[string]struct{})
	for _, a := range allocations {
		if a.NodeName != "" {
			assignedNodes[a.NodeName] = struct{}{}
		}
	}
	for _, nodeName := range nodes {
		if _, assigned := assignedNodes[nodeName]; !assigned {
			for i := range allocations {
				if allocations[i].NodeName == "" {
					allocations[i].NodeName = nodeName
					assignedNodes[nodeName] = struct{}{}
					changed = true
					break
				}
			}
			// No slot available — pool is full. TODO: fire an event.
		}
	}

	if changed {
		pool.Status.Allocations = allocations
		if err := r.client.Status().Update(ctx, pool); err != nil {
			return ctrl.Result{}, fmt.Errorf("updating NodeSlicePool status: %s", err)
		}
	}

	return ctrl.Result{}, nil
}

// ensureOwnerRef adds a non-controller OwnerReference for multui-NAD scenarios.
func (r *NodeSliceReconciler) ensureOwnerRef(ctx context.Context, pool *whereaboutsv1alpha1.NodeSlicePool, nad *nadv1.NetworkAttachmentDefinition) {
	for _, ref := range pool.OwnerReferences {
		if ref.UID == nad.UID {
			return // Already has this OwnerReference.
		}
	}

	patch := client.MergeFrom(pool.DeepCopy())
	pool.OwnerReferences = append(pool.OwnerReferences, metav1.OwnerReference{
		APIVersion: nad.APIVersion,
		Kind:       nad.Kind,
		Name:       nad.Name,
		UID:        nad.UID,
	})
	_ = r.client.Patch(ctx, pool, patch)
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

// checkMultiNADMismatch verifies that all NADs sharing the same network_name
// have consistent range and node_slice_size configurations.
func (r *NodeSliceReconciler) checkMultiNADMismatch(ctx context.Context, nad *nadv1.NetworkAttachmentDefinition, conf *nadIPAMConfig) error {
	var nadList nadv1.NetworkAttachmentDefinitionList
	if err := r.client.List(ctx, &nadList); err != nil {
		return fmt.Errorf("listing NADs for mismatch check: %s", err)
	}

	for i := range nadList.Items {
		other := &nadList.Items[i]
		if other.UID == nad.UID {
			continue
		}

		otherConf, err := parseNADIPAMConfig(other.Spec.Config)
		if err != nil || otherConf.NodeSliceSize == "" {
			continue
		}

		// Only compare NADs that share the same network name.
		thisName := conf.NetworkName
		if thisName == "" {
			thisName = conf.Name
		}
		otherName := otherConf.NetworkName
		if otherName == "" {
			otherName = otherConf.Name
		}
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
		return nil, fmt.Errorf("empty spec.config")
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
	if err := json.Unmarshal([]byte(specConfig), &singlePlugin); err == nil && singlePlugin.IPAM.Type == "whereabouts" {
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
	if err := json.Unmarshal([]byte(specConfig), &confList); err == nil {
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

	return nil, fmt.Errorf("no whereabouts IPAM config found")
}

// ipRangeFromConfig extracts the first IP range from the config, handling
// both single range and ipRanges formats.
func ipRangeFromConfig(conf *nadIPAMConfig) string {
	return strings.TrimSpace(conf.Range)
}

var _ reconcile.Reconciler = &NodeSliceReconciler{}
