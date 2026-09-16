package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newAudioTestConfig(t *testing.T, yamlContent string) *Config {
	t.Helper()

	cfgPath := filepath.Join(t.TempDir(), "config.yml")
	require.NoError(t, os.WriteFile(cfgPath, []byte(yamlContent), 0644))

	v := viper.New()
	v.SetConfigFile(cfgPath)
	require.NoError(t, v.ReadInConfig())

	return NewWithoutWatch(v)
}

func TestCommsAudio_Defaults_Unset(t *testing.T) {
	c := newAudioTestConfig(t, "comms:\n  enable: false\n")

	assert.Equal(t, DefaultCommsAudioSpeakerVolume, c.GetCommsAudioSpeakerVolume(),
		"unset speaker volume defaults to 100% so every EEPROM vintage boots at full DAC level")
	assert.Equal(t, DefaultCommsAudioMicVolume, c.GetCommsAudioMicVolume(),
		"unset mic volume defaults to 100% so capture level is deterministic at startup")

	_, set := c.GetCommsAudioAGC()
	assert.False(t, set)
	assert.Empty(t, c.GetCommsAudioSpeakerControl())
}

func TestCommsAudio_LoadAndClamp(t *testing.T) {
	c := newAudioTestConfig(t, `comms:
  audio:
    speakerVolume: 150
    micVolume: -5
    agc: true
    speakerControl: "My Speaker"
`)

	assert.Equal(t, 100, c.GetCommsAudioSpeakerVolume(), "silent clamp above 100")
	assert.Equal(t, 0, c.GetCommsAudioMicVolume(), "silent clamp below 0")

	enabled, set := c.GetCommsAudioAGC()
	assert.True(t, set)
	assert.True(t, enabled)
	assert.Equal(t, "My Speaker", c.GetCommsAudioSpeakerControl())
}

func TestCommsAudio_BoundaryValues(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want int
	}{
		{name: "zero", yaml: "comms:\n  audio:\n    speakerVolume: 0\n", want: 0},
		{name: "hundred", yaml: "comms:\n  audio:\n    speakerVolume: 100\n", want: 100},
		{name: "one", yaml: "comms:\n  audio:\n    speakerVolume: 1\n", want: 1},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			c := newAudioTestConfig(t, tc.yaml)
			assert.Equal(t, tc.want, c.GetCommsAudioSpeakerVolume())
		})
	}
}

func TestPersistCommsAudio_RoundTrip(t *testing.T) {
	c := newAudioTestConfig(t, `# top comment survives
comms:
  enable: true
`)

	speaker, mic := 45, 60
	agc := true
	require.NoError(t, c.PersistCommsAudio(&speaker, &mic, &agc))

	// In-memory state refreshed immediately.
	assert.Equal(t, 45, c.GetCommsAudioSpeakerVolume())
	assert.Equal(t, 60, c.GetCommsAudioMicVolume())

	enabled, set := c.GetCommsAudioAGC()
	assert.True(t, set)
	assert.True(t, enabled)

	// File contents: keys written, comment preserved.
	data, err := os.ReadFile(c.GetConfigFilePath())
	require.NoError(t, err)

	content := string(data)
	assert.Contains(t, content, "# top comment survives")
	assert.Contains(t, content, "speakerVolume: 45")
	assert.Contains(t, content, "micVolume: 60")
	assert.Contains(t, content, "agc: true")
}

func TestPersistCommsAudio_PartialWritesOnlyProvidedFields(t *testing.T) {
	c := newAudioTestConfig(t, "comms:\n  enable: true\n")

	agc := false
	require.NoError(t, c.PersistCommsAudio(nil, nil, &agc))

	data, err := os.ReadFile(c.GetConfigFilePath())
	require.NoError(t, err)

	content := string(data)
	assert.Contains(t, content, "agc: false")
	assert.NotContains(t, content, "speakerVolume")
	assert.NotContains(t, content, "micVolume")

	assert.Equal(t, DefaultCommsAudioSpeakerVolume, c.GetCommsAudioSpeakerVolume(),
		"unwritten speaker volume falls back to the 100% default")
}
