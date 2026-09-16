package network

import (
	"bufio"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/digineo/go-uci/v2"
)

const (
	defaultWirelessConfigPath string = "/etc/config/wireless"
	wirelessConfigName        string = "wireless"
	wifiIfaceSectionType      string = "wifi-iface"
	wifiDeviceSectionType     string = "wifi-device"

	// UCI option keys repeated across wifi-device and wifi-iface sections
	// and their whitelists. Centralized so goconst is happy.
	wirelessOptionBand                   string = "band"
	wirelessOptionCellDensity            string = "cell_density"
	wirelessOptionEnableDynamicPSOffload string = "enable_dynamic_ps_offload"
	wirelessOptionEnableMcastRateCtrl    string = "enable_mcast_rate_control"
	wirelessOptionEnableMcastWhitelist   string = "enable_mcast_whitelist"
	wirelessOptionDisabled               string = "disabled"
	wirelessOptionBeaconInt              string = "beacon_int"
	wirelessOptionBCF                    string = "bcf"
	wirelessOptionChannel                string = "channel"
	wirelessOptionEnablePS               string = "enable_ps"
	wirelessOptionKey                    string = "key"
	wirelessOptionEncryption             string = "encryption"
	wirelessOptionHTMode                 string = "htmode"
	wirelessOptionCountry                string = "country"
	wirelessOptionEnableTWT              string = "enable_twt"
	wirelessOptionMeshID                 string = "mesh_id"
	wirelessOptionSSID                   string = "ssid"
	wirelessOptionHWMode                 string = "hwmode"
	wirelessOptionTxPower                string = "txpower"
	wirelessOptionMode                   string = "mode"
	wirelessOptionPath                   string = "path"
	wirelessOptionReconf                 string = "reconf"
	wirelessOptionType                   string = "type"
	wirelessOptionMeshFwding             string = "mesh_fwding"
	wirelessOptionMeshRSSIThreshold      string = "mesh_rssi_threshold"
	wirelessOptionMcastRate              string = "mcast_rate"
	wirelessOptionMeshNolearn            string = "mesh_nolearn"
	wirelessOptionMeshRetryTimeout       string = "mesh_retry_timeout"
	wirelessOptionMeshConfirmTimeout     string = "mesh_confirm_timeout"
	wirelessOptionMeshHoldingTimeout     string = "mesh_holding_timeout"
)

// allWifiDeviceOptions enumerates every wifi-device option that
// UCIWirelessDevice can persist. WhitelistDeviceFields iterates this
// list to decide which options to delete on a reset; expanding the
// struct schema requires expanding this list as well.
var allWifiDeviceOptions = []string{ //nolint:gochecknoglobals // package-level constant
	wirelessOptionType, wirelessOptionPath, wirelessOptionBand, wirelessOptionHWMode, wirelessOptionHTMode, wirelessOptionReconf, wirelessOptionBCF,
	wirelessOptionCountry, wirelessOptionChannel, wirelessOptionCellDensity, wirelessOptionTxPower,
	wirelessOptionEnablePS, wirelessOptionEnableDynamicPSOffload, wirelessOptionEnableTWT,
	wirelessOptionEnableMcastWhitelist, wirelessOptionEnableMcastRateCtrl,
	wirelessOptionDisabled,
}

// allWifiIfaceOptions enumerates every wifi-iface option that
// UCIWirelessIface can persist. WhitelistInterfaceFields iterates this
// list to decide which options to delete on a wizard reset; expanding the
// struct schema requires expanding this list as well.
var allWifiIfaceOptions = []string{ //nolint:gochecknoglobals // package-level constant
	networkDeviceType, networkConfigName, wirelessOptionMode, wirelessOptionKey, wirelessOptionMeshID, wirelessOptionMeshFwding,
	wirelessOptionMeshRSSIThreshold, wirelessOptionEncryption, wirelessOptionSSID, wirelessOptionBeaconInt, wirelessOptionDisabled,
	wirelessOptionMcastRate, wirelessOptionMeshNolearn,
	wirelessOptionMeshRetryTimeout, wirelessOptionMeshConfirmTimeout, wirelessOptionMeshHoldingTimeout,
}

// WizardWifiDeviceWhitelist is the field whitelist applied to every
// wifi-device by the setup wizard's reset phase. Any option not in
// this list is removed. Notably, "disabled" is omitted so the device
// is implicitly re-enabled.
var WizardWifiDeviceWhitelist = []string{ //nolint:gochecknoglobals // package-level constant
	wirelessOptionType, wirelessOptionPath, wirelessOptionBand, wirelessOptionHWMode, wirelessOptionHTMode, wirelessOptionReconf, wirelessOptionBCF,
	wirelessOptionCountry, wirelessOptionChannel, wirelessOptionCellDensity, wirelessOptionTxPower,
	wirelessOptionEnablePS, wirelessOptionEnableDynamicPSOffload, wirelessOptionEnableTWT,
}

// WizardWifiIfaceWhitelist is the field whitelist applied to every
// wifi-iface by the setup wizard's reset phase. Any option not in
// this list is removed. The wizard re-applies "disabled" via
// DisableAllInterfaces immediately after.
var WizardWifiIfaceWhitelist = []string{ //nolint:gochecknoglobals // package-level constant
	networkConfigName, networkDeviceType, wirelessOptionKey, wirelessOptionEncryption, wirelessOptionMode, wirelessOptionSSID, wirelessOptionMeshID,
}

// UCIWirelessDevice represents a UCI wireless radio device (wifi-device section) configuration.
type UCIWirelessDevice struct {
	Type                   string `uci:"option type"`
	Path                   string `uci:"option path"`
	Band                   string `uci:"option band"`
	Channel                string `uci:"option channel"`
	HTMode                 string `uci:"option htmode"`
	Country                string `uci:"option country"`
	CellDensity            string `uci:"option cell_density"`
	HWMode                 string `uci:"option hwmode"`
	Reconf                 string `uci:"option reconf"`
	EnableMcastWhitelist   string `uci:"option enable_mcast_whitelist"`
	EnableMcastRateControl string `uci:"option enable_mcast_rate_control"`
	EnablePS               string `uci:"option enable_ps"`
	EnableDynamicPSOffload string `uci:"option enable_dynamic_ps_offload"`
	EnableTWT              string `uci:"option enable_twt"`
	BCF                    string `uci:"option bcf"`
	TxPower                string `uci:"option txpower"`
	Disabled               string `uci:"option disabled"`
}

// UCIWirelessIface represents a UCI wireless interface (wifi-iface section) configuration.
type UCIWirelessIface struct {
	Device            string `uci:"option device"`
	Network           string `uci:"option network"`
	Mode              string `uci:"option mode"`
	Key               string `uci:"option key"`
	MeshID            string `uci:"option mesh_id"`
	MeshFwding        string `uci:"option mesh_fwding"`
	MeshRSSIThreshold string `uci:"option mesh_rssi_threshold"`
	Encryption        string `uci:"option encryption"`
	SSID              string `uci:"option ssid"`
	BeaconInt         string `uci:"option beacon_int"`
	Disabled          string `uci:"option disabled"`

	// Secondary-link tuning written by ApplySecondaryMeshPolicy. Empty
	// values are skipped on write and the setter never deletes options.
	// The startup reconcile (EnsureSecondaryMeshPolicyOptions) adds only
	// what is missing, so a hand-edited value survives a boot; the wizard
	// and the wireless settings handler re-assert the full set on every
	// write they make.
	McastRate          string `uci:"option mcast_rate"`
	MeshNolearn        string `uci:"option mesh_nolearn"`
	MeshRetryTimeout   string `uci:"option mesh_retry_timeout"`
	MeshConfirmTimeout string `uci:"option mesh_confirm_timeout"`
	MeshHoldingTimeout string `uci:"option mesh_holding_timeout"`
}

// Mesh-link settings shared by the daemon's boot-time fallback
// (mgmt.setupBatMesh1Interface) and the setup wizard's phase 8. Both
// write the same wifi-iface for a batman-adv hardif, so the values live
// here rather than in either caller.
const (
	// WifiModeMesh is the wifi-iface `mode` value for 802.11s mesh links.
	WifiModeMesh = "mesh"

	// wifiEncryptionSAE is the only encryption a mesh link may use.
	wifiEncryptionSAE = "sae"

	// SecondaryMeshChannel2G and SecondaryMeshHTMode2G are the radio
	// settings applied to the 2.4 GHz radio that carries the secondary
	// batman-adv link (batmesh1): channel 8, 40 MHz HE.
	//
	// 40 MHz is the default because the link's job is a stable ~100 Mbps
	// between nearby nodes. At HE40 that floor is met around 2SS MCS 4-5
	// (~103-138 Mbps PHY), rates that hold at -65..-70 dBm on a moving
	// node. At HE20 the same throughput needs MCS 9-11 (256/1024-QAM),
	// which demands ~8-10 dB more SNR and collapses under motion. The
	// ~3 dB wideband noise penalty of 40 MHz is smaller than the MCS
	// margin it buys. A 40 MHz pair occupies half the 2.4 GHz band, so
	// 20 MHz stays selectable in the wizard for congested spectrum.
	// OpenWrt 24.10 wifi-scripts set noscan for any radio carrying a mesh
	// iface, which is what allows a >20 MHz mesh on 2.4 GHz.
	SecondaryMeshChannel2G = "8"
	SecondaryMeshHTMode2G  = "HE40"

	// SecondaryMeshRSSIThreshold is the mesh_rssi_threshold (dBm) applied
	// to the 2.4 GHz secondary link. wpa_supplicant treats -255..-1 as a
	// dBm floor for accepting a mesh peer, 0 as "no threshold", and 1 as
	// "leave the driver default alone".
	//
	// The secondary link exists to carry traffic between nodes close
	// enough for 2.4 GHz to be decisively faster than the 900 MHz primary
	// (S1G/HaLow) link, not to extend range. The primary link owns range.
	// So the threshold is set at the crossover point rather than at the
	// edge of usability: a 2.4 GHz link that is merely *reachable* is
	// worse than no 2.4 GHz link at all, because batman-adv will use it
	// and the low MCS rate consumes airtime out of all proportion to the
	// traffic it carries.
	//
	// -80 dBm holds HE20 MCS2/MCS3 (~14-20 Mbps of real throughput) with
	// a few dB of fade margin for mobile nodes, against roughly 1-4 Mbps
	// from a 2 MHz HaLow channel -- several times faster, which is the
	// bar. Dropping to -85 would admit MCS0/MCS1 links at ~4-9 Mbps that
	// flap under motion and are not worth preferring over the primary.
	SecondaryMeshRSSIThreshold = "-80"

	// SecondaryMeshMcastRate is the mcast_rate (kbit/s) for the 2.4 GHz
	// secondary link. Without it every group-addressed frame on the
	// 802.11s link -- batman-adv OGMs and ELP probes, plus every ATAK and
	// voice multicast packet that batman-adv floods as broadcast -- goes
	// out at the lowest legacy rate (1 Mbps) and each of batman-adv's
	// three per-hardif retransmissions burns ~24x the airtime it needs.
	// 24 Mbps OFDM is a mandatory 802.11g rate that any peer admitted at
	// or above SecondaryMeshRSSIThreshold can decode.
	SecondaryMeshMcastRate = "24000"

	// SecondaryMeshNolearn disables 802.11s path learning on the link.
	// batman-adv owns routing; the wizard already writes mesh_nolearn=1
	// into mesh11sd.mesh_params, but the Morse-patched mesh11sd applies
	// that only to the S1G interface, so the 2.4 GHz link needs the
	// option on its own wifi-iface.
	SecondaryMeshNolearn = "1"

	// SecondaryMeshPlinkTimeoutMs is written to mesh_retry_timeout,
	// mesh_confirm_timeout and mesh_holding_timeout (all in ms, max
	// 255). Maxing them stops a node that reconnects quickly after a
	// fade from re-peering faster than its neighbours finish tearing the
	// old link down, which otherwise ends in the nl80211 "key addition
	// failed" path and a 300 s SAE peer block -- a mobile-node failure
	// mode documented by upstream mesh11sd v5.0.0.
	SecondaryMeshPlinkTimeoutMs = "255"
)

// MeshLink describes one 802.11s wifi-iface bound to a batman-adv hardif.
type MeshLink struct {
	Radio         string // wifi-device section, e.g. "radio0"
	Network       string // batadv_hardif interface, e.g. BatmanSecondaryIface
	MeshID        string
	Key           string
	RSSIThreshold string // mesh_rssi_threshold in dBm; empty omits the option
}

// Section returns the wifi-iface section name for the link,
// "<Network>_<Radio>" (e.g. "batmesh1_radio0"). The primary link keeps
// the factory "default_<radio>" section, so callers use Section only for
// secondary hardifs — the name can never collide with the AP section the
// wizard writes on the same radio.
func (l MeshLink) Section() string {
	return l.Network + "_" + l.Radio
}

// IfaceConfig returns the wifi-iface option set for the link: the hardif
// binding, mode=mesh, the credentials, mesh_fwding=0 (batman-adv does
// the forwarding), the RSSI threshold when set, encryption=sae, and,
// when the link is the secondary hardif (BatmanSecondaryIface), the
// tuning from ApplySecondaryMeshPolicy.
func (l MeshLink) IfaceConfig() *UCIWirelessIface {
	cfg := &UCIWirelessIface{
		Device:            l.Radio,
		Network:           l.Network,
		Mode:              WifiModeMesh,
		MeshID:            l.MeshID,
		Key:               l.Key,
		MeshFwding:        "0",
		MeshRSSIThreshold: l.RSSIThreshold,
		Encryption:        wifiEncryptionSAE,
	}
	if l.Network == BatmanSecondaryIface {
		cfg.ApplySecondaryMeshPolicy()
	}

	return cfg
}

// ApplySecondaryMeshPolicy sets the daemon-owned tuning options for the
// 2.4 GHz secondary link on c. MeshLink.IfaceConfig and the wireless
// settings handler both call it so the option set has one definition;
// SecondaryMeshPolicyOptions is the same set as option/value pairs.
func (c *UCIWirelessIface) ApplySecondaryMeshPolicy() {
	c.McastRate = SecondaryMeshMcastRate
	c.MeshNolearn = SecondaryMeshNolearn
	c.MeshRetryTimeout = SecondaryMeshPlinkTimeoutMs
	c.MeshConfirmTimeout = SecondaryMeshPlinkTimeoutMs
	c.MeshHoldingTimeout = SecondaryMeshPlinkTimeoutMs
}

// SecondaryMeshPolicyOption is one tuning option the daemon owns on the
// batmesh1 wifi-iface.
type SecondaryMeshPolicyOption struct {
	Option string
	Value  string
}

// SecondaryMeshPolicyOptions returns the options and values every
// batmesh1 wifi-iface must carry, in write order. It must stay in step
// with ApplySecondaryMeshPolicy; a test pins the two together.
func SecondaryMeshPolicyOptions() []SecondaryMeshPolicyOption {
	return []SecondaryMeshPolicyOption{
		{Option: wirelessOptionMcastRate, Value: SecondaryMeshMcastRate},
		{Option: wirelessOptionMeshNolearn, Value: SecondaryMeshNolearn},
		{Option: wirelessOptionMeshRetryTimeout, Value: SecondaryMeshPlinkTimeoutMs},
		{Option: wirelessOptionMeshConfirmTimeout, Value: SecondaryMeshPlinkTimeoutMs},
		{Option: wirelessOptionMeshHoldingTimeout, Value: SecondaryMeshPlinkTimeoutMs},
	}
}

// EnsureSecondaryMeshPolicyOptions adds every SecondaryMeshPolicyOptions
// entry that section lacks and returns the option names it added, in
// policy order. Options already present keep their value, whatever it
// is, so a hand-edited section is never overwritten. It does not
// commit; the caller decides whether a reload is warranted.
func EnsureSecondaryMeshPolicyOptions(reader ConfigReader, section string) ([]string, error) {
	if section == "" {
		return nil, fmt.Errorf("section cannot be empty")
	}

	policy := SecondaryMeshPolicyOptions()
	added := make([]string, 0, len(policy))

	for _, p := range policy {
		if _, exists := reader.Get(wirelessConfigName, section, p.Option); exists {
			continue
		}

		if err := reader.SetType(wirelessConfigName, section, p.Option, uci.TypeOption, p.Value); err != nil {
			return added, fmt.Errorf("setting %s.%s.%s: %w", wirelessConfigName, section, p.Option, err)
		}

		added = append(added, p.Option)
	}

	return added, nil
}

// UCIWirelessConfigReader wraps the UCI functions for wireless configuration.
type UCIWirelessConfigReader struct {
	tree uci.Tree
}

// NewUCIWirelessConfigReader creates a new UCI wireless config reader with the default tree.
func NewUCIWirelessConfigReader() *UCIWirelessConfigReader {
	return &UCIWirelessConfigReader{
		tree: uci.NewTree(uci.DefaultTreePath),
	}
}

// Tree exposes the underlying digineo go-uci tree so other packages
// (e.g. the setup wizard's UCI snapshotter) can call methods like
// LoadConfig(name, true) on it. The setup wizard uses this to trigger
// a re-read of the on-disk config files into the in-memory tree
// after a snapshot restore overwrites those files.
func (r *UCIWirelessConfigReader) Tree() uci.Tree {
	return r.tree
}

func (r *UCIWirelessConfigReader) Get(config, section, option string) ([]string, bool) {
	return r.tree.Get(config, section, option)
}

func (r *UCIWirelessConfigReader) GetSections(config, secType string) ([]string, error) {
	return r.tree.GetSections(config, secType)
}

func (r *UCIWirelessConfigReader) SetType(config, section, option string, typ uci.OptionType, values ...string) error {
	return r.tree.SetType(config, section, option, typ, values...)
}

func (r *UCIWirelessConfigReader) Del(config, section, option string) error {
	return r.tree.Del(config, section, option)
}

func (r *UCIWirelessConfigReader) AddSection(config, section, typ string) error {
	return r.tree.AddSection(config, section, typ)
}

func (r *UCIWirelessConfigReader) DelSection(config, section string) error {
	return r.tree.DelSection(config, section)
}

func (r *UCIWirelessConfigReader) Commit() error {
	return r.tree.Commit()
}

func (r *UCIWirelessConfigReader) ReloadConfig() error {
	return r.tree.LoadConfig(wirelessConfigName, true)
}

type wirelessSection struct {
	options map[string]string
	typ     string
}

// GetWirelessMeshPassphrase returns the first enabled mesh interface passphrase
// from the OpenWrt wireless UCI configuration.
func GetWirelessMeshPassphrase() (string, error) {
	return GetWirelessMeshPassphraseFromPath(defaultWirelessConfigPath)
}

// GetWirelessMeshPassphraseFromPath returns the first enabled mesh interface
// passphrase from the provided OpenWrt wireless config path.
func GetWirelessMeshPassphraseFromPath(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("failed to open wireless config %s: %w", path, err)
	}
	defer file.Close()

	var (
		current      *wirelessSection
		meshFound    bool
		meshMissingK bool
	)

	finalize := func(section *wirelessSection) (string, bool) {
		if section == nil || section.typ != wifiIfaceSectionType {
			return "", false
		}

		if strings.ToLower(strings.TrimSpace(section.options["mode"])) != "mesh" { //nolint:goconst
			return "", false
		}

		if strings.TrimSpace(section.options["disabled"]) == "1" {
			return "", false
		}

		meshFound = true

		key := strings.TrimSpace(section.options["key"])
		if key == "" {
			meshMissingK = true

			return "", false
		}

		return key, true
	}

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		if typ, _, ok := parseUCIConfigLine(line); ok {
			if key, found := finalize(current); found {
				return key, nil
			}

			current = &wirelessSection{
				typ:     typ,
				options: map[string]string{},
			}

			continue
		}

		if current == nil {
			continue
		}

		if option, value, ok := parseUCIOptionLine(line); ok {
			current.options[option] = value
		}
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("failed to read wireless config %s: %w", path, err)
	}

	if key, found := finalize(current); found {
		return key, nil
	}

	if !meshFound {
		return "", fmt.Errorf("no enabled mesh wifi-iface section found in %s", path)
	}

	if meshMissingK {
		return "", fmt.Errorf("enabled mesh wifi-iface section missing key in %s", path)
	}

	return "", fmt.Errorf("mesh key not found in %s", path)
}

func parseUCIConfigLine(line string) (string, string, bool) {
	fields := strings.Fields(line)
	if len(fields) < 2 || fields[0] != "config" {
		return "", "", false
	}

	typ := trimUCIValue(fields[1])

	name := ""
	if len(fields) > 2 {
		name = trimUCIValue(fields[2])
	}

	return typ, name, true
}

func parseUCIOptionLine(line string) (string, string, bool) {
	if !strings.HasPrefix(line, "option") {
		return "", "", false
	}

	if len(line) > len("option") {
		next := line[len("option")]
		if next != ' ' && next != '\t' {
			return "", "", false
		}
	}

	rest := strings.TrimSpace(strings.TrimPrefix(line, "option"))
	if rest == "" {
		return "", "", false
	}

	spaceIdx := strings.IndexAny(rest, " \t")
	if spaceIdx == -1 {
		return "", "", false
	}

	name := strings.TrimSpace(rest[:spaceIdx])
	if name == "" {
		return "", "", false
	}

	value := trimUCIValue(strings.TrimSpace(rest[spaceIdx+1:]))

	return name, value, true
}

func trimUCIValue(v string) string {
	v = strings.TrimSpace(v)
	if len(v) < 2 {
		return v
	}

	first := v[0]
	last := v[len(v)-1]

	if (first == '\'' && last == '\'') || (first == '"' && last == '"') {
		return v[1 : len(v)-1]
	}

	return v
}

// GetWirelessDeviceByName loads and returns the UCI wireless device configuration by name.
//
// Parameters:
//   - name: The UCI section name (e.g., "radio0", "radio1")
//
// Returns the device configuration or an error if it cannot be read.
//
// Example:
//
//	cfg, err := GetWirelessDeviceByName("radio0")
//	if err != nil {
//	    log.Fatalf("Failed to get wireless device: %v", err)
//	}
//	fmt.Printf("Band: %s, Channel: %s\n", cfg.Band, cfg.Channel)
func GetWirelessDeviceByName(name string) (*UCIWirelessDevice, error) {
	return GetWirelessDeviceByNameWithReader(name, NewUCIWirelessConfigReader())
}

// GetWirelessDeviceByNameWithReader loads and returns the UCI wireless device configuration by
// name using the provided reader.
func GetWirelessDeviceByNameWithReader(name string, reader ConfigReader) (*UCIWirelessDevice, error) { //nolint:gocyclo,gocognit
	config := &UCIWirelessDevice{}

	if values, ok := reader.Get(wirelessConfigName, name, "type"); ok && len(values) > 0 {
		config.Type = values[0]
	}

	if values, ok := reader.Get(wirelessConfigName, name, "path"); ok && len(values) > 0 {
		config.Path = values[0]
	}

	if values, ok := reader.Get(wirelessConfigName, name, "band"); ok && len(values) > 0 {
		config.Band = values[0]
	}

	if values, ok := reader.Get(wirelessConfigName, name, "channel"); ok && len(values) > 0 {
		config.Channel = values[0]
	}

	if values, ok := reader.Get(wirelessConfigName, name, "htmode"); ok && len(values) > 0 {
		config.HTMode = values[0]
	}

	if values, ok := reader.Get(wirelessConfigName, name, "country"); ok && len(values) > 0 {
		config.Country = values[0]
	}

	if values, ok := reader.Get(wirelessConfigName, name, "cell_density"); ok && len(values) > 0 {
		config.CellDensity = values[0]
	}

	if values, ok := reader.Get(wirelessConfigName, name, "hwmode"); ok && len(values) > 0 {
		config.HWMode = values[0]
	}

	if values, ok := reader.Get(wirelessConfigName, name, wirelessOptionReconf); ok && len(values) > 0 {
		config.Reconf = values[0]
	}

	if values, ok := reader.Get(wirelessConfigName, name, "enable_mcast_whitelist"); ok && len(values) > 0 {
		config.EnableMcastWhitelist = values[0]
	}

	if values, ok := reader.Get(wirelessConfigName, name, "enable_mcast_rate_control"); ok && len(values) > 0 {
		config.EnableMcastRateControl = values[0]
	}

	if values, ok := reader.Get(wirelessConfigName, name, "enable_ps"); ok && len(values) > 0 {
		config.EnablePS = values[0]
	}

	if values, ok := reader.Get(wirelessConfigName, name, "enable_dynamic_ps_offload"); ok && len(values) > 0 {
		config.EnableDynamicPSOffload = values[0]
	}

	if values, ok := reader.Get(wirelessConfigName, name, "enable_twt"); ok && len(values) > 0 {
		config.EnableTWT = values[0]
	}

	if values, ok := reader.Get(wirelessConfigName, name, "bcf"); ok && len(values) > 0 {
		config.BCF = values[0]
	}

	if values, ok := reader.Get(wirelessConfigName, name, "txpower"); ok && len(values) > 0 {
		config.TxPower = values[0]
	}

	if values, ok := reader.Get(wirelessConfigName, name, "disabled"); ok && len(values) > 0 {
		config.Disabled = values[0]
	}

	return config, nil
}

// ifaceOptionFields maps every wifi-iface option to its field on cfg, in
// write order. Shared by the getter and the setter so the two cannot
// drift.
func ifaceOptionFields(cfg *UCIWirelessIface) []struct {
	field  *string
	option string
} {
	return []struct {
		field  *string
		option string
	}{
		{field: &cfg.Device, option: networkDeviceType},
		{field: &cfg.Network, option: networkConfigName},
		{field: &cfg.Mode, option: wirelessOptionMode},
		{field: &cfg.Key, option: wirelessOptionKey},
		{field: &cfg.MeshID, option: wirelessOptionMeshID},
		{field: &cfg.MeshFwding, option: wirelessOptionMeshFwding},
		{field: &cfg.MeshRSSIThreshold, option: wirelessOptionMeshRSSIThreshold},
		{field: &cfg.Encryption, option: wirelessOptionEncryption},
		{field: &cfg.SSID, option: wirelessOptionSSID},
		{field: &cfg.BeaconInt, option: wirelessOptionBeaconInt},
		{field: &cfg.Disabled, option: wirelessOptionDisabled},
		{field: &cfg.McastRate, option: wirelessOptionMcastRate},
		{field: &cfg.MeshNolearn, option: wirelessOptionMeshNolearn},
		{field: &cfg.MeshRetryTimeout, option: wirelessOptionMeshRetryTimeout},
		{field: &cfg.MeshConfirmTimeout, option: wirelessOptionMeshConfirmTimeout},
		{field: &cfg.MeshHoldingTimeout, option: wirelessOptionMeshHoldingTimeout},
	}
}

// GetWirelessIfaceByName loads and returns the UCI wireless interface configuration by name.
//
// Parameters:
//   - name: The UCI section name (e.g., "default_radio0", "mesh0")
//
// Returns the interface configuration or an error if it cannot be read.
//
// Example:
//
//	cfg, err := GetWirelessIfaceByName("default_radio0")
//	if err != nil {
//	    log.Fatalf("Failed to get wireless iface: %v", err)
//	}
//	fmt.Printf("Mode: %s, Encryption: %s\n", cfg.Mode, cfg.Encryption)
func GetWirelessIfaceByName(name string) (*UCIWirelessIface, error) {
	return GetWirelessIfaceByNameWithReader(name, NewUCIWirelessConfigReader())
}

// GetWirelessIfaceByNameWithReader loads and returns the UCI wireless interface configuration by
// name using the provided reader.
func GetWirelessIfaceByNameWithReader(name string, reader ConfigReader) (*UCIWirelessIface, error) {
	config := &UCIWirelessIface{}

	for _, f := range ifaceOptionFields(config) {
		if values, ok := reader.Get(wirelessConfigName, name, f.option); ok && len(values) > 0 {
			*f.field = values[0]
		}
	}

	return config, nil
}

// SetWirelessDeviceConfig creates or updates a wireless radio device configuration.
//
// Parameters:
//   - section: The UCI section name (e.g., "radio0", "radio1")
//   - config: The wireless device configuration to set
//
// Returns an error if the configuration cannot be saved.
//
// Example:
//
//	cfg := &UCIWirelessDevice{
//	    Type:    "mac80211",
//	    Band:    "2g",
//	    Channel: "6",
//	    HTMode:  "HT20",
//	    Country: "US",
//	}
//	err := SetWirelessDeviceConfig("radio0", cfg)
func SetWirelessDeviceConfig(section string, config *UCIWirelessDevice) error {
	return SetWirelessDeviceConfigWithReader(section, config, NewUCIWirelessConfigReader())
}

// SetWirelessDeviceConfigWithReader creates or updates a wireless device configuration using
// the provided reader.
func SetWirelessDeviceConfigWithReader(section string, config *UCIWirelessDevice, reader ConfigReader) error { //nolint:gocognit,gocyclo
	if config == nil {
		return fmt.Errorf("config cannot be nil")
	}

	_ = reader.AddSection(wirelessConfigName, section, wifiDeviceSectionType)

	if config.Type != "" {
		if err := reader.SetType(wirelessConfigName, section, "type", uci.TypeOption, config.Type); err != nil {
			return fmt.Errorf("failed to set type: %w", err)
		}
	}

	if config.Path != "" {
		if err := reader.SetType(wirelessConfigName, section, "path", uci.TypeOption, config.Path); err != nil {
			return fmt.Errorf("failed to set path: %w", err)
		}
	}

	if config.Band != "" {
		if err := reader.SetType(wirelessConfigName, section, "band", uci.TypeOption, config.Band); err != nil {
			return fmt.Errorf("failed to set band: %w", err)
		}
	}

	if config.Channel != "" {
		if err := reader.SetType(wirelessConfigName, section, "channel", uci.TypeOption, config.Channel); err != nil {
			return fmt.Errorf("failed to set channel: %w", err)
		}
	}

	if config.HTMode != "" {
		if err := reader.SetType(wirelessConfigName, section, "htmode", uci.TypeOption, config.HTMode); err != nil {
			return fmt.Errorf("failed to set htmode: %w", err)
		}
	}

	if config.Country != "" {
		if err := reader.SetType(wirelessConfigName, section, "country", uci.TypeOption, config.Country); err != nil {
			return fmt.Errorf("failed to set country: %w", err)
		}
	}

	if config.CellDensity != "" {
		if err := reader.SetType(wirelessConfigName, section, "cell_density", uci.TypeOption, config.CellDensity); err != nil {
			return fmt.Errorf("failed to set cell_density: %w", err)
		}
	}

	if config.HWMode != "" {
		if err := reader.SetType(wirelessConfigName, section, "hwmode", uci.TypeOption, config.HWMode); err != nil {
			return fmt.Errorf("failed to set hwmode: %w", err)
		}
	}

	if config.Reconf != "" {
		if err := reader.SetType(wirelessConfigName, section, wirelessOptionReconf, uci.TypeOption, config.Reconf); err != nil {
			return fmt.Errorf("failed to set reconf: %w", err)
		}
	}

	if config.EnableMcastWhitelist != "" {
		if err := reader.SetType(wirelessConfigName, section, "enable_mcast_whitelist", uci.TypeOption, config.EnableMcastWhitelist); err != nil {
			return fmt.Errorf("failed to set enable_mcast_whitelist: %w", err)
		}
	}

	if config.EnableMcastRateControl != "" {
		if err := reader.SetType(wirelessConfigName, section, "enable_mcast_rate_control", uci.TypeOption, config.EnableMcastRateControl); err != nil {
			return fmt.Errorf("failed to set enable_mcast_rate_control: %w", err)
		}
	}

	if config.EnablePS != "" {
		if err := reader.SetType(wirelessConfigName, section, "enable_ps", uci.TypeOption, config.EnablePS); err != nil {
			return fmt.Errorf("failed to set enable_ps: %w", err)
		}
	}

	if config.EnableDynamicPSOffload != "" {
		if err := reader.SetType(wirelessConfigName, section, "enable_dynamic_ps_offload", uci.TypeOption, config.EnableDynamicPSOffload); err != nil {
			return fmt.Errorf("failed to set enable_dynamic_ps_offload: %w", err)
		}
	}

	if config.EnableTWT != "" {
		if err := reader.SetType(wirelessConfigName, section, "enable_twt", uci.TypeOption, config.EnableTWT); err != nil {
			return fmt.Errorf("failed to set enable_twt: %w", err)
		}
	}

	if config.BCF != "" {
		if err := reader.SetType(wirelessConfigName, section, "bcf", uci.TypeOption, config.BCF); err != nil {
			return fmt.Errorf("failed to set bcf: %w", err)
		}
	}

	if config.TxPower != "" {
		if err := reader.SetType(wirelessConfigName, section, "txpower", uci.TypeOption, config.TxPower); err != nil {
			return fmt.Errorf("failed to set txpower: %w", err)
		}
	}

	if config.Disabled != "" {
		if err := reader.SetType(wirelessConfigName, section, "disabled", uci.TypeOption, config.Disabled); err != nil {
			return fmt.Errorf("failed to set disabled: %w", err)
		}
	}

	if err := reader.Commit(); err != nil {
		return fmt.Errorf("failed to commit wireless config: %w", err)
	}

	return nil
}

// SetWirelessIfaceConfig creates or updates a wireless interface configuration.
//
// Parameters:
//   - section: The UCI section name (e.g., "default_radio0", "mesh0")
//   - config: The wireless interface configuration to set
//
// Returns an error if the configuration cannot be saved.
//
// Example:
//
//	cfg := &UCIWirelessIface{
//	    Device:     "radio0",
//	    Network:    "batmesh0",
//	    Mode:       "mesh",
//	    MeshID:     "mymesh",
//	    Encryption: "sae",
//	    Key:        "mysecretkey",
//	}
//	err := SetWirelessIfaceConfig("mesh0", cfg)
func SetWirelessIfaceConfig(section string, config *UCIWirelessIface) error {
	return SetWirelessIfaceConfigWithReader(section, config, NewUCIWirelessConfigReader())
}

// SetWirelessIfaceConfigWithReader creates or updates a wireless interface configuration using
// the provided reader.
func SetWirelessIfaceConfigWithReader(section string, config *UCIWirelessIface, reader ConfigReader) error {
	if config == nil {
		return fmt.Errorf("config cannot be nil")
	}

	_ = reader.AddSection(wirelessConfigName, section, "wifi-iface")

	for _, f := range ifaceOptionFields(config) {
		if *f.field == "" {
			continue
		}

		if err := reader.SetType(wirelessConfigName, section, f.option, uci.TypeOption, *f.field); err != nil {
			return fmt.Errorf("failed to set %s: %w", f.option, err)
		}
	}

	if err := reader.Commit(); err != nil {
		return fmt.Errorf("failed to commit wireless config: %w", err)
	}

	return nil
}

// DeleteWirelessDevice removes a wireless radio device (wifi-device) configuration section.
//
// Parameters:
//   - section: The UCI section name to delete (e.g., "radio0", "radio1")
//
// Returns an error if the section cannot be deleted.
//
// Example:
//
//	err := DeleteWirelessDevice("radio0")
func DeleteWirelessDevice(section string) error {
	return DeleteWirelessDeviceWithReader(section, NewUCIWirelessConfigReader())
}

// DeleteWirelessDeviceWithReader removes a wireless device configuration section using the
// provided reader.
func DeleteWirelessDeviceWithReader(section string, reader ConfigReader) error {
	if err := reader.DelSection(wirelessConfigName, section); err != nil {
		return fmt.Errorf("failed to delete wireless device section: %w", err)
	}

	if err := reader.Commit(); err != nil {
		return fmt.Errorf("failed to commit wireless config: %w", err)
	}

	return nil
}

// DeleteWirelessIface removes a wireless interface (wifi-iface) configuration section.
//
// Parameters:
//   - section: The UCI section name to delete (e.g., "default_radio0", "mesh0")
//
// Returns an error if the section cannot be deleted.
//
// Example:
//
//	err := DeleteWirelessIface("default_radio0")
func DeleteWirelessIface(section string) error {
	return DeleteWirelessIfaceWithReader(section, NewUCIWirelessConfigReader())
}

// DeleteWirelessIfaceWithReader removes a wireless interface configuration section using the
// provided reader.
func DeleteWirelessIfaceWithReader(section string, reader ConfigReader) error {
	if err := reader.DelSection(wirelessConfigName, section); err != nil {
		return fmt.Errorf("failed to delete wireless iface section: %w", err)
	}

	if err := reader.Commit(); err != nil {
		return fmt.Errorf("failed to commit wireless config: %w", err)
	}

	return nil
}

// WhitelistDeviceFields removes every wifi-device option NOT in the
// whitelist from the named device section. Mirrors LuCI's
// whitelistFields() applied to a `wifi-device`. The wizard reset
// phase passes WizardWifiDeviceWhitelist; tests can pass arbitrary
// subsets to verify behavior.
//
// Does not commit — the caller batches multiple resets and commits
// once at the end of the reset phase.
func WhitelistDeviceFields(reader ConfigReader, deviceName string, allowList []string) error {
	if deviceName == "" {
		return fmt.Errorf("deviceName cannot be empty")
	}

	for _, option := range allWifiDeviceOptions {
		if slices.Contains(allowList, option) {
			continue
		}

		if _, exists := reader.Get(wirelessConfigName, deviceName, option); !exists {
			continue
		}

		if err := reader.Del(wirelessConfigName, deviceName, option); err != nil {
			return fmt.Errorf("deleting %s.%s.%s: %w",
				wirelessConfigName, deviceName, option, err)
		}
	}

	return nil
}

// WhitelistInterfaceFields removes every wifi-iface option NOT in
// the whitelist from the named interface section. Mirrors LuCI's
// whitelistFields() applied to a `wifi-iface`.
//
// Does not commit.
func WhitelistInterfaceFields(reader ConfigReader, ifaceName string, allowList []string) error {
	if ifaceName == "" {
		return fmt.Errorf("ifaceName cannot be empty")
	}

	for _, option := range allWifiIfaceOptions {
		if slices.Contains(allowList, option) {
			continue
		}

		if _, exists := reader.Get(wirelessConfigName, ifaceName, option); !exists {
			continue
		}

		if err := reader.Del(wirelessConfigName, ifaceName, option); err != nil {
			return fmt.Errorf("deleting %s.%s.%s: %w",
				wirelessConfigName, ifaceName, option, err)
		}
	}

	return nil
}

// DisableAllInterfaces sets `disabled='1'` on every wifi-iface in
// the wireless config. The wizard calls this immediately after
// whitelisting interface fields so a "disabled" attribute that the
// whitelist removed is reapplied uniformly. The wizard then
// individually re-enables only the interfaces it intends to keep.
//
// Does not commit.
func DisableAllInterfaces(reader ConfigReader) error {
	sections, err := reader.GetSections(wirelessConfigName, wifiIfaceSectionType)
	if err != nil {
		return fmt.Errorf("listing wifi-iface sections: %w", err)
	}

	for _, s := range sections {
		if err := reader.SetType(wirelessConfigName, s, "disabled", uci.TypeOption, "1"); err != nil {
			return fmt.Errorf("disabling %s: %w", s, err)
		}
	}

	return nil
}

// IsMorseDevice reports whether the named wifi-device section is a
// Morse Micro HaLow radio (`option type 'morse'`). The comparison is
// case-insensitive, matching the wizard's HaLow detection.
func IsMorseDevice(reader ConfigReader, deviceName string) bool {
	typ, ok := reader.Get(wirelessConfigName, deviceName, "type")
	if !ok || len(typ) == 0 {
		return false
	}

	return strings.EqualFold(typ[0], "morse")
}

// RemoveNonMeshIfacesOnMorseDevices deletes every wifi-iface whose
// `device` is a type=morse wifi-device and whose `mode` is anything
// but mesh (a missing mode counts: netifd defaults it to ap). A HaLow
// radio only ever carries 802.11s mesh links; an AP or STA section on
// one is a leftover from an older wizard build (the meshap_<radio>
// overlay) or a hand edit, and would let the settings UI bring up an
// AP on the mesh radio. Returns the deleted section names. Does not
// commit.
func RemoveNonMeshIfacesOnMorseDevices(reader ConfigReader) ([]string, error) {
	devices, err := reader.GetSections(wirelessConfigName, wifiDeviceSectionType)
	if err != nil {
		return nil, fmt.Errorf("listing wifi-device sections: %w", err)
	}

	morse := make(map[string]struct{}, len(devices))

	for _, dev := range devices {
		if IsMorseDevice(reader, dev) {
			morse[dev] = struct{}{}
		}
	}

	if len(morse) == 0 {
		return nil, nil
	}

	ifaces, err := reader.GetSections(wirelessConfigName, wifiIfaceSectionType)
	if err != nil {
		return nil, fmt.Errorf("listing wifi-iface sections: %w", err)
	}

	var removed []string

	for _, iface := range ifaces {
		dev, ok := reader.Get(wirelessConfigName, iface, "device")
		if !ok || len(dev) == 0 {
			continue
		}

		if _, onMorse := morse[dev[0]]; !onMorse {
			continue
		}

		if mode, _ := reader.Get(wirelessConfigName, iface, "mode"); len(mode) > 0 && mode[0] == WifiModeMesh {
			continue
		}

		if err := reader.DelSection(wirelessConfigName, iface); err != nil {
			return removed, fmt.Errorf("deleting %s: %w", iface, err)
		}

		removed = append(removed, iface)
	}

	return removed, nil
}
