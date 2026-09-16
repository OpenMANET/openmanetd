package mgmt

import (
	"context"
	"database/sql"
	"net"
	"testing"
	"time"

	"github.com/digineo/go-uci/v2"
	_ "github.com/mattn/go-sqlite3"
	"github.com/openmanet/go-alfred"
	proto "github.com/openmanet/openmanetd/internal/api/openmanet/network/v1"
	"github.com/openmanet/openmanetd/internal/database/models"
	"github.com/openmanet/openmanetd/internal/network"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── fakeAlfredClient ─────────────────────────────────────────────────────────

type fakeAlfredClient struct {
	setErr     error
	setCalls   int
	lastData   []byte
	requestErr error
	records    []alfred.Record
}

func (f *fakeAlfredClient) Set(_ uint8, _ uint8, data []byte) error {
	f.setCalls++
	f.lastData = data

	return f.setErr
}

func (f *fakeAlfredClient) Request(_ uint8) ([]alfred.Record, error) {
	return f.records, f.requestErr
}

// ── fakeDHCPReader ───────────────────────────────────────────────────────────

type fakeDHCPReader struct {
	data     map[string]map[string]map[string][]string
	sections map[string]map[string]string
}

func newFakeDHCPReader() *fakeDHCPReader {
	return &fakeDHCPReader{
		data:     make(map[string]map[string]map[string][]string),
		sections: make(map[string]map[string]string),
	}
}

func (f *fakeDHCPReader) Get(config, section, option string) ([]string, bool) {
	if f.data[config] == nil || f.data[config][section] == nil {
		return nil, false
	}

	v, ok := f.data[config][section][option]

	return v, ok
}

func (f *fakeDHCPReader) GetSections(config, secType string) ([]string, error) {
	var out []string

	if f.sections[config] != nil {
		for s, t := range f.sections[config] {
			if t == secType {
				out = append(out, s)
			}
		}
	}

	return out, nil
}

func (f *fakeDHCPReader) SetType(config, section, option string, _ uci.OptionType, values ...string) error {
	if f.data[config] == nil {
		f.data[config] = make(map[string]map[string][]string)
	}

	if f.data[config][section] == nil {
		f.data[config][section] = make(map[string][]string)
	}

	f.data[config][section][option] = values

	return nil
}

func (f *fakeDHCPReader) Del(config, section, option string) error {
	if f.data[config] != nil && f.data[config][section] != nil {
		delete(f.data[config][section], option)
	}

	return nil
}

func (f *fakeDHCPReader) AddSection(config, section, typ string) error {
	if f.sections[config] == nil {
		f.sections[config] = make(map[string]string)
	}

	f.sections[config][section] = typ

	if f.data[config] == nil {
		f.data[config] = make(map[string]map[string][]string)
	}

	if f.data[config][section] == nil {
		f.data[config][section] = make(map[string][]string)
	}

	return nil
}

func (f *fakeDHCPReader) DelSection(config, section string) error {
	if f.data[config] != nil {
		delete(f.data[config], section)
	}

	if f.sections[config] != nil {
		delete(f.sections[config], section)
	}

	return nil
}

func (f *fakeDHCPReader) Commit() error       { return nil }
func (f *fakeDHCPReader) ReloadConfig() error { return nil }

// seedDHCP seeds a DHCP section with start/limit values.
func (f *fakeDHCPReader) seedDHCP(section, start, limit string) {
	_ = f.AddSection("dhcp", section, "dhcp")
	_ = f.SetType("dhcp", section, "interface", uci.TypeOption, section)
	_ = f.SetType("dhcp", section, "start", uci.TypeOption, start)
	_ = f.SetType("dhcp", section, "limit", uci.TypeOption, limit)
}

// seedDHCPConfigured seeds the openmanetd config so IsDHCPConfiguredWithReader returns true.
func seedDHCPConfigured(r *fakeOpenMANETReader) {
	_ = r.AddSection("openmanetd", "config", "openmanet")
	_ = r.SetType("openmanetd", "config", "dhcpconfigured", uci.TypeOption, "1")
}

// makeInterface creates a fake NetworkInterface for testing.
func makeTestIface(mac, ip string) network.NetworkInterface {
	return network.NetworkInterface{
		Name: "br-ahwlan",
		MAC:  mac,
		IP: []network.IPAddress{
			{IP: net.ParseIP(ip)},
		},
	}
}

const nodeTestSchemaSQL = `
CREATE TABLE IF NOT EXISTS mesh_nodes (
  mac_addr       text PRIMARY KEY NOT NULL,
  hostname       text NOT NULL,
  ip_addr        text NOT NULL,
  latitude       real,
  longitude      real,
  altitude       real,
  uci_dhcp_start integer,
  uci_dhcp_limit integer,
  created_at     timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at     timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP
);`

// newNodeTestDBConn opens an in-memory SQLite database with the mesh_nodes
// schema and returns both the raw connection (for seeding rows with
// explicit timestamps) and the sqlc queries.
func newNodeTestDBConn(t *testing.T) (*sql.DB, *models.Queries) {
	t.Helper()

	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}

	t.Cleanup(func() { db.Close() })

	if _, err = db.Exec(nodeTestSchemaSQL); err != nil {
		t.Fatalf("apply schema: %v", err)
	}

	return db, models.New(db)
}

func newNodeTestDB(t *testing.T) *models.Queries {
	t.Helper()

	_, q := newNodeTestDBConn(t)

	return q
}

// seedNodeUpdatedAgo inserts a peer row whose updated_at is `ago` relative
// to SQLite's clock, e.g. "-25 hours", in the same UTC text format
// CURRENT_TIMESTAMP writes in production.
func seedNodeUpdatedAgo(t *testing.T, db *sql.DB, mac, ip, ago string) {
	t.Helper()

	_, err := db.ExecContext(context.Background(),
		`INSERT INTO mesh_nodes (mac_addr, hostname, ip_addr, uci_dhcp_start, uci_dhcp_limit, created_at, updated_at)
		 VALUES (?, ?, ?, 100, 16, datetime('now', ?), datetime('now', ?))`,
		mac, "peer-"+mac, ip, ago, ago,
	)
	require.NoError(t, err)
}

func TestRecordNodeData(t *testing.T) {
	tests := []struct {
		name    string
		node    *proto.Node
		wantErr string
		checkDB func(t *testing.T, q *models.Queries)
	}{
		{
			name: "valid node with position",
			node: &proto.Node{
				Mac:          "aa:bb:cc:dd:ee:ff",
				Hostname:     "node-1",
				Ipaddr:       "10.0.0.1",
				UciDhcpStart: "100",
				UciDhcpLimit: "150",
				Position: &proto.Position{
					Latitude:  37.7749,
					Longitude: -122.4194,
					Altitude:  50.5,
				},
			},
			checkDB: func(t *testing.T, q *models.Queries) {
				t.Helper()

				node, err := q.GetMeshNode(context.Background(), "aa:bb:cc:dd:ee:ff")
				require.NoError(t, err)
				assert.Equal(t, "node-1", node.Hostname)
				assert.Equal(t, "10.0.0.1", node.IpAddr)
				assert.Equal(t, sql.NullFloat64{Float64: 37.7749, Valid: true}, node.Latitude)
				assert.Equal(t, sql.NullFloat64{Float64: -122.4194, Valid: true}, node.Longitude)
				assert.InDelta(t, 50.5, node.Altitude.Float64, 0.01)
				assert.True(t, node.Altitude.Valid)
				assert.Equal(t, sql.NullInt64{Int64: 100, Valid: true}, node.UciDhcpStart)
				assert.Equal(t, sql.NullInt64{Int64: 150, Valid: true}, node.UciDhcpLimit)
			},
		},
		{
			name: "valid node without position",
			node: &proto.Node{
				Mac:          "11:22:33:44:55:66",
				Hostname:     "node-2",
				Ipaddr:       "10.0.0.2",
				UciDhcpStart: "10",
				UciDhcpLimit: "20",
			},
			checkDB: func(t *testing.T, q *models.Queries) {
				t.Helper()

				node, err := q.GetMeshNode(context.Background(), "11:22:33:44:55:66")
				require.NoError(t, err)
				assert.False(t, node.Latitude.Valid)
				assert.False(t, node.Longitude.Valid)
				assert.False(t, node.Altitude.Valid)
			},
		},
		{
			name: "empty DHCP fields",
			node: &proto.Node{
				Mac:      "de:ad:be:ef:00:01",
				Hostname: "node-3",
				Ipaddr:   "10.0.0.3",
			},
			checkDB: func(t *testing.T, q *models.Queries) {
				t.Helper()

				node, err := q.GetMeshNode(context.Background(), "de:ad:be:ef:00:01")
				require.NoError(t, err)
				assert.False(t, node.UciDhcpStart.Valid)
				assert.False(t, node.UciDhcpLimit.Valid)
			},
		},
		{
			name: "invalid UciDhcpStart",
			node: &proto.Node{
				Mac:          "ff:ff:ff:ff:ff:01",
				Hostname:     "bad-start",
				Ipaddr:       "10.0.0.4",
				UciDhcpStart: "abc",
			},
			wantErr: "parse UciDhcpStart",
		},
		{
			name: "invalid UciDhcpLimit",
			node: &proto.Node{
				Mac:          "ff:ff:ff:ff:ff:02",
				Hostname:     "bad-limit",
				Ipaddr:       "10.0.0.5",
				UciDhcpStart: "10",
				UciDhcpLimit: "xyz",
			},
			wantErr: "parse UciDhcpLimit",
		},
		{
			name: "upsert overwrites existing node",
			node: &proto.Node{
				Mac:      "aa:bb:cc:dd:ee:ff",
				Hostname: "node-1-updated",
				Ipaddr:   "10.0.0.99",
			},
			checkDB: func(t *testing.T, q *models.Queries) {
				t.Helper()

				node, err := q.GetMeshNode(context.Background(), "aa:bb:cc:dd:ee:ff")
				require.NoError(t, err)
				assert.Equal(t, "node-1-updated", node.Hostname)
				assert.Equal(t, "10.0.0.99", node.IpAddr)
			},
		},
	}

	// Use a shared DB so the upsert test can build on the first insert.
	db := newNodeTestDB(t)
	cfg := newTestManagementConfig()
	cfg.DB = db

	ndw := &NodeDataWorker{
		Config:   cfg,
		Interval: time.Second,
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ndw.RecordNodeData(tt.node)

			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)

				return
			}

			require.NoError(t, err)

			if tt.checkDB != nil {
				tt.checkDB(t, db)
			}
		})
	}
}

// ── sendNodeDataOnceWithDeps tests ──────────────────────────────────────────

func TestSendNodeDataOnce_DHCPNotConfigured(t *testing.T) {
	cfg := newTestManagementConfig()
	cfg.IFace = "br-ahwlan"
	ndw := &NodeDataWorker{Config: cfg, Interval: time.Second}

	openmanet := newFakeOpenMANETReader()
	// dhcpconfigured is "0" by default (not seeded)

	client := &fakeAlfredClient{}
	dhcp := newFakeDHCPReader()

	err := ndw.sendNodeDataOnceWithDeps(client, openmanet, dhcp,
		func(_ string) network.NetworkInterface { return network.NetworkInterface{} },
		func() (string, error) { return "test-host", nil },
	)
	require.NoError(t, err)
	assert.Equal(t, 0, client.setCalls, "should not send when DHCP not configured")
}

func TestSendNodeDataOnce_Success(t *testing.T) {
	cfg := newTestManagementConfig()
	cfg.IFace = "br-ahwlan"
	ndw := &NodeDataWorker{Config: cfg, Interval: time.Second}

	openmanet := newFakeOpenMANETReader()
	seedDHCPConfigured(openmanet)

	dhcp := newFakeDHCPReader()
	dhcp.seedDHCP("ahwlan", "100", "150")

	iface := makeTestIface("aa:bb:cc:dd:ee:ff", "10.0.0.1")
	client := &fakeAlfredClient{}

	err := ndw.sendNodeDataOnceWithDeps(client, openmanet, dhcp,
		func(_ string) network.NetworkInterface { return iface },
		func() (string, error) { return "test-host", nil },
	)
	require.NoError(t, err)
	assert.Equal(t, 1, client.setCalls)

	// Verify the sent data can be decoded
	var sent proto.Node
	require.NoError(t, sent.UnmarshalVT(client.lastData))
	assert.Equal(t, "aa:bb:cc:dd:ee:ff", sent.Mac)
	assert.Equal(t, "test-host", sent.Hostname)
	assert.Equal(t, "10.0.0.1", sent.Ipaddr)
	assert.Equal(t, "100", sent.UciDhcpStart)
	assert.Equal(t, "150", sent.UciDhcpLimit)
	assert.Nil(t, sent.Position, "no GPS configured, position should be nil")
}

func TestSendNodeDataOnce_BridgeInterfaceNormalized(t *testing.T) {
	cfg := newTestManagementConfig()
	cfg.IFace = "br-ahwlan"
	ndw := &NodeDataWorker{Config: cfg, Interval: time.Second}

	openmanet := newFakeOpenMANETReader()
	seedDHCPConfigured(openmanet)

	// The DHCP config is keyed by "ahwlan" (without br- prefix)
	dhcp := newFakeDHCPReader()
	dhcp.seedDHCP("ahwlan", "50", "25")

	iface := makeTestIface("11:22:33:44:55:66", "10.0.0.2")
	client := &fakeAlfredClient{}

	err := ndw.sendNodeDataOnceWithDeps(client, openmanet, dhcp,
		func(_ string) network.NetworkInterface { return iface },
		func() (string, error) { return "host", nil },
	)
	require.NoError(t, err)
	assert.Equal(t, 1, client.setCalls)
}

func TestSendNodeDataOnce_NoIPAddress(t *testing.T) {
	cfg := newTestManagementConfig()
	cfg.IFace = "br-ahwlan"
	ndw := &NodeDataWorker{Config: cfg, Interval: time.Second}

	openmanet := newFakeOpenMANETReader()
	seedDHCPConfigured(openmanet)

	dhcp := newFakeDHCPReader()
	dhcp.seedDHCP("ahwlan", "100", "150")

	// Interface with no IP addresses
	emptyIface := network.NetworkInterface{Name: "br-ahwlan", MAC: "aa:bb:cc:dd:ee:ff"}
	client := &fakeAlfredClient{}

	err := ndw.sendNodeDataOnceWithDeps(client, openmanet, dhcp,
		func(_ string) network.NetworkInterface { return emptyIface },
		func() (string, error) { return "host", nil },
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no IP address")
	assert.Equal(t, 0, client.setCalls)
}

func TestSendNodeDataOnce_HostnameError(t *testing.T) {
	cfg := newTestManagementConfig()
	cfg.IFace = "br-ahwlan"
	ndw := &NodeDataWorker{Config: cfg, Interval: time.Second}

	openmanet := newFakeOpenMANETReader()
	seedDHCPConfigured(openmanet)

	dhcp := newFakeDHCPReader()
	dhcp.seedDHCP("ahwlan", "100", "150")

	iface := makeTestIface("aa:bb:cc:dd:ee:ff", "10.0.0.1")
	client := &fakeAlfredClient{}

	err := ndw.sendNodeDataOnceWithDeps(client, openmanet, dhcp,
		func(_ string) network.NetworkInterface { return iface },
		func() (string, error) { return "", assert.AnError },
	)
	require.NoError(t, err)
	assert.Equal(t, 1, client.setCalls)

	// When hostname fails, it should use "unknown"
	var sent proto.Node
	require.NoError(t, sent.UnmarshalVT(client.lastData))
	assert.Equal(t, "unknown", sent.Hostname)
}

func TestSendNodeDataOnce_AlfredSetError(t *testing.T) {
	cfg := newTestManagementConfig()
	cfg.IFace = "br-ahwlan"
	ndw := &NodeDataWorker{Config: cfg, Interval: time.Second}

	openmanet := newFakeOpenMANETReader()
	seedDHCPConfigured(openmanet)

	dhcp := newFakeDHCPReader()
	dhcp.seedDHCP("ahwlan", "100", "150")

	iface := makeTestIface("aa:bb:cc:dd:ee:ff", "10.0.0.1")
	client := &fakeAlfredClient{setErr: assert.AnError}

	err := ndw.sendNodeDataOnceWithDeps(client, openmanet, dhcp,
		func(_ string) network.NetworkInterface { return iface },
		func() (string, error) { return "host", nil },
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "send node data")
}

// ── receiveNodeDataOnce tests ───────────────────────────────────────────────

func TestReceiveNodeDataOnce_RequestError(t *testing.T) {
	cfg := newTestManagementConfig()
	ndw := &NodeDataWorker{Config: cfg, Interval: time.Second}

	client := &fakeAlfredClient{requestErr: assert.AnError}

	err := ndw.receiveNodeDataOnce(client, func() (string, error) { return "host", nil })
	require.Error(t, err)
	assert.Contains(t, err.Error(), "request node data")
}

func TestReceiveNodeDataOnce_FiltersOwnHostname(t *testing.T) {
	cfg := newTestManagementConfig()
	cfg.DB = newNodeTestDB(t)
	ndw := &NodeDataWorker{Config: cfg, Interval: time.Second}

	// Create a node record with the local hostname
	ownNode := &proto.Node{
		Mac:      "aa:bb:cc:dd:ee:ff",
		Hostname: "my-host",
		Ipaddr:   "10.0.0.1",
	}

	data, err := ownNode.MarshalVT()
	require.NoError(t, err)

	client := &fakeAlfredClient{
		records: []alfred.Record{{Data: data}},
	}

	err = ndw.receiveNodeDataOnce(client, func() (string, error) { return "my-host", nil })
	require.NoError(t, err)

	// DB should have no entries since own data is filtered
	nodes, err := cfg.DB.ListMeshNodes(context.Background())
	require.NoError(t, err)
	assert.Empty(t, nodes)
}

func TestReceiveNodeDataOnce_RecordsRemoteNode(t *testing.T) {
	cfg := newTestManagementConfig()
	cfg.DB = newNodeTestDB(t)
	ndw := &NodeDataWorker{Config: cfg, Interval: time.Second}

	remoteNode := &proto.Node{
		Mac:      "aa:bb:cc:dd:ee:ff",
		Hostname: "remote-node",
		Ipaddr:   "10.0.0.2",
	}

	data, err := remoteNode.MarshalVT()
	require.NoError(t, err)

	client := &fakeAlfredClient{
		records: []alfred.Record{{Data: data}},
	}

	err = ndw.receiveNodeDataOnce(client, func() (string, error) { return "my-host", nil })
	require.NoError(t, err)

	node, err := cfg.DB.GetMeshNode(context.Background(), "aa:bb:cc:dd:ee:ff")
	require.NoError(t, err)
	assert.Equal(t, "remote-node", node.Hostname)
	assert.Equal(t, "10.0.0.2", node.IpAddr)
}

func TestReceiveNodeDataOnce_InvalidProtobuf(t *testing.T) {
	cfg := newTestManagementConfig()
	cfg.DB = newNodeTestDB(t)
	ndw := &NodeDataWorker{Config: cfg, Interval: time.Second}

	client := &fakeAlfredClient{
		records: []alfred.Record{{Data: []byte("not-protobuf")}},
	}

	// Should not return an error — invalid records are logged and skipped
	err := ndw.receiveNodeDataOnce(client, func() (string, error) { return "my-host", nil })
	require.NoError(t, err)
}

func TestReceiveNodeDataOnce_EmptyRecords(t *testing.T) {
	cfg := newTestManagementConfig()
	ndw := &NodeDataWorker{Config: cfg, Interval: time.Second}

	client := &fakeAlfredClient{records: nil}

	err := ndw.receiveNodeDataOnce(client, func() (string, error) { return "host", nil })
	require.NoError(t, err)
}

// ── expiry (ledger P5 step 4, D4) ────────────────────────────────────────────

func TestReceiveNodeData_ExpiresStaleRows(t *testing.T) {
	cfg := newTestManagementConfig()
	cfg.NodeExpiry = 24 * time.Hour

	db, q := newNodeTestDBConn(t)
	cfg.DB = q

	seedNodeUpdatedAgo(t, db, "aa:bb:cc:dd:ee:01", "10.41.0.1", "-25 hours")
	seedNodeUpdatedAgo(t, db, "aa:bb:cc:dd:ee:02", "10.41.0.2", "-1 hours")

	err := cfg.receiveNodeData(context.Background(), &fakeAlfredClient{}, func() (string, error) { return "my-host", nil })
	require.NoError(t, err)

	nodes, err := q.ListMeshNodes(context.Background())
	require.NoError(t, err)
	require.Len(t, nodes, 1)
	assert.Equal(t, "aa:bb:cc:dd:ee:02", nodes[0].MacAddr, "the silent 25 h-old peer is dropped, the 1 h-old one kept")
}

func TestReceiveNodeData_HeardPeerSurvivesSweep(t *testing.T) {
	cfg := newTestManagementConfig()
	cfg.NodeExpiry = 24 * time.Hour

	db, q := newNodeTestDBConn(t)
	cfg.DB = q

	seedNodeUpdatedAgo(t, db, "aa:bb:cc:dd:ee:01", "10.41.0.1", "-25 hours")

	heard := &proto.Node{Mac: "aa:bb:cc:dd:ee:01", Hostname: "peer", Ipaddr: "10.41.0.1"}
	data, err := heard.MarshalVT()
	require.NoError(t, err)

	client := &fakeAlfredClient{records: []alfred.Record{{Data: data}}}

	err = cfg.receiveNodeData(context.Background(), client, func() (string, error) { return "my-host", nil })
	require.NoError(t, err)

	node, err := q.GetMeshNode(context.Background(), "aa:bb:cc:dd:ee:01")
	require.NoError(t, err, "a peer heard this tick is refreshed before the sweep and survives")
	assert.Equal(t, "10.41.0.1", node.IpAddr)
}

func TestReceiveNodeData_ExpiryDisabled(t *testing.T) {
	cfg := newTestManagementConfig()
	cfg.NodeExpiry = 0

	db, q := newNodeTestDBConn(t)
	cfg.DB = q

	seedNodeUpdatedAgo(t, db, "aa:bb:cc:dd:ee:01", "10.41.0.1", "-400 days")

	err := cfg.receiveNodeData(context.Background(), &fakeAlfredClient{}, func() (string, error) { return "my-host", nil })
	require.NoError(t, err)

	nodes, err := q.ListMeshNodes(context.Background())
	require.NoError(t, err)
	assert.Len(t, nodes, 1, "nodeExpiry 0 keeps rows forever, as before P5")
}

func TestReceiveNodeData_RequestErrorSkipsExpiry(t *testing.T) {
	cfg := newTestManagementConfig()
	cfg.NodeExpiry = 24 * time.Hour

	db, q := newNodeTestDBConn(t)
	cfg.DB = q

	seedNodeUpdatedAgo(t, db, "aa:bb:cc:dd:ee:01", "10.41.0.1", "-25 hours")

	client := &fakeAlfredClient{requestErr: assert.AnError}

	err := cfg.receiveNodeData(context.Background(), client, func() (string, error) { return "my-host", nil })
	require.Error(t, err)

	nodes, listErr := q.ListMeshNodes(context.Background())
	require.NoError(t, listErr)
	assert.Len(t, nodes, 1, "a failed Alfred Request must skip the sweep so no peer is expired during an outage")
}
