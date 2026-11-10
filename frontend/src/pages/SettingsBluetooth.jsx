import { useState, useRef, useCallback, useEffect } from 'react';
import { Code } from '@connectrpc/connect';
import { BluetoothAction as Action, fetchBluetoothStatus, executeBluetoothAction, updateBluetoothPreferences } from '../services/bluetoothApi.js';
import { useVisibleInterval } from '../hooks/useVisibleInterval.js';
import './SettingsBluetooth.css';

const scanLabel = 'Scanning for PTT devices';

export default function SettingsBluetooth() {
  const [status, setStatus] = useState(null);
  const [failure, setFailure] = useState(null);
  const [statusUnavailable, setStatusUnavailable] = useState(false);
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
      if (!request.signal.aborted) {
        setStatus(next);
        setStatusUnavailable(false);
        setFailure((current) => current?.confirmed?.(next) ? null : current);
      }
    } catch {
      if (!request.signal.aborted) setStatusUnavailable(true);
    } finally {
      if (poll.current === request) poll.current = null;
    }
  }, []);

  useVisibleInterval(refresh, status?.ready && !status.active && !busy && !statusUnavailable ? 500 : status?.enabled ? 2000 : 5000);
  useEffect(() => () => {
    poll.current?.abort();
    poll.current = null;
    operation.current?.abort();
  }, []);

  async function run(label, task, confirmed) {
    operation.current?.abort();
    const request = new AbortController();
    operation.current = request;
    setBusy(label);
    setFailure(null);
    setNotice('');
    try {
      await task(request.signal);
    } catch (err) {
      if (!request.signal.aborted) {
        const message = err.rawMessage || err.message || 'Bluetooth action failed.';
        const uncertain = err.code === Code.DeadlineExceeded || err.code === Code.Unavailable || /timed out|deadline exceeded/i.test(message);
        setFailure({ message, uncertain, confirmed });
      }
    } finally {
      if (operation.current === request && !request.signal.aborted) {
        poll.current?.abort();
        poll.current = null;
        await refresh();
        if (operation.current === request && !request.signal.aborted) {
          operation.current = null;
          setBusy('');
        }
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
  const controlsBlocked = working || statusUnavailable;
  const selected = (status?.accessories || []).find((item) => item.path === status?.selectedDevicePath);
  const selectedName = selected?.name || status?.savedDeviceName || 'Bluetooth PTT';
  const pairing = enabled && !status?.connectionAllowed ? 'Disconnected' : 'Setup';
  const canChoosePTT = devices.some((device) => !status.ready || device.path !== status.selectedDevicePath);
  const preparingAudio = status?.connectionAllowed && selected?.connected && !status.ready;
  const progress = busy === scanLabel && canChoosePTT
    ? 'PTT found. Choose it below to continue.'
    : busy ? `${busy}…` : preparingAudio && !statusUnavailable ? 'PTT connected. Getting audio and buttons ready…' : '';
  const stateLabel = statusUnavailable ? 'Checking status' : !available ? 'Unavailable' : !enabled ? 'Off' : active ? 'In use' : status?.ready ? 'Ready to test' : pairing;
  const error = !working ? failure : null;
  const begin = () => run('Starting setup', async (signal) => {
    await executeBluetoothAction(Action.BEGIN_SETUP, { controllerPath: selectedController }, signal);
    await refresh();
    if (signal.aborted) return;
    setBusy(scanLabel);
    await executeBluetoothAction(Action.DISCOVER, {}, signal);
  });
  const action = (value, label, fields, confirmed) => run(label, (signal) => executeBluetoothAction(value, fields, signal), confirmed);
  const off = () => action(Action.OFF, 'Turning Bluetooth off');
  const connect = (device) => action(Action.CONNECT, device.paired ? 'Connecting PTT' : 'Pairing PTT',
    { devicePath: device.path }, (next) => next?.ready && next.selectedDevicePath === device.path);
  const toggleBoot = () => run('Saving startup preference', (signal) => updateBluetoothPreferences(!status.autoEnable, signal));
  const testTone = () => run('Playing test tone', async (signal) => {
    await executeBluetoothAction(Action.TEST_TONE, {}, signal);
    if (!signal.aborted) setNotice('Listen for a tone from the PTT speaker.');
  });

  return (
    <>
      <div className="lat-view-header">
        <div><h2>Bluetooth</h2><div className="crumb">Pair and test a Bluetooth PTT</div></div>
        <div className="lat-view-toolbar"><span className={`lat-chip ${active ? 'ok' : ''}`}>{stateLabel}</span></div>
      </div>
      <div className="lat-body grid-2x bluetooth-body">
        {statusUnavailable && !working ? <div className="lat-alert warn col-span-all" role="status">Waiting for the radio to respond. Bluetooth status may be out of date.</div> : null}
        {error && !statusUnavailable ? error.uncertain
          ? <div className="lat-alert warn col-span-all" role="status">The radio did not confirm that action. Check the status below before trying again.</div>
          : <div className="lat-alert crit col-span-all" role="alert">{error.message}</div> : null}
        {progress ? <div className="lat-alert col-span-all" role="status">{progress}</div> : null}
        {!status ? <section className="lat-panel col-span-all"><h3>Bluetooth hardware</h3><p>Loading Bluetooth status…</p><button className="lat-btn bluetooth-action" onClick={refresh}>Retry</button></section> : null}
        {status && !available ? <section className="lat-panel col-span-all"><h3>No Bluetooth controller found</h3><p>Some CM4 modules do not include Bluetooth. Connect a supported USB Bluetooth adapter to use a Bluetooth PTT.</p><p>Detecting an adapter does not turn its radio on.</p></section> : null}
        {available ? <section className="lat-panel">
          <h3>1. Pair your PTT</h3>
          <p>Bluetooth stays off until you start setup. Your saved pairing is kept when Bluetooth is off.</p>
          {status.savedDeviceAddress ? <div className="bluetooth-device">
            <div><strong>{selectedName}</strong><div>{status.savedDeviceAddress}</div><span>Saved PTT</span></div>
            <button className="lat-btn danger bluetooth-action" disabled={controlsBlocked}
              onClick={() => action(Action.FORGET, 'Forgetting PTT', undefined, (next) => next && !next.savedDeviceAddress)}>
              Forget PTT
            </button>
          </div> : null}
          {controllers.length > 1 ? <div className="lat-field"><label htmlFor="bluetooth-controller">Bluetooth adapter</label><select id="bluetooth-controller" className="lat-select" value={selectedController} disabled={enabled || working} onChange={(event) => setController(event.target.value)}>{controllers.map((item) => <option key={item.path} value={item.path}>{item.name || item.address}</option>)}</select></div> : null}
          <p>Turn on your BS-22, then quickly double-press its power/side key to enter pairing mode.</p>
          <a className="lat-btn ghost bluetooth-action" href="https://baofengtech.com/product/bs-22/" target="_blank" rel="noreferrer">BTECH pairing instructions</a>
          <div className="bluetooth-actions">
            {!enabled ? <button className="lat-btn primary bluetooth-action" disabled={controlsBlocked} onClick={begin}>Start Bluetooth setup</button> : <button className="lat-btn bluetooth-action" disabled={controlsBlocked} onClick={status?.connectionAllowed ? () => action(Action.DISCOVER, scanLabel) : begin}>Scan again</button>}
            {enabled || working ? <button className="lat-btn danger bluetooth-action" onClick={off} disabled={busy === 'Turning Bluetooth off'}>Bluetooth off</button> : null}
          </div>
          {enabled ? <>
            <p>Choose your PTT. Scanning stops after 20 seconds.</p>
            {devices.length === 0 ? <p>No supported PTT devices found. Check pairing mode and scan again.</p> : devices.map((device) => <div className="bluetooth-device" key={device.path}>
              <div><strong>{device.name}</strong><div>{device.address}</div><span>{device.paired ? 'Saved pairing' : 'New PTT'}</span></div>
              {status.ready && device.path === status.selectedDevicePath
                ? <span className="lat-chip ok">Connected</span>
                : <button className="lat-btn bluetooth-action" disabled={statusUnavailable || (working && busy !== scanLabel)} onClick={() => connect(device)}>{device.paired ? 'Connect' : 'Pair and connect'}</button>}
            </div>)}
          </> : null}
        </section> : null}
        {available ? <section className="lat-panel">
          <h3>2. Test and use</h3>
          {!status?.savedDeviceAddress ? <p>Select a PTT to begin.</p> : null}
          <div className="kv"><span className="k">PTT audio</span><span className={`v ${status?.audioReady && status?.hfpReady ? 'ok' : ''}`}>{status?.audioReady && status?.hfpReady ? 'Ready' : 'Waiting'}</span></div>
          <div className="kv"><span className="k">Button connection</span><span className={`v ${status?.buttonsReady ? 'ok' : ''}`}>{status?.buttonsReady ? 'Ready' : 'Waiting'}</span></div>
          <div className="kv" role="status"><span className="k">PTT button</span><span className={`v ${status?.buttonPressed ? 'accent' : ''}`}>{status?.buttonPressed ? 'Pressed' : 'Released'}</span></div>
          {!active ? <p>Press and release PTT to check the indicator, then play a test tone. Setup does not transmit voice to the mesh.</p> : <p>Your PTT is in use. Hold PTT to speak to the selected talk group.</p>}
          {notice ? <p role="status">{notice}</p> : null}
          <div className="bluetooth-actions">
            <button className="lat-btn bluetooth-action" disabled={controlsBlocked || !status?.audioReady || !status?.hfpReady || status?.buttonPressed} onClick={testTone}>Play test tone</button>
            {!active ? <button className="lat-btn primary bluetooth-action" disabled={controlsBlocked || !status?.ready} onClick={() => action(Action.ACTIVATE, 'Activating PTT', undefined, (next) => next?.active)}>Use this PTT</button> : null}
            {status?.connectionAllowed ? <button className="lat-btn bluetooth-action" disabled={controlsBlocked} onClick={() => action(Action.DISCONNECT, 'Disconnecting')}>Disconnect</button> : null}
          </div>
          <div className="bluetooth-preference"><span id="bluetooth-boot-label">Enable and reconnect at startup</span><button type="button" className={`lat-toggle bluetooth-action${status?.autoEnable ? ' on' : ''}`} role="switch" aria-checked={status?.autoEnable === true} aria-labelledby="bluetooth-boot-label" disabled={controlsBlocked || !status?.savedDeviceAddress} onClick={toggleBoot}><span className="track"><span className="thumb" /></span><span className="label">{status?.autoEnable ? 'On' : 'Off'}</span></button></div>
          <p>Bluetooth stays off until you set it up. Using this PTT enables automatic reconnect after reboot. Bluetooth off disables automatic reconnect.</p>
        </section> : null}
      </div>
    </>
  );
}
