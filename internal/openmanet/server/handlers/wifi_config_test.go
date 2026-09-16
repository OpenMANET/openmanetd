package handlers_test

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/digineo/go-uci/v2"
	"github.com/mdlayher/wifi"
	wificonfigv1 "github.com/openmanet/openmanetd/internal/api/openmanet/wifi_config/v1"
	batmanadv "github.com/openmanet/openmanetd/internal/batman-adv"
	"github.com/openmanet/openmanetd/internal/iwinfo"
	"github.com/openmanet/openmanetd/internal/network"
	"github.com/openmanet/openmanetd/internal/openmanet/server/handlers"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
)

// ── fake implementations ─────────────────────────────────────────────────────

type fakeWirelessStatusProvider struct {
	status map[string]*network.WirelessRadioStatus
	err    error
}

func (f *fakeWirelessStatusProvider) GetWirelessStatus(_ context.Context) (map[string]*network.WirelessRadioStatus, error) {
	return f.status, f.err
}

type fakeIwinfoProvider struct {
	infoByDevice map[string]*iwinfo.InterfaceInfo
	infoErr      error
	devices      []string
	devicesErr   error
}

func (f *fakeIwinfoProvider) GetDevices(_ context.Context) ([]string, error) {
	return f.devices, f.devicesErr
}

func (f *fakeIwinfoProvider) GetInfo(_ context.Context, device string) (*iwinfo.InterfaceInfo, error) {
	if f.infoErr != nil {
		return nil, f.infoErr
	}

	info, ok := f.infoByDevice[device]
	if !ok {
		return nil, errors.New("device not found")
	}

	return info, nil
}

func (f *fakeIwinfoProvider) GetInfoForAll(_ context.Context) (map[string]*iwinfo.InterfaceInfo, error) {
	return f.infoByDevice, f.infoErr
}

type fakeConfigReader struct {
	data         map[string]map[string]map[string][]string
	sectionTypes map[string]map[string]string
	commitCalled bool
	reloadCalled bool
	reloadCalls  int
	commitError  error
	reloadError  error
	setTypeError error
}

func (f *fakeConfigReader) Get(config, section, option string) ([]string, bool) {
	if configData, ok := f.data[config]; ok {
		if sectionData, ok := configData[section]; ok {
			if values, ok := sectionData[option]; ok {
				return values, true
			}
		}
	}

	return nil, false
}

func (f *fakeConfigReader) GetSections(config, secType string) ([]string, error) {
	var sections []string

	if typeMap, ok := f.sectionTypes[config]; ok {
		for section, stype := range typeMap {
			if stype == secType {
				sections = append(sections, section)
			}
		}
	}

	return sections, nil
}

func (f *fakeConfigReader) SetType(config, section, option string, _ uci.OptionType, values ...string) error {
	if f.setTypeError != nil {
		return f.setTypeError
	}

	if f.data[config] == nil {
		f.data[config] = make(map[string]map[string][]string)
	}

	if f.data[config][section] == nil {
		f.data[config][section] = make(map[string][]string)
	}

	f.data[config][section][option] = values

	return nil
}

func (f *fakeConfigReader) Del(config, section, option string) error {
	if f.data[config] == nil {
		return nil
	}

	if f.data[config][section] == nil {
		return nil
	}

	delete(f.data[config][section], option)

	return nil
}

func (f *fakeConfigReader) AddSection(config, section, typ string) error {
	if f.sectionTypes == nil {
		f.sectionTypes = make(map[string]map[string]string)
	}

	if f.sectionTypes[config] == nil {
		f.sectionTypes[config] = make(map[string]string)
	}

	f.sectionTypes[config][section] = typ

	if f.data[config] == nil {
		f.data[config] = make(map[string]map[string][]string)
	}

	if f.data[config][section] == nil {
		f.data[config][section] = make(map[string][]string)
	}

	return nil
}

func (f *fakeConfigReader) DelSection(config, section string) error {
	if f.sectionTypes[config] != nil {
		delete(f.sectionTypes[config], section)
	}

	if f.data[config] != nil {
		delete(f.data[config], section)
	}

	return nil
}

func (f *fakeConfigReader) Commit() error {
	f.commitCalled = true

	return f.commitError
}

func (f *fakeConfigReader) ReloadConfig() error {
	f.reloadCalled = true
	f.reloadCalls++

	return f.reloadError
}

type fakeDHCPLeaseProvider struct {
	leases *network.DHCPLeasesResponse
	err    error
}

func (f *fakeDHCPLeaseProvider) GetCurrentDHCPLeases(_ context.Context) (*network.DHCPLeasesResponse, error) {
	return f.leases, f.err
}

// ── helper constructors ──────────────────────────────────────────────────────

func newTestWifiConfigService(t *testing.T) *handlers.WifiConfigService {
	t.Helper()

	return &handlers.WifiConfigService{
		Log:            zerolog.Nop(),
		IwinfoClient:   &fakeIwinfoProvider{infoByDevice: map[string]*iwinfo.InterfaceInfo{}},
		Wifi:           &fakeWireless{},
		WirelessStatus: &fakeWirelessStatusProvider{status: map[string]*network.WirelessRadioStatus{}},
		ConfigReader:   newWifiConfigMockReader(),
		DHCPLeases:     &fakeDHCPLeaseProvider{leases: &network.DHCPLeasesResponse{}},
		ReloadServices: func(context.Context) error { return nil },
		GetMeshNeighbors: func() (*batmanadv.Neighbors, error) {
			return &batmanadv.Neighbors{}, nil
		},
		ParseBatHosts: func(_ string) (*batmanadv.BatHosts, error) {
			return &batmanadv.BatHosts{}, nil
		},
	}
}

func newWifiConfigMockReader() *fakeConfigReader {
	return &fakeConfigReader{
		data: map[string]map[string]map[string][]string{
			"wireless": {
				"radio2": {
					"type":    {"mac80211"},
					"band":    {"2g"},
					"channel": {"1"},
					"htmode":  {"HT20"},
					"country": {"US"},
					"txpower": {"20"},
					"path":    {"platform/soc/fe980000.usb/usb1/1-1"},
				},
				"radio3": {
					"type":    {"morse"},
					"band":    {"s1g"},
					"channel": {"42"},
					"hwmode":  {"11ah"},
					"country": {"US"},
					"txpower": {"14"},
					"path":    {"platform/soc/fe204000.spi"},
				},
				"default_radio2": {
					"device":     {"radio2"},
					"network":    {"ahwlan"},
					"mode":       {"ap"},
					"ssid":       {"openmanet"},
					"key":        {"testsecret"},
					"encryption": {"psk2"},
					"disabled":   {"0"},
				},
				"default_radio3": {
					"device":     {"radio3"},
					"network":    {"batmesh0"},
					"mode":       {"mesh"},
					"ssid":       {"openmanet"},
					"key":        {"meshsecret"},
					"mesh_id":    {"openmanet-mesh"},
					"encryption": {"sae"},
				},
			},
			// A wizard-configured node: bat0 plus the primary hardif.
			// batmesh1 is deliberately absent so mesh-mode tests can
			// prove the handler creates it.
			"network": {
				"bat0":     {"proto": {"batadv"}},
				"batmesh0": {"proto": {"batadv_hardif"}, "master": {"bat0"}},
			},
		},
		sectionTypes: map[string]map[string]string{
			"wireless": {
				"radio2":         "wifi-device",
				"radio3":         "wifi-device",
				"default_radio2": "wifi-iface",
				"default_radio3": "wifi-iface",
			},
			"network": {
				"bat0":     "interface",
				"batmesh0": "interface",
			},
		},
	}
}

// fakeRadioReloader stands in for WifiConfigService.ReloadServices.
type fakeRadioReloader struct {
	mu       sync.Mutex
	calls    int
	err      error
	lastLive bool // ctx.Err() == nil when invoked
}

func (f *fakeRadioReloader) Reload(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls++
	f.lastLive = ctx.Err() == nil

	return f.err
}

func (f *fakeRadioReloader) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.calls
}

func newTestWirelessStatus() map[string]*network.WirelessRadioStatus {
	return map[string]*network.WirelessRadioStatus{
		"radio2": {
			Up:       true,
			Disabled: false,
			Interfaces: []network.WirelessRadioInterface{
				{Section: "default_radio2", Ifname: "phy0-ap0", Config: network.WirelessIfaceStatusConfig{Mode: "ap"}},
			},
		},
		"radio3": {
			Up:       true,
			Disabled: false,
			Interfaces: []network.WirelessRadioInterface{
				{Section: "default_radio3", Ifname: "phy1-mesh0", Config: network.WirelessIfaceStatusConfig{Mode: "mesh"}},
			},
		},
	}
}

func newTestIwinfoData() map[string]*iwinfo.InterfaceInfo {
	return map[string]*iwinfo.InterfaceInfo{
		"phy0-ap0": {
			SSID:      "openmanet",
			Mode:      "Master",
			Channel:   1,
			Frequency: 2412,
			HTMode:    "HT20",
			TxPower:   20,
			Hardware:  iwinfo.HardwareInfo{Name: "MediaTek MT7603E"},
			Encryption: iwinfo.EncryptionInfo{
				Enabled:        true,
				WPA:            []int{2},
				Authentication: []string{"psk"},
				Ciphers:        []string{"ccmp"},
			},
		},
		"phy1-mesh0": {
			SSID:      "openmanet",
			Mode:      "Mesh Point",
			Channel:   42,
			Frequency: 923,
			HTMode:    "",
			TxPower:   14,
			Hardware:  iwinfo.HardwareInfo{Name: "Morse Micro MM8108"},
			Encryption: iwinfo.EncryptionInfo{
				Enabled:        true,
				WPA:            []int{3},
				Authentication: []string{"sae"},
				Ciphers:        []string{"ccmp"},
			},
		},
	}
}

// ── ListRadios tests ─────────────────────────────────────────────────────────

func TestListRadios_MultipleRadios(t *testing.T) {
	svc := newTestWifiConfigService(t)
	svc.WirelessStatus = &fakeWirelessStatusProvider{status: newTestWirelessStatus()}
	svc.IwinfoClient = &fakeIwinfoProvider{infoByDevice: newTestIwinfoData()}

	resp, err := svc.ListRadios(context.Background(), &emptypb.Empty{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(resp.GetRadios()) != 2 {
		t.Fatalf("expected 2 radios, got %d", len(resp.GetRadios()))
	}

	radioMap := map[string]*wificonfigv1.Radio{}
	for _, r := range resp.GetRadios() {
		radioMap[r.GetName()] = r
	}

	r2 := radioMap["radio2"]
	if r2 == nil {
		t.Fatal("expected radio2 in response")
	}

	if r2.GetBand() != wificonfigv1.WifiBand_WIFI_BAND_2G {
		t.Errorf("radio2 band: got %v, want %v", r2.GetBand(), wificonfigv1.WifiBand_WIFI_BAND_2G)
	}

	if r2.GetHardwareName() != "MediaTek MT7603E" {
		t.Errorf("radio2 hardware: got %q, want %q", r2.GetHardwareName(), "MediaTek MT7603E")
	}

	if r2.GetInterfaceName() != "default_radio2" {
		t.Errorf("radio2 interface_name: got %q, want %q", r2.GetInterfaceName(), "default_radio2")
	}

	r3 := radioMap["radio3"]
	if r3 == nil {
		t.Fatal("expected radio3 in response")
	}

	if r3.GetBand() != wificonfigv1.WifiBand_WIFI_BAND_S1G {
		t.Errorf("radio3 band: got %v, want %v", r3.GetBand(), wificonfigv1.WifiBand_WIFI_BAND_S1G)
	}

	if r3.GetHardwareName() != "Morse Micro MM8108" {
		t.Errorf("radio3 hardware: got %q, want %q", r3.GetHardwareName(), "Morse Micro MM8108")
	}
}

func TestListRadios_Empty(t *testing.T) {
	svc := newTestWifiConfigService(t)
	svc.ConfigReader = &fakeConfigReader{
		data:         map[string]map[string]map[string][]string{},
		sectionTypes: map[string]map[string]string{},
	}

	resp, err := svc.ListRadios(context.Background(), &emptypb.Empty{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(resp.GetRadios()) != 0 {
		t.Errorf("expected 0 radios, got %d", len(resp.GetRadios()))
	}
}

func TestListRadios_DisplayNameFormatting(t *testing.T) {
	svc := newTestWifiConfigService(t)
	svc.WirelessStatus = &fakeWirelessStatusProvider{status: newTestWirelessStatus()}
	svc.IwinfoClient = &fakeIwinfoProvider{infoByDevice: newTestIwinfoData()}

	resp, err := svc.ListRadios(context.Background(), &emptypb.Empty{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	radioMap := map[string]*wificonfigv1.Radio{}
	for _, r := range resp.GetRadios() {
		radioMap[r.GetName()] = r
	}

	r2 := radioMap["radio2"]
	if r2.GetDisplayName() != "2.4 GHz Radio (MediaTek MT7603E)" {
		t.Errorf("radio2 display_name: got %q, want %q", r2.GetDisplayName(), "2.4 GHz Radio (MediaTek MT7603E)")
	}

	r3 := radioMap["radio3"]
	if r3.GetDisplayName() != "HaLow Radio (Morse Micro MM8108)" {
		t.Errorf("radio3 display_name: got %q, want %q", r3.GetDisplayName(), "HaLow Radio (Morse Micro MM8108)")
	}
}

func TestListRadios_IwinfoFailure(t *testing.T) {
	svc := newTestWifiConfigService(t)
	svc.IwinfoClient = &fakeIwinfoProvider{infoErr: errors.New("ubus timeout")}
	svc.WirelessStatus = &fakeWirelessStatusProvider{status: newTestWirelessStatus()}

	resp, err := svc.ListRadios(context.Background(), &emptypb.Empty{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should still return radios, just without hardware names.
	if len(resp.GetRadios()) != 2 {
		t.Fatalf("expected 2 radios despite iwinfo failure, got %d", len(resp.GetRadios()))
	}

	for _, r := range resp.GetRadios() {
		if r.GetHardwareName() != "" {
			t.Errorf("expected empty hardware_name on iwinfo failure, got %q", r.GetHardwareName())
		}
	}
}

// ── GetRadioStatus tests ─────────────────────────────────────────────────────

func TestGetRadioStatus_APMode(t *testing.T) {
	mac1, _ := net.ParseMAC("D4:6D:6D:1A:2B:3C")
	mac2, _ := net.ParseMAC("F0:18:98:4D:5E:6F")

	svc := newTestWifiConfigService(t)
	svc.WirelessStatus = &fakeWirelessStatusProvider{status: newTestWirelessStatus()}
	svc.IwinfoClient = &fakeIwinfoProvider{infoByDevice: newTestIwinfoData()}
	svc.Wifi = &fakeWireless{
		interfaces: []*wifi.Interface{
			{Index: 1, Name: "phy0-ap0", HardwareAddr: mac1, Type: wifi.InterfaceTypeAPVLAN, Frequency: 2412, ChannelWidth: 20},
		},
		stationInfoByIface: map[string][]*wifi.StationInfo{
			"phy0-ap0": {
				{HardwareAddr: mac1, Signal: -52, TransmitBitrate: 65000000, ReceiveBitrate: 72000000, Connected: 3*time.Hour + 42*time.Minute},
				{HardwareAddr: mac2, Signal: -68, TransmitBitrate: 48000000, ReceiveBitrate: 54000000, Connected: 1*time.Hour + 15*time.Minute},
			},
		},
	}

	resp, err := svc.GetRadioStatus(context.Background(), &wificonfigv1.GetRadioStatusRequest{RadioName: "radio2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	status := resp.GetStatus()
	if !status.GetActive() {
		t.Error("expected active status")
	}

	if status.GetSsid() != "openmanet" {
		t.Errorf("ssid: got %q, want %q", status.GetSsid(), "openmanet")
	}

	if status.GetMode() != "Access Point" {
		t.Errorf("mode: got %q, want %q", status.GetMode(), "Access Point")
	}

	if status.GetChannel() != 1 {
		t.Errorf("channel: got %d, want %d", status.GetChannel(), 1)
	}

	if status.GetFrequency() != 2412 {
		t.Errorf("frequency: got %d, want %d", status.GetFrequency(), 2412)
	}

	if status.GetBandwidth() != "20 MHz" {
		t.Errorf("bandwidth: got %q, want %q", status.GetBandwidth(), "20 MHz")
	}

	if status.GetEncryption() != "WPA2-PSK (CCMP)" {
		t.Errorf("encryption: got %q, want %q", status.GetEncryption(), "WPA2-PSK (CCMP)")
	}

	if status.GetTxPower() != 20 {
		t.Errorf("tx_power: got %d, want %d", status.GetTxPower(), 20)
	}

	if status.GetConnectedClients() != 2 {
		t.Errorf("connected_clients: got %d, want %d", status.GetConnectedClients(), 2)
	}

	if status.GetMeshPeers() != 0 {
		t.Errorf("mesh_peers: got %d, want %d", status.GetMeshPeers(), 0)
	}

	if status.GetWifiMode() != wificonfigv1.WifiMode_WIFI_MODE_AP {
		t.Errorf("wifi_mode: got %v, want %v", status.GetWifiMode(), wificonfigv1.WifiMode_WIFI_MODE_AP)
	}
}

func TestGetRadioStatus_MeshMode(t *testing.T) {
	mac1, _ := net.ParseMAC("C8:3E:1A:7B:00:A3")
	mac2, _ := net.ParseMAC("C8:3E:1A:7B:00:D7")

	svc := newTestWifiConfigService(t)
	svc.WirelessStatus = &fakeWirelessStatusProvider{status: newTestWirelessStatus()}
	svc.IwinfoClient = &fakeIwinfoProvider{infoByDevice: newTestIwinfoData()}
	svc.Wifi = &fakeWireless{
		interfaces: []*wifi.Interface{
			{Index: 2, Name: "phy1-mesh0", HardwareAddr: mac1, Type: wifi.InterfaceTypeMeshPoint, Frequency: 923, ChannelWidth: 4},
		},
		stationInfoByIface: map[string][]*wifi.StationInfo{
			"phy1-mesh0": {
				{HardwareAddr: mac1, Signal: -61},
				{HardwareAddr: mac2, Signal: -73},
			},
		},
	}

	resp, err := svc.GetRadioStatus(context.Background(), &wificonfigv1.GetRadioStatusRequest{RadioName: "radio3"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	status := resp.GetStatus()
	if status.GetMode() != "Mesh Point (802.11s)" {
		t.Errorf("mode: got %q, want %q", status.GetMode(), "Mesh Point (802.11s)")
	}

	if status.GetEncryption() != "WPA3-SAE" {
		t.Errorf("encryption: got %q, want %q", status.GetEncryption(), "WPA3-SAE")
	}

	if status.GetMeshPeers() != 2 {
		t.Errorf("mesh_peers: got %d, want %d", status.GetMeshPeers(), 2)
	}

	if status.GetConnectedClients() != 0 {
		t.Errorf("connected_clients: got %d, want %d", status.GetConnectedClients(), 0)
	}

	if status.GetWifiMode() != wificonfigv1.WifiMode_WIFI_MODE_MESH {
		t.Errorf("wifi_mode: got %v, want %v", status.GetWifiMode(), wificonfigv1.WifiMode_WIFI_MODE_MESH)
	}
}

func TestGetRadioStatus_Inactive(t *testing.T) {
	svc := newTestWifiConfigService(t)
	svc.WirelessStatus = &fakeWirelessStatusProvider{
		status: map[string]*network.WirelessRadioStatus{
			"radio2": {Up: false, Disabled: true, Interfaces: []network.WirelessRadioInterface{}},
		},
	}

	resp, err := svc.GetRadioStatus(context.Background(), &wificonfigv1.GetRadioStatusRequest{RadioName: "radio2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.GetStatus().GetActive() {
		t.Error("expected inactive status for disabled radio")
	}
}

func TestGetRadioStatus_NotFound(t *testing.T) {
	svc := newTestWifiConfigService(t)

	_, err := svc.GetRadioStatus(context.Background(), &wificonfigv1.GetRadioStatusRequest{RadioName: "radio99"})
	if err == nil {
		t.Fatal("expected error for nonexistent radio")
	}
}

// ── GetRadioSettings tests ───────────────────────────────────────────────────

func TestGetRadioSettings_APMode(t *testing.T) {
	svc := newTestWifiConfigService(t)

	resp, err := svc.GetRadioSettings(context.Background(), &wificonfigv1.GetRadioSettingsRequest{RadioName: "radio2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	settings := resp.GetSettings()
	if settings.GetSsid() != "openmanet" {
		t.Errorf("ssid: got %q, want %q", settings.GetSsid(), "openmanet")
	}

	if settings.GetChannel() != "1" {
		t.Errorf("channel: got %q, want %q", settings.GetChannel(), "1")
	}

	if settings.GetBandwidth() != wificonfigv1.WifiHTMode_WIFI_HT_MODE_HT20 {
		t.Errorf("bandwidth: got %v, want %v", settings.GetBandwidth(), wificonfigv1.WifiHTMode_WIFI_HT_MODE_HT20)
	}

	if settings.GetTxPower() != 20 {
		t.Errorf("tx_power: got %d, want %d", settings.GetTxPower(), 20)
	}

	if settings.GetEncryption() != wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_PSK2 {
		t.Errorf("encryption: got %v, want %v", settings.GetEncryption(), wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_PSK2)
	}

	if settings.GetCountry() != "US" {
		t.Errorf("country: got %q, want %q", settings.GetCountry(), "US")
	}

	// Password must NOT be returned.
	if settings.Password != nil {
		t.Error("password should not be returned in GetRadioSettings")
	}

	// MeshId should not be set for AP mode.
	if settings.MeshId != nil {
		t.Error("mesh_id should not be set for AP mode radio")
	}

	if settings.GetMode() != wificonfigv1.WifiMode_WIFI_MODE_AP {
		t.Errorf("mode: got %v, want %v", settings.GetMode(), wificonfigv1.WifiMode_WIFI_MODE_AP)
	}
}

func TestGetRadioSettings_MeshMode(t *testing.T) {
	svc := newTestWifiConfigService(t)

	resp, err := svc.GetRadioSettings(context.Background(), &wificonfigv1.GetRadioSettingsRequest{RadioName: "radio3"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	settings := resp.GetSettings()
	if settings.GetMeshId() != "openmanet-mesh" {
		t.Errorf("mesh_id: got %q, want %q", settings.GetMeshId(), "openmanet-mesh")
	}

	if settings.GetEncryption() != wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_SAE {
		t.Errorf("encryption: got %v, want %v", settings.GetEncryption(), wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_SAE)
	}

	if settings.GetCountry() != "US" {
		t.Errorf("country: got %q, want %q", settings.GetCountry(), "US")
	}

	if settings.GetMode() != wificonfigv1.WifiMode_WIFI_MODE_MESH {
		t.Errorf("mode: got %v, want %v", settings.GetMode(), wificonfigv1.WifiMode_WIFI_MODE_MESH)
	}
}

func TestGetRadioSettings_AvailableOptionsPopulated(t *testing.T) {
	svc := newTestWifiConfigService(t)

	resp, err := svc.GetRadioSettings(context.Background(), &wificonfigv1.GetRadioSettingsRequest{RadioName: "radio2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(resp.GetAvailableChannels()) == 0 {
		t.Error("expected available_channels to be populated")
	}

	if len(resp.GetAvailableBandwidths()) == 0 {
		t.Error("expected available_bandwidths to be populated")
	}

	if len(resp.GetAvailableEncryptions()) == 0 {
		t.Error("expected available_encryptions to be populated")
	}
}

func TestGetRadioSettings_NotFound(t *testing.T) {
	svc := newTestWifiConfigService(t)

	_, err := svc.GetRadioSettings(context.Background(), &wificonfigv1.GetRadioSettingsRequest{RadioName: "radio99"})
	if err == nil {
		t.Fatal("expected error when no linked interface found")
	}
}

// ── UpdateRadioSettings tests ────────────────────────────────────────────────

func TestUpdateRadioSettings_Success(t *testing.T) {
	reader := newWifiConfigMockReader()
	svc := newTestWifiConfigService(t)
	svc.ConfigReader = reader

	resp, err := svc.UpdateRadioSettings(context.Background(), &wificonfigv1.UpdateRadioSettingsRequest{
		RadioName: "radio2",
		Settings: &wificonfigv1.RadioSettings{
			Ssid:      "new-ssid",
			Channel:   "6",
			Bandwidth: wificonfigv1.WifiHTMode_WIFI_HT_MODE_HT40,
			TxPower:   15,
			Country:   strPtr("US"),
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !resp.GetSuccess() {
		t.Errorf("expected success, got message: %v", resp.GetMessage())
	}

	if !reader.commitCalled {
		t.Error("expected Commit to be called")
	}

	if !reader.reloadCalled {
		t.Error("expected ReloadConfig to be called after commit")
	}
}

func TestUpdateRadioSettings_PartialUpdate(t *testing.T) {
	reader := newWifiConfigMockReader()
	svc := newTestWifiConfigService(t)
	svc.ConfigReader = reader

	pwd := "newpassword"

	resp, err := svc.UpdateRadioSettings(context.Background(), &wificonfigv1.UpdateRadioSettingsRequest{
		RadioName: "radio2",
		Settings: &wificonfigv1.RadioSettings{
			Ssid:     "openmanet",
			Channel:  "1",
			Password: &pwd,
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !resp.GetSuccess() {
		t.Errorf("expected success, got message: %v", resp.GetMessage())
	}
}

func TestUpdateRadioSettings_CommitFailure(t *testing.T) {
	reader := newWifiConfigMockReader()
	reader.commitError = errors.New("disk full")
	svc := newTestWifiConfigService(t)
	svc.ConfigReader = reader

	resp, err := svc.UpdateRadioSettings(context.Background(), &wificonfigv1.UpdateRadioSettingsRequest{
		RadioName: "radio2",
		Settings: &wificonfigv1.RadioSettings{
			Ssid:    "test",
			Channel: "1",
		},
	})

	// Commit error is from SetWirelessDeviceConfigWithReader, which is called first.
	// It should result in a non-success response OR an error depending on how commit is wired.
	if err != nil {
		// If it propagates as a connect error that's fine too.
		return
	}

	if resp.GetSuccess() {
		t.Error("expected failure response when commit fails")
	}
}

func TestUpdateRadioSettings_ReloadFailure(t *testing.T) {
	reader := newWifiConfigMockReader()
	reader.reloadError = errors.New("reload failed")
	svc := newTestWifiConfigService(t)
	svc.ConfigReader = reader

	resp, err := svc.UpdateRadioSettings(context.Background(), &wificonfigv1.UpdateRadioSettingsRequest{
		RadioName: "radio2",
		Settings: &wificonfigv1.RadioSettings{
			Ssid:    "test",
			Channel: "1",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.GetSuccess() {
		t.Error("expected failure when reload fails")
	}

	if resp.Message == nil {
		t.Error("expected message explaining reload failure")
	}
}

func TestUpdateRadioSettings_NilSettings(t *testing.T) {
	svc := newTestWifiConfigService(t)

	_, err := svc.UpdateRadioSettings(context.Background(), &wificonfigv1.UpdateRadioSettingsRequest{
		RadioName: "radio2",
	})
	if err == nil {
		t.Fatal("expected error for nil settings")
	}
}

func TestUpdateRadioSettings_NoLinkedInterface(t *testing.T) {
	svc := newTestWifiConfigService(t)
	svc.ConfigReader = &fakeConfigReader{
		data: map[string]map[string]map[string][]string{
			"wireless": {
				"radio99": {"band": {"2g"}},
			},
		},
		sectionTypes: map[string]map[string]string{
			"wireless": {"radio99": "wifi-device"},
		},
	}

	_, err := svc.UpdateRadioSettings(context.Background(), &wificonfigv1.UpdateRadioSettingsRequest{
		RadioName: "radio99",
		Settings:  &wificonfigv1.RadioSettings{Ssid: "test", Channel: "1"},
	})
	if err == nil {
		t.Fatal("expected error when no linked interface found")
	}
}

// ── ListConnectedClients tests ───────────────────────────────────────────────

func TestListConnectedClients_WithClients(t *testing.T) {
	mac1, _ := net.ParseMAC("D4:6D:6D:1A:2B:3C")
	mac2, _ := net.ParseMAC("F0:18:98:4D:5E:6F")

	svc := newTestWifiConfigService(t)
	svc.WirelessStatus = &fakeWirelessStatusProvider{status: newTestWirelessStatus()}
	svc.Wifi = &fakeWireless{
		interfaces: []*wifi.Interface{
			{Index: 1, Name: "phy0-ap0", HardwareAddr: mac1},
		},
		stationInfoByIface: map[string][]*wifi.StationInfo{
			"phy0-ap0": {
				{
					HardwareAddr:    mac1,
					Signal:          -52,
					ReceiveBitrate:  72000000,
					TransmitBitrate: 65000000,
					Connected:       3*time.Hour + 42*time.Minute,
				},
				{
					HardwareAddr:    mac2,
					Signal:          -68,
					ReceiveBitrate:  54000000,
					TransmitBitrate: 48000000,
					Connected:       1*time.Hour + 15*time.Minute,
				},
			},
		},
	}
	svc.DHCPLeases = &fakeDHCPLeaseProvider{
		leases: &network.DHCPLeasesResponse{
			DHCPLeases: []network.DHCPLease{
				{Hostname: "laptop-bravo", MacAddr: "d4:6d:6d:1a:2b:3c", IPAddr: "10.41.0.101"},
				{Hostname: "phone-alpha", MacAddr: "f0:18:98:4d:5e:6f", IPAddr: "10.41.0.102"},
			},
		},
	}

	resp, err := svc.ListConnectedClients(context.Background(), &wificonfigv1.ListConnectedClientsRequest{RadioName: "radio2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	clients := resp.GetClients()
	if len(clients) != 2 {
		t.Fatalf("expected 2 clients, got %d", len(clients))
	}

	c1 := clients[0]
	if c1.GetHostname() != "laptop-bravo" {
		t.Errorf("client 1 hostname: got %q, want %q", c1.GetHostname(), "laptop-bravo")
	}

	if c1.GetMacAddress() != "d4:6d:6d:1a:2b:3c" {
		t.Errorf("client 1 mac: got %q, want %q", c1.GetMacAddress(), "d4:6d:6d:1a:2b:3c")
	}

	if c1.GetSignalDbm() != -52 {
		t.Errorf("client 1 signal: got %d, want %d", c1.GetSignalDbm(), -52)
	}

	if c1.GetRxRateBps() != 72000000 {
		t.Errorf("client 1 rx_rate: got %d, want %d", c1.GetRxRateBps(), 72000000)
	}

	if c1.GetTxRateBps() != 65000000 {
		t.Errorf("client 1 tx_rate: got %d, want %d", c1.GetTxRateBps(), 65000000)
	}
}

func TestListConnectedClients_Empty(t *testing.T) {
	svc := newTestWifiConfigService(t)
	svc.WirelessStatus = &fakeWirelessStatusProvider{status: newTestWirelessStatus()}
	svc.Wifi = &fakeWireless{
		interfaces: []*wifi.Interface{
			{Index: 1, Name: "phy0-ap0"},
		},
		stationInfoByIface: map[string][]*wifi.StationInfo{
			"phy0-ap0": {},
		},
	}

	resp, err := svc.ListConnectedClients(context.Background(), &wificonfigv1.ListConnectedClientsRequest{RadioName: "radio2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(resp.GetClients()) != 0 {
		t.Errorf("expected 0 clients, got %d", len(resp.GetClients()))
	}
}

func TestListConnectedClients_UnknownHostname(t *testing.T) {
	mac, _ := net.ParseMAC("AA:BB:CC:DD:EE:FF")

	svc := newTestWifiConfigService(t)
	svc.WirelessStatus = &fakeWirelessStatusProvider{status: newTestWirelessStatus()}
	svc.Wifi = &fakeWireless{
		interfaces: []*wifi.Interface{
			{Index: 1, Name: "phy0-ap0", HardwareAddr: mac},
		},
		stationInfoByIface: map[string][]*wifi.StationInfo{
			"phy0-ap0": {
				{HardwareAddr: mac, Signal: -70},
			},
		},
	}
	svc.DHCPLeases = &fakeDHCPLeaseProvider{
		leases: &network.DHCPLeasesResponse{},
	}

	resp, err := svc.ListConnectedClients(context.Background(), &wificonfigv1.ListConnectedClientsRequest{RadioName: "radio2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(resp.GetClients()) != 1 {
		t.Fatalf("expected 1 client, got %d", len(resp.GetClients()))
	}

	if resp.GetClients()[0].GetHostname() != "" {
		t.Errorf("expected empty hostname for unknown client, got %q", resp.GetClients()[0].GetHostname())
	}
}

func TestListConnectedClients_NotFound(t *testing.T) {
	svc := newTestWifiConfigService(t)

	_, err := svc.ListConnectedClients(context.Background(), &wificonfigv1.ListConnectedClientsRequest{RadioName: "radio99"})
	if err == nil {
		t.Fatal("expected error for nonexistent radio")
	}
}

// ── ListMeshPeers tests ──────────────────────────────────────────────────────

func TestListMeshPeers_WithPeers(t *testing.T) {
	mac1, _ := net.ParseMAC("C8:3E:1A:7B:00:A3")
	mac2, _ := net.ParseMAC("C8:3E:1A:7B:00:D7")

	svc := newTestWifiConfigService(t)
	svc.WirelessStatus = &fakeWirelessStatusProvider{status: newTestWirelessStatus()}
	svc.Wifi = &fakeWireless{
		interfaces: []*wifi.Interface{
			{Index: 2, Name: "phy1-mesh0", HardwareAddr: mac1},
		},
		stationInfoByIface: map[string][]*wifi.StationInfo{
			"phy1-mesh0": {
				{HardwareAddr: mac1, Signal: -61},
				{HardwareAddr: mac2, Signal: -73},
			},
		},
	}

	batNeighbors := batmanadv.Neighbors{
		// Throughput is what `batctl nj` prints: kbit/s.
		{HardIfname: "phy1-mesh0", NeighAddress: "c8:3e:1a:7b:00:a3", Throughput: 22200, LastSeenMsecs: 200},
		{HardIfname: "phy1-mesh0", NeighAddress: "c8:3e:1a:7b:00:d7", Throughput: 7100, LastSeenMsecs: 400},
	}
	svc.GetMeshNeighbors = func() (*batmanadv.Neighbors, error) {
		return &batNeighbors, nil
	}

	svc.ParseBatHosts = func(_ string) (*batmanadv.BatHosts, error) {
		return &batmanadv.BatHosts{
			Nodes: []batmanadv.Node{
				{
					NodeMAC: "c8:3e:1a:7b:00:a3",
					Hosts: []batmanadv.BatHost{
						{MAC: "c8:3e:1a:7b:00:a3", Hostname: "HaLowLink2-a3b2"},
					},
				},
				{
					NodeMAC: "c8:3e:1a:7b:00:d7",
					Hosts: []batmanadv.BatHost{
						{MAC: "c8:3e:1a:7b:00:d7", Hostname: "HaLowLink2-d7e4"},
					},
				},
			},
		}, nil
	}

	resp, err := svc.ListMeshPeers(context.Background(), &wificonfigv1.ListMeshPeersRequest{RadioName: "radio3"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	peers := resp.GetPeers()
	if len(peers) != 2 {
		t.Fatalf("expected 2 peers, got %d", len(peers))
	}

	// Build a map keyed by MAC for deterministic checks.
	peerMap := map[string]*wificonfigv1.MeshPeer{}
	for _, p := range peers {
		peerMap[p.GetMacAddress()] = p
	}

	p1 := peerMap["c8:3e:1a:7b:00:a3"]
	if p1 == nil {
		t.Fatal("expected peer c8:3e:1a:7b:00:a3")
	}

	if p1.GetHostname() != "HaLowLink2-a3b2" {
		t.Errorf("peer 1 hostname: got %q, want %q", p1.GetHostname(), "HaLowLink2-a3b2")
	}

	if p1.GetSignalDbm() != -61 {
		t.Errorf("peer 1 signal: got %d, want %d", p1.GetSignalDbm(), -61)
	}

	// Throughput: batctl nj reports kbit/s → /1000 = Mbps. A 2.4 GHz
	// HT20 link reads ~22 Mbps; the old /10 showed it as 2220 Mbps.
	if p1.GetThroughputMbps() != 22.2 {
		t.Errorf("peer 1 throughput: got %f, want %f", p1.GetThroughputMbps(), 22.2)
	}

	p2 := peerMap["c8:3e:1a:7b:00:d7"]
	if p2 == nil {
		t.Fatal("expected peer c8:3e:1a:7b:00:d7")
	}

	if p2.GetHostname() != "HaLowLink2-d7e4" {
		t.Errorf("peer 2 hostname: got %q, want %q", p2.GetHostname(), "HaLowLink2-d7e4")
	}

	if p2.GetThroughputMbps() != 7.1 {
		t.Errorf("peer 2 throughput: got %f, want %f", p2.GetThroughputMbps(), 7.1)
	}
}

func TestListMeshPeers_Empty(t *testing.T) {
	svc := newTestWifiConfigService(t)
	svc.WirelessStatus = &fakeWirelessStatusProvider{status: newTestWirelessStatus()}

	resp, err := svc.ListMeshPeers(context.Background(), &wificonfigv1.ListMeshPeersRequest{RadioName: "radio3"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(resp.GetPeers()) != 0 {
		t.Errorf("expected 0 peers, got %d", len(resp.GetPeers()))
	}
}

func TestListMeshPeers_UnknownHostname(t *testing.T) {
	svc := newTestWifiConfigService(t)
	svc.WirelessStatus = &fakeWirelessStatusProvider{status: newTestWirelessStatus()}
	svc.Wifi = &fakeWireless{
		interfaces: []*wifi.Interface{
			{Index: 2, Name: "phy1-mesh0"},
		},
	}

	batNeighbors := batmanadv.Neighbors{
		{HardIfname: "phy1-mesh0", NeighAddress: "aa:bb:cc:dd:ee:ff", Throughput: 10, LastSeenMsecs: 100},
	}
	svc.GetMeshNeighbors = func() (*batmanadv.Neighbors, error) {
		return &batNeighbors, nil
	}

	resp, err := svc.ListMeshPeers(context.Background(), &wificonfigv1.ListMeshPeersRequest{RadioName: "radio3"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(resp.GetPeers()) != 1 {
		t.Fatalf("expected 1 peer, got %d", len(resp.GetPeers()))
	}

	if resp.GetPeers()[0].GetHostname() != "" {
		t.Errorf("expected empty hostname, got %q", resp.GetPeers()[0].GetHostname())
	}
}

func TestListMeshPeers_NotFound(t *testing.T) {
	svc := newTestWifiConfigService(t)

	_, err := svc.ListMeshPeers(context.Background(), &wificonfigv1.ListMeshPeersRequest{RadioName: "radio99"})
	if err == nil {
		t.Fatal("expected error for nonexistent radio")
	}
}

// ── formatting helper tests ──────────────────────────────────────────────────

func TestFormatEncryptionDisplay(t *testing.T) {
	tests := []struct {
		name string
		enc  iwinfo.EncryptionInfo
		want string
	}{
		{
			name: "WPA3-SAE",
			enc:  iwinfo.EncryptionInfo{Enabled: true, WPA: []int{3}, Authentication: []string{"sae"}, Ciphers: []string{"ccmp"}},
			want: "WPA3-SAE",
		},
		{
			name: "WPA2-PSK (CCMP)",
			enc:  iwinfo.EncryptionInfo{Enabled: true, WPA: []int{2}, Authentication: []string{"psk"}, Ciphers: []string{"ccmp"}},
			want: "WPA2-PSK (CCMP)",
		},
		{
			name: "Open",
			enc:  iwinfo.EncryptionInfo{Enabled: false},
			want: "Open",
		},
		{
			name: "Encrypted no details",
			enc:  iwinfo.EncryptionInfo{Enabled: true},
			want: "Encrypted",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := handlers.FormatEncryptionDisplay(tt.enc)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatBandwidthDisplay(t *testing.T) {
	tests := []struct {
		htmode string
		want   string
	}{
		{"HT20", "20 MHz"},
		{"HT40", "40 MHz"},
		{"VHT80", "80 MHz"},
		{"HE160", "160 MHz"},
		{"NOHT", "No HT"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.htmode, func(t *testing.T) {
			got := handlers.FormatBandwidthDisplay(tt.htmode)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatModeDisplay(t *testing.T) {
	tests := []struct {
		mode string
		want string
	}{
		{"Master", "Access Point"},
		{"Mesh Point", "Mesh Point (802.11s)"},
		{"Client", "Client"},
		{"Unknown", "Unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			got := handlers.FormatModeDisplay(tt.mode)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatBandDisplayName(t *testing.T) {
	tests := []struct {
		band wificonfigv1.WifiBand
		want string
	}{
		{wificonfigv1.WifiBand_WIFI_BAND_2G, "2.4 GHz Radio"},
		{wificonfigv1.WifiBand_WIFI_BAND_5G, "5 GHz Radio"},
		{wificonfigv1.WifiBand_WIFI_BAND_6G, "6 GHz Radio"},
		{wificonfigv1.WifiBand_WIFI_BAND_S1G, "HaLow Radio"},
		{wificonfigv1.WifiBand_WIFI_BAND_60G, "60 GHz Radio"},
	}

	for _, tt := range tests {
		t.Run(tt.band.String(), func(t *testing.T) {
			got := handlers.FormatBandDisplayName(tt.band)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────

func strPtr(s string) *string {
	return &s
}

// ── enum conversion tests ────────────────────────────────────────────────────

func TestWifiBandToProto(t *testing.T) {
	tests := []struct {
		input string
		want  wificonfigv1.WifiBand
	}{
		{"2g", wificonfigv1.WifiBand_WIFI_BAND_2G},
		{"5g", wificonfigv1.WifiBand_WIFI_BAND_5G},
		{"6g", wificonfigv1.WifiBand_WIFI_BAND_6G},
		{"s1g", wificonfigv1.WifiBand_WIFI_BAND_S1G},
		{"60g", wificonfigv1.WifiBand_WIFI_BAND_60G},
		{"S1G", wificonfigv1.WifiBand_WIFI_BAND_S1G},
		{"unknown", wificonfigv1.WifiBand_WIFI_BAND_UNSPECIFIED},
		{"", wificonfigv1.WifiBand_WIFI_BAND_UNSPECIFIED},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := handlers.WifiBandToProto(tt.input)
			if got != tt.want {
				t.Errorf("WifiBandToProto(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestWifiModeToProto(t *testing.T) {
	tests := []struct {
		input string
		want  wificonfigv1.WifiMode
	}{
		{"ap", wificonfigv1.WifiMode_WIFI_MODE_AP},
		{"master", wificonfigv1.WifiMode_WIFI_MODE_AP},
		{"mesh", wificonfigv1.WifiMode_WIFI_MODE_MESH},
		{"mesh point", wificonfigv1.WifiMode_WIFI_MODE_MESH},
		{"sta", wificonfigv1.WifiMode_WIFI_MODE_STA},
		{"client", wificonfigv1.WifiMode_WIFI_MODE_STA},
		{"managed", wificonfigv1.WifiMode_WIFI_MODE_STA},
		{"adhoc", wificonfigv1.WifiMode_WIFI_MODE_ADHOC},
		{"ad-hoc", wificonfigv1.WifiMode_WIFI_MODE_ADHOC},
		{"ibss", wificonfigv1.WifiMode_WIFI_MODE_ADHOC},
		{"monitor", wificonfigv1.WifiMode_WIFI_MODE_MONITOR},
		{"unknown", wificonfigv1.WifiMode_WIFI_MODE_UNSPECIFIED},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := handlers.WifiModeToProto(tt.input)
			if got != tt.want {
				t.Errorf("WifiModeToProto(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestWifiEncryptionRoundTrip(t *testing.T) {
	tests := []struct {
		input string
		enum  wificonfigv1.WifiEncryption
	}{
		{"sae", wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_SAE},
		{"psk2", wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_PSK2},
		{"psk", wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_PSK},
		{"psk-mixed", wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_PSK_MIXED},
		{"sae-mixed", wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_SAE_MIXED},
		{"none", wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_NONE},
		{"owe", wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_OWE},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			proto := handlers.WifiEncryptionToProto(tt.input)
			if proto != tt.enum {
				t.Errorf("WifiEncryptionToProto(%q) = %v, want %v", tt.input, proto, tt.enum)
			}

			back := handlers.ProtoToWifiEncryption(proto)
			if back != tt.input {
				t.Errorf("ProtoToWifiEncryption(%v) = %q, want %q", proto, back, tt.input)
			}
		})
	}
}

func TestWifiHTModeRoundTrip(t *testing.T) {
	tests := []struct {
		input string
		enum  wificonfigv1.WifiHTMode
	}{
		{"NOHT", wificonfigv1.WifiHTMode_WIFI_HT_MODE_NOHT},
		{"HT20", wificonfigv1.WifiHTMode_WIFI_HT_MODE_HT20},
		{"HT40-", wificonfigv1.WifiHTMode_WIFI_HT_MODE_HT40_MINUS},
		{"HT40+", wificonfigv1.WifiHTMode_WIFI_HT_MODE_HT40_PLUS},
		{"HT40", wificonfigv1.WifiHTMode_WIFI_HT_MODE_HT40},
		{"VHT20", wificonfigv1.WifiHTMode_WIFI_HT_MODE_VHT20},
		{"VHT40", wificonfigv1.WifiHTMode_WIFI_HT_MODE_VHT40},
		{"VHT80", wificonfigv1.WifiHTMode_WIFI_HT_MODE_VHT80},
		{"VHT160", wificonfigv1.WifiHTMode_WIFI_HT_MODE_VHT160},
		{"HE20", wificonfigv1.WifiHTMode_WIFI_HT_MODE_HE20},
		{"HE40", wificonfigv1.WifiHTMode_WIFI_HT_MODE_HE40},
		{"HE80", wificonfigv1.WifiHTMode_WIFI_HT_MODE_HE80},
		{"HE160", wificonfigv1.WifiHTMode_WIFI_HT_MODE_HE160},
		{"1 MHz", wificonfigv1.WifiHTMode_WIFI_HT_MODE_S1G_1MHZ},
		{"2 MHz", wificonfigv1.WifiHTMode_WIFI_HT_MODE_S1G_2MHZ},
		{"4 MHz", wificonfigv1.WifiHTMode_WIFI_HT_MODE_S1G_4MHZ},
		{"8 MHz", wificonfigv1.WifiHTMode_WIFI_HT_MODE_S1G_8MHZ},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			proto := handlers.WifiHTModeToProto(tt.input)
			if proto != tt.enum {
				t.Errorf("WifiHTModeToProto(%q) = %v, want %v", tt.input, proto, tt.enum)
			}

			back := handlers.ProtoToWifiHTMode(proto)
			if back != tt.input {
				t.Errorf("ProtoToWifiHTMode(%v) = %q, want %q", proto, back, tt.input)
			}
		})
	}
}

func TestWifiEncryptionToProto_UnspecifiedReturnsEmpty(t *testing.T) {
	got := handlers.ProtoToWifiEncryption(wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_UNSPECIFIED)
	if got != "" {
		t.Errorf("ProtoToWifiEncryption(UNSPECIFIED) = %q, want empty", got)
	}
}

func TestWifiHTModeToProto_UnspecifiedReturnsEmpty(t *testing.T) {
	got := handlers.ProtoToWifiHTMode(wificonfigv1.WifiHTMode_WIFI_HT_MODE_UNSPECIFIED)
	if got != "" {
		t.Errorf("ProtoToWifiHTMode(UNSPECIFIED) = %q, want empty", got)
	}
}

func TestGetRadioSettings_AvailableOptionsAreEnumTypes(t *testing.T) {
	svc := newTestWifiConfigService(t)

	resp, err := svc.GetRadioSettings(context.Background(), &wificonfigv1.GetRadioSettingsRequest{RadioName: "radio2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 2g band should return NOHT, HT20, HT40.
	wantBW := []wificonfigv1.WifiHTMode{
		wificonfigv1.WifiHTMode_WIFI_HT_MODE_NOHT,
		wificonfigv1.WifiHTMode_WIFI_HT_MODE_HT20,
		wificonfigv1.WifiHTMode_WIFI_HT_MODE_HT40,
	}

	if len(resp.GetAvailableBandwidths()) != len(wantBW) {
		t.Fatalf("available_bandwidths: got %d, want %d", len(resp.GetAvailableBandwidths()), len(wantBW))
	}

	for i, got := range resp.GetAvailableBandwidths() {
		if got != wantBW[i] {
			t.Errorf("available_bandwidths[%d]: got %v, want %v", i, got, wantBW[i])
		}
	}

	// Encryptions should always be SAE, PSK2, PSK, NONE.
	wantEnc := []wificonfigv1.WifiEncryption{
		wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_SAE,
		wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_PSK2,
		wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_PSK,
		wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_NONE,
	}

	if len(resp.GetAvailableEncryptions()) != len(wantEnc) {
		t.Fatalf("available_encryptions: got %d, want %d", len(resp.GetAvailableEncryptions()), len(wantEnc))
	}

	for i, got := range resp.GetAvailableEncryptions() {
		if got != wantEnc[i] {
			t.Errorf("available_encryptions[%d]: got %v, want %v", i, got, wantEnc[i])
		}
	}
}

func TestUpdateRadioSettings_WithEncryptionEnum(t *testing.T) {
	reader := newWifiConfigMockReader()
	svc := newTestWifiConfigService(t)
	svc.ConfigReader = reader

	resp, err := svc.UpdateRadioSettings(context.Background(), &wificonfigv1.UpdateRadioSettingsRequest{
		RadioName: "radio2",
		Settings: &wificonfigv1.RadioSettings{
			Ssid:       "test-ssid",
			Channel:    "6",
			Bandwidth:  wificonfigv1.WifiHTMode_WIFI_HT_MODE_HT20,
			TxPower:    20,
			Encryption: wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_SAE,
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !resp.GetSuccess() {
		t.Errorf("expected success, got message: %v", resp.GetMessage())
	}

	// Verify encryption was written as UCI string.
	vals, ok := reader.Get("wireless", "default_radio2", "encryption")
	if !ok || len(vals) == 0 {
		t.Fatal("expected encryption to be set in UCI")
	}

	if vals[0] != "sae" {
		t.Errorf("UCI encryption: got %q, want %q", vals[0], "sae")
	}

	// Verify htmode was written as UCI string.
	vals, ok = reader.Get("wireless", "radio2", "htmode")
	if !ok || len(vals) == 0 {
		t.Fatal("expected htmode to be set in UCI")
	}

	if vals[0] != "HT20" {
		t.Errorf("UCI htmode: got %q, want %q", vals[0], "HT20")
	}
}

func TestUpdateRadioSettings_UnspecifiedEncryptionSkipped(t *testing.T) {
	reader := newWifiConfigMockReader()
	svc := newTestWifiConfigService(t)
	svc.ConfigReader = reader

	// Set initial encryption explicitly.
	_ = reader.SetType("wireless", "default_radio2", "encryption", 0, "psk2")

	resp, err := svc.UpdateRadioSettings(context.Background(), &wificonfigv1.UpdateRadioSettingsRequest{
		RadioName: "radio2",
		Settings: &wificonfigv1.RadioSettings{
			Ssid:    "test",
			Channel: "1",
			// Encryption and Bandwidth left as UNSPECIFIED (zero value) = don't change.
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !resp.GetSuccess() {
		t.Errorf("expected success, got message: %v", resp.GetMessage())
	}

	// Encryption should still be psk2 (not overwritten).
	vals, ok := reader.Get("wireless", "default_radio2", "encryption")
	if !ok || len(vals) == 0 {
		t.Fatal("expected encryption to still be set")
	}

	if vals[0] != "psk2" {
		t.Errorf("expected encryption unchanged at %q, got %q", "psk2", vals[0])
	}
}

func TestUpdateRadioSettings_WithMode(t *testing.T) {
	reader := newWifiConfigMockReader()
	svc := newTestWifiConfigService(t)
	svc.ConfigReader = reader

	resp, err := svc.UpdateRadioSettings(context.Background(), &wificonfigv1.UpdateRadioSettingsRequest{
		RadioName: "radio2",
		Settings: &wificonfigv1.RadioSettings{
			Ssid:    "test-ssid",
			Channel: "6",
			Mode:    wificonfigv1.WifiMode_WIFI_MODE_MESH,
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !resp.GetSuccess() {
		t.Errorf("expected success, got message: %v", resp.GetMessage())
	}

	vals, ok := reader.Get("wireless", "default_radio2", "mode")
	if !ok || len(vals) == 0 {
		t.Fatal("expected mode to be set in UCI")
	}

	if vals[0] != "mesh" {
		t.Errorf("UCI mode: got %q, want %q", vals[0], "mesh")
	}
}

func TestUpdateRadioSettings_UnspecifiedModeSkipped(t *testing.T) {
	reader := newWifiConfigMockReader()
	svc := newTestWifiConfigService(t)
	svc.ConfigReader = reader

	// Set initial mode explicitly.
	_ = reader.SetType("wireless", "default_radio2", "mode", 0, "ap")

	resp, err := svc.UpdateRadioSettings(context.Background(), &wificonfigv1.UpdateRadioSettingsRequest{
		RadioName: "radio2",
		Settings: &wificonfigv1.RadioSettings{
			Ssid:    "test",
			Channel: "1",
			// Mode left as UNSPECIFIED (zero value) = don't change.
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !resp.GetSuccess() {
		t.Errorf("expected success, got message: %v", resp.GetMessage())
	}

	vals, ok := reader.Get("wireless", "default_radio2", "mode")
	if !ok || len(vals) == 0 {
		t.Fatal("expected mode to still be set")
	}

	if vals[0] != "ap" {
		t.Errorf("expected mode unchanged at %q, got %q", "ap", vals[0])
	}
}

// A type=morse (HaLow) wifi-device only ever carries mesh-mode
// ifaces: the settings API must refuse to flip one to any other mode
// and leave the iface untouched.
func TestUpdateRadioSettings_RejectsNonMeshModeOnMorseRadio(t *testing.T) {
	modes := []wificonfigv1.WifiMode{
		wificonfigv1.WifiMode_WIFI_MODE_AP,
		wificonfigv1.WifiMode_WIFI_MODE_STA,
		wificonfigv1.WifiMode_WIFI_MODE_ADHOC,
		wificonfigv1.WifiMode_WIFI_MODE_MONITOR,
	}

	for _, mode := range modes {
		t.Run(mode.String(), func(t *testing.T) {
			reader := newWifiConfigMockReader()
			if err := reader.SetType("wireless", "radio2", "type", uci.TypeOption, "morse"); err != nil {
				t.Fatalf("seed radio2 type: %v", err)
			}

			if err := reader.SetType("wireless", "default_radio2", "mode", uci.TypeOption, "mesh"); err != nil {
				t.Fatalf("seed default_radio2 mode: %v", err)
			}

			svc := newTestWifiConfigService(t)
			svc.ConfigReader = reader

			_, err := svc.UpdateRadioSettings(context.Background(), &wificonfigv1.UpdateRadioSettingsRequest{
				RadioName: "radio2",
				Settings: &wificonfigv1.RadioSettings{
					Ssid:    "client-ap",
					Channel: "42",
					Mode:    mode,
				},
			})
			requireConnectCode(t, err, connect.CodeInvalidArgument)

			if !strings.Contains(err.Error(), "type=morse") {
				t.Errorf("error should name the type=morse rule, got %q", err.Error())
			}

			vals, ok := reader.Get("wireless", "default_radio2", "mode")
			if !ok || len(vals) == 0 || vals[0] != "mesh" {
				t.Errorf("default_radio2 mode must stay mesh, got %v", vals)
			}

			if reader.commitCalled {
				t.Error("nothing may be committed on a rejected request")
			}
		})
	}
}

// Mesh and mode-less (channel/txpower-only) edits on a HaLow radio
// still go through; the rule only forbids non-mesh modes.
func TestUpdateRadioSettings_MorseRadioAcceptsMeshAndUnspecifiedMode(t *testing.T) {
	modes := map[string]wificonfigv1.WifiMode{
		"mesh":        wificonfigv1.WifiMode_WIFI_MODE_MESH,
		"unspecified": wificonfigv1.WifiMode_WIFI_MODE_UNSPECIFIED,
	}

	for name, mode := range modes {
		t.Run(name, func(t *testing.T) {
			reader := newWifiConfigMockReader()
			if err := reader.SetType("wireless", "radio2", "type", uci.TypeOption, "morse"); err != nil {
				t.Fatalf("seed radio2 type: %v", err)
			}

			svc := newTestWifiConfigService(t)
			svc.ConfigReader = reader

			resp, err := svc.UpdateRadioSettings(context.Background(), &wificonfigv1.UpdateRadioSettingsRequest{
				RadioName: "radio2",
				Settings: &wificonfigv1.RadioSettings{
					Ssid:    "halowmesh",
					Channel: "42",
					Mode:    mode,
				},
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if !resp.GetSuccess() {
				t.Errorf("expected success, got message: %v", resp.GetMessage())
			}
		})
	}
}

func TestUpdateRadioSettings_MeshModeSetsMeshFwdingZero(t *testing.T) {
	reader := newWifiConfigMockReader()
	svc := newTestWifiConfigService(t)
	svc.ConfigReader = reader

	resp, err := svc.UpdateRadioSettings(context.Background(), &wificonfigv1.UpdateRadioSettingsRequest{
		RadioName: "radio2",
		Settings: &wificonfigv1.RadioSettings{
			Ssid:    "test-mesh",
			Channel: "6",
			Mode:    wificonfigv1.WifiMode_WIFI_MODE_MESH,
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !resp.GetSuccess() {
		t.Errorf("expected success, got message: %v", resp.GetMessage())
	}

	vals, ok := reader.Get("wireless", "default_radio2", "mesh_fwding")
	if !ok || len(vals) == 0 {
		t.Fatal("expected mesh_fwding to be set on the wifi-iface")
	}

	if vals[0] != "0" {
		t.Errorf("mesh_fwding: got %q, want %q", vals[0], "0")
	}
}

func TestUpdateRadioSettings_NonMeshModeLeavesMeshFwdingAlone(t *testing.T) {
	reader := newWifiConfigMockReader()
	svc := newTestWifiConfigService(t)
	svc.ConfigReader = reader

	resp, err := svc.UpdateRadioSettings(context.Background(), &wificonfigv1.UpdateRadioSettingsRequest{
		RadioName: "radio2",
		Settings: &wificonfigv1.RadioSettings{
			Ssid:    "test-ap",
			Channel: "6",
			Mode:    wificonfigv1.WifiMode_WIFI_MODE_AP,
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !resp.GetSuccess() {
		t.Errorf("expected success, got message: %v", resp.GetMessage())
	}

	if vals, ok := reader.Get("wireless", "default_radio2", "mesh_fwding"); ok {
		t.Errorf("expected mesh_fwding not to be written for AP mode, got %v", vals)
	}

	// AP mode must rebind to ahwlan so the iface joins br-ahwlan.
	vals, ok := reader.Get("wireless", "default_radio2", "network")
	if !ok || len(vals) == 0 {
		t.Fatal("expected network to be set on the wifi-iface")
	}

	if vals[0] != "ahwlan" {
		t.Errorf("network: got %q, want %q", vals[0], "ahwlan")
	}
}

func TestUpdateRadioSettings_MeshMode_BindsToUnusedBatmesh(t *testing.T) {
	reader := newWifiConfigMockReader()
	svc := newTestWifiConfigService(t)
	svc.ConfigReader = reader

	// Fixture: default_radio2 is on ahwlan; default_radio3 is on batmesh0.
	// Switching radio2 to mesh should pick batmesh1 (the only free slot).
	resp, err := svc.UpdateRadioSettings(context.Background(), &wificonfigv1.UpdateRadioSettingsRequest{
		RadioName: "radio2",
		Settings: &wificonfigv1.RadioSettings{
			Ssid:    "mesh-ssid",
			Channel: "6",
			Mode:    wificonfigv1.WifiMode_WIFI_MODE_MESH,
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !resp.GetSuccess() {
		t.Errorf("expected success, got message: %v", resp.GetMessage())
	}

	vals, ok := reader.Get("wireless", "default_radio2", "network")
	if !ok || len(vals) == 0 {
		t.Fatal("expected network to be set on the wifi-iface")
	}

	if vals[0] != "batmesh1" {
		t.Errorf("network: got %q, want %q", vals[0], "batmesh1")
	}
}

func TestUpdateRadioSettings_MeshMode_AlreadyOnBatmesh_LeavesUnchanged(t *testing.T) {
	reader := newWifiConfigMockReader()
	svc := newTestWifiConfigService(t)
	svc.ConfigReader = reader

	// Fixture: default_radio3 is already on batmesh0 in mesh mode.
	// A mesh update should not migrate it.
	resp, err := svc.UpdateRadioSettings(context.Background(), &wificonfigv1.UpdateRadioSettingsRequest{
		RadioName: "radio3",
		Settings: &wificonfigv1.RadioSettings{
			Ssid:    "mesh-ssid",
			Channel: "42",
			Mode:    wificonfigv1.WifiMode_WIFI_MODE_MESH,
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !resp.GetSuccess() {
		t.Errorf("expected success, got message: %v", resp.GetMessage())
	}

	vals, ok := reader.Get("wireless", "default_radio3", "network")
	if !ok || len(vals) == 0 {
		t.Fatal("expected network to remain set on the wifi-iface")
	}

	if vals[0] != "batmesh0" {
		t.Errorf("network: got %q, want %q (must not migrate when already on batmesh*)", vals[0], "batmesh0")
	}
}

func TestUpdateRadioSettings_MeshMode_NoSlotReturnsFailedPrecondition(t *testing.T) {
	reader := newWifiConfigMockReader()
	svc := newTestWifiConfigService(t)
	svc.ConfigReader = reader

	// Take batmesh1 with an unrelated iface so both canonical slots are
	// in use by ifaces other than the target. default_radio2 is on
	// ahwlan; switching it to mesh must now fail.
	if err := reader.AddSection("wireless", "extra_mesh", "wifi-iface"); err != nil {
		t.Fatalf("AddSection: %v", err)
	}

	if err := reader.SetType("wireless", "extra_mesh", "network", uci.TypeOption, "batmesh1"); err != nil {
		t.Fatalf("SetType network: %v", err)
	}

	if err := reader.SetType("wireless", "extra_mesh", "device", uci.TypeOption, "radioX"); err != nil {
		t.Fatalf("SetType device: %v", err)
	}

	_, err := svc.UpdateRadioSettings(context.Background(), &wificonfigv1.UpdateRadioSettingsRequest{
		RadioName: "radio2",
		Settings: &wificonfigv1.RadioSettings{
			Ssid:    "mesh-ssid",
			Channel: "6",
			Mode:    wificonfigv1.WifiMode_WIFI_MODE_MESH,
		},
	})
	if err == nil {
		t.Fatal("expected an error when no batmesh slot is free")
	}

	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("expected *connect.Error, got %T: %v", err, err)
	}

	if connectErr.Code() != connect.CodeFailedPrecondition {
		t.Errorf("code: got %v, want %v", connectErr.Code(), connect.CodeFailedPrecondition)
	}

	// Iface UCI must be unchanged.
	vals, ok := reader.Get("wireless", "default_radio2", "network")
	if !ok || len(vals) == 0 || vals[0] != "ahwlan" {
		t.Errorf("network: expected unchanged %q, got %v (ok=%v)", "ahwlan", vals, ok)
	}
}

func TestUpdateRadioSettings_NoModeChange_LeavesNetworkAlone(t *testing.T) {
	reader := newWifiConfigMockReader()
	svc := newTestWifiConfigService(t)
	svc.ConfigReader = reader

	// default_radio3 is on batmesh0. A txpower-only update must not
	// touch the network binding.
	resp, err := svc.UpdateRadioSettings(context.Background(), &wificonfigv1.UpdateRadioSettingsRequest{
		RadioName: "radio3",
		Settings: &wificonfigv1.RadioSettings{
			TxPower: 12,
			// Mode left as UNSPECIFIED.
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !resp.GetSuccess() {
		t.Errorf("expected success, got message: %v", resp.GetMessage())
	}

	vals, ok := reader.Get("wireless", "default_radio3", "network")
	if !ok || len(vals) == 0 {
		t.Fatal("expected network to remain set")
	}

	if vals[0] != "batmesh0" {
		t.Errorf("network: got %q, want %q (must not change when mode is UNSPECIFIED)", vals[0], "batmesh0")
	}
}

func TestWifiModeRoundTrip(t *testing.T) {
	tests := []struct {
		uci  string
		enum wificonfigv1.WifiMode
	}{
		{"ap", wificonfigv1.WifiMode_WIFI_MODE_AP},
		{"mesh", wificonfigv1.WifiMode_WIFI_MODE_MESH},
		{"sta", wificonfigv1.WifiMode_WIFI_MODE_STA},
		{"adhoc", wificonfigv1.WifiMode_WIFI_MODE_ADHOC},
		{"monitor", wificonfigv1.WifiMode_WIFI_MODE_MONITOR},
	}

	for _, tt := range tests {
		t.Run(tt.uci, func(t *testing.T) {
			proto := handlers.WifiModeToProto(tt.uci)
			if proto != tt.enum {
				t.Errorf("WifiModeToProto(%q) = %v, want %v", tt.uci, proto, tt.enum)
			}

			back := handlers.ProtoToWifiMode(proto)
			if back != tt.uci {
				t.Errorf("ProtoToWifiMode(%v) = %q, want %q", proto, back, tt.uci)
			}
		})
	}

	if got := handlers.ProtoToWifiMode(wificonfigv1.WifiMode_WIFI_MODE_UNSPECIFIED); got != "" {
		t.Errorf("ProtoToWifiMode(UNSPECIFIED) = %q, want empty string", got)
	}
}

func TestCurrentRadioSettings_MatchesGetRadioSettings(t *testing.T) {
	svc := newTestWifiConfigService(t)
	svc.ConfigReader = meshRSSIFixture(t, "default_radio3", "-75")

	viaRPC, err := svc.GetRadioSettings(context.Background(), &wificonfigv1.GetRadioSettingsRequest{RadioName: "radio3"})
	require.NoError(t, err)

	cur, err := svc.CurrentRadioSettings("radio3")
	require.NoError(t, err)

	assert.Equal(t, viaRPC.GetSettings().GetMeshId(), cur.GetMeshId())
	assert.Equal(t, viaRPC.GetSettings().GetChannel(), cur.GetChannel())
	assert.Equal(t, viaRPC.GetSettings().GetTxPower(), cur.GetTxPower())
	assert.Equal(t, viaRPC.GetSettings().GetMode(), cur.GetMode())
	assert.Equal(t, viaRPC.GetSettings().GetMeshRssiThreshold(), cur.GetMeshRssiThreshold())
	assert.Equal(t, int32(-75), cur.GetMeshRssiThreshold())
	assert.Nil(t, cur.Password, "passwords are never read back")
}

func TestCurrentRadioSettings_UnknownRadio(t *testing.T) {
	svc := newTestWifiConfigService(t)

	_, err := svc.CurrentRadioSettings("radio9")

	var cerr *connect.Error
	require.ErrorAs(t, err, &cerr)
	assert.Equal(t, connect.CodeNotFound, cerr.Code())
}

func TestApplyRadioSettingsBatch_TwoRadiosOneReload(t *testing.T) {
	reader := newWifiConfigMockReader()
	svc := newTestWifiConfigService(t)
	svc.ConfigReader = reader
	rl := &fakeRadioReloader{}
	svc.ReloadServices = rl.Reload

	err := svc.ApplyRadioSettingsBatch(context.Background(), []handlers.RadioSettingsUpdate{
		{RadioName: "radio2", Settings: &wificonfigv1.RadioSettings{Ssid: "ap-new", Channel: "6", TxPower: 15}},
		{RadioName: "radio3", Settings: &wificonfigv1.RadioSettings{Ssid: "mesh-new", MeshId: strPtr("mesh-new"), Channel: "28", TxPower: 14}},
	})
	require.NoError(t, err)

	assert.Equal(t, 1, reader.reloadCalls, "one reload for the whole batch")
	assert.Equal(t, 1, rl.count(), "one service reload for the whole batch")

	ch, _ := reader.Get("wireless", "radio2", "channel")
	assert.Equal(t, []string{"6"}, ch)

	meshID, _ := reader.Get("wireless", "default_radio3", "mesh_id")
	assert.Equal(t, []string{"mesh-new"}, meshID)
}

func TestApplyRadioSettingsBatch_StageErrorSkipsReload(t *testing.T) {
	reader := newWifiConfigMockReader()
	svc := newTestWifiConfigService(t)
	svc.ConfigReader = reader
	rl := &fakeRadioReloader{}
	svc.ReloadServices = rl.Reload

	err := svc.ApplyRadioSettingsBatch(context.Background(), []handlers.RadioSettingsUpdate{
		{RadioName: "radio2", Settings: &wificonfigv1.RadioSettings{Ssid: "ap-new", Channel: "6", TxPower: 15}},
		{RadioName: "radio9", Settings: &wificonfigv1.RadioSettings{Ssid: "ghost", Channel: "1", TxPower: 15}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "radio9")
	assert.Equal(t, 0, rl.count(), "no service reload when staging fails")
	assert.Equal(t, 0, reader.reloadCalls, "a failed batch must not reload")
}

func TestApplyRadioSettingsBatch_ReloadErrorSurfaces(t *testing.T) {
	reader := newWifiConfigMockReader()
	reader.reloadError = errors.New("wifi reload failed")
	svc := newTestWifiConfigService(t)
	svc.ConfigReader = reader

	err := svc.ApplyRadioSettingsBatch(context.Background(), []handlers.RadioSettingsUpdate{
		{RadioName: "radio2", Settings: &wificonfigv1.RadioSettings{Ssid: "ap-new", Channel: "6", TxPower: 15}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "wifi reload failed")
}

func TestUpdateRadioSettings_ZeroTxPowerLeavesUCIUntouched(t *testing.T) {
	reader := newWifiConfigMockReader()
	svc := newTestWifiConfigService(t)
	svc.ConfigReader = reader

	resp, err := svc.UpdateRadioSettings(context.Background(), &wificonfigv1.UpdateRadioSettingsRequest{
		RadioName: "radio2",
		Settings:  &wificonfigv1.RadioSettings{Ssid: "ap-new", Channel: "6", TxPower: 0},
	})
	require.NoError(t, err)
	require.True(t, resp.GetSuccess())

	tx, _ := reader.Get("wireless", "radio2", "txpower")
	assert.Equal(t, []string{"20"}, tx, "tx_power 0 means 'unset' and must not force 0 dBm")
}

// A mode=mesh wifi-iface must carry mesh_id only. The frontend mirrors
// mesh_id into ssid to satisfy the proto's ssid min_len, and the AP
// section being converted already has an ssid; either one left in the
// section keeps the radio from coming up.
func TestUpdateRadioSettings_MeshMode_WritesMeshIDNotSSID(t *testing.T) {
	reader := newWifiConfigMockReader()
	svc := newTestWifiConfigService(t)
	svc.ConfigReader = reader

	resp, err := svc.UpdateRadioSettings(context.Background(), &wificonfigv1.UpdateRadioSettingsRequest{
		RadioName: "radio2",
		Settings: &wificonfigv1.RadioSettings{
			Ssid:       "backhaul",
			MeshId:     strPtr("backhaul"),
			Password:   strPtr("meshsecret"),
			Channel:    "6",
			Mode:       wificonfigv1.WifiMode_WIFI_MODE_MESH,
			Encryption: wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_SAE,
		},
	})
	require.NoError(t, err)
	require.True(t, resp.GetSuccess(), "message: %v", resp.GetMessage())

	mode, _ := reader.Get("wireless", "default_radio2", "mode")
	assert.Equal(t, []string{"mesh"}, mode)

	meshID, _ := reader.Get("wireless", "default_radio2", "mesh_id")
	assert.Equal(t, []string{"backhaul"}, meshID)

	ssid, hasSSID := reader.Get("wireless", "default_radio2", "ssid")
	assert.False(t, hasSSID, "ssid must not be written on a mesh iface, got %v", ssid)
}

// An edit that leaves mode unspecified on an iface already in mesh mode
// (channel/txpower change) still carries an ssid because the proto
// requires one; it must not be written, and a stale ssid already on
// the section must be cleared.
func TestUpdateRadioSettings_MeshIface_UnspecifiedMode_DropsStaleSSID(t *testing.T) {
	reader := newWifiConfigMockReader()
	svc := newTestWifiConfigService(t)
	svc.ConfigReader = reader

	// Fixture default_radio3 is mode=mesh with both ssid and mesh_id.
	resp, err := svc.UpdateRadioSettings(context.Background(), &wificonfigv1.UpdateRadioSettingsRequest{
		RadioName: "radio3",
		Settings: &wificonfigv1.RadioSettings{
			Ssid:    "openmanet-mesh",
			MeshId:  strPtr("openmanet-mesh"),
			Channel: "28",
			TxPower: 14,
		},
	})
	require.NoError(t, err)
	require.True(t, resp.GetSuccess(), "message: %v", resp.GetMessage())

	mode, _ := reader.Get("wireless", "default_radio3", "mode")
	assert.Equal(t, []string{"mesh"}, mode, "mode must be left alone when unspecified")

	meshID, _ := reader.Get("wireless", "default_radio3", "mesh_id")
	assert.Equal(t, []string{"openmanet-mesh"}, meshID)

	ssid, hasSSID := reader.Get("wireless", "default_radio3", "ssid")
	assert.False(t, hasSSID, "stale ssid must be cleared from a mesh iface, got %v", ssid)
}

// The inverse: switching a mesh iface back to AP drops the stale
// mesh_id so the AP section only carries its ssid.
func TestUpdateRadioSettings_APMode_DropsStaleMeshID(t *testing.T) {
	reader := newWifiConfigMockReader()
	svc := newTestWifiConfigService(t)
	svc.ConfigReader = reader

	// Turn default_radio2 into a mesh iface first (radio3 is HaLow and
	// rejects AP mode, so the round trip has to happen on radio2).
	require.NoError(t, reader.SetType("wireless", "default_radio2", "mode", uci.TypeOption, "mesh"))
	require.NoError(t, reader.SetType("wireless", "default_radio2", "mesh_id", uci.TypeOption, "backhaul"))
	require.NoError(t, reader.SetType("wireless", "default_radio2", "network", uci.TypeOption, "batmesh1"))
	require.NoError(t, reader.Del("wireless", "default_radio2", "ssid"))

	resp, err := svc.UpdateRadioSettings(context.Background(), &wificonfigv1.UpdateRadioSettingsRequest{
		RadioName: "radio2",
		Settings: &wificonfigv1.RadioSettings{
			Ssid:       "openmanet",
			Password:   strPtr("testsecret"),
			Channel:    "6",
			Mode:       wificonfigv1.WifiMode_WIFI_MODE_AP,
			Encryption: wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_PSK2,
		},
	})
	require.NoError(t, err)
	require.True(t, resp.GetSuccess(), "message: %v", resp.GetMessage())

	mode, _ := reader.Get("wireless", "default_radio2", "mode")
	assert.Equal(t, []string{"ap"}, mode)

	ssid, _ := reader.Get("wireless", "default_radio2", "ssid")
	assert.Equal(t, []string{"openmanet"}, ssid)

	meshID, hasMeshID := reader.Get("wireless", "default_radio2", "mesh_id")
	assert.False(t, hasMeshID, "stale mesh_id must be cleared from an AP iface, got %v", meshID)
}

// A client that only knows ssid still gets a usable mesh iface: the
// network name lands in mesh_id, never in ssid.
func TestUpdateRadioSettings_MeshMode_SSIDOnlyBecomesMeshID(t *testing.T) {
	reader := newWifiConfigMockReader()
	svc := newTestWifiConfigService(t)
	svc.ConfigReader = reader

	resp, err := svc.UpdateRadioSettings(context.Background(), &wificonfigv1.UpdateRadioSettingsRequest{
		RadioName: "radio2",
		Settings: &wificonfigv1.RadioSettings{
			Ssid:    "backhaul",
			Channel: "6",
			Mode:    wificonfigv1.WifiMode_WIFI_MODE_MESH,
		},
	})
	require.NoError(t, err)
	require.True(t, resp.GetSuccess(), "message: %v", resp.GetMessage())

	meshID, _ := reader.Get("wireless", "default_radio2", "mesh_id")
	assert.Equal(t, []string{"backhaul"}, meshID)

	ssid, hasSSID := reader.Get("wireless", "default_radio2", "ssid")
	assert.False(t, hasSSID, "ssid must not be written on a mesh iface, got %v", ssid)
}

// The committed config has to reach netifd: a UCI commit alone leaves
// the radio on its old settings until reboot.
func TestUpdateRadioSettings_ReloadsServicesAfterCommit(t *testing.T) {
	svc := newTestWifiConfigService(t)
	rl := &fakeRadioReloader{}
	svc.ReloadServices = rl.Reload

	resp, err := svc.UpdateRadioSettings(context.Background(), &wificonfigv1.UpdateRadioSettingsRequest{
		RadioName: "radio2",
		Settings:  &wificonfigv1.RadioSettings{Ssid: "openmanet", Channel: "6"},
	})
	require.NoError(t, err)
	require.True(t, resp.GetSuccess(), "message: %v", resp.GetMessage())
	assert.Equal(t, 1, rl.count())
}

func TestUpdateRadioSettings_ReloadFailureReportsMessage(t *testing.T) {
	svc := newTestWifiConfigService(t)
	rl := &fakeRadioReloader{err: errors.New("reload_config: exit status 1")}
	svc.ReloadServices = rl.Reload

	resp, err := svc.UpdateRadioSettings(context.Background(), &wificonfigv1.UpdateRadioSettingsRequest{
		RadioName: "radio2",
		Settings:  &wificonfigv1.RadioSettings{Ssid: "openmanet", Channel: "6"},
	})
	require.NoError(t, err)
	assert.False(t, resp.GetSuccess())
	assert.Contains(t, resp.GetMessage(), "reload_config: exit status 1")
}

// The reload can drop the operator's own connection, which cancels the
// request context; the reload must still run to completion on a live
// context rather than being killed midway.
func TestUpdateRadioSettings_ReloadSurvivesCancelledContext(t *testing.T) {
	svc := newTestWifiConfigService(t)
	rl := &fakeRadioReloader{}
	svc.ReloadServices = rl.Reload

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	resp, err := svc.UpdateRadioSettings(ctx, &wificonfigv1.UpdateRadioSettingsRequest{
		RadioName: "radio2",
		Settings:  &wificonfigv1.RadioSettings{Ssid: "openmanet", Channel: "6"},
	})
	require.NoError(t, err)
	require.True(t, resp.GetSuccess(), "message: %v", resp.GetMessage())
	assert.Equal(t, 1, rl.count())
	assert.True(t, rl.lastLive, "reload must run on a context detached from the request")
}

func TestUpdateRadioSettings_UnspecifiedModeSkipsHardifGuard(t *testing.T) {
	reader := newWifiConfigMockReader()
	delete(reader.data["network"], "bat0")
	delete(reader.sectionTypes["network"], "bat0")

	svc := newTestWifiConfigService(t)
	svc.ConfigReader = reader

	// A channel-only edit on the AP radio never touches batman.
	resp, err := svc.UpdateRadioSettings(context.Background(), &wificonfigv1.UpdateRadioSettingsRequest{
		RadioName: "radio2",
		Settings:  &wificonfigv1.RadioSettings{Ssid: "openmanet", Channel: "6"},
	})
	require.NoError(t, err)
	assert.True(t, resp.GetSuccess(), "message: %v", resp.GetMessage())
}

// Binding a mesh iface to batmesh1 is useless unless network.batmesh1
// exists as a batadv_hardif on bat0; the wizard creates both, a
// factory image has neither.
func TestUpdateRadioSettings_MeshMode_CreatesMissingBatmeshHardif(t *testing.T) {
	reader := newWifiConfigMockReader()
	svc := newTestWifiConfigService(t)
	svc.ConfigReader = reader

	_, has := reader.Get("wireless", "network", "batmesh1")
	require.False(t, has)

	resp, err := svc.UpdateRadioSettings(context.Background(), &wificonfigv1.UpdateRadioSettingsRequest{
		RadioName: "radio2",
		Settings: &wificonfigv1.RadioSettings{
			Ssid:    "backhaul",
			MeshId:  strPtr("backhaul"),
			Channel: "6",
			Mode:    wificonfigv1.WifiMode_WIFI_MODE_MESH,
		},
	})
	require.NoError(t, err)
	require.True(t, resp.GetSuccess(), "message: %v", resp.GetMessage())

	net, _ := reader.Get("wireless", "default_radio2", "network")
	assert.Equal(t, []string{"batmesh1"}, net)

	proto, _ := reader.Get("network", "batmesh1", "proto")
	assert.Equal(t, []string{"batadv_hardif"}, proto)

	master, _ := reader.Get("network", "batmesh1", "master")
	assert.Equal(t, []string{"bat0"}, master)

	assert.Equal(t, "interface", reader.sectionTypes["network"]["batmesh1"])
}

func TestUpdateRadioSettings_MeshMode_NoBatmanDevice_FailedPrecondition(t *testing.T) {
	reader := newWifiConfigMockReader()
	delete(reader.data["network"], "bat0")
	delete(reader.sectionTypes["network"], "bat0")

	svc := newTestWifiConfigService(t)
	svc.ConfigReader = reader
	rl := &fakeRadioReloader{}
	svc.ReloadServices = rl.Reload

	_, err := svc.UpdateRadioSettings(context.Background(), &wificonfigv1.UpdateRadioSettingsRequest{
		RadioName: "radio2",
		Settings: &wificonfigv1.RadioSettings{
			Ssid:    "backhaul",
			MeshId:  strPtr("backhaul"),
			Channel: "6",
			Mode:    wificonfigv1.WifiMode_WIFI_MODE_MESH,
		},
	})

	var cerr *connect.Error
	require.ErrorAs(t, err, &cerr)
	assert.Equal(t, connect.CodeFailedPrecondition, cerr.Code())
	assert.Contains(t, cerr.Message(), "bat0")

	// Precondition failures must not leave partial writes or reload.
	ch, _ := reader.Get("wireless", "radio2", "channel")
	assert.Equal(t, []string{"1"}, ch, "device config must not be written")

	mode, _ := reader.Get("wireless", "default_radio2", "mode")
	assert.Equal(t, []string{"ap"}, mode)
	assert.Equal(t, 0, rl.count())
}

// secondaryMeshCapableProviders returns iwinfo + wireless status fakes
// that report radio2 as an MT7915 with runtime interface phy2-ap0.
func secondaryMeshCapableProviders(hardware string) (*fakeIwinfoProvider, *fakeWirelessStatusProvider) {
	iw := &fakeIwinfoProvider{infoByDevice: map[string]*iwinfo.InterfaceInfo{
		"phy2-ap0": {Hardware: iwinfo.HardwareInfo{Name: hardware}},
	}}
	ws := &fakeWirelessStatusProvider{status: map[string]*network.WirelessRadioStatus{
		"radio2": {Interfaces: []network.WirelessRadioInterface{{Ifname: "phy2-ap0"}}},
	}}

	return iw, ws
}

func assertSecondaryMeshPolicy(t *testing.T, reader *fakeConfigReader, section string, want bool) {
	t.Helper()

	for _, p := range network.SecondaryMeshPolicyOptions() {
		vals, ok := reader.Get("wireless", section, p.Option)
		if want {
			if !ok || len(vals) != 1 || vals[0] != p.Value {
				t.Errorf("%s: got %v (ok=%v), want %s", p.Option, vals, ok, p.Value)
			}

			continue
		}

		if ok {
			t.Errorf("%s must not be written, got %v", p.Option, vals)
		}
	}
}

func TestUpdateRadioSettings_MeshOnBatmesh1_MT7915_WritesSecondaryPolicy(t *testing.T) {
	reader := newWifiConfigMockReader()
	svc := newTestWifiConfigService(t)
	svc.ConfigReader = reader
	svc.IwinfoClient, svc.WirelessStatus = secondaryMeshCapableProviders("MediaTek MT7915AN")

	// default_radio2 moves from ahwlan to the only free hardif, batmesh1.
	resp, err := svc.UpdateRadioSettings(context.Background(), &wificonfigv1.UpdateRadioSettingsRequest{
		RadioName: "radio2",
		Settings: &wificonfigv1.RadioSettings{
			Ssid:    "mesh-ssid",
			Channel: "6",
			Mode:    wificonfigv1.WifiMode_WIFI_MODE_MESH,
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !resp.GetSuccess() {
		t.Fatalf("expected success, got message: %v", resp.GetMessage())
	}

	assertSecondaryMeshPolicy(t, reader, "default_radio2", true)
}

func TestUpdateRadioSettings_MeshOnBatmesh1_MT7916_WritesSecondaryPolicy(t *testing.T) {
	reader := newWifiConfigMockReader()
	svc := newTestWifiConfigService(t)
	svc.ConfigReader = reader
	svc.IwinfoClient, svc.WirelessStatus = secondaryMeshCapableProviders("MediaTek MT7916")

	resp, err := svc.UpdateRadioSettings(context.Background(), &wificonfigv1.UpdateRadioSettingsRequest{
		RadioName: "radio2",
		Settings:  &wificonfigv1.RadioSettings{Ssid: "mesh-ssid", Channel: "6", Mode: wificonfigv1.WifiMode_WIFI_MODE_MESH},
	})
	if err != nil || !resp.GetSuccess() {
		t.Fatalf("expected success, err=%v msg=%v", err, resp.GetMessage())
	}

	assertSecondaryMeshPolicy(t, reader, "default_radio2", true)
}

func TestUpdateRadioSettings_MeshOnBatmesh1_UnsupportedChipset_SkipsPolicy(t *testing.T) {
	reader := newWifiConfigMockReader()
	svc := newTestWifiConfigService(t)
	svc.ConfigReader = reader
	svc.IwinfoClient, svc.WirelessStatus = secondaryMeshCapableProviders("Qualcomm Atheros QCA9880")

	resp, err := svc.UpdateRadioSettings(context.Background(), &wificonfigv1.UpdateRadioSettingsRequest{
		RadioName: "radio2",
		Settings:  &wificonfigv1.RadioSettings{Ssid: "mesh-ssid", Channel: "6", Mode: wificonfigv1.WifiMode_WIFI_MODE_MESH},
	})
	if err != nil || !resp.GetSuccess() {
		t.Fatalf("expected success, err=%v msg=%v", err, resp.GetMessage())
	}

	vals, ok := reader.Get("wireless", "default_radio2", "network")
	if !ok || vals[0] != "batmesh1" {
		t.Fatalf("radio2 must still bind to batmesh1, got %v (ok=%v)", vals, ok)
	}

	assertSecondaryMeshPolicy(t, reader, "default_radio2", false)
}

func TestUpdateRadioSettings_MeshOnBatmesh0_SkipsPolicy(t *testing.T) {
	reader := newWifiConfigMockReader()
	svc := newTestWifiConfigService(t)
	svc.ConfigReader = reader

	// default_radio3 already sits on batmesh0 (the HaLow primary); the
	// policy is for the secondary link only, whatever the chipset.
	iw := &fakeIwinfoProvider{infoByDevice: map[string]*iwinfo.InterfaceInfo{
		"wlan3": {Hardware: iwinfo.HardwareInfo{Name: "MediaTek MT7915AN"}},
	}}
	svc.IwinfoClient = iw
	svc.WirelessStatus = &fakeWirelessStatusProvider{status: map[string]*network.WirelessRadioStatus{
		"radio3": {Interfaces: []network.WirelessRadioInterface{{Ifname: "wlan3"}}},
	}}

	resp, err := svc.UpdateRadioSettings(context.Background(), &wificonfigv1.UpdateRadioSettingsRequest{
		RadioName: "radio3",
		Settings:  &wificonfigv1.RadioSettings{Ssid: "mesh-ssid", Channel: "42", Mode: wificonfigv1.WifiMode_WIFI_MODE_MESH},
	})
	if err != nil || !resp.GetSuccess() {
		t.Fatalf("expected success, err=%v msg=%v", err, resp.GetMessage())
	}

	assertSecondaryMeshPolicy(t, reader, "default_radio3", false)
}

func TestUpdateRadioSettings_MeshOnBatmesh1_IwinfoError_SkipsPolicyButSucceeds(t *testing.T) {
	reader := newWifiConfigMockReader()
	svc := newTestWifiConfigService(t)
	svc.ConfigReader = reader
	svc.IwinfoClient = &fakeIwinfoProvider{infoErr: errors.New("iwinfo down")}
	_, svc.WirelessStatus = secondaryMeshCapableProviders("MediaTek MT7915AN")

	resp, err := svc.UpdateRadioSettings(context.Background(), &wificonfigv1.UpdateRadioSettingsRequest{
		RadioName: "radio2",
		Settings:  &wificonfigv1.RadioSettings{Ssid: "mesh-ssid", Channel: "6", Mode: wificonfigv1.WifiMode_WIFI_MODE_MESH},
	})
	if err != nil || !resp.GetSuccess() {
		t.Fatalf("a telemetry failure must not fail the mode change, err=%v msg=%v", err, resp.GetMessage())
	}

	assertSecondaryMeshPolicy(t, reader, "default_radio2", false)
}

func TestUpdateRadioSettings_APMode_SkipsPolicy(t *testing.T) {
	reader := newWifiConfigMockReader()
	svc := newTestWifiConfigService(t)
	svc.ConfigReader = reader
	svc.IwinfoClient, svc.WirelessStatus = secondaryMeshCapableProviders("MediaTek MT7915AN")

	resp, err := svc.UpdateRadioSettings(context.Background(), &wificonfigv1.UpdateRadioSettingsRequest{
		RadioName: "radio2",
		Settings:  &wificonfigv1.RadioSettings{Ssid: "test-ap", Channel: "6", Mode: wificonfigv1.WifiMode_WIFI_MODE_AP},
	})
	if err != nil || !resp.GetSuccess() {
		t.Fatalf("expected success, err=%v msg=%v", err, resp.GetMessage())
	}

	assertSecondaryMeshPolicy(t, reader, "default_radio2", false)
}

// meshRSSIFixture returns the standard mock reader with mesh_rssi_threshold
// set on the given wifi-iface section.
func meshRSSIFixture(t *testing.T, section, value string) *fakeConfigReader {
	t.Helper()

	reader := newWifiConfigMockReader()
	require.NoError(t, reader.SetType("wireless", section, "mesh_rssi_threshold", uci.TypeOption, value))

	return reader
}

func TestGetRadioSettings_MeshRSSIThreshold(t *testing.T) {
	svc := newTestWifiConfigService(t)
	svc.ConfigReader = meshRSSIFixture(t, "default_radio3", "-75")

	resp, err := svc.GetRadioSettings(context.Background(), &wificonfigv1.GetRadioSettingsRequest{RadioName: "radio3"})
	require.NoError(t, err)

	settings := resp.GetSettings()
	require.NotNil(t, settings.MeshRssiThreshold, "stored floor must be reported for a mesh iface")
	assert.Equal(t, int32(-75), settings.GetMeshRssiThreshold())
}

func TestGetRadioSettings_MeshRSSIThreshold_AbsentOmitted(t *testing.T) {
	svc := newTestWifiConfigService(t)

	resp, err := svc.GetRadioSettings(context.Background(), &wificonfigv1.GetRadioSettingsRequest{RadioName: "radio3"})
	require.NoError(t, err)
	assert.Nil(t, resp.GetSettings().MeshRssiThreshold, "no UCI option means the field stays unset")
}

func TestGetRadioSettings_MeshRSSIThreshold_UnparsableOmitted(t *testing.T) {
	svc := newTestWifiConfigService(t)
	svc.ConfigReader = meshRSSIFixture(t, "default_radio3", "auto")

	resp, err := svc.GetRadioSettings(context.Background(), &wificonfigv1.GetRadioSettingsRequest{RadioName: "radio3"})
	require.NoError(t, err)
	assert.Nil(t, resp.GetSettings().MeshRssiThreshold, "a non-numeric option is not reported")
}

func TestGetRadioSettings_MeshRSSIThreshold_OverflowOmitted(t *testing.T) {
	svc := newTestWifiConfigService(t)
	svc.ConfigReader = meshRSSIFixture(t, "default_radio3", "2147483648")

	resp, err := svc.GetRadioSettings(context.Background(), &wificonfigv1.GetRadioSettingsRequest{RadioName: "radio3"})
	require.NoError(t, err)
	assert.Nil(t, resp.GetSettings().MeshRssiThreshold, "a value outside int32 is not reported (never wrapped)")
}

func TestGetRadioSettings_TxPowerOverflowReadsAsUnset(t *testing.T) {
	reader := newWifiConfigMockReader()
	require.NoError(t, reader.SetType("wireless", "radio2", "txpower", uci.TypeOption, "2147483648"))

	svc := newTestWifiConfigService(t)
	svc.ConfigReader = reader

	resp, err := svc.GetRadioSettings(context.Background(), &wificonfigv1.GetRadioSettingsRequest{RadioName: "radio2"})
	require.NoError(t, err)
	assert.Equal(t, int32(0), resp.GetSettings().GetTxPower(), "a value outside int32 reads as unset, never wrapped")
}

func TestGetRadioSettings_MeshRSSIThreshold_OutOfRangeReported(t *testing.T) {
	svc := newTestWifiConfigService(t)
	svc.ConfigReader = meshRSSIFixture(t, "default_radio3", "-90")

	resp, err := svc.GetRadioSettings(context.Background(), &wificonfigv1.GetRadioSettingsRequest{RadioName: "radio3"})
	require.NoError(t, err)

	settings := resp.GetSettings()
	require.NotNil(t, settings.MeshRssiThreshold, "the read reports what UCI holds; only updates are range-checked")
	assert.Equal(t, int32(-90), settings.GetMeshRssiThreshold())
}

func TestGetRadioSettings_APMode_OmitsMeshRSSIThreshold(t *testing.T) {
	svc := newTestWifiConfigService(t)
	svc.ConfigReader = meshRSSIFixture(t, "default_radio2", "-80")

	resp, err := svc.GetRadioSettings(context.Background(), &wificonfigv1.GetRadioSettingsRequest{RadioName: "radio2"})
	require.NoError(t, err)
	assert.Nil(t, resp.GetSettings().MeshRssiThreshold, "a stale option on an AP iface is not reported")
}

func TestUpdateRadioSettings_MeshMode_WritesMeshRSSIThreshold(t *testing.T) {
	reader := newWifiConfigMockReader()
	svc := newTestWifiConfigService(t)
	svc.ConfigReader = reader

	resp, err := svc.UpdateRadioSettings(context.Background(), &wificonfigv1.UpdateRadioSettingsRequest{
		RadioName: "radio2",
		Settings: &wificonfigv1.RadioSettings{
			Ssid:              "mesh-ssid",
			Channel:           "6",
			Mode:              wificonfigv1.WifiMode_WIFI_MODE_MESH,
			MeshRssiThreshold: proto.Int32(-75),
		},
	})
	require.NoError(t, err)
	require.True(t, resp.GetSuccess(), resp.GetMessage())

	vals, ok := reader.Get("wireless", "default_radio2", "mesh_rssi_threshold")
	require.True(t, ok, "mesh_rssi_threshold must be written on the mesh iface")
	assert.Equal(t, []string{"-75"}, vals)
}

func TestUpdateRadioSettings_MeshIface_UnspecifiedMode_WritesMeshRSSIThreshold(t *testing.T) {
	reader := newWifiConfigMockReader()
	svc := newTestWifiConfigService(t)
	svc.ConfigReader = reader

	// Make default_radio2 a stored mesh iface first, then send an
	// update without Mode: the effective mode still resolves to mesh
	// from UCI, so the floor is staged.
	require.NoError(t, reader.SetType("wireless", "default_radio2", "mode", uci.TypeOption, "mesh"))

	resp, err := svc.UpdateRadioSettings(context.Background(), &wificonfigv1.UpdateRadioSettingsRequest{
		RadioName: "radio2",
		Settings: &wificonfigv1.RadioSettings{
			Ssid:              "mesh-ssid",
			Channel:           "6",
			MeshRssiThreshold: proto.Int32(-70),
		},
	})
	require.NoError(t, err)
	require.True(t, resp.GetSuccess(), resp.GetMessage())

	vals, ok := reader.Get("wireless", "default_radio2", "mesh_rssi_threshold")
	require.True(t, ok, "effective mode comes from UCI when the request omits it")
	assert.Equal(t, []string{"-70"}, vals)
}

func TestUpdateRadioSettings_MeshMode_UnsetLeavesMeshRSSIThreshold(t *testing.T) {
	reader := meshRSSIFixture(t, "default_radio3", "-80")
	svc := newTestWifiConfigService(t)
	svc.ConfigReader = reader

	resp, err := svc.UpdateRadioSettings(context.Background(), &wificonfigv1.UpdateRadioSettingsRequest{
		RadioName: "radio3",
		Settings: &wificonfigv1.RadioSettings{
			Ssid:    "mesh-ssid",
			Channel: "44",
			Mode:    wificonfigv1.WifiMode_WIFI_MODE_MESH,
		},
	})
	require.NoError(t, err)
	require.True(t, resp.GetSuccess(), resp.GetMessage())

	vals, ok := reader.Get("wireless", "default_radio3", "mesh_rssi_threshold")
	require.True(t, ok, "an update without the field must not remove the option")
	assert.Equal(t, []string{"-80"}, vals)
}

func TestUpdateRadioSettings_APMode_IgnoresMeshRSSIThreshold(t *testing.T) {
	reader := newWifiConfigMockReader()
	svc := newTestWifiConfigService(t)
	svc.ConfigReader = reader

	resp, err := svc.UpdateRadioSettings(context.Background(), &wificonfigv1.UpdateRadioSettingsRequest{
		RadioName: "radio2",
		Settings: &wificonfigv1.RadioSettings{
			Ssid:              "openmanet",
			Channel:           "6",
			Mode:              wificonfigv1.WifiMode_WIFI_MODE_AP,
			MeshRssiThreshold: proto.Int32(-75),
		},
	})
	require.NoError(t, err)
	require.True(t, resp.GetSuccess(), resp.GetMessage())

	_, ok := reader.Get("wireless", "default_radio2", "mesh_rssi_threshold")
	assert.False(t, ok, "the floor is meaningless on an AP iface and must not be written")
}

func TestUpdateRadioSettings_MorseRadio_IgnoresMeshRSSIThreshold(t *testing.T) {
	reader := newWifiConfigMockReader()
	svc := newTestWifiConfigService(t)
	svc.ConfigReader = reader

	resp, err := svc.UpdateRadioSettings(context.Background(), &wificonfigv1.UpdateRadioSettingsRequest{
		RadioName: "radio3",
		Settings: &wificonfigv1.RadioSettings{
			Ssid:              "mesh-ssid",
			Channel:           "42",
			Mode:              wificonfigv1.WifiMode_WIFI_MODE_MESH,
			MeshRssiThreshold: proto.Int32(-75),
		},
	})
	require.NoError(t, err)
	require.True(t, resp.GetSuccess(), resp.GetMessage())

	_, ok := reader.Get("wireless", "default_radio3", "mesh_rssi_threshold")
	assert.False(t, ok, "the HaLow floor is not operator-tunable; the field is ignored on a morse radio")
}

func TestUpdateRadioSettings_MeshToAPOrSTA_ClearsMeshOnlyOptions(t *testing.T) {
	modes := map[string]wificonfigv1.WifiMode{
		"ap":  wificonfigv1.WifiMode_WIFI_MODE_AP,
		"sta": wificonfigv1.WifiMode_WIFI_MODE_STA,
	}

	for name, mode := range modes {
		t.Run(name, func(t *testing.T) {
			reader := newWifiConfigMockReader()

			// default_radio2 lived a mesh life: hardif binding plus every
			// mesh-only option the daemon writes.
			seed := map[string]string{
				"mode":                "mesh",
				"network":             "batmesh1",
				"mesh_id":             "old-mesh",
				"mesh_fwding":         "0",
				"mesh_rssi_threshold": "-80",
			}
			for _, p := range network.SecondaryMeshPolicyOptions() {
				seed[p.Option] = p.Value
			}

			for k, v := range seed {
				require.NoError(t, reader.SetType("wireless", "default_radio2", k, uci.TypeOption, v))
			}

			svc := newTestWifiConfigService(t)
			svc.ConfigReader = reader

			resp, err := svc.UpdateRadioSettings(context.Background(), &wificonfigv1.UpdateRadioSettingsRequest{
				RadioName: "radio2",
				Settings: &wificonfigv1.RadioSettings{
					Ssid:    "openmanet",
					Channel: "6",
					Mode:    mode,
				},
			})
			require.NoError(t, err)
			require.True(t, resp.GetSuccess(), resp.GetMessage())

			for _, opt := range []string{
				"mesh_id", "mesh_fwding", "mesh_rssi_threshold",
				"mcast_rate", "mesh_nolearn", "mesh_retry_timeout", "mesh_confirm_timeout", "mesh_holding_timeout",
			} {
				_, ok := reader.Get("wireless", "default_radio2", opt)
				assert.False(t, ok, "%s must be cleared when the iface leaves mesh mode", opt)
			}

			vals, ok := reader.Get("wireless", "default_radio2", "network")
			require.True(t, ok)
			assert.Equal(t, []string{"ahwlan"}, vals)

			vals, ok = reader.Get("wireless", "default_radio2", "ssid")
			require.True(t, ok)
			assert.Equal(t, []string{"openmanet"}, vals)
		})
	}
}
