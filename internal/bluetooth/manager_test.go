package bluetooth_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/openmanet/openmanetd/internal/bluetooth"
	"github.com/stretchr/testify/require"
)

func TestManagerOffWaitsForDiscoveryCleanup(t *testing.T) {
	f := &fakeHardware{inventory: bluetooth.Inventory{Controllers: []bluetooth.Controller{{Path: "/org/bluez/hci0"}}}, started: make(chan struct{}), cleanup: make(chan struct{})}
	m := bluetooth.NewManager(f, nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	require.NoError(t, m.Begin(ctx, "/org/bluez/hci0"))
	scanDone := make(chan error, 1)
	go func() { scanDone <- m.Discover(ctx, time.Minute) }()
	// Always release the fake cleanup, including on failed assertions.
	t.Cleanup(func() {
		cancel()
		select {
		case <-f.cleanup:
		default:
			close(f.cleanup)
		}
		<-scanDone
	})
	select {
	case <-f.started:
	case <-ctx.Done():
		t.Fatal("scan never started")
	}
	offDone := make(chan error, 1)
	go func() { offDone <- m.Off(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-f.cleanup:
		default:
			close(f.cleanup)
		}
		<-offDone
	})
	// A blocked scan cannot permit premature power-off, regardless of scheduling.
	require.Equal(t, []string{"off:/org/bluez/hci0", "on:/org/bluez/hci0", "scan"}, f.Calls())
	close(f.cleanup)
	// Preserve results for Cleanup to join even on assertion failure.
	err := <-offDone
	offDone <- err
	require.NoError(t, err)
	require.Equal(t, []string{"off:/org/bluez/hci0", "on:/org/bluez/hci0", "scan", "scan-cleaned", "off:/org/bluez/hci0"}, f.Calls())
	require.False(t, m.State().Enabled)
	require.False(t, m.State().ConnectAllowed)
}

func TestManagerStartsOffAndValidatesSelection(t *testing.T) {
	f := &fakeHardware{inventory: bluetooth.Inventory{
		Controllers: []bluetooth.Controller{{Path: "/org/bluez/hci0"}, {Path: "/org/bluez/hci1"}},
		Accessories: []bluetooth.Accessory{{Path: "/org/bluez/hci1/dev_A", Adapter: "/org/bluez/hci1", Supported: true}},
	}}
	m := bluetooth.NewManager(f, nil, nil)
	require.Empty(t, f.Calls())
	require.ErrorIs(t, m.Discover(context.Background(), time.Minute), bluetooth.ErrDisabled)
	require.ErrorIs(t, m.Begin(context.Background(), "/missing"), bluetooth.ErrUnavailable)
	require.NoError(t, m.Off(context.Background()))
	require.Equal(t, []string{"off:/org/bluez/hci0", "off:/org/bluez/hci1"}, f.Calls())
	require.NoError(t, m.Begin(context.Background(), "/org/bluez/hci0"))
	require.Error(t, m.Pair(context.Background(), "/org/bluez/hci1/dev_A"))
	require.NotContains(t, f.Calls(), "pair")
	require.False(t, m.State().Active, "setup must not activate mesh transmission")
}

func TestManagerConnectActivateAndOff(t *testing.T) {
	f := &fakeHardware{inventory: bluetooth.Inventory{
		Controllers: []bluetooth.Controller{{Path: "/org/bluez/hci0"}},
		Accessories: []bluetooth.Accessory{{Path: "/org/bluez/hci0/dev_41_42_86_99_1D_61", Adapter: "/org/bluez/hci0", Supported: true, Paired: true, Bonded: true}},
	}}
	runtime := &fakeRuntime{hardware: f}
	m := bluetooth.NewManager(f, runtime, nil)
	ctx := context.Background()
	require.NoError(t, m.Begin(ctx, "/org/bluez/hci0"))
	require.NoError(t, m.Connect(ctx, "/org/bluez/hci0/dev_41_42_86_99_1D_61"))
	lease := runtime.Lease()
	require.True(t, lease.ConnectionAllowed())
	require.False(t, lease.CanTransmit())
	require.ErrorIs(t, m.Activate(ctx), bluetooth.ErrNotReady)
	lease.ObserveAudioReady(true)
	lease.ObserveButtonsReady(true)
	lease.ObserveHFPReady(true)
	require.NoError(t, m.Activate(ctx))
	require.True(t, lease.CanTransmit())
	require.NoError(t, m.Off(ctx))
	require.False(t, lease.ConnectionAllowed())
	require.Equal(t, []string{"off:/org/bluez/hci0", "on:/org/bluez/hci0", "trust:/org/bluez/hci0/dev_41_42_86_99_1D_61", "runtime-start", "runtime-stop", "off:/org/bluez/hci0"}, f.Calls())
}

func TestTemporaryPairingCannotStartRuntime(t *testing.T) {
	f := &fakeHardware{inventory: bluetooth.Inventory{
		Controllers: []bluetooth.Controller{{Path: "/org/bluez/hci0"}},
		Accessories: []bluetooth.Accessory{{Path: "/org/bluez/hci0/dev_A", Adapter: "/org/bluez/hci0", Supported: true, Paired: true}},
	}}
	m := bluetooth.NewManager(f, &fakeRuntime{hardware: f}, nil)
	ctx := context.Background()
	require.NoError(t, m.Begin(ctx, "/org/bluez/hci0"))
	require.Error(t, m.Connect(ctx, "/org/bluez/hci0/dev_A"))
	require.Contains(t, f.Calls(), "pair", "temporary pairing must not skip persistent bonding")
	require.NotContains(t, f.Calls(), "runtime-start")
	status, err := m.Status(ctx)
	require.NoError(t, err)
	require.Empty(t, status.Settings.DeviceAddress)
}

func TestManagerTrustFailureDoesNotStartRuntime(t *testing.T) {
	expected := errors.New("trust failed")
	f := &fakeHardware{trustErr: expected, inventory: bluetooth.Inventory{
		Controllers: []bluetooth.Controller{{Path: "/org/bluez/hci0"}},
		Accessories: []bluetooth.Accessory{{Path: "/org/bluez/hci0/dev_A", Adapter: "/org/bluez/hci0", Supported: true, Bonded: true}},
	}}
	m := bluetooth.NewManager(f, &fakeRuntime{hardware: f}, nil)
	require.NoError(t, m.Begin(context.Background(), "/org/bluez/hci0"))
	require.ErrorIs(t, m.Connect(context.Background(), "/org/bluez/hci0/dev_A"), expected)
	require.Contains(t, f.Calls(), "trust:/org/bluez/hci0/dev_A")
	require.NotContains(t, f.Calls(), "runtime-start")
	require.False(t, m.State().Active)
}
