package alsa_test

import (
	"context"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openmanet/openmanetd/internal/comms/control/alsa"
)

func TestMixerSnapshot_UnknownBeforeAnyOperation(t *testing.T) {
	v := &alsa.Volume{Log: zerolog.Nop()}

	var dst alsa.MixerSnapshot
	v.Snapshot(&dst)

	assert.Equal(t, -1, dst.SpeakerVolumePct)
	assert.Equal(t, -1, dst.MicVolumePct)
	assert.False(t, dst.AGCKnown)
}

func TestMixerSnapshot_ReflectsLastState(t *testing.T) {
	withCard(t, "0")

	mx, _, _, _ := fullCardMixer()
	op := &fakeOpener{mixer: mx}
	v := &alsa.Volume{Log: zerolog.Nop(), Open: op.opener()}

	_, err := v.State(context.Background())
	require.NoError(t, err)

	var dst alsa.MixerSnapshot
	v.Snapshot(&dst)

	assert.Equal(t, 50, dst.SpeakerVolumePct)
	assert.Equal(t, 57, dst.MicVolumePct)
	assert.True(t, dst.AGCKnown)
	assert.True(t, dst.AGCEnabled)
}

func TestMixerSnapshotter_RefreshZeroAlloc(t *testing.T) {
	// testing.AllocsPerRun must not be called under t.Parallel.
	v := &alsa.Volume{Log: zerolog.Nop()}
	s := &alsa.MixerSnapshotter{V: v}

	s.Refresh()

	allocs := testing.AllocsPerRun(100, func() {
		s.Refresh()
	})

	assert.Equal(t, 0.0, allocs, "Refresh must not allocate")
	assert.NotNil(t, s.Data())
}

func TestMixerSnapshotter_NilVolume(t *testing.T) {
	s := &alsa.MixerSnapshotter{}
	s.Refresh() // must not panic

	data, ok := s.Data().(*alsa.MixerSnapshot)
	require.True(t, ok)
	assert.Equal(t, -1, data.SpeakerVolumePct)
}
