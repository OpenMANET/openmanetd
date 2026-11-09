package bluetooth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Settings stores identity and explicit boot consent, never a transient power
// state. Adapter addresses survive hci index changes across reboots.
type Settings struct {
	AutoEnable     bool   `json:"auto_enable"`
	AdapterAddress string `json:"adapter_address"`
	DeviceAddress  string `json:"device_address"`
	DeviceName     string `json:"device_name"`
}

type SettingsStore interface {
	Load() (Settings, error)
	Save(Settings) error
}

// FileStore is serialized by Manager. Atomic replacement preserves the previous
// complete settings if writing fails; the directory belongs to openmanetd.
type FileStore struct{ Path string }

func (f FileStore) Load() (Settings, error) {
	data, err := os.ReadFile(f.Path)
	if errors.Is(err, os.ErrNotExist) {
		return Settings{}, nil
	}
	if err != nil {
		return Settings{}, err
	}
	var settings Settings
	if err := json.Unmarshal(data, &settings); err != nil {
		return Settings{}, fmt.Errorf("read Bluetooth settings: %w", err)
	}
	return settings, nil
}
func (f FileStore) Save(settings Settings) error {
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(f.Path), ".bluetooth-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(data); err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err = errors.Join(err, closeErr); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), f.Path)
}
