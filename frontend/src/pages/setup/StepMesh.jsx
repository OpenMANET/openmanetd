// =============================================================================
// StepMesh.jsx — Mesh radio configuration (mesh ID, passphrase, channel,
//                bandwidth) plus role-conditional sub-mode select
// =============================================================================
//
// The mesh-radio dropdown is filtered to HaLow radios only. When the
// device has exactly one HaLow radio (the common case), the field
// renders as a read-only display rather than a dropdown. Bandwidth is
// the parent control: changing it filters the channel list to legal
// values for that bandwidth (channel data comes from
// SetupRadio.bandwidths returned by GetSetupStatus).

import { useEffect, useMemo } from 'react';
import LatSelect from '../../components/LatSelect.jsx';
import QrScanInput from '../../components/QrScanInput.jsx';
import { useSetup, SETUP_ACTIONS } from '../../contexts/SetupContext.jsx';
import {
  MeshRole,
  MeshPointMode,
} from '../../gen/openmanet/setup/v1/setup_pb.js';
import { WifiEncryption } from '../../gen/openmanet/wifi_config/v1/wifi_config_pb.js';
import {
  ENCRYPTION_LABELS,
  MESH_POINT_MODE_LABELS,
  MESH_GATE_MODE_LABELS,
  optionsFromMap,
} from './labels.js';
import {
  findCountryEntry,
  channelsForCountryBandwidth,
  bandwidthsForCountry,
  meshJoinIssues,
} from './meshChannels.js';

// Selectable mesh-point modes. MESH_POINT_MODE_NONE is hidden here (and
// rejected by validateProfile on the backend) until openmanetd's
// address-reservation worker can leave a DHCP-client ahwlan alone —
// wizard-parity ledger decision D1 (2026-08-27). The label stays in
// MESH_POINT_MODE_LABELS so the review summary can still name it.
const HIDDEN_MESH_POINT_MODES = new Set([MeshPointMode.UNSPECIFIED, MeshPointMode.NONE]);
const MESH_POINT_MODE_OPTIONS = optionsFromMap(MESH_POINT_MODE_LABELS)
  .filter(o => !HIDDEN_MESH_POINT_MODES.has(o.value));

const BANDWIDTH_LABELS = {
  1: '1 MHz',
  2: '2 MHz',
  4: '4 MHz',
  8: '8 MHz',
};

export default function StepMesh({ status }) {
  const { state, dispatch } = useSetup();
  const halowRadios = useMemo(
    () => (status?.radios ?? []).filter(r => r.isHalow),
    [status],
  );

  const countries = useMemo(() => status?.countries ?? [], [status]);

  // If the user hasn't picked a radio (HYDRATE_FROM_STATUS missed it
  // somehow), pick the first HaLow on render.
  useEffect(() => {
    if (!state.mesh.radioName && halowRadios.length > 0) {
      dispatch({ type: SETUP_ACTIONS.SET_MESH_FIELD, field: 'radioName', value: halowRadios[0].name });
    }
  }, [state.mesh.radioName, halowRadios, dispatch]);

  const countryEntry = useMemo(
    () => findCountryEntry(countries, state.mesh.countryCode),
    [countries, state.mesh.countryCode],
  );

  const legalBandwidths = useMemo(
    () => bandwidthsForCountry(countryEntry),
    [countryEntry],
  );

  // Snap bandwidth to a legal value when the country changes (e.g.
  // some EU countries only allow 1MHz / 2MHz HaLow). A scanned code
  // holds its own values instead — meshJoinIssues surfaces the warning
  // and the operator resolves it manually.
  useEffect(() => {
    if (state.mesh.fromScan) return;
    if (legalBandwidths.length === 0) return;
    if (!legalBandwidths.includes(state.mesh.bandwidthMhz)) {
      dispatch({ type: SETUP_ACTIONS.SET_MESH_FIELD, field: 'bandwidthMhz', value: legalBandwidths[0] });
    }
  }, [legalBandwidths, state.mesh.bandwidthMhz, state.mesh.fromScan, dispatch]);

  const channels = useMemo(
    () => channelsForCountryBandwidth(countryEntry, state.mesh.bandwidthMhz),
    [countryEntry, state.mesh.bandwidthMhz],
  );

  // Snap channel to the first legal one when (country, bandwidth)
  // change. Gated the same way as the bandwidth snap above.
  useEffect(() => {
    if (state.mesh.fromScan) return;
    if (channels.length === 0) return;
    if (!channels.includes(state.mesh.channel)) {
      dispatch({ type: SETUP_ACTIONS.SET_MESH_FIELD, field: 'channel', value: channels[0] });
    }
  }, [channels, state.mesh.channel, state.mesh.fromScan, dispatch]);

  const issues = useMemo(() => meshJoinIssues(state, status), [state, status]);

  return (
    <div className="setup-step">
      <h3>Mesh Configuration</h3>

      <div className="setup-scan">
        <div className="setup-scan-title">Have a QR code from another node?</div>
        <div className="setup-help">Photograph its Share Mesh code to fill this step.</div>
        <QrScanInput
          onDecoded={(payload) => dispatch({ type: SETUP_ACTIONS.APPLY_MESH_JOIN, payload, radios: status?.radios ?? [] })}
        />
        {state.meshJoin && (
          <div className={issues.length > 0 ? 'lat-alert warn' : 'lat-alert ok'}>
            <div>Scanned from {state.meshJoin.sourceHostname || 'another node'}.</div>
            {state.meshJoin.backhaulSkippedReason === 'no-capable-radio' && (
              <div>Backhaul skipped: no radio on this device can run the 2.4 GHz mesh backhaul.</div>
            )}
            {issues.map(msg => <div key={msg}>{msg}</div>)}
          </div>
        )}
      </div>

      {halowRadios.length === 0 && (
        <div className="lat-alert crit">
          No HaLow radio detected. The wizard cannot continue without
          one — see the previous screen for details.
        </div>
      )}

      {halowRadios.length === 1 ? (
        <div className="lat-field">
          <label>Mesh radio</label>
          <div className="lat-input" style={{ pointerEvents: 'none' }}>
            {halowRadios[0].name}
            {halowRadios[0].hardwareName && (
              <span className="setup-help" style={{ marginLeft: 8 }}>
                ({halowRadios[0].hardwareName})
              </span>
            )}
          </div>
        </div>
      ) : (
        <div className="lat-field">
          <label htmlFor="setup-mesh-radio">Mesh radio</label>
          <LatSelect
            ariaLabel="Mesh radio"
            value={state.mesh.radioName}
            options={halowRadios.map(r => ({ value: r.name, label: r.name }))}
            onChange={(v) => dispatch({ type: SETUP_ACTIONS.SET_MESH_FIELD, field: 'radioName', value: v })}
          />
        </div>
      )}

      <div className="lat-field">
        <label htmlFor="setup-mesh-id">Mesh ID</label>
        <input
          id="setup-mesh-id"
          className="lat-input"
          type="text"
          value={state.mesh.meshId}
          onChange={(e) => dispatch({ type: SETUP_ACTIONS.SET_MESH_FIELD, field: 'meshId', value: e.target.value })}
          maxLength={32}
          autoComplete="off"
          spellCheck={false}
        />
        <div className="setup-help">All nodes on the same mesh must share this ID.</div>
      </div>

      <div className="lat-field">
        <label>Encryption</label>
        <div className="lat-input" style={{ pointerEvents: 'none' }}>
          {ENCRYPTION_LABELS[WifiEncryption.SAE]}
        </div>
        <div className="setup-help">
          Mesh links are always WPA3 (SAE). Open meshes are not supported.
        </div>
      </div>

      <div className="lat-field">
        <label htmlFor="setup-mesh-pass">Mesh passphrase</label>
        <input
          id="setup-mesh-pass"
          className="lat-input"
          type="password"
          value={state.mesh.passphrase}
          onChange={(e) => dispatch({ type: SETUP_ACTIONS.SET_MESH_FIELD, field: 'passphrase', value: e.target.value })}
          minLength={8}
          maxLength={63}
          autoComplete="new-password"
        />
        {state.mesh.passphrase && state.mesh.passphrase.length < 8 && (
          <div className="setup-error">Passphrase must be at least 8 characters.</div>
        )}
      </div>

      <div className="lat-field">
        <label>Country</label>
        {countries.length === 0 ? (
          <div className="lat-input" style={{ pointerEvents: 'none' }}>
            US (regulatory database not installed; falling back to baked-in defaults)
          </div>
        ) : (
          <LatSelect
            ariaLabel="Regulatory country"
            value={state.mesh.countryCode}
            options={countries.map(c => ({
              value: c.code,
              label: `${c.code} — ${c.name || c.code}`,
            }))}
            onChange={(v) => dispatch({ type: SETUP_ACTIONS.SET_MESH_FIELD, field: 'countryCode', value: v })}
          />
        )}
        <div className="setup-help">
          Sets the regulatory domain on the HaLow radio. Determines which
          channels and bandwidths are legal here.
        </div>
      </div>

      <div className="setup-field">
        <label>Bandwidth</label>
        <LatSelect
          ariaLabel="Mesh bandwidth"
          value={state.mesh.bandwidthMhz}
          options={legalBandwidths.map(mhz => ({
            value: mhz, label: BANDWIDTH_LABELS[mhz] ?? `${mhz} MHz`,
          }))}
          onChange={(v) => dispatch({ type: SETUP_ACTIONS.SET_MESH_FIELD, field: 'bandwidthMhz', value: v })}
        />
        <div className="setup-help">
          On 802.11ah, channel choice depends on bandwidth.
        </div>
      </div>

      <div className="setup-field">
        <label>Channel</label>
        <LatSelect
          ariaLabel="Mesh channel"
          value={state.mesh.channel}
          options={channels.map(c => ({ value: c, label: String(c) }))}
          onChange={(v) => dispatch({ type: SETUP_ACTIONS.SET_MESH_FIELD, field: 'channel', value: v })}
        />
      </div>

      {state.role === MeshRole.MESH_GATE && (
        <div className="setup-field">
          <label>Gate mode</label>
          <LatSelect
            ariaLabel="Mesh gate mode"
            value={state.meshgateMode}
            options={optionsFromMap(MESH_GATE_MODE_LABELS)}
            onChange={(v) => dispatch({ type: SETUP_ACTIONS.SET_MESHGATE_MODE, value: v })}
          />
        </div>
      )}

      {state.role === MeshRole.MESH_POINT && (
        <div className="setup-field">
          <label>Mesh point mode</label>
          <LatSelect
            ariaLabel="Mesh point mode"
            value={state.meshpointMode}
            options={MESH_POINT_MODE_OPTIONS}
            onChange={(v) => dispatch({ type: SETUP_ACTIONS.SET_MESHPOINT_MODE, value: v })}
          />
        </div>
      )}
    </div>
  );
}
