package audio

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gen2brain/malgo"

	"github.com/openmanet/openmanetd/internal/comms/audiopool"
	"github.com/openmanet/openmanetd/internal/comms/device"
)

// Init holds the dependency bundle for opening hardware audio streams.
// The parent comms package constructs one Init per Start() invocation and
// discards it once StartHardware returns. Init carries the live streams it
// opened so its cleanup function can stop and close them in the same
// order they were opened.
type Init struct {
	currentBroadcast *BroadcastEncoder
	malgoCtx         *malgo.AllocatedContext
	playbackStreams  []device.AudioStream
	Deps
	PlaybackOutputLatency time.Duration
}

// BuildAudio resolves the configured capture / playback devices, opens a
// dedicated malgo playback device for every receive-capable PortSlot, and
// opens the shared broadcast capture device. Per-port playback streams
// are installed via PortSlot.SetStream after this call returns.
//
// All devices share the malgo context stored on Init.malgoCtx, which is
// initialized in StartHardware and freed in the cleanup closure.
func (in *Init) BuildAudio(slots []PortSlot) (*BroadcastEncoder, device.AudioDeviceInfo, error) {
	var zeroIn device.AudioDeviceInfo

	if in.malgoCtx == nil {
		return nil, zeroIn, fmt.Errorf("audio.BuildAudio: malgo context is not initialized")
	}

	outDev, err := device.ResolveAudio(in.malgoCtx, in.OutputDeviceSpec, false)
	if err != nil {
		return nil, zeroIn, err
	}

	inDev, err := device.ResolveAudio(in.malgoCtx, in.InputDeviceSpec, true)
	if err != nil {
		return nil, zeroIn, err
	}

	in.Log.Info().
		Str("alsa_card", os.Getenv("ALSA_CARD")).
		Msgf("comms: audio in=%s out=%s", inDev.Name, outDev.Name)

	playbackPeriod := uint32(computePlaybackPeriodFrames(in.PlaybackLatencyMs))

	opened := make([]device.AudioStream, 0, len(slots))

	closeOpened := func() {
		for _, s := range opened {
			if s != nil {
				_ = s.Close()
			}
		}
	}

	// BlueALSA permits one playback client per PCM. Mix talkgroups before
	// opening that endpoint; each port retains independent receive/beep gating.
	if strings.HasPrefix(strings.ToLower(outDev.Name), "bluealsa") {
		// Match the duration-based 20 ms Bluetooth hardware period.
		playbackPeriod = uint32(audiopool.FrameSize)
		slots = []PortSlot{sharedPlaybackSlot(slots)}
	}

	for i := range slots {
		slot := slots[i]
		if !slot.HasReceiver {
			continue
		}

		beepBuf := slot.BeepBuf
		playoutFrame := slot.PlayoutFrame

		var speaker *bluetoothSpeaker
		if strings.HasPrefix(strings.ToLower(outDev.Name), "bluealsa") {
			var speakerErr error
			speaker, speakerErr = openBluetoothSpeaker(outDev.Name)
			if speakerErr != nil {
				closeOpened()
				return nil, zeroIn, fmt.Errorf("open Bluetooth speaker control: %w", speakerErr)
			}
			mixed := playoutFrame
			playoutFrame = func(out []int16) {
				mixed(out)
				speaker.gate.process(out)
			}
		}

		rawPlayback, openErr := openMalgoPlayback(
			in.malgoCtx,
			outDev,
			uint32(audiopool.SampleRate),
			uint32(audiopool.Channels),
			playbackPeriod,
			beepBuf,
			playoutFrame,
		)
		if openErr != nil {
			if speaker != nil {
				speaker.close()
			}
			closeOpened()

			return nil, zeroIn, fmt.Errorf("open playback stream for port %d: %w", slot.Port, openErr)
		}

		// Record the ALSA ring latency so the TX path's beep-emergence
		// wait (transmitSettleWait) has an honest value to anchor on.
		// miniaudio does not expose the negotiated runtime period, and
		// ALSA's USB audio class driver typically rounds the requested
		// period up to the next power of two (e.g. it gives 1024 frames
		// when we ask for 960), so we model that rounding here. The
		// backend also queues malgoLowLatencyPeriods periods in the ALSA
		// ring before the DAC consumes them — that full ring is what
		// the beep must traverse before it physically exits the
		// speaker, not just a single period. Undercounting this was
		// leaking the start tone into the transmitted RTP stream via
		// speaker→mic coupling. The playbackChunker in
		// malgo_playback.go re-aligns whatever period ALSA picks back
		// onto audiopool.FrameSize chunks, so the downstream playoutFrame
		// closure never sees the discrepancy.
		effectivePeriodFrames := nextPow2(playbackPeriod)
		period := periodFramesToDuration(effectivePeriodFrames, uint32(audiopool.SampleRate))
		ringLatency := time.Duration(malgoLowLatencyPeriods) * period
		in.PlaybackOutputLatency = max(in.PlaybackOutputLatency, ringLatency)

		in.Log.Info().
			Int("configured_latency_ms", in.PlaybackLatencyMs).
			Int("requested_period_frames", int(playbackPeriod)).
			Int("effective_period_frames", int(effectivePeriodFrames)).
			Dur("requested_period_duration", period).
			Int("periods", malgoLowLatencyPeriods).
			Dur("ring_latency", ringLatency).
			Int("port", slot.Port).
			Str("device", outDev.Name).
			Msg("comms: playback stream opened")

		stream := device.NewMalgoStream(rawPlayback)
		if speaker != nil {
			stream = &speakerStream{AudioStream: stream, speaker: speaker}
			// Include the bounded control command and settle time in the
			// TX tone-emergence wait so a delayed beep cannot enter the mic.
			in.PlaybackOutputLatency = max(in.PlaybackOutputLatency, ringLatency+500*time.Millisecond+speakerSettle)
		}
		slot.SetStream(stream)
		opened = append(opened, stream)
		in.playbackStreams = append(in.playbackStreams, stream)
	}

	broadcast, err := in.OpenBroadcastStream(inDev)
	if err != nil {
		closeOpened()

		return nil, zeroIn, err
	}

	return broadcast, inDev, nil
}

// OpenBroadcastStream creates a malgo capture device that feeds the Opus
// encoder and RTP send pipeline via BroadcastEncoder. The actual encode
// and RTP send run on a dedicated goroutine inside BroadcastEncoder, NOT
// on the miniaudio audio callback thread, so encoder spikes / GC pauses
// / UDP backpressure cannot starve the audio thread and cause ADC
// overruns at the device.
//
// Under the unified design the capture device stays open for the
// lifetime of the comms run — beginTransmission / endTransmission
// toggle BroadcastEncoder.SetTxEnabled rather than opening/closing
// this stream.
func (in *Init) OpenBroadcastStream(inDev device.AudioDeviceInfo) (*BroadcastEncoder, error) {
	if in.Debug {
		if descs, dErr := device.DiscoverCM108(os.DirFS("/sys")); dErr == nil {
			in.Log.Debug().
				Int("cm108_count", len(descs)).
				Str("device", inDev.Name).
				Msg("comms: unified CM108 descriptor scan at broadcast open")
		}
	}

	periodFrames := uint32(buildCapturePeriodFrames(in.CaptureFramesPerBuffer, in.CaptureLatencyMs))
	if strings.HasPrefix(strings.ToLower(inDev.Name), "bluealsa") {
		periodFrames = uint32(audiopool.FrameSize)
	}
	periods := uint32(buildCapturePeriods(in.CaptureLatencyMs, int(periodFrames)))

	opener := func(onFrame func(samples []int16)) (captureStream, error) {
		return openMalgoCapture(
			in.malgoCtx,
			inDev,
			uint32(audiopool.SampleRate),
			uint32(audiopool.Channels),
			periodFrames,
			periods,
			onFrame,
		)
	}

	be, err := NewBroadcastEncoder(in.Deps, opener)
	if err != nil {
		return nil, err
	}

	// info.InputLatency is the period_frames value we requested converted
	// to a duration; miniaudio does not expose the negotiated runtime
	// period after InitDevice. ALSA's USB audio class driver typically
	// rounds the requested period up to a power of two (e.g. it gives us
	// 1024 frames when we ask for 960). The captureChunker in
	// malgo_capture.go re-aligns whatever period ALSA picks back onto
	// audiopool.FrameSize chunks before the encoder ever sees them, so
	// the discrepancy is invisible to the encode pipeline.
	info := be.s.Info()

	in.Log.Info().
		Int("configured_latency_ms", in.CaptureLatencyMs).
		Int("requested_period_frames", int(periodFrames)).
		Dur("requested_period_duration", info.InputLatency).
		Int("encode_chan_depth", broadcastEncoderChanDepth).
		Str("device", inDev.Name).
		Msg("comms: broadcast stream opened")

	return be, nil
}

// buildCapturePeriodFrames translates the CaptureFramesPerBuffer config
// knob into the value handed to malgo.DeviceConfig.PeriodSizeInFrames.
//
// Rules (mirrors the previous PortAudio semantics to keep existing
// YAML configs working unchanged):
//
//   - framesPerBuffer == 0 → audiopool.FrameSize (960 = 20 ms @ 48 kHz).
//     A small period is required so ALSA wakes the callback every Opus
//     frame instead of bursting multiple frames at once — the encoder's
//     per-frame deadline check assumes one frame arrives every 20 ms.
//     latencyMs is honored separately by the periods-count knob in
//     openMalgoCapture, which controls the depth of the ALSA ring.
//   - framesPerBuffer < 0 → pass 0 to malgo, letting miniaudio pick a
//     period aligned with the native ALSA period.
//   - framesPerBuffer > 0 → passthrough verbatim.
//
// buildCapturePeriods derives the ALSA periods count from
// CaptureLatencyMs. The total ring depth is periodFrames * periods,
// so we pick the smallest count that gives at least latencyMs of
// headroom. Floor of 3 (miniaudio's default) and a ceiling of 16 to
// keep the worst-case latency bounded if an operator sets a huge value.
func buildCapturePeriods(latencyMs, periodFrames int) int {
	const (
		minPeriods = 3
		maxPeriods = 16
	)

	if latencyMs <= 0 || periodFrames <= 0 {
		return minPeriods
	}

	periodMs := periodFrames * 1000 / audiopool.SampleRate
	if periodMs <= 0 {
		return minPeriods
	}

	periods := (latencyMs + periodMs - 1) / periodMs
	if periods < minPeriods {
		return minPeriods
	}

	if periods > maxPeriods {
		return maxPeriods
	}

	return periods
}

func buildCapturePeriodFrames(framesPerBuffer, _ int) int {
	switch {
	case framesPerBuffer == 0:
		return audiopool.FrameSize
	case framesPerBuffer < 0:
		return 0
	default:
		return framesPerBuffer
	}
}

// computePlaybackPeriodFrames translates the PlaybackLatencyMs config
// knob into a malgo PeriodSizeInFrames value. The rules mirror the
// prior PortAudio floor logic:
//
//   - latencyMs <= 0 → audiopool.FrameSize (20 ms) as a safe default
//   - latencyMs > 0  → the equivalent frame count at audiopool.SampleRate
//
// miniaudio may still round the period to a backend-preferred value at
// InitDevice time, but the requested period is what we log.
// malgoLowLatencyPeriods matches miniaudio's LowLatency performance
// profile default. The ALSA backend queues this many periods in the
// playback ring before the DAC consumes them; the beep-emergence
// settle wait must cover the full ring, not just one period.
const malgoLowLatencyPeriods = 3

// nextPow2 rounds v up to the next power of two. Returns 1 for v == 0
// and v unchanged when already a power of two. Used to model the ALSA
// USB-audio-class period rounding described above on PlaybackOutputLatency.
func nextPow2(v uint32) uint32 {
	if v == 0 {
		return 1
	}

	if v&(v-1) == 0 {
		return v
	}

	v--
	v |= v >> 1
	v |= v >> 2
	v |= v >> 4
	v |= v >> 8
	v |= v >> 16

	return v + 1
}

func computePlaybackPeriodFrames(latencyMs int) int {
	if latencyMs <= 0 {
		return audiopool.FrameSize
	}

	return latencyMs * audiopool.SampleRate / 1000
}

// StartHardware initializes the malgo context, opens the broadcast and
// per-port playback streams, starts them (always-on capture + playback),
// and returns a cleanup function that stops and closes them. The
// returned BroadcastEncoder is the same instance the parent runtime
// should install via CommsRuntime.SetBroadcast.
func (in *Init) StartHardware(slots []PortSlot) (broadcast *BroadcastEncoder, cleanup func(), err error) {
	logProc := func(message string) {
		// miniaudio emits transient ALSA recovery noise during USB audio
		// class startup and under normal scheduling jitter: "poll() failed"
		// and "EPIPE (read/write)" are both recovered internally via
		// snd_pcm_recover and do not correspond to lost audio. Drop them
		// so operators are not misled into chasing a non-issue; anything
		// else from malgo still lands at Trace level.
		if strings.Contains(message, "poll() failed") || strings.Contains(message, "EPIPE") {
			return
		}

		in.Log.Trace().Str("source", "malgo").Msg(message)
	}

	ctx, ctxErr := malgo.InitContext(nil, malgo.ContextConfig{}, logProc)
	if ctxErr != nil {
		return nil, nil, fmt.Errorf("comms: failed to initialize malgo context: %w", ctxErr)
	}

	in.malgoCtx = ctx

	if in.Debug {
		device.LogAudioDevices(ctx, in.Log)
	}

	be, _, audioErr := in.BuildAudio(slots)
	if audioErr != nil {
		_ = ctx.Uninit()
		ctx.Free()

		in.malgoCtx = nil

		return nil, nil, fmt.Errorf("comms: failed to build audio streams: %w", audioErr)
	}

	in.currentBroadcast = be

	// Start the always-on broadcast capture stream. Under the unified
	// design the capture callback runs continuously from here until
	// cleanup — SetTxEnabled toggles whether captured frames reach
	// the encoder, not whether the stream itself is running.
	if startErr := be.Start(); startErr != nil {
		_ = be.Close()
		in.currentBroadcast = nil

		for _, s := range in.playbackStreams {
			_ = s.Close()
		}

		in.playbackStreams = nil

		_ = ctx.Uninit()
		ctx.Free()

		in.malgoCtx = nil

		return nil, nil, fmt.Errorf("comms: failed to start broadcast stream: %w", startErr)
	}

	// Start the per-port playback streams that BuildAudio installed. On
	// any failure, stop and close every stream we already started plus
	// the broadcast stream before propagating the error.
	for idx, stream := range in.playbackStreams {
		if startErr := stream.Start(); startErr != nil {
			for j := range idx {
				_ = in.playbackStreams[j].Stop()
			}

			for _, s := range in.playbackStreams {
				_ = s.Close()
			}

			in.playbackStreams = nil

			_ = be.Close()
			in.currentBroadcast = nil

			_ = ctx.Uninit()
			ctx.Free()

			in.malgoCtx = nil

			return nil, nil, fmt.Errorf("comms: failed to start playback stream: %w", startErr)
		}
	}

	cleanup = func() {
		for _, s := range in.playbackStreams {
			_ = s.Stop()
			_ = s.Close()
		}

		in.playbackStreams = nil

		if in.currentBroadcast != nil {
			_ = in.currentBroadcast.Close()
			in.currentBroadcast = nil
		}

		if in.malgoCtx != nil {
			_ = in.malgoCtx.Uninit()
			in.malgoCtx.Free()
			in.malgoCtx = nil
		}
	}

	return be, cleanup, nil
}
