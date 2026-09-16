package network

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net"
	"os/exec"
	"sort"
	"strconv"

	"github.com/digineo/go-uci/v2"
	"github.com/openmanet/openmanetd/internal/database/models"
)

const (
	dhcpConfigName string = "dhcp"

	DefaultDHCPAddressLimit int    = 16
	DefaultDHCPLeaseTime    string = "12h"

	// UCI option keys repeated across dnsmasq and dhcp pool sections.
	// Centralized so goconst is happy and so any rename is a single edit.
	optionAuthoritative    string = "authoritative"
	optionCacheSize        string = "cachesize"
	optionEdnsPacketMax    string = "ednspacket_max"
	optionBogusPriv        string = "boguspriv"
	optionIgnore           string = "ignore"
	optionDNS              string = "dns"
	optionDomainNeeded     string = "domainneeded"
	optionDomain           string = "domain"
	optionRebindLocalhost  string = "rebind_localhost"
	optionLeaseTime        string = "leasetime"
	optionRebindProtection string = "rebind_protection"
	optionDNSService       string = "dns_service"
	optionLocalizeQueries  string = "localize_queries"
	optionExpandHosts      string = "expandhosts"
	optionLimit            string = "limit"
	optionForce            string = "force"
	optionReadEthers       string = "readethers"
	optionLocal            string = "local"
	optionStart            string = "start"
	optionRAFlags          string = "ra_flags"
	optionLocalService     string = "localservice"
	optionRASlaac          string = "ra_slaac"
	optionDHCPOption       string = "dhcp_option"
	optionInstance         string = "instance"
)

// UCIDnsmasq represents the dnsmasq global configuration section.
type UCIDnsmasq struct {
	DomainNeeded    string `uci:"option domainneeded"`
	LocaliseQueries string `uci:"option localize_queries"`
	RebindLocalhost string `uci:"option rebind_localhost"`
	Local           string `uci:"option local"`
	Domain          string `uci:"option domain"`
	ExpandHosts     string `uci:"option expandhosts"`
	CacheSize       string `uci:"option cachesize"`
	Authoritative   string `uci:"option authoritative"`
	ReadEthers      string `uci:"option readethers"`
	LocalService    string `uci:"option localservice"`
	EdnsPacketMax   string `uci:"option ednspacket_max"`
}

// UCIDHCP represents a DHCP pool configuration.
type UCIDHCP struct {
	Interface  string `uci:"option interface"`
	Start      string `uci:"option start"`
	Limit      string `uci:"option limit"`
	LeaseTime  string `uci:"option leasetime"`
	Ignore     string `uci:"option ignore"`
	DHCPOption string `uci:"list dhcp_option"`
	Ra         string `uci:"option ra"`
	RaDefault  string `uci:"option ra_default"`
	Force      string `uci:"option force"`
}

// DHCPConfigReader defines an interface for reading DHCP UCI configuration values.
type DHCPConfigReader interface {
	Get(config, section, option string) ([]string, bool)
	GetSections(config, secType string) ([]string, error)
	SetType(config, section, option string, typ uci.OptionType, values ...string) error
	Del(config, section, option string) error
	AddSection(config, section, typ string) error
	DelSection(config, section string) error
	Commit() error
	ReloadConfig() error
}

// UCIDHCPConfigReader wraps the UCI functions for DHCP configuration.
type UCIDHCPConfigReader struct {
	tree uci.Tree
}

// NewUCIDHCPConfigReader creates a new UCI DHCP config reader with the default tree.
func NewUCIDHCPConfigReader() *UCIDHCPConfigReader {
	return &UCIDHCPConfigReader{
		tree: uci.NewTree(uci.DefaultTreePath),
	}
}

func (r *UCIDHCPConfigReader) Get(config, section, option string) ([]string, bool) {
	return r.tree.Get(config, section, option)
}

func (r *UCIDHCPConfigReader) GetSections(config, secType string) ([]string, error) {
	return r.tree.GetSections(config, secType)
}

func (r *UCIDHCPConfigReader) SetType(config, section, option string, typ uci.OptionType, values ...string) error {
	return r.tree.SetType(config, section, option, typ, values...)
}

func (r *UCIDHCPConfigReader) Del(config, section, option string) error {
	return r.tree.Del(config, section, option)
}

func (r *UCIDHCPConfigReader) AddSection(config, section, typ string) error {
	return r.tree.AddSection(config, section, typ)
}

func (r *UCIDHCPConfigReader) DelSection(config, section string) error {
	return r.tree.DelSection(config, section)
}

// Commit commits the current configuration changes to UCI.
func (r *UCIDHCPConfigReader) Commit() error {
	return r.tree.Commit()
}

func (r *UCIDHCPConfigReader) ReloadConfig() error {
	return r.tree.LoadConfig(dhcpConfigName, true)
}

// GetDnsmasqConfig loads and returns the dnsmasq global configuration.
func GetDnsmasqConfig() (*UCIDnsmasq, error) {
	return GetDnsmasqConfigWithReader(NewUCIDHCPConfigReader())
}

// GetDnsmasqConfigWithReader loads and returns the dnsmasq configuration using the provided reader.
func GetDnsmasqConfigWithReader(reader DHCPConfigReader) (*UCIDnsmasq, error) {
	var config UCIDnsmasq

	if values, ok := reader.Get(dhcpConfigName, "dnsmasq", optionDomainNeeded); ok && len(values) > 0 {
		config.DomainNeeded = values[0]
	}

	if values, ok := reader.Get(dhcpConfigName, "dnsmasq", optionLocalizeQueries); ok && len(values) > 0 {
		config.LocaliseQueries = values[0]
	}

	if values, ok := reader.Get(dhcpConfigName, "dnsmasq", optionRebindLocalhost); ok && len(values) > 0 {
		config.RebindLocalhost = values[0]
	}

	if values, ok := reader.Get(dhcpConfigName, "dnsmasq", "local"); ok && len(values) > 0 {
		config.Local = values[0]
	}

	if values, ok := reader.Get(dhcpConfigName, "dnsmasq", optionDomain); ok && len(values) > 0 {
		config.Domain = values[0]
	}

	if values, ok := reader.Get(dhcpConfigName, "dnsmasq", optionExpandHosts); ok && len(values) > 0 {
		config.ExpandHosts = values[0]
	}

	if values, ok := reader.Get(dhcpConfigName, "dnsmasq", "cachesize"); ok && len(values) > 0 {
		config.CacheSize = values[0]
	}

	if values, ok := reader.Get(dhcpConfigName, "dnsmasq", "authoritative"); ok && len(values) > 0 {
		config.Authoritative = values[0]
	}

	if values, ok := reader.Get(dhcpConfigName, "dnsmasq", "readethers"); ok && len(values) > 0 {
		config.ReadEthers = values[0]
	}

	if values, ok := reader.Get(dhcpConfigName, "dnsmasq", "localservice"); ok && len(values) > 0 {
		config.LocalService = values[0]
	}

	if values, ok := reader.Get(dhcpConfigName, "dnsmasq", "ednspacket_max"); ok && len(values) > 0 {
		config.EdnsPacketMax = values[0]
	}

	return &config, nil
}

// GetDHCPConfig loads and returns the DHCP pool configuration by section name.
func GetDHCPConfig(section string) (*UCIDHCP, error) {
	return GetDHCPConfigWithReader(section, NewUCIDHCPConfigReader())
}

// GetDHCPConfigWithReader loads and returns the DHCP pool configuration using the provided reader.
func GetDHCPConfigWithReader(section string, reader DHCPConfigReader) (*UCIDHCP, error) {
	var config UCIDHCP

	if values, ok := reader.Get(dhcpConfigName, section, "interface"); ok && len(values) > 0 {
		config.Interface = values[0]
	}

	if values, ok := reader.Get(dhcpConfigName, section, "start"); ok && len(values) > 0 {
		config.Start = values[0]
	}

	if values, ok := reader.Get(dhcpConfigName, section, optionLimit); ok && len(values) > 0 {
		config.Limit = values[0]
	}

	if values, ok := reader.Get(dhcpConfigName, section, optionLeaseTime); ok && len(values) > 0 {
		config.LeaseTime = values[0]
	}

	if values, ok := reader.Get(dhcpConfigName, section, "ignore"); ok && len(values) > 0 {
		config.Ignore = values[0]
	}

	if values, ok := reader.Get(dhcpConfigName, section, optionDHCPOption); ok && len(values) > 0 {
		config.DHCPOption = values[0]
	}

	if values, ok := reader.Get(dhcpConfigName, section, "ra"); ok && len(values) > 0 {
		config.Ra = values[0]
	}

	if values, ok := reader.Get(dhcpConfigName, section, "ra_default"); ok && len(values) > 0 {
		config.RaDefault = values[0]
	}

	if values, ok := reader.Get(dhcpConfigName, section, optionForce); ok && len(values) > 0 {
		config.Force = values[0]
	}

	return &config, nil
}

// SetDHCPConfig creates or updates a DHCP pool configuration.
//
// Parameters:
//   - section: The UCI section name (e.g., "lan", "wan", "ahwlan")
//   - config: The DHCP configuration to set
//
// Returns an error if the configuration cannot be saved.
//
// Example:
//
//	dhcpConfig := &UCIDHCP{
//	    Interface: "lan",
//	    Start:     "100",
//	    Limit:     "150",
//	    LeaseTime: "12h",
//	}
//	err := SetDHCPConfig("lan", dhcpConfig)
//
// Note: This operation requires appropriate privileges and commits the configuration.
func SetDHCPConfig(section string, config *UCIDHCP) error {
	return SetDHCPConfigWithReader(section, config, NewUCIDHCPConfigReader())
}

// SetDHCPConfigWithReader creates or updates a DHCP pool configuration using the provided reader.
func SetDHCPConfigWithReader(section string, config *UCIDHCP, reader DHCPConfigReader) error {
	if config == nil {
		return fmt.Errorf("config cannot be nil")
	}

	// Add section if it doesn't exist (this will fail silently if it exists)
	_ = reader.AddSection(dhcpConfigName, section, "dhcp")

	if config.Interface != "" {
		if err := reader.SetType(dhcpConfigName, section, "interface", uci.TypeOption, config.Interface); err != nil {
			return fmt.Errorf("failed to set interface: %w", err)
		}
	}

	if config.Start != "" {
		if err := reader.SetType(dhcpConfigName, section, "start", uci.TypeOption, config.Start); err != nil {
			return fmt.Errorf("failed to set start: %w", err)
		}
	}

	if config.Limit != "" {
		if err := reader.SetType(dhcpConfigName, section, optionLimit, uci.TypeOption, config.Limit); err != nil {
			return fmt.Errorf("failed to set limit: %w", err)
		}
	}

	if config.LeaseTime != "" {
		if err := reader.SetType(dhcpConfigName, section, optionLeaseTime, uci.TypeOption, config.LeaseTime); err != nil {
			return fmt.Errorf("failed to set leasetime: %w", err)
		}
	}

	if config.Ignore != "" {
		if err := reader.SetType(dhcpConfigName, section, "ignore", uci.TypeOption, config.Ignore); err != nil {
			return fmt.Errorf("failed to set ignore: %w", err)
		}
	}

	if config.DHCPOption != "" {
		if err := reader.SetType(dhcpConfigName, section, optionDHCPOption, uci.TypeOption, config.DHCPOption); err != nil {
			return fmt.Errorf("failed to set dhcp_option: %w", err)
		}
	}

	if config.Ra != "" {
		if err := reader.SetType(dhcpConfigName, section, "ra", uci.TypeOption, config.Ra); err != nil {
			return fmt.Errorf("failed to set ra: %w", err)
		}
	}

	if config.RaDefault != "" {
		if err := reader.SetType(dhcpConfigName, section, "ra_default", uci.TypeOption, config.RaDefault); err != nil {
			return fmt.Errorf("failed to set ra_default: %w", err)
		}
	}

	if config.Force != "" {
		if err := reader.SetType(dhcpConfigName, section, optionForce, uci.TypeOption, config.Force); err != nil {
			return fmt.Errorf("failed to set force: %w", err)
		}
	}

	if err := reader.Commit(); err != nil {
		return fmt.Errorf("failed to commit DHCP config: %w", err)
	}

	return nil
}

// DeleteDHCPConfig removes a DHCP pool configuration section.
//
// Parameters:
//   - section: The UCI section name to delete (e.g., "lan", "wan")
//
// Returns an error if the section cannot be deleted.
//
// Example:
//
//	err := DeleteDHCPConfig("guest")
//	if err != nil {
//	    log.Fatalf("Failed to delete DHCP config: %v", err)
//	}
//
// Note: This operation requires appropriate privileges and commits the configuration.
func DeleteDHCPConfig(section string) error {
	return DeleteDHCPConfigWithReader(section, NewUCIDHCPConfigReader())
}

// DeleteDHCPConfigWithReader removes a DHCP pool configuration section using the provided reader.
func DeleteDHCPConfigWithReader(section string, reader DHCPConfigReader) error {
	if err := reader.DelSection(dhcpConfigName, section); err != nil {
		return fmt.Errorf("failed to delete DHCP section: %w", err)
	}

	if err := reader.Commit(); err != nil {
		return fmt.Errorf("failed to commit DHCP config: %w", err)
	}

	return nil
}

// DHCPSectionExists checks if a DHCP section exists in the configuration.
//
// Parameters:
//   - section: The UCI section name to check (e.g., "lan", "wan", "ahwlan")
//
// Returns true if the section exists, false otherwise.
//
// Example:
//
//	exists := DHCPSectionExists("lan")
//	if exists {
//	    fmt.Println("DHCP section exists")
//	}
func DHCPSectionExists(section string) bool {
	return DHCPSectionExistsWithReader(section, NewUCIDHCPConfigReader())
}

// DHCPSectionExistsWithReader checks if a DHCP section exists using the provided reader.
func DHCPSectionExistsWithReader(section string, reader DHCPConfigReader) bool {
	// Try to get any option from the section to verify it exists
	// We check for 'interface' as it's a common option in DHCP sections
	_, exists := reader.Get(dhcpConfigName, section, "interface")

	return exists
}

// EnableDHCP enables DHCP on the specified interface section.
//
// Parameters:
//   - section: The UCI section name (e.g., "lan", "wan")
//
// This sets the 'ignore' option to '0', enabling DHCP service.
func EnableDHCP(section string) error {
	return EnableDHCPWithReader(section, NewUCIDHCPConfigReader())
}

// EnableDHCPWithReader enables DHCP using the provided reader.
func EnableDHCPWithReader(section string, reader DHCPConfigReader) error {
	if err := reader.SetType(dhcpConfigName, section, "ignore", uci.TypeOption, "0"); err != nil {
		return fmt.Errorf("failed to enable DHCP: %w", err)
	}

	if err := reader.Commit(); err != nil {
		return fmt.Errorf("failed to commit DHCP config: %w", err)
	}

	return nil
}

// DisableDHCP disables DHCP on the specified interface section.
//
// Parameters:
//   - section: The UCI section name (e.g., "lan", "wan")
//
// This sets the 'ignore' option to '1', disabling DHCP service.
func DisableDHCP(section string) error {
	return DisableDHCPWithReader(section, NewUCIDHCPConfigReader())
}

// DisableDHCPWithReader disables DHCP using the provided reader.
func DisableDHCPWithReader(section string, reader DHCPConfigReader) error {
	if err := reader.SetType(dhcpConfigName, section, "ignore", uci.TypeOption, "1"); err != nil {
		return fmt.Errorf("failed to disable DHCP: %w", err)
	}

	if err := reader.Commit(); err != nil {
		return fmt.Errorf("failed to commit DHCP config: %w", err)
	}

	return nil
}

// IsDHCPEnabled checks if DHCP is enabled for the specified section.
//
// Parameters:
//   - section: The UCI section name (e.g., "lan", "wan")
//
// Returns:
//   - true if DHCP is enabled (ignore != '1'), false otherwise
//   - An error if the configuration cannot be read
func IsDHCPEnabled(section string) (bool, error) {
	return IsDHCPEnabledWithReader(section, NewUCIDHCPConfigReader())
}

// IsDHCPEnabledWithReader checks if DHCP is enabled using the provided reader.
func IsDHCPEnabledWithReader(section string, reader DHCPConfigReader) (bool, error) {
	config, err := GetDHCPConfigWithReader(section, reader)
	if err != nil {
		return false, err
	}

	// DHCP is enabled if 'ignore' is not set or is set to '0'
	return config.Ignore != "1", nil
}

// SetDHCPRange sets the DHCP address range for the specified section.
//
// Parameters:
//   - section: The UCI section name (e.g., "lan")
//   - start: The starting address offset (e.g., "100")
//   - limit: The maximum number of addresses to assign (e.g., "150")
//
// Example:
//
//	err := SetDHCPRange("lan", "100", "150")
//	// This will assign addresses from .100 to .249 (100 + 150 - 1)
func SetDHCPRange(section, start, limit string) error {
	return SetDHCPRangeWithReader(section, start, limit, NewUCIDHCPConfigReader())
}

// SetDHCPRangeWithReader sets the DHCP range using the provided reader.
func SetDHCPRangeWithReader(section, start, limit string, reader DHCPConfigReader) error {
	// Validate that start and limit are numeric
	if _, err := strconv.Atoi(start); err != nil {
		return fmt.Errorf("start must be a number: %w", err)
	}

	if _, err := strconv.Atoi(limit); err != nil {
		return fmt.Errorf("limit must be a number: %w", err)
	}

	if err := reader.SetType(dhcpConfigName, section, "start", uci.TypeOption, start); err != nil {
		return fmt.Errorf("failed to set start: %w", err)
	}

	if err := reader.SetType(dhcpConfigName, section, optionLimit, uci.TypeOption, limit); err != nil {
		return fmt.Errorf("failed to set limit: %w", err)
	}

	if err := reader.Commit(); err != nil {
		return fmt.Errorf("failed to commit DHCP config: %w", err)
	}

	return nil
}

// SetDHCPLeaseTime sets the lease time for DHCP addresses.
//
// Parameters:
//   - section: The UCI section name (e.g., "lan")
//   - leasetime: The lease time (e.g., "12h", "3600", "infinite")
//
// Example:
//
//	err := SetDHCPLeaseTime("lan", "12h")
func SetDHCPLeaseTime(section, leasetime string) error {
	return SetDHCPLeaseTimeWithReader(section, leasetime, NewUCIDHCPConfigReader())
}

// SetDHCPLeaseTimeWithReader sets the lease time using the provided reader.
func SetDHCPLeaseTimeWithReader(section, leasetime string, reader DHCPConfigReader) error {
	if err := reader.SetType(dhcpConfigName, section, optionLeaseTime, uci.TypeOption, leasetime); err != nil {
		return fmt.Errorf("failed to set leasetime: %w", err)
	}

	if err := reader.Commit(); err != nil {
		return fmt.Errorf("failed to commit DHCP config: %w", err)
	}

	return nil
}

// DHCPRange represents an allocated DHCP address range.
type DHCPRange struct {
	Start int // Starting offset
	End   int // Ending offset (Start + Limit - 1)
}

// CalculateAvailableDHCPStart analyzes address reservation records and calculates
// a non-conflicting DHCP start address within the network range.
//
// Parameters:
//   - nodes: Array of MeshNode containing address reservations
//   - networkAddr: Network address (e.g., "10.41.0.0")
//   - subnetMask: Subnet mask (e.g., "255.255.0.0")
//   - desiredLimit: The desired DHCP limit (number of addresses)
//
// Returns:
//   - The calculated DHCP start offset
//   - An error if no suitable range can be found
//
// Example:
//
//	nodes := []models.MeshNode{ /* ... */ }
//	start, err := CalculateAvailableDHCPStart(nodes, "10.41.0.0", "255.255.0.0", 150)
//	if err != nil {
//	    log.Fatalf("Failed to calculate DHCP start: %v", err)
//	}
//	fmt.Printf("Use DHCP start: %d\n", start)
//
// Note: This function accounts for existing DHCP ranges to prevent conflicts.
// It attempts to find the lowest available start address that can accommodate
// the desired limit without overlapping with existing ranges.
func CalculateAvailableDHCPStart(nodes []models.MeshNode, networkAddr, subnetMask string, desiredLimit int) (int, error) { //nolint:gocognit
	if desiredLimit <= 0 {
		return 0, fmt.Errorf("desiredLimit must be greater than 0")
	}

	// Parse network address and subnet mask
	ip := net.ParseIP(networkAddr)
	if ip == nil {
		return 0, fmt.Errorf("invalid network address: %s", networkAddr)
	}

	ip = ip.To4()
	if ip == nil {
		return 0, fmt.Errorf("network address must be IPv4: %s", networkAddr)
	}

	mask := net.ParseIP(subnetMask)
	if mask == nil {
		return 0, fmt.Errorf("invalid subnet mask: %s", subnetMask)
	}

	mask = mask.To4()
	if mask == nil {
		return 0, fmt.Errorf("subnet mask must be IPv4: %s", subnetMask)
	}

	// Calculate network size (number of available host addresses)
	// This calculates the total number of addresses in the subnet
	ones, bits := net.IPMask(mask).Size()
	if bits != 32 {
		return 0, fmt.Errorf("invalid subnet mask")
	}

	networkSize := (1 << uint(bits-ones)) - 2 // Subtract network and broadcast addresses

	if networkSize <= 0 {
		return 0, fmt.Errorf("network size too small")
	}

	// Collect existing DHCP ranges from records
	var existingRanges []DHCPRange

	for _, node := range nodes {
		// Ensure we have valid DHCP start and limit
		if !node.UciDhcpStart.Valid || !node.UciDhcpLimit.Valid {
			continue
		}

		// Parse start and limit
		start := int(node.UciDhcpStart.Int64)
		limit := int(node.UciDhcpLimit.Int64)

		if start > 0 && limit > 0 {
			existingRanges = append(existingRanges, DHCPRange{
				Start: start,
				End:   start + limit - 1,
			})
		}
	}

	// Sort ranges by start address for easier conflict detection
	sort.Slice(existingRanges, func(i, j int) bool {
		return existingRanges[i].Start < existingRanges[j].Start
	})

	// Find the first available gap that can fit our desired range
	// Strategy: Prefer offset 100 if available, otherwise find the first gap starting from 100 going forward,
	// and only if nothing found after 100, search before 100

	// Helper function to check if a candidate range is available
	checkCandidate := func(start int) bool {
		if start < 1 || start+desiredLimit-1 > networkSize {
			return false
		}

		proposedEnd := start + desiredLimit - 1
		for _, existing := range existingRanges {
			if rangesOverlap(start, proposedEnd, existing.Start, existing.End) {
				return false
			}
		}

		return true
	}

	// First, try offset 100 (preferred default)
	if checkCandidate(100) {
		return 100, nil
	}

	// If 100 doesn't work, scan from 100 forward to find the first available gap
	candidate := 100
	for candidate+desiredLimit-1 <= networkSize {
		if checkCandidate(candidate) {
			return candidate, nil
		}

		// Move past any conflicting range
		moved := false

		proposedEnd := candidate + desiredLimit - 1
		for _, existing := range existingRanges {
			if rangesOverlap(candidate, proposedEnd, existing.Start, existing.End) {
				candidate = existing.End + 1
				moved = true

				break
			}
		}

		if !moved {
			candidate++
		}
	}

	// If no space found after 100, search from offset 1 to 99
	candidate = 1
	for candidate < 100 && candidate+desiredLimit-1 <= networkSize {
		if checkCandidate(candidate) {
			return candidate, nil
		}

		// Move past any conflicting range
		moved := false

		proposedEnd := candidate + desiredLimit - 1
		for _, existing := range existingRanges {
			if rangesOverlap(candidate, proposedEnd, existing.Start, existing.End) {
				candidate = existing.End + 1
				moved = true

				break
			}
		}

		if !moved {
			candidate++
		}
	}

	return 0, fmt.Errorf("no available DHCP range found for limit %d within network size %d", desiredLimit, networkSize)
}

// rangesOverlap checks if two ranges overlap.
func rangesOverlap(start1, end1, start2, end2 int) bool {
	return start1 <= end2 && start2 <= end1
}

// DHCPLease represents a single DHCP lease entry.
type DHCPLease struct {
	Hostname string `json:"hostname"`
	MacAddr  string `json:"macaddr"`
	DUID     string `json:"duid"`
	IPAddr   string `json:"ipaddr"`
	Expires  int    `json:"expires"`
}

// GetExpires returns the expiration time in seconds.
func (l *DHCPLease) GetExpires() int {
	return l.Expires
}

// GetHostname returns the hostname of the lease.
func (l *DHCPLease) GetHostname() string {
	return l.Hostname
}

// GetMacAddr returns the MAC address of the lease.
func (l *DHCPLease) GetMacAddr() string {
	return l.MacAddr
}

// GetDUID returns the DHCP Unique Identifier.
func (l *DHCPLease) GetDUID() string {
	return l.DUID
}

// GetIPAddr returns the IP address of the lease.
func (l *DHCPLease) GetIPAddr() string {
	return l.IPAddr
}

// DHCPLeasesResponse represents the response from ubus getDHCPLeases call.
type DHCPLeasesResponse struct {
	DHCPLeases  []DHCPLease `json:"dhcp_leases"`
	DHCP6Leases []DHCPLease `json:"dhcp6_leases"`
}

// GetDHCPLeases returns all DHCP leases (IPv4).
func (r *DHCPLeasesResponse) GetDHCPLeases() []DHCPLease {
	return r.DHCPLeases
}

// GetDHCP6Leases returns all DHCPv6 leases.
func (r *DHCPLeasesResponse) GetDHCP6Leases() []DHCPLease {
	return r.DHCP6Leases
}

// GetAllLeases returns all leases (both IPv4 and IPv6).
func (r *DHCPLeasesResponse) GetAllLeases() []DHCPLease {
	all := make([]DHCPLease, 0, len(r.DHCPLeases)+len(r.DHCP6Leases))
	all = append(all, r.DHCPLeases...)
	all = append(all, r.DHCP6Leases...)

	return all
}

// UbusCommandExecutor defines an interface for executing ubus commands.
type UbusCommandExecutor interface {
	Execute(ctx context.Context, args ...string) ([]byte, error)
}

// DefaultUbusExecutor executes real ubus commands.
type DefaultUbusExecutor struct{}

// Execute runs the ubus command with the given arguments.
func (e *DefaultUbusExecutor) Execute(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "ubus", args...)

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ubus %v: %w", args, err)
	}

	return out, nil
}

// GetCurrentDHCPLeases retrieves all current DHCP leases from OpenWRT using ubus.
//
// Returns:
//   - A DHCPLeasesResponse containing all active DHCP leases
//   - An error if the command fails or JSON parsing fails
//
// Example:
//
//	leases, err := GetCurrentDHCPLeases()
//	if err != nil {
//	    log.Fatalf("Failed to get DHCP leases: %v", err)
//	}
//	for _, lease := range leases.GetDHCPLeases() {
//	    fmt.Printf("Host: %s, MAC: %s, IP: %s\n",
//	        lease.GetHostname(), lease.GetMacAddr(), lease.GetIPAddr())
//	}
func GetCurrentDHCPLeases() (*DHCPLeasesResponse, error) {
	return GetCurrentDHCPLeasesWithExecutor(context.Background(), &DefaultUbusExecutor{})
}

// GetCurrentDHCPLeasesWithExecutor retrieves DHCP leases using a custom executor.
// This function is primarily used for testing with mocked ubus commands.
func GetCurrentDHCPLeasesWithExecutor(ctx context.Context, executor UbusCommandExecutor) (*DHCPLeasesResponse, error) {
	// Execute ubus command
	output, err := executor.Execute(ctx, "call", "luci-rpc", "getDHCPLeases")
	if err != nil {
		return nil, fmt.Errorf("failed to execute ubus command: %w", err)
	}

	// Parse JSON response
	var response DHCPLeasesResponse
	if err := json.Unmarshal(output, &response); err != nil {
		return nil, fmt.Errorf("failed to parse DHCP leases JSON: %w", err)
	}

	return &response, nil
}

// ── Static Host Leases ──────────────────────────────────────────────────────

// UCIStaticHost represents a static DHCP reservation ("host" section in UCI).
type UCIStaticHost struct {
	Name string // hostname
	MAC  string // MAC address
	IP   string // reserved IP
}

// GetStaticHosts returns all static DHCP host reservations using the default reader.
func GetStaticHosts() ([]UCIStaticHost, error) {
	return GetStaticHostsWithReader(NewUCIDHCPConfigReader())
}

// GetStaticHostsWithReader returns all static DHCP host reservations from UCI.
// It enumerates all "host" type sections in the dhcp config.
func GetStaticHostsWithReader(reader DHCPConfigReader) ([]UCIStaticHost, error) {
	sections, err := reader.GetSections(dhcpConfigName, "host")
	if err != nil {
		return nil, fmt.Errorf("failed to enumerate host sections: %w", err)
	}

	hosts := make([]UCIStaticHost, 0, len(sections))

	for _, section := range sections {
		var host UCIStaticHost

		if values, ok := reader.Get(dhcpConfigName, section, "name"); ok && len(values) > 0 {
			host.Name = values[0]
		}

		if values, ok := reader.Get(dhcpConfigName, section, "mac"); ok && len(values) > 0 {
			host.MAC = values[0]
		}

		if values, ok := reader.Get(dhcpConfigName, section, "ip"); ok && len(values) > 0 {
			host.IP = values[0]
		}

		hosts = append(hosts, host)
	}

	return hosts, nil
}

// ── DHCP Range End Calculation ──────────────────────────────────────────────

// ComputeDHCPRangeEnd calculates the last IP in a DHCP pool range.
// Given a base interface IP, a start offset, and a limit, it returns the end IP.
// For example: base 10.41.0.0, start 100, limit 155 → end 10.41.0.254.
func ComputeDHCPRangeEnd(baseIP string, start, limit int) (string, error) {
	ip := net.ParseIP(baseIP).To4()
	if ip == nil {
		return "", fmt.Errorf("invalid base IP: %s", baseIP)
	}

	endOffset := start + limit - 1

	// Convert IP to uint32, add offset, convert back
	ipVal := uint32(ip[0])<<24 | uint32(ip[1])<<16 | uint32(ip[2])<<8 | uint32(ip[3])
	ipVal += uint32(endOffset)

	result := net.IPv4(byte(ipVal>>24), byte(ipVal>>16), byte(ipVal>>8), byte(ipVal))

	return result.String(), nil
}

// ComputeDHCPRangeStart calculates the first IP in a DHCP pool range.
func ComputeDHCPRangeStart(baseIP string, start int) (string, error) {
	ip := net.ParseIP(baseIP).To4()
	if ip == nil {
		return "", fmt.Errorf("invalid base IP: %s", baseIP)
	}

	ipVal := uint32(ip[0])<<24 | uint32(ip[1])<<16 | uint32(ip[2])<<8 | uint32(ip[3])
	ipVal += uint32(start)

	result := net.IPv4(byte(ipVal>>24), byte(ipVal>>16), byte(ipVal>>8), byte(ipVal))

	return result.String(), nil
}

// ── Setup wizard helpers (mirror LuCI Morse wizard) ──────────────────────────

const (
	dhcpSectionType string = "dhcp"

	// DefaultDhcpPoolLimit is the per-pool address count the wizard
	// uses (a /28-sized window, matching LuCI's createDhcp).
	DefaultDhcpPoolLimit string = "16"

	// DefaultDhcpPoolLeasetime is the lease duration the wizard sets
	// on every pool. Matches the LuCI capture fixtures.
	DefaultDhcpPoolLeasetime string = "12h"
)

// WizardDnsmasqWhitelist is the field whitelist the wizard applies to
// the surviving dnsmasq section during reset. Mirrors the resetUci()
// list in tools/morse/wizard.js.
var WizardDnsmasqWhitelist = []string{ //nolint:gochecknoglobals // package-level constant
	optionAuthoritative, optionDomainNeeded, optionLocalizeQueries, optionReadEthers,
	optionLocal, optionDomain, optionExpandHosts, optionLocalService, optionCacheSize,
	optionEdnsPacketMax, optionRebindLocalhost,
}

// WizardDhcpPoolWhitelist is the field whitelist the wizard applies
// to every dhcp pool section during the reset phase. Mirrors the
// resetUciNetworkTopology() list.
var WizardDhcpPoolWhitelist = []string{ //nolint:gochecknoglobals // package-level constant
	optionStart, optionLeaseTime, optionLimit, networkInterfaceType,
}

// allDnsmasqOptions enumerates every dnsmasq option the wizard or
// existing OpenWrt config may have set.
var allDnsmasqOptions = []string{ //nolint:gochecknoglobals // package-level constant
	optionAuthoritative, optionDomainNeeded, optionLocalizeQueries, optionReadEthers,
	optionLocal, optionDomain, optionExpandHosts, optionLocalService, optionCacheSize,
	optionEdnsPacketMax, optionRebindLocalhost, networkInterfaceType, "notinterface",
	"localuse", optionRebindProtection, optionBogusPriv,
}

// allDhcpPoolOptions enumerates every dhcp pool option the wizard or
// existing OpenWrt config may have set.
var allDhcpPoolOptions = []string{ //nolint:gochecknoglobals // package-level constant
	networkInterfaceType, optionStart, optionLimit, optionLeaseTime, optionIgnore,
	optionForce, "ra", optionRASlaac, optionRAFlags, optionDNS, optionDNSService,
	optionDHCPOption, optionInstance,
}

// WhitelistDnsmasqSurvivor removes every option NOT in allowList from
// the named dnsmasq section. The wizard's reset phase calls this on
// the single surviving dnsmasq instance after deleting interface-
// scoped ones, so the global dnsmasq config matches OpenWrt's defaults
// before per-network setupDnsmasq() writes are applied.
//
// Does not commit.
func WhitelistDnsmasqSurvivor(reader ConfigReader, dnsmasqName string, allowList []string) error {
	if dnsmasqName == "" {
		return fmt.Errorf("dnsmasqName cannot be empty")
	}

	for _, option := range allDnsmasqOptions {
		if containsString(allowList, option) {
			continue
		}

		if _, exists := reader.Get(dhcpConfigName, dnsmasqName, option); !exists {
			continue
		}

		if err := reader.Del(dhcpConfigName, dnsmasqName, option); err != nil {
			return fmt.Errorf("deleting %s.%s.%s: %w",
				dhcpConfigName, dnsmasqName, option, err)
		}
	}

	return nil
}

// WhitelistAndIgnoreAllPools sets `ignore='1'` on every existing dhcp
// pool section and removes every option NOT in WizardDhcpPoolWhitelist.
// Mirrors the per-pool reset block in resetUciNetworkTopology(). The
// wizard re-creates fresh pools immediately after via CreateDhcpPool,
// so leftover pool sections become inert placeholders that the user
// can still see in UCI but that don't affect routing.
//
// Does not commit.
func WhitelistAndIgnoreAllPools(reader ConfigReader) error {
	sections, err := reader.GetSections(dhcpConfigName, dhcpSectionType)
	if err != nil {
		return fmt.Errorf("listing dhcp pools: %w", err)
	}

	for _, s := range sections {
		for _, option := range allDhcpPoolOptions {
			if containsString(WizardDhcpPoolWhitelist, option) {
				continue
			}

			if _, exists := reader.Get(dhcpConfigName, s, option); !exists {
				continue
			}

			if err := reader.Del(dhcpConfigName, s, option); err != nil {
				return fmt.Errorf("deleting %s.%s.%s: %w",
					dhcpConfigName, s, option, err)
			}
		}

		if err := reader.SetType(dhcpConfigName, s, "ignore", uci.TypeOption, "1"); err != nil {
			return fmt.Errorf("setting %s.%s.ignore: %w", dhcpConfigName, s, err)
		}
	}

	return nil
}

// CreateDhcpPool creates a new dhcp pool section linked to networkID,
// populating the option set the LuCI Morse wizard's `createDhcp()`
// helper writes plus any scenario-specific extraOptions. The `start`
// offset is drawn from the supplied seeded RNG via RandomDhcpStart so
// tests are reproducible.
//
// Deliberately does NOT create or link a per-network dnsmasq
// instance: OpenWrt launches one dnsmasq process per `config
// dnsmasq` section, so a second instance races the global one for
// port 53 — the loser exits and takes its bound pool down with it.
// Both LuCI captures show exactly one dnsmasq and an instance-less
// pool.
//
// Returns the section name of the newly-created pool. Does not commit.
func CreateDhcpPool(reader ConfigReader, networkID string, extraOptions map[string][]string, rng *rand.Rand) (string, error) {
	if networkID == "" {
		return "", fmt.Errorf("networkID cannot be empty")
	}

	if rng == nil {
		return "", fmt.Errorf("rng cannot be nil")
	}

	// Resolve a unique section name. LuCI uses networkID, then
	// networkID + 1, 2, ... if it clashes with an existing dhcp
	// section name.
	proposedName := networkID

	for i := 0; ; i++ {
		_, exists := reader.Get(dhcpConfigName, proposedName, "interface")
		if !exists {
			// Also check if the section exists at all (it might have no options yet).
			sections, err := reader.GetSections(dhcpConfigName, dhcpSectionType)
			if err != nil {
				return "", fmt.Errorf("listing dhcp pools: %w", err)
			}

			if !containsString(sections, proposedName) {
				break
			}
		}

		proposedName = fmt.Sprintf("%s%d", networkID, i+1)
	}

	if err := reader.AddSection(dhcpConfigName, proposedName, dhcpSectionType); err != nil {
		return "", fmt.Errorf("adding dhcp section: %w", err)
	}

	if err := backfillPoolOptions(reader, proposedName, networkID, extraOptions, rng); err != nil {
		return "", err
	}

	return proposedName, nil
}

// backfillPoolOptions writes the wizard's standard pool option set on
// section. start/limit/leasetime are written only when absent — so a
// re-run of GetOrCreateDhcpPool doesn't reshuffle addresses on a pool
// that already carries them — while interface and force are always
// (re)written, since the reset phase's pool whitelist strips force
// (it isn't in WizardDhcpPoolWhitelist) on every run. extraOptions
// entries are always (re)written too, list-typed when they carry more
// than one value (mirrors the existing dhcp_option write style) —
// the reset phase's option universe also strips dhcp_option, so a
// re-run must restore it rather than assume it survived.
//
// Does not commit.
func backfillPoolOptions(reader ConfigReader, section, networkID string, extraOptions map[string][]string, rng *rand.Rand) error {
	if _, exists := reader.Get(dhcpConfigName, section, optionStart); !exists {
		if rng == nil {
			return fmt.Errorf("rng cannot be nil")
		}

		if err := reader.SetType(dhcpConfigName, section, optionStart, uci.TypeOption,
			strconv.Itoa(RandomDhcpStart(rng))); err != nil {
			return fmt.Errorf("setting %s.%s.start: %w", dhcpConfigName, section, err)
		}
	}

	if _, exists := reader.Get(dhcpConfigName, section, optionLimit); !exists {
		if err := reader.SetType(dhcpConfigName, section, optionLimit, uci.TypeOption, DefaultDhcpPoolLimit); err != nil {
			return fmt.Errorf("setting %s.%s.limit: %w", dhcpConfigName, section, err)
		}
	}

	if _, exists := reader.Get(dhcpConfigName, section, optionLeaseTime); !exists {
		if err := reader.SetType(dhcpConfigName, section, optionLeaseTime, uci.TypeOption, DefaultDhcpPoolLeasetime); err != nil {
			return fmt.Errorf("setting %s.%s.leasetime: %w", dhcpConfigName, section, err)
		}
	}

	if err := reader.SetType(dhcpConfigName, section, networkInterfaceType, uci.TypeOption, networkID); err != nil {
		return fmt.Errorf("setting %s.%s.%s: %w", dhcpConfigName, section, networkInterfaceType, err)
	}

	if err := reader.SetType(dhcpConfigName, section, optionForce, uci.TypeOption, "1"); err != nil {
		return fmt.Errorf("setting %s.%s.force: %w", dhcpConfigName, section, err)
	}

	for opt, values := range extraOptions {
		typ := uci.TypeOption
		if len(values) > 1 {
			typ = uci.TypeList
		}

		if err := reader.SetType(dhcpConfigName, section, opt, typ, values...); err != nil {
			return fmt.Errorf("setting %s.%s.%s: %w", dhcpConfigName, section, opt, err)
		}
	}

	return nil
}

// GetOrCreateDhcpPool finds an enabled dhcp pool that targets
// networkID, or re-enables a disabled matching pool, or creates a
// fresh one. Returns the section name of the resulting pool. Does
// not commit.
//
// The enabled-match path also runs backfillPoolOptions: interface/
// force/extraOptions are always rewritten (idempotent — same values
// unless the caller's extraOptions changed), and start/limit/
// leasetime are left untouched since backfillPoolOptions only writes
// them when absent. This isn't a no-op precondition-dependent path —
// even if a future caller reaches an "enabled" pool whose options
// were never stripped by a reset phase, the rewrite still converges
// to the correct wizard shape.
func GetOrCreateDhcpPool(reader ConfigReader, networkID string, extraOptions map[string][]string, rng *rand.Rand) (string, error) {
	if networkID == "" {
		return "", fmt.Errorf("networkID cannot be empty")
	}

	sections, err := reader.GetSections(dhcpConfigName, dhcpSectionType)
	if err != nil {
		return "", fmt.Errorf("listing dhcp pools: %w", err)
	}

	for _, s := range sections {
		iface, _ := reader.Get(dhcpConfigName, s, "interface")
		if len(iface) == 0 || iface[0] != networkID {
			continue
		}

		ignore, _ := reader.Get(dhcpConfigName, s, "ignore")
		if len(ignore) == 0 || ignore[0] != "1" {
			if err := backfillPoolOptions(reader, s, networkID, extraOptions, rng); err != nil {
				return "", err
			}

			return s, nil
		}
	}

	// No enabled pool — re-enable a disabled matching pool: clear
	// ignore AND stale instance (a prior firmware's wizard may have
	// left one), then backfill the wizard's option set. start/limit/
	// leasetime are kept when present (points randomize start; a
	// re-run must not reshuffle addresses), written fresh when the
	// reset whitelist stripped them.
	for _, s := range sections {
		iface, _ := reader.Get(dhcpConfigName, s, "interface")
		if len(iface) == 0 || iface[0] != networkID {
			continue
		}

		for _, opt := range []string{optionIgnore, optionInstance} {
			if err := reader.Del(dhcpConfigName, s, opt); err != nil {
				return "", fmt.Errorf("clearing %s on %s: %w", opt, s, err)
			}
		}

		if err := backfillPoolOptions(reader, s, networkID, extraOptions, rng); err != nil {
			return "", err
		}

		return s, nil
	}

	return CreateDhcpPool(reader, networkID, extraOptions, rng)
}

// containsString returns true iff slice contains v. Defined locally
// to avoid pulling in slices.Contains from a package other files in
// internal/network may not be importing yet.
func containsString(haystack []string, v string) bool {
	for _, s := range haystack {
		if s == v {
			return true
		}
	}

	return false
}
