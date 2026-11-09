package bluetooth

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	bluezService         = "org.bluez"
	adapterInterface     = "org.bluez.Adapter1"
	deviceInterface      = "org.bluez.Device1"
	managedObjectsMethod = "org.freedesktop.DBus.ObjectManager.GetManagedObjects"
	propertiesSetMethod  = "org.freedesktop.DBus.Properties.Set"
	propertiesGetMethod  = "org.freedesktop.DBus.Properties.Get"
)

type managedObjects map[dbus.ObjectPath]map[string]map[string]dbus.Variant

type Controller struct {
	Path        dbus.ObjectPath
	Address     string
	Name        string
	Powered     bool
	Discovering bool
}

type Accessory struct {
	Path      dbus.ObjectPath
	Adapter   dbus.ObjectPath
	Address   string
	Name      string
	Paired    bool
	Bonded    bool
	Connected bool
	Supported bool
}

type Inventory struct {
	Controllers []Controller
	Accessories []Accessory
}

type busCall func(context.Context, dbus.ObjectPath, string, ...interface{}) *dbus.Call

// BlueZ performs transport operations. The lifecycle owner must authorize all
// mutations. Snapshot is passive even when a controller is powered off.
type BlueZ struct {
	call    busCall
	export  func(interface{}, dbus.ObjectPath, string) error
	pairing chan struct{}
}

func NewBlueZ(conn *dbus.Conn) *BlueZ {
	return &BlueZ{export: conn.Export, pairing: make(chan struct{}, 1), call: func(ctx context.Context, path dbus.ObjectPath, method string, args ...interface{}) *dbus.Call {
		return conn.Object(bluezService, path).CallWithContext(ctx, method, 0, args...)
	}}
}

func (b *BlueZ) Snapshot(ctx context.Context) (Inventory, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var objects managedObjects
	if err := b.call(ctx, "/", managedObjectsMethod).Store(&objects); err != nil {
		return Inventory{}, fmt.Errorf("read Bluetooth inventory: %w", err)
	}
	return inventoryFromObjects(objects), nil
}

func inventoryFromObjects(objects managedObjects) Inventory {
	result := Inventory{Controllers: make([]Controller, 0), Accessories: make([]Accessory, 0)}
	for path, interfaces := range objects {
		if p, ok := interfaces[adapterInterface]; ok {
			result.Controllers = append(result.Controllers, Controller{Path: path, Address: stringProperty(p, "Address"), Name: stringProperty(p, "Alias"), Powered: boolProperty(p, "Powered"), Discovering: boolProperty(p, "Discovering")})
		}
		if p, ok := interfaces[deviceInterface]; ok {
			adapter, _ := p["Adapter"].Value().(dbus.ObjectPath)
			name := stringProperty(p, "Name")
			if name == "" {
				name = stringProperty(p, "Alias")
			}
			result.Accessories = append(result.Accessories, Accessory{Path: path, Adapter: adapter, Address: stringProperty(p, "Address"), Name: name, Paired: boolProperty(p, "Paired"), Bonded: boolProperty(p, "Bonded"), Connected: boolProperty(p, "Connected"), Supported: strings.EqualFold(strings.TrimSpace(name), "BS-22")})
		}
	}
	sort.Slice(result.Controllers, func(i, j int) bool { return result.Controllers[i].Path < result.Controllers[j].Path })
	sort.Slice(result.Accessories, func(i, j int) bool { return result.Accessories[i].Path < result.Accessories[j].Path })
	return result
}

func stringProperty(p map[string]dbus.Variant, key string) string {
	v, _ := p[key].Value().(string)
	return v
}
func boolProperty(p map[string]dbus.Variant, key string) bool {
	v, _ := p[key].Value().(bool)
	return v
}

func (b *BlueZ) SetPower(ctx context.Context, adapter dbus.ObjectPath, powered bool) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return b.call(ctx, adapter, propertiesSetMethod, adapterInterface, "Powered", dbus.MakeVariant(powered)).Err
}

// Discover uses Classic inquiry for BS-22 audio pairing, rather than allowing
// an LE advertisement to determine the bearer used by Device1.Pair. It releases
// its discovery session on completion or cancellation using a fresh deadline.
func (b *BlueZ) Discover(parent context.Context, adapter dbus.ObjectPath, duration time.Duration) (err error) {
	if duration <= 0 || duration > time.Minute {
		return errors.New("discovery duration must be between zero and one minute")
	}
	ctx, cancel := context.WithTimeout(parent, duration)
	defer cancel()
	startAttempted := false
	// A timeout does not prove the server rejected a mutation. Always release
	// our discovery session and filter, including when a reply was lost.
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if startAttempted {
			stopErr := b.call(cleanup, adapter, adapterInterface+".StopDiscovery").Err
			if stopErr != nil {
				err = errors.Join(err, fmt.Errorf("stop discovery: %w", stopErr))
			}
		}
		// Use a separate deadline so a failed stop cannot prevent filter cleanup.
		filterCtx, filterCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer filterCancel()
		if filterErr := b.call(filterCtx, adapter, adapterInterface+".SetDiscoveryFilter", map[string]dbus.Variant{}).Err; filterErr != nil {
			err = errors.Join(err, fmt.Errorf("clear discovery filter: %w", filterErr))
		}
	}()
	filter := map[string]dbus.Variant{"Transport": dbus.MakeVariant("bredr")}
	if err := b.call(ctx, adapter, adapterInterface+".SetDiscoveryFilter", filter).Err; err != nil {
		return fmt.Errorf("set discovery filter: %w", err)
	}
	startAttempted = true
	if err := b.call(ctx, adapter, adapterInterface+".StartDiscovery").Err; err != nil {
		return fmt.Errorf("start discovery: %w", err)
	}
	<-ctx.Done()
	// A caller cancellation/deadline is distinct from normal scan completion.
	return parent.Err()
}

func (b *BlueZ) Pair(ctx context.Context, device dbus.ObjectPath) (err error) {
	if b.export == nil || b.pairing == nil {
		return errors.New("pairing agent is not configured")
	}
	select {
	case b.pairing <- struct{}{}:
	default:
		return errors.New("Bluetooth pairing is already in progress")
	}
	defer func() { <-b.pairing }()
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	agent := &pairingAgent{device: device}
	if err = b.export(agent, pairingAgentPath, pairingAgentInterface); err != nil {
		return fmt.Errorf("export pairing agent: %w", err)
	}
	defer func() {
		agent.closed.Store(true)
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		unregisterErr := b.call(cleanup, "/org/bluez", "org.bluez.AgentManager1.UnregisterAgent", pairingAgentPath).Err
		// DoesNotExist is normal when registration itself failed or BlueZ restarted.
		var de dbus.Error
		if errors.As(unregisterErr, &de) && de.Name == "org.bluez.Error.DoesNotExist" {
			unregisterErr = nil
		}
		err = errors.Join(err, unregisterErr, b.export(nil, pairingAgentPath, pairingAgentInterface))
	}()
	if err = b.call(ctx, "/org/bluez", "org.bluez.AgentManager1.RegisterAgent", pairingAgentPath, "NoInputNoOutput").Err; err != nil {
		return fmt.Errorf("register pairing agent: %w", err)
	}
	var adapterValue dbus.Variant
	if err = b.call(ctx, device, propertiesGetMethod, deviceInterface, "Adapter").Store(&adapterValue); err != nil {
		return fmt.Errorf("read pairing controller: %w", err)
	}
	adapter, ok := adapterValue.Value().(dbus.ObjectPath)
	if !ok || !adapter.IsValid() {
		return errors.New("invalid pairing controller")
	}
	// Linux forces non-bonding authentication when the adapter is not
	// bondable, even for locally initiated Pair. Open only this scoped window.
	// Install cleanup before Set: a lost reply may still have enabled it.
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		err = errors.Join(err, b.call(cleanup, adapter, propertiesSetMethod, adapterInterface, "Pairable", dbus.MakeVariant(false)).Err)
	}()
	if err = b.call(ctx, adapter, propertiesSetMethod, adapterInterface, "Pairable", dbus.MakeVariant(true)).Err; err != nil {
		return fmt.Errorf("enable Bluetooth bonding: %w", err)
	}
	if err = b.call(ctx, device, deviceInterface+".Pair").Err; err != nil {
		agent.closed.Store(true)
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = b.call(cleanup, device, deviceInterface+".CancelPairing").Err // Preserve the original failure.
		return fmt.Errorf("pair Bluetooth microphone: %w", err)
	}
	var bonded dbus.Variant
	if err = b.call(ctx, device, propertiesGetMethod, deviceInterface, "Bonded").Store(&bonded); err != nil {
		return fmt.Errorf("verify Bluetooth bond: %w", err)
	}
	if persistent, _ := bonded.Value().(bool); !persistent {
		return errors.New("Bluetooth pairing did not create a persistent bond; put the accessory in pairing mode and retry")
	}
	return nil
}

func (b *BlueZ) Disconnect(ctx context.Context, device dbus.ObjectPath) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return b.call(ctx, device, deviceInterface+".Disconnect").Err
}

func (b *BlueZ) Forget(ctx context.Context, adapter, device dbus.ObjectPath) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return b.call(ctx, adapter, adapterInterface+".RemoveDevice", device).Err
}

// Trust authorizes services for a selected, bonded accessory. Manager validates
// selection and bond before calling; this does not power or connect an adapter.
func (b *BlueZ) Trust(ctx context.Context, device dbus.ObjectPath) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return b.call(ctx, device, propertiesSetMethod, deviceInterface, "Trusted", dbus.MakeVariant(true)).Err
}
