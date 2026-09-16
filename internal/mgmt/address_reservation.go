package mgmt

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/openmanet/go-alfred"
	batmanadv "github.com/openmanet/openmanetd/internal/batman-adv"
	"github.com/openmanet/openmanetd/internal/database/models"
	"github.com/openmanet/openmanetd/internal/network"
	"github.com/openmanet/openmanetd/internal/system"
	"github.com/openmanet/openmanetd/internal/util"
)

type AddressReservationWorker struct {
	Config             *ManagementConfig
	Client             *alfred.Client
	uciOpenMANETConfig network.OpenMANETConfigReader
	uciDHCPConfig      network.DHCPConfigReader
	uciNetworkConfig   network.ConfigReader
	done               <-chan struct{}

	reserveInterval time.Duration
}

// reservationDeps bundles the I/O seams of one reservation tick so the
// decision and its writes can run against fakes.
type reservationDeps struct {
	openMANETReader network.OpenMANETConfigReader
	networkReader   network.ConfigReader
	dhcpReader      network.DHCPConfigReader
	client          alfredClient
	getIface        func(string) network.NetworkInterface
	getMeshConfig   func(string) (*batmanadv.MeshConfig, error)
	getHostname     func() (string, error)
	reloadNetwork   func(context.Context) error
	reboot          func() error
}

func NewAddressReservationWorker(config *ManagementConfig, client *alfred.Client, ctx context.Context) *AddressReservationWorker {
	config.Log.Info().Msg("AddressReservationWorker initialized")

	return &AddressReservationWorker{
		Config:             config,
		Client:             client,
		uciOpenMANETConfig: network.NewUCIOpenMANETConfigReader(),
		uciDHCPConfig:      network.NewUCIDHCPConfigReader(),
		uciNetworkConfig:   network.NewUCINetworkConfigReader(),
		done:               ctx.Done(),

		reserveInterval: config.addressReservationWorkerReserveInterval,
	}
}

// productionDeps wires the tick to the real UCI readers, netlink, batctl
// and the reboot command.
func (arw *AddressReservationWorker) productionDeps() reservationDeps {
	return reservationDeps{
		openMANETReader: arw.uciOpenMANETConfig,
		networkReader:   arw.uciNetworkConfig,
		dhcpReader:      arw.uciDHCPConfig,
		client:          arw.Client,
		getIface:        network.GetInterfaceByName,
		getMeshConfig:   batmanadv.GetMeshConfig,
		getHostname:     os.Hostname,
		reloadNetwork:   network.ReloadNetwork,
		reboot:          system.Reboot,
	}
}

// ReserveAddressIfNeeded runs one reservation tick every reserveInterval
// until the worker's context is done.
func (arw *AddressReservationWorker) ReserveAddressIfNeeded(ctx context.Context) {
	ticker := time.NewTicker(arw.reserveInterval)
	defer ticker.Stop()

	deps := arw.productionDeps()

	for {
		select {
		case <-arw.done:
			return
		case <-ticker.C:
			// The LuCI wizard can rewrite these configs while the daemon stays
			// up. Refresh the worker's private readers before checking the flag
			// or committing changes against the new network and DHCP state.
			if err := arw.reloadUCIConfigs(); err != nil {
				arw.Config.Log.Error().Err(err).Msg("Error reloading UCI config for address reservation")

				continue
			}

			if err := arw.reserveOnceWithDeps(ctx, deps); err != nil {
				arw.Config.Log.Error().Err(err).Msg("Error in address reservation tick")
			}
		}
	}
}

func (arw *AddressReservationWorker) reloadUCIConfigs() error {
	readers := []struct {
		reload func() error
		name   string
	}{
		{name: "openmanetd", reload: arw.uciOpenMANETConfig.ReloadConfig},
		{name: "network", reload: arw.uciNetworkConfig.ReloadConfig},
		{name: "dhcp", reload: arw.uciDHCPConfig.ReloadConfig},
	}

	for _, reader := range readers {
		if err := reader.reload(); err != nil {
			return fmt.Errorf("reload %s: %w", reader.name, err)
		}
	}

	return nil
}

// reserveOnceWithDeps runs a single reservation tick. Each tick starts from
// a clean slate: it refreshes gossip, then either reserves (DHCP not yet
// configured), stays idle (configured, and either no peer holds this
// address or this node's MAC is the lowest in the conflict so a
// higher-MAC peer moves), or re-addresses (configured and this node has
// the higher MAC in the conflict).
func (arw *AddressReservationWorker) reserveOnceWithDeps(ctx context.Context, deps reservationDeps) error {
	// Refresh gossip first so the decision sees what peers say now, not
	// what the 60 s receive loop last stored (ledger P5 step 2). A failed
	// refresh is not fatal: decide on the rows already in the database.
	if recvErr := arw.Config.receiveNodeData(ctx, deps.client, deps.getHostname); recvErr != nil {
		arw.Config.Log.Warn().Err(recvErr).Msg("Could not refresh node gossip before reservation; using stored peers")
	}

	configured, err := network.IsDHCPConfiguredWithReader(deps.openMANETReader)
	if err != nil {
		return fmt.Errorf("check DHCP configuration: %w", err)
	}

	nodes, err := arw.Config.DB.ListMeshNodes(ctx)
	if err != nil {
		return fmt.Errorf("list mesh nodes: %w", err)
	}

	iface := deps.getIface(arw.Config.IFace)
	conflicts := findIPConflicts(iface, nodes)

	// One decision per tick; nothing is remembered between ticks.
	switch {
	case !configured:
		arw.Config.Log.Debug().Msg("DHCP not configured, reserving address")
	case len(conflicts) == 0:
		return nil
	case !yieldsAddress(iface.MAC, conflicts):
		arw.Config.Log.Warn().
			Str("peer", conflicts[0].Hostname).
			Str("ip", conflicts[0].IpAddr).
			Msg("IP conflict: this node has the lower MAC and keeps its address; the peer must move")

		return nil
	default:
		arw.Config.Log.Warn().
			Str("peer", conflicts[0].Hostname).
			Str("ip", conflicts[0].IpAddr).
			Msg("IP conflict: this node has the higher MAC, re-reserving")
	}

	return arw.applyReservationWithDeps(nodes, deps)
}

// yieldsAddress reports whether this node must give up its address in a
// conflict. The node with the lowest MAC keeps the address and every other
// party re-addresses, so two colliding gates converge in one extra reboot
// of one node instead of chasing each other (ledger D3). MACs compare
// case-insensitively; equal or empty MACs never yield.
func yieldsAddress(ownMAC string, peers []models.MeshNode) bool {
	own := strings.ToLower(ownMAC)

	for _, peer := range peers {
		mac := strings.ToLower(peer.MacAddr)
		if mac != "" && mac < own {
			return true
		}
	}

	return false
}

// findIPConflicts returns every peer row whose advertised address matches
// one of iface's addresses, in database order.
func findIPConflicts(iface network.NetworkInterface, nodes []models.MeshNode) []models.MeshNode {
	var conflicts []models.MeshNode

	for _, node := range nodes {
		for _, addr := range iface.IP {
			if addr.IP.String() == node.IpAddr {
				conflicts = append(conflicts, node)

				break
			}
		}
	}

	return conflicts
}

// applyReservationWithDeps claims an address and DHCP window from what the
// peer rows leave free, persists them, marks DHCP configured, cleans up the
// bootstrap sections and reboots so every service comes back on the final
// address (ledger D2: the reboot is by design).
func (arw *AddressReservationWorker) applyReservationWithDeps(nodes []models.MeshNode, deps reservationDeps) error {
	// The gateway flag decides both the address range and the cleanup.
	meshCfg, err := deps.getMeshConfig(arw.Config.BatInterface)
	if err != nil {
		return fmt.Errorf("get mesh config: %w", err)
	}

	staticIP, err := network.SelectAvailableStaticIPFromNodeData(nodes, meshCfg.IsGatewayMode())
	if err != nil {
		return fmt.Errorf("select available static IP: %w", err)
	}

	// dhcp and network sections are keyed by the interface without its br- prefix.
	normalizedIface, err := util.InterfaceWithoutBridge(arw.Config.IFace)
	if err != nil {
		return fmt.Errorf("normalize interface name: %w", err)
	}

	err = network.SetNetworkConfigWithReader(normalizedIface, &network.UCINetwork{
		Proto:          network.DefaultNetworkProto,
		IPAddr:         staticIP,
		NetMask:        network.DefaultNetworkMask,
		IPV6Class:      network.DefaultIPv6Class,
		IPV6IfaceID:    network.DefaultIPv6IfaceID,
		IPV6Assignment: network.DefaultIPv6Assign,
		Device:         arw.Config.IFace,
	}, deps.networkReader)
	if err != nil {
		return fmt.Errorf("set network config: %w", err)
	}

	dhcpStart, err := network.CalculateAvailableDHCPStart(nodes, network.DefaultNetworkAddress, network.DefaultNetworkMask, network.DefaultDHCPAddressLimit)
	if err != nil {
		return fmt.Errorf("calculate DHCP start: %w", err)
	}

	dhcpConfig := &network.UCIDHCP{
		Interface: normalizedIface,
		Start:     strconv.Itoa(dhcpStart),
		Limit:     strconv.Itoa(network.DefaultDHCPAddressLimit),
		LeaseTime: network.DefaultDHCPLeaseTime,
		Force:     "1",
	}

	arw.Config.Log.Debug().Interface("dhcpConfig", dhcpConfig).Msg("Setting DHCP config")

	if err = network.SetDHCPConfigWithReader(normalizedIface, dhcpConfig, deps.dhcpReader); err != nil {
		return fmt.Errorf("set DHCP config: %w", err)
	}

	arw.Config.Log.Info().Msgf("Static IP %s and DHCP configured via address reservation", staticIP)

	if err = network.SetDHCPConfiguredWithReader(deps.openMANETReader); err != nil {
		return fmt.Errorf("mark DHCP configured: %w", err)
	}

	// Bootstrap cleanup only happens here, on initial configuration. Sections
	// an operator creates later are left alone unless a re-reservation runs.
	if err = arw.cleanUpInterfacesWithDeps(meshCfg.IsGatewayMode(), deps.networkReader, deps.dhcpReader, deps.reloadNetwork); err != nil {
		return fmt.Errorf("clean up interfaces: %w", err)
	}

	arw.Config.Log.Info().Msg("Rebooting system to apply new network settings")

	if err = deps.reboot(); err != nil {
		return fmt.Errorf("reboot: %w", err)
	}

	return nil
}

func (arw *AddressReservationWorker) cleanUpInterfacesWithDeps(
	isGateway bool,
	networkReader network.ConfigReader,
	dhcpReader network.DHCPConfigReader,
	reloadFn func(context.Context) error,
) error { //nolint:gocognit
	if isGateway {
		arw.Config.Log.Info().Msg("Mesh gateway mode enabled, skipping interface cleanup")

		return nil
	}

	// Clean up 'lan' network sections if they exist
	if network.NetworkSectionExistsWithReader("lan", networkReader) {
		arw.Config.Log.Info().Msg("Removing 'lan' network section")

		err := network.DeleteNetworkConfigWithReader("lan", networkReader)
		if err != nil {
			return fmt.Errorf("error deleting 'lan' network section: %w", err)
		}
	}

	// Commit network changes
	if err := networkReader.Commit(); err != nil {
		return fmt.Errorf("error committing network config: %w", err)
	}

	// Clean up DHCP sections if they exist
	if network.DHCPSectionExistsWithReader("lan", dhcpReader) {
		arw.Config.Log.Info().Msg("Removing 'lan' DHCP section")

		err := network.DeleteDHCPConfigWithReader("lan", dhcpReader)
		if err != nil {
			return fmt.Errorf("error deleting 'lan' DHCP section: %w", err)
		}
	}

	// Commit DHCP changes
	if err := dhcpReader.Commit(); err != nil {
		return fmt.Errorf("error committing DHCP config: %w", err)
	}

	// Reload network to apply changes
	if err := reloadFn(context.Background()); err != nil {
		return fmt.Errorf("error reloading network configuration: %w", err)
	}

	return nil
}
