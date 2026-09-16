package handlers_test

// setup_ownership_test.go pins the WIZARD's value for the fixture rows
// the ownership map (fixture_test.go) marks as daemon- or operator-
// owned. Those rows are skipped by fixture parity on purpose; without
// an explicit test nothing would prove the wizard writes the bootstrap
// value the daemon later replaces. Rows whose wizard value is already
// pinned by a dedicated compat test (bat0.multicast_mode by
// TestCompat_MulticastMode*, ahwlan.ipaddr/dns by the point/gate
// address tests) are covered there rather than duplicated here; the
// tests below cover the rest.

import (
	"net"
	"strconv"
	"strings"
	"testing"

	setupv1 "github.com/openmanet/openmanetd/internal/api/openmanet/setup/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ownershipScenarios returns the two profiles that have captured
// fixtures, keyed by the after/<scenario> directory name.
func ownershipScenarios() map[string]*setupv1.MeshNodeProfile {
	return map[string]*setupv1.MeshNodeProfile{
		"mesh-gate-router-eth": gateRouterEthProfile(),
		"mesh-point-extender":  pointExtenderProfile(),
	}
}

// TestOwnership_AhwlanPoolStartIsWizardOffset pins the wizard-side
// pool start (LuCI's 255 + 16k formula, RandomDhcpStart). The daemon
// rewrites start at reservation time, so fixture parity ignores it.
func TestOwnership_AhwlanPoolStartIsWizardOffset(t *testing.T) {
	for scenario, profile := range ownershipScenarios() {
		t.Run(scenario, func(t *testing.T) {
			tr := runScenarioApply(t, profile)

			pool := tr.findDhcpPool("ahwlan")
			require.NotEmpty(t, pool)

			raw := tr.getOne("dhcp", pool, "start")
			start, err := strconv.Atoi(raw)
			require.NoError(t, err, "dhcp.ahwlan.start %q must be an integer", raw)

			assert.GreaterOrEqual(t, start, 255)
			assert.LessOrEqual(t, start, 255+16*14)
			assert.Zero(t, (start-255)%16, "start %d must be 255 + 16k", start)
		})
	}
}

// TestOwnership_PointKeepsLanUntilReservation pins that the wizard
// still writes network.lan and dhcp.lan on mesh points (LuCI parity).
// The daemon deletes both ~125 s after boot, which is why the extender
// fixture lacks them (daemonRemovedSections).
func TestOwnership_PointKeepsLanUntilReservation(t *testing.T) {
	tr := runScenarioApply(t, pointExtenderProfile())

	for _, row := range daemonRemovedSections() {
		assert.Truef(t, tr.hasSection(row.Config, row.SectionName),
			"wizard must write %s.%s on points; the daemon removes it later", row.Config, row.SectionName)
	}
}

// TestOwnership_GateAhwlanHasNoIPaddr pins the gate side of the
// two-stage addressing design: the wizard leaves ahwlan.ipaddr unset
// on gates and the daemon claims the first free 10.41.0.x.
func TestOwnership_GateAhwlanHasNoIPaddr(t *testing.T) {
	tr := runScenarioApply(t, gateRouterEthProfile())

	assert.Empty(t, tr.get("network", "ahwlan", "ipaddr"),
		"gate ahwlan.ipaddr is daemon-owned and must not be staged by the wizard")
}

// TestOwnership_PointExtenderDefaultRadio0IsAP pins the wizard side of
// the default_radio0 ownership row. On MT7915/MT7916 boards the daemon
// converts default_radio0 into the secondary mesh link on first boot,
// which is why the extender fixture shows it as mode=mesh network=batmesh1
// and fixture parity skips it. The wizard itself must leave it a plain
// AP bridged onto ahwlan — never pre-convert it — so an operator who
// enabled the 2.4 GHz AP keeps it until (and unless) the daemon claims
// the radio.
func TestOwnership_PointExtenderDefaultRadio0IsAP(t *testing.T) {
	tr := runScenarioApply(t, pointExtenderProfile())

	const section = "default_radio0"

	require.True(t, tr.hasSection("wireless", section),
		"the wizard writes radio0's AP as default_radio0")

	assert.Equal(t, "ap", tr.getOne("wireless", section, "mode"),
		"the wizard leaves default_radio0 an AP; the batmesh1 conversion is daemon-owned")
	assert.Equal(t, "ahwlan", tr.getOne("wireless", section, "network"),
		"the AP is bridged onto ahwlan, not the batmesh1 hardif")
	assert.Equal(t, "test-ap-2g", tr.getOne("wireless", section, "ssid"),
		"the operator's AP SSID must survive the wizard")
	assert.Empty(t, tr.get("wireless", section, "disabled"),
		"an enabled AP must not be left disabled")
}

// TestOwnership_MapRowsExistInFixtures guards the ownership map
// against drift: every per-option row must name a section and option
// that the fixture actually carries, otherwise the row is dead.
func TestOwnership_MapRowsExistInFixtures(t *testing.T) {
	for _, row := range ownedRows() {
		scenarios := []string{"mesh-gate-router-eth", "mesh-point-extender"}
		if row.Scenario != "" {
			scenarios = []string{row.Scenario}
		}

		for _, scenario := range scenarios {
			secs := loadFixture(t, scenario, row.Config)
			fx := findFixtureSection(secs, row.SectionType, row.SectionName)
			require.NotNilf(t, fx, "ownership row %s %s %q missing from after/%s", row.Config, row.SectionType, row.SectionName, scenario)

			if row.Option == "" {
				continue
			}

			_, ok := fx.Options[row.Option]
			assert.Truef(t, ok, "ownership row %s.%s.%s missing from after/%s", row.Config, row.SectionName, row.Option, scenario)
		}
	}
}

// ipInBootstrapWindow reports whether ip is a valid IPv4 in the
// wizard's 10.41.254.0/24 bootstrap window (RandomMeshIP).
func ipInBootstrapWindow(ip string) bool {
	return net.ParseIP(ip) != nil && strings.HasPrefix(ip, "10.41.254.")
}
