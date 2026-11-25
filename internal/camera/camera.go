// Package camera discovers node cameras and publishes their Cursor-on-Target
// video and sensor events to ATAK.
package camera

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/openmanet/openmanetd/internal/network"
)

const (
	defaultCameraInterface = "ahwlan"
	defaultRTSPPort        = 554
	defaultSRTPort         = 8890
	defaultRTSPName        = "rpicamera"
	cameraProbeTimeout     = 2 * time.Second
)

// Stream describes the video endpoint advertised to ATAK.
type Stream struct {
	Protocol  string
	LatencyMS int
	Address   string
	Path      string
	Port      int
}

// URL returns the stream's playback URL.
func (s Stream) URL() string {
	u := s.endpoint()
	return u.String()
}

func (s Stream) endpoint() url.URL {
	u := url.URL{
		Scheme: s.Protocol,
		Host:   net.JoinHostPort(s.Address, strconv.Itoa(s.Port)),
		Path:   s.Path,
	}

	if s.Protocol == "srt" {
		u.RawQuery = "streamid=read:" + strings.TrimPrefix(u.EscapedPath(), "/")
		u.Path = ""
		if s.LatencyMS > 0 {
			u.RawQuery += "&latency=" + strconv.Itoa(s.LatencyMS*1000)
		}
	}
	return u
}

// Detect reports whether cam can see a libcamera-compatible sensor.
func Detect(ctx context.Context) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, cameraProbeTimeout)
	defer cancel()

	output, err := exec.CommandContext(ctx, "cam", "-l").Output() //nolint:gosec // fixed local command and arguments
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return false, nil
		}

		return false, fmt.Errorf("list cameras: %w", err)
	}

	return cameraListContainsSensor(output), nil
}

// ResolveStream returns the existing MediaMTX endpoint on the configured
// OpenMANET camera interface. It requires no openmanetd-specific setting.
func ResolveStream(ctx context.Context) (Stream, error) {
	return resolveCameraStreamWith(ctx, network.NewUCINetworkConfigReader(), network.GetInterfaceByName)
}

type configReader interface {
	Get(config, section, option string) ([]string, bool)
}

type interfaceLookup func(string) network.NetworkInterface

func resolveCameraStreamWith(ctx context.Context, reader configReader, lookup interfaceLookup) (Stream, error) {
	if err := ctx.Err(); err != nil {
		return Stream{}, fmt.Errorf("resolve camera stream: %w", err)
	}

	interfaceName := firstConfigValue(reader, "camera-onvif-server", "rpicamera", "interface", defaultCameraInterface)
	deviceName := firstConfigValue(reader, "network", interfaceName, "device", network.DefaultBridgeInterfaceName)

	address := firstIPv4Address(lookup(deviceName))

	if err := ctx.Err(); err != nil {
		return Stream{}, fmt.Errorf("resolve camera stream: %w", err)
	}

	if address == "" {
		return Stream{}, fmt.Errorf("camera interface %q has no IPv4 address", deviceName)
	}

	protocol := firstConfigValue(reader, "openmanet_video", "playback", "transport", "srt")
	port := defaultSRTPort
	var latency int
	if protocol == "rtsp" {
		port = defaultRTSPPort
	} else {
		protocol = "srt"
		ms, err := strconv.Atoi(firstConfigValue(reader, "openmanet_video", "playback", "srt_latency_ms", ""))
		if err == nil && ms >= 20 && ms <= 8000 {
			latency = ms
		}
	}
	listener := firstConfigValue(reader, "mediamtx", "@mediamtx[0]", protocol+"_address", "")
	rtspName := firstConfigValue(reader, "camera-onvif-server", "rpicamera", "rtsp_name", defaultRTSPName)

	return Stream{
		Address:   address,
		Path:      "/" + strings.TrimLeft(rtspName, "/"),
		Protocol:  protocol,
		LatencyMS: latency,
		Port:      parsePort(listener, port),
	}, nil
}

func cameraListContainsSensor(output []byte) bool {
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		prefix, _, ok := strings.Cut(strings.TrimSpace(scanner.Text()), ":")
		if !ok {
			continue
		}

		if _, err := strconv.Atoi(strings.TrimSpace(prefix)); err == nil {
			return true
		}
	}

	return false
}

func firstConfigValue(reader configReader, configName, section, option, fallback string) string {
	values, ok := reader.Get(configName, section, option)
	if !ok || len(values) == 0 || strings.TrimSpace(values[0]) == "" {
		return fallback
	}

	return strings.TrimSpace(values[0])
}

func firstIPv4Address(iface network.NetworkInterface) string {
	for _, address := range iface.IP {
		ip := address.IP.To4()
		if ip != nil && !ip.IsLoopback() {
			return ip.String()
		}
	}

	return ""
}

func parsePort(address string, fallback int) int {
	value := strings.TrimSpace(address)
	if _, port, err := net.SplitHostPort(value); err == nil {
		value = port
	} else {
		value = strings.TrimPrefix(value, ":")
	}

	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		return fallback
	}

	return port
}
