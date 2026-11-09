package bluetooth_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/openmanet/openmanetd/internal/bluetooth"
	"github.com/stretchr/testify/require"
)

func TestFileSettingsDefaultAndRoundTrip(t *testing.T) {
	store := bluetooth.FileStore{Path: filepath.Join(t.TempDir(), "bluetooth.json")}
	settings, err := store.Load()
	require.NoError(t, err)
	require.False(t, settings.AutoEnable)
	settings = bluetooth.Settings{AdapterAddress: "AA:BB:CC:DD:EE:FF", DeviceAddress: "41:42:86:99:1D:61", AutoEnable: true}
	require.NoError(t, store.Save(settings))
	actual, err := store.Load()
	require.NoError(t, err)
	require.Equal(t, settings, actual)
	info, err := os.Stat(store.Path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), info.Mode().Perm())
}

func TestBootConsentRequiredEvenWithSavedBond(t *testing.T) {
	for _, auto := range []bool{false, true} {
		t.Run(map[bool]string{false: "default off", true: "explicit boot consent"}[auto], func(t *testing.T) {
			store := bluetooth.FileStore{Path: filepath.Join(t.TempDir(), "bluetooth.json")}
			require.NoError(t, store.Save(bluetooth.Settings{AutoEnable: auto, AdapterAddress: "AA:BB:CC:DD:EE:FF", DeviceAddress: "41:42:86:99:1D:61"}))
			f := &fakeHardware{inventory: bluetooth.Inventory{
				Controllers: []bluetooth.Controller{{Path: "/org/bluez/hci3", Address: "AA:BB:CC:DD:EE:FF"}},
				Accessories: []bluetooth.Accessory{{Path: "/org/bluez/hci3/dev_41_42_86_99_1D_61", Adapter: "/org/bluez/hci3", Address: "41:42:86:99:1D:61", Supported: true, Paired: true, Bonded: true}},
			}}
			runtime := &fakeRuntime{hardware: f}
			manager := bluetooth.NewManager(f, runtime, store)
			require.NoError(t, manager.Initialize(context.Background()))
			require.Equal(t, auto, manager.State().Enabled)
			require.Equal(t, auto, manager.State().Active)
			require.False(t, manager.State().Ready)
			if auto {
				require.Contains(t, f.Calls(), "runtime-start")
			} else {
				require.Equal(t, []string{"off:/org/bluez/hci3"}, f.Calls())
			}
			require.NoError(t, manager.Off(context.Background()))
		})
	}
}

func TestHotplugRemainsOff(t *testing.T) {
	f := &fakeHardware{}
	manager := bluetooth.NewManager(f, nil, nil)
	require.NoError(t, manager.Initialize(context.Background()))
	require.False(t, manager.State().Available)
	f.mu.Lock()
	f.inventory.Controllers = []bluetooth.Controller{{Path: "/org/bluez/hci1", Powered: true}}
	f.mu.Unlock()
	require.NoError(t, manager.Reconcile(context.Background()))
	require.True(t, manager.State().Available)
	require.False(t, manager.State().Enabled)
	require.Equal(t, []string{"off:/org/bluez/hci1"}, f.Calls())
}

func TestBootOptInCannotUseTemporaryPairing(t *testing.T) {
	store := bluetooth.FileStore{Path: filepath.Join(t.TempDir(), "bluetooth.json")}
	require.NoError(t, store.Save(bluetooth.Settings{AutoEnable: true, AdapterAddress: "AA:BB:CC:DD:EE:FF", DeviceAddress: "41:42:86:99:1D:61"}))
	f := &fakeHardware{inventory: bluetooth.Inventory{
		Controllers: []bluetooth.Controller{{Path: "/org/bluez/hci0", Address: "AA:BB:CC:DD:EE:FF"}},
		Accessories: []bluetooth.Accessory{{Path: "/org/bluez/hci0/dev_41_42_86_99_1D_61", Adapter: "/org/bluez/hci0", Address: "41:42:86:99:1D:61", Paired: true, Supported: true}},
	}}
	m := bluetooth.NewManager(f, &fakeRuntime{hardware: f}, store)
	require.NoError(t, m.Initialize(context.Background()))
	require.False(t, m.State().Enabled)
	require.False(t, m.State().Active)
	require.Equal(t, []string{"off:/org/bluez/hci0"}, f.Calls())
}

// A saved bond alone is not consent. Completing setup remembers consent;
// process shutdown preserves it, and the user's Off action revokes it.
func TestActivationRemembersConsentAcrossRestartUntilOff(t *testing.T) {
	ctx := context.Background()
	store := bluetooth.FileStore{Path: filepath.Join(t.TempDir(), "bluetooth.json")}
	f := &fakeHardware{inventory: bluetooth.Inventory{
		Controllers: []bluetooth.Controller{{Path: "/org/bluez/hci0", Address: "AA:BB:CC:DD:EE:FF"}},
		Accessories: []bluetooth.Accessory{{Path: "/org/bluez/hci0/dev_A", Adapter: "/org/bluez/hci0", Address: "41:42:86:99:1D:61", Supported: true, Bonded: true}},
	}}
	runtime := &fakeRuntime{hardware: f}
	m := bluetooth.NewManager(f, runtime, store)
	require.NoError(t, m.Initialize(ctx))
	require.False(t, m.State().Enabled, "a fresh installation stays off")
	require.NoError(t, m.Begin(ctx, "/org/bluez/hci0"))
	require.NoError(t, m.Connect(ctx, "/org/bluez/hci0/dev_A"))
	require.ErrorIs(t, m.Activate(ctx), bluetooth.ErrNotReady)
	settings, err := store.Load()
	require.NoError(t, err)
	require.False(t, settings.AutoEnable, "incomplete setup must not enable boot transmission")
	lease := runtime.Lease()
	lease.ObserveAudioReady(true)
	lease.ObserveButtonsReady(true)
	lease.ObserveHFPReady(true)
	require.NoError(t, m.Activate(ctx))
	require.True(t, m.State().AutoEnable)
	require.NoError(t, m.Shutdown(ctx))
	require.False(t, lease.CanTransmit())
	require.False(t, lease.ConnectionAllowed())
	settings, err = store.Load()
	require.NoError(t, err)
	require.True(t, settings.AutoEnable)

	restarted := bluetooth.NewManager(f, &fakeRuntime{hardware: f}, store)
	require.NoError(t, restarted.Initialize(ctx))
	require.True(t, restarted.State().Enabled)
	require.True(t, restarted.State().Active)
	require.False(t, restarted.State().Ready, "boot still requires fresh readiness observations")
	require.NoError(t, restarted.Off(ctx))
	settings, err = store.Load()
	require.NoError(t, err)
	require.False(t, settings.AutoEnable)
	require.Equal(t, "41:42:86:99:1D:61", settings.DeviceAddress, "Off preserves the bond identity")

	disabled := bluetooth.NewManager(f, &fakeRuntime{hardware: f}, store)
	require.NoError(t, disabled.Initialize(ctx))
	require.False(t, disabled.State().Enabled)
	require.False(t, disabled.State().Active)
}

func TestActivationSaveFailureDoesNotAuthorizeTransmission(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "bluetooth.json")
	store := bluetooth.FileStore{Path: path}
	f := &fakeHardware{inventory: bluetooth.Inventory{
		Controllers: []bluetooth.Controller{{Path: "/org/bluez/hci0", Address: "AA:BB:CC:DD:EE:FF"}},
		Accessories: []bluetooth.Accessory{{Path: "/org/bluez/hci0/dev_A", Adapter: "/org/bluez/hci0", Address: "41:42:86:99:1D:61", Supported: true, Bonded: true}},
	}}
	runtime := &fakeRuntime{hardware: f}
	m := bluetooth.NewManager(f, runtime, store)
	require.NoError(t, m.Initialize(ctx))
	require.NoError(t, m.Begin(ctx, "/org/bluez/hci0"))
	require.NoError(t, m.Connect(ctx, "/org/bluez/hci0/dev_A"))
	lease := runtime.Lease()
	lease.ObserveAudioReady(true)
	lease.ObserveButtonsReady(true)
	lease.ObserveHFPReady(true)
	// Replacing the settings file with a directory makes atomic save fail.
	require.NoError(t, os.Remove(path))
	require.NoError(t, os.Mkdir(path, 0700))
	require.Error(t, m.Activate(ctx))
	require.False(t, lease.CanTransmit())
	require.False(t, m.State().AutoEnable)
	// A storage failure must not prevent the immediate safety shutdown.
	require.Error(t, m.Off(ctx))
	require.False(t, lease.ConnectionAllowed())
	require.False(t, m.State().Enabled)
	calls := f.Calls()
	require.Equal(t, "off:/org/bluez/hci0", calls[len(calls)-1])
	require.Contains(t, calls, "runtime-stop")
}
