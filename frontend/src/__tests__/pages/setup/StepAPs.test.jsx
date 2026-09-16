// =============================================================================
// StepAPs.test.jsx — per-radio mode selector and AP encryption choices
// =============================================================================
//
// Pins (a) the encryption options the wizard offers per AP radio — the
// LuCI mesh wizard's psk2 / sae-mixed / sae plus the open modes, never
// psk-mixed — and (b) the Off / Access point / Mesh backhaul selector:
// "Mesh backhaul" appears only when the device reports the radio as
// capable (SetupRadio.supports_mesh_backhaul), and choosing it swaps
// the AP form for the backhaul mesh ID + passphrase fields.

import React, { useEffect } from 'react';
import { describe, it, expect, afterEach } from 'vitest';
import { render, screen, fireEvent, cleanup, within } from '@testing-library/react';

import StepAPs from '../../../pages/setup/StepAPs.jsx';
import { SetupProvider, useSetup, SETUP_ACTIONS } from '../../../contexts/SetupContext.jsx';

const STATUS = {
  radios: [
    { name: 'radio0', band: '2g', isHalow: false, hardwareName: 'MediaTek MT7915AN', supportsMeshBackhaul: true },
    { name: 'radio1', band: 's1g', isHalow: true },
    { name: 'radio2', band: '5g', isHalow: false, hardwareName: 'platform/soc/mmc1', supportsMeshBackhaul: false },
  ],
};

function renderStep() {
  return render(
    <SetupProvider>
      <StepAPs status={STATUS} />
    </SetupProvider>,
  );
}

function modeGroup(radio) {
  return screen.getByRole('radiogroup', { name: `Mode on ${radio}` });
}

function pickMode(radio, label) {
  fireEvent.click(within(modeGroup(radio)).getByRole('radio', { name: label }));
}

afterEach(cleanup);

describe('StepAPsEncryptionOptions', () => {
  it('offers SAE, PSK2, SAE_MIXED, OWE and NONE in that order', () => {
    renderStep();

    pickMode('radio0', 'Access point');
    fireEvent.click(screen.getByRole('button', { name: 'Encryption on radio0' }));

    const labels = screen.getAllByRole('option').map(o => o.textContent);
    expect(labels).toEqual([
      'WPA3 (SAE)',
      'WPA2 (PSK2)',
      'WPA2 / WPA3 (mixed)',
      'OWE (open, encrypted)',
      'None (open)',
    ]);
  });

  it('never offers the legacy WPA/WPA2 mixed mode', () => {
    renderStep();

    pickMode('radio0', 'Access point');
    fireEvent.click(screen.getByRole('button', { name: 'Encryption on radio0' }));

    expect(screen.queryByRole('option', { name: 'WPA / WPA2 (mixed, legacy)' })).toBeNull();
  });
});

describe('StepAPsRadioMode', () => {
  it('offers Mesh backhaul only on radios the device marks capable', () => {
    renderStep();

    expect(within(modeGroup('radio0')).queryByRole('radio', { name: 'Mesh backhaul' })).not.toBeNull();
    expect(within(modeGroup('radio2')).queryByRole('radio', { name: 'Mesh backhaul' })).toBeNull();
    expect(screen.queryByRole('radiogroup', { name: 'Mode on radio1' })).toBeNull(); // HaLow radio is not listed
  });

  it('starts every radio Off and shows the chipset next to it', () => {
    renderStep();

    expect(within(modeGroup('radio0')).getByRole('radio', { name: 'Off' }).getAttribute('aria-checked')).toBe('true');
    expect(screen.getByText('MediaTek MT7915AN')).toBeInTheDocument();
  });

  it('choosing Mesh backhaul shows mesh ID + passphrase and hides the AP form', () => {
    renderStep();

    pickMode('radio0', 'Mesh backhaul');

    expect(within(modeGroup('radio0')).getByRole('radio', { name: 'Mesh backhaul' }).getAttribute('aria-checked')).toBe('true');
    expect(screen.getByLabelText('Backhaul mesh ID')).toHaveValue('openmanet-2g');
    expect(screen.getByLabelText('Backhaul passphrase')).toBeInTheDocument();
    expect(screen.queryByLabelText('SSID')).toBeNull();
  });

  it('switching back to Access point restores the AP form and drops the backhaul fields', () => {
    renderStep();

    pickMode('radio0', 'Mesh backhaul');
    pickMode('radio0', 'Access point');

    expect(screen.getByLabelText('SSID')).toBeInTheDocument();
    expect(screen.queryByLabelText('Backhaul mesh ID')).toBeNull();
  });

  it('flags a short backhaul passphrase inline', () => {
    renderStep();

    pickMode('radio0', 'Mesh backhaul');
    fireEvent.change(screen.getByLabelText('Backhaul passphrase'), { target: { value: 'short' } });

    expect(screen.getByText('Passphrase must be at least 8 characters.')).toBeInTheDocument();
  });
});

describe('StepAPsBackhaulTuning', () => {
  it('offers bandwidth, channel and country once a radio is the backhaul', () => {
    renderStep(); // status with radio0 supportsMeshBackhaul: true
    fireEvent.click(screen.getByRole('radio', { name: 'Mesh backhaul' }));
    expect(screen.getByRole('button', { name: 'Backhaul bandwidth' }).textContent).toContain('Default (40 MHz)');
    expect(screen.getByRole('button', { name: 'Backhaul channel' }).textContent).toContain('Default (8)');
    expect(screen.getByRole('button', { name: 'Backhaul country' })).toBeInTheDocument();
  });

  it('lists the 2.4 GHz channels', () => {
    renderStep();
    fireEvent.click(screen.getByRole('radio', { name: 'Mesh backhaul' }));
    fireEvent.click(screen.getByRole('button', { name: 'Backhaul channel' }));
    const labels = screen.getAllByRole('option').map(o => o.textContent);
    expect(labels).toEqual(['Default (8)', '1', '2', '3', '4', '5', '6', '7', '8', '9', '10', '11']);
  });

  it('shows the spectrum footprint for the default channel and width', () => {
    renderStep();
    fireEvent.click(screen.getByRole('radio', { name: 'Mesh backhaul' }));
    expect(screen.getByText('Occupies ch 8 + ch 4 · 2417–2457 MHz')).toBeInTheDocument();
  });

  it('updates the footprint when the operator picks 20 MHz on channel 1', () => {
    renderStep();
    fireEvent.click(screen.getByRole('radio', { name: 'Mesh backhaul' }));
    fireEvent.click(screen.getByRole('button', { name: 'Backhaul bandwidth' }));
    fireEvent.click(screen.getByRole('option', { name: '20 MHz' }));
    fireEvent.click(screen.getByRole('button', { name: 'Backhaul channel' }));
    fireEvent.click(screen.getByRole('option', { name: '1' }));
    expect(screen.getByText('Occupies ch 1 · 2402–2422 MHz')).toBeInTheDocument();
  });

  function OutOfRangeChannelHarness() {
    const { dispatch } = useSetup();
    useEffect(() => {
      dispatch({ type: SETUP_ACTIONS.SET_RADIO_MODE, radioName: 'radio0', mode: 'backhaul' });
      dispatch({ type: SETUP_ACTIONS.SET_AP, value: { radioName: 'radio0', backhaulBandwidthMhz: 20, backhaulChannel: 14 } });
    }, [dispatch]);
    return <StepAPs status={STATUS} />;
  }

  it('hides the footprint line when the channel is outside 1-11', () => {
    render(<SetupProvider><OutOfRangeChannelHarness /></SetupProvider>);
    expect(screen.getByRole('button', { name: 'Backhaul channel' })).toBeInTheDocument();
    expect(screen.queryByText(/^Occupies/)).toBeNull();
  });
});
