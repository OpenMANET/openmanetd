package mgmt

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/digineo/go-uci/v2"
	"github.com/openmanet/openmanetd/internal/iwinfo"
	"github.com/openmanet/openmanetd/internal/network"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── fakeOpenMANETReader ──────────────────────────────────────────────────────

// fakeOpenMANETReader implements network.OpenMANETConfigReader backed by an
// in-memory map. It allows tests to seed data and inject errors.
type fakeOpenMANETReader struct {
	data        map[string]map[string]map[string][]string // config→section→option→values
	sections    map[string]map[string]string              // config→section→type
	commitErr   error
	setTypeErr  error
	commitCalls int
}

func newFakeOpenMANETReader() *fakeOpenMANETReader {
	return &fakeOpenMANETReader{
		data:     make(map[string]map[string]map[string][]string),
		sections: make(map[string]map[string]string),
	}
}

func (f *fakeOpenMANETReader) Get(config, section, option string) ([]string, bool) {
	if f.data[config] == nil {
		return nil, false
	}

	if f.data[config][section] == nil {
		return nil, false
	}

	v, ok := f.data[config][section][option]

	return v, ok
}

func (f *fakeOpenMANETReader) GetSections(config, secType string) ([]string, error) {
	var out []string

	if f.sections[config] != nil {
		for s, t := range f.sections[config] {
			if t == secType {
				out = append(out, s)
			}
		}
	}

	return out, nil
}

func (f *fakeOpenMANETReader) SetType(config, section, option string, _ uci.OptionType, values ...string) error {
	if f.setTypeErr != nil {
		return f.setTypeErr
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

func (f *fakeOpenMANETReader) Del(config, section, option string) error {
	if f.data[config] != nil && f.data[config][section] != nil {
		delete(f.data[config][section], option)
	}

	return nil
}

func (f *fakeOpenMANETReader) AddSection(config, section, typ string) error {
	if f.sections[config] == nil {
		f.sections[config] = make(map[string]string)
	}

	f.sections[config][section] = typ
	if f.data[config] == nil {
		f.data[config] = make(map[string]map[string][]string)
	}

	if f.data[config][section] == nil {
		f.data[config][section] = make(map[string][]string)
	}

	return nil
}

func (f *fakeOpenMANETReader) DelSection(config, section string) error {
	if f.data[config] != nil {
		delete(f.data[config], section)
	}

	if f.sections[config] != nil {
		delete(f.sections[config], section)
	}

	return nil
}

func (f *fakeOpenMANETReader) Commit() error {
	f.commitCalls++

	return f.commitErr
}

func (f *fakeOpenMANETReader) ReloadConfig() error {
	return nil
}

// seedBatMesh1Configured seeds batmesh1configured with the given value ("0"/"1").
func (f *fakeOpenMANETReader) seedBatMesh1Configured(value string) {
	_ = f.AddSection("openmanetd", "config", "openmanet")
	_ = f.SetType("openmanetd", "config", "batmesh1configured", uci.TypeOption, value)
}

// ── fakeWirelessReader ───────────────────────────────────────────────────────

// fakeWirelessReader implements network.ConfigReader backed by an in-memory map
// with named sections only (no anonymous-section handling needed for these tests).
type fakeWirelessReader struct {
	data         map[string]map[string]map[string][]string // config→section→option→values
	sectionTypes map[string]map[string]string              // config→section→type
	setTypeErr   error
	commitErr    error
	commitCalls  int
}

func newFakeWirelessReader() *fakeWirelessReader {
	return &fakeWirelessReader{
		data:         make(map[string]map[string]map[string][]string),
		sectionTypes: make(map[string]map[string]string),
	}
}

func (f *fakeWirelessReader) Get(config, section, option string) ([]string, bool) {
	if f.data[config] == nil {
		return nil, false
	}

	if f.data[config][section] == nil {
		return nil, false
	}

	v, ok := f.data[config][section][option]

	return v, ok
}

func (f *fakeWirelessReader) GetSections(config, secType string) ([]string, error) {
	var out []string

	if f.sectionTypes[config] != nil {
		for s, t := range f.sectionTypes[config] {
			if t == secType {
				out = append(out, s)
			}
		}
	}

	return out, nil
}

func (f *fakeWirelessReader) SetType(config, section, option string, _ uci.OptionType, values ...string) error {
	if f.setTypeErr != nil {
		return f.setTypeErr
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

func (f *fakeWirelessReader) Del(config, section, option string) error {
	if f.data[config] != nil && f.data[config][section] != nil {
		delete(f.data[config][section], option)
	}

	return nil
}

func (f *fakeWirelessReader) AddSection(config, section, typ string) error {
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

func (f *fakeWirelessReader) DelSection(config, section string) error {
	if f.data[config] != nil {
		delete(f.data[config], section)
	}

	if f.sectionTypes[config] != nil {
		delete(f.sectionTypes[config], section)
	}

	return nil
}

func (f *fakeWirelessReader) Commit() error {
	f.commitCalls++

	return f.commitErr
}

func (f *fakeWirelessReader) ReloadConfig() error {
	return nil
}

// seedWifiDevice seeds a named wifi-device section.
func (f *fakeWirelessReader) seedWifiDevice(section, band, channel, htmode string) {
	_ = f.AddSection("wireless", section, "wifi-device")
	if band != "" {
		_ = f.SetType("wireless", section, "band", uci.TypeOption, band)
	}

	if channel != "" {
		_ = f.SetType("wireless", section, "channel", uci.TypeOption, channel)
	}

	if htmode != "" {
		_ = f.SetType("wireless", section, "htmode", uci.TypeOption, htmode)
	}
}

// seedMeshIface seeds a named wifi-iface section with mode=mesh.
func (f *fakeWirelessReader) seedMeshIface(section, device, meshID, key string) {
	_ = f.AddSection("wireless", section, "wifi-iface")
	_ = f.SetType("wireless", section, "device", uci.TypeOption, device)
	_ = f.SetType("wireless", section, "mode", uci.TypeOption, "mesh")
	_ = f.SetType("wireless", section, "mesh_id", uci.TypeOption, meshID)
	_ = f.SetType("wireless", section, "key", uci.TypeOption, key)
	_ = f.SetType("wireless", section, "encryption", uci.TypeOption, "sae")
}

// seedAPIface seeds a named wifi-iface section with mode=ap bound to
// ahwlan; disabled is written only when non-empty.
func (f *fakeWirelessReader) seedAPIface(section, device, disabled string) {
	_ = f.AddSection("wireless", section, "wifi-iface")
	_ = f.SetType("wireless", section, "device", uci.TypeOption, device)
	_ = f.SetType("wireless", section, "mode", uci.TypeOption, "ap")
	_ = f.SetType("wireless", section, "network", uci.TypeOption, "ahwlan")
	_ = f.SetType("wireless", section, "ssid", uci.TypeOption, "client-wifi")

	if disabled != "" {
		_ = f.SetType("wireless", section, "disabled", uci.TypeOption, disabled)
	}
}

// ── fakeIwinfo ───────────────────────────────────────────────────────────────

// fakeIwinfo implements iwinfo.IwinfoProvider for test purposes.
type fakeIwinfo struct {
	infoMap    map[string]*iwinfo.InterfaceInfo
	infoMapErr error
}

func (f *fakeIwinfo) GetDevices(_ context.Context) ([]string, error) {
	var keys []string
	for k := range f.infoMap {
		keys = append(keys, k)
	}

	return keys, nil
}

func (f *fakeIwinfo) GetInfo(_ context.Context, device string) (*iwinfo.InterfaceInfo, error) {
	if f.infoMapErr != nil {
		return nil, f.infoMapErr
	}

	info, ok := f.infoMap[device]
	if !ok {
		return nil, errors.New("device not found")
	}

	return info, nil
}

func (f *fakeIwinfo) GetInfoForAll(_ context.Context) (map[string]*iwinfo.InterfaceInfo, error) {
	if f.infoMapErr != nil {
		return nil, f.infoMapErr
	}

	return f.infoMap, nil
}

// makeIwinfoWithHardware creates a fakeIwinfo with a single device whose
// Hardware.Name is set to the given value.
func makeIwinfoWithHardware(device, hardwareName string) *fakeIwinfo {
	return &fakeIwinfo{
		infoMap: map[string]*iwinfo.InterfaceInfo{
			device: {
				Hardware: iwinfo.HardwareInfo{Name: hardwareName},
			},
		},
	}
}

// ── helper ───────────────────────────────────────────────────────────────────

// newTestManagementConfig returns a minimal ManagementConfig suitable for unit
// tests. The logger is discarded so tests stay silent.
func newTestManagementConfig() *ManagementConfig {
	return &ManagementConfig{
		Log: zerolog.Nop(),
	}
}

// ── tests ────────────────────────────────────────────────────────────────────

func TestSetTransportInterfaceMTU_NilWirelessConfigReturnsError(t *testing.T) {
	m := newTestManagementConfig()
	m.WirelessConfig = nil

	// NewManager keeps running when nl80211 init fails; Start must not
	// dereference the nil client on the way to the MTU pass.
	err := m.setTransportInterfaceMTU()
	if err == nil {
		t.Fatal("expected an error for a nil WirelessConfig, got nil")
	}
}

func TestSetupBatMesh1Interface_AlreadyConfigured(t *testing.T) {
	m := newTestManagementConfig()
	openmanet := newFakeOpenMANETReader()
	openmanet.seedBatMesh1Configured("1")

	wireless := newFakeWirelessReader()
	iw := &fakeIwinfo{} // no calls expected

	err := m.setupBatMesh1InterfaceWithDeps(context.Background(), openmanet, wireless, iw, nil)
	if err != nil {
		t.Fatalf("expected nil for already-configured, got %v", err)
	}

	// Wireless reader must not have been written to.
	if wireless.commitCalls > 0 {
		t.Error("expected no wireless commits when already configured")
	}
}

func TestSetupBatMesh1Interface_OpenMANETCheckError(t *testing.T) {
	m := newTestManagementConfig()
	openmanet := newFakeOpenMANETReader()
	// Seed an invalid value to cause a parse error inside IsBatMesh1ConfiguredWithReader.
	_ = openmanet.AddSection("openmanetd", "config", "openmanet")
	_ = openmanet.SetType("openmanetd", "config", "batmesh1configured", uci.TypeOption, "invalid")

	wireless := newFakeWirelessReader()
	iw := &fakeIwinfo{}

	err := m.setupBatMesh1InterfaceWithDeps(context.Background(), openmanet, wireless, iw, nil)
	if err == nil {
		t.Fatal("expected error from invalid batmesh1configured value")
	}

	if !strings.Contains(err.Error(), "check batmesh1 configured") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestSetupBatMesh1Interface_IwinfoError(t *testing.T) {
	m := newTestManagementConfig()
	openmanet := newFakeOpenMANETReader()
	openmanet.seedBatMesh1Configured("0")

	wireless := newFakeWirelessReader()
	iw := &fakeIwinfo{infoMapErr: errors.New("ubus not available")}

	err := m.setupBatMesh1InterfaceWithDeps(context.Background(), openmanet, wireless, iw, nil)
	if err == nil {
		t.Fatal("expected error when iwinfo fails")
	}

	if !strings.Contains(err.Error(), "get iwinfo for all devices") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestSetupBatMesh1Interface_WirelessStatusError(t *testing.T) {
	m := newTestManagementConfig()
	openmanet := newFakeOpenMANETReader()
	openmanet.seedBatMesh1Configured("0")

	wireless := newFakeWirelessReader()
	status := &fakeWirelessStatusProvider{Err: errors.New("ubus unavailable")}

	err := m.setupBatMesh1InterfaceWithDeps(context.Background(), openmanet, wireless, &fakeIwinfo{}, status)
	require.NoError(t, err)
	assert.Zero(t, wireless.commitCalls)
	assert.Zero(t, openmanet.commitCalls)
}

func TestSetupBatMesh1Interface_DeviceSectionsError(t *testing.T) {
	m := newTestManagementConfig()
	openmanet := newFakeOpenMANETReader()
	openmanet.seedBatMesh1Configured("0")

	wireless := newFakeWirelessReader()
	reader := &sectionErrorReader{
		ConfigReader: wireless,
		sectionType:  "wifi-device",
		err:          errors.New("read devices"),
	}

	err := m.setupBatMesh1InterfaceWithDeps(context.Background(), openmanet, reader, &fakeIwinfo{}, &fakeWirelessStatusProvider{})
	require.Error(t, err)
	assert.ErrorContains(t, err, "get wifi-device sections")
}

func TestSetupBatMesh1Interface_HardwareMatch_MT7915(t *testing.T) {
	m := newTestManagementConfig()
	openmanet := newFakeOpenMANETReader()
	openmanet.seedBatMesh1Configured("0")

	wireless := newFakeWirelessReader()
	wireless.seedWifiDevice("radio1", "2g", "6", "HT20")
	// No mesh iface seeded — expect it to fail past the hardware check.
	iw := makeIwinfoWithHardware("wlh0", "MediaTek MT7915AN")

	err := m.setupBatMesh1InterfaceWithDeps(context.Background(), openmanet, wireless, iw, wirelessStatusForRadio(t, "radio1", "wlh0"))
	// Should fail at "no mesh iface", not at "no supported hardware".
	if err == nil {
		t.Fatal("expected error after hardware check (no mesh iface)")
	}

	if strings.Contains(err.Error(), "no supported hardware") {
		t.Errorf("hardware check should have passed for MT7915, got: %v", err)
	}
}

func TestSetupBatMesh1Interface_HardwareMatch_MT7916(t *testing.T) {
	m := newTestManagementConfig()
	openmanet := newFakeOpenMANETReader()
	openmanet.seedBatMesh1Configured("0")

	wireless := newFakeWirelessReader()
	wireless.seedWifiDevice("radio1", "2g", "6", "HT20")

	iw := makeIwinfoWithHardware("wlh0", "MediaTek MT7916AN")

	err := m.setupBatMesh1InterfaceWithDeps(context.Background(), openmanet, wireless, iw, wirelessStatusForRadio(t, "radio1", "wlh0"))
	// Should fail at "no mesh iface", not at "no supported hardware".
	if err == nil {
		t.Fatal("expected error after hardware check (no mesh iface)")
	}

	if strings.Contains(err.Error(), "no supported hardware") {
		t.Errorf("hardware check should have passed for MT7916, got: %v", err)
	}
}

func TestSetupBatMesh1Interface_NoMeshIface(t *testing.T) {
	m := newTestManagementConfig()
	openmanet := newFakeOpenMANETReader()
	openmanet.seedBatMesh1Configured("0")

	wireless := newFakeWirelessReader()
	// Seed only a non-mesh iface.
	_ = wireless.AddSection("wireless", "default_radio0", "wifi-iface")
	_ = wireless.SetType("wireless", "default_radio0", "mode", uci.TypeOption, "ap")
	wireless.seedWifiDevice("radio1", "2g", "6", "HT20")

	iw := makeIwinfoWithHardware("wlh0", "MediaTek MT7915AN")

	err := m.setupBatMesh1InterfaceWithDeps(context.Background(), openmanet, wireless, iw, wirelessStatusForRadio(t, "radio1", "wlh0"))
	if err == nil {
		t.Fatal("expected error when no mesh iface found")
	}

	if !strings.Contains(err.Error(), "no existing wifi-iface with mode=mesh") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestSetupBatMesh1Interface_IfaceSectionsError(t *testing.T) {
	m := newTestManagementConfig()
	openmanet := newFakeOpenMANETReader()
	openmanet.seedBatMesh1Configured("0")

	wireless := newFakeWirelessReader()
	wireless.seedWifiDevice("radio1", "2g", "6", "HT20")
	reader := &sectionErrorReader{
		ConfigReader: wireless,
		sectionType:  "wifi-iface",
		err:          errors.New("read interfaces"),
	}
	iw := makeIwinfoWithHardware("wlh0", "MediaTek MT7915AN")

	err := m.setupBatMesh1InterfaceWithDeps(context.Background(), openmanet, reader, iw, wirelessStatusForRadio(t, "radio1", "wlh0"))
	require.Error(t, err)
	assert.ErrorContains(t, err, "get wifi-iface sections")
}

func TestSetupBatMesh1Interface_No2gDevice(t *testing.T) {
	m := newTestManagementConfig()
	openmanet := newFakeOpenMANETReader()
	openmanet.seedBatMesh1Configured("0")

	wireless := newFakeWirelessReader()
	wireless.seedMeshIface("existing_mesh0", "radio4", "halowmesh", "secretkey")
	// Only a non-2g device.
	wireless.seedWifiDevice("radio4", "s1g", "42", "")

	iw := makeIwinfoWithHardware("wlh0", "MediaTek MT7915AN")

	err := m.setupBatMesh1InterfaceWithDeps(context.Background(), openmanet, wireless, iw, wirelessStatusForRadio(t, "radio4", "wlh0"))
	if err != nil {
		t.Fatalf("expected safe no-op when no supported 2.4 GHz radio exists, got %v", err)
	}

	if wireless.commitCalls != 0 || openmanet.commitCalls != 0 {
		t.Fatal("unsupported radio configuration was modified")
	}
}

func TestSetupBatMesh1Interface_Success(t *testing.T) {
	m := newTestManagementConfig()
	openmanet := newFakeOpenMANETReader()
	openmanet.seedBatMesh1Configured("0")

	wireless := newFakeWirelessReader()
	wireless.seedMeshIface("existing_mesh0", "radio4", "halowmesh", "secretkey999")
	wireless.seedWifiDevice("radio1", "2g", "6", "HT20")

	iw := makeIwinfoWithHardware("wlh0", "MediaTek MT7915AN")

	err := m.setupBatMesh1InterfaceWithDeps(context.Background(), openmanet, wireless, iw, wirelessStatusForRadio(t, "radio1", "wlh0"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify new wifi-iface was created as "batmesh1_radio1".
	newIface, ierr := network.GetWirelessIfaceByNameWithReader("batmesh1_radio1", wireless)
	if ierr != nil {
		t.Fatalf("failed to read back new iface: %v", ierr)
	}

	if newIface.Device != "radio1" {
		t.Errorf("Device: got %q, want %q", newIface.Device, "radio1")
	}

	if newIface.Network != "batmesh1" {
		t.Errorf("Network: got %q, want %q", newIface.Network, "batmesh1")
	}

	if newIface.Mode != "mesh" {
		t.Errorf("Mode: got %q, want %q", newIface.Mode, "mesh")
	}

	if newIface.MeshID != "halowmesh" {
		t.Errorf("MeshID: got %q, want %q", newIface.MeshID, "halowmesh")
	}

	if newIface.Key != "secretkey999" {
		t.Errorf("Key: got %q, want %q", newIface.Key, "secretkey999")
	}

	if newIface.MeshFwding != "0" {
		t.Errorf("MeshFwding: got %q, want %q", newIface.MeshFwding, "0")
	}

	if newIface.MeshRSSIThreshold != "-80" {
		t.Errorf("MeshRSSIThreshold: got %q, want %q", newIface.MeshRSSIThreshold, "-80")
	}

	if newIface.Encryption != "sae" {
		t.Errorf("Encryption: got %q, want %q", newIface.Encryption, "sae")
	}

	for _, p := range network.SecondaryMeshPolicyOptions() {
		vals, ok := wireless.Get("wireless", "batmesh1_radio1", p.Option)
		if !ok || len(vals) != 1 || vals[0] != p.Value {
			t.Errorf("%s: got %v (ok=%v), want %s", p.Option, vals, ok, p.Value)
		}
	}

	// Verify radio1 was updated.
	radio, rerr := network.GetWirelessDeviceByNameWithReader("radio1", wireless)
	if rerr != nil {
		t.Fatalf("failed to read back radio1: %v", rerr)
	}

	if radio.Channel != "8" {
		t.Errorf("Channel: got %q, want %q", radio.Channel, "8")
	}

	if radio.HTMode != "HE40" {
		t.Errorf("HTMode: got %q, want %q", radio.HTMode, "HE40")
	}

	// Verify band=2g is preserved (was set during seed).
	if radio.Band != "2g" {
		t.Errorf("Band should be preserved: got %q, want %q", radio.Band, "2g")
	}

	// Verify batmesh1configured was set to 1.
	configured, cerr := network.IsBatMesh1ConfiguredWithReader(openmanet)
	if cerr != nil {
		t.Fatalf("IsBatMesh1ConfiguredWithReader failed: %v", cerr)
	}

	if !configured {
		t.Error("expected batmesh1configured to be true after success")
	}
}

// TestSetupBatMesh1Interface_ClearsStaleDisabledOnLinkSection covers the
// wizard-rerun scenario: a prior wizard run created batmesh1_radio1, a
// later wizard re-run's reset phase disabled every wifi-iface (including
// batmesh1_radio1) and no backhaul was re-chosen, and now the daemon's
// boot-time fallback rewrites batmesh1_radio1. SetWirelessIfaceConfigWithReader
// only writes non-empty struct fields, so the stale disabled=1 would
// otherwise survive the rewrite and the daemon would mark batmesh1 as
// configured over a permanently-disabled link.
func TestSetupBatMesh1Interface_ClearsStaleDisabledOnLinkSection(t *testing.T) {
	m := newTestManagementConfig()
	openmanet := newFakeOpenMANETReader()
	openmanet.seedBatMesh1Configured("0")

	wireless := newFakeWirelessReader()
	wireless.seedMeshIface("existing_mesh0", "radio4", "halowmesh", "secretkey999")
	wireless.seedWifiDevice("radio1", "2g", "6", "HT20")

	// Pre-existing stale link section from a prior wizard run, left
	// disabled by a subsequent wizard reset.
	require.NoError(t, wireless.AddSection("wireless", "batmesh1_radio1", "wifi-iface"))
	require.NoError(t, wireless.SetType("wireless", "batmesh1_radio1", "disabled", uci.TypeOption, "1"))

	iw := makeIwinfoWithHardware("wlh0", "MediaTek MT7915AN")

	err := m.setupBatMesh1InterfaceWithDeps(context.Background(), openmanet, wireless, iw, wirelessStatusForRadio(t, "radio1", "wlh0"))
	require.NoError(t, err)

	newIface, ierr := network.GetWirelessIfaceByNameWithReader("batmesh1_radio1", wireless)
	require.NoError(t, ierr)

	assert.Empty(t, newIface.Disabled, "stale disabled=1 must be cleared on rewrite")
	assert.Equal(t, "batmesh1", newIface.Network)
	assert.Equal(t, "mesh", newIface.Mode)
}

func TestSetupBatMesh1Interface_doesNotTouchUnrelated2gRadio(t *testing.T) {
	m := newTestManagementConfig()
	openmanet := newFakeOpenMANETReader()
	openmanet.seedBatMesh1Configured("0")

	wireless := newFakeWirelessReader()
	wireless.seedMeshIface("existing_mesh0", "radio4", "halowmesh", "secretkey")
	wireless.seedWifiDevice("radio0", "2g", "1", "HT20")
	wireless.seedWifiDevice("radio1", "2g", "6", "HT20")

	iw := &fakeIwinfo{infoMap: map[string]*iwinfo.InterfaceInfo{
		"phy0-ap0":   {Hardware: iwinfo.HardwareInfo{Name: "Broadcom BCM43430"}},
		"phy1-mesh0": {Hardware: iwinfo.HardwareInfo{Name: "MediaTek MT7915AN"}},
	}}
	status := &fakeWirelessStatusProvider{Status: map[string]*network.WirelessRadioStatus{
		"radio0": {Interfaces: []network.WirelessRadioInterface{{Ifname: "phy0-ap0"}}},
		"radio1": {Interfaces: []network.WirelessRadioInterface{{Ifname: "phy1-mesh0"}}},
	}}

	require.NoError(t, m.setupBatMesh1InterfaceWithDeps(context.Background(), openmanet, wireless, iw, status))

	_, createdOnboardMesh := wireless.Get("wireless", "batmesh1_radio0", "device")
	assert.False(t, createdOnboardMesh, "must not create batmesh1 on the unrelated onboard radio")

	radio0, err := network.GetWirelessDeviceByNameWithReader("radio0", wireless)
	require.NoError(t, err)
	assert.Equal(t, "1", radio0.Channel)
	assert.Equal(t, "HT20", radio0.HTMode)
	assert.Empty(t, radio0.Disabled)

	iface, err := network.GetWirelessIfaceByNameWithReader("batmesh1_radio1", wireless)
	require.NoError(t, err)
	assert.Equal(t, "radio1", iface.Device)
}

func TestSetupBatMesh1Interface_MultipleSupportedRadiosIsSafeNoOp(t *testing.T) {
	m := newTestManagementConfig()
	openmanet := newFakeOpenMANETReader()
	openmanet.seedBatMesh1Configured("0")

	wireless := newFakeWirelessReader()
	wireless.seedWifiDevice("radio1", "2g", "1", "HT20")
	wireless.seedWifiDevice("radio2", "2g", "6", "HT20")

	iw := &fakeIwinfo{infoMap: map[string]*iwinfo.InterfaceInfo{
		"phy1": {Hardware: iwinfo.HardwareInfo{Name: "MediaTek MT7915AN"}},
		"phy2": {Hardware: iwinfo.HardwareInfo{Name: "MediaTek MT7916AN"}},
	}}
	status := &fakeWirelessStatusProvider{Status: map[string]*network.WirelessRadioStatus{
		"radio1": {Interfaces: []network.WirelessRadioInterface{{Ifname: "phy1"}}},
		"radio2": {Interfaces: []network.WirelessRadioInterface{{Ifname: "phy2"}}},
	}}

	require.NoError(t, m.setupBatMesh1InterfaceWithDeps(context.Background(), openmanet, wireless, iw, status))
	assert.Zero(t, wireless.commitCalls)
	assert.Zero(t, openmanet.commitCalls)
}

func TestSetupBatMesh1Interface_SetIfaceError(t *testing.T) {
	m := newTestManagementConfig()
	openmanet := newFakeOpenMANETReader()
	openmanet.seedBatMesh1Configured("0")

	wireless := newFakeWirelessReader()
	wireless.seedMeshIface("existing_mesh0", "radio4", "halowmesh", "secretkey")
	wireless.seedWifiDevice("radio1", "2g", "6", "HT20")
	wireless.setTypeErr = errors.New("UCI write failure")

	iw := makeIwinfoWithHardware("wlh0", "MediaTek MT7915AN")

	err := m.setupBatMesh1InterfaceWithDeps(context.Background(), openmanet, wireless, iw, wirelessStatusForRadio(t, "radio1", "wlh0"))
	if err == nil {
		t.Fatal("expected error when SetWirelessIface fails")
	}

	if !strings.Contains(err.Error(), "create wifi-iface") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestSetupBatMesh1Interface_SetDeviceError(t *testing.T) {
	m := newTestManagementConfig()
	openmanet := newFakeOpenMANETReader()
	openmanet.seedBatMesh1Configured("0")

	wireless := newFakeWirelessReader()
	wireless.seedMeshIface("existing_mesh0", "radio4", "halowmesh", "secretkey")
	wireless.seedWifiDevice("radio1", "2g", "6", "HT20")

	// Inject commit error so the iface creation succeeds (SetType), but the
	// device update commit fails.
	wireless.commitErr = errors.New("commit failure")

	iw := makeIwinfoWithHardware("wlh0", "MediaTek MT7915AN")

	err := m.setupBatMesh1InterfaceWithDeps(context.Background(), openmanet, wireless, iw, wirelessStatusForRadio(t, "radio1", "wlh0"))
	if err == nil {
		t.Fatal("expected error when Commit fails")
	}

	// Error should relate to either iface or device update.
	if !strings.Contains(err.Error(), "create wifi-iface") && !strings.Contains(err.Error(), "update wifi-device") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestSetupBatMesh1Interface_SetConfiguredError(t *testing.T) {
	m := newTestManagementConfig()
	openmanet := newFakeOpenMANETReader()
	openmanet.seedBatMesh1Configured("0")
	openmanet.commitErr = errors.New("openmanet commit failure")

	wireless := newFakeWirelessReader()
	wireless.seedMeshIface("existing_mesh0", "radio4", "halowmesh", "secretkey")
	wireless.seedWifiDevice("radio1", "2g", "6", "HT20")

	iw := makeIwinfoWithHardware("wlh0", "MediaTek MT7915AN")

	err := m.setupBatMesh1InterfaceWithDeps(context.Background(), openmanet, wireless, iw, wirelessStatusForRadio(t, "radio1", "wlh0"))
	if err == nil {
		t.Fatal("expected error when openmanet Commit fails")
	}

	if !strings.Contains(err.Error(), "mark batmesh1 configured") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestSetupBatMesh1Interface_IdempotentWhenAlreadyConfigured(t *testing.T) {
	m := newTestManagementConfig()
	openmanet := newFakeOpenMANETReader()
	openmanet.seedBatMesh1Configured("1")

	wireless := newFakeWirelessReader()
	// Poison the wireless reader to ensure it is never touched.
	wireless.setTypeErr = errors.New("should not be called")

	iw := &fakeIwinfo{infoMapErr: errors.New("should not be called")}

	err := m.setupBatMesh1InterfaceWithDeps(context.Background(), openmanet, wireless, iw, nil)
	if err != nil {
		t.Fatalf("expected nil for already-configured (idempotency check), got %v", err)
	}
}

func TestSetupBatMesh1Interface_LeavesEnabledAPAlone(t *testing.T) {
	m := newTestManagementConfig()
	openmanet := newFakeOpenMANETReader()
	openmanet.seedBatMesh1Configured("0")

	wireless := newFakeWirelessReader()
	wireless.seedMeshIface("existing_mesh0", "radio4", "halowmesh", "secretkey999")
	wireless.seedWifiDevice("radio1", "2g", "6", "HT20")
	wireless.seedAPIface("default_radio1", "radio1", "")

	iw := makeIwinfoWithHardware("wlh0", "MediaTek MT7915AN")

	require.NoError(t, m.setupBatMesh1InterfaceWithDeps(context.Background(), openmanet, wireless, iw, wirelessStatusForRadio(t, "radio1", "wlh0")))

	_, created := wireless.Get("wireless", "batmesh1_radio1", "device")
	assert.False(t, created, "no mesh link may be written beside an enabled AP")

	radio, err := network.GetWirelessDeviceByNameWithReader("radio1", wireless)
	require.NoError(t, err)
	assert.Equal(t, "6", radio.Channel, "the AP's channel must not move")
	assert.Equal(t, "HT20", radio.HTMode)

	ap, err := network.GetWirelessIfaceByNameWithReader("default_radio1", wireless)
	require.NoError(t, err)
	assert.Equal(t, "ap", ap.Mode)
	assert.Equal(t, "client-wifi", ap.SSID)

	configured, err := network.IsBatMesh1ConfiguredWithReader(openmanet)
	require.NoError(t, err)
	assert.False(t, configured, "flag stays 0 so a later boot retries once the AP is off")
	assert.Zero(t, wireless.commitCalls)
}

func TestSetupBatMesh1Interface_DisabledAPOnRadioGetsLinkAlongside(t *testing.T) {
	m := newTestManagementConfig()
	openmanet := newFakeOpenMANETReader()
	openmanet.seedBatMesh1Configured("0")

	wireless := newFakeWirelessReader()
	wireless.seedMeshIface("existing_mesh0", "radio4", "halowmesh", "secretkey999")
	wireless.seedWifiDevice("radio1", "2g", "6", "HT20")
	wireless.seedAPIface("default_radio1", "radio1", "1")

	iw := makeIwinfoWithHardware("wlh0", "MediaTek MT7915AN")

	require.NoError(t, m.setupBatMesh1InterfaceWithDeps(context.Background(), openmanet, wireless, iw, wirelessStatusForRadio(t, "radio1", "wlh0")))

	link, err := network.GetWirelessIfaceByNameWithReader("batmesh1_radio1", wireless)
	require.NoError(t, err)
	assert.Equal(t, "batmesh1", link.Network)
	assert.Equal(t, "mesh", link.Mode)

	ap, err := network.GetWirelessIfaceByNameWithReader("default_radio1", wireless)
	require.NoError(t, err)
	assert.Equal(t, "ap", ap.Mode, "the disabled AP section is left as it was")
	assert.Equal(t, "1", ap.Disabled)
	assert.Equal(t, "client-wifi", ap.SSID)
}

func TestSetupBatMesh1Interface_NeverWritesDefaultSection(t *testing.T) {
	m := newTestManagementConfig()
	openmanet := newFakeOpenMANETReader()
	openmanet.seedBatMesh1Configured("0")

	wireless := newFakeWirelessReader()
	wireless.seedMeshIface("existing_mesh0", "radio4", "halowmesh", "secretkey999")
	wireless.seedWifiDevice("radio1", "2g", "6", "HT20")

	iw := makeIwinfoWithHardware("wlh0", "MediaTek MT7915AN")

	require.NoError(t, m.setupBatMesh1InterfaceWithDeps(context.Background(), openmanet, wireless, iw, wirelessStatusForRadio(t, "radio1", "wlh0")))

	_, wroteDefault := wireless.Get("wireless", "default_radio1", "device")
	assert.False(t, wroteDefault, "default_<radio> belongs to the AP; the daemon must never create it")

	_, wroteLink := wireless.Get("wireless", "batmesh1_radio1", "device")
	assert.True(t, wroteLink)
}

func TestSetupBatMesh1Interface_OptionSetMatchesMeshLink(t *testing.T) {
	m := newTestManagementConfig()
	openmanet := newFakeOpenMANETReader()
	openmanet.seedBatMesh1Configured("0")

	wireless := newFakeWirelessReader()
	wireless.seedMeshIface("existing_mesh0", "radio4", "halowmesh", "secretkey999")
	wireless.seedWifiDevice("radio1", "2g", "6", "HT20")

	iw := makeIwinfoWithHardware("wlh0", "MediaTek MT7915AN")

	require.NoError(t, m.setupBatMesh1InterfaceWithDeps(context.Background(), openmanet, wireless, iw, wirelessStatusForRadio(t, "radio1", "wlh0")))

	want := network.MeshLink{
		Radio:         "radio1",
		Network:       network.BatmanSecondaryIface,
		MeshID:        "halowmesh",
		Key:           "secretkey999",
		RSSIThreshold: network.SecondaryMeshRSSIThreshold,
	}.IfaceConfig()

	got, err := network.GetWirelessIfaceByNameWithReader("batmesh1_radio1", wireless)
	require.NoError(t, err)
	assert.Equal(t, want, got, "the daemon must write exactly the shared MeshLink option set")

	radio, err := network.GetWirelessDeviceByNameWithReader("radio1", wireless)
	require.NoError(t, err)
	assert.Equal(t, network.SecondaryMeshChannel2G, radio.Channel)
	assert.Equal(t, network.SecondaryMeshHTMode2G, radio.HTMode)
	assert.Equal(t, "0", radio.Disabled)
}

// ── configureBatmanForceflood tests ─────────────────────────────────────────

func TestConfigureBatmanForceflood_Enabled(t *testing.T) {
	m := newTestManagementConfig()
	m.BatInterface = "bat0"
	m.BatmanMulticastForceflood = true

	reader := newFakeNetworkReader()

	var reloadCalled bool

	reloadFn := func(_ context.Context) error {
		reloadCalled = true

		return nil
	}

	err := m.configureBatmanForcefloodWithDeps(context.Background(), reader, reloadFn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	values, ok := reader.Get("network", "bat0", "multicast_mode")
	if !ok {
		t.Fatal("expected multicast_mode to be written to bat0 section")
	}

	if len(values) != 1 || values[0] != "0" {
		t.Errorf("multicast_mode: got %v, want [0] (forceflood on = classic flooding)", values)
	}

	if !reloadCalled {
		t.Error("expected reload to be called")
	}
}

func TestConfigureBatmanForceflood_Disabled(t *testing.T) {
	m := newTestManagementConfig()
	m.BatInterface = "bat0"
	m.BatmanMulticastForceflood = false

	reader := newFakeNetworkReader()
	reloadFn := func(_ context.Context) error { return nil }

	err := m.configureBatmanForcefloodWithDeps(context.Background(), reader, reloadFn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	values, ok := reader.Get("network", "bat0", "multicast_mode")
	if !ok {
		t.Fatal("expected multicast_mode to be written to bat0 section")
	}

	if len(values) != 1 || values[0] != "1" {
		t.Errorf("multicast_mode: got %v, want [1] (forceflood off = multicast optimisations on)", values)
	}
}

func TestConfigureBatmanForceflood_CommitError(t *testing.T) {
	m := newTestManagementConfig()
	m.BatInterface = "bat0"
	m.BatmanMulticastForceflood = true

	reader := newFakeNetworkReader()
	reader.commitErr = errors.New("commit failure")

	reloadFn := func(_ context.Context) error { return nil }

	err := m.configureBatmanForcefloodWithDeps(context.Background(), reader, reloadFn)
	if err == nil {
		t.Fatal("expected error when commit fails")
	}

	if !strings.Contains(err.Error(), "set multicast_mode on bat0") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestConfigureBatmanForceflood_ReloadError(t *testing.T) {
	m := newTestManagementConfig()
	m.BatInterface = "bat0"
	m.BatmanMulticastForceflood = true

	reader := newFakeNetworkReader()

	reloadFn := func(_ context.Context) error {
		return errors.New("reload failed")
	}

	err := m.configureBatmanForcefloodWithDeps(context.Background(), reader, reloadFn)
	if err == nil {
		t.Fatal("expected error when reload fails")
	}

	if !strings.Contains(err.Error(), "reload config") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// TestConfigureBatmanForceflood_UnchangedSkipsCommitAndReload pins ledger
// row D1: on an already-configured device the boot-time multicast_mode
// write must be a no-op — no UCI commit, no reload_config — so a reboot
// does not bounce the network stack for a value that is already there.
func TestConfigureBatmanForceflood_UnchangedSkipsCommitAndReload(t *testing.T) {
	m := newTestManagementConfig()
	m.BatInterface = "bat0"
	m.BatmanMulticastForceflood = true // maps to multicast_mode "0"

	reader := newFakeNetworkReader()
	require.NoError(t, reader.AddSection("network", "bat0", "interface"))
	require.NoError(t, reader.SetType("network", "bat0", "multicast_mode", uci.TypeOption, "0"))

	reloadCalled := false
	reloadFn := func(_ context.Context) error {
		reloadCalled = true

		return nil
	}

	require.NoError(t, m.configureBatmanForcefloodWithDeps(context.Background(), reader, reloadFn))

	assert.Zero(t, reader.commitCalls, "unchanged multicast_mode must not commit")
	assert.False(t, reloadCalled, "unchanged multicast_mode must not reload the network")

	values, ok := reader.Get("network", "bat0", "multicast_mode")
	require.True(t, ok)
	assert.Equal(t, []string{"0"}, values)
}

// TestConfigureBatmanForceflood_ChangedCommitsAndReloads is the other half
// of D1: when the persisted value differs from the configured one, the
// daemon rewrites it, commits exactly once, and reloads.
func TestConfigureBatmanForceflood_ChangedCommitsAndReloads(t *testing.T) {
	m := newTestManagementConfig()
	m.BatInterface = "bat0"
	m.BatmanMulticastForceflood = true // maps to multicast_mode "0"

	reader := newFakeNetworkReader()
	require.NoError(t, reader.AddSection("network", "bat0", "interface"))
	require.NoError(t, reader.SetType("network", "bat0", "multicast_mode", uci.TypeOption, "1"))

	reloadCalled := false
	reloadFn := func(_ context.Context) error {
		reloadCalled = true

		return nil
	}

	require.NoError(t, m.configureBatmanForcefloodWithDeps(context.Background(), reader, reloadFn))

	values, ok := reader.Get("network", "bat0", "multicast_mode")
	require.True(t, ok)
	assert.Equal(t, []string{"0"}, values)
	assert.Equal(t, 1, reader.commitCalls, "changed multicast_mode must commit exactly once")
	assert.True(t, reloadCalled, "changed multicast_mode must reload the network")
}

// TestTransportMTUConstants_MatchNetworkPackage pins that the daemon's
// netlink pass and the wizard's UCI writes agree on the transport MTU
// (wizard-parity P6): both read internal/network's constants.
func TestTransportMTUConstants_MatchNetworkPackage(t *testing.T) {
	assert.Equal(t, network.DefaultBridgeMTU, defaultAhwlanInterfaceMTU)
	assert.Equal(t, network.DefaultEthernetMTU, defaultEthernetInterfaceMTU)
	assert.Equal(t, 1460, defaultAhwlanInterfaceMTU, "the shipped value must not drift")
}

// seedBatMesh1Iface seeds a pre-policy batmesh1 mesh wifi-iface: what a
// node configured before the tuning options existed carries.
func (f *fakeWirelessReader) seedBatMesh1Iface(section, device string) {
	f.seedMeshIface(section, device, "2gmesh", "secret")
	_ = f.SetType("wireless", section, "network", uci.TypeOption, "batmesh1")
	_ = f.SetType("wireless", section, "mesh_fwding", uci.TypeOption, "0")
	_ = f.SetType("wireless", section, "mesh_rssi_threshold", uci.TypeOption, "-80")
}

// reloadRecorder counts reload calls and returns err.
type reloadRecorder struct {
	calls int
	err   error
}

func (r *reloadRecorder) fn(context.Context) error {
	r.calls++

	return r.err
}

func assertPolicyPresent(t *testing.T, wireless *fakeWirelessReader, section string) {
	t.Helper()

	for _, p := range network.SecondaryMeshPolicyOptions() {
		vals, ok := wireless.Get("wireless", section, p.Option)
		if !ok || len(vals) != 1 || vals[0] != p.Value {
			t.Errorf("%s.%s: got %v (ok=%v), want %s", section, p.Option, vals, ok, p.Value)
		}
	}
}

func assertPolicyAbsent(t *testing.T, wireless *fakeWirelessReader, section string) {
	t.Helper()

	for _, p := range network.SecondaryMeshPolicyOptions() {
		if vals, ok := wireless.Get("wireless", section, p.Option); ok {
			t.Errorf("%s.%s must not be written, got %v", section, p.Option, vals)
		}
	}
}

func TestReconcileBatMesh1Options_AddsMissingAndReloadsOnce(t *testing.T) {
	m := newTestManagementConfig()
	wireless := newFakeWirelessReader()
	wireless.seedWifiDevice("radio1", "2g", "8", "HE20")
	wireless.seedBatMesh1Iface("batmesh1_radio1", "radio1")

	reload := &reloadRecorder{}

	err := m.reconcileBatMesh1OptionsWithDeps(context.Background(), wireless,
		makeIwinfoWithHardware("wlan1", "MediaTek MT7915AN"), wirelessStatusForRadio(t, "radio1", "wlan1"), reload.fn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertPolicyPresent(t, wireless, "batmesh1_radio1")

	if wireless.commitCalls != 1 {
		t.Errorf("commitCalls: got %d, want 1", wireless.commitCalls)
	}

	if reload.calls != 1 {
		t.Errorf("reload calls: got %d, want 1", reload.calls)
	}
}

func TestReconcileBatMesh1Options_NoOpWhenPresent(t *testing.T) {
	m := newTestManagementConfig()
	wireless := newFakeWirelessReader()
	wireless.seedWifiDevice("radio1", "2g", "8", "HE20")
	wireless.seedBatMesh1Iface("batmesh1_radio1", "radio1")

	for _, p := range network.SecondaryMeshPolicyOptions() {
		_ = wireless.SetType("wireless", "batmesh1_radio1", p.Option, uci.TypeOption, p.Value)
	}

	reload := &reloadRecorder{}

	err := m.reconcileBatMesh1OptionsWithDeps(context.Background(), wireless,
		makeIwinfoWithHardware("wlan1", "MediaTek MT7915AN"), wirelessStatusForRadio(t, "radio1", "wlan1"), reload.fn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if wireless.commitCalls != 0 {
		t.Errorf("commitCalls: got %d, want 0", wireless.commitCalls)
	}

	if reload.calls != 0 {
		t.Errorf("reload calls: got %d, want 0", reload.calls)
	}
}

func TestReconcileBatMesh1Options_AddsOnlyMissingAndKeepsOperatorValue(t *testing.T) {
	m := newTestManagementConfig()
	wireless := newFakeWirelessReader()
	wireless.seedWifiDevice("radio1", "2g", "8", "HE20")
	wireless.seedBatMesh1Iface("batmesh1_radio1", "radio1")
	_ = wireless.SetType("wireless", "batmesh1_radio1", "mcast_rate", uci.TypeOption, "12000")
	reload := &reloadRecorder{}

	err := m.reconcileBatMesh1OptionsWithDeps(context.Background(), wireless,
		makeIwinfoWithHardware("wlan1", "MediaTek MT7915AN"), wirelessStatusForRadio(t, "radio1", "wlan1"), reload.fn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	vals, _ := wireless.Get("wireless", "batmesh1_radio1", "mcast_rate")
	if len(vals) != 1 || vals[0] != "12000" {
		t.Errorf("operator mcast_rate must be preserved, got %v", vals)
	}

	nolearn, ok := wireless.Get("wireless", "batmesh1_radio1", "mesh_nolearn")
	if !ok || nolearn[0] != "1" {
		t.Errorf("mesh_nolearn must be added, got %v (ok=%v)", nolearn, ok)
	}

	if reload.calls != 1 {
		t.Errorf("reload calls: got %d, want 1", reload.calls)
	}
}

func TestReconcileBatMesh1Options_SkipsUnsupportedRadio(t *testing.T) {
	m := newTestManagementConfig()
	wireless := newFakeWirelessReader()
	wireless.seedWifiDevice("radio1", "2g", "8", "HT20")
	wireless.seedBatMesh1Iface("batmesh1_radio1", "radio1")

	reload := &reloadRecorder{}

	err := m.reconcileBatMesh1OptionsWithDeps(context.Background(), wireless,
		makeIwinfoWithHardware("wlan1", "Qualcomm Atheros QCA9880"), wirelessStatusForRadio(t, "radio1", "wlan1"), reload.fn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertPolicyAbsent(t, wireless, "batmesh1_radio1")

	if wireless.commitCalls != 0 || reload.calls != 0 {
		t.Errorf("no commit/reload expected, got commit=%d reload=%d", wireless.commitCalls, reload.calls)
	}
}

func TestReconcileBatMesh1Options_IgnoresPrimaryLinkAndAPs(t *testing.T) {
	m := newTestManagementConfig()
	wireless := newFakeWirelessReader()
	wireless.seedWifiDevice("radio4", "s1g", "42", "")
	wireless.seedMeshIface("default_radio4", "radio4", "halowmesh", "secret")
	_ = wireless.SetType("wireless", "default_radio4", "network", uci.TypeOption, "batmesh0")
	wireless.seedWifiDevice("radio1", "2g", "6", "HE20")
	wireless.seedAPIface("default_radio1", "radio1", "0")

	reload := &reloadRecorder{}

	// No batmesh1 section at all: the reconcile must not even consult
	// iwinfo, so pass a provider that would error if called.
	err := m.reconcileBatMesh1OptionsWithDeps(context.Background(), wireless,
		&fakeIwinfo{infoMapErr: errors.New("must not be called")}, wirelessStatusForRadio(t, "radio1", "wlan1"), reload.fn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertPolicyAbsent(t, wireless, "default_radio4")
	assertPolicyAbsent(t, wireless, "default_radio1")

	if wireless.commitCalls != 0 || reload.calls != 0 {
		t.Errorf("no commit/reload expected, got commit=%d reload=%d", wireless.commitCalls, reload.calls)
	}
}

func TestReconcileBatMesh1Options_SkipsDisabledSection(t *testing.T) {
	m := newTestManagementConfig()
	wireless := newFakeWirelessReader()
	wireless.seedWifiDevice("radio1", "2g", "8", "HE40")
	wireless.seedBatMesh1Iface("batmesh1_radio1", "radio1")
	_ = wireless.SetType("wireless", "batmesh1_radio1", "disabled", uci.TypeOption, "1")

	reload := &reloadRecorder{}

	err := m.reconcileBatMesh1OptionsWithDeps(context.Background(), wireless,
		makeIwinfoWithHardware("wlan1", "MediaTek MT7915AN"), wirelessStatusForRadio(t, "radio1", "wlan1"), reload.fn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertPolicyAbsent(t, wireless, "batmesh1_radio1")

	if wireless.commitCalls != 0 {
		t.Errorf("commitCalls: got %d, want 0 (a disabled section must not be touched)", wireless.commitCalls)
	}

	if reload.calls != 0 {
		t.Errorf("reload calls: got %d, want 0", reload.calls)
	}
}

func TestReconcileBatMesh1Options_SetTypeError(t *testing.T) {
	m := newTestManagementConfig()
	wireless := newFakeWirelessReader()
	wireless.seedWifiDevice("radio1", "2g", "8", "HE20")
	wireless.seedBatMesh1Iface("batmesh1_radio1", "radio1")
	wireless.setTypeErr = errors.New("uci set failed")
	reload := &reloadRecorder{}

	err := m.reconcileBatMesh1OptionsWithDeps(context.Background(), wireless,
		makeIwinfoWithHardware("wlan1", "MediaTek MT7915AN"), wirelessStatusForRadio(t, "radio1", "wlan1"), reload.fn)
	if err == nil || !strings.Contains(err.Error(), "uci set failed") {
		t.Fatalf("expected wrapped set error, got %v", err)
	}

	if reload.calls != 0 {
		t.Errorf("reload must not run after a failed write, got %d", reload.calls)
	}
}

func TestReconcileBatMesh1Options_ReloadError(t *testing.T) {
	m := newTestManagementConfig()
	wireless := newFakeWirelessReader()
	wireless.seedWifiDevice("radio1", "2g", "8", "HE20")
	wireless.seedBatMesh1Iface("batmesh1_radio1", "radio1")

	reload := &reloadRecorder{err: errors.New("reload_config exit 1")}

	err := m.reconcileBatMesh1OptionsWithDeps(context.Background(), wireless,
		makeIwinfoWithHardware("wlan1", "MediaTek MT7915AN"), wirelessStatusForRadio(t, "radio1", "wlan1"), reload.fn)
	if err == nil || !strings.Contains(err.Error(), "reload_config exit 1") {
		t.Fatalf("expected wrapped reload error, got %v", err)
	}

	// The options were committed before the reload failed; a later
	// boot finds them present and does nothing.
	assertPolicyPresent(t, wireless, "batmesh1_radio1")
}

func TestReconcileBatMesh1Options_IfaceSectionsError(t *testing.T) {
	m := newTestManagementConfig()
	wireless := &sectionErrorReader{ConfigReader: newFakeWirelessReader(), sectionType: "wifi-iface", err: errors.New("uci read failed")}
	reload := &reloadRecorder{}

	err := m.reconcileBatMesh1OptionsWithDeps(context.Background(), wireless,
		makeIwinfoWithHardware("wlan1", "MediaTek MT7915AN"), wirelessStatusForRadio(t, "radio1", "wlan1"), reload.fn)
	if err == nil || !strings.Contains(err.Error(), "uci read failed") {
		t.Fatalf("expected wrapped sections error, got %v", err)
	}
}

func TestReconcileBatMesh1Options_WirelessStatusError_SkipsThisStart(t *testing.T) {
	m := newTestManagementConfig()
	wireless := newFakeWirelessReader()
	wireless.seedWifiDevice("radio1", "2g", "8", "HE20")
	wireless.seedBatMesh1Iface("batmesh1_radio1", "radio1")

	reload := &reloadRecorder{}

	err := m.reconcileBatMesh1OptionsWithDeps(context.Background(), wireless,
		makeIwinfoWithHardware("wlan1", "MediaTek MT7915AN"),
		&fakeWirelessStatusProvider{Err: errors.New("ubus down")}, reload.fn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertPolicyAbsent(t, wireless, "batmesh1_radio1")

	if wireless.commitCalls != 0 || reload.calls != 0 {
		t.Errorf("no commit/reload expected, got commit=%d reload=%d", wireless.commitCalls, reload.calls)
	}
}

func TestReconcileBatMesh1Options_IwinfoError_SkipsThisStart(t *testing.T) {
	m := newTestManagementConfig()
	wireless := newFakeWirelessReader()
	wireless.seedWifiDevice("radio1", "2g", "8", "HE20")
	wireless.seedBatMesh1Iface("batmesh1_radio1", "radio1")

	reload := &reloadRecorder{}

	err := m.reconcileBatMesh1OptionsWithDeps(context.Background(), wireless,
		&fakeIwinfo{infoMapErr: errors.New("iwinfo down")},
		wirelessStatusForRadio(t, "radio1", "wlan1"), reload.fn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertPolicyAbsent(t, wireless, "batmesh1_radio1")

	if wireless.commitCalls != 0 || reload.calls != 0 {
		t.Errorf("no commit/reload expected, got commit=%d reload=%d", wireless.commitCalls, reload.calls)
	}
}

func TestReconcileBatMesh1Options_SkipsUnresolvedHardware(t *testing.T) {
	m := newTestManagementConfig()
	wireless := newFakeWirelessReader()
	wireless.seedWifiDevice("radio1", "2g", "8", "HE20")
	wireless.seedBatMesh1Iface("batmesh1_radio1", "radio1")

	reload := &reloadRecorder{}

	// iwinfo knows only a different ifname (wlan9), so the radio1 ->
	// wlan1 mapping from wirelessStatusForRadio never resolves to a
	// hardware name.
	err := m.reconcileBatMesh1OptionsWithDeps(context.Background(), wireless,
		makeIwinfoWithHardware("wlan9", "MediaTek MT7915AN"),
		wirelessStatusForRadio(t, "radio1", "wlan1"), reload.fn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertPolicyAbsent(t, wireless, "batmesh1_radio1")

	if wireless.commitCalls != 0 || reload.calls != 0 {
		t.Errorf("no commit/reload expected, got commit=%d reload=%d", wireless.commitCalls, reload.calls)
	}
}
