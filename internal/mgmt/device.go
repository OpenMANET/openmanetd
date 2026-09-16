package mgmt

import (
	"context"
	"errors"
	"fmt"

	"github.com/openmanet/openmanetd/internal/iwinfo"
	"github.com/openmanet/openmanetd/internal/network"
)

const (
	// Interface MTU for interfaces. The bridge and ethernet values are
	// shared with the setup wizard, which persists them in UCI; the
	// netlink pass below still applies them at every start.
	defaultMeshInterfaceMTU     int = 1500
	defaultBatmanInterfaceMTU   int = 1460
	defaultAhwlanInterfaceMTU   int = network.DefaultBridgeMTU
	defaultEthernetInterfaceMTU int = network.DefaultEthernetMTU
)

// setTransportInterfaceMTU sets the MTU (Maximum Transmission Unit) for all
// wireless mesh interfaces defined in the WirelessConfig. It retrieves the list
// of mesh interfaces and attempts to set each interface's MTU to
// defaultMeshInterfaceMTU. If setting the MTU for a particular interface fails,
// the error is logged and the process continues with the remaining interfaces.
// A successful MTU update is also logged at the Info level.
//
// Returns an error if WirelessConfig is nil (NewManager tolerated a failed
// nl80211 init) or if the mesh interfaces cannot be retrieved from
// WirelessConfig, otherwise returns nil even if individual interface MTU
// updates fail.
func (m *ManagementConfig) setTransportInterfaceMTU() error {
	if m.WirelessConfig == nil {
		return errors.New("wireless config unavailable (nl80211 init failed); skipping transport MTU pass")
	}

	wirelessInterfaces, err := m.WirelessConfig.GetMeshInterfaces()
	if err != nil {
		return err
	}

	// Iterate over each wireless mesh interface and set its MTU.
	for _, iface := range wirelessInterfaces {
		if err := network.SetMTU(iface.Name, defaultMeshInterfaceMTU); err != nil {
			m.Log.Error().Err(err).Str("interface", iface.Name).Msg("Failed to set MTU for mesh interface")
		} else {
			m.Log.Info().Str("interface", iface.Name).Int("mtu", defaultMeshInterfaceMTU).Msg("Set MTU for mesh interface")
		}
	}

	// Additionally, set the MTU for the Batman interface and the main AHWLAN bridge interface.
	bridgeInterface := network.GetInterfaceByName(network.DefaultBridgeInterfaceName)
	if bridgeInterface.Name == "" {
		m.Log.Warn().Str("interface", network.DefaultBridgeInterfaceName).Msg("Bridge interface not found, skipping MTU configuration")
	} else if bridgeInterface.MTU != defaultAhwlanInterfaceMTU {
		if err := network.SetMTU(network.DefaultBridgeInterfaceName, defaultAhwlanInterfaceMTU); err != nil {
			m.Log.Error().Err(err).Str("interface", network.DefaultBridgeInterfaceName).Msg("Failed to set MTU for bridge interface")
		} else {
			m.Log.Info().Str("interface", network.DefaultBridgeInterfaceName).Int("mtu", defaultAhwlanInterfaceMTU).Msg("Set MTU for bridge interface")
		}
	}

	batmanInterface := network.GetInterfaceByName(network.DefaultBatmanInterfaceName)
	if batmanInterface.Name == "" {
		m.Log.Warn().Str("interface", network.DefaultBatmanInterfaceName).Msg("Batman interface not found, skipping MTU configuration")
	} else if batmanInterface.MTU != defaultBatmanInterfaceMTU {
		if err := network.SetMTU(network.DefaultBatmanInterfaceName, defaultBatmanInterfaceMTU); err != nil {
			m.Log.Error().Err(err).Str("interface", network.DefaultBatmanInterfaceName).Msg("Failed to set MTU for Batman interface")
		} else {
			m.Log.Info().Str("interface", network.DefaultBatmanInterfaceName).Int("mtu", defaultBatmanInterfaceMTU).Msg("Set MTU for Batman interface")
		}
	}

	m.setEthernetInterfaceMTU()

	return nil
}

// setEthernetInterfaceMTU sets the MTU (Maximum Transmission Unit) for the
// primary and secondary Ethernet interfaces defined in the ManagementConfig.
// It attempts to set the MTU for both DefaultEthernetInterfaceName and
// DefaultSecondaryEthernetInterfaceName to defaultEthernetInterfaceMTU. If setting
// the MTU for an interface fails, the error is logged and the process continues
// with the remaining interface. A successful MTU update is also logged at the
// Info level.
func (m *ManagementConfig) setEthernetInterfaceMTU() {
	ethernetInterfaces := []string{network.DefaultEthernetInterfaceName, network.DefaultSecondaryEthernetInterfaceName}

	for _, ifaceName := range ethernetInterfaces {
		iface := network.GetInterfaceByName(ifaceName)
		if iface.Name == "" {
			m.Log.Warn().Str("interface", ifaceName).Msg("Ethernet interface not found, skipping MTU configuration")

			continue
		}

		if iface.MTU != defaultEthernetInterfaceMTU {
			if err := network.SetMTU(ifaceName, defaultEthernetInterfaceMTU); err != nil {
				m.Log.Error().Err(err).Str("interface", ifaceName).Msg("Failed to set MTU for Ethernet interface")
			} else {
				m.Log.Info().Str("interface", ifaceName).Int("mtu", defaultEthernetInterfaceMTU).Msg("Set MTU for Ethernet interface")
			}
		}
	}
}

// setupBatMesh1Interface configures a new 2.4 GHz batman-adv batmesh1 wireless
// interface if it has not already been configured. It creates concrete UCI and
// iwinfo dependencies and delegates to setupBatMesh1InterfaceWithDeps.
func (m *ManagementConfig) setupBatMesh1Interface(ctx context.Context) error {
	return m.setupBatMesh1InterfaceWithDeps(
		ctx,
		m.uciOpenMANETConfig,
		network.NewUCIWirelessConfigReader(),
		iwinfo.NewClient(),
		network.NewDefaultWirelessStatusProvider(),
	)
}

// setupBatMesh1InterfaceWithDeps configures the 2.4 GHz secondary
// batman-adv mesh link (network batmesh1) when the wizard did not. It is
// idempotent: if batmesh1configured is already set the method returns
// immediately. The method:
//
//  1. Returns without changes when batmesh1 is already configured (the
//     wizard stages the flag to 1 when the operator chose the backhaul).
//  2. Correlates each 2.4 GHz UCI radio with its runtime interface and
//     selects a single radio backed by a chipset that
//     network.SupportsSecondaryMeshLink accepts.
//  3. Leaves the radio alone when its AP section (default_<radio>) is
//     enabled — the operator's AP wins over the fallback link.
//  4. Locates an existing wifi-iface with mode=mesh and borrows its
//     mesh_id and key values.
//  5. Creates wifi-iface <batmesh1>_<radio> from network.MeshLink so the
//     section can never collide with the AP section on the same radio.
//  6. Updates the matched 2g radio with the secondary-link channel and
//     width, then marks batmesh1 as configured.
func (m *ManagementConfig) setupBatMesh1InterfaceWithDeps(
	ctx context.Context,
	openmanetReader network.OpenMANETConfigReader,
	wirelessReader network.ConfigReader,
	iwinfoProvider iwinfo.IwinfoProvider,
	wirelessStatus network.WirelessStatusProvider,
) error {
	// Step 1: guard — return early if already configured.
	configured, err := network.IsBatMesh1ConfiguredWithReader(openmanetReader)
	if err != nil {
		return fmt.Errorf("check batmesh1 configured: %w", err)
	}

	if configured {
		m.Log.Debug().Msg("batmesh1 already configured, skipping")

		return nil
	}

	// Step 2: hardware check — require MT7915 or MT7916 chipset.
	allInfo, err := iwinfoProvider.GetInfoForAll(ctx)
	if err != nil {
		return fmt.Errorf("get iwinfo for all devices: %w", err)
	}

	status, err := wirelessStatus.GetWirelessStatus(ctx)
	if err != nil {
		m.Log.Debug().Err(err).Msg("Wireless status unavailable; skipping batmesh1 configuration")

		return nil
	}

	deviceSections, err := wirelessReader.GetSections("wireless", "wifi-device")
	if err != nil {
		return fmt.Errorf("get wifi-device sections: %w", err)
	}

	var radioSection string

	for _, section := range deviceSections {
		device, deviceErr := network.GetWirelessDeviceByNameWithReader(section, wirelessReader)
		if deviceErr != nil || device.Band != "2g" {
			continue
		}

		hardwareName := network.ResolveWirelessRadioHardwareName(section, status, allInfo)
		if !network.SupportsSecondaryMeshLink(hardwareName) {
			continue
		}

		if radioSection != "" {
			m.Log.Debug().Msg("Multiple supported 2.4 GHz radios found; skipping batmesh1 configuration")

			return nil
		}

		radioSection = section
	}

	if radioSection == "" {
		m.Log.Debug().Msg("No MT7915/MT7916 2.4 GHz radio found for batmesh1 configuration")

		return nil
	}

	// Step 3: an enabled AP on the radio wins. The operator (or the
	// wizard) chose it; never move its channel or add a link beside it.
	if radioHostsEnabledAP(radioSection, wirelessReader) {
		m.Log.Info().Str("radio", radioSection).Msg("Radio hosts an enabled AP; leaving it alone, no batmesh1 link written")

		return nil
	}

	// Step 4: find mesh credentials from an existing mesh wifi-iface.
	ifaceSections, err := wirelessReader.GetSections("wireless", "wifi-iface")
	if err != nil {
		return fmt.Errorf("get wifi-iface sections: %w", err)
	}

	var meshID, meshKey string

	for _, section := range ifaceSections {
		iface, ierr := network.GetWirelessIfaceByNameWithReader(section, wirelessReader)
		if ierr != nil {
			continue
		}

		if iface.Mode == network.WifiModeMesh {
			meshID = iface.MeshID
			meshKey = iface.Key

			break
		}
	}

	if meshID == "" {
		return fmt.Errorf("no existing wifi-iface with mode=mesh found; cannot determine mesh credentials")
	}

	link := network.MeshLink{
		Radio:         radioSection,
		Network:       network.BatmanSecondaryIface,
		MeshID:        meshID,
		Key:           meshKey,
		RSSIThreshold: network.SecondaryMeshRSSIThreshold,
	}
	newIfaceSection := link.Section()

	// Step 5: create the new wifi-iface.
	if err := network.SetWirelessIfaceConfigWithReader(newIfaceSection, link.IfaceConfig(), wirelessReader); err != nil {
		return fmt.Errorf("create wifi-iface %s: %w", newIfaceSection, err)
	}

	// SetWirelessIfaceConfigWithReader only writes non-empty struct
	// fields, and MeshLink.IfaceConfig().Disabled is always empty. If
	// this section already carried disabled=1 from a prior wizard run
	// (e.g. a wizard re-run's reset phase disabled every wifi-iface and
	// no backhaul was re-chosen), that stale value would otherwise
	// survive the rewrite and leave the link permanently dead even
	// though batmesh1configured gets set below. Clear it explicitly.
	if err := wirelessReader.Del("wireless", newIfaceSection, "disabled"); err != nil {
		return fmt.Errorf("clear disabled on %s: %w", newIfaceSection, err)
	}

	m.Log.Info().Str("section", newIfaceSection).Str("device", radioSection).Msg("Created batmesh1 wifi-iface")

	// Step 6: update the 2g radio device.
	radioUpdate := &network.UCIWirelessDevice{
		Channel:  network.SecondaryMeshChannel2G,
		HTMode:   network.SecondaryMeshHTMode2G,
		Disabled: "0",
	}

	if err := network.SetWirelessDeviceConfigWithReader(radioSection, radioUpdate, wirelessReader); err != nil {
		return fmt.Errorf("update wifi-device %s: %w", radioSection, err)
	}

	m.Log.Info().Str("section", radioSection).Str("channel", network.SecondaryMeshChannel2G).Str("htmode", network.SecondaryMeshHTMode2G).Str("disabled", "0").Msg("Updated 2g radio for batmesh1")

	// Step 6: mark batmesh1 as configured.
	if err := network.SetBatMesh1ConfiguredWithReader(openmanetReader); err != nil {
		return fmt.Errorf("mark batmesh1 configured: %w", err)
	}

	m.Log.Info().Msg("batmesh1 interface configuration complete")

	// Reload the UCI config to apply changes and ensure the new interface is active.
	_ = network.ForceReloadConfig(ctx)

	return nil
}

// radioHostsEnabledAP reports whether the AP section for radio
// ("default_<radio>", the name both the factory image and the wizard
// use) exists with mode=ap and is not disabled.
func radioHostsEnabledAP(radio string, reader network.ConfigReader) bool {
	iface, err := network.GetWirelessIfaceByNameWithReader("default_"+radio, reader)
	if err != nil {
		return false
	}

	return iface.Mode == "ap" && iface.Disabled != "1"
}

// reconcileBatMesh1Options adds any daemon-owned tuning option
// (network.SecondaryMeshPolicyOptions: mcast_rate, mesh_nolearn and the
// three plink timers) that an existing batmesh1 wifi-iface lacks, so a
// node configured before those options existed picks them up on its
// next start. It applies only to sections on MT7915/MT7916 radios, adds
// but never overwrites, and commits plus reloads once only when at
// least one option was written. It does not depend on
// batmesh1configured: wizard-, fallback- and settings-written enabled
// sections are all candidates; a disabled=1 section is skipped. An iwinfo
// or wireless-status lookup failure is logged at Warn and skips the pass
// for this start, matching the settings handler and the boot fallback;
// UCI read/write and reload failures are returned.
func (m *ManagementConfig) reconcileBatMesh1Options(ctx context.Context) error {
	return m.reconcileBatMesh1OptionsWithDeps(
		ctx,
		network.NewUCIWirelessConfigReader(),
		iwinfo.NewClient(),
		network.NewDefaultWirelessStatusProvider(),
		network.ForceReloadConfig,
	)
}

// reconcileBatMesh1OptionsWithDeps is the testable implementation of
// reconcileBatMesh1Options. Hardware lookups run lazily, only once a
// batmesh1 mesh section is found, so nodes without a secondary link pay
// no iwinfo/ubus round trip.
func (m *ManagementConfig) reconcileBatMesh1OptionsWithDeps(
	ctx context.Context,
	wirelessReader network.ConfigReader,
	iwinfoProvider iwinfo.IwinfoProvider,
	wirelessStatus network.WirelessStatusProvider,
	reloadFn func(context.Context) error,
) error {
	ifaceSections, err := wirelessReader.GetSections("wireless", "wifi-iface")
	if err != nil {
		return fmt.Errorf("get wifi-iface sections: %w", err)
	}

	var (
		hardware map[string]string // radio section -> iwinfo hardware name
		written  bool
	)

	for _, section := range ifaceSections {
		iface, ierr := network.GetWirelessIfaceByNameWithReader(section, wirelessReader)
		if ierr != nil || iface.Network != network.BatmanSecondaryIface || iface.Mode != network.WifiModeMesh {
			continue
		}

		// A disabled section is not on the air; writing tuning into it
		// would only buy a wifi reload. It is reconciled once enabled.
		if iface.Disabled == "1" {
			m.Log.Debug().Str("section", section).Msg("batmesh1 section disabled; leaving tuning alone")

			continue
		}

		if hardware == nil {
			hardware, err = resolveRadioHardware(ctx, iwinfoProvider, wirelessStatus)
			if err != nil {
				m.Log.Warn().Err(err).Msg("Radio hardware lookup unavailable; skipping batmesh1 tuning reconcile this start")

				return nil
			}
		}

		hw := hardware[iface.Device]
		if hw == "" {
			m.Log.Debug().Str("section", section).Str("radio", iface.Device).
				Msg("batmesh1 radio hardware name unresolved (radio not up yet?); will retry next start")

			continue
		}

		if !network.SupportsSecondaryMeshLink(hw) {
			m.Log.Debug().Str("section", section).Str("radio", iface.Device).Str("hardware", hw).
				Msg("batmesh1 section is not on an MT7915/MT7916 radio; leaving tuning alone")

			continue
		}

		added, aerr := network.EnsureSecondaryMeshPolicyOptions(wirelessReader, section)
		if aerr != nil {
			return fmt.Errorf("reconcile %s: %w", section, aerr)
		}

		if len(added) == 0 {
			continue
		}

		written = true

		m.Log.Info().Str("section", section).Strs("options", added).Msg("Added missing batmesh1 tuning options")
	}

	if !written {
		m.Log.Debug().Msg("batmesh1 tuning options already present; nothing to reconcile")

		return nil
	}

	if err := wirelessReader.Commit(); err != nil {
		return fmt.Errorf("commit wireless after batmesh1 reconcile: %w", err)
	}

	if err := reloadFn(ctx); err != nil {
		return fmt.Errorf("reload after batmesh1 reconcile: %w", err)
	}

	return nil
}

// resolveRadioHardware returns each UCI radio's iwinfo hardware name,
// keyed by radio section, using one iwinfo and one ubus round trip.
func resolveRadioHardware(
	ctx context.Context,
	iwinfoProvider iwinfo.IwinfoProvider,
	wirelessStatus network.WirelessStatusProvider,
) (map[string]string, error) {
	allInfo, err := iwinfoProvider.GetInfoForAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("get iwinfo for all devices: %w", err)
	}

	status, err := wirelessStatus.GetWirelessStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("get wireless status: %w", err)
	}

	out := make(map[string]string, len(status))
	for radio := range status {
		out[radio] = network.ResolveWirelessRadioHardwareName(radio, status, allInfo)
	}

	return out, nil
}

// configureBatmanForceflood persists the batman-adv multicast mode derived
// from batman.multicastForceflood to the bat0 interface section of the UCI
// network config so it survives reboots. The UCI option is `multicast_mode`,
// which OpenWrt's batadv proto handler passes straight to batctl; the
// kernel defines it as the negation of forceflood, so forceflood=true
// writes "0" (classic flooding) and false writes "1" (IGMP/MLD-snooping
// optimisations) — see network.MulticastModeForForceflood. A subsequent
// network reload applies the change. The write is change-only: when bat0
// already carries the wanted value nothing is committed and no reload is
// issued.
func (m *ManagementConfig) configureBatmanForceflood(ctx context.Context) error {
	return m.configureBatmanForcefloodWithDeps(
		ctx,
		network.NewUCINetworkConfigReader(),
		network.ForceReloadConfig,
	)
}

// configureBatmanForcefloodWithDeps is the testable implementation.
// Dependencies are injected so the function can be unit-tested without a
// real OpenWrt environment.
func (m *ManagementConfig) configureBatmanForcefloodWithDeps(
	ctx context.Context,
	reader network.ConfigReader,
	reloadFn func(context.Context) error,
) error {
	want := network.MulticastModeForForceflood(m.BatmanMulticastForceflood)
	current := network.MulticastModeWithReader(reader, m.BatInterface)

	if current == want {
		m.Log.Debug().
			Str("interface", m.BatInterface).
			Str("multicast_mode", want).
			Msg("batman-adv multicast_mode already persisted; skipping commit and reload")

		return nil
	}

	if err := network.SetNetworkConfigWithReader(m.BatInterface, &network.UCINetwork{
		MulticastMode: want,
	}, reader); err != nil {
		return fmt.Errorf("set multicast_mode on %s: %w", m.BatInterface, err)
	}

	m.Log.Info().
		Str("interface", m.BatInterface).
		Str("previous", current).
		Str("multicast_mode", want).
		Msg("Persisted batman-adv multicast_mode (forceflood) to UCI")

	if err := reloadFn(ctx); err != nil {
		return fmt.Errorf("reload config: %w", err)
	}

	return nil
}
