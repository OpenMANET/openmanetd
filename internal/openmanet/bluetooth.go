package openmanet

import (
	"context"
	"path/filepath"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/openmanet/openmanetd/internal/bluetooth"
	"github.com/openmanet/openmanetd/internal/comms"
	"github.com/rs/zerolog"
)

func startBluetooth(ctx context.Context, runtime *comms.CommsManager, configPath string, log zerolog.Logger) (*bluetooth.Manager, func()) {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		log.Warn().Err(err).Msg("Bluetooth system bus unavailable")
		return nil, func() {}
	}
	store := bluetooth.FileStore{Path: filepath.Join(filepath.Dir(configPath), "bluetooth.json")}
	manager := bluetooth.NewManager(bluetooth.NewBlueZ(conn), runtime, store)
	if err := manager.Initialize(ctx); err != nil {
		log.Warn().Err(err).Msg("Bluetooth startup remains disabled")
	}
	monitorCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-monitorCtx.Done():
				return
			case <-ticker.C:
				if err := manager.Reconcile(monitorCtx); err != nil {
					log.Debug().Err(err).Msg("Bluetooth controller reconciliation pending")
				}
			}
		}
	}()
	return manager, func() {
		cancel()
		<-done
		cleanup, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		if err := manager.Shutdown(cleanup); err != nil {
			log.Error().Err(err).Msg("Bluetooth shutdown failed")
		}
		if err := conn.Close(); err != nil {
			log.Debug().Err(err).Msg("Bluetooth bus close")
		}
	}
}
