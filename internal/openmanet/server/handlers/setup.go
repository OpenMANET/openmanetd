package handlers

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	setupv1 "github.com/openmanet/openmanetd/internal/api/openmanet/setup/v1"
	wificonfigv1 "github.com/openmanet/openmanetd/internal/api/openmanet/wifi_config/v1"
	"github.com/openmanet/openmanetd/internal/auth"
	"github.com/openmanet/openmanetd/internal/config"
	"github.com/openmanet/openmanetd/internal/iwinfo"
	"github.com/openmanet/openmanetd/internal/network"
	"github.com/openmanet/openmanetd/internal/network/morseregdb"
	"github.com/openmanet/openmanetd/internal/system"
	"github.com/openmanet/openmanetd/internal/tzinfo"
	"github.com/rs/zerolog"
	"google.golang.org/protobuf/types/known/emptypb"
)

// HostnameSetter stages the system hostname. Production wraps
// network.StageSystemHostnameWithReader; tests substitute a fake.
//
// CRITICAL: SetHostname must NOT commit the UCI tree. The wizard
// commits all seven configs atomically in phase 12. A premature commit
// here opens a window where a failure between phase 5 and phase 12
// would leave the hostname change durable but the rest of the
// wizard's writes rolled back — a half-applied wizard run.
type HostnameSetter interface {
	SetHostname(ctx context.Context, hostname string) error
}

// DefaultHostnameSetter is the production HostnameSetter. It writes
// `system.@system[0].hostname` via the supplied UCI reader. The write
// is STAGED — phase 12's UCI commit flushes it to disk along with the
// rest of the wizard's writes.
type DefaultHostnameSetter struct {
	Reader network.ConfigReader
}

// SetHostname implements HostnameSetter. Stages the write without
// committing.
func (d *DefaultHostnameSetter) SetHostname(_ context.Context, hostname string) error {
	return network.StageSystemHostnameWithReader(hostname, d.Reader)
}

// hostnameRFC1123 matches an RFC 1123 hostname label: 1-63 chars,
// alphanumeric, hyphens allowed internally, must NOT start or end
// with a hyphen. Mirrors the proto validator pattern so the handler
// can re-check before writing UCI as defense-in-depth.
var hostnameRFC1123 = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?$`)

// factoryHostnamePattern matches the OpenMANET firmware's default
// hostname (`BCM2711-XXXX`) so GetSetupStatus can flag a fresh
// device as "not yet configured" via current_hostname inspection.
var factoryHostnamePattern = regexp.MustCompile(`^BCM\d+-[0-9a-fA-F]+$`)

// UCISnapshot is an opaque snapshot of every UCI config the wizard
// touches. The snapshot is taken at phase 2 and restored on any
// failure between phases 3-13 so a partial wizard run doesn't leave
// the device in an inconsistent intermediate state.
type UCISnapshot interface {
	// Configs returns the names of the UCI configs captured in this
	// snapshot. Used by tests to assert the snapshotter saw the
	// expected scopes.
	Configs() []string
}

// UCISnapshotter captures and restores the UCI tree across the seven
// configs the setup wizard mutates. Production wraps the digineo
// go-uci tree with a per-config /etc/config/* file dump; tests
// substitute a fake that deep-copies the in-memory mock state.
type UCISnapshotter interface {
	// Snapshot reads the current UCI state of `configs` into an
	// opaque snapshot. The handler captures this immediately before
	// the reset phase so any later failure can be rolled back.
	Snapshot(ctx context.Context, configs []string) (UCISnapshot, error)
	// Restore writes the snapshot back over the live UCI tree. Used
	// by the rollback path on any phase 3-13 failure.
	Restore(ctx context.Context, snapshot UCISnapshot) error
}

// wizardConfigs lists the UCI configs the wizard touches. The
// snapshot phase captures all of them so any failure can be rolled
// back atomically. umdns is included so a failure between the
// base-network phase (which registers lan/ahwlan with umdns) and
// phase 12's commit rolls the umdns write back along with everything
// else, rather than leaving a half-applied umdns section live.
// openmanetd carries the address-reservation flags staged in phase 9.
// luci carries the wizard-used flag and homepage reset staged in phase 9.
var wizardConfigs = []string{ //nolint:gochecknoglobals // package-level constant
	"wireless", "network", "dhcp", "firewall", "system", "mesh11sd", "umdns", "openmanetd", "luci",
}

// SetupService implements the SetupService ConnectRPC service. It
// runs the first-boot setup wizard. Both RPCs are exempt from auth
// (see auth/middleware.go isAPISkipPath); the middleware additionally
// gates on `setup.enabled=true` and `setup.complete=false`. Phase 0
// of ApplySetup re-applies these checks as defense-in-depth.
type SetupService struct {
	Log            zerolog.Logger
	Reloader       system.ServiceReloader
	UCI            network.ConfigReader
	Snapshotter    UCISnapshotter
	PasswordSetter auth.PasswordSetter
	HostnameSetter HostnameSetter
	Iwinfo         iwinfo.IwinfoProvider
	WirelessStatus network.WirelessStatusProvider
	Interfaces     InterfaceProvider
	Cfg            *config.Config
	RNG            *rand.Rand
	LoadRegDB      func(path string) (*morseregdb.DB, error)
	SetTimeFn      func(time.Time) error // overrideable clock write for tests; nil falls back to unix.Settimeofday
	nowFn          func() time.Time      // overrideable time.Now for deterministic clock-drift tests; nil falls back to time.Now
	RegDBPath      string
	mu             sync.Mutex
}

// ─── Enum translators ─────────────────────────────────────────────────────────
//
// Each defined enum value has a string_name annotation in the .proto. The
// translators below carry that mapping into Go. Round-trip tests in
// setup_test.go assert the mapping is symmetric and unknown UCI strings
// produce the *_UNSPECIFIED variant.

// ProtoToMeshRole maps the wizard MeshRole enum onto the UCI
// `mesh11sd.mesh_params.mesh_gate_announcements` value.
func ProtoToMeshRole(r setupv1.MeshRole) string {
	switch r {
	case setupv1.MeshRole_MESH_ROLE_MESH_POINT:
		return "0"
	case setupv1.MeshRole_MESH_ROLE_MESH_GATE:
		return "1"
	default:
		return ""
	}
}

// MeshRoleToProto maps a UCI mesh_gate_announcements value back to
// the MeshRole enum. Unknown inputs return UNSPECIFIED.
func MeshRoleToProto(s string) setupv1.MeshRole {
	switch s {
	case "0":
		return setupv1.MeshRole_MESH_ROLE_MESH_POINT
	case "1":
		return setupv1.MeshRole_MESH_ROLE_MESH_GATE
	default:
		return setupv1.MeshRole_MESH_ROLE_UNSPECIFIED
	}
}

// meshPointModeNone is the UCI string value for
// MESH_POINT_MODE_NONE in network.wizard.device_mode_meshpoint.
const meshPointModeNone = "none"

// meshPointModeExtender is the UCI string value for
// MESH_POINT_MODE_EXTENDER in network.wizard.device_mode_meshpoint.
const meshPointModeExtender = "extender"

// ProtoToMeshPointMode maps the MeshPointMode enum onto the UCI
// `network.wizard.device_mode_meshpoint` value used by the
// settings UI.
func ProtoToMeshPointMode(m setupv1.MeshPointMode) string {
	switch m {
	case setupv1.MeshPointMode_MESH_POINT_MODE_NONE:
		return meshPointModeNone
	case setupv1.MeshPointMode_MESH_POINT_MODE_EXTENDER:
		return meshPointModeExtender
	default:
		return ""
	}
}

// MeshPointModeToProto maps a UCI device_mode_meshpoint value back
// to the MeshPointMode enum. Unknown inputs return UNSPECIFIED.
func MeshPointModeToProto(s string) setupv1.MeshPointMode {
	switch strings.ToLower(s) {
	case meshPointModeNone:
		return setupv1.MeshPointMode_MESH_POINT_MODE_NONE
	case meshPointModeExtender:
		return setupv1.MeshPointMode_MESH_POINT_MODE_EXTENDER
	default:
		return setupv1.MeshPointMode_MESH_POINT_MODE_UNSPECIFIED
	}
}

// ProtoToMeshGateMode maps the MeshGateMode enum onto the UCI
// `network.wizard.device_mode_meshgate` value.
func ProtoToMeshGateMode(m setupv1.MeshGateMode) string {
	switch m {
	case setupv1.MeshGateMode_MESH_GATE_MODE_ROUTER:
		return "router"
	case setupv1.MeshGateMode_MESH_GATE_MODE_ROUTER_FIREWALL:
		return "router_firewall"
	default:
		return ""
	}
}

// MeshGateModeToProto maps a UCI device_mode_meshgate value back to
// the MeshGateMode enum. Unknown inputs return UNSPECIFIED.
func MeshGateModeToProto(s string) setupv1.MeshGateMode {
	switch strings.ToLower(s) {
	case "router":
		return setupv1.MeshGateMode_MESH_GATE_MODE_ROUTER
	case "router_firewall":
		return setupv1.MeshGateMode_MESH_GATE_MODE_ROUTER_FIREWALL
	default:
		return setupv1.MeshGateMode_MESH_GATE_MODE_UNSPECIFIED
	}
}

// ProtoToUplinkType maps the UplinkType enum onto the UCI
// `network.wizard.uplink` prefix used by the LuCI Morse wizard.
// Callers append a port/radio suffix (e.g. "-eth0", "-radio2-sta")
// when one is supplied via Uplink.ethernet_port or
// Uplink.wireless.radio_name.
func ProtoToUplinkType(u setupv1.UplinkType) string {
	switch u {
	case setupv1.UplinkType_UPLINK_TYPE_ETHERNET:
		return "ethernet"
	case setupv1.UplinkType_UPLINK_TYPE_WIRELESS_STA:
		return "wifi-sta"
	default:
		return ""
	}
}

// UplinkTypeToProto maps a UCI uplink prefix back to the UplinkType
// enum. Unknown inputs return UNSPECIFIED.
func UplinkTypeToProto(s string) setupv1.UplinkType {
	switch strings.ToLower(s) {
	case "ethernet":
		return setupv1.UplinkType_UPLINK_TYPE_ETHERNET
	case "wifi-sta", "wireless-sta":
		return setupv1.UplinkType_UPLINK_TYPE_WIRELESS_STA
	default:
		return setupv1.UplinkType_UPLINK_TYPE_UNSPECIFIED
	}
}

// ─── GetSetupStatus ───────────────────────────────────────────────────────────

// GetSetupStatus reports whether the wizard is reachable on this
// device and provides the hardware context (radios, ethernet ports,
// hostname, regulatory countries) the wizard needs to render its
// first screens.
func (s *SetupService) GetSetupStatus(ctx context.Context, _ *emptypb.Empty) (*setupv1.GetSetupStatusResponse, error) {
	s.Log.Debug().Msg("GetSetupStatus request received")

	// is_setup_complete OR'ed with the legacy LuCI Morse wizard flag:
	// firmware that's already been through the LuCI wizard
	// (`luci.wizard.used=1`) has all the same UCI state the new wizard
	// would otherwise produce, so we treat it as fully configured and
	// hide the new wizard route.
	//
	// One system-config read serves both current_hostname and
	// current_timezone below.
	sysCfg := s.currentSystemConfig()

	resp := &setupv1.GetSetupStatusResponse{
		IsEnabled:       s.Cfg.GetSetupEnabled(),
		IsSetupComplete: s.Cfg.GetSetupComplete() || s.legacyLuciWizardUsed(),
		CurrentHostname: sysCfg.Hostname,
		Radios:          s.collectSetupRadios(ctx),
		EthernetPorts:   s.collectEthernetPorts(),
		Timezones:       tzinfo.Names(),
		CurrentTimezone: sysCfg.Zonename,
	}

	resp.HasHalowRadio = anyHalow(resp.Radios)
	resp.AlreadyConfigured = s.detectAlreadyConfigured(resp.CurrentHostname)
	resp.Countries, resp.CurrentCountry = s.collectRegulatoryCountries()

	return resp, nil
}

// legacyLuciWizardUsed reports whether a wizard — the legacy LuCI
// Morse wizard or this one — has already marked the device configured
// in /etc/config/luci:
//
//	config wizard 'wizard'
//	    option used '1'
//
// The Go wizard writes the same flag at apply (writeLuciBookkeeping) so
// LuCI stops offering its own wizard. When set we treat the device as
// fully set up. Operators who genuinely want to re-run setup must
// clear it together with setup.complete: `openmanetd setup-reset`
// does both.
func (s *SetupService) legacyLuciWizardUsed() bool {
	v, ok := s.UCI.Get("luci", "wizard", "used")
	if !ok || len(v) == 0 {
		return false
	}

	switch strings.ToLower(strings.TrimSpace(v[0])) {
	case "1", "true", "yes", "on":
		return true
	}

	return false
}

// collectRegulatoryCountries loads the Morse Micro regdb and returns
// (countries, current_country). The current country is read from the
// first morse wifi-device's `country` UCI field; empty if none.
//
// Failures here are NOT fatal: the regdb is provided by a userspace
// package and may be missing on developer hardware or older firmware.
// In that case we return an empty list and the frontend falls back to
// a baked-in US default so the wizard still completes.
func (s *SetupService) collectRegulatoryCountries() ([]*setupv1.SetupCountry, string) {
	loader := s.LoadRegDB
	if loader == nil {
		loader = morseregdb.Load
	}

	path := s.RegDBPath
	if path == "" {
		path = morseregdb.DefaultPath
	}

	db, err := loader(path)
	if err != nil {
		if !errors.Is(err, morseregdb.ErrNotInstalled) {
			s.Log.Warn().Err(err).Str("path", path).Msg("morse regdb load failed")
		}

		return nil, s.currentMorseCountry()
	}

	src := db.Countries()
	out := make([]*setupv1.SetupCountry, 0, len(src))

	for _, c := range src {
		bws := make([]*setupv1.SetupRadioBandwidth, 0, len(c.Bandwidths))
		for _, bw := range c.Bandwidths {
			bws = append(bws, &setupv1.SetupRadioBandwidth{
				Mhz:      bw.Mhz,
				Channels: bw.Channels,
			})
		}

		out = append(out, &setupv1.SetupCountry{
			Code:       c.Code,
			Name:       c.Name,
			Bandwidths: bws,
		})
	}

	return out, s.currentMorseCountry()
}

// currentMorseCountry reads `wireless.<radio>.country` from the first
// wifi-device whose type is `morse`. Empty if no morse radio exists or
// the country is unset on the radio.
func (s *SetupService) currentMorseCountry() string {
	deviceSections, err := s.UCI.GetSections("wireless", "wifi-device")
	if err != nil {
		return ""
	}

	for _, name := range deviceSections {
		typ, ok := s.UCI.Get("wireless", name, "type")
		if !ok || len(typ) == 0 || !strings.EqualFold(typ[0], "morse") {
			continue
		}

		c, ok := s.UCI.Get("wireless", name, "country")
		if !ok || len(c) == 0 {
			return ""
		}

		return strings.ToUpper(c[0])
	}

	return ""
}

// currentSystemConfig reads `system.@system[0]` via the UCI reader
// once, returning a zero-value UCISystem (never nil) if the section
// cannot be read — so GetSetupStatus can pull both current_hostname
// and current_timezone from a single read rather than two. The
// SetupGate polls GetSetupStatus, so halving its UCI reads matters.
func (s *SetupService) currentSystemConfig() *network.UCISystem {
	cfg, err := network.GetSystemConfigWithReader(s.UCI)
	if err != nil {
		s.Log.Warn().Err(err).Msg("reading system config")

		return &network.UCISystem{}
	}

	return cfg
}

// collectSetupRadios returns one SetupRadio entry per `wifi-device`
// in the wireless UCI config, marking radios with `option type 'morse'`
// as HaLow. Bandwidths/channels per radio are not yet populated; the
// wizard handler defers to WifiConfigService.GetRadioSettings for the
// channel list (frontend calls both RPCs in parallel).
//
// When at least one non-HaLow 2.4 GHz radio exists, the radios' live
// interfaces are resolved through iwinfo + ubus wireless status (the
// correlation mgmt.setupBatMesh1Interface uses) so the label can show
// the chipset and supports_mesh_backhaul can say whether the daemon
// would run the secondary link there. On HaLow-only boards nothing is
// resolved, keeping the SetupGate poll free of ubus calls.
func (s *SetupService) collectSetupRadios(ctx context.Context) []*setupv1.SetupRadio {
	deviceSections, err := s.UCI.GetSections("wireless", "wifi-device")
	if err != nil {
		s.Log.Warn().Err(err).Msg("listing wifi-device sections")

		return nil
	}

	radios := make([]*setupv1.SetupRadio, 0, len(deviceSections))
	candidates := make(map[string]struct{}, len(deviceSections))

	for _, name := range deviceSections {
		dev, err := network.GetWirelessDeviceByNameWithReader(name, s.UCI)
		if err != nil {
			continue
		}

		radios = append(radios, &setupv1.SetupRadio{
			Name:         name,
			Band:         dev.Band,
			HardwareName: dev.Path,
			IsHalow:      strings.EqualFold(dev.Type, "morse"),
		})

		if isSecondaryMeshCandidate(dev) {
			candidates[name] = struct{}{}
		}
	}

	if len(candidates) == 0 {
		return radios
	}

	hardware := s.resolveRadioHardware(ctx, deviceSections)

	for _, radio := range radios {
		hw, ok := hardware[radio.GetName()]
		if !ok {
			continue
		}

		radio.HardwareName = hw

		if _, candidate := candidates[radio.GetName()]; candidate {
			radio.SupportsMeshBackhaul = network.SupportsSecondaryMeshLink(hw)
		}
	}

	return radios
}

// isSecondaryMeshCandidate reports whether a wifi-device could carry the
// 2.4 GHz secondary mesh link at all: band 2g and not the HaLow radio.
// The chipset test is applied separately, once the live interface is
// resolved.
func isSecondaryMeshCandidate(dev *network.UCIWirelessDevice) bool {
	return dev.Band == "2g" && !strings.EqualFold(dev.Type, "morse")
}

// resolveRadioHardware maps each wifi-device section to the iwinfo
// hardware name of its live interface. Both providers are optional;
// when either is missing or fails every radio stays unresolved and the
// callers fall back (bus-path label, no backhaul offer, daemon fallback
// at boot). Two ubus calls per invocation.
func (s *SetupService) resolveRadioHardware(ctx context.Context, devices []string) map[string]string {
	out := make(map[string]string, len(devices))

	if s.Iwinfo == nil || s.WirelessStatus == nil {
		return out
	}

	infos, err := s.Iwinfo.GetInfoForAll(ctx)
	if err != nil {
		s.Log.Debug().Err(err).Msg("iwinfo unavailable; radio hardware names unresolved")

		return out
	}

	status, err := s.WirelessStatus.GetWirelessStatus(ctx)
	if err != nil {
		s.Log.Debug().Err(err).Msg("wireless status unavailable; radio hardware names unresolved")

		return out
	}

	for _, name := range devices {
		if hw := network.ResolveWirelessRadioHardwareName(name, status, infos); hw != "" {
			out[name] = hw
		}
	}

	return out
}

// collectEthernetPorts enumerates the ethernet interfaces that the
// wizard's uplink step lets the operator choose from. Pulled from
// the existing InterfaceProvider so the wizard sees the same view
// the dashboard does.
func (s *SetupService) collectEthernetPorts() []string {
	if s.Interfaces == nil {
		return nil
	}

	all, err := s.Interfaces.ListAll()
	if err != nil {
		s.Log.Warn().Err(err).Msg("listing network interfaces")

		return nil
	}

	out := make([]string, 0, len(all))

	for _, info := range all {
		if isLikelyEthernetPort(info.Name) {
			out = append(out, info.Name)
		}
	}

	return out
}

// isLikelyEthernetPort returns true for interface names that look
// like physical ethernet ports. Conservative heuristic: starts with
// "eth", "lan", "en", or matches "wan*". Excludes wireless (wlan*, wlh*)
// and bridges (br*) and batman (bat*, batmesh*).
func isLikelyEthernetPort(name string) bool {
	if name == "" {
		return false
	}

	switch {
	case strings.HasPrefix(name, "wlan"),
		strings.HasPrefix(name, "wlh"),
		strings.HasPrefix(name, "br"),
		strings.HasPrefix(name, "bat"),
		strings.HasPrefix(name, "lo"),
		strings.HasPrefix(name, "tun"),
		strings.HasPrefix(name, "tap"),
		strings.HasPrefix(name, "vxlan"),
		strings.HasPrefix(name, "tailscale"):
		return false
	}

	return strings.HasPrefix(name, "eth") ||
		strings.HasPrefix(name, "lan") ||
		strings.HasPrefix(name, "en") ||
		strings.HasPrefix(name, "wan")
}

// anyHalow reports whether the supplied radio list contains at least
// one HaLow (802.11ah / S1G) radio.
func anyHalow(radios []*setupv1.SetupRadio) bool {
	for _, r := range radios {
		if r.IsHalow {
			return true
		}
	}

	return false
}

// detectAlreadyConfigured returns true when the device's UCI state
// looks like the wizard (or an operator) has already configured it.
// Heuristics, in order of confidence:
//   - the wizard's own bookkeeping section `network.wizard` exists
//     (only the wizard ever writes that)
//   - auth.enable is true (auth is wizard-owned, factory image has it off)
//   - `network.ahwlan` interface exists (wizard's mesh network)
//   - the system hostname does NOT match the factory pattern
//
// Notably we do NOT inspect `mesh11sd.mesh_params.mesh_gate_announcements`
// because the factory image ships that section with `option
// mesh_gate_announcements '0'` already set, which would otherwise flag
// every fresh device as configured.
func (s *SetupService) detectAlreadyConfigured(currentHostname string) bool {
	if s.Cfg.GetAuthEnable() {
		return true
	}

	if sections, err := s.UCI.GetSections("network", "wizard"); err == nil && len(sections) > 0 {
		return true
	}

	if sections, err := s.UCI.GetSections("network", "interface"); err == nil && slices.Contains(sections, "ahwlan") {
		return true
	}

	if currentHostname != "" && !factoryHostnamePattern.MatchString(currentHostname) {
		return true
	}

	return false
}

// ─── ApplySetup (placeholder; phases land in subsequent rounds) ──────────────

// applySetupStream is the narrow Send-only interface the phase
// helpers use. The connect.ServerStream returned by the wire layer
// satisfies it. Tests substitute a fake collector to inspect the
// event sequence without standing up an httptest server.
type applySetupStream interface {
	Send(*setupv1.ApplySetupResponse) error
}

// ApplySetup atomically applies a complete MeshNodeProfile. Each
// phase emits a STATUS_STARTED event on entry and STATUS_DONE or
// STATUS_FAILED on exit. The terminal event (PHASE_TERMINAL) is
// emitted before service reloads so the frontend has a clean signal
// even if reloads sever the streaming connection.
func (s *SetupService) ApplySetup(
	ctx context.Context,
	req *setupv1.ApplySetupRequest,
	stream *connect.ServerStream[setupv1.ApplySetupResponse],
) error {
	return s.applySetup(ctx, req.GetProfile(), stream)
}

// applySetup is the testable core of ApplySetup. It accepts the
// narrow applySetupStream interface so unit tests can drive it
// without a real connect ServerStream (which has no public
// constructor).
func (s *SetupService) applySetup(
	ctx context.Context,
	profile *setupv1.MeshNodeProfile,
	stream applySetupStream,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if profile == nil {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("profile is required"))
	}

	// Phase 0: re-apply guard. Refuse if disabled or already complete.
	if err := s.checkReapplyGuard(ctx); err != nil {
		return err
	}

	// Phase 1: validation (cross-field invariants beyond what
	// buf.validate enforces). Emits STARTED/DONE events around the
	// validation block.
	if err := s.runValidate(ctx, stream, profile); err != nil {
		return s.emitTerminal(stream, setupv1.ApplySetupResponse_PHASE_VALIDATE, err, profile)
	}

	// Phase 2: snapshot pre-apply UCI state. Required so any phase
	// 3-13 failure can be rolled back atomically.
	snapshot, err := s.runSnapshot(ctx, stream)
	if err != nil {
		return s.emitTerminal(stream, setupv1.ApplySetupResponse_PHASE_SNAPSHOT, err, profile)
	}

	// runMutationPhases threads the snapshot/rollback pattern across
	// phases 3-12. On any failure it restores the snapshot before
	// returning so the device's UCI state is unchanged on disk
	// (commit hasn't run yet) AND in memory (the staged tree is
	// reverted).
	if failedPhase, err := s.runMutationPhases(ctx, stream, profile, snapshot); err != nil {
		s.rollback(ctx, snapshot)

		return s.emitTerminal(stream, failedPhase, err, profile)
	}

	// Phase 13: set the admin password. Failure here still triggers
	// UCI rollback because commit has run; the partially-set password
	// is left as-is (the user knows what they typed).
	if err := s.runPassword(ctx, stream, profile); err != nil {
		s.rollback(ctx, snapshot)

		return s.emitTerminal(stream, setupv1.ApplySetupResponse_PHASE_PASSWORD, err, profile)
	}

	// Phase 14: synchronously reload services. If reload fails for any
	// service AND the restart fallback also fails, abort here BEFORE
	// flipping setup.complete=true. This is the load-bearing safety
	// gate — without it, a failed reload would brick the device and
	// permanently disable the wizard.
	//
	// The reload runs synchronously inside the request context. If the
	// reload severs the streaming connection (it usually does on the
	// network reload), the user falls back to the SetupGate's polling
	// path which checks setup.complete every 4 seconds for 60 seconds.
	if err := s.runReloadServices(ctx, stream); err != nil {
		// Do NOT rollback UCI: phases 1-12 succeeded and a failed
		// reload doesn't mean UCI is bad. Leaving the new UCI on disk
		// means a reboot will pick up the correct config.
		return s.emitTerminal(stream, setupv1.ApplySetupResponse_PHASE_RELOAD_SERVICES, err, profile)
	}

	// Phase 15: atomic flag flip. ONLY runs after reload succeeded.
	// After this returns nil, the device is considered fully
	// configured and the re-apply guard rejects new ApplySetup calls.
	if err := s.runPersistFlags(ctx, stream); err != nil {
		// Do NOT rollback: phases 1-12 + password + reload succeeded,
		// the device just needs the user to re-run the wizard so the
		// flags get a chance to flip on a subsequent attempt.
		return s.emitTerminal(stream, setupv1.ApplySetupResponse_PHASE_PERSIST_FLAGS, err, profile)
	}

	// Phase 16: emit the success terminal event. By this point UCI is
	// committed, the password is set, services are reloaded, and the
	// flags are flipped. The streaming connection MAY have been
	// severed by the reload — the SetupGate's polling fallback
	// handles that by querying GetSetupStatus.
	if err := s.emitTerminalSuccess(stream, profile); err != nil {
		s.Log.Warn().Err(err).Msg("emit terminal success failed (connection likely already torn down by reload)")
	}

	return nil
}

// emitTerminalSuccess sends the final PHASE_TERMINAL event reporting
// the wizard succeeded, with the SSID and URL the user should use
// to reconnect.
func (s *SetupService) emitTerminalSuccess(stream applySetupStream, profile *setupv1.MeshNodeProfile) error {
	return stream.Send(&setupv1.ApplySetupResponse{
		Phase:  setupv1.ApplySetupResponse_PHASE_TERMINAL,
		Status: setupv1.ApplySetupResponse_STATUS_DONE,
		Result: &setupv1.ApplySetupResult{
			Success:      true,
			ExpectedSsid: expectedSSID(profile),
			ExpectedUrl:  expectedURL(profile),
		},
	})
}

// reloadServices is the canonical list of init.d services the wizard
// reload phase nudges, in dependency order. `network` reloads first
// so its new bridges/interfaces are live before `firewall` rebuilds
// chains and `wireless` brings up wifi-ifaces against the new
// network sections. `dhcp`, `mesh11sd`, and `system` come last
// because they depend on network being up. `umdns` comes last of all
// so it re-announces after every other service has settled; reload
// tolerance (reloadOneService) means an image without umdns installed
// just logs a warning here and the other six still succeed.
var reloadServices = []string{ //nolint:gochecknoglobals // package-level constant
	"network", "firewall", "wireless", "dhcp", "mesh11sd", "system", "umdns",
}

// runReloadServices reloads every service in the canonical list,
// synchronously, with `restart` as a fallback when `reload` fails.
// Per-service outcomes are logged AND emitted as PHASE_RELOAD_SERVICES
// progress events so the frontend can show partial progress.
//
// Failure semantics: only returns an error when EVERY service's
// reload AND restart both failed (which indicates init.d itself is
// broken, not our config). When a subset fails, the wizard logs
// warnings and proceeds to PERSIST_FLAGS — the UCI on disk is
// correct and a reboot will pick it up; flipping setup.complete=true
// is the right call so the device exits the wizard state.
//
// Runs synchronously inside the request context. Network reload may
// sever the streaming connection mid-phase; the SetupGate's polling
// fallback queries GetSetupStatus to discover whether setup
// eventually completed.
func (s *SetupService) runReloadServices(ctx context.Context, stream applySetupStream) error {
	if s.Reloader == nil {
		s.Log.Debug().Msg("no service reloader configured; skipping reload phase")

		return s.runPhase(stream, setupv1.ApplySetupResponse_PHASE_RELOAD_SERVICES,
			"no reloader configured; skipping",
			func() error { return nil })
	}

	return s.runPhase(stream, setupv1.ApplySetupResponse_PHASE_RELOAD_SERVICES,
		"reloading network services", func() error {
			succeeded := 0

			for _, svc := range reloadServices {
				if s.reloadOneService(ctx, svc) {
					succeeded++
				}
			}

			if succeeded == 0 && len(reloadServices) > 0 {
				return errors.New(
					"every service reload AND restart failed; init.d may be broken on this device")
			}

			return nil
		})
}

// reloadOneService tries `reload` on svc, falling back to `restart`
// on failure. Returns true if either succeeded, false if both failed.
// Logs the outcome at Info / Warn.
func (s *SetupService) reloadOneService(ctx context.Context, svc string) bool {
	if err := s.Reloader.Reload(ctx, svc); err == nil {
		s.Log.Info().Str("service", svc).Msg("reload ok")

		return true
	}

	if err := s.Reloader.Restart(ctx, svc); err != nil {
		s.Log.Warn().Str("service", svc).Err(err).
			Msg("service reload AND restart failed; UCI is correct on disk and will take effect on next reboot")

		return false
	}

	s.Log.Info().Str("service", svc).Msg("restart ok (reload had failed)")

	return true
}

// rollback is a best-effort restore of the UCI snapshot taken at
// phase 2. Any error is logged but not surfaced to the caller — the
// caller already has a more interesting error from the failing phase
// to report.
func (s *SetupService) rollback(ctx context.Context, snapshot UCISnapshot) {
	if s.Snapshotter == nil || snapshot == nil {
		return
	}

	if err := s.Snapshotter.Restore(ctx, snapshot); err != nil {
		s.Log.Error().Err(err).Msg("UCI rollback failed; on-disk state is unchanged but in-memory tree may be inconsistent")
	}
}

// checkReapplyGuard rejects ApplySetup when the wizard is disabled
// (CodeUnavailable) or already complete (CodeFailedPrecondition).
// The middleware applies the same gate; this is defense-in-depth.
func (s *SetupService) checkReapplyGuard(_ context.Context) error {
	if !s.Cfg.GetSetupEnabled() {
		return connect.NewError(connect.CodeUnavailable,
			errors.New("setup wizard is disabled on this device"))
	}

	if s.Cfg.GetSetupComplete() {
		return connect.NewError(connect.CodeFailedPrecondition,
			errors.New("setup wizard already completed on this device"))
	}

	// Set by the LuCI wizard and, since F2, by this wizard too — see
	// legacyLuciWizardUsed. setup-reset clears it alongside
	// setup.complete.
	if s.legacyLuciWizardUsed() {
		return connect.NewError(connect.CodeFailedPrecondition,
			errors.New("legacy LuCI Morse wizard already configured this device (luci.wizard.used=1)"))
	}

	if !s.hasUsableHalowRadio() {
		return connect.NewError(connect.CodeFailedPrecondition,
			errors.New("no usable HaLow radio detected on this device"))
	}

	return nil
}

// hasUsableHalowRadio returns true iff at least one wifi-device has
// `option type 'morse'`. The plan additionally calls for an iwinfo
// liveness check; that lands when the iwinfo wiring is verified end
// to end against a real radio.
func (s *SetupService) hasUsableHalowRadio() bool {
	deviceSections, err := s.UCI.GetSections("wireless", "wifi-device")
	if err != nil {
		return false
	}

	for _, name := range deviceSections {
		typ, ok := s.UCI.Get("wireless", name, "type")
		if !ok || len(typ) == 0 {
			continue
		}

		if strings.EqualFold(typ[0], "morse") {
			return true
		}
	}

	return false
}

// runValidate emits STATUS_STARTED, performs cross-field validation,
// and emits STATUS_DONE on success or STATUS_FAILED on validation
// failure. Returns the validation error so the caller can decide
// whether to roll back.
func (s *SetupService) runValidate(
	ctx context.Context,
	stream applySetupStream,
	profile *setupv1.MeshNodeProfile,
) error {
	if err := emitPhaseStarted(stream, setupv1.ApplySetupResponse_PHASE_VALIDATE,
		"validating profile"); err != nil {
		return err
	}

	err := s.validateProfile(profile)
	if err == nil {
		err = s.validateMeshBackhaul(ctx, profile)
	}

	if err != nil {
		_ = emitPhaseFailed(stream, setupv1.ApplySetupResponse_PHASE_VALIDATE, err.Error())

		return err
	}

	return emitPhaseDone(stream, setupv1.ApplySetupResponse_PHASE_VALIDATE, "profile valid")
}

// validateProfile checks the cross-field invariants that buf.validate
// can't express on its own.
//
//nolint:gocyclo // a flat list of independent checks is more readable than a function per check
func (s *SetupService) validateProfile(profile *setupv1.MeshNodeProfile) error {
	if !hostnameRFC1123.MatchString(profile.GetHostname()) {
		return fmt.Errorf("hostname %q is not a valid RFC 1123 label",
			profile.GetHostname())
	}

	if tz := profile.GetTimezone(); tz != "" && !tzinfo.Known(tz) {
		return fmt.Errorf("unknown timezone %q", tz)
	}

	role := profile.GetRole()
	if role == setupv1.MeshRole_MESH_ROLE_UNSPECIFIED {
		return errors.New("role is required")
	}

	switch role {
	case setupv1.MeshRole_MESH_ROLE_MESH_GATE:
		if profile.GetMeshgateMode() == setupv1.MeshGateMode_MESH_GATE_MODE_UNSPECIFIED {
			return errors.New("meshgate_mode is required when role is MESH_GATE")
		}

		if profile.GetMeshpointMode() != setupv1.MeshPointMode_MESH_POINT_MODE_UNSPECIFIED {
			return errors.New("meshpoint_mode must not be set when role is MESH_GATE")
		}

		if profile.GetUplink() == nil ||
			profile.GetUplink().GetType() == setupv1.UplinkType_UPLINK_TYPE_UNSPECIFIED {
			return errors.New("uplink is required when role is MESH_GATE")
		}
	case setupv1.MeshRole_MESH_ROLE_MESH_POINT:
		if profile.GetMeshpointMode() == setupv1.MeshPointMode_MESH_POINT_MODE_UNSPECIFIED {
			return errors.New("meshpoint_mode is required when role is MESH_POINT")
		}

		// D1 (wizard-parity ledger, 2026-08-27): the address-reservation
		// worker rewrites every non-gateway's ahwlan to a static address
		// on its first tick, so the DHCP-client ahwlan this mode needs
		// cannot survive boot. Rejected until the daemon supports it;
		// the enum value and scenarioMeshPointNone are kept for that day.
		if profile.GetMeshpointMode() == setupv1.MeshPointMode_MESH_POINT_MODE_NONE {
			return errors.New("meshpoint_mode NONE is not supported yet: " +
				"the address-reservation worker overrides a DHCP-client ahwlan on its first tick")
		}

		if profile.GetMeshgateMode() != setupv1.MeshGateMode_MESH_GATE_MODE_UNSPECIFIED {
			return errors.New("meshgate_mode must not be set when role is MESH_POINT")
		}
	}

	mesh := profile.GetMesh()
	if mesh == nil || mesh.GetRadioName() == "" {
		return errors.New("mesh radio configuration is required")
	}

	if !s.radioExistsAsMorse(mesh.GetRadioName()) {
		return fmt.Errorf("mesh radio %q is not a HaLow (type=morse) wifi-device", mesh.GetRadioName())
	}

	if err := validateMeshChannelBandwidth(mesh.GetBandwidthMhz(), mesh.GetChannel()); err != nil {
		return err
	}

	if err := s.validateMeshCountry(mesh); err != nil {
		return err
	}

	if err := validateAPs(profile.GetAps(), mesh.GetRadioName(), s.radioExistsAsMorse); err != nil {
		return err
	}

	if err := validateUplink(profile.GetUplink(), mesh.GetRadioName(), profile.GetAps(), s.radioExistsAsMorse); err != nil {
		return err
	}

	if err := validatePassphrase(mesh.GetEncryption(), mesh.GetPassphrase()); err != nil {
		return fmt.Errorf("mesh: %w", err)
	}

	if len(profile.GetAdminPassword()) < 8 {
		return errors.New("admin_password must be at least 8 characters")
	}

	return nil
}

// validateMeshCountry enforces that the (country_code, bandwidth_mhz,
// channel) triple in the mesh profile appears in the Morse Micro regdb.
// Behavior when the regdb is missing:
//
//   - Empty regdb (loader returns ErrNotInstalled) → accept any
//     country/bandwidth/channel that already passed the
//     validateMeshChannelBandwidth bandwidth-set check. The wizard's
//     baked-in fallback already constrains the user to legal US S1G
//     allocations on the frontend; the handler doesn't re-derive
//     regulatory data when the regdb is absent.
//   - Loader returns any other error → fail validation. A corrupt
//     regdb is a configuration bug worth surfacing.
//
// When the regdb is present, an empty country_code is rejected (the
// frontend defaults to the device's current country, so empty here
// means the user shipped a malformed profile).
func (s *SetupService) validateMeshCountry(mesh *setupv1.MeshRadioConfig) error {
	loader := s.LoadRegDB
	if loader == nil {
		loader = morseregdb.Load
	}

	path := s.RegDBPath
	if path == "" {
		path = morseregdb.DefaultPath
	}

	db, err := loader(path)
	if err != nil {
		if errors.Is(err, morseregdb.ErrNotInstalled) {
			return nil
		}

		return fmt.Errorf("load morse regdb: %w", err)
	}

	country := mesh.GetCountryCode()
	if country == "" {
		return errors.New("mesh.country_code is required when the regulatory database is installed")
	}

	if !db.IsLegalChannel(country, mesh.GetBandwidthMhz(), mesh.GetChannel()) {
		return fmt.Errorf("country %q does not allow channel %d at %d MHz",
			country, mesh.GetChannel(), mesh.GetBandwidthMhz())
	}

	return nil
}

// radioExistsAsMorse confirms the named wifi-device exists and has
// `option type 'morse'`. Phase 1 cross-field guard.
func (s *SetupService) radioExistsAsMorse(radioName string) bool {
	if radioName == "" {
		return false
	}

	return network.IsMorseDevice(s.UCI, radioName)
}

// validateMeshChannelBandwidth applies a minimal sanity check on
// the (channel, bandwidth) pair. Full regulatory validation against
// `WifiConfigService.GetRadioSettings` is deferred to the handler's
// later phase when the radio settings are loaded; the bandwidth must
// at least be one of the legal S1G / VHT values.
func validateMeshChannelBandwidth(bandwidthMHz, channel uint32) error {
	if channel == 0 {
		return errors.New("mesh channel is required")
	}

	switch bandwidthMHz {
	case 1, 2, 4, 8, 20, 40, 80, 160:
		return nil
	default:
		return fmt.Errorf("mesh bandwidth %d MHz is not a supported value", bandwidthMHz)
	}
}

// validateAPs enforces uniqueness of AP radio names, that none equals
// the mesh radio, and that none is a type=morse (HaLow) wifi-device —
// a HaLow radio only ever carries mesh-mode ifaces, and every entry
// here produces an AP section (disabled or not).
func validateAPs(aps []*setupv1.RadioApProfile, meshRadio string, isMorse func(string) bool) error {
	seen := make(map[string]struct{}, len(aps))

	for _, ap := range aps {
		name := ap.GetRadioName()
		if name == "" {
			return errors.New("AP radio_name is required")
		}

		if name == meshRadio {
			return fmt.Errorf("AP radio %q must differ from the mesh radio", name)
		}

		if isMorse(name) {
			return fmt.Errorf("AP radio %q is a HaLow (type=morse) wifi-device, which only carries mesh interfaces", name)
		}

		if _, dup := seen[name]; dup {
			return fmt.Errorf("AP radio %q listed more than once", name)
		}

		seen[name] = struct{}{}

		if !ap.GetEnabled() {
			continue
		}

		if err := validatePassphrase(ap.GetEncryption(), ap.GetPassphrase()); err != nil {
			return fmt.Errorf("AP %q: %w", name, err)
		}
	}

	return nil
}

// validateUplink confirms the wireless STA radio (when uplink type
// is WIRELESS_STA) is not the mesh radio, not a type=morse (HaLow)
// wifi-device — those only carry mesh-mode ifaces — and not also
// configured as an enabled AP: concurrent AP+STA on a single radio is
// not supported by the wizard.
func validateUplink(uplink *setupv1.Uplink, meshRadio string, aps []*setupv1.RadioApProfile, isMorse func(string) bool) error {
	if uplink == nil ||
		uplink.GetType() != setupv1.UplinkType_UPLINK_TYPE_WIRELESS_STA {
		return nil
	}

	wireless := uplink.GetWireless()
	if wireless == nil || wireless.GetRadioName() == "" {
		return errors.New("uplink.wireless.radio_name is required for wireless STA uplink")
	}

	if wireless.GetRadioName() == meshRadio {
		return fmt.Errorf("uplink STA radio %q must differ from the mesh radio", wireless.GetRadioName())
	}

	if isMorse(wireless.GetRadioName()) {
		return fmt.Errorf("uplink STA radio %q is a HaLow (type=morse) wifi-device, which only carries mesh interfaces",
			wireless.GetRadioName())
	}

	for _, ap := range aps {
		if ap.GetRadioName() != wireless.GetRadioName() {
			continue
		}

		if ap.GetEnabled() {
			return fmt.Errorf("uplink STA radio %q is also configured as an enabled AP",
				wireless.GetRadioName())
		}

		if ap.GetMeshBackhaul() != nil {
			return fmt.Errorf("mesh backhaul radio %q is also the wireless uplink radio",
				wireless.GetRadioName())
		}
	}

	if err := validatePassphrase(wireless.GetEncryption(), wireless.GetPassphrase()); err != nil {
		return fmt.Errorf("uplink: %w", err)
	}

	return nil
}

// validateMeshBackhaul enforces the per-radio mesh-backhaul rules: at
// most one backhaul entry, never combined with an enabled AP on the
// same radio, and only on a radio the daemon would accept (2.4 GHz,
// not HaLow, MT7915/MT7916 per iwinfo). The mesh-radio and STA-radio
// clashes are covered by validateAPs and validateUplink.
func (s *SetupService) validateMeshBackhaul(ctx context.Context, profile *setupv1.MeshNodeProfile) error {
	var backhaul *setupv1.RadioApProfile

	for _, ap := range profile.GetAps() {
		if ap.GetMeshBackhaul() == nil {
			continue
		}

		if ap.GetEnabled() {
			return fmt.Errorf("radio %q cannot be both an AP and the mesh backhaul", ap.GetRadioName())
		}

		if backhaul != nil {
			return fmt.Errorf("only one radio may be the mesh backhaul (%q and %q)",
				backhaul.GetRadioName(), ap.GetRadioName())
		}

		backhaul = ap
	}

	if backhaul == nil {
		return nil
	}

	if !s.radioSupportsMeshBackhaul(ctx, backhaul.GetRadioName()) {
		return fmt.Errorf("radio %q does not support mesh backhaul (needs a 2.4 GHz MT7915/MT7916 radio)",
			backhaul.GetRadioName())
	}

	return validateBackhaulRadioTuning(backhaul.GetRadioName(), backhaul.GetMeshBackhaul())
}

// validateBackhaulRadioTuning checks the optional channel/width/country
// on a backhaul entry: both channel and bandwidth are zero (keep the
// daemon defaults) or both are set, the width is a 2.4 GHz width and
// the channel is in the static 2.4 GHz list the settings API uses.
func validateBackhaulRadioTuning(radio string, bh *setupv1.MeshBackhaulProfile) error {
	if (bh.GetChannel() == 0) != (bh.GetBandwidthMhz() == 0) {
		return fmt.Errorf("mesh backhaul on %q: channel and bandwidth_mhz must be set together", radio)
	}

	if bh.GetChannel() == 0 {
		return nil
	}

	if network.SecondaryMeshHTMode(bh.GetBandwidthMhz()) == "" {
		return fmt.Errorf("mesh backhaul on %q: %d MHz is not a 2.4 GHz width (20 or 40)", radio, bh.GetBandwidthMhz())
	}

	if !slices.Contains(availableChannelsForBand(wificonfigv1.WifiBand_WIFI_BAND_2G), strconv.FormatUint(uint64(bh.GetChannel()), 10)) {
		return fmt.Errorf("mesh backhaul on %q: channel %d is not a 2.4 GHz channel (1-11)", radio, bh.GetChannel())
	}

	return nil
}

// radioSupportsMeshBackhaul answers with the same inventory
// GetSetupStatus reports, so the wizard can never be told a radio
// qualifies and then have the apply refuse it (or the reverse).
func (s *SetupService) radioSupportsMeshBackhaul(ctx context.Context, name string) bool {
	for _, radio := range s.collectSetupRadios(ctx) {
		if radio.GetName() == name {
			return radio.GetSupportsMeshBackhaul()
		}
	}

	return false
}

// validatePassphrase enforces WPA's 8..63 char rule unless the
// chosen encryption is open (NONE or OWE).
func validatePassphrase(enc wificonfigv1.WifiEncryption, pass string) error {
	switch enc {
	case wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_NONE,
		wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_OWE:
		return nil
	}

	if l := len(pass); l < 8 || l > 63 {
		return fmt.Errorf("passphrase length %d not in [8,63]", l)
	}

	return nil
}

// emitPhaseStarted/Done/Failed encapsulate the boilerplate for
// emitting per-phase events on the streaming response.
func emitPhaseStarted(stream applySetupStream,
	phase setupv1.ApplySetupResponse_Phase, message string) error {
	return stream.Send(&setupv1.ApplySetupResponse{
		Phase:   phase,
		Status:  setupv1.ApplySetupResponse_STATUS_STARTED,
		Message: message,
	})
}

func emitPhaseDone(stream applySetupStream,
	phase setupv1.ApplySetupResponse_Phase, message string) error {
	return stream.Send(&setupv1.ApplySetupResponse{
		Phase:   phase,
		Status:  setupv1.ApplySetupResponse_STATUS_DONE,
		Message: message,
	})
}

func emitPhaseFailed(stream applySetupStream,
	phase setupv1.ApplySetupResponse_Phase, message string) error {
	return stream.Send(&setupv1.ApplySetupResponse{
		Phase:   phase,
		Status:  setupv1.ApplySetupResponse_STATUS_FAILED,
		Message: message,
	})
}

// emitTerminal sends the final PHASE_TERMINAL event reporting the
// failed phase (when called from a failure path) and returns a
// CodeInternal error wrapping the underlying cause.
func (s *SetupService) emitTerminal(
	stream applySetupStream,
	failedPhase setupv1.ApplySetupResponse_Phase,
	cause error,
	profile *setupv1.MeshNodeProfile,
) error {
	_ = stream.Send(&setupv1.ApplySetupResponse{
		Phase:  setupv1.ApplySetupResponse_PHASE_TERMINAL,
		Status: setupv1.ApplySetupResponse_STATUS_FAILED,
		Result: &setupv1.ApplySetupResult{
			Success:      false,
			FailedPhase:  failedPhase,
			ExpectedSsid: expectedSSID(profile),
			ExpectedUrl:  expectedURL(profile),
		},
	})

	// Surface validation failures as InvalidArgument; everything else
	// becomes Internal.
	if failedPhase == setupv1.ApplySetupResponse_PHASE_VALIDATE {
		return connect.NewError(connect.CodeInvalidArgument, cause)
	}

	return connect.NewError(connect.CodeInternal, cause)
}

// expectedSSID returns the SSID of the highest-priority enabled AP
// in the profile, or "" if no enabled AP is configured.
func expectedSSID(profile *setupv1.MeshNodeProfile) string {
	for _, ap := range profile.GetAps() {
		if ap.GetEnabled() && ap.GetSsid() != "" {
			return ap.GetSsid()
		}
	}

	return ""
}

// expectedURL returns the mDNS URL the device will be reachable at
// after the wizard's hostname write.
func expectedURL(profile *setupv1.MeshNodeProfile) string {
	if profile.GetHostname() == "" {
		return ""
	}

	return "https://" + profile.GetHostname() + ".local:8081/login"
}
