package gpsd

import (
	"fmt"
	"math"
	"net"
)

// ToNMEA converts the current position to NMEA GGA format
// GGA - Global Positioning System Fix Data
func (g *GPSService) ToNMEA() string {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if !g.position.Valid {
		return ""
	}

	return formatGGA(g.position)
}

// formatGGA formats a position report as an NMEA GGA sentence
func formatGGA(pos PositionReport) string {
	// Calculate time in HHMMSS.ss format
	timeStr := pos.Timestamp.UTC().Format("150405.00")

	// Convert latitude to NMEA format (DDMM.MMMM)
	latDeg := math.Abs(pos.Latitude)
	latDegInt := int(latDeg)
	latMin := (latDeg - float64(latDegInt)) * 60.0
	latStr := fmt.Sprintf("%02d%08.5f", latDegInt, latMin)

	latHem := "N"
	if pos.Latitude < 0 {
		latHem = "S"
	}

	// Convert longitude to NMEA format (DDDMM.MMMM)
	lonDeg := math.Abs(pos.Longitude)
	lonDegInt := int(lonDeg)
	lonMin := (lonDeg - float64(lonDegInt)) * 60.0
	lonStr := fmt.Sprintf("%03d%08.5f", lonDegInt, lonMin)

	lonHem := "E"
	if pos.Longitude < 0 {
		lonHem = "W"
	}

	// Quality indicator based on GPS fix mode and DGPS status
	// 0 = Invalid, 1 = GPS fix (SPS), 2 = DGPS fix, 3 = PPS fix, etc.
	// GPSD Mode: 0/1 = no fix, 2 = 2D fix, 3 = 3D fix
	quality := "0"

	if pos.Mode >= 2 {
		if pos.DGPSStation > 0 {
			quality = "2" // DGPS fix
		} else {
			quality = "1" // GPS fix (SPS)
		}
	}

	// Number of satellites - use actual count or 00 if unknown
	numSat := "00"
	if pos.SatellitesUsed > 0 {
		numSat = fmt.Sprintf("%02d", pos.SatellitesUsed)
	}

	// Horizontal dilution of precision - only include if we have real data
	hdop := ""
	if pos.HDOP > 0 {
		hdop = fmt.Sprintf("%.1f", pos.HDOP)
	}

	// Altitude in meters
	altStr := fmt.Sprintf("%.1f", pos.Altitude)
	altUnit := "M"

	// Height of geoid (WGS84) above WGS84 ellipsoid - only include if we have real data
	geoidHeight := ""
	if pos.GeoidSeparation != 0 {
		geoidHeight = fmt.Sprintf("%.1f", pos.GeoidSeparation)
	}

	geoidUnit := "M"

	// Time since last DGPS update (empty if not using DGPS)
	dgpsAge := ""
	if pos.DGPSAge > 0 {
		dgpsAge = fmt.Sprintf("%.1f", pos.DGPSAge)
	}

	// DGPS station ID (empty if not using DGPS)
	dgpsID := ""
	if pos.DGPSStation > 0 {
		dgpsID = fmt.Sprintf("%04d", pos.DGPSStation)
	}

	// Construct the NMEA sentence (without checksum initially)
	sentence := fmt.Sprintf("GPGGA,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s",
		timeStr, latStr, latHem, lonStr, lonHem, quality, numSat, hdop,
		altStr, altUnit, geoidHeight, geoidUnit, dgpsAge, dgpsID)

	// Calculate checksum
	checksum := calculateNMEAChecksum(sentence)

	// Return complete NMEA sentence with $ prefix and checksum
	return fmt.Sprintf("$%s*%02X", sentence, checksum)
}

// calculateNMEAChecksum calculates the XOR checksum for an NMEA sentence
func calculateNMEAChecksum(sentence string) byte {
	var checksum byte = 0
	for i := 0; i < len(sentence); i++ {
		checksum ^= sentence[i]
	}

	return checksum
}

// sendRawNMEAToActiveDevices sends a raw NMEA sentence to all active DHCP lease devices.
func (g *GPSService) sendRawNMEAToActiveDevices(sentence string) {
	// Get current DHCP leases
	leases, err := g.getDHCPLeases()
	if err != nil {
		g.Log.Debug().Err(err).Msg("Failed to get DHCP leases for NMEA distribution")

		return
	}

	if len(leases.DHCPLeases) == 0 {
		return
	}

	// Send to each active device
	for _, lease := range leases.DHCPLeases {
		ipAddr := lease.IPAddr

		// Check if device is active via ARP
		isActive := g.checkDeviceActive(ipAddr)
		if !isActive {
			continue
		}

		// Send raw NMEA sentence via UDP
		addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%s", ipAddr, DefaultTAKGPSPort))
		if err != nil {
			g.Log.Debug().Err(err).Str("ip", ipAddr).Msg("Failed to resolve address for NMEA")

			continue
		}

		conn, err := net.DialUDP("udp", nil, addr)
		if err != nil {
			g.Log.Debug().Err(err).Str("ip", ipAddr).Msg("Failed to dial UDP for NMEA")

			continue
		}

		// Add newline to NMEA sentence
		nmeaWithNewline := sentence + "\r\n"

		_, err = conn.Write([]byte(nmeaWithNewline))
		conn.Close()

		if err != nil {
			g.Log.Debug().Err(err).Str("ip", ipAddr).Msg("Failed to send raw NMEA")
		}
	}
}
