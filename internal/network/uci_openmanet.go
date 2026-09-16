package network

import (
	"fmt"
	"strconv"

	"github.com/digineo/go-uci/v2"
)

/*
config openmanet 'config'
	option dhcpconfigured '0'
	option BLOSconfigured '0'
	option batmesh1configured '0'
	option config '/etc/openmanet/config.yml'
*/

const (
	openmanetdConfigName string = "openmanetd"
)

// UCIOpenMANET represents the OpenMANET UCI configuration.
type UCIOpenMANET struct {
	DHCPConfigured     string `uci:"option dhcpconfigured"`
	BLOSConfigured     string `uci:"option BLOSconfigured"`
	BatMesh1Configured string `uci:"option batmesh1configured"`
	Config             string `uci:"option config"`
}

// OpenMANETConfigReader defines an interface for reading OpenMANET UCI configuration values.
type OpenMANETConfigReader interface {
	Get(config, section, option string) ([]string, bool)
	GetSections(config, secType string) ([]string, error)
	SetType(config, section, option string, typ uci.OptionType, values ...string) error
	Del(config, section, option string) error
	AddSection(config, section, typ string) error
	DelSection(config, section string) error
	Commit() error
	ReloadConfig() error
}

// UCIOpenMANETConfigReader wraps the UCI functions for OpenMANET configuration.
type UCIOpenMANETConfigReader struct {
	tree uci.Tree
}

// NewUCIOpenMANETConfigReader creates a new UCI OpenMANET config reader with the default tree.
func NewUCIOpenMANETConfigReader() *UCIOpenMANETConfigReader {
	return &UCIOpenMANETConfigReader{
		tree: uci.NewTree(uci.DefaultTreePath),
	}
}

func (r *UCIOpenMANETConfigReader) Get(config, section, option string) ([]string, bool) {
	return r.tree.Get(config, section, option)
}

func (r *UCIOpenMANETConfigReader) GetSections(config, secType string) ([]string, error) {
	return r.tree.GetSections(config, secType)
}

func (r *UCIOpenMANETConfigReader) SetType(config, section, option string, typ uci.OptionType, values ...string) error {
	return r.tree.SetType(config, section, option, typ, values...)
}

func (r *UCIOpenMANETConfigReader) Del(config, section, option string) error {
	return r.tree.Del(config, section, option)
}

func (r *UCIOpenMANETConfigReader) AddSection(config, section, typ string) error {
	return r.tree.AddSection(config, section, typ)
}

func (r *UCIOpenMANETConfigReader) DelSection(config, section string) error {
	return r.tree.DelSection(config, section)
}

func (r *UCIOpenMANETConfigReader) Commit() error {
	return r.tree.Commit()
}

func (r *UCIOpenMANETConfigReader) ReloadConfig() error {
	return r.tree.LoadConfig(openmanetdConfigName, true)
}

// GetOpenMANETConfig loads and returns the OpenMANET configuration.
//
// Returns the OpenMANET configuration or an error if it cannot be read.
//
// Example:
//
//	config, err := GetOpenMANETConfig()
//	if err != nil {
//	    log.Fatalf("Failed to get OpenMANET config: %v", err)
//	}
//	fmt.Printf("Config path: %s\n", config.Config)
func GetOpenMANETConfig() (*UCIOpenMANET, error) {
	return GetOpenMANETConfigWithReader(NewUCIOpenMANETConfigReader())
}

// GetOpenMANETConfigWithReader loads and returns the OpenMANET configuration using the provided reader.
func GetOpenMANETConfigWithReader(reader OpenMANETConfigReader) (*UCIOpenMANET, error) {
	var config UCIOpenMANET

	if values, ok := reader.Get(openmanetdConfigName, "config", "dhcpconfigured"); ok && len(values) > 0 {
		config.DHCPConfigured = values[0]
	}

	if values, ok := reader.Get(openmanetdConfigName, "config", "BLOSconfigured"); ok && len(values) > 0 {
		config.BLOSConfigured = values[0]
	}

	if values, ok := reader.Get(openmanetdConfigName, "config", "batmesh1configured"); ok && len(values) > 0 {
		config.BatMesh1Configured = values[0]
	}

	if values, ok := reader.Get(openmanetdConfigName, "config", "config"); ok && len(values) > 0 {
		config.Config = values[0]
	}

	return &config, nil
}

// SetOpenMANETConfig creates or updates the OpenMANET configuration.
//
// Parameters:
//   - config: The OpenMANET configuration to set
//
// Returns an error if the configuration cannot be saved.
//
// Example:
//
//	config := &UCIOpenMANET{
//	    DHCPConfigured: "1",
//	    Config:         "/etc/openmanet/config.yml",
//	}
//	err := SetOpenMANETConfig(config)
//
// Note: This operation requires appropriate privileges and commits the configuration.
func SetOpenMANETConfig(config *UCIOpenMANET) error {
	return SetOpenMANETConfigWithReader(config, NewUCIOpenMANETConfigReader())
}

// SetOpenMANETConfigWithReader creates or updates the OpenMANET configuration using the provided reader.
func SetOpenMANETConfigWithReader(config *UCIOpenMANET, reader OpenMANETConfigReader) error {
	if config == nil {
		return fmt.Errorf("config cannot be nil")
	}

	// Add section if it doesn't exist (this will fail silently if it exists)
	_ = reader.AddSection(openmanetdConfigName, "config", "openmanet")

	if config.DHCPConfigured != "" {
		if err := reader.SetType(openmanetdConfigName, "config", "dhcpconfigured", uci.TypeOption, config.DHCPConfigured); err != nil {
			return fmt.Errorf("failed to set dhcpconfigured: %w", err)
		}
	}

	if config.BLOSConfigured != "" {
		if err := reader.SetType(openmanetdConfigName, "config", "BLOSconfigured", uci.TypeOption, config.BLOSConfigured); err != nil {
			return fmt.Errorf("failed to set BLOSconfigured: %w", err)
		}
	}

	if config.BatMesh1Configured != "" {
		if err := reader.SetType(openmanetdConfigName, "config", "batmesh1configured", uci.TypeOption, config.BatMesh1Configured); err != nil {
			return fmt.Errorf("failed to set batmesh1configured: %w", err)
		}
	}

	if config.Config != "" {
		if err := reader.SetType(openmanetdConfigName, "config", "config", uci.TypeOption, config.Config); err != nil {
			return fmt.Errorf("failed to set config: %w", err)
		}
	}

	if err := reader.Commit(); err != nil {
		return fmt.Errorf("failed to commit OpenMANET config: %w", err)
	}

	return nil
}

// IsDHCPConfigured checks if DHCP has been configured.
//
// Returns:
//   - true if DHCP is configured (dhcpconfigured == '1'), false otherwise
//   - An error if the configuration cannot be read
//
// Example:
//
//	configured, err := IsDHCPConfigured()
//	if err != nil {
//	    log.Fatalf("Failed to check DHCP status: %v", err)
//	}
//	if !configured {
//	    // Run DHCP configuration
//	}
func IsDHCPConfigured() (bool, error) {
	return IsDHCPConfiguredWithReader(NewUCIOpenMANETConfigReader())
}

// IsDHCPConfiguredWithReader checks if DHCP has been configured using the provided reader.
func IsDHCPConfiguredWithReader(reader OpenMANETConfigReader) (bool, error) {
	config, err := GetOpenMANETConfigWithReader(reader)
	if err != nil {
		return false, err
	}

	// Parse the dhcpconfigured value
	if config.DHCPConfigured == "0" || config.DHCPConfigured == "" {
		return false, nil
	}

	configured, err := strconv.Atoi(config.DHCPConfigured)
	if err != nil {
		return false, fmt.Errorf("invalid dhcpconfigured value: %w", err)
	}

	return configured == 1, nil
}

// SetDHCPConfigured marks DHCP as configured.
//
// This sets the 'dhcpconfigured' option to '1'.
//
// Example:
//
//	err := SetDHCPConfigured()
//	if err != nil {
//	    log.Fatalf("Failed to mark DHCP as configured: %v", err)
//	}
func SetDHCPConfigured() error {
	return SetDHCPConfiguredWithReader(NewUCIOpenMANETConfigReader())
}

// SetDHCPConfiguredWithReader marks DHCP as configured using the provided reader.
func SetDHCPConfiguredWithReader(reader OpenMANETConfigReader) error {
	// Ensure the section exists
	_ = reader.AddSection(openmanetdConfigName, "config", "openmanet")

	if err := reader.SetType(openmanetdConfigName, "config", "dhcpconfigured", uci.TypeOption, "1"); err != nil {
		return fmt.Errorf("failed to set dhcpconfigured: %w", err)
	}

	if err := reader.Commit(); err != nil {
		return fmt.Errorf("failed to commit OpenMANET config: %w", err)
	}

	return nil
}

// ClearDHCPConfigured marks DHCP as not configured.
//
// This sets the 'dhcpconfigured' option to '0'.
//
// Example:
//
//	err := ClearDHCPConfigured()
//	if err != nil {
//	    log.Fatalf("Failed to clear DHCP configured flag: %v", err)
//	}
func ClearDHCPConfigured() error {
	return ClearDHCPConfiguredWithReader(NewUCIOpenMANETConfigReader())
}

// ClearDHCPConfiguredWithReader marks DHCP as not configured using the provided reader.
func ClearDHCPConfiguredWithReader(reader OpenMANETConfigReader) error {
	// Ensure the section exists
	_ = reader.AddSection(openmanetdConfigName, "config", "openmanet")

	if err := reader.SetType(openmanetdConfigName, "config", "dhcpconfigured", uci.TypeOption, "0"); err != nil {
		return fmt.Errorf("failed to clear dhcpconfigured: %w", err)
	}

	if err := reader.Commit(); err != nil {
		return fmt.Errorf("failed to commit OpenMANET config: %w", err)
	}

	return nil
}

// IsBLOSConfigured checks if BLOS has been configured.
//
// Returns:
//   - true if BLOS is configured (BLOSconfigured == '1'), false otherwise
//   - An error if the configuration cannot be read
//
// Example:
//
//	configured, err := IsBLOSConfigured()
//	if err != nil {
//	    log.Fatalf("Failed to check BLOS status: %v", err)
//	}
//	if !configured {
//	    // Run BLOS configuration
//	}
func IsBLOSConfigured() (bool, error) {
	return IsBLOSConfiguredWithReader(NewUCIOpenMANETConfigReader())
}

// IsBLOSConfiguredWithReader checks if BLOS has been configured using the provided reader.
func IsBLOSConfiguredWithReader(reader OpenMANETConfigReader) (bool, error) {
	config, err := GetOpenMANETConfigWithReader(reader)
	if err != nil {
		return false, err
	}

	// Parse the BLOSconfigured value
	if config.BLOSConfigured == "0" || config.BLOSConfigured == "" {
		return false, nil
	}

	configured, err := strconv.Atoi(config.BLOSConfigured)
	if err != nil {
		return false, fmt.Errorf("invalid BLOSconfigured value: %w", err)
	}

	return configured == 1, nil
}

// SetBLOSConfigured marks BLOS as configured.
//
// This sets the 'BLOSconfigured' option to '1'.
//
// Example:
//
//	err := SetBLOSConfigured()
//	if err != nil {
//	    log.Fatalf("Failed to mark BLOS as configured: %v", err)
//	}
func SetBLOSConfigured() error {
	return SetBLOSConfiguredWithReader(NewUCIOpenMANETConfigReader())
}

// SetBLOSConfiguredWithReader marks BLOS as configured using the provided reader.
func SetBLOSConfiguredWithReader(reader OpenMANETConfigReader) error {
	// Ensure the section exists
	_ = reader.AddSection(openmanetdConfigName, "config", "openmanet")

	if err := reader.SetType(openmanetdConfigName, "config", "BLOSconfigured", uci.TypeOption, "1"); err != nil {
		return fmt.Errorf("failed to set BLOSconfigured: %w", err)
	}

	if err := reader.Commit(); err != nil {
		return fmt.Errorf("failed to commit OpenMANET config: %w", err)
	}

	return nil
}

// ClearBLOSConfigured marks BLOS as not configured.
//
// This sets the 'BLOSconfigured' option to '0'.
//
// Example:
//
//	err := ClearBLOSConfigured()
//	if err != nil {
//	    log.Fatalf("Failed to clear BLOS configured flag: %v", err)
//	}
func ClearBLOSConfigured() error {
	return ClearBLOSConfiguredWithReader(NewUCIOpenMANETConfigReader())
}

// ClearBLOSConfiguredWithReader marks BLOS as not configured using the provided reader.
func ClearBLOSConfiguredWithReader(reader OpenMANETConfigReader) error {
	// Ensure the section exists
	_ = reader.AddSection(openmanetdConfigName, "config", "openmanet")

	if err := reader.SetType(openmanetdConfigName, "config", "BLOSconfigured", uci.TypeOption, "0"); err != nil {
		return fmt.Errorf("failed to clear BLOSconfigured: %w", err)
	}

	if err := reader.Commit(); err != nil {
		return fmt.Errorf("failed to commit OpenMANET config: %w", err)
	}

	return nil
}

// IsBatMesh1Configured checks if batman-adv mesh1 has been configured.
//
// Returns:
//   - true if BatMesh1 is configured (batmesh1configured == '1'), false otherwise
//   - An error if the configuration cannot be read
//
// Example:
//
//	configured, err := IsBatMesh1Configured()
//	if err != nil {
//	    log.Fatalf("Failed to check BatMesh1 status: %v", err)
//	}
//	if !configured {
//	    // Run BatMesh1 configuration
//	}
func IsBatMesh1Configured() (bool, error) {
	return IsBatMesh1ConfiguredWithReader(NewUCIOpenMANETConfigReader())
}

// IsBatMesh1ConfiguredWithReader checks if batman-adv mesh1 has been configured using the provided reader.
func IsBatMesh1ConfiguredWithReader(reader OpenMANETConfigReader) (bool, error) {
	config, err := GetOpenMANETConfigWithReader(reader)
	if err != nil {
		return false, err
	}

	// Parse the batmesh1configured value
	if config.BatMesh1Configured == "0" || config.BatMesh1Configured == "" {
		return false, nil
	}

	configured, err := strconv.Atoi(config.BatMesh1Configured)
	if err != nil {
		return false, fmt.Errorf("invalid batmesh1configured value: %w", err)
	}

	return configured == 1, nil
}

// SetBatMesh1Configured marks batman-adv mesh1 as configured.
//
// This sets the 'batmesh1configured' option to '1'.
//
// Example:
//
//	err := SetBatMesh1Configured()
//	if err != nil {
//	    log.Fatalf("Failed to mark BatMesh1 as configured: %v", err)
//	}
func SetBatMesh1Configured() error {
	return SetBatMesh1ConfiguredWithReader(NewUCIOpenMANETConfigReader())
}

// SetBatMesh1ConfiguredWithReader marks batman-adv mesh1 as configured using the provided reader.
func SetBatMesh1ConfiguredWithReader(reader OpenMANETConfigReader) error {
	// Ensure the section exists
	_ = reader.AddSection(openmanetdConfigName, "config", "openmanet")

	if err := reader.SetType(openmanetdConfigName, "config", "batmesh1configured", uci.TypeOption, "1"); err != nil {
		return fmt.Errorf("failed to set batmesh1configured: %w", err)
	}

	if err := reader.Commit(); err != nil {
		return fmt.Errorf("failed to commit OpenMANET config: %w", err)
	}

	return nil
}

// ClearBatMesh1Configured marks batman-adv mesh1 as not configured.
//
// This sets the 'batmesh1configured' option to '0'.
//
// Example:
//
//	err := ClearBatMesh1Configured()
//	if err != nil {
//	    log.Fatalf("Failed to clear BatMesh1 configured flag: %v", err)
//	}
func ClearBatMesh1Configured() error {
	return ClearBatMesh1ConfiguredWithReader(NewUCIOpenMANETConfigReader())
}

// ClearBatMesh1ConfiguredWithReader marks batman-adv mesh1 as not configured using the provided reader.
func ClearBatMesh1ConfiguredWithReader(reader OpenMANETConfigReader) error {
	// Ensure the section exists
	_ = reader.AddSection(openmanetdConfigName, "config", "openmanet")

	if err := reader.SetType(openmanetdConfigName, "config", "batmesh1configured", uci.TypeOption, "0"); err != nil {
		return fmt.Errorf("failed to clear batmesh1configured: %w", err)
	}

	if err := reader.Commit(); err != nil {
		return fmt.Errorf("failed to commit OpenMANET config: %w", err)
	}

	return nil
}

// GetConfigPath returns the path to the OpenMANET configuration file.
//
// Returns:
//   - The config file path (defaults to "/etc/openmanet/config.yml" if not set)
//   - An error if the configuration cannot be read
//
// Example:
//
//	path, err := GetConfigPath()
//	if err != nil {
//	    log.Fatalf("Failed to get config path: %v", err)
//	}
//	fmt.Printf("Config path: %s\n", path)
func GetConfigPath() (string, error) {
	return GetConfigPathWithReader(NewUCIOpenMANETConfigReader())
}

// GetConfigPathWithReader returns the path to the OpenMANET configuration file using the provided reader.
func GetConfigPathWithReader(reader OpenMANETConfigReader) (string, error) {
	config, err := GetOpenMANETConfigWithReader(reader)
	if err != nil {
		return "", err
	}

	if config.Config == "" {
		return "/etc/openmanet/config.yml", nil
	}

	return config.Config, nil
}

// SetConfigPath sets the path to the OpenMANET configuration file.
//
// Parameters:
//   - path: The path to the configuration file
//
// Example:
//
//	err := SetConfigPath("/custom/path/config.yml")
//	if err != nil {
//	    log.Fatalf("Failed to set config path: %v", err)
//	}
func SetConfigPath(path string) error {
	return SetConfigPathWithReader(path, NewUCIOpenMANETConfigReader())
}

// SetConfigPathWithReader sets the path to the OpenMANET configuration file using the provided reader.
func SetConfigPathWithReader(path string, reader OpenMANETConfigReader) error {
	if path == "" {
		return fmt.Errorf("config path cannot be empty")
	}

	// Ensure the section exists
	_ = reader.AddSection(openmanetdConfigName, "config", "openmanet")

	if err := reader.SetType(openmanetdConfigName, "config", "config", uci.TypeOption, path); err != nil {
		return fmt.Errorf("failed to set config path: %w", err)
	}

	return nil
}
