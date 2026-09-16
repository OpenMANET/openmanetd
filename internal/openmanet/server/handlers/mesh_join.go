package handlers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"

	"buf.build/go/protovalidate"
	"connectrpc.com/connect"
	meshjoinv1 "github.com/openmanet/openmanetd/internal/api/openmanet/mesh_join/v1"
	wificonfigv1 "github.com/openmanet/openmanetd/internal/api/openmanet/wifi_config/v1"
	"github.com/openmanet/openmanetd/internal/meshjoin"
	"github.com/openmanet/openmanetd/internal/network"
	"github.com/openmanet/openmanetd/internal/network/morseregdb"
	"github.com/rs/zerolog"
	"google.golang.org/protobuf/types/known/emptypb"
)

const (
	// maxSourceHostname mirrors MeshJoinPayload.source_hostname's max_len.
	maxSourceHostname = 63

	uciTypeMorse = "morse"
)

// MeshJoinService implements meshjoinconnect.MeshJoinServiceHandler:
// it shares this node's mesh credentials as a QR payload and joins the
// node to meshes described by a scanned payload.
type MeshJoinService struct {
	Log          zerolog.Logger
	ConfigReader network.ConfigReader
	// Radios writes radio settings under the wifi service's UCI lock.
	// nil disables ApplyMeshJoin (CodeUnavailable).
	Radios RadioApplier
	// Hostname can be overridden for tests; nil falls back to os.Hostname.
	Hostname func() (string, error)
	// LoadRegDB / RegDBPath mirror SetupService: nil / "" fall back to
	// morseregdb.Load and morseregdb.DefaultPath.
	LoadRegDB func(path string) (*morseregdb.DB, error)
	RegDBPath string
}

// meshIface is one enabled mesh-mode wifi-iface joined with its radio.
type meshIface struct {
	device  *network.UCIWirelessDevice
	iface   *network.UCIWirelessIface
	section string
	radio   string
	network string
	isMorse bool
}

// GetMeshJoinQR renders this node's mesh credentials as a QR code.
func (s *MeshJoinService) GetMeshJoinQR(_ context.Context, _ *emptypb.Empty) (*meshjoinv1.GetMeshJoinQRResponse, error) {
	s.Log.Debug().Msg("GetMeshJoinQR request received")

	ifaces, err := s.listMeshIfaces()
	if err != nil {
		s.Log.Error().Err(err).Msg("Failed to read mesh interfaces")

		return nil, connect.NewError(connect.CodeInternal, err)
	}

	halow := findMeshIface(ifaces, func(mi meshIface) bool { return mi.isMorse })
	if halow == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("no HaLow mesh interface configured"))
	}

	halowCreds, err := credentialsFromIface(*halow)
	if err != nil {
		s.Log.Error().Err(err).Str("section", halow.section).Msg("Failed to read HaLow mesh credentials")

		return nil, connect.NewError(connect.CodeInternal, err)
	}

	if validateErr := validateCredentials("HaLow mesh", halowCreds); validateErr != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("%w; QR sharing needs a WPA3 (SAE) mesh with a passphrase", validateErr))
	}

	payload := &meshjoinv1.MeshJoinPayload{Halow: halowCreds}

	if bh := pickBackhaulIface(ifaces); bh != nil {
		creds, credErr := credentialsFromIface(*bh)
		if credErr != nil {
			s.Log.Warn().Err(credErr).Str("section", bh.section).Msg("Skipping backhaul in QR payload")
		} else if validateErr := validateCredentials("backhaul mesh", creds); validateErr != nil {
			s.Log.Warn().Err(validateErr).Str("section", bh.section).Msg("Skipping backhaul in QR payload")
		} else {
			payload.Backhaul = creds
		}
	}

	hostname, err := s.hostname()
	if err != nil {
		s.Log.Error().Err(err).Msg("Failed to read hostname")

		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("read hostname: %w", err))
	}

	if len(hostname) > maxSourceHostname {
		hostname = hostname[:maxSourceHostname]
	}

	payload.SourceHostname = hostname

	// Defense-in-depth: validate the fully assembled payload against the
	// proto's buf.validate constraints before encoding, so a QR is never
	// minted from a payload the receiver would reject. This catches
	// constraint violations the field-level validateCredentials pass does
	// not (e.g. a malformed country_code read from UCI). GetMeshJoinQR is
	// a rare, operator-triggered call, so building the validator per call
	// is acceptable.
	validator, err := protovalidate.New()
	if err != nil {
		s.Log.Error().Err(err).Msg("Failed to create payload validator")

		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("new validator: %w", err))
	}

	if validateErr := validator.Validate(payload); validateErr != nil {
		s.Log.Error().Err(validateErr).Msg("Assembled mesh join payload failed validation")

		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("validate payload: %w", validateErr))
	}

	text, err := meshjoin.Encode(payload)
	if err != nil {
		s.Log.Error().Err(err).Msg("Failed to encode mesh join payload")

		return nil, connect.NewError(connect.CodeInternal, err)
	}

	svg, err := meshjoin.RenderSVG(text)
	if err != nil {
		s.Log.Error().Err(err).Msg("Failed to render mesh join QR")

		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return &meshjoinv1.GetMeshJoinQRResponse{
		Payload:     payload,
		PayloadText: text,
		Svg:         svg,
	}, nil
}

// joinTarget is one radio ApplyMeshJoin will write.
type joinTarget struct {
	creds   *meshjoinv1.MeshCredentials
	radio   string
	role    meshjoinv1.MeshJoinRadioRole
	isHalow bool
}

// ApplyMeshJoin joins this node to the meshes in a scanned payload.
// Every target is resolved and validated before the first UCI write;
// wireless is reloaded once by the batch apply.
func (s *MeshJoinService) ApplyMeshJoin(ctx context.Context, req *meshjoinv1.ApplyMeshJoinRequest) (*meshjoinv1.ApplyMeshJoinResponse, error) {
	s.Log.Debug().Msg("ApplyMeshJoin request received")

	if s.Radios == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("radio settings are not available on this node"))
	}

	payload := req.GetPayload()

	halowRadio, err := s.resolveHalowRadio(req.GetHalowRadio())
	if err != nil {
		return nil, err
	}

	targets := []joinTarget{{
		radio:   halowRadio,
		role:    meshjoinv1.MeshJoinRadioRole_MESH_JOIN_RADIO_ROLE_HALOW,
		creds:   payload.GetHalow(),
		isHalow: true,
	}}

	var skipped *meshjoinv1.MeshJoinRadioResult

	if payload.GetBackhaul() != nil {
		radio, reason, err := s.resolveBackhaulRadio(req.GetBackhaulRadio())
		if err != nil {
			return nil, err
		}

		if reason != "" {
			skipped = &meshjoinv1.MeshJoinRadioResult{
				Role:   meshjoinv1.MeshJoinRadioRole_MESH_JOIN_RADIO_ROLE_BACKHAUL,
				Status: meshjoinv1.MeshJoinRadioStatus_MESH_JOIN_RADIO_STATUS_SKIPPED,
				Reason: reason,
			}
		} else {
			targets = append(targets, joinTarget{
				radio: radio,
				role:  meshjoinv1.MeshJoinRadioRole_MESH_JOIN_RADIO_ROLE_BACKHAUL,
				creds: payload.GetBackhaul(),
			})
		}
	}

	if err := s.validateTargets(targets); err != nil {
		return nil, err
	}

	updates := make([]RadioSettingsUpdate, 0, len(targets))

	for _, tgt := range targets {
		settings, err := s.buildSettings(tgt)
		if err != nil {
			return nil, err
		}

		updates = append(updates, RadioSettingsUpdate{RadioName: tgt.radio, Settings: settings})
	}

	if err := s.Radios.ApplyRadioSettingsBatch(ctx, updates); err != nil {
		s.Log.Error().Err(err).Msg("Failed to apply mesh join")

		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("apply radio settings: %w", err))
	}

	results := make([]*meshjoinv1.MeshJoinRadioResult, 0, len(targets)+1)

	for _, tgt := range targets {
		results = append(results, &meshjoinv1.MeshJoinRadioResult{
			RadioName: tgt.radio,
			Role:      tgt.role,
			Status:    meshjoinv1.MeshJoinRadioStatus_MESH_JOIN_RADIO_STATUS_APPLIED,
		})
	}

	if skipped != nil {
		results = append(results, skipped)
	}

	s.Log.Info().Int("radios", len(targets)).Msg("Mesh join applied")

	return &meshjoinv1.ApplyMeshJoinResponse{Radios: results}, nil
}

// resolveHalowRadio returns the HaLow target: the override when given
// (it must be a morse device), else the single morse device.
func (s *MeshJoinService) resolveHalowRadio(override string) (string, error) {
	if override != "" {
		if !network.IsMorseDevice(s.ConfigReader, override) {
			return "", connect.NewError(connect.CodeInvalidArgument,
				fmt.Errorf("halow_radio %q is not a HaLow (morse) radio", override))
		}

		return override, nil
	}

	devices, err := s.ConfigReader.GetSections(wirelessConfig, "wifi-device")
	if err != nil {
		return "", connect.NewError(connect.CodeInternal, fmt.Errorf("list wifi-device sections: %w", err))
	}

	morse := make([]string, 0, 1)

	for _, dev := range devices {
		if network.IsMorseDevice(s.ConfigReader, dev) {
			morse = append(morse, dev)
		}
	}

	sort.Strings(morse)

	switch len(morse) {
	case 0:
		return "", connect.NewError(connect.CodeFailedPrecondition, errors.New("no HaLow radio on this node"))
	case 1:
		return morse[0], nil
	default:
		return "", connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("%d HaLow radios found (%s); set halow_radio", len(morse), strings.Join(morse, ", ")))
	}
}

// resolveBackhaulRadio returns the backhaul target, or a skip reason
// when no radio qualifies and none was named. An override must be a
// non-morse device with a linked wifi-iface.
func (s *MeshJoinService) resolveBackhaulRadio(override string) (radio, skipReason string, err error) {
	ifaceSections, err := s.ConfigReader.GetSections(wirelessConfig, "wifi-iface")
	if err != nil {
		return "", "", connect.NewError(connect.CodeInternal, fmt.Errorf("list wifi-iface sections: %w", err))
	}

	if override != "" {
		if network.IsMorseDevice(s.ConfigReader, override) {
			return "", "", connect.NewError(connect.CodeInvalidArgument,
				fmt.Errorf("backhaul_radio %q is the HaLow radio", override))
		}

		if findLinkedIface(override, ifaceSections, s.ConfigReader) == "" {
			return "", "", connect.NewError(connect.CodeInvalidArgument,
				fmt.Errorf("backhaul_radio %q has no wifi-iface", override))
		}

		return override, "", nil
	}

	ifaces, err := s.listMeshIfaces()
	if err != nil {
		return "", "", connect.NewError(connect.CodeInternal, err)
	}

	if bh := pickBackhaulIface(ifaces); bh != nil {
		return bh.radio, "", nil
	}

	return "", "no 2.4 GHz radio is in mesh mode; switch one to mesh and scan again", nil
}

// validateTargets checks every target's credentials and regulatory
// tuple, collecting all issues into one CodeInvalidArgument so a client
// can show them together. Regdb load failures other than "not
// installed" are CodeInternal.
func (s *MeshJoinService) validateTargets(targets []joinTarget) error {
	issues := make([]string, 0, len(targets))

	for _, tgt := range targets {
		label := tgt.radio

		if err := validateCredentials(label, tgt.creds); err != nil {
			issues = append(issues, err.Error())

			continue
		}

		if tgt.isHalow {
			issue, err := s.checkHalowRegulatory(tgt.creds)
			if err != nil {
				s.Log.Error().Err(err).Msg("Failed to load regulatory database")

				return connect.NewError(connect.CodeInternal, err)
			}

			if issue != "" {
				issues = append(issues, label+": "+issue)
			}

			continue
		}

		if issue := checkBackhaulRegulatory(tgt.creds); issue != "" {
			issues = append(issues, label+": "+issue)
		}
	}

	if len(issues) > 0 {
		return connect.NewError(connect.CodeInvalidArgument, errors.New(strings.Join(issues, "; ")))
	}

	return nil
}

// checkHalowRegulatory validates (country, bandwidth, channel) against
// the Morse regdb. Without a regdb it accepts any S1G channel number,
// the same fallback the wizard uses.
func (s *MeshJoinService) checkHalowRegulatory(c *meshjoinv1.MeshCredentials) (string, error) {
	switch c.GetBandwidthMhz() {
	case 1, 2, 4, 8:
	default:
		return fmt.Sprintf("%d MHz is not a HaLow width", c.GetBandwidthMhz()), nil
	}

	db, err := s.loadRegDB()
	if err != nil {
		if !errors.Is(err, morseregdb.ErrNotInstalled) {
			return "", fmt.Errorf("load morse regdb: %w", err)
		}

		s.Log.Warn().Msg("Morse regdb not installed; accepting any S1G channel")

		if !slices.Contains(availableChannelsForBand(wificonfigv1.WifiBand_WIFI_BAND_S1G), strconv.FormatUint(uint64(c.GetChannel()), 10)) {
			return fmt.Sprintf("channel %d is not an S1G channel", c.GetChannel()), nil
		}

		return "", nil
	}

	if c.GetCountryCode() == "" {
		return "country_code is required when the regulatory database is installed", nil
	}

	if !db.IsLegalChannel(c.GetCountryCode(), c.GetBandwidthMhz(), c.GetChannel()) {
		return fmt.Sprintf("channel %d is not legal at %d MHz in %s", c.GetChannel(), c.GetBandwidthMhz(), c.GetCountryCode()), nil
	}

	return "", nil
}

// checkBackhaulRegulatory validates the 2.4 GHz tuple against the
// static list the settings API advertises.
func checkBackhaulRegulatory(c *meshjoinv1.MeshCredentials) string {
	if network.SecondaryMeshHTMode(c.GetBandwidthMhz()) == "" || c.GetBandwidthMhz() == 0 {
		return fmt.Sprintf("%d MHz is not a 2.4 GHz backhaul width (20 or 40)", c.GetBandwidthMhz())
	}

	if !slices.Contains(availableChannelsForBand(wificonfigv1.WifiBand_WIFI_BAND_2G), strconv.FormatUint(uint64(c.GetChannel()), 10)) {
		return fmt.Sprintf("channel %d is not a 2.4 GHz channel (1-11)", c.GetChannel())
	}

	return ""
}

func (s *MeshJoinService) loadRegDB() (*morseregdb.DB, error) {
	loader := s.LoadRegDB
	if loader == nil {
		loader = morseregdb.Load
	}

	path := s.RegDBPath
	if path == "" {
		path = morseregdb.DefaultPath
	}

	return loader(path) //nolint:wrapcheck // callers wrap with context
}

// buildSettings overlays the credentials on the radio's current
// settings so tx power and anything the payload does not carry survive.
func (s *MeshJoinService) buildSettings(tgt joinTarget) (*wificonfigv1.RadioSettings, error) {
	cur, err := s.Radios.CurrentRadioSettings(tgt.radio)
	if err != nil {
		var cerr *connect.Error
		if errors.As(err, &cerr) {
			return nil, err
		}

		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("read %s settings: %w", tgt.radio, err))
	}

	htmode := network.SecondaryMeshHTMode(tgt.creds.GetBandwidthMhz())
	if tgt.isHalow {
		htmode = bandwidthToHTMode(tgt.creds.GetBandwidthMhz())
	}

	bandwidth := WifiHTModeToProto(htmode)
	if bandwidth == wificonfigv1.WifiHTMode_WIFI_HT_MODE_UNSPECIFIED {
		return nil, connect.NewError(connect.CodeInternal,
			fmt.Errorf("radio %s: no htmode for %d MHz", tgt.radio, tgt.creds.GetBandwidthMhz()))
	}

	cur.Mode = wificonfigv1.WifiMode_WIFI_MODE_MESH
	cur.Ssid = tgt.creds.GetMeshId()
	cur.MeshId = strPtr(tgt.creds.GetMeshId())
	cur.Password = strPtr(tgt.creds.GetPassphrase())
	cur.Encryption = wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_SAE
	cur.Channel = strconv.FormatUint(uint64(tgt.creds.GetChannel()), 10)
	cur.Bandwidth = bandwidth
	cur.Disabled = boolPtr(false)

	if cc := tgt.creds.GetCountryCode(); cc != "" {
		cur.Country = strPtr(cc)
	}

	return cur, nil
}

func (s *MeshJoinService) hostname() (string, error) {
	if s.Hostname != nil {
		return s.Hostname()
	}

	return os.Hostname() //nolint:wrapcheck // caller wraps
}

// listMeshIfaces returns every enabled mesh-mode wifi-iface joined with
// its wifi-device, sorted by section name so target selection is
// deterministic regardless of UCI iteration order.
func (s *MeshJoinService) listMeshIfaces() ([]meshIface, error) {
	sections, err := s.ConfigReader.GetSections(wirelessConfig, "wifi-iface")
	if err != nil {
		return nil, fmt.Errorf("list wifi-iface sections: %w", err)
	}

	out := make([]meshIface, 0, len(sections))

	for _, sec := range sections {
		iface, err := network.GetWirelessIfaceByNameWithReader(sec, s.ConfigReader)
		if err != nil {
			return nil, fmt.Errorf("read iface %s: %w", sec, err)
		}

		if iface.Mode != uciModeMesh || iface.Disabled == "1" || iface.Device == "" {
			continue
		}

		dev, err := network.GetWirelessDeviceByNameWithReader(iface.Device, s.ConfigReader)
		if err != nil {
			return nil, fmt.Errorf("read device %s: %w", iface.Device, err)
		}

		out = append(out, meshIface{
			section: sec,
			radio:   iface.Device,
			network: iface.Network,
			isMorse: strings.EqualFold(dev.Type, uciTypeMorse),
			device:  dev,
			iface:   iface,
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].section < out[j].section })

	return out, nil
}

func findMeshIface(ifaces []meshIface, match func(meshIface) bool) *meshIface {
	for i := range ifaces {
		if match(ifaces[i]) {
			return &ifaces[i]
		}
	}

	return nil
}

// pickBackhaulIface prefers the mesh iface bound to the secondary
// batadv hardif, then any other non-HaLow mesh iface.
func pickBackhaulIface(ifaces []meshIface) *meshIface {
	if mi := findMeshIface(ifaces, func(mi meshIface) bool {
		return !mi.isMorse && mi.network == network.BatmanSecondaryIface
	}); mi != nil {
		return mi
	}

	return findMeshIface(ifaces, func(mi meshIface) bool { return !mi.isMorse })
}

// credentialsFromIface lifts the UCI iface + device options into a
// MeshCredentials message.
func credentialsFromIface(mi meshIface) (*meshjoinv1.MeshCredentials, error) {
	bw, ok := network.HTModeBandwidthMHz(mi.device.HTMode)
	if !ok {
		return nil, fmt.Errorf("radio %s: unknown htmode %q", mi.radio, mi.device.HTMode)
	}

	ch, err := strconv.ParseUint(mi.device.Channel, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("radio %s: channel %q is not a number: %w", mi.radio, mi.device.Channel, err)
	}

	return &meshjoinv1.MeshCredentials{
		MeshId:       mi.iface.MeshID,
		Passphrase:   mi.iface.Key,
		Encryption:   WifiEncryptionToProto(mi.iface.Encryption),
		BandwidthMhz: bw,
		Channel:      uint32(ch),
		CountryCode:  strings.ToUpper(mi.device.Country),
	}, nil
}

// validateCredentials applies the rules shared by both RPCs: SAE only,
// a passphrase of 8..63 characters, a mesh ID of 1..32 characters and a
// channel. Regulatory checks are separate.
func validateCredentials(label string, c *meshjoinv1.MeshCredentials) error {
	switch {
	case c == nil:
		return fmt.Errorf("%s: credentials are required", label)
	case c.GetEncryption() != wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_SAE:
		return fmt.Errorf("%s: mesh is not WPA3 (SAE)", label)
	case len(c.GetPassphrase()) < 8 || len(c.GetPassphrase()) > 63:
		return fmt.Errorf("%s: passphrase must be 8..63 characters", label)
	case c.GetMeshId() == "" || len(c.GetMeshId()) > 32:
		return fmt.Errorf("%s: mesh ID must be 1..32 characters", label)
	case c.GetChannel() == 0:
		return fmt.Errorf("%s: channel is required", label)
	default:
		return nil
	}
}
