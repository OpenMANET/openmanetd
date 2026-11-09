package audio

import (
	"testing"

	"github.com/gen2brain/malgo"
	"github.com/stretchr/testify/require"
)

func TestBluetoothTimingUsesDurationForBothDirections(t *testing.T) {
	for _, direction := range []malgo.DeviceType{malgo.Capture, malgo.Playback} {
		cfg := malgo.DefaultDeviceConfig(direction)
		cfg.SampleRate = 48000
		cfg.PeriodSizeInFrames = 960
		applyBluetoothTiming(&cfg, "bluealsa:DEV=41:42:86:99:1D:61,PROFILE=sco")
		require.Zero(t, cfg.PeriodSizeInFrames, "frame count overrides duration at the negotiated rate")
		require.Equal(t, uint32(20), cfg.PeriodSizeInMilliseconds)
		require.Equal(t, uint32(48000), cfg.SampleRate, "Opus application rate must remain unchanged")
	}
}

func TestBluetoothTimingPreservesOtherAudioDevices(t *testing.T) {
	cfg := malgo.DefaultDeviceConfig(malgo.Playback)
	cfg.PeriodSizeInFrames = 2880
	before := cfg
	applyBluetoothTiming(&cfg, "plughw:CARD=Device,DEV=0")
	require.Equal(t, before, cfg)
}
