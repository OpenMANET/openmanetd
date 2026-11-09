import { useState, useRef, useCallback, useEffect } from 'react';
import { BluetoothAction as Action, fetchBluetoothStatus, executeBluetoothAction, updateBluetoothPreferences } from '../services/bluetoothApi.js';
import { useVisibleInterval } from '../hooks/useVisibleInterval.js';
import './SettingsBluetooth.css';

export default function SettingsBluetooth() {
  const [status, setStatus] = useState(null);
  const [error, setError] = useState('');
  const [statusError, setStatusError] = useState('');
  const [busy, setBusy] = useState('');
  const [controller, setController] = useState('');
  const [notice, setNotice] = useState('');
  const poll = useRef(null);
  const operation = useRef(null);

  const refresh = useCallback(async () => {
    if (poll.current) return;
    const request = new AbortController();
    poll.current = request;
    try {
      const next = await fetchBluetoothStatus(request.signal);
      if (!request.signal.aborted) { setStatus(next); setStatusError(''); }
    } catch (err) {
      if (!request.signal.aborted) setStatusError(err.rawMessage || err.message || 'Unable to read Bluetooth status.');
    } finally {
      if (poll.current === request) poll.current = null;
    }
  }, []);

  useVisibleInterval(refresh, status?.enabled ? 500 : 5000);
  useEffect(() => () => {
    poll.current?.abort();
    poll.current = null;
    operation.current?.abort();
  }, []);

  async function run(label, task) {
    operation.current?.abort();
    const request = new AbortController();
    operation.current = request;
    setBusy(label);
    setError('');
    setNotice('');
    try {
      await task(request.signal);
    } catch (err) {
      if (!request.signal.aborted) setError(err.rawMessage || err.message || 'Bluetooth action failed.');
    } finally {
      if (operation.current === request && !request.signal.aborted) {
        operation.current = null;
        setBusy('');
        poll.current?.abort();
        poll.current = null;
        await refresh();
      }
    }
  }

  const controllers = status?.controllers || [];
  const selectedController = controllers.some((item) => item.path === controller) ? controller : controllers[0]?.path || '';
  const devices = (status?.accessories || []).filter((item) => item.supported && item.controllerPath === selectedController);
  const available = status?.available === true;
  const enabled = status?.enabled === true;
  const active = status?.active === true;
  const working = busy !== '';
  const selected = (status?.accessories || []).find((item) => item.path === status?.selectedDevicePath);
  const selectedName = selected?.name || status?.savedDeviceName || 'Bluetooth microphone';
  const pairing = enabled && !status?.connectionAllowed ? 'Disconnected' : 'Setup';
  const stateLabel = !available ? 'Unavailable' : !enabled ? 'Off' : active ? 'In use' : pairing;
  const begin = () => run('Starting setup', async (signal) => {
    await executeBluetoothAction(Action.BEGIN_SETUP, { controllerPath: selectedController }, signal);
    await refresh();
    await executeBluetoothAction(Action.DISCOVER, {}, signal);
  });
  const action = (value, label, fields) => run(label, (signal) => executeBluetoothAction(value, fields, signal));
  const off = () => action(Action.OFF, 'Turning Bluetooth off');
  const connect = (device) => action(Action.CONNECT, 'Connecting microphone', { devicePath: device.path });
  const toggleBoot = () => run('Saving startup preference', (signal) => updateBluetoothPreferences(!status.autoEnable, signal));
  const testTone = () => run('Playing test tone', async (signal) => {
    await executeBluetoothAction(Action.TEST_TONE, {}, signal);
    if (!signal.aborted) setNotice('Listen for a tone from the microphone speaker.');
  });

  return (
    <>
      <div className="lat-view-header">
        <div><h2>Bluetooth</h2><div className="crumb">Pair and test a wireless microphone</div></div>
        <div className="lat-view-toolbar"><span className={`lat-chip ${active ? 'ok' : ''}`}>{stateLabel}</span></div>
      </div>
      <div className="lat-body grid-2x bluetooth-body">
        {error || statusError ? <div className="lat-alert crit col-span-all" role="alert">{error || statusError}</div> : null}
        {busy ? <div className="lat-alert col-span-all" role="status">{busy}…</div> : null}
        {!status ? <section className="lat-panel col-span-all"><h3>Bluetooth hardware</h3><p>Loading Bluetooth status…</p><button className="lat-btn bluetooth-action" onClick={refresh}>Retry</button></section> : null}
        {status && !available ? <section className="lat-panel col-span-all"><h3>No Bluetooth controller found</h3><p>Some CM4 modules do not include Bluetooth. Connect a supported USB Bluetooth adapter to use a wireless microphone.</p><p>Detecting an adapter does not turn its radio on.</p></section> : null}
        {available ? <section className="lat-panel">
          <h3>1. Pair your microphone</h3>
          <p>Bluetooth stays off until you start setup. Your saved pairing is kept when Bluetooth is off.</p>
          {controllers.length > 1 ? <div className="lat-field"><label htmlFor="bluetooth-controller">Bluetooth adapter</label><select id="bluetooth-controller" className="lat-select" value={selectedController} disabled={enabled || working} onChange={(event) => setController(event.target.value)}>{controllers.map((item) => <option key={item.path} value={item.path}>{item.name || item.address}</option>)}</select></div> : null}
          <p>Turn on your BS-22, then quickly double-press its power/side key to enter pairing mode.</p>
          <a className="lat-btn ghost bluetooth-action" href="https://baofengtech.com/product/bs-22/" target="_blank" rel="noreferrer">BTECH pairing instructions</a>
          <div className="bluetooth-actions">
            {!enabled ? <button className="lat-btn primary bluetooth-action" disabled={working} onClick={begin}>Start Bluetooth setup</button> : <button className="lat-btn bluetooth-action" disabled={working} onClick={status?.connectionAllowed ? () => action(Action.DISCOVER, 'Looking for microphones') : begin}>Scan again</button>}
            {enabled || working ? <button className="lat-btn danger bluetooth-action" onClick={off} disabled={busy === 'Turning Bluetooth off'}>Bluetooth off</button> : null}
          </div>
          {enabled ? <>
            <p>Choose your microphone. Scanning stops after 20 seconds.</p>
            {devices.length === 0 ? <p>No supported microphones found. Check pairing mode and scan again.</p> : devices.map((device) => <div className="bluetooth-device" key={device.path}>
              <div><strong>{device.name}</strong><div>{device.address}</div><span>{device.paired ? 'Saved pairing' : 'New microphone'}</span></div>
              <button className="lat-btn bluetooth-action" disabled={working && busy !== 'Looking for microphones' && busy !== 'Starting setup'} onClick={() => connect(device)}>{device.paired ? 'Connect' : 'Pair and connect'}</button>
            </div>)}
          </> : null}
        </section> : null}
        {available ? <section className="lat-panel">
          <h3>2. Test and use</h3>
          <p>{status?.savedDeviceAddress ? `${selectedName} · ${status.savedDeviceAddress}` : 'Select a microphone to begin.'}</p>
          <div className="kv"><span className="k">Speaker and microphone</span><span className={`v ${status?.audioReady && status?.hfpReady ? 'ok' : ''}`}>{status?.audioReady && status?.hfpReady ? 'Ready' : 'Waiting'}</span></div>
          <div className="kv"><span className="k">Button connection</span><span className={`v ${status?.buttonsReady ? 'ok' : ''}`}>{status?.buttonsReady ? 'Ready' : 'Waiting'}</span></div>
          <div className="kv" role="status"><span className="k">PTT button</span><span className={`v ${status?.buttonPressed ? 'accent' : ''}`}>{status?.buttonPressed ? 'Pressed' : 'Released'}</span></div>
          {!active ? <p>Press and release PTT to check the indicator, then play a test tone. Setup does not transmit voice to the mesh.</p> : <p>Your microphone is in use. Hold PTT to speak to the selected talk group.</p>}
          {notice ? <p role="status">{notice}</p> : null}
          <div className="bluetooth-actions">
            <button className="lat-btn bluetooth-action" disabled={working || !status?.audioReady || !status?.hfpReady || status?.buttonPressed} onClick={testTone}>Play test tone</button>
            {!active ? <button className="lat-btn primary bluetooth-action" disabled={working || !status?.ready} onClick={() => action(Action.ACTIVATE, 'Activating microphone')}>Use this microphone</button> : null}
            {status?.connectionAllowed ? <button className="lat-btn bluetooth-action" disabled={working} onClick={() => action(Action.DISCONNECT, 'Disconnecting')}>Disconnect</button> : null}
            {status?.savedDeviceAddress ? <button className="lat-btn danger bluetooth-action" disabled={working} onClick={() => action(Action.FORGET, 'Forgetting microphone')}>Forget pairing</button> : null}
          </div>
          <div className="bluetooth-preference"><span id="bluetooth-boot-label">Enable and reconnect at startup</span><button type="button" className={`lat-toggle bluetooth-action${status?.autoEnable ? ' on' : ''}`} role="switch" aria-checked={status?.autoEnable === true} aria-labelledby="bluetooth-boot-label" disabled={working || !status?.savedDeviceAddress} onClick={toggleBoot}><span className="track"><span className="thumb" /></span><span className="label">{status?.autoEnable ? 'On' : 'Off'}</span></button></div>
          <p>Bluetooth stays off until you set it up. Using this microphone enables automatic reconnect after reboot. Bluetooth off disables automatic reconnect.</p>
        </section> : null}
      </div>
    </>
  );
}
