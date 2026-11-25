package camera

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/coreywagehoft/go-tak/pkg/cot"
	"github.com/coreywagehoft/go-tak/pkg/cotproto"
	"github.com/openmanet/openmanetd/internal/config"
	"github.com/openmanet/openmanetd/internal/network"
	"golang.org/x/net/ipv4"
)

const (
	cameraSensorType     = "b-m-p-s-p-loc"
	cameraStaleDuration  = 5 * time.Minute
	cameraMulticastPort  = 6969
	cameraMulticastTTL   = 64
	cameraPublishTimeout = 2 * time.Second
	xmlAttributeUID      = "uid"
)

// Position contains the GNSS values used to locate camera events.
type Position struct {
	Altitude        float64
	CE              float64
	GeoidSeparation float64
	Lat             float64
	LE              float64
	Lon             float64
	Speed           float64
	Track           float64
}

// Node identifies the OpenMANET camera node.
type Node struct {
	Callsign string
	UID      string
}

// BuildMessage builds one camera sensor event containing its video
// source. It intentionally does not build a normal radio marker.
func BuildMessage(position Position, node Node, stream Stream) *cotproto.TakMessage {
	videoUID := node.UID + "-video"
	detail := cot.NewXMLDetails()
	detail.AddChild("sensor", map[string]string{
		"azimuth":   "0",
		"elevation": "0",
		"fov":       "45",
		"hideFov":   "true",
		"range":     "100",
		"vfov":      "45",
	}, "")
	video := detail.AddChild("__video", map[string]string{
		xmlAttributeUID: videoUID,
		"url":           stream.URL(),
	}, "")
	path := stream.Path
	if stream.Protocol == "srt" {
		endpoint := stream.endpoint()
		path = endpoint.RequestURI()
	}
	video.AddChild("ConnectionEntry", map[string]string{
		"address":           stream.Address,
		"alias":             node.Callsign + " Video",
		"bufferTime":        "-1",
		"ignoreEmbeddedKLV": "false",
		"networkTimeout":    "12000",
		"path":              path,
		"port":              strconv.Itoa(stream.Port),
		"protocol":          stream.Protocol,
		"roverPort":         "-1",
		"rtspReliable":      "0",
		xmlAttributeUID:     videoUID,
	}, "")

	return cameraMessage(cameraSensorType, node.UID, node.Callsign, position, detail.AsXMLString())
}

// Publish publishes a mesh ping followed by one camera sensor event
// containing its video source through the OpenMANET bridge.
func Publish(ctx context.Context, position Position, node Node, stream Stream) error {
	ctx, cancel := context.WithTimeout(ctx, cameraPublishTimeout)
	defer cancel()

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("publish camera CoT: %w", err)
	}

	destination, err := net.ResolveUDPAddr("udp4", net.JoinHostPort(config.ATAKSAAddress, strconv.Itoa(cameraMulticastPort)))
	if err != nil {
		return fmt.Errorf("resolve ATAK multicast address: %w", err)
	}

	bridge, err := net.InterfaceByName(network.DefaultBridgeInterfaceName)
	if err != nil {
		return fmt.Errorf("find ATAK multicast interface %q: %w", network.DefaultBridgeInterfaceName, err)
	}

	conn, err := net.ListenUDP("udp4", nil)
	if err != nil {
		return fmt.Errorf("open ATAK multicast socket: %w", err)
	}
	defer func() {
		// The send result is authoritative; a UDP close error is not recoverable here.
		_ = conn.Close()
	}()

	deadline, ok := ctx.Deadline()
	if ok {
		if err := conn.SetWriteDeadline(deadline); err != nil {
			return fmt.Errorf("set ATAK multicast write deadline: %w", err)
		}
	}

	packet := ipv4.NewPacketConn(conn)

	return publishCameraMessage(ctx, packet, bridge, destination, BuildMessage(position, node, stream))
}

type multicastPacketWriter interface {
	SetMulticastInterface(*net.Interface) error
	SetMulticastTTL(int) error
	WriteTo([]byte, *ipv4.ControlMessage, net.Addr) (int, error)
}

func publishCameraMessage(
	ctx context.Context,
	packet multicastPacketWriter,
	bridge *net.Interface,
	destination *net.UDPAddr,
	message *cotproto.TakMessage,
) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("write ATAK multicast packet: %w", err)
	}

	if err := packet.SetMulticastTTL(cameraMulticastTTL); err != nil {
		return fmt.Errorf("set ATAK multicast TTL: %w", err)
	}

	if err := packet.SetMulticastInterface(bridge); err != nil {
		return fmt.Errorf("set ATAK multicast interface %q: %w", bridge.Name, err)
	}

	if err := writeCameraMessage(ctx, packet, destination, cot.MakePing("openmanet-ping")); err != nil {
		return err
	}

	return writeCameraMessage(ctx, packet, destination, message)
}

func writeCameraMessage(
	ctx context.Context,
	packet multicastPacketWriter,
	destination *net.UDPAddr,
	message *cotproto.TakMessage,
) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("write ATAK multicast packet: %w", err)
	}

	data, err := cot.MakeProtoMeshPacketV1(message)
	if err != nil {
		return fmt.Errorf("marshal ATAK mesh packet: %w", err)
	}

	if _, err := packet.WriteTo(data, nil, destination); err != nil {
		return fmt.Errorf("write ATAK multicast packet: %w", err)
	}

	return nil
}

func cameraMessage(eventType, uid, callsign string, position Position, xmlDetail string) *cotproto.TakMessage {
	message := cot.BasicMsg(eventType, uid, cameraStaleDuration)
	event := message.CotEvent
	event.Lat = position.Lat
	event.Lon = position.Lon
	event.Hae = position.Altitude + position.GeoidSeparation
	event.Ce = position.CE
	event.Le = position.LE
	event.Detail = &cotproto.Detail{
		XmlDetail: xmlDetail,
		Contact:   &cotproto.Contact{Callsign: callsign},
		Track: &cotproto.Track{
			Course: position.Track,
			Speed:  position.Speed,
		},
		PrecisionLocation: &cotproto.PrecisionLocation{
			Altsrc:      "GPS",
			Geopointsrc: "GPS",
		},
	}

	return message
}
