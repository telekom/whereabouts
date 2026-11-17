// Copyright 2026 Deutsche Telekom
// SPDX-License-Identifier: Apache-2.0

package doc

import (
	"maps"
	"slices"
	"strings"
)

// pd is the personal-data class and data subject of a log field, as defined
// in the tcaas-psa logging contract (chapter 3). A field that can hold data of
// several subjects carries the highest: end-customer > tenant-user > operator.
type pd struct{ class, subject, content string }

var (
	notPersonal = pd{"none", "none", ""}
	podAddress  = pd{"customer-data", "end-customer", "IP address allocated to a pod"}
	oripName    = pd{"customer-data", "end-customer", "OverlappingRangeIPReservation name, derived from the pod IP address"}
	errorText   = pd{"customer-data", "end-customer", "error text; can name pod IP addresses, podRefs and pool names"}
	podRef      = pd{"tenant-pii", "tenant-user", "pod namespace and name (podRef)"}
	podID       = pd{"tenant-pii", "tenant-user", "container ID or pod UID, which identify one pod"}
	ifName      = pd{"tenant-pii", "tenant-user", "pod interface name, which the pod's network selection can set"}
	poolName    = pd{"tenant-pii", "tenant-user", "network, IPPool, node slice or lease name; contains the network name and can contain the node name"}
	nodeName    = pd{"tenant-pii", "tenant-user", "node host name, which can carry the tenant's cluster name"}
)

// personalData classifies every field of every generated log table: logr and
// slog keys, and printf arguments as written in the code. TestLoggingReference
// fails for a field that is missing here and for an entry no log call uses.
var personalData = map[string]map[string]pd{
	"cni-plugin": {
		"IPPoolName(poolIdentifier)":     poolName,
		"allocation":                     pd{"tenant-pii", "tenant-user", "node slice allocation: node name and range"},
		"args.ContainerID":               podID,
		"args.IfName":                    ifName,
		"attempt + 1":                    notPersonal,
		"attempts":                       notPersonal,
		"backoff":                        notPersonal,
		"broadcastIP":                    notPersonal,
		"c.containerID":                  podID,
		"c.ifName":                       ifName,
		"c.ip":                           podAddress,
		"c.overlapIP":                    podAddress,
		"c.podRef":                       podRef,
		"candidate.String()":             podAddress,
		"client.Config.GetPodRef()":      podRef,
		"client.ContainerID":             podID,
		"closeErr":                       notPersonal,
		"clusteripres":                   pd{"customer-data", "end-customer", "OverlappingRangeIPReservation: IP address name, podRef, interface, container ID"},
		"configuredRange":                notPersonal,
		"containerID":                    podID,
		"delErr":                         errorText,
		"delMaxRetries":                  notPersonal,
		"err":                            errorText,
		"existing.Spec.PodUID":           podID,
		"filename":                       notPersonal,
		"firstIP":                        notPersonal,
		"foundflatfile":                  notPersonal,
		"getNodeSliceName(ipam)":         poolName,
		"hostname":                       nodeName,
		"ifName":                         ifName,
		"ip":                             podAddress,
		"ip.String()":                    podAddress,
		"ipRange.Range":                  notPersonal,
		"ipam.ContainerID":               podID,
		"ipam.IfName":                    ifName,
		"ipam.Namespace":                 notPersonal,
		"ipamConf.Gateway != nil":        notPersonal,
		"ipamConf.GetPodRef()":           podRef,
		"ipamConf.NetworkName != \"\"":   notPersonal,
		"ipamConf.NodeSliceSize != \"\"": notPersonal,
		"ipamConf.OptimisticIPAM":        notPersonal,
		"ipamConf.OverlappingRanges":     notPersonal,
		"ipamConf.PodUID":                podID,
		"ipamConf.PreferredIP":           podAddress,
		"ipforoverlappingrangeupdate":    podAddress,
		"ipnet.String()":                 notPersonal,
		"j":                              notPersonal,
		"key":                            podAddress,
		"l3":                             notPersonal,
		"lastErr":                        errorText,
		"lastIP":                         notPersonal,
		"leaseName":                      poolName,
		"legacyName":                     oripName,
		"len(ipamConf.Addresses)":        notPersonal,
		"len(ipamConf.DNS.Nameservers)":  notPersonal,
		"len(ipamConf.IPRanges)":         notPersonal,
		"len(ipamConf.Routes)":           notPersonal,
		"len(newips)":                    notPersonal,
		"levelStr":                       notPersonal,
		"mode":                           notPersonal,
		"n.IPAM.Gateway":                 notPersonal,
		"n.IPAM.IPRanges":                pd{"customer-data", "end-customer", "range configuration, including pick_addresses and preferred IP addresses of pods"},
		"n.IPAM.IPRanges[idx].Range":     notPersonal,
		"networkName":                    poolName,
		"newip":                          podAddress,
		"newip.IP":                       podAddress,
		"newips":                         podAddress,
		"nodeName":                       nodeName,
		"normalizedIP":                   oripName,
		"offset":                         notPersonal,
		"operation":                      notPersonal,
		"overlappingRangeIPReservation.Spec.IfName": ifName,
		"overlappingRangeIPReservation.Spec.PodUID": podID,
		"pickAddresses":                    pd{"customer-data", "end-customer", "candidate IP addresses, which can be assigned to pods"},
		"podRef":                           podRef,
		"podUID":                           podID,
		"poolIdentifier":                   poolName,
		"poolIdentifier.IPRange":           notPersonal,
		"preferred":                        podAddress,
		"preferredIP":                      podAddress,
		"r.IP.String()":                    podAddress,
		"rangeEnd":                         notPersonal,
		"rangeStart":                       notPersonal,
		"sleepSec":                         notPersonal,
		"subnet":                           notPersonal,
		"verb":                             notPersonal,
		"whereaboutstypes.MaxSleepForRace": notPersonal,
	},
	"operator": {
		"allocIfName":       ifName,
		"allocPodRef":       podRef,
		"count":             notPersonal,
		"currentIfName":     ifName,
		"currentPodRef":     podRef,
		"err":               notPersonal,
		"error":             errorText,
		"expectedIfName":    ifName,
		"expectedPodRef":    podRef,
		"ip":                podAddress,
		"key":               notPersonal,
		"logLevel":          notPersonal,
		"name":              pd{"customer-data", "end-customer", "name of the reconciled or rejected object; an OverlappingRangeIPReservation is named after the pod IP address"},
		"namespace":         pd{"tenant-pii", "tenant-user", "namespace of the reconciled object; a NetworkAttachmentDefinition lives in a tenant namespace"},
		"node":              nodeName,
		"nodes":             notPersonal,
		"operation":         notPersonal,
		"phase":             notPersonal,
		"podRef":            podRef,
		"podUID":            podID,
		"pool":              poolName,
		"range":             notPersonal,
		"reason":            pd{"tenant-pii", "tenant-user", "pod status reason, or webhook rejection text that can quote a podRef, container ID or pod UID"},
		"resIfName":         ifName,
		"resPodRef":         podRef,
		"reservationPodUID": podID,
		"secret":            notPersonal,
		"serviceCIDR":       notPersonal,
		"sliceSize":         notPersonal,
		"slices":            notPersonal,
		"webhookConfig":     notPersonal,
	},
	"install-cni": {
		"dst":      notPersonal,
		"err":      notPersonal,
		"error":    notPersonal,
		"interval": notPersonal,
		"logLevel": notPersonal,
		"path":     notPersonal,
		"src":      notPersonal,
	},
}

// renderPersonalData lists the personal fields of one binary with the levels
// that log them. missing are fields personalData lacks, stale are entries no
// log call uses.
func renderPersonalData(component string, calls []logCall) (rows string, missing, stale []string) {
	levels := map[string][]string{}
	for _, c := range calls {
		for _, field := range c.keys {
			if !slices.Contains(levels[field], c.level) {
				levels[field] = append(levels[field], c.level)
			}
		}
	}
	classes := personalData[component]
	var b strings.Builder
	for _, field := range slices.Sorted(maps.Keys(levels)) {
		p, ok := classes[field]
		if !ok {
			missing = append(missing, field)
			continue
		}
		if p.class == "none" {
			continue
		}
		lv := levels[field]
		slices.SortFunc(lv, func(a, b string) int { return levelRank(a) - levelRank(b) })
		b.WriteString("| " + component + " | `" + strings.ReplaceAll(field, "|", `\|`) + "` | " + p.class + " | " + p.subject + " | " + strings.Join(lv, ", ") + " | " + p.content + " |\n")
	}
	for _, field := range slices.Sorted(maps.Keys(classes)) {
		if _, ok := levels[field]; !ok {
			stale = append(stale, field)
		}
	}
	return b.String(), missing, stale
}
