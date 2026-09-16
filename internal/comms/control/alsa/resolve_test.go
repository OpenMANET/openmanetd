package alsa_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openmanet/openmanetd/internal/comms/control/alsa"
)

func TestCardFromEnv(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		unset   bool
		want    uint
		wantErr bool
	}{
		{name: "valid zero", value: "0", want: 0},
		{name: "valid positive", value: "3", want: 3},
		{name: "unset", unset: true, wantErr: true},
		{name: "empty", value: "", wantErr: true},
		{name: "garbage", value: "hw:1", wantErr: true},
		{name: "negative", value: "-1", wantErr: true},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if tc.unset {
				prev, had := os.LookupEnv("ALSA_CARD")

				t.Cleanup(func() {
					if had {
						_ = os.Setenv("ALSA_CARD", prev)
					}
				})
				require.NoError(t, os.Unsetenv("ALSA_CARD"))
			} else {
				withCard(t, tc.value)
			}

			card, err := alsa.CardFromEnv()
			if tc.wantErr {
				require.Error(t, err)
				assert.ErrorIs(t, err, alsa.ErrNoCard)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, card)
		})
	}
}

func TestResolveCtl_FirstMatchWins(t *testing.T) {
	first := newFakeCtl([]int{1}, 0, 10)
	second := newFakeCtl([]int{2}, 0, 10)
	mx := &fakeMixer{ctls: map[string]alsa.Ctl{
		"Master":                  first,
		"Speaker Playback Volume": second,
	}}

	ctl, name, err := alsa.ResolveCtl(mx, alsa.PlaybackVolumeNames)
	require.NoError(t, err)
	assert.Equal(t, "Master", name, "Master is first in the candidate list")
	assert.Same(t, first, ctl.(*fakeCtl))
}

func TestResolveCtl_FallsThroughCandidates(t *testing.T) {
	only := newFakeCtl([]int{5}, 0, 10)
	mx := &fakeMixer{ctls: map[string]alsa.Ctl{"Speaker Playback Volume": only}}

	ctl, name, err := alsa.ResolveCtl(mx, alsa.PlaybackVolumeNames)
	require.NoError(t, err)
	assert.Equal(t, "Speaker Playback Volume", name)
	assert.Same(t, only, ctl.(*fakeCtl))
}

func TestResolveCtl_NotFound(t *testing.T) {
	mx := &fakeMixer{ctls: map[string]alsa.Ctl{}}

	_, _, err := alsa.ResolveCtl(mx, alsa.CaptureVolumeNames)
	require.Error(t, err)
	assert.ErrorIs(t, err, alsa.ErrControlNotFound)
}
