// =============================================================================
// AudioFileTx.test.jsx — Tests for audio file TX panel component
// =============================================================================

import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, screen, fireEvent, act, cleanup } from '@testing-library/react';

// Mock service modules before importing component
vi.mock('../../services/audioFileTx.js', () => ({
  loadFile: vi.fn(),
  startPlayback: vi.fn(),
  stopPlayback: vi.fn(),
  isPlaying: vi.fn(() => false),
}));

vi.mock('../../services/audioEngine.js', () => ({
  getAudioContext: vi.fn(),
  getEncoder: vi.fn(),
  resetTxTimestamp: vi.fn(),
}));

import AudioFileTxPanel from '../../components/AudioFileTx.jsx';
import { loadFile, startPlayback, stopPlayback, isPlaying } from '../../services/audioFileTx.js';
import { getAudioContext, getEncoder, resetTxTimestamp } from '../../services/audioEngine.js';

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

const defaultProps = {
  onLog: vi.fn(),
  onPttSet: vi.fn(),
  txEnabled: { 1: true, 2: false, 3: false, 4: false, 5: false },
};

function renderPanel(overrides = {}) {
  return render(<AudioFileTxPanel {...defaultProps} {...overrides} />);
}

describe('TestAudioFileTxRender', () => {
  it('renders with play and stop buttons disabled', () => {
    renderPanel();
    const playBtn = screen.getByText('PLAY');
    const stopBtn = screen.getByText('STOP');
    expect(playBtn.disabled).toBe(true);
    expect(stopBtn.disabled).toBe(true);
  });

  it('renders loop toggle in off state', () => {
    const { container } = renderPanel();
    const toggle = container.querySelector('.audio-file-tx-loop');
    expect(toggle).toBeTruthy();
    expect(toggle.classList.contains('on')).toBe(false);
    expect(screen.getByText('Loop')).toBeTruthy();
  });
});

describe('TestAudioFileTxFileLoad', () => {
  it('enables play on successful file load', async () => {
    getAudioContext.mockReturnValue({});
    loadFile.mockResolvedValue({
      audioBuffer: { duration: 5.2, sampleRate: 48000 },
      name: 'test.wav',
      duration: 5.2,
      sampleRate: 48000,
    });

    renderPanel();
    const fileInput = document.querySelector('input[type="file"]');

    await act(async () => {
      fireEvent.change(fileInput, {
        target: { files: [new File(['audio'], 'test.wav', { type: 'audio/wav' })] },
      });
    });

    expect(screen.getByText('PLAY').disabled).toBe(false);
    expect(screen.getByText(/test\.wav/)).toBeTruthy();
  });

  it('shows error when audio not initialized', async () => {
    getAudioContext.mockReturnValue(null);
    renderPanel();
    const fileInput = document.querySelector('input[type="file"]');

    await act(async () => {
      fireEvent.change(fileInput, {
        target: { files: [new File(['audio'], 'test.wav', { type: 'audio/wav' })] },
      });
    });

    expect(screen.getByText('Audio not initialized')).toBeTruthy();
    expect(defaultProps.onLog).toHaveBeenCalledWith('Audio not initialized for file TX', 'err');
  });

  it('shows error on decode failure', async () => {
    getAudioContext.mockReturnValue({});
    loadFile.mockRejectedValue(new Error('Bad format'));
    renderPanel();
    const fileInput = document.querySelector('input[type="file"]');

    await act(async () => {
      fireEvent.change(fileInput, {
        target: { files: [new File(['audio'], 'bad.mp3', { type: 'audio/mp3' })] },
      });
    });

    expect(screen.getByText('Error: Bad format')).toBeTruthy();
    expect(screen.getByText('PLAY').disabled).toBe(true);
  });
});

describe('TestAudioFileTxPlay', () => {
  async function loadFileAndReady(props = {}) {
    getAudioContext.mockReturnValue({});
    getEncoder.mockReturnValue({ state: 'configured' });
    loadFile.mockResolvedValue({
      audioBuffer: { duration: 3.0, sampleRate: 48000 },
      name: 'clip.wav',
      duration: 3.0,
      sampleRate: 48000,
    });

    const result = renderPanel(props);
    const fileInput = document.querySelector('input[type="file"]');
    await act(async () => {
      fireEvent.change(fileInput, {
        target: { files: [new File(['audio'], 'clip.wav', { type: 'audio/wav' })] },
      });
    });
    return result;
  }

  it('logs error when no TX channels enabled', async () => {
    const onLog = vi.fn();
    await loadFileAndReady({
      onLog,
      txEnabled: { 1: false, 2: false, 3: false, 4: false, 5: false },
    });

    fireEvent.click(screen.getByText('PLAY'));
    expect(onLog).toHaveBeenCalledWith('No TX channels!', 'err');
    expect(startPlayback).not.toHaveBeenCalled();
  });

  it('logs error when encoder not available', async () => {
    const onLog = vi.fn();
    await loadFileAndReady({ onLog });

    // Override encoder to closed state after file is loaded
    getEncoder.mockReturnValue({ state: 'closed' });
    fireEvent.click(screen.getByText('PLAY'));
    expect(onLog).toHaveBeenCalledWith('Encoder not available', 'err');
  });

  it('starts playback with correct calls', async () => {
    const onPttSet = vi.fn();
    startPlayback.mockReturnValue(vi.fn());
    await loadFileAndReady({ onPttSet });

    fireEvent.click(screen.getByText('PLAY'));

    expect(onPttSet).toHaveBeenCalledWith(true);
    expect(resetTxTimestamp).toHaveBeenCalled();
    expect(startPlayback).toHaveBeenCalled();
    expect(screen.getByText('PLAY').disabled).toBe(true);
    expect(screen.getByText('STOP').disabled).toBe(false);
  });

  it('stops playback on stop click', async () => {
    const onPttSet = vi.fn();
    startPlayback.mockReturnValue(vi.fn());
    await loadFileAndReady({ onPttSet });

    fireEvent.click(screen.getByText('PLAY'));
    fireEvent.click(screen.getByText('STOP'));

    expect(stopPlayback).toHaveBeenCalled();
    expect(onPttSet).toHaveBeenCalledWith(false);
    expect(screen.getByText('PLAY').disabled).toBe(false);
  });
});

describe('TestAudioFileTxLoop', () => {
  it('toggles loop on click', () => {
    const { container } = renderPanel();
    const toggle = container.querySelector('.audio-file-tx-loop');
    const track = toggle.querySelector('.track');
    expect(toggle.classList.contains('on')).toBe(false);
    fireEvent.click(track);
    expect(toggle.classList.contains('on')).toBe(true);
    fireEvent.click(track);
    expect(toggle.classList.contains('on')).toBe(false);
  });
});

describe('TestAudioFileTxTimerCleanup', () => {
  it('clears the completion-poll interval on unmount', async () => {
    vi.useFakeTimers();
    try {
      getAudioContext.mockReturnValue({});
      getEncoder.mockReturnValue({ state: 'configured' });
      isPlaying.mockReturnValue(true);
      loadFile.mockResolvedValue({
        audioBuffer: { duration: 3.0, sampleRate: 48000 },
        name: 'clip.wav',
        duration: 3.0,
        sampleRate: 48000,
      });

      const { unmount } = renderPanel();
      const fileInput = document.querySelector('input[type="file"]');
      await act(async () => {
        fireEvent.change(fileInput, {
          target: { files: [new File(['audio'], 'clip.wav', { type: 'audio/wav' })] },
        });
      });

      fireEvent.click(screen.getByText('PLAY'));
      expect(vi.getTimerCount()).toBe(1); // completion poll armed

      unmount();

      // The poll must not survive unmount — a leaked interval firing after
      // jsdom teardown crashes the whole run with "window is not defined"
      // on slow CI runners.
      expect(vi.getTimerCount()).toBe(0);
    } finally {
      vi.useRealTimers();
    }
  });
});
