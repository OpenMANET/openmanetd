package comms

import (
	"context"
	"errors"

	"github.com/openmanet/openmanetd/internal/bluetooth"
)

// StartBluetooth temporarily replaces the local audio source during setup.
// Persisted comms settings are untouched; cancelling setup restores a previously
// running non-Bluetooth runtime. Lease keeps setup TX disabled until activation.
func (m *CommsManager) StartBluetooth(ctx context.Context, accessory bluetooth.Accessory, lease *bluetooth.Lease) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cc := m.buildFn()
	cc.ControlSource = controlSourceBS22
	cc.BluetoothDevicePath = string(accessory.Path)
	cc.BluetoothSession = lease
	tone := make(chan chan error, 1) // At most one pending user test tone.
	cc.testToneC = tone
	cc.AudioMixerStartup = nil // USB OpenVLM mixer settings do not apply to HFP.
	if !cc.bluetoothConnectionAllowed() {
		return ErrBluetoothDisabled
	}
	if _, err := cc.selectedBluetoothAudioSpec(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !m.bluetoothRunning && m.running {
		previous := m.buildFn()
		if !previous.usesBluetooth() {
			m.bluetoothRestore = previous
		}
	}
	m.disableLocked()
	if err := m.enableLocked(ctx, cc); err != nil {
		m.bluetoothRunning = false
		previous := m.bluetoothRestore
		m.bluetoothRestore = nil
		if previous != nil {
			return errors.Join(err, m.enableLocked(context.Background(), previous))
		}
		return err
	}
	m.bluetoothRunning = true
	m.bluetoothTone = tone
	return nil
}

// StopBluetooth joins the Bluetooth runtime before restoring the prior source.
// The lifecycle owner revokes its lease before calling this method.
func (m *CommsManager) StopBluetooth() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.bluetoothRunning {
		return nil
	}
	m.disableLocked()
	m.bluetoothRunning = false
	previous := m.bluetoothRestore
	m.bluetoothRestore = nil
	if previous != nil {
		return m.enableLocked(context.Background(), previous)
	}
	return nil
}

func (m *CommsManager) TestBluetoothTone(ctx context.Context) error {
	m.mu.Lock()
	tone, running := m.bluetoothTone, m.bluetoothRunning
	m.mu.Unlock()
	if !running || tone == nil {
		return ErrNotRunning
	}
	reply := make(chan error, 1)
	select {
	case tone <- reply:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-reply:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
