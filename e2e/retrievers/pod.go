package retrievers

import (
	"encoding/json"
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"

	nettypes "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
)

// ErrNoSecondaryIface means the pod has no IP on the requested secondary
// interface (no networks-status annotation, no matching interface, or no IPs).
// Callers consistency-checking mixed pod lists treat this as "skip", while a
// real annotation parse error stays a hard failure.
var ErrNoSecondaryIface = errors.New("pod has no IP on requested secondary interface")

func filterNetworkStatus(
	networkStatuses []nettypes.NetworkStatus, predicate func(nettypes.NetworkStatus) bool) *nettypes.NetworkStatus {
	for i := range networkStatuses {
		if predicate(networkStatuses[i]) {
			return &networkStatuses[i]
		}
	}
	return nil
}

func SecondaryIfaceIPValue(pod *corev1.Pod, ifName string) ([]string, error) {
	podNetStatus, found := pod.Annotations[nettypes.NetworkStatusAnnot]
	if !found {
		return nil, fmt.Errorf("%w: %s/%s missing networks-status annotation", ErrNoSecondaryIface, pod.Namespace, pod.Name)
	}

	var netStatus []nettypes.NetworkStatus
	if err := json.Unmarshal([]byte(podNetStatus), &netStatus); err != nil {
		return nil, fmt.Errorf("parse networks-status of %s/%s: %w", pod.Namespace, pod.Name, err)
	}

	secondaryInterfaceNetworkStatus := filterNetworkStatus(netStatus, func(status nettypes.NetworkStatus) bool {
		return status.Interface == ifName
	})

	if secondaryInterfaceNetworkStatus == nil {
		return nil, fmt.Errorf("%w: %s/%s has no %q", ErrNoSecondaryIface, pod.Namespace, pod.Name, ifName)
	}

	if len(secondaryInterfaceNetworkStatus.IPs) == 0 {
		return nil, fmt.Errorf("%w: %s/%s has no IPs on %q", ErrNoSecondaryIface, pod.Namespace, pod.Name, ifName)
	}

	return secondaryInterfaceNetworkStatus.IPs, nil
}
