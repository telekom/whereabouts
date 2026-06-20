// SPDX-FileCopyrightText: 2026 Deutsche Telekom AG
//
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"encoding/json"
	"testing"
)

func FuzzLoadIPAMConfiguration(f *testing.F) {
	f.Add("192.168.2.230/24", "192.168.2.223", "", "192.168.2.1/32", "192.168.2.1", "192.168.2.10/24", "K8S_POD_NAME=pod;K8S_POD_NAMESPACE=default", false)
	f.Add("192.168.1.5-192.168.1.25/24", "", "", "", "192.168.10.1", "", "", false)
	f.Add("fd00::/120", "fd00::10", "fd00::20", "fd00::1/128", "fd00::1", "fd00::30/120", "IP=fd00::40/120;GATEWAY=fd00::1", true)
	f.Add("not-a-cidr", "bad-start", "bad-end", "also-bad", "bad-gateway", "bad-address", "IP=not-a-cidr", false)
	f.Add("10.0.0.4/31", "", "", "", "", "", "", true)

	f.Fuzz(func(t *testing.T, ipRange, rangeStart, rangeEnd, exclude, gateway, address, envArgs string, asList bool) {
		skipOversizedConfigFuzzInput(t, ipRange, rangeStart, rangeEnd, exclude, gateway, address, envArgs)

		configBytes := fuzzIPAMConfig(t, ipRange, rangeStart, rangeEnd, exclude, gateway, address, asList)
		_, _ = LoadIPAMConfiguration(configBytes, envArgs)
	})
}

func FuzzParsePrevResult(f *testing.F) {
	f.Add(`{}`)
	f.Add(`{"prevResult":{"cniVersion":"1.0.0","interfaces":[{"name":"eth0","sandbox":""}],"ips":[{"address":"10.1.0.2/24","gateway":"10.1.0.1","interface":0}],"routes":[]}}`)
	f.Add(`{"prevResult":{"cniVersion":"0.3.1","ips":[{"version":"4","address":"10.1.0.2/24","gateway":"10.1.0.1"}]}}`)
	f.Add(`{"prevResult":{"ips":[]}}`)
	f.Add(`{"prevResult":"invalid"}`)

	f.Fuzz(func(t *testing.T, configData string) {
		skipOversizedConfigFuzzInput(t, configData)

		_, _ = ParsePrevResult([]byte(configData))
	})
}

func fuzzIPAMConfig(t *testing.T, ipRange, rangeStart, rangeEnd, exclude, gateway, address string, asList bool) []byte {
	t.Helper()

	ipam := map[string]interface{}{
		"type":       "whereabouts",
		"range":      ipRange,
		"kubernetes": map[string]string{"kubeconfig": "/etc/cni/net.d/whereabouts.d/whereabouts.kubeconfig"},
	}
	if rangeStart != "" {
		ipam["range_start"] = rangeStart
	}
	if rangeEnd != "" {
		ipam["range_end"] = rangeEnd
	}
	if exclude != "" {
		ipam["exclude"] = []string{exclude}
	}
	if gateway != "" {
		ipam["gateway"] = gateway
	}
	if address != "" {
		ipam["addresses"] = []map[string]string{{"address": address}}
	}

	plugin := map[string]interface{}{
		"cniVersion": "1.0.0",
		"name":       "fuzznet",
		"type":       "ipvlan",
		"master":     "foo0",
		"ipam":       ipam,
	}

	var config interface{} = plugin
	if asList {
		config = map[string]interface{}{
			"cniVersion": "1.0.0",
			"name":       "fuzzlist",
			"plugins":    []interface{}{plugin},
		}
	}

	configBytes, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("marshal fuzz config: %v", err)
	}
	return configBytes
}

func skipOversizedConfigFuzzInput(t *testing.T, values ...string) {
	t.Helper()

	for _, value := range values {
		if len(value) > 1024 {
			t.Skip()
		}
	}
}
