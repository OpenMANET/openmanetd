package network

import (
	"context"
	"fmt"
	"math/rand"
	"net"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/digineo/go-uci/v2"
	"github.com/openmanet/openmanetd/internal/database/models"
)

const (
	networkConfigName     string = "network"
	DefaultNetworkAddress string = "10.41.0.0"
	DefaultNetworkMask    string = "255.255.0.0"
	DefaultNetworkProto   string = "static"
	DefaultIPv6Assign     string = "64"
	DefaultIPv6Class      string = "local"
	DefaultIPv6IfaceID    string = "eui64"

	DefaultULAPrefix string = "fd01:ed20:ecb4::/48"
)

// UCINetworkConfig represents the UCI network configuration.
type UCINetwork struct {
	Proto          string `uci:"option proto"`
	NetMask        string `uci:"option netmask"`
	IPAddr         string `uci:"option ipaddr"`
	Gateway        string `uci:"option gateway"`
	DNS            string `uci:"option dns"`
	Device         string `uci:"option device"`
	Master         string `uci:"option master"`
	IPV6Assignment string `uci:"option ip6assign"`
	IPV6IfaceID    string `uci:"option ip6ifaceid"`
	IPV6Class      string `uci:"list ip6class"`
	// MulticastMode is the batman-adv `multicast_mode` option on a
	// `proto batadv` interface. The kernel defines it as the negation of
	// forceflood: "0" classic-floods every multicast frame (forceflood on),
	// "1" enables the IGMP/MLD-snooping optimisations (forceflood off).
	// See MulticastModeForForceflood.
	MulticastMode string `uci:"option multicast_mode"`
}

// UCIDevice represents a UCI network device configuration (config device).
// Devices can be physical interfaces, bridges, VLANs, or other virtual devices.
type UCIDevice struct {
	Name             string   `uci:"option name"`
	Type             string   `uci:"option type"`
	MacAddr          string   `uci:"option macaddr"`
	Ifname           string   `uci:"option ifname"`
	RxPause          string   `uci:"option rxpause"`
	TxPause          string   `uci:"option txpause"`
	AutoNeg          string   `uci:"option autoneg"`
	Speed            string   `uci:"option speed"`
	Duplex           string   `uci:"option duplex"`
	Table            string   `uci:"option table"`
	IgmpSnooping     string   `uci:"option igmp_snooping"`
	MulticastQuerier string   `uci:"option multicast_querier"`
	MTU              string   `uci:"option mtu"`
	Ports            []string `uci:"list ports"`
}

// RemovePort removes a port from the Ports list if it exists.
// Returns true if the port was found and removed, false otherwise.
//
// Example:
//
//	device := &UCIDevice{
//	    Ports: []string{"eth0", "eth1", "bat0"},
//	}
//	removed := device.RemovePort("eth1")  // true
//	// device.Ports is now []string{"eth0", "bat0"}
func (d *UCIDevice) RemovePort(port string) bool {
	for i, p := range d.Ports {
		if p == port {
			// Remove the port by creating a new slice
			d.Ports = append(d.Ports[:i], d.Ports[i+1:]...)

			return true
		}
	}

	return false
}

// ConfigReader defines an interface for reading UCI configuration values.
type ConfigReader interface {
	Get(config, section, option string) ([]string, bool)
	GetSections(config, secType string) ([]string, error)
	SetType(config, section, option string, typ uci.OptionType, values ...string) error
	Del(config, section, option string) error
	AddSection(config, section, typ string) error
	DelSection(config, section string) error
	Commit() error
	ReloadConfig() error
}

// UCINetworkConfigReader wraps the UCI functions for network configuration.
type UCINetworkConfigReader struct {
	tree uci.Tree
}

// NewUCINetworkConfigReader creates a new UCI network config reader with the default tree.
func NewUCINetworkConfigReader() *UCINetworkConfigReader {
	return &UCINetworkConfigReader{
		tree: uci.NewTree(uci.DefaultTreePath),
	}
}

func (r *UCINetworkConfigReader) Get(config, section, option string) ([]string, bool) {
	return r.tree.Get(config, section, option)
}

func (r *UCINetworkConfigReader) GetSections(config, secType string) ([]string, error) {
	return r.tree.GetSections(config, secType)
}

func (r *UCINetworkConfigReader) SetType(config, section, option string, typ uci.OptionType, values ...string) error {
	return r.tree.SetType(config, section, option, typ, values...)
}

func (r *UCINetworkConfigReader) Del(config, section, option string) error {
	return r.tree.Del(config, section, option)
}

func (r *UCINetworkConfigReader) AddSection(config, section, typ string) error {
	return r.tree.AddSection(config, section, typ)
}

func (r *UCINetworkConfigReader) DelSection(config, section string) error {
	return r.tree.DelSection(config, section)
}

func (r *UCINetworkConfigReader) Commit() error {
	return r.tree.Commit()
}

func (r *UCINetworkConfigReader) ReloadConfig() error {
	return r.tree.LoadConfig(networkConfigName, true)
}

// GetUCINetworkByName loads and returns the UCI network configuration by name.
//
// Parameters:
//   - name: The UCI section name (e.g., "lan", "wan", "ahwlan")
//
// Returns the network configuration or an error if it cannot be read.
//
// Example:
//
//	config, err := GetUCINetworkByName("lan")
//	if err != nil {
//	    log.Fatalf("Failed to get network config: %v", err)
//	}
//	fmt.Printf("IP Address: %s\n", config.IPAddr)
func GetUCINetworkByName(name string) (*UCINetwork, error) {
	return GetUCINetworkByNameWithReader(name, NewUCINetworkConfigReader())
}

// GetUCINetworkByNameWithReader loads and returns the UCI network configuration by name using the provided reader.
func GetUCINetworkByNameWithReader(name string, reader ConfigReader) (*UCINetwork, error) {
	var config UCINetwork

	if values, ok := reader.Get(networkConfigName, name, optionProto); ok && len(values) > 0 {
		config.Proto = values[0]
	}

	if values, ok := reader.Get(networkConfigName, name, optionNetmask); ok && len(values) > 0 {
		config.NetMask = values[0]
	}

	if values, ok := reader.Get(networkConfigName, name, "ipaddr"); ok && len(values) > 0 {
		config.IPAddr = values[0]
	}

	if values, ok := reader.Get(networkConfigName, name, "gateway"); ok && len(values) > 0 {
		config.Gateway = values[0]
	}

	if values, ok := reader.Get(networkConfigName, name, "dns"); ok && len(values) > 0 {
		config.DNS = values[0]
	}

	if values, ok := reader.Get(networkConfigName, name, networkDeviceType); ok && len(values) > 0 {
		config.Device = values[0]
	}

	if values, ok := reader.Get(networkConfigName, name, "master"); ok && len(values) > 0 {
		config.Master = values[0]
	}

	if values, ok := reader.Get(networkConfigName, name, optionIP6Assign); ok && len(values) > 0 {
		config.IPV6Assignment = values[0]
	}

	if values, ok := reader.Get(networkConfigName, name, optionIP6IfaceID); ok && len(values) > 0 {
		config.IPV6IfaceID = values[0]
	}

	if values, ok := reader.Get(networkConfigName, name, "ip6class"); ok && len(values) > 0 {
		config.IPV6Class = values[0]
	}

	return &config, nil
}

// SetNetworkConfig creates or updates a network interface configuration.
//
// Parameters:
//   - section: The UCI section name (e.g., "lan", "wan", "ahwlan")
//   - config: The network configuration to set
//
// Returns an error if the configuration cannot be saved.
//
// Example:
//
//	netConfig := &UCINetwork{
//	    Proto:   "static",
//	    IPAddr:  "192.168.1.1",
//	    NetMask: "255.255.255.0",
//	}
//	err := SetNetworkConfig("lan", netConfig)
//
// Note: This operation requires appropriate privileges and commits the configuration.
func SetNetworkConfig(section string, config *UCINetwork) error {
	return SetNetworkConfigWithReader(section, config, NewUCINetworkConfigReader())
}

// SetNetworkConfigWithReader creates or updates a network interface configuration using the provided reader.
func SetNetworkConfigWithReader(section string, config *UCINetwork, reader ConfigReader) error { //nolint:gocognit
	if config == nil {
		return fmt.Errorf("config cannot be nil")
	}

	// Add section if it doesn't exist (this will fail silently if it exists)
	_ = reader.AddSection(networkConfigName, section, networkInterfaceType)

	if config.Proto != "" {
		if err := reader.SetType(networkConfigName, section, optionProto, uci.TypeOption, config.Proto); err != nil {
			return fmt.Errorf("failed to set proto: %w", err)
		}
	}

	if config.NetMask != "" {
		if err := reader.SetType(networkConfigName, section, optionNetmask, uci.TypeOption, config.NetMask); err != nil {
			return fmt.Errorf("failed to set netmask: %w", err)
		}
	}

	if config.IPAddr != "" {
		if err := reader.SetType(networkConfigName, section, "ipaddr", uci.TypeOption, config.IPAddr); err != nil {
			return fmt.Errorf("failed to set ipaddr: %w", err)
		}
	}

	if config.Gateway != "" {
		if err := reader.SetType(networkConfigName, section, "gateway", uci.TypeOption, config.Gateway); err != nil {
			return fmt.Errorf("failed to set gateway: %w", err)
		}
	}

	if config.DNS != "" {
		if err := reader.SetType(networkConfigName, section, "dns", uci.TypeOption, config.DNS); err != nil {
			return fmt.Errorf("failed to set dns: %w", err)
		}
	}

	if config.Device != "" {
		if err := reader.SetType(networkConfigName, section, networkDeviceType, uci.TypeOption, config.Device); err != nil {
			return fmt.Errorf("failed to set device: %w", err)
		}
	}

	if config.Master != "" {
		if err := reader.SetType(networkConfigName, section, "master", uci.TypeOption, config.Master); err != nil {
			return fmt.Errorf("failed to set master: %w", err)
		}
	}

	if config.IPV6Assignment != "" {
		if err := reader.SetType(networkConfigName, section, optionIP6Assign, uci.TypeOption, config.IPV6Assignment); err != nil {
			return fmt.Errorf("failed to set ip6assign: %w", err)
		}
	}

	if config.IPV6IfaceID != "" {
		if err := reader.SetType(networkConfigName, section, optionIP6IfaceID, uci.TypeOption, config.IPV6IfaceID); err != nil {
			return fmt.Errorf("failed to set ip6ifaceid: %w", err)
		}
	}

	if config.IPV6Class != "" {
		if err := reader.SetType(networkConfigName, section, "ip6class", uci.TypeList, config.IPV6Class); err != nil {
			return fmt.Errorf("failed to set ip6class: %w", err)
		}
	}

	if config.MulticastMode != "" {
		if err := reader.SetType(networkConfigName, section, optionMulticastMode, uci.TypeOption, config.MulticastMode); err != nil {
			return fmt.Errorf("failed to set multicast_mode: %w", err)
		}
	}

	if err := reader.Commit(); err != nil {
		return fmt.Errorf("failed to commit network config: %w", err)
	}

	return nil
}

// DeleteNetworkConfig removes a network interface configuration section.
//
// Parameters:
//   - section: The UCI section name to delete (e.g., "lan", "wan")
//
// Returns an error if the section cannot be deleted.
//
// Example:
//
//	err := DeleteNetworkConfig("guest")
//	if err != nil {
//	    log.Fatalf("Failed to delete network config: %v", err)
//	}
//
// Note: This operation requires appropriate privileges and commits the configuration.
func DeleteNetworkConfig(section string) error {
	return DeleteNetworkConfigWithReader(section, NewUCINetworkConfigReader())
}

// DeleteNetworkConfigWithReader removes a network interface configuration section using the provided reader.
func DeleteNetworkConfigWithReader(section string, reader ConfigReader) error {
	if err := reader.DelSection(networkConfigName, section); err != nil {
		return fmt.Errorf("failed to delete network section: %w", err)
	}

	if err := reader.Commit(); err != nil {
		return fmt.Errorf("failed to commit network config: %w", err)
	}

	return nil
}

// NetworkSectionExists checks if a network section exists in the configuration.
//
// Parameters:
//   - section: The UCI section name to check (e.g., "lan", "wan", "ahwlan")
//
// Returns true if the section exists, false otherwise.
//
// Example:
//
//	exists := NetworkSectionExists("lan")
//	if exists {
//	    fmt.Println("LAN section exists")
//	}
func NetworkSectionExists(section string) bool {
	return NetworkSectionExistsWithReader(section, NewUCINetworkConfigReader())
}

// NetworkSectionExistsWithReader checks if a network section exists using the provided reader.
func NetworkSectionExistsWithReader(section string, reader ConfigReader) bool {
	// Try to get any option from the section to verify it exists
	// We check for 'proto' as it's a common option in network sections
	_, exists := reader.Get(networkConfigName, section, optionProto)

	return exists
}

// SetNetworkProto sets the protocol for a network interface.
//
// Parameters:
//   - section: The UCI section name (e.g., "lan", "wan")
//   - proto: The protocol (e.g., "static", "dhcp", "batadv")
//
// Example:
//
//	err := SetNetworkProto("wan", "dhcp")
func SetNetworkProto(section, proto string) error {
	return SetNetworkProtoWithReader(section, proto, NewUCINetworkConfigReader())
}

// SetNetworkProtoWithReader sets the protocol using the provided reader.
func SetNetworkProtoWithReader(section, proto string, reader ConfigReader) error {
	if err := reader.SetType(networkConfigName, section, optionProto, uci.TypeOption, proto); err != nil {
		return fmt.Errorf("failed to set proto: %w", err)
	}

	if err := reader.Commit(); err != nil {
		return fmt.Errorf("failed to commit network config: %w", err)
	}

	return nil
}

// SetNetworkIPAddr sets the IP address for a network interface.
//
// Parameters:
//   - section: The UCI section name (e.g., "lan", "wan")
//   - ipaddr: The IP address (e.g., "192.168.1.1")
//
// Example:
//
//	err := SetNetworkIPAddr("lan", "192.168.1.1")
func SetNetworkIPAddr(section, ipaddr string) error {
	return SetNetworkIPAddrWithReader(section, ipaddr, NewUCINetworkConfigReader())
}

// SetNetworkIPAddrWithReader sets the IP address using the provided reader.
func SetNetworkIPAddrWithReader(section, ipaddr string, reader ConfigReader) error {
	if err := reader.SetType(networkConfigName, section, "ipaddr", uci.TypeOption, ipaddr); err != nil {
		return fmt.Errorf("failed to set ipaddr: %w", err)
	}

	if err := reader.Commit(); err != nil {
		return fmt.Errorf("failed to commit network config: %w", err)
	}

	return nil
}

// SetNetworkNetmask sets the netmask for a network interface.
//
// Parameters:
//   - section: The UCI section name (e.g., "lan", "wan")
//   - netmask: The netmask (e.g., "255.255.255.0")
//
// Example:
//
//	err := SetNetworkNetmask("lan", "255.255.255.0")
func SetNetworkNetmask(section, netmask string) error {
	return SetNetworkNetmaskWithReader(section, netmask, NewUCINetworkConfigReader())
}

// SetNetworkNetmaskWithReader sets the netmask using the provided reader.
func SetNetworkNetmaskWithReader(section, netmask string, reader ConfigReader) error {
	if err := reader.SetType(networkConfigName, section, optionNetmask, uci.TypeOption, netmask); err != nil {
		return fmt.Errorf("failed to set netmask: %w", err)
	}

	if err := reader.Commit(); err != nil {
		return fmt.Errorf("failed to commit network config: %w", err)
	}

	return nil
}

// SetNetworkGateway sets the gateway for a network interface.
//
// Parameters:
//   - section: The UCI section name (e.g., "lan", "wan")
//   - gateway: The gateway IP address (e.g., "192.168.1.254")
//
// Example:
//
//	err := SetNetworkGateway("wan", "192.168.1.254")
func SetNetworkGateway(section, gateway string) error {
	return SetNetworkGatewayWithReader(section, gateway, NewUCINetworkConfigReader())
}

// SetNetworkGatewayWithReader sets the gateway using the provided reader.
func SetNetworkGatewayWithReader(section, gateway string, reader ConfigReader) error {
	if err := reader.SetType(networkConfigName, section, "gateway", uci.TypeOption, gateway); err != nil {
		return fmt.Errorf("failed to set gateway: %w", err)
	}

	if err := reader.Commit(); err != nil {
		return fmt.Errorf("failed to commit network config: %w", err)
	}

	return nil
}

// DeleteNetworkGateway removes the gateway configuration for a network interface.
//
// Parameters:
//   - section: The UCI section name (e.g., "lan", "wan")
//
// Returns an error if the gateway cannot be deleted.
//
// Example:
//
//	err := DeleteNetworkGateway("wan")
//	if err != nil {
//	    log.Fatalf("Failed to delete gateway: %v", err)
//	}
//
// Note: This operation requires appropriate privileges and commits the configuration.
func DeleteNetworkGateway(section string) error {
	return DeleteNetworkGatewayWithReader(section, NewUCINetworkConfigReader())
}

// DeleteNetworkGatewayWithReader removes the gateway configuration using the provided reader.
func DeleteNetworkGatewayWithReader(section string, reader ConfigReader) error {
	if err := reader.Del(networkConfigName, section, "gateway"); err != nil {
		return fmt.Errorf("failed to delete gateway: %w", err)
	}

	if err := reader.Commit(); err != nil {
		return fmt.Errorf("failed to commit network config: %w", err)
	}

	return nil
}

// SetNetworkDNS sets the DNS server for a network interface.
//
// Parameters:
//   - section: The UCI section name (e.g., "lan", "wan")
//   - dns: The DNS server IP address (e.g., "1.1.1.1")
//
// Example:
//
//	err := SetNetworkDNS("lan", "1.1.1.1")
func SetNetworkDNS(section, dns string) error {
	return SetNetworkDNSWithReader(section, dns, NewUCINetworkConfigReader())
}

// SetNetworkDNSWithReader sets the DNS server using the provided reader.
func SetNetworkDNSWithReader(section, dns string, reader ConfigReader) error {
	if err := reader.SetType(networkConfigName, section, "dns", uci.TypeOption, dns); err != nil {
		return fmt.Errorf("failed to set dns: %w", err)
	}

	if err := reader.Commit(); err != nil {
		return fmt.Errorf("failed to commit network config: %w", err)
	}

	return nil
}

// SetNetworkDevice sets the device for a network interface.
//
// Parameters:
//   - section: The UCI section name (e.g., "lan", "wan")
//   - device: The device name (e.g., "br-lan", "eth0")
//
// Example:
//
//	err := SetNetworkDevice("lan", "br-lan")
func SetNetworkDevice(section, device string) error {
	return SetNetworkDeviceWithReader(section, device, NewUCINetworkConfigReader())
}

// SetNetworkDeviceWithReader sets the device using the provided reader.
func SetNetworkDeviceWithReader(section, device string, reader ConfigReader) error {
	if err := reader.SetType(networkConfigName, section, networkDeviceType, uci.TypeOption, device); err != nil {
		return fmt.Errorf("failed to set device: %w", err)
	}

	if err := reader.Commit(); err != nil {
		return fmt.Errorf("failed to commit network config: %w", err)
	}

	return nil
}

// SetNetworkMaster sets the master interface for a network interface.
//
// Parameters:
//   - section: The UCI section name (e.g., "lan", "wan")
//   - master: The master interface name (e.g., "br-lan")
//
// Example:
//
//	err := SetNetworkMaster("eth0", "br-lan")
func SetNetworkMaster(section, master string) error {
	return SetNetworkMasterWithReader(section, master, NewUCINetworkConfigReader())
}

// SetNetworkMasterWithReader sets the master interface using the provided reader.
func SetNetworkMasterWithReader(section, master string, reader ConfigReader) error {
	if err := reader.SetType(networkConfigName, section, "master", uci.TypeOption, master); err != nil {
		return fmt.Errorf("failed to set network master: %w", err)
	}

	if err := reader.Commit(); err != nil {
		return fmt.Errorf("failed to commit network master: %w", err)
	}

	return nil
}

// SetNetworkIPV6Assignment sets the IPv6 assignment (prefix length) for a network interface.
//
// Parameters:
//   - section: The UCI section name (e.g., "lan", "wan")
//   - ip6assign: The IPv6 prefix length to assign (e.g., "60", "64")
//
// Example:
//
//	err := SetNetworkIPV6Assignment("lan", "60")
func SetNetworkIPV6Assignment(section, ip6assign string) error {
	return SetNetworkIPV6AssignmentWithReader(section, ip6assign, NewUCINetworkConfigReader())
}

// SetNetworkIPV6AssignmentWithReader sets the IPv6 assignment using the provided reader.
func SetNetworkIPV6AssignmentWithReader(section, ip6assign string, reader ConfigReader) error {
	if err := reader.SetType(networkConfigName, section, optionIP6Assign, uci.TypeOption, ip6assign); err != nil {
		return fmt.Errorf("failed to set ip6assign: %w", err)
	}

	if err := reader.Commit(); err != nil {
		return fmt.Errorf("failed to commit network config: %w", err)
	}

	return nil
}

// SetNetworkIPV6IfaceID sets the IPv6 interface ID for a network interface.
//
// Parameters:
//   - section: The UCI section name (e.g., "lan", "wan")
//   - ip6ifaceid: The IPv6 interface ID (e.g., "::1")
//
// Example:
//
//	err := SetNetworkIPV6IfaceID("lan", "::1")
func SetNetworkIPV6IfaceID(section, ip6ifaceid string) error {
	return SetNetworkIPV6IfaceIDWithReader(section, ip6ifaceid, NewUCINetworkConfigReader())
}

// SetNetworkIPV6IfaceIDWithReader sets the IPv6 interface ID using the provided reader.
func SetNetworkIPV6IfaceIDWithReader(section, ip6ifaceid string, reader ConfigReader) error {
	if err := reader.SetType(networkConfigName, section, optionIP6IfaceID, uci.TypeOption, ip6ifaceid); err != nil {
		return fmt.Errorf("failed to set ip6ifaceid: %w", err)
	}

	if err := reader.Commit(); err != nil {
		return fmt.Errorf("failed to commit network config: %w", err)
	}

	return nil
}

// SetNetworkIPV6Class sets the IPv6 class for a network interface.
//
// Parameters:
//   - section: The UCI section name (e.g., "lan", "wan")
//   - ip6class: The IPv6 class (e.g., "local", "wan6")
//
// Example:
//
//	err := SetNetworkIPV6Class("lan", "local")
func SetNetworkIPV6Class(section, ip6class string) error {
	return SetNetworkIPV6ClassWithReader(section, ip6class, NewUCINetworkConfigReader())
}

// SetNetworkIPV6ClassWithReader sets the IPv6 class using the provided reader.
func SetNetworkIPV6ClassWithReader(section, ip6class string, reader ConfigReader) error {
	if err := reader.SetType(networkConfigName, section, "ip6class", uci.TypeList, ip6class); err != nil {
		return fmt.Errorf("failed to set ip6class: %w", err)
	}

	if err := reader.Commit(); err != nil {
		return fmt.Errorf("failed to commit network config: %w", err)
	}

	return nil
}

// SelectAvailableStaticIPFromNodeData selects an available static IP address from the 10.41.0.0/16 network.
//
// Parameters:
//   - nodes: Array of MeshNode records containing address reservations
//   - gatewayMode: If true, selects from 10.41.0.0/24 range only. If false (default), selects from entire 10.41.0.0/16 range
//
// Returns:
//   - An available IP address from the specified range
//   - An error if no available IP can be found
//
// The function excludes:
//   - Already reserved IP addresses (from StaticIp field in AddressReservation)
//   - The 10.41.0.0/24 range (when gatewayMode is false)
//   - The 10.41.253.0/24 range (when gatewayMode is false)
//   - The 10.41.254.0/24 range (when gatewayMode is false)
//   - Network address (10.41.0.0)
//   - Broadcast address (10.41.255.255 or 10.41.0.255)
//
// Example:
//
//	nodes := []models.MeshNode{ /* ... */ }
//	ip, err := SelectAvailableStaticIPFromNodeData(nodes, false)
//	if err != nil {
//	    log.Fatalf("Failed to select IP: %v", err)
//	}
//	fmt.Printf("Selected IP: %s\n", ip)
func SelectAvailableStaticIPFromNodeData(nodes []models.MeshNode, gatewayMode bool) (string, error) { //nolint:gocognit
	// Collect all reserved IP addresses
	reservedIPs := make(map[string]bool)

	for _, node := range nodes {
		if node.IpAddr != "" {
			reservedIPs[node.IpAddr] = true
		}
	}

	// Verify the base network address is valid
	if net.ParseIP(DefaultNetworkAddress) == nil {
		return "", fmt.Errorf("failed to parse base IP")
	}

	if gatewayMode {
		// Gateway mode: only search in 10.41.0.0/24 range
		for fourthOctet := 1; fourthOctet < 255; fourthOctet++ {
			candidateIP := fmt.Sprintf("10.41.0.%d", fourthOctet)

			// Check if this IP is already reserved
			if reservedIPs[candidateIP] {
				// IP is reserved, continue to next candidate
				continue
			}

			// IP is available, return it
			return candidateIP, nil
		}

		return "", fmt.Errorf("no available IP addresses in 10.41.0.0/24 range")
	}

	// Normal mode: If there are 1 or fewer nodes, select a random IP to avoid conflicts
	// when multiple nodes start simultaneously
	if len(nodes) <= 1 {
		// Initialize random seed
		rng := rand.New(rand.NewSource(time.Now().UnixNano()))

		// Try to find a random available IP (max 1000 attempts to avoid infinite loop)
		for attempt := 0; attempt < 1000; attempt++ {
			// Generate random third octet (1-252, excluding 0, 253, 254, 255)
			thirdOctet := rng.Intn(252) + 1 // Generates 1-252
			if thirdOctet == 253 {
				thirdOctet = 252 // Avoid 253
			}

			// Generate random fourth octet (1-254)
			fourthOctet := rng.Intn(254) + 1 // Generates 1-254

			candidateIP := fmt.Sprintf("10.41.%d.%d", thirdOctet, fourthOctet)

			// Check if this IP is already reserved
			if !reservedIPs[candidateIP] {
				// IP is available, return it
				return candidateIP, nil
			}
		}
		// If random selection didn't find an IP, fall through to sequential search
	}

	// Normal mode: iterate through the 10.41.0.0/16 range
	// We have 256 * 256 = 65536 addresses total
	// Start from 10.41.1.1 (skip network address and 10.41.0.0/24)
	for thirdOctet := 1; thirdOctet < 256; thirdOctet++ {
		// Skip the restricted ranges: 10.41.253.0/24 and 10.41.254.0/24
		if thirdOctet == 253 || thirdOctet == 254 {
			continue
		}

		for fourthOctet := 1; fourthOctet < 255; fourthOctet++ {
			// Skip broadcast address within each /24 subnet
			if fourthOctet == 255 {
				continue
			}

			candidateIP := fmt.Sprintf("10.41.%d.%d", thirdOctet, fourthOctet)

			// Check if this IP is already reserved
			if reservedIPs[candidateIP] {
				// IP is reserved, continue to next candidate
				continue
			}

			// IP is available, return it
			return candidateIP, nil
		}
	}

	return "", fmt.Errorf("no available IP addresses in %s/16 range", DefaultNetworkAddress)
}

// GetDeviceByName loads and returns the UCI device configuration by name.
// Device sections in UCI are anonymous, so this searches all device sections
// for one with a matching 'name' option.
//
// Parameters:
//   - name: The device name (e.g., "br-ahwlan", "vxlan0", "tailscale0")
//
// Returns the device configuration or an error if it cannot be read.
//
// Example:
//
//	device, err := GetDeviceByName("br-ahwlan")
//	if err != nil {
//	    log.Fatalf("Failed to get device config: %v", err)
//	}
//	fmt.Printf("Device type: %s\n", device.Type)
func GetDeviceByName(name string) (*UCIDevice, error) {
	reader := NewUCINetworkConfigReader()

	return GetDeviceByNameWithReader(name, reader)
}

// GetDeviceByNameWithReader loads and returns the UCI device configuration by name using the provided reader.
// It searches every device section for one whose name option matches.
func GetDeviceByNameWithReader(name string, reader ConfigReader) (*UCIDevice, error) {
	section, err := findDeviceByName(reader, name)
	if err != nil {
		return nil, err
	}

	if section == "" {
		return nil, fmt.Errorf("device %s not found", name)
	}

	return loadDeviceFromSection(section, reader)
}

// loadDeviceFromSection loads a device configuration from a specific UCI section.
func loadDeviceFromSection(section string, reader ConfigReader) (*UCIDevice, error) {
	device := &UCIDevice{}

	if values, ok := reader.Get(networkConfigName, section, "name"); ok && len(values) > 0 {
		device.Name = values[0]
	}

	if values, ok := reader.Get(networkConfigName, section, "type"); ok && len(values) > 0 {
		device.Type = values[0]
	}

	if values, ok := reader.Get(networkConfigName, section, "macaddr"); ok && len(values) > 0 {
		device.MacAddr = values[0]
	}

	if values, ok := reader.Get(networkConfigName, section, "ifname"); ok && len(values) > 0 {
		device.Ifname = values[0]
	}

	if values, ok := reader.Get(networkConfigName, section, "ports"); ok && len(values) > 0 {
		device.Ports = values
	}

	if values, ok := reader.Get(networkConfigName, section, "rxpause"); ok && len(values) > 0 {
		device.RxPause = values[0]
	}

	if values, ok := reader.Get(networkConfigName, section, "txpause"); ok && len(values) > 0 {
		device.TxPause = values[0]
	}

	if values, ok := reader.Get(networkConfigName, section, "autoneg"); ok && len(values) > 0 {
		device.AutoNeg = values[0]
	}

	if values, ok := reader.Get(networkConfigName, section, "speed"); ok && len(values) > 0 {
		device.Speed = values[0]
	}

	if values, ok := reader.Get(networkConfigName, section, "duplex"); ok && len(values) > 0 {
		device.Duplex = values[0]
	}

	if values, ok := reader.Get(networkConfigName, section, "table"); ok && len(values) > 0 {
		device.Table = values[0]
	}

	if values, ok := reader.Get(networkConfigName, section, "igmp_snooping"); ok && len(values) > 0 {
		device.IgmpSnooping = values[0]
	}

	if values, ok := reader.Get(networkConfigName, section, "multicast_querier"); ok && len(values) > 0 {
		device.MulticastQuerier = values[0]
	}

	if values, ok := reader.Get(networkConfigName, section, "mtu"); ok && len(values) > 0 {
		device.MTU = values[0]
	}

	return device, nil
}

// SetDeviceConfig creates or updates a device configuration.
// Device sections in UCI are anonymous. If a device with the given name exists,
// it will be updated. Otherwise, a new anonymous device section is created.
//
// Parameters:
//   - name: The device name (e.g., "br-ahwlan", "vxlan0")
//   - device: The device configuration to set
//
// Returns an error if the configuration cannot be saved.
//
// Example:
//
//	device := &UCIDevice{
//	    Name: "br-ahwlan",
//	    Type: "bridge",
//	    Ports: []string{"bat0"},
//	}
//	err := SetDeviceConfig("br-ahwlan", device)
//
// Note: This operation requires appropriate privileges and commits the configuration.
func SetDeviceConfig(name string, device *UCIDevice) error {
	reader := NewUCINetworkConfigReader()

	return SetDeviceConfigWithReader(name, device, reader)
}

// SetDeviceConfigWithReader creates or updates a device configuration using the provided reader.
func SetDeviceConfigWithReader(name string, device *UCIDevice, reader ConfigReader) error { //nolint:gocognit,gocyclo
	// Find existing device section by name
	var section string

	sections, err := reader.GetSections(networkConfigName, networkDeviceType)
	if err != nil {
		return fmt.Errorf("failed to get device sections: %w", err)
	}

	// Search for existing device
	for _, sec := range sections {
		if values, ok := reader.Get(networkConfigName, sec, "name"); ok && len(values) > 0 {
			if values[0] == name {
				section = sec

				break
			}
		}
	}

	// If device doesn't exist, create a new anonymous section
	if section == "" {
		if err := reader.AddSection(networkConfigName, "", networkDeviceType); err != nil {
			return fmt.Errorf("failed to add device section: %w", err)
		}
		// Get the newly created section reference
		sections, err := reader.GetSections(networkConfigName, networkDeviceType)
		if err != nil {
			return fmt.Errorf("failed to get device sections: %w", err)
		}

		if len(sections) > 0 {
			section = sections[len(sections)-1] // Use the last one (newly created)
		}
	}

	if section == "" {
		return fmt.Errorf("failed to determine device section")
	}

	// Set device options
	if device.Name != "" {
		if err := reader.SetType(networkConfigName, section, "name", uci.TypeOption, device.Name); err != nil {
			return fmt.Errorf("failed to set device name: %w", err)
		}
	}

	if device.Type != "" {
		if err := reader.SetType(networkConfigName, section, "type", uci.TypeOption, device.Type); err != nil {
			return fmt.Errorf("failed to set device type: %w", err)
		}
	}

	if device.MacAddr != "" {
		if err := reader.SetType(networkConfigName, section, "macaddr", uci.TypeOption, device.MacAddr); err != nil {
			return fmt.Errorf("failed to set device macaddr: %w", err)
		}
	}

	if device.Ifname != "" {
		if err := reader.SetType(networkConfigName, section, "ifname", uci.TypeOption, device.Ifname); err != nil {
			return fmt.Errorf("failed to set device ifname: %w", err)
		}
	}

	if len(device.Ports) > 0 {
		if err := reader.SetType(networkConfigName, section, "ports", uci.TypeList, device.Ports...); err != nil {
			return fmt.Errorf("failed to set device ports: %w", err)
		}
	}

	if device.RxPause != "" {
		if err := reader.SetType(networkConfigName, section, "rxpause", uci.TypeOption, device.RxPause); err != nil {
			return fmt.Errorf("failed to set device rxpause: %w", err)
		}
	}

	if device.TxPause != "" {
		if err := reader.SetType(networkConfigName, section, "txpause", uci.TypeOption, device.TxPause); err != nil {
			return fmt.Errorf("failed to set device txpause: %w", err)
		}
	}

	if device.AutoNeg != "" {
		if err := reader.SetType(networkConfigName, section, "autoneg", uci.TypeOption, device.AutoNeg); err != nil {
			return fmt.Errorf("failed to set device autoneg: %w", err)
		}
	}

	if device.Speed != "" {
		if err := reader.SetType(networkConfigName, section, "speed", uci.TypeOption, device.Speed); err != nil {
			return fmt.Errorf("failed to set device speed: %w", err)
		}
	}

	if device.Duplex != "" {
		if err := reader.SetType(networkConfigName, section, "duplex", uci.TypeOption, device.Duplex); err != nil {
			return fmt.Errorf("failed to set device duplex: %w", err)
		}
	}

	if device.Table != "" {
		if err := reader.SetType(networkConfigName, section, "table", uci.TypeOption, device.Table); err != nil {
			return fmt.Errorf("failed to set device table: %w", err)
		}
	}

	if device.IgmpSnooping != "" {
		if err := reader.SetType(networkConfigName, section, "igmp_snooping", uci.TypeOption, device.IgmpSnooping); err != nil {
			return fmt.Errorf("failed to set device igmp_snooping: %w", err)
		}
	}

	if device.MulticastQuerier != "" {
		if err := reader.SetType(networkConfigName, section, "multicast_querier", uci.TypeOption, device.MulticastQuerier); err != nil {
			return fmt.Errorf("failed to set device multicast_querier: %w", err)
		}
	}

	if device.MTU != "" {
		if err := reader.SetType(networkConfigName, section, "mtu", uci.TypeOption, device.MTU); err != nil {
			return fmt.Errorf("failed to set device mtu: %w", err)
		}
	}

	if err := reader.Commit(); err != nil {
		return fmt.Errorf("failed to commit device configuration: %w", err)
	}

	return nil
}

// DeleteDeviceConfig removes a device configuration section by device name.
// Device sections in UCI are anonymous, so this searches for the device
// by its 'name' option.
//
// Parameters:
//   - name: The device name to delete (e.g., "br-ahwlan", "vxlan0")
//
// Returns an error if the section cannot be deleted.
//
// Example:
//
//	err := DeleteDeviceConfig("br-guest")
//	if err != nil {
//	    log.Fatalf("Failed to delete device config: %v", err)
//	}
//
// Note: This operation requires appropriate privileges and commits the configuration.
func DeleteDeviceConfig(name string) error {
	reader := NewUCINetworkConfigReader()

	return DeleteDeviceConfigWithReader(name, reader)
}

// DeleteDeviceConfigWithReader removes a device configuration section using the provided reader.
func DeleteDeviceConfigWithReader(name string, reader ConfigReader) error {
	// Find the device section by name
	sections, err := reader.GetSections(networkConfigName, networkDeviceType)
	if err != nil {
		return fmt.Errorf("failed to get device sections: %w", err)
	}

	var sectionToDelete string

	for _, section := range sections {
		if values, ok := reader.Get(networkConfigName, section, "name"); ok && len(values) > 0 {
			if values[0] == name {
				sectionToDelete = section

				break
			}
		}
	}

	if sectionToDelete == "" {
		return fmt.Errorf("device %s not found", name)
	}

	if err := reader.DelSection(networkConfigName, sectionToDelete); err != nil {
		return fmt.Errorf("failed to delete device section: %w", err)
	}

	if err := reader.Commit(); err != nil {
		return fmt.Errorf("failed to commit device deletion: %w", err)
	}

	return nil
}

// DeviceSectionExists checks if a device section exists in the configuration by device name.
// Device sections in UCI are anonymous, so this searches for a device with the given name.
//
// Parameters:
//   - name: The device name to check (e.g., "br-ahwlan", "vxlan0")
//
// Returns true if the device exists, false otherwise.
//
// Example:
//
//	exists := DeviceSectionExists("br-ahwlan")
//	if exists {
//	    fmt.Println("Device section exists")
//	}
func DeviceSectionExists(name string) bool {
	reader := NewUCINetworkConfigReader()

	return DeviceSectionExistsWithReader(name, reader)
}

// DeviceSectionExistsWithReader checks if a device section exists using the provided reader.
func DeviceSectionExistsWithReader(name string, reader ConfigReader) bool {
	// Get all device sections and search for the device by name
	sections, err := reader.GetSections(networkConfigName, networkDeviceType)
	if err != nil {
		return false
	}

	for _, section := range sections {
		if values, ok := reader.Get(networkConfigName, section, "name"); ok && len(values) > 0 {
			if values[0] == name {
				return true
			}
		}
	}

	return false
}

// GetAllDevices retrieves all device configurations from the network config.
// It returns a map of device names to UCIDevice configurations.
func GetAllDevices() (map[string]*UCIDevice, error) {
	reader := NewUCINetworkConfigReader()

	return GetAllDevicesWithReader(reader)
}

// GetAllDevicesWithReader retrieves all device configurations using the provided reader.
// It returns a map of device names to UCIDevice configurations.
func GetAllDevicesWithReader(reader ConfigReader) (map[string]*UCIDevice, error) {
	// Get all sections of type "device" (they are anonymous)
	sections, err := reader.GetSections(networkConfigName, networkDeviceType)
	if err != nil {
		return nil, fmt.Errorf("failed to get device sections: %w", err)
	}

	// Create map to hold devices (keyed by device name, not section)
	devices := make(map[string]*UCIDevice)

	// Load each device
	for _, section := range sections {
		device, err := loadDeviceFromSection(section, reader)
		if err != nil {
			// Log but continue on error for individual device
			continue
		}
		// Use device name as key
		if device.Name != "" {
			devices[device.Name] = device
		}
	}

	return devices, nil
}

// ForceReloadConfig forces a reload of the network configuration by executing the OpenWrt network init script.
func ForceReloadConfig(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "reload_config")

	return cmd.Run()
}

// ReloadNetwork reloads the network configuration by executing the OpenWrt network init script.
// It calls the '/etc/init.d/network reload' command to apply network configuration changes
// without restarting the entire network subsystem.
//
// Returns an error if the reload command fails to execute or returns a non-zero exit code.
func ReloadNetwork(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "/etc/init.d/network", "reload")

	return cmd.Run()
}

// RestartNetwork hard restarts the network service by executing the network init script.
// It runs the '/etc/init.d/network restart' command and returns an error if the
// command execution fails.
//
// Returns:
//   - error: nil if the network restart command succeeds, otherwise returns the error
//     from command execution
func RestartNetwork(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "/etc/init.d/network", "restart")

	return cmd.Run()
}

// ── Setup wizard helpers (mirror LuCI Morse wizard) ──────────────────────────

const (
	// BatmanDeviceName is the canonical batman-adv interface name the
	// wizard always emits. Matches LuCI Morse `defaultBatmanIfaceName`
	// behavior.
	BatmanDeviceName string = "bat0"

	// BatmanPrimaryIface is the primary batadv_hardif interface name.
	BatmanPrimaryIface string = "batmesh0"

	// BatmanSecondaryIface is the secondary batadv_hardif interface
	// name (typically used for 2.4 GHz mesh).
	BatmanSecondaryIface string = "batmesh1"

	networkInterfaceType string = "interface"
	networkDeviceType    string = "device"

	bridgeTypeBridge string = "bridge"

	// wizardDeviceSectionPrefix names the `config device` sections the
	// setup wizard creates for physical ports that only need to carry
	// an mtu. UnsetDeviceMTU removes them on the next reset.
	wizardDeviceSectionPrefix string = "wizard_device_"

	// minDeviceMTU is netifd's floor for `option mtu`; smaller values
	// are ignored and the kernel default stays.
	minDeviceMTU int = 68

	batadvProto       string = "batadv"
	batadvHardifProto string = "batadv_hardif"

	// UCI option keys repeated across the network/dhcp/vxlan/wireless
	// configs. Centralized so goconst is happy and so any rename is a
	// single edit.
	optionProto         string = "proto"
	optionNetmask       string = "netmask"
	optionIP6Assign     string = "ip6assign"
	optionIP6IfaceID    string = "ip6ifaceid"
	optionMulticastMode string = "multicast_mode"
)

// batmanDeviceOptions enumerates every batman-adv option set on a
// `proto batadv` interface by SetupBatmanDeviceOnNetwork, other than
// gw_mode and multicast_mode which are caller-supplied. Mirrors the
// LuCI uci.js setupBatmanDeviceOnNetwork() exactly.
var batmanDeviceOptions = []struct{ k, v string }{ //nolint:gochecknoglobals // package-level constant
	{optionProto, batadvProto},
	{"routing_algo", "BATMAN_V"},
	{"bridge_loop_avoidance", "1"},
	{"hop_penalty", "30"},
	{"bonding", "1"},
	{"aggregated_ogms", "1"},
	{"ap_isolation", "0"},
	{"fragmentation", "1"},
	{"orig_interval", "1000"},
	{"distributed_arp_table", "1"},
	{"network_coding", "1"},
	{"isolation_mark", "0x00000000/0x00000000"},
}

// Values of the batman-adv `multicast_mode` UCI option on a `proto batadv`
// interface. The kernel exports this switch as the negation of forceflood
// (BATADV_ATTR_MULTICAST_FORCEFLOOD_ENABLED = !multicast_mode,
// net/batman-adv/netlink.c): "0" makes every node classic-flood every
// multicast frame; "1" enables the IGMP/MLD-snooping optimisations so a
// group only reaches nodes that announced membership. OpenWrt's batadv
// proto handler passes the value straight to
// `batctl meshif <dev> multicast_mode`.
const (
	MulticastModeForceflood = "0"
	MulticastModeOptimised  = "1"
)

// MulticastModeForForceflood maps the batman.multicastForceflood config
// flag onto the multicast_mode UCI value. It is the single source of truth
// for both writers — the runtime daemon (mgmt.configureBatmanForceflood)
// and the setup wizard (SetupService.runBatmanAdv) — so they cannot
// disagree, and the only place the inversion is spelled out.
func MulticastModeForForceflood(forceflood bool) string {
	if forceflood {
		return MulticastModeForceflood
	}

	return MulticastModeOptimised
}

// MulticastModeWithReader returns the batman-adv multicast_mode option
// currently persisted on the given interface's network section, or "" when
// the option is unset. Unlike GetUCINetworkByNameWithReader it reads a
// single option and cannot fail, so callers doing a change-only reconcile
// have no error to handle. See MulticastModeForForceflood for the value's
// meaning.
func MulticastModeWithReader(reader ConfigReader, iface string) string {
	if values, ok := reader.Get(networkConfigName, iface, optionMulticastMode); ok && len(values) > 0 {
		return values[0]
	}

	return ""
}

// SetupBatmanDeviceOnNetwork creates (or updates) the batman-adv
// device interface on the network config, mirroring LuCI's
// setupBatmanDeviceOnNetwork() exactly. Use empty deviceName to
// default to BatmanDeviceName ("bat0"); empty gwMode defaults to
// "client"; empty multicastMode defaults to MulticastModeForceflood ("0",
// classic flooding — the shipped default and both LuCI fixture
// captures).
//
// Does not commit.
func SetupBatmanDeviceOnNetwork(reader ConfigReader, gwMode, deviceName, multicastMode string) error {
	if deviceName == "" {
		deviceName = BatmanDeviceName
	}

	if gwMode == "" {
		gwMode = "client"
	}

	if multicastMode == "" {
		multicastMode = MulticastModeForceflood
	}

	if !batmanInterfaceExists(reader, deviceName) {
		if err := reader.AddSection(networkConfigName, deviceName, networkInterfaceType); err != nil {
			return fmt.Errorf("creating batman device %s: %w", deviceName, err)
		}
	}

	// gw_mode and multicast_mode are caller-supplied and written
	// before the batmanDeviceOptions loop below, not after. Since that
	// loop runs second, last-write-wins means a hardcoded gw_mode or
	// multicast_mode reintroduced into batmanDeviceOptions would win
	// over the caller-supplied value rather than being silently
	// shadowed by it. That is what makes
	// TestCompat_MulticastModeMatchesForcefloodConfig sensitive to the
	// regression (root cause #3: multicast_mode hardcoded to "1"): the
	// wrong value actually reaches bat0 and the test catches it.
	// Writing these values after the loop instead would mask the same
	// regression, so keep this order.
	if err := reader.SetType(networkConfigName, deviceName, "gw_mode", uci.TypeOption, gwMode); err != nil {
		return fmt.Errorf("setting %s.%s.gw_mode: %w", networkConfigName, deviceName, err)
	}

	if err := reader.SetType(networkConfigName, deviceName, optionMulticastMode, uci.TypeOption, multicastMode); err != nil {
		return fmt.Errorf("setting %s.%s.multicast_mode: %w", networkConfigName, deviceName, err)
	}

	for _, kv := range batmanDeviceOptions {
		if err := reader.SetType(networkConfigName, deviceName, kv.k, uci.TypeOption, kv.v); err != nil {
			return fmt.Errorf("setting %s.%s.%s: %w",
				networkConfigName, deviceName, kv.k, err)
		}
	}

	return nil
}

// batmanInterfaceExists checks whether a network interface section
// with the given name exists. Used to make
// SetupBatmanDeviceOnNetwork idempotent.
func batmanInterfaceExists(reader ConfigReader, name string) bool {
	sections, err := reader.GetSections(networkConfigName, networkInterfaceType)
	if err != nil {
		return false
	}

	for _, s := range sections {
		if s == name {
			return true
		}
	}

	return false
}

// SetupBatmanInterfaceOnDevice creates the primary (batmesh0) and
// secondary (batmesh1) batadv_hardif interfaces bound to the supplied
// batman-adv device. Matches LuCI's setupBatmanInterfaceOnDevice()
// behavior up to and including the secondary interface; the bridge-
// port wiring (adding bat0 to br-ahwlan) is left to
// CreateOrRemoveBridgeAsNeeded so this helper can be exercised without
// a populated bridge fixture.
//
// Does not commit.
func SetupBatmanInterfaceOnDevice(reader ConfigReader, deviceName string) error {
	if deviceName == "" {
		deviceName = BatmanDeviceName
	}

	for _, ifaceName := range []string{BatmanPrimaryIface, BatmanSecondaryIface} {
		if !batmanInterfaceExists(reader, ifaceName) {
			if err := reader.AddSection(networkConfigName, ifaceName, networkInterfaceType); err != nil {
				return fmt.Errorf("creating %s: %w", ifaceName, err)
			}
		}

		if err := reader.SetType(networkConfigName, ifaceName, optionProto, uci.TypeOption, batadvHardifProto); err != nil {
			return fmt.Errorf("setting %s.proto: %w", ifaceName, err)
		}

		if err := reader.SetType(networkConfigName, ifaceName, "master", uci.TypeOption, deviceName); err != nil {
			return fmt.Errorf("setting %s.master: %w", ifaceName, err)
		}
	}

	return nil
}

// BatmanDeviceExists reports whether network.<name> exists as a
// batman-adv device (proto=batadv). Radio handlers use it to refuse a
// mesh binding on a node that never ran setup.
func BatmanDeviceExists(reader ConfigReader, name string) bool {
	if !batmanInterfaceExists(reader, name) {
		return false
	}

	proto, ok := reader.Get(networkConfigName, name, optionProto)

	return ok && len(proto) > 0 && proto[0] == batadvProto
}

// EnsureBatmanHardifInterface creates network.<ifaceName> as a
// batadv_hardif on deviceName when it does not exist and reports
// whether it did so. An existing section is left untouched, whatever
// its options. Does not commit.
func EnsureBatmanHardifInterface(reader ConfigReader, ifaceName, deviceName string) (bool, error) {
	if batmanInterfaceExists(reader, ifaceName) {
		return false, nil
	}

	if err := reader.AddSection(networkConfigName, ifaceName, networkInterfaceType); err != nil {
		return false, fmt.Errorf("creating %s: %w", ifaceName, err)
	}

	if err := reader.SetType(networkConfigName, ifaceName, optionProto, uci.TypeOption, batadvHardifProto); err != nil {
		return false, fmt.Errorf("setting %s.proto: %w", ifaceName, err)
	}

	if err := reader.SetType(networkConfigName, ifaceName, "master", uci.TypeOption, deviceName); err != nil {
		return false, fmt.Errorf("setting %s.master: %w", ifaceName, err)
	}

	return true, nil
}

// RemoveAllBatadvInterfaces deletes every network interface whose
// proto is `batadv` or `batadv_hardif`. Called by the reset phase so
// stale batman state from a previous run cannot conflict with new
// writes.
//
// Does not commit.
func RemoveAllBatadvInterfaces(reader ConfigReader) error {
	sections, err := reader.GetSections(networkConfigName, networkInterfaceType)
	if err != nil {
		return fmt.Errorf("listing network interfaces: %w", err)
	}

	for _, s := range sections {
		proto, _ := reader.Get(networkConfigName, s, optionProto)
		if len(proto) == 0 {
			continue
		}

		if proto[0] != batadvProto && proto[0] != batadvHardifProto {
			continue
		}

		if err := reader.DelSection(networkConfigName, s); err != nil {
			return fmt.Errorf("deleting %s: %w", s, err)
		}
	}

	return nil
}

// RemoveAllBridgeDevices deletes every `device` section whose `type`
// is `bridge`. Removes any leftover bridges (br-prpl, br-ahwlan, etc.)
// before the wizard rebuilds them with fresh MAC addresses and
// port lists. Mirrors the LuCI resetUciNetworkTopology() block.
//
// Does not commit.
func RemoveAllBridgeDevices(reader ConfigReader) error {
	sections, err := reader.GetSections(networkConfigName, networkDeviceType)
	if err != nil {
		return fmt.Errorf("listing network devices: %w", err)
	}

	for _, s := range sections {
		typ, _ := reader.Get(networkConfigName, s, "type")
		if len(typ) == 0 || typ[0] != bridgeTypeBridge {
			continue
		}

		if err := reader.DelSection(networkConfigName, s); err != nil {
			return fmt.Errorf("deleting %s: %w", s, err)
		}
	}

	return nil
}

// UnsetGatewayAndDeviceOnInterfaces clears the `gateway` and `device`
// options on every network interface section, except for the
// loopback. Mirrors LuCI's resetUciNetworkTopology() — without this,
// a leftover device binding from a previous run could persist after
// the wizard re-wires interfaces.
//
// Does not commit.
func UnsetGatewayAndDeviceOnInterfaces(reader ConfigReader) error {
	sections, err := reader.GetSections(networkConfigName, networkInterfaceType)
	if err != nil {
		return fmt.Errorf("listing network interfaces: %w", err)
	}

	for _, s := range sections {
		// Skip the loopback ("device 'lo'").
		device, _ := reader.Get(networkConfigName, s, networkDeviceType)
		if len(device) > 0 && device[0] == "lo" {
			continue
		}

		if err := reader.Del(networkConfigName, s, "gateway"); err != nil {
			return fmt.Errorf("unsetting gateway on %s: %w", s, err)
		}

		if err := reader.Del(networkConfigName, s, networkDeviceType); err != nil {
			return fmt.Errorf("unsetting device on %s: %w", s, err)
		}
	}

	return nil
}

// SetNetworkDevices wires the supplied device list onto the named
// network section, mirroring LuCI's setNetworkDevices(). The behavior
// depends on the section's existing `device` option:
//
//   - If the section's device points at an existing bridge, the
//     bridge's `ports` list is replaced with `devices`.
//   - Otherwise, if exactly one device is supplied, the section's
//     `device` is set to that single device.
//   - Otherwise (multiple devices, no bridge), the caller should run
//     CreateOrRemoveBridgeAsNeeded first; this helper falls back to
//     just setting the first device when no bridge is in play.
//
// Does not commit.
func SetNetworkDevices(reader ConfigReader, sectionID string, devices []string) error {
	if sectionID == "" {
		return fmt.Errorf("sectionID cannot be empty")
	}

	currentDeviceVals, _ := reader.Get(networkConfigName, sectionID, networkDeviceType)
	currentDevice := ""

	if len(currentDeviceVals) > 0 {
		currentDevice = currentDeviceVals[0]
	}

	if currentDevice != "" {
		bridgeSection, err := findBridgeBySectionName(reader, currentDevice)
		if err != nil {
			return err
		}

		if bridgeSection != "" {
			return reader.SetType(networkConfigName, bridgeSection, "ports", uci.TypeList, devices...)
		}
	}

	switch len(devices) {
	case 0:
		// Nothing to do.
		return nil
	case 1:
		return reader.SetType(networkConfigName, sectionID, networkDeviceType, uci.TypeOption, devices[0])
	default:
		// Without a bridge, the caller should have invoked
		// CreateOrRemoveBridgeAsNeeded; fall back to the first device
		// so callers that haven't built bridge support yet still
		// produce a defined network.
		return reader.SetType(networkConfigName, sectionID, networkDeviceType, uci.TypeOption, devices[0])
	}
}

// findBridgeBySectionName returns the section name of a `device`
// section whose `name` field equals the supplied bridge name, or "" if
// no such bridge exists.
func findBridgeBySectionName(reader ConfigReader, bridgeName string) (string, error) {
	sections, err := reader.GetSections(networkConfigName, networkDeviceType)
	if err != nil {
		return "", fmt.Errorf("listing network devices: %w", err)
	}

	for _, s := range sections {
		typ, _ := reader.Get(networkConfigName, s, "type")
		if len(typ) == 0 || typ[0] != bridgeTypeBridge {
			continue
		}

		name, _ := reader.Get(networkConfigName, s, "name")
		if len(name) > 0 && name[0] == bridgeName {
			return s, nil
		}
	}

	return "", nil
}

// FindBridgeBySectionName is the exported wrapper around the package-
// internal bridge lookup. The setup wizard handler uses it to discover
// the section name of the br-ahwlan bridge after it has been created
// in the base-network phase, so the batman-adv phase can append bat0
// to its ports list.
func FindBridgeBySectionName(reader ConfigReader, bridgeName string) (string, error) {
	return findBridgeBySectionName(reader, bridgeName)
}

// SetupAhwlanInterface writes the standard set of options the LuCI
// Morse wizard's setupNetworkWithDnsmasq() helper sets on the ahwlan
// network section: proto=static, netmask=255.255.0.0, ip6assign=64,
// ip6ifaceid=eui64, list ip6class 'local', device=br-ahwlan. When
// ipaddr is non-empty, the function also sets it (mesh-point scenarios
// pass a randomized address; mesh-gate scenarios omit it because
// openmanetd assigns the gateway address at runtime via ip route).
//
// The section is created if it doesn't already exist. Does not commit.
func SetupAhwlanInterface(reader ConfigReader, ipaddr string) error {
	const (
		section    = "ahwlan"
		bridgeName = "br-ahwlan"
	)

	if !batmanInterfaceExists(reader, section) {
		if err := reader.AddSection(networkConfigName, section, networkInterfaceType); err != nil {
			return fmt.Errorf("creating ahwlan interface: %w", err)
		}
	}

	writes := []struct {
		k, v string
	}{
		{optionProto, DefaultNetworkProto},
		{optionNetmask, DefaultNetworkMask},
		{optionIP6Assign, DefaultIPv6Assign},
		{optionIP6IfaceID, DefaultIPv6IfaceID},
		{networkDeviceType, bridgeName},
	}

	for _, w := range writes {
		if err := reader.SetType(networkConfigName, section, w.k, uci.TypeOption, w.v); err != nil {
			return fmt.Errorf("setting %s.%s.%s: %w", networkConfigName, section, w.k, err)
		}
	}

	// ip6class is a list option. Setting just `local` matches the LuCI
	// fixture; preserving any pre-existing values would be unsafe given
	// the reset phase already ran.
	if err := reader.SetType(networkConfigName, section, "ip6class", uci.TypeList, DefaultIPv6Class); err != nil {
		return fmt.Errorf("setting %s.%s.ip6class: %w", networkConfigName, section, err)
	}

	if ipaddr != "" {
		if err := reader.SetType(networkConfigName, section, "ipaddr", uci.TypeOption, ipaddr); err != nil {
			return fmt.Errorf("setting %s.%s.ipaddr: %w", networkConfigName, section, err)
		}

		// Mesh-point ahwlan also gets a DNS server. LuCI's
		// setupNetworkWithDnsmasq writes `1.1.1.1` here in the
		// isMeshPoint branch — without it the mesh point can't
		// resolve hostnames over its mesh address since nothing else
		// populates this option. Mesh-gate scenarios skip this
		// because the gateway's upstream provides DNS.
		if err := reader.SetType(networkConfigName, section, "dns", uci.TypeOption, "1.1.1.1"); err != nil {
			return fmt.Errorf("setting %s.%s.dns: %w", networkConfigName, section, err)
		}
	}

	return nil
}

// CreateBridgeDevice writes a `config device` section of type=bridge
// with the supplied name, port list, and macaddr. Mirrors LuCI's
// setBridgeWithPorts() in morseuci.js. Idempotent — if a bridge with
// the same name already exists, its options are overwritten with the
// supplied values. The section name returned is the UCI section
// identifier the caller can pass to AppendBridgePort.
//
// `name` is the bridge's UCI `name` field (e.g. "br-ahwlan"); the
// section name itself is anonymous (`@device[N]` in UCI export form)
// because LuCI emits anonymous device sections in its captures.
//
// Does not commit.
func CreateBridgeDevice(reader ConfigReader, name string, ports []string, macaddr string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("bridge name cannot be empty")
	}

	// Reuse an existing bridge section if one already has this name —
	// this keeps the helper idempotent across re-runs of the wizard
	// (or partial runs followed by reset).
	existing, err := findBridgeBySectionName(reader, name)
	if err != nil {
		return "", err
	}

	section := existing
	if section == "" {
		// Stable section name derived from the bridge name. Using a
		// named section (rather than anonymous) lets later phases
		// reliably look the bridge up by section ID without scanning
		// for type=bridge by `name` field.
		section = "wizard_bridge_" + sanitizeUCIName(name)

		if err := reader.AddSection(networkConfigName, section, networkDeviceType); err != nil {
			return "", fmt.Errorf("creating bridge device section: %w", err)
		}
	}

	if err := reader.SetType(networkConfigName, section, "name", uci.TypeOption, name); err != nil {
		return "", fmt.Errorf("setting bridge name: %w", err)
	}

	if err := reader.SetType(networkConfigName, section, "type", uci.TypeOption, bridgeTypeBridge); err != nil {
		return "", fmt.Errorf("setting bridge type: %w", err)
	}

	if macaddr != "" {
		if err := reader.SetType(networkConfigName, section, "macaddr", uci.TypeOption, macaddr); err != nil {
			return "", fmt.Errorf("setting bridge macaddr: %w", err)
		}
	}

	if len(ports) > 0 {
		if err := reader.SetType(networkConfigName, section, "ports", uci.TypeList, ports...); err != nil {
			return "", fmt.Errorf("setting bridge ports: %w", err)
		}
	}

	return section, nil
}

// findDeviceByName returns the section name of the `config device`
// whose `name` option equals name, regardless of its type, or "" when
// none exists. Vendor board configs ship such sections for physical
// ports (`config device` / `option name 'eth0'` / `option macaddr`),
// and netifd keeps one settings block per device name — a second
// section for the same name would drop the vendor's options.
func findDeviceByName(reader ConfigReader, name string) (string, error) {
	sections, err := reader.GetSections(networkConfigName, networkDeviceType)
	if err != nil {
		return "", fmt.Errorf("listing network devices: %w", err)
	}

	for _, s := range sections {
		got, _ := reader.Get(networkConfigName, s, "name")
		if len(got) > 0 && got[0] == name {
			return s, nil
		}
	}

	return "", nil
}

// SetDeviceMTUWithReader stages `option mtu` on the `config device`
// section whose `name` is name, creating the named section
// wizard_device_<name> (with `option name`) when none exists. netifd
// applies the option to the device on the next reload, so the value
// outlives the daemon's netlink pass. Does not commit — the setup
// wizard commits every tainted config in one go at its commit phase.
// Returns the section written. Values below netifd's minimum (68) are
// rejected so a typo can never stage an mtu netifd would ignore.
func SetDeviceMTUWithReader(reader ConfigReader, name string, mtu int) (string, error) {
	if name == "" {
		return "", fmt.Errorf("device name cannot be empty")
	}

	if mtu < minDeviceMTU {
		return "", fmt.Errorf("mtu %d is below netifd's minimum of %d", mtu, minDeviceMTU)
	}

	section, err := findDeviceByName(reader, name)
	if err != nil {
		return "", err
	}

	if section == "" {
		section = wizardDeviceSectionPrefix + sanitizeUCIName(name)

		if addErr := reader.AddSection(networkConfigName, section, networkDeviceType); addErr != nil {
			return "", fmt.Errorf("creating device section %s: %w", section, addErr)
		}

		if nameErr := reader.SetType(networkConfigName, section, "name", uci.TypeOption, name); nameErr != nil {
			return "", fmt.Errorf("setting name on %s: %w", section, nameErr)
		}
	}

	if setErr := reader.SetType(networkConfigName, section, "mtu", uci.TypeOption, strconv.Itoa(mtu)); setErr != nil {
		return "", fmt.Errorf("setting mtu on %s: %w", section, setErr)
	}

	return section, nil
}

// UnsetDeviceMTU removes `option mtu` from the `config device`
// sections the wizard's transport-mtu pass manages — those whose
// `name` is in deviceNames (br-ahwlan and the detected ethernet ports)
// — and deletes the wizard-owned port sections (wizard_device_*) that
// exist only to carry one. The setup wizard runs it in its reset phase
// so a re-run whose uplink port changed leaves no stale mtu behind;
// the base-network phase then stages the current values. Does not
// commit.
//
// Scoping to deviceNames is deliberate: an earlier build stripped mtu
// from every device section, which wiped an mtu a vendor board config
// or an operator had set on an unrelated device (br-lan, a wan bridge,
// a non-ethernet port the wizard never touches). Only the wizard's own
// devices are cleared now. A wizard_device_* section is always
// wizard-owned, so the delete pass removes every one regardless of
// name.
//
// Two passes: go-uci renders anonymous sections as @device[N] with N
// counted over every device section, named ones included, so deleting
// a wizard_device_* section shifts the refs of the anonymous sections
// after it. The strip pass therefore runs first over a list that stays
// valid, and the delete pass re-lists and touches named sections only.
func UnsetDeviceMTU(reader ConfigReader, deviceNames []string) error {
	managed := make(map[string]struct{}, len(deviceNames))
	for _, n := range deviceNames {
		managed[n] = struct{}{}
	}

	sections, err := reader.GetSections(networkConfigName, networkDeviceType)
	if err != nil {
		return fmt.Errorf("listing network devices: %w", err)
	}

	for _, s := range sections {
		if strings.HasPrefix(s, wizardDeviceSectionPrefix) {
			continue
		}

		name, _ := reader.Get(networkConfigName, s, "name")
		if len(name) == 0 {
			continue
		}

		if _, ok := managed[name[0]]; !ok {
			continue
		}

		if delErr := reader.Del(networkConfigName, s, "mtu"); delErr != nil {
			return fmt.Errorf("unsetting mtu on %s: %w", s, delErr)
		}
	}

	sections, err = reader.GetSections(networkConfigName, networkDeviceType)
	if err != nil {
		return fmt.Errorf("listing network devices: %w", err)
	}

	for _, s := range sections {
		if !strings.HasPrefix(s, wizardDeviceSectionPrefix) {
			continue
		}

		if delErr := reader.DelSection(networkConfigName, s); delErr != nil {
			return fmt.Errorf("deleting %s: %w", s, delErr)
		}
	}

	return nil
}

// AppendBridgePort adds `port` to the bridge section's `ports` list,
// preserving any existing ports. Idempotent: if the port is already
// present, no write happens. The setup wizard's batman-adv phase uses
// this to attach bat0 to br-ahwlan's port list AFTER the base network
// phase has constructed the bridge with its physical-port list, so
// the resulting ports order is `[<eth-ports...>, bat0]` matching the
// captured fixtures.
//
// Does not commit.
func AppendBridgePort(reader ConfigReader, bridgeSection, port string) error {
	if bridgeSection == "" {
		return fmt.Errorf("bridge section cannot be empty")
	}

	if port == "" {
		return fmt.Errorf("port cannot be empty")
	}

	existing, _ := reader.Get(networkConfigName, bridgeSection, "ports")
	if slices.Contains(existing, port) {
		return nil
	}

	updated := append(append([]string(nil), existing...), port)

	return reader.SetType(networkConfigName, bridgeSection, "ports", uci.TypeList, updated...)
}

// sanitizeUCIName replaces every character in s that isn't legal in a
// UCI section identifier with an underscore. Section names must match
// `[A-Za-z0-9_]+`.
func sanitizeUCIName(s string) string {
	out := make([]byte, 0, len(s))

	for i := 0; i < len(s); i++ {
		c := s[i]

		switch {
		case c >= 'a' && c <= 'z',
			c >= 'A' && c <= 'Z',
			c >= '0' && c <= '9',
			c == '_':
			out = append(out, c)
		default:
			out = append(out, '_')
		}
	}

	return string(out)
}

// SetInterfaceProtoWithReader updates the `proto` option on a network
// interface section. Used by the wizard's base-network phase to flip
// `lan.proto=dhcp` on gate-with-ethernet-uplink scenarios where the
// upstream provides DHCP. Convenience wrapper that does not validate
// the proto string against a known set.
func SetInterfaceProtoWithReader(reader ConfigReader, section, proto string) error {
	return reader.SetType(networkConfigName, section, optionProto, uci.TypeOption, proto)
}

// SetInterfaceDNSWithReader updates the `dns` option on a network
// interface section. Used by the wizard to set `lan.dns=1.1.1.1`
// unconditionally (mirroring LuCI's setupBatmanInterfaceOnDevice).
func SetInterfaceDNSWithReader(reader ConfigReader, section, dns string) error {
	return reader.SetType(networkConfigName, section, "dns", uci.TypeOption, dns)
}

// EnsureWan6Interface creates the `wan6` interface section with
// `proto=dhcpv6` if it does not already exist. The LuCI captures show
// this is added on gate-with-ethernet scenarios so the device picks
// up an IPv6 address from upstream.
//
// Does not commit.
func EnsureWan6Interface(reader ConfigReader) error {
	const section = "wan6"

	if !batmanInterfaceExists(reader, section) {
		if err := reader.AddSection(networkConfigName, section, networkInterfaceType); err != nil {
			return fmt.Errorf("creating wan6: %w", err)
		}
	}

	return reader.SetType(networkConfigName, section, optionProto, uci.TypeOption, "dhcpv6")
}

// EnsureWanInterface creates the `wan` interface section with
// `proto=dhcp` if it does not already exist, mirroring
// EnsureWan6Interface. The wizard uses this on gate-with-ethernet
// scenarios where the uplink port is bound to wan rather than lan.
//
// Does not commit.
func EnsureWanInterface(reader ConfigReader) error {
	const section = "wan"

	if !batmanInterfaceExists(reader, section) {
		if err := reader.AddSection(networkConfigName, section, networkInterfaceType); err != nil {
			return fmt.Errorf("creating wan: %w", err)
		}
	}

	return reader.SetType(networkConfigName, section, optionProto, uci.TypeOption, "dhcp")
}

// SetInterfaceDeviceWithReader binds a network interface section to a
// physical device (network.<section>.device). The wizard uses this to
// re-attach the uplink port after the reset phase strips every
// interface's device option. Does not commit.
func SetInterfaceDeviceWithReader(reader ConfigReader, section, device string) error {
	if section == "" || device == "" {
		return fmt.Errorf("section and device are required")
	}

	if err := reader.SetType(networkConfigName, section, networkDeviceType, uci.TypeOption, device); err != nil {
		return fmt.Errorf("setting %s.%s.device: %w", networkConfigName, section, err)
	}

	return nil
}
