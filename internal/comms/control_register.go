package comms

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/openmanet/openmanetd/internal/comms/control"
)

// This file wires the in-package control source constructors into the
// control.Registry. It lives in the comms package (not internal/comms/control)
// because the constructors themselves still live here in Phase 2 of the comms
// refactor — moving them across would create an import cycle since control is
// already imported by comms via event_alias.go. A future phase will relocate
// the backends into internal/comms/control and these inits will move with
// them.

// openvlmBackend is the ControlDeps.Backend payload for the OpenVLM HID source.
type openvlmBackend struct{}
type bs22Backend struct {
	path      dbus.ObjectPath
	readiness control.BS22Readiness
}

// roipBackend is the ControlDeps.Backend payload for the ROIP bridge source.
// Under the unified capture design the ROIP VOX path subscribes to the
// always-on broadcast capture stream via SetTap/ClearTap; there is no
// separate input device to resolve.
type roipBackend struct {
	IsReceiving    func() bool
	IsBroadcasting func() bool
	SetTap         func(chan []float32)
	ClearTap       func()
	VOXHoldTime    time.Duration
	MaxTXDuration  time.Duration
	VOXThreshold   float32
	COSGPIOMask    byte
}

// webBackend is the ControlDeps.Backend payload for the web RPC source. The
// factory writes the constructed *control.WebEventSource back into Sink so the rest
// of the comms runtime can find it.
type webBackend struct {
	Sink func(*control.WebEventSource)
}

// nanopttBackend is the ControlDeps.Backend payload for the evdev NanoPTT
// source.
type nanopttBackend struct {
	Cfg *CommsConfig
}

func init() {
	control.Register(controlSourceBS22, func(deps control.ControlDeps) (control.EventSource, error) {
		backend, ok := deps.Backend.(*bs22Backend)
		if !ok {
			return nil, errors.New("BS-22 requires a selected accessory")
		}
		return control.NewSelectedBS22EventSource(deps.Log, backend.path, backend.readiness)
	})

	control.Register(controlSourceBlueALSAXEvent, func(deps control.ControlDeps) (control.EventSource, error) {
		return control.NewBlueALSAXEventSource(deps.Log), nil
	})

	control.Register("openvlm", func(deps control.ControlDeps) (control.EventSource, error) {
		return control.NewOpenVLMSource(deps.Log), nil
	})

	control.Register(controlSourceROIP, func(deps control.ControlDeps) (control.EventSource, error) {
		b, ok := deps.Backend.(*roipBackend)
		if !ok || b == nil {
			return nil, errors.New("comms: roip control source missing backend deps")
		}

		return control.NewROIPSource(
			deps.Log,
			b.COSGPIOMask,
			b.VOXThreshold,
			b.VOXHoldTime,
			b.MaxTXDuration,
			b.IsReceiving,
			b.IsBroadcasting,
			b.SetTap,
			b.ClearTap,
		), nil
	})

	control.Register(controlSourceWeb, func(deps control.ControlDeps) (control.EventSource, error) {
		b, ok := deps.Backend.(*webBackend)
		if !ok || b == nil {
			return nil, errors.New("comms: web control source missing backend deps")
		}

		ws := control.NewWebEventSource(deps.Log)
		if b.Sink != nil {
			b.Sink(ws)
		}

		return ws, nil
	})

	control.Register(defaultControlSourceNanoPTT, func(deps control.ControlDeps) (control.EventSource, error) {
		b, ok := deps.Backend.(*nanopttBackend)
		if !ok || b == nil {
			return nil, errors.New("comms: nanoptt control source missing backend deps")
		}

		dev := b.Cfg.findCommDevice()
		if dev == nil {
			return nil, errors.New("comms: PTT device not found")
		}

		deps.Log.Info().Msgf("comms: PTT on evdev device: %s", dev.Name)

		return control.NewNanoPTTSource(dev, b.Cfg.CommKey, deps.Log), nil
	})
}

// controlLookup is a thin wrapper around control.Lookup so the comms package
// does not have to import the registry name in multiple files.
func controlLookup(name string) (control.Factory, bool) {
	return control.Lookup(name)
}

// buildControlDeps assembles the ControlDeps payload for the configured
// ControlSource. Returns an error when the source name is unknown to this
// helper. Validate() catches unknown sources earlier; the default branch
// here is a defense-in-depth check for the buildEventSource caller.
func (cfg *CommsConfig) buildControlDeps(rt *CommsRuntime) (control.ControlDeps, error) {
	deps := control.ControlDeps{Log: cfg.Log}

	switch cfg.ControlSource {
	case defaultCtrlSrc:
		deps.Backend = &openvlmBackend{}
	case controlSourceBS22:
		deps.Backend = &bs22Backend{path: dbus.ObjectPath(cfg.BluetoothDevicePath), readiness: cfg.BluetoothSession}
	case controlSourceBlueALSAXEvent:
		// Bluetooth event sources connect to BlueZ and BlueALSA directly.
	case controlSourceROIP:
		deps.Backend = &roipBackend{
			COSGPIOMask:    cfg.ROIPCOSGPIOMask,
			VOXThreshold:   cfg.ROIPVOXThreshold,
			VOXHoldTime:    cfg.ROIPVOXHoldTime,
			MaxTXDuration:  cfg.ROIPMaxTXDuration,
			IsReceiving:    func() bool { return cfg.isReceivingRemote(rt) },
			IsBroadcasting: func() bool { return rt.Broadcasting.Load() },
			SetTap:         func(ch chan []float32) { rt.BroadcastTap.Store(&ch) },
			ClearTap:       func() { rt.BroadcastTap.Store(nil) },
		}
	case controlSourceWeb:
		deps.Backend = &webBackend{
			Sink: func(ws *control.WebEventSource) { rt.WebEvtSrc = ws },
		}
	case defaultControlSourceNanoPTT:
		deps.Backend = &nanopttBackend{Cfg: cfg}
	default:
		return deps, fmt.Errorf("comms: unknown ControlSource %q", cfg.ControlSource)
	}

	return deps, nil
}

// Validate checks the comms configuration for self-consistency. Phase 2 of
// the comms refactor introduced this method specifically to verify that the
// configured ControlSource maps to a registered backend. Additional checks
// will be added by later phases. The method intentionally tolerates the
// empty string (treated as the openvlm default) so callers may invoke it
// either before or after normalizeControlSource has run.
func (cfg *CommsConfig) Validate() error {
	name := strings.ToLower(strings.TrimSpace(cfg.ControlSource))
	if name == "" {
		name = defaultCtrlSrc
	}
	if _, ok := control.Lookup(name); !ok {
		return errors.New("comms: unknown ControlSource " + name)
	}

	return nil
}
