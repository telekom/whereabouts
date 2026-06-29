package poolconsistency

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"github.com/telekom/whereabouts/e2e/retrievers"
	"github.com/telekom/whereabouts/pkg/storage"
)

type NodeSliceChecker struct {
	ipPools []storage.IPPool
	podList []corev1.Pod
}

func NewNodeSliceConsistencyCheck(ipPools []storage.IPPool, podList []corev1.Pod) *NodeSliceChecker {
	return &NodeSliceChecker{
		ipPools: ipPools,
		podList: podList,
	}
}

func (pc *NodeSliceChecker) MissingIPs() []string {
	var mismatchedIPs []string
	for i := range pc.podList {
		pod := &pc.podList[i]
		podIPs, err := retrievers.SecondaryIfaceIPValue(pod, "net1")
		if err != nil {
			panic(fmt.Errorf("node-slice MissingIPs: read net1 IP of pod %s/%s: %w", pod.Namespace, pod.Name, err))
		}
		podIP := podIPs[len(podIPs)-1]

		var found bool
		for _, pool := range pc.ipPools {
			for _, allocation := range pool.Allocations() {
				reservedIP := allocation.IP.String()

				if reservedIP == podIP {
					found = true
					break
				}
			}
		}
		if !found {
			mismatchedIPs = append(mismatchedIPs, podIP)
		}
	}
	return mismatchedIPs
}

func (pc *NodeSliceChecker) StaleIPs() []string {
	var staleIPs []string
	for _, pool := range pc.ipPools {
		for _, allocation := range pool.Allocations() {
			reservedIP := allocation.IP.String()
			found := false
			for i := range pc.podList {
				pod := &pc.podList[i]
				podIPs, err := retrievers.SecondaryIfaceIPValue(pod, "net1")
				if err != nil {
					panic(fmt.Errorf("node-slice StaleIPs: read net1 IP of pod %s/%s: %w", pod.Namespace, pod.Name, err))
				}
				podIP := podIPs[len(podIPs)-1]

				if reservedIP == podIP {
					found = true
					break
				}
			}

			if !found {
				staleIPs = append(staleIPs, allocation.IP.String())
			}
		}
	}

	return staleIPs
}
