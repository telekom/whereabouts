package main

import (
	"net"
	"os"
	"strings"
	"testing"

	cnitypes "github.com/containernetworking/cni/pkg/types"

	"github.com/telekom/whereabouts/pkg/logging"
	whereaboutstypes "github.com/telekom/whereabouts/pkg/types"
)

func TestLogIPAMConfigLoadedRedactsConfigDetails(t *testing.T) {
	logFile, err := os.CreateTemp(t.TempDir(), "whereabouts-log-*.log")
	if err != nil {
		t.Fatalf("creating temp log file: %v", err)
	}
	if err := logFile.Close(); err != nil {
		t.Fatalf("closing temp log file: %v", err)
	}

	logging.SetLogLevel("debug")
	t.Cleanup(func() {
		logging.SetLogLevel("error")
	})
	logging.SetLogFile(logFile.Name())

	cfg := &whereaboutstypes.IPAMConfig{
		IPRanges: []whereaboutstypes.RangeConfiguration{{
			Range: "10.44.0.0/24",
		}},
		Addresses: []whereaboutstypes.Address{{
			AddressStr: "10.44.0.99/24",
		}},
		Routes: []*cnitypes.Route{{Dst: mustCIDR("192.0.2.0/24")}},
		DNS: cnitypes.DNS{
			Nameservers: []string{"10.96.0.10"},
		},
		Gateway:       net.ParseIP("10.44.0.1"),
		Kubernetes:    whereaboutstypes.KubernetesConfig{KubeConfigPath: "/etc/cni/net.d/whereabouts.d/whereabouts.kubeconfig"},
		NetworkName:   "tenant-secret-network",
		NodeSliceSize: "24",
		PodName:       "sensitive-pod",
		PodNamespace:  "sensitive-namespace",
		PodUID:        "sensitive-uid",
	}

	logIPAMConfigLoaded("ADD", cfg)

	data, err := os.ReadFile(logFile.Name())
	if err != nil {
		t.Fatalf("reading log file: %v", err)
	}
	logged := string(data)

	for _, want := range []string{
		"ADD - IPAM configuration read",
		"ranges=1",
		"staticAddresses=1",
		"routes=1",
		"dnsNameservers=1",
		"gatewayConfigured=true",
		"networkNameSet=true",
		"nodeSliceEnabled=true",
	} {
		if !strings.Contains(logged, want) {
			t.Fatalf("expected log to contain %q, got: %s", want, logged)
		}
	}

	for _, sensitive := range []string{
		"10.44.0.0/24",
		"10.44.0.99",
		"10.96.0.10",
		"/etc/cni/net.d/whereabouts.d/whereabouts.kubeconfig",
		"tenant-secret-network",
		"sensitive-pod",
		"sensitive-namespace",
		"sensitive-uid",
	} {
		if strings.Contains(logged, sensitive) {
			t.Fatalf("log leaked sensitive config value %q: %s", sensitive, logged)
		}
	}
}
