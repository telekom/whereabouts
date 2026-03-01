package reconciler

import (
	"encoding/json"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	k8snetworkplumbingwgv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("Pod Wrapper operations", func() {
	generateDefaultNetworkStatus := func(ip string) k8snetworkplumbingwgv1.NetworkStatus {
		return k8snetworkplumbingwgv1.NetworkStatus{
			IPs:     []string{ip},
			Default: true,
		}
	}

	generateMultusNetworkStatus := func(ifaceName string, networkName string, ip string) k8snetworkplumbingwgv1.NetworkStatus {
		return k8snetworkplumbingwgv1.NetworkStatus{
			Name:      networkName,
			Interface: ifaceName,
			IPs:       []string{ip},
		}
	}

	generateMultusNetworkStatusList := func(ips ...string) []k8snetworkplumbingwgv1.NetworkStatus {
		var networkStatus []k8snetworkplumbingwgv1.NetworkStatus
		for i, ip := range ips {
			networkStatus = append(
				networkStatus,
				generateMultusNetworkStatus(
					fmt.Sprintf("network-%d", i),
					fmt.Sprintf("net%d", i),
					ip))
		}
		return networkStatus
	}

	generateMultusNetworkStatusAnnotationFromIPs := func(ips ...string) map[string]string {
		annotationString, err := json.Marshal(
			generateMultusNetworkStatusList(ips...))
		if err != nil {
			annotationString = []byte("")
		}
		return map[string]string{
			k8snetworkplumbingwgv1.NetworkStatusAnnot: string(annotationString),
		}
	}

	generateMultusNetworkStatusAnnotationFromNetworkStatus := func(networkStatus ...k8snetworkplumbingwgv1.NetworkStatus) map[string]string {
		annotationString, err := json.Marshal(networkStatus)
		if err != nil {
			annotationString = []byte("")
		}
		return map[string]string{
			k8snetworkplumbingwgv1.NetworkStatusAnnot: string(annotationString),
		}
	}

	generatePodSpec := func(ips ...string) v1.Pod {
		return v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: generateMultusNetworkStatusAnnotationFromIPs(ips...),
			},
		}
	}

	generatePodSpecWithNameAndNamespace := func(name string, namespace string, ips ...string) v1.Pod {
		return v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: generateMultusNetworkStatusAnnotationFromIPs(ips...),
				Name:        name,
				Namespace:   namespace,
			},
		}
	}

	Context("the wrap pod operation", func() {
		DescribeTable("should extract the IPs from the network status annotations", func(ips ...string) {
			expectedIPs := map[string]void{}
			for _, ip := range ips {
				expectedIPs[ip] = void{}
			}

			pod := generatePodSpec(ips...)
			Expect(wrapPod(pod).ips).To(Equal(expectedIPs))
		},
			Entry("when the annotation does not feature multus networks"),
			Entry("when the annotation has a multus networks", "192.168.14.14"),
			Entry("when the annotation has multiple multus networks", "192.168.14.14", "10.10.10.10"),
		)

		It("should skip the default network annotations", func() {
			secondaryIfacesNetworkStatuses := generateMultusNetworkStatusList("192.168.14.14", "10.10.10.10")

			networkStatus := append(
				secondaryIfacesNetworkStatuses,
				generateDefaultNetworkStatus("14.15.16.20"))
			pod := v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: generateMultusNetworkStatusAnnotationFromNetworkStatus(networkStatus...),
				},
			}

			podSecondaryIPs := wrapPod(pod).ips
			Expect(podSecondaryIPs).To(HaveLen(2))
			Expect(podSecondaryIPs).To(Equal(map[string]void{"192.168.14.14": {}, "10.10.10.10": {}}))
		})

		It("return an empty list when the network annotations of a pod are invalid", func() {
			pod := v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{k8snetworkplumbingwgv1.NetworkStatusAnnot: "this-wont-fly"},
				},
			}
			Expect(wrapPod(pod).ips).To(BeEmpty())
		})

		It("returns an empty list when a pod does not feature network status annotations", func() {
			pod := v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{},
				},
			}
			Expect(wrapPod(pod).ips).To(BeEmpty())
		})
	})

	Context("the index pods operation", func() {
		type podInfo struct {
			ips       []string
			name      string
			namespace string
		}

		DescribeTable("", func(podsInfo ...podInfo) {
			var pods []v1.Pod
			whereaboutsPods := map[string]void{}

			for _, info := range podsInfo {
				newPod := generatePodSpecWithNameAndNamespace(info.name, info.namespace, info.ips...)
				pods = append(pods, newPod)
				whereaboutsPods[composePodRef(newPod)] = void{}
			}
			expectedPodWrapper := map[string]podWrapper{}
			for _, info := range podsInfo {
				indexedPodIPs := map[string]void{}
				for _, ip := range info.ips {
					indexedPodIPs[ip] = void{}
				}
				expectedPodWrapper[fmt.Sprintf("%s/%s", info.namespace, info.name)] = podWrapper{ips: indexedPodIPs}
			}

			Expect(indexPods(pods, whereaboutsPods)).To(Equal(expectedPodWrapper))
		},
			Entry("when no pods are passed"),
			Entry("when a pod is passed", podInfo{
				ips:       []string{"10.10.10.10"},
				name:      "pod1",
				namespace: "default",
			}),
			Entry("when multiple pods are passed",
				podInfo{
					ips:       []string{"10.10.10.10"},
					name:      "pod1",
					namespace: "default",
				},
				podInfo{
					ips:       []string{"192.168.14.14", "200.200.200.200s"},
					name:      "pod200",
					namespace: "secretns",
				}))
	})

	Context("isPodMarkedForDeletion", func() {
		It("returns true when DisruptionTarget condition is True with DeletionByTaintManager reason", func() {
			conditions := []v1.PodCondition{
				{
					Type:   v1.DisruptionTarget,
					Status: v1.ConditionTrue,
					Reason: "DeletionByTaintManager",
				},
			}
			Expect(isPodMarkedForDeletion(conditions)).To(BeTrue())
		})

		It("returns false when conditions are empty", func() {
			Expect(isPodMarkedForDeletion(nil)).To(BeFalse())
			Expect(isPodMarkedForDeletion([]v1.PodCondition{})).To(BeFalse())
		})

		It("returns false when DisruptionTarget is present but status is not True", func() {
			conditions := []v1.PodCondition{
				{
					Type:   v1.DisruptionTarget,
					Status: v1.ConditionFalse,
					Reason: "DeletionByTaintManager",
				},
			}
			Expect(isPodMarkedForDeletion(conditions)).To(BeFalse())
		})

		It("returns false when DisruptionTarget is True but reason is different", func() {
			conditions := []v1.PodCondition{
				{
					Type:   v1.DisruptionTarget,
					Status: v1.ConditionTrue,
					Reason: "EvictionByEvictionAPI",
				},
			}
			Expect(isPodMarkedForDeletion(conditions)).To(BeFalse())
		})

		It("returns false when condition type does not match", func() {
			conditions := []v1.PodCondition{
				{
					Type:   v1.PodReady,
					Status: v1.ConditionTrue,
					Reason: "DeletionByTaintManager",
				},
			}
			Expect(isPodMarkedForDeletion(conditions)).To(BeFalse())
		})

		It("finds the matching condition among multiple conditions", func() {
			conditions := []v1.PodCondition{
				{
					Type:   v1.PodReady,
					Status: v1.ConditionTrue,
				},
				{
					Type:   v1.PodScheduled,
					Status: v1.ConditionTrue,
				},
				{
					Type:   v1.DisruptionTarget,
					Status: v1.ConditionTrue,
					Reason: "DeletionByTaintManager",
				},
			}
			Expect(isPodMarkedForDeletion(conditions)).To(BeTrue())
		})
	})
})
