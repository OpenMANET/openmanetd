package wireless

import (
	"net"

	"github.com/mdlayher/wifi"
)

// Provider is the slice of the wireless provider the snapshotter reads.
// *handlers.CachedWirelessProvider satisfies it: on a warm cache both
// methods return cached slices without allocating, so Refresh stays
// allocation-free and adds no netlink polling of its own.
type Provider interface {
	Interfaces() ([]*wifi.Interface, error)
	StationInfo(iface *wifi.Interface) ([]*wifi.StationInfo, error)
}

// macText holds a colon-separated lowercase MAC ("aa:bb:cc:dd:ee:ff")
// formatted without allocating. n is 0 when the address was not 6
// bytes long, which marshals as "".
type macText struct {
	b [17]byte
	n uint8
}

const (
	hexDigits  = "0123456789abcdef"
	macBytes   = 6
	macTextLen = 17
)

// MarshalText implements encoding.TextMarshaler.
func (m *macText) MarshalText() ([]byte, error) {
	return m.b[:m.n], nil
}

func (m *macText) set(hw net.HardwareAddr) {
	if len(hw) != macBytes {
		m.n = 0

		return
	}

	for i, c := range hw {
		m.b[i*3] = hexDigits[c>>4]
		m.b[i*3+1] = hexDigits[c&0x0f]

		if i < macBytes-1 {
			m.b[i*3+2] = ':'
		}
	}

	m.n = macTextLen
}

// StationSnapshot is one peer on a mesh interface. Field semantics are
// documented in docs/instrumentation-snapshot.md — keep that file in
// sync when adding or renaming fields here.
type StationSnapshot struct {
	TxPHY         string  `json:"tx_phy"`
	RxPHY         string  `json:"rx_phy"`
	TxRetries     int64   `json:"tx_retries"`
	TxFailed      int64   `json:"tx_failed"`
	InactiveMs    int64   `json:"inactive_ms"`
	SignalDBm     int32   `json:"signal_dbm"`
	SignalAvgDBm  int32   `json:"signal_avg_dbm"`
	TxBitrateKbps int32   `json:"tx_bitrate_kbps"`
	TxWidthMHz    int32   `json:"tx_width_mhz"`
	TxMCS         int32   `json:"tx_mcs"`
	TxNSS         int32   `json:"tx_nss"`
	RxBitrateKbps int32   `json:"rx_bitrate_kbps"`
	RxWidthMHz    int32   `json:"rx_width_mhz"`
	RxMCS         int32   `json:"rx_mcs"`
	RxNSS         int32   `json:"rx_nss"`
	MAC           macText `json:"mac"`
}

func (r *StationSnapshot) fill(st *wifi.StationInfo) {
	r.MAC.set(st.HardwareAddr)
	r.SignalDBm = int32(st.Signal)
	r.SignalAvgDBm = int32(st.SignalAverage)

	tx := SummarizeRate(st.TransmitRateInfo, st.TransmitBitrate)
	r.TxBitrateKbps = tx.BitrateKbps
	r.TxPHY = tx.PHY.String()
	r.TxWidthMHz = tx.WidthMHz
	r.TxMCS = tx.MCS
	r.TxNSS = tx.NSS

	rx := SummarizeRate(st.ReceiveRateInfo, st.ReceiveBitrate)
	r.RxBitrateKbps = rx.BitrateKbps
	r.RxPHY = rx.PHY.String()
	r.RxWidthMHz = rx.WidthMHz
	r.RxMCS = rx.MCS
	r.RxNSS = rx.NSS

	r.TxRetries = int64(st.TransmitRetries)
	r.TxFailed = int64(st.TransmitFailed)
	r.InactiveMs = st.Inactive.Milliseconds()
}

// InterfaceSnapshot is one mesh-point interface with its stations.
// Error is set when StationInfo failed for this interface; Stations is
// then empty.
type InterfaceSnapshot struct {
	Name     string            `json:"name"`
	Error    string            `json:"error,omitempty"`
	Stations []StationSnapshot `json:"stations"`
}

const (
	initialInterfaceCap = 2
	initialStationCap   = 8
)

func (e *InterfaceSnapshot) fill(p Provider, iface *wifi.Interface) {
	e.Name = iface.Name
	e.Error = ""

	if e.Stations == nil {
		e.Stations = make([]StationSnapshot, 0, initialStationCap)
	}

	e.Stations = e.Stations[:0]

	stations, err := p.StationInfo(iface)
	if err != nil {
		e.Error = err.Error()

		return
	}

	for _, st := range stations {
		if st == nil {
			continue
		}

		n := len(e.Stations)
		if n < cap(e.Stations) {
			e.Stations = e.Stations[:n+1]
		} else {
			e.Stations = append(e.Stations, StationSnapshot{})
		}

		e.Stations[n].fill(st)
	}
}

// Snapshot is the "wireless" section: every mesh-point interface with
// its stations. Error is set when the interface list itself failed;
// Interfaces is then empty.
type Snapshot struct {
	Error      string              `json:"error,omitempty"`
	Interfaces []InterfaceSnapshot `json:"interfaces"`
}

// Snapshotter is the instrumentation.Snapshotter adapter for the
// wireless section. Provider must be set before registration; a nil
// Provider yields an empty section. The snapshot's slices are reused
// across Refresh calls so a warm-cache Refresh does not allocate.
type Snapshotter struct {
	Provider Provider
	data     Snapshot
}

// Refresh implements instrumentation.Snapshotter.
func (s *Snapshotter) Refresh() {
	dst := &s.data
	dst.Error = ""

	if dst.Interfaces == nil {
		dst.Interfaces = make([]InterfaceSnapshot, 0, initialInterfaceCap)
	}

	dst.Interfaces = dst.Interfaces[:0]

	if s.Provider == nil {
		return
	}

	ifaces, err := s.Provider.Interfaces()
	if err != nil {
		dst.Error = err.Error()

		return
	}

	for _, iface := range ifaces {
		if iface == nil || iface.Type != wifi.InterfaceTypeMeshPoint {
			continue
		}

		n := len(dst.Interfaces)
		if n < cap(dst.Interfaces) {
			dst.Interfaces = dst.Interfaces[:n+1]
		} else {
			dst.Interfaces = append(dst.Interfaces, InterfaceSnapshot{})
		}

		dst.Interfaces[n].fill(s.Provider, iface)
	}
}

// Data implements instrumentation.Snapshotter.
func (s *Snapshotter) Data() any {
	return &s.data
}
