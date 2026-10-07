// SPDX-FileCopyrightText: 2026 Deutsche Telekom AG
// SPDX-License-Identifier: Apache-2.0

package certrotator

import (
	"context"
	"errors"
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestSecretBootstrapContract(t *testing.T) {
	key := types.NamespacedName{Namespace: "default", Name: "tls"}
	for _, scenario := range []string{"create", "existing", "create-race", "forbidden-read", "forbidden-create", "canceled"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if scenario == "canceled" {
				cancel()
			}
			existing := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace, Labels: map[string]string{"owner": "other"}},
				Type:       corev1.SecretTypeOpaque, Data: map[string][]byte{"ca.crt": []byte("ca"), "tls.crt": []byte("cert"), "tls.key": []byte("key")},
			}
			reads, creates, updates, patches := 0, 0, 0, 0
			builder := fake.NewClientBuilder().WithScheme(newTestScheme())
			if scenario == "existing" {
				builder.WithObjects(existing)
			}
			forbidden := apierrors.NewForbidden(schema.GroupResource{Resource: "secrets"}, key.Name, errors.New("denied"))
			c := builder.WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					reads++
					if ctx.Err() != nil {
						return ctx.Err()
					}
					if scenario == "forbidden-read" {
						return forbidden
					}
					return c.Get(ctx, key, obj, opts...)
				},
				Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
					creates++
					if scenario == "forbidden-create" {
						return forbidden
					}
					if scenario == "create-race" {
						if err := c.Create(ctx, existing); err != nil {
							return err
						}
					}
					return c.Create(ctx, obj, opts...)
				},
				Update: func(context.Context, client.WithWatch, client.Object, ...client.UpdateOption) error {
					updates++
					return nil
				},
				Patch: func(context.Context, client.WithWatch, client.Object, client.Patch, ...client.PatchOption) error {
					patches++
					return nil
				},
			}).Build()
			err := ensureSecret(ctx, c, key)
			switch scenario {
			case "forbidden-read", "forbidden-create":
				if !apierrors.IsForbidden(err) {
					t.Fatalf("lost forbidden error: %v", err)
				}
			case "canceled":
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("lost cancellation: %v", err)
				}
			default:
				if err != nil {
					t.Fatal(err)
				}
			}
			wantCreates := 1
			if scenario == "existing" || scenario == "forbidden-read" || scenario == "canceled" {
				wantCreates = 0
			}
			if reads != 1 || creates != wantCreates || updates != 0 || patches != 0 {
				t.Fatalf("calls: get=%d create=%d update=%d patch=%d", reads, creates, updates, patches)
			}
			if scenario == "existing" || scenario == "create-race" {
				var stored corev1.Secret
				if err := c.Get(context.Background(), key, &stored); err != nil {
					t.Fatal(err)
				}
				if stored.Type != existing.Type || !reflect.DeepEqual(stored.Data, existing.Data) || !reflect.DeepEqual(stored.Labels, existing.Labels) {
					t.Fatal("bootstrap overwrote existing Secret")
				}
			}
		})
	}
}
