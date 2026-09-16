package handlers_test

// setup_mtu_test.go pins the wizard-parity P6 write: `option mtu 1460`
// on br-ahwlan and on every ethernet port bridged into it. LuCI writes
// no mtu — the daemon has always set these through netlink at boot
// (internal/mgmt/device.go setTransportInterfaceMTU) — so the captured
// fixtures carry none and fixture parity cannot pin this. The
// divergence is deliberate (ledger M3, decided 2026-08-28): persisting
// the values lets a network reload keep them without waiting for the
// next daemon start. The daemon's netlink pass stays in place.

import (
	"context"
	"testing"

	"github.com/digineo/go-uci/v2"
	setupv1 "github.com/openmanet/openmanetd/internal/api/openmanet/setup/v1"
	"github.com/openmanet/openmanetd/internal/config"
	"github.com/openmanet/openmanetd/internal/network"
	"github.com/openmanet/openmanetd/internal/openmanet/server/handlers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// twoPortProvider makes the wizard see eth0 and eth1, so a gate
// scenario has one uplink and one bridged port. runScenarioApply's
// default provider lists nothing, which collapses to the eth0 fallback
// and leaves a gate bridge with no ethernet member at all.
func twoPortProvider() *fakeInterfaceProvider {
	return &fakeInterfaceProvider{infos: []network.NetworkInterfaceInfo{
		{Name: "eth0", LinkType: network.LinkTypeEthernet},
		{Name: "eth1", LinkType: network.LinkTypeEthernet},
	}}
}

// applyWithTwoPorts runs ApplySetup with eth0 and eth1 visible and
// returns the service and config (for re-runs) plus the staged tree.
func applyWithTwoPorts(t *testing.T, profile *setupv1.MeshNodeProfile) (*handlers.SetupService, *config.Config, *uciTree) {
	t.Helper()

	cfg := setupBLOSTestConfig(t, "setup:\n  enabled: true\n")
	svc, _ := newFullSetupService(t, cfg)
	svc.Interfaces = twoPortProvider()

	require.NoError(t, svc.ApplySetupForTest(context.Background(), profile, &streamCollector{}),
		"ApplySetup must complete cleanly")

	reader, ok := svc.UCI.(*fakeConfigReader)
	require.True(t, ok, "fakeConfigReader expected on UCI field")

	return svc, cfg, &uciTree{reader: reader}
}

// deviceSectionsNamed returns every `config device` section whose
// `name` option equals name — more than one is a netifd hazard.
func (t *uciTree) deviceSectionsNamed(name string) []string {
	out := []string{}

	for _, s := range t.sectionsOfType("network", "device") {
		if t.getOne("network", s, "name") == name {
			out = append(out, s)
		}
	}

	return out
}

// TestCompat_TransportMTU_Gate: uplink eth0 stays a plain wan/lan
// port; eth1 is bridged and carries the transport mtu with br-ahwlan.
func TestCompat_TransportMTU_Gate(t *testing.T) {
	_, _, tr := applyWithTwoPorts(t, gateRouterEthProfile())

	bridge := tr.findBridgeDevice("br-ahwlan")
	require.NotEmpty(t, bridge)
	require.Len(t, tr.deviceSectionsNamed("br-ahwlan"), 1,
		"the bridge's own section must carry the mtu — never a second wizard_device_ section")
	assert.Equal(t, "1460", tr.getOne("network", bridge, "mtu"),
		"br-ahwlan must persist the daemon's 1460 transport mtu")
	assert.ElementsMatch(t, []string{"eth1", "bat0"}, tr.get("network", bridge, "ports"))

	eth1 := tr.deviceSectionsNamed("eth1")
	require.Len(t, eth1, 1, "exactly one device section per port name")
	assert.Equal(t, "1460", tr.getOne("network", eth1[0], "mtu"))

	assert.Empty(t, tr.deviceSectionsNamed("eth0"),
		"the uplink port is not mesh transport and gets no UCI mtu")
	assert.Empty(t, tr.deviceSectionsNamed("bat0"),
		"bat0 derives its mtu from its hardifs — the daemon owns it")
}

// TestCompat_TransportMTU_Point: every ethernet port is bridged on a
// point, so every one carries the transport mtu.
func TestCompat_TransportMTU_Point(t *testing.T) {
	_, _, tr := applyWithTwoPorts(t, pointExtenderProfile())

	bridge := tr.findBridgeDevice("br-ahwlan")
	require.NotEmpty(t, bridge)
	require.Len(t, tr.deviceSectionsNamed("br-ahwlan"), 1,
		"the bridge's own section must carry the mtu — never a second wizard_device_ section")
	assert.Equal(t, "1460", tr.getOne("network", bridge, "mtu"))
	assert.ElementsMatch(t, []string{"eth0", "eth1", "bat0"}, tr.get("network", bridge, "ports"))

	for _, port := range []string{"eth0", "eth1"} {
		secs := tr.deviceSectionsNamed(port)
		require.Lenf(t, secs, 1, "%s must have exactly one device section", port)
		assert.Equalf(t, "1460", tr.getOne("network", secs[0], "mtu"), "%s mtu", port)
	}

	assert.Empty(t, tr.deviceSectionsNamed("bat0"))
}

// TestCompat_TransportMTU_VendorSectionReused: board.d ships a device
// section with the port's macaddr; the wizard must add mtu to it, not
// stage a second section netifd would let override the first.
func TestCompat_TransportMTU_VendorSectionReused(t *testing.T) {
	cfg := setupBLOSTestConfig(t, "setup:\n  enabled: true\n")
	svc, _ := newFullSetupService(t, cfg)
	svc.Interfaces = twoPortProvider()

	reader, ok := svc.UCI.(*fakeConfigReader)
	require.True(t, ok)
	require.NoError(t, reader.AddSection("network", "vendor_eth1", "device"))
	require.NoError(t, reader.SetType("network", "vendor_eth1", "name", uci.TypeOption, "eth1"))
	require.NoError(t, reader.SetType("network", "vendor_eth1", "macaddr", uci.TypeOption, "aa:bb:cc:dd:ee:01"))

	require.NoError(t, svc.ApplySetupForTest(context.Background(), gateRouterEthProfile(), &streamCollector{}))

	tr := &uciTree{reader: reader}
	assert.Equal(t, []string{"vendor_eth1"}, tr.deviceSectionsNamed("eth1"),
		"the vendor section must be reused, never duplicated")
	assert.Equal(t, "1460", tr.getOne("network", "vendor_eth1", "mtu"))
	assert.Equal(t, "aa:bb:cc:dd:ee:01", tr.getOne("network", "vendor_eth1", "macaddr"),
		"vendor macaddr must survive the wizard")
}

// TestCompat_TransportMTU_RerunWithSwappedUplink: the phase-4 reset
// strips every device mtu and drops wizard_device_* sections, so a
// re-run with the other port as uplink leaves the old bridged port
// clean and stages the new one.
func TestCompat_TransportMTU_RerunWithSwappedUplink(t *testing.T) {
	svc, cfg, tr := applyWithTwoPorts(t, gateRouterEthProfile()) // uplink eth0, bridged eth1
	require.Len(t, tr.deviceSectionsNamed("eth1"), 1)

	// Re-arm the wizard the way `openmanetd setup-reset` does.
	require.NoError(t, cfg.PersistSetupAndAuth(false, false))
	require.NoError(t, tr.reader.SetType("luci", "wizard", "used", uci.TypeOption, "0"))

	swapped := gateRouterEthProfile()
	swapped.Uplink.EthernetPort = "eth1"
	require.NoError(t, svc.ApplySetupForTest(context.Background(), swapped, &streamCollector{}))

	assert.Empty(t, tr.deviceSectionsNamed("eth1"),
		"the previous run's wizard_device_eth1 must be gone after reset")

	eth0 := tr.deviceSectionsNamed("eth0")
	require.Len(t, eth0, 1)
	assert.Equal(t, "1460", tr.getOne("network", eth0[0], "mtu"))

	bridge := tr.findBridgeDevice("br-ahwlan")
	require.NotEmpty(t, bridge)
	assert.Equal(t, "1460", tr.getOne("network", bridge, "mtu"))
	assert.ElementsMatch(t, []string{"eth0", "bat0"}, tr.get("network", bridge, "ports"))
}

// TestCompat_TransportMTU_ResetStripsVendorMTU: a vendor section that
// carried an mtu from an earlier run loses it in the reset and gets
// the wizard's value back only if the port is bridged this time.
func TestCompat_TransportMTU_ResetStripsVendorMTU(t *testing.T) {
	cfg := setupBLOSTestConfig(t, "setup:\n  enabled: true\n")
	svc, _ := newFullSetupService(t, cfg)
	svc.Interfaces = twoPortProvider()

	reader, ok := svc.UCI.(*fakeConfigReader)
	require.True(t, ok)
	require.NoError(t, reader.AddSection("network", "vendor_eth0", "device"))
	require.NoError(t, reader.SetType("network", "vendor_eth0", "name", uci.TypeOption, "eth0"))
	require.NoError(t, reader.SetType("network", "vendor_eth0", "mtu", uci.TypeOption, "1460"))

	// eth0 is the uplink here, so it must come out of the reset without an mtu.
	require.NoError(t, svc.ApplySetupForTest(context.Background(), gateRouterEthProfile(), &streamCollector{}))

	tr := &uciTree{reader: reader}
	assert.Equal(t, []string{"vendor_eth0"}, tr.deviceSectionsNamed("eth0"))
	assert.Empty(t, tr.getOne("network", "vendor_eth0", "mtu"),
		"reset must strip a stale mtu from the uplink port's vendor section")
}
