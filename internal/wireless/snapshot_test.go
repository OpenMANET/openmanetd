package wireless_test

import (
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/mdlayher/wifi"
	"github.com/openmanet/openmanetd/internal/wireless"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeProvider struct {
	ifaces      []*wifi.Interface
	ifacesErr   error
	stations    map[string][]*wifi.StationInfo
	stationsErr map[string]error
}

func (f *fakeProvider) Interfaces() ([]*wifi.Interface, error) {
	return f.ifaces, f.ifacesErr
}

func (f *fakeProvider) StationInfo(iface *wifi.Interface) ([]*wifi.StationInfo, error) {
	if err, ok := f.stationsErr[iface.Name]; ok {
		return nil, err
	}

	return f.stations[iface.Name], nil
}

func mac(t *testing.T, s string) net.HardwareAddr {
	t.Helper()

	hw, err := net.ParseMAC(s)
	require.NoError(t, err)

	return hw
}

func heStation(t *testing.T, macStr string, signal int) *wifi.StationInfo {
	t.Helper()

	return &wifi.StationInfo{
		HardwareAddr:    mac(t, macStr),
		Signal:          signal,
		SignalAverage:   signal - 2,
		TransmitBitrate: 86_700_000,
		ReceiveBitrate:  72_200_000,
		TransmitRetries: 12,
		TransmitFailed:  3,
		Inactive:        1500 * time.Millisecond,
		TransmitRateInfo: wifi.RateInfo{
			Bitrate:        86_700_000,
			ModulationType: wifi.RateModulationInfoTypeHE,
			Modulation:     wifi.HEModulationInfo{BaseModulationInfo: wifi.BaseModulationInfo{MCS: 7, NSS: 2}},
			ChannelWidth:   wifi.ChannelWidth40,
		},
		ReceiveRateInfo: wifi.RateInfo{
			Bitrate:        72_200_000,
			ModulationType: wifi.RateModulationInfoTypeHT,
			Modulation:     wifi.HTModulationInfo{BaseModulationInfo: wifi.BaseModulationInfo{MCS: 7, NSS: 1}, HTMCS: 7},
			ChannelWidth:   wifi.ChannelWidth20NoHT,
		},
	}
}

func twoIfaceProvider(t *testing.T) *fakeProvider {
	t.Helper()

	return &fakeProvider{
		ifaces: []*wifi.Interface{
			{Name: "mesh1", Type: wifi.InterfaceTypeMeshPoint},
			{Name: "phy0-ap0", Type: wifi.InterfaceTypeAP},
			{Name: "mesh0", Type: wifi.InterfaceTypeMeshPoint},
		},
		stations: map[string][]*wifi.StationInfo{
			"mesh1": {
				heStation(t, "9c:ef:d5:f9:80:4d", -61),
				{HardwareAddr: mac(t, "aa:bb:cc:dd:ee:ff"), Signal: -78, SignalAverage: -79, TransmitBitrate: 54_000},
			},
			"mesh0":    {},
			"phy0-ap0": {heStation(t, "11:22:33:44:55:66", -40)},
		},
	}
}

func TestSnapshotter_NilProvider(t *testing.T) {
	t.Parallel()

	var s wireless.Snapshotter

	s.Refresh()

	data, ok := s.Data().(*wireless.Snapshot)
	require.True(t, ok)
	assert.Empty(t, data.Error)
	assert.NotNil(t, data.Interfaces, "an empty list must marshal as [], not null")
	assert.Empty(t, data.Interfaces)

	out, err := json.Marshal(s.Data())
	require.NoError(t, err)
	assert.JSONEq(t, `{"interfaces":[]}`, string(out))
}

func TestSnapshotter_MapsMeshPointStations(t *testing.T) {
	t.Parallel()

	s := &wireless.Snapshotter{Provider: twoIfaceProvider(t)}
	s.Refresh()

	data, ok := s.Data().(*wireless.Snapshot)
	require.True(t, ok)
	assert.Empty(t, data.Error)
	require.Len(t, data.Interfaces, 2, "the AP interface is filtered out")

	mesh1 := data.Interfaces[0]
	assert.Equal(t, "mesh1", mesh1.Name)
	assert.Empty(t, mesh1.Error)
	require.Len(t, mesh1.Stations, 2)

	he := mesh1.Stations[0]
	assert.Equal(t, int32(-61), he.SignalDBm)
	assert.Equal(t, int32(-63), he.SignalAvgDBm)
	assert.Equal(t, int32(86700), he.TxBitrateKbps)
	assert.Equal(t, "he", he.TxPHY)
	assert.Equal(t, int32(40), he.TxWidthMHz)
	assert.Equal(t, int32(7), he.TxMCS)
	assert.Equal(t, int32(2), he.TxNSS)
	assert.Equal(t, int32(72200), he.RxBitrateKbps)
	assert.Equal(t, "ht", he.RxPHY)
	assert.Equal(t, int32(20), he.RxWidthMHz)
	assert.Equal(t, int32(7), he.RxMCS)
	assert.Equal(t, int32(1), he.RxNSS)
	assert.Equal(t, int64(12), he.TxRetries)
	assert.Equal(t, int64(3), he.TxFailed)
	assert.Equal(t, int64(1500), he.InactiveMs)

	plain := mesh1.Stations[1]
	assert.Equal(t, int32(54), plain.TxBitrateKbps, "no rate attrs: kbit/s from the plain bitrate")
	assert.Equal(t, "", plain.TxPHY)
	assert.Equal(t, int32(0), plain.TxWidthMHz)
	assert.Equal(t, int32(-1), plain.TxMCS)
	assert.Equal(t, int32(-1), plain.TxNSS)
	assert.Equal(t, int32(0), plain.RxBitrateKbps)

	mesh0 := data.Interfaces[1]
	assert.Equal(t, "mesh0", mesh0.Name)
	assert.NotNil(t, mesh0.Stations)
	assert.Empty(t, mesh0.Stations)

	out, err := json.Marshal(s.Data())
	require.NoError(t, err)
	assert.Contains(t, string(out), `"mac":"9c:ef:d5:f9:80:4d"`)
	assert.Contains(t, string(out), `"tx_phy":"he"`)
	assert.Contains(t, string(out), `"name":"mesh0","stations":[]`)
	assert.NotContains(t, string(out), `"error"`)
}

func TestSnapshotter_StationErrorIsPerInterface(t *testing.T) {
	t.Parallel()

	p := twoIfaceProvider(t)
	p.stationsErr = map[string]error{"mesh1": errors.New("netlink: no such device")}

	s := &wireless.Snapshotter{Provider: p}
	s.Refresh()

	data, ok := s.Data().(*wireless.Snapshot)
	require.True(t, ok)
	require.Len(t, data.Interfaces, 2)
	assert.Equal(t, "netlink: no such device", data.Interfaces[0].Error)
	assert.Empty(t, data.Interfaces[0].Stations)
	assert.Empty(t, data.Interfaces[1].Error)
}

func TestSnapshotter_InterfacesErrorIsSectionLevel(t *testing.T) {
	t.Parallel()

	p := twoIfaceProvider(t)
	p.ifacesErr = errors.New("nl80211 unavailable")

	s := &wireless.Snapshotter{Provider: p}
	s.Refresh()

	data, ok := s.Data().(*wireless.Snapshot)
	require.True(t, ok)
	assert.Equal(t, "nl80211 unavailable", data.Error)
	assert.Empty(t, data.Interfaces)
}

func TestSnapshotter_RefreshClearsPreviousState(t *testing.T) {
	t.Parallel()

	p := twoIfaceProvider(t)
	s := &wireless.Snapshotter{Provider: p}
	s.Refresh()

	p.ifaces = p.ifaces[:1] // only mesh1 now
	p.stations["mesh1"] = p.stations["mesh1"][:1]

	s.Refresh()

	data, ok := s.Data().(*wireless.Snapshot)
	require.True(t, ok)
	require.Len(t, data.Interfaces, 1)
	assert.Len(t, data.Interfaces[0].Stations, 1)
}

func TestSnapshotter_ShortMACMarshalsEmpty(t *testing.T) {
	t.Parallel()

	p := &fakeProvider{
		ifaces:   []*wifi.Interface{{Name: "mesh1", Type: wifi.InterfaceTypeMeshPoint}},
		stations: map[string][]*wifi.StationInfo{"mesh1": {{HardwareAddr: net.HardwareAddr{1, 2, 3}}}},
	}
	s := &wireless.Snapshotter{Provider: p}
	s.Refresh()

	out, err := json.Marshal(s.Data())
	require.NoError(t, err)
	assert.Contains(t, string(out), `"mac":""`)
}

func TestSnapshotter_DataPointerStable(t *testing.T) {
	t.Parallel()

	s := &wireless.Snapshotter{Provider: twoIfaceProvider(t)}

	first := s.Data()
	s.Refresh()
	second := s.Data()

	assert.Same(t, first, second)
}

// TestSnapshotter_ZeroAllocSteadyState proves Refresh allocates nothing
// once the slice capacities are established.
func TestSnapshotter_ZeroAllocSteadyState(t *testing.T) {
	// testing.AllocsPerRun must not be called under t.Parallel.
	s := &wireless.Snapshotter{Provider: twoIfaceProvider(t)}

	s.Refresh() // warm-up establishes capacities

	allocs := testing.AllocsPerRun(100, func() {
		s.Refresh()
	})

	assert.Equal(t, 0.0, allocs, "Refresh must not allocate after warm-up")
}
