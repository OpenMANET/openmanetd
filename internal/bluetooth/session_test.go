package bluetooth_test

import (
	"github.com/stretchr/testify/require"
	"sync"
	"testing"

	"github.com/openmanet/openmanetd/internal/bluetooth"
)

func TestSetupRequiresExplicitActivation(t *testing.T) {
	s := bluetooth.NewSession(true, false)
	if s.Snapshot().Enabled || s.CanTransmit() {
		t.Fatal("boot enabled Bluetooth")
	}
	g, err := s.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Activate(); err != bluetooth.ErrNotReady {
		t.Fatalf("activation before readiness: %v", err)
	}
	if !s.ObserveReady(g, true) {
		t.Fatal("ready observation rejected")
	}
	if s.CanTransmit() {
		t.Fatal("setup can transmit mesh voice")
	}
	if err = s.Activate(); err != nil {
		t.Fatal(err)
	}
	if !s.CanTransmit() {
		t.Fatal("explicit activation failed")
	}
	s.ObserveReady(g, false)
	if s.CanTransmit() {
		t.Fatal("link loss left transmit enabled")
	}
	s.ObserveReady(g, true)
	if !s.CanTransmit() {
		t.Fatal("authorized reconnect lost activation")
	}
	s.Disconnect()
	if s.CanTransmit() || s.Snapshot().ConnectAllowed {
		t.Fatal("disconnect failed to revoke")
	}
	if s.ObserveReady(g, true) {
		t.Fatal("stale reconnect was accepted")
	}
}

func TestHardwareAndBootConsent(t *testing.T) {
	for _, optIn := range []bool{false, true} {
		s := bluetooth.NewSession(false, optIn)
		if _, err := s.Begin(); err != bluetooth.ErrUnavailable {
			t.Fatal(err)
		}
		s.HardwareChanged(true)
		if s.Snapshot().Enabled {
			t.Fatal("hotplug powered on Bluetooth")
		}
	}
	s := bluetooth.NewSession(true, true)
	if !s.Snapshot().ConnectAllowed || s.CanTransmit() {
		t.Fatal("invalid boot opt-in readiness")
	}
	s.Off()
	if s.Snapshot().Enabled || s.Snapshot().ConnectAllowed {
		t.Fatal("off left radio authorized")
	}
	s.SetAutoEnable(true)
	if s.Snapshot().Enabled {
		t.Fatal("preference enabled current radio")
	}
}

func TestConcurrentRevocationAndObservations(t *testing.T) {
	s := bluetooth.NewSession(true, false)
	g, err := s.Begin()
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range 1000 {
			s.ObserveReady(g, true)
			s.CanTransmit()
		}
	}()
	go func() {
		defer wg.Done()
		for range 1000 {
			s.Snapshot()
			s.SetAutoEnable(false)
		}
	}()
	s.Off()
	wg.Wait()
	if s.Snapshot().Ready || s.CanTransmit() {
		t.Fatal("late worker revived disabled session")
	}
}

func TestLeaseRejectsOldSessionCallbacks(t *testing.T) {
	s := bluetooth.NewSession(true, false)
	_, err := s.Begin()
	require.NoError(t, err)
	old := s.Lease()
	old.ObservePTT(true)
	require.True(t, s.Snapshot().ButtonPressed)
	s.Off()
	generation, err := s.Begin()
	require.NoError(t, err)
	require.True(t, s.ObserveReady(generation, true))
	require.NoError(t, s.Activate())
	current := s.Lease()
	require.True(t, current.CanTransmit())
	require.False(t, old.CanTransmit())
	require.False(t, old.ConnectionAllowed())
	old.ObservePTT(true)
	require.False(t, s.Snapshot().ButtonPressed)
}

func TestReadyRequiresBothAudioAndButtons(t *testing.T) {
	s := bluetooth.NewSession(true, false)
	_, err := s.Begin()
	require.NoError(t, err)
	lease := s.Lease()
	lease.ObserveAudioReady(true)
	lease.ObserveButtonsReady(true)
	require.ErrorIs(t, s.Activate(), bluetooth.ErrNotReady)
	lease.ObserveHFPReady(true)
	require.NoError(t, s.Activate())
	require.True(t, lease.CanTransmit())
	lease.ObserveHFPReady(false)
	require.False(t, lease.CanTransmit())
	s.Off()
	lease.ObserveHFPReady(true)
	require.False(t, s.Snapshot().Ready)
}
