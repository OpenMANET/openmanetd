// =============================================================================
// BLOS.test.jsx — Tests for BLOS (VPN) page
// =============================================================================
//
// The page reads status via the `useBLOSStatus` hook, which is backed by a
// module-scoped pollStore singleton (see services/pollStore.js). That
// singleton intentionally retains its snapshot across mount/unmount so
// navigation back to /blos shows cached data instantly — but the same
// behavior makes per-test isolation fragile, because the snapshot leaks
// across tests within this file and races against the first poll cycle of
// each subsequent render. We mock the hook directly so the page sees a
// synchronous, deterministic status; pollStore has its own dedicated test
// in pollStore.test.js.

import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, screen, fireEvent, cleanup } from '@testing-library/react';

const { mockBLOSData, mockUpdateBLOSConfig, mockRefreshBLOSStatus } = vi.hoisted(() => ({
  mockBLOSData: { current: null },
  mockUpdateBLOSConfig: vi.fn(),
  mockRefreshBLOSStatus: vi.fn(),
}));

vi.mock('../../hooks/useBLOSStatus.js', () => ({
  useBLOSStatus: () => mockBLOSData.current,
  refreshBLOSStatus: mockRefreshBLOSStatus,
}));

vi.mock('@connectrpc/connect', () => ({
  createClient: () => ({
    updateBLOSConfig: mockUpdateBLOSConfig,
    // Async-iterable stub for streamBLOSEvents — fires when blosEnabled=true.
    streamBLOSEvents: () => ({
      async *[Symbol.asyncIterator]() {},
    }),
  }),
}));
vi.mock('../../services/connectClient.js', () => ({ transport: {} }));

import BLOSPage from '../../pages/BLOS.jsx';

function setSnapshot(snapshot) {
  mockBLOSData.current = snapshot;
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  mockBLOSData.current = null;
  mockUpdateBLOSConfig.mockReset();
  mockRefreshBLOSStatus.mockReset();
});

describe('TestBLOSLoading', () => {
  it('renders loading state', () => {
    // Hook returns null before the first poll completes.
    setSnapshot(null);
    render(<BLOSPage />);
    expect(screen.getByText('Loading BLOS status...')).toBeTruthy();
  });
});

describe('TestBLOSStatusEnabled', () => {
  it('shows enabled status', () => {
    setSnapshot({
      status: { blosEnabled: true, message: 'connected to headscale' },
      peers: [],
      error: null,
    });
    render(<BLOSPage />);
    expect(screen.getByText('Enabled')).toBeTruthy();
    expect(screen.getByText(/connected to headscale/)).toBeTruthy();
  });
});

describe('TestBLOSStatusDisabled', () => {
  it('shows disabled status', () => {
    setSnapshot({ status: { blosEnabled: false }, peers: [], error: null });
    render(<BLOSPage />);
    expect(screen.getByText('Disabled')).toBeTruthy();
    expect(screen.getByText('Off')).toBeTruthy();
  });
});

describe('TestBLOSStatusError', () => {
  it('shows error when status fetch fails', () => {
    setSnapshot({ status: null, peers: [], error: 'connection refused' });
    render(<BLOSPage />);
    expect(screen.getByText('connection refused')).toBeTruthy();
  });
});

describe('TestBLOSToggle', () => {
  it('toggles enable switch', () => {
    setSnapshot({ status: { blosEnabled: false }, peers: [], error: null });
    const { container } = render(<BLOSPage />);
    expect(screen.getByText('Off')).toBeTruthy();

    const toggle = container.querySelector('.lat-toggle');
    fireEvent.click(toggle);
    expect(screen.getByText('On')).toBeTruthy();
  });
});

describe('TestBLOSUpdateSuccess', () => {
  it('shows success message after save', async () => {
    setSnapshot({ status: { blosEnabled: false }, peers: [], error: null });
    mockUpdateBLOSConfig.mockResolvedValue({ success: true, message: 'BLOS enabled' });

    const { container } = render(<BLOSPage />);

    const toggle = container.querySelector('.lat-toggle');
    fireEvent.click(toggle);

    const authInput = screen.getByPlaceholderText('Paste auth key');
    fireEvent.change(authInput, { target: { value: 'tskey-auth-abc123' } });

    fireEvent.click(screen.getByText('Update BLOS Config'));

    await screen.findByText('BLOS enabled');
    expect(mockUpdateBLOSConfig).toHaveBeenCalledWith(
      expect.objectContaining({ enableBlos: true, authKey: 'tskey-auth-abc123' }),
    );
  });
});

describe('TestBLOSUpdateWithLoginServer', () => {
  it('sends login server URL when provided', async () => {
    setSnapshot({ status: { blosEnabled: true }, peers: [], error: null });
    mockUpdateBLOSConfig.mockResolvedValue({ success: true });

    render(<BLOSPage />);

    fireEvent.change(screen.getByPlaceholderText('Paste auth key'), {
      target: { value: 'key123' },
    });
    fireEvent.change(screen.getByPlaceholderText('https://headscale.example.com'), {
      target: { value: 'https://hs.local' },
    });
    fireEvent.click(screen.getByText('Update BLOS Config'));

    await vi.waitFor(() => {
      expect(mockUpdateBLOSConfig).toHaveBeenCalledWith(
        expect.objectContaining({ loginServerUrl: 'https://hs.local' }),
      );
    });
  });
});

describe('TestBLOSUpdateFailure', () => {
  it('shows error on update failure', async () => {
    setSnapshot({ status: { blosEnabled: false }, peers: [], error: null });
    mockUpdateBLOSConfig.mockRejectedValue(new Error('unauthorized'));

    render(<BLOSPage />);

    fireEvent.click(screen.getByText('Update BLOS Config'));

    await screen.findByText(/Failed to update BLOS/);
  });

  it('shows error when response indicates failure', async () => {
    setSnapshot({ status: { blosEnabled: false }, peers: [], error: null });
    mockUpdateBLOSConfig.mockResolvedValue({ success: false, message: 'invalid auth key' });

    render(<BLOSPage />);

    fireEvent.click(screen.getByText('Update BLOS Config'));

    await screen.findByText(/Failed to update BLOS/);
  });
});

describe('TestBLOSUpdateAuthKeyImpliesEnable', () => {
  it('requests enable when an auth key is entered with the toggle off', async () => {
    setSnapshot({ status: { blosEnabled: false }, peers: [], error: null });
    mockUpdateBLOSConfig.mockResolvedValue({ success: true, message: 'BLOS enabled' });

    render(<BLOSPage />);
    expect(screen.getByText('Off')).toBeTruthy();

    fireEvent.change(screen.getByPlaceholderText('Paste auth key'), {
      target: { value: 'tskey-auth-xyz' },
    });
    fireEvent.click(screen.getByText('Update BLOS Config'));

    await screen.findByText('BLOS enabled');
    expect(mockUpdateBLOSConfig).toHaveBeenCalledWith(
      expect.objectContaining({ enableBlos: true, authKey: 'tskey-auth-xyz' }),
    );
    // Toggle mirrors the requested state without waiting for the next poll.
    expect(screen.getByText('On')).toBeTruthy();
  });

  it('ignores a whitespace-only auth key and keeps the toggle state', async () => {
    setSnapshot({ status: { blosEnabled: false }, peers: [], error: null });
    mockUpdateBLOSConfig.mockResolvedValue({ success: true, message: 'BLOS disabled' });

    render(<BLOSPage />);

    fireEvent.change(screen.getByPlaceholderText('Paste auth key'), {
      target: { value: '   ' },
    });
    fireEvent.click(screen.getByText('Update BLOS Config'));

    await screen.findByText('BLOS disabled');
    expect(mockUpdateBLOSConfig).toHaveBeenCalledWith(
      expect.objectContaining({ enableBlos: false }),
    );
    expect(screen.getByText('Off')).toBeTruthy();
  });

  it('still sends disable when the toggle is off and no key is entered', async () => {
    setSnapshot({ status: { blosEnabled: true }, peers: [], error: null });
    mockUpdateBLOSConfig.mockResolvedValue({ success: true, message: 'BLOS disabled' });

    const { container } = render(<BLOSPage />);
    fireEvent.click(container.querySelector('.lat-toggle'));
    expect(screen.getByText('Off')).toBeTruthy();

    fireEvent.click(screen.getByText('Update BLOS Config'));

    await screen.findByText('BLOS disabled');
    expect(mockUpdateBLOSConfig).toHaveBeenCalledWith(
      expect.objectContaining({ enableBlos: false }),
    );
  });
});
