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

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	whereaboutsv1alpha1 "github.com/telekom/whereabouts/api/whereabouts.cni.cncf.io/v1alpha1"
	wbtestclient "github.com/telekom/whereabouts/e2e/client"
	"github.com/telekom/whereabouts/e2e/util"
)

var _ = Describe("collision detection", func() {
	var (
		clientInfo *wbtestclient.ClientInfo
		ctx        context.Context
	)

	BeforeEach(func() {
		ctx = context.Background()

		var (
			config *rest.Config
			err    error
		)

		config, err = util.ClusterConfig()
		Expect(err).NotTo(HaveOccurred(), "KUBECONFIG must be set to run e2e tests")

		clientInfo, err = wbtestclient.NewClientInfo(config)
		Expect(err).NotTo(HaveOccurred())
	})

	Context("pool-to-pool CIDR overlap (webhook)", func() {
		const (
			poolAName = "collision-test-pool-a"
			poolBName = "collision-test-pool-b"
			poolACIDR = "10.201.0.0/24"
			poolBCIDR = "10.201.0.0/25" // subset of poolACIDR — overlaps
		)

		var (
			poolA *whereaboutsv1alpha1.IPPool
			poolB *whereaboutsv1alpha1.IPPool
		)

		BeforeEach(func() {
			poolA = &whereaboutsv1alpha1.IPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      poolAName,
					Namespace: ipPoolNamespace,
				},
				Spec: whereaboutsv1alpha1.IPPoolSpec{
					Range:       poolACIDR,
					Allocations: map[string]whereaboutsv1alpha1.IPAllocation{},
				},
			}

			By("creating the first IPPool (pool-a)")
			_, err := clientInfo.WbClient.WhereaboutsV1alpha1().IPPools(ipPoolNamespace).Create(ctx, poolA, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "pool-a creation must succeed")
		})

		AfterEach(func() {
			By("cleaning up pool-a")
			err := clientInfo.WbClient.WhereaboutsV1alpha1().IPPools(ipPoolNamespace).Delete(ctx, poolAName, metav1.DeleteOptions{})
			if err != nil && !k8serrors.IsNotFound(err) {
				Expect(err).NotTo(HaveOccurred())
			}

			By("cleaning up pool-b")
			err = clientInfo.WbClient.WhereaboutsV1alpha1().IPPools(ipPoolNamespace).Delete(ctx, poolBName, metav1.DeleteOptions{})
			if err != nil && !k8serrors.IsNotFound(err) {
				Expect(err).NotTo(HaveOccurred())
			}
		})

		It("rejects a second IPPool whose CIDR overlaps the first", func() {
			poolB = &whereaboutsv1alpha1.IPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      poolBName,
					Namespace: ipPoolNamespace,
				},
				Spec: whereaboutsv1alpha1.IPPoolSpec{
					Range:       poolBCIDR,
					Allocations: map[string]whereaboutsv1alpha1.IPAllocation{},
				},
			}

			By("attempting to create an overlapping IPPool (pool-b)")
			_, err := clientInfo.WbClient.WhereaboutsV1alpha1().IPPools(ipPoolNamespace).Create(ctx, poolB, metav1.CreateOptions{})
			Expect(err).To(HaveOccurred(), "webhook must reject overlapping pool")
			Expect(err.Error()).To(MatchRegexp("(?i)overlaps"), "rejection error must mention 'overlaps'")
		})
	})

	Context("node CIDR collision warning (reconciler)", func() {
		var (
			collisionPool     *whereaboutsv1alpha1.IPPool
			collisionPoolName string
		)

		BeforeEach(func() {
			By("listing cluster nodes to find one with a PodCIDR")
			nodes, err := clientInfo.Client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
			Expect(err).NotTo(HaveOccurred())

			var nodePodCIDR string
			for _, node := range nodes.Items {
				// Prefer PodCIDRs slice; fall back to singular PodCIDR field
				if len(node.Spec.PodCIDRs) > 0 {
					nodePodCIDR = node.Spec.PodCIDRs[0]
					break
				}
				if node.Spec.PodCIDR != "" {
					nodePodCIDR = node.Spec.PodCIDR
					break
				}
			}

			if nodePodCIDR == "" {
				Skip("no node with PodCIDR found — skipping node CIDR collision warning test")
			}

			By(fmt.Sprintf("found node PodCIDR %q — creating overlapping IPPool", nodePodCIDR))

			collisionPoolName = "collision-test-node-cidr"
			collisionPool = &whereaboutsv1alpha1.IPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      collisionPoolName,
					Namespace: ipPoolNamespace,
				},
				Spec: whereaboutsv1alpha1.IPPoolSpec{
					// Use the same CIDR as the node PodCIDR to guarantee overlap
					Range:       nodePodCIDR,
					Allocations: map[string]whereaboutsv1alpha1.IPAllocation{},
				},
			}

			_, err = clientInfo.WbClient.WhereaboutsV1alpha1().IPPools(ipPoolNamespace).Create(ctx, collisionPool, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "overlapping IPPool creation must succeed (warning only, not blocked)")
		})

		AfterEach(func() {
			if collisionPoolName == "" {
				return
			}
			By("cleaning up node-cidr collision pool")
			err := clientInfo.WbClient.WhereaboutsV1alpha1().IPPools(ipPoolNamespace).Delete(ctx, collisionPoolName, metav1.DeleteOptions{})
			if err != nil && !k8serrors.IsNotFound(err) {
				Expect(err).NotTo(HaveOccurred())
			}
		})

		It("emits a CIDRCollision Warning event on the pool", func() {
			By("waiting for a CIDRCollision Warning event on the pool")
			Eventually(func() bool {
				events, err := clientInfo.Client.CoreV1().Events(ipPoolNamespace).List(ctx, metav1.ListOptions{
					FieldSelector: fmt.Sprintf("involvedObject.name=%s,reason=CIDRCollision", collisionPoolName),
				})
				if err != nil || len(events.Items) == 0 {
					return false
				}
				return events.Items[0].Type == corev1.EventTypeWarning
			}).WithTimeout(60*time.Second).WithPolling(2*time.Second).Should(BeTrue(),
				"expected CIDRCollision Warning event on pool %q", collisionPoolName)
		})
	})
})
