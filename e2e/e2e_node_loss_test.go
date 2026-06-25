// SPDX-FileCopyrightText: 2026 Deutsche Telekom AG
//
// SPDX-License-Identifier: Apache-2.0

package whereabouts_e2e

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	nettypes "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	wbtestclient "github.com/telekom/whereabouts/e2e/client"
	"github.com/telekom/whereabouts/e2e/entities"
	"github.com/telekom/whereabouts/e2e/retrievers"
	testenv "github.com/telekom/whereabouts/e2e/testenvironment"
	"github.com/telekom/whereabouts/e2e/util"
	wbstorage "github.com/telekom/whereabouts/pkg/storage/kubernetes"
)

var _ = Describe("Node loss", func() {
	const (
		testNamespace = "default"
		networkName   = "wa-node-loss"
		ipRange       = "10.121.0.0/24"
		lostPodName   = "wb-node-loss-1"
		reusePodName  = "wb-node-loss-2"
	)

	var (
		clientInfo *wbtestclient.ClientInfo
		config     *rest.Config
		err        error
		nad        *nettypes.NetworkAttachmentDefinition
	)

	BeforeEach(func() {
		_, err = testenv.NewConfig()
		Expect(err).NotTo(HaveOccurred())

		config, err = util.ClusterConfig()
		Expect(err).NotTo(HaveOccurred())

		clientInfo, err = wbtestclient.NewClientInfo(config)
		Expect(err).NotTo(HaveOccurred())

		nad = util.MacvlanNetworkWithWhereaboutsIPAMNetwork(
			networkName, testNamespace, ipRange, []string{}, wbstorage.UnnamedNetwork, true)
		_, err = clientInfo.AddNetAttachDef(nad)
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		Expect(clientInfo.DelNetAttachDef(nad)).To(Succeed())
	})

	It("releases and reuses IPs after a kind worker disappears", func() {
		ctx := context.Background()
		workerNode := firstReadyWorkerNode(ctx, clientInfo)

		By(fmt.Sprintf("creating a pod pinned to worker node %s", workerNode.Name))
		lostPod := podPinnedToNode(lostPodName, testNamespace, networkName, workerNode.Name)
		_, err = clientInfo.Client.CoreV1().Pods(testNamespace).Create(ctx, lostPod, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			_ = clientInfo.Client.CoreV1().Pods(testNamespace).Delete(ctx, lostPodName, metav1.DeleteOptions{})
		})
		Expect(wbtestclient.WaitForPodReady(ctx, clientInfo.Client, testNamespace, lostPodName, 90*time.Second)).To(Succeed())

		lostPod, err = clientInfo.Client.CoreV1().Pods(testNamespace).Get(ctx, lostPodName, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		ips, err := retrievers.SecondaryIfaceIPValue(lostPod, "net1")
		Expect(err).NotTo(HaveOccurred())
		Expect(ips).NotTo(BeEmpty())

		By("verifying the IPPool allocation and overlapping reservation exist")
		verifyAllocations(clientInfo, ipRange, ips[0], testNamespace, lostPodName, "net1")

		By(fmt.Sprintf("removing kind worker container %s", workerNode.Name))
		Expect(removeKindWorker(ctx, workerNode.Name)).To(Succeed())

		By("waiting for Kubernetes to observe node loss")
		Eventually(func() bool {
			node, err := clientInfo.Client.CoreV1().Nodes().Get(ctx, workerNode.Name, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				return true
			}
			Expect(err).NotTo(HaveOccurred())
			return nodeReadyStatus(node) != corev1.ConditionTrue
		}, 4*time.Minute, 5*time.Second).Should(BeTrue())

		By("waiting for taint-manager disruption or pod deletion")
		Eventually(func() bool {
			pod, err := clientInfo.Client.CoreV1().Pods(testNamespace).Get(ctx, lostPodName, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				return true
			}
			Expect(err).NotTo(HaveOccurred())
			return pod.DeletionTimestamp != nil || podMarkedForTaintManagerDeletion(pod)
		}, 4*time.Minute, 5*time.Second).Should(BeTrue())

		By("verifying the lost pod allocation and reservation are cleaned up")
		verifyNoAllocationsForPodRefWithin(clientInfo, ipRange, testNamespace, lostPodName, ips, 3*time.Minute)

		By("creating a replacement pod and verifying IP reuse")
		reusePod, err := clientInfo.ProvisionPod(
			reusePodName, testNamespace,
			util.PodTierLabel(reusePodName),
			entities.PodNetworkSelectionElements(networkName),
		)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = clientInfo.DeletePod(reusePod) })

		reuseIPs, err := retrievers.SecondaryIfaceIPValue(reusePod, "net1")
		Expect(err).NotTo(HaveOccurred())
		Expect(reuseIPs).NotTo(BeEmpty())
		Expect(reuseIPs[0]).To(Equal(ips[0]), "replacement pod should reuse the cleaned-up lowest IP")
		verifyAllocations(clientInfo, ipRange, reuseIPs[0], testNamespace, reusePodName, "net1")
	})
})

func firstReadyWorkerNode(ctx context.Context, clientInfo *wbtestclient.ClientInfo) corev1.Node {
	nodes, err := clientInfo.Client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	Expect(err).NotTo(HaveOccurred())

	for _, node := range nodes.Items {
		if _, isControlPlane := node.Labels["node-role.kubernetes.io/control-plane"]; isControlPlane {
			continue
		}
		if _, isMaster := node.Labels["node-role.kubernetes.io/master"]; isMaster {
			continue
		}
		if nodeReadyStatus(&node) == corev1.ConditionTrue {
			return node
		}
	}
	Fail("expected at least one ready worker node")
	return corev1.Node{}
}

func podPinnedToNode(name, namespace, networkName, nodeName string) *corev1.Pod {
	pod := entities.PodObject(
		name,
		namespace,
		util.PodTierLabel(name),
		entities.PodNetworkSelectionElements(networkName),
	)
	pod.Spec.NodeName = nodeName
	tolerationSeconds := int64(1)
	pod.Spec.Tolerations = append(pod.Spec.Tolerations,
		corev1.Toleration{
			Key:               "node.kubernetes.io/not-ready",
			Operator:          corev1.TolerationOpExists,
			Effect:            corev1.TaintEffectNoExecute,
			TolerationSeconds: &tolerationSeconds,
		},
		corev1.Toleration{
			Key:               "node.kubernetes.io/unreachable",
			Operator:          corev1.TolerationOpExists,
			Effect:            corev1.TaintEffectNoExecute,
			TolerationSeconds: &tolerationSeconds,
		},
	)
	return pod
}

func removeKindWorker(ctx context.Context, nodeName string) error {
	ociBin := os.Getenv("OCI_BIN")
	if ociBin == "" {
		ociBin = "docker"
	}

	cmd := exec.CommandContext(ctx, ociBin, "rm", "-f", nodeName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s rm -f %s failed: %w\n%s", ociBin, nodeName, err, output)
	}
	return nil
}

func nodeReadyStatus(node *corev1.Node) corev1.ConditionStatus {
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady {
			return condition.Status
		}
	}
	return corev1.ConditionUnknown
}

func podMarkedForTaintManagerDeletion(pod *corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.DisruptionTarget &&
			condition.Status == corev1.ConditionTrue &&
			condition.Reason == "DeletionByTaintManager" {
			return true
		}
	}
	return false
}

func verifyNoAllocationsForPodRefWithin(clientInfo *wbtestclient.ClientInfo, ipRange, namespace, podName string, ips []string, timeout time.Duration) {
	Eventually(func() bool {
		ipPool, err := clientInfo.WbClient.WhereaboutsV1alpha1().IPPools(ipPoolNamespace).Get(
			context.Background(),
			wbstorage.IPPoolName(wbstorage.PoolIdentifier{IPRange: ipRange, NetworkName: wbstorage.UnnamedNetwork}),
			metav1.GetOptions{},
		)
		if err != nil {
			return apierrors.IsNotFound(err)
		}
		return len(allocationForPodRef(getPodRef(namespace, podName), *ipPool)) == 0
	}, timeout, time.Second).Should(BeTrue())

	for _, ip := range ips {
		Eventually(func() bool {
			_, err := clientInfo.WbClient.WhereaboutsV1alpha1().OverlappingRangeIPReservations(ipPoolNamespace).Get(
				context.Background(),
				wbstorage.NormalizeIP(net.ParseIP(ip), wbstorage.UnnamedNetwork),
				metav1.GetOptions{},
			)
			return apierrors.IsNotFound(err)
		}, timeout, time.Second).Should(BeTrue())
	}
}
