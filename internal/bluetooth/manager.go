package bluetooth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/godbus/dbus/v5"
)

// Hardware is the bounded BlueZ transport used by the lifecycle owner.
type Hardware interface {
	Snapshot(context.Context) (Inventory, error)
	SetPower(context.Context, dbus.ObjectPath, bool) error
	Discover(context.Context, dbus.ObjectPath, time.Duration) error
	Pair(context.Context, dbus.ObjectPath) error
	Trust(context.Context, dbus.ObjectPath) error
	Disconnect(context.Context, dbus.ObjectPath) error
	Forget(context.Context, dbus.ObjectPath, dbus.ObjectPath) error
}

// Runtime is the comms adapter controlled by this lifecycle.
type Runtime interface {
	StartBluetooth(context.Context, Accessory, *Lease) error
	StopBluetooth() error
	TestBluetoothTone(context.Context) error
}

// Manager serializes changes to controller ownership. A long scan or pairing
// runs outside the gate so Off can cancel it and join its cleanup before power
// is removed. The gate has one slot; it bounds concurrent hardware mutations.
// Session observations are separately synchronized and never require the gate.
type Manager struct {
	hardware Hardware
	runtime  Runtime
	store    SettingsStore
	session  *Session
	gate     chan struct{}
	// The gate protects the fields below; no mutex is held during hardware I/O.
	adapter        dbus.ObjectPath
	adapterAddress string
	selected       Accessory
	settings       Settings
	runtimeCancel  context.CancelFunc
	cancel         context.CancelFunc
	done           chan struct{}
}

func NewManager(hardware Hardware, runtime Runtime, store SettingsStore) *Manager {
	return &Manager{hardware: hardware, runtime: runtime, store: store, session: NewSession(false, false), gate: make(chan struct{}, 1)}
}

func (m *Manager) State() State { return m.session.Snapshot() }

func (m *Manager) acquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case m.gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (m *Manager) release() { <-m.gate }

// stopOperation must hold the gate. The operation's completion path never
// acquires the gate, so cancellation can wait for actual BlueZ cleanup.
func (m *Manager) stopOperation() {
	if m.cancel != nil {
		m.cancel()
		<-m.done
		m.cancel, m.done = nil, nil
	}
}

// Off remembers the user's choice to disable Bluetooth across reboots.
func (m *Manager) Off(ctx context.Context) error {
	return m.powerOff(ctx, true)
}

// Shutdown stops radio activity without changing the saved user preference.
// It is used for process shutdown and startup reconciliation.
func (m *Manager) Shutdown(ctx context.Context) error {
	return m.powerOff(ctx, false)
}

// A fresh cleanup context makes a disconnected HTTP client unable to abort off.
func (m *Manager) powerOff(ctx context.Context, remember bool) error {
	if err := m.acquire(ctx); err != nil {
		return err
	}
	defer m.release()
	m.session.Off()
	m.stopOperation()
	var settingsErr error
	if remember {
		settings := m.settings
		settings.AutoEnable = false
		settingsErr = m.saveSettings(settings)
	}
	runtimeErr := errors.Join(settingsErr, m.stopRuntime())
	m.adapter = ""
	cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	inventory, err := m.hardware.Snapshot(cleanup)
	if err != nil {
		return errors.Join(runtimeErr, err)
	}
	m.session.HardwareChanged(len(inventory.Controllers) != 0)
	result := runtimeErr
	for _, controller := range inventory.Controllers {
		if err := m.hardware.SetPower(cleanup, controller.Path, false); err != nil {
			result = errors.Join(result, fmt.Errorf("power off %s: %w", controller.Path, err))
		}
	}
	return result
}

// Begin is explicit user consent. Inventory is read without discovery and the
// requested controller must exist; arbitrary D-Bus object paths are rejected.
func (m *Manager) Begin(ctx context.Context, adapter dbus.ObjectPath) error {
	if err := m.acquire(ctx); err != nil {
		return err
	}
	defer m.release()
	inventory, err := m.hardware.Snapshot(ctx)
	if err != nil {
		return err
	}
	found := false
	for _, controller := range inventory.Controllers {
		if controller.Path == adapter {
			found = true
			m.adapterAddress = controller.Address
		}
	}
	if !found {
		return ErrUnavailable
	}
	m.session.Off()
	m.stopOperation()
	if err := m.stopRuntime(); err != nil {
		return err
	}
	// Reconcile all controllers before starting a new session, and attempt
	// every shutdown even if one controller fails. This also clears stale
	// connections from a previous setup on the same controller.
	var shutdownErr error
	for _, controller := range inventory.Controllers {
		shutdownErr = errors.Join(shutdownErr, m.hardware.SetPower(ctx, controller.Path, false))
	}
	if shutdownErr != nil {
		return shutdownErr
	}
	m.session.HardwareChanged(true)
	if _, err := m.session.Begin(); err != nil {
		return err
	}
	if err := m.hardware.SetPower(ctx, adapter, true); err != nil {
		m.session.Off()
		// A failed reply may still have powered the controller. Undo using a live context.
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return errors.Join(err, m.hardware.SetPower(cleanup, adapter, false))
	}
	m.adapter = adapter
	return nil
}

func (m *Manager) operation(ctx context.Context, fn func(context.Context, dbus.ObjectPath) error) error {
	if err := m.acquire(ctx); err != nil {
		return err
	}
	if !m.session.Snapshot().ConnectAllowed || m.adapter == "" {
		m.release()
		return ErrDisabled
	}
	m.stopOperation()
	work, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	m.cancel, m.done = cancel, done
	adapter := m.adapter
	m.release()
	defer close(done)
	defer cancel()
	return fn(work, adapter)
}

func (m *Manager) Discover(ctx context.Context, duration time.Duration) error {
	return m.operation(ctx, func(ctx context.Context, adapter dbus.ObjectPath) error {
		return m.hardware.Discover(ctx, adapter, duration)
	})
}

func (m *Manager) Pair(ctx context.Context, device dbus.ObjectPath) error {
	return m.operation(ctx, func(ctx context.Context, adapter dbus.ObjectPath) error {
		inventory, err := m.hardware.Snapshot(ctx)
		if err != nil {
			return err
		}
		for _, accessory := range inventory.Accessories {
			if accessory.Path == device && accessory.Adapter == adapter && accessory.Supported {
				if accessory.Bonded {
					return nil
				}
				return m.hardware.Pair(ctx, device)
			}
		}
		return errors.New("select a supported accessory on the active controller")
	})
}

// stopRuntime must hold the gate. Cancellation and joining finish before any
// controller power-down or pairing removal.
func (m *Manager) stopRuntime() error {
	if m.runtimeCancel == nil {
		return nil
	}
	m.runtimeCancel()
	m.runtimeCancel = nil
	return m.runtime.StopBluetooth()
}

func (m *Manager) Connect(ctx context.Context, device dbus.ObjectPath) error {
	if err := m.acquire(ctx); err != nil {
		return err
	}
	if m.runtime == nil || !m.session.Snapshot().Enabled {
		m.release()
		return ErrDisabled
	}
	generation, err := m.session.Begin()
	if err == nil {
		err = m.stopRuntime()
	}
	m.release()
	if err != nil {
		return err
	}
	if err := m.Pair(ctx, device); err != nil {
		return err
	}
	if err := m.acquire(ctx); err != nil {
		return err
	}
	defer m.release()
	state := m.session.Snapshot()
	if state.Generation != generation || !state.ConnectAllowed {
		return ErrDisabled
	}
	inventory, err := m.hardware.Snapshot(ctx)
	if err != nil {
		return err
	}
	for _, accessory := range inventory.Accessories {
		if accessory.Path != device || accessory.Adapter != m.adapter || !accessory.Supported || !accessory.Bonded {
			continue
		}
		// Trust only the explicitly selected accessory after a persistent bond
		// is verified. Radio power and reconnection still require session consent.
		if err := m.hardware.Trust(ctx, accessory.Path); err != nil {
			return fmt.Errorf("trust selected Bluetooth accessory: %w", err)
		}
		settings := m.settings
		if settings.DeviceAddress != accessory.Address {
			settings.AutoEnable = false
		}
		settings.DeviceAddress, settings.DeviceName = accessory.Address, accessory.Name
		for _, controller := range inventory.Controllers {
			if controller.Path == accessory.Adapter {
				settings.AdapterAddress = controller.Address
			}
		}
		if err := m.saveSettings(settings); err != nil {
			return err
		}
		runCtx, cancel := context.WithCancel(context.Background())
		if err := m.runtime.StartBluetooth(runCtx, accessory, m.session.Lease()); err != nil {
			cancel()
			return err
		}
		m.runtimeCancel = cancel
		m.selected = accessory
		return nil
	}
	return errors.New("paired accessory is no longer available")
}

func (m *Manager) Activate(ctx context.Context) error {
	if err := m.acquire(ctx); err != nil {
		return err
	}
	defer m.release()
	state := m.session.Snapshot()
	if !state.Enabled || !state.ConnectAllowed {
		return ErrDisabled
	}
	if !state.Ready {
		return ErrNotReady
	}
	previous := m.settings
	settings := previous
	settings.AutoEnable = true
	if err := m.saveSettings(settings); err != nil {
		return err
	}
	if err := m.session.Activate(); err != nil {
		return errors.Join(err, m.saveSettings(previous))
	}
	return nil
}

func (m *Manager) Disconnect(ctx context.Context) error {
	if err := m.acquire(ctx); err != nil {
		return err
	}
	defer m.release()
	m.session.Disconnect()
	m.stopOperation()
	result := m.stopRuntime()
	if m.selected.Path != "" {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		result = errors.Join(result, m.hardware.Disconnect(cleanup, m.selected.Path))
	}
	return result
}

func (m *Manager) Forget(ctx context.Context) error {
	if err := m.acquire(ctx); err != nil {
		return err
	}
	defer m.release()
	m.session.Disconnect()
	m.stopOperation()
	result := m.stopRuntime()
	if m.selected.Path == "" {
		return errors.Join(result, m.saveSettings(Settings{}))
	}
	cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := m.hardware.Forget(cleanup, m.selected.Adapter, m.selected.Path); err != nil {
		return errors.Join(result, err)
	}
	m.selected = Accessory{}
	return errors.Join(result, m.saveSettings(Settings{}))
}

func (m *Manager) TestTone(ctx context.Context) error {
	if err := m.acquire(ctx); err != nil {
		return err
	}
	defer m.release()
	state := m.session.Snapshot()
	if m.runtimeCancel == nil || !state.AudioReady || !state.HFPReady {
		return ErrNotReady
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return m.runtime.TestBluetoothTone(ctx)
}
