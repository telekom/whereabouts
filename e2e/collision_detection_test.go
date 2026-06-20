// SPDX-FileCopyrightText: 2026 Deutsche Telekom AG
//
// SPDX-License-Identifier: Apache-2.0

package whereabouts_e2e

import (
	"context"
	"fmt"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	whereaboutsv1alpha1 "github.com/telekom/whereabouts/api/whereabouts.cni.cncf.io/v1alpha1"
	wbtestclient "github.com/telekom/whereabouts/e2e/client"
	"github.com/telekom/whereabouts/e2e/util"
)

var _ = Describe("Collision detection", func() {
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
		if err != nil {
			Skip(fmt.Sprintf("skipping collision detection e2e: KUBECONFIG not set or invalid: %v", err))
		}

		clientInfo, err = wbtestclient.NewClientInfo(config)
		Expect(err).NotTo(HaveOccurred())
	})

	Context("node CIDR collision warning (reconciler)", func() {
		var (
			collisionPoolName string
		)

		BeforeEach(func() {
			By("listing cluster nodes to find one with a PodCIDR")
			nodes, err := clientInfo.Client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
			Expect(err).NotTo(HaveOccurred())

			var nodePodCIDR string
			for i := range nodes.Items {
				node := &nodes.Items[i]
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
			pool := &whereaboutsv1alpha1.IPPool{
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

			_, err = clientInfo.WbClient.WhereaboutsV1alpha1().IPPools(ipPoolNamespace).Create(ctx, pool, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "overlapping IPPool creation must succeed (warning only, not blocked)")
		})

		AfterEach(func() {
			if collisionPoolName == "" {
				return
			}
			By("cleaning up node-cidr collision pool")
			err := clientInfo.WbClient.WhereaboutsV1alpha1().IPPools(ipPoolNamespace).Delete(ctx, collisionPoolName, metav1.DeleteOptions{})
			if err != nil && !apierrors.IsNotFound(err) {
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
			}).WithTimeout(30*time.Second).WithPolling(2*time.Second).Should(BeTrue(),
				"expected CIDRCollision Warning event on pool %q", collisionPoolName)
		})
	})

	Context("service CIDR collision warning (reconciler)", func() {
		const svcCIDRPoolName = "collision-test-svc-cidr"
		const defaultServiceCIDR = "10.96.0.0/12"

		It("emits a CIDRCollision Warning event when pool overlaps service CIDR", func() {
			serviceCIDR := os.Getenv("SERVICE_CIDR")
			if serviceCIDR == "" {
				serviceCIDR = defaultServiceCIDR
			}

			By("creating IPPool with service CIDR range " + serviceCIDR)
			pool := &whereaboutsv1alpha1.IPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      svcCIDRPoolName,
					Namespace: ipPoolNamespace,
				},
				Spec: whereaboutsv1alpha1.IPPoolSpec{
					Range:       serviceCIDR,
					Allocations: map[string]whereaboutsv1alpha1.IPAllocation{},
				},
			}

			_, err := clientInfo.WbClient.WhereaboutsV1alpha1().IPPools(ipPoolNamespace).Create(ctx, pool, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "service-CIDR overlapping pool creation must succeed (collision is a warning, not a block)")

			DeferCleanup(func() {
				By("cleaning up service-cidr collision pool")
				delErr := clientInfo.WbClient.WhereaboutsV1alpha1().IPPools(ipPoolNamespace).Delete(ctx, svcCIDRPoolName, metav1.DeleteOptions{})
				if delErr != nil && !apierrors.IsNotFound(delErr) {
					Expect(delErr).NotTo(HaveOccurred())
				}
			})

			By("polling for a CIDRCollision Warning event on pool " + svcCIDRPoolName)
			Eventually(func() bool {
				events, listErr := clientInfo.Client.CoreV1().Events(ipPoolNamespace).List(ctx, metav1.ListOptions{
					FieldSelector: fmt.Sprintf("involvedObject.name=%s,reason=CIDRCollision", svcCIDRPoolName),
				})
				if listErr != nil || len(events.Items) == 0 {
					return false
				}
				return events.Items[0].Type == corev1.EventTypeWarning
			}).WithTimeout(30*time.Second).WithPolling(2*time.Second).Should(BeTrue(),
				"expected CIDRCollision Warning event on pool %q (service CIDR %s)", svcCIDRPoolName, serviceCIDR)
		})
	})
})
