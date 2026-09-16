package comms

import (
	"context"
	"time"

	"github.com/openmanet/openmanetd/internal/comms/audiopool"
	"github.com/openmanet/openmanetd/internal/comms/control"
	"github.com/openmanet/openmanetd/internal/config"
)

// defaultPttStartDelayMs is the mic-warmup floor applied when
// CommsConfig.PttStartDelayMs is left unset (== 0). 50 ms is long enough
// to let USB audio class capture devices commit their first DMA cycle
// before the encoder starts pulling frames. The previous hard-coded
// 200 ms was a conservative guess. The actual time beginTransmission
// sleeps may be larger than this value when the playback output latency
// requires a longer beep settle window — see transmitSettleWait. To
// skip the wait entirely (and accept the start-tone leak risk on
// hardware with speaker→mic coupling) set PttStartDelayMs to a
// negative value.
const defaultPttStartDelayMs = 50

// frameDuration is the wall-clock length of one Opus frame at the
// capture/playback sample rate (20 ms). The start/stop beeps are
// exactly one frame long.
const frameDuration = time.Duration(audiopool.FrameSize) * time.Second /
	time.Duration(audiopool.SampleRate)

// beepSettleMargin is the extra slack added on top of the playback
// output latency and the beep frame duration when waiting for the
// start tone to fully clear the speaker before the mic capture stream
// goes live. Accounts for:
//
//   - the hand-off delay from PlaybackBuffer (a Go channel) to the
//     playbackChunker — worst case one period, since the malgo
//     callback pulls from the chunker once per period;
//   - playback-callback scheduling jitter; and
//   - room reverb tail on the speaker→mic acoustic path.
const beepSettleMargin = 40 * time.Millisecond

// pttStartDelay returns the configured mic-warmup duration. A negative
// PttStartDelayMs means "skip the wait"; a zero or unset value falls
// back to defaultPttStartDelayMs; positive values are taken as-is.
// Callers should generally use transmitSettleWait, which also accounts
// for the playback output latency required to keep the start beep out
// of the transmitted stream.
func (cfg *CommsConfig) pttStartDelay() time.Duration {
	switch {
	case cfg.PttStartDelayMs < 0:
		return 0
	case cfg.PttStartDelayMs == 0:
		return defaultPttStartDelayMs * time.Millisecond
	default:
		return time.Duration(cfg.PttStartDelayMs) * time.Millisecond
	}
}

// transmitSettleWait returns the duration beginTransmission should
// sleep between queueing the start-tone beep and calling
// BroadcastStream.Start(). It is the greater of:
//
//   - the configured mic-warmup delay (pttStartDelay), and
//   - the beep's physical emission window: the actual playback output
//     latency reported by malgo plus one frame (the beep duration)
//     plus a small margin for scheduling jitter and room reverb.
//
// Without the second term the mic stream goes live before the beep has
// physically emerged from the speaker; any acoustic or device sidetone
// path from speaker → mic then captures the beep and it gets encoded
// into the transmitted RTP stream so the remote side hears it.
//
// When PttStartDelayMs is negative the caller has explicitly opted out
// of the settle wait, so this returns 0 even if the beep has not yet
// cleared the speaker.
func (cfg *CommsConfig) transmitSettleWait(rt *CommsRuntime) time.Duration {
	if cfg.PttStartDelayMs < 0 {
		return 0
	}

	beepSettle := rt.PlaybackOutputLatency + frameDuration + beepSettleMargin

	return max(cfg.pttStartDelay(), beepSettle)
}

// beepWakeWindow is how long a playback stream woken solely to make a PTT
// beep audible keeps running before it is re-slept. Sized to cover the
// worst-case beep emergence path (ALSA ring latency + one frame + settle
// margin, ≈150 ms on USB audio class hardware) with generous slack; the
// timer is Reset on every beep, so a stop-beep queued late in a start-beep's
// window always gets a full window of its own.
const beepWakeWindow = 500 * time.Millisecond

// queueBeep queues one beep frame into exactly one audible playback stream.
// Preference order:
//
//  1. The first port whose stream is running (an RX-enabled port) — one
//     clean copy of the tone. The pre-P4 code fanned the beep out to every
//     receive-capable port, which played N overlapping copies through dmix
//     into the same physical output.
//  2. No stream running (zero receive-enabled ports): wake the first
//     startable stream, queue the beep, and arm its re-sleep timer —
//     PTT feedback must stay audible even with all monitors off.
//  3. No streams at all (web mode, audio-failed mode): queue to the first
//     port's buffer, matching the legacy harmless-with-no-consumer path.
//
// Callers must drain the playback buffers first (drainPlaybackBuffer), so
// the buffered channel send below cannot block.
func (cfg *CommsConfig) queueBeep(rt *CommsRuntime, beep []int16) {
	for _, pc := range rt.Ports {
		if pc.PlaybackBuffer != nil && pc.playbackIsRunning() {
			pc.PlaybackBuffer <- beep

			return
		}
	}

	for _, pc := range rt.Ports {
		if pc.PlaybackBuffer == nil {
			continue
		}

		if err := pc.startPlayback(); err != nil {
			cfg.Log.Warn().Err(err).Int("port", pc.cfg.Port).
				Msg("comms: failed to wake playback stream for beep")

			continue
		}

		if !pc.playbackIsRunning() {
			// No stream installed on this port; try the next.
			continue
		}

		pc.PlaybackBuffer <- beep

		pc.armBeepSleep(beepWakeWindow)

		return
	}

	for _, pc := range rt.Ports {
		if pc.PlaybackBuffer != nil {
			pc.PlaybackBuffer <- beep

			return
		}
	}
}

// trySendPlaybackFrame attempts a non-blocking send of frame into pc's
// playback buffer. Returns whether the buffer accepted it. A free
// function (not a closure) so queueLocalAudioFrame's hot path allocates
// nothing — a closure capturing frame would otherwise escape to the heap.
func trySendPlaybackFrame(pc *PortChannel, frame []int16) bool {
	select {
	case pc.PlaybackBuffer <- frame:
		return true
	default:
		return false
	}
}

// queueLocalAudioFrame queues one locally generated PCM frame (an
// announcement) into a single audible playback stream. Preference:
//
//  1. The active talk group's port, when its stream is running — the
//     announcement should come out of the channel it announces.
//  2. The first running stream (mirrors queueBeep's rule 1).
//  3. Wake the first startable stream, queue the frame, and arm the beep
//     re-sleep timer ONCE (beepWakeWindow). This branch is only reached for
//     an announcement with NO receive-enabled port; in the normal
//     selection->announce flow the active port's stream is already running
//     (started by applyReceivePlayback), so frames take rule 1. Because the
//     timer is armed once (not per frame), a clip longer than beepWakeWindow
//     played into a woken-but-RX-disabled port could be re-slept mid-clip —
//     an accepted edge for the no-monitor case; the common path is unaffected.
//
// Unlike queueBeep the sends are non-blocking: the announcer produces a
// frame every 20 ms indefinitely, and a stalled buffer must drop (the
// player counts it) rather than wedge the player goroutine. Returns
// whether a buffer accepted the frame.
func (cfg *CommsConfig) queueLocalAudioFrame(rt *CommsRuntime, frame []int16) bool {
	if ch := int(rt.ActiveChannel.Load()); ch > 0 {
		if port, err := config.TalkGroupPort(ch); err == nil {
			for _, pc := range rt.Ports {
				if pc.cfg.Port == port && pc.PlaybackBuffer != nil && pc.playbackIsRunning() {
					return trySendPlaybackFrame(pc, frame)
				}
			}
		}
	}

	for _, pc := range rt.Ports {
		if pc.PlaybackBuffer != nil && pc.playbackIsRunning() {
			return trySendPlaybackFrame(pc, frame)
		}
	}

	for _, pc := range rt.Ports {
		if pc.PlaybackBuffer == nil {
			continue
		}

		if err := pc.startPlayback(); err != nil {
			cfg.Log.Warn().Err(err).Int("port", pc.cfg.Port).
				Msg("comms: failed to wake playback stream for announcement")

			continue
		}

		if !pc.playbackIsRunning() {
			continue
		}

		if !trySendPlaybackFrame(pc, frame) {
			return false
		}

		pc.armBeepSleep(beepWakeWindow)

		return true
	}

	return false
}

// ─── Transmission state ───────────────────────────────────────────────────────

func (cfg *CommsConfig) isBroadcasting(rt *CommsRuntime) bool {
	return rt.Broadcasting.Load()
}

func (cfg *CommsConfig) drainPlaybackBuffer(rt *CommsRuntime) {
	for _, pc := range rt.Ports {
		buf := pc.PlaybackBuffer
		if buf == nil {
			continue
		}

		// Drain this port's buffer non-blockingly via a labeled break.
	drain:
		for {
			select {
			case <-buf:
			default:
				break drain
			}
		}
	}
}

// beginTransmission opens the TX gate on the always-on capture stream and
// plays the start-tone into the local speaker to signal the start of
// transmission.
//
// The broadcast capture stream is opened once at StartHardware and stays
// open for the lifetime of the comms run. beginTransmission flips an atomic
// gate inside BroadcastCapture so the captureCallback begins forwarding
// frames to the Opus encoder + RTP send pipeline. When the gate is closed
// the capture callback still runs (the VOX tap continues to observe mic
// frames) but encoded frames never hit the wire.
func (cfg *CommsConfig) beginTransmission(rt *CommsRuntime) {
	if rt.Broadcasting.Load() {
		cfg.Log.Debug().Msg("PTTDown ignored; already broadcasting")

		return
	}

	// Half-duplex: refuse to transmit while the channel is actively receiving
	// audio from a remote peer.
	if cfg.isReceivingRemote(rt) {
		cfg.Log.Debug().Msg("PTTDown ignored; channel busy (receiving remote audio)")

		return
	}

	rt.Broadcasting.Store(true)

	// Web mode: the browser provides its own audio I/O and UI feedback,
	// so skip beep tones, playback drain, and capture-gate management.
	if rt.WebBridge != nil {
		cfg.Log.Debug().Msg("Begin web transmission")

		return
	}

	cfg.Log.Debug().Msg("Begin transmission: playing start tone and opening TX gate")
	cfg.drainPlaybackBuffer(rt)
	cfg.queueBeep(rt, rt.BeepBufferStart)

	// Settle window before the TX gate opens. Holds the gate closed
	// until the start-tone beep has fully emerged from the speaker —
	// otherwise an acoustic (or device sidetone) path from speaker →
	// mic captures the beep and the remote side hears it. The wait
	// also covers hardware that warms its capture path slowly. Sized
	// by transmitSettleWait from the playback output latency and
	// CommsConfig.PttStartDelayMs.
	if d := cfg.transmitSettleWait(rt); d > 0 {
		time.Sleep(d)
	}

	bs := rt.Broadcast()
	if bs == nil {
		cfg.Log.Error().Msg("BroadcastStream is nil; cannot begin transmission")
		rt.Broadcasting.Store(false)

		return
	}

	bs.SetTxEnabled(true)

	cfg.Log.Debug().Msg("TX gate opened")
}

// endTransmission closes the TX gate on the always-on capture stream and
// plays the stop-tone. The capture stream itself keeps running after
// endTransmission so the VOX tap (if any) continues to observe the mic.
func (cfg *CommsConfig) endTransmission(rt *CommsRuntime) {
	if !rt.Broadcasting.Load() {
		cfg.Log.Debug().Msg("PTTUp ignored; mic already idle")

		return
	}

	// Web mode: skip capture-gate management and beep tones.
	if rt.WebBridge != nil {
		cfg.Log.Debug().Msg("End web transmission")
		rt.Broadcasting.Store(false)

		return
	}

	cfg.Log.Debug().Msg("End transmission: closing TX gate and playing stop tone")

	if bs := rt.Broadcast(); bs == nil {
		cfg.Log.Warn().Msg("BroadcastStream was nil during end transmission")
	} else {
		bs.SetTxEnabled(false)
	}

	cfg.drainPlaybackBuffer(rt)
	cfg.queueBeep(rt, rt.BeepBufferStop)

	rt.Broadcasting.Store(false)
}

// Run is the main event loop. It starts a receiveLoop goroutine for every
// Receive-capable port plus a single halfDuplexDecayLoop that clears the
// cached RemoteRxActive flag when every gate has gone quiet, then blocks
// dispatching PTT events until ctx is canceled.
func (cfg *CommsConfig) Run(parentCtx context.Context, rt *CommsRuntime, src control.EventSource) {
	// Run owns the loops it spawns: derive a context canceled when Run
	// returns for any reason — parent cancellation or the event source
	// closing its channel (e.g. the PTT device dying). Without this,
	// Start's deferred teardown closes every receiver while the manager's
	// context is still live and the receive loops retry against
	// permanently dead sockets until Disable is called.
	ctx, cancel := context.WithCancel(parentCtx)
	defer cancel()

	for _, pc := range rt.Ports {
		if pc.Receiver != nil {
			go cfg.receiveLoop(ctx, pc, rt)
		}
	}

	go cfg.halfDuplexDecayLoop(ctx, rt)

	if rt.FECAdapter != nil {
		go rt.FECAdapter.Run(ctx)
	}

	events := src.Events(ctx)

	if aux, ok := src.(control.AuxEventSource); ok && cfg.AuxHandler != nil {
		// The aux pump deliberately runs on the parent context, not the
		// Run-scoped one: it must drain aux events still queued when the
		// PTT channel closes, and it has its own natural termination — the
		// control source closes its aux channel when it dies, and Disable
		// cancels the parent. Run-scoped cancellation would cut the drain
		// short (pinned by TestRun_AuxEvents_DispatchedToHandler).
		go cfg.runAuxPump(parentCtx, aux)
	}

	// In-run audio recovery: when hardware audio failed at startup (or the
	// dongle was absent), periodically re-attempt init. Disabled in web
	// mode, when audio is already up, or when the interval is unset (<= 0,
	// the zero value used by unit tests). recoverC stays nil when disabled;
	// a receive from a nil channel blocks forever, so the extra case is
	// inert. Single attempt per tick on this goroutine — bounded by design.
	// Accepted tradeoff: tryAudioRecovery is not ctx-aware, so ctx
	// cancellation during an in-flight ALSA open leaves shutdown waiting
	// behind that one attempt before Run can return.
	var (
		recoverC        <-chan time.Time
		recoverTick     *time.Ticker
		recoverAttempts int
	)

	if cfg.ControlSource != controlSourceWeb && cfg.audioRecoveryInterval > 0 && rt.Broadcast() == nil {
		recoverTick = time.NewTicker(cfg.audioRecoveryInterval)
		defer recoverTick.Stop()

		recoverC = recoverTick.C
	}

	for {
		select {
		case <-ctx.Done():
			cfg.Log.Info().Msg("comms context canceled; exiting run loop")

			return
		case <-recoverC:
			recoverAttempts++

			if cfg.tryAudioRecovery(rt, recoverAttempts) {
				recoverTick.Stop()

				recoverC = nil
			}
		case ev, ok := <-events:
			if !ok {
				return
			}

			switch ev {
			case control.PTTDown:
				cfg.beginTransmission(rt)
			case control.PTTUp:
				cfg.endTransmission(rt)
			case control.PTTToggle:
				if cfg.isBroadcasting(rt) {
					cfg.Log.Debug().Msg("Comm toggle: stopping transmission")
					cfg.endTransmission(rt)
				} else {
					cfg.Log.Debug().Msg("Comm toggle: starting transmission")
					cfg.beginTransmission(rt)
				}
			}
		}
	}
}

// runAuxPump forwards every AuxEvent received from the source into
// cfg.AuxHandler. The pump exits when the source closes its aux channel
// (which happens when the source's read goroutine exits — typically on
// context cancel or HID read error).
func (cfg *CommsConfig) runAuxPump(ctx context.Context, aux control.AuxEventSource) {
	ch := aux.AuxEvents()

	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}

			cfg.AuxHandler.Handle(ctx, ev)
		}
	}
}
