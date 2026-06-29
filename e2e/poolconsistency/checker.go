package poolconsistency

import (
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"github.com/telekom/whereabouts/e2e/retrievers"
	"github.com/telekom/whereabouts/pkg/storage"
)

type Checker struct {
	ipPool  storage.IPPool
	podList []corev1.Pod
}

func NewPoolConsistencyCheck(ipPool storage.IPPool, podList []corev1.Pod) *Checker {
	return &Checker{
		ipPool:  ipPool,
		podList: podList,
	}
}

func (pc *Checker) MissingIPs() []string {
	var mismatchedIPs []string
	for i := range pc.podList {
		pod := &pc.podList[i]
		podIPs, err := retrievers.SecondaryIfaceIPValue(pod, "net1")
		if errors.Is(err, retrievers.ErrNoSecondaryIface) {
			continue
		}
		if err != nil {
			panic(fmt.Errorf("pool-consistency MissingIPs: read net1 IP of pod %s/%s: %w", pod.Namespace, pod.Name, err))
		}
		podIP := podIPs[len(podIPs)-1]

		var found bool
		for _, allocation := range pc.ipPool.Allocations() {
			reservedIP := allocation.IP.String()

			if reservedIP == podIP {
				found = true
				break
			}
		}

		if !found {
			mismatchedIPs = append(mismatchedIPs, podIP)
		}
	}
	return mismatchedIPs
}

func (pc *Checker) StaleIPs() []string {
	var staleIPs []string
	for _, allocation := range pc.ipPool.Allocations() {
		reservedIP := allocation.IP.String()
		found := false
		for i := range pc.podList {
			pod := &pc.podList[i]
			podIPs, err := retrievers.SecondaryIfaceIPValue(pod, "net1")
			if errors.Is(err, retrievers.ErrNoSecondaryIface) {
				continue
			}
			if err != nil {
				panic(fmt.Errorf("pool-consistency StaleIPs: read net1 IP of pod %s/%s: %w", pod.Namespace, pod.Name, err))
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
	return staleIPs
}
