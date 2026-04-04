// SPDX-FileCopyrightText: 2026 Deutsche Telekom AG
//
// SPDX-License-Identifier: Apache-2.0

package whereabouts_e2e

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	nettypes "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	wbtestclient "github.com/telekom/whereabouts/e2e/client"
	"github.com/telekom/whereabouts/e2e/entities"
	"github.com/telekom/whereabouts/e2e/retrievers"
	testenv "github.com/telekom/whereabouts/e2e/testenvironment"
	"github.com/telekom/whereabouts/e2e/util"
	wbstorage "github.com/telekom/whereabouts/pkg/storage/kubernetes"
)

var _ = Describe("Whereabouts coverage", func() {
	const testNamespace = "default"

	var (
		clientInfo *wbtestclient.ClientInfo
		config     *rest.Config
		err        error
	)

	BeforeEach(func() {
		_, err = testenv.NewConfig()
		Expect(err).NotTo(HaveOccurred())

		config, err = util.ClusterConfig()
		Expect(err).NotTo(HaveOccurred())

		clientInfo, err = wbtestclient.NewClientInfo(config)
		Expect(err).NotTo(HaveOccurred())
	})

	// -----------------------------------------------------------------------
	// Scenario 1: IP pool exhaustion
	// /30 = network (.0) + 2 usable (.1, .2) + broadcast (.3)
	// -----------------------------------------------------------------------
	Context("IP pool exhaustion", func() {
		const (
			networkName = "wa-coverage-exhaust"
			// Use a range that does NOT overlap with 10.10.0.0/16 used elsewhere
			ipRange = "10.110.0.0/30"
		)

		var netAttachDef *nettypes.NetworkAttachmentDefinition

		BeforeEach(func() {
			netAttachDef = util.MacvlanNetworkWithWhereaboutsIPAMNetwork(
				networkName, testNamespace, ipRange, []string{}, wbstorage.UnnamedNetwork, true)
			_, err = clientInfo.AddNetAttachDef(netAttachDef)
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			Expect(clientInfo.DelNetAttachDef(netAttachDef)).To(Succeed())
		})

		It("exhausts the pool, then recovers after a pod is deleted", func() {
			By("provisioning pod-1 — should get first usable IP")
			pod1, err := clientInfo.ProvisionPod(
				"wb-exhaust-pod1", testNamespace,
				util.PodTierLabel("wb-exhaust-pod1"),
				entities.PodNetworkSelectionElements(networkName),
			)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { _ = clientInfo.DeletePod(pod1) })

			ips1, err := retrievers.SecondaryIfaceIPValue(pod1, "net1")
			Expect(err).NotTo(HaveOccurred())
			Expect(ips1).NotTo(BeEmpty())
			Expect(inRange(ipRange, ips1[0])).To(Succeed())

			By("provisioning pod-2 — should get second usable IP")
			pod2, err := clientInfo.ProvisionPod(
				"wb-exhaust-pod2", testNamespace,
				util.PodTierLabel("wb-exhaust-pod2"),
				entities.PodNetworkSelectionElements(networkName),
			)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { _ = clientInfo.DeletePod(pod2) })

			ips2, err := retrievers.SecondaryIfaceIPValue(pod2, "net1")
			Expect(err).NotTo(HaveOccurred())
			Expect(ips2).NotTo(BeEmpty())
			Expect(inRange(ipRange, ips2[0])).To(Succeed())

			// The two allocated IPs must be distinct
			Expect(ips1[0]).NotTo(Equal(ips2[0]))

			By("provisioning pod-3 — pool is exhausted, should fail")
			_, err = clientInfo.ProvisionPod(
				"wb-exhaust-pod3", testNamespace,
				util.PodTierLabel("wb-exhaust-pod3"),
				entities.PodNetworkSelectionElements(networkName),
			)
			Expect(err).To(HaveOccurred(), "expected pool-exhaustion error for third pod")

			// Clean up the stuck pod-3 if it was created (best-effort)
			defer func() {
				pod3, getErr := clientInfo.Client.CoreV1().Pods(testNamespace).Get(
					context.Background(), "wb-exhaust-pod3", metav1.GetOptions{})
				if getErr == nil {
					_ = clientInfo.DeletePod(pod3)
				}
			}()

			By("deleting pod-1 to free its IP")
			Expect(clientInfo.DeletePod(pod1)).To(Succeed())
			verifyNoAllocationsForPodRef(clientInfo, ipRange, testNamespace, pod1.Name, ips1)

			By("provisioning pod-4 — should succeed with the recycled IP")
			pod4, err := clientInfo.ProvisionPod(
				"wb-exhaust-pod4", testNamespace,
				util.PodTierLabel("wb-exhaust-pod4"),
				entities.PodNetworkSelectionElements(networkName),
			)
			Expect(err).NotTo(HaveOccurred())
			defer func() { _ = clientInfo.DeletePod(pod4) }()

			ips4, err := retrievers.SecondaryIfaceIPValue(pod4, "net1")
			Expect(err).NotTo(HaveOccurred())
			Expect(ips4).NotTo(BeEmpty())
			Expect(inRange(ipRange, ips4[0])).To(Succeed())
			// Recycled IP must be the one that was freed
			Expect(ips4[0]).To(Equal(ips1[0]))

			By("cleaning up pod-2 and pod-4")
			Expect(clientInfo.DeletePod(pod2)).To(Succeed())
			verifyNoAllocationsForPodRef(clientInfo, ipRange, testNamespace, pod2.Name, ips2)
			Expect(clientInfo.DeletePod(pod4)).To(Succeed())
			verifyNoAllocationsForPodRef(clientInfo, ipRange, testNamespace, pod4.Name, ips4)
		})
	})

	// -----------------------------------------------------------------------
	// Scenario 2: Operator restart recovery
	// -----------------------------------------------------------------------
	Context("Operator restart recovery", func() {
		const (
			networkName  = "wa-coverage-restart"
			ipRange      = "10.120.0.0/24"
			operatorName = "whereabouts-controller-manager"
			operatorNS   = "kube-system"
			replicaCount = int32(3)
			rsName       = "wb-restart-rs"
		)

		var netAttachDef *nettypes.NetworkAttachmentDefinition

		BeforeEach(func() {
			netAttachDef = util.MacvlanNetworkWithWhereaboutsIPAMNetwork(
				networkName, testNamespace, ipRange, []string{}, wbstorage.UnnamedNetwork, true)
			_, err = clientInfo.AddNetAttachDef(netAttachDef)
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			Expect(clientInfo.DelNetAttachDef(netAttachDef)).To(Succeed())
		})

		It("preserves IP allocations across an operator rollout restart", func() {
			By(fmt.Sprintf("provisioning ReplicaSet %q with %d replicas", rsName, replicaCount))
			replicaSet, err := clientInfo.ProvisionReplicaSet(
				rsName, testNamespace, replicaCount,
				util.PodTierLabel(rsName),
				entities.PodNetworkSelectionElements(networkName),
			)
			Expect(err).NotTo(HaveOccurred())
			defer func() { _ = clientInfo.DeleteReplicaSet(replicaSet) }()

			By("collecting IPs assigned to all replica pods")
			podList, err := clientInfo.Client.CoreV1().Pods(testNamespace).List(
				context.Background(),
				metav1.ListOptions{LabelSelector: entities.ReplicaSetQuery(rsName)},
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(podList.Items).To(HaveLen(int(replicaCount)))

			preRestartIPs := make(map[string]string) // podName → secondary IP
			for i := range podList.Items {
				p := &podList.Items[i]
				ips, err := retrievers.SecondaryIfaceIPValue(p, "net1")
				Expect(err).NotTo(HaveOccurred())
				Expect(ips).NotTo(BeEmpty())
				preRestartIPs[p.Name] = ips[0]
			}
			Expect(preRestartIPs).To(HaveLen(int(replicaCount)))

			By("triggering a rollout restart of the whereabouts operator")
			deployment, err := clientInfo.Client.AppsV1().Deployments(operatorNS).Get(
				context.Background(), operatorName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())

			if deployment.Spec.Template.Annotations == nil {
				deployment.Spec.Template.Annotations = make(map[string]string)
			}
			deployment.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] =
				time.Now().UTC().Format(time.RFC3339)

			_, err = clientInfo.Client.AppsV1().Deployments(operatorNS).Update(
				context.Background(), deployment, metav1.UpdateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the operator rollout to complete")
			const rolloutTimeout = 3 * time.Minute
			// Get the deployment generation after the restart annotation was applied
			updatedDep, err := clientInfo.Client.AppsV1().Deployments(operatorNS).Get(
				context.Background(), operatorName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
			targetGeneration := updatedDep.Generation

			Eventually(func() bool {
				dep, err := clientInfo.Client.AppsV1().Deployments(operatorNS).Get(
					context.Background(), operatorName, metav1.GetOptions{})
				if err != nil {
					return false
				}
				// Ensure the controller has observed our update AND all replicas are ready
				return dep.Status.ObservedGeneration >= targetGeneration &&
					dep.Status.UpdatedReplicas == dep.Status.Replicas &&
					dep.Status.ReadyReplicas == dep.Status.Replicas &&
					dep.Status.UnavailableReplicas == 0
			}, rolloutTimeout, 5*time.Second).Should(BeTrue(),
				"operator rollout should complete with all replicas ready")

			By("verifying existing pods still hold their pre-restart IPs")
			verifiedCount := 0
			for i := range podList.Items {
				p := &podList.Items[i]
				currentPod, err := clientInfo.Client.CoreV1().Pods(testNamespace).Get(
					context.Background(), p.Name, metav1.GetOptions{})
				Expect(err).NotTo(HaveOccurred(),
					"pod %s should still exist after operator restart", p.Name)
				ips, err := retrievers.SecondaryIfaceIPValue(currentPod, "net1")
				Expect(err).NotTo(HaveOccurred())
				Expect(ips).NotTo(BeEmpty())
				Expect(ips[0]).To(Equal(preRestartIPs[p.Name]),
					"pod %s changed IP after operator restart", p.Name)
				verifiedCount++
			}
			Expect(verifiedCount).To(BeNumerically(">", 0),
				"at least one pod should have been verified post-restart")

			By("verifying a new pod gets a non-conflicting IP")
			newPod, err := clientInfo.ProvisionPod(
				"wb-restart-new", testNamespace,
				util.PodTierLabel("wb-restart-new"),
				entities.PodNetworkSelectionElements(networkName),
			)
			Expect(err).NotTo(HaveOccurred())
			defer func() { _ = clientInfo.DeletePod(newPod) }()

			newIPs, err := retrievers.SecondaryIfaceIPValue(newPod, "net1")
			Expect(err).NotTo(HaveOccurred())
			Expect(newIPs).NotTo(BeEmpty())
			Expect(inRange(ipRange, newIPs[0])).To(Succeed())

			existingIPs := make([]string, 0, len(preRestartIPs))
			for _, ip := range preRestartIPs {
				existingIPs = append(existingIPs, ip)
			}
			Expect(existingIPs).NotTo(ContainElement(newIPs[0]),
				"new pod received an already-allocated IP")

			By("deleting all replica pods and verifying IP pool drains")
			for i := range podList.Items {
				p := &podList.Items[i]
				existingPod, getErr := clientInfo.Client.CoreV1().Pods(testNamespace).Get(
					context.Background(), p.Name, metav1.GetOptions{})
				if getErr == nil {
					Expect(clientInfo.DeletePod(existingPod)).To(Succeed())
					verifyNoAllocationsForPodRef(clientInfo, ipRange, testNamespace, p.Name, []string{preRestartIPs[p.Name]})
				}
			}

			Expect(clientInfo.DeletePod(newPod)).To(Succeed())
			verifyNoAllocationsForPodRef(clientInfo, ipRange, testNamespace, newPod.Name, newIPs)

			By("cleaning up the ReplicaSet")
			Expect(clientInfo.DeleteReplicaSet(replicaSet)).To(Succeed())
		})
	})
})
