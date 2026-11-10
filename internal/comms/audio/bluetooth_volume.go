package audio

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const bluetoothVolumePath = "/etc/openmanetd/bluetooth-speaker-volume.json"
const defaultBluetoothSpeakerGain uint16 = 8 << 8

// Serialize startup/recovery access. No disk access occurs in audio callbacks.
var bluetoothVolumeMu sync.Mutex

type bluetoothVolume struct {
	Address string `json:"address"`
	Volume  uint16 `json:"volume"`
}

// rememberBluetoothVolume records playback gain before applying our idle mute.
// BlueALSA can disappear before close restores that gain, and may persist zero
// or the quiet idle gain across restarts. Never use another accessory's saved gain to undo a mute.
func rememberBluetoothVolume(path, address string, observed uint16) (uint16, error) {
	return writeBluetoothVolume(path, address, observed, true)
}

// Explicit user levels must be saved; recovery recognizes both idle levels.
func writeBluetoothVolume(path, address string, observed uint16, recovering bool) (uint16, error) {
	bluetoothVolumeMu.Lock()
	defer bluetoothVolumeMu.Unlock()
	address = strings.ToUpper(address)
	var saved bluetoothVolume
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return 0, fmt.Errorf("read Bluetooth playback volume: %w", err)
	}
	if err == nil {
		if err := json.Unmarshal(data, &saved); err != nil {
			return 0, fmt.Errorf("decode Bluetooth playback volume: %w", err)
		}
		if saved.Address == address && ((recovering && (observed == 0 || observed == speakerIdleGain)) || observed == saved.Volume) {
			return saved.Volume, nil
		}
	}
	// A first connection may see BlueALSA's retained idle gain from a
	// previous installation. Use an audible default when no preference
	// exists for this accessory. The idle gate still keeps the speaker quiet.
	if recovering && (observed == 0 || observed == speakerIdleGain) {
		observed = defaultBluetoothSpeakerGain
	}
	data, err = json.Marshal(bluetoothVolume{Address: address, Volume: observed})
	if err != nil {
		return 0, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".bluetooth-volume-*")
	if err != nil {
		return 0, err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(data); err == nil {
		err = tmp.Sync()
	}
	if err = errors.Join(err, tmp.Close()); err != nil {
		return 0, err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return 0, err
	}
	return observed, nil
}
