// =============================================================================
// Comms.test.jsx — smoke tests for the Push-to-talk page
// =============================================================================
//
// Comms wires together the WebSocket transport, the audio engine, whisper,
// the mesh status hook, and several sub-components. This suite mocks each
// dependency at the module boundary and verifies that:
//   - the page renders without crashing,
//   - the converted visibility-gated intervals do not fire while the tab is
//     reported hidden,
//   - the basic UI elements (PTT button, channel grid container) are present.

import React from 'react';
import { MemoryRouter } from 'react-router-dom';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent } from '@testing-library/react';
import { initAudio, startMic } from '../../services/audioEngine.js';
import { send, sendByte } from '../../services/websocketService.js';

// ---------- transport / engine / whisper / mesh stubs -----------------------

vi.mock('../../services/websocketService.js', () => ({
  connect: vi.fn(),
  disconnect: vi.fn(),
  setCallbacks: vi.fn(),
  sendToggle: vi.fn(),
  sendByte: vi.fn(),
  send: vi.fn(),
  isOpen: vi.fn(() => false),
}));

vi.mock('../../services/audioEngine.js', () => ({
  initAudio: vi.fn(async () => true),
  decodeAndPlay: vi.fn(),
  resetTxTimestamp: vi.fn(),
  startMic: vi.fn(),
  stopMic: vi.fn(),
  setVolume: vi.fn(),
  setMicGain: vi.fn(),
  playBuffer: vi.fn(),
  startMicMonitor: vi.fn(),
  enumerateDevices: vi.fn(async () => ({ inputs: [], outputs: [] })),
  setOutputDevice: vi.fn(),
  setMicDevice: vi.fn(),
  setEncoderCallback: vi.fn(),
  clearEncoderCallback: vi.fn(),
}));

vi.mock('../../services/whisperService.js', () => ({
  isReady: vi.fn(() => false),
  initWhisper: vi.fn(),
  feedAudio: vi.fn(),
  checkSilenceAndTranscribe: vi.fn(),
  checkWhisperAvailable: vi.fn(async () => false),
}));

vi.mock('../../services/commsApi.js', () => ({
  fetchCommsStatus: vi.fn(async () => ({ enabled: true, talkgroup: 1 })),
}));

vi.mock('../../services/replayBuffer.js', () => ({
  getReplayPcm: vi.fn(() => null),
}));

vi.mock('../../hooks/useMeshStatus.js', () => ({
  useMeshStatus: () => ({ status: { connected: true }, nodes: [] }),
}));

import CommsPage from '../../pages/Comms.jsx';

beforeEach(() => {
  vi.clearAllMocks();
  // matchMedia isn't implemented in jsdom; provide a permissive stub.
  if (!window.matchMedia) {
    window.matchMedia = vi.fn(() => ({
      matches: false,
      addListener: vi.fn(),
      removeListener: vi.fn(),
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      dispatchEvent: vi.fn(),
    }));
  }
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

describe('TestCommsRender', () => {
  it('renders the page without crashing and shows the PTT button', () => {
    const { container } = render(<MemoryRouter><CommsPage /></MemoryRouter>);
    expect(container.querySelector('.ptt-ring')).toBeInTheDocument();
    expect(container.querySelector('.ptt-ring').textContent).toMatch(/TX/);
  });

  it('shows WS DOWN in the topbar when not connected', () => {
    render(<MemoryRouter><CommsPage /></MemoryRouter>);
    expect(screen.getByText(/WS DOWN/i)).toBeInTheDocument();
  });

  it('shows the AUDIO and WHISPER toolbar actions', () => {
    render(<MemoryRouter><CommsPage /></MemoryRouter>);
    expect(screen.getByRole('button', { name: /^AUDIO$/i })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /^WHISPER$/i })).toBeInTheDocument();
  });

  it('starts listening without opening the microphone or sending voice', async () => {
    render(<MemoryRouter><CommsPage /></MemoryRouter>);
    expect(initAudio).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Start listening' }));
    await screen.findByText(/AUDIO ON/i);
    expect(initAudio).toHaveBeenCalledTimes(1);
    expect(startMic).not.toHaveBeenCalled();
    expect(send).not.toHaveBeenCalled();
    expect(sendByte).not.toHaveBeenCalled();
  });
});

describe('TestCommsIntervalsVisibilityGated', () => {
  it('does not fire any setInterval callback while the document is hidden', async () => {
    Object.defineProperty(document, 'visibilityState', {
      configurable: true,
      get: () => 'hidden',
    });
    vi.useFakeTimers();
    try {
      const { fetchCommsStatus } = await import('../../services/commsApi.js');
      const { checkSilenceAndTranscribe } = await import('../../services/whisperService.js');

      render(<MemoryRouter><CommsPage /></MemoryRouter>);

      // Allow any synchronous mount-time effects to run, then advance the
      // clock far enough to cover the longest converted interval (10s).
      vi.advanceTimersByTime(15000);

      // The visibility-gated wrapper must not have invoked the polling
      // callbacks while hidden. (Initial-mount calls also won't fire because
      // useVisibleInterval only ticks when visibilityState === 'visible'.)
      expect(fetchCommsStatus).not.toHaveBeenCalled();
      expect(checkSilenceAndTranscribe).not.toHaveBeenCalled();
    } finally {
      Object.defineProperty(document, 'visibilityState', {
        configurable: true,
        get: () => 'visible',
      });
    }
  });
});
