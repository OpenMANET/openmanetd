package gpsd

import "time"

const (
	DefaultTAKGPSPort   string = "4349"
	DefaultGPSDAddress  string = "localhost:2947"
	atakSAMulticastPort string = "6969" // Port for ATAK SA multicast
	// atakSAMulticastPortNum is atakSAMulticastPort as an int for net.UDPAddr.
	atakSAMulticastPortNum int = 6969
	// atakMulticastTTL is the Time-To-Live value for CoT multicast packets sent to ATAK SA address
	atakMulticastTTL int = 64
	// defaultSelfMarkerType is the CoT type for self markers
	// defaultSelfMarkerType string = "a-f-G-U-C" // SELF MARKER
	// radioUnitType is the CoT type for a ground radio unit
	radioUnitType string = "a-f-G-U-U-S-R" // Gnd/RADIO UNIT;RADIO UNIT
	// defaultStaleDuration is the default duration before a CoT message is considered stale
	defaultStaleDuration time.Duration = 5 * time.Minute
	maxReconnectAttempts int           = 3
	// arpProbeTimeout bounds the ARP liveness probe sent to each DHCP lease
	// before deciding whether an EUD is still attached.
	arpProbeTimeout time.Duration = 500 * time.Millisecond
	// cotMulticastRateLimit is the minimum interval between CoT multicast sends to avoid flooding
	cotMulticastRateLimit time.Duration = 30 * time.Second // Minimum interval between CoT multicast sends

	// gpsdClassTPV is the gpsd JSON "class" value for a Time-Position-Velocity report.
	gpsdClassTPV string = "TPV"
	// gpsdClassSKY is the gpsd JSON "class" value for a satellite-visibility report.
	gpsdClassSKY string = "SKY"
	// gnssSourceGPS labels NMEA sentences originating from the GPS constellation.
	gnssSourceGPS string = "GPS"
)
