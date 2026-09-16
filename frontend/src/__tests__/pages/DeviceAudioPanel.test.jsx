// =============================================================================
// DeviceAudioPanel.test.jsx — Device hardware mixer panel
// =============================================================================

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react';
import React from 'react';

const fetchAudioMixer = vi.fn();
const updateAudioMixer = vi.fn();

vi.mock('../../services/commsApi.js', () => ({
  fetchAudioMixer: (...a) => fetchAudioMixer(...a),
  updateAudioMixer: (...a) => updateAudioMixer(...a),
}));

import DeviceAudioPanel from '../../pages/DeviceAudioPanel.jsx';

const fullState = {
  available: true,
  speakerVolume: 70,
  micVolume: 55,
  agcEnabled: false,
  speakerControl: 'Master',
  micControl: 'Mic Capture Volume',
  agcControl: 'Auto Gain Control',
};

describe('TestDeviceAudioPanel', () => {
  beforeEach(() => {
    fetchAudioMixer.mockReset();
    updateAudioMixer.mockReset();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('renders sliders from polled state', async () => {
    fetchAudioMixer.mockResolvedValue(fullState);

    render(<DeviceAudioPanel />);

    await waitFor(() => {
      expect(screen.getByLabelText('Device speaker volume')).toHaveValue('70');
      expect(screen.getByLabelText('Device mic volume')).toHaveValue('55');
    });
  });

  it('shows the no-device empty state when unavailable', async () => {
    fetchAudioMixer.mockResolvedValue({ available: false });

    render(<DeviceAudioPanel />);

    await waitFor(() => {
      expect(screen.getByText('No audio device detected.')).toBeTruthy();
    });
  });

  it('shows an error alert when the fetch fails', async () => {
    fetchAudioMixer.mockResolvedValue(null);

    render(<DeviceAudioPanel />);

    await waitFor(() => {
      expect(screen.getByText(/request failed/i)).toBeTruthy();
    });
  });

  it('commits speaker volume on pointer release, not per tick', async () => {
    fetchAudioMixer.mockResolvedValue(fullState);
    updateAudioMixer.mockResolvedValue({ ...fullState, speakerVolume: 30 });

    render(<DeviceAudioPanel />);
    const slider = await screen.findByLabelText('Device speaker volume');

    fireEvent.change(slider, { target: { value: '30' } });
    expect(updateAudioMixer).not.toHaveBeenCalled();

    await act(async () => {
      fireEvent.pointerUp(slider);
    });

    expect(updateAudioMixer).toHaveBeenCalledWith({ speakerVolume: 30 });
  });

  it('toggles AGC with a single call', async () => {
    fetchAudioMixer.mockResolvedValue(fullState);
    updateAudioMixer.mockResolvedValue({ ...fullState, agcEnabled: true });

    render(<DeviceAudioPanel />);
    const toggle = await screen.findByRole('button', { name: /auto gain control/i });

    await act(async () => {
      fireEvent.click(toggle);
    });

    expect(updateAudioMixer).toHaveBeenCalledWith({ agcEnabled: true });
  });

  it('hides the mic slider when the control is absent', async () => {
    fetchAudioMixer.mockResolvedValue({ ...fullState, micVolume: undefined });

    render(<DeviceAudioPanel />);

    await waitFor(() => {
      expect(screen.getByLabelText('Device speaker volume')).toBeTruthy();
    });
    expect(screen.queryByLabelText('Device mic volume')).toBeNull();
  });

  it('committing the speaker slider does not clobber a concurrently mid-drag mic slider', async () => {
    fetchAudioMixer.mockResolvedValue(fullState);
    updateAudioMixer.mockResolvedValue({ ...fullState, speakerVolume: 20 });

    render(<DeviceAudioPanel />);
    const speakerSlider = await screen.findByLabelText('Device speaker volume');
    const micSlider = await screen.findByLabelText('Device mic volume');

    // Start (but don't release) a drag on the mic slider.
    fireEvent.change(micSlider, { target: { value: '10' } });
    expect(micSlider).toHaveValue('10');

    // Drag and release the speaker slider while the mic drag is still open.
    fireEvent.change(speakerSlider, { target: { value: '20' } });
    await act(async () => {
      fireEvent.pointerUp(speakerSlider);
    });

    expect(updateAudioMixer).toHaveBeenCalledWith({ speakerVolume: 20 });
    // The mic slider's in-progress drag value must survive the speaker commit.
    expect(micSlider).toHaveValue('10');
  });

  it('does not commit on a Tab key-up landing focus on the slider', async () => {
    fetchAudioMixer.mockResolvedValue(fullState);

    render(<DeviceAudioPanel />);
    const slider = await screen.findByLabelText('Device speaker volume');

    await act(async () => {
      fireEvent.keyUp(slider, { key: 'Tab' });
    });

    expect(updateAudioMixer).not.toHaveBeenCalled();
  });

  it('commits on a value-changing key-up such as ArrowUp', async () => {
    fetchAudioMixer.mockResolvedValue(fullState);
    updateAudioMixer.mockResolvedValue({ ...fullState, speakerVolume: 71 });

    render(<DeviceAudioPanel />);
    const slider = await screen.findByLabelText('Device speaker volume');

    fireEvent.change(slider, { target: { value: '71' } });
    await act(async () => {
      fireEvent.keyUp(slider, { key: 'ArrowUp' });
    });

    expect(updateAudioMixer).toHaveBeenCalledWith({ speakerVolume: 71 });
  });

  it('suppresses poll overwrites while a slider drag is active', async () => {
    vi.useFakeTimers();
    try {
      fetchAudioMixer.mockResolvedValue(fullState);

      render(<DeviceAudioPanel />);
      await act(async () => {}); // flush the immediate mount poll
      const slider = screen.getByLabelText('Device speaker volume');
      expect(slider).toHaveValue('70');

      // Open a drag, then let a poll land with a different hardware value.
      fireEvent.change(slider, { target: { value: '30' } });
      fetchAudioMixer.mockResolvedValue({ ...fullState, speakerVolume: 90 });
      await act(async () => {
        vi.advanceTimersByTime(5000);
      });

      // The in-progress drag must not be overwritten by the poll response.
      expect(slider).toHaveValue('30');
    } finally {
      vi.useRealTimers();
    }
  });

  it('reverts an interrupted drag on pointercancel and resumes polling', async () => {
    vi.useFakeTimers();
    try {
      fetchAudioMixer.mockResolvedValue(fullState);

      render(<DeviceAudioPanel />);
      await act(async () => {});
      const slider = screen.getByLabelText('Device speaker volume');

      // A touch drag interrupted by scroll fires pointercancel, never
      // pointerup: the drag must be discarded, not committed.
      fireEvent.change(slider, { target: { value: '30' } });
      await act(async () => {
        fireEvent.pointerCancel(slider);
      });

      expect(updateAudioMixer).not.toHaveBeenCalled();
      expect(slider).toHaveValue('70');

      // Polling must resume immediately afterward.
      fetchAudioMixer.mockResolvedValue({ ...fullState, speakerVolume: 90 });
      await act(async () => {
        vi.advanceTimersByTime(5000);
      });
      expect(slider).toHaveValue('90');
    } finally {
      vi.useRealTimers();
    }
  });

  it('cannot suppress polling forever after a drag ends without any release event', async () => {
    vi.useFakeTimers();
    try {
      fetchAudioMixer.mockResolvedValue(fullState);

      render(<DeviceAudioPanel />);
      await act(async () => {});
      const slider = screen.getByLabelText('Device speaker volume');

      // Simulate a pointerup that never landed on the input: the drag flag
      // stays set with no commit and no cancel.
      fireEvent.change(slider, { target: { value: '30' } });
      fetchAudioMixer.mockResolvedValue({ ...fullState, speakerVolume: 90 });

      // After enough consecutive suppressed polls the stale drag must be
      // dropped and the panel must converge to hardware state again.
      for (let i = 0; i < 4; i++) {
        await act(async () => {
          vi.advanceTimersByTime(5000);
        });
      }

      expect(slider).toHaveValue('90');
    } finally {
      vi.useRealTimers();
    }
  });

  it('renders the AGC toggle optimistically before the update RPC resolves', async () => {
    fetchAudioMixer.mockResolvedValue(fullState);
    let resolveUpdate;
    updateAudioMixer.mockImplementation(() => new Promise((resolve) => {
      resolveUpdate = resolve;
    }));

    render(<DeviceAudioPanel />);
    const toggle = await screen.findByRole('button', { name: /auto gain control/i });
    expect(toggle).toHaveAttribute('aria-pressed', 'false');

    fireEvent.click(toggle);

    // The optimistic new state must be visible immediately, before the
    // mocked updateAudioMixer promise ever resolves.
    await waitFor(() => {
      expect(toggle).toHaveAttribute('aria-pressed', 'true');
    });
    expect(updateAudioMixer).toHaveBeenCalledWith({ agcEnabled: true });

    await act(async () => {
      resolveUpdate({ ...fullState, agcEnabled: true });
    });
  });
});
