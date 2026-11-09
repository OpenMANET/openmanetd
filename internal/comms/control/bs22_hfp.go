package control

import (
	"context"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

const bs22HandsFreeUUID = "0000111e-0000-1000-8000-00805f9b34fb"

// monitorHFP owns a separate bus connection so profile negotiation cannot
// stall BLE button processing. Every bus operation is bounded by cancellation.
func (s *bs22EventSource) monitorHFP(ctx context.Context) {
	conn, err := s.dial()
	if err != nil {
		s.log.Error().Err(err).Msg("BS-22 HFP: system bus unavailable")
		return
	}
	defer conn.Close()
	ticker := time.NewTicker(2 * time.Second)
	var lastConnect time.Time
	if s.readiness != nil {
		defer s.readiness.ObserveHFPReady(false)
	}
	defer ticker.Stop()
	for {
		s.ensureHFP(ctx, conn, &lastConnect)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *bs22EventSource) ensureHFP(ctx context.Context, conn *dbus.Conn, lastConnect *time.Time) {
	ready := false
	defer func() {
		if s.readiness != nil {
			s.readiness.ObserveHFPReady(ready)
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var objects bluezManagedObjectMap
	if err := conn.Object(bluezService, bluezRootPath).CallWithContext(ctx, bluezManagedObjectsMethod, 0).Store(&objects); err != nil {
		return
	}
	dev, ok := s.findDevice(objects)
	if !ok || !variantBool(objects[dev.Path][bluezDeviceInterface], "Paired") {
		return
	}
	var pcms bluezManagedObjectMap
	if err := conn.Object(bluealsaService, bluealsaManagerPath).CallWithContext(ctx, bluealsaManagedObjectsMethod, 0).Store(&pcms); err != nil {
		return
	}
	ready = hasRunningBS22HFPAudio(pcms, dev.Path)
	if hasBS22HFPPCM(pcms, dev.Path) {
		return
	}
	if time.Since(*lastConnect) < 15*time.Second {
		return
	}
	*lastConnect = time.Now()
	err := conn.Object(bluezService, dev.Path).CallWithContext(ctx, "org.bluez.Device1.ConnectProfile", 0, bs22HandsFreeUUID).Err
	if err != nil {
		s.log.Debug().Err(err).Str("device", dev.Address).Msg("BS-22 HFP: connection pending; will retry")
		return
	}
	s.log.Info().Str("device", dev.Address).Msg("BS-22 HFP: Hands-Free profile connected")
}

func hasBS22HFPPCM(objects bluezManagedObjectMap, device dbus.ObjectPath) bool {
	for _, interfaces := range objects {
		props, ok := interfaces["org.bluealsa.PCM1"]
		if ok && variantObjectPath(props, "Device") == device && strings.HasPrefix(variantString(props, "Transport"), "HFP") {
			return true
		}
	}
	return false
}

func hasRunningBS22HFPAudio(objects bluezManagedObjectMap, device dbus.ObjectPath) bool {
	var source, sink bool
	for _, interfaces := range objects {
		props, ok := interfaces["org.bluealsa.PCM1"]
		if !ok || variantObjectPath(props, "Device") != device || !strings.HasPrefix(variantString(props, "Transport"), "HFP") || !variantBool(props, "Running") {
			continue
		}
		switch variantString(props, "Mode") {
		case "source":
			source = true
		case "sink":
			sink = true
		}
	}
	return source && sink
}
