package mgmt

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/openmanet/go-alfred"
	proto "github.com/openmanet/openmanetd/internal/api/openmanet/network/v1"
	"github.com/openmanet/openmanetd/internal/database/models"
	"github.com/openmanet/openmanetd/internal/network"
)

// alfredClient is an alias for alfred.ReadWriteClient so in-package code
// can keep using the short name while external tests and consumers depend
// on the exported interface in the alfred package.
type alfredClient = alfred.ReadWriteClient

const (
	NodeDataType        uint8 = uint8(proto.DataType_DATA_TYPE_NODE)
	NodeDataTypeVersion uint8 = 1
)

type NodeDataWorker struct {
	Config   *ManagementConfig
	Client   *alfred.Client
	done     <-chan struct{}
	Interval time.Duration
}

func NewNodeDataWorker(config *ManagementConfig, client *alfred.Client, interval time.Duration, ctx context.Context) *NodeDataWorker {
	config.Log.Info().Msg("NodeDataWorker initialized")

	return &NodeDataWorker{
		Config:   config,
		Client:   client,
		Interval: interval,
		done:     ctx.Done(),
	}
}

// Start begins the periodic sending of node data to the Alfred client.
func (ndw *NodeDataWorker) StartSend() { //nolint:gocognit
	ticker := time.NewTicker(ndw.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ndw.done:
			return
		case <-ticker.C:
			if err := ndw.sendNodeDataOnce(ndw.Client); err != nil {
				ndw.Config.Log.Error().Err(err).Msg("Error in node data send tick")
			}
		}
	}
}

// sendNodeDataOnce performs a single iteration of the node data send logic.
// It checks DHCP configuration, gathers interface/hostname/GPS data, and
// publishes the result via the provided alfred client.
func (ndw *NodeDataWorker) sendNodeDataOnce(client alfredClient) error {
	return ndw.sendNodeDataOnceWithDeps(client, ndw.Config.uciOpenMANETConfig, ndw.Config.uciDHCPConfig,
		network.GetInterfaceByName, os.Hostname)
}

// sendNodeDataOnceWithDeps is the dependency-injectable version of sendNodeDataOnce.
func (ndw *NodeDataWorker) sendNodeDataOnceWithDeps(
	client alfredClient,
	openMANETReader network.OpenMANETConfigReader,
	dhcpReader network.DHCPConfigReader,
	getIface func(string) network.NetworkInterface,
	getHostname func() (string, error),
) error {
	configured, err := network.IsDHCPConfiguredWithReader(openMANETReader)
	if err != nil {
		return fmt.Errorf("check DHCP configuration: %w", err)
	}

	if !configured {
		ndw.Config.Log.Debug().Msg("Static Address & DHCP not configured, skipping node data send")

		return nil
	}

	// Get interface information
	iface := getIface(ndw.Config.IFace)

	hostname, err := getHostname()
	if err != nil {
		ndw.Config.Log.Error().Err(err).Msg("Error getting hostname")

		hostname = "unknown"
	}

	// if ndw.Config.IFace is prefixed with "br-", remove the prefix because dhcp and network config is tied to the physical interface
	normalizedIface := ndw.Config.IFace
	if after, ok := strings.CutPrefix(ndw.Config.IFace, "br-"); ok {
		normalizedIface = after
	}

	// Get DHCP info from UCI
	dhcp, err := network.GetDHCPConfigWithReader(normalizedIface, dhcpReader)
	if err != nil {
		return fmt.Errorf("get DHCP configuration: %w", err)
	}

	if len(iface.IP) == 0 {
		return fmt.Errorf("interface %s has no IP address", ndw.Config.IFace)
	}

	nodeData := proto.Node{
		Mac:          iface.MAC,
		Hostname:     hostname,
		Ipaddr:       iface.IP[0].IP.String(),
		UciDhcpStart: dhcp.Start,
		UciDhcpLimit: dhcp.Limit,
	}

	// Get position data if GPS is available
	if ndw.Config.GPS != nil {
		positionReport := ndw.Config.GPS.GetPosition()
		if positionReport.Mode <= 1 {
			ndw.Config.Log.Debug().Msg("No valid GPS position available")
		}

		if positionReport.Mode > 1 {
			ndw.Config.Log.Debug().
				Float64("lat", positionReport.Latitude).
				Float64("lon", positionReport.Longitude).
				Float64("alt", positionReport.Altitude).
				Uint8("mode", uint8(positionReport.Mode)).
				Msg("Current GPS position")

			nodeData.Position = &proto.Position{
				Latitude:  positionReport.Latitude,
				Longitude: positionReport.Longitude,
				Altitude:  float32(positionReport.Altitude),
			}
		}
	} else {
		ndw.Config.Log.Debug().Msg("GPS service not available")
	}

	nodeDataBytes, err := nodeData.MarshalVT()
	if err != nil {
		return fmt.Errorf("marshal node data: %w", err)
	}

	if err := client.Set(NodeDataType, NodeDataTypeVersion, nodeDataBytes); err != nil {
		return fmt.Errorf("send node data: %w", err)
	}

	return nil
}

func (ndw *NodeDataWorker) StartReceive() {
	ticker := time.NewTicker(ndw.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ndw.done:
			return
		case <-ticker.C:
			if err := ndw.receiveNodeDataOnce(ndw.Client, os.Hostname); err != nil {
				ndw.Config.Log.Error().Err(err).Msg("Error in node data receive tick")
			}
		}
	}
}

// receiveNodeDataOnce performs a single receive iteration for the worker
// loop. The logic lives on ManagementConfig so the address-reservation
// worker can run the same refresh before it decides.
func (ndw *NodeDataWorker) receiveNodeDataOnce(client alfredClient, getHostname func() (string, error)) error {
	return ndw.Config.receiveNodeData(context.Background(), client, getHostname)
}

// RecordNodeData persists node information to the database by creating or
// updating a mesh node record. See ManagementConfig.recordNodeData.
func (ndw *NodeDataWorker) RecordNodeData(nodeData *proto.Node) error {
	return ndw.Config.recordNodeData(context.Background(), nodeData)
}

// receiveNodeData requests node gossip from Alfred and upserts every record
// except this node's own into mesh_nodes. It is shared by the NodeDataWorker
// receive loop (every 60 s) and by the AddressReservationWorker, which calls
// it immediately before each decision so the decision never acts on rows
// older than the last receive tick.
func (m *ManagementConfig) receiveNodeData(ctx context.Context, client alfredClient, getHostname func() (string, error)) error {
	records, err := client.Request(NodeDataType)
	if err != nil {
		return fmt.Errorf("request node data: %w", err)
	}

	hostname, err := getHostname()
	if err != nil {
		m.Log.Error().Err(err).Msg("Error getting hostname")
	}

	for _, rec := range records {
		var nodeData proto.Node

		if unmarshalErr := nodeData.UnmarshalVT(rec.Data); unmarshalErr != nil {
			m.Log.Error().Err(unmarshalErr).Msg("Error unmarshaling node data")

			continue
		}

		// ignore our own node data
		if nodeData.Hostname == hostname {
			continue
		}

		m.Log.Debug().Msgf("Received node data: %+v", &nodeData)

		if recordErr := m.recordNodeData(ctx, &nodeData); recordErr != nil {
			m.Log.Error().Err(recordErr).Msg("Error recording node data")
		}
	}

	if expireErr := m.expireStaleNodes(ctx); expireErr != nil {
		m.Log.Error().Err(expireErr).Msg("Error expiring stale mesh nodes")
	}

	return nil
}

// expireStaleNodes drops peers not heard from within NodeExpiry so their
// address and DHCP window stop counting as reserved. It runs after the
// upserts of the same receive, so a peer that is still gossiping is always
// refreshed before it could be swept. No-op when expiry is disabled.
//
// The cutoff must be UTC: SQLite's CURRENT_TIMESTAMP writes UTC text and
// go-sqlite3 binds time.Time as text in the same layout, so the comparison
// is lexicographic and a local-zone cutoff would be off by the UTC offset.
//
// receiveNodeData returns early on a client.Request error, before this call,
// so the sweep never runs during an Alfred outage — a transient outage
// cannot mass-expire every peer at once.
func (m *ManagementConfig) expireStaleNodes(ctx context.Context) error {
	if m.NodeExpiry <= 0 || m.DB == nil {
		return nil
	}

	cutoff := time.Now().UTC().Add(-m.NodeExpiry)

	if err := m.DB.DeleteMeshNodesUpdatedBefore(ctx, cutoff); err != nil {
		return fmt.Errorf("expire mesh nodes older than %s: %w", m.NodeExpiry, err)
	}

	return nil
}

// recordNodeData persists node information to the database by creating or
// updating a mesh node record. It converts the protobuf Node data into
// database model parameters, handling optional DHCP configuration fields
// (UciDhcpStart and UciDhcpLimit) by parsing them into nullable int64
// values. Any parsing errors are logged and returned immediately. If
// database insertion fails, the error is logged but not returned, allowing
// the function to complete successfully despite database errors.
func (m *ManagementConfig) recordNodeData(ctx context.Context, nodeData *proto.Node) error { //nolint:gocognit
	var dhcpStart, dhcpLimit sql.NullInt64

	if nodeData.UciDhcpStart != "" {
		start, err := strconv.ParseInt(nodeData.UciDhcpStart, 10, 64)
		if err != nil {
			m.Log.Error().Err(err).Msg("Error parsing UciDhcpStart")

			return fmt.Errorf("parse UciDhcpStart: %w", err)
		}

		dhcpStart = sql.NullInt64{Int64: start, Valid: true}
	}

	if nodeData.UciDhcpLimit != "" {
		limit, err := strconv.ParseInt(nodeData.UciDhcpLimit, 10, 64)
		if err != nil {
			m.Log.Error().Err(err).Msg("Error parsing UciDhcpLimit")

			return fmt.Errorf("parse UciDhcpLimit: %w", err)
		}

		dhcpLimit = sql.NullInt64{Int64: limit, Valid: true}
	}

	// Insert or update node data in the database
	_, err := m.DB.CreateMeshNode(ctx, models.CreateMeshNodeParams{
		MacAddr:      nodeData.Mac,
		IpAddr:       nodeData.Ipaddr,
		Hostname:     nodeData.Hostname,
		Latitude:     sql.NullFloat64{Float64: nodeData.Position.GetLatitude(), Valid: nodeData.Position != nil},
		Longitude:    sql.NullFloat64{Float64: nodeData.Position.GetLongitude(), Valid: nodeData.Position != nil},
		Altitude:     sql.NullFloat64{Float64: float64(nodeData.Position.GetAltitude()), Valid: nodeData.Position != nil},
		UciDhcpStart: dhcpStart,
		UciDhcpLimit: dhcpLimit,
	})
	if err != nil {
		m.Log.Error().Err(err).Msg("Error inserting node data into database")
	}

	// Delete duplicate entries if any (should not happen due to unique constraint)
	err = m.DB.DeleteDuplicateMeshNodes(ctx)
	if err != nil {
		m.Log.Error().Err(err).Msg("Error deleting duplicate mesh nodes")
	}

	return nil
}
