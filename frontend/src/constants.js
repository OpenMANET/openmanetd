// =============================================================================
// constants.js — Shared constants for the OpenMANET Comms Bridge WebUI
// =============================================================================
// Extracted from the monolithic index.html so every module imports from one
// source of truth.  Changing a value here propagates everywhere automatically.

// -----------------------------------------------------------------------------
// Audio configuration
// -----------------------------------------------------------------------------

// Native sample rate used by the AudioContext, Opus codec, and mic/speaker.
// 48 kHz is the only rate Opus supports natively without internal resampling.
export const SAMPLE_RATE = 48000;

// Whisper speech-to-text expects 16 kHz mono PCM.  We downsample from 48 kHz
// by a factor of 3 before feeding audio into the model.
export const WHISPER_RATE = 16000;

// Number of PCM samples in one 20 ms Opus frame at 48 kHz.
// (48000 samples/sec * 0.020 sec = 960 samples)
export const FRAME_SIZE = 960;

// -----------------------------------------------------------------------------
// Channel definitions
// -----------------------------------------------------------------------------
// Each channel maps to a UDP port pair on the Go backend (openmanetd).
// Odd-numbered ports are used for the RTP-like audio stream.
export const CHANNELS_DEF = [
  { ch: 1, port: 38801 },
  { ch: 2, port: 38803 },
  { ch: 3, port: 38805 },
  { ch: 4, port: 38807 },
  { ch: 5, port: 38809 },
];

// -----------------------------------------------------------------------------
// Ring buffer sizing for decoded PCM playback
// -----------------------------------------------------------------------------

// Total ring buffer capacity in samples.  Power of 2 so modulo can be replaced
// with bitwise AND for performance (though we use modulo for clarity).
// At 48 kHz this holds ~341 ms of audio — enough to absorb jitter without
// adding noticeable latency.
export const PCM_RING_SIZE = 16384;

// Number of samples that must accumulate in the ring before playback begins.
// This is 12 Opus frames (12 * 960 = 11520 samples = 240 ms). The RX
// delivery chain (backend jitter buffer → webaudio bridge → RPC stream
// → websocket hub → browser) is bursty: frames are coalesced at the
// webPlayoutLoop safety-poll tick (100 ms), then at TCP coalescing, then
// again by browser event-loop scheduling. A shallower cushion bottomed
// out the ring between bursts and produced the audible stutter. 240 ms
// of startup latency is still imperceptible for push-to-talk voice, and
// leaves ~100 ms of headroom in the 341 ms ring capacity.
export const JITTER_PREFILL = 11520;

// -----------------------------------------------------------------------------
// Whisper speech-to-text thresholds
// -----------------------------------------------------------------------------

// How long a channel must be silent before we consider the utterance "done" and
// send accumulated audio to Whisper for transcription.
export const WHISPER_SILENCE_MS = 1500;

// Minimum utterance length to bother transcribing (avoids noise / clicks).
export const WHISPER_MIN_SEC = 0.5;

// Maximum utterance length to transcribe in one go.  Whisper's tiny model
// handles up to 30 s well; beyond that accuracy degrades and latency spikes.
export const WHISPER_MAX_SEC = 30;

// -----------------------------------------------------------------------------
// RX waveform visualizer
// -----------------------------------------------------------------------------

// Number of peak-amplitude samples kept in the scrolling waveform display.
// At ~50 fps update rate, 200 samples gives ~4 seconds of visible history.
export const RX_WAVE_HISTORY = 200;

// VOX (voice-activated transmit) hangtime in milliseconds.
// PTT stays active for this duration after mic level drops below threshold.
export const VOX_HANGTIME_MS = 300;

// Number of mesh poll snapshots to keep for sparkline graphs.
// At 10s poll interval, 30 points = 5 minutes of history.
export const NEIGHBOR_HISTORY_LENGTH = 30;

// -----------------------------------------------------------------------------
// WebSocket binary message type bytes
// -----------------------------------------------------------------------------
// The first byte of every binary WebSocket message identifies its type.
// These constants match the Go backend's protocol definition.
export const MSG_TYPE = {
  RX_AUDIO:   0x01, // Server -> Client: received audio frame
  TX_AUDIO:   0x02, // Client -> Server: mic/file audio frame
  TX_TOGGLE:  0x03, // Client -> Server: toggle TX on a channel
  RX_TOGGLE:  0x04, // Client -> Server: toggle RX on a channel
  RX_ALL_ON:  0x05, // Client -> Server: enable RX on all channels
  RX_ALL_OFF: 0x06, // Client -> Server: disable RX on all channels
  TX_ALL_ON:  0x07, // Client -> Server: enable TX on all channels
  TX_ALL_OFF: 0x08, // Client -> Server: disable TX on all channels
  PTT_DOWN:   0x09, // Client -> Server: push-to-talk pressed
  PTT_UP:     0x0A, // Client -> Server: push-to-talk released
};

// -----------------------------------------------------------------------------
// Layout
// -----------------------------------------------------------------------------

// Viewport width below which the app shell switches to the bottom tab bar and
// every grid collapses to a single column. CSS cannot import this value, so
// each stylesheet repeats the literal `768px` with a comment naming this
// constant as the source of truth. constants.test.js pins this JS value, and
// a separate check in that same file reads every stylesheet under src/ and
// asserts each one that mentions `768px` spells it as `max-width:
// ${MOBILE_BREAKPOINT}px` — so the CSS half is pinned too, not just this one.
export const MOBILE_BREAKPOINT = 768;
