import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import SettingsVideo from '../../pages/SettingsVideo.jsx';

afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

it('saves the local encoder choice and switches the playback URL to RTSP', async () => {
  const options = { transport: 'srt', hardwareAcceleration: true, latency: 0, push: false, destination: '', path: '' };
  let saved;
  vi.stubGlobal('fetch', vi.fn(async (url, req) => {
    let data;
    if (url.endsWith('/status')) data = { connections: { items: [] }, adaptive: { kbps: 500, reason: 'clean', mesh_routes: { '10.41.0.1': { hard_ifname: 'wlh0', neigh_address: 'aa:bb:cc:dd:ee:ff' } } } };
    else if (req.method === 'PUT') data = saved = JSON.parse(req.body);
    else data = { options, cameraPath: 'camera', srtPort: '8890', rtspPort: '554', softwareAvailable: true };
    return { ok: true, json: async () => data };
  }));
  render(<SettingsVideo />);
  const toggle = await screen.findByLabelText('Hardware acceleration');
  await screen.findByText('wlh0 via aa:bb:cc:dd:ee:ff');
  expect(toggle.checked).toBe(true);
  expect(screen.getByLabelText('Camera playback URL').value).toContain('?streamid=read:camera');
  const resolution = screen.getByLabelText('Resolution');
  expect(resolution.value).toBe('current');
  fireEvent.change(resolution, { target: { value: '720p' } });
  const advertise = screen.getByLabelText('Advertise camera via CoT');
  expect(advertise.checked).toBe(true);
  fireEvent.click(advertise);
  fireEvent.click(screen.getByLabelText('Push video to a remote SRT listener'));
  fireEvent.change(screen.getByLabelText('Remote publish URL'), { target: { value: 'srt://receiver:8890?streamid=publish:camera' } });
  fireEvent.click(screen.getByLabelText('GhostNet adaptive bitrate'));
  fireEvent.change(screen.getByLabelText('Maximum bitrate (kbit/s)'), { target: { value: '900' } });
  fireEvent.click(toggle);
  fireEvent.change(screen.getByLabelText('Preferred transport'), { target: { value: 'rtsp' } });
  fireEvent.click(screen.getByRole('button', { name: 'Save video settings' }));
  await screen.findByText('Video settings saved.');
  expect(saved.cameraCoTEnabled).toBe(false);
  expect(saved.adaptive).toEqual({ enabled: true, minKbps: 125, maxKbps: 900 });
  expect(saved.push).toBe(true);
  expect(saved.destination).toBe('srt://receiver:8890?streamid=publish:camera');
  expect(saved.resolution).toBe('720p');
  expect(saved.hardwareAcceleration).toBe(false);
  expect(screen.getByLabelText('Camera playback URL').value).toMatch(/^rtsp:.*:554\/camera$/);
});
