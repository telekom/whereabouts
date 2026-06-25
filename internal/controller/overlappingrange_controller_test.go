// Copyright 2025 Deutsche Telekom
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	whereaboutsv1alpha1 "github.com/telekom/whereabouts/api/whereabouts.cni.cncf.io/v1alpha1"
)

type oripDeleteInterceptClient struct {
	client.Client
	beforeDeleteHook func(c client.Client)
	fired            bool
}

func (w *oripDeleteInterceptClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	if _, ok := obj.(*whereaboutsv1alpha1.OverlappingRangeIPReservation); ok && !w.fired {
		w.fired = true
		if w.beforeDeleteHook != nil {
			w.beforeDeleteHook(w.Client)
		}
	}
	deleteOptions := (&client.DeleteOptions{}).ApplyOptions(opts)
	if deleteOptions.Preconditions != nil && deleteOptions.Preconditions.UID != nil {
		var live whereaboutsv1alpha1.OverlappingRangeIPReservation
		if err := w.Client.Get(ctx, types.NamespacedName{Namespace: obj.GetNamespace(), Name: obj.GetName()}, &live); err == nil &&
			live.UID != *deleteOptions.Preconditions.UID {
			return apierrors.NewConflict(
				schema.GroupResource{Group: "whereabouts.cni.cncf.io", Resource: "overlappingrangeipreservations"},
				obj.GetName(),
				errors.New("uid precondition failed"),
			)
		}
	}
	return w.Client.Delete(ctx, obj, opts...)
}

var _ = Describe("OverlappingRangeReconciler", func() {
	const (
		resName      = "test-reservation"
		resNamespace = "default"
		interval     = 30 * time.Second
	)

	var (
		ctx        context.Context
		reconciler *OverlappingRangeReconciler
		req        ctrl.Request
	)

	BeforeEach(func() {
		ctx = context.Background()
		req = ctrl.Request{
			NamespacedName: types.NamespacedName{
				Namespace: resNamespace,
				Name:      resName,
			},
		}
	})

	buildReconciler := func(objs ...client.Object) {
		scheme := newTestScheme()
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(&whereaboutsv1alpha1.OverlappingRangeIPReservation{}).
			WithObjects(objs...).
			Build()
		fakeRecorder := events.NewFakeRecorder(10)
		go func() {
			for event := range fakeRecorder.Events {
				_ = event
			}
		}()
		reconciler = &OverlappingRangeReconciler{
			client:            fakeClient,
			recorder:          fakeRecorder,
			reconcileInterval: interval,
		}
	}

	Context("when the reservation's pod exists", func() {
		It("should not delete the reservation and requeue", func() {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-pod",
					Namespace: "default",
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
				},
			}
			reservation := &whereaboutsv1alpha1.OverlappingRangeIPReservation{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resName,
					Namespace: resNamespace,
				},
				Spec: whereaboutsv1alpha1.OverlappingRangeIPReservationSpec{
					ContainerID: "abc123",
					PodRef:      "default/my-pod",
					IfName:      "eth0",
				},
			}
			buildReconciler(reservation, pod)

			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(interval))

			// Verify reservation still exists.
			var updated whereaboutsv1alpha1.OverlappingRangeIPReservation
			Expect(reconciler.client.Get(ctx, req.NamespacedName, &updated)).To(Succeed())
		})

		It("should delete the reservation when the stored pod UID differs from the live pod UID", func() {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "reused-name",
					Namespace: "default",
					UID:       types.UID("new-pod-uid"),
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
				},
			}
			reservation := &whereaboutsv1alpha1.OverlappingRangeIPReservation{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resName,
					Namespace: resNamespace,
				},
				Spec: whereaboutsv1alpha1.OverlappingRangeIPReservationSpec{
					ContainerID: "abc123",
					PodRef:      "default/reused-name",
					PodUID:      "old-pod-uid",
					IfName:      "eth0",
				},
			}
			buildReconciler(reservation, pod)

			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			var updated whereaboutsv1alpha1.OverlappingRangeIPReservation
			err = reconciler.client.Get(ctx, req.NamespacedName, &updated)
			Expect(err).To(HaveOccurred())
			Expect(client.IgnoreNotFound(err)).To(Succeed())
		})

		DescribeTable("should delete the reservation when the pod is completed",
			func(phase corev1.PodPhase) {
				pod := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "completed-pod",
						Namespace: "default",
					},
					Status: corev1.PodStatus{
						Phase: phase,
					},
				}
				reservation := &whereaboutsv1alpha1.OverlappingRangeIPReservation{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resName,
						Namespace: resNamespace,
					},
					Spec: whereaboutsv1alpha1.OverlappingRangeIPReservationSpec{
						ContainerID: "abc123",
						PodRef:      "default/completed-pod",
						IfName:      "eth0",
					},
				}
				buildReconciler(reservation, pod)

				result, err := reconciler.Reconcile(ctx, req)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.RequeueAfter).To(BeZero())

				var updated whereaboutsv1alpha1.OverlappingRangeIPReservation
				err = reconciler.client.Get(ctx, req.NamespacedName, &updated)
				Expect(err).To(HaveOccurred())
				Expect(client.IgnoreNotFound(err)).To(Succeed())
			},
			Entry("succeeded pod", corev1.PodSucceeded),
			Entry("failed pod", corev1.PodFailed),
		)
	})

	Context("when the reservation is replaced before deletion", func() {
		It("should not delete the replacement reservation", func() {
			reservation := &whereaboutsv1alpha1.OverlappingRangeIPReservation{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resName,
					Namespace: resNamespace,
					UID:       types.UID("old-reservation-uid"),
				},
				Spec: whereaboutsv1alpha1.OverlappingRangeIPReservationSpec{
					ContainerID: "old-container",
					PodRef:      "default/gone-pod",
					IfName:      "eth0",
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(newTestScheme()).
				WithStatusSubresource(&whereaboutsv1alpha1.OverlappingRangeIPReservation{}).
				WithObjects(reservation).
				Build()
			wrappedClient := &oripDeleteInterceptClient{
				Client: fakeClient,
				beforeDeleteHook: func(c client.Client) {
					var old whereaboutsv1alpha1.OverlappingRangeIPReservation
					Expect(c.Get(ctx, req.NamespacedName, &old)).To(Succeed())
					Expect(c.Delete(ctx, &old)).To(Succeed())

					replacement := &whereaboutsv1alpha1.OverlappingRangeIPReservation{
						ObjectMeta: metav1.ObjectMeta{
							Name:      resName,
							Namespace: resNamespace,
							UID:       types.UID("new-reservation-uid"),
						},
						Spec: whereaboutsv1alpha1.OverlappingRangeIPReservationSpec{
							ContainerID: "new-container",
							PodRef:      "default/new-pod",
							IfName:      "eth0",
						},
					}
					Expect(c.Create(ctx, replacement)).To(Succeed())
				},
			}

			fakeRecorder := events.NewFakeRecorder(10)
			go func() {
				for event := range fakeRecorder.Events {
					_ = event
				}
			}()
			reconciler = &OverlappingRangeReconciler{
				client:            wrappedClient,
				recorder:          fakeRecorder,
				reconcileInterval: interval,
			}

			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).To(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			var updated whereaboutsv1alpha1.OverlappingRangeIPReservation
			Expect(fakeClient.Get(ctx, req.NamespacedName, &updated)).To(Succeed())
			Expect(updated.UID).To(Equal(types.UID("new-reservation-uid")))
			Expect(updated.Spec.PodRef).To(Equal("default/new-pod"))
		})
	})

	Context("when the reservation's pod is lost with its node", func() {
		It("should delete the reservation for PodUnknown with NodeLost reason", func() {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "lost-pod",
					Namespace: "default",
				},
				Status: corev1.PodStatus{
					Phase:  corev1.PodUnknown,
					Reason: "NodeLost",
				},
			}
			reservation := &whereaboutsv1alpha1.OverlappingRangeIPReservation{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resName,
					Namespace: resNamespace,
				},
				Spec: whereaboutsv1alpha1.OverlappingRangeIPReservationSpec{
					ContainerID: "abc123",
					PodRef:      "default/lost-pod",
					IfName:      "eth0",
				},
			}
			buildReconciler(reservation, pod)

			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			var updated whereaboutsv1alpha1.OverlappingRangeIPReservation
			err = reconciler.client.Get(ctx, req.NamespacedName, &updated)
			Expect(err).To(HaveOccurred())
			Expect(client.IgnoreNotFound(err)).To(Succeed())
		})

		It("should keep the reservation for generic PodUnknown without NodeLost reason", func() {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "unknown-pod",
					Namespace: "default",
				},
				Status: corev1.PodStatus{
					Phase:  corev1.PodUnknown,
					Reason: "StatusUnknown",
				},
			}
			reservation := &whereaboutsv1alpha1.OverlappingRangeIPReservation{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resName,
					Namespace: resNamespace,
				},
				Spec: whereaboutsv1alpha1.OverlappingRangeIPReservationSpec{
					ContainerID: "abc123",
					PodRef:      "default/unknown-pod",
					IfName:      "eth0",
				},
			}
			buildReconciler(reservation, pod)

			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(interval))

			var updated whereaboutsv1alpha1.OverlappingRangeIPReservation
			Expect(reconciler.client.Get(ctx, req.NamespacedName, &updated)).To(Succeed())
		})
	})

	Context("when the reservation's pod is missing", func() {
		It("should delete the reservation", func() {
			reservation := &whereaboutsv1alpha1.OverlappingRangeIPReservation{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resName,
					Namespace: resNamespace,
				},
				Spec: whereaboutsv1alpha1.OverlappingRangeIPReservationSpec{
					ContainerID: "abc123",
					PodRef:      "default/missing-pod",
					IfName:      "eth0",
				},
			}
			buildReconciler(reservation)

			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			// Verify reservation was deleted.
			var updated whereaboutsv1alpha1.OverlappingRangeIPReservation
			err = reconciler.client.Get(ctx, req.NamespacedName, &updated)
			Expect(err).To(HaveOccurred())
			Expect(client.IgnoreNotFound(err)).To(Succeed())
		})
	})

	Context("when the reservation is not found", func() {
		It("should return no error and no requeue", func() {
			buildReconciler() // no objects

			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())
		})
	})

	Context("when the reservation has an invalid podRef", func() {
		It("should delete the reservation", func() {
			reservation := &whereaboutsv1alpha1.OverlappingRangeIPReservation{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resName,
					Namespace: resNamespace,
				},
				Spec: whereaboutsv1alpha1.OverlappingRangeIPReservationSpec{
					ContainerID: "abc123",
					PodRef:      "invalid-no-slash",
					IfName:      "eth0",
				},
			}
			buildReconciler(reservation)

			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			// Verify reservation was deleted.
			var updated whereaboutsv1alpha1.OverlappingRangeIPReservation
			err = reconciler.client.Get(ctx, req.NamespacedName, &updated)
			Expect(err).To(HaveOccurred())
			Expect(client.IgnoreNotFound(err)).To(Succeed())
		})
	})

	Context("when the reservation has an empty podRef", func() {
		It("should requeue with reconcileInterval", func() {
			reservation := &whereaboutsv1alpha1.OverlappingRangeIPReservation{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resName,
					Namespace: resNamespace,
				},
				Spec: whereaboutsv1alpha1.OverlappingRangeIPReservationSpec{
					ContainerID: "abc123",
					PodRef:      "",
					IfName:      "eth0",
				},
			}
			buildReconciler(reservation)

			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(interval))
		})
	})
})
