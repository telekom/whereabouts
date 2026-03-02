// Copyright 2025 Deutsche Telekom
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	nadv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"

	whereaboutsv1alpha1 "github.com/telekom/whereabouts/pkg/api/whereabouts.cni.cncf.io/v1alpha1"
)

func newTestScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(whereaboutsv1alpha1.AddToScheme(scheme))
	utilruntime.Must(nadv1.AddToScheme(scheme))
	return scheme
}

var _ = Describe("IPPoolReconciler", func() {
	const (
		poolName      = "test-pool"
		poolNamespace = "default"
		poolRange     = "10.0.0.0/24"
		interval      = 30 * time.Second
	)

	var (
		ctx        context.Context
		scheme     *runtime.Scheme
		reconciler *IPPoolReconciler
		req        ctrl.Request
	)

	BeforeEach(func() {
		ctx = context.Background()
		scheme = newTestScheme()
		req = ctrl.Request{
			NamespacedName: types.NamespacedName{
				Namespace: poolNamespace,
				Name:      poolName,
			},
		}
	})

	buildReconciler := func(objs ...client.Object) {
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(objs...).
			Build()
		reconciler = &IPPoolReconciler{
			client:            fakeClient,
			reconcileInterval: interval,
		}
	}

	Context("when the pool has no allocations", func() {
		It("should requeue with reconcileInterval", func() {
			pool := &whereaboutsv1alpha1.IPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      poolName,
					Namespace: poolNamespace,
				},
				Spec: whereaboutsv1alpha1.IPPoolSpec{
					Range:       poolRange,
					Allocations: map[string]whereaboutsv1alpha1.IPAllocation{},
				},
			}
			buildReconciler(pool)

			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(interval))
		})
	})

	Context("when the pool has a valid pod allocation", func() {
		It("should not remove the allocation", func() {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-pod",
					Namespace: "default",
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
				},
			}
			pool := &whereaboutsv1alpha1.IPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      poolName,
					Namespace: poolNamespace,
				},
				Spec: whereaboutsv1alpha1.IPPoolSpec{
					Range: poolRange,
					Allocations: map[string]whereaboutsv1alpha1.IPAllocation{
						"1": {
							ContainerID: "abc123",
							PodRef:      "default/my-pod",
							IfName:      "eth0",
						},
					},
				},
			}
			buildReconciler(pool, pod)

			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(interval))

			// Verify allocation still exists.
			var updated whereaboutsv1alpha1.IPPool
			Expect(reconciler.client.Get(ctx, req.NamespacedName, &updated)).To(Succeed())
			Expect(updated.Spec.Allocations).To(HaveKey("1"))
		})
	})

	Context("when the pool has an orphaned allocation (pod not found)", func() {
		It("should remove the orphaned allocation", func() {
			pool := &whereaboutsv1alpha1.IPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      poolName,
					Namespace: poolNamespace,
				},
				Spec: whereaboutsv1alpha1.IPPoolSpec{
					Range: poolRange,
					Allocations: map[string]whereaboutsv1alpha1.IPAllocation{
						"1": {
							ContainerID: "abc123",
							PodRef:      "default/missing-pod",
							IfName:      "eth0",
						},
					},
				},
			}
			buildReconciler(pool)

			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(interval))

			// Verify allocation was removed.
			var updated whereaboutsv1alpha1.IPPool
			Expect(reconciler.client.Get(ctx, req.NamespacedName, &updated)).To(Succeed())
			Expect(updated.Spec.Allocations).To(BeEmpty())
		})
	})

	Context("when the pool has an allocation with invalid podRef format", func() {
		It("should remove the allocation", func() {
			pool := &whereaboutsv1alpha1.IPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      poolName,
					Namespace: poolNamespace,
				},
				Spec: whereaboutsv1alpha1.IPPoolSpec{
					Range: poolRange,
					Allocations: map[string]whereaboutsv1alpha1.IPAllocation{
						"1": {
							ContainerID: "abc123",
							PodRef:      "invalid-no-slash",
							IfName:      "eth0",
						},
					},
				},
			}
			buildReconciler(pool)

			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(interval))

			var updated whereaboutsv1alpha1.IPPool
			Expect(reconciler.client.Get(ctx, req.NamespacedName, &updated)).To(Succeed())
			Expect(updated.Spec.Allocations).To(BeEmpty())
		})
	})

	Context("when the pool has an allocation with empty podRef", func() {
		It("should remove the allocation", func() {
			pool := &whereaboutsv1alpha1.IPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      poolName,
					Namespace: poolNamespace,
				},
				Spec: whereaboutsv1alpha1.IPPoolSpec{
					Range: poolRange,
					Allocations: map[string]whereaboutsv1alpha1.IPAllocation{
						"1": {
							ContainerID: "abc123",
							PodRef:      "",
							IfName:      "eth0",
						},
					},
				},
			}
			buildReconciler(pool)

			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(interval))

			var updated whereaboutsv1alpha1.IPPool
			Expect(reconciler.client.Get(ctx, req.NamespacedName, &updated)).To(Succeed())
			Expect(updated.Spec.Allocations).To(BeEmpty())
		})
	})

	Context("when the pool is not found", func() {
		It("should return no error and no requeue", func() {
			buildReconciler() // no objects

			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())
		})
	})

	Context("when the pool has a pending pod", func() {
		It("should requeue with shorter interval (5s)", func() {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pending-pod",
					Namespace: "default",
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodPending,
				},
			}
			pool := &whereaboutsv1alpha1.IPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      poolName,
					Namespace: poolNamespace,
				},
				Spec: whereaboutsv1alpha1.IPPoolSpec{
					Range: poolRange,
					Allocations: map[string]whereaboutsv1alpha1.IPAllocation{
						"1": {
							ContainerID: "abc123",
							PodRef:      "default/pending-pod",
							IfName:      "eth0",
						},
					},
				},
			}
			buildReconciler(pool, pod)

			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(5 * time.Second))
		})
	})

	Context("when the pool has an allocation for a pod with DisruptionTarget condition", func() {
		It("should remove the allocation", func() {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "evicted-pod",
					Namespace: "default",
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
					Conditions: []corev1.PodCondition{
						{
							Type:   corev1.DisruptionTarget,
							Status: corev1.ConditionTrue,
							Reason: "DeletionByTaintManager",
						},
					},
				},
			}
			pool := &whereaboutsv1alpha1.IPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      poolName,
					Namespace: poolNamespace,
				},
				Spec: whereaboutsv1alpha1.IPPoolSpec{
					Range: poolRange,
					Allocations: map[string]whereaboutsv1alpha1.IPAllocation{
						"1": {
							ContainerID: "abc123",
							PodRef:      "default/evicted-pod",
							IfName:      "eth0",
						},
					},
				},
			}
			buildReconciler(pool, pod)

			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(interval))

			var updated whereaboutsv1alpha1.IPPool
			Expect(reconciler.client.Get(ctx, req.NamespacedName, &updated)).To(Succeed())
			Expect(updated.Spec.Allocations).To(BeEmpty())
		})
	})
})
