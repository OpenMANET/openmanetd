// =============================================================================
// GpsStatus.test.jsx — Tests for GPS / GNSS status and configuration page
// =============================================================================

import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, screen, waitFor, fireEvent, cleanup } from '@testing-library/react';

const { mockGetGNSSStatus, mockGetGNSSConfig, mockUpdateGNSSConfig } = vi.hoisted(() => ({
  mockGetGNSSStatus: vi.fn(),
  mockGetGNSSConfig: vi.fn(),
  mockUpdateGNSSConfig: vi.fn(),
}));
vi.mock('@connectrpc/connect', () => ({
  createClient: () => ({
    getGNSSStatus: mockGetGNSSStatus,
    getGNSSConfig: mockGetGNSSConfig,
    updateGNSSConfig: mockUpdateGNSSConfig,
  }),
}));
vi.mock('../../services/connectClient.js', () => ({ transport: {} }));
vi.mock('../../gen/openmanet/gnss/v1/gnss_service_pb.js', () => ({
  GNSSService: {},
}));

import GpsStatusPage from '../../pages/GpsStatus.jsx';
import { GNSSSource } from '../../gen/openmanet/gnss/v1/gnss_pb.js';

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  mockGetGNSSStatus.mockReset();
  mockGetGNSSConfig.mockReset();
  mockUpdateGNSSConfig.mockReset();
});

// ── Fixtures ────────────────────────────────────────────────────────────────

const CONFIG_DISABLED = {
  settings: { enableGps: false },
  outputProtocols: { sendAsNmea: false, sendAsCot: false, cotUid: '' },
};

const CONFIG_ENABLED = {
  settings: { enableGps: true },
  outputProtocols: { sendAsNmea: true, sendAsCot: true, cotUid: 'my-device-123' },
};

const STATUS_3D_FIX = {
  position: {
    fixType: 3,
    latitude: 34.0522,
    longitude: -118.2437,
    altitude: 71,
    speed: 0,
    heading: 245.0,
    pdop: 1.2,
    hdop: 0.9,
    lastUpdate: new Date('2026-04-01T09:42:15Z'),
  },
  satelliteStatus: {
    satellitesUsed: 8,
    satellitesInView: 12,
    satellites: [
      { prn: 2, elevation: 45, azimuth: 120, snr: 38, used: true },
      { prn: 5, elevation: 72, azimuth: 210, snr: 42, used: true },
      { prn: 7, elevation: 15, azimuth: 330, snr: 18, used: false },
    ],
  },
};

const STATUS_NO_FIX = {
  position: { fixType: 0, latitude: 0, longitude: 0, altitude: 0, speed: 0, heading: 0, pdop: 0, hdop: 0 },
  satelliteStatus: { satellitesUsed: 0, satellitesInView: 0, satellites: [] },
};

// ── Loading state ───────────────────────────────────────────────────────────

describe('TestGpsStatusLoading', () => {
  it('renders loading state while fetching config', () => {
    mockGetGNSSConfig.mockReturnValue(new Promise(() => {}));
    mockGetGNSSStatus.mockReturnValue(new Promise(() => {}));
    render(<GpsStatusPage />);
    expect(screen.getAllByText('Loading GNSS data...').length).toBeGreaterThan(0);
  });
});

// ── Error state ─────────────────────────────────────────────────────────────

describe('TestGpsStatusConfigError', () => {
  it('shows error when config fetch fails', async () => {
    mockGetGNSSConfig.mockRejectedValue(new Error('connection refused'));
    mockGetGNSSStatus.mockResolvedValue(STATUS_NO_FIX);
    render(<GpsStatusPage />);
    await waitFor(() => {
      expect(screen.getByText(/Failed to load GNSS config: connection refused/)).toBeTruthy();
    });
  });
});

// ── No GPS data ─────────────────────────────────────────────────────────────

describe('TestGpsStatusNoData', () => {
  it('renders page shell with no position data', async () => {
    mockGetGNSSConfig.mockResolvedValue(CONFIG_DISABLED);
    mockGetGNSSStatus.mockResolvedValue(null);
    render(<GpsStatusPage />);
    await waitFor(() => {
      expect(screen.getByRole('heading', { name: /GPS \/ GNSS/ })).toBeTruthy();
    });
    expect(screen.getByText('No position data')).toBeTruthy();
    expect(screen.getByText('No satellite data available.')).toBeTruthy();
  });
});

// ── No fix state ────────────────────────────────────────────────────────────

describe('TestGpsStatusNoFix', () => {
  it('shows No Fix big-num when fix type is 0', async () => {
    mockGetGNSSConfig.mockResolvedValue(CONFIG_DISABLED);
    mockGetGNSSStatus.mockResolvedValue(STATUS_NO_FIX);
    render(<GpsStatusPage />);
    await waitFor(() => {
      expect(screen.getAllByText('No Fix').length).toBeGreaterThan(0);
    });
    // Topbar subtitle should also show NO FIX with satellite count.
    expect(screen.getByText(/NO FIX · 0\/0 SATS/)).toBeTruthy();
  });
});

// ── 3D fix with full data ───────────────────────────────────────────────────

describe('TestGpsStatus3DFix', () => {
  it('displays position data for a 3D fix', async () => {
    mockGetGNSSConfig.mockResolvedValue(CONFIG_DISABLED);
    mockGetGNSSStatus.mockResolvedValue(STATUS_3D_FIX);
    render(<GpsStatusPage />);
    await waitFor(() => {
      expect(screen.getByText('3D')).toBeTruthy();
    });
    // Topbar subtitle
    expect(screen.getByText(/3D FIX · 8\/12 SATS/)).toBeTruthy();
    // Position values rendered as DMS
    expect(screen.getByText(/34° 03′ .*″ N/)).toBeTruthy();
    expect(screen.getByText(/118° 14′ .*″ W/)).toBeTruthy();
    expect(screen.getByText('71.0 m')).toBeTruthy();
    // Fix quality KV values
    expect(screen.getByText('FIXED')).toBeTruthy();
    expect(screen.getByText('1.2')).toBeTruthy();
    expect(screen.getByText('0.9')).toBeTruthy();
  });
});

// ── Globe panel ─────────────────────────────────────────────────────────────

describe('TestGpsStatusGlobePanel', () => {
  it('shows Globe panel with coordinate line', async () => {
    mockGetGNSSConfig.mockResolvedValue(CONFIG_DISABLED);
    mockGetGNSSStatus.mockResolvedValue(STATUS_3D_FIX);
    const { container } = render(<GpsStatusPage />);
    await waitFor(() => {
      expect(screen.getByRole('heading', { name: /Globe · WGS84/ })).toBeTruthy();
    });
    const coord = container.querySelector('.gps-globe-coord');
    expect(coord).toBeTruthy();
    expect(coord.textContent).toContain('34.0522');
    expect(coord.textContent).toContain('N');
    expect(coord.textContent).toContain('118.2437');
    expect(coord.textContent).toContain('W');
  });
});

// ── MGRS ────────────────────────────────────────────────────────────────────

describe('TestGpsStatusMGRS', () => {
  it('renders MGRS derived from lat/lon', async () => {
    mockGetGNSSConfig.mockResolvedValue(CONFIG_DISABLED);
    mockGetGNSSStatus.mockResolvedValue(STATUS_3D_FIX);
    render(<GpsStatusPage />);
    await waitFor(() => {
      expect(screen.getByText('MGRS')).toBeTruthy();
    });
    // LA area is in UTM zone 11S, expect "11S" prefix.
    expect(screen.getByText(/^11S /)).toBeTruthy();
  });
});

// ── Topbar chips ────────────────────────────────────────────────────────────

describe('TestGpsStatusTopbarChips', () => {
  it('renders HDOP / PDOP / GPGGA chips with values', async () => {
    mockGetGNSSConfig.mockResolvedValue(CONFIG_DISABLED);
    mockGetGNSSStatus.mockResolvedValue(STATUS_3D_FIX);
    render(<GpsStatusPage />);
    await waitFor(() => {
      expect(screen.getByText(/HDOP 0.9/)).toBeTruthy();
    });
    expect(screen.getByText(/PDOP 1.2/)).toBeTruthy();
    expect(screen.getByText(/GPGGA /)).toBeTruthy();
  });
});

// ── Satellite SNR table ─────────────────────────────────────────────────────

describe('TestGpsStatusSatellites', () => {
  it('renders satellite SNR table with PRN, constellation, elev, azim, snr, used', async () => {
    mockGetGNSSConfig.mockResolvedValue(CONFIG_DISABLED);
    mockGetGNSSStatus.mockResolvedValue(STATUS_3D_FIX);
    const { container } = render(<GpsStatusPage />);
    await waitFor(() => {
      expect(screen.getByRole('heading', { name: /Satellite SNR/ })).toBeTruthy();
    });
    const rows = container.querySelectorAll('.gps-panel-snr tbody tr');
    expect(rows.length).toBe(3);
    // Row 1 — PRN 2, GPS, 45°, 120°, 38, ✓
    const row1 = rows[0].querySelectorAll('td');
    expect(row1[0].textContent).toBe('2');
    expect(row1[1].textContent).toBe('GPS');
    expect(row1[2].textContent).toBe('45°');
    expect(row1[3].textContent).toBe('120°');
    expect(row1[4].textContent).toBe('38');
    expect(row1[5].textContent).toBe('✓');
    // Row 3 — PRN 7, not used
    const row3 = rows[2].querySelectorAll('td');
    expect(row3[0].textContent).toBe('7');
    expect(row3[4].textContent).toBe('18');
    expect(row3[5].textContent).toBe('✗');
  });

  it('filters to used-only when USED button clicked', async () => {
    mockGetGNSSConfig.mockResolvedValue(CONFIG_DISABLED);
    mockGetGNSSStatus.mockResolvedValue(STATUS_3D_FIX);
    const { container } = render(<GpsStatusPage />);
    await waitFor(() => {
      expect(container.querySelectorAll('.gps-panel-snr tbody tr').length).toBe(3);
    });
    fireEvent.click(screen.getByText('USED'));
    // Only 2 used satellites remain.
    expect(container.querySelectorAll('.gps-panel-snr tbody tr').length).toBe(2);
  });
});

// ── Output protocols panel ──────────────────────────────────────────────────

describe('TestGpsStatusOutputDisabled', () => {
  it('renders output protocol toggles and save button', async () => {
    mockGetGNSSConfig.mockResolvedValue(CONFIG_DISABLED);
    mockGetGNSSStatus.mockResolvedValue(STATUS_NO_FIX);
    render(<GpsStatusPage />);
    await waitFor(() => {
      expect(screen.getByRole('heading', { name: /Output Protocols/ })).toBeTruthy();
    });
    expect(screen.getByText('GPS Enabled')).toBeTruthy();
    expect(screen.getByText('NMEA Out')).toBeTruthy();
    expect(screen.getByText('CoT Out')).toBeTruthy();
    expect(screen.getByText('SAVE GNSS CONFIG')).toBeTruthy();
  });
});

describe('TestGpsStatusOutputEnabled', () => {
  it('shows config values when GPS is enabled', async () => {
    mockGetGNSSConfig.mockResolvedValue(CONFIG_ENABLED);
    mockGetGNSSStatus.mockResolvedValue(STATUS_3D_FIX);
    render(<GpsStatusPage />);
    await waitFor(() => {
      expect(screen.getByRole('heading', { name: /Output Protocols/ })).toBeTruthy();
    });
    const cotInput = screen.getByDisplayValue('my-device-123');
    expect(cotInput).toBeTruthy();
  });
});

// ── Toggle interaction ──────────────────────────────────────────────────────

describe('TestGpsStatusToggleGPS', () => {
  it('clicking a toggle track does not trigger save by itself', async () => {
    mockGetGNSSConfig.mockResolvedValue(CONFIG_DISABLED);
    mockGetGNSSStatus.mockResolvedValue(STATUS_NO_FIX);
    const { container } = render(<GpsStatusPage />);
    await waitFor(() => screen.getByText('GPS Enabled'));

    const track = container.querySelector('.lat-toggle .track');
    expect(track).toBeTruthy();
    fireEvent.click(track);

    expect(mockUpdateGNSSConfig).not.toHaveBeenCalled();
  });
});

// ── Save config ─────────────────────────────────────────────────────────────

describe('TestGpsStatusSaveSuccess', () => {
  it('saves config and shows success message', async () => {
    mockGetGNSSConfig.mockResolvedValue(CONFIG_DISABLED);
    mockGetGNSSStatus.mockResolvedValue(STATUS_NO_FIX);
    mockUpdateGNSSConfig.mockResolvedValue({ success: true, message: 'GNSS configuration updated successfully.' });

    render(<GpsStatusPage />);
    await waitFor(() => screen.getByText('SAVE GNSS CONFIG'));

    fireEvent.click(screen.getByText('SAVE GNSS CONFIG'));

    await waitFor(() => {
      expect(screen.getByText('GNSS configuration updated successfully.')).toBeTruthy();
    });
    expect(mockUpdateGNSSConfig).toHaveBeenCalledWith(
      expect.objectContaining({
        settings: CONFIG_DISABLED.settings,
        outputProtocols: CONFIG_DISABLED.outputProtocols,
      }),
    );
  });
});

describe('TestGpsStatusSaveFailure', () => {
  it('shows error on save failure', async () => {
    mockGetGNSSConfig.mockResolvedValue(CONFIG_DISABLED);
    mockGetGNSSStatus.mockResolvedValue(STATUS_NO_FIX);
    mockUpdateGNSSConfig.mockRejectedValue(new Error('permission denied'));

    render(<GpsStatusPage />);
    await waitFor(() => screen.getByText('SAVE GNSS CONFIG'));

    fireEvent.click(screen.getByText('SAVE GNSS CONFIG'));

    await waitFor(() => {
      expect(screen.getByText(/Failed to save GNSS config: permission denied/)).toBeTruthy();
    });
  });

  it('shows error when response indicates failure', async () => {
    mockGetGNSSConfig.mockResolvedValue(CONFIG_DISABLED);
    mockGetGNSSStatus.mockResolvedValue(STATUS_NO_FIX);
    mockUpdateGNSSConfig.mockResolvedValue({ success: false, message: 'config file locked' });

    render(<GpsStatusPage />);
    await waitFor(() => screen.getByText('SAVE GNSS CONFIG'));

    fireEvent.click(screen.getByText('SAVE GNSS CONFIG'));

    await waitFor(() => {
      expect(screen.getByText(/Failed to save GNSS config/)).toBeTruthy();
    });
  });
});

// ── CoT UID input ───────────────────────────────────────────────────────────

describe('TestGpsStatusCoTUIDInput', () => {
  it('allows editing CoT UID and includes it in save', async () => {
    mockGetGNSSConfig.mockResolvedValue(CONFIG_DISABLED);
    mockGetGNSSStatus.mockResolvedValue(STATUS_NO_FIX);
    mockUpdateGNSSConfig.mockResolvedValue({ success: true, message: 'saved' });

    const { container } = render(<GpsStatusPage />);
    await waitFor(() => screen.getByText('SAVE GNSS CONFIG'));

    const cotInput = container.querySelector('.gps-panel-output .lat-input');
    expect(cotInput).toBeTruthy();
    fireEvent.change(cotInput, { target: { value: 'test-uid-456' } });
    expect(cotInput.value).toBe('test-uid-456');

    fireEvent.click(screen.getByText('SAVE GNSS CONFIG'));

    await waitFor(() => {
      expect(mockUpdateGNSSConfig).toHaveBeenCalledWith(
        expect.objectContaining({
          outputProtocols: expect.objectContaining({ cotUid: 'test-uid-456' }),
        }),
      );
    });
  });
});

// ── GNSS source selection ───────────────────────────────────────────────────

describe('TestGpsStatusGNSSSourceSelect', () => {
  it('defaults to internal and hides the external-CoT hint', async () => {
    mockGetGNSSConfig.mockResolvedValue(CONFIG_DISABLED);
    mockGetGNSSStatus.mockResolvedValue(STATUS_NO_FIX);

    render(<GpsStatusPage />);
    await waitFor(() => screen.getByText('SAVE GNSS CONFIG'));

    expect(screen.getByLabelText('GNSS position source').textContent).toContain('INTERNAL');
    expect(screen.queryByText(/No sky plot \/ satellite data is available/)).toBeNull();
  });

  it('selecting external CoT shows the hint and saves the new source', async () => {
    mockGetGNSSConfig.mockResolvedValue(CONFIG_DISABLED);
    mockGetGNSSStatus.mockResolvedValue(STATUS_NO_FIX);
    mockUpdateGNSSConfig.mockResolvedValue({ success: true, message: 'saved' });

    render(<GpsStatusPage />);
    await waitFor(() => screen.getByText('SAVE GNSS CONFIG'));

    fireEvent.click(screen.getByLabelText('GNSS position source'));
    fireEvent.click(screen.getByRole('option', { name: /EXTERNAL/ }));

    await waitFor(() => {
      expect(screen.getByText(/No sky plot \/ satellite data is available/)).toBeTruthy();
    });

    fireEvent.click(screen.getByText('SAVE GNSS CONFIG'));

    await waitFor(() => {
      expect(mockUpdateGNSSConfig).toHaveBeenCalledWith(
        expect.objectContaining({
          settings: expect.objectContaining({ source: GNSSSource.GNSS_SOURCE_EXTERNAL_COT }),
        }),
      );
    });
  });

  it('renders the hint when config already reports external CoT', async () => {
    mockGetGNSSConfig.mockResolvedValue({
      ...CONFIG_ENABLED,
      settings: { ...CONFIG_ENABLED.settings, source: GNSSSource.GNSS_SOURCE_EXTERNAL_COT },
    });
    mockGetGNSSStatus.mockResolvedValue(STATUS_3D_FIX);

    render(<GpsStatusPage />);
    await waitFor(() => screen.getByText('SAVE GNSS CONFIG'));

    expect(screen.getByLabelText('GNSS position source').textContent).toContain('EXTERNAL');
    expect(screen.getByText(/No sky plot \/ satellite data is available/)).toBeTruthy();
  });
});

// ── 2D fix ──────────────────────────────────────────────────────────────────

describe('TestGpsStatus2DFix', () => {
  it('displays 2D big-num for fix type 2', async () => {
    const status2D = {
      position: {
        fixType: 2, latitude: 51.5074, longitude: -0.1278,
        altitude: 0, speed: 3.0, heading: 90.0, pdop: 0, hdop: 1.5,
      },
      satelliteStatus: { satellitesUsed: 4, satellitesInView: 6, satellites: [] },
    };
    mockGetGNSSConfig.mockResolvedValue(CONFIG_DISABLED);
    mockGetGNSSStatus.mockResolvedValue(status2D);
    render(<GpsStatusPage />);
    await waitFor(() => {
      expect(screen.getByText('2D')).toBeTruthy();
    });
    expect(screen.getByText(/2D FIX · 4\/6 SATS/)).toBeTruthy();
    expect(screen.getByText(/51° 30′ .*″ N/)).toBeTruthy();
  });
});

// ── Polling ─────────────────────────────────────────────────────────────────

// ── Mobile card rendering (DataTable) ───────────────────────────────────────

describe('TestGpsSnrMobileCards', () => {
  it('renders satellite rows as both table rows and cards', async () => {
    mockGetGNSSConfig.mockResolvedValue(CONFIG_DISABLED);
    mockGetGNSSStatus.mockResolvedValue(STATUS_3D_FIX);
    const { container } = render(<GpsStatusPage />);
    await waitFor(() => {
      expect(container.querySelector('.gps-panel-snr .lat-tabular')).toBeTruthy();
    });
    const tabular = container.querySelector('.gps-panel-snr .lat-tabular');
    const tableRows = tabular.querySelectorAll('.lat-table tbody tr').length;
    const cards = tabular.querySelectorAll('.lat-cardlist .lat-card').length;
    expect(tableRows).toBeGreaterThan(0);
    expect(cards).toBe(tableRows);
  });

  it('heads each satellite card with its PRN', async () => {
    mockGetGNSSConfig.mockResolvedValue(CONFIG_DISABLED);
    mockGetGNSSStatus.mockResolvedValue(STATUS_3D_FIX);
    const { container } = render(<GpsStatusPage />);
    await waitFor(() => {
      expect(container.querySelector('.lat-card-head')).toBeTruthy();
    });
    const head = container.querySelector('.gps-panel-snr .lat-card-head');
    expect(head.textContent).toMatch(/^\d+$/);
  });
});

describe('TestGpsStatusPolling', () => {
  it('polls for status updates on interval', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    mockGetGNSSConfig.mockResolvedValue(CONFIG_DISABLED);
    mockGetGNSSStatus.mockResolvedValue(STATUS_NO_FIX);

    render(<GpsStatusPage />);
    await waitFor(() => screen.getByRole('heading', { name: /GPS \/ GNSS/ }));

    const initialCalls = mockGetGNSSStatus.mock.calls.length;

    vi.advanceTimersByTime(2000);
    await waitFor(() => {
      expect(mockGetGNSSStatus.mock.calls.length).toBeGreaterThan(initialCalls);
    });

    vi.useRealTimers();
  });
});
