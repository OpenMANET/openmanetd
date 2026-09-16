package network

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/digineo/go-uci/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newFirewallMock returns a fresh, empty firewall mock. Tests populate
// it to mirror the captured `before/firewall` fixture as needed.
func newFirewallMock(t *testing.T) *mockConfigReader {
	t.Helper()

	return &mockConfigReader{
		data:         map[string]map[string]map[string][]string{},
		sectionTypes: map[string]map[string]string{},
		anonSections: map[string][]string{},
	}
}

// addAnonZone adds an anonymous zone with the given name option.
func addAnonZone(t *testing.T, m *mockConfigReader, name string, networks ...string) {
	t.Helper()

	require.NoError(t, m.AddSection("firewall", "", "zone"))
	idx := len(m.anonSections["firewall"]) - 1

	listSections, err := m.GetSections("firewall", "zone")
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(listSections), idx+1)

	section := listSections[len(listSections)-1]
	require.NoError(t, m.SetType("firewall", section, "name", uci.TypeOption, name))

	if len(networks) > 0 {
		require.NoError(t, m.SetType("firewall", section, "network", uci.TypeList, networks...))
	}

	require.NoError(t, m.SetType("firewall", section, "input", uci.TypeOption, "ACCEPT"))
	require.NoError(t, m.SetType("firewall", section, "output", uci.TypeOption, "ACCEPT"))
	require.NoError(t, m.SetType("firewall", section, "forward", uci.TypeOption, "ACCEPT"))
}

// ── AddDefaultWanFirewallRules ───────────────────────────────────────────────

// TestAddDefaultWanFirewallRules_OnRealUCITree_DigineoAnonymousCollision
// exercises the fix against the actual digineo/go-uci library backed
// by a real on-disk firewall file. Without the wizardRuleSectionName
// fix, the second AddSection (the defaults section was already there)
// fails with "type mismatch ... got defaults, want rule".
func TestAddDefaultWanFirewallRules_OnRealUCITree_DigineoAnonymousCollision(t *testing.T) {
	dir := t.TempDir()

	// Write a minimal /etc/config/firewall with an anonymous defaults
	// block, mirroring every real OpenWrt firewall config.
	const firewallContent = `config defaults
	option syn_flood '1'
	option input 'ACCEPT'
	option output 'ACCEPT'
	option forward 'REJECT'
`

	require.NoError(t, os.WriteFile(filepath.Join(dir, "firewall"),
		[]byte(firewallContent), 0o644))

	reader := &UCIFirewallConfigReader{tree: uci.NewTree(dir)}

	require.NoError(t, AddDefaultWanFirewallRules(reader, "ahwlan"),
		"AddDefaultWanFirewallRules must succeed when an anonymous defaults block is present")

	// Confirm all 13 rules made it onto the tree.
	sections, err := reader.GetSections("firewall", "rule")
	require.NoError(t, err)
	assert.Len(t, sections, 13)
}

// TestAddDefaultWanFirewallRules_CoexistsWithAnonymousDefaults is a
// regression for the production bug where the wizard's
// SCENARIO_TOPOLOGY phase failed with
//
//	adding rule "Allow-DHCP-Renew": adding rule section: type
//	mismatch for firewall., got defaults, want rule
//
// Root cause: digineo/go-uci's AddSection treats an empty section
// name as a lookup against existing anonymous sections regardless of
// type — and /etc/config/firewall ships with an anonymous `defaults`
// block, so the very first AddSection("firewall", "", "rule") tried
// to overwrite that and bailed on a type mismatch. Fix: every wizard-
// added rule now uses a derived UCI-safe section name.
func TestAddDefaultWanFirewallRules_CoexistsWithAnonymousDefaults(t *testing.T) {
	m := newFirewallMock(t)

	// Pre-populate the firewall config with an anonymous `defaults`
	// section, exactly like every real OpenWrt firewall config.
	require.NoError(t, m.AddSection("firewall", "", "defaults"))
	require.NoError(t, m.SetType("firewall", "@defaults[0]", "syn_flood",
		uci.TypeOption, "1"))

	// This should succeed despite the anonymous defaults block.
	require.NoError(t, AddDefaultWanFirewallRules(m, "ahwlan"))

	sections, err := m.GetSections("firewall", "rule")
	require.NoError(t, err)
	assert.Len(t, sections, 13)

	// The defaults section must still be intact.
	v, ok := m.Get("firewall", "@defaults[0]", "syn_flood")
	require.True(t, ok)
	assert.Equal(t, "1", v[0])
}

func TestAddDefaultWanFirewallRules_Emits13Rules(t *testing.T) {
	m := newFirewallMock(t)

	require.NoError(t, AddDefaultWanFirewallRules(m, "ahwlan"))

	sections, err := m.GetSections("firewall", "rule")
	require.NoError(t, err)
	assert.Len(t, sections, 13, "should emit exactly 13 rules")
}

func TestAddDefaultWanFirewallRules_RuleNamesMatchFixture(t *testing.T) {
	m := newFirewallMock(t)

	require.NoError(t, AddDefaultWanFirewallRules(m, "ahwlan"))

	wantNames := []string{
		"Allow-DHCP-Renew",
		"Allow-Ping",
		"Allow-IGMP",
		"Allow-DHCPv6",
		"Allow-MLD",
		"Allow-ICMPv6-Input",
		"Allow-ICMPv6-Forward",
		"Allow-IPSec-ESP",
		"Allow-ISAKMP",
		"Allow Batman Mesh TCP 4242",
		"Allow Incoming Comms",
		"Block-DHCP-Request-Out-ahwlan",
		"Block-DHCP-Response-In-ahwlan",
	}

	sections, err := m.GetSections("firewall", "rule")
	require.NoError(t, err)
	require.Len(t, sections, len(wantNames))

	for i, want := range wantNames {
		v, ok := m.Get("firewall", sections[i], "name")
		require.Truef(t, ok, "rule %d missing name", i)
		require.Lenf(t, v, 1, "rule %d has multi-value name", i)
		assert.Equalf(t, want, v[0], "rule %d", i)
	}
}

func TestAddDefaultWanFirewallRules_LocalZoneSubstitutedInBlockRules(t *testing.T) {
	m := newFirewallMock(t)

	require.NoError(t, AddDefaultWanFirewallRules(m, "myzone"))

	sections, err := m.GetSections("firewall", "rule")
	require.NoError(t, err)

	// Block-DHCP-Request-Out is rule 12 (index 11), Block-DHCP-Response-In is 13 (index 12).
	requestRule := sections[11]
	responseRule := sections[12]

	v, ok := m.Get("firewall", requestRule, "name")
	require.True(t, ok)
	assert.Equal(t, "Block-DHCP-Request-Out-myzone", v[0])

	v, ok = m.Get("firewall", requestRule, "src")
	require.True(t, ok)
	assert.Equal(t, "myzone", v[0])

	v, ok = m.Get("firewall", responseRule, "name")
	require.True(t, ok)
	assert.Equal(t, "Block-DHCP-Response-In-myzone", v[0])

	v, ok = m.Get("firewall", responseRule, "dest")
	require.True(t, ok)
	assert.Equal(t, "myzone", v[0])
}

func TestAddDefaultWanFirewallRules_AllowMldHasIcmpTypeList(t *testing.T) {
	m := newFirewallMock(t)

	require.NoError(t, AddDefaultWanFirewallRules(m, "ahwlan"))

	sections, err := m.GetSections("firewall", "rule")
	require.NoError(t, err)

	mldSection := sections[4] // 5th rule = Allow-MLD

	v, ok := m.Get("firewall", mldSection, "icmp_type")
	require.True(t, ok)
	assert.Equal(t, []string{"130/0", "131/0", "132/0", "143/0"}, v)

	v, ok = m.Get("firewall", mldSection, "src_ip")
	require.True(t, ok)
	assert.Equal(t, IPv6LinkLocalCIDR, v[0])
}

func TestAddDefaultWanFirewallRules_AllowPingUsesSingleIcmpTypeOption(t *testing.T) {
	m := newFirewallMock(t)

	require.NoError(t, AddDefaultWanFirewallRules(m, "ahwlan"))

	sections, err := m.GetSections("firewall", "rule")
	require.NoError(t, err)

	pingSection := sections[1]

	// Single-value icmp_type written as option (not list).
	calls := m.setTypeCalls
	found := false

	for _, c := range calls {
		if c.section == pingSection && c.option == "icmp_type" {
			assert.Equal(t, uci.TypeOption, c.typ, "single icmp_type should be option")
			assert.Equal(t, []string{"echo-request"}, c.values)

			found = true
		}
	}

	assert.True(t, found, "Allow-Ping must set icmp_type")
}

func TestAddDefaultWanFirewallRules_AllowIncomingCommsHasMulticastDest(t *testing.T) {
	m := newFirewallMock(t)

	require.NoError(t, AddDefaultWanFirewallRules(m, "ahwlan"))

	sections, err := m.GetSections("firewall", "rule")
	require.NoError(t, err)

	rule := sections[10] // Allow Incoming Comms

	v, ok := m.Get("firewall", rule, "dest_ip")
	require.True(t, ok)
	assert.Equal(t, CommsMulticastGroup, v[0])

	v, ok = m.Get("firewall", rule, "dest_port")
	require.True(t, ok)
	// Pinned as a literal on purpose: the comms talk-group range is
	// 38801-38864 (internal/config/multicast.go) and upstream LuCI
	// writes the same. Comparing against the constant could never fail.
	assert.Equal(t, "38801-38864", v[0])
}

func TestAddDefaultWanFirewallRules_RejectsEmptyLocalZone(t *testing.T) {
	m := newFirewallMock(t)
	err := AddDefaultWanFirewallRules(m, "")
	assert.Error(t, err)
}

// ── RemoveAllRules ───────────────────────────────────────────────────────────

func TestRemoveAllRules_DeletesAllRuleSections(t *testing.T) {
	m := newFirewallMock(t)
	require.NoError(t, AddDefaultWanFirewallRules(m, "ahwlan"))

	sections, err := m.GetSections("firewall", "rule")
	require.NoError(t, err)
	require.Len(t, sections, 13)

	require.NoError(t, RemoveAllRules(m))

	sections, err = m.GetSections("firewall", "rule")
	require.NoError(t, err)
	assert.Empty(t, sections)
}

func TestRemoveAllRules_NoOpOnEmptyConfig(t *testing.T) {
	m := newFirewallMock(t)

	require.NoError(t, RemoveAllRules(m))
}

// ── WhitelistAndDisableForwardings ───────────────────────────────────────────

func TestWhitelistAndDisableForwardings_SetsEnabledZeroEverywhere(t *testing.T) {
	m := newFirewallMock(t)

	// Three forwardings. Two enabled, one already disabled.
	require.NoError(t, m.AddSection("firewall", "", "forwarding"))
	require.NoError(t, m.SetType("firewall", "@forwarding[0]", "src", uci.TypeOption, "lan"))
	require.NoError(t, m.SetType("firewall", "@forwarding[0]", "dest", uci.TypeOption, "wan"))

	require.NoError(t, m.AddSection("firewall", "mmrouter", "forwarding"))
	require.NoError(t, m.SetType("firewall", "mmrouter", "src", uci.TypeOption, "ahwlan"))
	require.NoError(t, m.SetType("firewall", "mmrouter", "dest", uci.TypeOption, "lan"))

	require.NoError(t, m.AddSection("firewall", "", "forwarding"))
	require.NoError(t, m.SetType("firewall", "@forwarding[1]", "src", uci.TypeOption, "lan"))
	require.NoError(t, m.SetType("firewall", "@forwarding[1]", "dest", uci.TypeOption, "ahwlan"))
	require.NoError(t, m.SetType("firewall", "@forwarding[1]", "enabled", uci.TypeOption, "0"))

	require.NoError(t, WhitelistAndDisableForwardings(m))

	for _, s := range []string{"@forwarding[0]", "mmrouter", "@forwarding[1]"} {
		v, ok := m.Get("firewall", s, "enabled")
		require.Truef(t, ok, "%s missing enabled", s)
		assert.Equalf(t, "0", v[0], "%s should be disabled", s)
	}
}

// ── UnsetMtuFixAndMasq ───────────────────────────────────────────────────────

func TestUnsetMtuFixAndMasq_ClearsBothFlags(t *testing.T) {
	m := newFirewallMock(t)
	addAnonZone(t, m, "lan", "lan")
	addAnonZone(t, m, "wan", "wan")

	require.NoError(t, m.SetType("firewall", "@zone[0]", "mtu_fix", uci.TypeOption, "1"))
	require.NoError(t, m.SetType("firewall", "@zone[0]", "masq", uci.TypeOption, "1"))
	require.NoError(t, m.SetType("firewall", "@zone[1]", "mtu_fix", uci.TypeOption, "1"))
	require.NoError(t, m.SetType("firewall", "@zone[1]", "masq", uci.TypeOption, "1"))

	require.NoError(t, UnsetMtuFixAndMasq(m))

	for _, s := range []string{"@zone[0]", "@zone[1]"} {
		_, ok := m.Get("firewall", s, "mtu_fix")
		assert.Falsef(t, ok, "%s mtu_fix should be unset", s)

		_, ok = m.Get("firewall", s, "masq")
		assert.Falsef(t, ok, "%s masq should be unset", s)
	}
}

// ── GetOrCreateZone ──────────────────────────────────────────────────────────

func TestGetOrCreateZone_ReturnsExistingZoneName(t *testing.T) {
	m := newFirewallMock(t)
	addAnonZone(t, m, "lan", "lan")

	got, err := GetOrCreateZone(m, "lan")
	require.NoError(t, err)
	assert.Equal(t, "lan", got)

	// No new zone was created.
	sections, err := m.GetSections("firewall", "zone")
	require.NoError(t, err)
	assert.Len(t, sections, 1)
}

func TestGetOrCreateZone_CreatesNewZoneWhenAbsent(t *testing.T) {
	m := newFirewallMock(t)

	got, err := GetOrCreateZone(m, "ahwlan")
	require.NoError(t, err)
	assert.Equal(t, "ahwlan", got)

	// Zone created with the right defaults.
	v, ok := m.Get("firewall", "ahwlan", "name")
	require.True(t, ok)
	assert.Equal(t, "ahwlan", v[0])

	v, ok = m.Get("firewall", "ahwlan", "input")
	require.True(t, ok)
	assert.Equal(t, "ACCEPT", v[0])

	v, ok = m.Get("firewall", "ahwlan", "output")
	require.True(t, ok)
	assert.Equal(t, "ACCEPT", v[0])

	v, ok = m.Get("firewall", "ahwlan", "forward")
	require.True(t, ok)
	assert.Equal(t, "ACCEPT", v[0])

	v, ok = m.Get("firewall", "ahwlan", "network")
	require.True(t, ok)
	assert.Equal(t, []string{"ahwlan"}, v)
}

func TestGetOrCreateZone_ResolvesNameClash(t *testing.T) {
	m := newFirewallMock(t)
	addAnonZone(t, m, "ahwlan", "other")

	got, err := GetOrCreateZone(m, "ahwlan")
	require.NoError(t, err)
	// Existing zone covers a DIFFERENT network, so a new zone with a
	// suffixed name should be created.
	assert.NotEqual(t, "ahwlan", got)
	assert.True(t, strings.HasPrefix(got, "ahwlan"))
}

func TestGetOrCreateZone_RejectsEmptyNetworkID(t *testing.T) {
	m := newFirewallMock(t)
	_, err := GetOrCreateZone(m, "")
	assert.Error(t, err)
}

// ── GetOrCreateForwarding ────────────────────────────────────────────────────

func TestGetOrCreateForwarding_ReturnsExistingEnabled(t *testing.T) {
	m := newFirewallMock(t)
	require.NoError(t, m.AddSection("firewall", "mmrouter", "forwarding"))
	require.NoError(t, m.SetType("firewall", "mmrouter", "src", uci.TypeOption, "ahwlan"))
	require.NoError(t, m.SetType("firewall", "mmrouter", "dest", uci.TypeOption, "lan"))

	got, err := GetOrCreateForwarding(m, "ahwlan", "lan", "mmrouter", true)
	require.NoError(t, err)
	assert.Equal(t, "mmrouter", got)
}

func TestGetOrCreateForwarding_CreatesNewNamedSection(t *testing.T) {
	m := newFirewallMock(t)
	addAnonZone(t, m, "lan", "lan")

	got, err := GetOrCreateForwarding(m, "ahwlan", "lan", "mmrouter", true)
	require.NoError(t, err)
	assert.Equal(t, "mmrouter", got)

	v, ok := m.Get("firewall", "mmrouter", "src")
	require.True(t, ok)
	assert.Equal(t, "ahwlan", v[0])

	v, ok = m.Get("firewall", "mmrouter", "dest")
	require.True(t, ok)
	assert.Equal(t, "lan", v[0])

	// `lan` zone got mtu_fix/masq because it is the destination and
	// natOnDest was requested.
	v, ok = m.Get("firewall", "@zone[0]", "mtu_fix")
	require.True(t, ok)
	assert.Equal(t, "1", v[0])

	v, ok = m.Get("firewall", "@zone[0]", "masq")
	require.True(t, ok)
	assert.Equal(t, "1", v[0])
}

func TestGetOrCreateForwarding_NoMasqWhenNatOnDestFalse(t *testing.T) {
	m := newFirewallMock(t)
	addAnonZone(t, m, "lan", "lan")

	got, err := GetOrCreateForwarding(m, "ahwlan", "lan", "mmextender", false)
	require.NoError(t, err)
	assert.Equal(t, "mmextender", got)

	// mtu_fix is still always set on the destination zone.
	v, ok := m.Get("firewall", "@zone[0]", "mtu_fix")
	require.True(t, ok)
	assert.Equal(t, "1", v[0])

	// masq is NOT set when natOnDest is false.
	_, ok = m.Get("firewall", "@zone[0]", "masq")
	assert.False(t, ok, "masq must not be set when natOnDest is false")
}

func TestGetOrCreateForwarding_DisablesOtherForwardingsFromSameSrc(t *testing.T) {
	m := newFirewallMock(t)
	addAnonZone(t, m, "lan", "lan")

	// Existing forwarding from ahwlan to wan.
	require.NoError(t, m.AddSection("firewall", "", "forwarding"))
	require.NoError(t, m.SetType("firewall", "@forwarding[0]", "src", uci.TypeOption, "ahwlan"))
	require.NoError(t, m.SetType("firewall", "@forwarding[0]", "dest", uci.TypeOption, "wan"))

	_, err := GetOrCreateForwarding(m, "ahwlan", "lan", "mmrouter", true)
	require.NoError(t, err)

	// Old forwarding is now disabled.
	v, ok := m.Get("firewall", "@forwarding[0]", "enabled")
	require.True(t, ok)
	assert.Equal(t, "0", v[0])
}

func TestGetOrCreateForwarding_ReenablesMatchingDisabledForwarding(t *testing.T) {
	m := newFirewallMock(t)
	addAnonZone(t, m, "lan", "lan")

	// Existing forwarding ahwlan→lan, currently disabled.
	require.NoError(t, m.AddSection("firewall", "", "forwarding"))
	require.NoError(t, m.SetType("firewall", "@forwarding[0]", "src", uci.TypeOption, "ahwlan"))
	require.NoError(t, m.SetType("firewall", "@forwarding[0]", "dest", uci.TypeOption, "lan"))
	require.NoError(t, m.SetType("firewall", "@forwarding[0]", "enabled", uci.TypeOption, "0"))

	got, err := GetOrCreateForwarding(m, "ahwlan", "lan", "mmrouter", true)
	require.NoError(t, err)
	// Re-enabled the existing forwarding instead of creating mmrouter.
	assert.Equal(t, "@forwarding[0]", got)

	// `enabled` is cleared, not set to "1" — this converges to the
	// same shape as a freshly-created forwarding (which never writes
	// `enabled` at all) so a second wizard run is idempotent.
	_, ok := m.Get("firewall", "@forwarding[0]", "enabled")
	assert.False(t, ok, "enabled should be cleared, not explicitly set, on re-enable")

	// mmrouter section was NOT created.
	_, ok = m.Get("firewall", "mmrouter", "src")
	assert.False(t, ok)
}

func TestGetOrCreateForwarding_RejectsEmptyArgs(t *testing.T) {
	m := newFirewallMock(t)
	_, err := GetOrCreateForwarding(m, "", "lan", "x", true)
	assert.Error(t, err)

	_, err = GetOrCreateForwarding(m, "ahwlan", "", "x", true)
	assert.Error(t, err)
}

// ── error propagation ────────────────────────────────────────────────────────

func TestAddDefaultWanFirewallRules_PropagatesSetError(t *testing.T) {
	m := newFirewallMock(t)
	wantErr := errors.New("set failed")
	m.setTypeError = wantErr

	err := AddDefaultWanFirewallRules(m, "ahwlan")
	require.Error(t, err)
	assert.ErrorIs(t, err, wantErr)
}

func TestRemoveAllRules_PropagatesDelError(t *testing.T) {
	m := newFirewallMock(t)
	require.NoError(t, AddDefaultWanFirewallRules(m, "ahwlan"))

	m.delSectionErr = errors.New("delete failed")

	err := RemoveAllRules(m)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "deleting")
}
