// =============================================================================
// Dashboard.test.jsx — Tests for the Lattice dashboard layout
// =============================================================================

import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest';
import { render, screen, waitFor, cleanup } from '@testing-library/react';

// ── Mocks ──────────────────────────────────────────────────────────────────

const {
  mockGetDashboardStatus,
  mockGetGNSSStatus,
  mockGetBLOSStatus,
  mockListBLOSPeers,
  mockListNetworkInterfaces,
} = vi.hoisted(() => ({
  mockGetDashboardStatus: vi.fn(),
  mockGetGNSSStatus: vi.fn(),
  mockGetBLOSStatus: vi.fn(),
  mockListBLOSPeers: vi.fn(),
  mockListNetworkInterfaces: vi.fn(),
}));

vi.mock('@connectrpc/connect', () => ({
  createClient: () => ({
    // All services share a single mock object — only the methods the page
    // calls need to be populated.
    getDashboardStatus: mockGetDashboardStatus,
    getGNSSStatus: mockGetGNSSStatus,
    getBLOSStatus: mockGetBLOSStatus,
    listBLOSPeers: mockListBLOSPeers,
    listNetworkInterfaces: mockListNetworkInterfaces,
  }),
}));
vi.mock('../../services/connectClient.js', () => ({ transport: {} }));
vi.mock('../../gen/openmanet/dashboard/v1/dashboard_service_pb.js', () => ({
  DashboardService: {},
}));
vi.mock('../../gen/openmanet/gnss/v1/gnss_service_pb.js', () => ({
  GNSSService: {},
}));
vi.mock('../../gen/openmanet/blos/v1/blos_service_pb.js', () => ({
  BLOSService: {},
}));
vi.mock('../../gen/openmanet/network_interface/v1/network_interface_service_pb.js', () => ({
  NetworkInterfaceService: {},
}));
vi.mock('../../gen/openmanet/dashboard/v1/dashboard_pb.js', () => ({
  NetworkInterfaceState: { CONNECTED: 1, DISCONNECTED: 2, NOT_CONNECTED: 3 },
}));
vi.mock('../../gen/openmanet/network_interface/v1/interface_pb.js', () => ({
  InterfaceType: {
    UNSPECIFIED: 0, BRIDGE: 1, ETHERNET: 2, WIFI_AP: 3,
    HALOW_MESH: 4, BATMAN: 5, LOOPBACK: 6, VXLAN: 7,
  },
  InterfaceStatus: { UNSPECIFIED: 0, UP: 1, DOWN: 2 },
}));
vi.mock('../../services/meshApi.js', () => ({
  fetchMeshStatus: vi.fn().mockResolvedValue({
    status: { connected: false, neighbors: 0, mesh_interfaces: 0, is_gateway: false },
    nodes: [], neighbors: [], interfaces: [],
  }),
  fetchMeshTopology: vi.fn().mockResolvedValue(null),
  fetchMeshTopologyDelta: vi.fn().mockResolvedValue(null),
}));
vi.mock('../../components/TopologyMap.jsx', () => ({
  default: () => null,
}));

import DashboardPage from '../../pages/Dashboard.jsx';
import { fetchMeshStatus } from '../../services/meshApi.js';

beforeEach(() => {
  mockGetGNSSStatus.mockResolvedValue({
    position: { fixType: 1 },
    satelliteStatus: { satellitesUsed: 0, satellitesInView: 0, satellites: [] },
  });
  mockGetBLOSStatus.mockResolvedValue({ blosEnabled: true });
  mockListBLOSPeers.mockResolvedValue({ peers: [] });
  mockListNetworkInterfaces.mockResolvedValue({ interfaces: [] });
  // The module mock's default is consumed by the first test that sets
  // neighbors; re-arm it so later tests never inherit another test's mesh.
  fetchMeshStatus.mockResolvedValue({
    status: { connected: false, neighbors: 0, mesh_interfaces: 0, is_gateway: false },
    nodes: [], neighbors: [], interfaces: [],
  });
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  mockGetDashboardStatus.mockReset();
  mockGetGNSSStatus.mockReset();
  mockGetBLOSStatus.mockReset();
  mockListBLOSPeers.mockReset();
  mockListNetworkInterfaces.mockReset();
});

// ── Fixtures ───────────────────────────────────────────────────────────────

function makeDeviceInfo(overrides = {}) {
  return {
    hostname: 'HaLowLink2-6cff',
    model: 'MorseMicro HaLowLink 2',
    firmware: 'OpenWrt 23.05.3 / OpenMANET 1.7.0',
    kernel: '5.15.150',
    architecture: 'mipsel_24kc',
    ...overrides,
  };
}

function makeSystemResources(overrides = {}) {
  return {
    uptime: { seconds: BigInt(224580), nanos: 0 }, // 2d 14h 23m
    localTime: { seconds: BigInt(1774021337), nanos: 0 },
    cpuLoadPercent: 12,
    memoryTotalBytes: BigInt(260046848),
    memoryUsedBytes: BigInt(176160768),
    overlayTotalBytes: BigInt(3880000),
    overlayUsedBytes: BigInt(3250000),
    ...overrides,
  };
}

function makeNetworkEntry(overrides = {}) {
  return {
    interfaceName: 'eth0',
    displayName: 'WAN (eth0)',
    state: 2,
    detail: 'Disconnected',
    rxBytes: BigInt(0),
    txBytes: BigInt(0),
    ...overrides,
  };
}

// Kernel-classified network interfaces returned by
// NetworkInterfaceService.listNetworkInterfaces. The dashboard's network
// panel renders these directly, including non-curated wireless types like
// WIFI_AP that the dashboard summary used to omit.
function makeNetworkInterface(overrides = {}) {
  return {
    name: 'eth0',
    type: 2, // ETHERNET
    ipAddress: '',
    macAddress: '',
    status: 2, // DOWN
    rxBytes: BigInt(0),
    txBytes: BigInt(0),
    mtu: 1500,
    ...overrides,
  };
}

function makeNetworkInterfaceList() {
  return {
    interfaces: [
      makeNetworkInterface({ name: 'eth0', type: 2, status: 2, rxBytes: BigInt(1024), txBytes: BigInt(2048) }),
      makeNetworkInterface({ name: 'br-ahwlan', type: 1, status: 1, ipAddress: '10.41.25.72/16', rxBytes: BigInt(142300000), txBytes: BigInt(87600000) }),
      makeNetworkInterface({ name: 'wlh0', type: 3, status: 1, ipAddress: '', rxBytes: BigInt(2048), txBytes: BigInt(4096) }),
      makeNetworkInterface({ name: 'phy1-ap0', type: 4, status: 1, rxBytes: BigInt(12345678), txBytes: BigInt(9876543) }),
      makeNetworkInterface({ name: 'bat0', type: 5, status: 1, rxBytes: BigInt(5_000_000_000), txBytes: BigInt(3_000_000_000) }),
      makeNetworkInterface({ name: 'tailscale0', type: 7, status: 2, rxBytes: BigInt(0), txBytes: BigInt(0) }),
      makeNetworkInterface({ name: 'lo', type: 6, status: 1, rxBytes: BigInt(0), txBytes: BigInt(0) }),
    ],
  };
}

function makeDashboardResponse(overrides = {}) {
  return {
    deviceInfo: makeDeviceInfo(),
    systemResources: makeSystemResources(),
    networkSummary: {
      entries: [
        makeNetworkEntry({ interfaceName: 'eth0', displayName: 'WAN (eth0)', state: 2, detail: 'Disconnected', rxBytes: BigInt(1024), txBytes: BigInt(2048) }),
        makeNetworkEntry({ interfaceName: 'br-ahwlan', displayName: 'LAN (br-ahwlan)', state: 1, detail: '10.41.25.72/16', rxBytes: BigInt(142300000), txBytes: BigInt(87600000) }),
        makeNetworkEntry({ interfaceName: 'phy1-ap0', displayName: 'HaLow Mesh (phy1-ap0)', state: 1, detail: 'Connected — 3 neighbors', rxBytes: BigInt(12345678), txBytes: BigInt(9876543) }),
        makeNetworkEntry({ interfaceName: 'bat0', displayName: 'BATMAN (bat0)', state: 1, detail: '4 originators', rxBytes: BigInt(5_000_000_000), txBytes: BigInt(3_000_000_000) }),
        makeNetworkEntry({ interfaceName: 'tailscale0', displayName: 'Tailscale (tailscale0)', state: 3, detail: 'Not connected', rxBytes: BigInt(0), txBytes: BigInt(0) }),
      ],
    },
    activeServices: [],
    ...overrides,
  };
}

// ── Loading state ──────────────────────────────────────────────────────────

describe('TestDashboardLoading', () => {
  it('renders loading indicator before data arrives', () => {
    mockGetDashboardStatus.mockReturnValue(new Promise(() => {}));
    render(<DashboardPage />);
    expect(screen.getByText('Loading dashboard...')).toBeTruthy();
  });

  it('hides loading after data loads', async () => {
    mockGetDashboardStatus.mockResolvedValue(makeDashboardResponse());
    render(<DashboardPage />);
    await waitFor(() => {
      expect(screen.getByText('◇ Dashboard')).toBeTruthy();
      expect(screen.queryByText('Loading dashboard...')).toBeNull();
    });
  });

  it('hides loading even on error', async () => {
    mockGetDashboardStatus.mockRejectedValue(new Error('network error'));
    render(<DashboardPage />);
    await waitFor(() => {
      expect(screen.queryByText('Loading dashboard...')).toBeNull();
    });
  });
});

// ── Chrome: topbar + view header ───────────────────────────────────────────

describe('TestDashboardChrome', () => {
  it('renders node id, status chips, and header toolbar', async () => {
    mockGetDashboardStatus.mockResolvedValue(makeDashboardResponse());
    const { container } = render(<DashboardPage />);
    await waitFor(() => screen.getByText('◇ Dashboard'));

    const topbar = container.querySelector('.lat-topbar');
    expect(topbar).toBeTruthy();
    expect(topbar.textContent).toContain('HALOWLINK2-6CFF');

    const chips = [...container.querySelectorAll('.lat-chip')].map((c) => c.textContent.trim());
    expect(chips.some((c) => /MESH (UP|DOWN)/.test(c))).toBe(true);
    expect(chips.some((c) => /GPS/.test(c))).toBe(true);
    expect(chips.some((c) => /BLOS · 0 PEERS/.test(c))).toBe(true);

    // The CUSTOMIZE and EXPORT toolbar actions were removed — the
    // header should carry no toolbar buttons at all.
    const toolbarBtns = [...container.querySelectorAll('.lat-view-toolbar .lat-btn')]
      .map((b) => b.textContent.trim());
    expect(toolbarBtns).not.toContain('CUSTOMIZE');
    expect(toolbarBtns).not.toContain('EXPORT');
  });

  it('hides the BLOS chip when BLOS is disabled', async () => {
    mockGetBLOSStatus.mockResolvedValue({ blosEnabled: false });
    mockGetDashboardStatus.mockResolvedValue(makeDashboardResponse());
    const { container } = render(<DashboardPage />);
    await waitFor(() => screen.getByText('◇ Dashboard'));

    const chips = [...container.querySelectorAll('.lat-chip')].map((c) => c.textContent.trim());
    expect(chips.some((c) => /BLOS/.test(c))).toBe(false);
  });

  it('does not render a PTT Latency card (PTT lives on Comms)', async () => {
    mockGetDashboardStatus.mockResolvedValue(makeDashboardResponse());
    render(<DashboardPage />);
    await waitFor(() => screen.getByText('◇ Dashboard'));
    expect(screen.queryByText('PTT Latency')).toBeNull();
  });

  it('uses the first connected interface address as the node IP', async () => {
    mockGetDashboardStatus.mockResolvedValue(makeDashboardResponse());
    const { container } = render(<DashboardPage />);
    await waitFor(() => screen.getByText('◇ Dashboard'));
    const nodeId = container.querySelector('.lat-topbar .node-id');
    expect(nodeId).toBeTruthy();
    expect(nodeId.textContent).toContain('10.41.25.72/16');
  });
});

// ── KPI cards (Row 1) ──────────────────────────────────────────────────────

describe('TestDashboardKpis', () => {
  it('renders the two KPI panels', async () => {
    mockGetDashboardStatus.mockResolvedValue(makeDashboardResponse());
    render(<DashboardPage />);
    await waitFor(() => {
      expect(screen.getByText('Mesh Peers')).toBeTruthy();
      expect(screen.getByText('Link Quality · 5m')).toBeTruthy();
    });
  });
});

// ── Row 2 + Row 3 panels ───────────────────────────────────────────────────

describe('TestDashboardPanels', () => {
  it('renders the peers, alerts, resources, and interfaces panels', async () => {
    mockGetDashboardStatus.mockResolvedValue(makeDashboardResponse());
    render(<DashboardPage />);
    await waitFor(() => {
      expect(screen.getByText('Mesh Peers · Live')).toBeTruthy();
      expect(screen.getByText('Alerts · Active')).toBeTruthy();
      expect(screen.getByText('System Resources')).toBeTruthy();
      expect(screen.getByText('Network Interfaces')).toBeTruthy();
    });
  });

  it('does not render the topology panel (topology has its own route)', async () => {
    mockGetDashboardStatus.mockResolvedValue(makeDashboardResponse());
    render(<DashboardPage />);
    await waitFor(() => screen.getByText('◇ Dashboard'));
    expect(screen.queryByText('Network Topology')).toBeNull();
  });

  it('renders system-resource pbars and kv column with live data', async () => {
    mockGetDashboardStatus.mockResolvedValue(makeDashboardResponse());
    const { container } = render(<DashboardPage />);
    await waitFor(() => {
      expect(screen.getByText('CPU')).toBeTruthy();
      expect(screen.getByText('MEM')).toBeTruthy();
      expect(screen.getByText('OVERLAY')).toBeTruthy();
      expect(screen.getByText('2d 14h 23m')).toBeTruthy();
      expect(screen.getByText('MorseMicro HaLowLink 2')).toBeTruthy();
      expect(screen.getByText('5.15.150')).toBeTruthy();
      expect(screen.getByText('OpenWrt 23.05.3 / OpenMANET 1.7.0')).toBeTruthy();
      expect(screen.getByText('mipsel_24kc')).toBeTruthy();
    });
    // 3 pbars: CPU, MEM, OVERLAY. LOAD 1M and HW Rev were removed because
    // neither field is surfaced by the current system-status handler.
    expect(container.querySelectorAll('.pbar').length).toBe(3);
    // Board model sits directly under Uptime and above Kernel.
    const keys = Array.from(container.querySelectorAll('.dashboard-resources-kv .kv .k'))
      .map((el) => el.textContent);
    expect(keys.slice(0, 3)).toEqual(['Uptime', 'Model', 'Kernel']);
    expect(screen.queryByText('LOAD 1M')).toBeNull();
    expect(screen.queryByText('HW Rev')).toBeNull();
  });

  it('renders a network interfaces table row per entry', async () => {
    mockGetDashboardStatus.mockResolvedValue(makeDashboardResponse());
    mockListNetworkInterfaces.mockResolvedValue(makeNetworkInterfaceList());
    const { container } = render(<DashboardPage />);
    await waitFor(() => {
      expect(screen.getByText('Network Interfaces')).toBeTruthy();
    });
    const table = [...container.querySelectorAll('table.lat-table')]
      .find((t) => t.textContent.includes('Iface'));
    expect(table).toBeTruthy();
    // 6 interface rows from the fixture (loopback is filtered out).
    await waitFor(() => {
      expect(table.querySelectorAll('tbody tr').length).toBe(6);
    });
    expect(table.textContent).toContain('eth0');
    expect(table.textContent).toContain('wlh0');
    expect(table.textContent).toContain('bat0');
    expect(table.textContent).toContain('tailscale0');
    // Loopback is hidden from the operator-facing summary.
    expect(table.textContent).not.toContain(/(^|\s)lo(\s|$)/);
    // Each non-loopback row carries a role label derived from the
    // kernel-classified interface type.
    expect(table.textContent).toContain('AP');
    expect(table.textContent).toContain('mesh radio');
    expect(table.textContent).toContain('BLOS');
  });

  it('renders RX/TX byte counters in the network interfaces table', async () => {
    mockGetDashboardStatus.mockResolvedValue(makeDashboardResponse());
    mockListNetworkInterfaces.mockResolvedValue(makeNetworkInterfaceList());
    const { container } = render(<DashboardPage />);
    await waitFor(() => {
      expect(screen.getByText('Network Interfaces')).toBeTruthy();
    });
    const table = [...container.querySelectorAll('table.lat-table')]
      .find((t) => t.textContent.includes('Iface'));
    await waitFor(() => {
      expect(table.querySelectorAll('tbody tr').length).toBeGreaterThan(1);
    });
    const rowsByIface = new Map(
      [...table.querySelectorAll('tbody tr')].map((tr) => {
        const cells = [...tr.querySelectorAll('td')].map((td) => td.textContent.trim());
        return [cells[0].trim(), cells];
      }),
    );
    // eth0: 1024 RX, 2048 TX — boundary KB rendering
    expect(rowsByIface.get('eth0')[4]).toBe('1.0 KB');
    expect(rowsByIface.get('eth0')[5]).toBe('2.0 KB');
    // br-ahwlan: 142.3 MB / 87.6 MB
    expect(rowsByIface.get('br-ahwlan')[4]).toBe('135.7 MB');
    expect(rowsByIface.get('br-ahwlan')[5]).toBe('83.5 MB');
    // bat0: 5 GB / 3 GB
    expect(rowsByIface.get('bat0')[4]).toBe('4.66 GB');
    expect(rowsByIface.get('bat0')[5]).toBe('2.79 GB');
    // tailscale0: zeroed counters render as 0 B, not as a dash.
    expect(rowsByIface.get('tailscale0')[4]).toBe('0 B');
    expect(rowsByIface.get('tailscale0')[5]).toBe('0 B');
  });

  it('peers-live table shows an empty-state row when no neighbors', async () => {
    mockGetDashboardStatus.mockResolvedValue(makeDashboardResponse());
    render(<DashboardPage />);
    await waitFor(() => {
      expect(screen.getByText('No neighbors reporting')).toBeTruthy();
    });
  });

  it('peers-live table headers show Throughput instead of TQ', async () => {
    mockGetDashboardStatus.mockResolvedValue(makeDashboardResponse());
    const { container } = render(<DashboardPage />);
    await waitFor(() => screen.getByText('Mesh Peers · Live'));
    // The mesh-peers table <th> row should carry a Throughput column
    // and no longer carry a TQ column.
    const headers = [...container.querySelectorAll('.lat-table thead th')]
      .map((th) => th.textContent.trim());
    expect(headers).toContain('Throughput');
    expect(headers).not.toContain('TQ');
  });

  it('renders CPU load to 2 decimal places', async () => {
    mockGetDashboardStatus.mockResolvedValue(
      makeDashboardResponse({
        systemResources: makeSystemResources({ cpuLoadPercent: 12.345 }),
      }),
    );
    const { container } = render(<DashboardPage />);
    await waitFor(() => screen.getByText('System Resources'));
    // Find the CPU row's detail cell — it sits alongside the "CPU" label.
    const pbarText = [...container.querySelectorAll('.pbar-row')]
      .map((row) => row.textContent)
      .find((t) => t.startsWith('CPU'));
    expect(pbarText).toContain('12.35%'); // toFixed(2) rounds half up
  });
});

// ── Alerts ─────────────────────────────────────────────────────────────────

describe('TestDashboardAlerts', () => {
  it('shows a MESH DOWN alert when not connected', async () => {
    mockGetDashboardStatus.mockResolvedValue(makeDashboardResponse());
    const { container } = render(<DashboardPage />);
    await waitFor(() => screen.getByText('Alerts · Active'));
    const alerts = [...container.querySelectorAll('.lat-alert')].map((a) => a.textContent.trim());
    expect(alerts.some((a) => /MESH DOWN/.test(a))).toBe(true);
  });
});

// ── Null data ──────────────────────────────────────────────────────────────

describe('TestDashboardNullData', () => {
  it('handles completely null response fields', async () => {
    mockGetDashboardStatus.mockResolvedValue({
      deviceInfo: null,
      systemResources: null,
      networkSummary: null,
      activeServices: [],
    });
    render(<DashboardPage />);
    await waitFor(() => {
      expect(screen.getByText('◇ Dashboard')).toBeTruthy();
      expect(screen.getByText('System Resources')).toBeTruthy();
      expect(screen.getByText('Network Interfaces')).toBeTruthy();
      expect(screen.getByText('No network data')).toBeTruthy();
    });
  });
});

// ── Polling ────────────────────────────────────────────────────────────────

describe('TestDashboardPolling', () => {
  beforeEach(() => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it('polls for dashboard status at interval', async () => {
    mockGetDashboardStatus.mockResolvedValue(makeDashboardResponse());
    render(<DashboardPage />);
    await waitFor(() => screen.getByText('◇ Dashboard'));
    const initialCalls = mockGetDashboardStatus.mock.calls.length;
    vi.advanceTimersByTime(5000);
    await waitFor(() => {
      expect(mockGetDashboardStatus.mock.calls.length).toBeGreaterThan(initialCalls);
    });
  });

  it('continues polling after fetch error', async () => {
    mockGetDashboardStatus
      .mockResolvedValueOnce(makeDashboardResponse())
      .mockRejectedValueOnce(new Error('transient'))
      .mockResolvedValueOnce(makeDashboardResponse());

    render(<DashboardPage />);
    await waitFor(() => screen.getByText('◇ Dashboard'));

    vi.advanceTimersByTime(5000);
    await waitFor(() => {
      expect(mockGetDashboardStatus.mock.calls.length).toBe(2);
    });

    vi.advanceTimersByTime(5000);
    await waitFor(() => {
      expect(mockGetDashboardStatus.mock.calls.length).toBe(3);
    });
  });
});

// ── Uptime formatting ──────────────────────────────────────────────────────

describe('TestUptimeFormatting', () => {
  it('formats days+hours+minutes', async () => {
    mockGetDashboardStatus.mockResolvedValue(makeDashboardResponse());
    render(<DashboardPage />);
    await waitFor(() => expect(screen.getByText('2d 14h 23m')).toBeTruthy());
  });

  it('formats hours only', async () => {
    const resources = makeSystemResources({ uptime: { seconds: BigInt(7380), nanos: 0 } });
    mockGetDashboardStatus.mockResolvedValue(makeDashboardResponse({ systemResources: resources }));
    render(<DashboardPage />);
    await waitFor(() => expect(screen.getByText('2h 3m')).toBeTruthy());
  });

  it('formats minutes only', async () => {
    const resources = makeSystemResources({ uptime: { seconds: BigInt(300), nanos: 0 } });
    mockGetDashboardStatus.mockResolvedValue(makeDashboardResponse({ systemResources: resources }));
    render(<DashboardPage />);
    await waitFor(() => expect(screen.getByText('5m')).toBeTruthy());
  });
});

// ── Peer TX Rate column ────────────────────────────────────────────────────

describe('TestDashboardPeerTxRate', () => {
  const HE40 = { bitrateKbps: 86700, phy: 4, widthMhz: 40, mcs: 7, nss: 2 };
  const HT20 = { bitrateKbps: 21600, phy: 2, widthMhz: 20, mcs: 3, nss: 1 };

  function meshWith(neighbors) {
    fetchMeshStatus.mockResolvedValue({
      status: { connected: true, neighbors: neighbors.length, mesh_interfaces: 2, is_gateway: false },
      nodes: [], neighbors, interfaces: [],
    });
  }

  function peerRow(name) {
    return screen.getByText(name).closest('tr');
  }

  function cells(row) {
    return [...row.querySelectorAll('td')].map((td) => td.textContent);
  }

  it('adds a TX Rate column after Throughput', async () => {
    mockGetDashboardStatus.mockResolvedValue(makeDashboardResponse());
    const { container } = render(<DashboardPage />);
    await waitFor(() => screen.getByText('Mesh Peers · Live'));

    const headers = [...container.querySelectorAll('.lat-panel .lat-table thead th')]
      .map((th) => th.textContent.trim());
    const thr = headers.indexOf('Throughput');
    expect(thr).toBeGreaterThan(-1);
    expect(headers[thr + 1]).toBe('TX Rate');
  });

  it('renders the TX rate with PHY, width, MCS and streams', async () => {
    mockGetDashboardStatus.mockResolvedValue(makeDashboardResponse());
    meshWith([{ name: 'bravo_mesh1', mac: 'aa:bb:cc:dd:ee:01', signal: -61, throughput: 60_000_000, iface: 'mesh1', tx: HE40, rx: HT20 }]);
    render(<DashboardPage />);

    await waitFor(() => screen.getByText('bravo'));
    const row = cells(peerRow('bravo'));
    expect(row[4]).toContain('86.7 Mbps');
    expect(row[4]).toContain('mesh1 · HE40 · MCS7 · 2SS');
  });

  it('shows a dash when the driver reported no rate', async () => {
    mockGetDashboardStatus.mockResolvedValue(makeDashboardResponse());
    meshWith([{ name: 'gamma_mesh0', mac: 'aa:bb:cc:dd:ee:02', signal: -75, throughput: 0, iface: 'mesh0', tx: null, rx: null }]);
    render(<DashboardPage />);

    await waitFor(() => screen.getByText('gamma'));
    expect(cells(peerRow('gamma'))[4]).toBe('—');
  });

  it('omits unknown parts of the detail line', async () => {
    mockGetDashboardStatus.mockResolvedValue(makeDashboardResponse());
    meshWith([{ name: 'delta_mesh0', mac: 'aa:bb:cc:dd:ee:03', signal: -70, throughput: 0, iface: 'mesh0', tx: { bitrateKbps: 54, phy: 0, widthMhz: 0, mcs: -1, nss: -1 }, rx: null }]);
    render(<DashboardPage />);

    await waitFor(() => screen.getByText('delta'));
    const cell = cells(peerRow('delta'))[4];
    expect(cell).toContain('54 Kbps');
    expect(cell).toContain('mesh0');
    expect(cell).not.toContain('MCS');
    expect(cell).not.toContain('SS');
  });

  it('keeps the fastest interface rate when a host has two radios', async () => {
    mockGetDashboardStatus.mockResolvedValue(makeDashboardResponse());
    meshWith([
      { name: 'echo_mesh0', mac: 'aa:bb:cc:dd:ee:04', signal: -55, throughput: 7_000_000, iface: 'mesh0', tx: HT20, rx: HT20 },
      { name: 'echo_mesh1', mac: 'aa:bb:cc:dd:ee:05', signal: -66, throughput: 60_000_000, iface: 'mesh1', tx: HE40, rx: HE40 },
    ]);
    render(<DashboardPage />);

    await waitFor(() => screen.getByText('echo'));
    expect(screen.getAllByText('echo')).toHaveLength(1);
    const row = cells(peerRow('echo'));
    expect(row[4]).toContain('86.7 Mbps');
    expect(row[4]).toContain('mesh1 · HE40');
    expect(row[5]).toBe('-55'); // RSSI still the strongest radio
  });
});
