// =============================================================================
// SettingsNetwork.jsx — Network interfaces and DHCP settings tab
// =============================================================================

import { useState, useEffect, useCallback } from 'react';
import { createClient } from '@connectrpc/connect';
import { transport } from '../services/connectClient.js';
import { NetworkInterfaceService } from '../gen/openmanet/network_interface/v1/network_interface_service_pb.js';
import { useNetworkInterfaces, refreshNetworkInterfaces } from '../hooks/useNetworkInterfaces.js';
import DataTable from '../components/DataTable.jsx';
import './SettingsNetwork.css';

const netClient = createClient(NetworkInterfaceService, transport);

// Shared interface list polls at the same cadence Dashboard uses (30s).
// The Refresh button calls refreshNetworkInterfaces() for an off-cycle
// fetch so the user does not have to wait for the next regular tick.
const SETTINGS_IFACE_POLL_MS = 30_000;

const IFACE_TYPE_LABELS = {
  0: 'Unknown',
  1: 'Bridge',
  2: 'Ethernet',
  3: 'WiFi AP',
  4: 'HaLow Mesh',
  5: 'Batman',
  6: 'Loopback',
  7: 'VXLAN',
};

const IFACE_STATUS_UP = 1;

function formatBytes(bytes) {
  if (bytes == null) return '—';
  const n = Number(bytes);
  if (!Number.isFinite(n) || n === 0) return '0 B';
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  if (n < 1024 * 1024 * 1024) return `${(n / (1024 * 1024)).toFixed(1)} MB`;
  return `${(n / (1024 * 1024 * 1024)).toFixed(2)} GB`;
}

const INTERFACE_COLUMNS = [
  { key: 'name', label: 'Name', className: 'iface-name', render: (iface) => iface.name },
  { key: 'type', label: 'Type', render: (iface) => IFACE_TYPE_LABELS[iface.type] || 'Unknown' },
  {
    key: 'status',
    label: 'Status',
    render: (iface) => {
      const up = iface.status === IFACE_STATUS_UP;
      return (
        <span className={`lat-chip ${up ? 'ok' : 'crit'}`}>
          <span className="dot" />{up ? 'Up' : 'Down'}
        </span>
      );
    },
  },
  { key: 'ip', label: 'IP', className: 'mono', render: (iface) => iface.ipAddress || '—' },
  { key: 'mac', label: 'MAC', className: 'mono', render: (iface) => iface.macAddress || '—' },
  { key: 'rx', label: 'RX', className: 'num', headerClass: 'num', render: (iface) => formatBytes(iface.rxBytes) },
  { key: 'tx', label: 'TX', className: 'num', headerClass: 'num', render: (iface) => formatBytes(iface.txBytes) },
  { key: 'mtu', label: 'MTU', className: 'num', headerClass: 'num', render: (iface) => iface.mtu || '—' },
];

function InterfacesPanel() {
  // Shared with Dashboard so navigating Dashboard → SettingsNetwork
  // renders cached interfaces immediately instead of flashing empty.
  const snapshot = useNetworkInterfaces(SETTINGS_IFACE_POLL_MS);
  const loading = snapshot === null;
  const interfaces = snapshot?.interfaces ?? [];
  const error = snapshot?.error ?? null;

  const upCount = interfaces.filter((i) => i.status === IFACE_STATUS_UP).length;

  return (
    <div className="lat-panel net-panel">
      <div className="panel-head">
        <h3>Network Interfaces</h3>
        <div className="panel-head-right">
          {!loading && interfaces.length > 0 && (
            <span className="lat-chip ok"><span className="dot" />{upCount} / {interfaces.length} Up</span>
          )}
          <div className="actions">
            <button type="button" onClick={refreshNetworkInterfaces} disabled={loading}>Refresh</button>
          </div>
        </div>
      </div>

      {error && <div className="lat-alert crit">{error}</div>}

      {loading ? (
        <div className="lat-empty">Loading…</div>
      ) : (
        <DataTable
          ariaLabel="Network interfaces"
          columns={INTERFACE_COLUMNS}
          rows={interfaces}
          rowKey={(iface) => iface.name}
          emptyLabel="No interfaces found."
        />
      )}
    </div>
  );
}

// Module-level like INTERFACE_COLUMNS above — neither depends on props or
// state, so there's no reason to rebuild them on every DHCPPanel render.
const ACTIVE_LEASE_COLUMNS = [
  { key: 'hostname', label: 'Hostname', render: (r) => r.hostname || '—' },
  { key: 'macAddress', label: 'MAC', className: 'mono', render: (r) => r.macAddress ?? '—' },
  { key: 'ipAddress', label: 'IP', className: 'mono', render: (r) => r.ipAddress ?? '—' },
  { key: 'expiresSeconds', label: 'Expires', className: 'num', headerClass: 'num', render: (r) => `${r.expiresSeconds}s` },
];

const STATIC_LEASE_COLUMNS = [
  { key: 'hostname', label: 'Hostname', render: (r) => r.hostname || '—' },
  { key: 'macAddress', label: 'MAC', className: 'mono', render: (r) => r.macAddress ?? '—' },
  { key: 'ipAddress', label: 'IP', className: 'mono', render: (r) => r.ipAddress ?? '—' },
];

function LeasesTable({ rows, columns, ariaLabel }) {
  return (
    <DataTable
      ariaLabel={ariaLabel}
      columns={columns}
      rows={rows ?? []}
      rowKey={(r, i) => r.macAddress ?? String(i)}
      emptyLabel="No entries."
    />
  );
}

function DHCPPanel() {
  const [config, setConfig] = useState(null);
  const [activeLeases, setActiveLeases] = useState([]);
  const [staticLeases, setStaticLeases] = useState([]);
  const [loading, setLoading] = useState(true);
  const [showActive, setShowActive] = useState(false);
  const [showStatic, setShowStatic] = useState(false);
  const [error, setError] = useState(null);

  const load = useCallback(async () => {
    setLoading(true);
    const [cfgRes, activeRes, staticRes] = await Promise.allSettled([
      netClient.getDHCPServerConfig({}),
      netClient.listActiveDHCPLeases({}),
      netClient.listStaticDHCPLeases({}),
    ]);
    if (cfgRes.status === 'fulfilled') {
      setConfig(cfgRes.value.config);
      setError(null);
    } else {
      setConfig(null);
      setError(cfgRes.reason?.message ?? 'Failed to load DHCP config');
    }
    if (activeRes.status === 'fulfilled') setActiveLeases(activeRes.value.leases ?? []);
    if (staticRes.status === 'fulfilled') setStaticLeases(staticRes.value.leases ?? []);
    setLoading(false);
  }, []);

  useEffect(() => {
    // Fetch-on-mount: pull DHCP config + leases from the daemon to seed
    // the form. No external system supports useSyncExternalStore here.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    load();
  }, [load]);

  return (
    <div className="lat-panel net-panel">
      <div className="panel-head">
        <h3>DHCP Server</h3>
        <div className="panel-head-right">
          {!loading && config && (
            <span className={`lat-chip ${config.dnsForwardingEnabled ? 'ok' : ''}`}>
              <span className="dot" />{config.activeLeaseCount ?? 0} Active
            </span>
          )}
          <div className="actions">
            <button type="button" onClick={load} disabled={loading}>Refresh</button>
          </div>
        </div>
      </div>

      {error && <div className="lat-alert crit">{error}</div>}

      {loading ? (
        <div className="lat-empty">Loading…</div>
      ) : !config ? (
        <div className="lat-empty">DHCP server not configured.</div>
      ) : (
        <>
          <div className="status-strip">
            <div className="kv">
              <span className="k">Interface</span>
              <span className="v accent">{config.interfaceName || '—'}</span>
            </div>
            <div className="kv">
              <span className="k">Range</span>
              <span className="v">{config.rangeStart} — {config.rangeEnd}</span>
            </div>
            <div className="kv">
              <span className="k">Lease Time</span>
              <span className="v">{config.leaseTime || '—'}</span>
            </div>
            <div className="kv">
              <span className="k">DNS Forwarding</span>
              <span className={`v ${config.dnsForwardingEnabled ? 'ok' : 'warn'}`}>
                {config.dnsForwardingEnabled ? 'Enabled' : 'Disabled'}
              </span>
            </div>
            <div className="kv">
              <span className="k">Active Leases</span>
              <span className="v">{config.activeLeaseCount ?? 0}</span>
            </div>
          </div>

          <div className="disclosure">
            <button
              type="button"
              className="disclosure-head"
              aria-expanded={showActive}
              onClick={() => setShowActive((v) => !v)}
            >
              <span className="caret">{showActive ? '▼' : '▶'}</span>
              Active Leases ({activeLeases.length})
            </button>
            {showActive && (
              <div className="disclosure-body">
                <LeasesTable rows={activeLeases} columns={ACTIVE_LEASE_COLUMNS} ariaLabel="Active DHCP leases" />
              </div>
            )}
          </div>

          <div className="disclosure">
            <button
              type="button"
              className="disclosure-head"
              aria-expanded={showStatic}
              onClick={() => setShowStatic((v) => !v)}
            >
              <span className="caret">{showStatic ? '▼' : '▶'}</span>
              Static Reservations ({staticLeases.length})
            </button>
            {showStatic && (
              <div className="disclosure-body">
                <LeasesTable rows={staticLeases} columns={STATIC_LEASE_COLUMNS} ariaLabel="Static DHCP reservations" />
              </div>
            )}
          </div>
        </>
      )}
    </div>
  );
}

export default function SettingsNetwork() {
  return (
    <div className="settings-network">
      <h2 className="settings-h2">◇ Network</h2>
      <div className="settings-network-list">
        <InterfacesPanel />
        <DHCPPanel />
      </div>
    </div>
  );
}
