// Package bluetooth owns the authorization policy for Bluetooth setup and use.
package bluetooth

import (
	"errors"
	"sync"
)

var (
	ErrUnavailable = errors.New("Bluetooth controller unavailable")
	ErrDisabled    = errors.New("Bluetooth session is disabled")
	ErrNotReady    = errors.New("Bluetooth microphone is not ready")
)

// State is a copy of session policy, not a claim about radio hardware state.
// Hardware workers must apply the policy and report observed readiness.
type State struct {
	Generation     uint64
	Available      bool
	Enabled        bool
	ConnectAllowed bool
	Active         bool
	Ready          bool
	AutoEnable     bool
	ButtonPressed  bool
	AudioReady     bool
	ButtonsReady   bool
	HFPReady       bool
}

// Session separates consent from hardware observations. Generation invalidates
// in-flight scans and connection results after disconnect, off, or removal.
// It performs no I/O; callers must cancel/join workers and terminate TX before
// reporting completion of a hardware shutdown.
type Session struct {
	mu    sync.Mutex // protects state
	state State
}

// NewSession starts disabled unless the user previously opted into boot enable.
// A saved bond is deliberately not an input to this decision.
func NewSession(available, autoEnable bool) *Session {
	return &Session{state: State{Available: available, AutoEnable: autoEnable,
		Enabled: available && autoEnable, ConnectAllowed: available && autoEnable,
		Active: available && autoEnable}}
}

func (s *Session) Snapshot() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// Begin authorizes setup or an explicit connection, but never mesh transmission.
func (s *Session) Begin() (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.state.Available {
		return s.state.Generation, ErrUnavailable
	}
	s.state.Generation++
	s.state.Enabled = true
	s.state.ConnectAllowed = true
	s.state.Active = false
	s.state.Ready = false
	s.state.AudioReady = false
	s.state.ButtonsReady = false
	s.state.HFPReady = false
	s.state.ButtonPressed = false
	return s.state.Generation, nil
}

// ObserveReady accepts only observations belonging to the current session.
// ready requires verified audio streams and button monitoring, not just a bond.
func (s *Session) ObserveReady(generation uint64, ready bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if generation != s.state.Generation || !s.state.ConnectAllowed {
		return false
	}
	s.state.Ready = ready
	s.state.AudioReady = ready
	s.state.ButtonsReady = ready
	s.state.HFPReady = ready
	return true
}

// Activate is the explicit "Use this microphone" action following setup.
func (s *Session) Activate() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.state.Enabled || !s.state.ConnectAllowed {
		return ErrDisabled
	}
	if !s.state.Ready {
		return ErrNotReady
	}
	s.state.Active = true
	return nil
}

func (s *Session) CanTransmit() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.Available && s.state.Enabled && s.state.ConnectAllowed && s.state.Active && s.state.Ready
}

// Disconnect revokes connection and TX permission but leaves controller power
// authorized. Off additionally revokes power authorization. Neither forgets a bond.
func (s *Session) Disconnect() { s.revoke(false) }
func (s *Session) Off()        { s.revoke(true) }
func (s *Session) revoke(powerOff bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Generation++
	s.state.ConnectAllowed = false
	s.state.Active = false
	s.state.Ready = false
	s.state.AudioReady = false
	s.state.ButtonsReady = false
	s.state.HFPReady = false
	s.state.ButtonPressed = false
	if powerOff {
		s.state.Enabled = false
	}
}

// HardwareChanged never enables a newly inserted controller. Removal revokes
// the current session even if automatic enable at boot was configured.
func (s *Session) HardwareChanged(available bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Available == available {
		return
	}
	s.state.Generation++
	s.state.Available = available
	s.state.Enabled = false
	s.state.ConnectAllowed = false
	s.state.Active = false
	s.state.Ready = false
	s.state.AudioReady = false
	s.state.ButtonsReady = false
	s.state.HFPReady = false
	s.state.ButtonPressed = false
}

// SetAutoEnable changes the next-boot preference, not current authorization.
func (s *Session) SetAutoEnable(enabled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.AutoEnable = enabled
}

// Lease binds runtime callbacks to one setup generation. Late events from a
// cancelled runtime cannot change status or transmit in a replacement session.
type Lease struct {
	session    *Session
	generation uint64
}

func (s *Session) Lease() *Lease {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &Lease{session: s, generation: s.state.Generation}
}

func (l *Lease) ConnectionAllowed() bool {
	state := l.session.Snapshot()
	return state.Generation == l.generation && state.Available && state.Enabled && state.ConnectAllowed
}
func (l *Lease) CanTransmit() bool {
	state := l.session.Snapshot()
	return state.Generation == l.generation && state.Available && state.Enabled && state.ConnectAllowed && state.Active && state.Ready
}
func (l *Lease) ObservePTT(pressed bool) {
	s := l.session
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Generation == l.generation && s.state.ConnectAllowed {
		s.state.ButtonPressed = pressed
	}
}

func (l *Lease) ObserveAudioReady(ready bool)   { l.observeComponent("audio", ready) }
func (l *Lease) ObserveButtonsReady(ready bool) { l.observeComponent("buttons", ready) }
func (l *Lease) ObserveHFPReady(ready bool)     { l.observeComponent("hfp", ready) }
func (l *Lease) observeComponent(component string, ready bool) {
	s := l.session
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Generation != l.generation || !s.state.ConnectAllowed {
		return
	}
	switch component {
	case "audio":
		s.state.AudioReady = ready
	case "buttons":
		s.state.ButtonsReady = ready
	case "hfp":
		s.state.HFPReady = ready
	}
	s.state.Ready = s.state.AudioReady && s.state.ButtonsReady && s.state.HFPReady
	if !s.state.Ready {
		s.state.ButtonPressed = false
	}
}
