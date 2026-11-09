package audio

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBluetoothVolumeSurvivesMutedRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "volume.json")
	volume, err := rememberBluetoothVolume(path, "AA:BB", 9<<8)
	require.NoError(t, err)
	require.Equal(t, uint16(9<<8), volume)
	// Simulate losing BlueALSA before shutdown can restore the playback gain.
	volume, err = rememberBluetoothVolume(path, "aa:bb", 0)
	require.NoError(t, err)
	require.Equal(t, uint16(9<<8), volume)
	// A nonzero change from the accessory is retained for subsequent recovery.
	volume, err = rememberBluetoothVolume(path, "AA:BB", 5<<8)
	require.NoError(t, err)
	require.Equal(t, uint16(5<<8), volume)
	volume, err = rememberBluetoothVolume(path, "AA:BB", 0)
	require.NoError(t, err)
	require.Equal(t, uint16(5<<8), volume)
	// A new, muted accessory must not inherit the previous accessory's gain.
	volume, err = rememberBluetoothVolume(path, "CC:DD", 0)
	require.NoError(t, err)
	require.Zero(t, volume)
}

func TestBluetoothVolumeCannotSaveDoesNotProceed(t *testing.T) {
	_, err := rememberBluetoothVolume(filepath.Join(t.TempDir(), "missing", "volume.json"), "AA:BB", 15<<8)
	require.Error(t, err)
	path := filepath.Join(t.TempDir(), "volume.json")
	require.NoError(t, os.WriteFile(path, []byte("broken"), 0600))
	_, err = rememberBluetoothVolume(path, "AA:BB", 0)
	require.Error(t, err)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "broken", string(data))
}

func TestExplicitBluetoothVolumeZeroSurvivesRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "volume.json")
	_, err := rememberBluetoothVolume(path, "AA:BB", 15<<8)
	require.NoError(t, err)
	_, err = writeBluetoothVolume(path, "AA:BB", 0, false)
	require.NoError(t, err)
	got, err := rememberBluetoothVolume(path, "AA:BB", 0)
	require.NoError(t, err)
	require.Zero(t, got)
}

func TestPreferredSpeakerGain(t *testing.T) {
	require.Equal(t, uint16(7<<8), preferredSpeakerGain(15<<8, 7<<8, true))
	require.Zero(t, preferredSpeakerGain(7<<8, 0, true))
	require.Equal(t, uint16(8<<8), preferredSpeakerGain(7<<8, 2<<8, false))
	require.Equal(t, uint16(15<<8), preferredSpeakerGain(15<<8, 2<<8, false))
}

func TestPreferredSpeakerGainIdleDownAndLimits(t *testing.T) {
	require.Equal(t, uint16(6<<8), preferredSpeakerGain(7<<8, 0, false))
	require.Zero(t, preferredSpeakerGain(0, 0, false))
	require.Equal(t, uint16(7<<8), preferredSpeakerGain(7<<8, 1<<8, false))
	require.Equal(t, uint16(1<<8), preferredSpeakerGain(0, 2<<8, false))
}

func TestBluetoothVolumeSurvivesQuietIdleRecovery(t *testing.T) {
	for _, preferred := range []uint16{0, 1 << 8, 13 << 8} {
		path := filepath.Join(t.TempDir(), "volume.json")
		_, err := writeBluetoothVolume(path, "AA:BB", preferred, false)
		require.NoError(t, err)
		got, err := rememberBluetoothVolume(path, "AA:BB", speakerIdleGain)
		require.NoError(t, err)
		require.Equal(t, preferred, got)
		// An intentional level-one setting remains distinguishable from recovery.
		got, err = writeBluetoothVolume(path, "AA:BB", speakerIdleGain, false)
		require.NoError(t, err)
		require.Equal(t, speakerIdleGain, got)
		got, err = rememberBluetoothVolume(path, "CC:DD", speakerIdleGain)
		require.NoError(t, err)
		require.Equal(t, speakerIdleGain, got, "different accessories never inherit saved gain")
	}
}
