// Copyright 2025 Deutsche Telekom
// SPDX-License-Identifier: Apache-2.0

package webhook

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	whereaboutsv1alpha1 "github.com/telekom/whereabouts/api/whereabouts.cni.cncf.io/v1alpha1"
)

func newWebhookTestScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	utilruntime.Must(whereaboutsv1alpha1.AddToScheme(scheme))
	return scheme
}

var _ = Describe("IPPoolValidator", func() {
	var (
		ctx       context.Context
		validator *IPPoolValidator
	)

	BeforeEach(func() {
		ctx = context.Background()
		validator = &IPPoolValidator{}
	})

	Context("ValidateCreate", func() {
		It("should accept a valid IPPool", func() {
			pool := &whereaboutsv1alpha1.IPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pool",
					Namespace: "default",
				},
				Spec: whereaboutsv1alpha1.IPPoolSpec{
					Range: "10.0.0.0/24",
					Allocations: map[string]whereaboutsv1alpha1.IPAllocation{
						"1": {
							ContainerID: "abc123",
							PodRef:      "default/my-pod",
							IfName:      "eth0",
						},
					},
				},
			}
			warnings, err := validator.ValidateCreate(ctx, pool)
			Expect(err).NotTo(HaveOccurred())
			Expect(warnings).To(BeEmpty())
		})

		It("should reject an IPPool with invalid Range", func() {
			pool := &whereaboutsv1alpha1.IPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pool",
					Namespace: "default",
				},
				Spec: whereaboutsv1alpha1.IPPoolSpec{
					Range:       "not-a-cidr",
					Allocations: map[string]whereaboutsv1alpha1.IPAllocation{},
				},
			}
			_, err := validator.ValidateCreate(ctx, pool)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("invalid spec.range"))
		})

		It("should accept an IPPool with valid allocations", func() {
			pool := &whereaboutsv1alpha1.IPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pool",
					Namespace: "default",
				},
				Spec: whereaboutsv1alpha1.IPPoolSpec{
					Range: "10.0.0.0/24",
					Allocations: map[string]whereaboutsv1alpha1.IPAllocation{
						"1": {
							ContainerID: "abc123",
							PodRef:      "default/pod-a",
							IfName:      "eth0",
						},
						"2": {
							ContainerID: "def456",
							PodRef:      "kube-system/pod-b",
							IfName:      "net1",
						},
					},
				},
			}
			warnings, err := validator.ValidateCreate(ctx, pool)
			Expect(err).NotTo(HaveOccurred())
			Expect(warnings).To(BeEmpty())
		})

		It("should reject an IPPool with invalid podRef format", func() {
			pool := &whereaboutsv1alpha1.IPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pool",
					Namespace: "default",
				},
				Spec: whereaboutsv1alpha1.IPPoolSpec{
					Range: "10.0.0.0/24",
					Allocations: map[string]whereaboutsv1alpha1.IPAllocation{
						"1": {
							ContainerID: "abc123",
							PodRef:      "invalid-no-slash",
							IfName:      "eth0",
						},
					},
				},
			}
			_, err := validator.ValidateCreate(ctx, pool)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("namespace/name format"))
		})

		It("should issue a warning for an allocation with empty podRef", func() {
			pool := &whereaboutsv1alpha1.IPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pool",
					Namespace: "default",
				},
				Spec: whereaboutsv1alpha1.IPPoolSpec{
					Range: "10.0.0.0/24",
					Allocations: map[string]whereaboutsv1alpha1.IPAllocation{
						"1": {
							ContainerID: "abc123",
							PodRef:      "",
							IfName:      "eth0",
						},
					},
				},
			}
			warnings, err := validator.ValidateCreate(ctx, pool)
			Expect(err).NotTo(HaveOccurred())
			Expect(warnings).To(HaveLen(1))
			Expect(warnings[0]).To(ContainSubstring("empty podRef"))
		})
	})

	Context("ValidateUpdate", func() {
		It("should accept a valid update with same range", func() {
			oldPool := &whereaboutsv1alpha1.IPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pool",
					Namespace: "default",
				},
				Spec: whereaboutsv1alpha1.IPPoolSpec{
					Range:       "10.0.0.0/24",
					Allocations: map[string]whereaboutsv1alpha1.IPAllocation{},
				},
			}
			newPool := &whereaboutsv1alpha1.IPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pool",
					Namespace: "default",
				},
				Spec: whereaboutsv1alpha1.IPPoolSpec{
					Range: "10.0.0.0/24",
					Allocations: map[string]whereaboutsv1alpha1.IPAllocation{
						"1": {
							ContainerID: "abc123",
							PodRef:      "default/my-pod",
							IfName:      "eth0",
						},
					},
				},
			}
			warnings, err := validator.ValidateUpdate(ctx, oldPool, newPool)
			Expect(err).NotTo(HaveOccurred())
			Expect(warnings).To(BeEmpty())
		})

		It("should warn on a range change but allow it", func() {
			oldPool := &whereaboutsv1alpha1.IPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pool",
					Namespace: "default",
				},
				Spec: whereaboutsv1alpha1.IPPoolSpec{
					Range:       "10.0.0.0/24",
					Allocations: map[string]whereaboutsv1alpha1.IPAllocation{},
				},
			}
			newPool := &whereaboutsv1alpha1.IPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pool",
					Namespace: "default",
				},
				Spec: whereaboutsv1alpha1.IPPoolSpec{
					Range:       "10.0.1.0/24",
					Allocations: map[string]whereaboutsv1alpha1.IPAllocation{},
				},
			}
			warnings, err := validator.ValidateUpdate(ctx, oldPool, newPool)
			Expect(err).NotTo(HaveOccurred())
			Expect(warnings).To(HaveLen(1))
			Expect(warnings[0]).To(ContainSubstring("spec.range changed"))
		})

		It("should reject an update with invalid podRef", func() {
			oldPool := &whereaboutsv1alpha1.IPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pool",
					Namespace: "default",
				},
				Spec: whereaboutsv1alpha1.IPPoolSpec{
					Range:       "10.0.0.0/24",
					Allocations: map[string]whereaboutsv1alpha1.IPAllocation{},
				},
			}
			newPool := &whereaboutsv1alpha1.IPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pool",
					Namespace: "default",
				},
				Spec: whereaboutsv1alpha1.IPPoolSpec{
					Range: "10.0.0.0/24",
					Allocations: map[string]whereaboutsv1alpha1.IPAllocation{
						"1": {
							ContainerID: "abc123",
							PodRef:      "invalid",
							IfName:      "eth0",
						},
					},
				},
			}
			_, err := validator.ValidateUpdate(ctx, oldPool, newPool)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("namespace/name format"))
		})
	})

	Context("ValidateDelete", func() {
		It("should always succeed", func() {
			pool := &whereaboutsv1alpha1.IPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pool",
					Namespace: "default",
				},
				Spec: whereaboutsv1alpha1.IPPoolSpec{
					Range:       "10.0.0.0/24",
					Allocations: map[string]whereaboutsv1alpha1.IPAllocation{},
				},
			}
			warnings, err := validator.ValidateDelete(ctx, pool)
			Expect(err).NotTo(HaveOccurred())
			Expect(warnings).To(BeNil())
		})
	})

	Context("pool-to-pool overlap detection", func() {
		var (
			existingPool *whereaboutsv1alpha1.IPPool
		)

		BeforeEach(func() {
			existingPool = &whereaboutsv1alpha1.IPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "existing-pool",
					Namespace: "default",
				},
				Spec: whereaboutsv1alpha1.IPPoolSpec{
					Range:       "10.0.0.0/24",
					Allocations: map[string]whereaboutsv1alpha1.IPAllocation{},
				},
			}
		})

		It("should block create when new pool CIDR overlaps an existing pool", func() {
			fakeClient := fake.NewClientBuilder().
				WithScheme(newWebhookTestScheme()).
				WithObjects(existingPool).
				Build()
			v := &IPPoolValidator{Reader: fakeClient}

			newPool := &whereaboutsv1alpha1.IPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "new-pool",
					Namespace: "default",
				},
				Spec: whereaboutsv1alpha1.IPPoolSpec{
					Range:       "10.0.0.128/25", // overlaps with 10.0.0.0/24
					Allocations: map[string]whereaboutsv1alpha1.IPAllocation{},
				},
			}
			_, err := v.ValidateCreate(ctx, newPool)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("overlaps with existing IPPool"))
			Expect(err.Error()).To(ContainSubstring("existing-pool"))
		})

		It("should allow create when new pool CIDR does not overlap any existing pool", func() {
			fakeClient := fake.NewClientBuilder().
				WithScheme(newWebhookTestScheme()).
				WithObjects(existingPool).
				Build()
			v := &IPPoolValidator{Reader: fakeClient}

			newPool := &whereaboutsv1alpha1.IPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "new-pool",
					Namespace: "default",
				},
				Spec: whereaboutsv1alpha1.IPPoolSpec{
					Range:       "10.0.1.0/24", // does not overlap with 10.0.0.0/24
					Allocations: map[string]whereaboutsv1alpha1.IPAllocation{},
				},
			}
			_, err := v.ValidateCreate(ctx, newPool)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should allow update of the same pool (self-overlap excluded)", func() {
			fakeClient := fake.NewClientBuilder().
				WithScheme(newWebhookTestScheme()).
				WithObjects(existingPool).
				Build()
			v := &IPPoolValidator{Reader: fakeClient}

			// Updating the same pool with the same name/namespace — must not flag itself.
			updatedPool := &whereaboutsv1alpha1.IPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "existing-pool",
					Namespace: "default",
				},
				Spec: whereaboutsv1alpha1.IPPoolSpec{
					Range:       "10.0.0.0/24",
					Allocations: map[string]whereaboutsv1alpha1.IPAllocation{},
				},
			}
			_, err := v.ValidateUpdate(ctx, existingPool, updatedPool)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should block update when updated CIDR overlaps a different existing pool", func() {
			otherPool := &whereaboutsv1alpha1.IPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "other-pool",
					Namespace: "default",
				},
				Spec: whereaboutsv1alpha1.IPPoolSpec{
					Range:       "192.168.0.0/24",
					Allocations: map[string]whereaboutsv1alpha1.IPAllocation{},
				},
			}
			fakeClient := fake.NewClientBuilder().
				WithScheme(newWebhookTestScheme()).
				WithObjects(existingPool, otherPool).
				Build()
			v := &IPPoolValidator{Reader: fakeClient}

			// Updating "other-pool" to a CIDR that overlaps "existing-pool".
			updatedOtherPool := &whereaboutsv1alpha1.IPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "other-pool",
					Namespace: "default",
				},
				Spec: whereaboutsv1alpha1.IPPoolSpec{
					Range:       "10.0.0.0/25", // overlaps with existing-pool's 10.0.0.0/24
					Allocations: map[string]whereaboutsv1alpha1.IPAllocation{},
				},
			}
			_, err := v.ValidateUpdate(ctx, otherPool, updatedOtherPool)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("overlaps with existing IPPool"))
			Expect(err.Error()).To(ContainSubstring("existing-pool"))
		})

		It("should skip overlap check when Reader is nil (graceful degradation)", func() {
			v := &IPPoolValidator{Reader: nil}

			newPool := &whereaboutsv1alpha1.IPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "new-pool",
					Namespace: "default",
				},
				Spec: whereaboutsv1alpha1.IPPoolSpec{
					Range:       "10.0.0.0/24",
					Allocations: map[string]whereaboutsv1alpha1.IPAllocation{},
				},
			}
			_, err := v.ValidateCreate(ctx, newPool)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should not flag pools in a different namespace as overlapping", func() {
			// existingPool is in "default"; new pool is in "kube-system" — list is namespace-scoped.
			fakeClient := fake.NewClientBuilder().
				WithScheme(newWebhookTestScheme()).
				WithObjects(existingPool).
				Build()
			v := &IPPoolValidator{Reader: fakeClient}

			newPool := &whereaboutsv1alpha1.IPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "new-pool",
					Namespace: "kube-system",
				},
				Spec: whereaboutsv1alpha1.IPPoolSpec{
					Range:       "10.0.0.0/24", // same CIDR but different namespace
					Allocations: map[string]whereaboutsv1alpha1.IPAllocation{},
				},
			}
			_, err := v.ValidateCreate(ctx, newPool)
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
