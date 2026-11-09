package bluetooth_test

import (
	"context"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/openmanet/openmanetd/internal/bluetooth"
)

type fakeHardware struct {
	mu        sync.Mutex // protects calls and inventory
	calls     []string
	trustErr  error
	inventory bluetooth.Inventory
	started   chan struct{}
	cleanup   chan struct{}
}

func (f *fakeHardware) record(call string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
}
func (f *fakeHardware) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}
func (f *fakeHardware) Snapshot(context.Context) (bluetooth.Inventory, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return bluetooth.Inventory{Controllers: append([]bluetooth.Controller(nil), f.inventory.Controllers...), Accessories: append([]bluetooth.Accessory(nil), f.inventory.Accessories...)}, nil
}
func (f *fakeHardware) SetPower(_ context.Context, p dbus.ObjectPath, on bool) error {
	action := "off:"
	if on {
		action = "on:"
	}
	f.record(action + string(p))
	return nil
}
func (f *fakeHardware) Discover(ctx context.Context, _ dbus.ObjectPath, _ time.Duration) error {
	f.record("scan")
	close(f.started)
	<-ctx.Done()
	<-f.cleanup
	f.record("scan-cleaned")
	return ctx.Err()
}
func (f *fakeHardware) Pair(context.Context, dbus.ObjectPath) error                    { f.record("pair"); return nil }
func (f *fakeHardware) Disconnect(context.Context, dbus.ObjectPath) error              { return nil }
func (f *fakeHardware) Forget(context.Context, dbus.ObjectPath, dbus.ObjectPath) error { return nil }

type fakeRuntime struct {
	mu       sync.Mutex
	lease    *bluetooth.Lease
	hardware *fakeHardware
}

func (f *fakeRuntime) StartBluetooth(_ context.Context, _ bluetooth.Accessory, lease *bluetooth.Lease) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lease = lease
	f.hardware.record("runtime-start")
	return nil
}
func (f *fakeRuntime) StopBluetooth() error    { f.hardware.record("runtime-stop"); return nil }
func (f *fakeRuntime) Lease() *bluetooth.Lease { f.mu.Lock(); defer f.mu.Unlock(); return f.lease }

func (f *fakeRuntime) TestBluetoothTone(context.Context) error { f.hardware.record("tone"); return nil }

func (f *fakeHardware) Trust(_ context.Context, path dbus.ObjectPath) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "trust:"+string(path))
	return f.trustErr
}
