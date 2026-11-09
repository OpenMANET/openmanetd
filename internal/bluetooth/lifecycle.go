package bluetooth

import (
	"context"
	"errors"
	"strings"
)

type Status struct {
	State     State
	Inventory Inventory
	Selected  Accessory
	Settings  Settings
}

func (m *Manager) Status(ctx context.Context) (Status, error) {
	if err := m.acquire(ctx); err != nil {
		return Status{}, err
	}
	defer m.release()
	inventory, err := m.hardware.Snapshot(ctx)
	return Status{State: m.session.Snapshot(), Inventory: inventory, Selected: m.selected, Settings: m.settings}, err
}

// Initialize always starts with power off. Only a matching saved bond plus an
// explicit opt-in permits a boot connection. Hotplug never consumes boot consent.
func (m *Manager) Initialize(ctx context.Context) error {
	if err := m.Shutdown(ctx); err != nil {
		return err
	}
	if err := m.acquire(ctx); err != nil {
		return err
	}
	settings := Settings{}
	var err error
	if m.store != nil {
		settings, err = m.store.Load()
	}
	if err != nil {
		m.release()
		return err
	}
	m.settings = settings
	m.session.SetAutoEnable(settings.AutoEnable)
	inventory, err := m.hardware.Snapshot(ctx)
	if err != nil {
		m.release()
		return err
	}
	var selected Accessory
	for _, controller := range inventory.Controllers {
		if !strings.EqualFold(controller.Address, settings.AdapterAddress) {
			continue
		}
		for _, accessory := range inventory.Accessories {
			if accessory.Adapter == controller.Path && strings.EqualFold(accessory.Address, settings.DeviceAddress) && accessory.Bonded && accessory.Supported {
				selected = accessory
			}
		}
	}
	m.selected = selected
	m.release()
	if !settings.AutoEnable || selected.Path == "" {
		return nil
	}
	if err := m.Begin(ctx, selected.Adapter); err != nil {
		return errors.Join(err, m.Shutdown(context.Background()))
	}
	if err := m.Connect(ctx, selected.Path); err != nil {
		return errors.Join(err, m.Shutdown(context.Background()))
	}
	if err := m.acquire(ctx); err != nil {
		return err
	}
	defer m.release()
	// Active means consent; transmission still requires all readiness signals.
	m.session.authorizeBoot()
	return nil
}

func (s *Session) authorizeBoot() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.AutoEnable && s.state.Enabled && s.state.ConnectAllowed {
		s.state.Active = true
	}
}

func (m *Manager) saveSettings(settings Settings) error {
	if m.store != nil {
		if err := m.store.Save(settings); err != nil {
			return err
		}
	}
	m.settings = settings
	m.session.SetAutoEnable(settings.AutoEnable)
	return nil
}

func (m *Manager) SetAutoEnable(ctx context.Context, enabled bool) error {
	if err := m.acquire(ctx); err != nil {
		return err
	}
	defer m.release()
	if enabled && m.settings.DeviceAddress == "" {
		return errors.New("select a paired accessory before enabling automatic startup")
	}
	settings := m.settings
	settings.AutoEnable = enabled
	return m.saveSettings(settings)
}

// Reconcile reads inventory without discovery. Unselected/new controllers are
// kept off, and losing the selected adapter revokes its runtime immediately.
func (m *Manager) Reconcile(ctx context.Context) error {
	if err := m.acquire(ctx); err != nil {
		return err
	}
	defer m.release()
	inventory, err := m.hardware.Snapshot(ctx)
	if err != nil {
		m.session.Off()
		m.stopOperation()
		return errors.Join(err, m.stopRuntime())
	}
	selectedPresent := false
	for _, controller := range inventory.Controllers {
		if controller.Path == m.adapter && strings.EqualFold(controller.Address, m.adapterAddress) {
			selectedPresent = true
		}
	}
	state := m.session.Snapshot()
	var result error
	if state.Enabled && !selectedPresent {
		m.session.Off()
		m.stopOperation()
		result = m.stopRuntime()
		m.adapter = ""
	}
	m.session.HardwareChanged(len(inventory.Controllers) > 0)
	state = m.session.Snapshot()
	for _, controller := range inventory.Controllers {
		if controller.Powered && (!state.Enabled || controller.Path != m.adapter) {
			result = errors.Join(result, m.hardware.SetPower(ctx, controller.Path, false))
		}
	}
	return result
}
