// =============================================================================
// GpsStatus.jsx — GPS / GNSS status and configuration page
// =============================================================================

import { useState, useEffect, useCallback, useMemo, useRef } from 'react';
import { useGnssStatus } from '../hooks/useGnssStatus.js';
import { createClient } from '@connectrpc/connect';
import { transport } from '../services/connectClient.js';
import { GNSSService } from '../gen/openmanet/gnss/v1/gnss_service_pb.js';
import { GNSSSource } from '../gen/openmanet/gnss/v1/gnss_pb.js';
import SkyPlot from '../components/SkyPlot.jsx';
import { coastlines } from '../data/coastlines.js';
import { latLonToMGRS } from '../utils/mgrs.js';
import {
  prnToConstellation,
  satelliteConstellation,
  estimateCEP95,
  computeFixRateHz,
  dopSeverity,
  formatAgo,
  timestampToDate,
} from '../utils/gnss.js';
import LatSelect from '../components/LatSelect.jsx';
import DataTable from '../components/DataTable.jsx';
import './GpsStatus.css';

const gnssClient = createClient(GNSSService, transport);

const FIX_LABELS = { 0: 'No Fix', 1: 'No Fix', 2: '2D', 3: '3D' };
const FIX_SUBTITLE = { 0: 'NO FIX', 1: 'NO FIX', 2: '2D FIX', 3: '3D FIX' };
const POLL_INTERVAL = 2000;
const FIX_RATE_SAMPLES = 6;

// Globe canvas size bounds. The canvas is square and measured from its
// container so it fills a single-column mobile panel without overflowing it.
const GLOBE_MIN = 180;
const GLOBE_MAX = 300;
const MIN_ZOOM = 0.8;
const MAX_ZOOM = 4;
const DEG2RAD = Math.PI / 180;

const nodeName = (() => {
  const raw = typeof window !== 'undefined' ? window.location.hostname : '';
  const head = (raw || '').split('.')[0].toUpperCase();
  if (!head || /^\d+$/.test(head) || head === 'LOCALHOST') return 'NODE';
  return head;
})();

function formatDMS(deg, posSign, negSign) {
  if (deg == null || !Number.isFinite(deg)) return '—';
  const abs = Math.abs(deg);
  const d = Math.floor(abs);
  const mf = (abs - d) * 60;
  const m = Math.floor(mf);
  const s = (mf - m) * 60;
  const suffix = deg >= 0 ? posSign : negSign;
  return `${d}° ${m.toString().padStart(2, '0')}′ ${s.toFixed(2).padStart(5, '0')}″ ${suffix}`;
}

function fixColor(fixType) {
  if (fixType === 3) return 'ok';
  if (fixType === 2) return 'warn';
  return 'crit';
}

function snrBadge(snr) {
  if (snr == null || !Number.isFinite(snr)) return '';
  if (snr >= 30) return 'badge-ok';
  if (snr >= 15) return 'badge-warn';
  return 'badge-crit';
}

// ── Globe rendering ─────────────────────────────────────────────────────────

function drawGlobe(canvas, viewLat, viewLon, zoom, size) {
  const ctx = canvas.getContext('2d');
  if (!ctx) return null;

  const dpr = window.devicePixelRatio || 1;
  canvas.width = size * dpr;
  canvas.height = size * dpr;
  canvas.style.width = size + 'px';
  canvas.style.height = size + 'px';
  ctx.scale(dpr, dpr);
  ctx.clearRect(0, 0, size, size);

  const cx = size / 2;
  const cy = size / 2;
  const r = (size / 2 - 14) * zoom;

  const lonRad = -viewLon * DEG2RAD;
  const latRad = viewLat * DEG2RAD;
  const cosLatR = Math.cos(latRad);
  const sinLatR = Math.sin(latRad);

  function projectFull(pLat, pLon) {
    const phi = pLat * DEG2RAD;
    const lam = pLon * DEG2RAD + lonRad;
    const cosPhi = Math.cos(phi);
    const x3 = cosPhi * Math.sin(lam);
    const y3 = Math.sin(phi);
    const z3 = cosPhi * Math.cos(lam);
    const yr = y3 * cosLatR - z3 * sinLatR;
    const zr = y3 * sinLatR + z3 * cosLatR;
    return { x: cx + x3 * r, y: cy - yr * r, z: zr };
  }

  function project(pLat, pLon) {
    const p = projectFull(pLat, pLon);
    return p.z < -0.05 ? null : p;
  }

  function clipToHemisphere(poly) {
    const full = poly.map((pt) => projectFull(pt[1], pt[0]));
    const clipped = [];

    function edgePoint(a, b) {
      const t = (0 - a.z) / (b.z - a.z);
      const ix = a.x + t * (b.x - a.x);
      const iy = a.y + t * (b.y - a.y);
      const dx = ix - cx;
      const dy = iy - cy;
      const dist = Math.sqrt(dx * dx + dy * dy) || 1;
      return { x: cx + (dx / dist) * r, y: cy + (dy / dist) * r };
    }

    function addRimArc(from, to) {
      const a1 = Math.atan2(from.y - cy, from.x - cx);
      const a2 = Math.atan2(to.y - cy, to.x - cx);
      let delta = a2 - a1;
      if (delta > Math.PI) delta -= 2 * Math.PI;
      if (delta < -Math.PI) delta += 2 * Math.PI;
      const steps = Math.max(2, Math.ceil(Math.abs(delta) / 0.15));
      for (let s = 1; s < steps; s++) {
        const a = a1 + (delta * s) / steps;
        clipped.push({ x: cx + Math.cos(a) * r, y: cy + Math.sin(a) * r });
      }
    }

    let lastExit = null;

    for (let i = 0; i < full.length; i++) {
      const curr = full[i];
      const prev = full[(i + full.length - 1) % full.length];
      const currVisible = curr.z >= 0;
      const prevVisible = prev.z >= 0;

      if (prevVisible && !currVisible) {
        const ep = edgePoint(prev, curr);
        clipped.push(ep);
        lastExit = ep;
      } else if (!prevVisible && currVisible) {
        const ep = edgePoint(prev, curr);
        if (lastExit) addRimArc(lastExit, ep);
        clipped.push(ep);
      }

      if (currVisible) clipped.push({ x: curr.x, y: curr.y });
    }

    return clipped;
  }

  ctx.save();
  ctx.beginPath();
  ctx.arc(cx, cy, (size / 2 - 14) * Math.max(zoom, 1), 0, Math.PI * 2);
  ctx.clip();

  ctx.beginPath();
  ctx.arc(cx, cy, r, 0, Math.PI * 2);
  ctx.fillStyle = '#0b161e';
  ctx.fill();

  ctx.lineWidth = 0.4;
  for (let gLat = -80; gLat <= 80; gLat += 20) {
    ctx.beginPath();
    let started = false;
    for (let gLon = -180; gLon <= 180; gLon += 3) {
      const p = project(gLat, gLon);
      if (!p) { started = false; continue; }
      if (!started) { ctx.moveTo(p.x, p.y); started = true; }
      else ctx.lineTo(p.x, p.y);
    }
    ctx.strokeStyle = gLat === 0 ? 'rgba(0,229,255,0.3)' : 'rgba(0,229,255,0.14)';
    ctx.stroke();
  }
  for (let gLon = -180; gLon < 180; gLon += 30) {
    ctx.beginPath();
    let started = false;
    for (let gLat = -90; gLat <= 90; gLat += 3) {
      const p = project(gLat, gLon);
      if (!p) { started = false; continue; }
      if (!started) { ctx.moveTo(p.x, p.y); started = true; }
      else ctx.lineTo(p.x, p.y);
    }
    ctx.strokeStyle = gLon === 0 ? 'rgba(0,229,255,0.3)' : 'rgba(0,229,255,0.14)';
    ctx.stroke();
  }

  coastlines.forEach((poly) => {
    const clipped = clipToHemisphere(poly);
    if (clipped.length < 3) return;

    ctx.beginPath();
    ctx.moveTo(clipped[0].x, clipped[0].y);
    for (let i = 1; i < clipped.length; i++) {
      ctx.lineTo(clipped[i].x, clipped[i].y);
    }
    ctx.closePath();
    ctx.fillStyle = 'rgba(0,230,118,0.18)';
    ctx.fill();

    ctx.lineWidth = 1.0;
    ctx.strokeStyle = 'rgba(0,230,118,0.5)';
    ctx.beginPath();
    let started = false;
    for (let i = 0; i < poly.length; i++) {
      const p = project(poly[i][1], poly[i][0]);
      if (!p) { started = false; continue; }
      if (!started) { ctx.moveTo(p.x, p.y); started = true; }
      else ctx.lineTo(p.x, p.y);
    }
    ctx.stroke();
  });

  ctx.restore();
  ctx.beginPath();
  ctx.arc(cx, cy, (size / 2 - 14) * Math.max(zoom, 1), 0, Math.PI * 2);
  ctx.strokeStyle = 'rgba(0,229,255,0.45)';
  ctx.lineWidth = 1.2;
  ctx.stroke();

  return project;
}

// ── Globe panel ─────────────────────────────────────────────────────────────

function GlobePanel({ position, actionsRef }) {
  const canvasRef = useRef(null);
  const wrapRef = useRef(null);
  const viewRef = useRef({ lat: 20, lon: 0, zoom: 1 });
  const dragRef = useRef(null);
  const [, forceRender] = useState(0);
  const [globeSize, setGlobeSize] = useState(GLOBE_MAX);

  // The canvas is a fixed-size raster, so it has to be measured rather than
  // sized in CSS. Clamped so it fills a 360px panel without overflowing and
  // never grows past its desktop size.
  useEffect(() => {
    const wrap = wrapRef.current;
    if (!wrap) return undefined;
    const apply = (width) => {
      if (width <= 0) return;
      setGlobeSize(Math.round(Math.max(GLOBE_MIN, Math.min(GLOBE_MAX, width))));
    };
    apply(wrap.clientWidth);
    const ro = new ResizeObserver((entries) => {
      for (const entry of entries) apply(entry.contentRect.width);
    });
    ro.observe(wrap);
    return () => ro.disconnect();
  }, []);

  const lat = position?.latitude;
  const lon = position?.longitude;
  const alt = position?.altitude;
  const hasPos = lat != null && lon != null && (lat !== 0 || lon !== 0);

  // Auto-center the globe on the first valid position fix. Subsequent fixes
  // do not move the view (user pan/zoom is preserved); the centerView button
  // is the explicit way to re-center.
  const centeredRef = useRef(false);
  useEffect(() => {
    if (hasPos && !centeredRef.current) {
      viewRef.current.lat = lat;
      viewRef.current.lon = lon;
      centeredRef.current = true;
    }
  }, [hasPos, lat, lon]);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const v = viewRef.current;
    const project = drawGlobe(canvas, v.lat, v.lon, v.zoom, globeSize);
    if (!project || !hasPos) return;

    const ctx = canvas.getContext('2d');
    const p = project(lat, lon);
    if (!p || !ctx) return;
    ctx.beginPath();
    ctx.arc(p.x, p.y, 12, 0, Math.PI * 2);
    ctx.fillStyle = 'rgba(0,229,255,0.12)';
    ctx.fill();
    ctx.beginPath();
    ctx.arc(p.x, p.y, 7, 0, Math.PI * 2);
    ctx.strokeStyle = 'rgba(0,229,255,0.6)';
    ctx.lineWidth = 1.5;
    ctx.stroke();
    ctx.beginPath();
    ctx.arc(p.x, p.y, 3.5, 0, Math.PI * 2);
    ctx.fillStyle = '#00e5ff';
    ctx.fill();
  });

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;

    const onWheel = (e) => {
      e.preventDefault();
      const v = viewRef.current;
      const delta = e.deltaY > 0 ? 0.9 : 1.1;
      v.zoom = Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, v.zoom * delta));
      forceRender((n) => n + 1);
    };
    const onPointerDown = (e) => {
      canvas.setPointerCapture(e.pointerId);
      dragRef.current = { x: e.clientX, y: e.clientY };
    };
    const onPointerMove = (e) => {
      if (!dragRef.current) return;
      const dx = e.clientX - dragRef.current.x;
      const dy = e.clientY - dragRef.current.y;
      dragRef.current = { x: e.clientX, y: e.clientY };
      const v = viewRef.current;
      const sensitivity = 0.3 / v.zoom;
      v.lon -= dx * sensitivity;
      v.lat += dy * sensitivity;
      v.lat = Math.max(-85, Math.min(85, v.lat));
      forceRender((n) => n + 1);
    };
    const onPointerUp = () => {
      dragRef.current = null;
    };

    canvas.addEventListener('wheel', onWheel, { passive: false });
    canvas.addEventListener('pointerdown', onPointerDown);
    canvas.addEventListener('pointermove', onPointerMove);
    canvas.addEventListener('pointerup', onPointerUp);
    canvas.addEventListener('pointercancel', onPointerUp);

    return () => {
      canvas.removeEventListener('wheel', onWheel);
      canvas.removeEventListener('pointerdown', onPointerDown);
      canvas.removeEventListener('pointermove', onPointerMove);
      canvas.removeEventListener('pointerup', onPointerUp);
      canvas.removeEventListener('pointercancel', onPointerUp);
    };
  }, []);

  const resetView = useCallback(() => {
    const v = viewRef.current;
    v.lat = 20;
    v.lon = 0;
    v.zoom = 1;
    forceRender((n) => n + 1);
  }, []);

  const centerView = useCallback(() => {
    const v = viewRef.current;
    if (hasPos) {
      v.lat = lat;
      v.lon = lon;
      forceRender((n) => n + 1);
    }
  }, [hasPos, lat, lon]);

  // Expose the imperative reset/center actions to the parent through the
  // optional actionsRef. Done in an effect so the parent reads the latest
  // memoized callbacks rather than a stale closure.
  useEffect(() => {
    if (actionsRef) actionsRef.current = { resetView, centerView };
  }, [actionsRef, resetView, centerView]);

  return (
    <div className="lat-panel gps-panel-globe">
      <div className="panel-head">
        <h3>Globe · WGS84</h3>
        <div className="actions">
          <button type="button" onClick={resetView}>RESET</button>
        </div>
      </div>
      <div className="gps-globe-wrap" ref={wrapRef}>
        <canvas ref={canvasRef} className="gps-globe-canvas" />
        {hasPos ? (
          <div className="gps-globe-coord">
            {lat.toFixed(4)} {lat >= 0 ? 'N' : 'S'} · {Math.abs(lon).toFixed(4)} {lon >= 0 ? 'E' : 'W'}
            {alt != null && <> · {alt.toFixed(1)} m</>}
          </div>
        ) : (
          <div className="gps-globe-coord muted">No position data</div>
        )}
        <div className="gps-globe-hint">
          <span className="hint-fine">Drag to rotate · scroll to zoom</span>
          <span className="hint-coarse">Drag to rotate</span>
        </div>
      </div>
    </div>
  );
}

// ── Sky plot panel ──────────────────────────────────────────────────────────

const SOURCE_OPTIONS = [
  { value: GNSSSource.GNSS_SOURCE_INTERNAL, label: 'INTERNAL · LOCAL RECEIVER' },
  { value: GNSSSource.GNSS_SOURCE_EXTERNAL_COT, label: 'EXTERNAL · EUD (ATAK) VIA COT' },
];

const BAND_OPTIONS = [
  { value: 'all', label: 'ALL BANDS' },
  { value: 'GPS', label: 'GPS' },
  { value: 'GLONASS', label: 'GLONASS' },
  { value: 'GALILEO', label: 'GALILEO' },
  { value: 'BEIDOU', label: 'BEIDOU' },
  { value: 'QZSS', label: 'QZSS' },
  { value: 'SBAS', label: 'SBAS' },
];

function SkyPlotPanel({ satelliteStatus }) {
  const [bandFilter, setBandFilter] = useState('all');
  const sats = useMemo(() => satelliteStatus?.satellites ?? [], [satelliteStatus]);
  const filteredSats = useMemo(() => (
    bandFilter === 'all' ? sats : sats.filter((s) => satelliteConstellation(s) === bandFilter)
  ), [sats, bandFilter]);
  const inView = bandFilter === 'all'
    ? (satelliteStatus?.satellitesInView ?? sats.length)
    : filteredSats.length;
  const ago = formatAgo(satelliteStatus?.lastUpdate);

  return (
    <div className="lat-panel gps-panel-sky">
      <div className="panel-head">
        <h3>Sky Plot · {inView} Sats</h3>
        <div className="actions">
          <LatSelect
            ariaLabel="Constellation filter"
            value={bandFilter}
            onChange={setBandFilter}
            options={BAND_OPTIONS}
          />
        </div>
      </div>
      <div className="gps-sky-wrap">
        <SkyPlot satellites={filteredSats} />
        <div className="gps-sky-hint">Zenith · N up · rings = 30° elevation</div>
        <div className="gps-sky-hint">Updated {ago ?? '—'}</div>
      </div>
    </div>
  );
}

// ── Position panel ──────────────────────────────────────────────────────────

function PositionPanel({ position, mgrs }) {
  const lat = position?.latitude;
  const lon = position?.longitude;
  const alt = position?.altitude;
  const speed = position?.speed;
  const heading = position?.heading;

  return (
    <div className="lat-panel gps-panel-position">
      <div className="panel-head"><h3>Position</h3></div>
      <div className="gps-position">
        <div className="gps-pos-label">Latitude</div>
        <div className="gps-pos-value accent">{formatDMS(lat, 'N', 'S')}</div>
        <div className="gps-pos-label">Longitude</div>
        <div className="gps-pos-value accent">{formatDMS(lon, 'E', 'W')}</div>
        <div className="gps-pos-label">Alt · MSL</div>
        <div className="gps-pos-value">{alt != null ? `${alt.toFixed(1)} m` : '—'}</div>
        <div className="gps-pos-label">MGRS</div>
        <div className="gps-pos-value gps-pos-mgrs">{mgrs ?? '—'}</div>
        <div className="gps-pos-label">Heading · Speed</div>
        <div className="gps-pos-value">
          {heading != null ? `${heading.toFixed(0)}°` : '—'}
          {' · '}
          {speed != null ? `${speed.toFixed(1)} m/s` : '—'}
        </div>
      </div>
    </div>
  );
}

// ── Satellite SNR table panel ───────────────────────────────────────────────

const SNR_COLUMNS = [
  { key: 'prn', label: 'PRN', render: (sat) => sat.prn },
  { key: 'constellation', label: 'Constellation', render: (sat) => prnToConstellation(sat.prn) },
  {
    key: 'elev',
    label: 'Elev',
    render: (sat) => (sat.elevation != null ? `${sat.elevation.toFixed(0)}°` : '—'),
  },
  {
    key: 'azim',
    label: 'Azim',
    render: (sat) => (sat.azimuth != null ? `${sat.azimuth.toFixed(0).padStart(3, '0')}°` : '—'),
  },
  {
    key: 'snr',
    label: 'SNR',
    cellClass: (sat) => snrBadge(sat.snr),
    render: (sat) => (sat.snr != null ? sat.snr.toFixed(0) : '—'),
  },
  {
    key: 'used',
    label: 'Used',
    cellClass: (sat) => (sat.used ? 'badge-ok' : 'badge-crit'),
    render: (sat) => (sat.used ? '✓' : '✗'),
  },
];

function SatelliteSnrPanel({ satelliteStatus }) {
  const [filter, setFilter] = useState('all');
  const sats = satelliteStatus?.satellites ?? [];
  const rows = filter === 'used' ? sats.filter((s) => s.used) : sats;

  return (
    <div className="lat-panel col-span-2 gps-panel-snr">
      <div className="panel-head">
        <h3>Satellite SNR</h3>
        <div className="actions">
          <button
            type="button"
            className={filter === 'used' ? 'gps-filter-active' : ''}
            onClick={() => setFilter('used')}
          >
            USED
          </button>
          <button
            type="button"
            className={filter === 'all' ? 'gps-filter-active' : ''}
            onClick={() => setFilter('all')}
          >
            ALL
          </button>
        </div>
      </div>
      <DataTable
        ariaLabel="Satellite SNR"
        columns={SNR_COLUMNS}
        rows={rows}
        rowKey={(sat, i) => `${sat.prn}-${i}`}
        emptyLabel="No satellite data available."
      />
    </div>
  );
}

// ── Fix quality panel ───────────────────────────────────────────────────────

function FixQualityPanel({ position }) {
  const fixType = position?.fixType ?? 0;
  const hasFix = fixType >= 2;
  const hdop = position?.hdop;
  const pdop = position?.pdop;
  const cep95 = estimateCEP95(hdop);

  return (
    <div className="lat-panel gps-panel-fix">
      <div className="panel-head"><h3>Fix Quality</h3></div>
      <div className={`big-num ${fixColor(fixType)}`}>{FIX_LABELS[fixType] ?? 'No Fix'}</div>
      <div className="kv">
        <span className="k">Fix type</span>
        <span className={`v ${hasFix ? 'ok' : 'crit'}`}>{hasFix ? 'FIXED' : 'NO FIX'}</span>
      </div>
      <div className="kv">
        <span className="k">HDOP</span>
        <span className={`v ${dopSeverity(hdop) || ''}`}>
          {hdop != null && hdop > 0 ? hdop.toFixed(1) : '—'}
        </span>
      </div>
      <div className="kv">
        <span className="k">PDOP</span>
        <span className={`v ${dopSeverity(pdop) || ''}`}>
          {pdop != null && pdop > 0 ? pdop.toFixed(1) : '—'}
        </span>
      </div>
      <div className="kv">
        <span className="k">CEP 95% · EST.</span>
        <span className="v">{cep95 != null ? `${cep95.toFixed(1)} m` : '—'}</span>
      </div>
    </div>
  );
}

// ── Output protocols panel ──────────────────────────────────────────────────

function OutputProtocolsPanel({ config, onConfigChange, onSave, saving }) {
  const settings = config?.settings ?? {};
  const output = config?.outputProtocols ?? {};

  const update = (section, key, val) => {
    onConfigChange({ ...config, [section]: { ...config[section], [key]: val } });
  };

  const toggle = (on, label, onChange) => (
    <label className={`lat-toggle${on ? ' on' : ''}`}>
      <span className="track" onClick={() => onChange(!on)}>
        <span className="thumb" />
      </span>
      <span className="label">{label}</span>
    </label>
  );

  return (
    <div className="lat-panel col-span-3 gps-panel-output">
      <div className="panel-head">
        <h3>Output Protocols</h3>
      </div>
      <div className="gps-output-row">
        {toggle(settings.enableGps ?? false, 'GPS Enabled', (v) => update('settings', 'enableGps', v))}
        {toggle(output.sendAsNmea ?? false, 'NMEA Out', (v) => update('outputProtocols', 'sendAsNmea', v))}
        {toggle(output.sendAsCot ?? false, 'CoT Out', (v) => update('outputProtocols', 'sendAsCot', v))}
        <div className="lat-field">
          <label>GNSS Source</label>
          <LatSelect
            ariaLabel="GNSS position source"
            value={settings.source ?? GNSSSource.GNSS_SOURCE_INTERNAL}
            onChange={(v) => update('settings', 'source', v)}
            options={SOURCE_OPTIONS}
          />
        </div>
        <div className="lat-field">
          <label>CoT UID</label>
          <input
            className="lat-input"
            value={output.cotUid ?? ''}
            onChange={(e) => update('outputProtocols', 'cotUid', e.target.value)}
            placeholder={nodeName}
          />
        </div>
      </div>
      {settings.source === GNSSSource.GNSS_SOURCE_EXTERNAL_COT && (
        <div className="lat-alert warn gps-output-source-hint">
          Position is adopted from a directly-connected EUD&apos;s (e.g. ATAK) CoT broadcast
          instead of a local receiver. No sky plot / satellite data is available in this mode.
        </div>
      )}
      <div className="gps-output-save">
        <button
          type="button"
          className="lat-btn primary"
          onClick={onSave}
          disabled={saving}
        >
          {saving ? 'SAVING…' : 'SAVE GNSS CONFIG'}
        </button>
      </div>
    </div>
  );
}

// ── Page ────────────────────────────────────────────────────────────────────

export default function GpsStatusPage() {
  const [config, setConfig] = useState(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState(null);
  const [success, setSuccess] = useState(null);
  const mapActionsRef = useRef(null);
  const fixTimesRef = useRef([]);

  // Shared with Dashboard's chip — dedupes the GNSS RPC when both pages
  // are open simultaneously.
  const status = useGnssStatus(POLL_INTERVAL);

  // Record each new fix timestamp for the sliding-window fix rate.
  useEffect(() => {
    const ts = status?.position?.lastUpdate;
    if (!ts) return;
    const d = timestampToDate(ts);
    if (d == null || isNaN(d.getTime())) return;
    const arr = fixTimesRef.current;
    if (arr.length === 0 || arr[arr.length - 1].getTime() !== d.getTime()) {
      arr.push(d);
      if (arr.length > FIX_RATE_SAMPLES) arr.shift();
    }
  }, [status]);

  const fetchConfig = useCallback(async () => {
    try {
      const resp = await gnssClient.getGNSSConfig({});
      setConfig(resp);
      setError(null);
    } catch (e) {
      setError('Failed to load GNSS config: ' + e.message);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    // Fetch-on-mount: pull persisted GNSS config from the daemon to seed
    // the form. No external system supports useSyncExternalStore here.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    fetchConfig();
  }, [fetchConfig]);

  const handleSave = useCallback(async () => {
    setSaving(true);
    setError(null);
    setSuccess(null);
    try {
      const resp = await gnssClient.updateGNSSConfig({
        settings: config.settings,
        outputProtocols: config.outputProtocols,
      });
      if (!resp.success) throw new Error(resp.message || 'Update failed');
      setSuccess(resp.message || 'GNSS configuration updated.');
      fetchConfig();
    } catch (e) {
      setError('Failed to save GNSS config: ' + e.message);
    } finally {
      setSaving(false);
    }
  }, [config, fetchConfig]);

  const position = status?.position;
  const satelliteStatus = status?.satelliteStatus;

  const mgrs = useMemo(
    () => (position ? latLonToMGRS(position.latitude, position.longitude) : null),
    [position],
  );

  const fixRateHz = useMemo(
    // fixTimesRef is the rolling window of fix timestamps maintained in an
    // earlier effect; reading .current here gives the freshest sample set.
    // eslint-disable-next-line react-hooks/refs
    () => computeFixRateHz(fixTimesRef.current),
    // Recompute on every status update so the chip stays fresh.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [status],
  );

  const handleCopyMgrs = useCallback(async () => {
    if (!mgrs) return;
    try {
      await navigator.clipboard.writeText(mgrs);
      setSuccess(`MGRS copied: ${mgrs}`);
      setTimeout(() => setSuccess(null), 1500);
    } catch (e) {
      setError('Clipboard unavailable: ' + e.message);
    }
  }, [mgrs]);

  if (loading) {
    return (
      <>
        <div className="lat-topbar">
          <div className="node-id">
            {nodeName}
            <span className="ip">Loading GNSS data...</span>
          </div>
        </div>
        <div className="lat-view-header">
          <div>
            <h2>◇ GPS / GNSS</h2>
            <div className="crumb">Globe · Sky plot · Position · Satellites · Output</div>
          </div>
        </div>
        <div className="lat-body">
          <div className="lat-panel col-span-all gps-loading">Loading GNSS data...</div>
        </div>
      </>
    );
  }

  const fixType = position?.fixType ?? 0;
  const satsUsed = satelliteStatus?.satellitesUsed ?? 0;
  const satsInView = satelliteStatus?.satellitesInView ?? (satelliteStatus?.satellites?.length ?? 0);
  const hdop = position?.hdop;
  const pdop = position?.pdop;
  const hdopSev = dopSeverity(hdop);
  const pdopSev = dopSeverity(pdop);

  return (
    <>
      <div className="lat-topbar">
        <div className="node-id">
          {nodeName}
          <span className="ip">{FIX_SUBTITLE[fixType] ?? 'NO FIX'} · {satsUsed}/{satsInView} SATS</span>
        </div>
        <div className="chips">
          <span className={`lat-chip ${hdopSev}`}>
            <span className="dot" /> HDOP {hdop != null && hdop > 0 ? hdop.toFixed(1) : '—'}
          </span>
          <span className={`lat-chip ${pdopSev}`}>
            <span className="dot" /> PDOP {pdop != null && pdop > 0 ? pdop.toFixed(1) : '—'}
          </span>
          <span className="lat-chip">
            <span className="dot" /> GPGGA {fixRateHz != null ? `${fixRateHz}HZ` : '—'}
          </span>
        </div>
      </div>

      <div className="lat-view-header">
        <div>
          <h2>◇ GPS / GNSS</h2>
          <div className="crumb">Globe · Sky plot · Position · Satellites · Output</div>
        </div>
        <div className="lat-view-toolbar">
          <button
            type="button"
            className="lat-btn ghost"
            onClick={handleCopyMgrs}
            disabled={!mgrs}
            title={mgrs ? 'Copy MGRS to clipboard' : 'No fix'}
          >
            COPY MGRS
          </button>
          <button
            type="button"
            className="lat-btn ghost"
            onClick={() => mapActionsRef.current?.resetView()}
          >
            RESET VIEW
          </button>
          <button
            type="button"
            className="lat-btn"
            onClick={() => mapActionsRef.current?.centerView()}
            disabled={position?.latitude == null || (position.latitude === 0 && position.longitude === 0)}
          >
            CENTER
          </button>
        </div>
      </div>

      {error && <div className="lat-alert crit">{error}</div>}
      {success && <div className="lat-alert ok">{success}</div>}

      <div className="lat-body grid-3 gps-body">
        <GlobePanel position={position} actionsRef={mapActionsRef} />
        <SkyPlotPanel satelliteStatus={satelliteStatus} />
        <PositionPanel position={position} mgrs={mgrs} />
        <SatelliteSnrPanel satelliteStatus={satelliteStatus} />
        <FixQualityPanel position={position} />
        <OutputProtocolsPanel
          config={config}
          onConfigChange={setConfig}
          onSave={handleSave}
          saving={saving}
        />
      </div>
    </>
  );
}
