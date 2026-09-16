package comms

import (
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/openmanet/openmanetd/internal/comms/control"
	"github.com/openmanet/openmanetd/internal/comms/talkgroup"
	"github.com/openmanet/openmanetd/internal/comms/webaudio"
	"github.com/openmanet/openmanetd/internal/config"
)

// Service is the live comms subsystem instance returned (conceptually) by
// Start. It carries an immutable *CommsConfig snapshot and the live
// *CommsRuntime, and exposes the accessor methods that the HTTP handlers
// need to read or mutate runtime state.
//
// Cfg is set once at construction and never replaced; Rt is set once after
// Start finishes building the runtime and is cleared on shutdown via
// SetDefault(nil). Methods on *Service tolerate nil receivers and a nil
// Service.Rt so handlers can defensively call them before comms is enabled.
type Service struct {
	Cfg *CommsConfig
	Rt  *CommsRuntime
}

// defaultService is the process-wide singleton most recently published by
// Start. It exists so external call sites that do not yet receive a
// *Service by injection (handlers, tests) can resolve the live instance
// lazily via Default(). Phase D2 of the comms refactor adds a typed
// `Service func() *comms.Service` field to the HTTP handler so handlers
// no longer need to reach in here directly.
//
// Access is lock-free via atomic.Pointer: Start publishes with Store, every
// accessor reads with Load. The pointer itself is the only mutable state;
// the referent *Service is fully built before publication and its own
// fields carry their own synchronization.
//
//nolint:gochecknoglobals // injection-point shim — see Default / SetDefault.
var defaultService atomic.Pointer[Service]

// ErrNotRunning is returned by Service methods when the comms subsystem has
// not been started (or has been stopped). It is exported so callers can use
// errors.Is to distinguish it from other failure modes.
var ErrNotRunning = errors.New("comms: subsystem is not running")

// Default returns the Service most recently published by Start, or nil when
// comms has not been started (or has stopped).
func Default() *Service {
	return defaultService.Load()
}

// SetDefault publishes svc as the process-wide default Service. Start calls
// this after constructing the runtime; shutdown passes nil. Tests that
// build a Service directly use it the same way to wire up the lazy lookup.
func SetDefault(svc *Service) {
	defaultService.Store(svc)
}

// ─── Service methods ─────────────────────────────────────────────────────────

// ActiveMulticastAddr returns the multicast group address of the first
// configured port. Returns "" when no ports are configured.
func (s *Service) ActiveMulticastAddr() string {
	if s == nil || s.Cfg == nil || len(s.Cfg.McastPorts) == 0 {
		return ""
	}

	return s.Cfg.McastPorts[0].Address
}

// ActiveMulticastPort returns the UDP port of the active talk group,
// falling back to the first configured port when nothing has been
// selected yet. Before the selection spine existed this always returned
// the first port, which made the status RPC's ActiveTalkgroup a fiction.
func (s *Service) ActiveMulticastPort() int {
	if s == nil || s.Cfg == nil || len(s.Cfg.McastPorts) == 0 {
		return 0
	}

	if s.Rt != nil {
		if ch := int(s.Rt.ActiveChannel.Load()); ch > 0 {
			if port, err := config.TalkGroupPort(ch); err == nil {
				return port
			}
		}
	}

	return s.Cfg.McastPorts[0].Port
}

// setSend flips the send toggle without emitting an event. Returns
// whether the stored value actually changed.
func (s *Service) setSend(portIdx int, enabled bool) (bool, error) {
	if s == nil || s.Rt == nil {
		return false, ErrNotRunning
	}

	if portIdx < 0 || portIdx >= len(s.Rt.Ports) {
		return false, fmt.Errorf("comms: port index %d out of range [0, %d)", portIdx, len(s.Rt.Ports))
	}

	old := s.Rt.Ports[portIdx].SendEnabled.Swap(enabled)

	return old != enabled, nil
}

// setReceiveFlag flips the receive toggle atomically without touching the
// playback stream. Returns whether the stored value actually changed.
func (s *Service) setReceiveFlag(portIdx int, enabled bool) (bool, error) {
	if s == nil || s.Rt == nil {
		return false, ErrNotRunning
	}

	if portIdx < 0 || portIdx >= len(s.Rt.Ports) {
		return false, fmt.Errorf("comms: port index %d out of range [0, %d)", portIdx, len(s.Rt.Ports))
	}

	old := s.Rt.Ports[portIdx].ReceiveEnabled.Swap(enabled)

	return old != enabled, nil
}

// applyReceivePlayback starts or stops the port's playback stream to match
// its current ReceiveEnabled flag. Device I/O only — the caller must have
// already flipped the atomic. Deliberately NOT called while holding
// rt.selectMu: startPlayback/stopPlayback perform malgo device calls,
// serialized by the port's own playbackMu, and concurrency.md forbids a
// global lock across device I/O. A stream failure is logged, not returned.
func (s *Service) applyReceivePlayback(portIdx int) {
	pc := s.Rt.Ports[portIdx]

	if !pc.ReceiveEnabled.Load() {
		if err := pc.stopPlayback(); err != nil {
			s.Cfg.Log.Warn().Err(err).Int("port", pc.cfg.Port).
				Msg("comms: failed to sleep playback stream on RX disable")
		}

		return
	}

	// Discard beeps queued while the stream was asleep — a stale start
	// tone from minutes ago must not play the moment the port wakes.
	if pc.PlaybackBuffer != nil {
	drain:
		for {
			select {
			case <-pc.PlaybackBuffer:
			default:
				break drain
			}
		}
	}

	if err := pc.startPlayback(); err != nil {
		s.Cfg.Log.Warn().Err(err).Int("port", pc.cfg.Port).
			Msg("comms: failed to wake playback stream on RX enable")
	}
}

// setReceive flips the receive toggle without emitting an event, starting
// or stopping the port's playback stream to match (P4: only enabled ports
// keep a running malgo device). A device-level stream failure is logged,
// not returned: the RTP-side enable must still take effect — web mode and
// audio-failed mode have no stream at all, and device failures are owned
// by the audio recovery machinery. Returns whether the stored value
// actually changed; the playback choreography is skipped entirely on a
// no-change set as a pure cost optimization (the calls are idempotent).
// Delegates to setReceiveFlag (atomic) + applyReceivePlayback (device I/O).
func (s *Service) setReceive(portIdx int, enabled bool) (bool, error) {
	changed, err := s.setReceiveFlag(portIdx, enabled)
	if err != nil {
		return false, err
	}

	if !changed {
		return false, nil
	}

	s.applyReceivePlayback(portIdx)

	return true, nil
}

// EnableTalkGroupSend toggles RTP transmission on the port at portIdx
// and emits a KindDirection event when the state actually changed.
func (s *Service) EnableTalkGroupSend(portIdx int, enabled bool) error {
	changed, err := s.setSend(portIdx, enabled)
	if err != nil {
		return err
	}

	if changed {
		s.notifyDirection(portIdx)
	}

	return nil
}

// EnableTalkGroupReceive toggles RTP reception on the port at portIdx and
// starts or stops the port's playback stream to match (P4: only enabled
// ports keep a running malgo device), emitting a KindDirection event when
// the state actually changed.
func (s *Service) EnableTalkGroupReceive(portIdx int, enabled bool) error {
	changed, err := s.setReceive(portIdx, enabled)
	if err != nil {
		return err
	}

	if changed {
		s.notifyDirection(portIdx)
	}

	return nil
}

// notifyDirection emits a KindDirection event for the port at portIdx.
// Ports that do not map to a talk group channel (custom McastPorts in
// tests) are silently skipped.
func (s *Service) notifyDirection(portIdx int) {
	rt := s.Rt

	pc := rt.Ports[portIdx]

	ch, err := config.TalkGroupChannel(pc.cfg.Port)
	if err != nil {
		return
	}

	rt.Events.Notify(talkgroup.Event{
		Kind:    talkgroup.KindDirection,
		Channel: ch,
		Send:    pc.SendEnabled.Load(),
		Receive: pc.ReceiveEnabled.Load(),
		Source:  talkgroup.SourceRPC,
		At:      time.Now(),
	})
}

// SelectTalkGroup makes channel the single active talk group: RX+TX on
// its port, all other ports disabled. All selection sources (RPC, GPIO,
// web) funnel through here so every consumer of the event registry sees
// an identical stream. A selection that changes nothing (already active,
// no stray toggles) emits no event — this keeps a boot-time GPIO read
// that matches the seeded channel from announcing at startup.
func (s *Service) SelectTalkGroup(channel int, src talkgroup.Source) error {
	if s == nil || s.Rt == nil {
		return ErrNotRunning
	}

	targetPort, err := config.TalkGroupPort(channel)
	if err != nil {
		return err
	}

	rt := s.Rt

	targetIdx := -1

	for i, pc := range rt.Ports {
		if pc.cfg.Port == targetPort {
			targetIdx = i

			break
		}
	}

	if targetIdx == -1 {
		return fmt.Errorf("comms: talk group %d is not provisioned", channel)
	}

	// Phase 1 — atomic flips only, serialized by selectMu. No device I/O
	// inside the lock: holding selectMu across a malgo Start/Stop would let
	// one hung driver call freeze every selection source. Playback is
	// reconciled unlocked in phase 2, each start/stop serialized by the
	// port's own playbackMu.
	rt.selectMu.Lock()

	var (
		changed   bool
		rxChanged = make([]int, 0, len(rt.Ports))
	)

	// Disable every non-target port FIRST, then enable the target LAST, so a
	// lock-free reader (TX sendToAllPorts, RX loops, status snapshot) never
	// observes two ports active at once — the exclusive-select guarantee holds
	// even mid-flip. All atomic-only; no device I/O under selectMu.
	for i := range rt.Ports {
		if i == targetIdx {
			continue
		}

		sc, sendErr := s.setSend(i, false)
		if sendErr != nil {
			rt.selectMu.Unlock()

			return sendErr
		}

		rc, recvErr := s.setReceiveFlag(i, false)
		if recvErr != nil {
			rt.selectMu.Unlock()

			return recvErr
		}

		if rc {
			rxChanged = append(rxChanged, i)
		}

		changed = changed || sc || rc
	}

	sc, sendErr := s.setSend(targetIdx, true)
	if sendErr != nil {
		rt.selectMu.Unlock()

		return sendErr
	}

	rc, recvErr := s.setReceiveFlag(targetIdx, true)
	if recvErr != nil {
		rt.selectMu.Unlock()

		return recvErr
	}

	if rc {
		rxChanged = append(rxChanged, targetIdx)
	}

	changed = changed || sc || rc

	prev := int(rt.ActiveChannel.Swap(int32(channel)))

	// Emit the selection event while STILL holding selectMu, so concurrent
	// selections notify listeners in the same order they flipped
	// ActiveChannel. If the event fired after Unlock (and after the
	// variable-latency phase-2 device I/O below), a slower select's Notify
	// could land after a newer one and the latest-wins announcer would speak
	// the superseded channel while ActiveChannel already holds the newer one.
	// Listeners are non-blocking by contract (announcer latest-wins slot;
	// stream bounded drop-newest), so this critical section stays bounded and
	// performs no device I/O — the "no lock across blocking ops" rule still
	// holds (the playback reconcile stays unlocked in phase 2).
	if changed || prev != channel {
		rt.Events.Notify(talkgroup.Event{
			Kind:    talkgroup.KindSelected,
			Channel: channel,
			Prev:    prev,
			Send:    true,
			Receive: true,
			Source:  src,
			At:      time.Now(),
		})
	}

	rt.selectMu.Unlock()

	// Phase 2 — reconcile playback for the ports whose receive flag changed.
	// Unlocked: startPlayback/stopPlayback are malgo device calls, serialized
	// per-port by playbackMu; selectMu must never wrap them.
	for _, i := range rxChanged {
		s.applyReceivePlayback(i)
	}

	return nil
}

// ActiveTalkGroup returns the 1-based active talk group, or 0 when comms
// is not running or nothing has been selected yet.
func (s *Service) ActiveTalkGroup() int {
	if s == nil || s.Rt == nil {
		return 0
	}

	return int(s.Rt.ActiveChannel.Load())
}

// Events returns the talk group event registry, or nil when comms is not
// running.
func (s *Service) Events() *talkgroup.Registry {
	if s == nil || s.Rt == nil {
		return nil
	}

	return s.Rt.Events
}

// TalkGroupStates returns a snapshot of per-port direction-toggle state.
func (s *Service) TalkGroupStates() ([]McastPortState, error) {
	if s == nil || s.Rt == nil {
		return nil, ErrNotRunning
	}

	states := make([]McastPortState, len(s.Rt.Ports))

	for i, pc := range s.Rt.Ports {
		states[i] = McastPortState{
			Address:        pc.cfg.Address,
			Port:           pc.cfg.Port,
			SendEnabled:    pc.SendEnabled.Load(),
			ReceiveEnabled: pc.ReceiveEnabled.Load(),
		}
	}

	return states, nil
}

// WebEventSource returns the web control source if one was constructed,
// otherwise nil.
func (s *Service) WebEventSource() *control.WebEventSource {
	if s == nil || s.Rt == nil {
		return nil
	}

	return s.Rt.WebEvtSrc
}

// WebAudioBridge returns the web audio bridge if one was constructed,
// otherwise nil.
func (s *Service) WebAudioBridge() *webaudio.Bridge {
	if s == nil || s.Rt == nil {
		return nil
	}

	return s.Rt.WebBridge
}
