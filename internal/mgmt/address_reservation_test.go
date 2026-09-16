package mgmt

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/digineo/go-uci/v2"
	"github.com/openmanet/go-alfred"
	proto "github.com/openmanet/openmanetd/internal/api/openmanet/network/v1"
	batmanadv "github.com/openmanet/openmanetd/internal/batman-adv"
	"github.com/openmanet/openmanetd/internal/database/models"
	"github.com/openmanet/openmanetd/internal/network"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeNetworkReader implements network.ConfigReader for address reservation tests.
// It reuses the same map-based approach as fakeOpenMANETReader / fakeWirelessReader.
type fakeNetworkReader struct {
	data        map[string]map[string]map[string][]string
	sections    map[string]map[string]string
	commitErr   error
	delErr      error
	commitCalls int
}

func newFakeNetworkReader() *fakeNetworkReader {
	return &fakeNetworkReader{
		data:     make(map[string]map[string]map[string][]string),
		sections: make(map[string]map[string]string),
	}
}

func (f *fakeNetworkReader) Get(config, section, option string) ([]string, bool) {
	if f.data[config] == nil || f.data[config][section] == nil {
		return nil, false
	}

	v, ok := f.data[config][section][option]

	return v, ok
}

func (f *fakeNetworkReader) GetSections(config, secType string) ([]string, error) {
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

func (f *fakeNetworkReader) SetType(config, section, option string, _ uci.OptionType, values ...string) error {
	if f.data[config] == nil {
		f.data[config] = make(map[string]map[string][]string)
	}

	if f.data[config][section] == nil {
		f.data[config][section] = make(map[string][]string)
	}

	f.data[config][section][option] = values

	return nil
}

func (f *fakeNetworkReader) Del(config, section, option string) error {
	if f.data[config] != nil && f.data[config][section] != nil {
		delete(f.data[config][section], option)
	}

	return nil
}

func (f *fakeNetworkReader) AddSection(config, section, typ string) error {
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

func (f *fakeNetworkReader) DelSection(config, section string) error {
	if f.delErr != nil {
		return f.delErr
	}

	if f.data[config] != nil {
		delete(f.data[config], section)
	}

	if f.sections[config] != nil {
		delete(f.sections[config], section)
	}

	return nil
}

func (f *fakeNetworkReader) Commit() error {
	f.commitCalls++

	return f.commitErr
}

func (f *fakeNetworkReader) ReloadConfig() error {
	return nil
}

// reloadTrackingReader wraps the existing map-based reader solely to record
// reload calls. The mutex protects the fields below, allowing this fake to be
// used safely by future goroutine-driven worker tests.
type reloadTrackingReader struct {
	*fakeNetworkReader

	mu          sync.Mutex // protects reloadCalls and reloadErr below
	reloadCalls int
	reloadErr   error
}

func newReloadTrackingReader() *reloadTrackingReader {
	return &reloadTrackingReader{fakeNetworkReader: newFakeNetworkReader()}
}

func (r *reloadTrackingReader) ReloadConfig() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.reloadCalls++

	return r.reloadErr
}

func (r *reloadTrackingReader) calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.reloadCalls
}

func (r *reloadTrackingReader) setReloadError(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.reloadErr = err
}

// seedLanNetworkSection seeds a "lan" section so NetworkSectionExistsWithReader returns true.
func (f *fakeNetworkReader) seedLanNetworkSection() {
	_ = f.AddSection("network", "lan", "interface")
	_ = f.SetType("network", "lan", "proto", uci.TypeOption, "static")
}

// seedLanDHCPSection seeds a "lan" section so DHCPSectionExistsWithReader returns true.
func (f *fakeNetworkReader) seedLanDHCPSection() {
	_ = f.AddSection("dhcp", "lan", "dhcp")
	_ = f.SetType("dhcp", "lan", "interface", uci.TypeOption, "lan")
}

func noopReload(_ context.Context) error {
	return nil
}

func TestAddressReservationReloadsAllUCIConfigs(t *testing.T) {
	openmanetReader := newReloadTrackingReader()
	networkReader := newReloadTrackingReader()
	dhcpReader := newReloadTrackingReader()
	arw := &AddressReservationWorker{Config: &ManagementConfig{}, uciOpenMANETConfig: openmanetReader, uciNetworkConfig: networkReader, uciDHCPConfig: dhcpReader}

	require.NoError(t, arw.reloadUCIConfigs())
	deps := arw.productionDeps()
	assert.Same(t, openmanetReader, deps.openMANETReader)
	assert.Same(t, networkReader, deps.networkReader)
	assert.Same(t, dhcpReader, deps.dhcpReader)
	assert.Equal(t, 1, openmanetReader.calls())
	assert.Equal(t, 1, networkReader.calls())
	assert.Equal(t, 1, dhcpReader.calls())
}

func TestAddressReservationStopsWhenUCIReloadFails(t *testing.T) {
	openmanetReader := newReloadTrackingReader()
	networkReader := newReloadTrackingReader()
	dhcpReader := newReloadTrackingReader()

	networkReader.setReloadError(errors.New("network reload failed"))
	arw := &AddressReservationWorker{Config: &ManagementConfig{}, uciOpenMANETConfig: openmanetReader, uciNetworkConfig: networkReader, uciDHCPConfig: dhcpReader}

	err := arw.reloadUCIConfigs()
	require.Error(t, err)
	assert.ErrorContains(t, err, "reload network")
	assert.Equal(t, 1, openmanetReader.calls())
	assert.Equal(t, 1, networkReader.calls())
	assert.Equal(t, 0, dhcpReader.calls())
}

func TestCleanUpInterfacesWithDeps_GatewayModeSkips(t *testing.T) {
	cfg := newTestManagementConfig()
	arw := &AddressReservationWorker{Config: cfg}

	networkReader := newFakeNetworkReader()
	dhcpReader := newFakeNetworkReader()

	err := arw.cleanUpInterfacesWithDeps(true, networkReader, dhcpReader, noopReload)
	require.NoError(t, err)

	assert.Equal(t, 0, networkReader.commitCalls, "no network commits expected in gateway mode")
	assert.Equal(t, 0, dhcpReader.commitCalls, "no DHCP commits expected in gateway mode")
}

func TestCleanUpInterfacesWithDeps_LanSectionsExist(t *testing.T) {
	cfg := newTestManagementConfig()
	arw := &AddressReservationWorker{Config: cfg}

	networkReader := newFakeNetworkReader()
	networkReader.seedLanNetworkSection()

	dhcpReader := newFakeNetworkReader()
	dhcpReader.seedLanDHCPSection()

	err := arw.cleanUpInterfacesWithDeps(false, networkReader, dhcpReader, noopReload)
	require.NoError(t, err)

	// Verify lan sections were deleted.
	_, networkExists := networkReader.Get("network", "lan", "proto")
	assert.False(t, networkExists, "lan network section should be deleted")

	_, dhcpExists := dhcpReader.Get("dhcp", "lan", "interface")
	assert.False(t, dhcpExists, "lan DHCP section should be deleted")

	// DeleteNetworkConfigWithReader calls Commit internally, plus cleanUpInterfacesWithDeps
	// calls Commit again. So we expect 2 commits per reader.
	assert.Equal(t, 2, networkReader.commitCalls)
	assert.Equal(t, 2, dhcpReader.commitCalls)
}

func TestCleanUpInterfacesWithDeps_NoLanSections(t *testing.T) {
	cfg := newTestManagementConfig()
	arw := &AddressReservationWorker{Config: cfg}

	networkReader := newFakeNetworkReader()
	dhcpReader := newFakeNetworkReader()

	err := arw.cleanUpInterfacesWithDeps(false, networkReader, dhcpReader, noopReload)
	require.NoError(t, err)

	// Commits still happen even when no sections to delete.
	assert.Equal(t, 1, networkReader.commitCalls)
	assert.Equal(t, 1, dhcpReader.commitCalls)
}

func TestCleanUpInterfacesWithDeps_NetworkCommitError(t *testing.T) {
	cfg := newTestManagementConfig()
	arw := &AddressReservationWorker{Config: cfg}

	networkReader := newFakeNetworkReader()
	networkReader.commitErr = errors.New("network commit failure")

	dhcpReader := newFakeNetworkReader()

	err := arw.cleanUpInterfacesWithDeps(false, networkReader, dhcpReader, noopReload)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "error committing network config")
}

func TestCleanUpInterfacesWithDeps_DHCPCommitError(t *testing.T) {
	cfg := newTestManagementConfig()
	arw := &AddressReservationWorker{Config: cfg}

	networkReader := newFakeNetworkReader()
	dhcpReader := newFakeNetworkReader()
	dhcpReader.commitErr = errors.New("dhcp commit failure")

	err := arw.cleanUpInterfacesWithDeps(false, networkReader, dhcpReader, noopReload)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "error committing DHCP config")
}

func TestCleanUpInterfacesWithDeps_NetworkDeleteError(t *testing.T) {
	cfg := newTestManagementConfig()
	arw := &AddressReservationWorker{Config: cfg}

	networkReader := newFakeNetworkReader()
	networkReader.seedLanNetworkSection()
	networkReader.delErr = errors.New("delete failed")

	dhcpReader := newFakeNetworkReader()

	err := arw.cleanUpInterfacesWithDeps(false, networkReader, dhcpReader, noopReload)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "error deleting 'lan' network section")
}

func TestCleanUpInterfacesWithDeps_ReloadError(t *testing.T) {
	cfg := newTestManagementConfig()
	arw := &AddressReservationWorker{Config: cfg}

	networkReader := newFakeNetworkReader()
	dhcpReader := newFakeNetworkReader()

	reloadErr := func(_ context.Context) error {
		return errors.New("reload failed")
	}

	err := arw.cleanUpInterfacesWithDeps(false, networkReader, dhcpReader, reloadErr)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "error reloading network configuration")
}

// ── reserveOnceWithDeps fixture ──────────────────────────────────────────────

// reservationFixture wires an AddressReservationWorker to in-memory fakes so a
// single tick can run without batctl, netlink, UCI files, or a reboot.
type reservationFixture struct {
	arw       *AddressReservationWorker
	db        *models.Queries
	openmanet *fakeOpenMANETReader
	network   *fakeNetworkReader
	dhcp      *fakeNetworkReader
	client    *fakeAlfredClient
	iface     network.NetworkInterface
	gwMode    string
	meshErr   error
	rebootErr error
	reboots   int
	reloads   int
}

func newReservationFixture(t *testing.T) *reservationFixture {
	t.Helper()

	cfg := newTestManagementConfig()
	cfg.IFace = "br-ahwlan"
	cfg.BatInterface = "bat0"
	cfg.DB = newNodeTestDB(t)

	return &reservationFixture{
		arw:       &AddressReservationWorker{Config: cfg},
		db:        cfg.DB,
		openmanet: newFakeOpenMANETReader(),
		network:   newFakeNetworkReader(),
		dhcp:      newFakeNetworkReader(),
		client:    &fakeAlfredClient{},
		iface:     makeTestIface("aa:bb:cc:dd:ee:01", "10.41.254.7"),
		gwMode:    batmanadv.GwModeClient,
	}
}

func (f *reservationFixture) deps() reservationDeps {
	return reservationDeps{
		openMANETReader: f.openmanet,
		networkReader:   f.network,
		dhcpReader:      f.dhcp,
		client:          f.client,
		getHostname:     func() (string, error) { return "this-node", nil },
		getIface:        func(string) network.NetworkInterface { return f.iface },
		getMeshConfig: func(string) (*batmanadv.MeshConfig, error) {
			if f.meshErr != nil {
				return nil, f.meshErr
			}

			return &batmanadv.MeshConfig{GwMode: f.gwMode}, nil
		},
		reloadNetwork: func(context.Context) error {
			f.reloads++

			return nil
		},
		reboot: func() error {
			f.reboots++

			return f.rebootErr
		},
	}
}

func (f *reservationFixture) tick(t *testing.T) error {
	t.Helper()

	return f.arw.reserveOnceWithDeps(context.Background(), f.deps())
}

// seedPeer inserts a peer row as the node-data receive path would. dhcpStart
// 0 leaves the DHCP window NULL (a peer that advertises no pool).
func (f *reservationFixture) seedPeer(t *testing.T, mac, ip string, dhcpStart int64) {
	t.Helper()

	_, err := f.db.CreateMeshNode(context.Background(), models.CreateMeshNodeParams{
		MacAddr:      mac,
		Hostname:     "peer-" + mac,
		IpAddr:       ip,
		UciDhcpStart: sql.NullInt64{Int64: dhcpStart, Valid: dhcpStart > 0},
		UciDhcpLimit: sql.NullInt64{Int64: int64(network.DefaultDHCPAddressLimit), Valid: dhcpStart > 0},
	})
	require.NoError(t, err)
}

func firstValue(r *fakeNetworkReader, config, section, option string) string {
	values, ok := r.Get(config, section, option)
	if !ok || len(values) == 0 {
		return ""
	}

	return values[0]
}

func (f *reservationFixture) ahwlanIP() string {
	return firstValue(f.network, "network", "ahwlan", "ipaddr")
}

func (f *reservationFixture) dhcpStart() string { return firstValue(f.dhcp, "dhcp", "ahwlan", "start") }

func (f *reservationFixture) dhcpConfigured() string {
	values, ok := f.openmanet.Get("openmanetd", "config", "dhcpconfigured")
	if !ok || len(values) == 0 {
		return ""
	}

	return values[0]
}

// assertNoWrites checks that a tick left every UCI reader untouched and did
// not reboot.
func (f *reservationFixture) assertNoWrites(t *testing.T) {
	t.Helper()

	assert.Equal(t, 0, f.network.commitCalls, "network must not be committed")
	assert.Equal(t, 0, f.dhcp.commitCalls, "dhcp must not be committed")
	assert.Equal(t, 0, f.openmanet.commitCalls, "openmanetd must not be committed")
	assert.Equal(t, 0, f.reboots, "must not reboot")
}

// ── reserveOnceWithDeps characterisation tests (pin today's behavior) ────────

func TestReserveOnce_ConfiguredNoConflict_Idle(t *testing.T) {
	f := newReservationFixture(t)
	seedDHCPConfigured(f.openmanet)
	f.iface = makeTestIface("aa:bb:cc:dd:ee:01", "10.41.0.5")
	f.seedPeer(t, "aa:bb:cc:dd:ee:02", "10.41.0.1", 100)

	require.NoError(t, f.tick(t))

	f.assertNoWrites(t)
}

func TestReserveOnce_GatewayFirstPick(t *testing.T) {
	f := newReservationFixture(t)
	f.gwMode = batmanadv.GwModeServer

	require.NoError(t, f.tick(t))

	assert.Equal(t, "10.41.0.1", f.ahwlanIP(), "a gateway with no peers takes the first address")
	assert.Equal(t, network.DefaultNetworkProto, firstValue(f.network, "network", "ahwlan", "proto"))
	assert.Equal(t, network.DefaultNetworkMask, firstValue(f.network, "network", "ahwlan", "netmask"))
	assert.Equal(t, "br-ahwlan", firstValue(f.network, "network", "ahwlan", "device"))
	assert.Equal(t, "100", f.dhcpStart(), "pool offset 100 is preferred")
	assert.Equal(t, strconv.Itoa(network.DefaultDHCPAddressLimit), firstValue(f.dhcp, "dhcp", "ahwlan", "limit"))
	assert.Equal(t, network.DefaultDHCPLeaseTime, firstValue(f.dhcp, "dhcp", "ahwlan", "leasetime"))
	assert.Equal(t, "1", firstValue(f.dhcp, "dhcp", "ahwlan", "force"))
	assert.Equal(t, "1", f.dhcpConfigured())
	assert.Equal(t, 0, f.reloads, "gateways skip the lan cleanup and its reload")
	assert.Equal(t, 1, f.reboots)
}

func TestReserveOnce_GatewaySkipsReservedAddressesAndPools(t *testing.T) {
	f := newReservationFixture(t)
	f.gwMode = batmanadv.GwModeServer
	f.seedPeer(t, "aa:bb:cc:dd:ee:02", "10.41.0.1", 100)
	f.seedPeer(t, "aa:bb:cc:dd:ee:03", "10.41.0.2", 116)

	require.NoError(t, f.tick(t))

	assert.Equal(t, "10.41.0.3", f.ahwlanIP())
	assert.Equal(t, "132", f.dhcpStart(), "first 16-address window after 100 and 116")
	assert.Equal(t, 1, f.reboots)
}

func TestReserveOnce_PointAloneRandomRange(t *testing.T) {
	f := newReservationFixture(t)
	f.network.seedLanNetworkSection()
	f.dhcp.seedLanDHCPSection()

	require.NoError(t, f.tick(t))

	octets := strings.Split(f.ahwlanIP(), ".")
	require.Len(t, octets, 4, "ipaddr %q must be dotted quad", f.ahwlanIP())
	assert.Equal(t, "10", octets[0])
	assert.Equal(t, "41", octets[1])

	third, err := strconv.Atoi(octets[2])
	require.NoError(t, err)
	assert.GreaterOrEqual(t, third, 1)
	assert.LessOrEqual(t, third, 252, "253/254 are reserved third octets")

	fourth, err := strconv.Atoi(octets[3])
	require.NoError(t, err)
	assert.GreaterOrEqual(t, fourth, 1)
	assert.LessOrEqual(t, fourth, 254)

	_, lanNet := f.network.Get("network", "lan", "proto")
	assert.False(t, lanNet, "points drop the bootstrap lan network section")

	_, lanDHCP := f.dhcp.Get("dhcp", "lan", "interface")
	assert.False(t, lanDHCP, "points drop the bootstrap lan dhcp section")
	assert.Equal(t, 1, f.reloads, "points reload the network after cleanup")
	assert.Equal(t, 1, f.reboots)
}

func TestReserveOnce_PointWithPeersFirstFree(t *testing.T) {
	f := newReservationFixture(t)
	f.seedPeer(t, "aa:bb:cc:dd:ee:02", "10.41.1.1", 100)
	f.seedPeer(t, "aa:bb:cc:dd:ee:03", "10.41.1.2", 116)

	require.NoError(t, f.tick(t))

	assert.Equal(t, "10.41.1.3", f.ahwlanIP(), "two or more peers switch the point to sequential first-free")
	assert.Equal(t, "132", f.dhcpStart())
	assert.Equal(t, 1, f.reboots)
}

func TestReserveOnce_ConflictReReserves(t *testing.T) {
	f := newReservationFixture(t)
	f.gwMode = batmanadv.GwModeServer
	seedDHCPConfigured(f.openmanet)
	f.iface = makeTestIface("aa:bb:cc:dd:ee:01", "10.41.0.1")
	// Task 3: the peer has the lower MAC, so this node is the one that moves.
	f.seedPeer(t, "aa:bb:cc:dd:ee:00", "10.41.0.1", 100)

	require.NoError(t, f.tick(t))

	assert.Equal(t, "10.41.0.2", f.ahwlanIP(), "the peer row keeps .1, so the next free address is .2")
	assert.Equal(t, 1, f.reboots)
}

func TestReserveOnce_MeshConfigError_NoWrites(t *testing.T) {
	f := newReservationFixture(t)
	f.meshErr = errors.New("batctl mj: exit status 1")

	err := f.tick(t)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get mesh config")

	f.assertNoWrites(t)
}

func TestReserveOnce_InvalidDHCPConfiguredValue(t *testing.T) {
	f := newReservationFixture(t)
	_ = f.openmanet.AddSection("openmanetd", "config", "openmanet")
	_ = f.openmanet.SetType("openmanetd", "config", "dhcpconfigured", uci.TypeOption, "yes")

	err := f.tick(t)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "check DHCP configuration")

	f.assertNoWrites(t)
}

// TestReserveOnce_NonBridgeInterface_NoWrites pins an existing quirk: the
// worker only knows how to derive the UCI section from a br-* name, so a
// non-bridge mesh interface can never reserve.
func TestReserveOnce_NonBridgeInterface_NoWrites(t *testing.T) {
	f := newReservationFixture(t)
	f.arw.Config.IFace = "eth0"

	err := f.tick(t)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "normalize interface name")

	f.assertNoWrites(t)
}

func TestReserveOnce_RebootError_AfterWrites(t *testing.T) {
	f := newReservationFixture(t)
	f.gwMode = batmanadv.GwModeServer
	f.rebootErr = errors.New("reboot: exec: not found")

	err := f.tick(t)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reboot")

	assert.Equal(t, "1", f.dhcpConfigured(), "UCI writes happen before the reboot attempt")
	assert.Equal(t, 1, f.reboots)
}

func TestFindIPConflicts(t *testing.T) {
	iface := makeTestIface("aa:bb:cc:dd:ee:01", "10.41.0.1")
	nodes := []models.MeshNode{
		{MacAddr: "aa:bb:cc:dd:ee:02", IpAddr: "10.41.0.9"},
		{MacAddr: "aa:bb:cc:dd:ee:03", IpAddr: "10.41.0.1"},
		{MacAddr: "aa:bb:cc:dd:ee:04", IpAddr: "10.41.0.1"},
	}

	got := findIPConflicts(iface, nodes)
	require.Len(t, got, 2)
	assert.Equal(t, "aa:bb:cc:dd:ee:03", got[0].MacAddr)
	assert.Equal(t, "aa:bb:cc:dd:ee:04", got[1].MacAddr)

	assert.Empty(t, findIPConflicts(network.NetworkInterface{}, nodes), "no addresses, no conflicts")
}

// gossipRecord marshals a peer announcement the way NodeDataWorker.StartSend
// publishes it, for feeding fakeAlfredClient.records.
func gossipRecord(t *testing.T, mac, hostname, ip, dhcpStart string) alfred.Record {
	t.Helper()

	node := &proto.Node{
		Mac:          mac,
		Hostname:     hostname,
		Ipaddr:       ip,
		UciDhcpStart: dhcpStart,
		UciDhcpLimit: strconv.Itoa(network.DefaultDHCPAddressLimit),
	}

	data, err := node.MarshalVT()
	require.NoError(t, err)

	return alfred.Record{Data: data}
}

// ── receive-before-decide (ledger P5 step 2) ─────────────────────────────────

func TestReserveOnce_RefreshesGossipBeforeDeciding(t *testing.T) {
	f := newReservationFixture(t)
	f.gwMode = batmanadv.GwModeServer
	// The peer exists only in Alfred, not yet in the database.
	f.client.records = []alfred.Record{gossipRecord(t, "aa:bb:cc:dd:ee:02", "peer-gate", "10.41.0.1", "100")}

	require.NoError(t, f.tick(t))

	assert.Equal(t, "10.41.0.2", f.ahwlanIP(), "the address heard this tick must be treated as reserved")
	assert.Equal(t, "116", f.dhcpStart(), "the pool heard this tick must be treated as reserved")

	peer, err := f.db.GetMeshNode(context.Background(), "aa:bb:cc:dd:ee:02")
	require.NoError(t, err)
	assert.Equal(t, "10.41.0.1", peer.IpAddr, "the refresh persists what it heard")
}

func TestReserveOnce_GossipRequestError_UsesStoredPeers(t *testing.T) {
	f := newReservationFixture(t)
	f.gwMode = batmanadv.GwModeServer
	f.client.requestErr = errors.New("alfred: connection refused")
	f.seedPeer(t, "aa:bb:cc:dd:ee:02", "10.41.0.1", 100)

	require.NoError(t, f.tick(t), "a failed refresh must not stall the reservation")

	assert.Equal(t, "10.41.0.2", f.ahwlanIP(), "decision falls back to the stored rows")
	assert.Equal(t, 1, f.reboots)
}

// ── MAC tie-break + per-tick conflict decision (ledger P5 step 3, D3) ────────

func TestReserveOnce_ConflictLowerMACStays(t *testing.T) {
	f := newReservationFixture(t)
	f.gwMode = batmanadv.GwModeServer
	seedDHCPConfigured(f.openmanet)
	f.iface = makeTestIface("aa:bb:cc:dd:ee:01", "10.41.0.1")
	f.seedPeer(t, "aa:bb:cc:dd:ee:02", "10.41.0.1", 100)

	require.NoError(t, f.tick(t))

	f.assertNoWrites(t)
}

func TestReserveOnce_ConflictHigherMACMoves(t *testing.T) {
	f := newReservationFixture(t)
	f.gwMode = batmanadv.GwModeServer
	seedDHCPConfigured(f.openmanet)
	f.iface = makeTestIface("aa:bb:cc:dd:ee:02", "10.41.0.1")
	f.seedPeer(t, "aa:bb:cc:dd:ee:01", "10.41.0.1", 100)

	require.NoError(t, f.tick(t))

	assert.Equal(t, "10.41.0.2", f.ahwlanIP(), "the peer keeps .1; this node takes the next free address")
	assert.Equal(t, 1, f.reboots)
}

// TestReserveOnce_ConflictFlagDoesNotLeak replaces the Task 1
// characterisation test: after a conflict is resolved, a clean tick is idle.
func TestReserveOnce_ConflictFlagDoesNotLeak(t *testing.T) {
	f := newReservationFixture(t)
	f.gwMode = batmanadv.GwModeServer
	seedDHCPConfigured(f.openmanet)
	f.iface = makeTestIface("aa:bb:cc:dd:ee:02", "10.41.0.1")
	f.seedPeer(t, "aa:bb:cc:dd:ee:01", "10.41.0.1", 100)

	require.NoError(t, f.tick(t))
	require.Equal(t, 1, f.reboots)

	// Second tick: this node now sits on a clean address.
	f.iface = makeTestIface("aa:bb:cc:dd:ee:02", "10.41.0.2")

	require.NoError(t, f.tick(t))

	assert.Equal(t, 1, f.reboots, "no conflict this tick, so no re-reservation")
}

func TestReserveOnce_NotConfiguredIgnoresTieBreak(t *testing.T) {
	f := newReservationFixture(t)
	f.gwMode = batmanadv.GwModeServer
	// dhcpconfigured stays 0; the bootstrap address happens to collide with a
	// lower-MAC peer, which must not stop the first reservation.
	f.iface = makeTestIface("aa:bb:cc:dd:ee:01", "10.41.0.1")
	f.seedPeer(t, "aa:bb:cc:dd:ee:02", "10.41.0.1", 100)

	require.NoError(t, f.tick(t))

	assert.Equal(t, "10.41.0.2", f.ahwlanIP())
	assert.Equal(t, 1, f.reboots)
}

func TestYieldsAddress(t *testing.T) {
	tests := []struct {
		name  string
		own   string
		peers []string
		want  bool
	}{
		{name: "peer lower, this node moves", own: "aa:bb:cc:dd:ee:02", peers: []string{"aa:bb:cc:dd:ee:01"}, want: true},
		{name: "peer higher, this node stays", own: "aa:bb:cc:dd:ee:01", peers: []string{"aa:bb:cc:dd:ee:02"}, want: false},
		{name: "case-insensitive", own: "AA:BB:CC:DD:EE:02", peers: []string{"aa:bb:cc:dd:ee:01"}, want: true},
		{name: "any lower peer wins", own: "aa:bb:cc:dd:ee:02", peers: []string{"aa:bb:cc:dd:ee:03", "aa:bb:cc:dd:ee:01"}, want: true},
		{name: "all peers higher", own: "aa:bb:cc:dd:ee:01", peers: []string{"aa:bb:cc:dd:ee:03", "aa:bb:cc:dd:ee:02"}, want: false},
		{name: "equal MACs never yield", own: "aa:bb:cc:dd:ee:01", peers: []string{"aa:bb:cc:dd:ee:01"}, want: false},
		{name: "empty peer MAC ignored", own: "aa:bb:cc:dd:ee:01", peers: []string{""}, want: false},
		{name: "no peers", own: "aa:bb:cc:dd:ee:01", peers: nil, want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			peers := make([]models.MeshNode, 0, len(tc.peers))
			for _, mac := range tc.peers {
				peers = append(peers, models.MeshNode{MacAddr: mac})
			}

			assert.Equal(t, tc.want, yieldsAddress(tc.own, peers))
		})
	}
}

// ── expiry seen from the reservation tick ────────────────────────────────────

func TestReserveOnce_ExpiredPeerNoLongerReserves(t *testing.T) {
	f := newReservationFixture(t)
	f.gwMode = batmanadv.GwModeServer
	f.arw.Config.NodeExpiry = 24 * time.Hour

	db, q := newNodeTestDBConn(t)
	f.arw.Config.DB = q
	f.db = q
	seedNodeUpdatedAgo(t, db, "aa:bb:cc:dd:ee:02", "10.41.0.1", "-25 hours")

	require.NoError(t, f.tick(t))

	assert.Equal(t, "10.41.0.1", f.ahwlanIP(), "a peer silent for longer than nodeExpiry releases its address")
	assert.Equal(t, "100", f.dhcpStart(), "…and its DHCP window")
}
