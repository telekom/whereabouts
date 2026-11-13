// Copyright 2025 Deutsche Telekom
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"crypto/tls"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/spf13/cobra"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"
	ctrlwebhook "sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/telekom/whereabouts/internal/controller"
	"github.com/telekom/whereabouts/internal/webhook"
	"github.com/telekom/whereabouts/internal/webhook/certrotator"
)

// leaderElectionID is the identity used for the controller-manager leader
// election lease. Extracted as a constant so it is easy to locate and update,
// e.g. to avoid conflicts during migration from the old ip-control-loop leader
// lock.
const leaderElectionID = "whereabouts-controller.whereabouts.cni.cncf.io"

func newControllerCommand() *cobra.Command {
	var (
		metricsAddr          string
		healthProbeAddr      string
		leaderElect          bool
		leaderElectNamespace string
		reconcileInterval    time.Duration
		webhookPort          int
		certDir              string
		namespace            string
		webhookServiceName   string
		webhookSecretName    string
		webhookConfigName    string
		cleanupTerminating   bool
		cleanupDisrupted     bool
		verifyNetworkStatus  bool
		serviceCIDR          string
		webhookTLSGoDefaults bool
	)

	cmd := &cobra.Command{
		Use:   "controller",
		Short: "Run leader-elected reconcilers and validating webhooks",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := setupLogger(cmd); err != nil {
				return err
			}
			log := ctrl.Log.WithName("controller")

			cfg, err := ctrl.GetConfig()
			if err != nil {
				return fmt.Errorf("loading kubeconfig: %w", err)
			}

			mgr, err := ctrl.NewManager(cfg, ctrl.Options{
				Scheme: scheme,
				Metrics: server.Options{
					BindAddress: metricsAddr,
				},
				HealthProbeBindAddress:        healthProbeAddr,
				LeaderElection:                leaderElect,
				LeaderElectionID:              leaderElectionID,
				LeaderElectionNamespace:       leaderElectNamespace,
				LeaderElectionReleaseOnCancel: true,
				// All replicas serve webhooks; only the leader runs reconcilers.
				WebhookServer: ctrlwebhook.NewServer(ctrlwebhook.Options{
					Port:    webhookPort,
					CertDir: certDir,
					TLSOpts: webhookTLSOpts(webhookTLSGoDefaults),
				}),
			})
			if err != nil {
				return err
			}

			// Reconcilers (leader-elected).
			serviceCIDRs, err := parseServiceCIDRs(serviceCIDR)
			if err != nil {
				return fmt.Errorf("parsing --service-cidr: %w", err)
			}
			if err := controller.SetupWithManager(mgr, reconcileInterval, controller.ReconcilerOptions{
				CleanupTerminating:  cleanupTerminating,
				CleanupDisrupted:    cleanupDisrupted,
				VerifyNetworkStatus: verifyNetworkStatus,
				ServiceCIDRs:        serviceCIDRs,
			}); err != nil {
				return err
			}

			// Certificate rotation for the webhook server.
			ctx := cmd.Context()
			certReady, err := certrotator.Enable(ctx, mgr, certrotator.Options{
				Namespace:   namespace,
				CertDir:     certDir,
				DNSName:     fmt.Sprintf("%s.%s.svc", webhookServiceName, namespace),
				SecretName:  webhookSecretName,
				WebhookName: webhookConfigName,
				// CAOrganization defaults to "whereabouts" (see certrotator.Options).
			})
			if err != nil {
				return err
			}

			// Register webhooks after cert bootstrap.
			webhookReady, err := webhook.SetupWithManager(mgr, certReady)
			if err != nil {
				return err
			}

			if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
				return err
			}
			if err := mgr.AddReadyzCheck("readyz", webhookReady); err != nil {
				return err
			}

			log.Info("starting controller manager")
			return mgr.Start(ctrl.SetupSignalHandler())
		},
	}

	cmd.Flags().StringVar(&metricsAddr, "metrics-bind-address", ":8080", "Address the Prometheus metrics endpoint binds to")
	cmd.Flags().StringVar(&healthProbeAddr, "health-probe-bind-address", ":8081", "Address the health/readiness probes bind to")
	cmd.Flags().BoolVar(&leaderElect, "leader-elect", true, "Enable leader election for the controller manager")
	cmd.Flags().StringVar(&leaderElectNamespace, "leader-elect-namespace", "", "Namespace for leader election lease (when empty, controller-runtime defaults to the manager's in-cluster namespace)")
	cmd.Flags().DurationVar(&reconcileInterval, "reconcile-interval", 30*time.Second, "Interval for periodic reconciliation of IP pools")
	cmd.Flags().IntVar(&webhookPort, "webhook-port", 9443, "Port the webhook server listens on")
	cmd.Flags().StringVar(&certDir, "cert-dir", "/var/run/webhook-certs", "Directory for TLS certificates")
	cmd.Flags().StringVar(&namespace, "namespace", "", "Namespace where the operator runs (required for webhook cert DNS)")
	utilruntime.Must(cobra.MarkFlagRequired(cmd.Flags(), "namespace"))
	cmd.Flags().StringVar(&webhookServiceName, "webhook-service-name", "whereabouts-webhook-service", "Name of the webhook Service (used for TLS certificate DNS SAN)")
	cmd.Flags().StringVar(&webhookSecretName, "webhook-secret-name", "whereabouts-webhook-cert", "Name of the Secret storing webhook TLS certificates")
	cmd.Flags().StringVar(&webhookConfigName, "webhook-config-name", "whereabouts-validating-webhook-configuration", "Name of the ValidatingWebhookConfiguration to inject CA into")
	cmd.Flags().BoolVar(&cleanupTerminating, "cleanup-terminating-pods", false, "Treat terminating pods (DeletionTimestamp set) as orphaned and release their IPs immediately")
	cmd.Flags().BoolVar(&cleanupDisrupted, "cleanup-disrupted-pods", true, "Treat pods with DisruptionTarget condition (taint-manager eviction) as orphaned")
	cmd.Flags().BoolVar(&verifyNetworkStatus, "verify-network-status", true, "Verify allocated IPs against Multus network-status annotation on pods")
	cmd.Flags().BoolVar(&webhookTLSGoDefaults, "webhook-tls-go-defaults", false, "Use Go's default TLS settings for the webhook server instead of the pinned TLS 1.2 minimum and cipher list (transitional; may be removed in a future release)")
	cmd.Flags().StringVar(&serviceCIDR, "service-cidr", "", "Comma-separated list of Kubernetes service CIDR ranges for collision detection (e.g. 10.96.0.0/12)")

	return cmd
}

// webhookTLSCipherSuites are the TLS 1.2 cipher suites the webhook server
// accepts: ECDHE key exchange with AES-GCM or ChaCha20-Poly1305 only. Go does
// not let TLS 1.3 suites be configured, so TLS 1.3 keeps Go's defaults.
var webhookTLSCipherSuites = []uint16{
	tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
	tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
	tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
	tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
	tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,
	tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
}

// webhookTLSOpts pins the webhook server to TLS 1.2+ and the approved TLS 1.2
// cipher suites. goDefaults skips the pinning (transitional opt-out).
func webhookTLSOpts(goDefaults bool) []func(*tls.Config) {
	if goDefaults {
		return nil
	}
	return []func(*tls.Config){func(c *tls.Config) {
		c.MinVersion = tls.VersionTLS12
		c.CipherSuites = webhookTLSCipherSuites
	}}
}

// parseServiceCIDRs splits a comma-separated CIDR string, validates each entry, and removes empty entries.
func parseServiceCIDRs(s string) ([]string, error) {
	if s == "" {
		return nil, nil
	}
	result := make([]string, 0, len(strings.Split(s, ",")))
	for _, part := range strings.Split(s, ",") {
		cidr := strings.TrimSpace(part)
		if cidr == "" {
			continue
		}
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return nil, fmt.Errorf("invalid CIDR %q: %w", cidr, err)
		}
		result = append(result, cidr)
	}
	return result, nil
}
