// Copyright 2025 Deutsche Telekom
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"go.uber.org/zap/zapcore"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	nadv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"

	whereaboutsv1alpha1 "github.com/telekom/whereabouts/api/whereabouts.cni.cncf.io/v1alpha1"
	"github.com/telekom/whereabouts/pkg/version"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(whereaboutsv1alpha1.AddToScheme(scheme))
	utilruntime.Must(nadv1.AddToScheme(scheme))
}

func main() {
	rootCmd := newRootCommand()
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func newRootCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "whereabouts-operator",
		Short:   "Whereabouts IPAM operator",
		Version: version.GetFullVersionWithRuntimeInfo(),
	}

	cmd.PersistentFlags().String("log-level", "info", "Log level (debug, info, warn, error)")
	cmd.PersistentFlags().String("log-format", "", `Log format: json or console (default "console" for --log-level=debug, "json" otherwise)`)

	cmd.AddCommand(
		newControllerCommand(),
	)

	return cmd
}

// setupLogger installs the controller-runtime logger configured by the
// persistent --log-level and --log-format flags.
func setupLogger(cmd *cobra.Command) error {
	flags := cmd.Root().PersistentFlags()
	logLevel, err := flags.GetString("log-level")
	if err != nil {
		return fmt.Errorf("reading log-level flag: %w", err)
	}
	logFormat, err := flags.GetString("log-format")
	if err != nil {
		return fmt.Errorf("reading log-format flag: %w", err)
	}
	opts, err := loggerOptions(logLevel, logFormat)
	if err != nil {
		return err
	}
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	return nil
}

// loggerOptions maps --log-level and --log-format to zap options. An unknown
// level falls back to info (with a message on stderr), as it always did. An
// empty format selects console for debug and json otherwise, which is the
// output each level produced before --log-format existed. console is zap's
// development profile (stack traces from warn, DPanic panics); json is the
// production profile.
func loggerOptions(logLevel, logFormat string) (zap.Options, error) {
	var opts zap.Options
	switch logLevel {
	case "debug":
		opts.Level = zapcore.DebugLevel
	case "info":
		opts.Level = zapcore.InfoLevel
	case "warn":
		opts.Level = zapcore.WarnLevel
	case "error":
		opts.Level = zapcore.ErrorLevel
	default:
		fmt.Fprintf(os.Stderr, "unrecognized log-level %q, defaulting to info\n", logLevel)
		logLevel = "info"
		opts.Level = zapcore.InfoLevel
	}

	switch logFormat {
	case "":
		opts.Development = logLevel == "debug"
	case "console":
		opts.Development = true
	case "json":
		opts.Development = false
	default:
		return opts, fmt.Errorf("invalid --log-format %q: must be json or console", logFormat)
	}
	return opts, nil
}
