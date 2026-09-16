// =============================================================================
// audioEngine.test.js — Tests for the core audio engine
// =============================================================================

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

let engine;

// Reusable mock factories
function createMockGainNode() {
  return {
    gain: { value: 0 },
    connect: vi.fn(),
    disconnect: vi.fn(),
  };
}

function createMockCompressorNode() {
  return {
    threshold: { value: 0 },
    knee: { value: 0 },
    ratio: { value: 0 },
    attack: { value: 0 },
    release: { value: 0 },
    connect: vi.fn(),
    disconnect: vi.fn(),
  };
}

function createMockAudioContext() {
  const gainNode = createMockGainNode();
  return {
    sampleRate: 48000,
    destination: {},
    createGain: vi.fn(() => createMockGainNode()),
    createDynamicsCompressor: vi.fn(() => createMockCompressorNode()),
    createScriptProcessor: vi.fn(() => ({
      connect: vi.fn(),
      disconnect: vi.fn(),
      onaudioprocess: null,
    })),
    createMediaStreamSource: vi.fn(() => ({
      connect: vi.fn(),
      disconnect: vi.fn(),
    })),
    createBuffer: vi.fn(() => ({
      getChannelData: vi.fn(() => ({ set: vi.fn() })),
    })),
    createBufferSource: vi.fn(() => ({
      connect: vi.fn(),
      start: vi.fn(),
      stop: vi.fn(),
      buffer: null,
    })),
    audioWorklet: {
      addModule: vi.fn().mockRejectedValue(new Error('no worklet in test')),
    },
    _gainNode: gainNode,
  };
}

let mockCtx;

beforeEach(async () => {
  vi.resetModules();

  mockCtx = createMockAudioContext();
  vi.stubGlobal('AudioContext', vi.fn(function () { return mockCtx; }));
  vi.stubGlobal('webkitAudioContext', undefined);

  // AudioDecoder mock
  vi.stubGlobal('AudioDecoder', vi.fn(function (opts) {
    this._output = opts.output;
    this._error = opts.error;
    this.state = 'configured';
    this.configure = vi.fn();
    this.decode = vi.fn();
    this.close = vi.fn();
  }));

  // AudioEncoder mock
  vi.stubGlobal('AudioEncoder', vi.fn(function (opts) {
    this._output = opts.output;
    this._error = opts.error;
    this.state = 'configured';
    this.configure = vi.fn();
    this.encode = vi.fn();
    this.close = vi.fn();
  }));

  vi.stubGlobal('EncodedAudioChunk', vi.fn(function (opts) {
    this.type = opts.type;
    this.timestamp = opts.timestamp;
    this.data = opts.data;
    this.byteLength = opts.data?.byteLength || 0;
    this.copyTo = vi.fn((buf) => {
      new Uint8Array(buf).set(new Uint8Array(this.data));
    });
  }));

  vi.stubGlobal('AudioData', vi.fn(function (opts) {
    this.format = opts?.format;
    this.sampleRate = opts?.sampleRate;
    this.numberOfFrames = opts?.numberOfFrames;
    this.numberOfChannels = opts?.numberOfChannels;
    this.timestamp = opts?.timestamp;
    this.data = opts?.data;
    this.close = vi.fn();
    this.copyTo = vi.fn((dest) => {
      if (this.data) dest.set(this.data);
    });
  }));

  vi.stubGlobal('AudioWorkletNode', undefined);
  vi.stubGlobal('SharedArrayBuffer', undefined);

  vi.stubGlobal('navigator', {
    mediaDevices: {
      getUserMedia: vi.fn().mockResolvedValue({
        getTracks: () => [{ stop: vi.fn() }],
      }),
      enumerateDevices: vi.fn().mockResolvedValue([]),
    },
  });

  engine = await import('../services/audioEngine.js');
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

// ---------------------------------------------------------------------------
// initAudio
// ---------------------------------------------------------------------------

describe('TestAudioEngineInit', () => {
  it('creates AudioContext at 48kHz', async () => {
    await engine.initAudio(vi.fn());
    expect(AudioContext).toHaveBeenCalledWith({ sampleRate: 48000 });
  });

  it('is idempotent — second call returns same result', async () => {
    const result1 = await engine.initAudio(vi.fn());
    const result2 = await engine.initAudio(vi.fn());
    expect(AudioContext).toHaveBeenCalledTimes(1);
    expect(result1).toEqual(result2);
  });

  it('falls back to ScriptProcessor when no AudioWorklet', async () => {
    const logFn = vi.fn();
    const result = await engine.initAudio(logFn);
    expect(result.useWorklet).toBe(false);
    expect(mockCtx.createScriptProcessor).toHaveBeenCalled();
  });

  it('sets up Opus decoder', async () => {
    const logFn = vi.fn();
    await engine.initAudio(logFn);
    expect(AudioDecoder).toHaveBeenCalled();
    expect(logFn).toHaveBeenCalledWith('Opus decoder ready', 'info');
  });

  it('sets up Opus encoder', async () => {
    const logFn = vi.fn();
    await engine.initAudio(logFn);
    expect(AudioEncoder).toHaveBeenCalled();
    expect(logFn).toHaveBeenCalledWith('Opus encoder ready', 'info');
  });

  it('updates onPcm callback on subsequent initAudio calls', async () => {
    const firstPcm = vi.fn();
    const secondPcm = vi.fn();

    await engine.initAudio(vi.fn(), { onPcm: firstPcm });

    // Simulate a decoded frame — should call firstPcm
    const decoderInstance = AudioDecoder.mock.instances[0];
    const fakeAudioData = {
      numberOfFrames: 2,
      copyTo: vi.fn((dst) => { dst[0] = 0.5; dst[1] = -0.3; }),
      close: vi.fn(),
    };
    decoderInstance._output(fakeAudioData);
    expect(firstPcm).toHaveBeenCalledTimes(1);
    expect(secondPcm).not.toHaveBeenCalled();

    // Second initAudio call — should update the onPcm callback
    await engine.initAudio(vi.fn(), { onPcm: secondPcm });

    // AudioContext should NOT be recreated
    expect(AudioContext).toHaveBeenCalledTimes(1);

    // Simulate another decoded frame — should call secondPcm now
    decoderInstance._output(fakeAudioData);
    expect(secondPcm).toHaveBeenCalledTimes(1);
  });

  it('handles missing WebCodecs gracefully', async () => {
    vi.stubGlobal('AudioDecoder', undefined);
    vi.resetModules();
    const eng = await import('../services/audioEngine.js');

    const logFn = vi.fn();
    await eng.initAudio(logFn);
    expect(logFn).toHaveBeenCalledWith('WebCodecs not available — RX disabled', 'err');
  });
});

// ---------------------------------------------------------------------------
// setVolume / setMicGain
// ---------------------------------------------------------------------------

describe('TestAudioEngineVolume', () => {
  it('setVolume sets gain node value', async () => {
    await engine.initAudio(vi.fn());
    // The gain node is created via createGain, find it
    const gainCall = mockCtx.createGain.mock.results[0].value;
    engine.setVolume(50);
    expect(gainCall.gain.value).toBe(0.5);
  });

  it('setVolume above 100 amplifies past unity', async () => {
    await engine.initAudio(vi.fn());
    const gainCall = mockCtx.createGain.mock.results[0].value;
    engine.setVolume(150);
    expect(gainCall.gain.value).toBe(1.5);
  });

  it('setMicGain stores gain value', () => {
    // setMicGain doesn't need initAudio
    expect(() => engine.setMicGain(60)).not.toThrow();
  });
});

describe('TestAudioEngineRxLimiter', () => {
  it('initAudio routes speaker gain through a limiter to destination', async () => {
    await engine.initAudio(vi.fn());
    expect(mockCtx.createDynamicsCompressor).toHaveBeenCalledTimes(1);

    const gainCall = mockCtx.createGain.mock.results[0].value;
    const limiter = mockCtx.createDynamicsCompressor.mock.results[0].value;
    expect(gainCall.connect).toHaveBeenCalledWith(limiter);
    expect(limiter.connect).toHaveBeenCalledWith(mockCtx.destination);
  });

  it('falls back to a direct connection when the API is missing', async () => {
    delete mockCtx.createDynamicsCompressor;
    await engine.initAudio(vi.fn());
    const gainCall = mockCtx.createGain.mock.results[0].value;
    expect(gainCall.connect).toHaveBeenCalledWith(mockCtx.destination);
  });
});

describe('TestAudioEngineTxChain', () => {
  it('startMic wires source through gain and limiter into the processor', async () => {
    await engine.initAudio(vi.fn());
    await engine.startMic(vi.fn(), vi.fn());

    const source = mockCtx.createMediaStreamSource.mock.results[0].value;
    // createGain order: rx gain (init), tx gain, mic silent gain.
    const txGain = mockCtx.createGain.mock.results[1].value;
    // createDynamicsCompressor order: rx limiter (init), tx limiter.
    const txLimiter = mockCtx.createDynamicsCompressor.mock.results[1].value;
    const procResults = mockCtx.createScriptProcessor.mock.results;
    const micProcessor = procResults[procResults.length - 1].value;

    expect(source.connect).toHaveBeenCalledWith(txGain);
    expect(txGain.connect).toHaveBeenCalledWith(txLimiter);
    expect(txLimiter.connect).toHaveBeenCalledWith(micProcessor);
  });

  it('setMicGain drives the live tx gain node past unity', async () => {
    await engine.initAudio(vi.fn());
    await engine.startMic(vi.fn(), vi.fn());

    const txGain = mockCtx.createGain.mock.results[1].value;
    engine.setMicGain(150);
    expect(txGain.gain.value).toBe(1.5);
  });

  it('onaudioprocess forwards samples without a second gain multiply', async () => {
    await engine.initAudio(vi.fn());
    const onMicLevel = vi.fn();
    engine.setMicGain(50); // 0.5 — applied by the graph node, not the copy loop
    await engine.startMic(vi.fn(), onMicLevel);

    const procResults = mockCtx.createScriptProcessor.mock.results;
    const micProcessor = procResults[procResults.length - 1].value;

    const input = new Float32Array(4).fill(0.5);
    micProcessor.onaudioprocess({
      inputBuffer: { getChannelData: () => input },
    });

    expect(onMicLevel).toHaveBeenCalledTimes(1);
    const seen = onMicLevel.mock.calls[0][0];
    expect(seen[0]).toBeCloseTo(0.5); // not 0.25: gain lives in the graph now
  });
});

// ---------------------------------------------------------------------------
// decodeAndPlay
// ---------------------------------------------------------------------------

describe('TestAudioEngineDecodeAndPlay', () => {
  it('calls decoder.decode with EncodedAudioChunk', async () => {
    await engine.initAudio(vi.fn());
    const opusData = new Uint8Array([1, 2, 3]);
    engine.decodeAndPlay(opusData, 1, '10.0.0.1');
    expect(EncodedAudioChunk).toHaveBeenCalledWith(
      expect.objectContaining({ type: 'key', timestamp: 0 })
    );
  });

  it('resets timestamp on source change', async () => {
    await engine.initAudio(vi.fn());
    engine.decodeAndPlay(new Uint8Array([1]), 1, '10.0.0.1');
    engine.decodeAndPlay(new Uint8Array([2]), 1, '10.0.0.1');
    // Second call should have timestamp 20000 (20ms in microseconds)
    const secondChunk = EncodedAudioChunk.mock.calls[1][0];
    expect(secondChunk.timestamp).toBe(20000);

    // Change source — timestamp resets
    engine.decodeAndPlay(new Uint8Array([3]), 2, '10.0.0.2');
    const thirdChunk = EncodedAudioChunk.mock.calls[2][0];
    expect(thirdChunk.timestamp).toBe(0);
  });
});

// ---------------------------------------------------------------------------
// Pending Rx frame buffer (pre-init buffering)
// ---------------------------------------------------------------------------

describe('TestAudioEnginePendingRxBuffer', () => {
  it('buffers frames when decoder is not initialized', () => {
    // Before initAudio, decodeAndPlay should buffer, not throw
    engine.decodeAndPlay(new Uint8Array([1, 2, 3]), 1, '10.0.0.1');
    engine.decodeAndPlay(new Uint8Array([4, 5, 6]), 2, '10.0.0.2');

    // Frames were not decoded (no decoder yet)
    expect(EncodedAudioChunk).not.toHaveBeenCalled();
  });

  it('flushes buffered frames after initAudio', async () => {
    // Queue frames before decoder exists
    engine.decodeAndPlay(new Uint8Array([1]), 1, '10.0.0.1');
    engine.decodeAndPlay(new Uint8Array([2]), 2, '10.0.0.2');
    engine.decodeAndPlay(new Uint8Array([3]), 1, '10.0.0.3');

    // Now initialize — should flush all 3 buffered frames
    await engine.initAudio(vi.fn());

    // The decoder should have been called once per buffered frame
    const decoderInstance = AudioDecoder.mock.instances[0];
    expect(decoderInstance.decode).toHaveBeenCalledTimes(3);
  });

  it('flushes frames in order preserving channel and srcIP', async () => {
    engine.decodeAndPlay(new Uint8Array([10]), 1, '10.0.0.1');
    engine.decodeAndPlay(new Uint8Array([20]), 3, '10.0.0.5');

    await engine.initAudio(vi.fn());

    // Each flushed frame creates an EncodedAudioChunk
    expect(EncodedAudioChunk).toHaveBeenCalledTimes(2);

    // First frame: timestamp resets to 0 (new source)
    expect(EncodedAudioChunk.mock.calls[0][0].timestamp).toBe(0);

    // Second frame: source changes (ch 1→3, IP changes), timestamp resets to 0
    expect(EncodedAudioChunk.mock.calls[1][0].timestamp).toBe(0);
  });

  it('caps the pending buffer at 50 frames', () => {
    // Queue 60 frames — only the first 50 should be buffered
    for (let i = 0; i < 60; i++) {
      engine.decodeAndPlay(new Uint8Array([i]), 1, '10.0.0.1');
    }

    // No decoding yet (decoder is null)
    expect(EncodedAudioChunk).not.toHaveBeenCalled();
  });

  it('decodes only 50 frames after init when 60 were queued', async () => {
    for (let i = 0; i < 60; i++) {
      engine.decodeAndPlay(new Uint8Array([i]), 1, '10.0.0.1');
    }

    await engine.initAudio(vi.fn());

    const decoderInstance = AudioDecoder.mock.instances[0];
    expect(decoderInstance.decode).toHaveBeenCalledTimes(50);
  });

  it('does not buffer frames when decoder is closed', async () => {
    await engine.initAudio(vi.fn());

    const decoderInstance = AudioDecoder.mock.instances[0];

    // Simulate a normal decode first
    engine.decodeAndPlay(new Uint8Array([1]), 1, '10.0.0.1');
    expect(decoderInstance.decode).toHaveBeenCalledTimes(1);

    // Now mark decoder as closed — frames should be dropped, not buffered
    decoderInstance.state = 'closed';
    engine.decodeAndPlay(new Uint8Array([2]), 1, '10.0.0.1');

    // Still only 1 call — the closed-state frame was dropped (not buffered
    // because opusDecoder is truthy but closed, distinct from null)
    expect(decoderInstance.decode).toHaveBeenCalledTimes(1);
  });

  it('clears the pending buffer after flush', async () => {
    engine.decodeAndPlay(new Uint8Array([1]), 1, '10.0.0.1');

    await engine.initAudio(vi.fn());

    const decoderInstance = AudioDecoder.mock.instances[0];
    expect(decoderInstance.decode).toHaveBeenCalledTimes(1);

    // Decode another frame post-init — should NOT replay the buffered frame
    engine.decodeAndPlay(new Uint8Array([2]), 1, '10.0.0.1');
    expect(decoderInstance.decode).toHaveBeenCalledTimes(2);
  });

  it('does not flush if decoder creation fails', async () => {
    // Make AudioDecoder constructor throw so opusDecoder stays null
    vi.stubGlobal('AudioDecoder', vi.fn(() => {
      throw new Error('codec init failed');
    }));
    vi.resetModules();
    const eng = await import('../services/audioEngine.js');

    // Queue a frame
    eng.decodeAndPlay(new Uint8Array([1]), 1, '10.0.0.1');

    // Init — decoder creation fails, flush should not run
    await eng.initAudio(vi.fn());

    // No EncodedAudioChunk should have been created
    expect(EncodedAudioChunk).not.toHaveBeenCalled();
  });

  it('post-init frames go directly to decoder without buffering', async () => {
    await engine.initAudio(vi.fn());

    const decoderInstance = AudioDecoder.mock.instances[0];

    engine.decodeAndPlay(new Uint8Array([1]), 1, '10.0.0.1');
    engine.decodeAndPlay(new Uint8Array([2]), 1, '10.0.0.1');
    engine.decodeAndPlay(new Uint8Array([3]), 1, '10.0.0.1');

    // All 3 should decode immediately
    expect(decoderInstance.decode).toHaveBeenCalledTimes(3);

    // Timestamps should increment: 0, 20000, 40000
    expect(EncodedAudioChunk.mock.calls[0][0].timestamp).toBe(0);
    expect(EncodedAudioChunk.mock.calls[1][0].timestamp).toBe(20000);
    expect(EncodedAudioChunk.mock.calls[2][0].timestamp).toBe(40000);
  });

  it('timestamp resets properly for flushed frames from different sources', async () => {
    // Queue frames from two different sources
    engine.decodeAndPlay(new Uint8Array([1]), 1, '10.0.0.1');
    engine.decodeAndPlay(new Uint8Array([2]), 1, '10.0.0.1');
    engine.decodeAndPlay(new Uint8Array([3]), 2, '10.0.0.5');

    await engine.initAudio(vi.fn());

    // Frame 1: ch=1, new source → timestamp=0
    expect(EncodedAudioChunk.mock.calls[0][0].timestamp).toBe(0);
    // Frame 2: same source → timestamp=20000
    expect(EncodedAudioChunk.mock.calls[1][0].timestamp).toBe(20000);
    // Frame 3: different source → timestamp resets to 0
    expect(EncodedAudioChunk.mock.calls[2][0].timestamp).toBe(0);
  });
});

// ---------------------------------------------------------------------------
// enumerateDevices
// ---------------------------------------------------------------------------

describe('TestAudioEngineEnumerateDevices', () => {
  it('separates inputs and outputs', async () => {
    navigator.mediaDevices.enumerateDevices.mockResolvedValue([
      { kind: 'audioinput', deviceId: 'mic1', label: 'Mic' },
      { kind: 'audiooutput', deviceId: 'spk1', label: 'Speaker' },
      { kind: 'videoinput', deviceId: 'cam1', label: 'Camera' },
    ]);

    const result = await engine.enumerateDevices();
    expect(result.inputs).toHaveLength(1);
    expect(result.outputs).toHaveLength(1);
    expect(result.inputs[0].deviceId).toBe('mic1');
    expect(result.outputs[0].deviceId).toBe('spk1');
  });

  it('returns empty arrays when mediaDevices unavailable', async () => {
    vi.stubGlobal('navigator', {});
    vi.resetModules();
    const eng = await import('../services/audioEngine.js');

    const result = await eng.enumerateDevices();
    expect(result).toEqual({ inputs: [], outputs: [] });
  });
});

// ---------------------------------------------------------------------------
// startMic / stopMic
// ---------------------------------------------------------------------------

describe('TestAudioEngineMic', () => {
  it('startMic acquires microphone with constraints', async () => {
    await engine.initAudio(vi.fn());
    await engine.startMic(vi.fn(), vi.fn());
    expect(navigator.mediaDevices.getUserMedia).toHaveBeenCalledWith({
      audio: expect.objectContaining({
        echoCancellation: true,
        noiseSuppression: true,
        autoGainControl: true,
      }),
    });
  });

  it('stopMic releases mic tracks', async () => {
    const mockTrack = { stop: vi.fn() };
    navigator.mediaDevices.getUserMedia.mockResolvedValue({
      getTracks: () => [mockTrack],
    });

    await engine.initAudio(vi.fn());
    await engine.startMic(vi.fn(), vi.fn());
    engine.stopMic();
    expect(mockTrack.stop).toHaveBeenCalled();
  });

  it('startMic logs error and returns when mediaDevices unavailable', async () => {
    vi.stubGlobal('navigator', {});
    vi.resetModules();
    const eng = await import('../services/audioEngine.js');

    const logFn = vi.fn();
    await eng.initAudio(logFn);
    await eng.startMic(vi.fn(), vi.fn());
    expect(logFn).toHaveBeenCalledWith(
      expect.stringContaining('Mic unavailable'),
      'err'
    );
  });

  it('startMicMonitor returns false when mediaDevices unavailable', async () => {
    vi.stubGlobal('navigator', {});
    vi.resetModules();
    const eng = await import('../services/audioEngine.js');
    await eng.initAudio(vi.fn());

    const result = await eng.startMicMonitor(vi.fn());
    expect(result).toBe(false);
  });
});

// ---------------------------------------------------------------------------
// playBuffer
// ---------------------------------------------------------------------------

describe('TestAudioEnginePlayBuffer', () => {
  it('creates buffer source and plays', async () => {
    await engine.initAudio(vi.fn());
    const pcm = new Float32Array([0.1, 0.2, 0.3]);
    const src = engine.playBuffer(pcm);
    expect(mockCtx.createBuffer).toHaveBeenCalledWith(1, 3, 48000);
    expect(mockCtx.createBufferSource).toHaveBeenCalled();
    expect(src).toBeTruthy();
  });

  it('returns null for empty pcm', async () => {
    await engine.initAudio(vi.fn());
    expect(engine.playBuffer(null)).toBeNull();
    expect(engine.playBuffer(new Float32Array(0))).toBeNull();
  });
});

// ---------------------------------------------------------------------------
// resetTxTimestamp
// ---------------------------------------------------------------------------

describe('TestAudioEngineResetTxTimestamp', () => {
  it('resets without error', () => {
    expect(() => engine.resetTxTimestamp()).not.toThrow();
  });
});

// ---------------------------------------------------------------------------
// encoder callback
// ---------------------------------------------------------------------------

describe('TestAudioEngineEncoderCallback', () => {
  it('set and clear encoder callback', () => {
    const cb = vi.fn();
    engine.setEncoderCallback(cb);
    engine.clearEncoderCallback();
    // No direct assertion on internal state, but ensure no errors
  });
});

// ---------------------------------------------------------------------------
// getAudioContext / getEncoder
// ---------------------------------------------------------------------------

describe('TestAudioEngineGetters', () => {
  it('getAudioContext returns null before init', () => {
    expect(engine.getAudioContext()).toBeNull();
  });

  it('getAudioContext returns context after init', async () => {
    await engine.initAudio(vi.fn());
    expect(engine.getAudioContext()).toBeTruthy();
  });

  it('getEncoder returns encoder after init', async () => {
    await engine.initAudio(vi.fn());
    expect(engine.getEncoder()).toBeTruthy();
    expect(engine.getEncoder().state).toBe('configured');
  });
});
