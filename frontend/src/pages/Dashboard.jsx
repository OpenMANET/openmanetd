// =============================================================================
// Dashboard.jsx — Mesh operator overview
// =============================================================================
//
// Fixed-grid tactical dashboard:
//   topbar: NODE id + MESH / GPS / BLOS chips + local-timezone clock
//   row 1:  3 KPI panels — Mesh Peers · Link Quality 5m · Battery & Power
//   row 2:  Mesh Peers Live (col-span-3) · Alerts (col-span-1)
//   row 3:  System Resources (col-span-2) · Network Interfaces (col-span-2)
//
// PTT latency is intentionally not on this page — it lives on the Comms page
// with the rest of the realtime audio instrumentation. The full topology
// graph has its own /topology route; the dashboard deliberately does not
// embed it.

import { useState, useEffect, useCallback, useMemo, useRef } from 'react';
import { createClient } from "@connectrpc/connect";
import { transport } from "../services/connectClient.js";
import { DashboardService } from "../gen/openmanet/dashboard/v1/dashboard_service_pb.js";
import { NetworkInterfaceState } from "../gen/openmanet/dashboard/v1/dashboard_pb.js";
import {
  InterfaceType,
  InterfaceStatus,
} from "../gen/openmanet/network_interface/v1/interface_pb.js";
import { useVisibleInterval } from '../hooks/useVisibleInterval.js';
import { useMeshStatus } from '../hooks/useMeshStatus.js';
import { useMeshTopology } from '../hooks/useMeshTopology.js';
import { useGnssStatus } from '../hooks/useGnssStatus.js';
import { useBLOSStatus } from '../hooks/useBLOSStatus.js';
import { useNetworkInterfaces } from '../hooks/useNetworkInterfaces.js';
import { pushSparklineSample, useSparklineSamples } from '../services/sparklineStore.js';
import { classifyAlerts, findLostPeers } from './dashboardAlerts.js';
import './Dashboard.css';

// Key used by the module-scoped sparkline store. Kept as a constant so
// a rename can't silently split the series between reader and writer.
const LQ_SERIES_KEY = 'dashboard.linkQualityPct';

const dashClient = createClient(DashboardService, transport);

const DASH_POLL_MS = 5000;
// Interface state (UP/DOWN, IPs, link types) changes on the order of
// minutes, not seconds — and the same data is already shared with
// buildNetworkSummary via the backend's CachedInterfaceProvider. Polling
// at 30s keeps the panel current without adding measurable CPU on the
// device.
const IFACE_POLL_MS = 30_000;
const MESH_POLL_MS = 10000;
const CHIP_POLL_MS = 10000;
const CLOCK_TICK_MS = 1000;
const LQ_HISTORY_LEN = 60;
const NEIGHBOR_STALE_MS = 30_000;
// A peer is considered "lost" once we have missed ~3 mesh polls; the
// alert then auto-clears 60s later (total 90s forget window) so a
// long-gone peer doesn't permanently haunt the alerts panel. If the
// peer comes back inside that window the alert is suppressed
// immediately because the peer is in the present-name set again.
const PEER_LOST_MS = NEIGHBOR_STALE_MS;
const PEER_FORGET_MS = PEER_LOST_MS + 60_000;

// ── Formatting ─────────────────────────────────────────────────────────────

function formatUptime(dur) {
  if (!dur) return '—';
  const totalSec = Number(dur.seconds);
  if (totalSec <= 0) return '0m';
  const d = Math.floor(totalSec / 86400);
  const h = Math.floor((totalSec % 86400) / 3600);
  const m = Math.floor((totalSec % 3600) / 60);
  const parts = [];
  if (d > 0) parts.push(`${d}d`);
  if (h > 0 || d > 0) parts.push(`${h}h`);
  parts.push(`${m}m`);
  return parts.join(' ');
}

function formatBytes(bytes) {
  if (!bytes) return '0 B';
  const n = Number(bytes);
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  if (n < 1024 * 1024 * 1024) return `${(n / (1024 * 1024)).toFixed(1)} MB`;
  return `${(n / (1024 * 1024 * 1024)).toFixed(2)} GB`;
}

function formatMbps(bps) {
  if (!bps || bps <= 0) return '—';
  const mbps = bps / 1_000_000;
  if (mbps < 0.1) return `${(bps / 1000).toFixed(0)} Kbps`;
  if (mbps < 10) return `${mbps.toFixed(2)} Mbps`;
  return `${mbps.toFixed(1)} Mbps`;
}

// LinkRate.Phy enum labels (openmanet.service.v1.LinkRate.Phy); 0 is
// "unspecified" and renders nothing.
const PHY_LABELS = { 1: 'Legacy', 2: 'HT', 3: 'VHT', 4: 'HE', 5: 'EHT' };

function formatLinkRate(rate) {
  if (!rate || !rate.bitrateKbps) return '—';
  return formatMbps(rate.bitrateKbps * 1000);
}

// "mesh1 · HE40 · MCS7 · 2SS"; unknown parts are omitted.
function formatLinkRateDetail(rate, iface) {
  if (!rate) return '';
  const parts = [];
  if (iface) parts.push(iface);
  const phy = PHY_LABELS[rate.phy] || '';
  if (phy && rate.widthMhz > 0) parts.push(`${phy}${rate.widthMhz}`);
  else if (phy) parts.push(phy);
  else if (rate.widthMhz > 0) parts.push(`${rate.widthMhz} MHz`);
  if (rate.mcs >= 0) parts.push(`MCS${rate.mcs}`);
  if (rate.nss > 0) parts.push(`${rate.nss}SS`);
  return parts.join(' · ');
}

function formatLast(tsMs, nowMs) {
  if (!tsMs) return '—';
  const diff = Math.max(0, nowMs - tsMs);
  if (diff < 1000) return '<1s';
  if (diff < 60_000) return `${Math.floor(diff / 1000)}s`;
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)}m`;
  return `${Math.floor(diff / 3_600_000)}h`;
}

// Formats the current time in the browser's local timezone — the frontend
// runs in the operator's browser, so this matches the host the operator is
// looking at. `timeZoneName: 'short'` produces a short label like "PDT",
// "EST", or "GMT+10" depending on the locale's conventions.
function clockLocal(now) {
  const time = now.toLocaleTimeString('en-GB', { hour12: false });
  const parts = new Intl.DateTimeFormat('en-US', { timeZoneName: 'short' })
    .formatToParts(now);
  const tz = parts.find((p) => p.type === 'timeZoneName')?.value || 'UTC';
  return `${time} ${tz}`;
}

// ── Signal / link quality ──────────────────────────────────────────────────

// Most 802.11 surveys map good/moderate/weak at −50/−70/−85 dBm.  Linear
// interpolation between −50 (excellent) and −90 (floor) gives a reasonable
// 0–100 % score for the link-quality KPI.
function signalToPct(dbm) {
  if (!Number.isFinite(dbm) || dbm === 0) return 0;
  if (dbm >= -50) return 100;
  if (dbm <= -90) return 0;
  return Math.round(((dbm + 90) / 40) * 100);
}

function sigBars(dbm) {
  const pct = signalToPct(dbm);
  // 5 bars: warn when only one bar, off when zero.
  const filled = Math.max(0, Math.min(5, Math.round(pct / 20)));
  return [0, 1, 2, 3, 4].map((i) => {
    if (i >= filled) return '';
    if (filled <= 1) return 'warn';
    return 'on';
  });
}

// ── Topology helpers ───────────────────────────────────────────────────────

// Build a hop-from-self map and a TQ-by-mac map from the mesh topology
// snapshot. The snapshot shape is the one returned by fetchMeshTopology:
// flat `nodes[]` keyed by primary `mac` (with secondaryMacs[] for radios
// publishing on additional MACs) and a flat `edges[]` array of links.
function buildPeerRows(topology, neighbors, historyRef, nowMs) {
  if (!neighbors || neighbors.length === 0) return [];

  const topoNodes = topology?.nodes ?? [];
  const topoEdges = topology?.edges ?? [];

  // Hostname-by-mac including secondary MACs, so neighbor MACs resolve to
  // friendly names even when batctl reports a per-radio address.
  const hostByMac = new Map();
  for (const node of topoNodes) {
    const name = node.hostname || '';
    const primary = (node.mac || '').toLowerCase();
    if (primary && name) hostByMac.set(primary, name);
    for (const sec of node.secondaryMacs || []) {
      const lc = (sec || '').toLowerCase();
      if (lc && name) hostByMac.set(lc, name);
    }
  }

  // Hops come straight from the daemon's per-node hopsFromSelf — no
  // client-side BFS needed. Index by every known MAC for that node.
  const hopsByMac = new Map();
  for (const node of topoNodes) {
    const h = Number.isFinite(node.hopsFromSelf) ? node.hopsFromSelf : null;
    if (h == null) continue;
    const primary = (node.mac || '').toLowerCase();
    if (primary) hopsByMac.set(primary, h);
    for (const sec of node.secondaryMacs || []) {
      const lc = (sec || '').toLowerCase();
      if (lc) hopsByMac.set(lc, h);
    }
  }

  // Build TQ lookup keyed by neighbor MAC, taking the strongest edge
  // metric incident on each MAC.
  const tqByMac = new Map();
  for (const e of topoEdges) {
    const a = (e.fromMac || '').toLowerCase();
    const b = (e.toMac || '').toLowerCase();
    const metric = e.metric ?? 0;
    if (a) {
      const prev = tqByMac.get(a) ?? 0;
      if (metric > prev) tqByMac.set(a, metric);
    }
    if (b) {
      const prev = tqByMac.get(b) ?? 0;
      if (metric > prev) tqByMac.set(b, metric);
    }
  }

  // batctl neighbor names carry a "_<iface>" suffix so the same physical
  // host publishing on two radios shows up as two neighbor rows. Strip
  // the suffix so the dashboard counts nodes, not radio interfaces, and
  // collapses per-interface rows into one per host.
  const stripSuffix = (s) => (s || '').split('_')[0];

  const groups = new Map();
  for (const n of neighbors) {
    const mac = (n.mac || '').toLowerCase();
    const hist = historyRef.current[n.name || n.mac];
    const lastSeenMs = hist?.lastSeenMs ?? nowMs;
    const rawHostname = hostByMac.get(mac) || n.name || mac.slice(-5);
    const hostname = stripSuffix(rawHostname);
    const signal = n.signal ?? 0;
    const throughput = n.throughput ?? 0;
    const tq = tqByMac.get(mac) ?? 0;
    const hopCount = hopsByMac.get(mac) ?? 1;
    const tx = n.tx ?? null;
    const txIface = n.iface || '';

    const existing = groups.get(hostname);
    if (!existing) {
      groups.set(hostname, {
        key: hostname || mac,
        name: hostname,
        mac: n.mac || '',
        hops: hopCount || 1,
        tq,
        throughput,
        tx,
        txIface,
        rssi: signal,
        sig: sigBars(signal),
        lastMs: lastSeenMs,
      });
      continue;
    }

    // Within a host group keep the best-observed channel across its
    // interfaces: strongest RSSI, max throughput, min hops, max TQ.
    // Last-seen is the freshest of the contributing radios.
    if (signal < 0 && signal > (existing.rssi || -200)) {
      existing.rssi = signal;
      existing.sig = sigBars(signal);
      existing.mac = n.mac || existing.mac;
    }
    if (throughput > existing.throughput) existing.throughput = throughput;
    // Rate follows the fastest radio, independent of which one won RSSI.
    if (tx && (!existing.tx || (tx.bitrateKbps ?? 0) > (existing.tx.bitrateKbps ?? 0))) {
      existing.tx = tx;
      existing.txIface = txIface;
    }
    if (tq > existing.tq) existing.tq = tq;
    if (hopCount && hopCount < existing.hops) existing.hops = hopCount;
    if (lastSeenMs > existing.lastMs) existing.lastMs = lastSeenMs;
  }

  return [...groups.values()]
    .map((row) => ({
      ...row,
      txRate: formatLinkRate(row.tx),
      txDetail: formatLinkRateDetail(row.tx, row.txIface),
    }))
    .sort((a, b) => a.hops - b.hops || (b.throughput - a.throughput));
}

// ── Alerts ─────────────────────────────────────────────────────────────────
// Pure helpers live in dashboardAlerts.js so this file stays JSX-only.

// ── Network interface helpers ──────────────────────────────────────────────

// Role label derived from the kernel-classified interface type. The mesh
// radio type (HALOW_MESH) is the wireless mesh-point interface; WIFI_AP is
// a hostapd-managed access point. VXLAN is the BLOS (Tailscale) tunnel.
const ROLE_BY_TYPE = {
  [InterfaceType.BRIDGE]: 'bridge',
  [InterfaceType.ETHERNET]: 'uplink',
  [InterfaceType.WIFI_AP]: 'AP',
  [InterfaceType.HALOW_MESH]: 'mesh radio',
  [InterfaceType.BATMAN]: 'mesh',
  [InterfaceType.VXLAN]: 'BLOS',
};

function roleForInterface(iface) {
  return ROLE_BY_TYPE[iface.type] || '—';
}

function statusBadge(status) {
  if (status === InterfaceStatus.UP) return { cls: 'badge-ok', dot: 'ok', label: 'UP' };
  if (status === InterfaceStatus.DOWN) return { cls: 'badge-crit', dot: 'crit', label: 'DOWN' };
  return { cls: 'badge-warn', dot: 'warn', label: 'IDLE' };
}

// Pull an address from the curated dashboard `detail` field. The daemon
// formats connected interfaces like "10.41.25.72/16", "Connected — 3
// neighbors", or "100.64.0.16/32" — the CIDR extractor finds the first
// two. Used by the topbar's primary-IP picker only.
function extractAddr(detail) {
  if (!detail) return '—';
  const m = detail.match(/\d+\.\d+\.\d+\.\d+(\/\d+)?/);
  return m ? m[0] : detail.split('—')[0].trim() || detail;
}

// ── Main ───────────────────────────────────────────────────────────────────

export default function DashboardPage() {
  const [data, setData] = useState(null);
  const [loading, setLoading] = useState(true);
  const [now, setNow] = useState(() => Date.now());

  // Link-quality series lives in the module-scoped store so the
  // sparkline survives navigation away and back — per-component refs
  // would reset every mount.
  const lqHistory = useSparklineSamples(LQ_SERIES_KEY);

  const neighborHistoryRef = useRef({});
  // peerHistRef tracks the lastSeenMs for each currently-or-recently
  // connected peer keyed by upper-cased hostname. Used by the alerts
  // panel to surface peers that have disappeared.
  const peerHistRef = useRef({});

  // Shared across Dashboard / Comms / Topology — dedupes the underlying RPCs.
  const meshData = useMeshStatus(MESH_POLL_MS);
  const meshTopology = useMeshTopology(MESH_POLL_MS);
  const gps = useGnssStatus(CHIP_POLL_MS);
  // Shared with the BLOS page — the chip and the page render from the
  // same snapshot so navigating Dashboard → BLOS → Dashboard never
  // shows a stale peer count.
  const blos = useBLOSStatus(CHIP_POLL_MS);
  const blosPeers = blos?.peers?.length ?? 0;
  const blosEnabled = blos?.status?.blosEnabled ?? false;
  // Shared with SettingsNetwork — the kernel-classified interface list
  // shows every interface (incl. wlan AP + halow mesh) with its real
  // role instead of only the curated WAN/LAN/MESH/BAT/Tailscale rollup.
  const ifaceSnapshot = useNetworkInterfaces(IFACE_POLL_MS);
  const interfaces = useMemo(
    () => ifaceSnapshot?.interfaces ?? [],
    [ifaceSnapshot],
  );
  const topology = meshTopology?.topology ?? null;
  const delta = meshTopology?.delta ?? null;

  // ─ Polls ─
  const fetchStatus = useCallback(async () => {
    try {
      const resp = await dashClient.getDashboardStatus({});
      setData(resp);
    } catch {
      // best-effort
    } finally {
      setLoading(false);
    }
  }, []);

  useVisibleInterval(fetchStatus, DASH_POLL_MS);

  // Drive derived peer history + LQ rolling average off each new mesh snapshot.
  useEffect(() => {
    if (!Array.isArray(meshData?.neighbors)) return;

    const nowMs = Date.now();
    let sumPct = 0;
    let count = 0;
    for (const n of meshData.neighbors) {
      const key = n.name || n.mac;
      if (!key) continue;
      if (!neighborHistoryRef.current[key]) {
        neighborHistoryRef.current[key] = { lastSeenMs: nowMs };
      }
      neighborHistoryRef.current[key].lastSeenMs = nowMs;
      if (Number.isFinite(n.signal) && n.signal !== 0) {
        sumPct += signalToPct(n.signal);
        count += 1;
      }
    }
    // Prune peers we haven't seen for a while so old entries don't
    // anchor the peer list.
    for (const [key, h] of Object.entries(neighborHistoryRef.current)) {
      if (nowMs - h.lastSeenMs > NEIGHBOR_STALE_MS * 10) {
        delete neighborHistoryRef.current[key];
      }
    }
    if (count > 0) {
      pushSparklineSample(LQ_SERIES_KEY, Math.round(sumPct / count), LQ_HISTORY_LEN);
    }
  }, [meshData]);

  const tickClock = useCallback(() => setNow(Date.now()), []);
  useVisibleInterval(tickClock, CLOCK_TICK_MS);

  // ─ Derived ─
  const neighbors = useMemo(() => meshData?.neighbors ?? [], [meshData]);
  const peerRows = useMemo(
    // neighborHistoryRef is a long-lived accumulator updated in the
    // neighbors-tracking effect above; buildPeerRows reads .current to look
    // up per-neighbor last-seen timestamps. The lint rule flags any ref
    // access reachable from useMemo, but the read here is by-design.
    // eslint-disable-next-line react-hooks/refs
    () => buildPeerRows(topology, neighbors, neighborHistoryRef, now),
    [topology, neighbors, now],
  );

  // Track which peers we are currently seeing so the alerts panel can
  // surface ones that have disappeared. Stamping happens on every
  // peerRows change; pruning forgets entries that have been gone longer
  // than PEER_FORGET_MS so the alert auto-clears.
  useEffect(() => {
    const nowMs = Date.now();
    for (const p of peerRows) {
      const name = (p.name || '').toUpperCase();
      if (!name) continue;
      peerHistRef.current[name] = nowMs;
    }
    for (const [name, lastSeenMs] of Object.entries(peerHistRef.current)) {
      if (nowMs - lastSeenMs > PEER_FORGET_MS) {
        delete peerHistRef.current[name];
      }
    }
  }, [peerRows]);

  const lostPeers = useMemo(() => {
    const present = new Set();
    for (const p of peerRows) {
      const name = (p.name || '').toUpperCase();
      if (name) present.add(name);
    }
    // peerHistRef is updated by the effect immediately above; reading
    // .current here surfaces the same set findLostPeers needs to compare
    // against. The map is mutable on purpose, hence the ref.
    // eslint-disable-next-line react-hooks/refs
    return findLostPeers(peerHistRef.current, present, now, PEER_LOST_MS, PEER_FORGET_MS);
  }, [peerRows, now]);

  const peerCount = peerRows.length;
  // Gateway = the batman-adv-selected best gateway reported by
  // StatusService.selected_gateway_mac. We cross-reference the MAC against
  // the mesh topology node list (primary + secondary MACs) to surface the
  // node's hostname. Falls back to ListNodes (which carries hostnames
  // keyed by IP, not MAC, so cross-reference via the mesh neighbor list)
  // and finally to a short MAC suffix when no name is known. Self takes
  // precedence when this node itself is the elected gateway.
  // eslint-disable-next-line react-hooks/preserve-manual-memoization
  const gatewayName = useMemo(() => {
    if (meshData?.status?.is_gateway) return (data?.deviceInfo?.hostname || 'SELF').toUpperCase();
    const mac = (meshData?.status?.selected_gateway_mac || '').toLowerCase();
    if (!mac) return '—';

    for (const node of topology?.nodes ?? []) {
      const macs = [node.mac, ...(node.secondaryMacs || [])];
      if (macs.some((m) => (m || '').toLowerCase() === mac)) {
        if (node.hostname) return node.hostname.toUpperCase();
        break;
      }
    }

    // Some neighbors only show up in the batctl neighbor table — match
    // against the live neighbor list and strip the per-radio "_<iface>"
    // suffix that batctl adds.
    for (const n of meshData?.neighbors ?? []) {
      if ((n.mac || '').toLowerCase() !== mac) continue;
      const name = (n.name || '').split('_')[0];
      if (name) return name.toUpperCase();
    }

    return mac.slice(9).toUpperCase();
  }, [meshData, data, topology]);

  const hopsAvg = useMemo(() => {
    if (peerRows.length === 0) return '—';
    const sum = peerRows.reduce((a, p) => a + p.hops, 0);
    return (sum / peerRows.length).toFixed(1);
  }, [peerRows]);

  const throughputTotal = useMemo(() => {
    const bps = (neighbors || []).reduce((a, n) => a + (n.throughput || 0), 0);
    return formatMbps(bps);
  }, [neighbors]);

  const linkQuality = lqHistory.length > 0 ? lqHistory[lqHistory.length - 1] : 0;
  const lqClass = linkQuality >= 80 ? 'ok' : linkQuality >= 50 ? '' : 'warn';

  const cpuTemp = data?.systemResources?.cpuTempCelsius;
  const tempAvailable = cpuTemp != null && cpuTemp > 0;
  const tempClass = !tempAvailable ? '' : cpuTemp >= 85 ? 'crit' : cpuTemp >= 70 ? 'warn' : '';
  const tempLabel = tempAvailable ? `${cpuTemp.toFixed(1)} °C` : '—';

  const alerts = useMemo(
    () => classifyAlerts({ mesh: meshData, lostPeers, delta }),
    [meshData, lostPeers, delta],
  );

  // Acked alert texts. ACK ALL snapshots the currently-visible non-OK
  // alert texts; alerts whose text matches stay hidden. When the
  // underlying condition clears (text disappears from the live set) the
  // ack auto-expires, so the same condition re-occurring later re-fires
  // the alert. OK-level entries (MESH UP, MESH HEALED) are status
  // indicators and are never ack-able.
  const [ackedAlerts, setAckedAlerts] = useState(() => new Set());

  useEffect(() => {
    // Prune acked alerts whose underlying condition has cleared. The
    // functional update short-circuits when the set is already empty or
    // unchanged, so this is cheap on every alerts update.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setAckedAlerts((prev) => {
      if (prev.size === 0) return prev;
      const live = new Set(alerts.map((a) => a.text));
      const next = new Set();
      for (const t of prev) {
        if (live.has(t)) next.add(t);
      }
      return next.size === prev.size ? prev : next;
    });
  }, [alerts]);

  const visibleAlerts = useMemo(
    () => alerts.filter((a) => a.level === 'ok' || !ackedAlerts.has(a.text)),
    [alerts, ackedAlerts],
  );

  const ackableCount = visibleAlerts.filter((a) => a.level !== 'ok').length;

  const handleAckAll = useCallback(() => {
    setAckedAlerts((prev) => {
      const next = new Set(prev);
      for (const a of alerts) {
        if (a.level !== 'ok') next.add(a.text);
      }
      return next;
    });
  }, [alerts]);

  // Hide loopback from the network panel — it adds noise on every device
  // and never carries operator-relevant state. Sort UP-first then by name
  // so the panel's first rows are the live links.
  const visibleInterfaces = useMemo(() => {
    const filtered = interfaces.filter(
      (iface) => iface.type !== InterfaceType.LOOPBACK,
    );
    return filtered.sort((a, b) => {
      const aUp = a.status === InterfaceStatus.UP ? 0 : 1;
      const bUp = b.status === InterfaceStatus.UP ? 0 : 1;
      if (aUp !== bUp) return aUp - bUp;
      return (a.name || '').localeCompare(b.name || '');
    });
  }, [interfaces]);

  // ─ Topbar state ─
  const hostname = data?.deviceInfo?.hostname || 'NODE';
  // Prefer the curated dashboard summary (which carries the WAN/LAN
  // detail strings) but fall back to the first UP interface with an IP
  // from the kernel list when the curated summary hasn't populated yet.
  const primaryIp = useMemo(() => {
    const entries = data?.networkSummary?.entries ?? [];
    for (const e of entries) {
      if (e.state !== NetworkInterfaceState.CONNECTED) continue;
      const addr = extractAddr(e.detail);
      if (addr && addr !== '—') return addr;
    }
    for (const iface of interfaces) {
      if (iface.status !== InterfaceStatus.UP) continue;
      if (iface.type === InterfaceType.LOOPBACK) continue;
      if (iface.ipAddress) return iface.ipAddress;
    }
    return '—';
  }, [data, interfaces]);

  const gpsFix = (gps?.position?.fixType ?? 0) >= 2; // 2D or 3D
  const gpsSats = gps?.satelliteStatus?.satellitesUsed ?? 0;
  const meshUp = !!meshData?.status?.connected;

  if (loading) {
    return <div className="dashboard-loading">Loading dashboard...</div>;
  }

  return (
    <>
      <div className="lat-topbar">
        <div className="node-id">
          {hostname.toUpperCase()}
          <span className="ip">{primaryIp}</span>
        </div>
        <div className="chips">
          <span className={`lat-chip ${meshUp ? 'ok' : 'crit'}`}>
            <span className="dot" /> MESH {meshUp ? 'UP' : 'DOWN'}
          </span>
          <span className={`lat-chip ${gpsFix ? 'ok' : 'warn'}`}>
            <span className="dot" /> GPS {gpsFix ? `LOCK · ${gpsSats} SATS` : 'NO FIX'}
          </span>
          {blosEnabled && (
            <span className={`lat-chip ${blosPeers > 0 ? 'ok' : 'warn'}`}>
              <span className="dot" /> BLOS · {blosPeers} PEERS
            </span>
          )}
          <span className="dash-clock">{clockLocal(new Date(now))}</span>
        </div>
      </div>

      <div className="lat-view-header">
        <div>
          <h2>◇ Dashboard</h2>
          <div className="crumb">Overview · Live telemetry · {(DASH_POLL_MS / 1000).toFixed(0)}s refresh</div>
        </div>
      </div>

      <div className="lat-body grid-4">
        {/* Row 1: 3 KPIs — PTT Latency lives on Comms, not here. */}
        <div className="dashboard-kpi-row">
          <div className="lat-panel">
            <div className="panel-head"><h3>Mesh Peers</h3></div>
            <div className="big-num">{peerCount}<span className="unit">nodes</span></div>
            <div className="kv"><span className="k">Gateway</span><span className="v accent">{gatewayName}</span></div>
            <div className="kv"><span className="k">Hops avg</span><span className="v">{hopsAvg}</span></div>
            <div className="kv"><span className="k">Throughput</span><span className="v">{throughputTotal}</span></div>
          </div>
          <div className="lat-panel">
            <div className="panel-head"><h3>Link Quality · 5m</h3></div>
            <div className={`big-num ${lqClass}`}>{linkQuality}<span className="unit">%</span></div>
            <div className={`spark${lqClass === 'warn' ? ' warn' : ''}`}>
              {lqHistory.length === 0 ? (
                <span style={{ height: '2%' }} />
              ) : (
                lqHistory.map((v, i) => (
                  <span key={i} style={{ height: `${Math.max(2, v)}%` }} />
                ))
              )}
            </div>
          </div>
        </div>

        {/* Row 2: peers table + alerts */}
        <div className="lat-panel col-span-3">
          <div className="panel-head">
            <h3>Mesh Peers · Live</h3>
          </div>
          <div className="table-scroll">
            <table className="lat-table">
              <thead>
                <tr>
                  <th>Node</th><th>MAC</th><th>Hops</th><th>Throughput</th>
                  <th>TX Rate</th><th>RSSI</th><th>Signal</th><th>Last</th>
                </tr>
              </thead>
              <tbody>
                {peerRows.length === 0 && (
                  <tr><td colSpan={8} className="mono">No neighbors reporting</td></tr>
                )}
                {peerRows.map((p) => (
                  <tr key={p.key}>
                    <td>{p.name}</td>
                    <td className="mono">{p.mac || '—'}</td>
                    <td>{p.hops}</td>
                    <td>{formatMbps(p.throughput)}</td>
                    <td>
                      {p.txRate}
                      {p.txDetail && <div className="mono">{p.txDetail}</div>}
                    </td>
                    <td>{p.rssi ? `${p.rssi}` : '—'}</td>
                    <td>
                      <div className="sig-bars">
                        {p.sig.map((cls, i) => (
                          <span key={i} className={cls} />
                        ))}
                      </div>
                    </td>
                    <td>{formatLast(p.lastMs, now)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>

        <div className="lat-panel">
          <div className="panel-head">
            <h3>Alerts · Active</h3>
            <div className="actions">
              <button
                type="button"
                onClick={handleAckAll}
                disabled={ackableCount === 0}
              >
                ACK ALL
              </button>
            </div>
          </div>
          {visibleAlerts.length === 0 && (
            <div className="lat-alert ok">NO ACTIVE ALERTS</div>
          )}
          {visibleAlerts.map((a) => (
            <div key={`${a.level}:${a.text}`} className={`lat-alert ${a.level}`}>
              {a.text}
            </div>
          ))}
        </div>

        {/* Row 3: system resources + interfaces */}
        <div className="lat-panel col-span-2">
          <div className="panel-head"><h3>System Resources</h3></div>
          <div className="dashboard-resources">
            <div className="dashboard-resources-bars">
              <PbarRow
                label="CPU"
                pct={data?.systemResources?.cpuLoadPercent ?? 0}
                detail={`${(data?.systemResources?.cpuLoadPercent ?? 0).toFixed(2)}%`}
              />
              <PbarRow
                label="MEM"
                pct={memPct(data?.systemResources?.memoryUsedBytes, data?.systemResources?.memoryTotalBytes)}
                detail={`${formatBytes(data?.systemResources?.memoryUsedBytes)} / ${formatBytes(data?.systemResources?.memoryTotalBytes)}`}
              />
              <PbarRow
                label="OVERLAY"
                pct={memPct(data?.systemResources?.overlayUsedBytes, data?.systemResources?.overlayTotalBytes)}
                detail={`${formatBytes(data?.systemResources?.overlayUsedBytes)} / ${formatBytes(data?.systemResources?.overlayTotalBytes)}`}
              />
            </div>
            <div className="dashboard-resources-kv">
              <div className="kv"><span className="k">Uptime</span><span className="v accent">{formatUptime(data?.systemResources?.uptime)}</span></div>
              <div className="kv"><span className="k">Model</span><span className="v">{data?.deviceInfo?.model || '—'}</span></div>
              <div className="kv"><span className="k">Kernel</span><span className="v">{data?.deviceInfo?.kernel || '—'}</span></div>
              <div className="kv"><span className="k">Firmware</span><span className="v">{data?.deviceInfo?.firmware || '—'}</span></div>
              <div className="kv"><span className="k">Arch</span><span className="v">{data?.deviceInfo?.architecture || '—'}</span></div>
              <div className="kv"><span className="k">Temp</span><span className={`v ${tempClass}`}>{tempLabel}</span></div>
            </div>
          </div>
        </div>

        <div className="lat-panel col-span-2">
          <div className="panel-head"><h3>Network Interfaces</h3></div>
          <div className="table-scroll">
            <table className="lat-table">
              <thead>
                <tr>
                  <th>Iface</th><th>Addr</th><th>State</th><th>Role</th><th>RX</th><th>TX</th>
                </tr>
              </thead>
              <tbody>
                {visibleInterfaces.length === 0 && (
                  <tr><td colSpan={6} className="mono">No network data</td></tr>
                )}
                {visibleInterfaces.map((iface) => {
                  const badge = statusBadge(iface.status);
                  return (
                    <tr key={iface.name}>
                      <td>
                        <span className={`dot-i ${badge.dot}`} />
                        {iface.name}
                      </td>
                      <td className="mono">{iface.ipAddress || '—'}</td>
                      <td className={badge.cls}>{badge.label}</td>
                      <td>{roleForInterface(iface)}</td>
                      <td className="mono">{formatBytes(iface.rxBytes)}</td>
                      <td className="mono">{formatBytes(iface.txBytes)}</td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </div>

      </div>
    </>
  );
}

// ── Small presentational helpers ──────────────────────────────────────────

function memPct(used, total) {
  const u = Number(used ?? 0);
  const t = Number(total ?? 0);
  if (t <= 0) return 0;
  return Math.max(0, Math.min(100, Math.round((u / t) * 100)));
}

function PbarRow({ label, pct, detail }) {
  const warn = pct >= 90 ? 'crit' : pct >= 70 ? 'warn' : '';
  return (
    <div className="pbar-row">
      <span className="pbar-label">{label}</span>
      <div className={`pbar ${warn}`.trim()}>
        <span style={{ width: `${pct}%` }} />
      </div>
      <span className="pbar-val">{detail || `${pct}%`}</span>
    </div>
  );
}
