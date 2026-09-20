package blos

import (
	"context"
	"errors"
	"net"
	"os/exec"
	"sync"
	"time"

	batmanadv "github.com/openmanet/openmanetd/internal/batman-adv"
	"github.com/openmanet/openmanetd/internal/config"
	"github.com/openmanet/openmanetd/internal/firewall"
	"github.com/openmanet/openmanetd/internal/network"
	"github.com/openmanet/openmanetd/internal/system"
	"github.com/rs/zerolog"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/types/key"
)

// BLOS manages Beyond Line-of-Sight networking via Tailscale, VXLAN, and Batman-adv.
type BLOS struct {
	logger             zerolog.Logger
	uciOpenManetConfig network.OpenMANETConfigReader
	uciNetworkConfig   network.ConfigReader
	uciFirewallConfig  firewall.ConfigReader
	interfaceManager   InterfaceManager
	tsClient           TailscaleClient
	cfg                *config.Config
	statusWorker       *StatusWorker
	lastSyncedPeerIPs  map[string]bool

	// Overrideable function fields for testability.
	// When nil, the private helper methods fall back to the real implementations.
	Reboot         func() error
	RunCmd         func(ctx context.Context, name string, args ...string) error
	SetMTU         func(name string, mtu int) error
	multicastConns []*net.UDPConn
	mu             sync.Mutex
}

// NewBLOS creates a new BLOS instance. It checks gateway mode but performs no
// I/O beyond that. Call Start(ctx) to configure interfaces and begin polling.
// Returns (nil, nil) if the node is not in gateway mode.
func NewBLOS(cfg *config.Config, logger zerolog.Logger) (*BLOS, error) {
	return newBLOS(cfg, logger, batmanadv.GetMeshConfig)
}

// meshConfigFunc reads the batman-adv mesh configuration for an interface.
// It is a parameter of newBLOS so tests can substitute a fake for the
// batctl-backed batmanadv.GetMeshConfig.
type meshConfigFunc func(iface string) (*batmanadv.MeshConfig, error)

func newBLOS(cfg *config.Config, logger zerolog.Logger, getMeshConfig meshConfigFunc) (*BLOS, error) {
	// Get mesh config to determine if we are a gateway
	meshCfg, err := getMeshConfig(cfg.GetAlfredBatInterface())
	if err != nil {
		logger.Error().Err(err).Msg("Error getting mesh config")

		return nil, err
	}

	// Only initialize BLOS if we are in gateway mode
	if !meshCfg.IsGatewayMode() {
		logger.Info().Msg("Not in gateway mode, skipping BLOS initialization")

		return nil, nil
	}

	// One shared LocalAPI client for prefs and status so the daemon holds a
	// single keep-alive connection to tailscaled instead of one per caller.
	tsClient := &LocalTailscaleClient{}

	r := &BLOS{
		cfg:                cfg,
		logger:             logger,
		uciOpenManetConfig: network.NewUCIOpenMANETConfigReader(),
		uciNetworkConfig:   network.NewUCINetworkConfigReader(),
		uciFirewallConfig:  firewall.NewUCIFirewallConfigReader(),
		interfaceManager:   &RealInterfaceManager{},
		tsClient:           tsClient,
	}

	// Initialize the status worker (not started yet)
	interval := time.Duration(cfg.GetBLOSStatusWorkerInterval()) * time.Second
	r.statusWorker = NewStatusWorker(tsClient, interval, logger)

	return r, nil
}

// Start configures network interfaces and begins the status polling loop.
// It must be called after NewBLOS and before the BLOS instance is used.
func (r *BLOS) Start(ctx context.Context) error {
	if r.statusWorker != nil {
		// Set the callback to sync VXLAN peers when Tailscale status updates
		r.statusWorker.SetOnStatusUpdate(func(callbackCtx context.Context) error {
			return r.syncVXLANPeersWithTailscale(callbackCtx)
		})
	}

	if r.tsClient != nil {
		if err := r.configureInterfaces(ctx); err != nil {
			return err
		}
	}

	if r.statusWorker != nil {
		r.statusWorker.Start(ctx)
	}

	r.logger.Info().Msg("BLOS (Beyond Line-of-Sight) module initialized successfully")

	return nil
}

func (r *BLOS) reboot() error {
	if r.Reboot != nil {
		return r.Reboot()
	}

	return system.Reboot()
}

func (r *BLOS) runCmd(ctx context.Context, name string, args ...string) error {
	if r.RunCmd != nil {
		return r.RunCmd(ctx, name, args...)
	}

	return exec.CommandContext(ctx, name, args...).Run()
}

func (r *BLOS) setMTU(name string, mtu int) error {
	if r.SetMTU != nil {
		return r.SetMTU(name, mtu)
	}

	return network.SetMTU(name, mtu)
}

// configureInterfaces sets up the network interfaces required for BLOS operation.
// It first checks the Tailscale tunnel status and validates that it is in a valid state
// (Running or Starting). If the tunnel requires authentication, it returns an error.
// Then it sequentially configures the tunnel interface, VxLAN interface, and Batman
// interface, and creates VxLAN multicast peers. Returns an error if any step fails.
func (r *BLOS) configureInterfaces(ctx context.Context) error { //nolint:gocognit
	tunnelStatus, err := r.tsClient.Status(ctx)
	if err != nil {
		r.logger.Error().Err(err).Msg("Failed to get Tailscale status")

		return err
	}

	switch tunnelStatus.BackendState {
	case backendStateRunning, backendStateStarting:
		r.logger.Info().Msgf("Tunnel status: %s", tunnelStatus.BackendState)
	case backendStateStopped:
		r.logger.Info().Msg("Tunnel is stopped")

		return errors.New("tunnel is stopped. Ensure Tailscale is running and authenticated, then restart openmanetd")
	case backendStateNeedsLogin, backendStateNeedsMachineAuth:
		r.logger.Error().Msgf("Tunnel requires login or machine authentication: %s", tunnelStatus.BackendState)

		return errors.New("tunnel needs to autheticate, or machine auth is broken. Fix the authentication and restart openmanetd")
	default:
		r.logger.Info().Msgf("Tunnel is in state: %s", tunnelStatus.BackendState)
	}

	// Only configure BLOS interfaces if the mesh network interface exists
	if network.NetworkSectionExistsWithReader(r.cfg.GetAlfredBatInterface(), r.uciNetworkConfig) { //nolint:nestif
		blosConfigured, err := network.IsBLOSConfiguredWithReader(r.uciOpenManetConfig)
		if err != nil {
			return err
		}

		if !blosConfigured {
			// Configure wireguard (tailscale) tunnel interface
			if err = r.createOrConfigureTunnelInterface(ctx); err != nil {
				return err
			}

			// Configure VxLAN interface
			if err = r.createOrConfigureVxLanInterface(ctx); err != nil {
				return err
			}

			// Configure Batman interface for tunnel
			if err = r.createOrConfigureBatmanInterface(); err != nil {
				return err
			}

			// Mark BLOS as configured in the OpenMANET config to avoid reconfiguring on every startup
			if err = network.SetBLOSConfiguredWithReader(r.uciOpenManetConfig); err != nil {
				return err
			}

			// Reboot the system to clean up things and apply new network settings.  This is required to properly set up the tunnel and interfaces in the correct order, and to ensure a clean state.  We will likely need to reboot at least once anyway after installation to get Tailscale set up, so doing it here ensures we don't end up in a broken state if we try to configure interfaces before Tailscale is fully set up and authenticated.
			r.logger.Info().Msg("BLOS configured successfully, rebooting system to apply changes")

			if err = r.reboot(); err != nil {
				r.logger.Error().Err(err).Msg("Failed to reboot system after BLOS configuration")

				return err
			}

			// The system is going down; do not run the post-reboot MTU chain
			// below. The tunnel/vxlan interfaces only exist after the reboot
			// brings up the newly-committed UCI config. Any call to them here
			// would fail with "interface not found" and cause the caller to
			// roll back the just-persisted blos.enable flag.
			return nil
		}

		// Apply the MTU chain documented above the MTU constants in
		// interfaces.go. The tunnel MTU matches Tailscale's own default so we
		// don't fight its PMTUD; the VXLAN MTU subtracts the 50-byte VXLAN
		// encapsulation overhead to leave batman-adv a non-fragmenting
		// payload size.
		if err := r.setMTU(defaultTunnelDeviceName, defaultTunnelDeviceMTUValue); err != nil {
			return err
		}

		if err := r.setMTU(defaultVxLanDeviceName, vxLanDefaultMTUValue); err != nil {
			return err
		}

		r.logger.Debug().Msg("BLOS interfaces configured successfully")
	}

	return nil
}

// GetPeers returns the current Tailscale peer information.
func (r *BLOS) GetPeers() map[key.NodePublic]*ipnstate.PeerStatus {
	if r.statusWorker == nil {
		return nil
	}

	return r.statusWorker.GetPeers()
}

// GetPeer returns a specific Tailscale peer by node key.
func (r *BLOS) GetPeer(nodeKey key.NodePublic) (*ipnstate.PeerStatus, bool) {
	if r.statusWorker == nil {
		return nil, false
	}

	return r.statusWorker.GetPeer(nodeKey)
}

// GetStatus returns the last fetched Tailscale status.
func (r *BLOS) GetStatus() *ipnstate.Status {
	if r.statusWorker == nil {
		return nil
	}

	return r.statusWorker.GetStatus()
}

// Stop stops the BLOS worker processes and closes any open multicast sockets.
func (r *BLOS) Stop() {
	if r.statusWorker != nil {
		r.statusWorker.Stop()
	}

	r.mu.Lock()
	conns := r.multicastConns
	r.multicastConns = nil
	r.mu.Unlock()

	for _, c := range conns {
		if err := c.Close(); err != nil {
			r.logger.Debug().Err(err).Msg("Error closing multicast socket during shutdown")
		}
	}
}
