import React, { useEffect, useRef, useState } from 'react';
import { useVisibleInterval } from '../hooks/useVisibleInterval.js';

const api = '/api/settings/video';

async function request(path = '', method = 'GET', value) {
  const response = await fetch(api + path, {
    method, cache: 'no-store',
    ...(value && { headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(value) }),
  });
  const data = await response.json();
  if (!response.ok) throw new Error(data.error || 'Video service unavailable');
  return data;
}

export default function SettingsVideo() {
  const [settings, setSettings] = useState(null);
  const [draft, setDraft] = useState(null);
  const [status, setStatus] = useState(null);
  const [statusError, setStatusError] = useState('');
  const [message, setMessage] = useState('');
  const [busy, setBusy] = useState(false);
  const [source, setSource] = useState('');
  const polling = useRef(false);

  useEffect(() => {
    let active = true;
    request().then(data => {
      if (active) { setSettings(data); setDraft(data.options); }
    }).catch(err => { if (active) setMessage(err.message); });
    return () => { active = false; };
  }, []);

  useVisibleInterval(async () => {
    if (polling.current) return;
    polling.current = true;
    try { setStatus(await request('/status')); setStatusError(''); }
    catch (err) { setStatusError(err.message); setStatus(null); }
    finally { polling.current = false; }
  }, 3000);

  const change = (key, value) => setDraft(previous => ({ ...previous, [key]: value }));
  async function perform(action, success) {
    setBusy(true); setMessage('');
    try { await action(); setMessage(success); }
    catch (err) { setMessage(err.message); }
    finally { setBusy(false); }
  }
  function transfer(kind, action) {
    return perform(() => request('/transfer', 'POST', { kind, action, source }), action === 'open' ? 'Transfer opened.' : 'Transfer closed.');
  }

  if (!draft) return <section className="lat-panel" role="status">{message || 'Loading video settings…'}</section>;
  const host = window.location.hostname;
  const options = settings.options;
  const srt = (action, path) => `srt://${host}:${settings.srtPort}?streamid=${action}:${path}${options.latency ? '&latency=' + options.latency * 1000 : ''}`;
  const adaptive = draft.adaptive ?? { enabled: false, minKbps: 125, maxKbps: 1100 };
  const playback = options.transport === 'rtsp'
    ? `rtsp://${host}:${settings.rtspPort}/${settings.cameraPath}`
    : srt('read', settings.cameraPath);
  const urlField = (label, value) => <label className="lat-field">{label}<input className="lat-input" readOnly value={value} onFocus={e => e.target.select()} /></label>;

  return <div className="lat-body">
    <form className="lat-panel" onSubmit={e => {
      e.preventDefault();
      perform(async () => {
        const saved = await request('', 'PUT', draft);
        setSettings(previous => ({ ...previous, options: saved }));
        setDraft(saved);
      }, 'Video settings saved.');
    }}>
      <h3>Camera playback</h3>
      <label className="lat-field">Preferred transport
        <select className="lat-select" value={draft.transport} onChange={e => change('transport', e.target.value)}>
          <option value="srt">SRT</option><option value="rtsp">RTSP</option>
        </select>
      </label>
      <label className="lat-field">SRT caller latency (ms)
        <input className="lat-input" type="number" min="20" max="8000" step="1" placeholder="Client default" value={draft.latency || ''} onChange={e => change('latency', Number(e.target.value))} />
      </label>
      {urlField('Camera playback URL', playback)}
      <label><input type="checkbox" checked={draft.cameraCoTEnabled ?? true}
        onChange={e => change('cameraCoTEnabled', e.target.checked)} /> Advertise camera via CoT</label>
      <p>Camera announcements use the saved playback transport. Turning this off stops future camera announcements.</p>
      <h3>Local H.264 encoder</h3>
      <label className="lat-field">Resolution
        <select className="lat-select" value={draft.resolution || 'current'} onChange={e => change('resolution', e.target.value)}>
          <option value="current">Keep current resolution</option>
          <option value="720p">1280 × 720</option>
          <option value="1080p">1920 × 1080</option>
        </select>
      </label>
      <p>The selected resolution applies to all viewers the next time the local camera starts.</p>
      <label><input type="checkbox" checked={draft.hardwareAcceleration} disabled={!settings.softwareAvailable && draft.hardwareAcceleration}
        onChange={e => change('hardwareAcceleration', e.target.checked)} /> Hardware acceleration</label>
      <p>{settings.softwareAvailable ? 'Turn off to encode on the CPU. Takes effect the next time the local camera starts.' : 'Software H.264 encoding is unavailable on this image.'}</p>
      {draft.transport === 'srt' && <>
        <label><input type="checkbox" checked={adaptive.enabled}
          onChange={e => change('adaptive', { ...adaptive, enabled: e.target.checked })} /> GhostNet adaptive bitrate</label>
        {adaptive.enabled && <>
          <p>Auto (mesh route) follows the radio Batman selects for each viewer. The camera keeps the selected resolution.</p>
          <label className="lat-field">Minimum bitrate (kbit/s)
            <input className="lat-input" required type="number" min="1" max={adaptive.maxKbps} value={adaptive.minKbps || ''} onChange={e => change('adaptive', { ...adaptive, minKbps: Number(e.target.value) })} />
          </label>
          <label className="lat-field">Maximum bitrate (kbit/s)
            <input className="lat-input" required type="number" min={adaptive.minKbps || 1} max="100000" value={adaptive.maxKbps || ''} onChange={e => change('adaptive', { ...adaptive, maxKbps: Number(e.target.value) })} />
          </label>
        </>}
      </>}
      <h3>Outgoing push</h3>
      <label><input type="checkbox" checked={draft.push} onChange={e => change('push', e.target.checked)} /> Push video to a remote SRT listener</label>
      {draft.push && <>
        <label className="lat-field">Local camera path<input className="lat-input" value={draft.path} placeholder={settings.cameraPath} onChange={e => change('path', e.target.value)} /></label>
        <label className="lat-field">Remote publish URL<input className="lat-input" required value={draft.destination} placeholder="srt://host:8890?streamid=publish:srt-ingest" onChange={e => change('destination', e.target.value)} /></label>
      </>}
      <button disabled={busy} className="lat-btn primary">Save video settings</button>
    </form>
    <section className="lat-panel"><h3>SRT transfers</h3>
      <p>Open ingest to accept a publisher on this node. Anyone who can reach a publishing port can send to the open path.</p>
      {urlField('Encoder publish URL', srt('publish', 'srt-ingest'))}
      {urlField('Ingest playback URL', srt('read', 'srt-ingest'))}
      <button className="lat-btn" disabled={busy} onClick={() => transfer('ingest', 'open')}>Open ingest</button>{' '}
      <button className="lat-btn" disabled={busy} onClick={() => transfer('ingest', 'close')}>Close ingest</button>
      <p>Open a relay to pull from a remote SRT listener when a viewer connects. Ingest and relay paths close when MediaMTX restarts.</p>
      <label className="lat-field">Remote source URL<input className="lat-input" value={source} placeholder="srt://host:8890?streamid=read:rpicamera" onChange={e => setSource(e.target.value)} /></label>
      {urlField('Relay playback URL', srt('read', 'srt-relay'))}
      <button className="lat-btn" disabled={busy || !source} onClick={() => transfer('relay', 'open')}>Open relay</button>{' '}
      <button className="lat-btn" disabled={busy} onClick={() => transfer('relay', 'close')}>Close relay</button>
    </section>
    {message && <p role="status">{message}</p>}
    <section className="lat-panel"><h3>SRT connections</h3>
      {statusError || (!status ? 'Loading statistics…' : !status.connections.items?.length && 'No active listener connections.')}
      {status?.connections.items?.map(peer => <dl className="kv" key={peer.id}>
        <dt>{peer.state} · {peer.path}</dt><dd>{peer.remoteAddr}</dd>
        <dt>RTT / send buffer</dt><dd>{peer.rttMillis?.toFixed(1) ?? '—'} / {peer.sendBufferMillis ?? '—'} ms</dd>
        <dt>Sent / received</dt><dd>{peer.bytesSent} / {peer.bytesReceived} bytes</dd>
      </dl>)}
      {status?.adaptive && <dl className="kv"><dt>Adaptive bitrate target</dt><dd>{status.adaptive.kbps} kbit/s</dd>
        <dt>Bitrate decision</dt><dd>{status.adaptive.reason}</dd>
        {Object.entries(status.adaptive.mesh_routes ?? {}).map(([host, hop]) =>
          <React.Fragment key={host}><dt>Route to {host}</dt><dd>{hop.hard_ifname} via {hop.neigh_address}</dd></React.Fragment>)}
      </dl>}
      {status?.push && <dl className="kv"><dt>Outgoing push</dt><dd>{status.push.state} {status.push.remoteAddr}</dd>
        {status.push.state === 'Sending' && <><dt>RTT / send buffer</dt><dd>{status.push.rttMillis.toFixed(1)} / {status.push.sendBufferMillis} ms</dd>
          <dt>Sent / retransmitted</dt><dd>{status.push.bytesSent} bytes / {status.push.retransmissions} packets</dd></>}
      </dl>}
    </section>
  </div>;
}
