// Copyright 2025 Deutsche Telekom
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/telekom/whereabouts/internal/controller"
)

func newControllerCommand() *cobra.Command {
	var (
		metricsAddr          string
		healthProbeAddr      string
		leaderElect          bool
		leaderElectNamespace string
		reconcileInterval    time.Duration
	)

	cmd := &cobra.Command{
		Use:   "controller",
		Short: "Run leader-elected reconcilers for IPPool, NodeSlicePool, and OverlappingRangeIPReservation",
		RunE: func(cmd *cobra.Command, _ []string) error {
			setupLogger(cmd)
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
				LeaderElectionID:              "whereabouts-controller",
				LeaderElectionNamespace:       leaderElectNamespace,
				LeaderElectionReleaseOnCancel: true,
			})
			if err != nil {
				return err
			}

			if err := controller.SetupWithManager(mgr, reconcileInterval); err != nil {
				return err
			}

			if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
				return err
			}
			if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
				return err
			}

			log.Info("starting controller manager")
			return mgr.Start(ctrl.SetupSignalHandler())
		},
	}

	cmd.Flags().StringVar(&metricsAddr, "metrics-bind-address", ":8080", "Address the Prometheus metrics endpoint binds to")
	cmd.Flags().StringVar(&healthProbeAddr, "health-probe-bind-address", ":8081", "Address the health/readiness probes bind to")
	cmd.Flags().BoolVar(&leaderElect, "leader-elect", true, "Enable leader election for the controller manager")
	cmd.Flags().StringVar(&leaderElectNamespace, "leader-elect-namespace", "", "Namespace for leader election lease (defaults to pod namespace)")
	cmd.Flags().DurationVar(&reconcileInterval, "reconcile-interval", 30*time.Second, "Interval for periodic reconciliation of IP pools")

	return cmd
}
