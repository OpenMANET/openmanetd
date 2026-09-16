// =============================================================================
// StepMesh.test.jsx — mesh-point mode option list
// =============================================================================
//
// Pins wizard-parity ledger decision D1 (2026-08-27): MESH_POINT_MODE_NONE
// is not offered until openmanetd's address-reservation worker can leave
// a DHCP-client ahwlan alone. validateProfile rejects it on the backend;
// this test guards the UI half. The label stays in MESH_POINT_MODE_LABELS
// (labels.test.js requires every enum value to be labelled).

import React from 'react';
import { describe, it, expect, afterEach } from 'vitest';
import { render, screen, fireEvent, cleanup, waitFor } from '@testing-library/react';

import StepMesh from '../../../pages/setup/StepMesh.jsx';
import { SetupProvider } from '../../../contexts/SetupContext.jsx';
import { MESH_POINT_MODE_LABELS } from '../../../pages/setup/labels.js';
import { MeshPointMode } from '../../../gen/openmanet/setup/v1/setup_pb.js';
import { samplePayload, encodePayload } from '../../meshJoinFixtures.js';

// initialState already selects MESH_POINT, so the mode select renders
// without any dispatch. One HaLow radio keeps the "no radio" alert away.
const STATUS = {
  radios: [{ name: 'radio1', band: 's1g', isHalow: true }],
  countries: [],
};

function renderStep() {
  return render(
    <SetupProvider>
      <StepMesh status={STATUS} />
    </SetupProvider>,
  );
}

afterEach(cleanup);

describe('StepMeshPointModeOptions', () => {
  it('offers Extender only; NONE stays hidden until the daemon supports it', () => {
    renderStep();

    fireEvent.click(screen.getByRole('button', { name: 'Mesh point mode' }));

    const labels = screen.getAllByRole('option').map(o => o.textContent);
    expect(labels).toEqual([MESH_POINT_MODE_LABELS[MeshPointMode.EXTENDER]]);
    expect(labels).not.toContain(MESH_POINT_MODE_LABELS[MeshPointMode.NONE]);
  });
});

describe('StepMeshEncryption', () => {
  it('fixes encryption to WPA3 (SAE) and never offers an open mesh', () => {
    renderStep();

    expect(screen.queryByRole('button', { name: 'Mesh encryption' })).toBeNull();
    expect(screen.getByText('WPA3 (SAE)')).toBeInTheDocument();
    expect(screen.getByLabelText('Mesh passphrase')).toBeInTheDocument();
  });
});

const EU_STATUS = {
  radios: [{ name: 'radio1', band: 's1g', isHalow: true }],
  countries: [
    { code: 'EU', name: 'Europe', bandwidths: [{ mhz: 1, channels: [1, 3] }, { mhz: 2, channels: [2] }] },
    { code: 'US', name: 'USA', bandwidths: [{ mhz: 8, channels: [12, 28, 44] }] },
  ],
};

function pasteCode(text) {
  fireEvent.click(screen.getByRole('button', { name: 'Paste code' }));
  fireEvent.change(screen.getByLabelText('Code text'), { target: { value: text } });
  fireEvent.click(screen.getByRole('button', { name: 'Use code' }));
}

describe('StepMeshScan', () => {
  it('prefills the mesh fields from a code and names the source', async () => {
    render(<SetupProvider><StepMesh status={EU_STATUS} /></SetupProvider>);
    pasteCode(encodePayload(samplePayload()));
    await waitFor(() => expect(screen.getByLabelText('Mesh ID')).toHaveValue('field-mesh'));
    expect(screen.getByLabelText('Mesh passphrase')).toHaveValue('correct-horse');
    expect(screen.getByRole('button', { name: 'Mesh channel' }).textContent).toContain('44');
    expect(screen.getByText(/Scanned from alpha/)).toBeInTheDocument();
  });

  it('keeps an illegal scanned channel and warns instead of snapping it', async () => {
    render(<SetupProvider><StepMesh status={EU_STATUS} /></SetupProvider>);
    const payload = samplePayload();
    payload.halow.countryCode = 'EU'; // 8 MHz / 44 is not legal in EU
    pasteCode(encodePayload(payload));
    await waitFor(() => expect(screen.getByLabelText('Mesh ID')).toHaveValue('field-mesh'));
    expect(screen.getByRole('button', { name: 'Mesh channel' }).textContent).toContain('44');
    expect(screen.getByText(/8 MHz is not available/)).toBeInTheDocument();
  });

  it('resumes snapping once the operator edits a scanned field', async () => {
    render(<SetupProvider><StepMesh status={EU_STATUS} /></SetupProvider>);
    const payload = samplePayload();
    payload.halow.countryCode = 'EU';
    pasteCode(encodePayload(payload));
    await waitFor(() => screen.getByText(/8 MHz is not available/));
    fireEvent.click(screen.getByRole('button', { name: 'Mesh bandwidth' }));
    fireEvent.click(screen.getByRole('option', { name: '2 MHz' }));
    await waitFor(() => expect(screen.getByRole('button', { name: 'Mesh channel' }).textContent).toContain('2'));
    expect(screen.queryByText(/is not available/)).toBeNull();
  });
});
