// =============================================================================
// QrScanInput.test.jsx — photo + paste decode paths
// =============================================================================

import React from 'react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/react';
import { samplePayload, encodePayload } from '../meshJoinFixtures.js';

const { mockJsQR } = vi.hoisted(() => ({ mockJsQR: vi.fn() }));
vi.mock('jsqr', () => ({ default: mockJsQR }));

import QrScanInput from '../../components/QrScanInput.jsx';

const validText = encodePayload(samplePayload());
let originalGetContext;

beforeEach(() => {
  mockJsQR.mockReset();
  vi.stubGlobal('createImageBitmap', vi.fn(async () => ({ width: 2048, height: 1024, close: vi.fn() })));
  originalGetContext = HTMLCanvasElement.prototype.getContext;
  HTMLCanvasElement.prototype.getContext = () => ({
    drawImage: vi.fn(),
    getImageData: (_x, _y, w, h) => ({ data: new Uint8ClampedArray(w * h * 4) }),
  });
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  HTMLCanvasElement.prototype.getContext = originalGetContext;
});

function renderInput(overrides = {}) {
  const props = { onDecoded: vi.fn(), onError: vi.fn(), ...overrides };
  return { ...render(<QrScanInput {...props} />), props };
}

function pickPhoto() {
  const input = screen.getByLabelText('Photograph a QR code');
  const file = new File(['x'], 'qr.jpg', { type: 'image/jpeg' });
  fireEvent.change(input, { target: { files: [file] } });
  return input;
}

describe('QrScanInputPhoto', () => {
  it('uses a rear-camera file input over plain HTTP', () => {
    renderInput();
    const input = screen.getByLabelText('Photograph a QR code');
    expect(input.getAttribute('type')).toBe('file');
    expect(input.getAttribute('accept')).toBe('image/*');
    expect(input.getAttribute('capture')).toBe('environment');
  });

  it('decodes a photo and hands back the payload', async () => {
    mockJsQR.mockReturnValue({ data: validText });
    const { props } = renderInput();
    pickPhoto();
    await waitFor(() => expect(props.onDecoded).toHaveBeenCalledTimes(1));
    expect(props.onDecoded.mock.calls[0][0].halow.meshId).toBe('field-mesh');
    expect(props.onError).not.toHaveBeenCalled();
  });

  it('downscales large photos to 1024 px on the long edge', async () => {
    mockJsQR.mockReturnValue({ data: validText });
    renderInput();
    pickPhoto();
    await waitFor(() => expect(mockJsQR).toHaveBeenCalled());
    const [, w, h] = mockJsQR.mock.calls[0];
    expect(w).toBe(1024);
    expect(h).toBe(512);
  });

  it('reports when no QR is in the photo', async () => {
    mockJsQR.mockReturnValue(null);
    const { props } = renderInput();
    pickPhoto();
    await waitFor(() => expect(screen.getByRole('alert')).toBeInTheDocument());
    expect(screen.getByRole('alert').textContent).toMatch(/No QR code found/);
    expect(props.onError).toHaveBeenCalledWith(expect.stringMatching(/No QR code found/));
    expect(props.onDecoded).not.toHaveBeenCalled();
  });

  it('reports a clear error when the canvas has no 2d context', async () => {
    HTMLCanvasElement.prototype.getContext = () => null;
    const { props } = renderInput();
    pickPhoto();
    await waitFor(() => expect(screen.getByRole('alert')).toBeInTheDocument());
    expect(screen.getByRole('alert').textContent).toMatch(/cannot decode photos/);
    expect(props.onError).toHaveBeenCalledWith(expect.stringMatching(/cannot decode photos/));
    expect(props.onDecoded).not.toHaveBeenCalled();
    expect(mockJsQR).not.toHaveBeenCalled();
  });

  it('reports a foreign QR code', async () => {
    mockJsQR.mockReturnValue({ data: 'WIFI:S:cafe;P:latte;;' });
    renderInput();
    pickPhoto();
    await waitFor(() => expect(screen.getByRole('alert').textContent).toMatch(/not an OpenMANET mesh code/));
  });

  it('does nothing when the picker is cancelled', () => {
    const { props } = renderInput();
    const input = screen.getByLabelText('Photograph a QR code');
    fireEvent.change(input, { target: { files: [] } });
    expect(props.onDecoded).not.toHaveBeenCalled();
    expect(props.onError).not.toHaveBeenCalled();
  });

  it('disables the scan button while decoding', async () => {
    let resolve;
    mockJsQR.mockImplementation(() => ({ data: validText }));
    vi.stubGlobal('createImageBitmap', vi.fn(() => new Promise(r => { resolve = r; })));
    renderInput();
    pickPhoto();
    await waitFor(() => expect(screen.getByRole('button', { name: 'Decoding…' })).toBeDisabled());
    resolve({ width: 10, height: 10, close: vi.fn() });
    await waitFor(() => expect(screen.getByRole('button', { name: 'Scan QR' })).not.toBeDisabled());
  });
});

describe('QrScanInputPaste', () => {
  it('decodes pasted text', async () => {
    const { props } = renderInput();
    fireEvent.click(screen.getByRole('button', { name: 'Paste code' }));
    fireEvent.change(screen.getByLabelText('Code text'), { target: { value: validText } });
    fireEvent.click(screen.getByRole('button', { name: 'Use code' }));
    await waitFor(() => expect(props.onDecoded).toHaveBeenCalledTimes(1));
  });

  it('keeps Use code disabled until something is typed', () => {
    renderInput();
    fireEvent.click(screen.getByRole('button', { name: 'Paste code' }));
    expect(screen.getByRole('button', { name: 'Use code' })).toBeDisabled();
  });

  it('shows the version message for a newer code', async () => {
    renderInput();
    fireEvent.click(screen.getByRole('button', { name: 'Paste code' }));
    fireEvent.change(screen.getByLabelText('Code text'), { target: { value: 'OPENMANET2:AAAA' } });
    fireEvent.click(screen.getByRole('button', { name: 'Use code' }));
    await waitFor(() => expect(screen.getByRole('alert').textContent).toMatch(/newer OpenMANET build/));
  });
});
