package mgmt

import (
	"context"
	"time"

	"github.com/openmanet/go-alfred"
	"github.com/openmanet/openmanetd/internal/database/models"
	"github.com/openmanet/openmanetd/internal/gpsd"
	"github.com/openmanet/openmanetd/internal/network"
	"github.com/openmanet/openmanetd/internal/util/board"
	"github.com/rs/zerolog"
)

const (
	nodeDataWorkerInterval time.Duration = 60 * time.Second

	gatewayDataWorkerSendInterval time.Duration = 60 * time.Second
	gatewayDataWorkerRecvInterval time.Duration = 10 * time.Second

	addressReservationWorkerReserveInterval time.Duration = 125 * time.Second

	// meshNeighborsWorkerInterval balances topology responsiveness against
	// gossip bandwidth. 15 s is ~4× the batadv-vis heartbeat and fast
	// enough to reflect field changes within half a minute.
	meshNeighborsWorkerInterval time.Duration = 15 * time.Second
)

type ManagementConfig struct {
	Log                                     zerolog.Logger
	DB                                      *models.Queries
	GPS                                     *gpsd.GPSService
	uciOpenMANETConfig                      *network.UCIOpenMANETConfigReader
	uciDHCPConfig                           *network.UCIDHCPConfigReader
	uciNetworkConfig                        *network.UCINetworkConfigReader
	boardConfigInfo                         *board.Board
	WirelessConfig                          *WirelessConfig
	alfredClient                            *alfred.Client
	SocketPath                              string
	AlfredMode                              string
	IFace                                   string
	BatInterface                            string
	gatewayWorkerSendInterval               time.Duration
	gatewayWorkerRecvInterval               time.Duration
	addressReservationWorkerReserveInterval time.Duration
	// NodeExpiry drops mesh_nodes rows not refreshed by gossip within this
	// window so departed peers stop reserving addresses (ledger D4). Zero
	// disables expiry.
	NodeExpiry                 time.Duration
	NodeDataType               bool
	PositionDataType           bool
	AddressReservationDataType bool
	MeshNeighborsDataType      bool
	BatmanMulticastForceflood  bool
	GatewayDataType            bool
}

func NewManager(cfg ManagementConfig) (*ManagementConfig, error) {
	boardConfigInfo, err := board.NewBoardConfigInfo()
	if err != nil {
		cfg.Log.Error().Err(err).Msg("Failed to load board configuration")
	}

	// Init nl80211 wirelsss client
	wirelessConfig, err := NewWirelessConfig()
	if err != nil {
		cfg.Log.Error().Err(err).Msg("Failed to load wireless configuration")
	}

	return &ManagementConfig{
		Log:                        cfg.Log,
		AlfredMode:                 cfg.AlfredMode,
		IFace:                      cfg.IFace,
		BatInterface:               cfg.BatInterface,
		SocketPath:                 cfg.SocketPath,
		GatewayDataType:            cfg.GatewayDataType,
		NodeDataType:               cfg.NodeDataType,
		PositionDataType:           cfg.PositionDataType,
		AddressReservationDataType: cfg.AddressReservationDataType,
		MeshNeighborsDataType:      cfg.MeshNeighborsDataType,
		BatmanMulticastForceflood:  cfg.BatmanMulticastForceflood,
		NodeExpiry:                 cfg.NodeExpiry,
		WirelessConfig:             wirelessConfig,
		DB:                         cfg.DB,
		GPS:                        cfg.GPS,

		gatewayWorkerSendInterval:               gatewayDataWorkerSendInterval,
		gatewayWorkerRecvInterval:               gatewayDataWorkerRecvInterval,
		addressReservationWorkerReserveInterval: addressReservationWorkerReserveInterval,

		uciOpenMANETConfig: network.NewUCIOpenMANETConfigReader(),
		uciDHCPConfig:      network.NewUCIDHCPConfigReader(),
		uciNetworkConfig:   network.NewUCINetworkConfigReader(),

		boardConfigInfo: boardConfigInfo,
	}, nil
}

func (m *ManagementConfig) Start(ctx context.Context) {
	if err := m.setTransportInterfaceMTU(); err != nil {
		m.Log.Error().Err(err).Msg("Failed to set MTU for transport interface")
	}

	if err := m.configureBatmanForceflood(ctx); err != nil {
		m.Log.Error().Err(err).Msg("Failed to configure batman-adv multicast forceflood")
	}

	if err := m.setupBatMesh1Interface(ctx); err != nil {
		m.Log.Error().Err(err).Msg("Failed to setup batmesh1 interface")
	}

	if err := m.reconcileBatMesh1Options(ctx); err != nil {
		m.Log.Error().Err(err).Msg("Failed to reconcile batmesh1 tuning options")
	}

	client, err := alfred.NewClient(alfred.WithSocketPath(m.SocketPath))
	if err != nil {
		m.Log.Fatal().Err(err).Msg("Failed to create Alfred client")
	}

	m.alfredClient = client

	m.Log.Info().Msg("Alfred Client Started")

	if m.AddressReservationDataType {
		addressReservationWorker := NewAddressReservationWorker(m, client, ctx)
		go addressReservationWorker.ReserveAddressIfNeeded(ctx)
	}

	if m.NodeDataType {
		// Start the node data worker
		nodeDataWorker := NewNodeDataWorker(m, client, nodeDataWorkerInterval, ctx)
		go nodeDataWorker.StartSend()
		go nodeDataWorker.StartReceive()
	}

	if m.GatewayDataType {
		// Start the gateway worker
		gatewayDataWorker := NewGatewayWorker(m, client, ctx)
		go gatewayDataWorker.StartSend()
		go gatewayDataWorker.StartReceive()
	}

	if m.MeshNeighborsDataType {
		meshNeighborsWorker := NewMeshNeighborsWorker(m, client, meshNeighborsWorkerInterval, ctx)
		go meshNeighborsWorker.StartSend()
	}
}

// Client returns the shared alfred client created during Start. Callers
// outside the mgmt package (e.g. the mesh-neighbors snapshotter) use
// this to consume gossip without opening a second connection. Returns
// nil before Start has run or when Alfred is disabled.
func (m *ManagementConfig) Client() *alfred.Client {
	return m.alfredClient
}
