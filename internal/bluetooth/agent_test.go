package bluetooth

import (
	"context"
	"errors"
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestPairingAgentScopesAuthorization(t *testing.T) {
	a := &pairingAgent{device: "/selected"}
	if a.RequestAuthorization("/selected") != nil {
		t.Fatal("selected accessory rejected")
	}
	if a.RequestAuthorization("/other") == nil {
		t.Fatal("unselected accessory accepted")
	}
	if a.AuthorizeService("/selected", "00001101-0000-1000-8000-00805f9b34fb") == nil {
		t.Fatal("unrelated service accepted")
	}
	if a.AuthorizeService("/selected", "0000111e-0000-1000-8000-00805f9b34fb") != nil {
		t.Fatal("Hands-Free rejected")
	}
	a.Cancel()
	if a.RequestConfirmation("/selected", 0) == nil {
		t.Fatal("late confirmation accepted")
	}
}

func TestPairFailureCancelsAndUnregistersAgent(t *testing.T) {
	original := errors.New("pairing failed")
	var methods []string
	exported := false
	b := &BlueZ{pairing: make(chan struct{}, 1), export: func(value interface{}, _ dbus.ObjectPath, _ string) error { exported = value != nil; return nil }, call: func(ctx context.Context, _ dbus.ObjectPath, method string, args ...interface{}) *dbus.Call {
		if method == propertiesGetMethod && args[1] == "Adapter" {
			return &dbus.Call{Body: []interface{}{dbus.MakeVariant(dbus.ObjectPath("/org/bluez/hci0"))}}
		}
		methods = append(methods, method)
		if method == deviceInterface+".Pair" {
			return &dbus.Call{Err: original}
		}
		return &dbus.Call{}
	}}
	if err := b.Pair(context.Background(), "/selected"); !errors.Is(err, original) {
		t.Fatalf("lost original error: %v", err)
	}
	if exported {
		t.Fatal("agent leaked after failure")
	}
	if len(methods) != 6 || methods[3] != deviceInterface+".CancelPairing" || methods[4] != propertiesSetMethod || methods[5] != "org.bluez.AgentManager1.UnregisterAgent" {
		t.Fatalf("unexpected cleanup: %v", methods)
	}
}

func TestPairingRequiresPersistentBondAndClosesWindow(t *testing.T) {
	for _, tc := range []struct {
		name      string
		bonded    bool
		enableErr error
		wantErr   bool
	}{
		{name: "bonded", bonded: true},
		{name: "temporary pairing", wantErr: true},
		{name: "lost enable reply", enableErr: context.DeadlineExceeded, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var pairable bool
			var paired, disabled, exported bool
			b := &BlueZ{pairing: make(chan struct{}, 1), export: func(value interface{}, _ dbus.ObjectPath, _ string) error { exported = value != nil; return nil }}
			b.call = func(ctx context.Context, path dbus.ObjectPath, method string, args ...interface{}) *dbus.Call {
				switch method {
				case propertiesGetMethod:
					if args[1] == "Adapter" {
						return &dbus.Call{Body: []interface{}{dbus.MakeVariant(dbus.ObjectPath("/org/bluez/hci0"))}}
					}
					return &dbus.Call{Body: []interface{}{dbus.MakeVariant(tc.bonded)}}
				case propertiesSetMethod:
					if path != "/org/bluez/hci0" || args[1] != "Pairable" {
						t.Fatal("wrong bonding target")
					}
					pairable = args[2].(dbus.Variant).Value().(bool)
					if pairable {
						return &dbus.Call{Err: tc.enableErr}
					}
					if ctx.Err() != nil {
						t.Fatal("bonding cleanup used expired context")
					}
					disabled = true
				case deviceInterface + ".Pair":
					if !pairable || !exported {
						t.Fatal("pairing without bonding window and agent")
					}
					paired = true
				}
				return &dbus.Call{}
			}
			err := b.Pair(context.Background(), "/selected")
			if (err != nil) != tc.wantErr {
				t.Fatalf("Pair() error=%v", err)
			}
			if pairable || !disabled || exported {
				t.Fatal("pairing window or agent leaked")
			}
			if tc.enableErr == nil && !paired {
				t.Fatal("pair operation skipped")
			}
			if tc.enableErr != nil && paired {
				t.Fatal("paired after failed enable")
			}
		})
	}
}
