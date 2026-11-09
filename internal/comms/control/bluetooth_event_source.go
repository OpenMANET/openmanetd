package control

import (
	"context"
	"errors"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/godbus/dbus/v5"
	"github.com/rs/zerolog"
	"golang.org/x/sys/unix"
)

const (
	bluezPropertiesChangedRule      = "type='signal',interface='org.freedesktop.DBus.Properties',sender='org.bluez'"
	bluezPropertiesChangedSignal    = "org.freedesktop.DBus.Properties.PropertiesChanged"
	bluealsaService                 = "org.bluealsa"
	bluealsaPathPrefix              = "/org/bluealsa"
	bluealsaManagerPath             = dbus.ObjectPath("/org/bluealsa")
	bluealsaManagedObjectsMethod    = "org.freedesktop.DBus.ObjectManager.GetManagedObjects"
	bluealsaInterfacesAddedRule     = "type='signal',interface='org.freedesktop.DBus.ObjectManager',path='/org/bluealsa',member='InterfacesAdded'"
	bluealsaInterfacesRemovedRule   = "type='signal',interface='org.freedesktop.DBus.ObjectManager',path='/org/bluealsa',member='InterfacesRemoved'"
	bluealsaInterfacesAddedSignal   = "org.freedesktop.DBus.ObjectManager.InterfacesAdded"
	bluealsaInterfacesRemovedSignal = "org.freedesktop.DBus.ObjectManager.InterfacesRemoved"
	bluealsaRFCOMMInterface         = "org.bluealsa.RFCOMM1"
	bluealsaRFCOMMOpenMethod        = bluealsaRFCOMMInterface + ".Open"
	bluezDeviceInterface            = "org.bluez.Device1"
	bluezHFPUUID                    = "0000111e-0000-1000-8000-00805f9b34fb"
	bluealsaJournalMarker           = "AT message: SET: command:+XEVENT, value:"
	bluealsaRFCOMMRescanInterval    = 2 * time.Second
)

type systemBusDialer func() (*dbus.Conn, error)
type rfcommPathLister func(*dbus.Conn) ([]dbus.ObjectPath, error)
type rfcommOpener func(context.Context, *dbus.Conn, dbus.ObjectPath) (io.ReadCloser, error)

type blueALSAXEventSource struct {
	log            zerolog.Logger
	dial           systemBusDialer
	listRFCOMM     rfcommPathLister
	openRFCOMM     rfcommOpener
	rescanInterval time.Duration
	dedupeMu       sync.Mutex
	lastEventName  string
	lastEventAt    time.Time
}

type blueALSARFCOMMMonitor struct {
	cancel context.CancelFunc
}

// NewBlueALSAXEventSource monitors BlueALSA DBus messages and emits PTT press/release events.
func NewBlueALSAXEventSource(log zerolog.Logger) EventSource {
	return &blueALSAXEventSource{
		log: log,
		dial: func() (*dbus.Conn, error) {
			return dbus.ConnectSystemBus()
		},
		listRFCOMM: listBlueALSARFCOMMPaths,
		openRFCOMM: openBlueALSARFCOMM,
	}
}

func (s *blueALSAXEventSource) Events(ctx context.Context) <-chan PTTEvent {
	ch := make(chan PTTEvent, 4)

	go func() {
		defer close(ch)

		if s.dial == nil {
			return
		}

		conn, err := s.dial()
		if err != nil {
			s.log.Error().Err(err).Msg("BlueALSA: failed to connect to system DBus")
			return
		}
		defer conn.Close()

		if err := addDBusMatch(conn, bluealsaInterfacesAddedRule); err != nil {
			s.log.Error().Err(err).Msg("BlueALSA: failed to add InterfacesAdded match rule")
			return
		}

		if err := addDBusMatch(conn, bluealsaInterfacesRemovedRule); err != nil {
			s.log.Error().Err(err).Msg("BlueALSA: failed to add InterfacesRemoved match rule")
			return
		}

		if s.listRFCOMM == nil || s.openRFCOMM == nil {
			s.log.Error().Msg("BlueALSA: RFCOMM monitor is not configured")
			return
		}

		rescanInterval := s.rescanInterval
		if rescanInterval <= 0 {
			rescanInterval = bluealsaRFCOMMRescanInterval
		}
		rescanTicker := time.NewTicker(rescanInterval)
		defer rescanTicker.Stop()

		sigCh := make(chan *dbus.Signal, 16)
		conn.Signal(sigCh)
		defer conn.RemoveSignal(sigCh)

		s.log.Info().Msg("Starting BlueALSA XEVENT monitor via RFCOMM1.Open()")

		active := make(map[dbus.ObjectPath]*blueALSARFCOMMMonitor)
		var activeMu sync.Mutex
		var monitors sync.WaitGroup

		startMonitor := func(path dbus.ObjectPath) {
			activeMu.Lock()
			if _, exists := active[path]; exists {
				activeMu.Unlock()
				return
			}
			monitorCtx, cancel := context.WithCancel(ctx)
			monitor := &blueALSARFCOMMMonitor{cancel: cancel}
			active[path] = monitor
			activeMu.Unlock()

			monitors.Go(func() {
				defer func() {
					activeMu.Lock()
					if current, exists := active[path]; exists && current == monitor {
						delete(active, path)
					}
					activeMu.Unlock()
					cancel()
				}()

				s.monitorRFCOMM(ctx, monitorCtx, conn, path, ch)
			})
		}

		stopMonitor := func(path dbus.ObjectPath) {
			activeMu.Lock()
			monitor, exists := active[path]
			activeMu.Unlock()

			if exists {
				monitor.cancel()
			}
		}

		stopAll := func() {
			activeMu.Lock()
			cancels := make([]context.CancelFunc, 0, len(active))
			for path, monitor := range active {
				cancels = append(cancels, monitor.cancel)
				delete(active, path)
			}
			activeMu.Unlock()

			for _, cancel := range cancels {
				cancel()
			}
		}
		defer monitors.Wait()
		defer stopAll()

		s.syncRFCOMMPaths(conn, startMonitor)

		for {
			select {
			case <-ctx.Done():
				return
			case <-rescanTicker.C:
				s.syncRFCOMMPaths(conn, startMonitor)
			case sig, ok := <-sigCh:
				if !ok {
					return
				}

				if path, ok := blueALSAInterfacesAddedRFCOMMPath(sig); ok {
					startMonitor(path)
				}
				if path, ok := blueALSAInterfacesRemovedRFCOMMPath(sig); ok {
					stopMonitor(path)
				}
			}
		}
	}()

	return ch
}

func (s *blueALSAXEventSource) syncRFCOMMPaths(conn *dbus.Conn, startMonitor func(dbus.ObjectPath)) {
	if s.listRFCOMM == nil {
		return
	}

	paths, err := s.listRFCOMM(conn)
	if err != nil {
		s.log.Error().Err(err).Msg("BlueALSA: failed to enumerate RFCOMM objects")
		return
	}

	for _, path := range paths {
		startMonitor(path)
	}
}

func (s *blueALSAXEventSource) monitorRFCOMM(
	ctx context.Context,
	monitorCtx context.Context,
	conn *dbus.Conn,
	path dbus.ObjectPath,
	out chan<- PTTEvent,
) {
	reader, err := s.openRFCOMM(monitorCtx, conn, path)
	if err != nil {
		s.log.Debug().Err(err).Str("path", string(path)).Msg("BlueALSA: failed to open RFCOMM channel")
		return
	}
	defer reader.Close()

	s.log.Info().Str("path", string(path)).Msg("BlueALSA: monitoring RFCOMM channel")

	done := make(chan struct{})
	go func() {
		select {
		case <-monitorCtx.Done():
			_ = reader.Close()
		case <-done:
		}
	}()
	defer close(done)

	if s.consumeRFCOMM(monitorCtx, out, reader) {
		// The connection can disappear without sending the button release.
		s.emitEventName(ctx, out, "PTT_UP")
	}
}

func (s *blueALSAXEventSource) emitEventName(ctx context.Context, out chan<- PTTEvent, eventName string) bool {
	if s.shouldSuppressDuplicate(eventName) {
		return true
	}

	s.log.Debug().Msgf("BlueALSA XEVENT: %s", eventName)

	var ev PTTEvent

	switch eventName {
	case "PTT_DOWN":
		ev = PTTDown
	case "PTT_UP":
		ev = PTTUp
	default:
		s.log.Debug().Msgf("Ignoring unsupported BlueALSA XEVENT: %s", eventName)

		return true
	}

	return sendPTTEvent(ctx, out, ev)
}

func (s *blueALSAXEventSource) shouldSuppressDuplicate(eventName string) bool {
	if eventName == "" {
		return false
	}

	s.dedupeMu.Lock()
	defer s.dedupeMu.Unlock()

	now := time.Now()
	if s.lastEventName == eventName && now.Sub(s.lastEventAt) < 200*time.Millisecond {
		return true
	}

	s.lastEventName = eventName
	s.lastEventAt = now
	return false
}

func (s *blueALSAXEventSource) consumeRFCOMM(ctx context.Context, out chan<- PTTEvent, r io.Reader) bool {
	buf := make([]byte, 4096)
	var held bool

	for {
		n, err := r.Read(buf)
		if ctx.Err() != nil {
			return held
		}
		if n > 0 {
			for _, eventName := range blueALSAEventNames(string(buf[:n])) {
				if !s.emitEventName(ctx, out, eventName) {
					return held
				}
				switch eventName {
				case "PTT_DOWN":
					held = true
				case "PTT_UP":
					held = false
				}
			}
		}

		if err != nil {
			if ctx.Err() != nil || errors.Is(err, io.EOF) || errors.Is(err, os.ErrClosed) {
				return held
			}

			s.log.Debug().Err(err).Msg("BlueALSA: RFCOMM read failed")
			return held
		}
	}
}

func addDBusMatch(conn *dbus.Conn, rule string) error {
	return conn.BusObject().Call("org.freedesktop.DBus.AddMatch", 0, rule).Err
}

func listBlueALSARFCOMMPaths(conn *dbus.Conn) ([]dbus.ObjectPath, error) {
	if conn == nil {
		return nil, errors.New("nil DBus connection")
	}

	var managed map[dbus.ObjectPath]map[string]map[string]dbus.Variant
	call := conn.Object(bluealsaService, bluealsaManagerPath).Call(bluealsaManagedObjectsMethod, 0)
	if call.Err != nil {
		return nil, call.Err
	}
	if err := call.Store(&managed); err != nil {
		return nil, err
	}

	return blueALSARFCOMMPaths(managed), nil
}

func openBlueALSARFCOMM(ctx context.Context, conn *dbus.Conn, path dbus.ObjectPath) (io.ReadCloser, error) {
	if conn == nil {
		return nil, errors.New("nil DBus connection")
	}

	var fd dbus.UnixFD
	call := conn.Object(bluealsaService, path).CallWithContext(ctx, bluealsaRFCOMMOpenMethod, 0)
	if call.Err != nil {
		return nil, call.Err
	}
	if err := call.Store(&fd); err != nil {
		return nil, err
	}

	// Register the descriptor with Go's poller so Close interrupts a pending Read.
	if err := unix.SetNonblock(int(fd), true); err != nil {
		_ = unix.Close(int(fd))
		return nil, err
	}
	file := os.NewFile(uintptr(fd), string(path))
	if file == nil {
		return nil, errors.New("RFCOMM1.Open returned invalid file descriptor")
	}

	return file, nil
}

func blueALSARFCOMMPaths(
	managed map[dbus.ObjectPath]map[string]map[string]dbus.Variant,
) []dbus.ObjectPath {
	paths := make([]dbus.ObjectPath, 0)

	for path, ifaces := range managed {
		if _, ok := ifaces[bluealsaRFCOMMInterface]; ok {
			paths = append(paths, path)
		}
	}

	sort.Slice(paths, func(i, j int) bool {
		return string(paths[i]) < string(paths[j])
	})

	return paths
}

func blueALSAInterfacesAddedRFCOMMPath(sig *dbus.Signal) (dbus.ObjectPath, bool) {
	if sig == nil || sig.Name != bluealsaInterfacesAddedSignal || len(sig.Body) < 2 {
		return "", false
	}

	path, ok := sig.Body[0].(dbus.ObjectPath)
	if !ok {
		return "", false
	}

	ifaces, ok := sig.Body[1].(map[string]map[string]dbus.Variant)
	if !ok {
		return "", false
	}

	_, ok = ifaces[bluealsaRFCOMMInterface]
	return path, ok
}

func blueALSAInterfacesRemovedRFCOMMPath(sig *dbus.Signal) (dbus.ObjectPath, bool) {
	if sig == nil || sig.Name != bluealsaInterfacesRemovedSignal || len(sig.Body) < 2 {
		return "", false
	}

	path, ok := sig.Body[0].(dbus.ObjectPath)
	if !ok {
		return "", false
	}

	ifaces, ok := sig.Body[1].([]string)
	if !ok {
		return "", false
	}

	for _, iface := range ifaces {
		if iface == bluealsaRFCOMMInterface {
			return path, true
		}
	}

	return "", false
}

func blueALSAEventName(msg string) (string, bool) {
	const marker = "+XEVENT"

	idx := indexASCIIFold(msg, marker)
	if idx == -1 {
		return "", false
	}

	remainder := strings.TrimSpace(msg[idx+len(marker):])
	parts := strings.FieldsFunc(remainder, func(r rune) bool {
		return r == ',' || r == ':' || r == '=' || r == ' '
	})

	if len(parts) == 0 {
		return "", false
	}

	eventName := normalizeBlueALSAEventToken(parts[0])
	if eventName == "" {
		return "", false
	}

	return eventName, true
}

func blueALSAEventNames(packet string) []string {
	packet = strings.TrimSpace(packet)
	if packet == "" {
		return nil
	}

	parts := strings.FieldsFunc(packet, func(r rune) bool {
		return r == '\r' || r == '\n'
	})
	if len(parts) == 0 {
		parts = []string{packet}
	}

	events := make([]string, 0, len(parts))
	for _, part := range parts {
		if eventName, ok := blueALSAEventName(strings.TrimSpace(part)); ok {
			events = append(events, eventName)
		}
	}

	return events
}

func blueALSAJournalEventName(line string) (string, bool) {
	idx := strings.Index(line, bluealsaJournalMarker)
	if idx == -1 {
		return "", false
	}

	raw := normalizeBlueALSAEventToken(line[idx+len(bluealsaJournalMarker):])
	if raw == "" {
		return "", false
	}

	return raw, true
}

func normalizeBlueALSAEventToken(token string) string {
	token = strings.ToUpper(strings.TrimSpace(token))
	token = strings.TrimFunc(token, func(r rune) bool {
		return !(unicode.IsDigit(r) || unicode.IsLetter(r) || r == '_')
	})
	return token
}

func indexASCIIFold(s, substr string) int {
	if len(substr) == 0 {
		return 0
	}
	if len(substr) > len(s) {
		return -1
	}

	for i := 0; i <= len(s)-len(substr); i++ {
		match := true
		for j := 0; j < len(substr); j++ {
			sb := s[i+j]
			tb := substr[j]
			if 'a' <= sb && sb <= 'z' {
				sb -= 'a' - 'A'
			}
			if 'a' <= tb && tb <= 'z' {
				tb -= 'a' - 'A'
			}
			if sb != tb {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}

	return -1
}

func sendPTTEvent(ctx context.Context, out chan<- PTTEvent, ev PTTEvent) bool {
	select {
	case out <- ev:
		return true
	case <-ctx.Done():
		return false
	}
}

func logBlueZSignal(log zerolog.Logger, sig *dbus.Signal) {
	if sig == nil || sig.Name != bluezPropertiesChangedSignal || len(sig.Body) < 2 {
		return
	}

	iface, ok := sig.Body[0].(string)
	if !ok || iface != bluezDeviceInterface {
		return
	}

	changed, ok := sig.Body[1].(map[string]dbus.Variant)
	if !ok {
		return
	}

	if v, exists := changed["Connected"]; exists {
		if connected, ok := v.Value().(bool); ok {
			log.Info().Msgf("Bluetooth device connection event: connected=%v", connected)
		}
	}

	if v, exists := changed["UUIDs"]; exists {
		uuids, ok := v.Value().([]string)
		if !ok {
			return
		}

		for _, uuid := range uuids {
			if strings.EqualFold(uuid, bluezHFPUUID) {
				log.Info().Msg("Detected HFP device UUID; BlueALSA XEVENT fallback remains active")

				return
			}
		}
	}
}
