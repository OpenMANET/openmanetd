package audio

import (
	"strings"

	"github.com/gen2brain/malgo"
)

// Bluetooth SCO negotiates 8 or 16 kHz independently of the 48 kHz application
// stream. Frame-count periods are interpreted at that negotiated rate by ALSA:
// 960 frames become 120 ms at 8 kHz, bursting six Opus frames per callback.
// Duration-based periods keep capture and jitter-buffer playout at 20 ms.
func applyBluetoothTiming(cfg *malgo.DeviceConfig, deviceName string) {
	if !strings.HasPrefix(strings.ToLower(deviceName), "bluealsa") {
		return
	}
	cfg.PeriodSizeInFrames = 0
	cfg.PeriodSizeInMilliseconds = 20
}
