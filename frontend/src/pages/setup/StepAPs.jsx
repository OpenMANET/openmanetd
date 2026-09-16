// =============================================================================
// StepAPs.jsx — Per-radio Wi-Fi access point configuration
// =============================================================================
//
// One toggle + form per non-mesh radio. Disabled by default — the user
// opts in for each radio they want broadcasting an AP. The "client
// device Wi-Fi" framing comes from the plan: this is where end users'
// phones / laptops join the mesh through.

import LatSelect from '../../components/LatSelect.jsx';
import { useSetup, SETUP_ACTIONS } from '../../contexts/SetupContext.jsx';
import { apDefaults } from '../../contexts/apDefaults.js';
import { WifiEncryption } from '../../gen/openmanet/wifi_config/v1/wifi_config_pb.js';
import { ENCRYPTION_LABELS } from './labels.js';
import { BACKHAUL_BANDWIDTHS, BACKHAUL_CHANNELS_2G, backhaulFootprint, formatBackhaulFootprint } from './meshChannels.js';

// Same three secured choices the LuCI mesh wizard offers (psk2,
// sae-mixed, sae) plus the open modes. PSK_MIXED (WPA1+WPA2) stays in
// the proto for wire compatibility but is not offered here.
const AP_ENCRYPTION_VALUES = [
  WifiEncryption.SAE,
  WifiEncryption.PSK2,
  WifiEncryption.SAE_MIXED,
  WifiEncryption.OWE,
  WifiEncryption.NONE,
];

// One selector per radio. 'backhaul' is offered only when the device
// reports the radio can carry the 2.4 GHz batman-adv link
// (SetupRadio.supports_mesh_backhaul, MT7915/MT7916 today).
const RADIO_MODES = [
  { value: 'off',      label: 'Off' },
  { value: 'ap',       label: 'Access point' },
  { value: 'backhaul', label: 'Mesh backhaul' },
];

function radioMode(ap) {
  if (ap.meshBackhaul) return 'backhaul';
  if (ap.enabled) return 'ap';
  return 'off';
}

const BACKHAUL_BANDWIDTH_OPTIONS = [
  { value: 0, label: 'Default (40 MHz)' },
  ...BACKHAUL_BANDWIDTHS.map(mhz => ({ value: mhz, label: `${mhz} MHz` })),
];
const BACKHAUL_CHANNEL_OPTIONS = [
  { value: 0, label: 'Default (8)' },
  ...BACKHAUL_CHANNELS_2G.map(ch => ({ value: ch, label: String(ch) })),
];

export default function StepAPs({ status }) {
  const { state, dispatch } = useSetup();

  const allRadios = status?.radios ?? [];
  const apRadios = allRadios.filter(r => !r.isHalow);

  if (apRadios.length === 0) {
    return (
      <div className="setup-step">
        <h3>Client Device Wi-Fi</h3>
        <p>
          This device has no non-HaLow radios available for client APs.
          You can still continue without configuring any.
        </p>
        {allRadios.length > 0 && (
          <div className="setup-help">
            Detected radios: {allRadios.map(r => `${r.name} (${r.band || 'band ?'}${r.isHalow ? ', HaLow — reserved for mesh' : ''})`).join('; ')}.
          </div>
        )}
        {allRadios.length === 0 && (
          <div className="lat-alert warn">
            No radios were reported by the daemon. If this device has
            built-in Wi-Fi, ensure the kernel modules and
            <code> /etc/config/wireless</code> are populated, then reload
            the wizard.
          </div>
        )}
      </div>
    );
  }

  return (
    <div className="setup-step">
      <h3>Client Device Wi-Fi</h3>
      <p className="setup-help">
        Optional: configure a Wi-Fi access point on each radio so phones
        and laptops can join your mesh — or, on a supported 2.4 GHz radio,
        run a second mesh link to nearby nodes instead.
      </p>

      {apRadios.map(radio => (
        <APRow key={radio.name} radio={radio} state={state} dispatch={dispatch} status={status} />
      ))}
    </div>
  );
}

function APRow({ radio, state, dispatch, status }) {
  const ap = state.aps.find(a => a.radioName === radio.name) ?? apDefaults(radio.name);
  const mode = radioMode(ap);
  const modes = radio.supportsMeshBackhaul
    ? RADIO_MODES
    : RADIO_MODES.filter(m => m.value !== 'backhaul');

  const setField = (field, value) => {
    const clearsScan = field.startsWith('backhaul') ? { backhaulFromScan: false } : {};
    dispatch({
      type: SETUP_ACTIONS.SET_AP,
      value: { ...ap, ...clearsScan, [field]: value, radioName: radio.name },
    });
  };

  const footprint = formatBackhaulFootprint(
    backhaulFootprint(ap.backhaulChannel || 8, ap.backhaulBandwidthMhz || 40),
  );

  const setMode = (value) => {
    dispatch({ type: SETUP_ACTIONS.SET_RADIO_MODE, radioName: radio.name, mode: value });
  };

  const showPassphrase = ap.encryption !== WifiEncryption.NONE && ap.encryption !== WifiEncryption.OWE;
  const backhaulPassTooShort = ap.backhaulPassphrase && ap.backhaulPassphrase.length < 8;

  return (
    <div className="setup-ap-row">
      <div className="setup-ap-head">
        <div>
          <strong>{radio.name}</strong>
          {radio.band && <span className="setup-help" style={{ marginLeft: 8 }}>{radio.band}</span>}
          {radio.hardwareName && <span className="setup-help" style={{ marginLeft: 8 }}>{radio.hardwareName}</span>}
        </div>
      </div>

      <div className="lat-seg" role="radiogroup" aria-label={`Mode on ${radio.name}`}>
        {modes.map(m => (
          <button
            key={m.value}
            type="button"
            role="radio"
            aria-checked={mode === m.value}
            className={mode === m.value ? 'active' : ''}
            onClick={() => setMode(m.value)}
          >
            {m.label}
          </button>
        ))}
      </div>

      {mode === 'ap' && (
        <>
          <div className="lat-field">
            <label htmlFor={`setup-ap-ssid-${radio.name}`}>SSID</label>
            <input
              id={`setup-ap-ssid-${radio.name}`}
              className="lat-input"
              type="text"
              value={ap.ssid}
              onChange={(e) => setField('ssid', e.target.value)}
              maxLength={32}
              autoComplete="off"
              spellCheck={false}
            />
          </div>

          <div className="lat-field">
            <label>Encryption</label>
            <LatSelect
              ariaLabel={`Encryption on ${radio.name}`}
              value={ap.encryption}
              options={AP_ENCRYPTION_VALUES.map(v => ({ value: v, label: ENCRYPTION_LABELS[v] }))}
              onChange={(v) => setField('encryption', v)}
            />
          </div>

          {showPassphrase && (
            <div className="lat-field">
              <label htmlFor={`setup-ap-pass-${radio.name}`}>Passphrase</label>
              <input
                id={`setup-ap-pass-${radio.name}`}
                className="lat-input"
                type="password"
                value={ap.passphrase}
                onChange={(e) => setField('passphrase', e.target.value)}
                minLength={8}
                maxLength={63}
                autoComplete="new-password"
              />
              {ap.passphrase && ap.passphrase.length < 8 && (
                <div className="setup-error">Passphrase must be at least 8 characters.</div>
              )}
            </div>
          )}
        </>
      )}

      {mode === 'backhaul' && (
        <>
          <div className="setup-help">
            Second batman-adv mesh link on this radio — WPA3 (SAE). Every
            node on the 2.4 GHz backhaul must share the mesh ID, passphrase,
            channel and width.
          </div>

          {ap.backhaulFromScan && (
            <div className="lat-alert ok">Filled from {state.meshJoin?.sourceHostname || 'a scanned code'}&apos;s code.</div>
          )}

          <div className="lat-field">
            <label htmlFor={`setup-backhaul-id-${radio.name}`}>Backhaul mesh ID</label>
            <input
              id={`setup-backhaul-id-${radio.name}`}
              className="lat-input"
              type="text"
              value={ap.backhaulMeshId}
              onChange={(e) => setField('backhaulMeshId', e.target.value)}
              maxLength={32}
              autoComplete="off"
              spellCheck={false}
            />
          </div>

          <div className="lat-field">
            <label htmlFor={`setup-backhaul-pass-${radio.name}`}>Backhaul passphrase</label>
            <input
              id={`setup-backhaul-pass-${radio.name}`}
              className="lat-input"
              type="password"
              value={ap.backhaulPassphrase}
              onChange={(e) => setField('backhaulPassphrase', e.target.value)}
              minLength={8}
              maxLength={63}
              autoComplete="new-password"
            />
            {backhaulPassTooShort && (
              <div className="setup-error">Passphrase must be at least 8 characters.</div>
            )}
          </div>

          <div className="lat-field">
            <label>Backhaul bandwidth</label>
            <LatSelect
              ariaLabel="Backhaul bandwidth"
              value={ap.backhaulBandwidthMhz ?? 0}
              options={BACKHAUL_BANDWIDTH_OPTIONS}
              onChange={(v) => setField('backhaulBandwidthMhz', v)}
            />
          </div>

          <div className="lat-field">
            <label>Backhaul channel</label>
            <LatSelect
              ariaLabel="Backhaul channel"
              value={ap.backhaulChannel ?? 0}
              options={BACKHAUL_CHANNEL_OPTIONS}
              onChange={(v) => setField('backhaulChannel', v)}
            />
            {footprint && <div className="setup-help">Occupies {footprint}</div>}
            <div className="setup-help">Set bandwidth and channel together, or leave both at Default.</div>
          </div>

          <div className="lat-field">
            <label>Backhaul country</label>
            <LatSelect
              ariaLabel="Backhaul country"
              value={ap.backhaulCountryCode ?? ''}
              options={[
                { value: '', label: `Same as mesh radio (${state.mesh.countryCode || 'unset'})` },
                ...(status?.countries ?? []).map(c => ({ value: c.code, label: `${c.code} — ${c.name || c.code}` })),
              ]}
              onChange={(v) => setField('backhaulCountryCode', v)}
            />
          </div>
        </>
      )}
    </div>
  );
}
