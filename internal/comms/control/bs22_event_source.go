package control

import (
	"context"
	"encoding/binary"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/rs/zerolog"
)

const (
	bluezService                       = "org.bluez"
	bluezRootPath                      = dbus.ObjectPath("/")
	bluezManagedObjectsMethod          = "org.freedesktop.DBus.ObjectManager.GetManagedObjects"
	bluezInterfacesAddedRule           = "type='signal',interface='org.freedesktop.DBus.ObjectManager',sender='org.bluez',member='InterfacesAdded'"
	bluezInterfacesRemovedRule         = "type='signal',interface='org.freedesktop.DBus.ObjectManager',sender='org.bluez',member='InterfacesRemoved'"
	bluezInterfacesAddedSignal         = "org.freedesktop.DBus.ObjectManager.InterfacesAdded"
	bluezInterfacesRemovedSignal       = "org.freedesktop.DBus.ObjectManager.InterfacesRemoved"
	bluezAdapterInterface              = "org.bluez.Adapter1"
	bluezGattServiceInterface          = "org.bluez.GattService1"
	bluezGattCharacteristicInterface   = "org.bluez.GattCharacteristic1"
	bluezGattCharacteristicStartNotify = bluezGattCharacteristicInterface + ".StartNotify"
	bluezGattCharacteristicWriteValue  = bluezGattCharacteristicInterface + ".WriteValue"
	bluezAdapterConnectDeviceMethod    = bluezAdapterInterface + ".ConnectDevice"
	bluezBearerLEInterface             = "org.bluez.Bearer.LE1"
	bluezBearerLEConnectMethod         = bluezBearerLEInterface + ".Connect"
	bluezAddressTypePublic             = "public"
	bluezAddressTypeRandom             = "random"
	bs22Name                           = "BS-22"
	bs22HMServiceUUID                  = "00001100-d102-11e1-9b23-00025b00a5a5"
	bs22HMWriteUUID                    = "00001101-d102-11e1-9b23-00025b00a5a5"
	bs22HMNotifyUUID                   = "00001102-d102-11e1-9b23-00025b00a5a5"
	bs22HMVendorID                     = 0x100A
	bs22HMReadSettings                 = 13
	bs22HMKeyEventInd                  = 257
	bs22HMResponseBit                  = 0x8000
	bs22BLEConnectRetryInterval        = 5 * time.Second
	bs22BLERescanInterval              = 2 * time.Second
	bs22BLEPrimeRetryInitial           = 30 * time.Second
	bs22BLEPrimeRetryMax               = 5 * time.Minute
)

type bluezManagedObjectMap map[dbus.ObjectPath]map[string]map[string]dbus.Variant

type bluezManagedLister func(*dbus.Conn) (bluezManagedObjectMap, error)
type bluezCharacteristicNotifier func(*dbus.Conn, dbus.ObjectPath) error
type bluezCharacteristicWriter func(*dbus.Conn, dbus.ObjectPath, []byte) error
type bluezDeviceConnector func(*dbus.Conn, bs22DeviceInfo) error

type bs22DeviceInfo struct {
	Path        dbus.ObjectPath
	AdapterPath dbus.ObjectPath
	Alias       string
	Name        string
	Address     string
	AddressType string
	Connected   bool
}

type bs22BLEBinding struct {
	Device      bs22DeviceInfo
	ServicePath dbus.ObjectPath
	WritePath   dbus.ObjectPath
	NotifyPath  dbus.ObjectPath
}

type bs22HMPacket struct {
	VendorID uint16
	Command  uint16
	Payload  []byte
}

func (p bs22HMPacket) CommandCode() uint16 {
	return p.Command &^ bs22HMResponseBit
}

func (p bs22HMPacket) IsResponse() bool {
	return p.Command&bs22HMResponseBit != 0
}

type BS22Readiness interface {
	ObserveButtonsReady(bool)
	ObserveHFPReady(bool)
}

type bs22EventSource struct {
	channelSteps   chan int
	speakerVolumes chan int
	readiness      BS22Readiness
	selectedDevice dbus.ObjectPath

	manageHFP      bool
	log            zerolog.Logger
	dial           systemBusDialer
	listManaged    bluezManagedLister
	startNotify    bluezCharacteristicNotifier
	writeValue     bluezCharacteristicWriter
	connectDevice  bluezDeviceConnector
	xeventFactory  func(zerolog.Logger) EventSource
	rescanInterval time.Duration
	connectRetry   time.Duration
	dedupeMu       sync.Mutex
	lastEvent      PTTEvent
	lastEventAt    time.Time
}

// NewBS22EventSource monitors the BS-22 BLE HM control path and falls back to
// BlueALSA XEVENT events from the Classic HFP link.
func NewBS22EventSource(log zerolog.Logger) EventSource {
	return &bs22EventSource{
		log:            log,
		channelSteps:   make(chan int, 8),
		speakerVolumes: make(chan int, 16),
		manageHFP:      true,
		dial: func() (*dbus.Conn, error) {
			return dbus.ConnectSystemBus()
		},
		listManaged:   listBlueZManagedObjects,
		startNotify:   startBlueZNotify,
		writeValue:    writeBlueZCharacteristicValue,
		connectDevice: connectBlueZDevice,
		xeventFactory: NewBlueALSAXEventSource,
	}
}

// NewSelectedBS22EventSource restricts both transports and fallback button
// events to the accessory selected by the user. The caller owns session consent.
func NewSelectedBS22EventSource(log zerolog.Logger, device dbus.ObjectPath, readiness BS22Readiness) (EventSource, error) {
	if !device.IsValid() || !strings.HasPrefix(string(device), "/org/bluez/") || !strings.Contains(string(device), "/dev_") {
		return nil, errors.New("select a Bluetooth accessory before starting comms")
	}

	source := NewBS22EventSource(log).(*bs22EventSource)
	source.selectedDevice = device
	source.readiness = readiness
	source.xeventFactory = func(log zerolog.Logger) EventSource {
		fallback := NewBlueALSAXEventSource(log).(*blueALSAXEventSource)
		fallback.selectedRFCOMM = dbus.ObjectPath(strings.Replace(string(device), "/org/bluez/", "/org/bluealsa/", 1) + "/rfcomm")
		return fallback
	}
	return source, nil
}

func (s *bs22EventSource) findDevice(managed bluezManagedObjectMap) (bs22DeviceInfo, bool) {
	if s.selectedDevice == "" {
		return findBS22Device(managed)
	}
	interfaces, ok := managed[s.selectedDevice]
	if !ok {
		return bs22DeviceInfo{}, false
	}
	return findBS22Device(bluezManagedObjectMap{s.selectedDevice: interfaces})
}

func (s *bs22EventSource) Events(ctx context.Context) <-chan PTTEvent {
	out := make(chan PTTEvent, 4)
	raw := make(chan PTTEvent, 8)

	var wg sync.WaitGroup

	if s.manageHFP {
		wg.Add(1)
		go func() { defer wg.Done(); s.monitorHFP(ctx) }()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		s.monitorBLE(ctx, raw)
	}()

	if s.xeventFactory != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.forwardBlueALSAFallback(ctx, raw)
		}()
	}

	go func() {
		wg.Wait()
		close(raw)
	}()

	go func() {
		defer close(out)
		if s.speakerVolumes != nil {
			defer close(s.speakerVolumes)
		}
		if s.channelSteps != nil {
			defer close(s.channelSteps)
		}
		defer wg.Wait() // Closing out proves all transport workers have stopped.

		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-raw:
				if !ok {
					return
				}

				if !s.emitMergedEvent(ctx, out, ev) {
					return
				}
			}
		}
	}()

	return out
}

func (s *bs22EventSource) emitMergedEvent(ctx context.Context, out chan<- PTTEvent, ev PTTEvent) bool {
	if s.shouldSuppressDuplicate(ev) {
		return true
	}

	return sendPTTEvent(ctx, out, ev)
}

func (s *bs22EventSource) shouldSuppressDuplicate(ev PTTEvent) bool {
	s.dedupeMu.Lock()
	defer s.dedupeMu.Unlock()

	now := time.Now()
	if s.lastEvent == ev && now.Sub(s.lastEventAt) < 200*time.Millisecond {
		return true
	}

	s.lastEvent = ev
	s.lastEventAt = now
	return false
}

func (s *bs22EventSource) forwardBlueALSAFallback(ctx context.Context, out chan<- PTTEvent) {
	src := s.xeventFactory(s.log)
	if src == nil {
		return
	}

	events := src.Events(ctx)
	var volumes <-chan int
	if volumeSource, ok := src.(SpeakerVolumeEventSource); ok {
		volumes = volumeSource.SpeakerVolumes()
	}
	var steps <-chan int
	if channels, ok := src.(ChannelEventSource); ok {
		steps = channels.ChannelEvents()
	}
	defer func() {
		for range events {
		}
	}() // Join on every cancellation path.
	for {
		select {
		case <-ctx.Done():
			return
		case gain, ok := <-volumes:
			if !ok {
				volumes = nil
				continue
			}
			queueChannelStep(s.speakerVolumes, gain)
		case step, ok := <-steps:
			if !ok {
				steps = nil
				continue
			}
			queueChannelStep(s.channelSteps, step)
		case ev, ok := <-events:
			if !ok {
				return
			}

			if !sendPTTEvent(ctx, out, ev) {
				return
			}
		}
	}
}

func (s *bs22EventSource) monitorBLE(ctx context.Context, out chan<- PTTEvent) {
	if s.dial == nil || s.listManaged == nil || s.startNotify == nil || s.writeValue == nil {
		s.log.Error().Msg("BS-22 BLE: monitor is not configured")
		return
	}

	conn, err := s.dial()
	if err != nil {
		s.log.Error().Err(err).Msg("BS-22 BLE: failed to connect to system DBus")
		return
	}
	defer conn.Close()

	for _, rule := range []string{
		bluezPropertiesChangedRule,
		bluezInterfacesAddedRule,
		bluezInterfacesRemovedRule,
	} {
		if err := addDBusMatch(conn, rule); err != nil {
			s.log.Error().Err(err).Str("rule", rule).Msg("BS-22 BLE: failed to add DBus match rule")
			return
		}
	}

	sigCh := make(chan *dbus.Signal, 32)
	conn.Signal(sigCh)
	defer conn.RemoveSignal(sigCh)

	s.runBLEMonitor(ctx, conn, sigCh, out)
}

func (s *bs22EventSource) runBLEMonitor(ctx context.Context, conn *dbus.Conn, sigCh <-chan *dbus.Signal, out chan<- PTTEvent) {
	rescanInterval := s.rescanInterval
	if rescanInterval <= 0 {
		rescanInterval = bs22BLERescanInterval
	}
	rescanTicker := time.NewTicker(rescanInterval)
	defer rescanTicker.Stop()

	connectRetry := s.connectRetry
	if connectRetry <= 0 {
		connectRetry = bs22BLEConnectRetryInterval
	}

	var current bs22BLEBinding
	var haveCurrent bool
	var primed bool
	var primeBackoff time.Duration
	var nextPrimeAttemptAt time.Time
	var lastConnectAttempt time.Time
	var held bool

	clearBinding := func() {
		if s.readiness != nil {
			s.readiness.ObserveButtonsReady(false)
		}
		if held {
			// BlueZ cannot deliver the button release once the binding is gone.
			sendPTTEvent(ctx, out, PTTUp)
			held = false
		}
		current = bs22BLEBinding{}
		haveCurrent = false
		primed = false
		primeBackoff = 0
		nextPrimeAttemptAt = time.Time{}
	}
	defer clearBinding()

	syncBinding := func() {
		managed, err := s.listManaged(conn)
		if err != nil {
			s.log.Debug().Err(err).Msg("BS-22 BLE: failed to enumerate BlueZ managed objects")
			clearBinding()
			return
		}

		device, ok := s.findDevice(managed)
		if !ok {
			clearBinding()
			return
		}

		binding, ok := findBS22BLEBindingForDevice(managed, device)
		if !ok || !device.Connected {
			clearBinding()
			// Device1.Connected is shared by BR/EDR and LE. When a Classic
			// connection is up but the HM GATT service is absent, still request
			// LE explicitly; otherwise the PTT source can silently remain on the
			// wrong bearer indefinitely.
			if s.connectDevice != nil && time.Since(lastConnectAttempt) >= connectRetry {
				lastConnectAttempt = time.Now()
				if err := s.connectDevice(conn, device); err != nil && !isIgnorableBlueZConnectError(err) {
					s.log.Debug().
						Err(err).
						Str("device", string(device.Path)).
						Str("address_type", normalizeBlueZAddressType(device.AddressType)).
						Msg("BS-22 BLE: connect request failed")
				}
			}
			return
		}

		if !haveCurrent || current.NotifyPath != binding.NotifyPath {
			clearBinding()
			if err := s.startNotify(conn, binding.NotifyPath); err != nil && !isIgnorableBlueZNotifyError(err) {
				s.log.Debug().Err(err).Str("path", string(binding.NotifyPath)).Msg("BS-22 BLE: StartNotify failed")
				if s.connectDevice != nil && time.Since(lastConnectAttempt) >= connectRetry {
					lastConnectAttempt = time.Now()
					if connectErr := s.connectDevice(conn, device); connectErr != nil && !isIgnorableBlueZConnectError(connectErr) {
						s.log.Debug().Err(connectErr).Str("device", string(device.Path)).Msg("BS-22 BLE: connect request after StartNotify failure failed")
					}
				}
				return
			}

			current = binding
			haveCurrent = true
			s.log.Info().
				Str("device", current.Device.Address).
				Str("notify", string(current.NotifyPath)).
				Str("write", string(current.WritePath)).
				Msg("BS-22 BLE: monitoring HM control channel")
		}

		if primed {
			return
		}

		now := time.Now()
		if !nextPrimeAttemptAt.IsZero() && now.Before(nextPrimeAttemptAt) {
			return
		}

		if err := s.primeBLE(conn, current); err != nil {
			if isBlueZDisconnectedError(err) {
				// Cached GATT objects can outlive LE while Classic keeps
				// Device1.Connected true. Reconnect LE instead of backing off
				// forever against the stale binding.
				clearBinding()
				if s.connectDevice != nil && time.Since(lastConnectAttempt) >= connectRetry {
					lastConnectAttempt = time.Now()
					if connectErr := s.connectDevice(conn, device); connectErr != nil && !isIgnorableBlueZConnectError(connectErr) {
						s.log.Debug().Err(connectErr).Msg("BS-22 BLE: reconnect after control channel disconnect failed")
					}
				}
				return
			}
			// Do not continuously retry priming on every rescan tick. On some
			// stacks this can destabilize SCO/HFP negotiation. Keep passive HM
			// notify + BlueALSA XEVENT fallback active and retry with a slow
			// backoff.
			primeBackoff = nextBS22PrimeBackoff(primeBackoff)
			nextPrimeAttemptAt = now.Add(primeBackoff)
			s.log.Warn().
				Err(err).
				Str("device", current.Device.Address).
				Dur("retry_in", primeBackoff).
				Msg("BS-22 BLE: failed to prime HM control channel; using passive HM/XEVENT fallback")
			return
		}

		primed = true
		if s.readiness != nil {
			s.readiness.ObserveButtonsReady(true)
		}
		primeBackoff = 0
		nextPrimeAttemptAt = time.Time{}
		s.log.Info().Str("device", current.Device.Address).Msg("BS-22 BLE: control channel ready; preserving HFP audio")
	}

	syncBinding()

	for {
		select {
		case <-ctx.Done():
			return
		case <-rescanTicker.C:
			syncBinding()
		case sig, ok := <-sigCh:
			if !ok {
				return
			}

			if haveCurrent {
				if data, ok := bluezCharacteristicValue(sig, current.NotifyPath); ok {
					if ev, ok := bs22PTTEventFromBytes(data); ok {
						if !sendPTTEvent(ctx, out, ev) {
							return
						}
						held = ev == PTTDown
					} else if pkt, ok := parseBS22HMPacket(data); ok {
						s.log.Debug().
							Uint16("command", pkt.CommandCode()).
							Bool("response", pkt.IsResponse()).
							Int("payload_len", len(pkt.Payload)).
							Msg("BS-22 BLE: ignored HM packet")
					}
				}
			}

			if bluezNeedsBS22Rescan(sig, current.NotifyPath) {
				syncBinding()
			}
		}
	}
}

// primeBLE checks the HM control channel without changing the audio route.
// BS-22 bench capture with command 15 enabled produced silence even through
// native ALSA. ReadSettings alone preserves HFP microphone audio and PTT events.
func (s *bs22EventSource) primeBLE(conn *dbus.Conn, binding bs22BLEBinding) error {
	return s.writeValue(conn, binding.WritePath, bs22HMCommandBytes(bs22HMVendorID, bs22HMReadSettings))
}

func listBlueZManagedObjects(conn *dbus.Conn) (bluezManagedObjectMap, error) {
	if conn == nil {
		return nil, errors.New("nil DBus connection")
	}

	var managed bluezManagedObjectMap
	call := boundedBlueZCall(conn, bluezRootPath, bluezManagedObjectsMethod)
	if call.Err != nil {
		return nil, call.Err
	}
	if err := call.Store(&managed); err != nil {
		return nil, err
	}

	return managed, nil
}

func startBlueZNotify(conn *dbus.Conn, path dbus.ObjectPath) error {
	if conn == nil {
		return errors.New("nil DBus connection")
	}

	return boundedBlueZCall(conn, path, bluezGattCharacteristicStartNotify).Err
}

func writeBlueZCharacteristicValue(conn *dbus.Conn, path dbus.ObjectPath, data []byte) error {
	if conn == nil {
		return errors.New("nil DBus connection")
	}

	options := map[string]dbus.Variant{}
	return boundedBlueZCall(conn, path, bluezGattCharacteristicWriteValue, data, options).Err
}

func connectBlueZDevice(conn *dbus.Conn, device bs22DeviceInfo) error {
	if conn == nil {
		return errors.New("nil DBus connection")
	}

	if device.Path == "" {
		return errors.New("empty device path")
	}

	var lastErr error

	if err := connectBlueZLEBearer(conn, device.Path); err == nil || isIgnorableBlueZConnectError(err) {
		return nil
	} else if !isBlueZUnsupportedMethodError(err) {
		lastErr = err
	}

	if err := connectBlueZAdapterDevice(conn, device); err == nil || isIgnorableBlueZConnectError(err) {
		return nil
	} else if !isBlueZUnsupportedMethodError(err) {
		lastErr = err
	}

	if lastErr != nil {
		return lastErr
	}

	return errors.New("no supported BlueZ connect method for BS-22 device")
}

func connectBlueZLEBearer(conn *dbus.Conn, path dbus.ObjectPath) error {
	if conn == nil {
		return errors.New("nil DBus connection")
	}

	if path == "" {
		return errors.New("empty device path")
	}

	return boundedBlueZCall(conn, path, bluezBearerLEConnectMethod).Err
}

func connectBlueZAdapterDevice(conn *dbus.Conn, device bs22DeviceInfo) error {
	if conn == nil {
		return errors.New("nil DBus connection")
	}

	if device.AdapterPath == "" {
		return errors.New("empty adapter path")
	}

	if device.Address == "" {
		return errors.New("empty device address")
	}

	properties := map[string]dbus.Variant{
		"Address":     dbus.MakeVariant(device.Address),
		"AddressType": dbus.MakeVariant(normalizeBlueZAddressType(device.AddressType)),
	}

	// On BlueZ 5.72, ConnectDevice uses AddressType to choose LE public or
	// random addressing. Do not fall back to Device1.Connect, which selects a
	// bearer automatically for dual-mode devices.
	return boundedBlueZCall(conn, device.AdapterPath, bluezAdapterConnectDeviceMethod, properties).Err
}

func findBS22Device(managed bluezManagedObjectMap) (bs22DeviceInfo, bool) {
	devices := make([]bs22DeviceInfo, 0)

	for path, ifaces := range managed {
		props, ok := ifaces[bluezDeviceInterface]
		if !ok {
			continue
		}

		info := bs22DeviceInfo{
			Path:        path,
			AdapterPath: variantObjectPath(props, "Adapter"),
			Alias:       variantString(props, "Alias"),
			Name:        variantString(props, "Name"),
			Address:     variantString(props, "Address"),
			AddressType: variantString(props, "AddressType"),
			Connected:   variantBool(props, "Connected"),
		}

		if !isBS22Device(info, props) {
			continue
		}

		devices = append(devices, info)
	}

	if len(devices) == 0 {
		return bs22DeviceInfo{}, false
	}

	sort.Slice(devices, func(i, j int) bool {
		if devices[i].Connected != devices[j].Connected {
			return devices[i].Connected
		}

		return string(devices[i].Path) < string(devices[j].Path)
	})

	return devices[0], true
}

func findBS22BLEBindingForDevice(managed bluezManagedObjectMap, device bs22DeviceInfo) (bs22BLEBinding, bool) {
	var servicePath dbus.ObjectPath
	servicePaths := make([]dbus.ObjectPath, 0)

	for path, ifaces := range managed {
		props, ok := ifaces[bluezGattServiceInterface]
		if !ok {
			continue
		}

		if !strings.EqualFold(variantString(props, "UUID"), bs22HMServiceUUID) {
			continue
		}

		if variantObjectPath(props, "Device") != device.Path {
			continue
		}

		servicePaths = append(servicePaths, path)
	}

	if len(servicePaths) == 0 {
		return bs22BLEBinding{}, false
	}

	sort.Slice(servicePaths, func(i, j int) bool {
		return string(servicePaths[i]) < string(servicePaths[j])
	})
	servicePath = servicePaths[0]

	var writePath dbus.ObjectPath
	var notifyPath dbus.ObjectPath
	charPaths := make([]dbus.ObjectPath, 0)
	for path, ifaces := range managed {
		props, ok := ifaces[bluezGattCharacteristicInterface]
		if !ok {
			continue
		}

		if variantObjectPath(props, "Service") != servicePath {
			continue
		}

		charPaths = append(charPaths, path)
	}

	sort.Slice(charPaths, func(i, j int) bool {
		return string(charPaths[i]) < string(charPaths[j])
	})

	for _, path := range charPaths {
		props := managed[path][bluezGattCharacteristicInterface]
		switch {
		case strings.EqualFold(variantString(props, "UUID"), bs22HMWriteUUID):
			writePath = path
		case strings.EqualFold(variantString(props, "UUID"), bs22HMNotifyUUID):
			notifyPath = path
		}
	}

	if writePath == "" || notifyPath == "" {
		return bs22BLEBinding{}, false
	}

	return bs22BLEBinding{
		Device:      device,
		ServicePath: servicePath,
		WritePath:   writePath,
		NotifyPath:  notifyPath,
	}, true
}

func isBS22Device(device bs22DeviceInfo, props map[string]dbus.Variant) bool {
	if containsFold(device.Alias, bs22Name) || containsFold(device.Name, bs22Name) {
		return true
	}

	for _, uuid := range variantStringSlice(props, "UUIDs") {
		if strings.EqualFold(uuid, bs22HMServiceUUID) {
			return true
		}
	}

	return false
}

func parseBS22HMPacket(data []byte) (bs22HMPacket, bool) {
	if len(data) < 4 {
		return bs22HMPacket{}, false
	}

	packet := bs22HMPacket{
		VendorID: binary.BigEndian.Uint16(data[0:2]),
		Command:  binary.BigEndian.Uint16(data[2:4]),
		Payload:  append([]byte(nil), data[4:]...),
	}

	return packet, true
}

func bs22PTTEventFromBytes(data []byte) (PTTEvent, bool) {
	packet, ok := parseBS22HMPacket(data)
	if !ok || packet.VendorID != bs22HMVendorID {
		return 0, false
	}

	return bs22PTTEventFromPacket(packet)
}

func bs22PTTEventFromPacket(packet bs22HMPacket) (PTTEvent, bool) {
	if packet.CommandCode() != bs22HMKeyEventInd || len(packet.Payload) < 2 {
		return 0, false
	}

	switch {
	case packet.Payload[0] == 16 && packet.Payload[1] == 16:
		return PTTDown, true
	case packet.Payload[0] == 16 && packet.Payload[1] == 17:
		return PTTUp, true
	default:
		return 0, false
	}
}

func bs22HMCommandBytes(vendorID, command uint16, payload ...byte) []byte {
	packet := make([]byte, 4+len(payload))
	binary.BigEndian.PutUint16(packet[0:2], vendorID)
	binary.BigEndian.PutUint16(packet[2:4], command)
	copy(packet[4:], payload)
	return packet
}

func nextBS22PrimeBackoff(prev time.Duration) time.Duration {
	if prev <= 0 {
		return bs22BLEPrimeRetryInitial
	}

	next := prev * 2
	if next > bs22BLEPrimeRetryMax {
		return bs22BLEPrimeRetryMax
	}

	return next
}

func bluezCharacteristicValue(sig *dbus.Signal, path dbus.ObjectPath) ([]byte, bool) {
	if sig == nil || sig.Path != path || sig.Name != bluezPropertiesChangedSignal || len(sig.Body) < 2 {
		return nil, false
	}

	iface, ok := sig.Body[0].(string)
	if !ok || iface != bluezGattCharacteristicInterface {
		return nil, false
	}

	changed, ok := sig.Body[1].(map[string]dbus.Variant)
	if !ok {
		return nil, false
	}

	value, ok := changed["Value"]
	if !ok {
		return nil, false
	}

	data, ok := value.Value().([]byte)
	if !ok {
		return nil, false
	}

	return append([]byte(nil), data...), true
}

func bluezNeedsBS22Rescan(sig *dbus.Signal, notifyPath dbus.ObjectPath) bool {
	if sig == nil {
		return false
	}

	switch sig.Name {
	case bluezInterfacesAddedSignal, bluezInterfacesRemovedSignal:
		return true
	case bluezPropertiesChangedSignal:
		if sig.Path == notifyPath {
			return false
		}

		if len(sig.Body) == 0 {
			return false
		}

		iface, ok := sig.Body[0].(string)
		if !ok {
			return false
		}

		return iface == bluezDeviceInterface || iface == bluezGattServiceInterface || iface == bluezGattCharacteristicInterface
	default:
		return false
	}
}

func isIgnorableBlueZNotifyError(err error) bool {
	if err == nil {
		return false
	}

	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "in progress") || strings.Contains(msg, "already notifying")
}

func isIgnorableBlueZConnectError(err error) bool {
	if err == nil {
		return false
	}

	switch blueZErrorName(err) {
	case "org.bluez.Error.AlreadyConnected", "org.bluez.Error.InProgress", "org.bluez.Error.AlreadyExists":
		return true
	}

	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "already connected") || strings.Contains(msg, "in progress") || strings.Contains(msg, "already exists")
}

func isBlueZUnsupportedMethodError(err error) bool {
	if err == nil {
		return false
	}

	switch blueZErrorName(err) {
	case "org.freedesktop.DBus.Error.UnknownMethod", "org.freedesktop.DBus.Error.UnknownInterface", "org.bluez.Error.NotSupported":
		return true
	}

	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unknown method") || strings.Contains(msg, "unknown interface") ||
		strings.Contains(msg, "doesn't exist") || strings.Contains(msg, "not supported")
}

func blueZErrorName(err error) string {
	if err == nil {
		return ""
	}

	var dbusErr dbus.Error
	if errors.As(err, &dbusErr) {
		return dbusErr.Name
	}
	var dbusErrPtr *dbus.Error
	if errors.As(err, &dbusErrPtr) && dbusErrPtr != nil {
		return dbusErrPtr.Name
	}
	return ""
}

func normalizeBlueZAddressType(addressType string) string {
	if strings.EqualFold(addressType, bluezAddressTypeRandom) {
		return bluezAddressTypeRandom
	}

	return bluezAddressTypePublic
}

func variantString(props map[string]dbus.Variant, key string) string {
	if props == nil {
		return ""
	}

	v, ok := props[key]
	if !ok {
		return ""
	}

	value, _ := v.Value().(string)
	return value
}

func variantBool(props map[string]dbus.Variant, key string) bool {
	if props == nil {
		return false
	}

	v, ok := props[key]
	if !ok {
		return false
	}

	value, _ := v.Value().(bool)
	return value
}

func variantObjectPath(props map[string]dbus.Variant, key string) dbus.ObjectPath {
	if props == nil {
		return ""
	}

	v, ok := props[key]
	if !ok {
		return ""
	}

	value, _ := v.Value().(dbus.ObjectPath)
	return value
}

func variantStringSlice(props map[string]dbus.Variant, key string) []string {
	if props == nil {
		return nil
	}

	v, ok := props[key]
	if !ok {
		return nil
	}

	value, _ := v.Value().([]string)
	return value
}

func containsFold(s, substr string) bool {
	if substr == "" {
		return true
	}

	return strings.Contains(strings.ToUpper(s), strings.ToUpper(substr))
}

// Bound legacy transport helpers as well as lifecycle operations; shutdown must
// never wait indefinitely for a controller or BlueZ reply.
func boundedBlueZCall(conn *dbus.Conn, path dbus.ObjectPath, method string, args ...interface{}) *dbus.Call {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return conn.Object(bluezService, path).CallWithContext(ctx, method, 0, args...)
}

func isBlueZDisconnectedError(err error) bool {
	if err == nil {
		return false
	}
	if blueZErrorName(err) == "org.bluez.Error.NotConnected" {
		return true
	}
	// BlueZ 5.72 sometimes uses Error.Failed with this exact body.
	return strings.EqualFold(strings.TrimSpace(err.Error()), "Not connected")
}

func (s *bs22EventSource) ChannelEvents() <-chan int { return s.channelSteps }

func (s *bs22EventSource) SpeakerVolumes() <-chan int { return s.speakerVolumes }
