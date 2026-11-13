package openmanet

import (
	"context"
	"io/fs"
	"math/rand"
	"net/http"
	_ "net/http/pprof" //nolint:gosec
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/common-nighthawk/go-figure"
	"github.com/openmanet/openmanetd/internal/auth"
	batmanadv "github.com/openmanet/openmanetd/internal/batman-adv"
	"github.com/openmanet/openmanetd/internal/blos"
	"github.com/openmanet/openmanetd/internal/comms"
	"github.com/openmanet/openmanetd/internal/comms/control/alsa"
	"github.com/openmanet/openmanetd/internal/config"
	"github.com/openmanet/openmanetd/internal/database"
	"github.com/openmanet/openmanetd/internal/database/models"
	"github.com/openmanet/openmanetd/internal/frontend"
	"github.com/openmanet/openmanetd/internal/gpsd"
	"github.com/openmanet/openmanetd/internal/instrumentation"
	"github.com/openmanet/openmanetd/internal/logs"
	"github.com/openmanet/openmanetd/internal/mgmt"
	"github.com/openmanet/openmanetd/internal/network"
	"github.com/openmanet/openmanetd/internal/openmanet/server"
	"github.com/openmanet/openmanetd/internal/openmanet/server/handlers"
	"github.com/openmanet/openmanetd/internal/system"
	"github.com/openmanet/openmanetd/internal/sysupgrade"
	"github.com/openmanet/openmanetd/internal/terminal"
	"github.com/openmanet/openmanetd/internal/util/board"
	"github.com/openmanet/openmanetd/internal/util/logger"
	"github.com/openmanet/openmanetd/internal/wireless"
	"github.com/rs/zerolog"
)

func Start(staticFS fs.FS) {
	var (
		ctx, cancel = context.WithCancel(context.Background())
		banner      = figure.NewFigure("OpenMANET", "big", true)
		c           = make(chan os.Signal, 1)
		cfg         = config.New(nil)
		log         = logger.InitLogging(ctx)
		gps         *gpsd.GPSService
		manager     *mgmt.ManagementConfig
	)

	banner.Print()
	applyRuntimeTuning(cfg, log)

	// Create Comms manager (always, so the API handler can use it even if comms is currently disabled)
	mixerVol := &alsa.Volume{
		Log: logger.GetLogger("alsa-mixer"),
		Names: alsa.NamesFromOverrides(
			cfg.GetCommsAudioSpeakerControl(),
			cfg.GetCommsAudioMicControl(),
			cfg.GetCommsAudioAGCControl(),
		),
	}
	commsManager := comms.NewCommsManager(cfg, logger.GetLogger("comms"), mixerVol)

	if board.CommsSupported() && cfg.GetCommsEnable() {
		if err := commsManager.Enable(); err != nil {
			log.Error().Err(err).Msg("Failed to enable comms module")
		}
	} else if !board.CommsSupported() {
		log.Warn().Msg("Current board does not support Comms features; skipping initialization of comms module")
	}

	// Establish database connection
	db, err := database.NewConnection(ctx, logger.GetLogger("database"), cfg.GetDBFile())
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to connect to database")
	}

	if cfg.GetResetDBOnStart() {
		err = resetDBOnStart(ctx, db, logger.GetLogger("database"))
		if err != nil {
			log.Fatal().Err(err).Msg("Failed to reset database on start")
		}
	}

	if cfg.GetEnableGNSS() && board.GNSSsupoorted() {
		// Initialize and start GNSS module
		gps, err = gpsd.NewGPSService(logger.GetLogger("gps"), cfg)
		if err != nil {
			log.Fatal().Err(err).Msg("Failed to initialize GPS service")
		}
	}

	// Initialize and start management module
	if cfg.GetAlfredEnable() {
		manager, err = mgmt.NewManager(mgmt.ManagementConfig{
			Log:                        logger.GetLogger("mgmt"),
			GPS:                        gps,
			AlfredMode:                 cfg.GetAlfredMode(),
			IFace:                      cfg.GetMeshNetInterface(),
			BatInterface:               cfg.GetAlfredBatInterface(),
			SocketPath:                 cfg.GetAlfredSocketPath(),
			GatewayDataType:            cfg.GetAlfredDataTypeGateway(),
			NodeDataType:               cfg.GetAlfredDataTypeNode(),
			PositionDataType:           cfg.GetAlfredDataTypePosition(),
			AddressReservationDataType: cfg.GetAlfredDataTypeAddressReservation(),
			MeshNeighborsDataType:      cfg.GetAlfredDataTypeMeshNeighbors(),
			BatmanMulticastForceflood:  cfg.GetBatmanMulticastForceflood(),
			NodeExpiry:                 cfg.GetAlfredNodeExpiry(),
			DB:                         db,
		})
		if err != nil {
			log.Fatal().Err(err).Msg("Failed to initialize management workers")
		}

		manager.Start(ctx)
	} else {
		log.Info().Msg("Alfred integration disabled; skipping management workers")
	}

	// Clear the batman-adv hosts file on startup
	// to remove any stale entries
	// Stale entries can cause issues with name resolution for nodes that have changed IPs
	// This can also cause issues with gateway selection if the stale entry is for a gateway node
	err = batmanadv.ClearBatHosts()
	if err != nil {
		log.Error().Err(err).Msg("Error clearing batman-adv hosts file on startup")
	}

	// Create BLOS manager (always, so the API handler can use it even if BLOS is currently disabled)
	blosManager := blos.NewBLOSManager(cfg, logger.GetLogger("blos"))

	// Construct the sysupgrade manager early so the instrumentation
	// worker can register its snapshotter alongside comms/blos. The
	// manager spawns no background goroutines on construction; the
	// only goroutine it owns is the per-upgrade one created on
	// StartUpgrade.
	sysupgradeMgr := sysupgrade.NewManager(sysupgrade.Options{
		Log:                 logger.GetLogger("sysupgrade"),
		Repo:                "OpenMANET/firmware",
		Board:               handlers.NewCachedBoardProvider(&handlers.DefaultBoardProvider{}),
		Firmware:            handlers.NewCachedFirmwareProvider(&system.OpenWrtFirmwareProvider{}),
		SysInfo:             &system.LinuxSysInfo{},
		Capable:             &system.LinuxSysupgradeCapabilityProvider{},
		Cache:               sysupgrade.NewDiskCache("/var/lib/openmanetd/sysupgrade-releases.json"),
		Releases:            &sysupgrade.GitHubReleasesClient{Repo: "OpenMANET/firmware", Log: logger.GetLogger("sysupgrade-github")},
		Runner:              &sysupgrade.ExecSysupgradeRunner{},
		FactoryReset:        &sysupgrade.ExecFactoryResetRunner{},
		FactoryResetCapable: &system.LinuxFactoryResetCapabilityProvider{},
		DownloadDir:         "/tmp/openmanetd/sysupgrade",
		PersistentLogDir:    "/etc/openmanetd/sysupgrade",
	})

	// One TTL-bounded wireless cache serves both the API handlers and
	// the instrumentation snapshotter, so the snapshot adds no netlink
	// polling of its own. manager is nil when alfred is disabled.
	wifiProvider := buildWifiProvider(manager)

	// Wire the instrumentation snapshot registry and conditionally spawn
	// the periodic worker. The registry is always constructed (cheap) but
	// the worker goroutine is only started when the config flag is true,
	// so a disabled deployment pays nothing beyond the adapter structs.
	startInstrumentationWorker(ctx, cfg, blosManager, sysupgradeMgr, mixerVol, wifiProvider, log)

	// BatctlSnapshotter owns one background goroutine that refreshes the
	// outputs of batctl oj / nj / mj / gwj plus /tmp/bat-hosts every 5s.
	// Every RPC handler that used to fork batctl per-request now reads
	// from the shared cache via the snapshotter's typed accessors.
	batctlSnapshotter := handlers.NewBatctlSnapshotter(
		logger.GetLogger("batctl-snapshot"),
		cfg.GetAlfredBatInterface(),
		handlers.DefaultBatctlSnapshotInterval,
	)
	batctlSnapshotter.Start(ctx)

	// SystemSnapshotter refreshes /proc/uptime, /proc/meminfo,
	// /proc/loadavg, /overlay, and service PID files every 2s. Each
	// Dashboard.GetDashboardStatus RPC used to re-open all of those
	// synchronously; now they are read once per cycle and served from
	// the cache to every concurrent caller.
	sysSnapshotter := handlers.NewSystemSnapshotter(
		logger.GetLogger("system-snapshot"),
		&system.LinuxSysInfo{},
		&system.InitDServiceChecker{},
		system.DefaultMonitoredServices(),
		handlers.DefaultSystemSnapshotInterval,
	)
	sysSnapshotter.Start(ctx)

	// The topology provider enriches the cached originator list with
	// bat-hosts + self-MAC + hop derivation for the RPC handler. Because
	// the snapshotter implements OriginatorProvider, the `batctl oj` call
	// is shared with every other handler.
	meshOrigProvider := &batmanadv.BatctlOriginatorTopologyProvider{
		Originators: batctlSnapshotter,
	}

	// Start the mesh-topology delta tracker. The tracker keeps a rolling
	// snapshot ring so the MeshTopologyService.GetMeshTopologyDelta RPC
	// can return churn metrics without re-shelling out per call. Exits
	// on ctx cancellation.
	meshDeltaTracker := handlers.NewDeltaTracker(
		logger.GetLogger("mesh-delta"),
		batctlSnapshotter,
		batctlSnapshotter,
		time.Duration(cfg.GetMeshTopologyDeltaSampleInterval())*time.Second,
		cfg.GetMeshTopologyMaxDeltaSamples(),
	)
	meshDeltaTracker.Start(ctx)

	// Mesh-neighbors gossip snapshotter: consumes MeshNeighbors records
	// published by every node's mgmt.MeshNeighborsWorker and caches them
	// for the topology handler's gossip-aware classifier. Shares the
	// Alfred client opened by the management module so we don't open a
	// second connection to the socket.
	meshNeighborsSnap := startMeshNeighborsSnapshotter(ctx, cfg, manager)

	// Set up session-based authentication when enabled.
	var (
		sessionStore  *auth.SessionStore
		authenticator auth.Authenticator
	)

	if cfg.GetAuthEnable() {
		sessionStore = auth.NewSessionStore(
			time.Duration(cfg.GetAuthSessionMaxAgeSecs())*time.Second,
			cfg.GetAuthSessionMaxSize(),
		)
		sessionStore.StartCleanup(ctx, 5*time.Minute)

		authenticator = &auth.PAMAuthenticator{ServiceName: cfg.GetAuthPAMService()}
		log.Info().Str("pamService", cfg.GetAuthPAMService()).Msg("authentication enabled")
	}

	// Start API Server
	interfaceProvider := &network.NetlinkInterfaceProvider{}

	// Setup wizard wiring: shared UCI reader (production wraps the
	// default go-uci tree, so all nine wizard configs are addressed
	// through one reader). The snapshotter captures the raw file
	// contents of /etc/config/{wireless,network,dhcp,firewall,system,
	// mesh11sd,umdns,openmanetd,luci} before phase 3 runs and restores them atomically on
	// any failure between phases 3 and 12. The post-bricking
	// restructure makes this load-bearing: without it, a phase-12
	// commit failure leaves the device with a half-applied wizard
	// state on disk that bricks the next boot.
	setupReader := network.NewUCIWirelessConfigReader()
	setupSnapshotter := newWizardSnapshotter(setupReader)
	setupRNG := rand.New(rand.NewSource(time.Now().UnixNano()))

	apiServer := server.APIServer{
		Cfg:                   cfg,
		Log:                   logger.GetLogger("api"),
		DB:                    db,
		GPS:                   gps,
		BLOSManager:           blosManager,
		Tailscale:             blosManager,
		CommsManager:          commsManager,
		Mixer:                 mixerVol,
		MeshDeltaTracker:      meshDeltaTracker,
		MeshOrigProvider:      meshOrigProvider,
		MeshVisProvider:       batctlSnapshotter,
		MeshNeighborsProvider: meshNeighborsSnap,
		BatctlSnapshotter:     batctlSnapshotter,
		SystemSnapshotter:     sysSnapshotter,
		Logread:               &logs.LogreadProvider{},
		Dmesg:                 &logs.DmesgProvider{},
		Interfaces:            interfaceProvider,
		DHCP: &network.UCIDHCPConfigProvider{
			DHCPReader:    network.NewUCIDHCPConfigReader(),
			NetworkReader: network.NewUCINetworkConfigReader(),
		},
		Leases: &network.UbusLeaseProvider{
			Executor: &network.DefaultUbusExecutor{},
		},
		SessionStore:        sessionStore,
		Authenticator:       authenticator,
		Sysupgrade:          sysupgradeMgr,
		AuthEnabled:         cfg.GetAuthEnable(),
		SetupUCIReader:      setupReader,
		SetupSnapshotter:    setupSnapshotter,
		SetupPasswordSetter: &auth.ChpasswdSetter{},
		SetupHostnameSetter: &handlers.DefaultHostnameSetter{Reader: setupReader},
		SetupReloader:       &system.InitDReloader{},
		SetupRNG:            setupRNG,
	}

	// buildWifiProvider returns nil when the manager is absent or its
	// nl80211 client failed to initialize; both consumers then stay
	// unset (name-based interface classification, no wifi handlers)
	// instead of dereferencing a nil WirelessConfig. Reading the
	// classifier through the cache also folds its Interfaces() walk
	// into the one the handlers already share.
	if wifiProvider != nil {
		apiServer.Wifi = wifiProvider
		interfaceProvider.WifiInterfaces = wifiProvider.Interfaces
	}

	api := server.NewAPIServer(apiServer)

	log.Info().Msg("OpenMANETd API Server starting on port 8087")

	if cfg.BLOSEnabled() && board.BLOSsupported() {
		if err := blosManager.Enable(context.Background()); err != nil {
			log.Fatal().Err(err).Msg("Failed to initialize BLOS module")
		}
	}

	// Wait for interrupt signal to gracefully shutdown the application
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	// Start the API server in a goroutine so it doesn't block
	go func() {
		if err := api.ApiServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("API Server failed")
		}
	}()

	termMgr := buildTerminalManager(cfg, log)

	frontendServer := frontend.NewFrontendServer(ctx, cfg, staticFS, sessionStore, cfg.GetAuthEnable(), termMgr)

	go func() {
		if err := frontendServer.Run(ctx); err != nil {
			log.Error().Err(err).Msg("Frontend Server failed")
		}
	}()

	// Block until we receive an interrupt signal, then gracefully shutdown.
	<-c

	// Cancel context to signal all context-aware goroutines (mgmt workers, hub, etc.)
	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)

	_ = api.Stop(shutdownCtx)

	shutdownCancel()

	_ = database.CloseConnection()

	commsManager.Disable()
	blosManager.Disable()

	if cfg.GetEnableGNSS() {
		gps.Close()
	}

	log.Info().Msg("Exiting OpenMANETd")
	os.Exit(0)
}

// startInstrumentationWorker constructs the instrumentation snapshot
// registry, registers the comms, BLOS, sysupgrade and wireless
// adapters, and starts the periodic worker goroutine when the config
// flag is enabled. The registry itself is cheap; only the worker has
// runtime cost. Errors during setup are logged but never fatal — a
// misconfigured snapshot subsystem must not prevent the daemon from
// serving traffic.
func startInstrumentationWorker(ctx context.Context, cfg *config.Config, blosManager *blos.BLOSManager, sysupgradeMgr *sysupgrade.Manager, mixerVol *alsa.Volume, wifiProvider *handlers.CachedWirelessProvider, log zerolog.Logger) {
	if !cfg.GetInstrumentationEnable() {
		return
	}

	instrLog := logger.GetLogger("instrumentation")

	hostname, err := os.Hostname()
	if err != nil {
		instrLog.Warn().Err(err).Msg("instrumentation: failed to read hostname; leaving empty")

		hostname = ""
	}

	reg := instrumentation.NewRegistry(instrumentation.Options{
		Log:      instrLog,
		Version:  "", // populated from build metadata when available
		Hostname: hostname,
	})

	if err = reg.Register("comms", &comms.CommsSnapshotter{}); err != nil {
		log.Error().Err(err).Msg("instrumentation: failed to register comms snapshotter")

		return
	}

	if err = reg.Register("audio_mixer", &alsa.MixerSnapshotter{V: mixerVol}); err != nil {
		log.Error().Err(err).Msg("instrumentation: failed to register audio mixer snapshotter")

		return
	}

	if err = reg.Register("blos", &blos.BLOSSnapshotter{Manager: blosManager}); err != nil {
		log.Error().Err(err).Msg("instrumentation: failed to register blos snapshotter")

		return
	}

	if sysupgradeMgr != nil {
		if err = reg.Register("sysupgrade", &sysupgrade.Snapshotter{Manager: sysupgradeMgr}); err != nil {
			log.Error().Err(err).Msg("instrumentation: failed to register sysupgrade snapshotter")

			return
		}
	}

	// A typed nil must never reach the Provider interface field, so the
	// section is registered only when the cache exists.
	if wifiProvider != nil {
		if err = reg.Register("wireless", &wireless.Snapshotter{Provider: wifiProvider}); err != nil {
			log.Error().Err(err).Msg("instrumentation: failed to register wireless snapshotter")

			return
		}
	}

	worker, err := instrumentation.NewWorker(instrumentation.WorkerOptions{
		Registry:       reg,
		Interval:       time.Duration(cfg.GetInstrumentationIntervalSecs()) * time.Second,
		OutputDir:      cfg.GetInstrumentationSnapshotDir(),
		FilenamePrefix: "openmanetd-snapshot",
		Log:            instrLog,
	})
	if err != nil {
		log.Error().Err(err).Msg("instrumentation: failed to construct snapshot worker")

		return
	}

	go worker.Run(ctx)
}

// buildTerminalManager constructs the web-terminal manager when the feature
// is enabled in config, or returns nil when disabled. Returning nil signals
// to the frontend handler that the route should answer 404.
func buildTerminalManager(cfg *config.Config, log zerolog.Logger) *terminal.Manager {
	if !cfg.GetTerminalEnable() {
		return nil
	}

	if !cfg.GetAuthEnable() {
		log.Warn().Msg("terminal: web terminal root shell is enabled while UI auth is disabled; anyone who can reach the UI has root")
	}

	tcfg := terminal.DefaultConfig()
	tcfg.Shell = cfg.GetTerminalShell()

	return terminal.New(log.With().Str("subsystem", "terminal").Logger(), tcfg)
}

// applyRuntimeTuning configures Go runtime parameters and optionally starts
// the pprof debug endpoint based on the application configuration.
func applyRuntimeTuning(cfg *config.Config, log zerolog.Logger) {
	prof := board.CurrentExecutionProfile()

	cfgGOMAXPROCS := cfg.GetRuntimeGOMAXPROCS()
	if n := resolveGOMAXPROCS(cfgGOMAXPROCS, prof); n > 0 {
		runtime.GOMAXPROCS(n)

		source := "board"
		if cfgGOMAXPROCS > 0 {
			source = "config"
		}

		log.Info().Int("gomaxprocs", n).Str("source", source).Msg("Applied GOMAXPROCS")
	}

	if cfg.GetDebugPprof() {
		pprofAddr := cfg.GetDebugPprofAddress()

		go func() {
			log.Info().Str("addr", pprofAddr).Msg("pprof debug endpoint enabled")

			if err := http.ListenAndServe(pprofAddr, nil); err != nil { //nolint:gosec
				log.Error().Err(err).Msg("pprof server failed")
			}
		}()
	}
}

// resolveGOMAXPROCS returns the GOMAXPROCS value to apply, or 0 to leave Go's
// runtime default (runtime.NumCPU()) untouched. An explicit config value
// (> 0) takes precedence over the board's recommendation; otherwise the
// board's ExecutionProfile value is used (which may itself be 0 = no override).
func resolveGOMAXPROCS(cfgVal int, prof board.ExecutionProfile) int {
	if cfgVal > 0 {
		return cfgVal
	}

	return prof.GOMAXPROCS
}

// buildWifiProvider constructs the TTL-bounded wireless cache shared by
// the API handlers, the instrumentation snapshotter, and the netlink
// interface classifier (interfaceProvider.WifiInterfaces), or returns nil
// when alfred is disabled (manager is nil) or when mgmt.NewManager
// tolerated a failed nl80211 init (manager.WirelessConfig is nil, e.g.
// the family is absent or the driver isn't loaded). NewManager logs
// and continues in that case rather than failing startup, so wrapping
// a nil WirelessConfig here would hand the cache a nil *wifi.Client
// and panic on the first Refresh; returning nil instead leaves the API
// and the snapshot with no wifi provider, same as before this cache
// existed. Building the cache once here and handing the same pointer
// to both consumers means the snapshot adds no netlink polling of its
// own.
func buildWifiProvider(manager *mgmt.ManagementConfig) *handlers.CachedWirelessProvider {
	if manager == nil || manager.WirelessConfig == nil {
		return nil
	}

	return handlers.NewCachedWirelessProvider(manager.WirelessConfig, handlers.DefaultWirelessCacheTTL)
}

// startMeshNeighborsSnapshotter constructs and starts the
// MeshNeighborsSnapshotter when alfred is enabled and the manager has
// produced a client. Returns a typed-nil-safe interface value (the
// literal `nil`, not a typed nil pointer wrapped in an interface) so
// the APIServer nil-check on the field behaves correctly.
func startMeshNeighborsSnapshotter(
	ctx context.Context,
	cfg *config.Config,
	manager *mgmt.ManagementConfig,
) batmanadv.MeshNeighborsProvider {
	if manager == nil || !cfg.GetAlfredDataTypeMeshNeighbors() {
		return nil
	}

	alfredClient := manager.Client()
	if alfredClient == nil {
		return nil
	}

	snap := &batmanadv.MeshNeighborsSnapshotter{
		Log:      logger.GetLogger("mesh-neighbors"),
		Client:   alfredClient,
		Interval: batmanadv.DefaultMeshNeighborsSnapshotInterval,
	}
	snap.Start(ctx)

	return snap
}

func resetDBOnStart(ctx context.Context, db *models.Queries, log zerolog.Logger) error {
	log.Info().Msg("Resetting database on start as per configuration")
	// Add any additional tables that need to be cleared here
	err := db.DeleteAllMeshNodes(ctx)
	if err != nil {
		return err
	}

	return nil
}
