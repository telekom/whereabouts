// Copyright 2026 Deutsche Telekom AG
// SPDX-License-Identifier: Apache-2.0

package webhook

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	whereaboutsv1alpha1 "github.com/telekom/whereabouts/api/whereabouts.cni.cncf.io/v1alpha1"
)

var _ = Describe("webhook rejection logging", func() {
	DescribeTable("classifies validation failures without leaking request values",
		func(err error, wantReason string, forbidden []string) {
			reason := validationRejectionReason(err)

			Expect(reason).To(Equal(wantReason))
			for _, value := range forbidden {
				Expect(reason).NotTo(ContainSubstring(value))
			}
		},
		Entry("CIDR value",
			fmt.Errorf("invalid spec.range: invalid CIDR %q: parse error", "10.96.1.0/99"),
			"invalid CIDR",
			[]string{"10.96.1.0/99"},
		),
		Entry("immutable IP range update",
			fmt.Errorf("spec.range is immutable and cannot be changed (was %q, now %q)", "10.0.0.0/24", "10.0.1.0/24"),
			"immutable field changed",
			[]string{"10.0.0.0/24", "10.0.1.0/24"},
		),
		Entry("pod reference",
			fmt.Errorf("spec.podRef: podRef %q must contain a '/' separator", "default-pod"),
			"invalid pod reference",
			[]string{"default-pod"},
		),
		Entry("slice size",
			fmt.Errorf("invalid spec.sliceSize: sliceSize %q is larger than range", "/16"),
			"invalid slice size",
			[]string{"/16"},
		),
		Entry("unknown request detail",
			errors.New("unexpected admission failure for object 10.0.0.5 and container abc123"),
			"validation failed",
			[]string{"10.0.0.5", "abc123"},
		),
	)

	It("keeps detailed admission errors while using a redacted log reason", func() {
		validator := &OverlappingRangeValidator{}
		oldRes := &whereaboutsv1alpha1.OverlappingRangeIPReservation{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "10.0.0.5",
				Namespace: "default",
			},
			Spec: whereaboutsv1alpha1.OverlappingRangeIPReservationSpec{
				ContainerID: "container-a",
				PodRef:      "default/pod-a",
				IfName:      "net1",
				PodUID:      "uid-a",
			},
		}
		newRes := oldRes.DeepCopy()
		newRes.Spec.PodRef = "default/pod-b"

		_, err := validator.ValidateUpdate(context.Background(), oldRes, newRes)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("default/pod-a"))
		Expect(err.Error()).To(ContainSubstring("default/pod-b"))

		reason := validationRejectionReason(err)
		Expect(reason).To(Equal("immutable field changed"))
		Expect(reason).NotTo(ContainSubstring("10.0.0.5"))
		Expect(reason).NotTo(ContainSubstring("default/pod-a"))
		Expect(reason).NotTo(ContainSubstring("default/pod-b"))
		Expect(reason).NotTo(ContainSubstring("container-a"))
		Expect(reason).NotTo(ContainSubstring("uid-a"))

		logValidationRejection(logr.Discard(), "update", err)
	})
})
