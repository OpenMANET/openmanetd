package network

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/digineo/go-uci/v2"
	"github.com/stretchr/testify/assert"
)

func TestGetWirelessMeshPassphraseFromPath(t *testing.T) {
	path := filepath.Join("..", "..", "testfixtures", "uci", "wireless")

	key, err := GetWirelessMeshPassphraseFromPath(path)
	if err != nil {
		t.Fatalf("GetWirelessMeshPassphraseFromPath failed: %v", err)
	}

	if key != "thisisnotarealpassword" {
		t.Fatalf("expected mesh passphrase %q, got %q", "thisisnotarealpassword", key)
	}
}

func TestGetWirelessMeshPassphraseFromPathSkipsDisabled(t *testing.T) {
	path := writeWirelessFixture(t, `
config wifi-iface 'mesh_disabled'
	option mode 'mesh'
	option encryption 'sae'
	option key 'disabled-key'
	option disabled '1'

config wifi-iface 'mesh_enabled'
	option mode 'mesh'
	option encryption 'sae'
	option key 'enabled-key'
`)

	key, err := GetWirelessMeshPassphraseFromPath(path)
	if err != nil {
		t.Fatalf("GetWirelessMeshPassphraseFromPath failed: %v", err)
	}

	if key != "enabled-key" {
		t.Fatalf("expected mesh passphrase %q, got %q", "enabled-key", key)
	}
}

func TestGetWirelessMeshPassphraseFromPathMissingKey(t *testing.T) {
	path := writeWirelessFixture(t, `
config wifi-iface 'mesh0'
	option mode 'mesh'
	option encryption 'sae'
`)

	_, err := GetWirelessMeshPassphraseFromPath(path)
	if err == nil {
		t.Fatalf("expected error when mesh key is missing")
	}

	if !strings.Contains(err.Error(), "missing key") {
		t.Fatalf("expected missing key error, got %v", err)
	}
}

func TestGetWirelessMeshPassphraseFromPathNoMeshInterface(t *testing.T) {
	path := writeWirelessFixture(t, `
config wifi-iface 'ap0'
	option mode 'ap'
	option key 'ap-password'
`)

	_, err := GetWirelessMeshPassphraseFromPath(path)
	if err == nil {
		t.Fatalf("expected error when no mesh interface exists")
	}

	if !strings.Contains(err.Error(), "no enabled mesh") {
		t.Fatalf("expected no mesh section error, got %v", err)
	}
}

func writeWirelessFixture(t *testing.T, content string) string {
	t.Helper()

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "wireless")

	if err := os.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0o600); err != nil {
		t.Fatalf("failed to write wireless fixture: %v", err)
	}

	return path
}

// newWirelessMockReader returns a mockConfigReader pre-populated with wireless config
// data matching the sample OpenWrt wireless configuration.
func newWirelessMockReader() *mockConfigReader {
	return &mockConfigReader{
		data: map[string]map[string]map[string][]string{
			"wireless": {
				"radio1": {
					"type":         {"mac80211"},
					"path":         {"scb/fd500000.pcie/pci0000:00/0000:00:00.0/0000:01:00.0"},
					"band":         {"2g"},
					"channel":      {"8"},
					"htmode":       {"HE20"},
					"country":      {"US"},
					"cell_density": {"0"},
					"txpower":      {"20"},
				},
				"radio4": {
					"type":                      {"morse"},
					"path":                      {"platform/soc/fe204000.spi/spi_master/spi0/spi0.0"},
					"band":                      {"s1g"},
					"hwmode":                    {"11ah"},
					"reconf":                    {"0"},
					"enable_mcast_whitelist":    {"0"},
					"enable_mcast_rate_control": {"1"},
					"country":                   {"US"},
					"enable_ps":                 {"0"},
					"enable_dynamic_ps_offload": {"0"},
					"enable_twt":                {"0"},
					"bcf":                       {"bcf_fgh100mhaamd.bin"},
					"channel":                   {"42"},
					"disabled":                  {"1"},
				},
				"default_radio1": {
					"device":              {"radio1"},
					"network":             {"batmesh1"},
					"mode":                {"mesh"},
					"key":                 {"rmx4110vqqpfj"},
					"mesh_id":             {"halowmesh"},
					"mesh_fwding":         {"0"},
					"mesh_rssi_threshold": {"0"},
					"encryption":          {"sae"},
				},
				"default_radio4": {
					"mode":       {"mesh"},
					"device":     {"radio4"},
					"network":    {"batmesh0"},
					"ssid":       {"BCM2711-1003"},
					"encryption": {"sae"},
					"key":        {"rmx4110vqqpfj"},
					"beacon_int": {"1000"},
					"mesh_id":    {"halowmesh"},
				},
			},
		},
		sectionTypes: map[string]map[string]string{
			"wireless": {
				"radio1":         "wifi-device",
				"radio4":         "wifi-device",
				"default_radio1": "wifi-iface",
				"default_radio4": "wifi-iface",
			},
		},
	}
}

// --- GetWirelessDeviceByNameWithReader ---

func TestGetWirelessDeviceByNameWithReader_Radio1(t *testing.T) {
	reader := newWirelessMockReader()

	want := &UCIWirelessDevice{
		Type:        "mac80211",
		Path:        "scb/fd500000.pcie/pci0000:00/0000:00:00.0/0000:01:00.0",
		Band:        "2g",
		Channel:     "8",
		HTMode:      "HE20",
		Country:     "US",
		CellDensity: "0",
		TxPower:     "20",
	}

	got, err := GetWirelessDeviceByNameWithReader("radio1", reader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestGetWirelessDeviceByNameWithReader_Radio4(t *testing.T) {
	reader := newWirelessMockReader()

	want := &UCIWirelessDevice{
		Type:                   "morse",
		Path:                   "platform/soc/fe204000.spi/spi_master/spi0/spi0.0",
		Band:                   "s1g",
		Channel:                "42",
		Country:                "US",
		HWMode:                 "11ah",
		Reconf:                 "0",
		EnableMcastWhitelist:   "0",
		EnableMcastRateControl: "1",
		EnablePS:               "0",
		EnableDynamicPSOffload: "0",
		EnableTWT:              "0",
		BCF:                    "bcf_fgh100mhaamd.bin",
		Disabled:               "1",
	}

	got, err := GetWirelessDeviceByNameWithReader("radio4", reader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestGetWirelessDeviceByNameWithReader_NonExistent(t *testing.T) {
	reader := newWirelessMockReader()

	got, err := GetWirelessDeviceByNameWithReader("radio99", reader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !reflect.DeepEqual(got, &UCIWirelessDevice{}) {
		t.Errorf("expected empty device config for nonexistent section, got %+v", got)
	}
}

// --- GetWirelessIfaceByNameWithReader ---

func TestGetWirelessIfaceByNameWithReader_DefaultRadio1(t *testing.T) {
	reader := newWirelessMockReader()

	want := &UCIWirelessIface{
		Device:            "radio1",
		Network:           "batmesh1",
		Mode:              "mesh",
		Key:               "rmx4110vqqpfj",
		MeshID:            "halowmesh",
		MeshFwding:        "0",
		MeshRSSIThreshold: "0",
		Encryption:        "sae",
	}

	got, err := GetWirelessIfaceByNameWithReader("default_radio1", reader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestGetWirelessIfaceByNameWithReader_DefaultRadio4(t *testing.T) {
	reader := newWirelessMockReader()

	want := &UCIWirelessIface{
		Device:     "radio4",
		Network:    "batmesh0",
		Mode:       "mesh",
		Key:        "rmx4110vqqpfj",
		MeshID:     "halowmesh",
		Encryption: "sae",
		SSID:       "BCM2711-1003",
		BeaconInt:  "1000",
	}

	got, err := GetWirelessIfaceByNameWithReader("default_radio4", reader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestGetWirelessIfaceByNameWithReader_NonExistent(t *testing.T) {
	reader := newWirelessMockReader()

	got, err := GetWirelessIfaceByNameWithReader("nonexistent", reader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !reflect.DeepEqual(got, &UCIWirelessIface{}) {
		t.Errorf("expected empty iface config for nonexistent section, got %+v", got)
	}
}

// --- SetWirelessDeviceConfigWithReader ---

func TestSetWirelessDeviceConfigWithReader_Complete(t *testing.T) {
	reader := newWirelessMockReader()

	cfg := &UCIWirelessDevice{
		Type:    "mac80211",
		Band:    "5g",
		Channel: "36",
		HTMode:  "HE80",
		Country: "US",
	}

	if err := SetWirelessDeviceConfigWithReader("radio2", cfg, reader); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !reader.commitCalled {
		t.Error("expected Commit to be called")
	}

	got, err := GetWirelessDeviceByNameWithReader("radio2", reader)
	if err != nil {
		t.Fatalf("unexpected error reading back config: %v", err)
	}

	if got.Band != "5g" {
		t.Errorf("Band: got %q, want %q", got.Band, "5g")
	}

	if got.Channel != "36" {
		t.Errorf("Channel: got %q, want %q", got.Channel, "36")
	}

	if got.HTMode != "HE80" {
		t.Errorf("HTMode: got %q, want %q", got.HTMode, "HE80")
	}
}

func TestSetWirelessDeviceConfigWithReader_TxPower(t *testing.T) {
	reader := newWirelessMockReader()

	cfg := &UCIWirelessDevice{
		Band:    "2g",
		Channel: "6",
		TxPower: "14",
	}

	if err := SetWirelessDeviceConfigWithReader("radio5", cfg, reader); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, err := GetWirelessDeviceByNameWithReader("radio5", reader)
	if err != nil {
		t.Fatalf("unexpected error reading back config: %v", err)
	}

	if got.TxPower != "14" {
		t.Errorf("TxPower: got %q, want %q", got.TxPower, "14")
	}
}

func TestSetWirelessDeviceConfigWithReader_MinimalConfig(t *testing.T) {
	reader := newWirelessMockReader()

	cfg := &UCIWirelessDevice{
		Band:    "2g",
		Channel: "1",
	}

	if err := SetWirelessDeviceConfigWithReader("radio3", cfg, reader); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, err := GetWirelessDeviceByNameWithReader("radio3", reader)
	if err != nil {
		t.Fatalf("unexpected error reading back config: %v", err)
	}

	if got.Band != "2g" {
		t.Errorf("Band: got %q, want %q", got.Band, "2g")
	}

	if got.Type != "" {
		t.Errorf("Type should be empty for minimal config, got %q", got.Type)
	}
}

func TestSetWirelessDeviceConfigWithReader_NilConfig(t *testing.T) {
	reader := newWirelessMockReader()

	err := SetWirelessDeviceConfigWithReader("radio0", nil, reader)
	if err == nil {
		t.Fatal("expected error for nil config")
	}

	if !strings.Contains(err.Error(), "cannot be nil") {
		t.Errorf("expected nil config error, got %v", err)
	}
}

func TestSetWirelessDeviceConfigWithReader_SetTypeError(t *testing.T) {
	reader := newWirelessMockReader()
	reader.setTypeError = errors.New("set type failed")

	cfg := &UCIWirelessDevice{Type: "mac80211"}

	err := SetWirelessDeviceConfigWithReader("radio0", cfg, reader)
	if err == nil {
		t.Fatal("expected error when SetType fails")
	}

	if !strings.Contains(err.Error(), "failed to set type") {
		t.Errorf("expected set type error, got %v", err)
	}
}

func TestSetWirelessDeviceConfigWithReader_CommitError(t *testing.T) {
	reader := newWirelessMockReader()
	reader.commitError = errors.New("commit failed")

	cfg := &UCIWirelessDevice{Band: "2g"}

	err := SetWirelessDeviceConfigWithReader("radio0", cfg, reader)
	if err == nil {
		t.Fatal("expected error when Commit fails")
	}

	if !strings.Contains(err.Error(), "failed to commit") {
		t.Errorf("expected commit error, got %v", err)
	}
}

// --- SetWirelessIfaceConfigWithReader ---

func TestSetWirelessIfaceConfigWithReader_MeshIface(t *testing.T) {
	reader := newWirelessMockReader()

	cfg := &UCIWirelessIface{
		Device:     "radio1",
		Network:    "batmesh1",
		Mode:       "mesh",
		Key:        "newsecretkey",
		MeshID:     "newmesh",
		Encryption: "sae",
	}

	if err := SetWirelessIfaceConfigWithReader("mesh_new", cfg, reader); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !reader.commitCalled {
		t.Error("expected Commit to be called")
	}

	got, err := GetWirelessIfaceByNameWithReader("mesh_new", reader)
	if err != nil {
		t.Fatalf("unexpected error reading back config: %v", err)
	}

	if got.Mode != "mesh" {
		t.Errorf("Mode: got %q, want %q", got.Mode, "mesh")
	}

	if got.Key != "newsecretkey" {
		t.Errorf("Key: got %q, want %q", got.Key, "newsecretkey")
	}

	if got.MeshID != "newmesh" {
		t.Errorf("MeshID: got %q, want %q", got.MeshID, "newmesh")
	}
}

func TestSetWirelessIfaceConfigWithReader_WithBeaconAndSSID(t *testing.T) {
	reader := newWirelessMockReader()

	cfg := &UCIWirelessIface{
		Device:     "radio4",
		Network:    "batmesh0",
		Mode:       "mesh",
		SSID:       "MyMesh",
		BeaconInt:  "500",
		Encryption: "sae",
		Key:        "meshkey123",
	}

	if err := SetWirelessIfaceConfigWithReader("default_radio4", cfg, reader); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, err := GetWirelessIfaceByNameWithReader("default_radio4", reader)
	if err != nil {
		t.Fatalf("unexpected error reading back config: %v", err)
	}

	if got.SSID != "MyMesh" {
		t.Errorf("SSID: got %q, want %q", got.SSID, "MyMesh")
	}

	if got.BeaconInt != "500" {
		t.Errorf("BeaconInt: got %q, want %q", got.BeaconInt, "500")
	}
}

func TestSetWirelessIfaceConfigWithReader_NilConfig(t *testing.T) {
	reader := newWirelessMockReader()

	err := SetWirelessIfaceConfigWithReader("mesh0", nil, reader)
	if err == nil {
		t.Fatal("expected error for nil config")
	}

	if !strings.Contains(err.Error(), "cannot be nil") {
		t.Errorf("expected nil config error, got %v", err)
	}
}

func TestSetWirelessIfaceConfigWithReader_SetTypeError(t *testing.T) {
	reader := newWirelessMockReader()
	reader.setTypeError = errors.New("set type failed")

	cfg := &UCIWirelessIface{Device: "radio1"}

	err := SetWirelessIfaceConfigWithReader("mesh0", cfg, reader)
	if err == nil {
		t.Fatal("expected error when SetType fails")
	}

	if !strings.Contains(err.Error(), "failed to set device") {
		t.Errorf("expected set device error, got %v", err)
	}
}

func TestSetWirelessIfaceConfigWithReader_CommitError(t *testing.T) {
	reader := newWirelessMockReader()
	reader.commitError = errors.New("commit failed")

	cfg := &UCIWirelessIface{Mode: "mesh"}

	err := SetWirelessIfaceConfigWithReader("mesh0", cfg, reader)
	if err == nil {
		t.Fatal("expected error when Commit fails")
	}

	if !strings.Contains(err.Error(), "failed to commit") {
		t.Errorf("expected commit error, got %v", err)
	}
}

// --- DeleteWirelessDeviceWithReader ---

func TestDeleteWirelessDeviceWithReader(t *testing.T) {
	reader := newWirelessMockReader()

	if err := DeleteWirelessDeviceWithReader("radio1", reader); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !reader.commitCalled {
		t.Error("expected Commit to be called")
	}

	// Verify the section is gone.
	got, err := GetWirelessDeviceByNameWithReader("radio1", reader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !reflect.DeepEqual(got, &UCIWirelessDevice{}) {
		t.Errorf("expected empty device after deletion, got %+v", got)
	}
}

func TestDeleteWirelessDeviceWithReader_DelSectionError(t *testing.T) {
	reader := newWirelessMockReader()
	reader.delSectionErr = errors.New("del section failed")

	err := DeleteWirelessDeviceWithReader("radio1", reader)
	if err == nil {
		t.Fatal("expected error when DelSection fails")
	}

	if !strings.Contains(err.Error(), "failed to delete wireless device section") {
		t.Errorf("expected delete section error, got %v", err)
	}
}

func TestDeleteWirelessDeviceWithReader_CommitError(t *testing.T) {
	reader := newWirelessMockReader()
	reader.commitError = errors.New("commit failed")

	err := DeleteWirelessDeviceWithReader("radio1", reader)
	if err == nil {
		t.Fatal("expected error when Commit fails")
	}

	if !strings.Contains(err.Error(), "failed to commit") {
		t.Errorf("expected commit error, got %v", err)
	}
}

// --- DeleteWirelessIfaceWithReader ---

func TestDeleteWirelessIfaceWithReader(t *testing.T) {
	reader := newWirelessMockReader()

	if err := DeleteWirelessIfaceWithReader("default_radio1", reader); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !reader.commitCalled {
		t.Error("expected Commit to be called")
	}

	// Verify the section is gone.
	got, err := GetWirelessIfaceByNameWithReader("default_radio1", reader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !reflect.DeepEqual(got, &UCIWirelessIface{}) {
		t.Errorf("expected empty iface after deletion, got %+v", got)
	}
}

func TestDeleteWirelessIfaceWithReader_DelSectionError(t *testing.T) {
	reader := newWirelessMockReader()
	reader.delSectionErr = errors.New("del section failed")

	err := DeleteWirelessIfaceWithReader("default_radio1", reader)
	if err == nil {
		t.Fatal("expected error when DelSection fails")
	}

	if !strings.Contains(err.Error(), "failed to delete wireless iface section") {
		t.Errorf("expected delete section error, got %v", err)
	}
}

func TestDeleteWirelessIfaceWithReader_CommitError(t *testing.T) {
	reader := newWirelessMockReader()
	reader.commitError = errors.New("commit failed")

	err := DeleteWirelessIfaceWithReader("default_radio1", reader)
	if err == nil {
		t.Fatal("expected error when Commit fails")
	}

	if !strings.Contains(err.Error(), "failed to commit") {
		t.Errorf("expected commit error, got %v", err)
	}
}

// --- UCIWirelessDevice.Disabled field tests ---

func TestGetWirelessDeviceByNameWithReader_DisabledField(t *testing.T) {
	reader := newWirelessMockReader()

	got, err := GetWirelessDeviceByNameWithReader("radio4", reader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got.Disabled != "1" {
		t.Errorf("Disabled: got %q, want %q", got.Disabled, "1")
	}
}

func TestGetWirelessDeviceByNameWithReader_DisabledNotSet(t *testing.T) {
	reader := newWirelessMockReader()

	got, err := GetWirelessDeviceByNameWithReader("radio1", reader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got.Disabled != "" {
		t.Errorf("Disabled: got %q, want empty string", got.Disabled)
	}
}

func TestSetWirelessDeviceConfigWithReader_Disabled(t *testing.T) {
	reader := newWirelessMockReader()

	cfg := &UCIWirelessDevice{
		Band:     "5g",
		Channel:  "36",
		Disabled: "1",
	}

	if err := SetWirelessDeviceConfigWithReader("radio_dis", cfg, reader); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, err := GetWirelessDeviceByNameWithReader("radio_dis", reader)
	if err != nil {
		t.Fatalf("unexpected error reading back config: %v", err)
	}

	if got.Disabled != "1" {
		t.Errorf("Disabled: got %q, want %q", got.Disabled, "1")
	}
}

func TestSetWirelessDeviceConfigWithReader_DisabledZero(t *testing.T) {
	reader := newWirelessMockReader()

	cfg := &UCIWirelessDevice{
		Band:     "2g",
		Channel:  "6",
		Disabled: "0",
	}

	if err := SetWirelessDeviceConfigWithReader("radio_en", cfg, reader); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, err := GetWirelessDeviceByNameWithReader("radio_en", reader)
	if err != nil {
		t.Fatalf("unexpected error reading back config: %v", err)
	}

	if got.Disabled != "0" {
		t.Errorf("Disabled: got %q, want %q", got.Disabled, "0")
	}
}

func TestSetWirelessDeviceConfigWithReader_DisabledEmpty(t *testing.T) {
	reader := newWirelessMockReader()

	cfg := &UCIWirelessDevice{
		Band:    "2g",
		Channel: "1",
	}

	if err := SetWirelessDeviceConfigWithReader("radio_nod", cfg, reader); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, err := GetWirelessDeviceByNameWithReader("radio_nod", reader)
	if err != nil {
		t.Fatalf("unexpected error reading back config: %v", err)
	}

	if got.Disabled != "" {
		t.Errorf("Disabled: got %q, want empty (should not be set)", got.Disabled)
	}
}

func TestSetWirelessDeviceConfigWithReader_DisabledSetTypeError(t *testing.T) {
	reader := newWirelessMockReader()
	reader.setTypeError = errors.New("set type failed")

	cfg := &UCIWirelessDevice{Disabled: "1"}

	err := SetWirelessDeviceConfigWithReader("radio0", cfg, reader)
	if err == nil {
		t.Fatal("expected error when SetType fails")
	}

	if !strings.Contains(err.Error(), "failed to set disabled") {
		t.Errorf("expected set disabled error, got %v", err)
	}
}

// --- WhitelistDeviceFields / WhitelistInterfaceFields / DisableAllInterfaces ---

func TestWhitelistDeviceFields_RemovesNonWhitelistedOptions(t *testing.T) {
	reader := newWirelessMockReader()

	if err := WhitelistDeviceFields(reader, "radio4", WizardWifiDeviceWhitelist); err != nil {
		t.Fatalf("WhitelistDeviceFields: %v", err)
	}

	for _, opt := range []string{"type", "path", "band", "hwmode", "channel", "country"} {
		if _, ok := reader.Get("wireless", "radio4", opt); !ok {
			t.Errorf("whitelisted option %q was removed", opt)
		}
	}

	for _, opt := range []string{"enable_mcast_whitelist", "enable_mcast_rate_control", "disabled"} {
		if _, ok := reader.Get("wireless", "radio4", opt); ok {
			t.Errorf("non-whitelisted option %q was not removed", opt)
		}
	}
}

func TestWhitelistDeviceFields_NoOpIfAllWhitelisted(t *testing.T) {
	reader := newWirelessMockReader()

	allOpts := []string{
		"type", "path", "band", "hwmode", "htmode", "reconf", "bcf",
		"country", "channel", "cell_density", "txpower",
		"enable_ps", "enable_dynamic_ps_offload", "enable_twt",
		"enable_mcast_whitelist", "enable_mcast_rate_control", "disabled",
	}

	if err := WhitelistDeviceFields(reader, "radio4", allOpts); err != nil {
		t.Fatalf("WhitelistDeviceFields: %v", err)
	}

	for _, opt := range []string{
		"type", "enable_mcast_whitelist", "enable_mcast_rate_control", "disabled",
	} {
		if _, ok := reader.Get("wireless", "radio4", opt); !ok {
			t.Errorf("option %q removed despite being in allowList", opt)
		}
	}
}

func TestWhitelistDeviceFields_RejectsEmptyName(t *testing.T) {
	reader := newWirelessMockReader()
	if err := WhitelistDeviceFields(reader, "", WizardWifiDeviceWhitelist); err == nil {
		t.Errorf("expected error for empty deviceName")
	}
}

func TestWhitelistInterfaceFields_RemovesNonWhitelistedOptions(t *testing.T) {
	reader := newWirelessMockReader()

	if err := WhitelistInterfaceFields(reader, "default_radio4", WizardWifiIfaceWhitelist); err != nil {
		t.Fatalf("WhitelistInterfaceFields: %v", err)
	}

	for _, opt := range []string{"network", "device", "key", "encryption", "mode", "ssid", "mesh_id"} {
		if _, ok := reader.Get("wireless", "default_radio4", opt); !ok {
			t.Errorf("whitelisted option %q removed", opt)
		}
	}

	for _, opt := range []string{"beacon_int"} {
		if _, ok := reader.Get("wireless", "default_radio4", opt); ok {
			t.Errorf("non-whitelisted option %q not removed", opt)
		}
	}
}

func TestWhitelistInterfaceFields_RejectsEmptyName(t *testing.T) {
	reader := newWirelessMockReader()
	if err := WhitelistInterfaceFields(reader, "", WizardWifiIfaceWhitelist); err == nil {
		t.Errorf("expected error for empty ifaceName")
	}
}

func TestDisableAllInterfaces_SetsDisabledOnEvery(t *testing.T) {
	reader := newWirelessMockReader()

	if err := DisableAllInterfaces(reader); err != nil {
		t.Fatalf("DisableAllInterfaces: %v", err)
	}

	for _, name := range []string{"default_radio1", "default_radio4"} {
		v, ok := reader.Get("wireless", name, "disabled")
		if !ok {
			t.Errorf("%s missing disabled", name)

			continue
		}

		if len(v) != 1 || v[0] != "1" {
			t.Errorf("%s disabled = %v, want [\"1\"]", name, v)
		}
	}

	// wifi-device sections are NOT touched.
	if _, ok := reader.Get("wireless", "radio1", "disabled"); ok {
		t.Error("radio1 (wifi-device) should not have disabled set")
	}
}

func TestDisableAllInterfaces_PropagatesSetError(t *testing.T) {
	reader := newWirelessMockReader()
	reader.setTypeError = errors.New("set failed")

	if err := DisableAllInterfaces(reader); err == nil {
		t.Errorf("expected propagated error")
	}
}

func TestWhitelistDeviceFields_PropagatesDelError(t *testing.T) {
	reader := newWirelessMockReader()
	reader.delError = errors.New("del failed")

	if err := WhitelistDeviceFields(reader, "radio4", WizardWifiDeviceWhitelist); err == nil {
		t.Errorf("expected propagated error")
	}
}

func TestMeshLink_Section(t *testing.T) {
	link := MeshLink{Radio: "radio0", Network: BatmanSecondaryIface}

	assert.Equal(t, "batmesh1_radio0", link.Section())
	assert.NotEqual(t, "default_radio0", link.Section(),
		"the link section must never be the AP section the wizard writes on the same radio")
	assert.Equal(t, "batmesh0_radio2", MeshLink{Radio: "radio2", Network: BatmanPrimaryIface}.Section(),
		"the name is keyed by the hardif, not hard-wired to batmesh1")
}

func TestMeshLink_IfaceConfig(t *testing.T) {
	link := MeshLink{
		Radio:         "radio0",
		Network:       BatmanSecondaryIface,
		MeshID:        "backhaul",
		Key:           "secretkey999",
		RSSIThreshold: SecondaryMeshRSSIThreshold,
	}

	want := &UCIWirelessIface{
		Device:             "radio0",
		Network:            "batmesh1",
		Mode:               "mesh",
		MeshID:             "backhaul",
		Key:                "secretkey999",
		MeshFwding:         "0",
		MeshRSSIThreshold:  "-80",
		Encryption:         "sae",
		McastRate:          "24000",
		MeshNolearn:        "1",
		MeshRetryTimeout:   "255",
		MeshConfirmTimeout: "255",
		MeshHoldingTimeout: "255",
	}

	assert.Equal(t, want, link.IfaceConfig())
}

func TestMeshLink_IfaceConfig_NoThreshold(t *testing.T) {
	got := MeshLink{Radio: "radio2", Network: BatmanPrimaryIface, MeshID: "m", Key: "k"}.IfaceConfig()

	assert.Empty(t, got.MeshRSSIThreshold, "an empty threshold must not be written")
	assert.Equal(t, "batmesh0", got.Network)
	assert.Empty(t, got.McastRate, "primary link must not carry secondary policy")
	assert.Empty(t, got.MeshNolearn, "primary link must not carry secondary policy")
	assert.Empty(t, got.MeshRetryTimeout, "primary link must not carry secondary policy")
	assert.Empty(t, got.MeshConfirmTimeout, "primary link must not carry secondary policy")
	assert.Empty(t, got.MeshHoldingTimeout, "primary link must not carry secondary policy")
}

// seedIface adds a wifi-iface with the given device and mode to the
// wireless mock; an empty mode leaves the option unset.
func seedIface(t *testing.T, reader *mockConfigReader, name, device, mode string) {
	t.Helper()

	if err := reader.AddSection("wireless", name, "wifi-iface"); err != nil {
		t.Fatalf("AddSection %s: %v", name, err)
	}

	if err := reader.SetType("wireless", name, "device", uci.TypeOption, device); err != nil {
		t.Fatalf("SetType %s.device: %v", name, err)
	}

	if mode == "" {
		return
	}

	if err := reader.SetType("wireless", name, "mode", uci.TypeOption, mode); err != nil {
		t.Fatalf("SetType %s.mode: %v", name, err)
	}
}

func TestRemoveNonMeshIfacesOnMorseDevices_DeletesAPAndSTAOnMorseOnly(t *testing.T) {
	reader := newWirelessMockReader()
	// radio4 is the fixture's morse radio (default_radio4 is its mesh
	// iface); radio1 is mac80211 (default_radio1 is a mesh backhaul).
	seedIface(t, reader, "meshap_radio4", "radio4", "ap")
	seedIface(t, reader, "sta_radio4", "radio4", "sta")
	seedIface(t, reader, "nomode_radio4", "radio4", "")
	seedIface(t, reader, "ap_radio1", "radio1", "ap")

	removed, err := RemoveNonMeshIfacesOnMorseDevices(reader)
	if err != nil {
		t.Fatalf("RemoveNonMeshIfacesOnMorseDevices: %v", err)
	}

	want := map[string]bool{"meshap_radio4": true, "sta_radio4": true, "nomode_radio4": true}
	if len(removed) != len(want) {
		t.Fatalf("removed = %v, want the three non-mesh radio4 ifaces", removed)
	}

	for _, name := range removed {
		if !want[name] {
			t.Errorf("unexpected removal %s", name)
		}

		if _, ok := reader.Get("wireless", name, "device"); ok {
			t.Errorf("%s still present after removal", name)
		}
	}

	for _, name := range []string{"default_radio4", "default_radio1", "ap_radio1"} {
		if _, ok := reader.Get("wireless", name, "device"); !ok {
			t.Errorf("%s must survive: mesh on morse, or any mode on mac80211", name)
		}
	}
}

func TestRemoveNonMeshIfacesOnMorseDevices_NoMorseRadioIsNoop(t *testing.T) {
	reader := newWirelessMockReader()
	delete(reader.data["wireless"], "radio4")
	delete(reader.sectionTypes["wireless"], "radio4")
	seedIface(t, reader, "ap_radio1", "radio1", "ap")

	removed, err := RemoveNonMeshIfacesOnMorseDevices(reader)
	if err != nil {
		t.Fatalf("RemoveNonMeshIfacesOnMorseDevices: %v", err)
	}

	if len(removed) != 0 {
		t.Errorf("removed = %v, want nothing without a morse radio", removed)
	}

	if reader.delSectionCall != "" {
		t.Errorf("DelSection called (%s) with no morse radio present", reader.delSectionCall)
	}
}

func TestRemoveNonMeshIfacesOnMorseDevices_PropagatesDelSectionError(t *testing.T) {
	reader := newWirelessMockReader()
	seedIface(t, reader, "meshap_radio4", "radio4", "ap")
	reader.delSectionErr = errors.New("del failed")

	if _, err := RemoveNonMeshIfacesOnMorseDevices(reader); err == nil {
		t.Error("expected propagated DelSection error")
	}
}

func TestIsMorseDevice(t *testing.T) {
	reader := newWirelessMockReader()

	if !IsMorseDevice(reader, "radio4") {
		t.Error("radio4 (type=morse) must be reported as morse")
	}

	if IsMorseDevice(reader, "radio1") {
		t.Error("radio1 (type=mac80211) must not be reported as morse")
	}

	if IsMorseDevice(reader, "radio9") {
		t.Error("unknown device must not be reported as morse")
	}
}

// --- Secondary mesh policy ---

func TestSecondaryMeshPolicyOptions_ValuesAndOrder(t *testing.T) {
	got := SecondaryMeshPolicyOptions()

	want := []SecondaryMeshPolicyOption{
		{Option: "mcast_rate", Value: "24000"},
		{Option: "mesh_nolearn", Value: "1"},
		{Option: "mesh_retry_timeout", Value: "255"},
		{Option: "mesh_confirm_timeout", Value: "255"},
		{Option: "mesh_holding_timeout", Value: "255"},
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SecondaryMeshPolicyOptions: got %v, want %v", got, want)
	}
}

func TestUCIWirelessIface_ApplySecondaryMeshPolicy_MatchesPolicyOptions(t *testing.T) {
	reader := newWirelessMockReader()

	cfg := &UCIWirelessIface{Device: "radio1", Network: "batmesh1", Mode: "mesh"}
	cfg.ApplySecondaryMeshPolicy()

	if err := SetWirelessIfaceConfigWithReader("batmesh1_radio1", cfg, reader); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, p := range SecondaryMeshPolicyOptions() {
		vals, ok := reader.Get("wireless", "batmesh1_radio1", p.Option)
		if !ok || len(vals) != 1 || vals[0] != p.Value {
			t.Errorf("%s: got %v (ok=%v), want [%s]", p.Option, vals, ok, p.Value)
		}
	}
}

func TestMeshLink_IfaceConfig_CarriesSecondaryPolicy(t *testing.T) {
	cfg := MeshLink{Radio: "radio1", Network: "batmesh1", MeshID: "m", Key: "k", RSSIThreshold: "-80"}.IfaceConfig()

	want := &UCIWirelessIface{
		Device: "radio1", Network: "batmesh1", Mode: "mesh", MeshID: "m", Key: "k",
		MeshFwding: "0", MeshRSSIThreshold: "-80", Encryption: "sae",
		McastRate: "24000", MeshNolearn: "1",
		MeshRetryTimeout: "255", MeshConfirmTimeout: "255", MeshHoldingTimeout: "255",
	}

	if !reflect.DeepEqual(cfg, want) {
		t.Fatalf("IfaceConfig:\n got %+v\nwant %+v", cfg, want)
	}
}

func TestSetWirelessIfaceConfigWithReader_RoundTripsPolicyFields(t *testing.T) {
	reader := newWirelessMockReader()

	cfg := &UCIWirelessIface{
		Device:             "radio1",
		Network:            "batmesh1",
		Mode:               "mesh",
		McastRate:          "12000",
		MeshNolearn:        "0",
		MeshRetryTimeout:   "100",
		MeshConfirmTimeout: "101",
		MeshHoldingTimeout: "102",
	}

	if err := SetWirelessIfaceConfigWithReader("batmesh1_radio1", cfg, reader); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, err := GetWirelessIfaceByNameWithReader("batmesh1_radio1", reader)
	if err != nil {
		t.Fatalf("unexpected error reading back: %v", err)
	}

	if got.McastRate != "12000" || got.MeshNolearn != "0" ||
		got.MeshRetryTimeout != "100" || got.MeshConfirmTimeout != "101" || got.MeshHoldingTimeout != "102" {
		t.Errorf("policy fields did not round-trip: %+v", got)
	}
}

func TestSetWirelessIfaceConfigWithReader_SkipsEmptyPolicyFields(t *testing.T) {
	reader := newWirelessMockReader()

	cfg := &UCIWirelessIface{Device: "radio1", Network: "batmesh1", Mode: "mesh"}

	if err := SetWirelessIfaceConfigWithReader("batmesh1_radio1", cfg, reader); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, p := range SecondaryMeshPolicyOptions() {
		if _, ok := reader.Get("wireless", "batmesh1_radio1", p.Option); ok {
			t.Errorf("%s must not be written when the field is empty", p.Option)
		}
	}
}

func TestWhitelistInterfaceFields_RemovesPolicyOptions(t *testing.T) {
	reader := newWirelessMockReader()

	cfg := &UCIWirelessIface{Device: "radio1", Network: "batmesh1", Mode: "mesh", Key: "k", Encryption: "sae"}
	cfg.ApplySecondaryMeshPolicy()

	if err := SetWirelessIfaceConfigWithReader("batmesh1_radio1", cfg, reader); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := WhitelistInterfaceFields(reader, "batmesh1_radio1", WizardWifiIfaceWhitelist); err != nil {
		t.Fatalf("WhitelistInterfaceFields: %v", err)
	}

	for _, p := range SecondaryMeshPolicyOptions() {
		if _, ok := reader.Get("wireless", "batmesh1_radio1", p.Option); ok {
			t.Errorf("%s must be removed by the wizard reset whitelist", p.Option)
		}
	}

	if vals, ok := reader.Get("wireless", "batmesh1_radio1", "key"); !ok || vals[0] != "k" {
		t.Errorf("key must survive the whitelist, got %v (ok=%v)", vals, ok)
	}
}

func TestEnsureSecondaryMeshPolicyOptions_AddsAllWhenMissing(t *testing.T) {
	reader := newWirelessMockReader()
	_ = reader.AddSection("wireless", "batmesh1_radio1", "wifi-iface")
	_ = reader.SetType("wireless", "batmesh1_radio1", "device", uci.TypeOption, "radio1")
	_ = reader.SetType("wireless", "batmesh1_radio1", "network", uci.TypeOption, "batmesh1")
	_ = reader.SetType("wireless", "batmesh1_radio1", "mode", uci.TypeOption, "mesh")

	added, err := EnsureSecondaryMeshPolicyOptions(reader, "batmesh1_radio1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []string{"mcast_rate", "mesh_nolearn", "mesh_retry_timeout", "mesh_confirm_timeout", "mesh_holding_timeout"}
	if !reflect.DeepEqual(added, want) {
		t.Errorf("added: got %v, want %v", added, want)
	}

	for _, p := range SecondaryMeshPolicyOptions() {
		vals, ok := reader.Get("wireless", "batmesh1_radio1", p.Option)
		if !ok || vals[0] != p.Value {
			t.Errorf("%s: got %v (ok=%v), want %s", p.Option, vals, ok, p.Value)
		}
	}

	if reader.commitCalled {
		t.Error("EnsureSecondaryMeshPolicyOptions must not commit; the caller owns the commit")
	}
}

func TestEnsureSecondaryMeshPolicyOptions_NoOpWhenPresent(t *testing.T) {
	reader := newWirelessMockReader()
	_ = reader.AddSection("wireless", "batmesh1_radio1", "wifi-iface")

	for _, p := range SecondaryMeshPolicyOptions() {
		_ = reader.SetType("wireless", "batmesh1_radio1", p.Option, uci.TypeOption, p.Value)
	}

	before := len(reader.setTypeCalls)

	added, err := EnsureSecondaryMeshPolicyOptions(reader, "batmesh1_radio1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(added) != 0 {
		t.Errorf("expected nothing added, got %v", added)
	}

	if len(reader.setTypeCalls) != before {
		t.Errorf("expected no SetType calls, got %d new", len(reader.setTypeCalls)-before)
	}
}

func TestEnsureSecondaryMeshPolicyOptions_PreservesOperatorValue(t *testing.T) {
	reader := newWirelessMockReader()
	_ = reader.AddSection("wireless", "batmesh1_radio1", "wifi-iface")
	_ = reader.SetType("wireless", "batmesh1_radio1", "mcast_rate", uci.TypeOption, "12000")

	added, err := EnsureSecondaryMeshPolicyOptions(reader, "batmesh1_radio1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []string{"mesh_nolearn", "mesh_retry_timeout", "mesh_confirm_timeout", "mesh_holding_timeout"}
	if !reflect.DeepEqual(added, want) {
		t.Errorf("added: got %v, want %v", added, want)
	}

	vals, _ := reader.Get("wireless", "batmesh1_radio1", "mcast_rate")
	if vals[0] != "12000" {
		t.Errorf("operator mcast_rate must be preserved, got %v", vals)
	}
}

func TestEnsureSecondaryMeshPolicyOptions_SetTypeError(t *testing.T) {
	reader := newWirelessMockReader()
	_ = reader.AddSection("wireless", "batmesh1_radio1", "wifi-iface")
	reader.setTypeError = errors.New("boom")

	_, err := EnsureSecondaryMeshPolicyOptions(reader, "batmesh1_radio1")
	if err == nil || !strings.Contains(err.Error(), "mcast_rate") {
		t.Fatalf("expected wrapped mcast_rate error, got %v", err)
	}
}

func TestEnsureSecondaryMeshPolicyOptions_EmptySection(t *testing.T) {
	if _, err := EnsureSecondaryMeshPolicyOptions(newWirelessMockReader(), ""); err == nil {
		t.Fatal("expected error for empty section name")
	}
}
