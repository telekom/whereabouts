//go:build integration

// SPDX-FileCopyrightText: 2026 Deutsche Telekom AG
// SPDX-License-Identifier: Apache-2.0

package certrotator

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/open-policy-agent/cert-controller/pkg/rotator"
	admissionv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	ctrlwebhook "sigs.k8s.io/controller-runtime/pkg/webhook"
)

type rotatorManager struct {
	manager.Manager
	rotator           *rotator.CertRotator
	restartsOnRefresh bool
}

func (m *rotatorManager) Add(r manager.Runnable) error {
	if cr, ok := r.(*rotator.CertRotator); ok {
		m.rotator = cr
		m.restartsOnRefresh = cr.RestartOnSecretRefresh
		// Production must restart on refresh. Disable process exit only in this
		// in-process integration test; simulate the kubelet Secret mount below.
		cr.RestartOnSecretRefresh = false
	}
	return m.Manager.Add(r)
}

func TestCertificateBootstrapAPIContract(t *testing.T) {
	assets, err := filepath.Abs("../../../bin/envtest")
	if err != nil {
		t.Fatal(err)
	}
	testEnv := &envtest.Environment{
		BinaryAssetsDirectory: assets, DownloadBinaryAssets: true, DownloadBinaryAssetsVersion: "1.37.0",
	}
	cfg, err := testEnv.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := testEnv.Stop(); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, err := client.New(cfg, client.Options{})
	if err != nil {
		t.Fatal(err)
	}
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "cert-contract-"}}
	if err := c.Create(ctx, ns); err != nil {
		t.Fatal(err)
	}
	dnsName := "whereabouts-webhook-service." + ns.Name + ".svc"
	config := &admissionv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: "cert-contract"},
		Webhooks: []admissionv1.ValidatingWebhook{{
			Name: "test.whereabouts.example", AdmissionReviewVersions: []string{"v1"},
			SideEffects:  func() *admissionv1.SideEffectClass { v := admissionv1.SideEffectClassNone; return &v }(),
			ClientConfig: admissionv1.WebhookClientConfig{Service: &admissionv1.ServiceReference{Namespace: ns.Name, Name: "whereabouts-webhook-service"}},
		}},
	}
	if err := c.Create(ctx, config); err != nil {
		t.Fatal(err)
	}
	// Keep scratch artifacts beneath the worktree, never the host temp directory.
	certDir, err := filepath.Abs("../../../bin/cert-contract")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(certDir, 0700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(certDir); err != nil {
			t.Error(err)
		}
	})
	base, err := ctrl.NewManager(cfg, ctrl.Options{
		Metrics:       metricsserver.Options{BindAddress: "0"},
		WebhookServer: ctrlwebhook.NewServer(ctrlwebhook.Options{Port: -1, CertDir: certDir}),
	})
	if err != nil {
		t.Fatal(err)
	}
	mgr := &rotatorManager{Manager: base}
	ready, err := Enable(ctx, mgr, Options{Namespace: ns.Name, SecretName: "tls", CertDir: certDir, DNSName: dnsName, WebhookName: config.Name})
	if err != nil {
		t.Fatal(err)
	}
	cr := mgr.rotator
	if cr == nil || !mgr.restartsOnRefresh || cr.NeedLeaderElection() || cr.CAName != "whereabouts-ca" || cr.CAOrganization != "whereabouts" || cr.DNSName != dnsName || len(cr.ExtraDNSNames) != 0 {
		t.Fatalf("rotation config changed: %+v", cr)
	}
	if !reflect.DeepEqual(cr.Webhooks, []rotator.WebhookInfo{{Name: config.Name, Type: rotator.Validating}}) {
		t.Fatalf("CA injection targets: %+v", cr.Webhooks)
	}
	var secret corev1.Secret
	key := types.NamespacedName{Namespace: ns.Name, Name: "tls"}
	if err := c.Get(ctx, key, &secret); err != nil {
		t.Fatal(err)
	}
	if secret.Type != corev1.SecretTypeTLS || len(secret.Data[corev1.TLSCertKey]) != 0 {
		t.Fatal("missing empty bootstrap Secret")
	}
	select {
	case <-ready:
		t.Fatal("ready before rotation/mount/CA injection")
	default:
	}
	done := make(chan error, 1)
	go func() { done <- mgr.Start(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(15 * time.Second):
			t.Error("manager did not stop")
		}
	})
	deadline := time.Now().Add(45 * time.Second)
	var mounted bool
	for time.Now().Before(deadline) {
		if err := c.Get(ctx, key, &secret); err != nil {
			t.Fatal(err)
		}
		if len(secret.Data[corev1.TLSCertKey]) > 0 {
			if err := os.WriteFile(filepath.Join(certDir, "tls.crt"), secret.Data[corev1.TLSCertKey], 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(certDir, "tls.key"), secret.Data[corev1.TLSPrivateKeyKey], 0600); err != nil {
				t.Fatal(err)
			}
			mounted = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !mounted {
		t.Fatal("rotator did not populate bootstrapped Secret")
	}
	select {
	case <-ready:
	case <-time.After(45 * time.Second):
		t.Fatal("certificate mount/CA injection never became ready")
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(config), config); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(config.Webhooks[0].ClientConfig.CABundle, secret.Data["ca.crt"]) {
		t.Fatal("injected CA differs from Secret CA")
	}
	block, _ := pem.Decode(secret.Data["ca.crt"])
	if block == nil {
		t.Fatal("invalid CA PEM")
	}
	ca, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if ca.Subject.CommonName != "whereabouts-ca" || !reflect.DeepEqual(ca.Subject.Organization, []string{"whereabouts"}) {
		t.Fatalf("changed CA identity: %+v", ca.Subject)
	}
	pair, err := tls.X509KeyPair(secret.Data[corev1.TLSCertKey], secret.Data[corev1.TLSPrivateKeyKey])
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(leaf.DNSNames, []string{dnsName}) {
		t.Fatalf("changed DNS SANs: %v", leaf.DNSNames)
	}
}
