package bluetooth

import (
	"strings"
	"sync/atomic"

	"github.com/godbus/dbus/v5"
)

const pairingAgentPath = dbus.ObjectPath("/org/openmanet/bluetooth/pairing")
const pairingAgentInterface = "org.bluez.Agent1"

// pairingAgent is exported only during an explicitly requested pairing attempt.
// The device is immutable; cancellation atomically revokes late callbacks.
type pairingAgent struct {
	device dbus.ObjectPath
	closed atomic.Bool
}

func rejectedPairing() *dbus.Error {
	return dbus.NewError("org.bluez.Error.Rejected", []interface{}{"Pairing request is not authorized by this setup session"})
}
func (a *pairingAgent) authorize(device dbus.ObjectPath) *dbus.Error {
	if a.closed.Load() || device != a.device {
		return rejectedPairing()
	}
	return nil
}
func (a *pairingAgent) Release() *dbus.Error { a.closed.Store(true); return nil }
func (a *pairingAgent) Cancel() *dbus.Error  { a.closed.Store(true); return nil }
func (a *pairingAgent) RequestAuthorization(device dbus.ObjectPath) *dbus.Error {
	return a.authorize(device)
}
func (a *pairingAgent) RequestConfirmation(device dbus.ObjectPath, _ uint32) *dbus.Error {
	return a.authorize(device)
}
func (a *pairingAgent) AuthorizeService(device dbus.ObjectPath, uuid string) *dbus.Error {
	if err := a.authorize(device); err != nil {
		return err
	}
	switch strings.ToLower(uuid) {
	case "0000111e-0000-1000-8000-00805f9b34fb", "0000111f-0000-1000-8000-00805f9b34fb":
		return nil
	default:
		return rejectedPairing()
	}
}

// PIN/key entry is not supported by the BS-22 setup flow. Never guess a PIN.
func (a *pairingAgent) RequestPinCode(_ dbus.ObjectPath) (string, *dbus.Error) {
	return "", rejectedPairing()
}
func (a *pairingAgent) RequestPasskey(_ dbus.ObjectPath) (uint32, *dbus.Error) {
	return 0, rejectedPairing()
}
func (a *pairingAgent) DisplayPinCode(_ dbus.ObjectPath, _ string) *dbus.Error {
	return rejectedPairing()
}
func (a *pairingAgent) DisplayPasskey(_ dbus.ObjectPath, _ uint32, _ uint16) *dbus.Error {
	return rejectedPairing()
}
