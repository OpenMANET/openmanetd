// =============================================================================
// BLOS.jsx — Beyond Line of Sight (VPN/Tailscale) configuration page
// =============================================================================
// The Lattice layout includes panels for traffic, DERP relay, overlay peers,
// ACL tags, and a keepalive log. All panels bind to data exposed by
// BLOSService (GetBLOSStatus / ListBLOSPeers / StreamBLOSEvents). When the
// BLOS subsystem is not running, the RPCs return zero-valued or empty
// structures and the UI renders em-dashes.

import { useState, useEffect, useRef } from 'react';
import { createClient } from "@connectrpc/connect";
import { transport } from "../services/connectClient.js";
import { BLOSService } from "../gen/openmanet/blos/v1/blos_service_pb.js";
import { BLOSEventKind } from "../gen/openmanet/blos/v1/blos_pb.js";
import { pushSparklineSample, useSparklineSamples } from '../services/sparklineStore.js';
import { useBLOSStatus, refreshBLOSStatus } from '../hooks/useBLOSStatus.js';
import './BLOS.css';

// Keys for the shared sparkline store. Keep them in sync with every
// producer/consumer or the two sides would quietly operate on separate
// buffers.
const RX_SERIES_KEY = 'blos.rxBytesPerSec';
const TX_SERIES_KEY = 'blos.txBytesPerSec';

const blosClient = createClient(BLOSService, transport);

// Event-log cap. The keepalive panel shows the most recent N events — older
// entries are discarded so the panel never grows unbounded.
const MAX_EVENTS = 50;

// Rolling traffic-history window for the RX/TX spark bars.
const TRAFFIC_HISTORY_CAP = 12;

const EVENT_KIND_LABELS = {
  [BLOSEventKind.BACKEND_STATE]: 'STATE',
  [BLOSEventKind.PEER_ADDED]: 'PEER+',
  [BLOSEventKind.PEER_LOST]: 'PEER-',
  [BLOSEventKind.PEER_ONLINE]: 'ONLINE',
  [BLOSEventKind.PEER_OFFLINE]: 'OFFLINE',
  [BLOSEventKind.DERP_CHANGED]: 'DERP',
  [BLOSEventKind.KEEPALIVE]: 'KA',
};

function formatKbps(bytesPerSec) {
  if (!bytesPerSec || bytesPerSec <= 0) return '—';
  const kbps = (bytesPerSec * 8) / 1000;
  if (kbps < 10) return kbps.toFixed(1);
  return Math.round(kbps).toString();
}

function formatBytes(n) {
  if (!n) return '—';
  const v = Number(n);
  if (v < 1024) return `${v} B`;
  if (v < 1024 * 1024) return `${(v / 1024).toFixed(1)} KiB`;
  if (v < 1024 * 1024 * 1024) return `${(v / (1024 * 1024)).toFixed(1)} MiB`;
  return `${(v / (1024 * 1024 * 1024)).toFixed(2)} GiB`;
}

function formatSince(ts) {
  if (!ts?.seconds) return '—';
  const ms = Number(ts.seconds) * 1000;
  const diff = Math.max(0, Date.now() - ms);
  const s = Math.floor(diff / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ${s % 60}s`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ${m % 60}m`;
  const d = Math.floor(h / 24);
  return `${d}d ${h % 24}h`;
}

function formatLastSeen(ts) {
  if (!ts?.seconds) return '—';
  const diff = Math.max(0, Date.now() - Number(ts.seconds) * 1000);
  const s = Math.floor(diff / 1000);
  if (s < 60) return `${s}s ago`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  return `${Math.floor(h / 24)}d ago`;
}

function stripTrailingDot(s) {
  return s && s.endsWith('.') ? s.slice(0, -1) : s || '';
}

// Tiny bar-style sparkline matching the mockup's `.spark` primitive.
// `data` is a rolling array of non-negative numbers (newest last). Bar heights
// scale relative to the window max so a quiet tunnel still produces visible
// bars when traffic is present. A minimum of 2% prevents disappearing slivers.
function SparkBars({ data, variant }) {
  if (!data || data.length === 0) return <div className="spark blos-spark-empty" />;
  const max = Math.max(...data, 1);
  return (
    <div className={`spark${variant ? ' ' + variant : ''} blos-spark`}>
      {data.map((v, i) => {
        const pct = Math.max(2, (Number(v) / max) * 100);
        return <span key={i} style={{ height: `${pct}%` }} />;
      })}
    </div>
  );
}

export default function BLOSPage() {
  const [events, setEvents] = useState([]);
  // RX/TX traffic series live in the module-scoped sparkline store so
  // the bars persist across navigation — component-local useState was
  // resetting to [] every time the user left and returned to the page.
  const rxHistory = useSparklineSamples(RX_SERIES_KEY);
  const txHistory = useSparklineSamples(TX_SERIES_KEY);
  // Shared with the Dashboard chip — single store, single in-flight RPC.
  const snapshot = useBLOSStatus(10_000);
  const status = snapshot?.status ?? null;
  const peers = snapshot?.peers ?? [];
  const fetchError = snapshot?.error ?? null;
  const [enableBlos, setEnableBlos] = useState(false);
  const [authKey, setAuthKey] = useState('');
  const [loginServer, setLoginServer] = useState('');
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState(null);
  const [success, setSuccess] = useState(null);

  // Display either a save error (last write attempt) or a fetch error
  // surfaced by the shared store.
  const error = saveError ?? fetchError;

  // Loading is true only before the first snapshot publishes; once the
  // store has data (even an error envelope) we render the page so the
  // user sees the error banner instead of being stuck on "Loading…".
  const loading = snapshot === null;

  // Each new snapshot drives the sparkline buffers. Sparklines are an
  // external store (not React state), so writing them from an effect is the
  // correct pattern.
  useEffect(() => {
    if (!status) return;
    const rx = Number(status?.counters?.rxBytesPerSec ?? 0);
    const tx = Number(status?.counters?.txBytesPerSec ?? 0);
    pushSparklineSample(RX_SERIES_KEY, rx, TRAFFIC_HISTORY_CAP);
    pushSparklineSample(TX_SERIES_KEY, tx, TRAFFIC_HISTORY_CAP);
  }, [status]);

  // Mirror the server-reported enable bit into the local toggle so the UI
  // reflects the persisted state on first load and after external changes.
  useEffect(() => {
    if (!status) return;
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setEnableBlos(status.blosEnabled ?? false);
  }, [status]);

  // Event stream: open when BLOS is running, close on unmount or disable.
  const streamAbortRef = useRef(null);
  const running = status?.blosEnabled ?? false;

  useEffect(() => {
    if (!running) return undefined;

    const controller = new AbortController();
    streamAbortRef.current = controller;

    (async () => {
      try {
        const stream = blosClient.streamBLOSEvents({}, { signal: controller.signal });
        // Server wraps each BLOSEvent in a StreamBLOSEventsResponse envelope.
        // Unwrap to keep the events array holding bare BLOSEvent instances.
        for await (const resp of stream) {
          const ev = resp?.event;
          if (!ev) continue;
          setEvents((prev) => {
            const next = [...prev, ev];
            return next.length > MAX_EVENTS ? next.slice(-MAX_EVENTS) : next;
          });
        }
      } catch {
        // Stream aborted on unmount or network failure — swallow silently;
        // the next mount/re-enable will reopen it.
      }
    })();

    return () => {
      controller.abort();
      streamAbortRef.current = null;
    };
  }, [running]);

  const handleSave = async () => {
    setSaving(true);
    setSaveError(null);
    setSuccess(null);
    // Pasting an auth key is an intent to bring the tunnel up — the
    // operator should not also have to flip the toggle. The backend treats
    // enable_blos=false as "disable", so derive the flag here rather than
    // trusting the toggle alone.
    const enable = enableBlos || authKey.trim() !== '';
    try {
      const req = { enableBlos: enable, authKey };
      if (loginServer) req.loginServerUrl = loginServer;
      const resp = await blosClient.updateBLOSConfig(req);
      if (resp.success === false) throw new Error(resp.message || 'Update failed');
      setSuccess(resp.message || 'BLOS configuration updated.');
      setAuthKey('');
      // Mirror the requested state immediately so the toggle does not lag
      // behind until the next status poll lands.
      setEnableBlos(enable);
      refreshBLOSStatus();
    } catch (e) {
      setSaveError('Failed to update BLOS: ' + e.message);
    } finally {
      setSaving(false);
    }
  };

  if (loading) {
    return (
      <div className="blos-loading">Loading BLOS status...</div>
    );
  }

  const enabled = status?.blosEnabled ?? false;
  const statusMsg = status?.message || '';
  const identity = status?.identity;
  const derp = status?.derp;
  const counters = status?.counters;
  const network = status?.network;

  const overlayIp = identity?.overlayIps?.[0] || '—';
  const hostname = stripTrailingDot(identity?.dnsName) || identity?.hostname || '—';
  const since = formatSince(identity?.connectedSince);

  return (
    <>
      <div className="lat-topbar">
        <div className="node-id">
          BLOS
          <span className="ip">{enabled ? 'TUNNEL UP' : 'TUNNEL DOWN'}</span>
        </div>
        <div className="chips">
          <span className={`lat-chip ${enabled ? 'ok' : 'crit'}`}>
            <span className="dot" />
            {enabled ? 'ENABLED' : 'DISABLED'}
          </span>
        </div>
      </div>
      <div className="lat-view-header">
        <div>
          <h2>◇ BLOS · Beyond Line of Sight</h2>
          <div className="crumb">Tailscale overlay</div>
        </div>
        <div className="lat-view-toolbar">
          <button className="lat-btn ghost" type="button" title="Rotate authentication key">ROTATE KEY</button>
        </div>
      </div>

      {error && <div className="blos-banner crit">{error}</div>}
      {success && <div className="blos-banner ok">{success}</div>}

      <div className="lat-body blos-grid">

        {/* ── Tunnel status card ───────────────────────────────── */}
        <div className="lat-panel">
          <div className="panel-head"><h3>Tunnel</h3></div>
          <div className={`big-num ${enabled ? 'ok' : 'crit'}`}>
            {enabled ? 'Enabled' : 'Disabled'}
          </div>
          {statusMsg && <div className="blos-sub">{statusMsg}</div>}
          <div className="kv"><span className="k">Overlay IP</span><span className="v">{overlayIp}</span></div>
          <div className="kv"><span className="k">Hostname</span><span className="v">{hostname}</span></div>
          <div className="kv"><span className="k">Since</span><span className="v">{since}</span></div>
        </div>

        {/* ── RX / TX / DERP KPI cards ─────────────────────────── */}
        <div className="lat-panel">
          <div className="panel-head"><h3>RX · 60s</h3></div>
          <div className="big-num">
            {formatKbps(counters?.rxBytesPerSec)}
            <span className="unit">kbps</span>
          </div>
          <SparkBars data={rxHistory} />
          <div className="kv">
            <span className="k">Total RX</span>
            <span className="v">{formatBytes(counters?.rxBytesTotal)}</span>
          </div>
        </div>

        <div className="lat-panel">
          <div className="panel-head"><h3>TX · 60s</h3></div>
          <div className="big-num">
            {formatKbps(counters?.txBytesPerSec)}
            <span className="unit">kbps</span>
          </div>
          <SparkBars data={txHistory} variant="warn" />
          <div className="kv">
            <span className="k">Total TX</span>
            <span className="v">{formatBytes(counters?.txBytesTotal)}</span>
          </div>
        </div>

        <div className="lat-panel">
          <div className="panel-head"><h3>DERP Relay</h3></div>
          <div className="kv"><span className="k">Region</span><span className="v">{derp?.region || '—'}</span></div>
          <div className="kv">
            <span className="k">Latency</span>
            <span className="v">{derp?.latencyMs ? `${derp.latencyMs} ms` : '—'}</span>
          </div>
          <div className="kv">
            <span className="k">Path</span>
            <span className={`v${derp?.path === 'derp' ? ' warn' : ''}`}>{derp?.path || '—'}</span>
          </div>
          <div className="kv"><span className="k">Endpoint</span><span className="v mono-v">{derp?.endpoint || '—'}</span></div>
          <div className="kv">
            <span className="k">Keepalive</span>
            <span className="v">
              {derp?.keepaliveInterval?.seconds
                ? `${Number(derp.keepaliveInterval.seconds)}s`
                : '—'}
            </span>
          </div>
        </div>

        {/* ── Overlay peers + ACL tags ───────────────────────── */}
        <div className="lat-panel col-span-3">
          <div className="panel-head">
            <h3>Overlay Peers</h3>
            <span className="panel-head-right">{peers.length} visible</span>
          </div>
          {peers.length === 0 ? (
            <div className="blos-empty">
              {enabled ? 'No overlay peers yet.' : 'BLOS is disabled.'}
            </div>
          ) : (
            <div className="blos-peers">
              <div className="blos-peer-row head">
                <span>Host</span>
                <span>Overlay IP</span>
                <span>Endpoint</span>
                <span>Path</span>
                <span>RX</span>
                <span>TX</span>
                <span>Last seen</span>
              </div>
              {peers.map((p) => (
                <div className="blos-peer-row" key={p.dnsName || p.hostname}>
                  <span className="blos-peer-host">
                    <span className={`dot ${p.online ? 'ok' : 'crit'}`} />
                    {p.hostname || stripTrailingDot(p.dnsName) || '—'}
                  </span>
                  <span className="mono-v">{p.overlayIps?.[0] || '—'}</span>
                  <span className="mono-v">{p.endpoint || '—'}</span>
                  <span className={p.path === 'derp' ? 'warn' : ''}>{p.path || '—'}</span>
                  <span>{formatBytes(p.rxBytes)}</span>
                  <span>{formatBytes(p.txBytes)}</span>
                  <span>{formatLastSeen(p.lastSeen)}</span>
                </div>
              ))}
            </div>
          )}
        </div>

        <div className="lat-panel">
          <div className="panel-head"><h3>ACL / Tags</h3></div>
          <div className="kv">
            <span className="k">Exit node</span>
            <span className="v">{network?.exitNode ? 'Yes' : 'No'}</span>
          </div>
          <div className="kv">
            <span className="k">Subnet routes</span>
            <span className="v">{network?.advertisedRoutes?.join(', ') || '—'}</span>
          </div>
          <div className="kv">
            <span className="k">Tags</span>
            <span className="v">{network?.aclTags?.join(', ') || '—'}</span>
          </div>
        </div>

        {/* ── Configuration ──────────────────────────────────── */}
        <div className="lat-panel col-span-2">
          <div className="panel-head"><h3>Configuration</h3></div>

          <div className="blos-toggle-row">
            <label>Enable BLOS</label>
            <button
              type="button"
              className={`lat-toggle${enableBlos ? ' on' : ''}`}
              onClick={() => setEnableBlos(!enableBlos)}
            >
              <span className="track"><span className="thumb" /></span>
              <span className="label">{enableBlos ? 'On' : 'Off'}</span>
            </button>
          </div>

          <div className="lat-field">
            <label htmlFor="blos-auth-key">Auth Key</label>
            <input
              id="blos-auth-key"
              className="lat-input"
              type="password"
              value={authKey}
              onChange={(e) => setAuthKey(e.target.value)}
              placeholder="Paste auth key"
              autoComplete="off"
            />
            <span className="hint">Never echoed back · blank keeps current · entering a key enables BLOS</span>
          </div>

          <div className="lat-field">
            <label htmlFor="blos-login-server">Login Server URL (optional)</label>
            <input
              id="blos-login-server"
              className="lat-input"
              value={loginServer}
              onChange={(e) => setLoginServer(e.target.value)}
              placeholder="https://headscale.example.com"
              autoComplete="off"
            />
          </div>

          <div className="blos-actions">
            <button
              className="lat-btn primary"
              onClick={handleSave}
              disabled={saving}
              type="button"
            >
              {saving ? 'Saving…' : 'Update BLOS Config'}
            </button>
          </div>
        </div>

        <div className="lat-panel col-span-2">
          <div className="panel-head"><h3>Keepalive &amp; Events</h3></div>
          {events.length === 0 ? (
            <div className="blos-empty">
              {enabled ? 'Waiting for events…' : 'BLOS is disabled.'}
            </div>
          ) : (
            <div className="blos-events">
              {events.slice().reverse().map((ev, i) => (
                <div className="blos-event-row" key={`${ev.ts?.seconds}-${ev.ts?.nanos}-${i}`}>
                  <span className="blos-event-kind">
                    {EVENT_KIND_LABELS[ev.kind] || 'EVT'}
                  </span>
                  <span className="blos-event-subject">{ev.subject || '—'}</span>
                  <span className="blos-event-msg">{ev.message}</span>
                </div>
              ))}
            </div>
          )}
        </div>

      </div>
    </>
  );
}
