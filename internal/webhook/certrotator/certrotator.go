// Copyright 2025 Deutsche Telekom
// SPDX-License-Identifier: Apache-2.0

// Package certrotator wraps cert-controller/pkg/rotator to bootstrap and
// auto-rotate self-signed TLS certificates for the webhook server.
package certrotator

import (
	"github.com/open-policy-agent/cert-controller/pkg/rotator"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

// Options configures the certificate rotator.
type Options struct {
	// Namespace where the webhook secret and service live.
	Namespace string
	// CertDir is the directory to write TLS cert/key files to.
	CertDir string
	// DNSName is the SAN for the generated certificate.
	DNSName string
	// SecretName is the Kubernetes Secret holding the TLS cert/key pair.
	SecretName string
	// WebhookName is the ValidatingWebhookConfiguration resource name.
	WebhookName string
	// IsReady is closed when the initial certificate has been provisioned.
	IsReady chan struct{}
}

//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch
//+kubebuilder:rbac:groups=admissionregistration.k8s.io,resources=validatingwebhookconfigurations,verbs=get;list;watch;update;patch

// Enable adds a certificate rotator runnable to the manager.
func Enable(mgr manager.Manager, opts Options) error {
	log := ctrl.Log.WithName("certrotator")
	log.Info("enabling certificate rotation",
		"namespace", opts.Namespace,
		"secret", opts.SecretName,
		"webhookConfig", opts.WebhookName,
	)

	return rotator.AddRotator(mgr, &rotator.CertRotator{
		SecretKey: types.NamespacedName{
			Namespace: opts.Namespace,
			Name:      opts.SecretName,
		},
		CertDir:        opts.CertDir,
		CAName:         "whereabouts-ca",
		CAOrganization: "telekom",
		DNSName:        opts.DNSName,
		IsReady:        opts.IsReady,
		Webhooks: []rotator.WebhookInfo{
			{
				Name: opts.WebhookName,
				Type: rotator.Validating,
			},
		},
		RequireLeaderElection:  false,
		RestartOnSecretRefresh: true,
	})
}
