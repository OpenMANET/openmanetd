package bluetooth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

func TestSnapshotIsPassive(t *testing.T) {
	calls := 0
	b := &BlueZ{call: func(ctx context.Context, path dbus.ObjectPath, method string, args ...interface{}) *dbus.Call {
		calls++
		if path != "/" || method != managedObjectsMethod || len(args) != 0 {
			t.Fatalf("status attempted mutation: %s", method)
		}
		objects := managedObjects{"/org/bluez/hci0": {adapterInterface: {"Powered": dbus.MakeVariant(false)}}}
		return &dbus.Call{Body: []interface{}{objects}}
	}}
	snapshot, err := b.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(snapshot.Controllers) != 1 || snapshot.Controllers[0].Powered {
		t.Fatal("unexpected passive inventory")
	}
}

func TestDiscoveryCancellationStopsUsingLiveContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var methods []string
	b := &BlueZ{call: func(ctx context.Context, path dbus.ObjectPath, method string, args ...interface{}) *dbus.Call {
		methods = append(methods, method)
		if method == adapterInterface+".StartDiscovery" {
			cancel()
		}
		if method == adapterInterface+".StopDiscovery" && ctx.Err() != nil {
			t.Fatal("cleanup inherited canceled context")
		}
		return &dbus.Call{}
	}}
	if err := b.Discover(ctx, "/org/bluez/hci0", time.Minute); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	if len(methods) != 4 || methods[2] != adapterInterface+".StopDiscovery" {
		t.Fatalf("discovery leaked: %v", methods)
	}
}

func TestAbsentControllerInventory(t *testing.T) {
	inventory := inventoryFromObjects(managedObjects{})
	if len(inventory.Controllers) != 0 || len(inventory.Accessories) != 0 {
		t.Fatal("invented unavailable hardware")
	}
}

func TestDiscoveryStartTimeoutStillCleansUp(t *testing.T) {
	var methods []string
	b := &BlueZ{call: func(ctx context.Context, path dbus.ObjectPath, method string, args ...interface{}) *dbus.Call {
		methods = append(methods, method)
		if method == adapterInterface+".StartDiscovery" {
			return &dbus.Call{Err: context.DeadlineExceeded}
		}
		if len(methods) > 2 && ctx.Err() != nil {
			t.Fatal("cleanup context already expired")
		}
		if len(methods) == 4 && len(args[0].(map[string]dbus.Variant)) != 0 {
			t.Fatal("discovery filter retained")
		}
		return &dbus.Call{}
	}}
	err := b.Discover(context.Background(), "/org/bluez/hci0", time.Minute)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lost original error: %v", err)
	}
	if len(methods) != 4 || methods[2] != adapterInterface+".StopDiscovery" || methods[3] != adapterInterface+".SetDiscoveryFilter" {
		t.Fatalf("missing cleanup: %v", methods)
	}
}

func TestTrustOnlySetsSelectedDeviceProperty(t *testing.T) {
	calls := 0
	b := &BlueZ{call: func(ctx context.Context, path dbus.ObjectPath, method string, args ...interface{}) *dbus.Call {
		calls++
		if path != "/org/bluez/hci0/dev_A" || method != propertiesSetMethod || len(args) != 3 || args[0] != deviceInterface || args[1] != "Trusted" || args[2].(dbus.Variant).Value() != true {
			t.Fatalf("unexpected trust mutation: %s %s %v", path, method, args)
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("trust has no deadline")
		}
		return &dbus.Call{}
	}}
	if err := b.Trust(context.Background(), "/org/bluez/hci0/dev_A"); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("calls=%d", calls)
	}
}
