package comms

import (
	"context"
	"fmt"
	"math"
	"os"
	"time"

	"github.com/rs/zerolog"

	"github.com/openmanet/openmanetd/internal/comms/announce"
	"github.com/openmanet/openmanetd/internal/comms/audio"
	"github.com/openmanet/openmanetd/internal/comms/audiopool"
	"github.com/openmanet/openmanetd/internal/comms/control"
	"github.com/openmanet/openmanetd/internal/comms/device"
	"github.com/openmanet/openmanetd/internal/comms/gpio"
	"github.com/openmanet/openmanetd/internal/comms/rtp"
	"github.com/openmanet/openmanetd/internal/comms/talkgroup"
	"github.com/openmanet/openmanetd/internal/comms/webaudio"
	"github.com/openmanet/openmanetd/internal/config"
	"github.com/openmanet/openmanetd/internal/util/board"
)

// startHardwareAudio constructs an audio.Init bound to cfg/rt, builds the
// per-port playback slots, and starts the malgo streams. Returns the cleanup
// function that StartHardware produced (which the caller defers).
//
// Extracted from Start so that Start's cognitive complexity stays under the
// linter threshold and the audio sub-package wiring lives next to its other
// per-port helpers.
func (cfg *CommsConfig) startHardwareAudio(rt *CommsRuntime) (cleanup func(), err error) {
	audioInit := &audio.Init{
		Deps: audio.Deps{
			Log:                    cfg.Log,
			Trace:                  cfg.Trace,
			Debug:                  cfg.Debug,
			MicGain:                cfg.MicGain,
			CaptureLatencyMs:       cfg.CaptureLatencyMs,
			PlaybackLatencyMs:      cfg.PlaybackLatencyMs,
			CaptureFramesPerBuffer: cfg.CaptureFramesPerBuffer,
			InputDeviceSpec:        cfg.BluetoothInputDevice,
			OutputDeviceSpec:       cfg.BluetoothOutputDevice,
			Encoder:                rt.Encoder,
			Send:                   func(payload []byte) { cfg.sendToAllPorts(rt, payload) },
			Tap:                    &rt.BroadcastTap,
		},
	}

	// beepChannelDepth is the one-shot side channel that the TX path
	// (transmit.go beginTransmission/endTransmission) uses to inject
	// start/stop beep tones. The malgo playback callback drains it
	// before falling through to playoutOneFrame.
	const beepChannelDepth = 4

	slots := make([]audio.PortSlot, 0, len(rt.Ports))

	for i := range rt.Ports {
		pc := rt.Ports[i]
		if pc.Receiver == nil {
			continue
		}

		pc.PlaybackBuffer = make(chan []int16, beepChannelDepth)

		pcRef := pc

		slots = append(slots, audio.PortSlot{
			HasReceiver:  true,
			Port:         pc.cfg.Port,
			BeepBuf:      pc.PlaybackBuffer,
			SetStream:    func(s device.AudioStream) { pcRef.setPlaybackStream(s) },
			PlayoutFrame: func(out []int16) { cfg.playoutOneFrame(pcRef, rt, pcRef.Jitter, out) },
		})
	}

	broadcast, cleanup, hwErr := audioInit.StartHardware(slots)
	if hwErr != nil {
		// The SetStream hooks may have stored streams that StartHardware's
		// unwind already closed; detach them so a later toggle or beep can
		// never call into a freed malgo device.
		for _, pc := range rt.Ports {
			pc.clearPlaybackStream()
		}

		return nil, hwErr
	}

	rt.SetBroadcast(broadcast)
	rt.PlaybackOutputLatency = audioInit.PlaybackOutputLatency

	// StartHardware started every playback stream; record that, then
	// re-sleep the streams of ports that are not receive-enabled (P4: an
	// idle port must not keep a malgo RT thread waking every 20 ms).
	cfg.markAndSyncPlayback(rt)

	// Detach the per-port streams before the hardware cleanup closes
	// them, for the same freed-device reason as the failure path above.
	wrapped := func() {
		for _, pc := range rt.Ports {
			pc.clearPlaybackStream()
		}

		cleanup()
	}

	return wrapped, nil
}

// markAndSyncPlayback records that StartHardware started every installed
// playback stream, then stops the streams of receive-disabled ports so
// only enabled ports keep a running malgo device. Called after every
// successful hardware init (boot and in-run recovery), so a recovery
// honors toggles made while audio was down.
func (cfg *CommsConfig) markAndSyncPlayback(rt *CommsRuntime) {
	for _, pc := range rt.Ports {
		if !pc.playbackStreamInstalled() {
			continue
		}

		pc.markPlaybackRunning()

		if pc.ReceiveEnabled.Load() {
			continue
		}

		if err := pc.stopPlayback(); err != nil {
			// Non-fatal: the stream keeps running, which is the pre-P4
			// status quo for a disabled port, not a correctness problem.
			cfg.Log.Warn().Err(err).Int("port", pc.cfg.Port).
				Msg("comms: failed to sleep playback stream for disabled port")
		}
	}
}

const (
	// audioInitAttempts bounds the startup hardware-audio init retries.
	// The OpenVLM dongle can transiently fail its first dmix slave start
	// while USB enumeration settles at boot (EPIPE from snd_pcm_start);
	// a couple of spaced retries absorbs that without delaying startup
	// noticeably.
	audioInitAttempts = 3

	// defaultAudioInitRetryDelay is the production wait between startup
	// attempts; applyDefaults installs it when the field is zero.
	defaultAudioInitRetryDelay = 750 * time.Millisecond

	// defaultAudioRecoveryInterval paces in-run hardware audio recovery
	// attempts. Slow enough that a permanently absent dongle costs nothing
	// measurable, fast enough that plugging one in feels immediate.
	defaultAudioRecoveryInterval = 10 * time.Second

	// recoveryDetectEveryNth bounds how often tryAudioRecovery re-runs ALSA
	// card detection once the first attempt has already run it once. At
	// the default 10s audioRecoveryInterval this is roughly once a minute.
	// Detection itself logs a Warn on every miss (openvlm.go), so re-running
	// it every tick on a permanently dongle-less device would flood logd;
	// bounding the re-run rate keeps that noise in check without disabling
	// detection outright (a dongle plugged in after startup still gets
	// picked up within a minute).
	recoveryDetectEveryNth = 6
)

// initAudioIO wires up the local audio path for cfg.ControlSource. In web
// mode it constructs a webaudio bridge; in non-web modes (openvlm, nanoptt,
// roip) it tries to open the malgo capture/playback streams, retrying up to
// audioInitAttempts times with cfg.audioInitRetryDelay between attempts.
//
// A failure to bring up local audio is non-fatal: the comms subsystem stays
// alive so RTP relay between mesh peers continues, and the WebUI's
// per-channel RX/TX toggles still take effect. The TX/RX hot paths already
// guard against the resulting nil BroadcastStream / PlaybackStream. The Run
// loop's audio recovery ticker keeps re-attempting init in the background.
//
// Returns the malgo cleanup function (nil when there is nothing to clean
// up) so Start can defer it.
func (cfg *CommsConfig) initAudioIO(ctx context.Context, rt *CommsRuntime) func() {
	if cfg.ControlSource == controlSourceWeb {
		// Web mode: skip the malgo pipeline entirely; the browser provides audio I/O.
		rt.WebBridge = webaudio.NewBridge(cfg.Log, func(payload []byte) {
			cfg.sendToAllPorts(rt, payload)
		})

		return nil
	}

	startHA := cfg.startHardwareAudioFn
	if startHA == nil {
		startHA = cfg.startHardwareAudio
	}

	var lastErr error

	for attempt := 1; attempt <= audioInitAttempts; attempt++ {
		cleanup, hwErr := startHA(rt)
		if hwErr == nil {
			if attempt > 1 {
				cfg.Log.Info().Int("attempt", attempt).Msg("comms: hardware audio init succeeded after retry")
			}

			return cleanup
		}

		lastErr = hwErr
		cfg.Log.Warn().Err(hwErr).
			Int("attempt", attempt).
			Int("max_attempts", audioInitAttempts).
			Msg("comms: hardware audio init attempt failed")

		if attempt == audioInitAttempts {
			break
		}

		if !sleepCtx(ctx, cfg.audioInitRetryDelay) {
			return nil
		}
	}

	cfg.Log.Error().Err(lastErr).
		Str("alsa_card", os.Getenv("ALSA_CARD")).
		Msg("comms: hardware audio init failed; continuing without local mic/speaker")

	return nil
}

// sleepCtx waits for d or until ctx is canceled. Returns false when the
// context ended the wait (or was already canceled).
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}

	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// tryAudioRecovery performs one in-run hardware audio init attempt. Called
// only from the Run goroutine (ticker case) with a monotonically increasing
// attempt counter local to that goroutine — never accessed concurrently, so
// it needs no synchronization of its own.
//
// Re-runs ALSA card detection when ALSA_CARD is still unset — the OpenVLM
// may have been plugged in after startup detection ran — but only on the
// first attempt and every recoveryDetectEveryNth attempt thereafter (see
// its doc comment): detection logs its own Warn on every miss, and running
// it on every 10s tick forever on a dongle-less device floods logd.
//
// Failure logging is similarly throttled: the first failed attempt logs at
// Warn (so the operator sees the daemon is degraded), every subsequent
// consecutive failure logs at Debug so a permanently absent dongle doesn't
// produce ~17k Warn lines/day. Returns true when audio is up.
func (cfg *CommsConfig) tryAudioRecovery(rt *CommsRuntime, attempt int) bool {
	if os.Getenv("ALSA_CARD") == "" &&
		(cfg.ControlSource == defaultCtrlSrc || cfg.ControlSource == controlSourceROIP) &&
		(attempt == 1 || attempt%recoveryDetectEveryNth == 0) {
		cfg.detectALSACard()
	}

	startHA := cfg.startHardwareAudioFn
	if startHA == nil {
		startHA = cfg.startHardwareAudio
	}

	cleanup, err := startHA(rt)
	if err != nil {
		logEvent := cfg.Log.Debug()
		if attempt == 1 {
			logEvent = cfg.Log.Warn()
		}

		logEvent.Err(err).
			Int("attempt", attempt).
			Dur("retry_in", cfg.audioRecoveryInterval).
			Msg("comms: audio recovery attempt failed")

		return false
	}

	rt.audioCleanup = cleanup

	cfg.applyMixerStartup()

	cfg.Log.Info().Msg("comms: hardware audio recovered")

	return true
}

// applyMixerStartup invokes the wired startup mixer re-apply, if any.
func (cfg *CommsConfig) applyMixerStartup() {
	if cfg.AudioMixerStartup != nil {
		cfg.AudioMixerStartup()
	}
}

// detectALSACard runs ALSA card auto-detection through cfg.detectALSACardFn
// when set (test seam), falling back to the real
// control.DetectAndSetALSACard otherwise. Follows the same override pattern
// as startHardwareAudio/startHardwareAudioFn.
func (cfg *CommsConfig) detectALSACard() {
	if cfg.detectALSACardFn != nil {
		cfg.detectALSACardFn()

		return
	}

	control.DetectAndSetALSACard(cfg.Log)
}

// seedActiveChannel derives the boot-time active talk group from the
// seeded per-port toggles (first port with both directions enabled),
// records it, and emits a SourceInit event. The announcer deliberately
// ignores SourceInit, so boot is silent.
func (cfg *CommsConfig) seedActiveChannel(rt *CommsRuntime) {
	for _, pc := range rt.Ports {
		if !pc.SendEnabled.Load() || !pc.ReceiveEnabled.Load() {
			continue
		}

		ch, err := config.TalkGroupChannel(pc.cfg.Port)
		if err != nil {
			continue
		}

		rt.ActiveChannel.Store(int32(ch))
		rt.Events.Notify(talkgroup.Event{
			Kind: talkgroup.KindSelected, Channel: ch,
			Send: true, Receive: true,
			Source: talkgroup.SourceInit, At: time.Now(),
		})

		return
	}
}

// startAnnouncer wires the announcement player to the event registry.
// Best-effort: clip decode failure logs and disables announcements
// (mirroring the audio-init posture). Web mode is skipped — the browser
// owns the speaker; it gets the event stream instead. The registry
// listener is never removed: registry and player share the runtime's
// lifetime, and Run exits with ctx.
func (cfg *CommsConfig) startAnnouncer(ctx context.Context, rt *CommsRuntime) {
	if rt.WebBridge != nil {
		return
	}

	player, err := announce.New(cfg.Log, func(frame []int16) bool {
		return cfg.queueLocalAudioFrame(rt, frame)
	})
	if err != nil {
		cfg.Log.Warn().Err(err).Msg("comms: announcements disabled")

		return
	}

	rt.Announcer = player

	go player.Run(ctx)

	rt.Events.Add(func(ev talkgroup.Event) {
		if ev.Kind != talkgroup.KindSelected || ev.Source == talkgroup.SourceInit {
			return
		}

		player.Announce(ev.Channel)
	})
}

// startGPIOSelector launches the hardware talk group selector when the
// board wires one (Raven) and the operator has not disabled it.
// Best-effort: an open failure (driver quirk, permissions) logs and
// degrades gracefully — RPC and web selection keep working.
func (cfg *CommsConfig) startGPIOSelector(ctx context.Context, svc *Service) {
	supported := cfg.gpioSelectorSupportedFn
	if supported == nil {
		supported = board.GPIOSelectorSupported
	}

	if !supported() || !cfg.GPIOSelectorEnable {
		return
	}

	sel := &gpio.Selector{Log: cfg.Log}

	events, err := sel.Events(ctx)
	if err != nil {
		cfg.Log.Warn().Err(err).Msg("comms: GPIO selector unavailable")

		return
	}

	svc.Rt.GPIOSel = sel

	cfg.Log.Debug().Msg("comms: GPIO talk group selector started")

	go svc.forwardSelections(events, cfg.Log)
}

// forwardSelections applies each channel emitted by the GPIO selector as a
// talk group selection. The FIRST emission is the selector's boot-time read
// of the physical switch position: it is forwarded as SourceInit so the
// daemon adopts that position (flip + ActiveChannel update + stream event)
// WITHOUT the announcer speaking it — honoring the "no boot-time
// announcement" decision. Every later emission is a live operator action,
// forwarded as SourceGPIO (which the announcer does play). The loop exits
// when events closes (ctx cancel or the selector's error breaker).
func (s *Service) forwardSelections(events <-chan int, log zerolog.Logger) {
	src := talkgroup.SourceInit

	for ch := range events {
		log.Debug().Int("channel", ch).Str("source", src.String()).
			Msg("comms: applying GPIO talk group selection")

		if err := s.SelectTalkGroup(ch, src); err != nil {
			log.Warn().Err(err).Int("channel", ch).
				Msg("comms: GPIO talk group selection failed")
		}

		src = talkgroup.SourceGPIO
	}

	log.Debug().Msg("comms: GPIO selection forwarder stopped")
}

// Start initializes all comms subsystems and blocks until ctx is canceled.
// Returns nil on clean shutdown, or an error if initialization fails.
// The caller is responsible for canceling ctx to stop the subsystem.
func (cfg *CommsConfig) Start(ctx context.Context) error {
	if !cfg.Enable {
		cfg.Log.Info().Msg("comms: functionality disabled; not starting")

		return nil
	}

	cfg.applyDefaults()

	if cfg.ControlSource != controlSourceWeb {
		if cfg.ControlSource == defaultCtrlSrc || cfg.ControlSource == controlSourceROIP {
			control.DetectAndSetALSACard(cfg.Log)
		}
	}

	cfg.applyMixerStartup()

	switch {
	case cfg.Trace:
		cfg.Log = cfg.Log.Level(zerolog.TraceLevel)
	case cfg.Debug:
		cfg.Log = cfg.Log.Level(zerolog.DebugLevel)
	}

	cfg.Log.Info().Msgf(
		"comms: starting iface=%s talkgroups=%d key=%s debug=%t trace=%t loopback=%t device=%s",
		cfg.Iface, len(cfg.McastPorts), cfg.CommKey,
		cfg.Debug, cfg.Trace, cfg.Loopback, cfg.ControlSource,
	)

	// ── codec ──────────────────────────────────────────────────────────────
	enc, err := cfg.buildEncoder()
	if err != nil {
		return fmt.Errorf("comms: failed to build Opus encoder: %w", err)
	}

	// ── beep tones ─────────────────────────────────────────────────────────
	// Phase 5: beep buffers are int16-native so they can be written directly
	// into the malgo int16 playback callback without an extra conversion.
	// Amplitude 0.2 * 32767 ≈ 6553 matches the previous float32 volume.
	beepStart := make([]int16, audiopool.FrameSize)
	beepStop := make([]int16, audiopool.FrameSize)

	const beepAmp = 0.2 * 32767

	for i := range beepStart {
		beepStart[i] = int16(math.Sin(2*math.Pi*1000*float64(i)/float64(audiopool.SampleRate)) * beepAmp)
		beepStop[i] = int16(math.Sin(2*math.Pi*600*float64(i)/float64(audiopool.SampleRate)) * beepAmp)
	}

	// ── network ────────────────────────────────────────────────────────────
	ports, localIP, netErr := cfg.buildNetwork()
	if netErr != nil {
		return fmt.Errorf("comms: failed to set up network: %w", netErr)
	}

	// Per-port decoders: allocated after buildNetwork so every
	// receive-capable port (Receiver != nil) gets its own instance.
	if decErr := buildPortDecoders(ports); decErr != nil {
		for _, pc := range ports {
			pc.closePartial()
		}

		return fmt.Errorf("comms: failed to build Opus decoders: %w", decErr)
	}

	// ── assemble runtime ───────────────────────────────────────────────────
	rt := &CommsRuntime{
		Encoder:         enc,
		Ports:           ports,
		BeepBufferStart: beepStart,
		BeepBufferStop:  beepStop,
		Events:          talkgroup.NewRegistry(cfg.Log),
	}

	rt.LocalIP.Store(&localIP)

	// ── FEC adapter ────────────────────────────────────────────────────────
	// Construct after ports are populated so the adapter's prev slice is
	// sized correctly. The constructor also makes an initial
	// SetPacketLossPerc(floor) call to scrub any stale level from a prior
	// enable cycle. The Run goroutine is launched inside cfg.Run()
	// alongside halfDuplexDecayLoop.
	rt.FECAdapter = NewFECAdapter(rt, enc, cfg.PacketLossPerc, cfg.Log)

	// Wrap the static config and the freshly built runtime in a *Service
	// so the HTTP handlers and other consumers can resolve them via
	// SetDefault / Default. The Service is published just before the event
	// source is built so anything that depends on the live runtime can
	// observe it as soon as Run starts.
	svc := &Service{Cfg: cfg, Rt: rt}

	defer func() {
		for _, pc := range rt.Ports {
			if pc.Receiver != nil {
				_ = pc.Receiver.Close()
			}

			if pc.RTPSess != nil {
				if s, ok := pc.RTPSess.(*rtp.Session); ok {
					_ = s.Close()
				}
			}
		}

		SetDefault(nil)
	}()

	SetDefault(svc)

	cfg.seedActiveChannel(rt)

	// ── event source ───────────────────────────────────────────────────────
	src, srcErr := cfg.buildEventSource(rt)
	if srcErr != nil {
		return fmt.Errorf("comms: failed to build event source: %w", srcErr)
	}

	// ── audio I/O ─────────────────────────────────────────────────────────
	rt.audioCleanup = cfg.initAudioIO(ctx, rt)

	defer func() {
		if rt.audioCleanup != nil {
			rt.audioCleanup()
		}
	}()

	// ── announcer ─────────────────────────────────────────────────────────
	// Must run after initAudioIO: it needs the per-port playback streams
	// that initAudioIO/startHardwareAudio install, and it checks
	// rt.WebBridge (set by initAudioIO's web-mode branch) to skip web mode.
	cfg.startAnnouncer(ctx, rt)

	// ── GPIO selector ─────────────────────────────────────────────────────
	// Raven-only hardware talk group selector; skipped when the board
	// doesn't wire one or the operator disabled it. Best-effort: an open
	// failure degrades gracefully, leaving RPC/web selection working.
	cfg.startGPIOSelector(ctx, svc)

	// ── run loop ───────────────────────────────────────────────────────────
	cfg.Run(ctx, rt, src)

	cfg.Log.Info().Msg("comms: subsystem stopped")

	return nil
}
