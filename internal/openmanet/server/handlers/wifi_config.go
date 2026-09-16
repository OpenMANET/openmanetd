package handlers

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/mdlayher/wifi"
	wificonfigv1 "github.com/openmanet/openmanetd/internal/api/openmanet/wifi_config/v1"
	batmanadv "github.com/openmanet/openmanetd/internal/batman-adv"
	"github.com/openmanet/openmanetd/internal/iwinfo"
	"github.com/openmanet/openmanetd/internal/mgmt"
	"github.com/openmanet/openmanetd/internal/network"
	"github.com/rs/zerolog"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/emptypb"
)

// htmode constants and their formatted bandwidth strings used by both
// FormatBandwidthDisplay and HTModeFromBandwidth. Centralized so a new
// htmode value is a single edit and so goconst is satisfied.
const (
	htModeNoHT   = "NOHT"
	htModeHT20   = "HT20"
	htModeHT40   = "HT40"
	htModeVHT20  = "VHT20"
	htModeVHT40  = "VHT40"
	htModeVHT80  = "VHT80"
	htModeVHT160 = "VHT160"
	htModeHE20   = "HE20"
	htModeHE40   = "HE40"
	htModeHE80   = "HE80"
	htModeHE160  = "HE160"

	bandwidth20MHz  = "20 MHz"
	bandwidth40MHz  = "40 MHz"
	bandwidth80MHz  = "80 MHz"
	bandwidth160MHz = "160 MHz"

	wifiAuthSAE = "sae"
)

// WifiConfigService implements the wifi_configv1connect.WifiConfigServiceHandler.
type WifiConfigService struct {
	Log            zerolog.Logger
	IwinfoClient   iwinfo.IwinfoProvider
	Wifi           mgmt.WirelessProvider
	WirelessStatus network.WirelessStatusProvider
	ConfigReader   network.ConfigReader
	ParseBatHosts  func(string) (*batmanadv.BatHosts, error)
	DHCPLeases     LeaseProvider
	// GetMeshNeighbors can be overridden for testing; defaults to batmanadv.GetMeshNeighbors.
	GetMeshNeighbors func() (*batmanadv.Neighbors, error)
	// ReloadServices hands the committed UCI to the running system after
	// a radio write. nil falls back to network.ForceReloadConfig
	// (`reload_config`), which has procd reload `network` for the changed
	// wireless/network configs so netifd re-syncs the radios.
	ReloadServices func(ctx context.Context) error

	mu sync.Mutex // serializes UCI writes
}

// reloadServicesTimeout bounds the detached reload_config run.
const reloadServicesTimeout = 30 * time.Second

// RadioSettingsUpdate is one radio's new settings inside a batch apply.
type RadioSettingsUpdate struct {
	Settings  *wificonfigv1.RadioSettings
	RadioName string
}

// RadioApplier is the slice of WifiConfigService other handlers use to
// read and write radio settings under the same UCI write lock.
type RadioApplier interface {
	CurrentRadioSettings(radioName string) (*wificonfigv1.RadioSettings, error)
	ApplyRadioSettingsBatch(ctx context.Context, updates []RadioSettingsUpdate) error
}

// stageWriteError reports a UCI write failure while staging radio
// settings. UpdateRadioSettings surfaces it as Success=false with the
// message (its historical contract) instead of an RPC error.
type stageWriteError struct {
	msg string
}

func (e *stageWriteError) Error() string {
	return e.msg
}

func (s *WifiConfigService) parseBatHosts(path string) (*batmanadv.BatHosts, error) {
	if s.ParseBatHosts != nil {
		return s.ParseBatHosts(path)
	}

	return batmanadv.ParseBatHostsFile(path)
}

func (s *WifiConfigService) getMeshNeighbors() (*batmanadv.Neighbors, error) {
	if s.GetMeshNeighbors != nil {
		return s.GetMeshNeighbors()
	}

	return batmanadv.GetMeshNeighbors()
}

// ListRadios returns all physical radios available on the device.
func (s *WifiConfigService) ListRadios(ctx context.Context, _ *emptypb.Empty) (*wificonfigv1.ListRadiosResponse, error) {
	s.Log.Debug().Msg("ListRadios request received")

	deviceSections, err := s.ConfigReader.GetSections(wirelessConfig, "wifi-device")
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("list wifi-device sections: %w", err))
	}

	ifaceSections, err := s.ConfigReader.GetSections(wirelessConfig, "wifi-iface")
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("list wifi-iface sections: %w", err))
	}

	// Get iwinfo data to correlate hardware names.
	var iwinfoData map[string]*iwinfo.InterfaceInfo
	if s.IwinfoClient != nil {
		iwinfoData, _ = s.IwinfoClient.GetInfoForAll(ctx)
	}

	// Get wireless status to map UCI radio→Linux ifname.
	var wsStatus map[string]*network.WirelessRadioStatus
	if s.WirelessStatus != nil {
		wsStatus, _ = s.WirelessStatus.GetWirelessStatus(ctx)
	}

	radios := make([]*wificonfigv1.Radio, 0, len(deviceSections))

	for _, devName := range deviceSections {
		dev, err := network.GetWirelessDeviceByNameWithReader(devName, s.ConfigReader)
		if err != nil {
			continue
		}

		// Find the first wifi-iface linked to this radio.
		ifaceName := findLinkedIface(devName, ifaceSections, s.ConfigReader)

		// Determine hardware name from iwinfo by matching ifname.
		hardwareName := network.ResolveWirelessRadioHardwareName(devName, wsStatus, iwinfoData)

		displayName := FormatBandDisplayName(WifiBandToProto(dev.Band))
		if hardwareName != "" {
			displayName += " (" + hardwareName + ")"
		}

		radios = append(radios, &wificonfigv1.Radio{
			Name:          devName,
			DisplayName:   displayName,
			HardwareName:  hardwareName,
			Band:          WifiBandToProto(dev.Band),
			InterfaceName: ifaceName,
		})
	}

	return &wificonfigv1.ListRadiosResponse{Radios: radios}, nil
}

// GetRadioStatus returns the current runtime status for a specific radio.
func (s *WifiConfigService) GetRadioStatus(ctx context.Context, req *wificonfigv1.GetRadioStatusRequest) (*wificonfigv1.GetRadioStatusResponse, error) {
	s.Log.Debug().Str("radio", req.GetRadioName()).Msg("GetRadioStatus request received")

	wsStatus, err := s.WirelessStatus.GetWirelessStatus(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("wireless status: %w", err))
	}

	radio, ok := wsStatus[req.GetRadioName()]
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("radio %q not found", req.GetRadioName()))
	}

	status := &wificonfigv1.RadioStatus{
		Active: radio.Up && !radio.Disabled,
	}

	if len(radio.Interfaces) == 0 {
		return &wificonfigv1.GetRadioStatusResponse{Status: status}, nil
	}

	ifname := radio.Interfaces[0].Ifname
	mode := radio.Interfaces[0].Config.Mode

	status.WifiMode = WifiModeToProto(mode)

	// Get iwinfo data for the interface.
	if s.IwinfoClient != nil {
		info, infoErr := s.IwinfoClient.GetInfo(ctx, ifname)
		if infoErr == nil && info != nil {
			status.Ssid = info.GetSSID()
			status.Mode = FormatModeDisplay(info.GetMode())
			status.Channel = int32(info.GetChannel())
			status.Frequency = int32(info.GetFrequency())
			status.Bandwidth = FormatBandwidthDisplay(info.GetHTMode())
			status.Encryption = FormatEncryptionDisplay(info.Encryption)
			status.TxPower = int32(info.GetTxPower())
		}
	}

	// Count connected stations.
	stationCount := s.countStations(ifname)
	if status.WifiMode == wificonfigv1.WifiMode_WIFI_MODE_AP {
		status.ConnectedClients = int32(stationCount)
	} else {
		status.MeshPeers = int32(stationCount)
	}

	return &wificonfigv1.GetRadioStatusResponse{Status: status}, nil
}

// GetRadioSettings returns the editable configuration for a specific radio.
func (s *WifiConfigService) GetRadioSettings(_ context.Context, req *wificonfigv1.GetRadioSettingsRequest) (*wificonfigv1.GetRadioSettingsResponse, error) {
	s.Log.Debug().Str("radio", req.GetRadioName()).Msg("GetRadioSettings request received")

	settings, band, err := s.readRadioSettings(req.GetRadioName())
	if err != nil {
		return nil, err
	}

	// Populate available options for dropdowns.
	return &wificonfigv1.GetRadioSettingsResponse{
		Settings:             settings,
		AvailableChannels:    availableChannelsForBand(band),
		AvailableBandwidths:  availableBandwidthsForBand(band),
		AvailableEncryptions: availableEncryptions(),
	}, nil
}

// CurrentRadioSettings returns the radio's stored settings (password
// omitted, like GetRadioSettings) for callers that overlay a change on
// top of them.
func (s *WifiConfigService) CurrentRadioSettings(radioName string) (*wificonfigv1.RadioSettings, error) {
	settings, _, err := s.readRadioSettings(radioName)

	return settings, err
}

// readRadioSettings reads the device and linked iface for radioName.
// Errors are connect errors: CodeNotFound when no iface links to the
// radio, CodeInternal for UCI read failures.
func (s *WifiConfigService) readRadioSettings(radioName string) (*wificonfigv1.RadioSettings, wificonfigv1.WifiBand, error) {
	dev, err := network.GetWirelessDeviceByNameWithReader(radioName, s.ConfigReader)
	if err != nil {
		return nil, wificonfigv1.WifiBand_WIFI_BAND_UNSPECIFIED,
			connect.NewError(connect.CodeInternal, fmt.Errorf("read device config: %w", err))
	}

	ifaceSections, err := s.ConfigReader.GetSections(wirelessConfig, "wifi-iface")
	if err != nil {
		return nil, wificonfigv1.WifiBand_WIFI_BAND_UNSPECIFIED,
			connect.NewError(connect.CodeInternal, fmt.Errorf("list wifi-iface sections: %w", err))
	}

	ifaceName := findLinkedIface(radioName, ifaceSections, s.ConfigReader)
	if ifaceName == "" {
		return nil, wificonfigv1.WifiBand_WIFI_BAND_UNSPECIFIED,
			connect.NewError(connect.CodeNotFound, fmt.Errorf("no interface linked to radio %q", radioName))
	}

	iface, err := network.GetWirelessIfaceByNameWithReader(ifaceName, s.ConfigReader)
	if err != nil {
		return nil, wificonfigv1.WifiBand_WIFI_BAND_UNSPECIFIED,
			connect.NewError(connect.CodeInternal, fmt.Errorf("read iface config: %w", err))
	}

	// ParseInt with bitSize 32 rejects values that int32 cannot hold;
	// a parse failure leaves tx_power at 0, which the API treats as
	// "unset" (the frontend seeds a display value from live status).
	var txPower int32
	if v, err := strconv.ParseInt(dev.TxPower, 10, 32); err == nil {
		txPower = int32(v)
	}

	settings := &wificonfigv1.RadioSettings{
		Ssid:       iface.SSID,
		Channel:    dev.Channel,
		Bandwidth:  WifiHTModeToProto(dev.HTMode),
		TxPower:    txPower,
		Encryption: WifiEncryptionToProto(iface.Encryption),
		Mode:       WifiModeToProto(iface.Mode),
	}

	if iface.MeshID != "" {
		settings.MeshId = strPtr(iface.MeshID)
	}

	if dev.Country != "" {
		settings.Country = strPtr(dev.Country)
	}

	if iface.Disabled == "1" {
		settings.Disabled = boolPtr(true)
	}

	// The admission floor only means something on a mesh iface. Report
	// whatever UCI holds when it parses as an int32; protovalidate
	// bounds updates, not reads.
	if iface.Mode == uciModeMesh {
		if v, err := strconv.ParseInt(iface.MeshRSSIThreshold, 10, 32); err == nil {
			settings.MeshRssiThreshold = int32Ptr(int32(v))
		}
	}

	return settings, WifiBandToProto(dev.Band), nil
}

// rejectNonMeshModeOnMorse enforces that a type=morse (HaLow)
// wifi-device only ever carries mesh-mode ifaces: it returns a
// CodeInvalidArgument error when the request would turn one into an
// AP, STA, ad-hoc, or monitor iface. An unspecified mode
// (channel/txpower-only edits) or mesh passes.
func (s *WifiConfigService) rejectNonMeshModeOnMorse(radioName string, mode wificonfigv1.WifiMode) error {
	if mode == wificonfigv1.WifiMode_WIFI_MODE_UNSPECIFIED || mode == wificonfigv1.WifiMode_WIFI_MODE_MESH {
		return nil
	}

	if !network.IsMorseDevice(s.ConfigReader, radioName) {
		return nil
	}

	s.Log.Warn().Str("radio", radioName).Stringer("mode", mode).
		Msg("Rejecting non-mesh mode on HaLow radio")

	return connect.NewError(connect.CodeInvalidArgument,
		fmt.Errorf("radio %q is a HaLow (type=morse) wifi-device, which only carries mesh interfaces", radioName))
}

// ifaceDisabledValue maps the optional `disabled` flag to its UCI
// value. Empty means the flag was not sent and the option is left
// unchanged (SetWirelessIfaceConfigWithReader skips empty fields).
func ifaceDisabledValue(settings *wificonfigv1.RadioSettings) string {
	if settings.Disabled == nil {
		return ""
	}

	if settings.GetDisabled() {
		return "1"
	}

	return "0"
}

// UpdateRadioSettings applies new configuration to a radio.
func (s *WifiConfigService) UpdateRadioSettings(ctx context.Context, req *wificonfigv1.UpdateRadioSettingsRequest) (*wificonfigv1.UpdateRadioSettingsResponse, error) {
	s.Log.Debug().Str("radio", req.GetRadioName()).Msg("UpdateRadioSettings request received")

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.stageRadioSettings(ctx, req.GetRadioName(), req.GetSettings()); err != nil {
		var sw *stageWriteError
		if errors.As(err, &sw) {
			return &wificonfigv1.UpdateRadioSettingsResponse{Success: false, Message: strPtr(sw.msg)}, nil
		}

		return nil, err
	}

	if err := s.applyCommitted(ctx); err != nil {
		return &wificonfigv1.UpdateRadioSettingsResponse{
			Success: false,
			Message: strPtr(fmt.Sprintf("config committed but reload failed: %v", err)),
		}, nil
	}

	return &wificonfigv1.UpdateRadioSettingsResponse{Success: true}, nil
}

// ApplyRadioSettingsBatch stages every update under one lock and
// reloads once. Any staging error aborts before the reload; UCI writes
// already staged for earlier radios are committed by the per-radio
// setters and are not rolled back — callers re-read state on error.
func (s *WifiConfigService) ApplyRadioSettingsBatch(ctx context.Context, updates []RadioSettingsUpdate) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, u := range updates {
		if err := s.stageRadioSettings(ctx, u.RadioName, u.Settings); err != nil {
			return fmt.Errorf("stage %s: %w", u.RadioName, err)
		}
	}

	if err := s.applyCommitted(ctx); err != nil {
		return fmt.Errorf("reload wireless: %w", err)
	}

	return nil
}

// applyCommitted makes the committed UCI take effect: it re-reads the
// daemon's in-memory tree from disk, then hands the change to the
// system. The second step is what actually restarts the radios — the
// go-uci reload alone only refreshes this process's view, and the
// device would sit on its old wireless config until reboot.
//
// The system reload runs on a context detached from the request: it
// can drop the operator's own connection (they may be on the radio
// being reconfigured), and a canceled request context would kill
// reload_config midway.
func (s *WifiConfigService) applyCommitted(ctx context.Context) error {
	if err := s.ConfigReader.ReloadConfig(); err != nil {
		return fmt.Errorf("re-read uci: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), reloadServicesTimeout)
	defer cancel()

	s.Log.Info().Msg("Reloading network services to apply wireless config")

	if s.ReloadServices != nil {
		return s.ReloadServices(ctx)
	}

	if err := network.ForceReloadConfig(ctx); err != nil {
		return fmt.Errorf("reload_config: %w", err)
	}

	return nil
}

// stageRadioSettings writes the device and iface options for one radio.
// The caller holds s.mu. Validation and lookup failures are connect
// errors; UCI write failures are *stageWriteError.
func (s *WifiConfigService) stageRadioSettings(ctx context.Context, radioName string, settings *wificonfigv1.RadioSettings) error { //nolint:gocognit,gocyclo // one linear staging sequence; splitting it hides the write order
	if settings == nil {
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("settings are required"))
	}

	if err := s.rejectNonMeshModeOnMorse(radioName, settings.GetMode()); err != nil {
		return err
	}

	mode := ProtoToWifiMode(settings.GetMode())

	// A mesh iface only carries traffic once its batadv_hardif hangs
	// off bat0. Refuse before writing anything on a node that never
	// ran setup rather than commit a binding that can never come up.
	if mode == uciModeMesh && !network.BatmanDeviceExists(s.ConfigReader, network.BatmanDeviceName) {
		s.Log.Warn().Str("radio", radioName).Msg("Rejecting mesh mode: no batman-adv device")

		return connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("batman-adv device %q is not configured; run setup first", network.BatmanDeviceName))
	}

	// Find the linked interface section.
	ifaceSections, err := s.ConfigReader.GetSections(wirelessConfig, "wifi-iface")
	if err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("list wifi-iface sections: %w", err))
	}

	ifaceName := findLinkedIface(radioName, ifaceSections, s.ConfigReader)
	if ifaceName == "" {
		return connect.NewError(connect.CodeNotFound, fmt.Errorf("no interface linked to radio %q", radioName))
	}

	// Build device config update. A tx_power of 0 means "unset": leave
	// the UCI option alone rather than forcing 0 dBm.
	devCfg := &network.UCIWirelessDevice{
		Channel: settings.GetChannel(),
	}

	if settings.GetTxPower() > 0 {
		devCfg.TxPower = strconv.Itoa(int(settings.GetTxPower()))
	}

	if htmode := ProtoToWifiHTMode(settings.GetBandwidth()); htmode != "" {
		devCfg.HTMode = htmode
	}

	if settings.Country != nil {
		devCfg.Country = settings.GetCountry()
	}

	if err := network.SetWirelessDeviceConfigWithReader(radioName, devCfg, s.ConfigReader); err != nil {
		return &stageWriteError{msg: fmt.Sprintf("failed to update device config: %v", err)}
	}

	// Build interface config update.
	ifaceCfg := &network.UCIWirelessIface{}

	if settings.Password != nil && settings.GetPassword() != "" {
		ifaceCfg.Key = settings.GetPassword()
	}

	if enc := ProtoToWifiEncryption(settings.GetEncryption()); enc != "" {
		ifaceCfg.Encryption = enc
	}

	if mode != "" {
		ifaceCfg.Mode = mode

		switch mode {
		case uciModeMesh:
			// batman-adv handles mesh forwarding; disable 802.11s
			// in-kernel forwarding so frames are not double-forwarded.
			ifaceCfg.MeshFwding = "0"

			// Mesh radios must be bound to a batadv_hardif network
			// (batmesh*); ahwlan is the AP bridge and does not attach
			// to bat0, so leaving the iface there breaks the mesh.
			// If the iface is already on a batmesh* slot (e.g. the
			// wizard's primary mesh radio), leave it.
			currentNet := ""
			if vals, ok := s.ConfigReader.Get(wirelessConfig, ifaceName, "network"); ok && len(vals) > 0 {
				currentNet = vals[0]
			}

			batmesh := currentNet

			if !strings.HasPrefix(currentNet, "batmesh") {
				unused, err := findUnusedBatmesh(s.ConfigReader, ifaceName)
				if err != nil {
					s.Log.Warn().Err(err).Str("radio", radioName).Msg("no batmesh network available")

					return connect.NewError(connect.CodeFailedPrecondition, err)
				}

				ifaceCfg.Network = unused
				batmesh = unused
			}

			// The wizard creates both hardifs; a factory image or a
			// hand-edited network config may lack this one. The
			// wireless reader shares the UCI tree, so the section is
			// committed together with the iface below.
			created, err := network.EnsureBatmanHardifInterface(s.ConfigReader, batmesh, network.BatmanDeviceName)
			if err != nil {
				return &stageWriteError{msg: fmt.Sprintf("failed to create network.%s: %v", batmesh, err)}
			}

			if created {
				s.Log.Info().Str("network", batmesh).Msg("Created batadv_hardif for mesh radio")
			}

			s.stageSecondaryMeshPolicy(ctx, radioName, batmesh, ifaceCfg)

		case uciModeAP, uciModeSTA:
			// Coming back from mesh (or any other state) — rebind to
			// the AP bridge so the iface joins br-ahwlan.
			ifaceCfg.Network = wifiNetworkAhwlan
		}
	}

	// Drop the mesh-only options a previous mesh life left behind so the
	// section reads like a fresh AP/STA iface. Pulled out of the switch
	// above (rather than nested in its AP/STA case) to keep that if/switch
	// tree under the nestif complexity gate.
	if mode == uciModeAP || mode == uciModeSTA {
		if clearErr := s.clearMeshOnlyOptions(ifaceName); clearErr != nil {
			return clearErr
		}
	}

	effMode := s.effectiveIfaceMode(ifaceName, mode)

	// The admission floor only means something on a mesh iface, and the
	// HaLow (type=morse) link owns range: its floor is not operator-
	// tunable, so the field is ignored there. Unset leaves the UCI
	// option alone (SetWirelessIfaceConfigWithReader skips empty fields).
	if settings.MeshRssiThreshold != nil && effMode == uciModeMesh && !network.IsMorseDevice(s.ConfigReader, radioName) {
		ifaceCfg.MeshRSSIThreshold = strconv.Itoa(int(settings.GetMeshRssiThreshold()))
	}

	if err := s.stageIfaceIdentity(ifaceName, effMode, settings, ifaceCfg); err != nil {
		return err
	}

	ifaceCfg.Disabled = ifaceDisabledValue(settings)

	if err := network.SetWirelessIfaceConfigWithReader(ifaceName, ifaceCfg, s.ConfigReader); err != nil {
		return &stageWriteError{msg: fmt.Sprintf("failed to update iface config: %v", err)}
	}

	return nil
}

// radioSupportsSecondaryMesh reports whether radioName is backed by a
// chipset the daemon tunes as the 2.4 GHz secondary link (MT7915 /
// MT7916, per network.SupportsSecondaryMeshLink). A lookup failure logs
// at Warn and counts as unsupported: a mode change must never fail
// because iwinfo or ubus was unavailable.
func (s *WifiConfigService) radioSupportsSecondaryMesh(ctx context.Context, radioName string) bool {
	if s.IwinfoClient == nil || s.WirelessStatus == nil {
		return false
	}

	allInfo, err := s.IwinfoClient.GetInfoForAll(ctx)
	if err != nil {
		s.Log.Warn().Err(err).Str("radio", radioName).Msg("iwinfo unavailable; skipping secondary mesh tuning")

		return false
	}

	status, err := s.WirelessStatus.GetWirelessStatus(ctx)
	if err != nil {
		s.Log.Warn().Err(err).Str("radio", radioName).Msg("wireless status unavailable; skipping secondary mesh tuning")

		return false
	}

	return network.SupportsSecondaryMeshLink(network.ResolveWirelessRadioHardwareName(radioName, status, allInfo))
}

// stageSecondaryMeshPolicy copies the daemon's batmesh1 tuning
// (mcast_rate, mesh_nolearn, plink timers) onto cfg when the mesh iface
// binds to the secondary hardif on an MT7915/MT7916 radio. The HaLow
// primary and any other hardif are left alone.
func (s *WifiConfigService) stageSecondaryMeshPolicy(ctx context.Context, radioName, batmesh string, cfg *network.UCIWirelessIface) {
	if batmesh != network.BatmanSecondaryIface || !s.radioSupportsSecondaryMesh(ctx, radioName) {
		return
	}

	cfg.ApplySecondaryMeshPolicy()
}

// meshOnlyOptions lists the wifi-iface options that only mean something
// on a mode=mesh section: the 802.11s forwarding switch, the admission
// floor, and the batmesh1 tuning policy. mesh_id is handled by
// stageIfaceIdentity.
func meshOnlyOptions() []string {
	policy := network.SecondaryMeshPolicyOptions()
	opts := make([]string, 0, len(policy)+2)
	opts = append(opts, wifiOptionMeshFwding, wifiOptionMeshRSSI)

	for _, p := range policy {
		opts = append(opts, p.Option)
	}

	return opts
}

// clearMeshOnlyOptions deletes meshOnlyOptions from ifaceName. Del on a
// missing option is a no-op, so an iface that was never mesh is left
// exactly as it was.
func (s *WifiConfigService) clearMeshOnlyOptions(ifaceName string) error {
	for _, opt := range meshOnlyOptions() {
		if err := s.ConfigReader.Del(wirelessConfig, ifaceName, opt); err != nil {
			return &stageWriteError{msg: fmt.Sprintf("failed to clear %s on %s: %v", opt, ifaceName, err)}
		}
	}

	return nil
}

// effectiveIfaceMode returns the requested mode, else the mode already
// on the wifi-iface section, else "".
func (s *WifiConfigService) effectiveIfaceMode(ifaceName, mode string) string {
	if mode != "" {
		return mode
	}

	if vals, ok := s.ConfigReader.Get(wirelessConfig, ifaceName, wifiOptionMode); ok && len(vals) > 0 {
		return vals[0]
	}

	return ""
}

// stageIfaceIdentity stages the network-name option that matches the
// iface's effective mode and clears the other one. A mode=mesh
// wifi-iface carries mesh_id only: the frontend mirrors mesh_id into
// ssid to satisfy the proto's ssid min_len, and an AP section being
// converted already has an ssid — either one left in the section keeps
// the radio from coming up. AP and STA ifaces carry ssid only. mode is
// the effective mode from effectiveIfaceMode (requested, else the one
// already on the section), so a channel-only edit of a mesh iface
// (which still has to send an ssid) never re-introduces it. Other modes
// keep the legacy write-what-was-sent behavior. Del on a missing option
// is a no-op.
func (s *WifiConfigService) stageIfaceIdentity(ifaceName, mode string, settings *wificonfigv1.RadioSettings, ifaceCfg *network.UCIWirelessIface) error {
	switch mode {
	case uciModeMesh:
		// Clients that only know ssid still get a usable mesh: the
		// network name lands in mesh_id.
		ifaceCfg.MeshID = settings.GetSsid()
		if settings.GetMeshId() != "" {
			ifaceCfg.MeshID = settings.GetMeshId()
		}

		if err := s.ConfigReader.Del(wirelessConfig, ifaceName, wifiOptionSSID); err != nil {
			return &stageWriteError{msg: fmt.Sprintf("failed to clear ssid on mesh iface: %v", err)}
		}
	case uciModeAP, uciModeSTA:
		ifaceCfg.SSID = settings.GetSsid()

		if err := s.ConfigReader.Del(wirelessConfig, ifaceName, wifiOptionMeshID); err != nil {
			return &stageWriteError{msg: fmt.Sprintf("failed to clear mesh_id on %s iface: %v", mode, err)}
		}
	default:
		ifaceCfg.SSID = settings.GetSsid()

		if settings.MeshId != nil {
			ifaceCfg.MeshID = settings.GetMeshId()
		}
	}

	return nil
}

// ListConnectedClients returns all clients connected to an AP-mode radio.
func (s *WifiConfigService) ListConnectedClients(ctx context.Context, req *wificonfigv1.ListConnectedClientsRequest) (*wificonfigv1.ListConnectedClientsResponse, error) {
	s.Log.Debug().Str("radio", req.GetRadioName()).Msg("ListConnectedClients request received")

	ifname, err := s.resolveIfname(ctx, req.GetRadioName())
	if err != nil {
		return nil, err
	}

	iface := s.findWifiInterface(ifname)
	if iface == nil {
		return &wificonfigv1.ListConnectedClientsResponse{}, nil
	}

	stations, err := s.Wifi.StationInfo(iface)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("station info: %w", err))
	}

	// Build MAC→hostname map from DHCP leases.
	macToHostname := s.buildMACHostnameMap(ctx)

	clients := make([]*wificonfigv1.ConnectedClient, 0, len(stations))

	for _, st := range stations {
		// net.HardwareAddr.String() is documented to return lower-case
		// hex; macToHostname is keyed by lower-case MACs from ingestion
		// (see buildMACHostnameMap).
		mac := st.HardwareAddr.String()

		clients = append(clients, &wificonfigv1.ConnectedClient{
			Hostname:   macToHostname[mac],
			MacAddress: mac,
			SignalDbm:  int32(st.Signal),
			RxRateBps:  int64(st.ReceiveBitrate),
			TxRateBps:  int64(st.TransmitBitrate),
			Connected:  durationpb.New(st.Connected),
		})
	}

	return &wificonfigv1.ListConnectedClientsResponse{Clients: clients}, nil
}

// ListMeshPeers returns all mesh peers visible to a mesh-mode radio.
func (s *WifiConfigService) ListMeshPeers(ctx context.Context, req *wificonfigv1.ListMeshPeersRequest) (*wificonfigv1.ListMeshPeersResponse, error) {
	s.Log.Debug().Str("radio", req.GetRadioName()).Msg("ListMeshPeers request received")

	ifname, err := s.resolveIfname(ctx, req.GetRadioName())
	if err != nil {
		return nil, err
	}

	// Get bat-hosts for MAC→hostname mapping.
	batHosts, batErr := s.parseBatHosts(batmanadv.BatHostsFilePath)
	if batErr != nil {
		s.Log.Warn().Err(batErr).Msg("Failed to parse bat-hosts; hostnames will be empty")

		batHosts = &batmanadv.BatHosts{}
	}

	// Get batman-adv neighbors for throughput/last_seen.
	batNeighbors, batNErr := s.getMeshNeighbors()
	if batNErr != nil {
		s.Log.Warn().Err(batNErr).Msg("Failed to get batman-adv neighbors; throughput/last_seen unavailable")
	}

	// Build peer list, preferring batman-adv neighbor list as the primary source.
	peers := s.buildMeshPeerList(ifname, batHosts, batNeighbors)

	return &wificonfigv1.ListMeshPeersResponse{Peers: peers}, nil
}

// ── internal helpers ─────────────────────────────────────────────────────────

const wirelessConfig = "wireless"

// UCI mode option values, used both as switch cases and as return values
// when converting between proto and UCI representations.
const (
	uciModeAP      = "ap"
	uciModeMesh    = "mesh"
	uciModeSTA     = "sta"
	uciModeAdHoc   = "adhoc"
	uciModeMonitor = "monitor"
)

// findLinkedIface returns the first wifi-iface section whose "device" option
// matches the given radio device name.
func findLinkedIface(deviceName string, ifaceSections []string, reader network.ConfigReader) string {
	for _, sec := range ifaceSections {
		vals, ok := reader.Get(wirelessConfig, sec, "device")
		if ok && len(vals) > 0 && vals[0] == deviceName {
			return sec
		}
	}

	return ""
}

// findUnusedBatmesh returns the first canonical batmesh* network (in order:
// batmesh0, batmesh1) whose name is not currently set as the "network" option
// on any wifi-iface section other than excludeIface. Returns an error if every
// candidate is already bound.
func findUnusedBatmesh(reader network.ConfigReader, excludeIface string) (string, error) {
	candidates := [...]string{network.BatmanPrimaryIface, network.BatmanSecondaryIface}

	sections, err := reader.GetSections(wirelessConfig, "wifi-iface")
	if err != nil {
		return "", fmt.Errorf("list wifi-iface sections: %w", err)
	}

	inUse := make(map[string]struct{}, len(sections))

	for _, sec := range sections {
		if sec == excludeIface {
			continue
		}

		vals, ok := reader.Get(wirelessConfig, sec, "network")
		if !ok || len(vals) == 0 {
			continue
		}

		inUse[vals[0]] = struct{}{}
	}

	for _, name := range candidates {
		if _, taken := inUse[name]; !taken {
			return name, nil
		}
	}

	return "", fmt.Errorf("no available batmesh network (all in use)")
}

// resolveIfname maps a UCI radio name to the running Linux interface name.
func (s *WifiConfigService) resolveIfname(ctx context.Context, radioName string) (string, error) {
	wsStatus, err := s.WirelessStatus.GetWirelessStatus(ctx)
	if err != nil {
		return "", connect.NewError(connect.CodeInternal, fmt.Errorf("wireless status: %w", err))
	}

	radio, ok := wsStatus[radioName]
	if !ok || len(radio.Interfaces) == 0 {
		return "", connect.NewError(connect.CodeNotFound, fmt.Errorf("radio %q not found or has no interfaces", radioName))
	}

	return radio.Interfaces[0].Ifname, nil
}

// findWifiInterface finds a wifi.Interface by name from the Wifi provider.
func (s *WifiConfigService) findWifiInterface(ifname string) *wifi.Interface {
	ifaces, err := s.Wifi.Interfaces()
	if err != nil {
		return nil
	}

	for _, iface := range ifaces {
		if iface.Name == ifname {
			return iface
		}
	}

	return nil
}

// countStations returns the number of stations connected to the named interface.
func (s *WifiConfigService) countStations(ifname string) int {
	iface := s.findWifiInterface(ifname)
	if iface == nil {
		return 0
	}

	stations, err := s.Wifi.StationInfo(iface)
	if err != nil {
		return 0
	}

	return len(stations)
}

// buildMACHostnameMap creates a lowercase-MAC → hostname map from DHCP leases.
func (s *WifiConfigService) buildMACHostnameMap(ctx context.Context) map[string]string {
	m := map[string]string{}

	if s.DHCPLeases == nil {
		return m
	}

	resp, err := s.DHCPLeases.GetCurrentDHCPLeases(ctx)
	if err != nil {
		return m
	}

	// ubus emits DHCP lease MACs in the case dnsmasq stored them (often
	// upper-case). Normalize to lower-case keys so lookups by
	// net.HardwareAddr.String() — which is always lower-case — hit.
	for _, l := range resp.GetDHCPLeases() {
		m[strings.ToLower(l.MacAddr)] = l.Hostname
	}

	return m
}

// buildMeshPeerList assembles the protobuf MeshPeer list.
func (s *WifiConfigService) buildMeshPeerList(
	ifname string,
	batHosts *batmanadv.BatHosts,
	batNeighbors *batmanadv.Neighbors,
) []*wificonfigv1.MeshPeer {
	if batNeighbors == nil || batNeighbors.IsEmpty() {
		return nil
	}

	// Filter neighbors to the interface we care about.
	filtered := batNeighbors.FilterByInterface(ifname)

	// Also get station info for signal enrichment.
	signalByMAC := map[string]int{}
	iface := s.findWifiInterface(ifname)

	if iface != nil {
		stInfos, err := s.Wifi.StationInfo(iface)
		if err == nil {
			// net.HardwareAddr.String() returns lower-case hex, so the
			// map key is naturally normalized.
			for _, st := range stInfos {
				signalByMAC[st.HardwareAddr.String()] = st.Signal
			}
		}
	}

	peers := make([]*wificonfigv1.MeshPeer, 0, len(filtered))

	// batNeighbors were normalized to lower-case at ingestion by
	// batmanadv.GetMeshNeighbors, so n.NeighAddress can be used as a
	// direct map key.
	for _, n := range filtered {
		mac := n.NeighAddress
		hostname := batHosts.GetHostByMAC(mac)

		peer := &wificonfigv1.MeshPeer{
			Hostname:   hostname,
			MacAddress: mac,
			SignalDbm:  int32(signalByMAC[mac]),
			// `batctl nj` emits BATADV_ATTR_THROUGHPUT, which the kernel
			// scales to kbit/s before putting it on netlink.
			ThroughputMbps: float64(n.Throughput) / 1000.0,
			LastSeen:       durationpb.New(time.Duration(n.LastSeenMsecs) * time.Millisecond),
		}

		peers = append(peers, peer)
	}

	return peers
}

// ── formatting helpers ───────────────────────────────────────────────────────

// FormatEncryptionDisplay converts iwinfo EncryptionInfo to a display string.
func FormatEncryptionDisplay(enc iwinfo.EncryptionInfo) string {
	if !enc.IsEnabled() {
		return "Open"
	}

	wpa := enc.GetWPA()
	auth := enc.GetAuthentication()
	ciphers := enc.GetCiphers()

	if len(wpa) == 0 || len(auth) == 0 {
		return "Encrypted"
	}

	// Use the highest WPA version.
	maxWPA := wpa[0]
	for _, v := range wpa {
		if v > maxWPA {
			maxWPA = v
		}
	}

	var prefix string

	switch maxWPA {
	case 3:
		prefix = "WPA3"
	case 2:
		prefix = "WPA2"
	case 1:
		prefix = "WPA"
	default:
		prefix = fmt.Sprintf("WPA%d", maxWPA)
	}

	authStr := strings.ToUpper(auth[0])
	if authStr == "SAE" {
		return prefix + "-SAE"
	}

	result := prefix + "-" + authStr
	if len(ciphers) > 0 {
		result += " (" + strings.ToUpper(ciphers[0]) + ")"
	}

	return result
}

// FormatBandwidthDisplay converts an HTMode string to human-readable bandwidth.
func FormatBandwidthDisplay(htmode string) string {
	htmode = strings.ToUpper(htmode)

	bandwidthMap := map[string]string{
		htModeNoHT:   "No HT",
		htModeHT20:   bandwidth20MHz,
		htModeHT40:   bandwidth40MHz,
		htModeVHT20:  bandwidth20MHz,
		htModeVHT40:  bandwidth40MHz,
		htModeVHT80:  bandwidth80MHz,
		htModeVHT160: bandwidth160MHz,
		htModeHE20:   bandwidth20MHz,
		htModeHE40:   bandwidth40MHz,
		htModeHE80:   bandwidth80MHz,
		htModeHE160:  bandwidth160MHz,
	}

	if bw, ok := bandwidthMap[htmode]; ok {
		return bw
	}

	// S1G bandwidths.
	if strings.HasSuffix(htmode, "MHZ") || strings.HasSuffix(htmode, "MHz") {
		return htmode
	}

	if htmode == "" {
		return ""
	}

	return htmode
}

// FormatModeDisplay converts iwinfo mode string to user-friendly display.
func FormatModeDisplay(mode string) string {
	switch strings.ToLower(mode) {
	case "master":
		return "Access Point"
	case "mesh point":
		return "Mesh Point (802.11s)"
	case "client":
		return "Client"
	case "ad-hoc":
		return "Ad-Hoc"
	case "monitor":
		return "Monitor"
	default:
		return mode
	}
}

// FormatBandDisplayName converts a WifiBand enum to a display name.
func FormatBandDisplayName(band wificonfigv1.WifiBand) string {
	switch band {
	case wificonfigv1.WifiBand_WIFI_BAND_2G:
		return "2.4 GHz Radio"
	case wificonfigv1.WifiBand_WIFI_BAND_5G:
		return "5 GHz Radio"
	case wificonfigv1.WifiBand_WIFI_BAND_6G:
		return "6 GHz Radio"
	case wificonfigv1.WifiBand_WIFI_BAND_S1G:
		return "HaLow Radio"
	case wificonfigv1.WifiBand_WIFI_BAND_60G:
		return "60 GHz Radio"
	default:
		return band.String() + " Radio"
	}
}

// ── option lists ─────────────────────────────────────────────────────────────

func availableChannelsForBand(band wificonfigv1.WifiBand) []string {
	switch band {
	case wificonfigv1.WifiBand_WIFI_BAND_2G:
		return []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11"}
	case wificonfigv1.WifiBand_WIFI_BAND_5G:
		return []string{"36", "40", "44", "48", "52", "56", "60", "64",
			"100", "104", "108", "112", "116", "120", "124", "128",
			"132", "136", "140", "144", "149", "153", "157", "161", "165"}
	case wificonfigv1.WifiBand_WIFI_BAND_6G:
		return []string{"1", "5", "9", "13", "17", "21", "25", "29",
			"33", "37", "41", "45", "49", "53", "57", "61",
			"65", "69", "73", "77", "81", "85", "89", "93"}
	case wificonfigv1.WifiBand_WIFI_BAND_S1G:
		return []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10",
			"11", "12", "13", "14", "15", "16", "17", "18", "19", "20",
			"21", "22", "23", "24", "25", "26", "27", "28", "29", "30",
			"31", "32", "33", "34", "35", "36", "37", "38", "39", "40",
			"41", "42", "43", "44", "45", "46", "47", "48", "49", "50",
			"51"}
	default:
		return nil
	}
}

func availableBandwidthsForBand(band wificonfigv1.WifiBand) []wificonfigv1.WifiHTMode {
	switch band {
	case wificonfigv1.WifiBand_WIFI_BAND_2G:
		return []wificonfigv1.WifiHTMode{
			wificonfigv1.WifiHTMode_WIFI_HT_MODE_NOHT,
			wificonfigv1.WifiHTMode_WIFI_HT_MODE_HT20,
			wificonfigv1.WifiHTMode_WIFI_HT_MODE_HT40,
		}
	case wificonfigv1.WifiBand_WIFI_BAND_5G:
		return []wificonfigv1.WifiHTMode{
			wificonfigv1.WifiHTMode_WIFI_HT_MODE_VHT20,
			wificonfigv1.WifiHTMode_WIFI_HT_MODE_VHT40,
			wificonfigv1.WifiHTMode_WIFI_HT_MODE_VHT80,
			wificonfigv1.WifiHTMode_WIFI_HT_MODE_VHT160,
		}
	case wificonfigv1.WifiBand_WIFI_BAND_6G:
		return []wificonfigv1.WifiHTMode{
			wificonfigv1.WifiHTMode_WIFI_HT_MODE_HE20,
			wificonfigv1.WifiHTMode_WIFI_HT_MODE_HE40,
			wificonfigv1.WifiHTMode_WIFI_HT_MODE_HE80,
			wificonfigv1.WifiHTMode_WIFI_HT_MODE_HE160,
		}
	case wificonfigv1.WifiBand_WIFI_BAND_S1G:
		return []wificonfigv1.WifiHTMode{
			wificonfigv1.WifiHTMode_WIFI_HT_MODE_S1G_1MHZ,
			wificonfigv1.WifiHTMode_WIFI_HT_MODE_S1G_2MHZ,
			wificonfigv1.WifiHTMode_WIFI_HT_MODE_S1G_4MHZ,
			wificonfigv1.WifiHTMode_WIFI_HT_MODE_S1G_8MHZ,
		}
	default:
		return nil
	}
}

func availableEncryptions() []wificonfigv1.WifiEncryption {
	return []wificonfigv1.WifiEncryption{
		wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_SAE,
		wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_PSK2,
		wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_PSK,
		wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_NONE,
	}
}

// ── enum conversion helpers ──────────────────────────────────────────────────

// WifiBandToProto converts a UCI band string to the WifiBand enum.
func WifiBandToProto(s string) wificonfigv1.WifiBand {
	switch strings.ToLower(s) {
	case "2g":
		return wificonfigv1.WifiBand_WIFI_BAND_2G
	case "5g":
		return wificonfigv1.WifiBand_WIFI_BAND_5G
	case "6g":
		return wificonfigv1.WifiBand_WIFI_BAND_6G
	case "s1g":
		return wificonfigv1.WifiBand_WIFI_BAND_S1G
	case "60g":
		return wificonfigv1.WifiBand_WIFI_BAND_60G
	default:
		return wificonfigv1.WifiBand_WIFI_BAND_UNSPECIFIED
	}
}

// WifiModeToProto converts an iwinfo/UCI mode string to the WifiMode enum.
func WifiModeToProto(s string) wificonfigv1.WifiMode {
	switch strings.ToLower(s) {
	case uciModeAP, "master":
		return wificonfigv1.WifiMode_WIFI_MODE_AP
	case uciModeMesh, "mesh point":
		return wificonfigv1.WifiMode_WIFI_MODE_MESH
	case uciModeSTA, "client", "managed":
		return wificonfigv1.WifiMode_WIFI_MODE_STA
	case uciModeAdHoc, "ad-hoc", "ibss":
		return wificonfigv1.WifiMode_WIFI_MODE_ADHOC
	case uciModeMonitor:
		return wificonfigv1.WifiMode_WIFI_MODE_MONITOR
	default:
		return wificonfigv1.WifiMode_WIFI_MODE_UNSPECIFIED
	}
}

// ProtoToWifiMode converts a WifiMode enum to the canonical UCI mode string.
// Returns "" for UNSPECIFIED so callers can leave the existing value untouched.
func ProtoToWifiMode(m wificonfigv1.WifiMode) string {
	switch m {
	case wificonfigv1.WifiMode_WIFI_MODE_AP:
		return uciModeAP
	case wificonfigv1.WifiMode_WIFI_MODE_MESH:
		return uciModeMesh
	case wificonfigv1.WifiMode_WIFI_MODE_STA:
		return uciModeSTA
	case wificonfigv1.WifiMode_WIFI_MODE_ADHOC:
		return uciModeAdHoc
	case wificonfigv1.WifiMode_WIFI_MODE_MONITOR:
		return uciModeMonitor
	default:
		return ""
	}
}

// WifiEncryptionToProto converts a UCI encryption string to the WifiEncryption enum.
func WifiEncryptionToProto(s string) wificonfigv1.WifiEncryption {
	switch strings.ToLower(s) {
	case wifiAuthSAE:
		return wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_SAE
	case "psk2":
		return wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_PSK2
	case "psk":
		return wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_PSK
	case "psk-mixed":
		return wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_PSK_MIXED
	case "sae-mixed":
		return wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_SAE_MIXED
	case "none":
		return wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_NONE
	case "owe":
		return wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_OWE
	default:
		return wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_UNSPECIFIED
	}
}

// ProtoToWifiEncryption converts a WifiEncryption enum to the UCI string.
func ProtoToWifiEncryption(e wificonfigv1.WifiEncryption) string {
	switch e {
	case wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_SAE:
		return wifiAuthSAE
	case wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_PSK2:
		return "psk2"
	case wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_PSK:
		return "psk"
	case wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_PSK_MIXED:
		return "psk-mixed"
	case wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_SAE_MIXED:
		return "sae-mixed"
	case wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_NONE:
		return "none"
	case wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_OWE:
		return "owe"
	default:
		return ""
	}
}

// WifiHTModeToProto converts a UCI htmode string to the WifiHTMode enum.
func WifiHTModeToProto(s string) wificonfigv1.WifiHTMode {
	switch strings.ToUpper(s) {
	case htModeNoHT:
		return wificonfigv1.WifiHTMode_WIFI_HT_MODE_NOHT
	case htModeHT20:
		return wificonfigv1.WifiHTMode_WIFI_HT_MODE_HT20
	case "HT40-":
		return wificonfigv1.WifiHTMode_WIFI_HT_MODE_HT40_MINUS
	case "HT40+":
		return wificonfigv1.WifiHTMode_WIFI_HT_MODE_HT40_PLUS
	case htModeHT40:
		return wificonfigv1.WifiHTMode_WIFI_HT_MODE_HT40
	case htModeVHT20:
		return wificonfigv1.WifiHTMode_WIFI_HT_MODE_VHT20
	case htModeVHT40:
		return wificonfigv1.WifiHTMode_WIFI_HT_MODE_VHT40
	case htModeVHT80:
		return wificonfigv1.WifiHTMode_WIFI_HT_MODE_VHT80
	case htModeVHT160:
		return wificonfigv1.WifiHTMode_WIFI_HT_MODE_VHT160
	case htModeHE20:
		return wificonfigv1.WifiHTMode_WIFI_HT_MODE_HE20
	case htModeHE40:
		return wificonfigv1.WifiHTMode_WIFI_HT_MODE_HE40
	case htModeHE80:
		return wificonfigv1.WifiHTMode_WIFI_HT_MODE_HE80
	case htModeHE160:
		return wificonfigv1.WifiHTMode_WIFI_HT_MODE_HE160
	default:
		// S1G bandwidth values use lowercase with spaces.
		switch strings.ToLower(s) {
		case "1 mhz":
			return wificonfigv1.WifiHTMode_WIFI_HT_MODE_S1G_1MHZ
		case "2 mhz":
			return wificonfigv1.WifiHTMode_WIFI_HT_MODE_S1G_2MHZ
		case "4 mhz":
			return wificonfigv1.WifiHTMode_WIFI_HT_MODE_S1G_4MHZ
		case "8 mhz":
			return wificonfigv1.WifiHTMode_WIFI_HT_MODE_S1G_8MHZ
		default:
			return wificonfigv1.WifiHTMode_WIFI_HT_MODE_UNSPECIFIED
		}
	}
}

// ProtoToWifiHTMode converts a WifiHTMode enum to the UCI string.
func ProtoToWifiHTMode(h wificonfigv1.WifiHTMode) string {
	switch h {
	case wificonfigv1.WifiHTMode_WIFI_HT_MODE_NOHT:
		return htModeNoHT
	case wificonfigv1.WifiHTMode_WIFI_HT_MODE_HT20:
		return htModeHT20
	case wificonfigv1.WifiHTMode_WIFI_HT_MODE_HT40_MINUS:
		return "HT40-"
	case wificonfigv1.WifiHTMode_WIFI_HT_MODE_HT40_PLUS:
		return "HT40+"
	case wificonfigv1.WifiHTMode_WIFI_HT_MODE_HT40:
		return htModeHT40
	case wificonfigv1.WifiHTMode_WIFI_HT_MODE_VHT20:
		return htModeVHT20
	case wificonfigv1.WifiHTMode_WIFI_HT_MODE_VHT40:
		return htModeVHT40
	case wificonfigv1.WifiHTMode_WIFI_HT_MODE_VHT80:
		return htModeVHT80
	case wificonfigv1.WifiHTMode_WIFI_HT_MODE_VHT160:
		return htModeVHT160
	case wificonfigv1.WifiHTMode_WIFI_HT_MODE_HE20:
		return htModeHE20
	case wificonfigv1.WifiHTMode_WIFI_HT_MODE_HE40:
		return htModeHE40
	case wificonfigv1.WifiHTMode_WIFI_HT_MODE_HE80:
		return htModeHE80
	case wificonfigv1.WifiHTMode_WIFI_HT_MODE_HE160:
		return htModeHE160
	case wificonfigv1.WifiHTMode_WIFI_HT_MODE_S1G_1MHZ:
		return "1 MHz"
	case wificonfigv1.WifiHTMode_WIFI_HT_MODE_S1G_2MHZ:
		return "2 MHz"
	case wificonfigv1.WifiHTMode_WIFI_HT_MODE_S1G_4MHZ:
		return "4 MHz"
	case wificonfigv1.WifiHTMode_WIFI_HT_MODE_S1G_8MHZ:
		return "8 MHz"
	default:
		return ""
	}
}

// ── pointer helpers ──────────────────────────────────────────────────────────

func strPtr(s string) *string {
	return &s
}

func boolPtr(b bool) *bool {
	return &b
}

func int32Ptr(v int32) *int32 {
	return &v
}
