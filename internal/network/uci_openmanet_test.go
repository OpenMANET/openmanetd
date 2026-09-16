package network

import (
	"errors"
	"testing"

	"github.com/digineo/go-uci/v2"
)

const testOpenMANETConfigPath = "/etc/openmanet/config.yml"

// mockOpenMANETConfigReader is a mock implementation of OpenMANETConfigReader for testing.
type mockOpenMANETConfigReader struct {
	data     map[string]map[string]map[string][]string // config -> section -> option -> values
	sections map[string]map[string]string              // config -> section -> type
}

// Commit is a no-op for the mock, simulating a successful commit.
func (m *mockOpenMANETConfigReader) Commit() error {
	return nil
}

// ReloadConfig is a no-op for the mock, simulating a successful reload.
func (m *mockOpenMANETConfigReader) ReloadConfig() error {
	return nil
}

func newMockOpenMANETConfigReader() *mockOpenMANETConfigReader {
	return &mockOpenMANETConfigReader{
		data:     make(map[string]map[string]map[string][]string),
		sections: make(map[string]map[string]string),
	}
}

func (m *mockOpenMANETConfigReader) Get(config, section, option string) ([]string, bool) {
	if m.data[config] == nil {
		return nil, false
	}

	if m.data[config][section] == nil {
		return nil, false
	}

	values, ok := m.data[config][section][option]

	return values, ok
}

func (m *mockOpenMANETConfigReader) GetSections(config, secType string) ([]string, error) {
	var sections []string
	if m.sections[config] != nil {
		for section, typ := range m.sections[config] {
			if typ == secType {
				sections = append(sections, section)
			}
		}
	}

	return sections, nil
}

func (m *mockOpenMANETConfigReader) SetType(config, section, option string, typ uci.OptionType, values ...string) error {
	if m.data[config] == nil {
		m.data[config] = make(map[string]map[string][]string)
	}

	if m.data[config][section] == nil {
		m.data[config][section] = make(map[string][]string)
	}

	m.data[config][section][option] = values

	return nil
}

func (m *mockOpenMANETConfigReader) Del(config, section, option string) error {
	if m.data[config] != nil && m.data[config][section] != nil {
		delete(m.data[config][section], option)
	}

	return nil
}

func (m *mockOpenMANETConfigReader) AddSection(config, section, typ string) error {
	if m.sections[config] == nil {
		m.sections[config] = make(map[string]string)
	}

	m.sections[config][section] = typ
	if m.data[config] == nil {
		m.data[config] = make(map[string]map[string][]string)
	}

	if m.data[config][section] == nil {
		m.data[config][section] = make(map[string][]string)
	}

	return nil
}

func (m *mockOpenMANETConfigReader) DelSection(config, section string) error {
	if m.data[config] != nil {
		delete(m.data[config], section)
	}

	if m.sections[config] != nil {
		delete(m.sections[config], section)
	}

	return nil
}

// setupMockOpenMANETData initializes the mock with sample OpenMANET configuration.
func setupMockOpenMANETData(m *mockOpenMANETConfigReader) {
	_ = m.AddSection("openmanetd", "config", "openmanet")
	_ = m.SetType("openmanetd", "config", "dhcpconfigured", uci.TypeOption, "0")
	_ = m.SetType("openmanetd", "config", "config", uci.TypeOption, testOpenMANETConfigPath)
}

func TestGetOpenMANETConfigWithReader(t *testing.T) {
	mock := newMockOpenMANETConfigReader()
	setupMockOpenMANETData(mock)

	config, err := GetOpenMANETConfigWithReader(mock)
	if err != nil {
		t.Fatalf("GetOpenMANETConfigWithReader failed: %v", err)
	}

	if config.DHCPConfigured != "0" {
		t.Errorf("Expected DHCPConfigured=0, got %s", config.DHCPConfigured)
	}

	if config.Config != testOpenMANETConfigPath {
		t.Errorf("Expected Config=/etc/openmanet/config.yml, got %s", config.Config)
	}
}

func TestGetOpenMANETConfigWithReader_Empty(t *testing.T) {
	mock := newMockOpenMANETConfigReader()

	config, err := GetOpenMANETConfigWithReader(mock)
	if err != nil {
		t.Fatalf("GetOpenMANETConfigWithReader failed: %v", err)
	}

	if config.DHCPConfigured != "" {
		t.Errorf("Expected empty DHCPConfigured, got %s", config.DHCPConfigured)
	}

	if config.Config != "" {
		t.Errorf("Expected empty Config, got %s", config.Config)
	}
}

func TestSetOpenMANETConfigWithReader(t *testing.T) {
	mock := newMockOpenMANETConfigReader()

	config := &UCIOpenMANET{
		DHCPConfigured: "1",
		Config:         "/custom/path/config.yml",
	}

	err := SetOpenMANETConfigWithReader(config, mock)
	if err != nil {
		t.Fatalf("SetOpenMANETConfigWithReader failed: %v", err)
	}

	// Verify the values were set
	readConfig, err := GetOpenMANETConfigWithReader(mock)
	if err != nil {
		t.Fatalf("GetOpenMANETConfigWithReader failed: %v", err)
	}

	if readConfig.DHCPConfigured != "1" {
		t.Errorf("Expected DHCPConfigured=1, got %s", readConfig.DHCPConfigured)
	}

	if readConfig.Config != "/custom/path/config.yml" {
		t.Errorf("Expected Config=/custom/path/config.yml, got %s", readConfig.Config)
	}
}

func TestSetOpenMANETConfigWithReader_NilConfig(t *testing.T) {
	mock := newMockOpenMANETConfigReader()

	err := SetOpenMANETConfigWithReader(nil, mock)
	if err == nil {
		t.Error("Expected error for nil config, got nil")
	}
}

func TestSetOpenMANETConfigWithReader_PartialConfig(t *testing.T) {
	mock := newMockOpenMANETConfigReader()

	// Set only DHCPConfigured
	config := &UCIOpenMANET{
		DHCPConfigured: "1",
	}

	err := SetOpenMANETConfigWithReader(config, mock)
	if err != nil {
		t.Fatalf("SetOpenMANETConfigWithReader failed: %v", err)
	}

	readConfig, err := GetOpenMANETConfigWithReader(mock)
	if err != nil {
		t.Fatalf("GetOpenMANETConfigWithReader failed: %v", err)
	}

	if readConfig.DHCPConfigured != "1" {
		t.Errorf("Expected DHCPConfigured=1, got %s", readConfig.DHCPConfigured)
	}

	if readConfig.Config != "" {
		t.Errorf("Expected empty Config, got %s", readConfig.Config)
	}
}

func TestIsDHCPConfiguredWithReader(t *testing.T) {
	tests := []struct {
		name           string
		dhcpConfigured string
		expected       bool
		expectError    bool
	}{
		{
			name:           "configured",
			dhcpConfigured: "1",
			expected:       true,
			expectError:    false,
		},
		{
			name:           "not configured",
			dhcpConfigured: "0",
			expected:       false,
			expectError:    false,
		},
		{
			name:           "empty",
			dhcpConfigured: "",
			expected:       false,
			expectError:    false,
		},
		{
			name:           "invalid value",
			dhcpConfigured: "invalid",
			expected:       false,
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := newMockOpenMANETConfigReader()
			if tt.dhcpConfigured != "" {
				_ = mock.AddSection("openmanetd", "config", "openmanet")
				_ = mock.SetType("openmanetd", "config", "dhcpconfigured", uci.TypeOption, tt.dhcpConfigured)
			}

			configured, err := IsDHCPConfiguredWithReader(mock)

			if tt.expectError {
				if err == nil {
					t.Error("Expected error, got nil")
				}

				return
			}

			if err != nil {
				t.Fatalf("IsDHCPConfiguredWithReader failed: %v", err)
			}

			if configured != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, configured)
			}
		})
	}
}

func TestSetDHCPConfiguredWithReader(t *testing.T) {
	mock := newMockOpenMANETConfigReader()

	err := SetDHCPConfiguredWithReader(mock)
	if err != nil {
		t.Fatalf("SetDHCPConfiguredWithReader failed: %v", err)
	}

	values, ok := mock.Get("openmanetd", "config", "dhcpconfigured")
	if !ok || len(values) == 0 || values[0] != "1" {
		t.Errorf("Expected dhcpconfigured=1, got %v", values)
	}

	// Verify using IsDHCPConfigured
	configured, err := IsDHCPConfiguredWithReader(mock)
	if err != nil {
		t.Fatalf("IsDHCPConfiguredWithReader failed: %v", err)
	}

	if !configured {
		t.Error("Expected DHCP to be configured")
	}
}

func TestClearDHCPConfiguredWithReader(t *testing.T) {
	mock := newMockOpenMANETConfigReader()
	_ = mock.AddSection("openmanetd", "config", "openmanet")
	_ = mock.SetType("openmanetd", "config", "dhcpconfigured", uci.TypeOption, "1")

	err := ClearDHCPConfiguredWithReader(mock)
	if err != nil {
		t.Fatalf("ClearDHCPConfiguredWithReader failed: %v", err)
	}

	values, ok := mock.Get("openmanetd", "config", "dhcpconfigured")
	if !ok || len(values) == 0 || values[0] != "0" {
		t.Errorf("Expected dhcpconfigured=0, got %v", values)
	}

	// Verify using IsDHCPConfigured
	configured, err := IsDHCPConfiguredWithReader(mock)
	if err != nil {
		t.Fatalf("IsDHCPConfiguredWithReader failed: %v", err)
	}

	if configured {
		t.Error("Expected DHCP to not be configured")
	}
}

func TestGetConfigPathWithReader(t *testing.T) {
	tests := []struct {
		name         string
		configPath   string
		expectedPath string
	}{
		{
			name:         "custom path",
			configPath:   "/custom/path/config.yml",
			expectedPath: "/custom/path/config.yml",
		},
		{
			name:         "default path",
			configPath:   "",
			expectedPath: testOpenMANETConfigPath,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := newMockOpenMANETConfigReader()
			if tt.configPath != "" {
				_ = mock.AddSection("openmanetd", "config", "openmanet")
				_ = mock.SetType("openmanetd", "config", "config", uci.TypeOption, tt.configPath)
			}

			path, err := GetConfigPathWithReader(mock)
			if err != nil {
				t.Fatalf("GetConfigPathWithReader failed: %v", err)
			}

			if path != tt.expectedPath {
				t.Errorf("Expected %s, got %s", tt.expectedPath, path)
			}
		})
	}
}

func TestSetConfigPathWithReader(t *testing.T) {
	mock := newMockOpenMANETConfigReader()

	path := "/new/config/path.yml"

	err := SetConfigPathWithReader(path, mock)
	if err != nil {
		t.Fatalf("SetConfigPathWithReader failed: %v", err)
	}

	values, ok := mock.Get("openmanetd", "config", "config")
	if !ok || len(values) == 0 || values[0] != path {
		t.Errorf("Expected config=%s, got %v", path, values)
	}

	// Verify using GetConfigPath
	readPath, err := GetConfigPathWithReader(mock)
	if err != nil {
		t.Fatalf("GetConfigPathWithReader failed: %v", err)
	}

	if readPath != path {
		t.Errorf("Expected %s, got %s", path, readPath)
	}
}

func TestSetConfigPathWithReader_EmptyPath(t *testing.T) {
	mock := newMockOpenMANETConfigReader()

	err := SetConfigPathWithReader("", mock)
	if err == nil {
		t.Error("Expected error for empty path, got nil")
	}
}

// mockOpenMANETConfigReaderWithErrors is a mock that returns errors for testing error paths.
type mockOpenMANETConfigReaderWithErrors struct{}

// Commit always returns an error for error simulation.
func (m *mockOpenMANETConfigReaderWithErrors) Commit() error {
	return errors.New("mock error")
}

// ReloadConfig always returns an error for error simulation.
func (m *mockOpenMANETConfigReaderWithErrors) ReloadConfig() error {
	return errors.New("mock error")
}

func (m *mockOpenMANETConfigReaderWithErrors) Get(config, section, option string) ([]string, bool) {
	return nil, false
}

func (m *mockOpenMANETConfigReaderWithErrors) GetSections(config, secType string) ([]string, error) {
	return nil, errors.New("mock error")
}

func (m *mockOpenMANETConfigReaderWithErrors) SetType(config, section, option string, typ uci.OptionType, values ...string) error {
	return errors.New("mock error")
}

func (m *mockOpenMANETConfigReaderWithErrors) Del(config, section, option string) error {
	return errors.New("mock error")
}

func (m *mockOpenMANETConfigReaderWithErrors) AddSection(config, section, typ string) error {
	return errors.New("mock error")
}

func (m *mockOpenMANETConfigReaderWithErrors) DelSection(config, section string) error {
	return errors.New("mock error")
}

func TestSetOpenMANETConfigWithReader_ErrorHandling(t *testing.T) {
	mock := &mockOpenMANETConfigReaderWithErrors{}

	config := &UCIOpenMANET{
		DHCPConfigured: "1",
	}

	err := SetOpenMANETConfigWithReader(config, mock)
	if err == nil {
		t.Error("Expected error from SetOpenMANETConfigWithReader")
	}
}

func TestCommitWithOpenMANETReader(t *testing.T) {
	mock := newMockOpenMANETConfigReader()
	// Should succeed (no error)
	if err := mock.Commit(); err != nil {
		t.Errorf("Expected Commit to succeed, got error: %v", err)
	}

	mockErr := &mockOpenMANETConfigReaderWithErrors{}
	// Should fail (return error)
	if err := mockErr.Commit(); err == nil {
		t.Error("Expected Commit to fail, got nil error")
	}
}

func TestSetDHCPConfiguredWithReader_ErrorHandling(t *testing.T) {
	mock := &mockOpenMANETConfigReaderWithErrors{}

	err := SetDHCPConfiguredWithReader(mock)
	if err == nil {
		t.Error("Expected error from SetDHCPConfiguredWithReader")
	}
}

func TestClearDHCPConfiguredWithReader_ErrorHandling(t *testing.T) {
	mock := &mockOpenMANETConfigReaderWithErrors{}

	err := ClearDHCPConfiguredWithReader(mock)
	if err == nil {
		t.Error("Expected error from ClearDHCPConfiguredWithReader")
	}
}

func TestSetConfigPathWithReader_ErrorHandling(t *testing.T) {
	mock := &mockOpenMANETConfigReaderWithErrors{}

	err := SetConfigPathWithReader("/some/path", mock)
	if err == nil {
		t.Error("Expected error from SetConfigPathWithReader")
	}
}

func TestSetDHCPConfigured_UpdatesExistingValue(t *testing.T) {
	mock := newMockOpenMANETConfigReader()

	// Start with value set to 0
	_ = mock.AddSection("openmanetd", "config", "openmanet")
	_ = mock.SetType("openmanetd", "config", "dhcpconfigured", uci.TypeOption, "0")

	configured, _ := IsDHCPConfiguredWithReader(mock)
	if configured {
		t.Error("Expected initial state to be not configured")
	}

	// Set to configured
	err := SetDHCPConfiguredWithReader(mock)
	if err != nil {
		t.Fatalf("SetDHCPConfiguredWithReader failed: %v", err)
	}

	configured, _ = IsDHCPConfiguredWithReader(mock)
	if !configured {
		t.Error("Expected state to be configured after SetDHCPConfigured")
	}

	// Clear configured state
	err = ClearDHCPConfiguredWithReader(mock)
	if err != nil {
		t.Fatalf("ClearDHCPConfiguredWithReader failed: %v", err)
	}

	configured, _ = IsDHCPConfiguredWithReader(mock)
	if configured {
		t.Error("Expected state to be not configured after ClearDHCPConfigured")
	}
}

func TestCompleteWorkflow(t *testing.T) {
	mock := newMockOpenMANETConfigReader()

	// Step 1: Initial configuration
	config := &UCIOpenMANET{
		DHCPConfigured: "0",
		Config:         testOpenMANETConfigPath,
	}

	err := SetOpenMANETConfigWithReader(config, mock)
	if err != nil {
		t.Fatalf("Failed to set initial config: %v", err)
	}

	// Step 2: Verify initial state
	configured, err := IsDHCPConfiguredWithReader(mock)
	if err != nil {
		t.Fatalf("Failed to check DHCP configured: %v", err)
	}

	if configured {
		t.Error("Expected DHCP to not be configured initially")
	}

	// Step 3: Mark DHCP as configured
	err = SetDHCPConfiguredWithReader(mock)
	if err != nil {
		t.Fatalf("Failed to set DHCP configured: %v", err)
	}

	// Step 4: Verify DHCP is configured
	configured, err = IsDHCPConfiguredWithReader(mock)
	if err != nil {
		t.Fatalf("Failed to check DHCP configured: %v", err)
	}

	if !configured {
		t.Error("Expected DHCP to be configured")
	}

	// Step 5: Change config path
	newPath := "/new/location/config.yml"

	err = SetConfigPathWithReader(newPath, mock)
	if err != nil {
		t.Fatalf("Failed to set config path: %v", err)
	}

	// Step 6: Verify config path
	path, err := GetConfigPathWithReader(mock)
	if err != nil {
		t.Fatalf("Failed to get config path: %v", err)
	}

	if path != newPath {
		t.Errorf("Expected path %s, got %s", newPath, path)
	}

	// Step 7: Get full config
	finalConfig, err := GetOpenMANETConfigWithReader(mock)
	if err != nil {
		t.Fatalf("Failed to get final config: %v", err)
	}

	if finalConfig.DHCPConfigured != "1" {
		t.Errorf("Expected DHCPConfigured=1, got %s", finalConfig.DHCPConfigured)
	}

	if finalConfig.Config != newPath {
		t.Errorf("Expected Config=%s, got %s", newPath, finalConfig.Config)
	}
}

// ========== BLOS Configuration Tests ==========

func TestIsBLOSConfiguredWithReader(t *testing.T) {
	tests := []struct {
		name           string
		BLOSConfigured string
		expected       bool
		expectError    bool
	}{
		{
			name:           "configured",
			BLOSConfigured: "1",
			expected:       true,
			expectError:    false,
		},
		{
			name:           "not configured",
			BLOSConfigured: "0",
			expected:       false,
			expectError:    false,
		},
		{
			name:           "empty",
			BLOSConfigured: "",
			expected:       false,
			expectError:    false,
		},
		{
			name:           "invalid value",
			BLOSConfigured: "invalid",
			expected:       false,
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := newMockOpenMANETConfigReader()
			if tt.BLOSConfigured != "" {
				_ = mock.AddSection("openmanetd", "config", "openmanet")
				_ = mock.SetType("openmanetd", "config", "BLOSconfigured", uci.TypeOption, tt.BLOSConfigured)
			}

			configured, err := IsBLOSConfiguredWithReader(mock)

			if tt.expectError {
				if err == nil {
					t.Error("Expected error, got nil")
				}

				return
			}

			if err != nil {
				t.Fatalf("IsBLOSConfiguredWithReader failed: %v", err)
			}

			if configured != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, configured)
			}
		})
	}
}

func TestSetBLOSConfiguredWithReader(t *testing.T) {
	mock := newMockOpenMANETConfigReader()

	err := SetBLOSConfiguredWithReader(mock)
	if err != nil {
		t.Fatalf("SetBLOSConfiguredWithReader failed: %v", err)
	}

	values, ok := mock.Get("openmanetd", "config", "BLOSconfigured")
	if !ok || len(values) == 0 || values[0] != "1" {
		t.Errorf("Expected BLOSconfigured=1, got %v", values)
	}

	// Verify using IsBLOSConfigured
	configured, err := IsBLOSConfiguredWithReader(mock)
	if err != nil {
		t.Fatalf("IsBLOSConfiguredWithReader failed: %v", err)
	}

	if !configured {
		t.Error("Expected BLOS to be configured")
	}
}

func TestClearBLOSConfiguredWithReader(t *testing.T) {
	mock := newMockOpenMANETConfigReader()
	_ = mock.AddSection("openmanetd", "config", "openmanet")
	_ = mock.SetType("openmanetd", "config", "BLOSconfigured", uci.TypeOption, "1")

	err := ClearBLOSConfiguredWithReader(mock)
	if err != nil {
		t.Fatalf("ClearBLOSConfiguredWithReader failed: %v", err)
	}

	values, ok := mock.Get("openmanetd", "config", "BLOSconfigured")
	if !ok || len(values) == 0 || values[0] != "0" {
		t.Errorf("Expected BLOSconfigured=0, got %v", values)
	}

	// Verify using IsBLOSConfigured
	configured, err := IsBLOSConfiguredWithReader(mock)
	if err != nil {
		t.Fatalf("IsBLOSConfiguredWithReader failed: %v", err)
	}

	if configured {
		t.Error("Expected BLOS to not be configured")
	}
}

func TestSetBLOSConfiguredWithReader_ErrorHandling(t *testing.T) {
	mock := &mockOpenMANETConfigReaderWithErrors{}

	err := SetBLOSConfiguredWithReader(mock)
	if err == nil {
		t.Error("Expected error from SetBLOSConfiguredWithReader")
	}
}

func TestClearBLOSConfiguredWithReader_ErrorHandling(t *testing.T) {
	mock := &mockOpenMANETConfigReaderWithErrors{}

	err := ClearBLOSConfiguredWithReader(mock)
	if err == nil {
		t.Error("Expected error from ClearBLOSConfiguredWithReader")
	}
}

func TestSetBLOSConfigured_UpdatesExistingValue(t *testing.T) {
	mock := newMockOpenMANETConfigReader()

	// Start with value set to 0
	_ = mock.AddSection("openmanetd", "config", "openmanet")
	_ = mock.SetType("openmanetd", "config", "BLOSconfigured", uci.TypeOption, "0")

	configured, _ := IsBLOSConfiguredWithReader(mock)
	if configured {
		t.Error("Expected initial state to be not configured")
	}

	// Set to configured
	err := SetBLOSConfiguredWithReader(mock)
	if err != nil {
		t.Fatalf("SetBLOSConfiguredWithReader failed: %v", err)
	}

	configured, _ = IsBLOSConfiguredWithReader(mock)
	if !configured {
		t.Error("Expected state to be configured after SetBLOSConfigured")
	}

	// Clear configured state
	err = ClearBLOSConfiguredWithReader(mock)
	if err != nil {
		t.Fatalf("ClearBLOSConfiguredWithReader failed: %v", err)
	}

	configured, _ = IsBLOSConfiguredWithReader(mock)
	if configured {
		t.Error("Expected state to be not configured after ClearBLOSConfigured")
	}
}

func TestGetOpenMANETConfigWithReader_IncludesBLOS(t *testing.T) {
	mock := newMockOpenMANETConfigReader()
	_ = mock.AddSection("openmanetd", "config", "openmanet")
	_ = mock.SetType("openmanetd", "config", "dhcpconfigured", uci.TypeOption, "1")
	_ = mock.SetType("openmanetd", "config", "BLOSconfigured", uci.TypeOption, "1")
	_ = mock.SetType("openmanetd", "config", "config", uci.TypeOption, testOpenMANETConfigPath)

	config, err := GetOpenMANETConfigWithReader(mock)
	if err != nil {
		t.Fatalf("GetOpenMANETConfigWithReader failed: %v", err)
	}

	if config.DHCPConfigured != "1" {
		t.Errorf("Expected DHCPConfigured=1, got %s", config.DHCPConfigured)
	}

	if config.BLOSConfigured != "1" {
		t.Errorf("Expected BLOSConfigured=1, got %s", config.BLOSConfigured)
	}

	if config.Config != testOpenMANETConfigPath {
		t.Errorf("Expected Config=/etc/openmanet/config.yml, got %s", config.Config)
	}
}

func TestSetOpenMANETConfigWithReader_IncludesBLOS(t *testing.T) {
	mock := newMockOpenMANETConfigReader()

	config := &UCIOpenMANET{
		DHCPConfigured: "1",
		BLOSConfigured: "1",
		Config:         "/custom/path/config.yml",
	}

	err := SetOpenMANETConfigWithReader(config, mock)
	if err != nil {
		t.Fatalf("SetOpenMANETConfigWithReader failed: %v", err)
	}

	// Verify the values were set
	readConfig, err := GetOpenMANETConfigWithReader(mock)
	if err != nil {
		t.Fatalf("GetOpenMANETConfigWithReader failed: %v", err)
	}

	if readConfig.DHCPConfigured != "1" {
		t.Errorf("Expected DHCPConfigured=1, got %s", readConfig.DHCPConfigured)
	}

	if readConfig.BLOSConfigured != "1" {
		t.Errorf("Expected BLOSConfigured=1, got %s", readConfig.BLOSConfigured)
	}

	if readConfig.Config != "/custom/path/config.yml" {
		t.Errorf("Expected Config=/custom/path/config.yml, got %s", readConfig.Config)
	}
}

func TestCompleteWorkflowWithBLOS(t *testing.T) {
	mock := newMockOpenMANETConfigReader()

	// Step 1: Initial configuration
	config := &UCIOpenMANET{
		DHCPConfigured: "0",
		BLOSConfigured: "0",
		Config:         testOpenMANETConfigPath,
	}

	err := SetOpenMANETConfigWithReader(config, mock)
	if err != nil {
		t.Fatalf("Failed to set initial config: %v", err)
	}

	// Step 2: Verify initial state
	dhcpConfigured, err := IsDHCPConfiguredWithReader(mock)
	if err != nil {
		t.Fatalf("Failed to check DHCP configured: %v", err)
	}

	if dhcpConfigured {
		t.Error("Expected DHCP to not be configured initially")
	}

	BLOSConfigured, err := IsBLOSConfiguredWithReader(mock)
	if err != nil {
		t.Fatalf("Failed to check BLOS configured: %v", err)
	}

	if BLOSConfigured {
		t.Error("Expected BLOS to not be configured initially")
	}

	// Step 3: Mark DHCP and BLOS as configured
	err = SetDHCPConfiguredWithReader(mock)
	if err != nil {
		t.Fatalf("Failed to set DHCP configured: %v", err)
	}

	err = SetBLOSConfiguredWithReader(mock)
	if err != nil {
		t.Fatalf("Failed to set BLOS configured: %v", err)
	}

	// Step 4: Verify both are configured
	dhcpConfigured, err = IsDHCPConfiguredWithReader(mock)
	if err != nil {
		t.Fatalf("Failed to check DHCP configured: %v", err)
	}

	if !dhcpConfigured {
		t.Error("Expected DHCP to be configured")
	}

	BLOSConfigured, err = IsBLOSConfiguredWithReader(mock)
	if err != nil {
		t.Fatalf("Failed to check BLOS configured: %v", err)
	}

	if !BLOSConfigured {
		t.Error("Expected BLOS to be configured")
	}

	// Step 5: Get full config
	finalConfig, err := GetOpenMANETConfigWithReader(mock)
	if err != nil {
		t.Fatalf("Failed to get final config: %v", err)
	}

	if finalConfig.DHCPConfigured != "1" {
		t.Errorf("Expected DHCPConfigured=1, got %s", finalConfig.DHCPConfigured)
	}

	if finalConfig.BLOSConfigured != "1" {
		t.Errorf("Expected BLOSConfigured=1, got %s", finalConfig.BLOSConfigured)
	}

	// Step 6: Clear BLOS configured
	err = ClearBLOSConfiguredWithReader(mock)
	if err != nil {
		t.Fatalf("Failed to clear BLOS configured: %v", err)
	}

	// Step 7: Verify BLOS is not configured but DHCP still is
	BLOSConfigured, err = IsBLOSConfiguredWithReader(mock)
	if err != nil {
		t.Fatalf("Failed to check BLOS configured: %v", err)
	}

	if BLOSConfigured {
		t.Error("Expected BLOS to not be configured after clearing")
	}

	dhcpConfigured, err = IsDHCPConfiguredWithReader(mock)
	if err != nil {
		t.Fatalf("Failed to check DHCP configured: %v", err)
	}

	if !dhcpConfigured {
		t.Error("Expected DHCP to still be configured")
	}
}

// ========== BatMesh1 Configuration Tests ==========

func TestIsBatMesh1ConfiguredWithReader(t *testing.T) {
	tests := []struct {
		name               string
		batMesh1Configured string
		expected           bool
		expectError        bool
	}{
		{
			name:               "configured",
			batMesh1Configured: "1",
			expected:           true,
			expectError:        false,
		},
		{
			name:               "not configured",
			batMesh1Configured: "0",
			expected:           false,
			expectError:        false,
		},
		{
			name:               "empty",
			batMesh1Configured: "",
			expected:           false,
			expectError:        false,
		},
		{
			name:               "invalid value",
			batMesh1Configured: "invalid",
			expected:           false,
			expectError:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := newMockOpenMANETConfigReader()
			if tt.batMesh1Configured != "" {
				_ = mock.AddSection("openmanetd", "config", "openmanet")
				_ = mock.SetType("openmanetd", "config", "batmesh1configured", uci.TypeOption, tt.batMesh1Configured)
			}

			configured, err := IsBatMesh1ConfiguredWithReader(mock)

			if tt.expectError {
				if err == nil {
					t.Error("Expected error, got nil")
				}

				return
			}

			if err != nil {
				t.Fatalf("IsBatMesh1ConfiguredWithReader failed: %v", err)
			}

			if configured != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, configured)
			}
		})
	}
}

func TestSetBatMesh1ConfiguredWithReader(t *testing.T) {
	mock := newMockOpenMANETConfigReader()

	err := SetBatMesh1ConfiguredWithReader(mock)
	if err != nil {
		t.Fatalf("SetBatMesh1ConfiguredWithReader failed: %v", err)
	}

	values, ok := mock.Get("openmanetd", "config", "batmesh1configured")
	if !ok || len(values) == 0 || values[0] != "1" {
		t.Errorf("Expected batmesh1configured=1, got %v", values)
	}

	// Verify using IsBatMesh1Configured
	configured, err := IsBatMesh1ConfiguredWithReader(mock)
	if err != nil {
		t.Fatalf("IsBatMesh1ConfiguredWithReader failed: %v", err)
	}

	if !configured {
		t.Error("Expected BatMesh1 to be configured")
	}
}

func TestClearBatMesh1ConfiguredWithReader(t *testing.T) {
	mock := newMockOpenMANETConfigReader()
	_ = mock.AddSection("openmanetd", "config", "openmanet")
	_ = mock.SetType("openmanetd", "config", "batmesh1configured", uci.TypeOption, "1")

	err := ClearBatMesh1ConfiguredWithReader(mock)
	if err != nil {
		t.Fatalf("ClearBatMesh1ConfiguredWithReader failed: %v", err)
	}

	values, ok := mock.Get("openmanetd", "config", "batmesh1configured")
	if !ok || len(values) == 0 || values[0] != "0" {
		t.Errorf("Expected batmesh1configured=0, got %v", values)
	}

	// Verify using IsBatMesh1Configured
	configured, err := IsBatMesh1ConfiguredWithReader(mock)
	if err != nil {
		t.Fatalf("IsBatMesh1ConfiguredWithReader failed: %v", err)
	}

	if configured {
		t.Error("Expected BatMesh1 to not be configured")
	}
}

func TestSetBatMesh1ConfiguredWithReader_ErrorHandling(t *testing.T) {
	mock := &mockOpenMANETConfigReaderWithErrors{}

	err := SetBatMesh1ConfiguredWithReader(mock)
	if err == nil {
		t.Error("Expected error from SetBatMesh1ConfiguredWithReader")
	}
}

func TestClearBatMesh1ConfiguredWithReader_ErrorHandling(t *testing.T) {
	mock := &mockOpenMANETConfigReaderWithErrors{}

	err := ClearBatMesh1ConfiguredWithReader(mock)
	if err == nil {
		t.Error("Expected error from ClearBatMesh1ConfiguredWithReader")
	}
}

func TestSetBatMesh1Configured_UpdatesExistingValue(t *testing.T) {
	mock := newMockOpenMANETConfigReader()

	// Start with value set to 0
	_ = mock.AddSection("openmanetd", "config", "openmanet")
	_ = mock.SetType("openmanetd", "config", "batmesh1configured", uci.TypeOption, "0")

	configured, _ := IsBatMesh1ConfiguredWithReader(mock)
	if configured {
		t.Error("Expected initial state to be not configured")
	}

	// Set to configured
	err := SetBatMesh1ConfiguredWithReader(mock)
	if err != nil {
		t.Fatalf("SetBatMesh1ConfiguredWithReader failed: %v", err)
	}

	configured, _ = IsBatMesh1ConfiguredWithReader(mock)
	if !configured {
		t.Error("Expected state to be configured after SetBatMesh1Configured")
	}

	// Clear configured state
	err = ClearBatMesh1ConfiguredWithReader(mock)
	if err != nil {
		t.Fatalf("ClearBatMesh1ConfiguredWithReader failed: %v", err)
	}

	configured, _ = IsBatMesh1ConfiguredWithReader(mock)
	if configured {
		t.Error("Expected state to be not configured after ClearBatMesh1Configured")
	}
}

func TestGetOpenMANETConfigWithReader_IncludesBatMesh1(t *testing.T) {
	mock := newMockOpenMANETConfigReader()
	_ = mock.AddSection("openmanetd", "config", "openmanet")
	_ = mock.SetType("openmanetd", "config", "dhcpconfigured", uci.TypeOption, "1")
	_ = mock.SetType("openmanetd", "config", "BLOSconfigured", uci.TypeOption, "1")
	_ = mock.SetType("openmanetd", "config", "batmesh1configured", uci.TypeOption, "1")
	_ = mock.SetType("openmanetd", "config", "config", uci.TypeOption, testOpenMANETConfigPath)

	config, err := GetOpenMANETConfigWithReader(mock)
	if err != nil {
		t.Fatalf("GetOpenMANETConfigWithReader failed: %v", err)
	}

	if config.DHCPConfigured != "1" {
		t.Errorf("Expected DHCPConfigured=1, got %s", config.DHCPConfigured)
	}

	if config.BLOSConfigured != "1" {
		t.Errorf("Expected BLOSConfigured=1, got %s", config.BLOSConfigured)
	}

	if config.BatMesh1Configured != "1" {
		t.Errorf("Expected BatMesh1Configured=1, got %s", config.BatMesh1Configured)
	}

	if config.Config != testOpenMANETConfigPath {
		t.Errorf("Expected Config=%s, got %s", testOpenMANETConfigPath, config.Config)
	}
}

func TestSetOpenMANETConfigWithReader_IncludesBatMesh1(t *testing.T) {
	mock := newMockOpenMANETConfigReader()

	config := &UCIOpenMANET{
		DHCPConfigured:     "1",
		BLOSConfigured:     "1",
		BatMesh1Configured: "1",
		Config:             "/custom/path/config.yml",
	}

	err := SetOpenMANETConfigWithReader(config, mock)
	if err != nil {
		t.Fatalf("SetOpenMANETConfigWithReader failed: %v", err)
	}

	// Verify the values were set
	readConfig, err := GetOpenMANETConfigWithReader(mock)
	if err != nil {
		t.Fatalf("GetOpenMANETConfigWithReader failed: %v", err)
	}

	if readConfig.DHCPConfigured != "1" {
		t.Errorf("Expected DHCPConfigured=1, got %s", readConfig.DHCPConfigured)
	}

	if readConfig.BLOSConfigured != "1" {
		t.Errorf("Expected BLOSConfigured=1, got %s", readConfig.BLOSConfigured)
	}

	if readConfig.BatMesh1Configured != "1" {
		t.Errorf("Expected BatMesh1Configured=1, got %s", readConfig.BatMesh1Configured)
	}

	if readConfig.Config != "/custom/path/config.yml" {
		t.Errorf("Expected Config=/custom/path/config.yml, got %s", readConfig.Config)
	}
}
