// =============================================================================
// audioEngine.js — Core audio engine for the OpenMANET Comms Bridge
// =============================================================================
//
// This module handles all audio I/O:
//   1. AudioContext creation at 48 kHz (the native Opus sample rate)
//   2. Gain node for speaker volume control
//   3. AudioWorklet with SharedArrayBuffer for glitch-free playback on the
//      audio thread, with ScriptProcessorNode as a fallback for browsers that
//      don't support AudioWorklet or SharedArrayBuffer
//   4. WebCodecs AudioDecoder (Opus → PCM) for received audio
//   5. WebCodecs AudioEncoder (PCM → Opus) for microphone input
//   6. Microphone stream acquisition and processing
//
// WHY WEBCODECS?
// WebCodecs AudioDecoder/AudioEncoder give us direct access to the Opus codec
// without needing a MediaRecorder or <audio> element dance.  This is critical
// for low-latency PTT radio: we need frame-by-frame decode with no buffering
// beyond our own jitter buffer.
//
// WHY AUDIOWORKLET + SHAREDARRAYBUFFER?
// The ScriptProcessorNode runs on the main thread and is deprecated.  Its
// audio callback can be delayed by DOM work, causing glitches.  AudioWorklet
// runs on a dedicated audio thread and reads from the SharedArrayBuffer ring
// with zero main-thread involvement during playback.

import { SAMPLE_RATE, PCM_RING_SIZE } from '../constants.js';
import {
  createRingBuffer,
  ringWrite,
  ringRead,
} from './ringBuffer.js';
import { appendReplay } from './replayBuffer.js';

// -----------------------------------------------------------------------------
// Module state
// -----------------------------------------------------------------------------

let audioCtx = null;
let gainNode = null;
let rxLimiter = null;      // DynamicsCompressorNode after gainNode — lets the
                           // speaker gain run past unity without hard clipping
let decoderNode = null;    // AudioWorkletNode or ScriptProcessorNode
let opusDecoder = null;    // WebCodecs AudioDecoder
let opusEncoder = null;    // WebCodecs AudioEncoder
let micStream = null;      // MediaStream from getUserMedia
let micSource = null;      // MediaStreamAudioSourceNode
let micGainNode = null;    // GainNode applying mic gain in the audio graph
let micLimiter = null;     // DynamicsCompressorNode after micGainNode
let micProcessor = null;   // ScriptProcessorNode for mic input
let micSilentGain = null;  // Silent gain node keeping micProcessor alive
let txTimestamp = 0;       // Microsecond timestamp for AudioEncoder

// Limiter settings shared by the RX (speaker) and TX (mic) paths: a
// high-ratio compressor with a fast attack acts as a safety limiter, so
// gains above 1.0 compress gracefully instead of clipping at the DAC or
// the Opus encoder. Signals below -6 dBFS pass through unaffected.
function configureLimiter(limiter) {
  limiter.threshold.value = -6;
  limiter.knee.value = 6;
  limiter.ratio.value = 12;
  limiter.attack.value = 0.003;
  limiter.release.value = 0.25;
  return limiter;
}

// Creates a configured limiter, or null on engines without
// DynamicsCompressorNode — callers fall back to a direct connection.
function createLimiter() {
  if (typeof audioCtx.createDynamicsCompressor !== 'function') return null;
  return configureLimiter(audioCtx.createDynamicsCompressor());
}

let useWorklet = false;    // true if AudioWorklet path succeeded
let ringState = null;      // { ringBuf, ring, state } from createRingBuffer
let dropWatchTimer = null;    // periodic ring diagnostic reporter
let lastDropCount = 0;        // last observed value of state[3]
let lastUnderrunSamples = 0;  // last observed value of state[4]

// RX pipeline counters — incremented in the hot path, sampled by the
// 2 s reporter to localize where the RX stream is bottlenecking.
//   rxFramesIn  — decodeAndPlay() entries (≈ websocket RX_AUDIO arrivals)
//   decodeIn    — successful opusDecoder.decode() calls
//   decodeOut   — AudioDecoder output callback fires (decoded PCM frames)
let rxFramesIn = 0;
let decodeIn = 0;
let decodeOut = 0;
let lastRxFramesIn = 0;
let lastDecodeIn = 0;
let lastDecodeOut = 0;

// Decoder state tracking — we need to know which channel and IP the current
// audio belongs to so the Whisper service can associate transcriptions.
let lastDecodeCh = 0;
let lastDecodeIP = '';
let rxTimestamp = 0;       // Microsecond timestamp fed to AudioDecoder

// Pending Rx frame buffer — holds frames that arrive before the Opus decoder
// is initialized (i.e. before the first user interaction triggers initAudio).
// Flushed once the decoder is ready.  Bounded to ~1 second of 20 ms frames.
const PENDING_RX_MAX = 50;
let pendingRxFrames = [];

// Log callback set by initAudio's caller.
let logFn = null;

// Callbacks for decoded PCM — set by the consumer for waveform visualization
// and whisper feeding.
let onDecodedPcm = null;

// -----------------------------------------------------------------------------
// initAudio(onLog, { onPcm })
// -----------------------------------------------------------------------------
// Initializes the entire audio pipeline.  Must be called from a user gesture
// (click/touch/keydown) to satisfy browser autoplay policies.
//
// Parameters:
//   onLog  — function(msg, cls) for status messages
//   opts.onPcm — optional callback(pcm, ch, srcIP) called with each decoded
//                PCM frame for visualization and whisper feeding
//
// Returns: a state object (currently just { useWorklet }) for diagnostics.
// Subsequent calls are no-ops if the AudioContext is already created.
// The onPcm callback is always updated so consumers can refresh their
// closure (e.g. after a React re-render) without re-initialising audio.
export async function initAudio(onLog, opts = {}) {
  if (opts.onPcm) onDecodedPcm = opts.onPcm;

  if (audioCtx) return { useWorklet };

  logFn = onLog || (() => {});
  onDecodedPcm = opts.onPcm || null;

  // Create AudioContext at 48 kHz — the only sample rate Opus supports
  // natively.  This avoids any browser-side resampling on decode.
  audioCtx = new (window.AudioContext || window.webkitAudioContext)({
    sampleRate: SAMPLE_RATE,
  });

  // Master gain node for speaker volume.  Default is 80% (0.8).  The gain
  // feeds a limiter so slider values past 100% amplify cleanly instead of
  // clipping at the destination.
  gainNode = audioCtx.createGain();
  gainNode.gain.value = 0.8;
  rxLimiter = createLimiter();
  if (rxLimiter) {
    gainNode.connect(rxLimiter);
    rxLimiter.connect(audioCtx.destination);
  } else {
    gainNode.connect(audioCtx.destination);
  }

  // ── Try AudioWorklet + SharedArrayBuffer (preferred path) ──────────────
  // AudioWorklet runs the PCM reader on a dedicated audio thread, which is
  // immune to main-thread jank (DOM updates, JS execution, GC pauses).
  // SharedArrayBuffer lets the worklet read the ring buffer directly without
  // message-passing latency.
  useWorklet = false;
  if (typeof AudioWorkletNode !== 'undefined' && typeof SharedArrayBuffer !== 'undefined') {
    try {
      ringState = createRingBuffer(true);
      await audioCtx.audioWorklet.addModule('pcm-worklet.js');
      decoderNode = new AudioWorkletNode(audioCtx, 'pcm-worklet', {
        outputChannelCount: [1],
      });
      // Send the SharedArrayBuffers to the worklet so it can read from them.
      decoderNode.port.postMessage({
        type: 'init',
        ringBuf: ringState.ringBuf,
        stateBuf: ringState.state.buffer,
      });
      decoderNode.connect(gainNode);
      useWorklet = true;
      logFn('AudioWorklet active', 'info');
    } catch (e) {
      logFn(`Worklet unavailable (${e.message}), using ScriptProcessor`, 'info');
      useWorklet = false;
    }
  }

  // ── Fallback: ScriptProcessorNode ──────────────────────────────────────
  // Deprecated but universally supported.  Runs the PCM reader on the main
  // thread inside an onaudioprocess callback.
  if (!useWorklet) {
    ringState = createRingBuffer(false);
    decoderNode = audioCtx.createScriptProcessor(1024, 1, 1);
    decoderNode.onaudioprocess = (e) => {
      const out = e.outputBuffer.getChannelData(0);
      ringRead(ringState.ring, ringState.state, PCM_RING_SIZE, out, out.length);
    };
    decoderNode.connect(gainNode);
  }

  // ── WebCodecs AudioDecoder (Opus → PCM) ────────────────────────────────
  if (typeof AudioDecoder !== 'undefined') {
    try {
      opusDecoder = new AudioDecoder({
        output: (audioData) => {
          decodeOut++;
          // Extract PCM samples from the decoded AudioData.
          const pcm = new Float32Array(audioData.numberOfFrames);
          audioData.copyTo(pcm, { planeIndex: 0 });
          audioData.close();

          // Write decoded samples into the ring buffer for playback.
          ringWrite(ringState.ring, ringState.state, PCM_RING_SIZE, pcm);

          // Buffer for replay feature.
          if (lastDecodeCh) appendReplay(lastDecodeCh, pcm);

          // Notify consumer (waveform viz, whisper feeding, etc.)
          if (onDecodedPcm) {
            onDecodedPcm(pcm, lastDecodeCh, lastDecodeIP);
          }
        },
        error: (e) => logFn(`Decoder: ${e.message}`, 'err'),
      });
      opusDecoder.configure({
        codec: 'opus',
        sampleRate: SAMPLE_RATE,
        numberOfChannels: 1,
      });
      logFn('Opus decoder ready', 'info');
    } catch (e) {
      opusDecoder = null;
      logFn(`WebCodecs unavailable: ${e.message}`, 'err');
    }
  } else {
    logFn('WebCodecs not available — RX disabled', 'err');
  }

  // ── Ring diagnostic reporter ──────────────────────────────────────────
  // Polls the ring buffer's drop (state[3]) and underrun (state[4])
  // counters every 2 s. Drops mean the ring filled up; underruns mean
  // the reader hit an empty ring and had to zero-fill. Sustained values
  // of either indicate an RX pipeline problem the user would hear as
  // stutter.
  if (ringState && !dropWatchTimer) {
    lastDropCount = 0;
    lastUnderrunSamples = 0;
    dropWatchTimer = setInterval(() => {
      if (!ringState) return;
      const totalDrops = Atomics.load(ringState.state, 3);
      const totalUnderruns = Atomics.load(ringState.state, 4);
      const avail = (Atomics.load(ringState.state, 0)
        - Atomics.load(ringState.state, 1) + PCM_RING_SIZE) % PCM_RING_SIZE;

      const dropDelta = totalDrops - lastDropCount;
      const underrunDelta = totalUnderruns - lastUnderrunSamples;

      const rxDelta = rxFramesIn - lastRxFramesIn;
      const decInDelta = decodeIn - lastDecodeIn;
      const decOutDelta = decodeOut - lastDecodeOut;

      if (dropDelta > 0) {
        lastDropCount = totalDrops;
        logFn(`RX ring dropped ${dropDelta} frame(s) in last 2s (total=${totalDrops})`, 'warn');
      }
      if (underrunDelta > 0) {
        lastUnderrunSamples = totalUnderruns;
        const ms = (underrunDelta / SAMPLE_RATE * 1000).toFixed(1);
        logFn(`RX ring underran ${underrunDelta} samples (${ms} ms) in last 2s; occupancy=${avail}`, 'warn');
      }
      // Log stage counts whenever any RX activity is happening so we can
      // see where frames are disappearing: ws→decoder→ring.
      if (rxDelta > 0 || decInDelta > 0 || decOutDelta > 0 || underrunDelta > 0) {
        lastRxFramesIn = rxFramesIn;
        lastDecodeIn = decodeIn;
        lastDecodeOut = decodeOut;
        logFn(
          `RX stages 2s: ws=${rxDelta} decIn=${decInDelta} decOut=${decOutDelta} occupancy=${avail}`,
          'info',
        );
      }
    }, 2000);
  }

  // ── Flush any Rx frames that arrived before the decoder was ready ──────
  if (opusDecoder && pendingRxFrames.length > 0) {
    const queued = pendingRxFrames;
    pendingRxFrames = [];
    for (const frame of queued) {
      decodeAndPlay(frame.data, frame.ch, frame.srcIP);
    }
  }

  // ── WebCodecs AudioEncoder (PCM → Opus) ────────────────────────────────
  // The encoder's output callback is set up here but the actual sending of
  // encoded chunks happens via a callback passed to startMic().
  if (typeof AudioEncoder !== 'undefined') {
    try {
      opusEncoder = new AudioEncoder({
        output: (chunk) => {
          // The encoded Opus frame is delivered to the mic callback.
          // We store the callback reference in _onEncodedChunk.
          const buf = new ArrayBuffer(chunk.byteLength);
          chunk.copyTo(buf);
          if (_onEncodedChunk) _onEncodedChunk(buf);
        },
        error: (e) => logFn(`Encoder: ${e.message}`, 'err'),
      });
      opusEncoder.configure({
        codec: 'opus',
        sampleRate: SAMPLE_RATE,
        numberOfChannels: 1,
        bitrate: 32000,
        opus: { frameDuration: 20000 }, // 20 ms frames
      });
      logFn('Opus encoder ready', 'info');
    } catch (e) {
      opusEncoder = null;
      logFn(`Encoder unavailable: ${e.message}`, 'err');
    }
  }

  return { useWorklet };
}

// Holds the callback for encoded Opus chunks during mic capture or file TX.
let _onEncodedChunk = null;

// -----------------------------------------------------------------------------
// setEncoderCallback(callback)
// -----------------------------------------------------------------------------
// Sets the callback for encoded Opus chunks.  Used by audio file TX to wire
// up the encoder output to the WebSocket sender without starting the mic.
export function setEncoderCallback(callback) {
  _onEncodedChunk = callback;
}

// -----------------------------------------------------------------------------
// clearEncoderCallback()
// -----------------------------------------------------------------------------
// Clears the encoder callback.  Called when file TX stops.
export function clearEncoderCallback() {
  _onEncodedChunk = null;
}

// Mic gain (0-1), applied to PCM samples before encoding.
let micGain = 0.8;

// Selected device IDs.
let selectedMicId = '';

// -----------------------------------------------------------------------------
// enumerateDevices()
// -----------------------------------------------------------------------------
// Returns { inputs: [...], outputs: [...] } with available audio devices.
export async function enumerateDevices() {
  try {
    if (!navigator.mediaDevices) return { inputs: [], outputs: [] };
    const devices = await navigator.mediaDevices.enumerateDevices();
    return {
      inputs: devices.filter((d) => d.kind === 'audioinput'),
      outputs: devices.filter((d) => d.kind === 'audiooutput'),
    };
  } catch {
    return { inputs: [], outputs: [] };
  }
}

// -----------------------------------------------------------------------------
// setOutputDevice(deviceId)
// -----------------------------------------------------------------------------
// Switches the audio output to the specified device using setSinkId.
export async function setOutputDevice(deviceId) {
  if (audioCtx && typeof audioCtx.setSinkId === 'function') {
    try {
      await audioCtx.setSinkId(deviceId);
      return true;
    } catch (e) {
      if (logFn) logFn(`Output device error: ${e.message}`, 'err');
      return false;
    }
  }
  return false;
}

// -----------------------------------------------------------------------------
// setMicDevice(deviceId)
// -----------------------------------------------------------------------------
// Stores the preferred mic device ID. Takes effect on next startMic/startMicMonitor call.
export function setMicDevice(deviceId) {
  selectedMicId = deviceId;
}

// Returns the current selected mic device ID for getUserMedia constraints.
function getMicConstraints() {
  const constraints = {
    sampleRate: SAMPLE_RATE,
    channelCount: 1,
    echoCancellation: true,
    noiseSuppression: true,
    autoGainControl: true,
  };
  if (selectedMicId) {
    constraints.deviceId = { exact: selectedMicId };
  }
  return constraints;
}

// -----------------------------------------------------------------------------
// setMicGain(value)
// -----------------------------------------------------------------------------
// Sets the mic gain.  `value` is 0-200 (matching the range slider); values
// above 100 amplify past unity, limited by the mic-path limiter.
export function setMicGain(value) {
  micGain = value / 100;
  if (micGainNode) micGainNode.gain.value = micGain;
}

// Wires micSource → micGainNode → micLimiter → micProcessor.  The gain
// lives in the audio graph (not the copy loop) so amplification past
// unity is limited before it reaches the encoder, and the level meter
// sees exactly what will be encoded.
function connectMicChain() {
  micGainNode = audioCtx.createGain();
  micGainNode.gain.value = micGain;
  micSource.connect(micGainNode);

  micLimiter = createLimiter();
  if (micLimiter) {
    micGainNode.connect(micLimiter);
    micLimiter.connect(micProcessor);
  } else {
    micGainNode.connect(micProcessor);
  }
}

// Copies a ScriptProcessor input buffer.  The browser reuses the
// underlying buffer between callbacks, so a copy is required before
// handing samples to AudioData or the level meter.
function copyFrame(input) {
  const frame = new Float32Array(input.length);
  frame.set(input);
  return frame;
}

// -----------------------------------------------------------------------------
// decodeAndPlay(opusData, ch, srcIP)
// -----------------------------------------------------------------------------
// Decodes an Opus frame and writes the resulting PCM into the ring buffer.
//
// When the source (channel or IP) changes, the decoder timestamp resets to
// zero.  This is necessary because AudioDecoder uses timestamps to detect
// gaps and overlaps — stale timestamps from a previous source would confuse it.
export function decodeAndPlay(opusData, ch, srcIP) {
  rxFramesIn++;
  if (!opusDecoder || opusDecoder.state === 'closed') {
    // Buffer frames that arrive before the decoder is initialized so they
    // can be played back once initAudio() completes.
    if (!opusDecoder && pendingRxFrames.length < PENDING_RX_MAX) {
      pendingRxFrames.push({ data: opusData, ch, srcIP });
    }
    return;
  }

  // Reset decoder timestamp when the source changes to avoid feeding stale
  // timing info to the AudioDecoder.
  if (ch !== lastDecodeCh || srcIP !== lastDecodeIP) {
    rxTimestamp = 0;
  }
  lastDecodeCh = ch;
  lastDecodeIP = srcIP;

  try {
    opusDecoder.decode(
      new EncodedAudioChunk({
        type: 'key',
        timestamp: rxTimestamp,
        data: opusData,
      })
    );
    decodeIn++;
    // Advance timestamp by 20 ms (in microseconds) per Opus frame.
    rxTimestamp += 20000;
  } catch (e) {
    if (logFn) logFn(`Decode error: ${e.message}`, 'err');
  }
}

// -----------------------------------------------------------------------------
// startMic(onEncodedChunk, onMicLevel)
// -----------------------------------------------------------------------------
// Acquires the microphone and begins encoding PCM into Opus frames.
//
// Parameters:
//   onEncodedChunk — function(arrayBuffer) called with each encoded Opus frame
//   onMicLevel     — function(pcmFloat32Array) called with raw mic samples for
//                    level metering (before encoding)
//
// The mic uses echo cancellation, noise suppression, and auto gain control
// to clean up the signal before encoding.
export async function startMic(onEncodedChunk, onMicLevel) {
  _onEncodedChunk = onEncodedChunk;

  // If mic is already open (e.g. from VOX monitor mode), upgrade the
  // processor callback to encode + meter instead of meter-only.
  if (micStream) {
    if (micProcessor) {
      micProcessor.onaudioprocess = (e) => {
        if (!opusEncoder || opusEncoder.state === 'closed') return;
        const input = e.inputBuffer.getChannelData(0);
        // Gain is applied by micGainNode in the graph; just copy.
        const frame = copyFrame(input);
        if (onMicLevel) onMicLevel(frame);
        try {
          const ad = new AudioData({
            format: 'f32-planar',
            sampleRate: SAMPLE_RATE,
            numberOfFrames: frame.length,
            numberOfChannels: 1,
            timestamp: txTimestamp,
            data: frame,
          });
          txTimestamp += (input.length / SAMPLE_RATE) * 1000000;
          opusEncoder.encode(ad);
          ad.close();
        } catch { /* transient encoder errors */ }
      };
    }
    return;
  }

  if (!navigator.mediaDevices) {
    if (logFn) logFn('Mic unavailable: page must be served over HTTPS or accessed via localhost. For HTTP on a private network, launch Chromium with --unsafely-treat-insecure-origin-as-secure=<url>.', 'err');
    return;
  }

  try {
    micStream = await navigator.mediaDevices.getUserMedia({
      audio: getMicConstraints(),
    });

    micSource = audioCtx.createMediaStreamSource(micStream);

    // ScriptProcessorNode for mic capture.  We can't use AudioWorklet here
    // easily because we need to call the WebCodecs encoder synchronously
    // with each buffer, and the encoder lives on the main thread.
    micProcessor = audioCtx.createScriptProcessor(1024, 1, 1);
    micProcessor.onaudioprocess = (e) => {
      if (!opusEncoder || opusEncoder.state === 'closed') return;

      const input = e.inputBuffer.getChannelData(0);

      // Gain is applied by micGainNode in the graph; just copy.
      const gained = copyFrame(input);

      // Notify consumer for level metering (with gained audio).
      if (onMicLevel) onMicLevel(gained);

      try {
        const ad = new AudioData({
          format: 'f32-planar',
          sampleRate: SAMPLE_RATE,
          numberOfFrames: gained.length,
          numberOfChannels: 1,
          timestamp: txTimestamp,
          data: gained,
        });
        txTimestamp += (input.length / SAMPLE_RATE) * 1000000;
        opusEncoder.encode(ad);
        ad.close();
      } catch {
        // Silently ignore encoding errors — they're usually transient
        // (e.g., encoder reset during PTT release).
      }
    };

    connectMicChain();

    // Connect the processor to a silent gain node so the browser doesn't
    // garbage-collect the ScriptProcessorNode.  (A disconnected
    // ScriptProcessorNode may stop receiving events in some browsers.)
    micSilentGain = audioCtx.createGain();
    micSilentGain.gain.value = 0;
    micProcessor.connect(micSilentGain);
    micSilentGain.connect(audioCtx.destination);
  } catch (e) {
    if (logFn) logFn(`Mic error: ${e.message}`, 'err');
  }
}

// -----------------------------------------------------------------------------
// stopMic()
// -----------------------------------------------------------------------------
// Stops the microphone stream and disconnects all mic-related audio nodes.
// Safe to call even if the mic isn't active.
export function stopMic() {
  _onEncodedChunk = null;

  if (micSilentGain) {
    micSilentGain.disconnect();
    micSilentGain = null;
  }
  if (micProcessor) {
    micProcessor.disconnect();
    micProcessor = null;
  }
  if (micLimiter) {
    micLimiter.disconnect();
    micLimiter = null;
  }
  if (micGainNode) {
    micGainNode.disconnect();
    micGainNode = null;
  }
  if (micSource) {
    micSource.disconnect();
    micSource = null;
  }
  if (micStream) {
    // Stop all tracks to release the mic (turns off the browser's recording
    // indicator).
    micStream.getTracks().forEach((t) => t.stop());
    micStream = null;
  }
}

// -----------------------------------------------------------------------------
// setVolume(value)
// -----------------------------------------------------------------------------
// Sets the speaker volume.  `value` is 0-200 (matching the range slider);
// values above 100 amplify past unity, limited by the RX limiter.
export function setVolume(value) {
  if (gainNode) {
    gainNode.gain.value = value / 100;
  }
}

// -----------------------------------------------------------------------------
// getAudioContext()
// -----------------------------------------------------------------------------
// Returns the AudioContext instance.  Needed by audioFileTx to decode audio
// files using the same context (and therefore the same sample rate).
export function getAudioContext() {
  return audioCtx;
}

// -----------------------------------------------------------------------------
// getEncoder()
// -----------------------------------------------------------------------------
// Returns the Opus encoder instance.  Needed by audioFileTx to encode file
// audio frames for transmission.
export function getEncoder() {
  return opusEncoder;
}

// -----------------------------------------------------------------------------
// resetTxTimestamp()
// -----------------------------------------------------------------------------
// Resets the TX timestamp counter to zero.  Called when starting file playback
// or a new PTT session so encoder timestamps start fresh.
export function resetTxTimestamp() {
  txTimestamp = 0;
}

// -----------------------------------------------------------------------------
// playBuffer(pcm)
// -----------------------------------------------------------------------------
// Plays a Float32Array of PCM samples through the speaker gain node.
// Used by the replay feature to play back buffered audio.
// Returns the AudioBufferSourceNode so the caller can stop it if needed.
export function playBuffer(pcm) {
  if (!audioCtx || !gainNode || !pcm || pcm.length === 0) return null;
  const buf = audioCtx.createBuffer(1, pcm.length, SAMPLE_RATE);
  buf.getChannelData(0).set(pcm);
  const src = audioCtx.createBufferSource();
  src.buffer = buf;
  src.connect(gainNode);
  src.start();
  return src;
}

// -----------------------------------------------------------------------------
// startMicMonitor(onMicLevel)
// -----------------------------------------------------------------------------
// Opens the mic for level monitoring only (no encoding). Used by VOX mode
// to detect speech level without transmitting. Returns true if successful.
export async function startMicMonitor(onMicLevel) {
  if (micStream) return true; // mic already open

  if (!navigator.mediaDevices) return false;

  try {
    if (!audioCtx) return false;

    micStream = await navigator.mediaDevices.getUserMedia({
      audio: getMicConstraints(),
    });

    micSource = audioCtx.createMediaStreamSource(micStream);
    micProcessor = audioCtx.createScriptProcessor(1024, 1, 1);
    micProcessor.onaudioprocess = (e) => {
      const input = e.inputBuffer.getChannelData(0);
      // Gain is applied by micGainNode in the graph; just copy.
      if (onMicLevel) onMicLevel(copyFrame(input));
    };

    connectMicChain();
    micSilentGain = audioCtx.createGain();
    micSilentGain.gain.value = 0;
    micProcessor.connect(micSilentGain);
    micSilentGain.connect(audioCtx.destination);
    return true;
  } catch (e) {
    if (logFn) logFn(`Mic monitor error: ${e.message}`, 'err');
    return false;
  }
}

// -----------------------------------------------------------------------------
// isMicActive()
// -----------------------------------------------------------------------------
// Returns true if the mic stream is currently open.
export function isMicActive() {
  return !!micStream;
}
