// =============================================================================
// AudioControls.test.jsx — Tests for speaker/mic volume sliders + VOX
// =============================================================================

import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import AudioControls from '../../components/AudioControls.jsx';

function renderControls(overrides = {}) {
  const defaultProps = {
    speakerVol: 75,
    micVol: 50,
    onSpeakerChange: vi.fn(),
    onMicChange: vi.fn(),
    voxEnabled: false,
    voxThreshold: 0.15,
    onVoxToggle: vi.fn(),
    onVoxThresholdChange: vi.fn(),
    ...overrides,
  };
  return { ...render(<AudioControls {...defaultProps} />), props: defaultProps };
}

describe('TestAudioControlsRender', () => {
  it('renders speaker and mic sliders', () => {
    renderControls();
    expect(screen.getByText('Speaker')).toBeTruthy();
    expect(screen.getByText('Mic Gain')).toBeTruthy();

    const sliders = screen.getAllByRole('slider');
    expect(sliders.length).toBeGreaterThanOrEqual(2);
  });

  it('sets correct initial values', () => {
    renderControls({ speakerVol: 80, micVol: 40 });
    const speaker = screen.getByLabelText('Speaker');
    const mic = screen.getByLabelText('Mic Gain');
    expect(speaker.value).toBe('80');
    expect(mic.value).toBe('40');
  });

  it('sliders range to 200 for headroom past unity', () => {
    renderControls({ speakerVol: 150, micVol: 150 });
    const speaker = screen.getByLabelText('Speaker');
    const mic = screen.getByLabelText('Mic Gain');
    expect(speaker.max).toBe('200');
    expect(mic.max).toBe('200');
    expect(speaker.value).toBe('150');
    expect(mic.value).toBe('150');
  });
});

describe('TestAudioControlsOnChange', () => {
  it('calls onSpeakerChange with correct value', () => {
    const { props } = renderControls();
    const speaker = screen.getByLabelText('Speaker');
    fireEvent.change(speaker, { target: { value: '90' } });
    expect(props.onSpeakerChange).toHaveBeenCalledWith(90);
  });

  it('calls onMicChange with correct value', () => {
    const { props } = renderControls();
    const mic = screen.getByLabelText('Mic Gain');
    fireEvent.change(mic, { target: { value: '25' } });
    expect(props.onMicChange).toHaveBeenCalledWith(25);
  });

  it('passes numeric values not strings', () => {
    const { props } = renderControls();
    const speaker = screen.getByLabelText('Speaker');
    fireEvent.change(speaker, { target: { value: '0' } });
    expect(props.onSpeakerChange).toHaveBeenCalledWith(0);
    expect(typeof props.onSpeakerChange.mock.calls[0][0]).toBe('number');
  });
});

describe('TestAudioControlsVox', () => {
  it('renders VOX toggle label', () => {
    renderControls();
    expect(screen.getByText('VOX')).toBeTruthy();
  });

  it('VOX toggle reflects voxEnabled=true with "on" class', () => {
    const { container } = renderControls({ voxEnabled: true });
    const toggle = container.querySelector('.lat-toggle');
    expect(toggle.classList.contains('on')).toBe(true);
  });

  it('VOX toggle reflects voxEnabled=false without "on" class', () => {
    const { container } = renderControls({ voxEnabled: false });
    const toggle = container.querySelector('.lat-toggle');
    expect(toggle.classList.contains('on')).toBe(false);
  });

  it('calls onVoxToggle when VOX track clicked', () => {
    const { container, props } = renderControls();
    fireEvent.click(container.querySelector('.lat-toggle .track'));
    expect(props.onVoxToggle).toHaveBeenCalledWith(true);
  });

  it('shows threshold slider when VOX is enabled', () => {
    renderControls({ voxEnabled: true });
    expect(screen.getByText('Threshold')).toBeTruthy();
    const sliders = screen.getAllByRole('slider');
    // Speaker + Mic + VOX threshold = 3 sliders
    expect(sliders.length).toBe(3);
  });

  it('hides threshold slider when VOX is disabled', () => {
    renderControls({ voxEnabled: false });
    expect(screen.queryByText('Threshold')).toBeNull();
    const sliders = screen.getAllByRole('slider');
    expect(sliders.length).toBe(2);
  });
});

describe('TestAudioControlsDevices', () => {
  it('renders output device select when outputs are provided', () => {
    renderControls({
      audioDevices: {
        outputs: [{ deviceId: 'out-1', label: 'Speakers' }],
        inputs: [],
      },
    });
    expect(screen.getByText('Output')).toBeTruthy();
    // Open the dropdown to verify the device label is in the listbox.
    fireEvent.click(screen.getByRole('button', { name: 'Output' }));
    expect(screen.getByRole('option', { name: 'Speakers' })).toBeTruthy();
  });

  it('renders input device select when inputs are provided', () => {
    renderControls({
      audioDevices: {
        outputs: [],
        inputs: [{ deviceId: 'in-1', label: 'Built-in Mic' }],
      },
    });
    expect(screen.getByText('Input')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Input' }));
    expect(screen.getByRole('option', { name: 'Built-in Mic' })).toBeTruthy();
  });

  it('omits device selectors when no devices are available', () => {
    renderControls({ audioDevices: { outputs: [], inputs: [] } });
    expect(screen.queryByText('Output')).toBeNull();
    expect(screen.queryByText('Input')).toBeNull();
  });
});
