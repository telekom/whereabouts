// Copyright 2025 Deutsche Telekom
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"

	"github.com/spf13/cobra"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"
	ctrlwebhook "sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/telekom/whereabouts/internal/webhook"
	"github.com/telekom/whereabouts/internal/webhook/certrotator"
)

func newWebhookCommand() *cobra.Command {
	var (
		metricsAddr     string
		healthProbeAddr string
		webhookPort     int
		certDir         string
		namespace       string
	)

	cmd := &cobra.Command{
		Use:   "webhook",
		Short: "Run validating admission webhooks with self-signed cert rotation",
		RunE: func(cmd *cobra.Command, _ []string) error {
			setupLogger(cmd)
			log := ctrl.Log.WithName("webhook")

			if namespace == "" {
				return fmt.Errorf("--namespace is required")
			}

			mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
				Scheme: scheme,
				Metrics: server.Options{
					BindAddress: metricsAddr,
				},
				HealthProbeBindAddress: healthProbeAddr,
				// No leader election — webhook serving is stateless.
				LeaderElection: false,
				WebhookServer: ctrlwebhook.NewServer(ctrlwebhook.Options{
					Port:    webhookPort,
					CertDir: certDir,
				}),
			})
			if err != nil {
				return err
			}

			// Set up certificate rotation; blocks webhook registration until
			// certs are ready.
			certReady := make(chan struct{})
			ctx := cmd.Context()
			if err := certrotator.Enable(ctx, mgr, certrotator.Options{
				Namespace:   namespace,
				CertDir:     certDir,
				DNSName:     fmt.Sprintf("whereabouts-webhook.%s.svc", namespace),
				SecretName:  "whereabouts-webhook-cert",
				WebhookName: "whereabouts-validating-webhooks",
				IsReady:     certReady,
			}); err != nil {
				return err
			}

			// Register webhooks after cert bootstrap.
			webhookSetup := webhook.NewSetup(mgr, certReady)
			if err := mgr.Add(webhookSetup); err != nil {
				return err
			}

			if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
				return err
			}
			if err := mgr.AddReadyzCheck("readyz", webhookSetup.ReadyCheck()); err != nil {
				return err
			}

			log.Info("starting webhook server")
			return mgr.Start(ctrl.SetupSignalHandler())
		},
	}

	cmd.Flags().StringVar(&metricsAddr, "metrics-bind-address", ":8082", "Address the Prometheus metrics endpoint binds to")
	cmd.Flags().StringVar(&healthProbeAddr, "health-probe-bind-address", ":8083", "Address the health/readiness probes bind to")
	cmd.Flags().IntVar(&webhookPort, "webhook-port", 9443, "Port the webhook server listens on")
	cmd.Flags().StringVar(&certDir, "cert-dir", "/tmp/k8s-webhook-server/serving-certs", "Directory for TLS certificates")
	cmd.Flags().StringVar(&namespace, "namespace", "", "Namespace where the webhook service runs (required)")

	return cmd
}
