package network

import (
	"context"
	"fmt"
	"net"
	"os/exec"

	"github.com/vishvananda/netlink"
)

const (
	// Default Bridge Interface Name
	DefaultBridgeInterfaceName            string = "br-ahwlan"
	DefaultBatmanInterfaceName            string = "bat0"
	DefaultEthernetInterfaceName          string = "eth0"
	DefaultSecondaryEthernetInterfaceName string = "eth1"

	// DefaultBridgeMTU and DefaultEthernetMTU are the transport MTUs
	// for br-ahwlan and the ethernet ports bridged into it: 1500 minus
	// the batman-adv header, so frames from the wired side fit through
	// bat0 without fragmentation. The daemon applies them through
	// netlink at every start (internal/mgmt) and the setup wizard
	// persists them as `option mtu` on the UCI device sections so a
	// network reload keeps them.
	DefaultBridgeMTU   int = 1460
	DefaultEthernetMTU int = 1460
)

type NetworkInterface struct {
	Name  string
	MAC   string
	IP    []IPAddress
	MTU   int
	Flags net.Flags
}

type IPAddress struct {
	IP        net.IP
	Netmask   net.IPMask
	Broadcast net.IP
}

// GetInterfaceByName retrieves information about a network interface by its name.
// It returns an NetworkInterface struct containing details such as the interface's name,
// MTU, flags, MAC address, and associated IP addresses. If the interface is not found
// or an error occurs while fetching interfaces, an empty NetworkInterface is returned.
//
// Parameters:
//   - name: The name of the network interface to look up.
//
// Returns:
//   - NetworkInterface: Struct with details of the specified network interface.
func GetInterfaceByName(name string) NetworkInterface {
	// Get all network interface information of the system
	interfaces, err := net.Interfaces()
	if err != nil {
		fmt.Println("Failed to get network interface information: ", err)

		return NetworkInterface{}
	}

	for _, iface := range interfaces {
		if iface.Name == name {
			return NetworkInterface{
				Name:  iface.Name,
				MTU:   iface.MTU,
				Flags: iface.Flags,
				MAC:   iface.HardwareAddr.String(),
				IP:    getInterfaceIPAddresses(iface),
			}
		}
	}

	return NetworkInterface{}
}

func getInterfaceIPAddresses(iface net.Interface) []IPAddress {
	var ipAddresses []IPAddress

	addrs, err := iface.Addrs()
	if err != nil {
		fmt.Println("Failed to get IP addresses for interface: ", err)

		return ipAddresses
	}

	for _, addr := range addrs {
		var ip net.IP

		var netmask net.IPMask

		var broadcast net.IP

		switch v := addr.(type) {
		case *net.IPNet:
			ip = v.IP
			netmask = v.Mask
			broadcast = calculateBroadcastAddress(v)
		case *net.IPAddr:
			ip = v.IP
			netmask = ip.DefaultMask()
			broadcast = calculateBroadcastAddress(&net.IPNet{IP: v.IP, Mask: netmask})
		}

		ipAddresses = append(ipAddresses, IPAddress{
			IP:        ip,
			Netmask:   netmask,
			Broadcast: broadcast,
		})
	}

	return ipAddresses
}

func calculateBroadcastAddress(ipNet *net.IPNet) net.IP {
	ip := ipNet.IP.To4()
	if ip == nil {
		return nil
	}

	broadcast := make(net.IP, len(ip))
	for i := 0; i < len(ip); i++ {
		broadcast[i] = ip[i] | ^ipNet.Mask[i]
	}

	return broadcast
}

// GetCIDR returns the CIDR notation(s) for the network interface.
// It converts each IP address and its netmask into CIDR format (e.g., "192.168.1.10/24").
// If the interface has no IP addresses, an empty slice is returned.
//
// Returns:
//   - []string: A slice of CIDR notation strings for all IP addresses on the interface.
//
// Example:
//
//	iface := GetInterfaceByName("eth0")
//	cidrs := iface.GetCIDR()
//	for _, cidr := range cidrs {
//	    fmt.Println(cidr)  // Output: "192.168.1.10/24"
//	}
func (ni *NetworkInterface) GetCIDR() []string {
	var cidrs []string

	for _, ipAddr := range ni.IP {
		if ipAddr.IP == nil || ipAddr.Netmask == nil {
			continue
		}

		// Create IPNet from IP and Netmask
		ipNet := &net.IPNet{
			IP:   ipAddr.IP,
			Mask: ipAddr.Netmask,
		}

		cidrs = append(cidrs, ipNet.String())
	}

	return cidrs
}

// SetMTU sets the MTU (Maximum Transmission Unit) for a network interface.
// It uses the netlink library to configure the interface MTU.
//
// Parameters:
//   - name: The name of the network interface to modify.
//   - mtu: The desired MTU value in bytes.
//
// Returns:
//   - error: An error if the interface doesn't exist or if setting the MTU fails, nil otherwise.
//
// Example:
//
//	err := SetMTU("eth0", 1500)
//	if err != nil {
//	    log.Printf("Failed to set MTU: %v", err)
//	}
//
// Note: This function requires appropriate permissions to modify network interfaces.
// If you unit test this, you will most likely want to mock the netlink interactions to avoid modifying actual network settings during tests.
func SetMTU(name string, mtu int) error {
	// Get the network interface by name
	link, err := netlink.LinkByName(name)
	if err != nil {
		return fmt.Errorf("interface %s not found: %w", name, err)
	}

	// Set the MTU using netlink
	if err := netlink.LinkSetMTU(link, mtu); err != nil {
		return fmt.Errorf("failed to set MTU for %s: %w", name, err)
	}

	return nil
}

// GetNetworkCIDR returns the network CIDR address for a network interface.
// It calculates the network address by ANDing the IP address with its netmask,
// then returns it in CIDR notation (e.g., "10.41.0.0/16" for IP 10.41.1.1 with /16 mask).
//
// Parameters:
//   - name: The name of the network interface to query.
//
// Returns:
//   - string: The network CIDR address (e.g., "10.41.0.0/16"), empty string if no IPv4 address found.
//   - error: An error if the interface doesn't exist or if fetching addresses fails, nil otherwise.
//
// Example:
//
//	networkCIDR, err := GetNetworkCIDR("eth0")
//	if err != nil {
//	    log.Printf("Failed to get network CIDR: %v", err)
//	}
//	fmt.Println(networkCIDR)  // Output: "10.41.0.0/16"
//
// Note: This function returns only the first IPv4 network address found. IPv6 addresses are skipped.
func GetNetworkCIDR(name string) (string, error) {
	// Get the network interface by name using netlink
	link, err := netlink.LinkByName(name)
	if err != nil {
		return "", fmt.Errorf("interface %s not found: %w", name, err)
	}

	// Get all addresses associated with the interface
	addrs, err := netlink.AddrList(link, netlink.FAMILY_V4)
	if err != nil {
		return "", fmt.Errorf("failed to get addresses for interface %s: %w", name, err)
	}

	if len(addrs) == 0 {
		return "", fmt.Errorf("no IPv4 addresses found on interface %s", name)
	}

	// Use the first IPv4 address
	addr := addrs[0]
	if addr.IPNet == nil {
		return "", fmt.Errorf("invalid IP address on interface %s", name)
	}

	// Calculate the network address by ANDing IP with netmask
	ip := addr.IP.To4()
	if ip == nil {
		return "", fmt.Errorf("not an IPv4 address on interface %s", name)
	}

	mask := addr.Mask
	networkIP := make(net.IP, len(ip))

	for i := 0; i < len(ip); i++ {
		networkIP[i] = ip[i] & mask[i]
	}

	// Create the network CIDR
	networkCIDR := &net.IPNet{
		IP:   networkIP,
		Mask: mask,
	}

	return networkCIDR.String(), nil
}

// PerformIfUp brings up a network interface by name.
// It executes the "ifup" command with the provided interface name.
// Returns an error if the command fails to execute or if the interface
// cannot be brought up.
func PerformIfUp(ctx context.Context, name string) error {
	cmd := exec.CommandContext(ctx, "ifup", name)

	return cmd.Run()
}

// PerformIfDown brings down a network interface by name.
// It executes the "ifdown" command with the provided interface name.
// Returns an error if the command fails to execute or if the interface
// cannot be brought down.
func PerformIfDown(ctx context.Context, name string) error {
	cmd := exec.CommandContext(ctx, "ifdown", name)

	return cmd.Run()
}
