package comms

import (
	"strings"

	evdev "github.com/gvalkov/golang-evdev"

	"github.com/openmanet/openmanetd/internal/comms/device"
)

const (
	defaultControlSourceOpenVLM = "openvlm"
	defaultControlSourceNanoPTT = "nanoptt"
	controlSourceBS22           = "bs22"
	controlSourceBlueALSAXEvent = "bluealsa_xevent"
	controlSourceROIP           = "roip"
	controlSourceWeb            = "web"
)

// normalizeControlSource maps raw config strings to canonical control source names.
// Unrecognized values (including empty string) default to "openvlm".
func normalizeControlSource(src string) string {
	switch strings.ToLower(strings.TrimSpace(src)) {
	case controlSourceBS22:
		return controlSourceBS22
	case controlSourceBlueALSAXEvent:
		return controlSourceBlueALSAXEvent
	case defaultControlSourceNanoPTT:
		return defaultControlSourceNanoPTT
	case controlSourceROIP:
		return controlSourceROIP
	case controlSourceWeb:
		return controlSourceWeb
	default:
		return defaultControlSourceOpenVLM
	}
}

// findCommDevice searches for a Linux input device whose name matches
// cfg.NanoPTTDeviceName within the glob pattern cfg.NanoPTTDevicePath.
func (cfg *CommsConfig) findCommDevice() *evdev.InputDevice {
	return device.FindEvdev(cfg.NanoPTTDevicePath, cfg.NanoPTTDeviceName)
}
