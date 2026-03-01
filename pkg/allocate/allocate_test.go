package allocate

import (
	"fmt"
	"net"
	"testing"

	"github.com/telekom/whereabouts/pkg/types"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestAllocate(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "cmd")
}

var _ = Describe("Allocation operations", func() {
	It("can IterateForAssignment on an IPv4 address", func() {

		firstip, ipnet, err := net.ParseCIDR("192.168.1.1/24")
		Expect(err).NotTo(HaveOccurred())

		// figure out the range start.
		calculatedrangestart := net.ParseIP(firstip.Mask(ipnet.Mask).String())

		var ipres []types.IPReservation
		var exrange []string
		newip, _, err := IterateForAssignment(*ipnet, calculatedrangestart, nil, ipres, exrange, "0xdeadbeef", "", "")
		Expect(err).NotTo(HaveOccurred())
		Expect(fmt.Sprint(newip)).To(Equal("192.168.1.1"))

	})

	It("can IterateForAssignment on an IPv6 address when the first hextet has NO leading zeroes", func() {

		firstip, ipnet, err := net.ParseCIDR("caa5::0/112")
		Expect(err).NotTo(HaveOccurred())

		// figure out the range start.
		calculatedrangestart := net.ParseIP(firstip.Mask(ipnet.Mask).String())

		var ipres []types.IPReservation
		var exrange []string
		newip, _, err := IterateForAssignment(*ipnet, calculatedrangestart, nil, ipres, exrange, "0xdeadbeef", "", "")
		Expect(err).NotTo(HaveOccurred())
		Expect(fmt.Sprint(newip)).To(Equal("caa5::1"))

	})

	It("can IterateForAssignment on an IPv6 address when the first hextet has ALL leading zeroes", func() {

		firstip, ipnet, err := net.ParseCIDR("::1/126")
		Expect(err).NotTo(HaveOccurred())

		// figure out the range start.
		calculatedrangestart := net.ParseIP(firstip.Mask(ipnet.Mask).String())

		var ipres []types.IPReservation
		var exrange []string
		newip, _, err := IterateForAssignment(*ipnet, calculatedrangestart, nil, ipres, exrange, "0xdeadbeef", "", "")
		Expect(err).NotTo(HaveOccurred())
		Expect(fmt.Sprint(newip)).To(Equal("::1"))

	})

	//

	It("can IterateForAssignment on an IPv6 address when the first hextet has TWO leading zeroes", func() {

		firstip, ipnet, err := net.ParseCIDR("fd::1/116")
		Expect(err).NotTo(HaveOccurred())

		// figure out the range start.
		calculatedrangestart := net.ParseIP(firstip.Mask(ipnet.Mask).String())

		var ipres []types.IPReservation
		var exrange []string
		newip, _, err := IterateForAssignment(*ipnet, calculatedrangestart, nil, ipres, exrange, "0xdeadbeef", "", "")
		Expect(err).NotTo(HaveOccurred())
		Expect(fmt.Sprint(newip)).To(Equal("fd::1"))

	})

	It("can IterateForAssignment on an IPv6 address when the first hextet has leading zeroes", func() {

		firstip, ipnet, err := net.ParseCIDR("100::2:1/126")
		Expect(err).NotTo(HaveOccurred())

		// figure out the range start.
		calculatedrangestart := net.ParseIP(firstip.Mask(ipnet.Mask).String())

		var ipres []types.IPReservation
		var exrange []string
		newip, _, err := IterateForAssignment(*ipnet, calculatedrangestart, nil, ipres, exrange, "0xdeadbeef", "", "")
		Expect(err).NotTo(HaveOccurred())
		Expect(fmt.Sprint(newip)).To(Equal("100::2:1"))
	})

	It("can IterateForAssignment on an IPv4 address excluding a range", func() {

		firstip, ipnet, err := net.ParseCIDR("192.168.0.0/29")
		Expect(err).NotTo(HaveOccurred())

		// figure out the range start.
		calculatedrangestart := net.ParseIP(firstip.Mask(ipnet.Mask).String())

		var ipres []types.IPReservation
		exrange := []string{"192.168.0.0/30"}
		newip, _, _ := IterateForAssignment(*ipnet, calculatedrangestart, nil, ipres, exrange, "0xdeadbeef", "", "")
		Expect(fmt.Sprint(newip)).To(Equal("192.168.0.4"))

	})

	It("can IterateForAssignment on an IPv4 address excluding a range which is a single IP", func() {
		firstip, ipnet, err := net.ParseCIDR("192.168.0.0/29")
		Expect(err).NotTo(HaveOccurred())

		// figure out the range start.
		calculatedrangestart := net.ParseIP(firstip.Mask(ipnet.Mask).String())

		var ipres []types.IPReservation
		exrange := []string{"192.168.0.1"}
		newip, _, err := IterateForAssignment(*ipnet, calculatedrangestart, nil, ipres, exrange, "0xdeadbeef", "", "")
		Expect(err).NotTo(HaveOccurred())
		Expect(fmt.Sprint(newip)).To(Equal("192.168.0.2"))
	})

	It("correctly handles invalid syntax for an exclude range with IPv4", func() {
		firstip, ipnet, err := net.ParseCIDR("192.168.0.0/29")
		Expect(err).NotTo(HaveOccurred())

		// figure out the range start.
		calculatedrangestart := net.ParseIP(firstip.Mask(ipnet.Mask).String())

		var ipres []types.IPReservation
		exrange := []string{"192.168.0.1/123"}
		_, _, err = IterateForAssignment(*ipnet, calculatedrangestart, nil, ipres, exrange, "0xdeadbeef", "", "")
		Expect(err).To(MatchError(HavePrefix("could not parse exclude range")))
	})

	It("can IterateForAssignment on an IPv6 address excluding a range", func() {

		firstip, ipnet, err := net.ParseCIDR("100::2:1/125")
		Expect(err).NotTo(HaveOccurred())

		// figure out the range start.
		calculatedrangestart := net.ParseIP(firstip.Mask(ipnet.Mask).String())

		var ipres []types.IPReservation
		exrange := []string{"100::2:1/126"}
		newip, _, _ := IterateForAssignment(*ipnet, calculatedrangestart, nil, ipres, exrange, "0xdeadbeef", "", "")
		Expect(fmt.Sprint(newip)).To(Equal("100::2:4"))

	})

	It("can IterateForAssignment on an IPv6 address excluding a range which is a single IP", func() {
		firstip, ipnet, err := net.ParseCIDR("100::2:1/125")
		Expect(err).NotTo(HaveOccurred())

		// figure out the range start.
		calculatedrangestart := net.ParseIP(firstip.Mask(ipnet.Mask).String())

		var ipres []types.IPReservation
		exrange := []string{"100::2:1"}
		newip, _, _ := IterateForAssignment(*ipnet, calculatedrangestart, nil, ipres, exrange, "0xdeadbeef", "", "")
		Expect(fmt.Sprint(newip)).To(Equal("100::2:2"))
	})

	It("correctly handles invalid syntax for an exclude range with IPv6", func() {
		firstip, ipnet, err := net.ParseCIDR("100::2:1/125")
		Expect(err).NotTo(HaveOccurred())

		// figure out the range start.
		calculatedrangestart := net.ParseIP(firstip.Mask(ipnet.Mask).String())

		var ipres []types.IPReservation
		exrange := []string{"100::2::1"}
		_, _, err = IterateForAssignment(*ipnet, calculatedrangestart, nil, ipres, exrange, "0xdeadbeef", "", "")
		Expect(err).To(MatchError(HavePrefix("could not parse exclude range")))
	})

	It("can IterateForAssignment on an IPv6 address excluding a very large range", func() {

		firstip, ipnet, err := net.ParseCIDR("2001:db8::/30")
		Expect(err).NotTo(HaveOccurred())

		// figure out the range start.
		calculatedrangestart := net.ParseIP(firstip.Mask(ipnet.Mask).String())

		var ipres []types.IPReservation
		exrange := []string{"2001:db8::0/32"}
		newip, _, _ := IterateForAssignment(*ipnet, calculatedrangestart, nil, ipres, exrange, "0xdeadbeef", "", "")
		Expect(fmt.Sprint(newip)).To(Equal("2001:db9::"))

	})

	It("can IterateForAssignment on an IPv4 address excluding unsorted ranges", func() {

		firstip, ipnet, err := net.ParseCIDR("192.168.0.0/28")
		Expect(err).NotTo(HaveOccurred())

		// figure out the range start.
		calculatedrangestart := net.ParseIP(firstip.Mask(ipnet.Mask).String())

		var ipres []types.IPReservation
		exrange := []string{"192.168.0.0/30", "192.168.0.6/31", "192.168.0.8/31", "192.168.0.4/30"}
		newip, _, _ := IterateForAssignment(*ipnet, calculatedrangestart, nil, ipres, exrange, "0xdeadbeef", "", "")
		Expect(fmt.Sprint(newip)).To(Equal("192.168.0.10"))

		exrange = []string{"192.168.0.0/30", "192.168.0.14/31", "192.168.0.4/30", "192.168.0.6/31", "192.168.0.8/31"}
		newip, _, _ = IterateForAssignment(*ipnet, calculatedrangestart, nil, ipres, exrange, "0xdeadbeef", "", "")
		Expect(fmt.Sprint(newip)).To(Equal("192.168.0.10"))
	})

	It("can IterateForAssignment on an IPv4 address excluding a range and respect the requested range", func() {

		firstip, ipnet, err := net.ParseCIDR("192.168.0.0/29")
		Expect(err).NotTo(HaveOccurred())

		ipres := []types.IPReservation{
			{
				IP:     net.ParseIP("192.168.0.4"),
				PodRef: "default/pod1",
			},
			{
				IP:     net.ParseIP("192.168.0.5"),
				PodRef: "default/pod1",
			},
			{
				IP:     net.ParseIP("192.168.0.6"),
				PodRef: "default/pod1",
			},
		}
		exrange := []string{"192.168.0.0/30"}
		_, _, err = IterateForAssignment(*ipnet, firstip, nil, ipres, exrange, "0xdeadbeef", "", "")
		Expect(err).To(MatchError(HavePrefix("Could not allocate IP in range")))

	})
	It("can IterateForAssignment on an IPv4 address excluding the last allocatable IP and respect the requested range", func() {

		firstip, ipnet, err := net.ParseCIDR("192.168.0.0/29")
		Expect(err).NotTo(HaveOccurred())

		ipres := []types.IPReservation{
			{
				IP:     net.ParseIP("192.168.0.1"),
				PodRef: "default/pod1",
			},
			{
				IP:     net.ParseIP("192.168.0.2"),
				PodRef: "default/pod1",
			},
			{
				IP:     net.ParseIP("192.168.0.3"),
				PodRef: "default/pod1",
			},
		}
		exrange := []string{"192.168.0.4/30"}
		_, _, err = IterateForAssignment(*ipnet, firstip, nil, ipres, exrange, "0xdeadbeef", "", "")
		Expect(err).To(MatchError(HavePrefix("Could not allocate IP in range")))

	})

	It("can IterateForAssignment on an IPv6 address excluding a range and respect the requested range", func() {

		firstip, ipnet, err := net.ParseCIDR("100::2:1/125")
		Expect(err).NotTo(HaveOccurred())

		ipres := []types.IPReservation{
			{
				IP:     net.ParseIP("100::2:1"),
				PodRef: "default/pod1",
			},
			{
				IP:     net.ParseIP("100::2:2"),
				PodRef: "default/pod1",
			},
			{
				IP:     net.ParseIP("100::2:3"),
				PodRef: "default/pod1",
			},
		}

		exrange := []string{"100::2:4/126"}
		_, _, err = IterateForAssignment(*ipnet, firstip, nil, ipres, exrange, "0xdeadbeef", "", "")
		Expect(err).To(MatchError(HavePrefix("Could not allocate IP in range")))

	})

	// Make sure that the network IP and the broadcast IP are excluded from the range.
	// According to https://github.com/telekom/whereabouts, we look at a range "... excluding the first
	// network address and the last broadcast address".
	When("no range_end is specified", func() {
		It("the range start and end must be ignored if they are not within the bounds of the range for IPv4", func() {
			_, ipnet, err := net.ParseCIDR("192.168.0.0/29")
			Expect(err).NotTo(HaveOccurred())
			rangeStart := net.ParseIP("192.168.0.0") // Network address, out of bounds.
			newip, _, err := IterateForAssignment(*ipnet, rangeStart, nil, nil, nil, "0xdeadbeef", "", "")
			Expect(err).NotTo(HaveOccurred())
			Expect(fmt.Sprint(newip)).To(Equal("192.168.0.1"))
		})
	})

	When("a range_end is specified", func() {
		It("the range start and end must be ignored if they are not within the bounds of the range for IPv4", func() {
			_, ipnet, err := net.ParseCIDR("192.168.0.0/29")
			Expect(err).NotTo(HaveOccurred())
			rangeStart := net.ParseIP("192.168.0.0") // Network address, out of bounds.
			rangeEnd := net.ParseIP("192.168.0.8")   // Broadcast address, out of bounds.
			newip, _, err := IterateForAssignment(*ipnet, rangeStart, rangeEnd, nil, nil, "0xdeadbeef", "", "")
			Expect(err).NotTo(HaveOccurred())
			Expect(fmt.Sprint(newip)).To(Equal("192.168.0.1"))
		})
	})

	// Make sure that the end_range parameter is respected even when it is part of the exclude range.
	It("can IterateForAssignment on an IPv4 address excluding a range and respect endrange value", func() {
		_, ipnet, err := net.ParseCIDR("192.168.0.0/28")
		Expect(err).NotTo(HaveOccurred())
		startip := net.ParseIP("192.168.0.1")
		lastip := net.ParseIP("192.168.0.6")

		ipres := []types.IPReservation{
			{
				IP:     net.ParseIP("192.168.0.1"),
				PodRef: "default/pod1",
			},
			{
				IP:     net.ParseIP("192.168.0.2"),
				PodRef: "default/pod1",
			},
			{
				IP:     net.ParseIP("192.168.0.3"),
				PodRef: "default/pod1",
			},
		}
		exrange := []string{"192.168.0.4/30"}
		_, _, err = IterateForAssignment(*ipnet, startip, lastip, ipres, exrange, "0xdeadbeef", "", "")
		Expect(err).To(MatchError(HavePrefix("Could not allocate IP in range")))
	})

	Context("test reserve lists", func() {
		When("an empty reserve list is provided", func() {
			It("is properly updated", func() {
				_, ipnet, err := net.ParseCIDR("192.168.0.0/28")
				Expect(err).NotTo(HaveOccurred())
				startip := net.ParseIP("192.168.0.1")
				lastip := net.ParseIP("192.168.0.6")

				ipres := []types.IPReservation{}
				_, ipres, err = IterateForAssignment(*ipnet, startip, lastip, ipres, nil, "0xdeadbeef", "dummy-0", "")
				Expect(err).NotTo(HaveOccurred())
				Expect(len(ipres)).To(Equal(1))
				Expect(fmt.Sprint(ipres[0].IP)).To(Equal("192.168.0.1"))
			})
		})

		When("a reserve list is provided", func() {
			It("is properly updated", func() {
				_, ipnet, err := net.ParseCIDR("192.168.0.0/28")
				Expect(err).NotTo(HaveOccurred())
				startip := net.ParseIP("192.168.0.1")
				lastip := net.ParseIP("192.168.0.6")

				ipres := []types.IPReservation{
					{
						IP:     net.ParseIP("192.168.0.1"),
						PodRef: "default/pod1",
					},
					{
						IP:     net.ParseIP("192.168.0.2"),
						PodRef: "default/pod1",
					},
					{
						IP:     net.ParseIP("192.168.0.3"),
						PodRef: "default/pod1",
					},
				}

				_, ipres, err = IterateForAssignment(*ipnet, startip, lastip, ipres, nil, "0xdeadbeef", "dummy-0", "")
				Expect(err).NotTo(HaveOccurred())
				Expect(len(ipres)).To(Equal(4))
				Expect(fmt.Sprint(ipres[3].IP)).To(Equal("192.168.0.4"))
			})
		})

		When("a reserve list with a hole is provided", func() {
			It("is properly updated", func() {
				_, ipnet, err := net.ParseCIDR("192.168.0.0/28")
				Expect(err).NotTo(HaveOccurred())
				startip := net.ParseIP("192.168.0.1")
				lastip := net.ParseIP("192.168.0.6")

				ipres := []types.IPReservation{
					{
						IP:     net.ParseIP("192.168.0.1"),
						PodRef: "default/pod1",
					},
					{
						IP:     net.ParseIP("192.168.0.2"),
						PodRef: "default/pod1",
					},
					{
						IP:     net.ParseIP("192.168.0.5"),
						PodRef: "default/pod1",
					},
				}

				_, ipres, err = IterateForAssignment(*ipnet, startip, lastip, ipres, nil, "0xdeadbeef", "dummy-0", "")
				Expect(err).NotTo(HaveOccurred())
				Expect(len(ipres)).To(Equal(4))
				Expect(fmt.Sprint(ipres[3].IP)).To(Equal("192.168.0.3"))
			})
		})
	})

	Context("DeallocateIP", func() {
		It("removes the matching reservation and returns its IP", func() {
			reservelist := []types.IPReservation{
				{IP: net.ParseIP("192.168.1.1"), ContainerID: "aaa", PodRef: "default/pod1", IfName: "eth0"},
				{IP: net.ParseIP("192.168.1.2"), ContainerID: "bbb", PodRef: "default/pod2", IfName: "eth0"},
				{IP: net.ParseIP("192.168.1.3"), ContainerID: "ccc", PodRef: "default/pod3", IfName: "net1"},
			}
			updatedList, ip := DeallocateIP(reservelist, "bbb", "eth0")
			Expect(ip).To(Equal(net.ParseIP("192.168.1.2")))
			Expect(updatedList).To(HaveLen(2))
			// Swap-remove: last element replaces removed one
			Expect(fmt.Sprint(updatedList[0].IP)).To(Equal("192.168.1.1"))
			Expect(fmt.Sprint(updatedList[1].IP)).To(Equal("192.168.1.3"))
		})

		It("returns nil IP and unchanged list when containerID is not found", func() {
			reservelist := []types.IPReservation{
				{IP: net.ParseIP("192.168.1.1"), ContainerID: "aaa", PodRef: "default/pod1", IfName: "eth0"},
			}
			updatedList, ip := DeallocateIP(reservelist, "zzz", "eth0")
			Expect(ip).To(BeNil())
			Expect(updatedList).To(HaveLen(1))
		})

		It("matches on both containerID and ifName", func() {
			reservelist := []types.IPReservation{
				{IP: net.ParseIP("192.168.1.1"), ContainerID: "aaa", PodRef: "default/pod1", IfName: "eth0"},
				{IP: net.ParseIP("192.168.1.2"), ContainerID: "aaa", PodRef: "default/pod1", IfName: "net1"},
			}
			updatedList, ip := DeallocateIP(reservelist, "aaa", "net1")
			Expect(ip).To(Equal(net.ParseIP("192.168.1.2")))
			Expect(updatedList).To(HaveLen(1))
			Expect(updatedList[0].IfName).To(Equal("eth0"))
		})

		It("handles single-element list", func() {
			reservelist := []types.IPReservation{
				{IP: net.ParseIP("192.168.1.1"), ContainerID: "aaa", PodRef: "default/pod1", IfName: "eth0"},
			}
			updatedList, ip := DeallocateIP(reservelist, "aaa", "eth0")
			Expect(ip).To(Equal(net.ParseIP("192.168.1.1")))
			Expect(updatedList).To(BeEmpty())
		})
	})

	Context("AssignIP idempotency", func() {
		It("returns the existing IP when podRef and ifName already have an allocation", func() {
			ipamConf := types.RangeConfiguration{
				Range:      "192.168.1.0/24",
				OmitRanges: []string{},
			}
			reservelist := []types.IPReservation{
				{IP: net.ParseIP("192.168.1.5"), ContainerID: "old-id", PodRef: "default/mypod", IfName: "eth0"},
			}
			result, updatedList, err := AssignIP(ipamConf, reservelist, "new-id", "default/mypod", "eth0")
			Expect(err).NotTo(HaveOccurred())
			Expect(fmt.Sprint(result.IP)).To(Equal("192.168.1.5"))
			Expect(updatedList).To(HaveLen(1))
			// containerID should be updated to the new one
			Expect(updatedList[0].ContainerID).To(Equal("new-id"))
		})

		It("allocates a new IP when podRef matches but ifName differs", func() {
			ipamConf := types.RangeConfiguration{
				Range:      "192.168.1.0/24",
				OmitRanges: []string{},
			}
			reservelist := []types.IPReservation{
				{IP: net.ParseIP("192.168.1.5"), ContainerID: "aaa", PodRef: "default/mypod", IfName: "eth0"},
			}
			result, updatedList, err := AssignIP(ipamConf, reservelist, "bbb", "default/mypod", "net1")
			Expect(err).NotTo(HaveOccurred())
			// Should get a different IP (192.168.1.1 as the lowest available)
			Expect(fmt.Sprint(result.IP)).To(Equal("192.168.1.1"))
			Expect(updatedList).To(HaveLen(2))
		})
	})
})
