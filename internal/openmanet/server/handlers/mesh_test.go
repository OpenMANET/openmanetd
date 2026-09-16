package handlers_test

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/mdlayher/wifi"
	serviceproto "github.com/openmanet/openmanetd/internal/api/openmanet/service/v1"
	batmanadv "github.com/openmanet/openmanetd/internal/batman-adv"
	"github.com/openmanet/openmanetd/internal/openmanet/server/handlers"
	"github.com/openmanet/openmanetd/internal/wireless"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/emptypb"
)

// testFixturesDir returns the path to the testfixtures directory at the root of
// the module, regardless of which package is importing it.
func testFixturesDir() string {
	_, filename, _, _ := runtime.Caller(0)
	// Walk up from internal/openmanet/server/handlers/ to the module root.
	return filepath.Join(filepath.Dir(filename), "..", "..", "..", "..", "testfixtures")
}

// fixtureBatHostsPath returns the path to the bat-hosts test fixture.
func fixtureBatHostsPath() string {
	return filepath.Join(testFixturesDir(), "batman-adv", "bat-hosts")
}

func newMeshService(fw *fakeWireless, parseBatHosts func(string) (*batmanadv.BatHosts, error)) *handlers.MeshService {
	return &handlers.MeshService{
		Log:           zerolog.Nop(),
		Wifi:          fw,
		ParseBatHosts: parseBatHosts,
	}
}

func TestListMeshNeighbors_NoInterfaces(t *testing.T) {
	fw := &fakeWireless{meshInterfaces: nil}
	svc := newMeshService(fw, func(path string) (*batmanadv.BatHosts, error) {
		return batmanadv.ParseBatHostsFile(fixtureBatHostsPath())
	})

	resp, err := svc.ListMeshNeighbors(context.Background(), &emptypb.Empty{})
	require.NoError(t, err)
	assert.Empty(t, resp.GetNeighbors())
}

func TestListMeshNeighbors_WithStations(t *testing.T) {
	meshIface := makeInterface("mesh0", wifi.InterfaceTypeMeshPoint)
	station := makeStation("9c:ef:d5:f9:80:4d", -65) // MAC present in fixture bat-hosts

	fw := &fakeWireless{
		meshInterfaces: []*wifi.Interface{meshIface},
		stationInfo:    []*wifi.StationInfo{station},
	}
	svc := newMeshService(fw, func(_ string) (*batmanadv.BatHosts, error) {
		return batmanadv.ParseBatHostsFile(fixtureBatHostsPath())
	})

	resp, err := svc.ListMeshNeighbors(context.Background(), &emptypb.Empty{})
	require.NoError(t, err)
	require.Len(t, resp.GetNeighbors(), 1)

	n := resp.GetNeighbors()[0]
	assert.Equal(t, "9c:ef:d5:f9:80:4d", n.GetHardwareAddress())
	assert.Equal(t, "BCM2711-97d6_phy2-mesh0", n.GetNeighbor(), "hostname should come from bat-hosts fixture")
	assert.Equal(t, int32(-65), n.GetSignal())
}

func TestListMeshNeighbors_NoStations(t *testing.T) {
	meshIface := makeInterface("mesh0", wifi.InterfaceTypeMeshPoint)
	fw := &fakeWireless{
		meshInterfaces: []*wifi.Interface{meshIface},
		stationInfo:    nil,
	}
	svc := newMeshService(fw, func(_ string) (*batmanadv.BatHosts, error) {
		return batmanadv.ParseBatHostsFile(fixtureBatHostsPath())
	})

	resp, err := svc.ListMeshNeighbors(context.Background(), &emptypb.Empty{})
	require.NoError(t, err)
	assert.Empty(t, resp.GetNeighbors())
}

func TestListMeshNeighbors_WifiError(t *testing.T) {
	fw := &fakeWireless{meshInterfacesErr: errors.New("netlink failure")}
	svc := newMeshService(fw, func(_ string) (*batmanadv.BatHosts, error) {
		return batmanadv.ParseBatHostsFile(fixtureBatHostsPath())
	})

	_, err := svc.ListMeshNeighbors(context.Background(), &emptypb.Empty{})
	require.Error(t, err)
}

func TestListMeshNeighbors_StationInfoError(t *testing.T) {
	meshIface := makeInterface("mesh0", wifi.InterfaceTypeMeshPoint)
	fw := &fakeWireless{
		meshInterfaces: []*wifi.Interface{meshIface},
		stationInfoErr: errors.New("station info failed"),
	}
	svc := newMeshService(fw, func(_ string) (*batmanadv.BatHosts, error) {
		return batmanadv.ParseBatHostsFile(fixtureBatHostsPath())
	})

	_, err := svc.ListMeshNeighbors(context.Background(), &emptypb.Empty{})
	require.Error(t, err)
}

func TestListMeshNeighbors_BatHostsError(t *testing.T) {
	fw := &fakeWireless{meshInterfaces: nil}
	svc := newMeshService(fw, func(_ string) (*batmanadv.BatHosts, error) {
		return nil, errors.New("bat-hosts unavailable")
	})

	_, err := svc.ListMeshNeighbors(context.Background(), &emptypb.Empty{})
	require.Error(t, err)
}

func TestListMeshNeighbors_MultipleInterfaces(t *testing.T) {
	mesh0 := makeInterface("mesh0", wifi.InterfaceTypeMeshPoint)
	mesh1 := makeInterface("mesh1", wifi.InterfaceTypeMeshPoint)

	fw := &fakeWireless{
		meshInterfaces: []*wifi.Interface{mesh0, mesh1},
		stationInfoByIface: map[string][]*wifi.StationInfo{
			"mesh0": {makeStation("9c:ef:d5:f9:80:4d", -65)},
			"mesh1": {makeStation("11:22:33:44:55:66", -70)},
		},
	}
	svc := newMeshService(fw, func(_ string) (*batmanadv.BatHosts, error) {
		return batmanadv.ParseBatHostsFile(fixtureBatHostsPath())
	})

	resp, err := svc.ListMeshNeighbors(context.Background(), &emptypb.Empty{})
	require.NoError(t, err)
	assert.Len(t, resp.GetNeighbors(), 2, "should include stations from both interfaces")
}

func TestListMeshNeighbors_UnknownMAC(t *testing.T) {
	meshIface := makeInterface("mesh0", wifi.InterfaceTypeMeshPoint)
	// Use a MAC that is NOT in the bat-hosts fixture.
	station := makeStation("ff:ff:ff:ff:ff:ff", -50)

	fw := &fakeWireless{
		meshInterfaces: []*wifi.Interface{meshIface},
		stationInfo:    []*wifi.StationInfo{station},
	}
	svc := newMeshService(fw, func(_ string) (*batmanadv.BatHosts, error) {
		return batmanadv.ParseBatHostsFile(fixtureBatHostsPath())
	})

	resp, err := svc.ListMeshNeighbors(context.Background(), &emptypb.Empty{})
	require.NoError(t, err)
	require.Len(t, resp.GetNeighbors(), 1)

	// Unknown MAC results in empty hostname, not an error.
	assert.Equal(t, "", resp.GetNeighbors()[0].GetNeighbor())
	assert.Equal(t, "ff:ff:ff:ff:ff:ff", resp.GetNeighbors()[0].GetHardwareAddress())
}

func TestListMeshNeighbors_FieldMapping(t *testing.T) {
	meshIface := makeInterface("mesh0", wifi.InterfaceTypeMeshPoint)
	station := makeStation("9c:ef:d5:f9:80:4d", -65)

	fw := &fakeWireless{
		meshInterfaces: []*wifi.Interface{meshIface},
		stationInfo:    []*wifi.StationInfo{station},
	}
	svc := newMeshService(fw, func(_ string) (*batmanadv.BatHosts, error) {
		return batmanadv.ParseBatHostsFile(fixtureBatHostsPath())
	})

	resp, err := svc.ListMeshNeighbors(context.Background(), &emptypb.Empty{})
	require.NoError(t, err)
	require.Len(t, resp.GetNeighbors(), 1)

	n := resp.GetNeighbors()[0]
	assert.Equal(t, int32(-65), n.GetSignalStrength(), "SignalAverage maps to SignalStrength")
	assert.Equal(t, int32(-65), n.GetSignal(), "Signal maps to Signal")
	assert.Equal(t, int32(54000), n.GetThroughput(), "TransmitBitrate maps to Throughput (no batman-adv enrichment)")
	assert.Equal(t, "9c:ef:d5:f9:80:4d", n.GetHardwareAddress(), "HardwareAddr maps to HardwareAddress")
}

// MeshNeighbor.throughput carries the nl80211 station bitrate in
// bit/s; when batman-adv enrichment replaces it, the `batctl nj`
// value (kbit/s) must be scaled to the same unit, and a multi-gigabit
// hardif must saturate rather than wrap the int32.
func TestListMeshNeighbors_BatmanThroughputScaledToBps(t *testing.T) {
	tests := map[string]struct {
		kbps int
		want int32
	}{
		"2.4 GHz HT20 link":   {kbps: 22200, want: 22_200_000},
		"halow link":          {kbps: 7100, want: 7_100_000},
		"zero throughput":     {kbps: 0, want: 0},
		"negative throughput": {kbps: -1, want: 0},
		"multi-gigabit wire":  {kbps: 10_000_000, want: math.MaxInt32},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			meshIface := makeInterface("mesh0", wifi.InterfaceTypeMeshPoint)
			station := makeStation("9c:ef:d5:f9:80:4d", -65)

			fw := &fakeWireless{
				meshInterfaces: []*wifi.Interface{meshIface},
				stationInfo:    []*wifi.StationInfo{station},
			}
			svc := newMeshService(fw, func(_ string) (*batmanadv.BatHosts, error) {
				return batmanadv.ParseBatHostsFile(fixtureBatHostsPath())
			})
			svc.GetMeshNeighbors = func() (*batmanadv.Neighbors, error) {
				return &batmanadv.Neighbors{
					{HardIfname: "mesh0", NeighAddress: "9c:ef:d5:f9:80:4d", Throughput: tc.kbps, LastSeenMsecs: 300},
				}, nil
			}

			resp, err := svc.ListMeshNeighbors(context.Background(), &emptypb.Empty{})
			require.NoError(t, err)
			require.Len(t, resp.GetNeighbors(), 1)

			n := resp.GetNeighbors()[0]
			assert.Equal(t, tc.want, n.GetThroughput())
			assert.Equal(t, int64(300), n.GetLastSeen())
		})
	}
}

func TestListMeshNeighbors_LinkRateMapping(t *testing.T) {
	meshIface := makeInterface("mesh1", wifi.InterfaceTypeMeshPoint)
	tx := wifi.RateInfo{
		Bitrate:        86_700_000,
		ModulationType: wifi.RateModulationInfoTypeHE,
		Modulation:     wifi.HEModulationInfo{BaseModulationInfo: wifi.BaseModulationInfo{MCS: 7, NSS: 2}},
		ChannelWidth:   wifi.ChannelWidth40,
	}
	rx := wifi.RateInfo{
		Bitrate:        72_200_000,
		ModulationType: wifi.RateModulationInfoTypeHT,
		Modulation:     wifi.HTModulationInfo{BaseModulationInfo: wifi.BaseModulationInfo{MCS: 7, NSS: 1}, HTMCS: 7},
		ChannelWidth:   wifi.ChannelWidth20NoHT,
	}
	station := makeStationWithRate("9c:ef:d5:f9:80:4d", -65, tx, rx)

	fw := &fakeWireless{
		meshInterfaces: []*wifi.Interface{meshIface},
		stationInfo:    []*wifi.StationInfo{station},
	}
	svc := newMeshService(fw, func(_ string) (*batmanadv.BatHosts, error) {
		return batmanadv.ParseBatHostsFile(fixtureBatHostsPath())
	})

	resp, err := svc.ListMeshNeighbors(context.Background(), &emptypb.Empty{})
	require.NoError(t, err)
	require.Len(t, resp.GetNeighbors(), 1)

	n := resp.GetNeighbors()[0]
	assert.Equal(t, "mesh1", n.GetInterface())
	assert.Equal(t, int32(86_700_000), n.GetThroughput(), "throughput keeps the station bitrate semantics")

	require.NotNil(t, n.GetTx())
	assert.Equal(t, int32(86700), n.GetTx().GetBitrateKbps())
	assert.Equal(t, serviceproto.LinkRate_PHY_HE, n.GetTx().GetPhy())
	assert.Equal(t, int32(40), n.GetTx().GetWidthMhz())
	assert.Equal(t, int32(7), n.GetTx().GetMcs())
	assert.Equal(t, int32(2), n.GetTx().GetNss())

	require.NotNil(t, n.GetRx())
	assert.Equal(t, int32(72200), n.GetRx().GetBitrateKbps())
	assert.Equal(t, serviceproto.LinkRate_PHY_HT, n.GetRx().GetPhy())
	assert.Equal(t, int32(20), n.GetRx().GetWidthMhz())
	assert.Equal(t, int32(7), n.GetRx().GetMcs())
	assert.Equal(t, int32(1), n.GetRx().GetNss())
}

func TestListMeshNeighbors_LinkRateAbsent(t *testing.T) {
	meshIface := makeInterface("mesh0", wifi.InterfaceTypeMeshPoint)
	station := makeStation("9c:ef:d5:f9:80:4d", -65) // TransmitBitrate 54000 bit/s, no rate attrs

	fw := &fakeWireless{
		meshInterfaces: []*wifi.Interface{meshIface},
		stationInfo:    []*wifi.StationInfo{station},
	}
	svc := newMeshService(fw, func(_ string) (*batmanadv.BatHosts, error) {
		return batmanadv.ParseBatHostsFile(fixtureBatHostsPath())
	})

	resp, err := svc.ListMeshNeighbors(context.Background(), &emptypb.Empty{})
	require.NoError(t, err)
	require.Len(t, resp.GetNeighbors(), 1)

	n := resp.GetNeighbors()[0]
	assert.Equal(t, "mesh0", n.GetInterface())

	require.NotNil(t, n.GetTx())
	assert.Equal(t, int32(54), n.GetTx().GetBitrateKbps(), "kbit/s from the plain station bitrate")
	assert.Equal(t, serviceproto.LinkRate_PHY_UNSPECIFIED, n.GetTx().GetPhy())
	assert.Equal(t, int32(0), n.GetTx().GetWidthMhz())
	assert.Equal(t, int32(-1), n.GetTx().GetMcs())
	assert.Equal(t, int32(-1), n.GetTx().GetNss())

	require.NotNil(t, n.GetRx())
	assert.Equal(t, int32(0), n.GetRx().GetBitrateKbps())
	assert.Equal(t, serviceproto.LinkRate_PHY_UNSPECIFIED, n.GetRx().GetPhy())
}

func TestLinkRatePhyProto(t *testing.T) {
	tests := map[wireless.PHY]serviceproto.LinkRate_Phy{
		wireless.PHYUnknown: serviceproto.LinkRate_PHY_UNSPECIFIED,
		wireless.PHYLegacy:  serviceproto.LinkRate_PHY_LEGACY,
		wireless.PHYHT:      serviceproto.LinkRate_PHY_HT,
		wireless.PHYVHT:     serviceproto.LinkRate_PHY_VHT,
		wireless.PHYHE:      serviceproto.LinkRate_PHY_HE,
		wireless.PHYEHT:     serviceproto.LinkRate_PHY_EHT,
		wireless.PHY(42):    serviceproto.LinkRate_PHY_UNSPECIFIED,
	}

	for p, want := range tests {
		assert.Equal(t, want, handlers.LinkRatePhyProto(p))
	}
}
