package gpsd

import (
	"context"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"github.com/openmanet/openmanetd/internal/config"
	"github.com/openmanet/openmanetd/internal/network"
	"github.com/rs/zerolog"
)

// PositionReport holds the current GPS position data
type PositionReport struct {
	Timestamp       time.Time // Time of position fix
	Latitude        float64   // Latitude in degrees
	Longitude       float64   // Longitude in degrees
	Altitude        float64   // Altitude in meters above sea level
	Speed           float64   // Speed over ground in m/s
	Track           float64   // Course over ground in degrees
	Climb           float64   // Climb/sink rate in m/s
	Valid           bool      // Whether the position data is valid
	Mode            int       // GPS fix mode (0=no fix, 1=no fix, 2=2D, 3=3D)
	SatellitesUsed  int       // Number of satellites used in navigation solution
	HDOP            float64   // Horizontal dilution of precision
	GeoidSeparation float64   // Height of geoid above WGS84 ellipsoid in meters
	EPH             float64   // Estimated horizontal position error (1-sigma, meters)
	EPX             float64   // Estimated longitude error (1-sigma, meters)
	EPY             float64   // Estimated latitude error (1-sigma, meters)
	EPV             float64   // Estimated altitude error (1-sigma, meters)
	DGPSAge         float64   // Age of DGPS data in seconds (0 if not using DGPS)
	DGPSStation     int       // DGPS reference station ID (0 if not using DGPS)
}

// TPVReport represents a Time-Position-Velocity report from GPSD
type TPVReport struct {
	Class    string  `json:"class"`
	Device   string  `json:"device,omitempty"`
	Time     string  `json:"time,omitempty"`
	Mode     int     `json:"mode"`
	Lat      float64 `json:"lat,omitempty"`
	Lon      float64 `json:"lon,omitempty"`
	Alt      float64 `json:"alt,omitempty"`
	Track    float64 `json:"track,omitempty"`
	Speed    float64 `json:"speed,omitempty"`
	Climb    float64 `json:"climb,omitempty"`
	GeoidSep float64 `json:"geoidSep,omitempty"` // Geoid separation in meters
	EPH      float64 `json:"eph,omitempty"`      // Estimated horizontal position error (1-sigma, meters)
	EPX      float64 `json:"epx,omitempty"`      // Estimated longitude error (1-sigma, meters)
	EPY      float64 `json:"epy,omitempty"`      // Estimated latitude error (1-sigma, meters)
	EPV      float64 `json:"epv,omitempty"`      // Estimated altitude error (1-sigma, meters)
	DGPSAge  float64 `json:"dgpsAge,omitempty"`  // Age of DGPS data in seconds
	DGPSSta  int     `json:"dgpsSta,omitempty"`  // DGPS reference station ID
}

// SKYSatellite is the per-satellite shape inside a gpsd SKY report.
type SKYSatellite struct {
	PRN    int     `json:"PRN"`
	El     float64 `json:"el"`
	Az     float64 `json:"az"`
	Ss     float64 `json:"ss"`
	Used   bool    `json:"used"`
	Gnssid int     `json:"gnssid,omitempty"`
}

// SKYReport represents a satellite information report from GPSD
type SKYReport struct {
	Class      string         `json:"class"`
	Device     string         `json:"device,omitempty"`
	Time       string         `json:"time,omitempty"`
	Satellites []SKYSatellite `json:"satellites,omitempty"`
	HDOP       float64        `json:"hdop,omitempty"` // Horizontal dilution of precision
	VDOP       float64        `json:"vdop,omitempty"` // Vertical dilution of precision
	PDOP       float64        `json:"pdop,omitempty"` // Position dilution of precision
	NSat       int            `json:"nSat,omitempty"` // Number of satellites visible
	USat       int            `json:"uSat,omitempty"` // Number of satellites used in solution
}

// SatelliteInfo holds data for a single visible satellite.
type SatelliteInfo struct {
	PRN    int     // Satellite PRN number
	El     float64 // Elevation in degrees
	Az     float64 // Azimuth in degrees
	Ss     float64 // Signal strength (SNR) in dB-Hz
	Used   bool    // Whether this satellite is used in the navigation solution
	Gnssid int     // gpsd gnssid (0=GPS,1=SBAS,2=Galileo,3=BeiDou,5=QZSS,6=GLONASS,7=IRNSS); 0 if absent
}

// SatelliteReport holds the cached satellite constellation data from the last SKY report.
type SatelliteReport struct {
	Timestamp  time.Time       // Time of the last SKY report that carried constellation data
	Satellites []SatelliteInfo // Individual satellite data
	HDOP       float64         // Horizontal dilution of precision
	VDOP       float64         // Vertical dilution of precision
	PDOP       float64         // Position dilution of precision
	NSat       int             // Number of satellites visible
	USat       int             // Number of satellites used
}

// GPSService represents a GPS service client that connects to GPSD
type GPSService struct {
	Log               zerolog.Logger
	lastMulticastTime time.Time
	conn              net.Conn
	// GetDHCPLeases overrides the DHCP lease lookup used to identify
	// directly-connected EUDs (e.g. for CoT sender validation). Falls back
	// to network.GetCurrentDHCPLeases when nil; tests set this to a fake.
	GetDHCPLeases func() (*network.DHCPLeasesResponse, error)
	// SendMulticast overrides the datagram send used for CoT multicast
	// (ping + radio marker). Falls back to sendCoTMulticast when nil; tests
	// set this to capture the emitted packets.
	SendMulticast func([]byte) error
	// interfaceByName and listInterfaces override net.InterfaceByName and
	// net.Interfaces for multicast egress selection; nil means the real
	// functions. Tests set these to drive selectMulticastInterfaces.
	interfaceByName func(string) (*net.Interface, error)
	listInterfaces  func() ([]net.Interface, error)
	// interfaceAddrs and arpProbe override (*net.Interface).Addrs and the
	// ARP liveness probe used by checkDeviceActive; nil means the real
	// implementations. Tests set these to simulate a LAN without raw sockets.
	interfaceAddrs    func(*net.Interface) ([]net.Addr, error)
	arpProbe          func(*net.Interface, netip.Addr) error
	done              chan struct{}
	Config            *config.Config
	cancel            context.CancelFunc
	address           string
	satellites        SatelliteReport
	position          PositionReport
	reconnectDelay    time.Duration
	reconnectAttempts int
	mu                sync.RWMutex
	cameraPresent     bool
	// reannouncing is a single-flight guard for the CoT re-announce kicked
	// off when an external position is adopted. SendIfRequiredAsCoT does a
	// ubus lease lookup plus an ARP probe per lease, which can outlast an
	// EUD's ~1 Hz SA broadcast interval; without this, a fast broadcaster
	// would stack up unbounded overlapping goroutines.
	reannouncing atomic.Bool
}
