// =============================================================================
// StepReview.jsx — Final review + ApplySetup streaming RPC
// =============================================================================
//
// Renders a summary of every field the user entered, then on click
// kicks off the streaming ApplySetup RPC. The wizard tracks per-phase
// status as events arrive and renders three terminal cases:
//
//   stream completes with PHASE_TERMINAL success=true   → success page
//   stream completes with PHASE_TERMINAL success=false  → failure page
//   stream ends abruptly (network reload severed it)    → ambiguous page,
//                                                          poll GetSetupStatus
//                                                          for 60s

import { useEffect, useRef, useState } from 'react';
import { create } from '@bufbuild/protobuf';
import { timestampFromDate } from '@bufbuild/protobuf/wkt';
import { setupClient } from '../../services/setupClient.js';
import { resumeSetup } from '../../services/setupDismiss.js';
import { useSetup } from '../../contexts/SetupContext.jsx';
import {
  ApplySetupRequestSchema,
  ApplySetupResponse_Phase as Phase,
  ApplySetupResponse_Status as Status,
  MeshNodeProfileSchema,
  MeshRadioConfigSchema,
  MeshRole,
  UplinkSchema,
  RadioApProfileSchema,
  MeshBackhaulProfileSchema,
  WifiStaProfileSchema,
  UplinkType,
} from '../../gen/openmanet/setup/v1/setup_pb.js';
import { WifiEncryption } from '../../gen/openmanet/wifi_config/v1/wifi_config_pb.js';
import {
  ROLE_LABELS,
  MESH_POINT_MODE_LABELS,
  MESH_GATE_MODE_LABELS,
  UPLINK_TYPE_LABELS,
  ENCRYPTION_LABELS,
} from './labels.js';
import { meshJoinIssues } from './meshChannels.js';

// Phase metadata: each tuple is [enum, label] in the canonical order
// the events arrive. Phase 99 (TERMINAL) is excluded from the list and
// rendered separately in the terminal panel.
const PHASE_DEFS = [
  [Phase.VALIDATE,           'Validate'],
  [Phase.SNAPSHOT,           'Snapshot'],
  [Phase.RESET_WIRELESS,     'Reset wireless'],
  [Phase.RESET_NETWORK,      'Reset network'],
  [Phase.HOSTNAME,           'Hostname'],
  [Phase.SET_TIMEZONE,       'Timezone'],
  [Phase.BASE_NETWORK,       'Base network'],
  [Phase.WIRELESS_MESH,      'Mesh'],
  [Phase.PER_RADIO_AP_STA,   'Per-radio'],
  [Phase.SCENARIO_TOPOLOGY,  'Topology'],
  [Phase.BATMAN_ADV,         'Batman-adv'],
  [Phase.MESH11SD,           'mesh11sd'],
  [Phase.COMMIT,             'Commit'],
  [Phase.PASSWORD,           'Password'],
  [Phase.PERSIST_FLAGS,      'Persist flags'],
];

const POLL_INTERVAL_MS = 4000;
const POLL_TIMEOUT_MS  = 60000;

// rebootNotice is repeated on the review, success, and reconnecting
// screens. After the wizard's own network reload, openmanetd's
// address-reservation worker waits for peer gossip (first tick 125 s
// after bat0 comes up — internal/mgmt/mgmt.go), claims the node's final
// mesh address, and reboots the device: a second outage the operator
// must expect. mDNS follows the hostname, so the .local URL survives it.
function rebootNotice(hostname) {
  const mdns = `${hostname || 'hostname'}.local`;
  return `The device reboots itself about 2–3 minutes after apply and comes back on its final mesh address; ${mdns} keeps working.`;
}

export default function StepReview({ status }) {
  const { state } = useSetup();
  const [phaseMap, setPhaseMap] = useState({});  // phase enum → 'started'|'done'|'failed'
  const [phaseError, setPhaseError] = useState(null); // {phase, message}
  const [terminal, setTerminal] = useState(null); // 'success' | 'failure' | 'ambiguous'
  const [terminalResult, setTerminalResult] = useState(null);
  const [busy, setBusy] = useState(false);
  const cancelRef = useRef(false);

  useEffect(() => () => { cancelRef.current = true; }, []);

  const apply = async () => {
    setBusy(true);
    setPhaseMap({});
    setPhaseError(null);
    setTerminal(null);

    // Stamp the client's clock at apply time (not page load) so the
    // device's PHASE_SET_TIMEZONE step gets an accurate reference even
    // if the wizard sat open for a while before the user clicked Apply.
    const profile = profileToProto(state);
    profile.clientTime = timestampFromDate(new Date());
    const req = create(ApplySetupRequestSchema, { profile });
    let sawTerminal = false;

    try {
      const stream = setupClient.applySetup(req);

      for await (const ev of stream) {
        if (cancelRef.current) break;

        if (ev.phase === Phase.TERMINAL) {
          sawTerminal = true;
          setTerminalResult(ev.result ?? null);
          setTerminal(ev.result?.success ? 'success' : 'failure');
          if (ev.result?.success) {
            // Clear the session "Skip for now" flag: a completed wizard
            // must not leave a stale dismiss banner behind.
            resumeSetup();
          }
          // Notify the BeforeUnload guard so the success-page
          // navigation doesn't trigger a confirmation prompt.
          window.dispatchEvent(new Event('setup-applied'));
          break;
        }

        const status = statusKey(ev.status);
        if (status === 'failed') {
          setPhaseError({ phase: ev.phase, message: ev.message });
        }
        setPhaseMap(prev => ({ ...prev, [ev.phase]: status }));
      }
    } catch (err) {
      if (cancelRef.current) return;

      // Stream ended abruptly. Either the apply succeeded and the
      // network reload severed our connection, or it really failed.
      // Poll GetSetupStatus to disambiguate.
      if (!sawTerminal) {
        setTerminal('ambiguous');
        await pollForCompletion({
          onSuccess: () => {
            if (cancelRef.current) return;
            setTerminal('success');
            resumeSetup();
            window.dispatchEvent(new Event('setup-applied'));
          },
          onTimeout: () => {
            if (cancelRef.current) return;
            // Stay on the ambiguous page — show the user the expected
            // SSID/URL and let them try the new connection.
          },
        });
      } else if (!terminal) {
        setPhaseError({ phase: Phase.UNSPECIFIED, message: String(err.message || err) });
        setTerminal('failure');
      }
    } finally {
      setBusy(false);
    }
  };

  if (terminal === 'success') {
    return <SuccessPanel state={state} result={terminalResult} />;
  }

  if (terminal === 'failure') {
    return <FailurePanel error={phaseError} onRetry={() => { setTerminal(null); }} />;
  }

  if (terminal === 'ambiguous') {
    return <AmbiguousPanel state={state} />;
  }

  const blockers = applyBlockers(state, status);

  return (
    <div className="setup-step">
      <h3>Review &amp; Apply</h3>

      <div className="lat-alert crit">
        <div>
          Applying these settings will reload network services. The device
          will likely move to a new IP address and SSID; you&apos;ll need to
          reconnect.
        </div>
        <div>{rebootNotice(state.hostname)}</div>
      </div>

      <ReviewSummary state={state} />

      {blockers.length > 0 && !busy && (
        <div className="lat-alert warn" role="alert">
          <div>Apply is disabled. Fix the following before continuing:</div>
          <ul style={{ margin: '6px 0 0 20px' }}>
            {blockers.map((m, i) => <li key={i}>{m}</li>)}
          </ul>
        </div>
      )}

      <div className="setup-nav">
        <button
          type="button"
          className="lat-btn primary"
          disabled={busy || blockers.length > 0}
          onClick={apply}
        >
          {busy ? 'Applying…' : 'Apply'}
        </button>
      </div>

      {busy && (
        <ul className="setup-apply-phases" aria-label="Apply progress">
          {PHASE_DEFS.map(([phase, label]) => {
            const st = phaseMap[phase];
            return (
              <li key={phase}>
                <span className={`phase-status ${st ?? ''}`}>
                  {st ?? '—'}
                </span>
                <span>{label}</span>
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}

function statusKey(status) {
  switch (status) {
    case Status.STARTED: return 'started';
    case Status.DONE:    return 'done';
    case Status.FAILED:  return 'failed';
    default:             return undefined;
  }
}

// applyBlockers returns the list of human-readable reasons the user
// cannot click Apply yet. Empty array means good to go.
function applyBlockers(state, status) {
  const out = [];
  if (!state.hostname) {
    out.push('Hostname is empty (Step 1).');
  }
  if (!state.mesh.radioName) {
    out.push('Mesh radio not selected (Step 2).');
  }
  if (!state.mesh.meshId) {
    out.push('Mesh ID is empty (Step 2).');
  }
  if (!state.mesh.countryCode) {
    out.push('Regulatory country not selected (Step 2).');
  }
  if (!state.adminPassword || state.adminPassword.length < 8) {
    out.push('Admin password must be at least 8 characters (Password step).');
  } else if (state.adminPassword !== state.adminPasswordConfirm) {
    out.push('Admin password and confirmation do not match (Password step).');
  }
  for (const ap of state.aps) {
    if (!ap.meshBackhaul) continue;
    if (!ap.backhaulMeshId) {
      out.push(`Mesh backhaul on ${ap.radioName} needs a mesh ID (Wi-Fi step).`);
    }
    if (!ap.backhaulPassphrase || ap.backhaulPassphrase.length < 8) {
      out.push(`Mesh backhaul on ${ap.radioName} needs a passphrase of at least 8 characters (Wi-Fi step).`);
    }
    if (ap.backhaulMeshId && ap.backhaulMeshId.length > 32) {
      out.push(`Mesh backhaul on ${ap.radioName} mesh ID must be 32 characters or fewer (Wi-Fi step).`);
    }
  }
  for (const ap of state.aps) {
    if (!ap.meshBackhaul) continue;
    if ((ap.backhaulChannel || 0) === 0 !== ((ap.backhaulBandwidthMhz || 0) === 0)) {
      out.push(`Mesh backhaul on ${ap.radioName}: set bandwidth and channel together or leave both at Default (Wi-Fi step).`);
    }
  }
  for (const msg of meshJoinIssues(state, status)) {
    out.push(`${msg} (Step 2).`);
  }
  return out;
}

// profileToProto converts the wizard's reducer state into a
// MeshNodeProfile message for ApplySetup. Field names match the proto
// `name` field, NOT the JS localName.
function profileToProto(state) {
  const profile = create(MeshNodeProfileSchema, {
    hostname:      state.hostname,
    adminPassword: state.adminPassword,
    role:          state.role,
    timezone:      state.timezone,
    mesh: create(MeshRadioConfigSchema, {
      radioName:    state.mesh.radioName,
      meshId:       state.mesh.meshId,
      passphrase:   state.mesh.passphrase,
      encryption:   state.mesh.encryption,
      bandwidthMhz: state.mesh.bandwidthMhz,
      channel:      state.mesh.channel,
      countryCode:  state.mesh.countryCode,
    }),
    aps: state.aps.filter(a => a.enabled || a.meshBackhaul).map(a => create(RadioApProfileSchema, {
      radioName:  a.radioName,
      enabled:    a.enabled,
      ssid:       a.enabled ? a.ssid : '',
      passphrase: a.enabled ? a.passphrase : '',
      // A backhaul entry carries no AP credentials, but the proto still
      // requires a defined non-zero encryption — send SAE.
      encryption: a.enabled ? a.encryption : WifiEncryption.SAE,
      meshBackhaul: a.meshBackhaul
        ? create(MeshBackhaulProfileSchema, {
            meshId:       a.backhaulMeshId,
            passphrase:   a.backhaulPassphrase,
            bandwidthMhz: a.backhaulBandwidthMhz || 0,
            channel:      a.backhaulChannel || 0,
            countryCode:  a.backhaulCountryCode || '',
          })
        : undefined,
    })),
  });

  if (state.role === MeshRole.MESH_POINT) {
    profile.deviceMode = { case: 'meshpointMode', value: state.meshpointMode };
  } else if (state.role === MeshRole.MESH_GATE) {
    profile.deviceMode = { case: 'meshgateMode',  value: state.meshgateMode  };
    profile.uplink = create(UplinkSchema, {
      type:         state.uplink.type,
      ethernetPort: state.uplink.ethernetPort,
      wireless: state.uplink.type === UplinkType.WIRELESS_STA
        ? create(WifiStaProfileSchema, {
            radioName:  state.uplink.wireless.radioName,
            ssid:       state.uplink.wireless.ssid,
            passphrase: state.uplink.wireless.passphrase,
            encryption: state.uplink.wireless.encryption,
          })
        : undefined,
    });
  }

  return profile;
}

async function pollForCompletion({ onSuccess, onTimeout }) {
  const deadline = Date.now() + POLL_TIMEOUT_MS;
  while (Date.now() < deadline) {
    try {
      const resp = await setupClient.getSetupStatus({});
      if (resp.isSetupComplete) { onSuccess(); return; }
    } catch {
      /* network errors are expected during reload; keep polling */
    }
    await new Promise(r => setTimeout(r, POLL_INTERVAL_MS));
  }
  onTimeout();
}

function ReviewSummary({ state }) {
  const apEntries = state.aps.filter(a => a.enabled);
  const backhaul = state.aps.find(a => a.meshBackhaul);
  return (
    <>
      <div className="kv"><span className="k">Hostname</span><span className="v">{state.hostname}</span></div>
      <div className="kv"><span className="k">Role</span><span className="v">{ROLE_LABELS[state.role] ?? '?'}</span></div>
      {state.role === MeshRole.MESH_POINT && (
        <div className="kv">
          <span className="k">Mesh point mode</span>
          <span className="v">{MESH_POINT_MODE_LABELS[state.meshpointMode] ?? '?'}</span>
        </div>
      )}
      {state.role === MeshRole.MESH_GATE && (
        <>
          <div className="kv">
            <span className="k">Gate mode</span>
            <span className="v">{MESH_GATE_MODE_LABELS[state.meshgateMode] ?? '?'}</span>
          </div>
          <div className="kv">
            <span className="k">Uplink</span>
            <span className="v">{UPLINK_TYPE_LABELS[state.uplink.type] ?? '?'}</span>
          </div>
        </>
      )}
      <div className="kv"><span className="k">Mesh radio</span><span className="v">{state.mesh.radioName}</span></div>
      <div className="kv"><span className="k">Mesh ID</span><span className="v">{state.mesh.meshId}</span></div>
      {state.meshJoin && (
        <div className="kv"><span className="k">Source</span><span className="v accent">scanned from {state.meshJoin.sourceHostname || 'another node'}</span></div>
      )}
      <div className="kv">
        <span className="k">Country</span>
        <span className="v">{state.mesh.countryCode || '(not set)'}</span>
      </div>
      <div className="kv">
        <span className="k">Channel / bandwidth</span>
        <span className="v">{state.mesh.channel} @ {state.mesh.bandwidthMhz} MHz</span>
      </div>
      <div className="kv">
        <span className="k">Mesh encryption</span>
        <span className="v">{ENCRYPTION_LABELS[state.mesh.encryption] ?? '?'}</span>
      </div>
      <div className="kv">
        <span className="k">Client APs</span>
        <span className="v">
          {apEntries.length === 0
            ? 'none'
            : apEntries.map(a => `${a.radioName}: ${a.ssid}`).join(', ')}
        </span>
      </div>
      <div className="kv">
        <span className="k">Mesh backhaul</span>
        <span className="v">{backhaul ? `${backhaul.radioName}: ${backhaul.backhaulMeshId}` : 'none'}</span>
      </div>
      {backhaul && (
        <div className="kv">
          <span className="k">Backhaul channel</span>
          <span className="v">{backhaul.backhaulChannel || 8} · {backhaul.backhaulBandwidthMhz || 40} MHz · {backhaul.backhaulCountryCode || state.mesh.countryCode || '—'}</span>
        </div>
      )}
    </>
  );
}

function SuccessPanel({ state, result }) {
  const url = result?.expectedUrl || `https://${state.hostname}.local:8081/login`;
  const ssid = result?.expectedSsid;
  return (
    <div className="setup-step" role="status">
      <h3>Setup Complete</h3>
      <div className="lat-alert ok">Your device is configured.</div>
      <p>To continue managing this device:</p>
      <ol>
        {ssid && <li>Connect to the new mesh AP: <strong>{ssid}</strong></li>}
        <li>Open <a href={url}>{url}</a></li>
        <li>Sign in with the admin password you just set.</li>
      </ol>
      <p className="setup-help">{rebootNotice(state.hostname)}</p>
    </div>
  );
}

function FailurePanel({ error, onRetry }) {
  return (
    <div className="setup-step" role="alert">
      <h3>Setup Failed</h3>
      <div className="lat-alert crit">
        {error?.message ?? 'The wizard could not complete.'}
        {error?.phase !== undefined && (
          <div className="setup-help" style={{ marginTop: 4 }}>
            Failed phase: {Phase[error.phase] ?? error.phase}
          </div>
        )}
      </div>
      <div className="setup-nav">
        <button type="button" className="lat-btn primary" onClick={onRetry}>Back to review</button>
      </div>
    </div>
  );
}

function AmbiguousPanel({ state }) {
  const url = `https://${state.hostname}.local:8081/login`;
  return (
    <div className="setup-step" role="status">
      <h3>Confirming…</h3>
      <p>
        The wizard&apos;s connection to the device dropped during setup —
        this is expected when the network reconfigures. We&apos;re checking
        whether setup completed.
      </p>
      <p className="setup-help">
        If this takes more than a minute, try connecting now: <a href={url}>{url}</a>
      </p>
      <p className="setup-help">{rebootNotice(state.hostname)}</p>
    </div>
  );
}
