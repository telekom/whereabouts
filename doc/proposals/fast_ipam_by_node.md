# Whereabouts support for fast IPAM by using preallocated node slices

# Table of contents

- [Introduction](#introduction)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Design](#design)
  - [Changes in IPAM Config](#changes-in-ipam-config)
  - [Changes in Modules](#changes-in-modules)
  - [Backward compatibility](#backward-compatibility)
- [Alternative Design](#alternative-design)
- [Summary](#summary)
- [Discussions and Decisions](#discussions-and-decisions)

<hr>

## Introduction

Whereabouts originally used a single lease per cluster named "whereabouts" for locking for all allocation and deallocation
of IPs across the entire cluster running whereabouts. This causes issues with performance and reliability at scale. 
Even at 256 nodes, if you have 10 network-attachment-definitions per pod and run a pod on each node there will be so much lease contention that kubelet times
out before whereabouts can assign all 10 IPs per pod. This would only get worse at higher scale and whereabouts should be able to support
10+ network-attachment-definition per pod at the kubernetes supported scale of 5,000 nodes.

## Status

Fast IPAM is implemented as an experimental mode. It is enabled by setting
`node_slice_size` in a top-level IPAM `range`; there is no separate
`fast_ipam` flag. The current implementation rejects `node_slice_size` when it
is combined with `ipRanges`, so multi-range and dual-stack configurations
should continue to use standard IPAM.

### Goals

- Support existing whereabouts functionality without breaking changes
- Introduce a new mode that can be configured on NAD to use IPAM by node slices
- Support multiple NADs on same network range

### Non-Goals

- Migrate all users to this new mode

<hr>

### Implementation Phases

## Phases

In order to make iterative improvements and work towards this feature in pieces we have laid out the implementation in phases.

Initial phase:
[x] Fast ranges base functionality

Feature parity phase:
[ ] Range start, range-end function
[ ] Live changes to range (from regular to fast)
[ ] Multiple ranges / dual-stack Fast IPAM

Optimization phase:
[ ] Dynamic range slice rebalancing

## Design

The IPAM configuration format includes `node_slice_size` to enable and configure
this feature.

Fast IPAM uses the `NodeSlicePool` CRD to manage the slices of the network
ranges that nodes are assigned to. A controller-runtime reconciler reconciles
these NodeSlicePools based on cluster Nodes and NetworkAttachmentDefinitions.

Whereabouts enables this feature when `node_slice_size` is present. When
creating `IPPools`, it checks the `NodeSlicePool` to get the range for the
current node. It will set this on existing IPPool objects and use a lease per
IPPool. There will be an `IPPool` and `Lease` per network per node.
Where a network is defined by network name i.e. you can have multiple `network-attachment-definitions` with a shared network name and this will result in
a shared `NodeSlicePool`, `IPPool` and `Lease` per node for these `network-attachment-definitions`.

i.e. we have nad1 and nad2 both with network name `test-network`. When a node, `trusted-otter` joins the cluster this will result in
`NodeSlicePool` named `test-network` and per-node `IPPool` / `Lease` objects
named with the network name, node name, and normalized slice range, for example
`test-network-trusted-otter-192-168-0-0-24`. If these are separate networks you
would not set the network name or would set `network_name` differently per
`NetworkAttachmentDefinition`.

![node-slice-diagram](images/fast_ipam_by_node.png)

### Changes in IPAM Config

The implemented IPAM config uses `node_slice_size` as the feature switch:

<table>
<tr>
<th>Old IPAM Config</th>
<th>Changes</th>
<th>New IPAM Config</th>
</tr>
<tr>
<td>
  
```json
{
      "cniVersion": "0.3.0",
      "name": "whereaboutsexample",
      "type": "macvlan",
      "master": "eth0",
      "mode": "bridge",
      "ipam": {
        "type": "whereabouts",
        "range": "192.168.2.225/8"
      }
}
```
  
</td>
<td>

```diff
{
      "cniVersion": "0.3.0",
      "name": "whereaboutsexample",
      "type": "macvlan",
      "master": "eth0",
      "mode": "bridge",
      "ipam": {
        "type": "whereabouts",
        "range": "192.168.2.225/8"
+       "node_slice_size": "/22"
      }
}
```

</td>
<td>

```json
{
  "cniVersion": "0.3.0",
  "name": "whereaboutsexample",
  "type": "macvlan",
  "master": "eth0",
  "mode": "bridge",
  "ipam": {
    "type": "whereabouts",
    "range": "192.168.2.225/8",
    "node_slice_size": "/22"
  }
}
```

</td>
</tr>
</table>

### Changes in Modules

#### whereabouts/pkg/types/types.go

The current `IPAMConfig` includes:

```go
// NodeSliceSize sets the prefix length (e.g. "/28" or "28") for per-node
// IP slices. Enables the experimental Fast IPAM feature when non-empty.
NodeSliceSize string `json:"node_slice_size"`
```

```go
type PoolIdentifier struct {
	IPRange     string
	NetworkName string
	NodeName    string
}

func IPPoolName(poolIdentifier PoolIdentifier) string {
	if poolIdentifier.NodeName != "" {
		// fast node range naming convention
		if poolIdentifier.NetworkName == UnnamedNetwork {
			return fmt.Sprintf("%v-%v", poolIdentifier.NodeName, normalizeRange(poolIdentifier.IPRange))
		}
		return fmt.Sprintf("%v-%v-%v", poolIdentifier.NetworkName, poolIdentifier.NodeName, normalizeRange(poolIdentifier.IPRange))
	}

	// default naming convention
	if poolIdentifier.NetworkName == UnnamedNetwork {
		return normalizeRange(poolIdentifier.IPRange)
	}
	return fmt.Sprintf("%s-%s", poolIdentifier.NetworkName, normalizeRange(poolIdentifier.IPRange))
}
```

Whereabouts now uses the NodeSlicePool to find the slice range assigned to the
current node. `EffectivePoolIdentifier` then switches the IPPool and Lease name
to the node-specific slice identifier before allocation.

### NodeSlicePool CRD

```diff
// NodeSlicePoolSpec defines the desired state of NodeSlicePool
type NodeSlicePoolSpec struct {
	// Range is a RFC 4632/4291-style string that represents an IP address and prefix length in CIDR notation
	// this refers to the entire range where the node is allocated a subset
	Range string `json:"range"`

	SliceSize string `json:"sliceSize"`
}

// NodeSlicePoolStatus defines the desired state of NodeSlicePool
type NodeSlicePoolStatus struct {
	Allocations []NodeSliceAllocation `json:"allocations,omitempty"`
	TotalSlices int32 `json:"totalSlices,omitempty"`
	AssignedSlices int32 `json:"assignedSlices,omitempty"`
	FreeSlices int32 `json:"freeSlices,omitempty"`
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

type NodeSliceAllocation struct {
	NodeName   string `json:"nodeName"`
	SliceRange string `json:"sliceRange"`
}

// ParseCIDR formats the Range of the IPPool
func (i NodeSlicePool) ParseCIDR() (net.IP, *net.IPNet, error) {
	return net.ParseCIDR(i.Spec.Range)
}

// +genclient
// +kubebuilder:object:root=true

// NodeSlicePool is the Schema for the nodesliceippools API
type NodeSlicePool struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NodeSlicePoolSpec   `json:"spec,omitempty"`
	Status NodeSlicePoolStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// NodeSlicePoolList contains a list of NodeSlicePool
type NodeSlicePoolList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NodeSlicePool `json:"items"`
}
```

### Backward Compatibility

This feature only changes Whereabouts behavior when `node_slice_size` is set.
Otherwise Whereabouts works the same for IPAM configs without
`node_slice_size` defined.

`node_slice_size` currently requires a single top-level `range`. It cannot be
combined with `ipRanges`.

## Alternative Design

Another design is that the whereabouts daemonset that runs the install-cni script could be used to have a startup and 
shutdown hook which would handle the assignment of nodes to a node slice. This would require locking for the `NodeSlicePools`
on Node join. The reason to use the controller over this design is because the reconciliation pattern reduces the likelyhood for bugs (like leaked IPs) 
and because it will run as a singleton so it does not need to lock as long as it only has 1 worker processing its workqueue. 


### Summary

The controller-based design is implemented in the Whereabouts operator. The
operator reconciles NodeSlicePools and the CNI binary consumes the assigned
slice for allocations on the current node.

### Discussions and Decisions

TBD
