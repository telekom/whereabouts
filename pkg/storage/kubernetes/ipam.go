package kubernetes

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"

	"gomodules.xyz/jsonpatch/v2"

	whereaboutsv1alpha1 "github.com/telekom/whereabouts/api/whereabouts.cni.cncf.io/v1alpha1"
	"github.com/telekom/whereabouts/pkg/allocate"
	wbclient "github.com/telekom/whereabouts/pkg/generated/clientset/versioned"
	"github.com/telekom/whereabouts/pkg/iphelpers"
	"github.com/telekom/whereabouts/pkg/logging"
	"github.com/telekom/whereabouts/pkg/storage"
	whereaboutstypes "github.com/telekom/whereabouts/pkg/types"
)

const UnnamedNetwork string = ""

const (
	// retryInitialBackoff is the starting backoff duration between retries.
	retryInitialBackoff = 5 * time.Millisecond
	// retryMaxBackoff caps the exponential backoff to avoid excessive waits.
	retryMaxBackoff = 1 * time.Second
)

// retryBackoff sleeps for a jittered duration, respecting context cancellation.
func retryBackoff(ctx context.Context, d time.Duration) {
	// Apply ±50% jitter: sleep for d/2 + rand(d/2)
	half := int64(d / 2)
	if half <= 0 {
		half = 1
	}
	n, _ := rand.Int(rand.Reader, big.NewInt(half))
	jittered := time.Duration(half + n.Int64())
	select {
	case <-time.After(jittered):
	case <-ctx.Done():
	}
}

// KubernetesIPAM manages IP address blocks using Kubernetes CRDs as the
// storage backend. It embeds Client for API access and carries the per-request
// context needed to perform allocations and deallocations.
type KubernetesIPAM struct {
	// Client provides access to the Kubernetes and Whereabouts API clients.
	Client
	// Config is the parsed IPAM configuration for this allocation request.
	Config whereaboutstypes.IPAMConfig
	// Namespace is the Kubernetes namespace where IPPool CRDs are stored.
	Namespace string
	// ContainerID is the CNI container ID for the current request.
	ContainerID string
	// IfName is the network interface name inside the container.
	IfName string
}

func newKubernetesIPAM(containerID, ifName string, ipamConf whereaboutstypes.IPAMConfig, namespace string, kubernetesClient Client) *KubernetesIPAM {
	return &KubernetesIPAM{
		Config:      ipamConf,
		ContainerID: containerID,
		IfName:      ifName,
		Namespace:   namespace,
		Client:      kubernetesClient,
	}
}

// NewKubernetesIPAM returns a new KubernetesIPAM Client configured to a kubernetes CRD backend.
func NewKubernetesIPAM(containerID, ifName string, ipamConf whereaboutstypes.IPAMConfig) (*KubernetesIPAM, error) {
	var namespace string
	if cfg, err := clientcmd.LoadFromFile(ipamConf.Kubernetes.KubeConfigPath); err != nil {
		return nil, err
	} else if ctx, ok := cfg.Contexts[cfg.CurrentContext]; ok && ctx != nil {
		namespace = wbNamespaceFromCtx(ctx)
	} else {
		return nil, fmt.Errorf("k8s config: namespace not present in context")
	}

	kubernetesClient, err := NewClientViaKubeconfig(ipamConf.Kubernetes.KubeConfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed instantiating kubernetes client: %w", err)
	}
	k8sIPAM := newKubernetesIPAM(containerID, ifName, ipamConf, namespace, *kubernetesClient)
	return k8sIPAM, nil
}

// NewKubernetesIPAMWithNamespace returns a new KubernetesIPAM Client configured to a kubernetes CRD backend.
func NewKubernetesIPAMWithNamespace(containerID, ifName string, ipamConf whereaboutstypes.IPAMConfig, namespace string) (*KubernetesIPAM, error) {
	k8sIPAM, err := NewKubernetesIPAM(containerID, ifName, ipamConf)
	if err != nil {
		return nil, err
	}
	k8sIPAM.Namespace = namespace
	return k8sIPAM, nil
}

type PoolIdentifier struct {
	IPRange     string
	NetworkName string
	NodeName    string
}

// GetIPPool returns a storage.IPPool for the given range.
func (i *KubernetesIPAM) GetIPPool(ctx context.Context, poolIdentifier PoolIdentifier) (storage.IPPool, error) {
	name := IPPoolName(poolIdentifier)

	pool, err := i.getPool(ctx, name, poolIdentifier.IPRange)
	if err != nil {
		return nil, err
	}

	firstIP, _, err := pool.ParseCIDR()
	if err != nil {
		return nil, err
	}

	return &KubernetesIPPool{i.client, firstIP, pool}, nil
}

// GetExistingIPPool returns an existing storage.IPPool for read or cleanup
// paths. Unlike GetIPPool, it never creates a missing IPPool.
func (i *KubernetesIPAM) GetExistingIPPool(ctx context.Context, poolIdentifier PoolIdentifier) (storage.IPPool, error) {
	name := IPPoolName(poolIdentifier)

	pool, err := i.getExistingPool(ctx, name)
	if err != nil {
		return nil, err
	}

	firstIP, _, err := pool.ParseCIDR()
	if err != nil {
		return nil, err
	}

	return &KubernetesIPPool{i.client, firstIP, pool}, nil
}

func IPPoolName(poolIdentifier PoolIdentifier) string {
	if poolIdentifier.NodeName != "" {
		// fast node range naming convention
		if poolIdentifier.NetworkName == UnnamedNetwork {
			return fmt.Sprintf("%v-%v", poolIdentifier.NodeName, normalizeRange(poolIdentifier.IPRange))
		}
		return fmt.Sprintf("%v-%v-%v", poolIdentifier.NetworkName, poolIdentifier.NodeName, normalizeRange(poolIdentifier.IPRange))
	}

	// default naming convention
	if poolIdentifier.NetworkName == UnnamedNetwork {
		return normalizeRange(poolIdentifier.IPRange)
	}
	return fmt.Sprintf("%s-%s", poolIdentifier.NetworkName, normalizeRange(poolIdentifier.IPRange))
}

// EffectivePoolIdentifier returns the concrete IPPool identifier used for the
// configured range. Fast IPAM stores allocations in the current node's slice
// pool instead of the parent configured range.
func EffectivePoolIdentifier(ctx context.Context, ipam *KubernetesIPAM, configuredRange string) (PoolIdentifier, error) {
	poolIdentifier := PoolIdentifier{IPRange: configuredRange, NetworkName: ipam.Config.NetworkName}
	if ipam.Config.NodeSliceSize == "" {
		return poolIdentifier, nil
	}

	hostname, err := getNodeName(ipam)
	if err != nil {
		return poolIdentifier, err
	}
	nodeSliceRange, err := GetNodeSlicePoolRange(ctx, ipam, hostname)
	if err != nil {
		return poolIdentifier, err
	}

	poolIdentifier.NodeName = hostname
	poolIdentifier.IPRange = nodeSliceRange
	return poolIdentifier, nil
}

// normalizeRange converts an IP range CIDR into a string suitable for use as a
// Kubernetes resource name. Colons (IPv6) and slashes (CIDR notation) are
// replaced with dashes because metadata.name must match RFC 1123 DNS subdomain.
func normalizeRange(ipRange string) string {
	return iphelpers.NormalizeRangeForResourceName(ipRange)
}

func (i *KubernetesIPAM) getExistingPool(ctx context.Context, name string) (*whereaboutsv1alpha1.IPPool, error) {
	ctxWithTimeout, cancel := context.WithTimeout(ctx, storage.RequestTimeout)
	defer cancel()

	pool, err := i.client.WhereaboutsV1alpha1().IPPools(i.Namespace).Get(ctxWithTimeout, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("k8s get error: %w", err)
	}
	return pool, nil
}

func (i *KubernetesIPAM) getPool(ctx context.Context, name string, iprange string) (*whereaboutsv1alpha1.IPPool, error) {
	ctxWithTimeout, cancel := context.WithTimeout(ctx, storage.RequestTimeout)
	defer cancel()

	pool, err := i.client.WhereaboutsV1alpha1().IPPools(i.Namespace).Get(ctxWithTimeout, name, metav1.GetOptions{})
	if err != nil && apierrors.IsNotFound(err) {
		// pool does not exist, create it
		newPool := &whereaboutsv1alpha1.IPPool{}
		newPool.Name = name
		newPool.Spec.Range = iprange
		newPool.Spec.Allocations = make(map[string]whereaboutsv1alpha1.IPAllocation)
		_, err = i.client.WhereaboutsV1alpha1().IPPools(i.Namespace).Create(ctxWithTimeout, newPool, metav1.CreateOptions{})
		if err != nil && apierrors.IsAlreadyExists(err) {
			// the pool was just created -- allow retry
			return nil, &temporaryError{err}
		} else if err != nil {
			return nil, fmt.Errorf("k8s create error: %w", err)
		}
		// if the pool was created for the first time, trigger another retry of the allocation loop
		// so all of the metadata / resourceVersions are populated as necessary by the `client.Get` call
		return nil, &temporaryError{ErrPoolInitialized}
	} else if err != nil {
		return nil, fmt.Errorf("k8s get error: %w", err)
	}
	return pool, nil
}

// Status tests connectivity to the kubernetes backend.


// Close cleans up the IPAM client.
func (i *KubernetesIPAM) Close() error {
	return nil
}

// KubernetesIPPool represents an IPPool resource and its parsed set of allocations.
type KubernetesIPPool struct {
	client  wbclient.Interface
	firstIP net.IP
	pool    *whereaboutsv1alpha1.IPPool
}

// Allocations returns the initially retrieved set of allocations for this pool.
func (p *KubernetesIPPool) Allocations() []whereaboutstypes.IPReservation {
	return toIPReservationList(p.pool.Spec.Allocations, p.firstIP)
}

// Update sets the pool allocated IP list to the given IP reservations.
func (p *KubernetesIPPool) Update(ctx context.Context, reservations []whereaboutstypes.IPReservation) error {
	// marshal the current pool to serve as the base for the patch creation
	orig := p.pool.DeepCopy()
	origBytes, err := json.Marshal(orig)
	if err != nil {
		return err
	}

	// update the pool before marshaling once again
	allocations, err := toAllocationMap(reservations, p.firstIP)
	if err != nil {
		return err
	}
	p.pool.Spec.Allocations = allocations
	modBytes, err := json.Marshal(p.pool)
	if err != nil {
		return err
	}

	// create the patch
	patch, err := jsonpatch.CreatePatch(origBytes, modBytes)
	if err != nil {
		return err
	}

	// add additional tests to the patch
	ops := []jsonpatch.Operation{
		// ensure patch is applied to appropriate resource version only
		{Operation: "test", Path: "/metadata/resourceVersion", Value: orig.ResourceVersion},
	}
	for _, o := range patch {
		// safeguard add ops -- "add" will update existing paths, this "test" ensures the path is empty
		if o.Operation == "add" {
			var m map[string]any
			ops = append(ops, jsonpatch.Operation{Operation: "test", Path: o.Path, Value: m})
		}
	}
	ops = append(ops, patch...)
	patchData, err := json.Marshal(ops)
	if err != nil {
		return err
	}

	// apply the patch
	_, err = p.client.WhereaboutsV1alpha1().IPPools(orig.GetNamespace()).Patch(ctx, orig.GetName(), types.JSONPatchType, patchData, metav1.PatchOptions{})
	if err != nil {
		if apierrors.IsInvalid(err) {
			return &temporaryError{err}
		}
		if apierrors.IsConflict(err) {
			return &temporaryError{err}
		}
		return err
	}

	return nil
}

func toIPReservationList(allocations map[string]whereaboutsv1alpha1.IPAllocation, firstip net.IP) []whereaboutstypes.IPReservation {
	reservelist := []whereaboutstypes.IPReservation{}
	for offset, a := range allocations {
		numOffset, ok := new(big.Int).SetString(offset, 10)
		if !ok || numOffset.Sign() < 0 {
			// allocations that are not valid non-negative integers should be ignored
			logging.Errorf("Error decoding ip offset (backend: kubernetes): invalid offset %q", offset)
			continue
		}
		ip := iphelpers.IPAddOffset(firstip, numOffset)
		reservelist = append(reservelist, whereaboutstypes.IPReservation{IP: ip, ContainerID: a.ContainerID, PodRef: a.PodRef, IfName: a.IfName})
	}
	return reservelist
}

func toAllocationMap(reservelist []whereaboutstypes.IPReservation, firstip net.IP) (map[string]whereaboutsv1alpha1.IPAllocation, error) {
	allocations := make(map[string]whereaboutsv1alpha1.IPAllocation)
	for _, r := range reservelist {
		index, err := iphelpers.IPGetOffset(r.IP, firstip)
		if err != nil {
			return nil, err
		}
		allocations[index.String()] = whereaboutsv1alpha1.IPAllocation{ContainerID: r.ContainerID, PodRef: r.PodRef, IfName: r.IfName}
	}
	return allocations, nil
}

// KubernetesOverlappingRangeStore represents a OverlappingRangeStore interface.
type KubernetesOverlappingRangeStore struct {
	client    wbclient.Interface
	namespace string
}

// GetOverlappingRangeStore returns a clusterstore interface.
func (i *KubernetesIPAM) GetOverlappingRangeStore() (storage.OverlappingRangeStore, error) {
	return &KubernetesOverlappingRangeStore{i.client, i.Namespace}, nil
}

// IsAllocatedInOverlappingRange checks for IP addresses to see if they're allocated cluster wide, for overlapping
// ranges. First return value is true if the IP is allocated, second return value is true if the IP is allocated to the
// current podRef.
func (c *KubernetesOverlappingRangeStore) GetOverlappingRangeIPReservation(ctx context.Context, ip net.IP,
	_ /* podRef */, networkName string) (*whereaboutsv1alpha1.OverlappingRangeIPReservation, error) {
	normalizedIP := NormalizeIP(ip, networkName)

	logging.Debugf("Get overlappingRangewide allocation; normalized IP: %q, IP: %q, networkName: %q",
		normalizedIP, ip, networkName)

	r, err := c.client.WhereaboutsV1alpha1().OverlappingRangeIPReservations(c.namespace).Get(ctx, normalizedIP, metav1.GetOptions{})
	if err != nil && apierrors.IsNotFound(err) {
		// New-format name not found — try legacy name for backward compatibility with
		// OverlappingRangeIPReservation CRs created before the IPv6 expansion change.
		legacyName := LegacyNormalizeIP(ip, networkName)
		if legacyName != normalizedIP {
			logging.Debugf("New-format name %q not found, trying legacy name %q", normalizedIP, legacyName)
			r, err = c.client.WhereaboutsV1alpha1().OverlappingRangeIPReservations(c.namespace).Get(ctx, legacyName, metav1.GetOptions{})
			if err != nil && apierrors.IsNotFound(err) {
				// Neither format found — IP is not reserved.
				return nil, nil
			} else if err != nil {
				logging.Errorf("k8s get OverlappingRangeIPReservation error (legacy): %w", err)
				return nil, fmt.Errorf("k8s get OverlappingRangeIPReservation error: %w", err)
			}
			logging.Debugf("Legacy-format name %q is reserved; IP: %q, networkName: %q", legacyName, ip, networkName)
			return r, nil
		}
		// cluster ip reservation does not exist, this appears to be good news.
		return nil, nil
	} else if err != nil {
		logging.Errorf("k8s get OverlappingRangeIPReservation error: %w", err)
		return nil, fmt.Errorf("k8s get OverlappingRangeIPReservation error: %w", err)
	}

	logging.Debugf("Normalized IP is reserved; normalized IP: %q, IP: %q, networkName: %q",
		normalizedIP, ip, networkName)
	return r, nil
}

// UpdateOverlappingRangeAllocation updates clusterwide allocation for overlapping ranges.
func (c *KubernetesOverlappingRangeStore) UpdateOverlappingRangeAllocation(ctx context.Context, mode int, ip net.IP,
	podRef, ifName, networkName, podUID string) error {
	normalizedIP := NormalizeIP(ip, networkName)

	clusteripres := &whereaboutsv1alpha1.OverlappingRangeIPReservation{
		ObjectMeta: metav1.ObjectMeta{Name: normalizedIP, Namespace: c.namespace},
	}

	var err error
	var verb string
	switch mode {
	case whereaboutstypes.Allocate:
		// Put together our cluster ip reservation
		verb = "allocate"

		clusteripres.Spec = whereaboutsv1alpha1.OverlappingRangeIPReservationSpec{
			PodRef: podRef,
			IfName: ifName,
			PodUID: podUID,
		}

		_, err = c.client.WhereaboutsV1alpha1().OverlappingRangeIPReservations(c.namespace).Create(
			ctx, clusteripres, metav1.CreateOptions{})
		if apierrors.IsAlreadyExists(err) {
			return &temporaryError{fmt.Errorf("ORIP %s already exists (concurrent allocation): %w", normalizedIP, err)}
		}

	case whereaboutstypes.Deallocate:
		verb = "deallocate"

		if podUID != "" {
			existing, getErr := c.client.WhereaboutsV1alpha1().OverlappingRangeIPReservations(c.namespace).Get(ctx, normalizedIP, metav1.GetOptions{})
			if getErr != nil && !apierrors.IsNotFound(getErr) {
				return fmt.Errorf("k8s get OverlappingRangeIPReservation error: %w", getErr)
			}
			if getErr == nil && existing.Spec.PodUID != "" && existing.Spec.PodUID != podUID {
				logging.Debugf("Skipping delete of ORIP %q: stored UID %q differs from caller UID %q (stale reservation of another pod lifecycle)",
					normalizedIP, existing.Spec.PodUID, podUID)
				return nil
			}
		}

		err = c.client.WhereaboutsV1alpha1().OverlappingRangeIPReservations(c.namespace).Delete(ctx, clusteripres.GetName(), metav1.DeleteOptions{})
		if apierrors.IsNotFound(err) {
			// New-format name not found — attempt deletion by legacy name for backward
			// compatibility with CRs created before the IPv6 expansion change.
			legacyName := LegacyNormalizeIP(ip, networkName)
			if legacyName != normalizedIP {
				logging.Debugf("New-format name %q not found on delete, trying legacy name %q", normalizedIP, legacyName)
				if podUID != "" {
					existing, getErr := c.client.WhereaboutsV1alpha1().OverlappingRangeIPReservations(c.namespace).Get(ctx, legacyName, metav1.GetOptions{})
					if getErr != nil && !apierrors.IsNotFound(getErr) {
						return fmt.Errorf("k8s get legacy OverlappingRangeIPReservation error: %w", getErr)
					}
					if getErr == nil && existing.Spec.PodUID != "" && existing.Spec.PodUID != podUID {
						logging.Debugf("Skipping delete of legacy ORIP %q: stored UID %q differs from caller UID %q (stale reservation of another pod lifecycle)",
							legacyName, existing.Spec.PodUID, podUID)
						return nil
					}
				}
				err = c.client.WhereaboutsV1alpha1().OverlappingRangeIPReservations(c.namespace).Delete(ctx, legacyName, metav1.DeleteOptions{})
				if apierrors.IsNotFound(err) {
					err = nil
				}
			} else {
				err = nil
			}
		}
	}

	if err != nil {
		return err
	}

	logging.Debugf("K8s UpdateOverlappingRangeAllocation success on %v: %+v", verb, clusteripres)
	return nil
}

// NormalizeIP normalizes the IP into a valid RFC 1123 DNS subdomain for use as
// a Kubernetes resource name. IPv6 addresses are expanded to their full
// 8-group hex form (e.g. "::1" → "0000-0000-0000-0000-0000-0000-0000-0001")
// to avoid leading hyphens from compressed notation. IPv4 addresses are left
// unchanged, which preserves dots and remains valid for Kubernetes resource
// names. Optionally prepends the network-name for named networks.
func NormalizeIP(ip net.IP, networkName string) string {
	var normalizedIP string
	if ip4 := ip.To4(); ip4 != nil {
		normalizedIP = ip4.String()
	} else if ip6 := ip.To16(); ip6 != nil {
		groups := make([]string, 8)
		for i := range 8 {
			groups[i] = fmt.Sprintf("%04x", uint16(ip6[i*2])<<8|uint16(ip6[i*2+1]))
		}
		normalizedIP = strings.Join(groups, "-")
	} else {
		normalizedIP = strings.Trim(strings.NewReplacer(":", "-", ".", "-").Replace(ip.String()), "-")
	}
	if networkName != UnnamedNetwork {
		normalizedIP = fmt.Sprintf("%s-%s", networkName, normalizedIP)
	}
	return normalizedIP
}

// LegacyNormalizeIP produces the pre-expansion resource name used by
// OverlappingRangeIPReservation CRs created before the IPv6 full-expansion
// change. It applies a simple colon/dot → dash replacement and trims leading
// and trailing dashes. This is the old behavior:
//
//	strings.Trim(strings.NewReplacer(":", "-", ".", "-").Replace(ip.String()), "-")
//
// Use this only for backward-compatible lookups and deletes of legacy CRs.
// New reservations must always use NormalizeIP.
func LegacyNormalizeIP(ip net.IP, networkName string) string {
	raw := strings.Trim(strings.NewReplacer(":", "-", ".", "-").Replace(ip.String()), "-")
	if networkName != UnnamedNetwork {
		return fmt.Sprintf("%s-%s", networkName, raw)
	}
	return raw
}

// getNodeName prefers an OS env var of NODENAME, or, uses a file named ./nodename in the whereabouts configuration path.
func getNodeName(ipam *KubernetesIPAM) (string, error) {
	envName := os.Getenv("NODENAME")
	if envName != "" {
		return strings.TrimSpace(envName), nil
	}

	nodeNamePath := fmt.Sprintf("%s/%s", ipam.Config.ConfigurationPath, "nodename")
	file, err := os.Open(nodeNamePath)
	if err != nil {
		file, err = os.Open("/etc/hostname")
		if err != nil {
			logging.Errorf("Could not determine nodename and could not open /etc/hostname: %w", err)
			return "", err
		}
	}
	defer file.Close()

	// Read the contents of the file
	data := make([]byte, 1024) // Adjust the buffer size as needed
	n, err := file.Read(data)
	if err != nil {
		logging.Errorf("Error reading file: %w", err)
		return "", err
	}

	// Convert bytes to string
	hostname := string(data[:n])
	hostname = strings.TrimSpace(hostname)
	logging.Debugf("discovered current hostname as: %s", hostname)
	return hostname, nil
}

// newLeaderElector creates a new leaderelection.LeaderElector and associated
// channels by which to observe elections and depositions.
func newLeaderElector(ctx context.Context, clientset kubernetes.Interface, namespace string, ipamConf *KubernetesIPAM) (elector *leaderelection.LeaderElector, leaderOK, deposed chan struct{}) {
	// leaderOK will block gRPC startup until it's closed.
	leaderOK = make(chan struct{})
	// deposed is closed by the leader election callback when
	// we are deposed as leader so that we can clean up.
	deposed = make(chan struct{})

	leaseName := "whereabouts"
	if ipamConf.Config.NodeSliceSize != "" {
		// we lock per IP Pool so just use the pool name for the lease name
		hostname, err := getNodeName(ipamConf)
		if err != nil {
			logging.Errorf("Failed to create leader elector: %w", err)
			return nil, leaderOK, deposed
		}
		nodeSliceRange, err := GetNodeSlicePoolRange(ctx, ipamConf, hostname)
		if err != nil {
			logging.Errorf("Failed to create leader elector: %w", err)
			return nil, leaderOK, deposed
		}
		leaseName = IPPoolName(PoolIdentifier{IPRange: nodeSliceRange, NodeName: hostname, NetworkName: ipamConf.Config.NetworkName})
	}
	logging.Debugf("using lease with name: %v", leaseName)

	var rl = &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Name:      leaseName,
			Namespace: namespace,
		},
		Client: clientset.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: fmt.Sprintf("%s/%s", ipamConf.Config.PodNamespace, ipamConf.Config.PodName),
		},
	}

	// Make the leader elector, ready to be used in the Workgroup.
	// !bang
	le, err := leaderelection.NewLeaderElector(leaderelection.LeaderElectionConfig{
		Lock:            rl,
		LeaseDuration:   time.Duration(ipamConf.Config.LeaderLeaseDuration) * time.Millisecond,
		RenewDeadline:   time.Duration(ipamConf.Config.LeaderRenewDeadline) * time.Millisecond,
		RetryPeriod:     time.Duration(ipamConf.Config.LeaderRetryPeriod) * time.Millisecond,
		ReleaseOnCancel: true,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(_ context.Context) {
				logging.Debugf("OnStartedLeading() called")
				close(leaderOK)
			},
			OnStoppedLeading: func() {
				logging.Debugf("OnStoppedLeading() called")
				// The context being canceled will trigger a handler that will
				// deal with being deposed.
				close(deposed)
			},
		},
	})
	if err != nil {
		logging.Errorf("Failed to create leader elector: %w", err)
		return nil, leaderOK, deposed
	}
	return le, leaderOK, deposed
}

// IPManagement orchestrates IP allocation or deallocation using leader election.
// It acquires a Kubernetes lease lock, then delegates to IPManagementKubernetesUpdate
// to perform the actual pool update. The mode parameter must be types.Allocate or
// types.Deallocate. Returns the list of assigned IP networks (for Allocate) or nil
// (for Deallocate). The function blocks until the operation completes, the context
// is canceled, or leader election fails.
func IPManagement(ctx context.Context, mode int, ipamConf whereaboutstypes.IPAMConfig, client *KubernetesIPAM) ([]net.IPNet, error) {
	var newips []net.IPNet

	if ipamConf.PodName == "" {
		return newips, fmt.Errorf("IPAM client initialization error: no pod name")
	}

	// setup leader election
	le, leader, deposed := newLeaderElector(ctx, client.clientSet, client.Namespace, client)
	if le == nil {
		return newips, fmt.Errorf("failed to create leader elector")
	}
	var wg sync.WaitGroup
	wg.Add(2)

	stopM := make(chan struct{}, 1)
	result := make(chan error, 2)

	var err error
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				err = fmt.Errorf("time limit exceeded while waiting to become leader")
				stopM <- struct{}{}
				return
			case <-leader:
				logging.Debugf("Elected as leader, do processing")
				newips, err = IPManagementKubernetesUpdate(ctx, mode, client, ipamConf)
				stopM <- struct{}{}
				return
			case <-deposed:
				logging.Debugf("Deposed as leader, shutting down")
				err = fmt.Errorf("deposed as leader, cannot complete IP management")
				stopM <- struct{}{}
				return
			}
		}
	}()

	go func() {
		defer wg.Done()
		res := make(chan error)
		leCtx, leCancel := context.WithCancel(ctx)

		go func() {
			logging.Debugf("Started leader election")
			le.Run(leCtx)
			logging.Debugf("Finished leader election")
			res <- nil
		}()

		// wait for stop which tells us when IP allocation occurred or context deadline exceeded
		<-stopM
		// leCancel fn(leCtx)
		leCancel()
		result <- (<-res)
	}()
	wg.Wait()
	close(stopM)
	logging.Debugf("IPManagement: %v, %v", newips, err)
	return newips, err
}

func GetNodeSlicePoolRange(ctx context.Context, ipam *KubernetesIPAM, nodeName string) (string, error) {
	logging.Debugf("ipam namespace is %v", ipam.Namespace)
	nodeSlice, err := ipam.client.WhereaboutsV1alpha1().NodeSlicePools(ipam.Namespace).Get(ctx, getNodeSliceName(ipam), metav1.GetOptions{})
	if err != nil {
		logging.Errorf("error getting node slice %s/%s %w", ipam.Namespace, getNodeSliceName(ipam), err)
		return "", err
	}
	for _, allocation := range nodeSlice.Status.Allocations {
		if allocation.NodeName == nodeName {
			logging.Debugf("found matching node slice allocation for hostname %v: %v", nodeName, allocation)
			return allocation.SliceRange, nil
		}
	}
	logging.Errorf("error finding node within node slice allocations")
	return "", fmt.Errorf("no allocated node slice for node")
}

func getNodeSliceName(ipam *KubernetesIPAM) string {
	if ipam.Config.NetworkName != UnnamedNetwork {
		return ipam.Config.NetworkName
	}
	if ipRange := firstConfiguredRange(ipam.Config); ipRange != "" {
		return normalizeRange(ipRange)
	}
	return ipam.Config.Name
}

func firstConfiguredRange(config whereaboutstypes.IPAMConfig) string {
	if config.Range != "" {
		return config.Range
	}
	if len(config.IPRanges) > 0 {
		return config.IPRanges[0].Range
	}
	return ""
}

// committedAlloc tracks a successfully committed pool allocation for rollback.
type committedAlloc struct {
	pool        storage.IPPool
	poolID      PoolIdentifier
	ip          net.IP
	ipam        *KubernetesIPAM
	overlap     storage.OverlappingRangeStore
	overlapIP   net.IP
	containerID string
	podRef      string
	ifName      string
	networkName string
	podUID      string
}

// IPManagementKubernetesUpdate manages k8s updates.
func IPManagementKubernetesUpdate(ctx context.Context, mode int, ipam *KubernetesIPAM, ipamConf whereaboutstypes.IPAMConfig) (newips []net.IPNet, retErr error) {
	logging.Debugf("IPManagement -- mode: %d / containerID: %q / podRef: %q / ifName: %q ", mode, ipam.ContainerID, ipamConf.GetPodRef(), ipam.IfName)

	var newip net.IPNet
	// Skip invalid modes
	switch mode {
	case whereaboutstypes.Allocate, whereaboutstypes.Deallocate:
	default:
		return newips, fmt.Errorf("got an unknown mode passed to IPManagement: %v", mode)
	}

	var overlappingrangestore storage.OverlappingRangeStore
	var pool storage.IPPool

	// handle the ip add/del until successful
	// For multi-range (e.g. dual-stack), if allocation succeeds for range N
	// but fails for range N+1, we perform a best-effort rollback of the
	// earlier allocations so IPs are not left orphaned.
	var committed []committedAlloc

	// Deferred rollback: if the function returns an error during Allocate mode
	// and we have previously committed allocations, undo them.
	defer func() {
		if retErr != nil && mode == whereaboutstypes.Allocate && len(committed) > 0 {
			rollbackCommitted(context.Background(), committed)
		}
	}()

	var overlappingrangeallocations []whereaboutstypes.IPReservation
	var ipforoverlappingrangeupdate net.IP

	// Sticky IP: read the pod's preferred-ip annotation to attempt assigning
	// a specific IP address. This enables pods to retain the same IP across
	// restarts (e.g. StatefulSets). See upstream #621.
	var preferredIP net.IP
	if mode == whereaboutstypes.Allocate && ipamConf.PodName != "" && ipamConf.PodNamespace != "" {
		pod, podErr := ipam.clientSet.CoreV1().Pods(ipamConf.PodNamespace).Get(ctx, ipamConf.PodName, metav1.GetOptions{})
		if podErr == nil {
			if preferred, ok := pod.Annotations["whereabouts.cni.cncf.io/preferred-ip"]; ok {
				preferredIP = net.ParseIP(preferred)
				if preferredIP != nil {
					logging.Debugf("Pod %s has preferred IP annotation: %s", ipamConf.GetPodRef(), preferredIP)
				} else {
					logging.Debugf("Pod %s has invalid preferred-ip annotation: %q", ipamConf.GetPodRef(), preferred)
				}
			}
		}
	}

	for idx := range ipamConf.IPRanges {
		ipRange := ipamConf.IPRanges[idx]
		configuredRange := ipRange.Range // capture before potential node-slice reassignment
		var err error
		var attempts int
		skipOverlappingRangeUpdate := false
		var pendingOverlapDeletionIP net.IP
		backoff := retryInitialBackoff
		poolIdentifier := PoolIdentifier{IPRange: ipRange.Range, NetworkName: ipamConf.NetworkName}
	RETRYLOOP:
		for j := range storage.DatastoreRetries {
			attempts = j + 1
			skipPoolUpdate := false
			requestCtx, requestCancel := context.WithTimeout(ctx, storage.RequestTimeout)
			skipOverlappingRangeUpdate = false
			select {
			case <-ctx.Done():
				requestCancel()
				err = fmt.Errorf("IPAM context canceled before attempt %d: %w", j+1, ctx.Err())
				break RETRYLOOP
			default:
				// retry the IPAM loop if the context has not been canceled
			}
			overlappingrangestore, err = ipam.GetOverlappingRangeStore()
			if err != nil {
				logging.Errorf("IPAM error getting OverlappingRangeStore: %w", err)
				requestCancel()
				return newips, err
			}
			poolIdentifier = PoolIdentifier{IPRange: ipRange.Range, NetworkName: ipamConf.NetworkName}
			if ipamConf.NodeSliceSize != "" {
				hostname, err := getNodeName(ipam)
				if err != nil {
					logging.Errorf("Failed to get node hostname: %w", err)
					requestCancel()
					return newips, err
				}
				poolIdentifier.NodeName = hostname
				nodeSliceRange, err := GetNodeSlicePoolRange(requestCtx, ipam, hostname)
				if err != nil {
					requestCancel()
					return newips, err
				}
				_, ipNet, err := net.ParseCIDR(nodeSliceRange)
				if err != nil {
					logging.Errorf("Error parsing node slice cidr to net.IPNet: %w", err)
					requestCancel()
					return newips, err
				}
				poolIdentifier.IPRange = nodeSliceRange
				rangeStart, err := iphelpers.FirstUsableIP(*ipNet)
				if err != nil {
					logging.Errorf("Error parsing node slice cidr to range start: %w", err)
					requestCancel()
					return newips, err
				}
				rangeEnd, err := iphelpers.LastUsableIP(*ipNet)
				if err != nil {
					logging.Errorf("Error parsing node slice cidr to range end: %w", err)
					requestCancel()
					return newips, err
				}
				ipRange = whereaboutstypes.RangeConfiguration{
					OmitRanges:    ipRange.OmitRanges,
					Range:         nodeSliceRange,
					RangeStart:    rangeStart,
					RangeEnd:      rangeEnd,
					PickAddresses: ipRange.PickAddresses,
					L3:            ipRange.L3,
				}
			}
			logging.Debugf("using pool identifier: %v", poolIdentifier)
			if mode == whereaboutstypes.Deallocate {
				pool, err = ipam.GetExistingIPPool(requestCtx, poolIdentifier)
				if apierrors.IsNotFound(err) {
					logging.Debugf("IPPool %s not found for deallocation, treating as already released", IPPoolName(poolIdentifier))
					err = nil
					requestCancel()
					break RETRYLOOP
				}
			} else {
				pool, err = ipam.GetIPPool(requestCtx, poolIdentifier)
			}
			if err != nil {
				logging.Errorf("IPAM error reading pool allocations (attempt: %d): %w", j, err)
				if e, ok := err.(storage.Temporary); ok && e.Temporary() {
					requestCancel()
					retryBackoff(ctx, backoff)
					backoff = min(backoff*2, retryMaxBackoff)
					continue
				}
				requestCancel()
				return newips, err
			}

			reservelist := pool.Allocations()
			reservelist = append(reservelist, overlappingrangeallocations...)
			var updatedreservelist []whereaboutstypes.IPReservation
			switch mode {
			case whereaboutstypes.Allocate:
				// Set preferred IP from pod annotation for sticky assignment.
				if preferredIP != nil {
					ipRange.PreferredIP = preferredIP
				}
				newip, updatedreservelist, err = allocate.AssignIP(ipRange, reservelist, ipam.ContainerID, ipamConf.GetPodRef(), ipam.IfName)
				if err != nil {
					logging.Errorf("Error assigning IP: %w", err)
					requestCancel()
					return newips, err
				}
				// Now check if this is allocated overlappingrange wide
				// When it's allocated overlappingrange wide, we add it to a local reserved list
				// And we try again.
				if ipamConf.OverlappingRanges {
					overlappingRangeIPReservation, err := overlappingrangestore.GetOverlappingRangeIPReservation(requestCtx, newip.IP,
						ipamConf.GetPodRef(), ipamConf.NetworkName)
					if err != nil {
						logging.Errorf("Error getting cluster wide IP allocation: %w", err)
						requestCancel()
						return newips, err
					}

					if overlappingRangeIPReservation != nil {
						if overlappingRangeIPReservation.Spec.PodRef != ipamConf.GetPodRef() {
							logging.Debugf("Continuing loop, IP is already allocated (possibly from another range): %v", newip)
							// We create "dummy" records here for evaluation, but, we need to filter those out later.
							overlappingrangeallocations = append(overlappingrangeallocations, whereaboutstypes.IPReservation{IP: newip.IP, IsAllocated: true})
							requestCancel()
							continue
						}
						if overlappingRangeIPReservation.Spec.IfName != ipam.IfName {
							logging.Debugf("Continuing loop, IP is already allocated to podRef %q on ifName %q: %v",
								ipamConf.GetPodRef(), overlappingRangeIPReservation.Spec.IfName, newip)
							// Same pod on another interface must not reuse the reservation.
							overlappingrangeallocations = append(overlappingrangeallocations, whereaboutstypes.IPReservation{IP: newip.IP, IsAllocated: true})
							requestCancel()
							continue
						}

						// Same PodRef — check UID to detect stale reservations from an evicted
						// pod whose name was reused by a new pod with a different UID.
						if ipamConf.PodUID != "" && overlappingRangeIPReservation.Spec.PodUID != "" &&
							overlappingRangeIPReservation.Spec.PodUID != ipamConf.PodUID {
							logging.Debugf("Stale ORIP for %v: stored UID %q differs from current UID %q; deleting stale reservation",
								newip.IP, overlappingRangeIPReservation.Spec.PodUID, ipamConf.PodUID)
							delCtx, delCancel := context.WithTimeout(ctx, storage.RequestTimeout)
							// Pass the observed stale UID into the delete path. If the
							// reservation changes between the read above and the delete,
							// UpdateOverlappingRangeAllocation's UID guard will skip the
							// deletion instead of removing a newer reservation.
							delErr := overlappingrangestore.UpdateOverlappingRangeAllocation(delCtx, whereaboutstypes.Deallocate, newip.IP,
								ipamConf.GetPodRef(), ipam.IfName, ipamConf.NetworkName, overlappingRangeIPReservation.Spec.PodUID)
							delCancel()
							if delErr != nil {
								requestCancel()
								return newips, logging.Errorf("failed to delete stale ORIP for %v: %w", newip.IP, delErr)
							}
							// Fall through: create a fresh reservation below.
						} else {
							skipOverlappingRangeUpdate = true
						}
					}

					ipforoverlappingrangeupdate = newip.IP
				}

			case whereaboutstypes.Deallocate:
				updatedreservelist, ipforoverlappingrangeupdate = allocate.DeallocateIP(reservelist, ipam.ContainerID, ipam.IfName)
				if ipforoverlappingrangeupdate == nil {
					if ipamConf.OverlappingRanges && pendingOverlapDeletionIP != nil {
						ipforoverlappingrangeupdate = append(net.IP(nil), pendingOverlapDeletionIP...)
						updatedreservelist = reservelist
						skipPoolUpdate = true
						logging.Debugf("No pool allocation found for container ID %q in range %s; retrying overlapping reservation cleanup for IP %s",
							ipam.ContainerID, ipRange.Range, ipforoverlappingrangeupdate)
						break
					}
					// Allocation not found in this range — continue to remaining
					// ranges so that IPs in other ranges are still released.
					logging.Debugf("No allocation found for container ID %q in range %s, continuing to next range", ipam.ContainerID, ipRange.Range)
					requestCancel()
					break RETRYLOOP
				}
				pendingOverlapDeletionIP = append(net.IP(nil), ipforoverlappingrangeupdate...)
			}

			// Clean out any dummy records from the reservelist...
			var usereservelist []whereaboutstypes.IPReservation
			for _, rl := range updatedreservelist {
				if !rl.IsAllocated {
					usereservelist = append(usereservelist, rl)
				}
			}

			// Manual race condition testing (capped to prevent DoS)
			if ipamConf.SleepForRace > 0 {
				sleepSec := ipamConf.SleepForRace
				if sleepSec > whereaboutstypes.MaxSleepForRace {
					logging.Debugf("Capping sleep_for_race from %d to %d seconds", sleepSec, whereaboutstypes.MaxSleepForRace)
					sleepSec = whereaboutstypes.MaxSleepForRace
				}
				time.Sleep(time.Duration(sleepSec) * time.Second)
			}

			if !skipPoolUpdate {
				err = pool.Update(requestCtx, usereservelist)
				if err != nil {
					logging.Errorf("IPAM error updating pool (attempt: %d): %w", j, err)
					if e, ok := err.(storage.Temporary); ok && e.Temporary() {
						requestCancel()
						retryBackoff(ctx, backoff)
						backoff = min(backoff*2, retryMaxBackoff)
						continue
					}
					requestCancel()
					break RETRYLOOP
				}
			}
			// Update the clusterwide overlapping range reservation inside the retry
			// loop so that a transient ORIP conflict (AlreadyExists) causes a retry
			// rather than a hard failure.
			if ipamConf.OverlappingRanges && !skipOverlappingRangeUpdate {
				overlappingCtx, overlappingCancel := context.WithTimeout(ctx, storage.RequestTimeout)
				err = overlappingrangestore.UpdateOverlappingRangeAllocation(overlappingCtx, mode, ipforoverlappingrangeupdate,
					ipamConf.GetPodRef(), ipam.IfName, ipamConf.NetworkName, ipamConf.PodUID)
				overlappingCancel()
				if err != nil {
					logging.Errorf("Error performing UpdateOverlappingRangeAllocation (attempt: %d): %w", j, err)
					// Roll back the pool update so the IP is not reserved without
					// overlap protection, then decide whether to retry.
					if mode == whereaboutstypes.Allocate && pool != nil {
						rollbackCommitted(context.Background(), []committedAlloc{{
							pool:        pool,
							poolID:      poolIdentifier,
							ip:          newip.IP,
							ipam:        ipam,
							containerID: ipam.ContainerID,
							podRef:      ipamConf.GetPodRef(),
							ifName:      ipam.IfName,
						}})
					}
					if isRetryableRollbackError(err) {
						requestCancel()
						retryBackoff(ctx, backoff)
						backoff = min(backoff*2, retryMaxBackoff)
						continue
					}
					requestCancel()
					break RETRYLOOP
				}
			}
			requestCancel()
			break RETRYLOOP
		}

		if err != nil {
			return newips, logging.Errorf("IP allocation failed for range %s after %d attempts: %w", configuredRange, attempts, err)
		}

		// Track this allocation so we can roll it back if a later range fails.
		// Only append to newips in Allocate mode — during Deallocate, newip is
		// never assigned and would be a zero-value net.IPNet{}.
		if mode == whereaboutstypes.Allocate {
			commit := committedAlloc{
				pool:        pool,
				poolID:      poolIdentifier,
				ip:          append(net.IP(nil), newip.IP...),
				ipam:        ipam,
				containerID: ipam.ContainerID,
				podRef:      ipamConf.GetPodRef(),
				ifName:      ipam.IfName,
			}
			if ipamConf.OverlappingRanges && !skipOverlappingRangeUpdate {
				commit.overlap = overlappingrangestore
				commit.overlapIP = append(net.IP(nil), ipforoverlappingrangeupdate...)
				commit.podRef = ipamConf.GetPodRef()
				commit.ifName = ipam.IfName
				commit.networkName = ipamConf.NetworkName
				commit.podUID = ipamConf.PodUID
			}
			committed = append(committed, commit)
			if ipamConf.OverlappingRanges {
				// Reserve successful allocations locally so later ipRanges in the
				// same ADD cannot reuse the same overlapping IP.
				overlappingrangeallocations = append(overlappingrangeallocations, whereaboutstypes.IPReservation{IP: newip.IP, IsAllocated: true})
			}
			newips = append(newips, newip)
		}
	}
	return newips, nil
}

// rollbackRetries limits the number of conflict-retry attempts during
// best-effort rollback. A stale resourceVersion (due to concurrent pool
// updates) produces a temporaryError; we re-read the pool and retry.
const rollbackRetries = 5

// isRetryableRollbackError returns true for errors that warrant a rollback
// retry: resource version conflicts (storage.Temporary), transient API server
// errors (conflict, timeout, too-many-requests, server-timeout, service-unavailable),
// and network-level timeouts.
func isRetryableRollbackError(err error) bool {
	// storage.Temporary — the existing conflict error from pool.Update.
	if e, ok := err.(storage.Temporary); ok && e.Temporary() {
		return true
	}
	// Transient Kubernetes API errors.
	if apierrors.IsConflict(err) || apierrors.IsServerTimeout(err) || apierrors.IsTimeout(err) || apierrors.IsTooManyRequests(err) || apierrors.IsServiceUnavailable(err) {
		return true
	}
	// Network-level timeouts (e.g., context deadline exceeded, TCP timeout).
	if ne, ok := err.(interface{ Timeout() bool }); ok && ne.Timeout() {
		return true
	}
	return false
}

// rollbackCommitted performs a best-effort rollback of previously committed
// multi-range allocations. Each allocation is removed from its pool so the IP
// is not left orphaned when a later range fails. The first attempt uses the
// already-fetched pool; subsequent retry attempts re-read the pool from
// Kubernetes to avoid operating on a stale resourceVersion.
func rollbackCommitted(ctx context.Context, committed []committedAlloc) {
	for i := range committed {
		c := &committed[i]
		var lastErr error
		var attempts int
		for attempt := range rollbackRetries {
			attempts = attempt + 1
			pool := c.pool
			if attempt > 0 && c.ipam == nil {
				logging.Debugf("Rollback retry for IP %s skipping pool re-read: no IPAM reference available", c.ip)
			}
			if attempt > 0 && c.ipam != nil {
				freshPool, err := c.ipam.GetExistingIPPool(ctx, c.poolID)
				if err != nil {
					if apierrors.IsNotFound(err) {
						logging.Debugf("Rollback pool for IP %s no longer exists", c.ip)
						lastErr = nil
						break
					}
					logging.Errorf("Rollback re-read failed for IP %s (attempt %d): %v", c.ip, attempt+1, err)
					lastErr = err
					continue
				}
				pool = freshPool
			}

			allocs := pool.Allocations()
			var cleaned []whereaboutstypes.IPReservation
			removed := false
			for _, r := range allocs {
				if !rollbackAllocationMatches(c, r) {
					cleaned = append(cleaned, r)
					continue
				}
				removed = true
			}
			if !removed {
				logging.Debugf("Rollback skipping IP %s: allocation no longer belongs to container %q podRef %q ifName %q",
					c.ip, c.containerID, c.podRef, c.ifName)
				lastErr = nil
				break
			}
			rbCtx, rbCancel := context.WithTimeout(ctx, storage.RequestTimeout)
			err := pool.Update(rbCtx, cleaned)
			rbCancel()
			if err != nil {
				if isRetryableRollbackError(err) {
					logging.Debugf("Rollback transient error for IP %s (attempt %d), retrying", c.ip, attempt+1)
					lastErr = err
					continue
				}
				// Permanent error — stop retrying immediately.
				lastErr = err
				break
			}
			logging.Debugf("Rolled back allocation for IP %s", c.ip)
			rollbackOverlappingReservation(ctx, c)
			lastErr = nil
			break
		}
		if lastErr != nil {
			logging.Errorf("Multi-range rollback failed for IP %s after %d attempt(s): %v", c.ip, attempts, lastErr)
		}
	}
}

func rollbackAllocationMatches(c *committedAlloc, r whereaboutstypes.IPReservation) bool {
	if !r.IP.Equal(c.ip) {
		return false
	}
	if c.containerID == "" && c.podRef == "" && c.ifName == "" {
		return true
	}
	return r.ContainerID == c.containerID && r.PodRef == c.podRef && r.IfName == c.ifName
}

func rollbackOverlappingReservation(ctx context.Context, c *committedAlloc) {
	if c.overlap == nil || c.overlapIP == nil {
		return
	}
	var lastErr error
	var attempts int
	for attempt := range rollbackRetries {
		attempts = attempt + 1
		rbCtx, rbCancel := context.WithTimeout(ctx, storage.RequestTimeout)
		err := c.overlap.UpdateOverlappingRangeAllocation(
			rbCtx, whereaboutstypes.Deallocate, c.overlapIP, c.podRef, c.ifName, c.networkName, c.podUID)
		rbCancel()
		if err != nil {
			if isRetryableRollbackError(err) {
				logging.Debugf("ORIP rollback transient error for IP %s (attempt %d), retrying", c.overlapIP, attempt+1)
				lastErr = err
				continue
			}
			lastErr = err
			break
		}
		logging.Debugf("Rolled back overlapping reservation for IP %s", c.overlapIP)
		lastErr = nil
		break
	}
	if lastErr != nil {
		logging.Errorf("ORIP rollback failed for IP %s after %d attempt(s): %v", c.overlapIP, attempts, lastErr)
	}
}

func wbNamespaceFromCtx(ctx *clientcmdapi.Context) string {
	namespace := ctx.Namespace
	if namespace == "" {
		return metav1.NamespaceSystem
	}
	return namespace
}
