package blos

import (
	"context"
	"fmt"
	"net/netip"
	"slices"
	"strconv"

	"github.com/openmanet/openmanetd/internal/config"
	"github.com/openmanet/openmanetd/internal/firewall"
	"github.com/openmanet/openmanetd/internal/network"
	"tailscale.com/client/local"
	"tailscale.com/ipn"
	"tailscale.com/ipn/ipnstate"
)

// TailscaleClient abstracts the Tailscale local daemon API for testability.
type TailscaleClient interface {
	Status(ctx context.Context) (*ipnstate.Status, error)
	GetPrefs(ctx context.Context) (*ipn.Prefs, error)
	EditPrefs(ctx context.Context, mp *ipn.MaskedPrefs) (*ipn.Prefs, error)
}

// LocalTailscaleClient is the production implementation using the Tailscale SDK.
//
// It wraps exactly one local.Client for the lifetime of the value. A
// local.Client lazily builds a private http.Transport whose keep-alive
// connection to the tailscaled unix socket never expires and cannot be
// closed, so constructing a fresh client per call leaks one file
// descriptor (and the matching accepted socket inside tailscaled) every
// time. The zero value is ready to use and must not be copied once used.
type LocalTailscaleClient struct {
	lc local.Client
}

// Status returns the current Tailscale daemon status.
func (c *LocalTailscaleClient) Status(ctx context.Context) (*ipnstate.Status, error) {
	status, err := c.lc.Status(ctx)
	if err != nil {
		return nil, fmt.Errorf("tailscale status: %w", err)
	}

	return status, nil
}

// GetPrefs returns the current Tailscale preferences.
func (c *LocalTailscaleClient) GetPrefs(ctx context.Context) (*ipn.Prefs, error) {
	prefs, err := c.lc.GetPrefs(ctx)
	if err != nil {
		return nil, fmt.Errorf("tailscale get prefs: %w", err)
	}

	return prefs, nil
}

// EditPrefs updates Tailscale preferences.
func (c *LocalTailscaleClient) EditPrefs(ctx context.Context, mp *ipn.MaskedPrefs) (*ipn.Prefs, error) {
	prefs, err := c.lc.EditPrefs(ctx, mp)
	if err != nil {
		return nil, fmt.Errorf("tailscale edit prefs: %w", err)
	}

	return prefs, nil
}

// InterfaceManager defines an interface for managing network interfaces.
type InterfaceManager interface {
	BringUp(ctx context.Context, name string) error
}

// RealInterfaceManager is the real implementation that calls actual network commands.
type RealInterfaceManager struct{}

// BringUp brings up a network interface by name using ifup command.
func (r *RealInterfaceManager) BringUp(ctx context.Context, name string) error {
	return network.PerformIfUp(ctx, name)
}

// NoOpInterfaceManager is a no-op implementation for testing.
type NoOpInterfaceManager struct{}

// BringUp does nothing and returns nil (for testing).
func (n *NoOpInterfaceManager) BringUp(_ context.Context, name string) error {
	return nil
}

// MTU chain documentation:
//
// The on-wire encapsulation stack for an inner mesh frame is:
//
//	inner Ethernet frame (≤ vxLanDefaultMTUValue)
//	  + VXLAN(8) + UDP(8) + IP(20)     = 36B  → carried as UDP payload on tailscale0
//	  + WireGuard(32) + UDP(8) + IP(20) = 60B  → sent on the physical uplink
//
// Previous values (tailscale0=1500, vxlan0=1450) ignored WireGuard's ~60 byte
// overhead, so a full-sized inner frame became a ~1546 byte packet on the
// physical wire — guaranteed to fragment on a 1500 MTU internet path and
// silently dropped on paths where PMTUD is broken (common through NAT /
// DERP relay).
//
// The current chain uses Tailscale's own default (1280) as the tunnel MTU
// and subtracts the VXLAN overhead (50B) from that for the overlay MTU,
// leaving 1230 bytes of payload available to batman-adv. This is
// conservative: it works on any IPv6-minimum-MTU path, accommodates DERP
// relay's extra HTTPS framing, and leaves headroom for a future v6 underlay.
// Operators on a known-good fixed-MTU underlay can raise these values after
// a PMTUD measurement — see docs/instrumentation-snapshot.md.
const (
	defaultLearningValue        string = "1"
	defaultProxyValue           string = "1"
	vxLanProtocol               string = "vxlan"
	vxLanDefaultMTUValue        int    = 1230
	defaultTunnelDeviceName     string = "tailscale0"
	defaultTunnelDeviceMTUValue int    = 1280
	defaultVxLanDeviceName      string = "vxlan0"
	defaultBatmanInterfaceName  string = "battunnel0"
	defaultMeshNetZoneName      string = "ahwlan"
)

// createOrConfigureTunnelInterface creates or configures a tunnel interface in the UCI network configuration.
// It checks if a tunnel interface with the default device name already exists in the UCI configuration.
// If the interface doesn't exist, it creates a new network section with protocol set to "none" and the
// default tunnel device name. Returns an error if the network configuration operation fails.
func (r *BLOS) createOrConfigureTunnelInterface(ctx context.Context) error {
	// Check if the tunnel interface already exists in UCI
	if !network.NetworkSectionExistsWithReader(defaultTunnelDeviceName, r.uciNetworkConfig) { //nolint:nestif
		// Create a new network section for the tunnel interface
		if err := network.SetNetworkConfigWithReader(defaultTunnelDeviceName, &network.UCINetwork{
			Proto:  "none",
			Device: defaultTunnelDeviceName,
		}, r.uciNetworkConfig); err != nil {
			return err
		}

		if err := firewall.AddNetworkToZoneWithReader(defaultMeshNetZoneName, defaultTunnelDeviceName, r.uciFirewallConfig); err != nil {
			return err
		}

		if err := r.configureTailscalePreferences(ctx); err != nil {
			return err
		}

		// Remove tailscale0 from the br-ahwlan bridge if it's there, to avoid conflicts with the VXLAN interface
		device, err := network.GetDeviceByNameWithReader(r.cfg.MeshNetInterface, r.uciNetworkConfig)
		if err != nil {
			return err
		}

		if device != nil && slices.Contains(device.Ports, defaultTunnelDeviceName) {
			if err := r.runCmd(ctx, "uci", "del_list", "network."+r.cfg.MeshNetInterface+".ports="+defaultTunnelDeviceName); err != nil {
				return err
			}
		}

		r.logger.Debug().Msgf("Created BLOS tunnel interface %s", defaultTunnelDeviceName)
	}

	return nil
}

// createOrConfigureVxLanInterface ensures that a VXLAN interface exists in the UCI network configuration.
// It checks if a network section with the default VXLAN device name already exists. If not, it creates
// a new VXLAN network section with the following configuration:
//   - Proto: "vxlan" - sets the protocol type to VXLAN
//   - Learning: "1" - enables MAC address learning
//   - Tunlink: defaultTunnelDeviceName - links the VXLAN to the tunnel device
//   - Proxy: "1" - enables ARP proxying
//   - MTU: vxLanDefaultMTUValue - sets the VXLAN interface MTU
//   - RxCsum: "0" - disables receive checksum offload for performance
//   - TxCsum: "0" - disables transmit checksum offload for performance
//   - VID: "1" - sets the VXLAN Network Identifier (VNI)
//
// After creating the section, the network configuration is force-reloaded so
// the interface is brought up immediately.
//
// Returns an error if the VXLAN configuration creation or reload fails, otherwise returns nil.
func (r *BLOS) createOrConfigureVxLanInterface(ctx context.Context) error {
	// Check if the VXLAN interface already exists in UCI
	if !network.NetworkSectionExistsWithReader(defaultVxLanDeviceName, r.uciNetworkConfig) {
		// Create a new network section for the VXLAN interface
		if err := network.SetVXLANConfigWithReader(defaultVxLanDeviceName, &network.UCIVXLANConfig{
			Proto:    vxLanProtocol,
			Learning: defaultLearningValue,
			Tunlink:  defaultTunnelDeviceName,
			Proxy:    defaultProxyValue,
			MTU:      strconv.Itoa(vxLanDefaultMTUValue),
			RxCsum:   "0",
			TxCsum:   "0",
			VID:      "1",
		}, r.uciNetworkConfig); err != nil {
			return err
		}

		if err := network.ForceReloadConfig(ctx); err != nil {
			return err
		}

		r.logger.Debug().Msgf("Created BLOS VXLAN interface %s", defaultVxLanDeviceName)
	}

	return nil
}

// createOrConfigureBatmanInterface creates or configures a Batman (Better Approach To Mobile Ad-hoc Networking)
// interface for BLOS (Beyond Line of Sight). It checks if a Batman interface with the default name already exists
// in the UCI (Unified Configuration Interface) network configuration. If the interface does not exist, it creates
// a new network section configured as a batadv_hardif (Batman-adv hard interface) that uses the default VXLAN
// device and associates it with the configured mesh network interface. The function logs the creation of the
// interface when successful. Returns an error if the network configuration operation fails.
func (r *BLOS) createOrConfigureBatmanInterface() error {
	// Check if the Batman interface already exists in UCI
	if !network.NetworkSectionExistsWithReader(defaultBatmanInterfaceName, r.uciNetworkConfig) {
		// Create a new network section for the Batman interface
		if err := network.SetNetworkConfigWithReader(defaultBatmanInterfaceName, &network.UCINetwork{
			Proto:  "batadv_hardif",
			Device: defaultVxLanDeviceName,
			Master: r.cfg.AlfredBatInterface,
		}, r.uciNetworkConfig); err != nil {
			return err
		}

		r.logger.Debug().Msgf("Created BLOS Batman interface %s", defaultBatmanInterfaceName)
	}

	return nil
}

// configureTailscalePreferences retrieves current Tailscale preferences, updates them to enable
// RouteAll and disable NoSNAT, and applies the changes back to the Tailscale daemon.
func (r *BLOS) configureTailscalePreferences(ctx context.Context) error {
	// Get current preferences from Tailscale daemon
	prefs, err := r.tsClient.GetPrefs(ctx)
	if err != nil {
		r.logger.Error().Err(err).Msg("Failed to get Tailscale preferences")

		return err
	}

	// Update preferences using helper function
	r.updateTailscalePreferences(prefs)

	// Apply the updated preferences back to Tailscale
	_, err = r.tsClient.EditPrefs(ctx, &ipn.MaskedPrefs{
		Prefs:              *prefs,
		NoSNATSet:          true,
		AdvertiseRoutesSet: true,
	})
	if err != nil {
		r.logger.Error().Err(err).Msg("Failed to update Tailscale preferences")

		return err
	}

	r.logger.Info().Msg("Successfully configured Tailscale preferences (RouteAll: true, NoSNAT: false)")

	return nil
}

// updateTailscalePreferences updates the provided Prefs to disable NoSNAT and
// advertise the configured mesh subnet. The advertised subnet is read from
// the BLOS config (blos.advertisedMeshSubnet); the config loader validates
// that it parses, so a malformed value here falls back to the default rather
// than propagating a parse error into the Tailscale EditPrefs call.
func (r *BLOS) updateTailscalePreferences(prefs *ipn.Prefs) {
	subnetStr := config.DefaultBLOSAdvertisedMeshSubnet
	if r.cfg != nil {
		subnetStr = r.cfg.GetBLOSAdvertisedMeshSubnet()
	}

	subnet, err := netip.ParsePrefix(subnetStr)
	if err != nil {
		r.logger.Warn().
			Err(err).
			Str("value", subnetStr).
			Str("fallback", config.DefaultBLOSAdvertisedMeshSubnet).
			Msg("blos.advertisedMeshSubnet did not parse; using default")

		subnet = netip.MustParsePrefix(config.DefaultBLOSAdvertisedMeshSubnet)
	}

	r.logger.Info().
		Str("subnet", subnet.String()).
		Msg("Advertising mesh subnet to Tailscale")

	edits := ipn.MaskedPrefs{
		Prefs: ipn.Prefs{
			NoSNAT:          false,
			AdvertiseRoutes: []netip.Prefix{subnet},
		},
		NoSNATSet:          true,
		AdvertiseRoutesSet: true,
	}

	prefs.ApplyEdits(&edits)
}
