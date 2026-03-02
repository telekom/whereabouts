package iphelpers

import (
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/netip"
	"strconv"
	"strings"

	"github.com/gaissmai/extnetip"
	netutils "k8s.io/utils/net"
)

// toAddr converts a net.IP to netip.Addr.
func toAddr(ip net.IP) (netip.Addr, bool) {
	if ip == nil {
		return netip.Addr{}, false
	}
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}

// toPrefix converts a net.IPNet to netip.Prefix.
func toPrefix(ipnet net.IPNet) (netip.Prefix, bool) {
	addr, ok := toAddr(ipnet.IP)
	if !ok {
		return netip.Prefix{}, false
	}
	ones, _ := ipnet.Mask.Size()
	return netip.PrefixFrom(addr, ones), true
}

// CompareIPs reports whether out of 2 given IPs, ipX and ipY, ipY is smaller (-1), the same (0) or larger (1).
func CompareIPs(ipX net.IP, ipY net.IP) int {
	ax, okX := toAddr(ipX)
	ay, okY := toAddr(ipY)
	if !okX || !okY {
		return 0
	}
	return ax.Compare(ay)
}

// DivideRangeBySize takes an ipRange (e.g. "11.0.0.0/8") and a sliceSize (e.g. "/24")
// and returns a list of CIDRs that divide the input range into the given prefix lengths.
// Works with both IPv4 and IPv6.
func DivideRangeBySize(inputNetwork string, sliceSizeString string) ([]string, error) {
	sliceSizeString = strings.TrimPrefix(sliceSizeString, "/")
	sliceSize, err := strconv.Atoi(sliceSizeString)
	if err != nil {
		return nil, fmt.Errorf("invalid slice size %q: %s", sliceSizeString, err)
	}

	prefix, err := netip.ParsePrefix(inputNetwork)
	if err != nil {
		return nil, fmt.Errorf("error parsing CIDR %s: %s", inputNetwork, err)
	}
	if prefix.Addr() != prefix.Masked().Addr() {
		return nil, errors.New("netCIDR is not a valid network address")
	}

	netBits := prefix.Bits()
	if netBits > sliceSize {
		return nil, errors.New("subnetMaskSize must be greater or equal than netMaskSize")
	}

	addrLen := 32
	if prefix.Addr().Is6() {
		addrLen = 128
	}
	if sliceSize > addrLen {
		return nil, fmt.Errorf("slice size /%d exceeds address length /%d", sliceSize, addrLen)
	}

	numSubnets := new(big.Int).Lsh(big.NewInt(1), uint(sliceSize-netBits))
	subnetSize := new(big.Int).Lsh(big.NewInt(1), uint(addrLen-sliceSize))

	baseInt := netutils.BigForIP(prefix.Addr().AsSlice())
	var result []string

	for i := big.NewInt(0); i.Cmp(numSubnets) < 0; i.Add(i, big.NewInt(1)) {
		offset := new(big.Int).Mul(i, subnetSize)
		subnetBig := new(big.Int).Add(baseInt, offset)
		subnetIP := bigIntToIP(subnetBig, prefix.Addr().Is6())
		addr, _ := netip.AddrFromSlice(subnetIP)
		result = append(result, fmt.Sprintf("%s/%d", addr.Unmap(), sliceSize))
	}
	return result, nil
}

// IsIPInRange returns true if a given IP is within the continuous range of start and end IP (inclusively).
func IsIPInRange(in net.IP, start net.IP, end net.IP) (bool, error) {
	if in == nil || start == nil || end == nil {
		return false, fmt.Errorf("cannot determine if IP is in range, either of the values is '<nil>', "+
			"in: %v, start: %v, end: %v", in, start, end)
	}
	return CompareIPs(in, start) >= 0 && CompareIPs(in, end) <= 0, nil
}

// NetworkIP returns the network address of the subnet.
func NetworkIP(ipnet net.IPNet) net.IP {
	pfx, ok := toPrefix(ipnet)
	if !ok {
		return nil
	}
	return addrToNetIP(pfx.Masked().Addr(), ipnet.IP)
}

// SubnetBroadcastIP returns the broadcast IP (last address) for a given net.IPNet.
func SubnetBroadcastIP(ipnet net.IPNet) net.IP {
	pfx, ok := toPrefix(ipnet)
	if !ok {
		return nil
	}
	_, last := extnetip.Range(pfx)
	return addrToNetIP(last, ipnet.IP)
}

// FirstUsableIP returns the first usable IP (not the network IP) in a given net.IPNet.
// This does not work for IPv4 /31 to /32 or IPv6 /127 to /128 netmasks.
func FirstUsableIP(ipnet net.IPNet) (net.IP, error) {
	if !HasUsableIPs(ipnet) {
		return nil, fmt.Errorf("net mask is too short, subnet %s has no usable IP addresses, it is too small", ipnet)
	}
	return IncIP(NetworkIP(ipnet)), nil
}

// LastUsableIP returns the last usable IP (not the broadcast IP in a given net.IPNet).
// This does not work for IPv4 /31 to /32 or IPv6 /127 to /128 netmasks.
func LastUsableIP(ipnet net.IPNet) (net.IP, error) {
	if !HasUsableIPs(ipnet) {
		return nil, fmt.Errorf("net mask is too short, subnet %s has no usable IP addresses, it is too small", ipnet)
	}
	return DecIP(SubnetBroadcastIP(ipnet)), nil
}

// HasUsableIPs returns true if this subnet has usable IPs (i.e. not the network nor the broadcast IP).
func HasUsableIPs(ipnet net.IPNet) bool {
	ones, totalBits := ipnet.Mask.Size()
	return totalBits-ones > 1
}

// IncIP increases the given IP address by one.
// If the address is already the maximum (e.g. 255.255.255.255), it is returned unchanged.
func IncIP(ip net.IP) net.IP {
	addr, ok := toAddr(ip)
	if !ok {
		return ip
	}
	next := addr.Next()
	if !next.IsValid() {
		return ip
	}
	return addrToNetIP(next, ip)
}

// DecIP decreases the given IP address by one.
// If the address is already the minimum (0.0.0.0 or ::), it is returned unchanged.
func DecIP(ip net.IP) net.IP {
	addr, ok := toAddr(ip)
	if !ok {
		return ip
	}
	prev := addr.Prev()
	if !prev.IsValid() {
		return ip
	}
	return addrToNetIP(prev, ip)
}

// IPGetOffset returns the absolute offset between ip1 and ip2.
// The result is always non-negative. Uses k8s.io/utils/net for IP arithmetic.
func IPGetOffset(ip1, ip2 net.IP) (uint64, error) {
	addr1, ok1 := toAddr(ip1)
	addr2, ok2 := toAddr(ip2)
	if !ok1 || !ok2 {
		return 0, fmt.Errorf("invalid IP address(es): ip1=%v, ip2=%v", ip1, ip2)
	}
	if addr1.Is4() && !addr2.Is4() {
		return 0, fmt.Errorf("cannot calculate offset between IPv4 (%s) and IPv6 address (%s)", ip1, ip2)
	}
	if !addr1.Is4() && addr2.Is4() {
		return 0, fmt.Errorf("cannot calculate offset between IPv6 (%s) and IPv4 address (%s)", ip1, ip2)
	}

	a := netutils.BigForIP(ip1)
	b := netutils.BigForIP(ip2)
	diff := new(big.Int).Sub(a, b)
	diff.Abs(diff)

	if !diff.IsUint64() {
		return 0, fmt.Errorf("offset between %s and %s exceeds uint64", ip1, ip2)
	}
	return diff.Uint64(), nil
}

// IPAddOffset returns ip + offset. Uses k8s.io/utils/net for IP arithmetic.
func IPAddOffset(ip net.IP, offset uint64) net.IP {
	if ip == nil {
		return nil
	}

	base := netutils.BigForIP(ip)
	off := new(big.Int).SetUint64(offset)
	resultInt := new(big.Int).Add(base, off)

	// Reconstruct net.IP from the big.Int, preserving original length.
	b := resultInt.Bytes()
	if len(ip) == net.IPv4len {
		result := make(net.IP, net.IPv4len)
		if len(b) > net.IPv4len {
			b = b[len(b)-net.IPv4len:]
		}
		copy(result[net.IPv4len-len(b):], b)
		return result
	}
	result := make(net.IP, net.IPv6len)
	if len(b) > net.IPv6len {
		b = b[len(b)-net.IPv6len:]
	}
	copy(result[net.IPv6len-len(b):], b)
	return result
}

// IsIPv4 checks if an IP is v4.
func IsIPv4(checkip net.IP) bool {
	return checkip.To4() != nil
}

// GetIPRange returns the first and last IP in a range.
// If either rangeStart or rangeEnd are inside the range of first usable IP to last usable IP, then use them.
// Otherwise, they will be silently ignored and the first usable IP and/or last usable IP will be used.
// A valid rangeEnd cannot be smaller than a valid rangeStart.
func GetIPRange(ipnet net.IPNet, rangeStart net.IP, rangeEnd net.IP) (net.IP, net.IP, error) {
	firstUsableIP, err := FirstUsableIP(ipnet)
	if err != nil {
		return nil, nil, err
	}
	lastUsableIP, err := LastUsableIP(ipnet)
	if err != nil {
		return nil, nil, err
	}
	if rangeStart != nil {
		rangeStartInRange, err := IsIPInRange(rangeStart, firstUsableIP, lastUsableIP)
		if err != nil {
			return nil, nil, err
		}
		if rangeStartInRange {
			firstUsableIP = rangeStart
		}
	}
	if rangeEnd != nil {
		rangeEndInRange, err := IsIPInRange(rangeEnd, firstUsableIP, lastUsableIP)
		if err != nil {
			return nil, nil, err
		}
		if rangeEndInRange {
			lastUsableIP = rangeEnd
		}
	}
	return firstUsableIP, lastUsableIP, nil
}

// bigIntToIP converts a *big.Int to a net.IP of the appropriate length.
func bigIntToIP(i *big.Int, is6 bool) net.IP {
	b := i.Bytes()
	if is6 {
		result := make(net.IP, net.IPv6len)
		if len(b) > net.IPv6len {
			b = b[len(b)-net.IPv6len:]
		}
		copy(result[net.IPv6len-len(b):], b)
		return result
	}
	result := make(net.IP, net.IPv4len)
	if len(b) > net.IPv4len {
		b = b[len(b)-net.IPv4len:]
	}
	copy(result[net.IPv4len-len(b):], b)
	return result
}

// addrToNetIP converts a netip.Addr back to net.IP, preserving the slice length of origIP.
// This ensures IPv4 addresses maintain their 4-byte or 16-byte (IPv4-in-IPv6 mapped) representation.
func addrToNetIP(addr netip.Addr, origIP net.IP) net.IP {
	if addr.Is4() && len(origIP) == net.IPv6len {
		b := addr.As16()
		return net.IP(b[:])
	}
	return addr.AsSlice()
}


