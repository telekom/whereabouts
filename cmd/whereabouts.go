// Package main contains the beginning of the whereabouts cmd
package main

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/containernetworking/cni/pkg/skel"
	cnitypes "github.com/containernetworking/cni/pkg/types"
	current "github.com/containernetworking/cni/pkg/types/100"
	cniversion "github.com/containernetworking/cni/pkg/version"

	"github.com/telekom/whereabouts/pkg/config"
	"github.com/telekom/whereabouts/pkg/logging"
	"github.com/telekom/whereabouts/pkg/storage"
	"github.com/telekom/whereabouts/pkg/storage/kubernetes"
	"github.com/telekom/whereabouts/pkg/types"
	"github.com/telekom/whereabouts/pkg/version"
)

func cmdAddFunc(args *skel.CmdArgs) error {
	ipamConf, confVersion, err := config.LoadIPAMConfig(args.StdinData, args.Args)
	if err != nil {
		return logging.Errorf("IPAM configuration load failed: %s", err)
	}
	logging.Debugf("ADD - IPAM configuration successfully read: %+v", *ipamConf)
	ipam, err := kubernetes.NewKubernetesIPAM(args.ContainerID, args.IfName, *ipamConf)
	if err != nil {
		return logging.Errorf("failed to create Kubernetes IPAM manager: %v", err)
	}
	defer func() { safeCloseKubernetesBackendConnection(ipam) }()

	logging.Debugf("Beginning IPAM for ContainerID: %q - podRef: %q - ifName: %q", args.ContainerID, ipamConf.GetPodRef(), args.IfName)
	return cmdAdd(ipam, confVersion)
}

const (
	// delMaxRetries is the number of times to retry a failed CNI DEL before giving up.
	delMaxRetries = 3
	// delInitialBackoff is the initial backoff duration between DEL retries.
	delInitialBackoff = 1 * time.Second
)

func cmdDelFunc(args *skel.CmdArgs) error {
	ipamConf, _, err := config.LoadIPAMConfig(args.StdinData, args.Args)
	if err != nil {
		// CNI spec: DEL should be lenient about missing/invalid config.
		// Log the error but do not return it — the container is already gone.
		logging.Errorf("IPAM configuration load failed (DEL tolerant): %s", err)
		return nil
	}
	logging.Debugf("DEL - IPAM configuration successfully read: %+v", *ipamConf)

	var lastErr error
	backoff := delInitialBackoff
	for attempt := 0; attempt < delMaxRetries; attempt++ {
		if attempt > 0 {
			logging.Debugf("Retrying DEL (attempt %d/%d) after %s", attempt+1, delMaxRetries, backoff)
			time.Sleep(backoff)
			backoff *= 2
		}

		ipam, err := kubernetes.NewKubernetesIPAM(args.ContainerID, args.IfName, *ipamConf)
		if err != nil {
			lastErr = err
			logging.Errorf("IPAM client initialization error (attempt %d/%d): %v", attempt+1, delMaxRetries, err)
			continue
		}

		logging.Debugf("Beginning delete for ContainerID: %q - podRef: %q - ifName: %q", args.ContainerID, ipamConf.GetPodRef(), args.IfName)
		lastErr = cmdDel(ipam)
		safeCloseKubernetesBackendConnection(ipam)
		if lastErr == nil {
			return nil
		}
		logging.Errorf("DEL attempt %d/%d failed: %v", attempt+1, delMaxRetries, lastErr)
	}

	// All retries exhausted — CNI spec requires DEL to be idempotent.
	// Log the error but return nil to avoid blocking pod deletion.
	_ = logging.Errorf("DEL failed after %d attempts: %s", delMaxRetries, lastErr)
	return nil
}

func main() {
	skel.PluginMainFuncs(skel.CNIFuncs{
		Add:   cmdAddFunc,
		Check: cmdCheck,
		Del:   cmdDelFunc,
	},
		cniversion.All,
		fmt.Sprintf("whereabouts %s", version.GetFullVersionWithRuntimeInfo()))
}

func safeCloseKubernetesBackendConnection(ipam *kubernetes.KubernetesIPAM) {
	if err := ipam.Close(); err != nil {
		_ = logging.Errorf("failed to close the connection to the K8s backend: %v", err)
	}
}

func cmdCheck(args *skel.CmdArgs) error {
	ipamConf, _, err := config.LoadIPAMConfig(args.StdinData, args.Args)
	if err != nil {
		return logging.Errorf("IPAM configuration load failed: %s", err)
	}
	logging.Debugf("CHECK - IPAM configuration successfully read: %+v", *ipamConf)

	ipam, err := kubernetes.NewKubernetesIPAM(args.ContainerID, args.IfName, *ipamConf)
	if err != nil {
		return logging.Errorf("failed to create Kubernetes IPAM manager: %v", err)
	}
	defer func() { safeCloseKubernetesBackendConnection(ipam) }()

	ctx, cancel := context.WithTimeout(context.Background(), types.AddTimeLimit)
	defer cancel()

	// Verify an allocation exists for this container in every configured IP range.
	for _, ipRange := range ipamConf.IPRanges {
		poolIdentifier := kubernetes.PoolIdentifier{IpRange: ipRange.Range, NetworkName: ipamConf.NetworkName}
		pool, err := ipam.GetIPPool(ctx, poolIdentifier)
		if err != nil {
			if e, ok := err.(storage.Temporary); ok && e.Temporary() {
				return logging.Errorf("CHECK: transient error reading pool %s: %s", ipRange.Range, err)
			}
			return logging.Errorf("CHECK: error reading pool %s: %s", ipRange.Range, err)
		}

		found := false
		for _, alloc := range pool.Allocations() {
			if alloc.ContainerID == args.ContainerID && alloc.IfName == args.IfName {
				found = true
				break
			}
		}
		if !found {
			return logging.Errorf("CHECK: no allocation found for containerID %q ifName %q in range %s",
				args.ContainerID, args.IfName, ipRange.Range)
		}
		logging.Debugf("CHECK: allocation verified for containerID %q ifName %q in range %s",
			args.ContainerID, args.IfName, ipRange.Range)
	}

	return nil
}

func cmdAdd(client *kubernetes.KubernetesIPAM, cniVersion string) error {
	// Initialize our result, and assign DNS & routing.
	result := &current.Result{}
	result.DNS = client.Config.DNS
	result.Routes = client.Config.Routes

	var newips []net.IPNet

	ctx, cancel := context.WithTimeout(context.Background(), types.AddTimeLimit)
	defer cancel()

	newips, err := kubernetes.IPManagement(ctx, types.Allocate, client.Config, client)
	if err != nil {
		return logging.Errorf("error at storage engine: %s", err)
	}

	for _, newip := range newips {
		result.IPs = append(result.IPs, &current.IPConfig{
			Address: newip,
			Gateway: client.Config.Gateway})
	}

	// Assign all the static IP elements.
	for _, v := range client.Config.Addresses {
		result.IPs = append(result.IPs, &current.IPConfig{
			Address: v.Address,
			Gateway: v.Gateway})
	}

	if len(result.IPs) == 0 {
		return fmt.Errorf("no IP addresses allocated — check IPAM configuration (ipRanges may be empty)")
	}

	return cnitypes.PrintResult(result, cniVersion)
}

func cmdDel(client *kubernetes.KubernetesIPAM) error {
	ctx, cancel := context.WithTimeout(context.Background(), types.DelTimeLimit)
	defer cancel()

	_, err := kubernetes.IPManagement(ctx, types.Deallocate, client.Config, client)
	if err != nil {
		return logging.Errorf("error deallocating IP: %s", err)
	}
	return nil
}
