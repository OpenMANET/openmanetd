import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import SettingsVideo from '../../pages/SettingsVideo.jsx';

afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

it('saves the local encoder choice and switches the playback URL to RTSP', async () => {
  const options = { transport: 'srt', hardwareAcceleration: true, latency: 0, push: false, destination: '', path: '' };
  let saved;
  vi.stubGlobal('fetch', vi.fn(async (url, req) => {
    let data;
    if (url.endsWith('/status')) data = { connections: { items: [] } };
    else if (req.method === 'PUT') data = saved = JSON.parse(req.body);
    else data = { options, cameraPath: 'camera', srtPort: '8890', rtspPort: '554', softwareAvailable: true };
    return { ok: true, json: async () => data };
  }));
  render(<SettingsVideo />);
  const toggle = await screen.findByLabelText('Hardware acceleration');
  expect(toggle.checked).toBe(true);
  expect(screen.getByLabelText('Camera playback URL').value).toContain('?streamid=read:camera');
  fireEvent.click(toggle);
  fireEvent.change(screen.getByLabelText('Preferred transport'), { target: { value: 'rtsp' } });
  fireEvent.click(screen.getByRole('button', { name: 'Save video settings' }));
  await screen.findByText('Video settings saved.');
  expect(saved.hardwareAcceleration).toBe(false);
  expect(screen.getByLabelText('Camera playback URL').value).toMatch(/^rtsp:.*:554\/camera$/);
});
