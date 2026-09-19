// =============================================================================
// SettingsNetwork.test.jsx — Tests for the Network settings tab
// =============================================================================

import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, screen, waitFor, fireEvent, cleanup } from '@testing-library/react';

const { mockListNetworkInterfaces, mockGetDHCPServerConfig, mockListActiveDHCPLeases, mockListStaticDHCPLeases } = vi.hoisted(() => ({
  mockListNetworkInterfaces: vi.fn(),
  mockGetDHCPServerConfig: vi.fn(),
  mockListActiveDHCPLeases: vi.fn(),
  mockListStaticDHCPLeases: vi.fn(),
}));
vi.mock('@connectrpc/connect', () => ({
  createClient: () => ({
    listNetworkInterfaces: mockListNetworkInterfaces,
    getDHCPServerConfig: mockGetDHCPServerConfig,
    listActiveDHCPLeases: mockListActiveDHCPLeases,
    listStaticDHCPLeases: mockListStaticDHCPLeases,
  }),
}));
vi.mock('../../services/connectClient.js', () => ({ transport: {} }));

import SettingsNetworkPage from '../../pages/SettingsNetwork.jsx';

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  mockListNetworkInterfaces.mockReset();
  mockGetDHCPServerConfig.mockReset();
  mockListActiveDHCPLeases.mockReset();
  mockListStaticDHCPLeases.mockReset();
});

const INTERFACES = [
  { name: 'eth0', type: 2, status: 1, ipAddress: '192.168.1.1', macAddress: 'aa:bb:cc:dd:ee:01', rxBytes: 1048576, txBytes: 524288, mtu: 1500 },
  { name: 'wlh0', type: 4, status: 1, ipAddress: '10.41.1.1', macAddress: 'aa:bb:cc:dd:ee:02', rxBytes: 2097152, txBytes: 1048576, mtu: 1500 },
  { name: 'br-lan', type: 1, status: 2, ipAddress: '', macAddress: 'aa:bb:cc:dd:ee:03', rxBytes: 0, txBytes: 0, mtu: 1500 },
];

const DHCP_CONFIG = {
  config: {
    interfaceName: 'br-lan',
    rangeStart: '192.168.1.100',
    rangeEnd: '192.168.1.200',
    leaseTime: '12h',
    dnsForwardingEnabled: true,
    activeLeaseCount: 2,
  },
};

const ACTIVE_LEASES = {
  leases: [
    { hostname: 'laptop', macAddress: 'ff:ff:ff:ff:ff:01', ipAddress: '192.168.1.101', expiresSeconds: 3600 },
    { hostname: 'phone', macAddress: 'ff:ff:ff:ff:ff:02', ipAddress: '192.168.1.102', expiresSeconds: 7200 },
  ],
};

const STATIC_LEASES = {
  leases: [
    { hostname: 'printer', macAddress: 'ff:ff:ff:ff:ff:03', ipAddress: '192.168.1.10' },
  ],
};

describe('TestNetworkInterfacesRender', () => {
  it('renders interface table with data', async () => {
    mockListNetworkInterfaces.mockResolvedValue({ interfaces: INTERFACES });
    mockGetDHCPServerConfig.mockResolvedValue(DHCP_CONFIG);
    mockListActiveDHCPLeases.mockResolvedValue(ACTIVE_LEASES);
    mockListStaticDHCPLeases.mockResolvedValue(STATIC_LEASES);

    render(<SettingsNetworkPage />);
    // DataTable renders every row as both a <td> and a mobile
    // .lat-card-head/.kv .v, so a bare screen.getByText(name) now matches
    // twice. Scope to the table's <td> cells only.
    const inTable = (text) => screen.getAllByText(text).filter((el) => el.closest('td'));
    await waitFor(() => {
      expect(inTable('eth0').length).toBeGreaterThanOrEqual(1);
      expect(inTable('wlh0').length).toBeGreaterThanOrEqual(1);
      expect(screen.getAllByText('br-lan').length).toBeGreaterThanOrEqual(1);
    });
    // Type labels
    expect(inTable('Ethernet').length).toBe(1);
    expect(inTable('HaLow Mesh').length).toBe(1);
    expect(inTable('Bridge').length).toBe(1);
    // Status badges
    expect(inTable('Up').length).toBe(2);
    expect(inTable('Down').length).toBe(1);
    // IP addresses
    expect(inTable('192.168.1.1').length).toBe(1);
    expect(inTable('10.41.1.1').length).toBe(1);
  });
});

describe('TestNetworkInterfacesFallbacks', () => {
  it('shows fallback text for an unknown type, missing MAC, and zero MTU', async () => {
    mockListNetworkInterfaces.mockResolvedValue({
      interfaces: [
        { name: 'usb0', type: 42, status: 2, ipAddress: '10.0.0.9', macAddress: '', rxBytes: 0, txBytes: 0, mtu: 0 },
      ],
    });
    mockGetDHCPServerConfig.mockResolvedValue({ config: null });
    mockListActiveDHCPLeases.mockResolvedValue({ leases: [] });
    mockListStaticDHCPLeases.mockResolvedValue({ leases: [] });

    const { container } = render(<SettingsNetworkPage />);
    await waitFor(() => {
      expect(container.querySelector('.lat-table tbody tr')).toBeTruthy();
    });
    const cells = [...container.querySelector('.lat-table tbody tr').querySelectorAll('td')];
    // name(0), type(1), status(2), ip(3), mac(4), rx(5), tx(6), mtu(7)
    expect(cells[1].textContent).toBe('Unknown'); // IFACE_TYPE_LABELS fallback
    expect(cells[4].textContent).toBe('—'); // macAddress fallback
    expect(cells[7].textContent).toBe('—'); // mtu fallback
  });
});

describe('TestNetworkInterfacesEmpty', () => {
  it('shows no interfaces message', async () => {
    mockListNetworkInterfaces.mockResolvedValue({ interfaces: [] });
    mockGetDHCPServerConfig.mockResolvedValue({ config: null });
    mockListActiveDHCPLeases.mockResolvedValue({ leases: [] });
    mockListStaticDHCPLeases.mockResolvedValue({ leases: [] });

    render(<SettingsNetworkPage />);
    await waitFor(() => {
      expect(screen.getByText('No interfaces found.')).toBeTruthy();
    });
  });
});

describe('TestNetworkInterfacesError', () => {
  it('shows error when fetch fails', async () => {
    mockListNetworkInterfaces.mockRejectedValue(new Error('timeout'));
    mockGetDHCPServerConfig.mockRejectedValue(new Error('timeout'));
    mockListActiveDHCPLeases.mockRejectedValue(new Error('timeout'));
    mockListStaticDHCPLeases.mockRejectedValue(new Error('timeout'));

    render(<SettingsNetworkPage />);
    await waitFor(() => {
      expect(screen.getAllByText('timeout').length).toBeGreaterThanOrEqual(1);
    });
  });
});

describe('TestDHCPConfig', () => {
  it('renders DHCP server config', async () => {
    mockListNetworkInterfaces.mockResolvedValue({ interfaces: [] });
    mockGetDHCPServerConfig.mockResolvedValue(DHCP_CONFIG);
    mockListActiveDHCPLeases.mockResolvedValue(ACTIVE_LEASES);
    mockListStaticDHCPLeases.mockResolvedValue(STATIC_LEASES);

    render(<SettingsNetworkPage />);
    await waitFor(() => {
      expect(screen.getByText('DHCP Server')).toBeTruthy();
    });
    expect(screen.getByText(/192.168.1.100/)).toBeTruthy();
    expect(screen.getByText(/192.168.1.200/)).toBeTruthy();
    expect(screen.getByText('12h')).toBeTruthy();
  });
});

describe('TestDHCPActiveLeases', () => {
  it('expands active leases section', async () => {
    mockListNetworkInterfaces.mockResolvedValue({ interfaces: [] });
    mockGetDHCPServerConfig.mockResolvedValue(DHCP_CONFIG);
    mockListActiveDHCPLeases.mockResolvedValue(ACTIVE_LEASES);
    mockListStaticDHCPLeases.mockResolvedValue(STATIC_LEASES);

    render(<SettingsNetworkPage />);
    await waitFor(() => screen.getByText('DHCP Server'));

    // Click to expand active leases
    fireEvent.click(screen.getByText(/Active Leases \(2\)/));
    // DataTable renders every row as both a <td> and a mobile
    // .lat-card-head/.kv .v, so a bare screen.getByText(name) now matches
    // twice — same convention as TestNetworkInterfacesRender above.
    expect(screen.getAllByText('laptop').length).toBeGreaterThanOrEqual(1);
    expect(screen.getAllByText('phone').length).toBeGreaterThanOrEqual(1);
    expect(screen.getAllByText('192.168.1.101').length).toBeGreaterThanOrEqual(1);
  });
});

describe('TestDHCPStaticLeases', () => {
  it('expands static leases section', async () => {
    mockListNetworkInterfaces.mockResolvedValue({ interfaces: [] });
    mockGetDHCPServerConfig.mockResolvedValue(DHCP_CONFIG);
    mockListActiveDHCPLeases.mockResolvedValue(ACTIVE_LEASES);
    mockListStaticDHCPLeases.mockResolvedValue(STATIC_LEASES);

    render(<SettingsNetworkPage />);
    await waitFor(() => screen.getByText('DHCP Server'));

    fireEvent.click(screen.getByText(/Static Reservations \(1\)/));
    expect(screen.getAllByText('printer').length).toBeGreaterThanOrEqual(1);
    expect(screen.getAllByText('192.168.1.10').length).toBeGreaterThanOrEqual(1);
  });
});

describe('TestDHCPLeasesFallbacks', () => {
  it('shows dash fallbacks for a lease missing hostname, MAC, and IP', async () => {
    mockListNetworkInterfaces.mockResolvedValue({ interfaces: [] });
    mockGetDHCPServerConfig.mockResolvedValue(DHCP_CONFIG);
    // hostname: '' hits `r.hostname || '—'`; macAddress/ipAddress: null hit
    // `r.macAddress ?? '—'` / `r.ipAddress ?? '—'`. macAddress: null also
    // exercises the rowKey fallback (`r.macAddress ?? String(i)`) on both
    // the active and static lease tables (same LeasesTable component).
    mockListActiveDHCPLeases.mockResolvedValue({
      leases: [{ hostname: '', macAddress: null, ipAddress: null, expiresSeconds: 60 }],
    });
    mockListStaticDHCPLeases.mockResolvedValue({
      leases: [{ hostname: '', macAddress: null, ipAddress: null }],
    });

    render(<SettingsNetworkPage />);
    await waitFor(() => screen.getByText('DHCP Server'));

    fireEvent.click(screen.getByText(/Active Leases \(1\)/));
    const activeTable = screen.getByText(/Active Leases \(1\)/).closest('.disclosure').querySelector('.lat-table');
    const activeCells = [...activeTable.querySelectorAll('tbody tr td')].map((td) => td.textContent);
    expect(activeCells[0]).toBe('—'); // hostname
    expect(activeCells[1]).toBe('—'); // macAddress
    expect(activeCells[2]).toBe('—'); // ipAddress

    fireEvent.click(screen.getByText(/Static Reservations \(1\)/));
    const staticTable = screen.getByText(/Static Reservations \(1\)/).closest('.disclosure').querySelector('.lat-table');
    const staticCells = [...staticTable.querySelectorAll('tbody tr td')].map((td) => td.textContent);
    expect(staticCells[0]).toBe('—');
    expect(staticCells[1]).toBe('—');
    expect(staticCells[2]).toBe('—');
  });
});

describe('TestDHCPLeasesMobileCards', () => {
  it('renders active leases as both table rows and cards', async () => {
    mockListNetworkInterfaces.mockResolvedValue({ interfaces: [] });
    mockGetDHCPServerConfig.mockResolvedValue(DHCP_CONFIG);
    mockListActiveDHCPLeases.mockResolvedValue(ACTIVE_LEASES);
    mockListStaticDHCPLeases.mockResolvedValue(STATIC_LEASES);

    render(<SettingsNetworkPage />);
    await waitFor(() => screen.getByText('DHCP Server'));
    fireEvent.click(screen.getByText(/Active Leases \(2\)/));

    const tabular = screen.getByText(/Active Leases \(2\)/).closest('.disclosure').querySelector('.lat-tabular');
    expect(tabular).toBeTruthy();
    const tableRows = tabular.querySelectorAll('.lat-table tbody tr').length;
    expect(tableRows).toBe(2);
    expect(tabular.querySelectorAll('.lat-cardlist .lat-card')).toHaveLength(tableRows);
  });
});

describe('TestSettingsNetworkMobileCards', () => {
  it('renders interfaces as both table rows and cards', async () => {
    mockListNetworkInterfaces.mockResolvedValue({ interfaces: INTERFACES });
    mockGetDHCPServerConfig.mockResolvedValue(DHCP_CONFIG);
    mockListActiveDHCPLeases.mockResolvedValue(ACTIVE_LEASES);
    mockListStaticDHCPLeases.mockResolvedValue(STATIC_LEASES);

    const { container } = render(<SettingsNetworkPage />);
    await waitFor(() => {
      expect(container.querySelector('.lat-tabular')).toBeTruthy();
    });
    const tabular = container.querySelector('.lat-tabular');
    const tableRows = tabular.querySelectorAll('.lat-table tbody tr').length;
    expect(tableRows).toBeGreaterThan(0);
    expect(tabular.querySelectorAll('.lat-cardlist .lat-card')).toHaveLength(tableRows);
  });
});
