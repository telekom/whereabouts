// This code is based on code from the following repository
// https://github.com/bcreane/k8sutils

package client

import (
	"context"
	"errors"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	kubeClient "github.com/telekom/whereabouts/pkg/storage/kubernetes"
)

func isIPPoolAllocationsEmpty(ctx context.Context, k8sIPAM *kubeClient.KubernetesIPAM, ipPoolCIDR string) wait.ConditionWithContextFunc {
	return func(context.Context) (bool, error) {
		ipPool, err := k8sIPAM.GetIPPool(ctx, kubeClient.PoolIdentifier{IPRange: ipPoolCIDR, NetworkName: kubeClient.UnnamedNetwork})
		if err != nil {
			// ErrPoolInitialized is a temporaryError returned when the pool
			// doesn't exist and is freshly created — treat as zero allocations.
			if errors.Is(err, kubeClient.ErrPoolInitialized) {
				return true, nil
			}
			return false, err
		}

		if len(ipPool.Allocations()) != 0 {
			return false, nil
		}

		return true, nil
	}
}

func isIPPoolAllocationsEmptyForNodeSlices(ctx context.Context, k8sIPAM *kubeClient.KubernetesIPAM, ipPoolCIDR string, clientInfo *ClientInfo) wait.ConditionWithContextFunc {
	return func(context.Context) (bool, error) {
		nodeSlicePools, err := clientInfo.WbClient.WhereaboutsV1alpha1().NodeSlicePools(k8sIPAM.Namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return false, err
		}

		for i := range nodeSlicePools.Items {
			nodeSlicePool := &nodeSlicePools.Items[i]
			if nodeSlicePool.Spec.Range != ipPoolCIDR {
				continue
			}
			if k8sIPAM.Config.NetworkName != kubeClient.UnnamedNetwork && nodeSlicePool.Name != k8sIPAM.Config.NetworkName {
				continue
			}

			for _, allocation := range nodeSlicePool.Status.Allocations {
				ipPool, err := k8sIPAM.GetExistingIPPool(ctx, kubeClient.PoolIdentifier{
					NodeName:    allocation.NodeName,
					IPRange:     allocation.SliceRange,
					NetworkName: k8sIPAM.Config.NetworkName,
				})
				if err != nil {
					if apierrors.IsNotFound(err) {
						continue
					}
					return false, err
				}

				if len(ipPool.Allocations()) != 0 {
					return false, nil
				}
			}
		}
		return true, nil
	}
}

// WaitForZeroIPPoolAllocations polls up to timeout seconds for IP pool allocations to be gone from the Kubernetes cluster.
// Returns an error if any IP pool allocations remain after time limit, or if GETing IP pools causes an error.
func WaitForZeroIPPoolAllocations(ctx context.Context, k8sIPAM *kubeClient.KubernetesIPAM, ipPoolCIDR string, timeout time.Duration) error {
	return wait.PollUntilContextTimeout(ctx, time.Second, timeout, true, isIPPoolAllocationsEmpty(ctx, k8sIPAM, ipPoolCIDR))
}

// WaitForZeroIPPoolAllocationsAcrossNodeSlices polls up to timeout seconds for IP pool allocations to be gone from the Kubernetes cluster.
// Returns an error if any IP pool allocations remain after time limit, or if GETing IP pools causes an error.
func WaitForZeroIPPoolAllocationsAcrossNodeSlices(ctx context.Context, k8sIPAM *kubeClient.KubernetesIPAM, ipPoolCIDR string, timeout time.Duration, clientInfo *ClientInfo) error {
	return wait.PollUntilContextTimeout(ctx, time.Second, timeout, true, isIPPoolAllocationsEmptyForNodeSlices(ctx, k8sIPAM, ipPoolCIDR, clientInfo))
}
