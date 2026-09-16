package network

import (
	"context"
	"database/sql"
	"errors"
	"math/rand"
	"testing"

	"github.com/digineo/go-uci/v2"
	"github.com/openmanet/openmanetd/internal/database/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newSeededRand returns a *rand.Rand seeded deterministically so
// tests over the wizard's random helpers are reproducible.
func newSeededRand(t *testing.T, seed int64) *rand.Rand {
	t.Helper()

	return rand.New(rand.NewSource(seed))
}

const testMACAddress = "AA:BB:CC:DD:EE:FF"

// mockDHCPConfigReader is a mock implementation of DHCPConfigReader for testing.
type mockDHCPConfigReader struct {
	data     map[string]map[string]map[string][]string // config -> section -> option -> values
	sections map[string]map[string]string              // config -> section -> type
}

// Commit is a no-op for the mock, simulating a successful commit.
func (m *mockDHCPConfigReader) Commit() error {
	return nil
}

// ReloadConfig is a no-op for the mock, simulating a successful reload.
func (m *mockDHCPConfigReader) ReloadConfig() error {
	return nil
}

func newMockDHCPConfigReader() *mockDHCPConfigReader {
	return &mockDHCPConfigReader{
		data:     make(map[string]map[string]map[string][]string),
		sections: make(map[string]map[string]string),
	}
}

func (m *mockDHCPConfigReader) Get(config, section, option string) ([]string, bool) {
	if m.data[config] == nil {
		return nil, false
	}

	if m.data[config][section] == nil {
		return nil, false
	}

	values, ok := m.data[config][section][option]

	return values, ok
}

func (m *mockDHCPConfigReader) GetSections(config, secType string) ([]string, error) {
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

func (m *mockDHCPConfigReader) SetType(config, section, option string, typ uci.OptionType, values ...string) error {
	if m.data[config] == nil {
		m.data[config] = make(map[string]map[string][]string)
	}

	if m.data[config][section] == nil {
		m.data[config][section] = make(map[string][]string)
	}

	m.data[config][section][option] = values

	return nil
}

func (m *mockDHCPConfigReader) Del(config, section, option string) error {
	if m.data[config] != nil && m.data[config][section] != nil {
		delete(m.data[config][section], option)
	}

	return nil
}

func (m *mockDHCPConfigReader) AddSection(config, section, typ string) error {
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

func (m *mockDHCPConfigReader) DelSection(config, section string) error {
	if m.data[config] != nil {
		delete(m.data[config], section)
	}

	if m.sections[config] != nil {
		delete(m.sections[config], section)
	}

	return nil
}

// setupMockDnsmasqData initializes the mock with sample dnsmasq configuration.
func setupMockDnsmasqData(m *mockDHCPConfigReader) {
	_ = m.AddSection("dhcp", "dnsmasq", "dnsmasq")
	_ = m.SetType("dhcp", "dnsmasq", "domainneeded", uci.TypeOption, "1")
	_ = m.SetType("dhcp", "dnsmasq", "localize_queries", uci.TypeOption, "1")
	_ = m.SetType("dhcp", "dnsmasq", "rebind_localhost", uci.TypeOption, "1")
	_ = m.SetType("dhcp", "dnsmasq", "local", uci.TypeOption, "/lan/")
	_ = m.SetType("dhcp", "dnsmasq", "domain", uci.TypeOption, "lan")
	_ = m.SetType("dhcp", "dnsmasq", "expandhosts", uci.TypeOption, "1")
	_ = m.SetType("dhcp", "dnsmasq", "cachesize", uci.TypeOption, "1000")
	_ = m.SetType("dhcp", "dnsmasq", "authoritative", uci.TypeOption, "1")
	_ = m.SetType("dhcp", "dnsmasq", "readethers", uci.TypeOption, "1")
	_ = m.SetType("dhcp", "dnsmasq", "localservice", uci.TypeOption, "1")
	_ = m.SetType("dhcp", "dnsmasq", "ednspacket_max", uci.TypeOption, "1232")
}

// setupMockDHCPData initializes the mock with sample DHCP pool configurations.
func setupMockDHCPData(m *mockDHCPConfigReader) {
	// LAN DHCP
	_ = m.AddSection("dhcp", "lan", "dhcp")
	_ = m.SetType("dhcp", "lan", "interface", uci.TypeOption, "lan")
	_ = m.SetType("dhcp", "lan", "start", uci.TypeOption, "100")
	_ = m.SetType("dhcp", "lan", "limit", uci.TypeOption, "150")
	_ = m.SetType("dhcp", "lan", "leasetime", uci.TypeOption, "12h")
	_ = m.SetType("dhcp", "lan", "ra", uci.TypeOption, "server")
	_ = m.SetType("dhcp", "lan", "ra_default", uci.TypeOption, "1")

	// WAN DHCP (disabled)
	_ = m.AddSection("dhcp", "wan", "dhcp")
	_ = m.SetType("dhcp", "wan", "interface", uci.TypeOption, "wan")
	_ = m.SetType("dhcp", "wan", "ignore", uci.TypeOption, "1")

	// AHWLAN DHCP
	_ = m.AddSection("dhcp", "ahwlan", "dhcp")
	_ = m.SetType("dhcp", "ahwlan", "interface", uci.TypeOption, "ahwlan")
	_ = m.SetType("dhcp", "ahwlan", "start", uci.TypeOption, "100")
	_ = m.SetType("dhcp", "ahwlan", "limit", uci.TypeOption, "150")
	_ = m.SetType("dhcp", "ahwlan", "leasetime", uci.TypeOption, "12h")
	_ = m.SetType("dhcp", "ahwlan", "force", uci.TypeOption, "1")
}

func TestGetDnsmasqConfigWithReader(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(*mockDHCPConfigReader)
		expected UCIDnsmasq
	}{
		{
			name:  "all fields present",
			setup: setupMockDnsmasqData,
			expected: UCIDnsmasq{
				DomainNeeded:    "1",
				LocaliseQueries: "1",
				RebindLocalhost: "1",
				Local:           "/lan/",
				Domain:          "lan",
				ExpandHosts:     "1",
				CacheSize:       "1000",
				Authoritative:   "1",
				ReadEthers:      "1",
				LocalService:    "1",
				EdnsPacketMax:   "1232",
			},
		},
		{
			name:     "empty section returns zero-value config",
			setup:    func(m *mockDHCPConfigReader) {},
			expected: UCIDnsmasq{},
		},
		{
			name: "partial fields",
			setup: func(m *mockDHCPConfigReader) {
				_ = m.AddSection("dhcp", "dnsmasq", "dnsmasq")
				_ = m.SetType("dhcp", "dnsmasq", "domainneeded", uci.TypeOption, "1")
				_ = m.SetType("dhcp", "dnsmasq", "domain", uci.TypeOption, "mesh")
				_ = m.SetType("dhcp", "dnsmasq", "cachesize", uci.TypeOption, "500")
			},
			expected: UCIDnsmasq{
				DomainNeeded: "1",
				Domain:       "mesh",
				CacheSize:    "500",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := newMockDHCPConfigReader()
			tt.setup(mock)

			config, err := GetDnsmasqConfigWithReader(mock)
			if err != nil {
				t.Fatalf("GetDnsmasqConfigWithReader failed: %v", err)
			}

			if config.DomainNeeded != tt.expected.DomainNeeded {
				t.Errorf("DomainNeeded = %q, want %q", config.DomainNeeded, tt.expected.DomainNeeded)
			}

			if config.LocaliseQueries != tt.expected.LocaliseQueries {
				t.Errorf("LocaliseQueries = %q, want %q", config.LocaliseQueries, tt.expected.LocaliseQueries)
			}

			if config.RebindLocalhost != tt.expected.RebindLocalhost {
				t.Errorf("RebindLocalhost = %q, want %q", config.RebindLocalhost, tt.expected.RebindLocalhost)
			}

			if config.Local != tt.expected.Local {
				t.Errorf("Local = %q, want %q", config.Local, tt.expected.Local)
			}

			if config.Domain != tt.expected.Domain {
				t.Errorf("Domain = %q, want %q", config.Domain, tt.expected.Domain)
			}

			if config.ExpandHosts != tt.expected.ExpandHosts {
				t.Errorf("ExpandHosts = %q, want %q", config.ExpandHosts, tt.expected.ExpandHosts)
			}

			if config.CacheSize != tt.expected.CacheSize {
				t.Errorf("CacheSize = %q, want %q", config.CacheSize, tt.expected.CacheSize)
			}

			if config.Authoritative != tt.expected.Authoritative {
				t.Errorf("Authoritative = %q, want %q", config.Authoritative, tt.expected.Authoritative)
			}

			if config.ReadEthers != tt.expected.ReadEthers {
				t.Errorf("ReadEthers = %q, want %q", config.ReadEthers, tt.expected.ReadEthers)
			}

			if config.LocalService != tt.expected.LocalService {
				t.Errorf("LocalService = %q, want %q", config.LocalService, tt.expected.LocalService)
			}

			if config.EdnsPacketMax != tt.expected.EdnsPacketMax {
				t.Errorf("EdnsPacketMax = %q, want %q", config.EdnsPacketMax, tt.expected.EdnsPacketMax)
			}
		})
	}
}

func TestGetDHCPConfigWithReader(t *testing.T) {
	mock := newMockDHCPConfigReader()
	setupMockDHCPData(mock)

	// Test LAN DHCP
	lanConfig, err := GetDHCPConfigWithReader("lan", mock)
	if err != nil {
		t.Fatalf("GetDHCPConfigWithReader(lan) failed: %v", err)
	}

	if lanConfig.Interface != "lan" {
		t.Errorf("Expected Interface=lan, got %s", lanConfig.Interface)
	}

	if lanConfig.Start != "100" {
		t.Errorf("Expected Start=100, got %s", lanConfig.Start)
	}

	if lanConfig.Limit != "150" {
		t.Errorf("Expected Limit=150, got %s", lanConfig.Limit)
	}

	if lanConfig.LeaseTime != "12h" {
		t.Errorf("Expected LeaseTime=12h, got %s", lanConfig.LeaseTime)
	}

	if lanConfig.Ra != "server" {
		t.Errorf("Expected Ra=server, got %s", lanConfig.Ra)
	}

	// Test WAN DHCP (disabled)
	wanConfig, err := GetDHCPConfigWithReader("wan", mock)
	if err != nil {
		t.Fatalf("GetDHCPConfigWithReader(wan) failed: %v", err)
	}

	if wanConfig.Interface != "wan" {
		t.Errorf("Expected Interface=wan, got %s", wanConfig.Interface)
	}

	if wanConfig.Ignore != "1" {
		t.Errorf("Expected Ignore=1, got %s", wanConfig.Ignore)
	}

	// Test AHWLAN DHCP (with Force option)
	ahwlanConfig, err := GetDHCPConfigWithReader("ahwlan", mock)
	if err != nil {
		t.Fatalf("GetDHCPConfigWithReader(ahwlan) failed: %v", err)
	}

	if ahwlanConfig.Interface != "ahwlan" {
		t.Errorf("Expected Interface=ahwlan, got %s", ahwlanConfig.Interface)
	}

	if ahwlanConfig.Force != "1" {
		t.Errorf("Expected Force=1, got %s", ahwlanConfig.Force)
	}
}

func TestSetDHCPConfigWithReader(t *testing.T) {
	mock := newMockDHCPConfigReader()

	config := &UCIDHCP{
		Interface: "guest",
		Start:     "50",
		Limit:     "100",
		LeaseTime: "6h",
		Ignore:    "0",
		Force:     "1",
	}

	err := SetDHCPConfigWithReader("guest", config, mock)
	if err != nil {
		t.Fatalf("SetDHCPConfigWithReader failed: %v", err)
	}

	// Verify the values were set
	readConfig, err := GetDHCPConfigWithReader("guest", mock)
	if err != nil {
		t.Fatalf("GetDHCPConfigWithReader failed: %v", err)
	}

	if readConfig.Interface != "guest" {
		t.Errorf("Expected Interface=guest, got %s", readConfig.Interface)
	}

	if readConfig.Start != "50" {
		t.Errorf("Expected Start=50, got %s", readConfig.Start)
	}

	if readConfig.Limit != "100" {
		t.Errorf("Expected Limit=100, got %s", readConfig.Limit)
	}

	if readConfig.LeaseTime != "6h" {
		t.Errorf("Expected LeaseTime=6h, got %s", readConfig.LeaseTime)
	}

	if readConfig.Force != "1" {
		t.Errorf("Expected Force=1, got %s", readConfig.Force)
	}
}

func TestSetDHCPConfigWithReader_NilConfig(t *testing.T) {
	mock := newMockDHCPConfigReader()

	err := SetDHCPConfigWithReader("test", nil, mock)
	if err == nil {
		t.Error("Expected error for nil config, got nil")
	}
}

func TestDeleteDHCPConfigWithReader(t *testing.T) {
	mock := newMockDHCPConfigReader()
	setupMockDHCPData(mock)

	// Delete lan section
	err := DeleteDHCPConfigWithReader("lan", mock)
	if err != nil {
		t.Fatalf("DeleteDHCPConfigWithReader failed: %v", err)
	}

	// Verify it's deleted
	config, _ := GetDHCPConfigWithReader("lan", mock)
	if config.Interface != "" {
		t.Error("Expected empty config after deletion")
	}
}

func TestDHCPSectionExistsWithReader(t *testing.T) {
	tests := []struct {
		setup   func(*mockDHCPConfigReader)
		name    string
		section string
		want    bool
	}{
		{
			name:    "section_exists",
			section: "lan",
			setup: func(m *mockDHCPConfigReader) {
				_ = m.AddSection("dhcp", "lan", "dhcp")
				_ = m.SetType("dhcp", "lan", "interface", uci.TypeOption, "lan")
			},
			want: true,
		},
		{
			name:    "section_does_not_exist",
			section: "wan",
			setup:   func(m *mockDHCPConfigReader) {},
			want:    false,
		},
		{
			name:    "section_exists_no_interface",
			section: "guest",
			setup: func(m *mockDHCPConfigReader) {
				_ = m.AddSection("dhcp", "guest", "dhcp")
				_ = m.SetType("dhcp", "guest", "start", uci.TypeOption, "100")
			},
			want: false,
		},
		{
			name:    "section_exists_with_interface",
			section: "ahwlan",
			setup: func(m *mockDHCPConfigReader) {
				_ = m.AddSection("dhcp", "ahwlan", "dhcp")
				_ = m.SetType("dhcp", "ahwlan", "interface", uci.TypeOption, "ahwlan")
				_ = m.SetType("dhcp", "ahwlan", "start", uci.TypeOption, "100")
				_ = m.SetType("dhcp", "ahwlan", "limit", uci.TypeOption, "150")
			},
			want: true,
		},
		{
			name:    "multiple_sections_check_specific",
			section: "lan",
			setup: func(m *mockDHCPConfigReader) {
				_ = m.AddSection("dhcp", "lan", "dhcp")
				_ = m.SetType("dhcp", "lan", "interface", uci.TypeOption, "lan")
				_ = m.AddSection("dhcp", "wan", "dhcp")
				_ = m.SetType("dhcp", "wan", "interface", uci.TypeOption, "wan")
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := newMockDHCPConfigReader()
			if tt.setup != nil {
				tt.setup(mock)
			}

			got := DHCPSectionExistsWithReader(tt.section, mock)
			if got != tt.want {
				t.Errorf("DHCPSectionExistsWithReader() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEnableDHCPWithReader(t *testing.T) {
	mock := newMockDHCPConfigReader()
	_ = mock.AddSection("dhcp", "test", "dhcp")
	_ = mock.SetType("dhcp", "test", "ignore", uci.TypeOption, "1")

	err := EnableDHCPWithReader("test", mock)
	if err != nil {
		t.Fatalf("EnableDHCPWithReader failed: %v", err)
	}

	values, ok := mock.Get("dhcp", "test", "ignore")
	if !ok || len(values) == 0 || values[0] != "0" {
		t.Errorf("Expected ignore=0, got %v", values)
	}
}

func TestDisableDHCPWithReader(t *testing.T) {
	mock := newMockDHCPConfigReader()
	_ = mock.AddSection("dhcp", "test", "dhcp")
	_ = mock.SetType("dhcp", "test", "ignore", uci.TypeOption, "0")

	err := DisableDHCPWithReader("test", mock)
	if err != nil {
		t.Fatalf("DisableDHCPWithReader failed: %v", err)
	}

	values, ok := mock.Get("dhcp", "test", "ignore")
	if !ok || len(values) == 0 || values[0] != "1" {
		t.Errorf("Expected ignore=1, got %v", values)
	}
}

func TestIsDHCPEnabledWithReader(t *testing.T) {
	mock := newMockDHCPConfigReader()
	setupMockDHCPData(mock)

	// Test enabled DHCP (lan)
	enabled, err := IsDHCPEnabledWithReader("lan", mock)
	if err != nil {
		t.Fatalf("IsDHCPEnabledWithReader(lan) failed: %v", err)
	}

	if !enabled {
		t.Error("Expected lan DHCP to be enabled")
	}

	// Test disabled DHCP (wan)
	enabled, err = IsDHCPEnabledWithReader("wan", mock)
	if err != nil {
		t.Fatalf("IsDHCPEnabledWithReader(wan) failed: %v", err)
	}

	if enabled {
		t.Error("Expected wan DHCP to be disabled")
	}
}

func TestSetDHCPRangeWithReader(t *testing.T) {
	mock := newMockDHCPConfigReader()
	_ = mock.AddSection("dhcp", "test", "dhcp")

	err := SetDHCPRangeWithReader("test", "200", "50", mock)
	if err != nil {
		t.Fatalf("SetDHCPRangeWithReader failed: %v", err)
	}

	start, _ := mock.Get("dhcp", "test", "start")
	if len(start) == 0 || start[0] != "200" {
		t.Errorf("Expected start=200, got %v", start)
	}

	limit, _ := mock.Get("dhcp", "test", "limit")
	if len(limit) == 0 || limit[0] != "50" {
		t.Errorf("Expected limit=50, got %v", limit)
	}
}

func TestSetDHCPRangeWithReader_InvalidStart(t *testing.T) {
	mock := newMockDHCPConfigReader()

	err := SetDHCPRangeWithReader("test", "invalid", "50", mock)
	if err == nil {
		t.Error("Expected error for invalid start value")
	}
}

func TestSetDHCPRangeWithReader_InvalidLimit(t *testing.T) {
	mock := newMockDHCPConfigReader()

	err := SetDHCPRangeWithReader("test", "100", "invalid", mock)
	if err == nil {
		t.Error("Expected error for invalid limit value")
	}
}

func TestSetDHCPLeaseTimeWithReader(t *testing.T) {
	mock := newMockDHCPConfigReader()
	_ = mock.AddSection("dhcp", "test", "dhcp")

	err := SetDHCPLeaseTimeWithReader("test", "24h", mock)
	if err != nil {
		t.Fatalf("SetDHCPLeaseTimeWithReader failed: %v", err)
	}

	leasetime, _ := mock.Get("dhcp", "test", "leasetime")
	if len(leasetime) == 0 || leasetime[0] != "24h" {
		t.Errorf("Expected leasetime=24h, got %v", leasetime)
	}
}

// mockDHCPConfigReaderWithErrors is a mock that returns errors for testing error paths.
type mockDHCPConfigReaderWithErrors struct{}

// Commit always returns an error for error simulation.
func (m *mockDHCPConfigReaderWithErrors) Commit() error {
	return errors.New("mock error")
}

// ReloadConfig always returns an error for error simulation.
func (m *mockDHCPConfigReaderWithErrors) ReloadConfig() error {
	return errors.New("mock error")
}

func (m *mockDHCPConfigReaderWithErrors) Get(config, section, option string) ([]string, bool) {
	return nil, false
}

func (m *mockDHCPConfigReaderWithErrors) GetSections(config, secType string) ([]string, error) {
	return nil, errors.New("mock error")
}

func (m *mockDHCPConfigReaderWithErrors) SetType(config, section, option string, typ uci.OptionType, values ...string) error {
	return errors.New("mock error")
}

func (m *mockDHCPConfigReaderWithErrors) Del(config, section, option string) error {
	return errors.New("mock error")
}

func (m *mockDHCPConfigReaderWithErrors) AddSection(config, section, typ string) error {
	return errors.New("mock error")
}

func (m *mockDHCPConfigReaderWithErrors) DelSection(config, section string) error {
	return errors.New("mock error")
}

func TestSetDHCPConfigWithReader_ErrorHandling(t *testing.T) {
	mock := &mockDHCPConfigReaderWithErrors{}

	config := &UCIDHCP{
		Interface: "test",
	}

	err := SetDHCPConfigWithReader("test", config, mock)
	if err == nil {
		t.Error("Expected error from SetDHCPConfigWithReader")
	}
}

func TestCommitWithReader(t *testing.T) {
	mock := newMockDHCPConfigReader()
	// Should succeed (no error)
	if err := mock.Commit(); err != nil {
		t.Errorf("Expected Commit to succeed, got error: %v", err)
	}

	mockErr := &mockDHCPConfigReaderWithErrors{}
	// Should fail (return error)
	if err := mockErr.Commit(); err == nil {
		t.Error("Expected Commit to fail, got nil error")
	}
}

func TestDeleteDHCPConfigWithReader_ErrorHandling(t *testing.T) {
	mock := &mockDHCPConfigReaderWithErrors{}

	err := DeleteDHCPConfigWithReader("test", mock)
	if err == nil {
		t.Error("Expected error from DeleteDHCPConfigWithReader")
	}
}

func TestEnableDHCPWithReader_ErrorHandling(t *testing.T) {
	mock := &mockDHCPConfigReaderWithErrors{}

	err := EnableDHCPWithReader("test", mock)
	if err == nil {
		t.Error("Expected error from EnableDHCPWithReader")
	}
}

func TestDisableDHCPWithReader_ErrorHandling(t *testing.T) {
	mock := &mockDHCPConfigReaderWithErrors{}

	err := DisableDHCPWithReader("test", mock)
	if err == nil {
		t.Error("Expected error from DisableDHCPWithReader")
	}
}

func TestSetDHCPRangeWithReader_ErrorHandling(t *testing.T) {
	mock := &mockDHCPConfigReaderWithErrors{}

	err := SetDHCPRangeWithReader("test", "100", "50", mock)
	if err == nil {
		t.Error("Expected error from SetDHCPRangeWithReader")
	}
}

func TestSetDHCPLeaseTimeWithReader_ErrorHandling(t *testing.T) {
	mock := &mockDHCPConfigReaderWithErrors{}

	err := SetDHCPLeaseTimeWithReader("test", "12h", mock)
	if err == nil {
		t.Error("Expected error from SetDHCPLeaseTimeWithReader")
	}
}

func TestCalculateAvailableDHCPStart(t *testing.T) {
	tests := []struct {
		name         string
		networkAddr  string
		subnetMask   string
		nodes        []models.MeshNode
		desiredLimit int
		expectedMin  int
		expectedMax  int
		expectError  bool
	}{
		{
			name:         "no existing ranges",
			nodes:        []models.MeshNode{},
			networkAddr:  "10.41.0.0",
			subnetMask:   "255.255.0.0",
			desiredLimit: 150,
			expectedMin:  100,
			expectedMax:  100,
			expectError:  false,
		},
		{
			name: "one existing range - find gap after",
			nodes: []models.MeshNode{
				{
					UciDhcpStart: sql.NullInt64{Int64: 100, Valid: true},
					UciDhcpLimit: sql.NullInt64{Int64: 150, Valid: true},
				},
			},
			networkAddr:  "10.41.0.0",
			subnetMask:   "255.255.0.0",
			desiredLimit: 100,
			expectedMin:  250,
			expectedMax:  250,
			expectError:  false,
		},
		{
			name: "one existing range - find gap before",
			nodes: []models.MeshNode{
				{
					UciDhcpStart: sql.NullInt64{Int64: 200, Valid: true},
					UciDhcpLimit: sql.NullInt64{Int64: 50, Valid: true},
				},
			},
			networkAddr:  "10.41.0.0",
			subnetMask:   "255.255.0.0",
			desiredLimit: 50,
			expectedMin:  100,
			expectedMax:  100,
			expectError:  false,
		},
		{
			name: "multiple existing ranges",
			nodes: []models.MeshNode{
				{
					UciDhcpStart: sql.NullInt64{Int64: 100, Valid: true},
					UciDhcpLimit: sql.NullInt64{Int64: 50, Valid: true},
				},
				{
					UciDhcpStart: sql.NullInt64{Int64: 200, Valid: true},
					UciDhcpLimit: sql.NullInt64{Int64: 100, Valid: true},
				},
				{
					UciDhcpStart: sql.NullInt64{Int64: 400, Valid: true},
					UciDhcpLimit: sql.NullInt64{Int64: 50, Valid: true},
				},
			},
			networkAddr:  "10.41.0.0",
			subnetMask:   "255.255.0.0",
			desiredLimit: 90,
			expectedMin:  300,
			expectedMax:  300,
			expectError:  false,
		},
		{
			name: "ranges starting from offset 1",
			nodes: []models.MeshNode{
				{
					UciDhcpStart: sql.NullInt64{Int64: 1, Valid: true},
					UciDhcpLimit: sql.NullInt64{Int64: 50, Valid: true},
				},
			},
			networkAddr:  "10.41.0.0",
			subnetMask:   "255.255.0.0",
			desiredLimit: 40,
			expectedMin:  51,
			expectedMax:  100, // Will use 100 as default start
			expectError:  false,
		},
		{
			name: "subnet class C",
			nodes: []models.MeshNode{
				{
					UciDhcpStart: sql.NullInt64{Int64: 10, Valid: true},
					UciDhcpLimit: sql.NullInt64{Int64: 50, Valid: true},
				},
			},
			networkAddr:  "192.168.1.0",
			subnetMask:   "255.255.255.0",
			desiredLimit: 100,
			expectedMin:  100,
			expectedMax:  100,
			expectError:  false,
		},
		{
			name:         "invalid network address",
			nodes:        []models.MeshNode{},
			networkAddr:  "invalid",
			subnetMask:   "255.255.0.0",
			desiredLimit: 100,
			expectError:  true,
		},
		{
			name:         "invalid subnet mask",
			nodes:        []models.MeshNode{},
			networkAddr:  "10.41.0.0",
			subnetMask:   "invalid",
			desiredLimit: 100,
			expectError:  true,
		},
		{
			name:         "zero desired limit",
			nodes:        []models.MeshNode{},
			networkAddr:  "10.41.0.0",
			subnetMask:   "255.255.0.0",
			desiredLimit: 0,
			expectError:  true,
		},
		{
			name:         "negative desired limit",
			nodes:        []models.MeshNode{},
			networkAddr:  "10.41.0.0",
			subnetMask:   "255.255.0.0",
			desiredLimit: -10,
			expectError:  true,
		},
		{
			name: "nodes with invalid DHCP data",
			nodes: []models.MeshNode{
				{
					UciDhcpStart: sql.NullInt64{Valid: false},
					UciDhcpLimit: sql.NullInt64{Valid: false},
				},
				{
					UciDhcpStart: sql.NullInt64{Int64: 100, Valid: true},
					UciDhcpLimit: sql.NullInt64{Int64: 50, Valid: true},
				},
			},
			networkAddr:  "10.41.0.0",
			subnetMask:   "255.255.0.0",
			desiredLimit: 50,
			expectedMin:  150,
			expectedMax:  150,
			expectError:  false,
		},
		{
			name: "nodes with invalid start (null)",
			nodes: []models.MeshNode{
				{
					UciDhcpStart: sql.NullInt64{Valid: false},
					UciDhcpLimit: sql.NullInt64{Int64: 50, Valid: true},
				},
			},
			networkAddr:  "10.41.0.0",
			subnetMask:   "255.255.0.0",
			desiredLimit: 50,
			expectedMin:  100,
			expectedMax:  100,
			expectError:  false,
		},
		{
			name: "nodes with invalid limit (null)",
			nodes: []models.MeshNode{
				{
					UciDhcpStart: sql.NullInt64{Int64: 100, Valid: true},
					UciDhcpLimit: sql.NullInt64{Valid: false},
				},
			},
			networkAddr:  "10.41.0.0",
			subnetMask:   "255.255.0.0",
			desiredLimit: 50,
			expectedMin:  100,
			expectedMax:  100,
			expectError:  false,
		},
		{
			name: "network too small for desired limit",
			nodes: []models.MeshNode{
				{
					UciDhcpStart: sql.NullInt64{Int64: 1, Valid: true},
					UciDhcpLimit: sql.NullInt64{Int64: 200, Valid: true},
				},
			},
			networkAddr:  "192.168.1.0",
			subnetMask:   "255.255.255.0",
			desiredLimit: 100,
			expectedMin:  0,
			expectedMax:  0,
			expectError:  true, // Should fail because there's not enough space
		},
		{
			name: "large subnet with spanning ranges",
			nodes: []models.MeshNode{
				{
					UciDhcpStart: sql.NullInt64{Int64: 256, Valid: true},
					UciDhcpLimit: sql.NullInt64{Int64: 512, Valid: true},
				},
			},
			networkAddr:  "10.41.0.0",
			subnetMask:   "255.255.0.0",
			desiredLimit: 200,
			expectedMin:  768, // After the existing range (256 + 512 = 768)
			expectedMax:  768,
			expectError:  false,
		},
		{
			name: "skip nodes with null start",
			nodes: []models.MeshNode{
				{
					UciDhcpStart: sql.NullInt64{Valid: false},
					UciDhcpLimit: sql.NullInt64{Int64: 150, Valid: true},
				},
				{
					UciDhcpStart: sql.NullInt64{Int64: 200, Valid: true},
					UciDhcpLimit: sql.NullInt64{Int64: 50, Valid: true},
				},
			},
			networkAddr:  "10.41.0.0",
			subnetMask:   "255.255.0.0",
			desiredLimit: 50,
			expectedMin:  100, // Should get 100 since first node is skipped
			expectedMax:  100,
			expectError:  false,
		},
		{
			name: "skip nodes with null limit",
			nodes: []models.MeshNode{
				{
					UciDhcpStart: sql.NullInt64{Int64: 100, Valid: true},
					UciDhcpLimit: sql.NullInt64{Valid: false},
				},
				{
					UciDhcpStart: sql.NullInt64{Int64: 200, Valid: true},
					UciDhcpLimit: sql.NullInt64{Int64: 50, Valid: true},
				},
			},
			networkAddr:  "10.41.0.0",
			subnetMask:   "255.255.0.0",
			desiredLimit: 50,
			expectedMin:  100, // Should get 100 since first node is skipped
			expectedMax:  100,
			expectError:  false,
		},
		{
			name: "mixed valid and invalid nodes",
			nodes: []models.MeshNode{
				{
					UciDhcpStart: sql.NullInt64{Valid: false},
					UciDhcpLimit: sql.NullInt64{Int64: 50, Valid: true},
				},
				{
					UciDhcpStart: sql.NullInt64{Int64: 100, Valid: true},
					UciDhcpLimit: sql.NullInt64{Int64: 50, Valid: true},
				},
				{
					UciDhcpStart: sql.NullInt64{Int64: 200, Valid: true},
					UciDhcpLimit: sql.NullInt64{Valid: false},
				},
			},
			networkAddr:  "10.41.0.0",
			subnetMask:   "255.255.0.0",
			desiredLimit: 40,
			expectedMin:  150, // After the confirmed range (100-149)
			expectedMax:  150,
			expectError:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, err := CalculateAvailableDHCPStart(tt.nodes, tt.networkAddr, tt.subnetMask, tt.desiredLimit)

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error, got nil")
				}

				return
			}

			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if start < tt.expectedMin || start > tt.expectedMax {
				t.Errorf("Expected start between %d and %d, got %d", tt.expectedMin, tt.expectedMax, start)
			}

			// Verify no conflicts with existing ranges
			for _, node := range tt.nodes {
				// Skip nodes with invalid DHCP data (same as the function logic)
				if !node.UciDhcpStart.Valid || !node.UciDhcpLimit.Valid {
					continue
				}

				existingStart := int(node.UciDhcpStart.Int64)
				existingLimit := int(node.UciDhcpLimit.Int64)
				existingEnd := existingStart + existingLimit - 1
				proposedEnd := start + tt.desiredLimit - 1

				if rangesOverlap(start, proposedEnd, existingStart, existingEnd) {
					t.Errorf("Calculated range [%d-%d] conflicts with existing range [%d-%d]",
						start, proposedEnd, existingStart, existingEnd)
				}
			}
		})
	}
}

func TestDHCPRangeOverlap(t *testing.T) {
	tests := []struct {
		name          string
		start1, end1  int
		start2, end2  int
		expectOverlap bool
	}{
		{
			name:          "completely separate ranges",
			start1:        100,
			end1:          149,
			start2:        200,
			end2:          249,
			expectOverlap: false,
		},
		{
			name:          "adjacent ranges no overlap",
			start1:        100,
			end1:          149,
			start2:        150,
			end2:          199,
			expectOverlap: false,
		},
		{
			name:          "partial overlap",
			start1:        100,
			end1:          150,
			start2:        140,
			end2:          180,
			expectOverlap: true,
		},
		{
			name:          "complete containment",
			start1:        100,
			end1:          200,
			start2:        120,
			end2:          150,
			expectOverlap: true,
		},
		{
			name:          "exact same range",
			start1:        100,
			end1:          150,
			start2:        100,
			end2:          150,
			expectOverlap: true,
		},
		{
			name:          "reversed order partial overlap",
			start1:        140,
			end1:          180,
			start2:        100,
			end2:          150,
			expectOverlap: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			overlap := rangesOverlap(tt.start1, tt.end1, tt.start2, tt.end2)
			if overlap != tt.expectOverlap {
				t.Errorf("Expected overlap=%v, got %v for ranges [%d-%d] and [%d-%d]",
					tt.expectOverlap, overlap, tt.start1, tt.end1, tt.start2, tt.end2)
			}

			// Test symmetry
			overlapReverse := rangesOverlap(tt.start2, tt.end2, tt.start1, tt.end1)
			if overlapReverse != tt.expectOverlap {
				t.Errorf("Overlap is not symmetric: [%d-%d] vs [%d-%d]",
					tt.start1, tt.end1, tt.start2, tt.end2)
			}
		})
	}
}

// mockUbusExecutor is a mock implementation of UbusCommandExecutor for testing.
type mockUbusExecutor struct {
	err    error
	output []byte
}

// Execute returns the pre-configured output and error.
func (m *mockUbusExecutor) Execute(_ context.Context, args ...string) ([]byte, error) {
	return m.output, m.err
}

func TestDHCPLease_GetMethods(t *testing.T) {
	lease := DHCPLease{
		Expires:  43141,
		Hostname: "TestHost",
		MacAddr:  testMACAddress,
		DUID:     "01aabbccddeeff",
		IPAddr:   "10.41.0.100",
	}

	if lease.GetExpires() != 43141 {
		t.Errorf("GetExpires() = %d, want 43141", lease.GetExpires())
	}

	if lease.GetHostname() != "TestHost" {
		t.Errorf("GetHostname() = %s, want TestHost", lease.GetHostname())
	}

	if lease.GetMacAddr() != testMACAddress {
		t.Errorf("GetMacAddr() = %s, want AA:BB:CC:DD:EE:FF", lease.GetMacAddr())
	}

	if lease.GetDUID() != "01aabbccddeeff" {
		t.Errorf("GetDUID() = %s, want 01aabbccddeeff", lease.GetDUID())
	}

	if lease.GetIPAddr() != "10.41.0.100" {
		t.Errorf("GetIPAddr() = %s, want 10.41.0.100", lease.GetIPAddr())
	}
}

func TestDHCPLeasesResponse_GetMethods(t *testing.T) {
	dhcpLeases := []DHCPLease{
		{
			Expires:  43141,
			Hostname: "Host1",
			MacAddr:  testMACAddress,
			DUID:     "01aabbccddeeff",
			IPAddr:   "10.41.0.100",
		},
		{
			Expires: 42229,
			MacAddr: "11:22:33:44:55:66",
			DUID:    "01112233445566",
			IPAddr:  "10.41.0.101",
		},
	}

	dhcp6Leases := []DHCPLease{
		{
			Expires:  50000,
			Hostname: "IPv6Host",
			MacAddr:  "77:88:99:AA:BB:CC",
			DUID:     "017788899aabbcc",
			IPAddr:   "fe80::1",
		},
	}

	response := DHCPLeasesResponse{
		DHCPLeases:  dhcpLeases,
		DHCP6Leases: dhcp6Leases,
	}

	// Test GetDHCPLeases
	gotDHCP := response.GetDHCPLeases()
	if len(gotDHCP) != 2 {
		t.Errorf("GetDHCPLeases() returned %d leases, want 2", len(gotDHCP))
	}

	// Test GetDHCP6Leases
	gotDHCP6 := response.GetDHCP6Leases()
	if len(gotDHCP6) != 1 {
		t.Errorf("GetDHCP6Leases() returned %d leases, want 1", len(gotDHCP6))
	}

	// Test GetAllLeases
	gotAll := response.GetAllLeases()
	if len(gotAll) != 3 {
		t.Errorf("GetAllLeases() returned %d leases, want 3", len(gotAll))
	}

	// Verify the order is correct (DHCP4 first, then DHCP6)
	if gotAll[0].GetIPAddr() != "10.41.0.100" {
		t.Errorf("GetAllLeases()[0] IP = %s, want 10.41.0.100", gotAll[0].GetIPAddr())
	}

	if gotAll[2].GetIPAddr() != "fe80::1" {
		t.Errorf("GetAllLeases()[2] IP = %s, want fe80::1", gotAll[2].GetIPAddr())
	}
}

func TestGetCurrentDHCPLeasesWithExecutor(t *testing.T) {
	tests := []struct {
		mockErr       error
		validateFirst func(*testing.T, DHCPLease)
		name          string
		mockOutput    string
		expectDHCP    int
		expectDHCP6   int
		expectErr     bool
	}{
		{
			name: "successful response with leases",
			mockOutput: `{
				"dhcp_leases": [
					{
						"expires": 43141,
						"hostname": "Mac",
						"macaddr": "9A:67:9D:6C:6E:92",
						"duid": "019a679d6c6e92",
						"ipaddr": "10.41.0.180"
					},
					{
						"expires": 42229,
						"macaddr": "26:D2:E5:9A:BF:55",
						"duid": "0126d2e59abf55",
						"ipaddr": "10.41.0.187"
					}
				],
				"dhcp6_leases": []
			}`,
			mockErr:     nil,
			expectErr:   false,
			expectDHCP:  2,
			expectDHCP6: 0,
			validateFirst: func(t *testing.T, lease DHCPLease) {
				if lease.GetHostname() != "Mac" {
					t.Errorf("First lease hostname = %s, want Mac", lease.GetHostname())
				}

				if lease.GetMacAddr() != "9A:67:9D:6C:6E:92" {
					t.Errorf("First lease MAC = %s, want 9A:67:9D:6C:6E:92", lease.GetMacAddr())
				}

				if lease.GetIPAddr() != "10.41.0.180" {
					t.Errorf("First lease IP = %s, want 10.41.0.180", lease.GetIPAddr())
				}

				if lease.GetExpires() != 43141 {
					t.Errorf("First lease expires = %d, want 43141", lease.GetExpires())
				}
			},
		},
		{
			name: "empty leases response",
			mockOutput: `{
				"dhcp_leases": [],
				"dhcp6_leases": []
			}`,
			mockErr:     nil,
			expectErr:   false,
			expectDHCP:  0,
			expectDHCP6: 0,
		},
		{
			name: "with IPv6 leases",
			mockOutput: `{
				"dhcp_leases": [
					{
						"expires": 1000,
						"hostname": "test",
						"macaddr": "AA:BB:CC:DD:EE:FF",
						"duid": "01aabbccddeeff",
						"ipaddr": "10.41.0.1"
					}
				],
				"dhcp6_leases": [
					{
						"expires": 2000,
						"hostname": "testv6",
						"macaddr": "11:22:33:44:55:66",
						"duid": "01112233445566",
						"ipaddr": "fe80::1"
					}
				]
			}`,
			mockErr:     nil,
			expectErr:   false,
			expectDHCP:  1,
			expectDHCP6: 1,
		},
		{
			name:        "ubus command fails",
			mockOutput:  "",
			mockErr:     errors.New("ubus: command failed"),
			expectErr:   true,
			expectDHCP:  0,
			expectDHCP6: 0,
		},
		{
			name:        "invalid JSON response",
			mockOutput:  `{"invalid": json}`,
			mockErr:     nil,
			expectErr:   true,
			expectDHCP:  0,
			expectDHCP6: 0,
		},
		{
			name:        "empty response",
			mockOutput:  "",
			mockErr:     nil,
			expectErr:   true,
			expectDHCP:  0,
			expectDHCP6: 0,
		},
		{
			name: "lease without hostname",
			mockOutput: `{
				"dhcp_leases": [
					{
						"expires": 42229,
						"macaddr": "26:D2:E5:9A:BF:55",
						"duid": "0126d2e59abf55",
						"ipaddr": "10.41.0.187"
					}
				],
				"dhcp6_leases": []
			}`,
			mockErr:     nil,
			expectErr:   false,
			expectDHCP:  1,
			expectDHCP6: 0,
			validateFirst: func(t *testing.T, lease DHCPLease) {
				if lease.GetHostname() != "" {
					t.Errorf("Lease without hostname should have empty string, got %s", lease.GetHostname())
				}

				if lease.GetMacAddr() != "26:D2:E5:9A:BF:55" {
					t.Errorf("MAC address = %s, want 26:D2:E5:9A:BF:55", lease.GetMacAddr())
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockUbusExecutor{
				output: []byte(tt.mockOutput),
				err:    tt.mockErr,
			}

			response, err := GetCurrentDHCPLeasesWithExecutor(context.Background(), mock)

			if tt.expectErr {
				if err == nil {
					t.Error("Expected error but got none")
				}

				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)

				return
			}

			if response == nil {
				t.Fatal("Response is nil")
			}

			if len(response.DHCPLeases) != tt.expectDHCP {
				t.Errorf("DHCPLeases count = %d, want %d", len(response.DHCPLeases), tt.expectDHCP)
			}

			if len(response.DHCP6Leases) != tt.expectDHCP6 {
				t.Errorf("DHCP6Leases count = %d, want %d", len(response.DHCP6Leases), tt.expectDHCP6)
			}

			if tt.validateFirst != nil && len(response.DHCPLeases) > 0 {
				tt.validateFirst(t, response.DHCPLeases[0])
			}
		})
	}
}

func TestDefaultUbusExecutor_Execute(t *testing.T) {
	// This test verifies that DefaultUbusExecutor implements UbusCommandExecutor interface.
	// We can't actually run ubus in the test environment.
	var _ UbusCommandExecutor = &DefaultUbusExecutor{}
	// The actual execution is tested in integration tests
}

// ── Static Host Tests ────────────────────────────────────────────────────────

func TestGetStaticHostsWithReader_ReturnsHosts(t *testing.T) {
	reader := newMockDHCPConfigReader()
	reader.sections["dhcp"] = map[string]string{
		"printer": "host",
		"camera":  "host",
	}

	reader.data["dhcp"] = map[string]map[string][]string{
		"printer": {
			"name": {"printer-office"},
			"mac":  {"AA:BB:CC:11:22:33"},
			"ip":   {"10.41.0.10"},
		},
		"camera": {
			"name": {"camera-north"},
			"mac":  {"AA:BB:CC:44:55:66"},
			"ip":   {"10.41.0.11"},
		},
	}

	hosts, err := GetStaticHostsWithReader(reader)
	require.NoError(t, err)
	assert.Len(t, hosts, 2)

	// Build a map for order-independent assertion
	byName := make(map[string]UCIStaticHost)
	for _, h := range hosts {
		byName[h.Name] = h
	}

	assert.Equal(t, "printer-office", byName["printer-office"].Name)
	assert.Equal(t, "AA:BB:CC:11:22:33", byName["printer-office"].MAC)
	assert.Equal(t, "10.41.0.10", byName["printer-office"].IP)

	assert.Equal(t, "camera-north", byName["camera-north"].Name)
	assert.Equal(t, "AA:BB:CC:44:55:66", byName["camera-north"].MAC)
	assert.Equal(t, "10.41.0.11", byName["camera-north"].IP)
}

func TestGetStaticHostsWithReader_Empty(t *testing.T) {
	reader := newMockDHCPConfigReader()

	hosts, err := GetStaticHostsWithReader(reader)
	require.NoError(t, err)
	assert.Empty(t, hosts)
}

func TestGetStaticHostsWithReader_PartialFields(t *testing.T) {
	reader := newMockDHCPConfigReader()
	reader.sections["dhcp"] = map[string]string{
		"noname": "host",
	}

	reader.data["dhcp"] = map[string]map[string][]string{
		"noname": {
			"mac": {"AA:BB:CC:DD:EE:FF"},
			"ip":  {"10.41.0.50"},
		},
	}

	hosts, err := GetStaticHostsWithReader(reader)
	require.NoError(t, err)
	assert.Len(t, hosts, 1)
	assert.Equal(t, "", hosts[0].Name)
	assert.Equal(t, "AA:BB:CC:DD:EE:FF", hosts[0].MAC)
	assert.Equal(t, "10.41.0.50", hosts[0].IP)
}

func TestGetStaticHostsWithReader_GetSectionsError(t *testing.T) {
	reader := &mockDHCPConfigReaderWithErrors{}

	_, err := GetStaticHostsWithReader(reader)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "enumerate host sections")
}

// ── DHCP Range Computation Tests ─────────────────────────────────────────────

func TestComputeDHCPRangeStart(t *testing.T) {
	ip, err := ComputeDHCPRangeStart("10.41.0.0", 100)
	require.NoError(t, err)
	assert.Equal(t, "10.41.0.100", ip)
}

func TestComputeDHCPRangeEnd(t *testing.T) {
	ip, err := ComputeDHCPRangeEnd("10.41.0.0", 100, 155)
	require.NoError(t, err)
	assert.Equal(t, "10.41.0.254", ip)
}

func TestComputeDHCPRangeEnd_CrossesOctet(t *testing.T) {
	ip, err := ComputeDHCPRangeEnd("10.41.0.0", 200, 100)
	require.NoError(t, err)
	assert.Equal(t, "10.41.1.43", ip)
}

func TestComputeDHCPRangeStart_InvalidIP(t *testing.T) {
	_, err := ComputeDHCPRangeStart("invalid", 100)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid base IP")
}

func TestComputeDHCPRangeEnd_InvalidIP(t *testing.T) {
	_, err := ComputeDHCPRangeEnd("invalid", 100, 155)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid base IP")
}

// ── Setup wizard helpers ─────────────────────────────────────────────────────

// newDhcpResetMock builds a mockConfigReader pre-populated with a
// dnsmasq + lan + wan layout matching the captured `before/dhcp`
// fixture (minus comments and stray formatting).
func newDhcpResetMock(t *testing.T) *mockConfigReader {
	t.Helper()

	m := &mockConfigReader{
		data:         map[string]map[string]map[string][]string{},
		sectionTypes: map[string]map[string]string{},
		anonSections: map[string][]string{},
	}

	// Anonymous dnsmasq with all the OpenWrt default options.
	require.NoError(t, m.AddSection("dhcp", "", "dnsmasq"))

	for _, kv := range []struct{ k, v string }{
		{"domainneeded", "1"},
		{"boguspriv", "1"},
		{"localize_queries", "1"},
		{"rebind_protection", "1"},
		{"rebind_localhost", "1"},
		{"local", "/lan/"},
		{"domain", "lan"},
		{"expandhosts", "1"},
		{"cachesize", "1000"},
		{"authoritative", "1"},
		{"readethers", "1"},
		{"localservice", "1"},
		{"ednspacket_max", "1232"},
	} {
		require.NoError(t, m.SetType("dhcp", "@dnsmasq[0]", kv.k, uci.TypeOption, kv.v))
	}

	// LAN dhcp pool.
	require.NoError(t, m.AddSection("dhcp", "lan", "dhcp"))
	require.NoError(t, m.SetType("dhcp", "lan", "interface", uci.TypeOption, "lan"))
	require.NoError(t, m.SetType("dhcp", "lan", "start", uci.TypeOption, "100"))
	require.NoError(t, m.SetType("dhcp", "lan", "limit", uci.TypeOption, "150"))
	require.NoError(t, m.SetType("dhcp", "lan", "leasetime", uci.TypeOption, "12h"))

	// WAN dhcp pool.
	require.NoError(t, m.AddSection("dhcp", "wan", "dhcp"))
	require.NoError(t, m.SetType("dhcp", "wan", "interface", uci.TypeOption, "wan"))
	require.NoError(t, m.SetType("dhcp", "wan", "ignore", uci.TypeOption, "1"))

	m.commitCount = 0
	m.commitCalled = false

	return m
}

func TestWhitelistDnsmasqSurvivor_StripsExtraOptions(t *testing.T) {
	m := newDhcpResetMock(t)

	require.NoError(t, WhitelistDnsmasqSurvivor(m, "@dnsmasq[0]", WizardDnsmasqWhitelist))

	// Whitelisted survives.
	for _, opt := range WizardDnsmasqWhitelist {
		_, ok := m.Get("dhcp", "@dnsmasq[0]", opt)
		assert.Truef(t, ok, "whitelisted option %q removed", opt)
	}

	// Non-whitelisted removed (boguspriv, rebind_protection are in the
	// allDnsmasqOptions but not in the wizard whitelist).
	for _, opt := range []string{"boguspriv", "rebind_protection"} {
		_, ok := m.Get("dhcp", "@dnsmasq[0]", opt)
		assert.Falsef(t, ok, "non-whitelisted option %q not removed", opt)
	}
}

func TestWhitelistDnsmasqSurvivor_RejectsEmptyName(t *testing.T) {
	m := newDhcpResetMock(t)
	err := WhitelistDnsmasqSurvivor(m, "", WizardDnsmasqWhitelist)
	require.Error(t, err)
}

func TestWhitelistAndIgnoreAllPools_DisablesAndStrips(t *testing.T) {
	m := newDhcpResetMock(t)

	require.NoError(t, WhitelistAndIgnoreAllPools(m))

	// Both pools now have ignore=1.
	for _, name := range []string{"lan", "wan"} {
		v, ok := m.Get("dhcp", name, "ignore")
		require.Truef(t, ok, "%s missing ignore", name)
		assert.Equalf(t, "1", v[0], "%s ignore", name)
	}

	// Whitelisted pool fields preserved on lan.
	for _, opt := range []string{"start", "leasetime", "limit", "interface"} {
		_, ok := m.Get("dhcp", "lan", opt)
		assert.Truef(t, ok, "whitelisted lan option %q removed", opt)
	}

	// Non-whitelisted options removed if they exist (none do in this
	// mock — the before fixture has no `dns_service`/`force` etc. on
	// lan/wan, so this test is mostly verifying no false positives).
}

func TestCreateDhcpPool_WritesStandardFields(t *testing.T) {
	m := newDhcpResetMock(t)

	rng := newSeededRand(t, 42)
	name, err := CreateDhcpPool(m, "ahwlan", nil, rng)
	require.NoError(t, err)
	assert.Equal(t, "ahwlan", name)

	expect := map[string]string{
		"limit":     DefaultDhcpPoolLimit,
		"leasetime": DefaultDhcpPoolLeasetime,
		"force":     "1",
		"interface": "ahwlan",
	}

	for k, want := range expect {
		v, ok := m.Get("dhcp", "ahwlan", k)
		require.Truef(t, ok, "missing %s", k)
		assert.Equalf(t, want, v[0], "key %s", k)
	}

	// `start` is a randomized offset.
	v, ok := m.Get("dhcp", "ahwlan", "start")
	require.True(t, ok)

	startVal := v[0]
	assert.NotEmpty(t, startVal)

	// Fixture-shape: no dnsmasq instance binding and none of the
	// options the old wizard used to write (a second dnsmasq
	// instance races the global one for port 53).
	for _, gone := range []string{"instance", "ra", "ra_slaac", "ra_flags", "dns", "dns_service", "ignore"} {
		_, ok := m.Get("dhcp", "ahwlan", gone)
		assert.Falsef(t, ok, "option %q must not be written", gone)
	}
}

func TestCreateDhcpPool_WritesExtraOptions(t *testing.T) {
	m := newDhcpResetMock(t)
	rng := newSeededRand(t, 0)

	name, err := CreateDhcpPool(m, "lan", map[string][]string{"dhcp_option": {"3", "6"}}, rng)
	require.NoError(t, err)

	v, ok := m.Get("dhcp", name, "dhcp_option")
	require.True(t, ok)
	assert.Equal(t, []string{"3", "6"}, v)
}

func TestCreateDhcpPool_ResolvesNameClashByAppendingSuffix(t *testing.T) {
	m := newDhcpResetMock(t)
	// `ahwlan` already exists as a dhcp section name.
	require.NoError(t, m.AddSection("dhcp", "ahwlan", "dhcp"))

	rng := newSeededRand(t, 0)
	name, err := CreateDhcpPool(m, "ahwlan", nil, rng)
	require.NoError(t, err)
	assert.NotEqual(t, "ahwlan", name)
}

func TestCreateDhcpPool_RejectsEmptyArgs(t *testing.T) {
	m := newDhcpResetMock(t)
	rng := newSeededRand(t, 0)

	_, err := CreateDhcpPool(m, "", nil, rng)
	require.Error(t, err)

	_, err = CreateDhcpPool(m, "ahwlan", nil, nil)
	require.Error(t, err)
}

func TestGetOrCreateDhcpPool_ReturnsEnabledMatch(t *testing.T) {
	m := newDhcpResetMock(t)
	// Pre-existing enabled pool for ahwlan.
	require.NoError(t, m.AddSection("dhcp", "ahwlan", "dhcp"))
	require.NoError(t, m.SetType("dhcp", "ahwlan", "interface", uci.TypeOption, "ahwlan"))

	rng := newSeededRand(t, 0)
	got, err := GetOrCreateDhcpPool(m, "ahwlan", nil, rng)
	require.NoError(t, err)
	assert.Equal(t, "ahwlan", got)
}

// TestGetOrCreateDhcpPool_EnabledMatchBackfillsOptions pins that the
// enabled-match path (an already-enabled pool matching networkID) is
// NOT a bare pass-through: it must also run backfillPoolOptions, not
// rely on the caller having already run the wizard's reset phase
// (which sets ignore=1 before this function ever sees the pool in
// production). Without this, a pool that reaches GetOrCreateDhcpPool
// already enabled — by construction, or via a future caller that
// doesn't go through the reset phase — would keep whatever
// interface/force/extraOptions it happened to have, silently
// skipping the wizard's option set.
func TestGetOrCreateDhcpPool_EnabledMatchBackfillsOptions(t *testing.T) {
	m := newDhcpResetMock(t)

	// Enabled pool (no `ignore`) matching networkID, but missing
	// every field the wizard is supposed to guarantee.
	require.NoError(t, m.AddSection("dhcp", "ahwlan", "dhcp"))
	require.NoError(t, m.SetType("dhcp", "ahwlan", "interface", uci.TypeOption, "ahwlan"))

	rng := newSeededRand(t, 0)
	got, err := GetOrCreateDhcpPool(m, "ahwlan", map[string][]string{"dhcp_option": {"3", "6"}}, rng)
	require.NoError(t, err)
	assert.Equal(t, "ahwlan", got)

	force, ok := m.Get("dhcp", "ahwlan", "force")
	require.True(t, ok, "force must be backfilled on the enabled-match path")
	assert.Equal(t, "1", force[0])

	limit, ok := m.Get("dhcp", "ahwlan", "limit")
	require.True(t, ok, "limit must be backfilled on the enabled-match path")
	assert.Equal(t, DefaultDhcpPoolLimit, limit[0])

	lease, ok := m.Get("dhcp", "ahwlan", "leasetime")
	require.True(t, ok, "leasetime must be backfilled on the enabled-match path")
	assert.Equal(t, DefaultDhcpPoolLeasetime, lease[0])

	start, ok := m.Get("dhcp", "ahwlan", "start")
	require.True(t, ok, "start must be backfilled on the enabled-match path")
	assert.NotEmpty(t, start[0])

	dhcpOpt, ok := m.Get("dhcp", "ahwlan", "dhcp_option")
	require.True(t, ok, "extraOptions must be backfilled on the enabled-match path")
	assert.Equal(t, []string{"3", "6"}, dhcpOpt)
}

func TestGetOrCreateDhcpPool_ReenablesDisabledMatch(t *testing.T) {
	m := newDhcpResetMock(t)

	require.NoError(t, m.AddSection("dhcp", "ahwlan", "dhcp"))
	require.NoError(t, m.SetType("dhcp", "ahwlan", "interface", uci.TypeOption, "ahwlan"))
	require.NoError(t, m.SetType("dhcp", "ahwlan", "ignore", uci.TypeOption, "1"))

	rng := newSeededRand(t, 0)
	got, err := GetOrCreateDhcpPool(m, "ahwlan", nil, rng)
	require.NoError(t, err)
	assert.Equal(t, "ahwlan", got)

	_, ok := m.Get("dhcp", "ahwlan", "ignore")
	assert.False(t, ok, "ignore should have been cleared on re-enable")
}

func TestGetOrCreateDhcpPool_CreatesNewWhenAbsent(t *testing.T) {
	m := newDhcpResetMock(t)
	rng := newSeededRand(t, 0)

	got, err := GetOrCreateDhcpPool(m, "ahwlan", nil, rng)
	require.NoError(t, err)
	assert.Equal(t, "ahwlan", got)

	// Standard fields populated.
	v, ok := m.Get("dhcp", "ahwlan", "interface")
	require.True(t, ok)
	assert.Equal(t, "ahwlan", v[0])
}

// TestGetOrCreateDhcpPool_ReenableBackfillsForce pins the idempotence
// fix: the reset phase's WhitelistAndIgnoreAllPools strips `force`
// (it isn't in WizardDhcpPoolWhitelist) before setting ignore=1. The
// old re-enable path only cleared `ignore`, so a second wizard run
// silently dropped `force` from an already-provisioned pool. start/
// limit/leasetime (whitelisted, so they survive reset) must be kept
// unchanged rather than reshuffled.
func TestGetOrCreateDhcpPool_ReenableBackfillsForce(t *testing.T) {
	m := newDhcpResetMock(t)

	require.NoError(t, m.AddSection("dhcp", "ahwlan", "dhcp"))
	require.NoError(t, m.SetType("dhcp", "ahwlan", "interface", uci.TypeOption, "ahwlan"))
	require.NoError(t, m.SetType("dhcp", "ahwlan", "start", uci.TypeOption, "271"))
	require.NoError(t, m.SetType("dhcp", "ahwlan", "limit", uci.TypeOption, "16"))
	require.NoError(t, m.SetType("dhcp", "ahwlan", "leasetime", uci.TypeOption, "12h"))
	require.NoError(t, m.SetType("dhcp", "ahwlan", "ignore", uci.TypeOption, "1"))
	// `force` absent — this is what WhitelistAndIgnoreAllPools leaves
	// behind since force isn't whitelisted.

	rng := newSeededRand(t, 0)
	got, err := GetOrCreateDhcpPool(m, "ahwlan", nil, rng)
	require.NoError(t, err)
	assert.Equal(t, "ahwlan", got)

	v, ok := m.Get("dhcp", "ahwlan", "force")
	require.True(t, ok, "force must be backfilled on re-enable")
	assert.Equal(t, "1", v[0])

	// start/limit/leasetime kept unchanged, not reshuffled.
	start, ok := m.Get("dhcp", "ahwlan", "start")
	require.True(t, ok)
	assert.Equal(t, "271", start[0])

	limit, ok := m.Get("dhcp", "ahwlan", "limit")
	require.True(t, ok)
	assert.Equal(t, "16", limit[0])

	lease, ok := m.Get("dhcp", "ahwlan", "leasetime")
	require.True(t, ok)
	assert.Equal(t, "12h", lease[0])
}

// TestGetOrCreateDhcpPool_ReenableScrubsPriorFirmwareOptions pins the
// reset-phase interaction: a pool carried over from a prior firmware
// (or a pre-fix wizard run) may still have `instance`/`ra` from
// before this fix landed. allDhcpPoolOptions already covers both
// (neither is in WizardDhcpPoolWhitelist) so WhitelistAndIgnoreAllPools
// strips them at reset; the re-enable path must not reintroduce them.
func TestGetOrCreateDhcpPool_ReenableScrubsPriorFirmwareOptions(t *testing.T) {
	m := newDhcpResetMock(t)

	require.NoError(t, m.AddSection("dhcp", "ahwlan", "dhcp"))
	require.NoError(t, m.SetType("dhcp", "ahwlan", "interface", uci.TypeOption, "ahwlan"))
	require.NoError(t, m.SetType("dhcp", "ahwlan", "instance", uci.TypeOption, "ahwlan_dns"))
	require.NoError(t, m.SetType("dhcp", "ahwlan", "ra", uci.TypeOption, "server"))

	require.NoError(t, WhitelistAndIgnoreAllPools(m))

	rng := newSeededRand(t, 0)
	got, err := GetOrCreateDhcpPool(m, "ahwlan", nil, rng)
	require.NoError(t, err)
	assert.Equal(t, "ahwlan", got)

	_, ok := m.Get("dhcp", "ahwlan", "instance")
	assert.False(t, ok, "instance from a prior-firmware pool must be scrubbed, not reintroduced")

	_, ok = m.Get("dhcp", "ahwlan", "ra")
	assert.False(t, ok, "ra from a prior-firmware pool must be scrubbed, not reintroduced")
}

// TestGetOrCreateDhcpPool_ReenableRewritesExtraOptions pins that
// extraOptions (e.g. mesh-point-none's dhcp_option=[3,6]) are
// restored on re-enable even though dhcp_option is in the reset
// phase's option universe and gets stripped like any other
// non-whitelisted field.
func TestGetOrCreateDhcpPool_ReenableRewritesExtraOptions(t *testing.T) {
	m := newDhcpResetMock(t)

	require.NoError(t, m.AddSection("dhcp", "ahwlan", "dhcp"))
	require.NoError(t, m.SetType("dhcp", "ahwlan", "interface", uci.TypeOption, "ahwlan"))
	require.NoError(t, m.SetType("dhcp", "ahwlan", "ignore", uci.TypeOption, "1"))

	rng := newSeededRand(t, 0)
	got, err := GetOrCreateDhcpPool(m, "ahwlan", map[string][]string{"dhcp_option": {"3", "6"}}, rng)
	require.NoError(t, err)
	assert.Equal(t, "ahwlan", got)

	v, ok := m.Get("dhcp", "ahwlan", "dhcp_option")
	require.True(t, ok)
	assert.Equal(t, []string{"3", "6"}, v)
}
