package device

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gen2brain/malgo"
	"github.com/rs/zerolog"
)

const (
	bs22SCOAlias           = "bt_sco"
	bluealsaSCODefaultSpec = "bluealsa:PROFILE=sco"
)

// AudioDeviceInfo is a backend-neutral descriptor returned by ResolveAudio.
// It carries only the fields the audio package consumes so callers do not
// have to import malgo directly — the device package is the sole importer
// of the underlying binding.
//
// ID is opaque from the caller's perspective: callers pass it back into
// the capture/playback openers which in turn hand it to malgo. Name is
// the human-readable device name used in logs and config strings.
//
// DefaultHighInputLatency / DefaultHighOutputLatency are reported as zero
// because malgo does not expose a documented "high latency" hint the way
// PortAudio did. Callers that need a latency hint should compute one from
// the configured period size and sample rate.
type AudioDeviceInfo struct {
	Name                     string
	ID                       malgo.DeviceID
	MaxChannels              int
	DefaultHighInputLatency  time.Duration
	DefaultHighOutputLatency time.Duration
	IsInput                  bool
}

// ResolveAudio maps a device spec string to an AudioDeviceInfo.
//
//   - ""           → first device whose IsDefault flag is set, else index 0
//   - numeric      → index into the device list returned by ctx.Devices()
//   - other string → exact name match, then case-insensitive substring match
//
// The three-tier match semantics are preserved verbatim from the previous
// PortAudio-backed implementation so existing config strings keep working.
func ResolveAudio(ctx *malgo.AllocatedContext, spec string, wantInput bool) (AudioDeviceInfo, error) {
	if ctx == nil {
		return AudioDeviceInfo{}, errors.New("device.ResolveAudio: nil malgo context")
	}

	spec = strings.TrimSpace(spec)
	if info, ok, err := resolveDirectBlueALSA(spec, wantInput); ok {
		return info, err
	}

	kind := malgo.Playback
	if wantInput {
		kind = malgo.Capture
	}

	devs, err := ctx.Devices(kind)
	if err != nil {
		return AudioDeviceInfo{}, fmt.Errorf("malgo list devices: %w", err)
	}

	if len(devs) == 0 {
		return AudioDeviceInfo{}, fmt.Errorf("no %s devices found", kindName(wantInput))
	}

	if spec == "" {
		for i := range devs {
			if devs[i].IsDefault != 0 {
				return newAudioDeviceInfo(devs[i], wantInput), nil
			}
		}

		return newAudioDeviceInfo(devs[0], wantInput), nil
	}

	if idx, cErr := strconv.Atoi(spec); cErr == nil {
		if idx < 0 || idx >= len(devs) {
			return AudioDeviceInfo{}, fmt.Errorf("audio device index %d out of range (0..%d)", idx, len(devs)-1)
		}

		return newAudioDeviceInfo(devs[idx], wantInput), nil
	}

	if strings.EqualFold(spec, bs22SCOAlias) {
		for i := range devs {
			if isLikelySCODeviceName(devs[i].Name()) {
				return newAudioDeviceInfo(devs[i], wantInput), nil
			}
		}

		return newRawALSAAudioDeviceInfo(bluealsaSCODefaultSpec, wantInput)
	}

	for i := range devs {
		if devs[i].Name() == spec {
			return newAudioDeviceInfo(devs[i], wantInput), nil
		}
	}

	lowerSpec := strings.ToLower(spec)
	for i := range devs {
		if strings.Contains(strings.ToLower(devs[i].Name()), lowerSpec) {
			return newAudioDeviceInfo(devs[i], wantInput), nil
		}
	}

	return AudioDeviceInfo{}, fmt.Errorf("audio device %q not found for %s (available: %s)", spec, kindName(wantInput), listDeviceNames(devs))
}

func resolveDirectBlueALSA(spec string, wantInput bool) (AudioDeviceInfo, bool, error) {
	if spec == "" || !strings.HasPrefix(strings.ToLower(spec), "bluealsa") {
		return AudioDeviceInfo{}, false, nil
	}

	info, err := newRawALSAAudioDeviceInfo(spec, wantInput)
	return info, true, err
}

func kindName(wantInput bool) string {
	if wantInput {
		return "capture"
	}

	return "playback"
}

func newAudioDeviceInfo(d malgo.DeviceInfo, wantInput bool) AudioDeviceInfo {
	maxChannels := 0

	for _, f := range d.Formats {
		if int(f.Channels) > maxChannels {
			maxChannels = int(f.Channels)
		}
	}

	return AudioDeviceInfo{
		Name:        d.Name(),
		ID:          d.ID,
		MaxChannels: maxChannels,
		IsInput:     wantInput,
	}
}

func newRawALSAAudioDeviceInfo(spec string, wantInput bool) (AudioDeviceInfo, error) {
	id, err := deviceIDFromALSAName(spec)
	if err != nil {
		return AudioDeviceInfo{}, err
	}

	return AudioDeviceInfo{Name: spec, ID: id, MaxChannels: 1, IsInput: wantInput}, nil
}

func deviceIDFromALSAName(name string) (malgo.DeviceID, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return malgo.DeviceID{}, errors.New("empty ALSA device name")
	}
	if len(name) >= 256 {
		return malgo.DeviceID{}, fmt.Errorf("ALSA device name too long: %d", len(name))
	}

	var id malgo.DeviceID
	copy(id[:], name)
	id[len(name)] = 0
	return id, nil
}

func isLikelySCODeviceName(name string) bool {
	n := strings.ToLower(name)
	return strings.Contains(n, "sco") || strings.Contains(n, "hfp") || strings.Contains(n, "hsp") || strings.Contains(n, "handsfree")
}

func listDeviceNames(devs []malgo.DeviceInfo) string {
	if len(devs) == 0 {
		return "none"
	}

	names := make([]string, 0, len(devs))
	for i := range devs {
		name := strings.TrimSpace(devs[i].Name())
		if name == "" {
			name = fmt.Sprintf("<unnamed-%d>", i)
		}
		names = append(names, name)
	}
	return strings.Join(names, ", ")
}

// LogAudioDevices logs every capture and playback device at Debug level.
// Used at startup when Comms.Debug is true so an operator can see the full
// device list the active malgo context enumerated, matching the prior
// PortAudio diagnostic behavior.
func LogAudioDevices(ctx *malgo.AllocatedContext, log zerolog.Logger) {
	if ctx == nil {
		return
	}

	for _, kind := range []malgo.DeviceType{malgo.Capture, malgo.Playback} {
		devs, err := ctx.Devices(kind)
		if err != nil {
			log.Debug().Err(err).Msgf("Could not list %s devices", kindLabel(kind))

			continue
		}

		log.Debug().Msgf("Available %s devices (%d):", kindLabel(kind), len(devs))

		for i := range devs {
			log.Debug().Msgf("  [%d] %s (default=%t)", i, devs[i].Name(), devs[i].IsDefault != 0)
		}
	}
}

func kindLabel(kind malgo.DeviceType) string {
	if kind == malgo.Capture {
		return "capture"
	}

	return "playback"
}
