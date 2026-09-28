package gpsd

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strings"
	"time"

	"github.com/coreywagehoft/go-tak/pkg/cot"
	"github.com/coreywagehoft/go-tak/pkg/cotproto"
	"github.com/mdlayher/arp"
	"github.com/openmanet/openmanetd/internal/camera"
	"github.com/openmanet/openmanetd/internal/config"
	"github.com/openmanet/openmanetd/internal/util/board"
	"golang.org/x/net/ipv4"
)

// SendIfRequiredAsCoT sends the GPS position as a Cursor-on-Target (CoT) message to End User Devices (EUDs).
// It first validates that a GPS position is available, then retrieves active DHCP leases to identify
// potential EUD recipients. The method checks each leased device for activity and attempts to send
// the CoT message to active devices. If no active devices are found, it falls back to sending a
// CoT message to the ATAK Situational Awareness (SA) multicast address, subject to rate limiting
// (once every 30 seconds) to prevent network flooding.
//
// The method performs the following steps:
//  1. Validates GPS position availability
//  2. Retrieves current DHCP leases
//  3. Checks each leased device for activity via ARP
//  4. If no active devices are found, sends to multicast (rate-limited)
//
// Errors are logged but do not halt execution; the method returns early on validation failures.
func (g *GPSService) SendIfRequiredAsCoT() {
	// Check if we have a valid GPS position
	if !g.IsValid() {
		g.Log.Warn().Msg("No valid GPS position to send to EUDs")

		return
	}

	if g.sendCameraCoTIfPresent(context.Background()) {
		return
	}

	leases, err := g.getDHCPLeases()
	if err != nil {
		g.Log.Error().Err(err).Msg("Error getting DHCP leases for EUD location update")

		return
	}

	// Track have ANY active device
	deviceActive := false

	// Send CoT messages to each active EUD device if configured
	// Send as CoT only if configured and NMEA sending is disabled
	if len(leases.DHCPLeases) > 0 {
		// loop through leases.DHCPleases and send location to each EUD
		for _, lease := range leases.DHCPLeases {
			// Send an ARP request to verify the EUD is online
			if g.checkDeviceActive(lease.IPAddr) {
				// Device is active
				deviceActive = true
			}
		}
	}

	// Only send to multicast if no devices received any messages
	if !deviceActive {
		if !g.reserveCoTMulticastSend() {
			return
		}

		if err := g.sendCoTPing(); err != nil {
			g.Log.Error().Err(err).Msg("Failed to send CoT ping to multicast")
		}

		g.Log.Debug().Msg("No reachable devices found, sending CoT to ATAK SA multicast address")

		if err := g.sendCoTToMulticast(); err != nil {
			g.Log.Error().Err(err).Msg("Failed to send CoT to multicast")
		}
	}
}

// sendCameraCoTIfPresent reports whether camera publication owns this update.
// A true result always prevents the caller from emitting a normal radio marker,
// including when the camera stream cannot currently be advertised.
func (g *GPSService) sendCameraCoTIfPresent(ctx context.Context) bool {
	if !g.cameraPresent {
		return false
	}

	if !g.reserveCoTMulticastSend() {
		return true
	}

	if err := g.sendCameraCoTToMulticast(ctx); err != nil {
		g.Log.Error().Err(err).Msg("Failed to send camera CoT to multicast")
	}

	return true
}

func (g *GPSService) reserveCoTMulticastSend() bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	if time.Since(g.lastMulticastTime) < cotMulticastRateLimit {
		return false
	}

	g.lastMulticastTime = time.Now()

	return true
}

func (g *GPSService) sendCameraCoTToMulticast(ctx context.Context) error {
	pos := g.GetPosition()
	if !pos.Valid {
		return fmt.Errorf("no valid GPS position")
	}

	stream, err := camera.ResolveStream(ctx)
	if err != nil {
		return fmt.Errorf("resolve camera stream: %w", err)
	}

	callsign := nodeCallsign()

	return camera.Publish(ctx, camera.Position{
		Altitude:        pos.Altitude,
		CE:              pos.EPH,
		GeoidSeparation: pos.GeoidSeparation,
		Lat:             pos.Latitude,
		LE:              pos.EPV,
		Lon:             pos.Longitude,
		Speed:           pos.Speed,
		Track:           pos.Track,
	}, camera.Node{
		Callsign: callsign,
		UID:      callsign,
	}, stream)
}

// checkDeviceActive reports whether a DHCP-leased address belongs to a
// live, directly-connected EUD. It finds the local interface whose subnet
// contains ipAddr and ARP-probes the address on it. Any failure (no local
// subnet contains the address, the ARP client cannot be opened, or the
// probe times out) reports inactive: a stale lease must never suppress
// the multicast fallback, which is the only way the marker reaches the
// mesh when no EUD is attached.
func (g *GPSService) checkDeviceActive(ipAddr string) bool {
	target, err := netip.ParseAddr(ipAddr)
	if err != nil {
		g.Log.Debug().Str("ip", ipAddr).Msg("Invalid IP address for ARP check")

		return false
	}

	ifi := g.interfaceForAddr(target)
	if ifi == nil {
		g.Log.Debug().Str("ip", ipAddr).Msg("No local interface contains lease address; treating device as inactive")

		return false
	}

	probe := g.arpProbe
	if probe == nil {
		probe = g.resolveARP
	}

	if err := probe(ifi, target); err != nil {
		g.Log.Debug().Err(err).Str("ip", ipAddr).Str("interface", ifi.Name).Msg("Device did not answer ARP")

		return false
	}

	return true
}

// interfaceForAddr returns the up, non-loopback interface whose IPv4
// subnet contains target, or nil when none does.
func (g *GPSService) interfaceForAddr(target netip.Addr) *net.Interface {
	list := g.listInterfaces
	if list == nil {
		list = net.Interfaces
	}

	addrsOf := g.interfaceAddrs
	if addrsOf == nil {
		addrsOf = (*net.Interface).Addrs
	}

	ifaces, err := list()
	if err != nil {
		g.Log.Debug().Err(err).Msg("Failed to get network interfaces for ARP check")

		return nil
	}

	ip := net.IP(target.AsSlice())

	for i := range ifaces {
		ifi := &ifaces[i]

		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagLoopback != 0 {
			continue
		}

		addrs, err := addrsOf(ifi)
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if ok && ipNet.Contains(ip) {
				return ifi
			}
		}
	}

	return nil
}

// resolveARP is the real ARP probe: one request on ifi with a short
// deadline. A client that cannot be opened (raw socket denied, interface
// without a hardware address) is logged at Warn because it silently
// degrades EUD detection on every lease.
func (g *GPSService) resolveARP(ifi *net.Interface, target netip.Addr) error {
	client, err := arp.Dial(ifi)
	if err != nil {
		g.Log.Warn().Err(err).Str("interface", ifi.Name).Msg("Failed to create ARP client")

		return fmt.Errorf("arp dial %s: %w", ifi.Name, err)
	}
	defer client.Close()

	if err := client.SetDeadline(time.Now().Add(arpProbeTimeout)); err != nil {
		return fmt.Errorf("set ARP deadline: %w", err)
	}

	if _, err := client.Resolve(target); err != nil {
		return fmt.Errorf("arp resolve %s: %w", target, err)
	}

	return nil
}

// sendCoTToMulticast creates and sends an ATAK CoT message to the standard multicast address
func (g *GPSService) sendCoTToMulticast() error {
	pos := g.GetPosition()
	if !pos.Valid {
		return fmt.Errorf("no valid GPS position")
	}

	deviceInfo, err := board.NewBoardConfigInfo()
	if err != nil {
		g.Log.Warn().Err(err).Msg("Failed to get board config info for CoT message")
	}

	hostname := nodeCallsign()

	// Get platform name, handle nil deviceInfo
	platformName := "OpenMANET"

	if deviceInfo != nil {
		modelName := deviceInfo.Model.GetName()
		if modelName != "" {
			platformName = modelName
		}
	}

	// Calculate Height Above Ellipsoid (HAE)
	// HAE = MSL altitude + Geoid Separation
	hae := pos.Altitude
	if pos.GeoidSeparation != 0 {
		hae = pos.Altitude + pos.GeoidSeparation
	}

	// Create CoT Message
	takMsg := &cotproto.TakMessage{
		CotEvent: &cotproto.CotEvent{
			Type:      radioUnitType,
			Uid:       hostname,
			SendTime:  cot.TimeToMillis(time.Now()),
			StartTime: cot.TimeToMillis(time.Now()),
			StaleTime: cot.TimeToMillis(time.Now().Add(defaultStaleDuration)),
			How:       cot.HowDefault,
			Lat:       pos.Latitude,
			Lon:       pos.Longitude,
			Hae:       hae,
			Ce:        pos.EPH,
			Le:        pos.EPV,
			Detail: &cotproto.Detail{
				Contact: &cotproto.Contact{
					Callsign: hostname,
				},
				Group: &cotproto.Group{
					Name: "Magenta",
					Role: "MANET Radio",
				},
				Takv: &cotproto.Takv{
					Device:   hostname,
					Platform: fmt.Sprintf("%s (%s)", platformName, "OpenMANET"),
				},
				Track: &cotproto.Track{
					Speed:  pos.Speed,
					Course: pos.Track,
				},
				PrecisionLocation: &cotproto.PrecisionLocation{
					Geopointsrc: gnssSourceGPS,
					Altsrc:      gnssSourceGPS,
				},
			},
		},
	}

	// Marshal to bytes to send as protobuf
	data, err := cot.MakeProtoMeshPacketV1(takMsg)
	if err != nil {
		return fmt.Errorf("failed to marshal CoT protobuf: %w", err)
	}

	if err := g.sendMulticast(data); err != nil {
		return fmt.Errorf("failed to send CoT message: %w", err)
	}

	g.Log.Debug().
		Str("callsign", hostname).
		Float64("lat", pos.Latitude).
		Float64("lon", pos.Longitude).
		Float64("alt", pos.Altitude).
		Str("address", fmt.Sprintf("%s:%s", config.ATAKSAAddress, atakSAMulticastPort)).
		Msg("Sent CoT message to ATAK SA multicast")

	return nil
}

func nodeCallsign() string {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "openmanet-node"
	}

	// Preserve the existing MANET suffix used by normal radio markers.
	if !strings.Contains(hostname, "manet") {
		hostname = fmt.Sprintf("%s-MANET", hostname)
	}

	return hostname
}

// sendCoTAsExternalGPS creates and sends an ATAK CoT message to the EUD.
func (g *GPSService) sendCoTTAsExternalGPS(iPAddr string) error {
	pos := g.GetPosition()
	if !pos.Valid {
		return fmt.Errorf("no valid GPS position")
	}

	// Calculate Height Above Ellipsoid (HAE)
	// HAE = MSL altitude + Geoid Separation
	hae := pos.Altitude
	if pos.GeoidSeparation != 0 {
		hae = pos.Altitude + pos.GeoidSeparation
	}

	event := &cotproto.CotEvent{
		Uid:       "External-GPS",
		Type:      "a-f-G-E-S",
		SendTime:  cot.TimeToMillis(time.Now()),
		StartTime: cot.TimeToMillis(time.Now()),
		StaleTime: cot.TimeToMillis(time.Now().Add(defaultStaleDuration)),
		How:       cot.HowDefault,
		Lat:       pos.Latitude,
		Lon:       pos.Longitude,
		Hae:       hae,
		Le:        pos.EPV,
		Ce:        pos.EPH,
		Detail: &cotproto.Detail{
			Track: &cotproto.Track{
				Speed:  pos.Speed,
				Course: pos.Track,
			},
			PrecisionLocation: &cotproto.PrecisionLocation{
				Geopointsrc: gnssSourceGPS,
				Altsrc:      gnssSourceGPS,
			},
		},
	}

	cotEvent := cot.CotToEvent(event)

	// Marshal to XML
	xmlData, err := xml.Marshal(cotEvent)
	if err != nil {
		return fmt.Errorf("failed to marshal CoT XML: %w", err)
	}

	// Send to device address
	addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%s", iPAddr, DefaultTAKGPSPort))
	if err != nil {
		return fmt.Errorf("failed to resolve device address: %w", err)
	}

	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		return fmt.Errorf("failed to dial device address: %w", err)
	}
	defer conn.Close()

	_, err = conn.Write(xmlData)
	if err != nil {
		return fmt.Errorf("failed to send CoT message: %w", err)
	}

	g.Log.Debug().
		Float64("lat", pos.Latitude).
		Float64("lon", pos.Longitude).
		Float64("alt", pos.Altitude).
		Str("address", iPAddr).
		Msg("Sent CoT message to ATAK device")

	return nil
}

func (g *GPSService) sendCoTPing() error {
	// Marshal to bytes to send as protobuf
	data, err := cot.MakeProtoMeshPacketV1(cot.MakePing("openmanet-ping"))
	if err != nil {
		return fmt.Errorf("failed to marshal CoT protobuf: %w", err)
	}

	if err := g.sendMulticast(data); err != nil {
		return fmt.Errorf("failed to send CoT message: %w", err)
	}

	return nil
}

// sendMulticast dispatches to the injected SendMulticast override when set
// (tests), falling back to the real interface-pinned sender otherwise.
func (g *GPSService) sendMulticast(data []byte) error {
	if g.SendMulticast != nil {
		return g.SendMulticast(data)
	}

	return g.sendCoTMulticast(data)
}

// sendCoTMulticast writes one datagram to the ATAK SA multicast group with
// the egress interface pinned explicitly, rather than letting the kernel
// route the group address. An unpinned dial to 239.2.3.1 depends on the
// unicast routing table: a node with no uplink and no batman-adv gateway
// selected has no default route and the dial fails with "network is
// unreachable", while a node with a WAN uplink sends the marker out the
// WAN instead of the mesh bridge. Pinning mirrors the listener's join
// strategy (see joinMulticastOnAllInterfaces) and the camera publisher.
//
// The configured mesh bridge is preferred; when it is absent or down the
// datagram is sent on every up, multicast-capable, non-loopback interface
// so a differently-named bridge still carries the marker. The send
// succeeds if at least one interface accepted the datagram.
func (g *GPSService) sendCoTMulticast(data []byte) error {
	byName := g.interfaceByName
	if byName == nil {
		byName = net.InterfaceByName
	}

	list := g.listInterfaces
	if list == nil {
		list = net.Interfaces
	}

	candidates := selectMulticastInterfaces(g.meshInterfaceName(), byName, list)
	if len(candidates) == 0 {
		return errors.New("no multicast-capable interface available for CoT send")
	}

	dst := &net.UDPAddr{IP: net.ParseIP(config.ATAKSAAddress), Port: atakSAMulticastPortNum}

	var (
		errs []error
		sent bool
	)

	for i := range candidates {
		if err := writeMulticastOn(&candidates[i], dst, data); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", candidates[i].Name, err))

			continue
		}

		sent = true
	}

	if !sent {
		return errors.Join(errs...)
	}

	for _, err := range errs {
		g.Log.Debug().Err(err).Msg("CoT multicast send failed on one interface")
	}

	return nil
}

// writeMulticastOn sends data to dst with the socket's multicast egress
// forced to ifi.
func writeMulticastOn(ifi *net.Interface, dst *net.UDPAddr, data []byte) error {
	conn, err := net.ListenUDP("udp4", nil)
	if err != nil {
		return fmt.Errorf("open multicast socket: %w", err)
	}
	defer conn.Close()

	pconn := ipv4.NewPacketConn(conn)

	if err := pconn.SetMulticastInterface(ifi); err != nil {
		return fmt.Errorf("set multicast interface: %w", err)
	}

	if err := pconn.SetMulticastTTL(atakMulticastTTL); err != nil {
		return fmt.Errorf("set multicast TTL: %w", err)
	}

	if _, err := pconn.WriteTo(data, nil, dst); err != nil {
		return fmt.Errorf("write: %w", err)
	}

	return nil
}

// meshInterfaceName returns the configured mesh bridge name, or the
// package default when no Config was wired up.
func (g *GPSService) meshInterfaceName() string {
	if g.Config == nil {
		return config.DefaultMeshNetInterface
	}

	return g.Config.GetMeshNetInterface()
}

// selectMulticastInterfaces picks the interfaces a CoT multicast datagram
// is sent on. The named mesh interface wins when it resolves and is up;
// otherwise every up, multicast-capable, non-loopback interface is a
// candidate. An empty result means no usable interface exists.
func selectMulticastInterfaces(
	meshName string,
	byName func(string) (*net.Interface, error),
	list func() ([]net.Interface, error),
) []net.Interface {
	if ifi, err := byName(meshName); err == nil && multicastCapable(ifi) {
		return []net.Interface{*ifi}
	}

	ifaces, err := list()
	if err != nil {
		return nil
	}

	out := make([]net.Interface, 0, len(ifaces))

	for i := range ifaces {
		if multicastCapable(&ifaces[i]) {
			out = append(out, ifaces[i])
		}
	}

	return out
}

func multicastCapable(ifi *net.Interface) bool {
	return ifi.Flags&net.FlagUp != 0 &&
		ifi.Flags&net.FlagMulticast != 0 &&
		ifi.Flags&net.FlagLoopback == 0
}
