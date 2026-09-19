package handlers_test

import (
	"context"
	"math/rand"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/digineo/go-uci/v2"
	setupv1 "github.com/openmanet/openmanetd/internal/api/openmanet/setup/v1"
	wificonfigv1 "github.com/openmanet/openmanetd/internal/api/openmanet/wifi_config/v1"
	"github.com/openmanet/openmanetd/internal/network"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setup_compat_test.go enforces the structural invariants the wizard
// MUST produce for the device to boot in a usable state. These are not
// byte-equivalence tests against captured fixtures (the captures came
// from heterogeneous SKUs and include operator-set fields outside the
// wizard's scope) — they are direct assertions on the staged UCI tree
// after ApplySetup runs.
//
// The bricking incident on 2026-04-28 was caused by the handler
// completing every phase event but never writing several load-bearing
// UCI sections (network.ahwlan interface, br-ahwlan bridge, dhcp pool,
// wifi-iface network bindings, role-aware gw_mode). These tests pin
// down each of those invariants so the same regression cannot recur
// silently.

// uciTree exposes the contents of a fakeConfigReader after ApplySetup
// in a shape convenient for cross-config invariants. The fields are
// populated via runScenarioApply.
type uciTree struct {
	reader *fakeConfigReader
}

func (t *uciTree) hasSection(config, section string) bool {
	if t.reader.sectionTypes[config] == nil {
		return false
	}

	_, ok := t.reader.sectionTypes[config][section]

	return ok
}

func (t *uciTree) sectionsOfType(config, secType string) []string {
	out := []string{}
	if t.reader.sectionTypes[config] == nil {
		return out
	}

	for s, st := range t.reader.sectionTypes[config] {
		if st == secType {
			out = append(out, s)
		}
	}

	return out
}

func (t *uciTree) get(config, section, option string) []string {
	v, _ := t.reader.Get(config, section, option)

	return v
}

func (t *uciTree) getOne(config, section, option string) string {
	v := t.get(config, section, option)
	if len(v) == 0 {
		return ""
	}

	return v[0]
}

// findBridgeDevice returns the section name of the `config device`
// whose `name` field equals bridgeName, or "" if no such bridge exists.
func (t *uciTree) findBridgeDevice(bridgeName string) string {
	for _, s := range t.sectionsOfType("network", "device") {
		if t.getOne("network", s, "name") == bridgeName {
			return s
		}
	}

	return ""
}

// findFirewallZoneByName returns the section name of the `config zone`
// whose `name` field equals zoneName.
func (t *uciTree) findFirewallZoneByName(zoneName string) string {
	for _, s := range t.sectionsOfType("firewall", "zone") {
		if t.getOne("firewall", s, "name") == zoneName {
			return s
		}
	}

	return ""
}

// findFirewallForwarding returns the section name of the first
// `config forwarding` matching (src, dest), or "" if none.
func (t *uciTree) findFirewallForwarding(src, dest string) string {
	for _, s := range t.sectionsOfType("firewall", "forwarding") {
		if t.getOne("firewall", s, "src") == src &&
			t.getOne("firewall", s, "dest") == dest {
			return s
		}
	}

	return ""
}

// findFirewallRuleByName returns the section name of the first
// `config rule` whose `name` equals name, or "" if none.
func (t *uciTree) findFirewallRuleByName(name string) string {
	for _, s := range t.sectionsOfType("firewall", "rule") {
		if t.getOne("firewall", s, "name") == name {
			return s
		}
	}

	return ""
}

// findDhcpPool returns the section name of the first `config dhcp`
// whose `interface` equals iface and which is not marked `ignore=1`.
func (t *uciTree) findDhcpPool(iface string) string {
	for _, s := range t.sectionsOfType("dhcp", "dhcp") {
		if t.getOne("dhcp", s, "interface") != iface {
			continue
		}

		if t.getOne("dhcp", s, "ignore") == "1" {
			continue
		}

		return s
	}

	return ""
}

// runScenarioApply runs ApplySetup with the supplied profile against
// a fresh full-setup reader and returns the staged UCI tree. The test
// asserts NO error from ApplySetup — the wizard must complete cleanly.
func runScenarioApply(t *testing.T, profile *setupv1.MeshNodeProfile) *uciTree {
	t.Helper()

	cfg := setupBLOSTestConfig(t, "setup:\n  enabled: true\n")
	svc, _ := newFullSetupService(t, cfg)

	collector := &streamCollector{}
	require.NoError(t, svc.ApplySetupForTest(context.Background(), profile, collector),
		"ApplySetup must complete cleanly on the happy path")

	reader, ok := svc.UCI.(*fakeConfigReader)
	require.True(t, ok, "fakeConfigReader expected on UCI field")

	return &uciTree{reader: reader}
}

// gateRouterEthProfile returns a fully-valid mesh-gate router-on-eth
// profile, the most common gateway scenario.
func gateRouterEthProfile() *setupv1.MeshNodeProfile {
	prof := minimalProfile()
	prof.Hostname = "test-gate"
	prof.Role = setupv1.MeshRole_MESH_ROLE_MESH_GATE
	prof.DeviceMode = &setupv1.MeshNodeProfile_MeshgateMode{
		MeshgateMode: setupv1.MeshGateMode_MESH_GATE_MODE_ROUTER,
	}
	prof.Uplink = &setupv1.Uplink{
		Type:         setupv1.UplinkType_UPLINK_TYPE_ETHERNET,
		EthernetPort: "eth0",
	}
	prof.Aps = []*setupv1.RadioApProfile{
		{
			RadioName:  "radio0",
			Enabled:    true,
			Ssid:       "test-ap-2g",
			Passphrase: "appassword",
			Encryption: wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_PSK2,
		},
	}

	return prof
}

// pointExtenderProfile returns a fully-valid mesh-point-extender
// profile, the most common non-gateway scenario.
func pointExtenderProfile() *setupv1.MeshNodeProfile {
	prof := minimalProfile()
	prof.Hostname = "test-point"
	prof.Aps = []*setupv1.RadioApProfile{
		{
			RadioName:  "radio0",
			Enabled:    true,
			Ssid:       "test-ap-2g",
			Passphrase: "appassword",
			Encryption: wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_PSK2,
		},
	}

	return prof
}

// ── Universal invariants (every scenario) ─────────────────────────────────────

// TestCompat_BatmanDeviceWritten asserts bat0 is configured with all
// the batman-adv knobs and a role-appropriate gw_mode.
func TestCompat_BatmanDeviceWritten_Gate(t *testing.T) {
	tr := runScenarioApply(t, gateRouterEthProfile())
	assertBatmanDevice(t, tr, "server")
}

func TestCompat_BatmanDeviceWritten_Point(t *testing.T) {
	tr := runScenarioApply(t, pointExtenderProfile())
	assertBatmanDevice(t, tr, "client")
}

func assertBatmanDevice(t *testing.T, tr *uciTree, wantGwMode string) {
	t.Helper()
	require.True(t, tr.hasSection("network", "bat0"),
		"network.bat0 interface must exist after wizard run")

	assert.Equal(t, "batadv", tr.getOne("network", "bat0", "proto"),
		"bat0.proto must be batadv")
	assert.Equal(t, "BATMAN_V", tr.getOne("network", "bat0", "routing_algo"),
		"bat0.routing_algo must be BATMAN_V")
	assert.Equal(t, wantGwMode, tr.getOne("network", "bat0", "gw_mode"),
		"bat0.gw_mode must match role (server for gates, client for points)")
	assert.Equal(t, "1", tr.getOne("network", "bat0", "bridge_loop_avoidance"))
	assert.Equal(t, "30", tr.getOne("network", "bat0", "hop_penalty"))
	assert.Equal(t, "1", tr.getOne("network", "bat0", "fragmentation"))
	assert.Equal(t, "1000", tr.getOne("network", "bat0", "orig_interval"))
}

// TestCompat_MulticastModeMatchesForcefloodConfig pins root cause
// #3 (the wizard once hardcoded multicast_mode=1 while firmware, the
// runtime daemon, and both captures carry 0) and decision D7
// (2026-08-27): the default batman.multicastForceflood=true maps to
// multicast_mode=0 — classic flooding — so multicast RTP reaches
// every node without IGMP/MLD membership announcements crossing the
// mesh. The mapping is network.MulticastModeForForceflood, shared
// with the runtime daemon's configureBatmanForcefloodWithDeps.
func TestCompat_MulticastModeMatchesForcefloodConfig(t *testing.T) {
	tr := runScenarioApply(t, gateRouterEthProfile())

	assert.Equal(t, "0", tr.getOne("network", "bat0", "multicast_mode"),
		"default batman.multicastForceflood=true must write multicast_mode=0 (fixture parity)")
}

// TestCompat_MulticastModeForcefloodDisabledWritesOne asserts that
// when the operator sets batman.multicastForceflood: false in
// config.yml, the wizard writes multicast_mode=1 on bat0 — batman-adv's
// multicast optimisations on — through the same
// network.MulticastModeForForceflood mapping the runtime daemon
// applies.
func TestCompat_MulticastModeForcefloodDisabledWritesOne(t *testing.T) {
	cfg := setupBLOSTestConfig(t, "setup:\n  enabled: true\nbatman:\n  multicastForceflood: false\n")
	svc, _ := newFullSetupService(t, cfg)

	collector := &streamCollector{}
	require.NoError(t, svc.ApplySetupForTest(context.Background(), gateRouterEthProfile(), collector))

	reader, ok := svc.UCI.(*fakeConfigReader)
	require.True(t, ok, "fakeConfigReader expected on UCI field")

	tr := &uciTree{reader: reader}
	assert.Equal(t, "1", tr.getOne("network", "bat0", "multicast_mode"),
		"batman.multicastForceflood=false must write multicast_mode=1")
}

// TestCompat_BatmeshHardifsWritten asserts batmesh0 + batmesh1 exist
// with proto=batadv_hardif master=bat0.
func TestCompat_BatmeshHardifsWritten(t *testing.T) {
	tr := runScenarioApply(t, gateRouterEthProfile())

	for _, iface := range []string{"batmesh0", "batmesh1"} {
		require.Truef(t, tr.hasSection("network", iface),
			"network.%s must exist", iface)
		assert.Equal(t, "batadv_hardif",
			tr.getOne("network", iface, "proto"),
			"%s.proto must be batadv_hardif", iface)
		assert.Equal(t, "bat0",
			tr.getOne("network", iface, "master"),
			"%s.master must be bat0", iface)
	}
}

// TestCompat_AhwlanInterfaceWritten asserts the network.ahwlan
// interface section exists with the LuCI-equivalent options. THIS IS
// THE LOAD-BEARING SECTION — without it, the device has no L3 on the
// management network and is unreachable.
func TestCompat_AhwlanInterfaceWritten_Gate(t *testing.T) {
	tr := runScenarioApply(t, gateRouterEthProfile())
	assertAhwlanInterface(t, tr)
}

func TestCompat_AhwlanInterfaceWritten_Point(t *testing.T) {
	tr := runScenarioApply(t, pointExtenderProfile())
	assertAhwlanInterface(t, tr)
}

func assertAhwlanInterface(t *testing.T, tr *uciTree) {
	t.Helper()
	require.True(t, tr.hasSection("network", "ahwlan"),
		"network.ahwlan interface MUST be created — without it the device has no management IP")

	assert.Equal(t, "static", tr.getOne("network", "ahwlan", "proto"),
		"ahwlan.proto must be static")
	assert.Equal(t, "255.255.0.0", tr.getOne("network", "ahwlan", "netmask"),
		"ahwlan.netmask must be 255.255.0.0")
	assert.Equal(t, "64", tr.getOne("network", "ahwlan", "ip6assign"),
		"ahwlan.ip6assign must be 64")
	assert.Equal(t, "eui64", tr.getOne("network", "ahwlan", "ip6ifaceid"),
		"ahwlan.ip6ifaceid must be eui64 (required for batman/alfred over IPv6)")
	assert.Equal(t, "br-ahwlan", tr.getOne("network", "ahwlan", "device"),
		"ahwlan.device must point at the br-ahwlan bridge")

	ip6class := tr.get("network", "ahwlan", "ip6class")
	assert.Contains(t, ip6class, "local",
		"ahwlan.ip6class must include 'local'")
}

// TestCompat_AhwlanIPaddrSet_Point asserts mesh-point devices get a
// random ipaddr in the mesh subnet. (Mesh-gate scenario does NOT set
// ipaddr — openmanetd handles that at runtime.)
func TestCompat_AhwlanIPaddrSet_Point(t *testing.T) {
	tr := runScenarioApply(t, pointExtenderProfile())

	ip := tr.getOne("network", "ahwlan", "ipaddr")
	require.NotEmpty(t, ip, "mesh-point ahwlan.ipaddr must be set to a random in-subnet address")
	assert.True(t, ipInBootstrapWindow(ip),
		"ahwlan.ipaddr must be the wizard's 10.41.254.x bootstrap address (got %q); the daemon replaces it at reservation", ip)
	assert.NotEqual(t, "10.41.254.1", ip,
		"ahwlan.ipaddr must avoid the factory IP")
}

// TestCompat_BrAhwlanBridgeWritten asserts a `config device` of
// type=bridge name=br-ahwlan exists with bat0 in its ports list and
// an F2:-prefixed MAC. THIS IS THE LOAD-BEARING SECTION — without it,
// ethernet ports are orphaned and ahwlan has no L2 path to anything.
func TestCompat_BrAhwlanBridgeWritten_Gate(t *testing.T) {
	tr := runScenarioApply(t, gateRouterEthProfile())
	assertBrAhwlanBridge(t, tr)
}

func TestCompat_BrAhwlanBridgeWritten_Point(t *testing.T) {
	tr := runScenarioApply(t, pointExtenderProfile())
	assertBrAhwlanBridge(t, tr)
}

func assertBrAhwlanBridge(t *testing.T, tr *uciTree) {
	t.Helper()

	bridge := tr.findBridgeDevice("br-ahwlan")
	require.NotEmpty(t, bridge,
		"a config device with name=br-ahwlan must exist — without it, ethernet ports are orphaned")

	assert.Equal(t, "bridge", tr.getOne("network", bridge, "type"))

	macaddr := tr.getOne("network", bridge, "macaddr")
	assert.True(t, strings.HasPrefix(strings.ToUpper(macaddr), "F2:"),
		"br-ahwlan.macaddr must use the F2 OUI prefix (got %q)", macaddr)

	ports := tr.get("network", bridge, "ports")
	assert.Contains(t, ports, "bat0",
		"br-ahwlan.ports must include bat0 — without it, batman traffic doesn't bridge to ethernet/wifi")
}

// TestCompat_MeshIfaceBoundToBatmesh0 asserts the morse mesh
// wifi-iface has `network=batmesh0`. THIS IS THE LOAD-BEARING OPTION —
// without it, the wifi driver cannot bind the mesh radio to the
// batman-adv hardif and the mesh radio never comes up.
func TestCompat_MeshIfaceBoundToBatmesh0(t *testing.T) {
	tr := runScenarioApply(t, gateRouterEthProfile())

	// The mesh iface name follows the `default_<radio>` convention.
	const meshIface = "default_radio1"
	require.True(t, tr.hasSection("wireless", meshIface),
		"mesh wifi-iface %s must exist", meshIface)

	assert.Equal(t, "mesh", tr.getOne("wireless", meshIface, "mode"))
	assert.Equal(t, "batmesh0", tr.getOne("wireless", meshIface, "network"),
		"mesh wifi-iface MUST have network=batmesh0 — without it, the mesh radio doesn't bind to batman")
}

// TestCompat_MeshEncryptionDefaultsToSAE asserts an UNSPECIFIED mesh
// encryption still writes `encryption=sae` rather than skipping the
// write and inheriting whatever encryption survived the reset phase.
// 802.11s mesh in this system is always SAE.
func TestCompat_MeshEncryptionDefaultsToSAE(t *testing.T) {
	p := gateRouterEthProfile()
	p.Mesh.Encryption = wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_UNSPECIFIED
	tr := runScenarioApply(t, p)

	// Find the mesh iface (mode=mesh) and assert encryption written.
	var mesh string

	for _, s := range tr.sectionsOfType("wireless", "wifi-iface") {
		if tr.getOne("wireless", s, "mode") == "mesh" {
			mesh = s

			break
		}
	}

	require.NotEmpty(t, mesh)
	assert.Equal(t, "sae", tr.getOne("wireless", mesh, "encryption"),
		"UNSPECIFIED must default to sae, not skip the write and inherit pre-reset state")
}

// TestCompat_APBoundToAhwlan asserts AP wifi-ifaces get
// `network=ahwlan` (the gate scenario binding). Mesh-point-extender
// has its own binding tested separately.
func TestCompat_APBoundToAhwlan_Gate(t *testing.T) {
	tr := runScenarioApply(t, gateRouterEthProfile())

	// radio0 is the AP in our profile. The iface name is
	// default_radio0.
	const apIface = "default_radio0"
	require.True(t, tr.hasSection("wireless", apIface),
		"AP wifi-iface %s must exist", apIface)

	assert.Equal(t, "ap", tr.getOne("wireless", apIface, "mode"))
	assert.Equal(t, "ahwlan", tr.getOne("wireless", apIface, "network"),
		"AP wifi-iface MUST have network=ahwlan in gate scenarios — without it, the AP isn't bridged to the management network")
}

// TestCompat_DisabledAPHasDisabledOption asserts disabled APs in the
// profile produce a wifi-iface with `disabled=1` rather than being
// silently skipped (which leaves any pre-existing iface in place).
func TestCompat_DisabledAPHasDisabledOption(t *testing.T) {
	prof := pointExtenderProfile()
	prof.Aps = []*setupv1.RadioApProfile{
		{
			RadioName:  "radio0",
			Enabled:    false, // operator chose to disable
			Encryption: wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_NONE,
		},
	}

	tr := runScenarioApply(t, prof)

	const apIface = "default_radio0"
	require.True(t, tr.hasSection("wireless", apIface),
		"AP iface section must exist (created by reset phase)")

	assert.Equal(t, "1", tr.getOne("wireless", apIface, "disabled"),
		"disabled APs must have disabled=1 written; skipping with `continue` leaves the iface enabled")
}

// TestCompat_AhwlanDhcpPoolCreated asserts a DHCP pool exists for
// ahwlan with the LuCI-shape options. Without this, clients on the
// management network can't get an IP.
func TestCompat_AhwlanDhcpPoolCreated(t *testing.T) {
	tr := runScenarioApply(t, gateRouterEthProfile())

	pool := tr.findDhcpPool("ahwlan")
	require.NotEmpty(t, pool,
		"a dhcp pool for ahwlan must exist — without it, clients can't get IPs")

	assert.Equal(t, "ahwlan", tr.getOne("dhcp", pool, "interface"))
	assert.Equal(t, "16", tr.getOne("dhcp", pool, "limit"))
	assert.NotEmpty(t, tr.getOne("dhcp", pool, "start"),
		"dhcp pool must have a start offset")
	assert.NotEmpty(t, tr.getOne("dhcp", pool, "leasetime"),
		"dhcp pool must have a leasetime")
	assert.Equal(t, "1", tr.getOne("dhcp", pool, "force"),
		"dhcp pool must have force=1 so dnsmasq serves even when an upstream DHCP server is on the LAN")
}

// TestCompat_DnsmasqWhitelisted asserts the wizard does NOT create a
// second, per-network "ahwlan_dns" dnsmasq instance. OpenWrt launches
// one dnsmasq process per `config dnsmasq` section, so a second
// instance races the global (factory) one for port 53 — the loser
// exits and takes its bound pool down with it (root cause #2). The
// ahwlan pool attaches to the surviving global instance instead:
// fixture shape is exactly one dnsmasq and an instance-less pool.
func TestCompat_DnsmasqWhitelisted(t *testing.T) {
	tr := runScenarioApply(t, gateRouterEthProfile())

	assert.False(t, tr.hasSection("dhcp", "ahwlan_dns"),
		"a second per-network dnsmasq instance must not be created — it races the global instance for port 53")

	assert.Len(t, tr.sectionsOfType("dhcp", "dnsmasq"), 1,
		"exactly one dnsmasq section must survive the wizard")

	pool := tr.findDhcpPool("ahwlan")
	require.NotEmpty(t, pool, "ahwlan dhcp pool must exist")
	assert.Empty(t, tr.get("dhcp", pool, "instance"),
		"the ahwlan pool must not bind to a dnsmasq instance")
}

// TestCompat_SingleDnsmasqInstance pins root cause #2: a second
// dnsmasq section races the global one for port 53; the loser exits
// and takes its bound pool down ("flaky after setup"). Both fixtures
// show exactly one dnsmasq and pools with no instance option.
// D1 (wizard-parity ledger, 2026-08-27): "point-none" is not part of
// this sweep — MESH_POINT_MODE_NONE is rejected by validateProfile, so
// pointNoneProfile() can no longer reach runScenarioApply's ApplySetup
// happy path. See TestCompat_MeshPointNone_RejectedAtValidate.
func TestCompat_SingleDnsmasqInstance(t *testing.T) {
	for name, profile := range map[string]*setupv1.MeshNodeProfile{
		"gate-router-eth": gateRouterEthProfile(),
		"point-extender":  pointExtenderProfile(),
	} {
		t.Run(name, func(t *testing.T) {
			tr := runScenarioApply(t, profile)

			assert.Len(t, tr.sectionsOfType("dhcp", "dnsmasq"), 1,
				"exactly one dnsmasq section — a second instance races port 53")

			for _, pool := range tr.sectionsOfType("dhcp", "dhcp") {
				assert.Empty(t, tr.get("dhcp", pool, "instance"),
					"pool %s must not bind to a dnsmasq instance", pool)
			}
		})
	}
}

// TestCompat_AhwlanPoolFixtureParity locks the pool to the capture
// shape. force is asserted on both scenarios (deliberate divergence
// from the extender capture, which omits it — force only makes
// dnsmasq serve when a foreign DHCP server is present). start is
// randomized for points, so assert presence not value there.
func TestCompat_AhwlanPoolFixtureParity(t *testing.T) {
	tr := runScenarioApply(t, gateRouterEthProfile())

	pool := tr.findDhcpPool("ahwlan")
	require.NotEmpty(t, pool)

	secs := loadFixture(t, "mesh-gate-router-eth", "dhcp")
	fx := findFixtureSection(secs, "dhcp", "ahwlan")
	assertTreeMatchesFixtureOwned(t, tr, "mesh-gate-router-eth", "dhcp", pool, fx)
	assert.NotEmpty(t, tr.getOne("dhcp", pool, "start"))

	// Two-directional: the pool must carry nothing the capture lacks
	// (ra/ra_slaac/ra_flags/dns/dns_service/ignore/instance included).
	assertNoExtraOptions(t, tr, "dhcp", pool, fx)
}

// TestCompat_ApplyTwiceIdempotent re-runs the wizard and asserts the
// second pass produces an identical staged tree — pins the pool
// re-enable path (previously only ignore was cleared, dropping the
// pool's options after the reset whitelist stripped them).
func TestCompat_ApplyTwiceIdempotent(t *testing.T) {
	cfg := setupBLOSTestConfig(t, "setup:\n  enabled: true\n")
	svc, _ := newFullSetupService(t, cfg)

	// The service's RNG is reset to the same seed before each apply so
	// both runs draw identical MAC/wifi-key values at the same call
	// positions — otherwise the un-seeded time-based fallback would
	// make the two trees differ for reasons unrelated to what this
	// test pins (whole-tree equality across two applies). This
	// re-seeding does NOT by itself prove the dhcp pool's `start`
	// offset survives a re-run — a deterministic seed would reproduce
	// the same value even if GetOrCreateDhcpPool redrew it on every
	// call. That specific behavior (re-enable keeps an existing
	// `start` rather than redrawing) is pinned at the unit level by
	// TestGetOrCreateDhcpPool_ReenableBackfillsForce in
	// internal/network/uci_dhcp_test.go; this test's job is only to
	// confirm nothing else in the full apply pipeline perturbs the
	// tree on a second run once the per-call values are held fixed.
	svc.RNG = rand.New(rand.NewSource(1))

	collector := &streamCollector{}
	require.NoError(t, svc.ApplySetupForTest(context.Background(), gateRouterEthProfile(), collector))

	reader, ok := svc.UCI.(*fakeConfigReader)
	require.True(t, ok)

	first := deepCopyReaderData(reader)

	// Re-arm the wizard the way `openmanetd setup-reset` does:
	// completion flipped setup.complete/auth.enable AND wrote
	// luci.wizard.used=1 (writeLuciBookkeeping), and the re-apply
	// guard refuses on either.
	require.NoError(t, cfg.PersistSetupAndAuth(false, false))
	require.NoError(t, reader.SetType("luci", "wizard", "used", uci.TypeOption, "0"))

	svc.RNG = rand.New(rand.NewSource(1))
	require.NoError(t, svc.ApplySetupForTest(context.Background(), gateRouterEthProfile(), &streamCollector{}))

	assert.Equal(t, first, deepCopyReaderData(reader),
		"second apply must produce an identical UCI tree")
}

// TestCompat_AhwlanFirewallZone asserts the firewall zone for ahwlan
// exists with mtu_fix=1 and ACCEPT policy.
func TestCompat_AhwlanFirewallZone_Gate(t *testing.T) {
	tr := runScenarioApply(t, gateRouterEthProfile())
	assertAhwlanFirewallZone(t, tr)
}

func TestCompat_AhwlanFirewallZone_Point(t *testing.T) {
	tr := runScenarioApply(t, pointExtenderProfile())
	assertAhwlanFirewallZone(t, tr)
}

func assertAhwlanFirewallZone(t *testing.T, tr *uciTree) {
	t.Helper()

	zone := tr.findFirewallZoneByName("ahwlan")
	require.NotEmpty(t, zone, "firewall.ahwlan zone must exist")

	assert.Equal(t, "ACCEPT", tr.getOne("firewall", zone, "input"))
	assert.Equal(t, "ACCEPT", tr.getOne("firewall", zone, "output"))
	assert.Equal(t, "ACCEPT", tr.getOne("firewall", zone, "forward"))
	assert.Equal(t, "1", tr.getOne("firewall", zone, "mtu_fix"),
		"local zones (ahwlan) must have mtu_fix=1 so VPN-style MSS clamping works correctly")

	networks := tr.get("firewall", zone, "network")
	assert.Contains(t, networks, "ahwlan",
		"ahwlan zone must list the ahwlan network")
}

// TestCompat_MmrouterForwardingForGate asserts the mmrouter forwarding
// rule (ahwlan→lan) exists for gate scenarios.
func TestCompat_MmrouterForwardingForGate(t *testing.T) {
	tr := runScenarioApply(t, gateRouterEthProfile())

	fwd := tr.findFirewallForwarding("ahwlan", "lan")
	require.NotEmpty(t, fwd,
		"forwarding ahwlan→lan must exist on a router-mode mesh gate (the mmrouter forward)")
}

// TestCompat_MmextenderForwardingForExtender asserts the mmextender
// forwarding rule (ahwlan→lan) exists for mesh-point-extender. The
// capture shows the extender forwarding traffic from the mesh into
// its local clients, not the reverse.
func TestCompat_MmextenderForwardingForExtender(t *testing.T) {
	tr := runScenarioApply(t, pointExtenderProfile())

	fwd := tr.findFirewallForwarding("ahwlan", "lan")
	require.NotEmpty(t, fwd,
		"forwarding ahwlan→lan must exist on a mesh-point-extender (the mmextender forward)")
}

// TestCompat_ExtenderForwardingMatchesFixture pins root cause #4:
// the capture has a single ahwlan→lan forwarding and no masq on any
// zone — the old lan→ahwlan direction blocked mesh peers from
// reaching the extender's clients, and the masq side effect broke
// end-to-end mesh addressing.
func TestCompat_ExtenderForwardingMatchesFixture(t *testing.T) {
	tr := runScenarioApply(t, pointExtenderProfile())

	require.NotEmpty(t, tr.findFirewallForwarding("ahwlan", "lan"),
		"fixture direction is ahwlan→lan")
	assert.Empty(t, tr.findFirewallForwarding("lan", "ahwlan"),
		"reverse forwarding must not exist")

	ahwlanZone := tr.findFirewallZoneByName("ahwlan")
	require.NotEmpty(t, ahwlanZone)
	assert.Empty(t, tr.get("firewall", ahwlanZone, "masq"),
		"no masq on the mesh zone in any scenario")

	lanZone := tr.findFirewallZoneByName("lan")
	require.NotEmpty(t, lanZone)
	assert.Empty(t, tr.get("firewall", lanZone, "masq"),
		"extender fixture has no masq on lan either")
	assert.Equal(t, "1", tr.getOne("firewall", lanZone, "mtu_fix"))
}

// TestCompat_NoMasqOnAhwlanAnyScenario sweeps every scenario.
//
// D1 (wizard-parity ledger, 2026-08-27): "point-none" is not part of
// this sweep — MESH_POINT_MODE_NONE is rejected by validateProfile, so
// pointNoneProfile() can no longer reach runScenarioApply's ApplySetup
// happy path. See TestCompat_MeshPointNone_RejectedAtValidate.
func TestCompat_NoMasqOnAhwlanAnyScenario(t *testing.T) {
	for name, profile := range map[string]*setupv1.MeshNodeProfile{
		"gate-router-eth":      gateRouterEthProfile(),
		"gate-router-firewall": gateRouterFirewallEthProfile(),
		"point-extender":       pointExtenderProfile(),
	} {
		t.Run(name, func(t *testing.T) {
			tr := runScenarioApply(t, profile)
			zone := tr.findFirewallZoneByName("ahwlan")
			require.NotEmpty(t, zone)
			assert.Empty(t, tr.get("firewall", zone, "masq"))
		})
	}
}

// TestCompat_MmrouterForwardingHasMasqOnLan keeps the gate scenario
// covered: mmrouter still forwards ahwlan→lan with masq=1 on the lan
// zone (the destination NAT the gate needs to reach the upstream).
func TestCompat_MmrouterForwardingHasMasqOnLan(t *testing.T) {
	tr := runScenarioApply(t, gateRouterEthProfile())

	fwd := tr.findFirewallForwarding("ahwlan", "lan")
	require.NotEmpty(t, fwd, "mmrouter forwarding ahwlan→lan must exist for gate-router-eth")

	lanZone := tr.findFirewallZoneByName("lan")
	require.NotEmpty(t, lanZone)
	assert.Equal(t, "1", tr.getOne("firewall", lanZone, "masq"),
		"gate-router-eth fixture has masq=1 on the lan zone")
	assert.Equal(t, "1", tr.getOne("firewall", lanZone, "mtu_fix"))
}

// TestCompat_LanProtoOnGateWithEthUplink asserts network.lan.proto is
// flipped to dhcp on a router-with-ethernet-uplink gate.
func TestCompat_LanProtoOnGateWithEthUplink(t *testing.T) {
	tr := runScenarioApply(t, gateRouterEthProfile())

	assert.Equal(t, "dhcp", tr.getOne("network", "lan", "proto"),
		"router-with-ethernet uplink: network.lan.proto must be dhcp")
}

// TestCompat_LanDNSAlwaysSet asserts network.lan.dns=1.1.1.1 is set
// unconditionally (LuCI does this in setupBatmanInterfaceOnDevice).
func TestCompat_LanDNSAlwaysSet(t *testing.T) {
	tr := runScenarioApply(t, gateRouterEthProfile())

	assert.Equal(t, "1.1.1.1", tr.getOne("network", "lan", "dns"),
		"network.lan.dns must be set to 1.1.1.1 (mirrors LuCI's setupBatmanInterfaceOnDevice)")
}

// TestCompat_Mesh11sdGateAnnouncements asserts mesh11sd reflects the
// role choice (1 for gates, 0 for points).
func TestCompat_Mesh11sdGateAnnouncements_Gate(t *testing.T) {
	tr := runScenarioApply(t, gateRouterEthProfile())
	assert.Equal(t, "1",
		tr.getOne("mesh11sd", "mesh_params", "mesh_gate_announcements"),
		"mesh gate must announce itself")
}

func TestCompat_Mesh11sdGateAnnouncements_Point(t *testing.T) {
	tr := runScenarioApply(t, pointExtenderProfile())
	assert.Equal(t, "0",
		tr.getOne("mesh11sd", "mesh_params", "mesh_gate_announcements"),
		"mesh point must NOT announce itself as a gate")
}

// TestCompat_Mesh11sdFwdingDisabled asserts mesh_fwding=0 (batman-adv
// owns forwarding when batman is in play, which it always is here).
func TestCompat_Mesh11sdFwdingDisabled(t *testing.T) {
	tr := runScenarioApply(t, gateRouterEthProfile())
	assert.Equal(t, "0",
		tr.getOne("mesh11sd", "mesh_params", "mesh_fwding"),
		"mesh11sd.mesh_fwding must be 0 — batman-adv handles forwarding")
}

// TestCompat_Mesh11sdNolearnEnabled asserts mesh_nolearn=1 on every
// role: batman-adv owns path discovery, so the 802.11s driver must not
// learn mesh paths from received frames.
func TestCompat_Mesh11sdNolearnEnabled(t *testing.T) {
	for name, profile := range map[string]*setupv1.MeshNodeProfile{
		"gate":  gateRouterEthProfile(),
		"point": pointExtenderProfile(),
	} {
		t.Run(name, func(t *testing.T) {
			tr := runScenarioApply(t, profile)
			assert.Equal(t, "1",
				tr.getOne("mesh11sd", "mesh_params", "mesh_nolearn"),
				"mesh11sd.mesh_nolearn must be 1 — batman-adv handles path discovery")
		})
	}
}

// TestCompat_HostnameWritten asserts the wizard invokes the
// hostname setter with the profile's hostname. The fake setter records
// calls without writing to UCI, so we assert against its call log
// rather than re-reading the UCI tree.
func TestCompat_HostnameWritten(t *testing.T) {
	cfg := setupBLOSTestConfig(t, "setup:\n  enabled: true\n")
	svc, deps := newFullSetupService(t, cfg)

	prof := gateRouterEthProfile()

	require.NoError(t, svc.ApplySetupForTest(context.Background(), prof, &streamCollector{}))

	assert.Equal(t, []string{"test-gate"}, deps.Host.calls,
		"the hostname setter must be invoked once with the profile's hostname")
}

// TestCompat_TimezoneStagedFromProfile asserts the wizard writes both
// system.zonename (IANA) and system.timezone (POSIX TZ) matching the
// captured fixture when the profile requests a timezone.
func TestCompat_TimezoneStagedFromProfile(t *testing.T) {
	p := gateRouterEthProfile()
	p.Timezone = "America/Denver"
	tr := runScenarioApply(t, p)

	secs := loadFixture(t, "mesh-gate-router-eth", "system")
	fx := findFixtureSection(secs, "system", "")
	require.NotNil(t, fx)

	sys := tr.sectionsOfType("system", "system")
	require.NotEmpty(t, sys)
	assert.Equal(t, fx.Options["zonename"], tr.get("system", sys[0], "zonename"))
	assert.Equal(t, fx.Options["timezone"], tr.get("system", sys[0], "timezone"))
}

// TestCompat_DefaultWanFirewallRulesPresent asserts the 13 wizard-
// installed firewall rules exist.
func TestCompat_DefaultWanFirewallRulesPresent(t *testing.T) {
	tr := runScenarioApply(t, gateRouterEthProfile())

	rules := tr.sectionsOfType("firewall", "rule")
	assert.GreaterOrEqual(t, len(rules), 13,
		"expected at least 13 default firewall rules added by the wizard")

	// Spot-check a few key rules by their option signatures.
	wantNames := map[string]bool{
		"Allow-DHCP-Renew":              false,
		"Allow Batman Mesh TCP 4242":    false,
		"Block-DHCP-Request-Out-ahwlan": false,
		"Block-DHCP-Response-In-ahwlan": false,
	}

	for _, r := range rules {
		name := tr.getOne("firewall", r, "name")
		if _, ok := wantNames[name]; ok {
			wantNames[name] = true
		}
	}

	for name, found := range wantNames {
		assert.Truef(t, found, "expected wizard to write firewall rule %q", name)
	}
}

// TestCompat_CommsFirewallRuleMatchesFixture pins the whole "Allow
// Incoming Comms" rule against both captures. The port range is the
// talk-group range 38801-38864 (internal/config/multicast.go); the
// constant previously opened 33801-38864, 5000 ports wider than any
// consumer.
func TestCompat_CommsFirewallRuleMatchesFixture(t *testing.T) {
	for scenario, profile := range map[string]*setupv1.MeshNodeProfile{
		"mesh-gate-router-eth": gateRouterEthProfile(),
		"mesh-point-extender":  pointExtenderProfile(),
	} {
		t.Run(scenario, func(t *testing.T) {
			tr := runScenarioApply(t, profile)

			rule := tr.findFirewallRuleByName("Allow Incoming Comms")
			require.NotEmpty(t, rule, "wizard must write the comms rule")

			secs := loadFixture(t, scenario, "firewall")
			fx := findFixtureSectionByOption(secs, "rule", "name", "Allow Incoming Comms")
			require.NotNil(t, fx)

			assertTreeMatchesFixture(t, tr, "firewall", rule, fx)
			assertNoExtraOptions(t, tr, "firewall", rule, fx)
			assert.Equal(t, "38801-38864", tr.getOne("firewall", rule, "dest_port"))
		})
	}
}

// TestCompat_WizardBookkeepingSectionType pins that the wizard's
// network.wizard bookkeeping section is `config wizard 'wizard'`, the
// type LuCI writes (meshwizard.js uci.add('network','wizard','wizard'))
// and the type detectAlreadyConfigured reads. netifd instantiates only
// type=interface sections as interfaces, so writing it as `interface`
// produced a bogus, proto-less interface named wizard on every device.
func TestCompat_WizardBookkeepingSectionType(t *testing.T) {
	tr := runScenarioApply(t, gateRouterEthProfile())

	assert.Contains(t, tr.sectionsOfType("network", "wizard"), "wizard",
		"network.wizard must be a `wizard`-type section")
	assert.NotContains(t, tr.sectionsOfType("network", "interface"), "wizard",
		"network.wizard must not be an interface section (netifd would bring it up)")
	assert.Equal(t, "router", tr.getOne("network", "wizard", "device_mode_meshgate"))
	assert.Equal(t, "ethernet", tr.getOne("network", "wizard", "uplink"))
}

// TestCompat_ICMPv6BritishSpelling: fw4's type table and both
// captures use neighbour-*; an unknown name can invalidate the whole
// rule, breaking NDP toward the uplink zone.
func TestCompat_ICMPv6BritishSpelling(t *testing.T) {
	tr := runScenarioApply(t, gateRouterEthProfile())

	secs := loadFixture(t, "mesh-gate-router-eth", "firewall")
	fx := findFixtureSectionByOption(secs, "rule", "name", "Allow-ICMPv6-Input")
	require.NotNil(t, fx)

	// Locate the staged rule by its name option.
	var ruleSection string

	for _, s := range tr.sectionsOfType("firewall", "rule") {
		if tr.getOne("firewall", s, "name") == "Allow-ICMPv6-Input" {
			ruleSection = s

			break
		}
	}

	require.NotEmpty(t, ruleSection)

	assert.Equal(t, fx.Options["icmp_type"], tr.get("firewall", ruleSection, "icmp_type"),
		"icmp_type list must match the capture exactly, including neighbour- spelling and order")
}

// ── LuCI Morse parity invariants (post-2026-04-28 review) ────────────────────
//
// These tests pin down the five behavioral gaps identified in the
// detailed Morse-vs-Go comparison. They lock in the LuCI semantics
// the new wizard must reproduce so the device boots into a
// functionally equivalent end-state.

// pointNoneProfile returns a fully-valid mesh-point-none profile
// (headless mesh node — joins mesh, no extender role).
func pointNoneProfile() *setupv1.MeshNodeProfile {
	prof := minimalProfile()
	prof.Hostname = "test-point-none"
	prof.DeviceMode = &setupv1.MeshNodeProfile_MeshpointMode{
		MeshpointMode: setupv1.MeshPointMode_MESH_POINT_MODE_NONE,
	}
	prof.Aps = []*setupv1.RadioApProfile{}

	return prof
}

// gateRouterFirewallEthProfile returns a fully-valid mesh-gate
// router_firewall + ethernet uplink profile (the wan-zone scenario).
func gateRouterFirewallEthProfile() *setupv1.MeshNodeProfile {
	prof := minimalProfile()
	prof.Hostname = "test-gate-fw"
	prof.Role = setupv1.MeshRole_MESH_ROLE_MESH_GATE
	prof.DeviceMode = &setupv1.MeshNodeProfile_MeshgateMode{
		MeshgateMode: setupv1.MeshGateMode_MESH_GATE_MODE_ROUTER_FIREWALL,
	}
	prof.Uplink = &setupv1.Uplink{
		Type:         setupv1.UplinkType_UPLINK_TYPE_ETHERNET,
		EthernetPort: "eth0",
	}

	return prof
}

// D1 (wizard-parity ledger, 2026-08-27): MESH_POINT_MODE_NONE is
// rejected at validation until openmanetd's address-reservation worker
// can leave a DHCP-client ahwlan alone. Today the worker rewrites every
// non-gateway's ahwlan to a static address on its first tick
// (internal/mgmt/address_reservation.go), so the scenario's proto=dhcp
// never survives boot. The enum value stays (removing it is a wire
// break) and scenarioMeshPointNone stays as the write path to re-enable
// once the daemon supports it.
func TestCompat_MeshPointNone_RejectedAtValidate(t *testing.T) {
	cfg := setupBLOSTestConfig(t, "setup:\n  enabled: true\n")
	svc, deps := newFullSetupService(t, cfg)
	collector := &streamCollector{}

	err := svc.ApplySetupForTest(context.Background(), pointNoneProfile(), collector)
	require.Error(t, err)

	var connectErr *connect.Error
	require.ErrorAs(t, err, &connectErr)
	assert.Equal(t, connect.CodeInvalidArgument, connectErr.Code())
	assert.Contains(t, connectErr.Message(), "meshpoint_mode NONE")

	require.NotEmpty(t, collector.sent)
	terminal := collector.sent[len(collector.sent)-1]
	assert.Equal(t, setupv1.ApplySetupResponse_PHASE_TERMINAL, terminal.GetPhase())
	assert.Equal(t, setupv1.ApplySetupResponse_PHASE_VALIDATE, terminal.GetResult().GetFailedPhase())

	// Validation rejects before phase 2: no snapshot, no staged writes.
	assert.Equal(t, 0, deps.Snap.snapshotCalls, "no snapshot when validation rejects")

	reader, ok := svc.UCI.(*fakeConfigReader)
	require.True(t, ok, "fakeConfigReader expected on UCI field")

	tr := &uciTree{reader: reader}
	assert.False(t, tr.hasSection("network", "ahwlan"), "no phase may run after a validation reject")
	assert.False(t, tr.hasSection("network", "wizard"), "no bookkeeping after a validation reject")
}

// Gap 2: router_firewall scenario must add wan6 to the wan firewall
// zone's network list. Without this, IPv6 packets on wan6 hit the
// implicit-REJECT default zone and get dropped.
func TestCompat_RouterFirewallEth_Wan6InWanZone(t *testing.T) {
	tr := runScenarioApply(t, gateRouterFirewallEthProfile())

	wanZone := tr.findFirewallZoneByName("wan")
	require.NotEmpty(t, wanZone, "wan firewall zone must exist")

	networks := tr.get("firewall", wanZone, "network")
	assert.Contains(t, networks, "wan",
		"wan zone must include the wan network")
	assert.Contains(t, networks, "wan6",
		"wan zone must include wan6 — without it IPv6 traffic is dropped by the default-zone REJECT")
}

// Gap 2 (cont.): wan6 interface must be created with proto=dhcpv6 in
// the router_firewall scenario. This is Go-only — LuCI never creates
// wan6 (router_firewall is unreachable on its read-only radio), so no
// capture pins it (ledger V5).
func TestCompat_RouterFirewallEth_Wan6Created(t *testing.T) {
	tr := runScenarioApply(t, gateRouterFirewallEthProfile())

	require.True(t, tr.hasSection("network", "wan6"),
		"router_firewall scenario must create network.wan6")
	assert.Equal(t, "dhcpv6", tr.getOne("network", "wan6", "proto"),
		"wan6.proto must be dhcpv6")
}

// A router/firewall gate must retain wan as its sole active ahwlan forwarding
// destination when setup is applied repeatedly. The legacy LuCI shared Batman
// helper used to append an unrelated ahwlan→lan forwarding after the scenario
// had correctly selected wan.
func TestCompat_RouterFirewallEth_RerunKeepsWanForwarding(t *testing.T) {
	cfg := setupBLOSTestConfig(t, "setup:\n  enabled: true\n")
	svc, _ := newFullSetupService(t, cfg)
	profile := gateRouterFirewallEthProfile()

	require.NoError(t, svc.ApplySetupForTest(context.Background(), profile, &streamCollector{}))
	// The production wizard requires an explicit factory/reset action before a
	// second apply. Re-open only that gate so this test exercises the same UCI
	// tree on the second run rather than creating a fresh fixture.
	require.NoError(t, cfg.PersistSetupAndAuth(false, true))
	// Main also records LuCI wizard ownership; setup-reset clears this gate
	// before allowing another apply against the existing network state.
	require.NoError(t, svc.UCI.SetType("luci", "wizard", "used", uci.TypeOption, "0"))
	require.NoError(t, svc.ApplySetupForTest(context.Background(), profile, &streamCollector{}))

	reader, ok := svc.UCI.(*fakeConfigReader)
	require.True(t, ok, "fakeConfigReader expected on UCI field")

	tr := &uciTree{reader: reader}

	var activeDestinations []string

	for _, section := range tr.sectionsOfType("firewall", "forwarding") {
		if tr.getOne("firewall", section, "src") != "ahwlan" ||
			tr.getOne("firewall", section, "enabled") == "0" {
			continue
		}

		activeDestinations = append(activeDestinations,
			tr.getOne("firewall", section, "dest"))
	}

	assert.Equal(t, []string{"wan"}, activeDestinations,
		"router/firewall reruns must leave wan as the sole active ahwlan forwarding destination")
	assert.Equal(t, "dhcp", tr.getOne("network", "wan", "proto"),
		"router/firewall reruns must retain wan as the DHCP uplink")
}

// D2 (wizard-parity ledger, 2026-08-27): the router_firewall scenario
// keeps the factory wan zone policy — input REJECT / output ACCEPT /
// forward REJECT — and re-applies masq + mtu_fix on it as the
// destination of the mmrouter forwarding. LuCI's setupNetworkIface('wan')
// sets ACCEPT×3 instead (tools_wizard.js:364-368); the divergence is
// deliberate: OpenWrt's default policy plus the 13 explicit wan rules is
// what "router + firewall (untrusted upstream)" promises, and LuCI
// cannot reach this scenario on the shipped radio, so there is no
// capture to match. Flip the three policy values below if the decision
// is ever reversed.
func TestCompat_RouterFirewallEth_WanZonePolicy(t *testing.T) {
	tr := runScenarioApply(t, gateRouterFirewallEthProfile())

	wanZone := tr.findFirewallZoneByName("wan")
	require.NotEmpty(t, wanZone, "wan firewall zone must exist")

	assert.Equal(t, "REJECT", tr.getOne("firewall", wanZone, "input"),
		"factory wan input policy must survive the wizard")
	assert.Equal(t, "ACCEPT", tr.getOne("firewall", wanZone, "output"))
	assert.Equal(t, "REJECT", tr.getOne("firewall", wanZone, "forward"),
		"factory wan forward policy must survive the wizard")
	assert.Equal(t, "1", tr.getOne("firewall", wanZone, "masq"),
		"wan is the NAT destination of the mmrouter forwarding")
	assert.Equal(t, "1", tr.getOne("firewall", wanZone, "mtu_fix"),
		"wan is the destination of the mmrouter forwarding")
}

// When lan is the upstream (plain router), wan carries nothing: the
// factory masq/mtu_fix flags are stripped by the phase-4 reset and not
// re-applied. The LuCI capture agrees
// (testfixtures/setup-wizard/after/mesh-gate-router-eth/firewall:17-23
// has neither flag on wan).
func TestCompat_RouterEth_WanZoneNatFlagsStripped(t *testing.T) {
	tr := runScenarioApply(t, gateRouterEthProfile())

	wanZone := tr.findFirewallZoneByName("wan")
	require.NotEmpty(t, wanZone, "wan firewall zone must exist")

	assert.Empty(t, tr.get("firewall", wanZone, "masq"),
		"masq must be stripped from wan when lan is the upstream")
	assert.Empty(t, tr.get("firewall", wanZone, "mtu_fix"),
		"mtu_fix must be stripped from wan when lan is the upstream")
}

// TestCompat_Extender_WanZoneFactoryPolicyRetained pins D2 for the
// mesh-point extender: like the gate scenarios that bind nothing to
// wan, the wizard leaves the factory wan zone policy alone — input and
// forward stay REJECT (OpenWrt's default). LuCI's setupNetworkIface('wan')
// sets ACCEPT×3 and the extender capture shows that
// (testfixtures/setup-wizard/after/mesh-point-extender/firewall:19-21);
// the divergence is deliberate and harmless — nothing rides the wan
// zone on an extender, so the safe default policy is correct. The
// factory masq/mtu_fix flags are stripped by the phase-4 reset and
// never re-applied.
func TestCompat_Extender_WanZoneFactoryPolicyRetained(t *testing.T) {
	tr := runScenarioApply(t, pointExtenderProfile())

	wanZone := tr.findFirewallZoneByName("wan")
	require.NotEmpty(t, wanZone, "the factory wan zone must survive the wizard")

	assert.Equal(t, "REJECT", tr.getOne("firewall", wanZone, "input"),
		"the wizard must keep the factory REJECT input policy on wan (D2)")
	assert.Equal(t, "ACCEPT", tr.getOne("firewall", wanZone, "output"))
	assert.Equal(t, "REJECT", tr.getOne("firewall", wanZone, "forward"),
		"the wizard must keep the factory REJECT forward policy on wan (D2)")

	assert.Empty(t, tr.get("firewall", wanZone, "masq"),
		"masq is stripped by the phase-4 reset and not re-applied on an extender")
	assert.Empty(t, tr.get("firewall", wanZone, "mtu_fix"),
		"mtu_fix is stripped by the phase-4 reset and not re-applied on an extender")
}

// A type=morse wifi-device only ever carries mesh-mode ifaces. An
// older wizard build emitted a disabled meshap_<radio> AP overlay
// beside the mesh iface (a deliberate divergence from LuCI, which
// creates that section during its wizard and deletes it at save).
// The overlay is gone and the reset phase now removes any AP/STA
// leftover on a HaLow radio, so both LuCI captures and the wizard
// agree: the HaLow radio ends up with exactly one iface, mode=mesh.
func TestCompat_MorseRadioCarriesMeshOnly(t *testing.T) {
	cfg := setupBLOSTestConfig(t, "setup:\n  enabled: true\n")
	svc, _ := newFullSetupService(t, cfg)

	reader, ok := svc.UCI.(*fakeConfigReader)
	require.True(t, ok)

	// Leftovers: the old overlay and a hand-added STA, both on the
	// HaLow radio (radio1 in the fixture).
	for name, mode := range map[string]string{"meshap_radio1": "ap", "sta_radio1": "sta"} {
		require.NoError(t, reader.AddSection("wireless", name, "wifi-iface"))
		require.NoError(t, reader.SetType("wireless", name, "device", uci.TypeOption, "radio1"))
		require.NoError(t, reader.SetType("wireless", name, "mode", uci.TypeOption, mode))
	}

	collector := &streamCollector{}
	require.NoError(t, svc.ApplySetupForTest(context.Background(), gateRouterEthProfile(), collector))

	tr := &uciTree{reader: reader}

	var onMorse []string

	for _, iface := range tr.sectionsOfType("wireless", "wifi-iface") {
		if tr.getOne("wireless", iface, "device") == "radio1" {
			onMorse = append(onMorse, iface)
		}
	}

	require.Equal(t, []string{"default_radio1"}, onMorse,
		"the HaLow radio must carry exactly its mesh iface — no AP overlay, no leftovers")
	assert.Equal(t, "mesh", tr.getOne("wireless", "default_radio1", "mode"))
	assert.False(t, tr.hasSection("wireless", "meshap_radio1"))
	assert.False(t, tr.hasSection("wireless", "sta_radio1"))

	// The mac80211 radio's AP is untouched by the purge.
	assert.Equal(t, "ap", tr.getOne("wireless", "default_radio0", "mode"))
}

// Gap 4: scoped dnsmasq sections from the factory image must be
// removed in the reset phase. LuCI removes any dnsmasq with
// `interface` set or non-loopback `notinterface`. The wizard then
// creates per-network instances in the scenario phase.
func TestCompat_ScopedDnsmasqRemovedInReset(t *testing.T) {
	cfg := setupBLOSTestConfig(t, "setup:\n  enabled: true\n")
	svc, _ := newFullSetupService(t, cfg)

	reader, ok := svc.UCI.(*fakeConfigReader)
	require.True(t, ok)

	// Inject a scoped dnsmasq mimicking what a factory image with a
	// per-LAN dnsmasq might ship.
	require.NoError(t, reader.AddSection("dhcp", "scoped_lan_dns", "dnsmasq"))
	require.NoError(t, reader.SetType("dhcp", "scoped_lan_dns", "interface",
		uci.TypeOption, "lan"))

	// Also inject an "ahwlan_dns" section, mimicking a leftover from a
	// prior firmware's (pre-fix) wizard run that created the second,
	// port-53-racing instance this fix removed.
	require.NoError(t, reader.AddSection("dhcp", "ahwlan_dns", "dnsmasq"))
	require.NoError(t, reader.SetType("dhcp", "ahwlan_dns", "interface",
		uci.TypeOption, "ahwlan"))

	require.NoError(t, svc.ApplySetupForTest(context.Background(),
		gateRouterEthProfile(), &streamCollector{}))

	tr := &uciTree{reader: reader}

	// Both scoped dnsmasq sections must be gone after the wizard
	// runs — the reset phase deletes any dnsmasq with `interface` set
	// (or non-loopback `notinterface`), leaving only the anonymous
	// global instance the ahwlan pool attaches to.
	assert.False(t, tr.hasSection("dhcp", "scoped_lan_dns"),
		"reset phase must remove scoped dnsmasq sections")
	assert.False(t, tr.hasSection("dhcp", "ahwlan_dns"),
		"reset phase must remove a leftover prior-firmware ahwlan_dns section")
	assert.Len(t, tr.sectionsOfType("dhcp", "dnsmasq"), 1,
		"exactly one dnsmasq section must survive the wizard")
}

// Gap 5: mesh-point scenarios must write `network.ahwlan.dns=1.1.1.1`.
// LuCI's setupNetworkWithDnsmasq writes this in the isMeshPoint
// branch alongside the random ipaddr — without it the mesh point
// has no DNS resolver on its mesh address.
func TestCompat_MeshPointAhwlanHasDNS(t *testing.T) {
	tr := runScenarioApply(t, pointExtenderProfile())

	assert.Equal(t, "1.1.1.1", tr.getOne("network", "ahwlan", "dns"),
		"mesh-point ahwlan must have dns=1.1.1.1 (LuCI parity, captured fixture)")
}

// Gap 5 (negative): mesh-gate scenarios must NOT write
// `network.ahwlan.dns` because the gateway's upstream provides DNS.
// LuCI's setupNetworkWithDnsmasq with isMeshPoint=false skips this
// option.
func TestCompat_MeshGateAhwlanNoDNS(t *testing.T) {
	tr := runScenarioApply(t, gateRouterEthProfile())

	assert.Empty(t, tr.get("network", "ahwlan", "dns"),
		"mesh-gate ahwlan must NOT have dns set — the upstream router provides DNS")
}

// TestCompat_RouterFirewallEth_LanProtoUntouched confirms the wan-
// upstream scenario does NOT flip lan.proto. The factory's static lan
// is preserved; only wan/wan6 carry the upstream DHCP work.
func TestCompat_RouterFirewallEth_LanProtoUntouched(t *testing.T) {
	tr := runScenarioApply(t, gateRouterFirewallEthProfile())

	// The factory-fixture lan starts at proto=static; the firewall
	// scenario doesn't promote it to dhcp.
	assert.NotEqual(t, "dhcp", tr.getOne("network", "lan", "proto"),
		"router_firewall scenario must NOT change lan.proto to dhcp (the upstream is wan)")
}

// ── Uplink port binding (P0.1) ────────────────────────────────────────────────
//
// Phase 4's reset strips `device` from every interface
// (UnsetGatewayAndDeviceOnInterfaces) and deletes br-lan; the chosen
// uplink port was previously only used to *exclude* it from
// br-ahwlan — nothing rebound it, so the gate advertised
// gw_mode=server with zero upstream. These tests pin the fix.

// TestCompat_UplinkPortBoundToLan pins root cause #1 of "wizard
// produces broken devices": after reset strips lan.device, the
// router-eth scenario must rebind the chosen uplink port so the gate
// has upstream connectivity. Fixture: lan.device 'eth0'.
func TestCompat_UplinkPortBoundToLan(t *testing.T) {
	tr := runScenarioApply(t, gateRouterEthProfile())

	assert.Equal(t, "eth0", tr.getOne("network", "lan", "device"),
		"uplink port must be bound to lan — without it the gate has no upstream and a single-port board is unreachable by wire")
}

// TestCompat_Wan6CreatedForRouterEth: the gate fixture carries a wan6
// dhcpv6 interface; previously only router-firewall created it.
func TestCompat_Wan6CreatedForRouterEth(t *testing.T) {
	tr := runScenarioApply(t, gateRouterEthProfile())

	assert.True(t, tr.hasSection("network", "wan6"), "wan6 must exist (fixture parity)")
	assert.Equal(t, "dhcpv6", tr.getOne("network", "wan6", "proto"))
}

// TestCompat_UplinkPortBoundToWanForRouterFirewall: router-firewall
// binds the port to wan/wan6 (no LuCI capture exists for this SKU;
// shape hand-derived from the LuCI wizard source — flag for bench
// confirmation).
func TestCompat_UplinkPortBoundToWanForRouterFirewall(t *testing.T) {
	tr := runScenarioApply(t, gateRouterFirewallEthProfile())

	assert.Equal(t, "eth0", tr.getOne("network", "wan", "device"))
	assert.Equal(t, "eth0", tr.getOne("network", "wan6", "device"))
	assert.NotEqual(t, "eth0", tr.getOne("network", "lan", "device"),
		"router-firewall must not also bind the port to lan")
}

// TestCompat_LanFixtureParity_GateRouterEth locks the whole lan
// section to the capture. dns is ignored: fixture lan carries the
// pre-uplink static leftovers (ipaddr/netmask/ip6assign) from the
// capture SKU which SetInterfaceProto does not remove — assert the
// wizard-owned options only.
func TestCompat_LanFixtureParity_GateRouterEth(t *testing.T) {
	tr := runScenarioApply(t, gateRouterEthProfile())

	secs := loadFixture(t, "mesh-gate-router-eth", "network")
	lan := findFixtureSection(secs, "interface", "lan")
	require.NotNil(t, lan)

	assert.Equal(t, lan.Options["device"], tr.get("network", "lan", "device"))
	assert.Equal(t, lan.Options["proto"], tr.get("network", "lan", "proto"))
	assert.Equal(t, lan.Options["dns"], tr.get("network", "lan", "dns"))
}

// TestCompat_UplinkPortFallbackWhenUnset asserts that an unset
// ethernet_port falls back to a resolved port (Uplink.ethernet_port
// proto contract: "Empty falls back to the first ethernet port") and
// that the resolved port is still excluded from br-ahwlan.
func TestCompat_UplinkPortFallbackWhenUnset(t *testing.T) {
	p := gateRouterEthProfile()
	p.Uplink.EthernetPort = ""
	tr := runScenarioApply(t, p)

	bound := tr.getOne("network", "lan", "device")
	assert.NotEmpty(t, bound, "empty ethernet_port must fall back to the first detected port (proto contract)")

	bridge := tr.findBridgeDevice("br-ahwlan")
	require.NotEmpty(t, bridge)
	assert.NotContains(t, tr.get("network", bridge, "ports"), bound,
		"the bound uplink port must not also sit in br-ahwlan")
}

// TestCompat_UmdnsNetworksRegistered: the terminal event promises
// <hostname>.local; without umdns.@umdns[0].network the promise is
// false and a working device looks dead.
func TestCompat_UmdnsNetworksRegistered(t *testing.T) {
	for name, profile := range map[string]*setupv1.MeshNodeProfile{
		"gate-router-eth": gateRouterEthProfile(),
		"point-extender":  pointExtenderProfile(),
	} {
		t.Run(name, func(t *testing.T) {
			tr := runScenarioApply(t, profile)

			secs := tr.sectionsOfType("umdns", "umdns")
			require.Len(t, secs, 1)
			assert.Equal(t, []string{"lan", "ahwlan"}, tr.get("umdns", secs[0], "network"))
		})
	}
}

// TestCompat_OpenmanetdFlagsReset pins the wizard's half of the
// two-stage addressing design: openmanetd.config.dhcpconfigured=0
// tells AddressReservationWorker to claim a mesh address after boot
// (it acts whenever the value is not "1"), and batmesh1configured=0
// lets setupBatMesh1Interface run on MT7915/16 boards. Without the
// reset, re-running the wizard on a device that had reserved before
// keeps the stale flag and the daemon never re-reserves.
func TestCompat_OpenmanetdFlagsReset(t *testing.T) {
	for scenario, profile := range map[string]*setupv1.MeshNodeProfile{
		"gate":  gateRouterEthProfile(),
		"point": pointExtenderProfile(),
	} {
		t.Run(scenario, func(t *testing.T) {
			tr := runScenarioApply(t, profile)

			assert.Equal(t, "0", tr.getOne("openmanetd", "config", "dhcpconfigured"))
			assert.Equal(t, "0", tr.getOne("openmanetd", "config", "batmesh1configured"))
			assert.Contains(t, tr.sectionsOfType("openmanetd", "openmanet"), "config",
				"section must carry the shipped type `openmanet`")
		})
	}
}

// TestCompat_OpenmanetdFlagsOverwriteStaleOne seeds the flags a
// previously-reserved device carries and proves the wizard clears
// them (stage-only; commit happens in phase 12 with everything else).
func TestCompat_OpenmanetdFlagsOverwriteStaleOne(t *testing.T) {
	cfg := setupBLOSTestConfig(t, "setup:\n  enabled: true\n")
	svc, _ := newFullSetupService(t, cfg)

	reader, ok := svc.UCI.(*fakeConfigReader)
	require.True(t, ok)
	require.NoError(t, reader.AddSection("openmanetd", "config", "openmanet"))
	require.NoError(t, reader.SetType("openmanetd", "config", "dhcpconfigured", uci.TypeOption, "1"))
	require.NoError(t, reader.SetType("openmanetd", "config", "batmesh1configured", uci.TypeOption, "1"))

	require.NoError(t, svc.ApplySetupForTest(context.Background(), pointExtenderProfile(), &streamCollector{}))

	tr := &uciTree{reader: reader}
	assert.Equal(t, "0", tr.getOne("openmanetd", "config", "dhcpconfigured"))
	assert.Equal(t, "0", tr.getOne("openmanetd", "config", "batmesh1configured"))
}

// TestCompat_LuciWizardUsedWritten pins that the Go wizard records
// completion the way the LuCI mesh wizard does (luci.wizard.used=1,
// tools/morse/wizard.js save()). LuCI stays installed on shipped
// images; without the flag its landing page keeps steering operators
// into a flow that rewrites country, channel, timezone and password.
func TestCompat_LuciWizardUsedWritten(t *testing.T) {
	tr := runScenarioApply(t, pointExtenderProfile())

	assert.Equal(t, "1", tr.getOne("luci", "wizard", "used"))
	assert.Contains(t, tr.sectionsOfType("luci", "wizard"), "wizard")
}

// TestCompat_LuciHomepageCleared pins the homepage reset: LuCI's
// uci-defaults point luci.main.homepage at admin/morse/landing on
// first boot and the landing page moves it to admin/selectwizard.
// Both must be deleted (LuCI's own save does the same); any other
// value is an operator choice and stays.
func TestCompat_LuciHomepageCleared(t *testing.T) {
	cases := []struct {
		name     string
		seed     string
		wantGone bool
	}{
		{name: "landing", seed: "admin/morse/landing", wantGone: true},
		{name: "selectwizard", seed: "admin/selectwizard", wantGone: true},
		{name: "operator-choice", seed: "admin/status/overview", wantGone: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := setupBLOSTestConfig(t, "setup:\n  enabled: true\n")
			svc, _ := newFullSetupService(t, cfg)

			reader, ok := svc.UCI.(*fakeConfigReader)
			require.True(t, ok)
			require.NoError(t, reader.AddSection("luci", "main", "core"))
			require.NoError(t, reader.SetType("luci", "main", "homepage", uci.TypeOption, tc.seed))

			require.NoError(t, svc.ApplySetupForTest(context.Background(), pointExtenderProfile(), &streamCollector{}))

			tr := &uciTree{reader: reader}
			if tc.wantGone {
				assert.Empty(t, tr.get("luci", "main", "homepage"))
			} else {
				assert.Equal(t, tc.seed, tr.getOne("luci", "main", "homepage"))
			}
		})
	}
}

// TestCompat_LuciAbsentIsFine: minimal images ship without
// /etc/config/luci. The wizard must still complete (go-uci creates the
// config on AddSection; the snapshotter captures the missing file as
// nil and Restore removes it again on rollback).
func TestCompat_LuciAbsentIsFine(t *testing.T) {
	tr := runScenarioApply(t, gateRouterEthProfile())

	assert.Empty(t, tr.get("luci", "main", "homepage"))
	assert.Equal(t, "1", tr.getOne("luci", "wizard", "used"))
}

// ── Mesh backhaul (P4) ────────────────────────────────────────────────────────

func pointExtenderBackhaulProfile() *setupv1.MeshNodeProfile {
	prof := pointExtenderProfile()
	prof.Aps = []*setupv1.RadioApProfile{backhaulEntry("radio0")}

	return prof
}

// TestCompat_MeshBackhaul_WritesMeshLinkOptionSet asserts the wizard
// writes exactly the daemon's option set under the non-colliding
// section, with the operator's own credentials, and moves the radio to
// the secondary link's channel and width.
func TestCompat_MeshBackhaul_WritesMeshLinkOptionSet(t *testing.T) {
	tr := runScenarioApply(t, pointExtenderBackhaulProfile())

	want := network.MeshLink{
		Radio:         "radio0",
		Network:       network.BatmanSecondaryIface,
		MeshID:        "backhaul-2g",
		Key:           "backhaulpass",
		RSSIThreshold: network.SecondaryMeshRSSIThreshold,
	}
	require.True(t, tr.hasSection("wireless", want.Section()), "wifi-iface %s must exist", want.Section())

	got, err := network.GetWirelessIfaceByNameWithReader(want.Section(), tr.reader)
	require.NoError(t, err)
	assert.Equal(t, want.IfaceConfig(), got)
	assert.Empty(t, tr.getOne("wireless", want.Section(), "disabled"), "the link must be enabled")

	assert.Equal(t, network.SecondaryMeshChannel2G, tr.getOne("wireless", "radio0", "channel"))
	assert.Equal(t, network.SecondaryMeshHTMode2G, tr.getOne("wireless", "radio0", "htmode"))
	assert.Empty(t, tr.getOne("wireless", "radio0", "disabled"))
}

// TestCompat_MeshBackhaul_WritesSecondaryMeshPolicy pins the five tuning
// options the daemon owns on the 2.4 GHz link by value, independent of
// the IfaceConfig comparison above, so a constant change is a visible
// diff here.
func TestCompat_MeshBackhaul_WritesSecondaryMeshPolicy(t *testing.T) {
	tr := runScenarioApply(t, pointExtenderBackhaulProfile())

	section := network.MeshLink{Radio: "radio0", Network: network.BatmanSecondaryIface}.Section()

	assert.Equal(t, "24000", tr.getOne("wireless", section, "mcast_rate"))
	assert.Equal(t, "1", tr.getOne("wireless", section, "mesh_nolearn"))
	assert.Equal(t, "255", tr.getOne("wireless", section, "mesh_retry_timeout"))
	assert.Equal(t, "255", tr.getOne("wireless", section, "mesh_confirm_timeout"))
	assert.Equal(t, "255", tr.getOne("wireless", section, "mesh_holding_timeout"))
}

// TestCompat_MeshBackhaul_KeepsAPSectionDisabled asserts default_<radio>
// stays present but off, with no credentials, so the daemon's fallback
// and a later settings edit both see an intact, inert AP section.
func TestCompat_MeshBackhaul_KeepsAPSectionDisabled(t *testing.T) {
	tr := runScenarioApply(t, pointExtenderBackhaulProfile())

	require.True(t, tr.hasSection("wireless", "default_radio0"))
	assert.Equal(t, "ap", tr.getOne("wireless", "default_radio0", "mode"))
	assert.Equal(t, "1", tr.getOne("wireless", "default_radio0", "disabled"))
	assert.Empty(t, tr.getOne("wireless", "default_radio0", "ssid"))
	assert.NotEqual(t, "batmesh1", tr.getOne("wireless", "default_radio0", "network"),
		"the AP section is never converted into the mesh link")
}

// TestCompat_MeshBackhaul_FlagHandoff asserts batmesh1configured is 1
// when the wizard wrote the link (daemon fallback skips) and 0 otherwise
// (daemon fallback may run).
func TestCompat_MeshBackhaul_FlagHandoff(t *testing.T) {
	chosen := runScenarioApply(t, pointExtenderBackhaulProfile())
	assert.Equal(t, "1", chosen.getOne("openmanetd", "config", "batmesh1configured"))
	assert.Equal(t, "0", chosen.getOne("openmanetd", "config", "dhcpconfigured"),
		"the address-reservation flag is unaffected")

	notChosen := runScenarioApply(t, pointExtenderProfile())
	assert.Equal(t, "0", notChosen.getOne("openmanetd", "config", "batmesh1configured"))
	assert.False(t, notChosen.hasSection("wireless", "batmesh1_radio0"),
		"no backhaul chosen: the wizard writes no link section")
}

// TestCompat_MeshBackhaul_OtherRadioAPIntact asserts a backhaul on one
// radio does not disturb an enabled AP on another.
func TestCompat_MeshBackhaul_OtherRadioAPIntact(t *testing.T) {
	prof := pointExtenderBackhaulProfile()
	prof.Aps = append(prof.Aps, &setupv1.RadioApProfile{
		RadioName:  "radio2",
		Enabled:    true,
		Ssid:       "client-5g",
		Passphrase: "appassword",
		Encryption: wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_SAE,
	})

	cfg := setupBLOSTestConfig(t, "setup:\n  enabled: true\n")
	svc, _ := newFullSetupService(t, cfg)

	reader, ok := svc.UCI.(*fakeConfigReader)
	require.True(t, ok)

	reader.data["wireless"]["radio2"] = map[string][]string{"type": {"mac80211"}, "band": {"5g"}, "channel": {"36"}}
	reader.sectionTypes["wireless"]["radio2"] = "wifi-device"

	require.NoError(t, svc.ApplySetupForTest(context.Background(), prof, &streamCollector{}))

	tr := &uciTree{reader: reader}

	assert.Equal(t, "ap", tr.getOne("wireless", "default_radio2", "mode"))
	assert.Equal(t, "client-5g", tr.getOne("wireless", "default_radio2", "ssid"))
	assert.Empty(t, tr.getOne("wireless", "default_radio2", "disabled"))
	assert.Equal(t, "36", tr.getOne("wireless", "radio2", "channel"), "only the backhaul radio changes channel")
	assert.True(t, tr.hasSection("wireless", "batmesh1_radio0"))
}

// TestCompat_MeshBackhaul_WritesOperatorChannelWidthCountry asserts
// non-zero MeshBackhaulProfile radio fields replace the fixed defaults.
func TestCompat_MeshBackhaul_WritesOperatorChannelWidthCountry(t *testing.T) {
	prof := pointExtenderBackhaulProfile()
	prof.Aps[0].MeshBackhaul.BandwidthMhz = 40
	prof.Aps[0].MeshBackhaul.Channel = 6
	prof.Aps[0].MeshBackhaul.CountryCode = "GB"

	tr := runScenarioApply(t, prof)

	assert.Equal(t, "6", tr.getOne("wireless", "radio0", "channel"))
	assert.Equal(t, "HE40", tr.getOne("wireless", "radio0", "htmode"))
	assert.Equal(t, "GB", tr.getOne("wireless", "radio0", "country"))
}

// TestCompat_MeshBackhaul_Explicit20MHzWritesHE20 pins that 20 MHz stays
// selectable as the congested-spectrum fallback now that the default is
// 40 MHz.
func TestCompat_MeshBackhaul_Explicit20MHzWritesHE20(t *testing.T) {
	prof := pointExtenderBackhaulProfile()
	prof.Aps[0].MeshBackhaul.BandwidthMhz = 20
	prof.Aps[0].MeshBackhaul.Channel = 1

	tr := runScenarioApply(t, prof)

	assert.Equal(t, "1", tr.getOne("wireless", "radio0", "channel"))
	assert.Equal(t, "HE20", tr.getOne("wireless", "radio0", "htmode"))
}

// TestCompat_MeshBackhaul_ZeroFieldsKeepDefaults pins that the new
// fields at their zero values reproduce the pre-existing writes and
// leave the radio's country exactly as the reader seeded it.
func TestCompat_MeshBackhaul_ZeroFieldsKeepDefaults(t *testing.T) {
	seeded, _ := newFullSetupReader().Get("wireless", "radio0", "country")

	tr := runScenarioApply(t, pointExtenderBackhaulProfile())

	assert.Equal(t, network.SecondaryMeshChannel2G, tr.getOne("wireless", "radio0", "channel"))
	assert.Equal(t, network.SecondaryMeshHTMode2G, tr.getOne("wireless", "radio0", "htmode"))
	assert.Equal(t, seeded, tr.get("wireless", "radio0", "country"), "the wizard must not touch country unless the profile carries one")
}
