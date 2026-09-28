package gpsd

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/coreywagehoft/go-tak/pkg/cot"
	"github.com/openmanet/openmanetd/internal/config"
	"github.com/openmanet/openmanetd/internal/network"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeMulticastSender captures every datagram handed to SendMulticast.
type fakeMulticastSender struct {
	mu      sync.Mutex
	packets [][]byte
	err     error
}

func (f *fakeMulticastSender) send(data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	cp := make([]byte, len(data))
	copy(cp, data)
	f.packets = append(f.packets, cp)

	return f.err
}

func (f *fakeMulticastSender) sent() [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make([][]byte, len(f.packets))
	copy(out, f.packets)

	return out
}

func decodeMesh(t *testing.T, data []byte) (typ, uid string) {
	t.Helper()

	msg, _, err := cot.ReadProtoMesh(bufio.NewReader(bytes.NewReader(data)))
	require.NoError(t, err)
	require.NotNil(t, msg.GetCotEvent())

	return msg.GetCotEvent().GetType(), msg.GetCotEvent().GetUid()
}

func newNoLeaseGPS(sender *fakeMulticastSender, leases func() (*network.DHCPLeasesResponse, error)) *GPSService {
	return &GPSService{
		Log:           zerolog.Nop(),
		GetDHCPLeases: leases,
		SendMulticast: sender.send,
		position: PositionReport{
			Timestamp: time.Now(),
			Latitude:  37.7749,
			Longitude: -122.4194,
			Altitude:  50,
			Valid:     true,
			Mode:      3,
		},
	}
}

func emptyLeases() (*network.DHCPLeasesResponse, error) {
	return &network.DHCPLeasesResponse{}, nil
}

func TestSendIfRequiredAsCoT_noLeasesSendsPingThenMarker(t *testing.T) {
	sender := &fakeMulticastSender{}
	gps := newNoLeaseGPS(sender, emptyLeases)

	gps.SendIfRequiredAsCoT()

	packets := sender.sent()
	require.Len(t, packets, 2, "expected a mesh ping followed by the radio marker")

	pingType, _ := decodeMesh(t, packets[0])
	assert.Equal(t, cot.TypePing, pingType)

	markerType, markerUID := decodeMesh(t, packets[1])
	assert.Equal(t, radioUnitType, markerType)
	assert.Equal(t, nodeCallsign(), markerUID)
}

func TestSendIfRequiredAsCoT_leaseErrorSendsNothing(t *testing.T) {
	sender := &fakeMulticastSender{}
	gps := newNoLeaseGPS(sender, func() (*network.DHCPLeasesResponse, error) {
		return nil, errors.New("ubus unavailable")
	})

	gps.SendIfRequiredAsCoT()

	assert.Empty(t, sender.sent())

	gps.mu.RLock()
	defer gps.mu.RUnlock()

	assert.True(t, gps.lastMulticastTime.IsZero())
}

func TestSendIfRequiredAsCoT_rateLimitsMulticast(t *testing.T) {
	sender := &fakeMulticastSender{}
	gps := newNoLeaseGPS(sender, emptyLeases)

	gps.SendIfRequiredAsCoT()
	gps.SendIfRequiredAsCoT()

	assert.Len(t, sender.sent(), 2, "second call inside the rate-limit window must not send")
}

func TestSendIfRequiredAsCoT_sendErrorIsLoggedNotFatal(t *testing.T) {
	var logs bytes.Buffer

	sender := &fakeMulticastSender{err: errors.New("network is unreachable")}
	gps := newNoLeaseGPS(sender, emptyLeases)
	gps.Log = zerolog.New(&logs)

	gps.SendIfRequiredAsCoT()

	assert.Len(t, sender.sent(), 2, "marker send must still be attempted after ping failure")
	assert.Contains(t, logs.String(), "Failed to send CoT to multicast")
}

func TestSendRawNMEAToActiveDevices_usesInjectedLeases(t *testing.T) {
	calls := 0
	gps := &GPSService{
		Log: zerolog.Nop(),
		GetDHCPLeases: func() (*network.DHCPLeasesResponse, error) {
			calls++

			return nil, errors.New("ubus unavailable")
		},
	}

	gps.sendRawNMEAToActiveDevices("$GPGGA,,,,,,0,,,,,,,,*66")

	assert.Equal(t, 1, calls)
}

func TestSelectMulticastInterfaces_prefersConfiguredMesh(t *testing.T) {
	mesh := &net.Interface{Index: 7, Name: "br-ahwlan", Flags: net.FlagUp | net.FlagMulticast}
	byName := func(name string) (*net.Interface, error) {
		if name == "br-ahwlan" {
			return mesh, nil
		}

		return nil, errors.New("not found")
	}
	list := func() ([]net.Interface, error) {
		t.Fatal("interface list must not be consulted when the mesh interface resolves")

		return nil, nil
	}

	got := selectMulticastInterfaces("br-ahwlan", byName, list)

	require.Len(t, got, 1)
	assert.Equal(t, "br-ahwlan", got[0].Name)
}

func TestSelectMulticastInterfaces_fallsBackToUpMulticastInterfaces(t *testing.T) {
	byName := func(string) (*net.Interface, error) { return nil, errors.New("no such interface") }
	list := func() ([]net.Interface, error) {
		return []net.Interface{
			{Index: 1, Name: "lo", Flags: net.FlagUp | net.FlagLoopback | net.FlagMulticast},
			{Index: 2, Name: "down0", Flags: net.FlagMulticast},
			{Index: 3, Name: "p2p0", Flags: net.FlagUp | net.FlagPointToPoint},
			{Index: 4, Name: "br-lan", Flags: net.FlagUp | net.FlagMulticast},
			{Index: 5, Name: "bat0", Flags: net.FlagUp | net.FlagMulticast},
		}, nil
	}

	got := selectMulticastInterfaces("br-ahwlan", byName, list)

	names := make([]string, 0, len(got))
	for _, ifi := range got {
		names = append(names, ifi.Name)
	}

	assert.Equal(t, []string{"br-lan", "bat0"}, names)
}

func TestSelectMulticastInterfaces_skipsMeshWhenDown(t *testing.T) {
	byName := func(string) (*net.Interface, error) {
		return &net.Interface{Index: 7, Name: "br-ahwlan", Flags: net.FlagMulticast}, nil
	}
	list := func() ([]net.Interface, error) {
		return []net.Interface{{Index: 4, Name: "br-lan", Flags: net.FlagUp | net.FlagMulticast}}, nil
	}

	got := selectMulticastInterfaces("br-ahwlan", byName, list)

	require.Len(t, got, 1)
	assert.Equal(t, "br-lan", got[0].Name)
}

func TestSendCoTMulticast_noCandidateInterfaces(t *testing.T) {
	gps := &GPSService{
		Log:             zerolog.Nop(),
		interfaceByName: func(string) (*net.Interface, error) { return nil, errors.New("no such interface") },
		listInterfaces:  func() ([]net.Interface, error) { return nil, nil },
	}

	err := gps.sendCoTMulticast([]byte{0xbf})

	require.Error(t, err)
	assert.ErrorContains(t, err, "no multicast-capable interface")
}

// fakeLAN wires a single up interface br-ahwlan/10.41.0.1/16 into the
// interface lookup seams and records ARP probes.
type fakeLAN struct {
	mu      sync.Mutex
	probes  []string
	probeIf []string
	err     error
}

var fakeLANIface = net.Interface{Index: 7, Name: "br-ahwlan", Flags: net.FlagUp | net.FlagMulticast} //nolint:gochecknoglobals // test fixture

func (f *fakeLAN) list() ([]net.Interface, error) {
	return []net.Interface{
		{Index: 1, Name: "lo", Flags: net.FlagUp | net.FlagLoopback},
		fakeLANIface,
	}, nil
}

func (f *fakeLAN) addrs(ifi *net.Interface) ([]net.Addr, error) {
	if ifi.Name != "br-ahwlan" {
		return nil, nil
	}

	_, ipn, err := net.ParseCIDR("10.41.0.1/16")
	if err != nil {
		return nil, fmt.Errorf("parse fixture CIDR: %w", err)
	}

	return []net.Addr{ipn}, nil
}

func (f *fakeLAN) probe(ifi *net.Interface, ip netip.Addr) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.probes = append(f.probes, ip.String())
	f.probeIf = append(f.probeIf, ifi.Name)

	return f.err
}

func (f *fakeLAN) probed() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]string(nil), f.probes...)
}

func newLANGPS(lan *fakeLAN) *GPSService {
	return &GPSService{
		Log:            zerolog.Nop(),
		listInterfaces: lan.list,
		interfaceAddrs: lan.addrs,
		arpProbe:       lan.probe,
	}
}

func TestCheckDeviceActive_arpReplyIsActive(t *testing.T) {
	lan := &fakeLAN{}
	gps := newLANGPS(lan)

	assert.True(t, gps.checkDeviceActive("10.41.0.50"))
	assert.Equal(t, []string{"10.41.0.50"}, lan.probed())
	assert.Equal(t, []string{"br-ahwlan"}, lan.probeIf)
}

func TestCheckDeviceActive_arpFailureIsInactive(t *testing.T) {
	lan := &fakeLAN{err: errors.New("i/o timeout")}
	gps := newLANGPS(lan)

	assert.False(t, gps.checkDeviceActive("10.41.0.50"))
	assert.Len(t, lan.probed(), 1)
}

func TestCheckDeviceActive_outsideLocalSubnetIsInactiveWithoutProbe(t *testing.T) {
	lan := &fakeLAN{}
	gps := newLANGPS(lan)

	assert.False(t, gps.checkDeviceActive("192.168.5.5"))
	assert.Empty(t, lan.probed(), "no ARP probe for an address no local interface can reach")
}

func TestSendIfRequiredAsCoT_activeDeviceSuppressesMulticast(t *testing.T) {
	sender := &fakeMulticastSender{}
	lan := &fakeLAN{}
	gps := newNoLeaseGPS(sender, func() (*network.DHCPLeasesResponse, error) {
		return &network.DHCPLeasesResponse{DHCPLeases: []network.DHCPLease{{IPAddr: "10.41.0.50"}}}, nil
	})
	gps.listInterfaces = lan.list
	gps.interfaceAddrs = lan.addrs
	gps.arpProbe = lan.probe

	gps.SendIfRequiredAsCoT()

	assert.Empty(t, sender.sent(), "an active EUD must suppress the multicast fallback")
}

func TestSendIfRequiredAsCoT_staleLeaseStillMulticasts(t *testing.T) {
	sender := &fakeMulticastSender{}
	lan := &fakeLAN{err: errors.New("i/o timeout")}
	gps := newNoLeaseGPS(sender, func() (*network.DHCPLeasesResponse, error) {
		return &network.DHCPLeasesResponse{DHCPLeases: []network.DHCPLease{{IPAddr: "10.41.0.50"}}}, nil
	})
	gps.listInterfaces = lan.list
	gps.interfaceAddrs = lan.addrs
	gps.arpProbe = lan.probe

	gps.SendIfRequiredAsCoT()

	assert.Len(t, sender.sent(), 2, "a lease whose device no longer answers ARP must not block multicast")
}

// TestSendCoTMulticast_realInterfaces exercises the real pinned sender on
// whatever up multicast-capable interfaces the host has. It is also the
// test binary run inside an isolated network namespace without a default
// route to prove the "network is unreachable" regression is gone.
func TestSendCoTMulticast_realInterfaces(t *testing.T) {
	if len(selectMulticastInterfaces(config.DefaultMeshNetInterface, net.InterfaceByName, net.Interfaces)) == 0 {
		t.Skip("no up multicast-capable interface on this host")
	}

	gps := &GPSService{Log: zerolog.Nop()}

	data, err := cot.MakeProtoMeshPacketV1(cot.MakePing("openmanet-test"))
	require.NoError(t, err)

	require.NoError(t, gps.sendCoTMulticast(data))
}
