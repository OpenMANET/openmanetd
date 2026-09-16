// =============================================================================
// SettingsWireless.jsx — Wireless radio configuration tab
// =============================================================================

import { useState, useEffect, useCallback, useMemo, useRef } from 'react';
import { createClient } from '@connectrpc/connect';
import { transport } from '../services/connectClient.js';
import { WifiConfigService } from '../gen/openmanet/wifi_config/v1/wifi_config_service_pb.js';
import { WifiMode, WifiEncryption } from '../gen/openmanet/wifi_config/v1/wifi_config_pb.js';
import { MeshJoinRadioRole, MeshJoinRadioStatus } from '../gen/openmanet/mesh_join/v1/mesh_join_pb.js';
import { POWER_LEVELS, dBmToLevel, levelToDbm } from './SettingsWireless.power.js';
import LatSelect from '../components/LatSelect.jsx';
import MeshJoinQR from '../components/MeshJoinQR.jsx';
import QrScanInput from '../components/QrScanInput.jsx';
import { applyMeshJoin } from '../services/meshJoinApi.js';
import { checkMeshCredentials, htModeForBandwidth, bandwidthMhzForHTMode } from '../utils/meshJoin.js';
import './SettingsWireless.css';

const wifiClient = createClient(WifiConfigService, transport);

// Band numeric values (mirrors the WifiBand enum). The generated WifiBand enum
// does not expose short-name aliases (only WIFI_BAND_*), so we use literals.
// Band 4 == S1G (Sub-1 GHz / HaLow). S1G radios always operate in mesh mode
// and do not expose a Mode selector.
const BAND_S1G = 4;
const BAND_LABELS = { 0: '?', 1: '2.4 GHz', 2: '5 GHz', 3: '6 GHz', 4: 'Sub-1 GHz', 5: '60 GHz' };

const MODE_OPTIONS = [
  { value: WifiMode.AP,      label: 'Access Point' },
  { value: WifiMode.MESH,    label: 'Mesh' },
  { value: WifiMode.STA,     label: 'Station' },
  { value: WifiMode.ADHOC,   label: 'Ad-Hoc' },
  { value: WifiMode.MONITOR, label: 'Monitor' },
];

const ENCRYPTION_LABELS = {
  0: 'Unspecified', 1: 'WPA3-SAE', 2: 'WPA2-PSK', 3: 'WPA-PSK',
  4: 'PSK Mixed', 5: 'None', 6: 'OWE',
};

const HT_MODE_LABELS = {
  0: '—', 1: 'No HT', 2: 'HT20', 3: 'HT40-', 4: 'HT40+', 5: 'HT40',
  6: 'VHT20', 7: 'VHT40', 8: 'VHT80', 9: 'VHT160',
  10: 'HE20', 11: 'HE40', 12: 'HE80', 13: 'HE160',
  14: 'S1G 1 MHz', 15: 'S1G 2 MHz', 16: 'S1G 4 MHz', 17: 'S1G 8 MHz',
};

// Peer admission floor choices for a 2.4 GHz mesh radio (UCI
// mesh_rssi_threshold). -80 is what setup writes
// (network.SecondaryMeshRSSIThreshold); the proto bounds updates to
// -85..-70.
const MESH_RSSI_DEFAULT_DBM = -80;
const MESH_RSSI_OPTIONS = [-85, -80, -75, -70].map(dbm => ({
  value: dbm,
  label: dbm === MESH_RSSI_DEFAULT_DBM ? `${dbm} dBm (default)` : `${dbm} dBm`,
}));

// Shown when UCI carries no mesh_rssi_threshold at all (a radio that
// went mesh before the setting existed): the daemon default applies but
// nothing is written until the operator picks a value.
const MESH_RSSI_UNSET_OPTION = {
  value: null,
  label: `Not set (daemon default ${MESH_RSSI_DEFAULT_DBM} dBm)`,
};

// meshRssiOptions keeps a stored value that is not one of the four
// choices (a hand-edited UCI option) visible instead of showing "—".
function meshRssiOptions(current) {
  if (current == null) return [MESH_RSSI_UNSET_OPTION, ...MESH_RSSI_OPTIONS];
  if (MESH_RSSI_OPTIONS.some(o => o.value === current)) return MESH_RSSI_OPTIONS;
  return [{ value: current, label: `${current} dBm (current)` }, ...MESH_RSSI_OPTIONS];
}

function isMeshMode(mode) {
  return mode === WifiMode.MESH;
}

// seedTxPowerForDisplay picks an initial dBm to show in the selector when the
// stored UCI setting is unset (txPower == 0). Without this, dBmToLevel(0)
// always snaps to "Low" because 10 dBm is the nearest canonical value, which
// misleads operators into thinking the radio is running at low power. We
// prefer the live iwinfo reading (snapped to the nearest canonical level),
// and fall back to the medium canonical (17 dBm) when no status is available.
function seedTxPowerForDisplay(settings, status) {
  if (!settings) return settings;
  const stored = settings.txPower;
  if (stored != null && stored > 0) return settings;

  const live = status?.txPower;
  if (live != null && live > 0) {
    return { ...settings, txPower: levelToDbm(dBmToLevel(live)) };
  }
  return { ...settings, txPower: levelToDbm('medium') };
}

function settingsEqual(a, b) {
  if (!a || !b) return a === b;
  return (
    a.ssid === b.ssid &&
    (a.meshId ?? '') === (b.meshId ?? '') &&
    (a.password ?? '') === (b.password ?? '') &&
    a.channel === b.channel &&
    a.bandwidth === b.bandwidth &&
    a.txPower === b.txPower &&
    (a.country ?? '') === (b.country ?? '') &&
    a.encryption === b.encryption &&
    (a.disabled ?? false) === (b.disabled ?? false) &&
    (a.mode ?? 0) === (b.mode ?? 0) &&
    (a.meshRssiThreshold ?? null) === (b.meshRssiThreshold ?? null)
  );
}

// fieldsFromCredentials maps scanned MeshCredentials onto RadioSettings
// draft fields. Bandwidth picks the radio's advertised HT mode of that
// width so the select shows a real option.
function fieldsFromCredentials(c, availOpts) {
  return {
    mode:       WifiMode.MESH,
    meshId:     c.meshId,
    ssid:       c.meshId,
    password:   c.passphrase,
    encryption: c.encryption,
    channel:    String(c.channel),
    bandwidth:  htModeForBandwidth(c.bandwidthMhz, availOpts?.bandwidths ?? []),
    country:    c.countryCode,
    disabled:   false,
  };
}

// credentialsFromDraft is the inverse: what the operator reviewed (and
// maybe edited) is what ApplyMeshJoin receives. draft can be null when the
// target radio's GetRadioSettings hasn't resolved yet (or errors
// persistently, e.g. a radio with no linked iface) — return a safe
// empty-ish object rather than throwing on draft.meshId. Callers should
// still avoid invoking this with a null draft (see doJoin's own guard);
// this is the second line of defense.
function credentialsFromDraft(draft) {
  if (!draft) {
    return {
      meshId:       '',
      passphrase:   '',
      encryption:   WifiEncryption.UNSPECIFIED,
      bandwidthMhz: 0,
      channel:      0,
      countryCode:  '',
    };
  }
  return {
    meshId:       draft.meshId ?? '',
    passphrase:   draft.password ?? '',
    encryption:   draft.encryption ?? WifiEncryption.UNSPECIFIED,
    bandwidthMhz: bandwidthMhzForHTMode(draft.bandwidth),
    channel:      Number(draft.channel) || 0,
    countryCode:  (draft.country ?? '').toUpperCase(),
  };
}

const BACKHAUL_SKIPPED = 'Backhaul skipped: no 2.4 GHz radio is in mesh mode. Switch one to mesh and scan again.';

function PowerSelector({ valueDbm, onChange, hint }) {
  const selected = dBmToLevel(valueDbm);
  return (
    <div className="lat-field full">
      <label>Tx Power</label>
      <div className="power-selector" role="radiogroup" aria-label="TX power level">
        {POWER_LEVELS.map(p => (
          <button
            key={p.level}
            type="button"
            role="radio"
            aria-checked={selected === p.level}
            className={selected === p.level ? 'active' : ''}
            onClick={() => onChange(p.dBm)}
          >
            <span className="level">{p.label}</span>
            <span className="dbm">{p.dBm} dBm</span>
          </button>
        ))}
      </div>
      <div className="power-hint">
        <span>Selected: {POWER_LEVELS.find(p => p.level === selected)?.label} · {levelToDbm(selected)} dBm</span>
        {hint && <span className="readout">{hint}</span>}
      </div>
    </div>
  );
}

function ConnectedTable({ rows, kind }) {
  if (!rows || rows.length === 0) {
    return <div className="empty-row">No {kind === 'clients' ? 'connected clients' : 'mesh peers'}.</div>;
  }
  return (
    <table className="lat-table">
      <thead>
        <tr>
          <th>{kind === 'clients' ? 'Hostname' : 'Peer'}</th>
          <th>MAC</th>
          <th>Signal</th>
          {kind === 'clients' ? (
            <>
              <th className="num">Rx</th>
              <th className="num">Tx</th>
            </>
          ) : (
            <th className="num">Throughput</th>
          )}
        </tr>
      </thead>
      <tbody>
        {rows.map((r, i) => (
          <tr key={i}>
            <td>{r.hostname || '—'}</td>
            <td className="mono">{r.macAddress}</td>
            <td>{r.signalDbm} dBm</td>
            {kind === 'clients' ? (
              <>
                <td className="num">{formatBitrate(r.rxRateBps)}</td>
                <td className="num">{formatBitrate(r.txRateBps)}</td>
              </>
            ) : (
              <td className="num">{r.throughputMbps?.toFixed(1) ?? '0.0'} Mbps</td>
            )}
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function formatBitrate(bps) {
  if (!bps) return '—';
  const mbps = Number(bps) / 1_000_000;
  if (mbps >= 1) return `${mbps.toFixed(1)} Mbps`;
  return `${(Number(bps) / 1_000).toFixed(0)} Kbps`;
}

function RadioCard({ radio, prefill, reloadKey = 0, onCardChange }) {
  const [status, setStatus] = useState(null);
  const [original, setOriginal] = useState(null);
  const [draft, setDraft] = useState(null);
  const [availOpts, setAvailOpts] = useState({ channels: [], bandwidths: [], encryptions: [] });
  const [clients, setClients] = useState(null);
  const [peers, setPeers] = useState(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [expanded, setExpanded] = useState(false);
  const [error, setError] = useState(null);
  const [success, setSuccess] = useState(null);
  // Tracks the prefill.nonce already merged into the draft so a re-render
  // triggered by an operator edit (which changes `draft` but not
  // prefill.nonce) never re-clobbers the field with the scanned value.
  const appliedPrefillNonceRef = useRef(null);

  const isS1G = radio.band === BAND_S1G;

  const loadCard = useCallback(async () => {
    setLoading(true);
    try {
      const [statusRes, settingsRes] = await Promise.allSettled([
        wifiClient.getRadioStatus({ radioName: radio.name }),
        wifiClient.getRadioSettings({ radioName: radio.name }),
      ]);
      const statusVal = statusRes.status === 'fulfilled' ? statusRes.value.status : null;
      if (statusVal) setStatus(statusVal);
      if (settingsRes.status === 'fulfilled') {
        const s = seedTxPowerForDisplay(settingsRes.value.settings, statusVal);
        setOriginal(s);
        setDraft(s);
        setAvailOpts({
          channels: settingsRes.value.availableChannels ?? [],
          bandwidths: settingsRes.value.availableBandwidths ?? [],
          encryptions: settingsRes.value.availableEncryptions ?? [],
        });
      }
      setError(null);
    } catch (e) {
      setError(e.message);
    } finally {
      setLoading(false);
    }
  }, [radio.name]);

  const loadConnected = useCallback(async () => {
    const [c, p] = await Promise.allSettled([
      wifiClient.listConnectedClients({ radioName: radio.name }),
      wifiClient.listMeshPeers({ radioName: radio.name }),
    ]);
    if (c.status === 'fulfilled') setClients(c.value.clients ?? []);
    if (p.status === 'fulfilled') setPeers(p.value.peers ?? []);
  }, [radio.name]);

  useEffect(() => {
    // Fetch-on-mount: load card details for this radio.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    loadCard();
  }, [loadCard]);
  useEffect(() => {
    // Fetch-on-expand: load clients/peers when the card opens.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    if (expanded) loadConnected();
  }, [expanded, loadConnected]);

  useEffect(() => {
    // Re-read after a join wrote this radio (reloadKey bumps).
    if (reloadKey === 0) return;
    // eslint-disable-next-line react-hooks/set-state-in-effect
    loadCard();
  }, [reloadKey, loadCard]);

  useEffect(() => {
    // Scanned credentials land in the draft; the operator still presses
    // Join mesh (or Save) before anything is written. If the draft hasn't
    // finished loading yet when the scan lands, hold the prefill and apply
    // it once loadCard resolves — otherwise the merge below is a no-op
    // against a null draft and the scanned values are silently dropped.
    // Track the last-applied nonce so this effect (which also re-fires on
    // every subsequent draft change, including operator edits) applies a
    // given prefill exactly once.
    if (!prefill || !draft) return;
    if (appliedPrefillNonceRef.current === prefill.nonce) return;
    appliedPrefillNonceRef.current = prefill.nonce;
    setDraft(prev => (prev ? { ...prev, ...prefill.fields } : prev));
  }, [prefill, draft]);

  useEffect(() => {
    onCardChange?.(radio.name, { draft, availOpts, band: radio.band });
  }, [draft, availOpts, radio.name, radio.band, onCardChange]);

  const update = (field, value) => {
    setDraft(prev => ({ ...prev, [field]: value }));
  };

  const updateMode = (newMode) => {
    setDraft(prev => {
      const next = { ...prev, mode: newMode };
      // When switching INTO mesh, seed mesh_id from ssid so the operator
      // doesn't have to retype the network name, and select WPA3-SAE: mesh
      // links in OpenMANET are always SAE (see network.wifiEncryptionSAE),
      // so the operator should not have to pick it by hand. The admission
      // floor is seeded the same way (the stored value, else the daemon
      // default) so a settings-page AP→mesh flip writes what setup would;
      // leaving mesh restores the stored value so a round trip stays clean.
      if (isMeshMode(newMode)) {
        if (!next.meshId) next.meshId = prev.ssid;
        next.encryption = WifiEncryption.SAE;
        if (next.meshRssiThreshold == null) {
          next.meshRssiThreshold = original?.meshRssiThreshold ?? MESH_RSSI_DEFAULT_DBM;
        }
      } else {
        next.meshRssiThreshold = original?.meshRssiThreshold;
      }
      return next;
    });
  };

  const save = async () => {
    if (!draft) return;
    setSaving(true);
    setError(null);
    setSuccess(null);
    try {
      // For mesh mode, mirror mesh_id into ssid so the proto's ssid validation
      // (min_len 1) is satisfied with a meaningful value.
      const payload = { ...draft };
      if (isMeshMode(payload.mode) && payload.meshId) {
        payload.ssid = payload.meshId;
      }
      // An unset or unchanged floor sends nothing so the UCI option is
      // left alone (null must never reach the optional int32 field).
      if (payload.meshRssiThreshold == null || payload.meshRssiThreshold === original?.meshRssiThreshold) {
        delete payload.meshRssiThreshold;
      }
      const resp = await wifiClient.updateRadioSettings({
        radioName: radio.name,
        settings: payload,
      });
      if (resp.success === false) throw new Error(resp.message || 'Update failed');
      setSuccess('Settings saved. Wireless is restarting; your connection may drop if you are on this radio.');
      setTimeout(() => loadCard(), 2000);
    } catch (e) {
      setError('Save failed: ' + e.message);
    } finally {
      setSaving(false);
    }
  };

  const cancel = () => {
    setDraft(original);
    setError(null);
    setSuccess(null);
  };

  const toggleEnabled = () => {
    if (!draft) return;
    update('disabled', !(draft.disabled ?? false));
  };

  if (loading) {
    return (
      <div className="lat-panel radio-card">
        <div className="panel-head">
          <div className="radio-head-left">
            <div className="name">{radio.displayName || radio.name}</div>
            <div className="meta">{radio.hardwareName} · {BAND_LABELS[radio.band] || '?'}</div>
          </div>
        </div>
        <div className="loading-row">Loading…</div>
      </div>
    );
  }

  const dirty = !settingsEqual(original, draft);
  const enabled = !(draft?.disabled ?? false);
  const mesh = isMeshMode(draft?.mode);
  const showFloor = mesh && !isS1G;
  const floorOptions = meshRssiOptions(draft?.meshRssiThreshold);

  return (
    <div className={`lat-panel radio-card${enabled ? '' : ' disabled-card'}`}>
      <div className="panel-head">
        <div className="radio-head-left">
          <div className="name">{radio.displayName || radio.name}</div>
          <div className="meta">
            {radio.hardwareName} · {BAND_LABELS[radio.band] || '?'} · iface {radio.interfaceName}
          </div>
        </div>
        <div className="radio-head-right">
          <span className={`lat-chip ${status?.active ? 'ok' : 'crit'}`}>
            <span className="dot"></span>{status?.active ? 'Active' : 'Inactive'}
          </span>
          <button
            type="button"
            className={`lat-toggle${enabled ? ' on' : ''}`}
            aria-pressed={enabled}
            onClick={toggleEnabled}
          >
            <span className="track"><span className="thumb"></span></span>
            <span className="label">{enabled ? 'Enabled' : 'Disabled'}</span>
          </button>
        </div>
      </div>

      {error && <div className="lat-alert crit">{error}</div>}
      {success && <div className="lat-alert ok">{success}</div>}

      {status && (
        <div className="status-strip">
          <div className="kv"><span className="k">{mesh ? 'Mesh ID' : 'SSID'}</span><span className="v accent">{status.ssid || '—'}</span></div>
          <div className="kv"><span className="k">Mode</span><span className="v">{status.mode || '—'}</span></div>
          <div className="kv"><span className="k">Channel</span><span className="v">{status.channel || '—'}{status.frequency ? ` · ${status.frequency} MHz` : ''}</span></div>
          <div className="kv"><span className="k">Bandwidth</span><span className="v">{status.bandwidth || '—'}</span></div>
          <div className="kv"><span className="k">Encryption</span><span className="v">{status.encryption || '—'}</span></div>
          <div className="kv"><span className="k">Tx Power</span><span className="v accent">{status.txPower != null ? `${status.txPower} dBm` : '—'}</span></div>
          {mesh
            ? <div className="kv"><span className="k">Peers</span><span className="v">{status.meshPeers ?? 0}</span></div>
            : <div className="kv"><span className="k">Clients</span><span className="v">{status.connectedClients ?? 0}</span></div>}
        </div>
      )}

      {draft && (
        <div className="settings-grid">
          {!isS1G && (
            <div className="lat-field">
              <label>Mode</label>
              <LatSelect
                ariaLabel="Mode"
                value={draft.mode ?? WifiMode.UNSPECIFIED}
                onChange={updateMode}
                options={MODE_OPTIONS}
              />
            </div>
          )}

          <div className="lat-field">
            <label>{mesh ? 'Mesh ID' : 'SSID'}</label>
            <input
              className="lat-input"
              type="text"
              value={mesh ? (draft.meshId ?? '') : (draft.ssid ?? '')}
              onChange={e => update(mesh ? 'meshId' : 'ssid', e.target.value)}
            />
          </div>

          <div className="lat-field">
            <label>Password</label>
            <input
              className="lat-input"
              type="password"
              value={draft.password ?? ''}
              placeholder="(unchanged)"
              onChange={e => update('password', e.target.value)}
            />
          </div>

          <div className="lat-field">
            <label>Channel</label>
            <LatSelect
              ariaLabel="Channel"
              value={draft.channel ?? ''}
              onChange={v => update('channel', v)}
              options={
                availOpts.channels.length === 0
                  ? [{ value: draft.channel ?? '', label: draft.channel || '—' }]
                  : availOpts.channels.map(ch => ({ value: ch, label: String(ch) }))
              }
            />
          </div>

          <div className="lat-field">
            <label>Bandwidth</label>
            <LatSelect
              ariaLabel="Bandwidth"
              value={draft.bandwidth ?? 0}
              onChange={v => update('bandwidth', v)}
              options={
                availOpts.bandwidths.length === 0
                  ? [{ value: draft.bandwidth ?? 0, label: HT_MODE_LABELS[draft.bandwidth] || '—' }]
                  : availOpts.bandwidths.map(bw => ({ value: bw, label: HT_MODE_LABELS[bw] || String(bw) }))
              }
            />
          </div>

          <div className="lat-field">
            <label>Encryption</label>
            <LatSelect
              ariaLabel="Encryption"
              value={draft.encryption ?? 0}
              onChange={v => update('encryption', v)}
              options={availOpts.encryptions.map(enc => ({
                value: enc,
                label: ENCRYPTION_LABELS[enc] || String(enc),
              }))}
            />
          </div>

          <div className="lat-field">
            <label>Country</label>
            <input
              className="lat-input"
              type="text"
              maxLength={2}
              value={(draft.country ?? '').toUpperCase()}
              onChange={e => update('country', e.target.value.toUpperCase())}
            />
          </div>

          {showFloor && (
            <div className="lat-field">
              <label>Peer admission floor</label>
              <LatSelect
                ariaLabel="Peer admission floor"
                value={draft.meshRssiThreshold ?? null}
                onChange={v => update('meshRssiThreshold', v)}
                options={floorOptions}
              />
              <span className="hint">2.4 GHz peers heard below this are not admitted</span>
            </div>
          )}

          <PowerSelector
            valueDbm={draft.txPower}
            onChange={v => update('txPower', v)}
            hint="Saving will briefly reset the radio"
          />
        </div>
      )}

      {dirty && (
        <div className="actions-row">
          <button type="button" className="lat-btn primary" onClick={save} disabled={saving}>
            {saving ? 'Saving…' : 'Save'}
          </button>
          <button type="button" className="lat-btn ghost" onClick={cancel} disabled={saving}>
            Cancel
          </button>
          <span className="lat-chip warn"><span className="dot"></span>Unsaved Changes</span>
        </div>
      )}

      <div className="disclosure">
        <button
          type="button"
          className="disclosure-head"
          aria-expanded={expanded}
          onClick={() => setExpanded(e => !e)}
        >
          <span className="caret">{expanded ? '▼' : '▶'}</span>
          {mesh ? 'Mesh Peers' : 'Connected Clients'}
          {expanded && (mesh ? (peers != null ? ` (${peers.length})` : '') : (clients != null ? ` (${clients.length})` : ''))}
        </button>
        {expanded && (
          <div className="disclosure-body">
            {mesh ? <ConnectedTable rows={peers} kind="peers" /> : <ConnectedTable rows={clients} kind="clients" />}
          </div>
        )}
      </div>
    </div>
  );
}

export default function SettingsWireless() {
  const [radios, setRadios] = useState([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);
  // Per-radio draft + option lists reported by each RadioCard.
  const [cards, setCards] = useState({});
  // Held scan: which radios it targets and what was prefilled.
  const [join, setJoin] = useState(null);
  const [joinStatus, setJoinStatus] = useState({ busy: false, ok: null, error: null });
  const [refreshKey, setRefreshKey] = useState(0);

  const fetchRadios = useCallback(async () => {
    try {
      const resp = await wifiClient.listRadios({});
      // S1G first, then other bands by enum order. Stable across re-renders.
      const sorted = [...(resp.radios ?? [])].sort((a, b) => {
        if (a.band === BAND_S1G && b.band !== BAND_S1G) return -1;
        if (b.band === BAND_S1G && a.band !== BAND_S1G) return 1;
        return (a.band ?? 0) - (b.band ?? 0);
      });
      setRadios(sorted);
      setError(null);
    } catch (e) {
      setError('Failed to load radios: ' + e.message);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    // Fetch-on-mount: pull the radio list from the daemon.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    fetchRadios();
  }, [fetchRadios]);

  const onCardChange = useCallback((radioName, info) => {
    setCards(prev => ({ ...prev, [radioName]: info }));
  }, []);

  const handleDecoded = useCallback((payload) => {
    const halowRadio = radios.find(r => r.band === BAND_S1G);
    const backhaulRadio = radios.find(r => r.band !== BAND_S1G && isMeshMode(cards[r.name]?.draft?.mode));
    const nonce = Date.now();
    const prefills = {};
    if (halowRadio) {
      prefills[halowRadio.name] = { nonce, fields: fieldsFromCredentials(payload.halow, cards[halowRadio.name]?.availOpts) };
    }
    if (payload.backhaul && backhaulRadio) {
      prefills[backhaulRadio.name] = { nonce, fields: fieldsFromCredentials(payload.backhaul, cards[backhaulRadio.name]?.availOpts) };
    }
    setJoin({
      sourceHostname: payload.sourceHostname ?? '',
      halowRadio: halowRadio?.name ?? '',
      backhaulRadio: backhaulRadio?.name ?? '',
      hasBackhaul: Boolean(payload.backhaul),
      prefills,
    });
    setJoinStatus({ busy: false, ok: null, error: null });
  }, [radios, cards]);

  // Issues against the *current drafts*, so an operator's fix clears them.
  const issues = useMemo(() => {
    if (!join) return [];
    const out = [];
    const check = (radioName, isHalow) => {
      const card = cards[radioName];
      if (!card?.draft) {
        // GetRadioSettings for this target hasn't resolved yet (or errors
        // persistently, e.g. a radio with no linked iface). Block Join
        // rather than silently treating "no draft" as "no issue" — see
        // credentialsFromDraft, which would otherwise be called with null.
        out.push(`${radioName}: still loading — wait a moment`);
        return;
      }
      const creds = credentialsFromDraft(card.draft);
      const opts = {
        isHalow,
        channels: card.availOpts?.channels ?? [],
        bandwidths: (card.availOpts?.bandwidths ?? []).map(bandwidthMhzForHTMode).filter(Boolean),
        countryMaxLen: 2,
      };
      for (const issue of checkMeshCredentials(creds, opts)) {
        out.push(`${radioName}: ${issue.message}`);
      }
    };
    if (join.halowRadio) check(join.halowRadio, true);
    if (join.backhaulRadio) check(join.backhaulRadio, false);
    return out;
  }, [join, cards]);

  const doJoin = async () => {
    if (!join?.halowRadio) return;
    const halowDraft = cards[join.halowRadio]?.draft;
    const backhaulDraft = join.backhaulRadio ? cards[join.backhaulRadio]?.draft : null;
    // Belt and suspenders: the Join button is already disabled by `issues`
    // while a named target's draft hasn't loaded, but never dereference a
    // null draft here regardless of how doJoin gets invoked.
    if (!halowDraft || (join.backhaulRadio && !backhaulDraft)) return;
    setJoinStatus({ busy: true, ok: null, error: null });
    try {
      const request = {
        payload: {
          sourceHostname: join.sourceHostname,
          halow: credentialsFromDraft(halowDraft),
          backhaul: join.backhaulRadio ? credentialsFromDraft(backhaulDraft) : undefined,
        },
        halowRadio: join.halowRadio,
        backhaulRadio: join.backhaulRadio,
      };
      const resp = await applyMeshJoin(request);
      const parts = (resp.radios ?? []).map(r => {
        const role = r.role === MeshJoinRadioRole.BACKHAUL ? 'backhaul' : 'HaLow';
        return r.status === MeshJoinRadioStatus.SKIPPED ? `${role} skipped (${r.reason})` : `${r.radioName} (${role}) applied`;
      });
      setJoinStatus({ busy: false, ok: `Joined ${join.sourceHostname || 'mesh'}: ${parts.join(', ')}. Wireless is restarting; your connection may drop.`, error: null });
      setJoin(null);
      setRefreshKey(k => k + 1);
    } catch (e) {
      setJoinStatus({ busy: false, ok: null, error: 'Join failed: ' + e.message });
    }
  };

  const filledSummary = join && join.halowRadio
    ? `Filled ${join.halowRadio} (HaLow)${join.backhaulRadio ? ` and ${join.backhaulRadio} (backhaul)` : ''} from ${join.sourceHostname || 'the code'}. Review each radio, then press Join mesh.`
    : null;
  const backhaulSkipped = join && join.hasBackhaul && !join.backhaulRadio;

  return (
    <div className="settings-wireless">
      <h2 className="settings-h2">◇ Wireless Radios</h2>
      {error && <div className="settings-banner crit">{error}</div>}
      {loading ? (
        <div className="settings-loading"><div className="spinner"></div>Loading radios…</div>
      ) : (
        <div className="settings-wireless-list">
          <div className="lat-panel share-mesh">
            <div className="panel-head">
              <h3>Share Mesh</h3>
              <div className="actions">
                <button type="button" onClick={() => setRefreshKey(k => k + 1)}>refresh</button>
              </div>
            </div>
            <div className="share-mesh-grid">
              <MeshJoinQR refreshKey={refreshKey} />
              <div className="share-mesh-join">
                <h3>Join from QR</h3>
                <div className="share-mesh-help">
                  Photograph another node&apos;s Share Mesh code. Fields fill in below;
                  nothing changes until you press Join mesh.
                </div>
                <QrScanInput onDecoded={handleDecoded} />
                {join && !join.halowRadio && (
                  <div className="lat-alert crit">This node has no HaLow radio to join with.</div>
                )}
                {filledSummary && <div className="lat-alert ok">{filledSummary}</div>}
                {backhaulSkipped && <div className="lat-alert warn">{BACKHAUL_SKIPPED}</div>}
                {issues.length > 0 && (
                  <div className="lat-alert warn">
                    {issues.map(msg => <div key={msg}>{msg}</div>)}
                  </div>
                )}
                {join?.halowRadio && (
                  <button
                    type="button"
                    className="lat-btn primary share-mesh-join-btn"
                    onClick={doJoin}
                    disabled={issues.length > 0 || joinStatus.busy}
                  >
                    {joinStatus.busy ? 'Joining…' : 'Join mesh'}
                  </button>
                )}
                {join?.halowRadio && (
                  <div className="share-mesh-help">
                    Writes both radios and reloads wireless once. Your connection may drop while the radios restart.
                  </div>
                )}
                {joinStatus.ok && <div className="lat-alert ok">{joinStatus.ok}</div>}
                {joinStatus.error && <div className="lat-alert crit">{joinStatus.error}</div>}
              </div>
            </div>
          </div>

          {radios.length === 0 ? (
            <div className="lat-panel"><div className="empty-row">No radios detected.</div></div>
          ) : (
            radios.map(r => (
              <RadioCard
                key={r.name}
                radio={r}
                prefill={join?.prefills[r.name]}
                reloadKey={refreshKey}
                onCardChange={onCardChange}
              />
            ))
          )}
        </div>
      )}
    </div>
  );
}
