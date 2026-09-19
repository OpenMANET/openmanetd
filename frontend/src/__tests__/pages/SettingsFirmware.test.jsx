// =============================================================================
// SettingsFirmware.test.jsx — page-level tests for the Firmware settings tab
// =============================================================================

import React from 'react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, waitFor, cleanup, within } from '@testing-library/react';

import { Phase } from '../../gen/openmanet/sysupgrade/v1/sysupgrade_pb.js';

// ---- mock the sysupgrade API module ---------------------------------------
const apiState = {
  systemInfo: null,
  systemInfoErr: null,
  updates: [],
  fetchedAt: null,
  release: null,
  releaseErr: null,
  startCalls: [],
  startErr: null,
  cancelCalls: 0,
  cancelErr: null,
  initialStatus: null,
  streamFn: null, // override to drive the stream from a test
  staged: null,
  stagedErr: null,
  discardCalls: 0,
  discardErr: null,
  startLocalCalls: [],
  startLocalErr: null,
  uploadCalls: [],
  uploadResult: null,
  uploadErr: null,
  resetCap: null,
  resetCapErr: null,
  resetCalls: [],
  resetErr: null,
};

function defaultStream() {
  return {
    [Symbol.asyncIterator]() {
      return {
        next: () => new Promise(() => {}),
        return: () => Promise.resolve({ value: undefined, done: true }),
      };
    },
  };
}

vi.mock('../../services/sysupgradeApi.js', () => ({
  fetchSystemInfo: vi.fn(async () => {
    if (apiState.systemInfoErr) throw apiState.systemInfoErr;
    return apiState.systemInfo;
  }),
  listAvailableUpdates: vi.fn(async () => ({
    updates: apiState.updates,
    fetchedAt: apiState.fetchedAt,
  })),
  getReleaseDetail: vi.fn(async () => {
    if (apiState.releaseErr) throw apiState.releaseErr;
    return apiState.release;
  }),
  startUpgrade: vi.fn(async (req) => {
    apiState.startCalls.push(req);
    if (apiState.startErr) throw apiState.startErr;
  }),
  cancelUpgrade: vi.fn(async () => {
    apiState.cancelCalls += 1;
    if (apiState.cancelErr) throw apiState.cancelErr;
  }),
  getUpgradeStatus: vi.fn(async () => apiState.initialStatus),
  streamUpgradeProgress: vi.fn(() => (apiState.streamFn ?? defaultStream)()),
  getStagedImage: vi.fn(async () => {
    if (apiState.stagedErr) throw apiState.stagedErr;
    return apiState.staged;
  }),
  discardStagedImage: vi.fn(async () => {
    apiState.discardCalls += 1;
    if (apiState.discardErr) throw apiState.discardErr;
    apiState.staged = null;
  }),
  startLocalUpgrade: vi.fn(async (req) => {
    apiState.startLocalCalls.push(req);
    if (apiState.startLocalErr) throw apiState.startLocalErr;
  }),
  uploadFirmware: vi.fn(async (file, opts) => {
    apiState.uploadCalls.push({ file, opts });
    if (apiState.uploadErr) throw apiState.uploadErr;
    return apiState.uploadResult;
  }),
  fetchFactoryResetCapability: vi.fn(async () => {
    if (apiState.resetCapErr) throw apiState.resetCapErr;
    return apiState.resetCap;
  }),
  performFactoryReset: vi.fn(async (req) => {
    apiState.resetCalls.push(req);
    if (apiState.resetErr) throw apiState.resetErr;
  }),
}));

// Imported AFTER the mock is registered.
import SettingsFirmware from '../../pages/SettingsFirmware.jsx';

function resetApiState() {
  apiState.systemInfo = null;
  apiState.systemInfoErr = null;
  apiState.updates = [];
  apiState.fetchedAt = null;
  apiState.release = null;
  apiState.releaseErr = null;
  apiState.startCalls = [];
  apiState.startErr = null;
  apiState.cancelCalls = 0;
  apiState.cancelErr = null;
  apiState.initialStatus = null;
  apiState.streamFn = null;
  apiState.staged = null;
  apiState.stagedErr = null;
  apiState.discardCalls = 0;
  apiState.discardErr = null;
  apiState.startLocalCalls = [];
  apiState.startLocalErr = null;
  apiState.uploadCalls = [];
  apiState.uploadResult = null;
  apiState.uploadErr = null;
  apiState.resetCap = null;
  apiState.resetCapErr = null;
  apiState.resetCalls = [];
  apiState.resetErr = null;
}

const capableInfo = {
  hostname: 'test-1',
  distribution: 'OpenMANET',
  release: '24.10',
  target: 'bcm27xx/bcm2711',
  boardName: 'rpi-4b',
  model: 'RPI 4B',
  openmanetVersion: '1.7.0',
  kernel: '6.6.102',
  architecture: 'aarch64',
  buildDate: '2025-06-23',
  sysupgradeCapable: true,
  sysupgradeCapableReason: '',
  rootfsType: 'squashfs',
};

const incapableInfo = {
  ...capableInfo,
  hostname: 'dev',
  sysupgradeCapable: false,
  sysupgradeCapableReason: 'no /sbin/sysupgrade',
  rootfsType: 'overlay',
};

const sampleUpdate = {
  release: {
    tag: 'v1.9.0',
    name: 'OpenMANET 1.9.0',
    body: '## Changes\n- one\n- two\n',
    publishedAt: new Date('2026-04-22T12:00:00Z'),
    prerelease: false,
    version: '1.9.0',
    assets: [],
  },
  matchedAsset: {
    name: 'openmanet-1.9.0-sysupgrade.img.gz',
    sizeBytes: 52_400_000,
    downloadUrl: 'https://example.com/v1.9.0',
  },
  newerThanCurrent: true,
};

describe('SettingsFirmware', () => {
  beforeEach(() => {
    resetApiState();
  });

  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it('renders system info from the API and the capable chip', async () => {
    apiState.systemInfo = capableInfo;
    apiState.updates = [];
    apiState.fetchedAt = new Date('2026-04-25T12:00:00Z');

    render(<SettingsFirmware />);

    expect(await screen.findByText('test-1')).toBeInTheDocument();
    expect(await screen.findByText('OpenMANET 24.10')).toBeInTheDocument();
    expect(screen.getByText(/Sysupgrade Capable/i)).toBeInTheDocument();
    expect(screen.getByText('1.7.0')).toBeInTheDocument();
  });

  it('shows the not-capable alert and hides the updates panel when the device is incapable', async () => {
    apiState.systemInfo = incapableInfo;

    render(<SettingsFirmware />);

    expect(await screen.findByText(/Not Sysupgrade Capable/i)).toBeInTheDocument();
    expect(screen.getAllByText(/no \/sbin\/sysupgrade/i).length).toBeGreaterThan(0);
    expect(screen.queryByRole('button', { name: /check for updates/i })).not.toBeInTheDocument();
  });

  it('renders the available updates table when capable', async () => {
    apiState.systemInfo = capableInfo;
    apiState.updates = [sampleUpdate];
    apiState.fetchedAt = new Date('2026-04-25T12:00:00Z');

    render(<SettingsFirmware />);

    // DataTable renders every row as both a <td> and a mobile
    // .lat-card-head/.kv .v, so a bare screen.getByText(name) now matches
    // twice. Scope to the table's <td> cells only.
    await screen.findAllByText('v1.9.0');
    expect(screen.getAllByText('v1.9.0').find((el) => el.tagName === 'TD')).toBeTruthy();
    expect(
      screen.getAllByText('openmanet-1.9.0-sysupgrade.img.gz').find((el) => el.tagName === 'TD'),
    ).toBeTruthy();
    // matchedAsset chip says "1 newer"
    expect(screen.getByText(/1 newer/i)).toBeInTheDocument();
  });

  it('opens the confirm card when Install is clicked', async () => {
    apiState.systemInfo = capableInfo;
    apiState.updates = [sampleUpdate];
    apiState.release = {
      tag: 'v1.9.0',
      body: '# Release v1.9.0\n\nGreat new features.',
      assets: [],
    };

    const { container } = render(<SettingsFirmware />);

    // DataTable also renders an Install button inside the mobile card, so
    // scope to the table's copy to avoid an ambiguous match.
    await waitFor(() => {
      expect(container.querySelector('table.lat-table')).toBeTruthy();
    });
    const table = container.querySelector('table.lat-table');
    fireEvent.click(within(table).getByRole('button', { name: /^install$/i }));

    // Confirm card content
    expect(await screen.findByRole('button', { name: /install — device will reboot/i })).toBeInTheDocument();
    const selectedChip = screen.getAllByText(/Selected/i).find((el) => el.closest('td'));
    expect(selectedChip).toBeTruthy();
    // Release notes loaded via getReleaseDetail
    await waitFor(() => {
      expect(screen.getByText('Release v1.9.0')).toBeInTheDocument();
    });
  });

  it('calls startUpgrade with the chosen options', async () => {
    apiState.systemInfo = capableInfo;
    apiState.updates = [sampleUpdate];
    apiState.release = { tag: 'v1.9.0', body: 'notes', assets: [] };

    const { container } = render(<SettingsFirmware />);

    // DataTable also renders an Install button inside the mobile card, so
    // scope to the table's copy to avoid an ambiguous match.
    await waitFor(() => {
      expect(container.querySelector('table.lat-table')).toBeTruthy();
    });
    fireEvent.click(within(container.querySelector('table.lat-table')).getByRole('button', { name: /^install$/i }));

    const testOnly = await screen.findByLabelText(/Test only/i);
    fireEvent.click(testOnly);

    fireEvent.click(screen.getByRole('button', { name: /install — device will reboot/i }));

    await waitFor(() => {
      expect(apiState.startCalls).toHaveLength(1);
    });
    expect(apiState.startCalls[0].releaseTag).toBe('v1.9.0');
    expect(apiState.startCalls[0].assetName).toBe('openmanet-1.9.0-sysupgrade.img.gz');
    expect(apiState.startCalls[0].options.testOnly).toBe(true);
  });

  it('disables the confirm button when test-only and force conflict', async () => {
    apiState.systemInfo = capableInfo;
    apiState.updates = [sampleUpdate];
    apiState.release = { tag: 'v1.9.0', body: 'notes', assets: [] };

    const { container } = render(<SettingsFirmware />);

    // DataTable also renders an Install button inside the mobile card, so
    // scope to the table's copy to avoid an ambiguous match.
    await waitFor(() => {
      expect(container.querySelector('table.lat-table')).toBeTruthy();
    });
    fireEvent.click(within(container.querySelector('table.lat-table')).getByRole('button', { name: /^install$/i }));

    fireEvent.click(await screen.findByLabelText(/Test only/i));
    fireEvent.click(screen.getByLabelText(/^Force/i));

    const confirm = screen.getByRole('button', { name: /install — device will reboot/i });
    expect(confirm).toBeDisabled();
    expect(screen.getByText(/mutually exclusive/i)).toBeInTheDocument();
  });

  it('shows a downloading progress bar from the streaming RPC', async () => {
    apiState.systemInfo = capableInfo;
    apiState.updates = [sampleUpdate];

    apiState.initialStatus = {
      phase: Phase.DOWNLOADING,
      percent: 42,
      bytesDone: 1000,
      bytesTotal: 2000,
      releaseTag: 'v1.9.0',
      assetName: sampleUpdate.matchedAsset.name,
    };

    render(<SettingsFirmware />);

    expect(await screen.findByText(/Phase: Downloading/i)).toBeInTheDocument();
    expect(screen.getByText('42%')).toBeInTheDocument();
    expect(screen.getByText(/Upgrade in progress/i)).toBeInTheDocument();
  });

  it('shows the rebooting alert when phase=UPGRADING', async () => {
    apiState.systemInfo = capableInfo;
    apiState.initialStatus = {
      phase: Phase.UPGRADING,
      percent: 100,
      bytesDone: 0,
      bytesTotal: 0,
      releaseTag: 'v1.9.0',
      childPid: 28473,
    };

    render(<SettingsFirmware />);

    expect(await screen.findByText(/Phase: Upgrading/i)).toBeInTheDocument();
    expect(screen.getByText(/Sysupgrade is running/i)).toBeInTheDocument();
    expect(screen.getByText('28473')).toBeInTheDocument();
  });

  it('shows the failure alert when phase=FAILED', async () => {
    apiState.systemInfo = capableInfo;
    apiState.initialStatus = {
      phase: Phase.FAILED,
      percent: 0,
      bytesDone: 0,
      bytesTotal: 0,
      error: 'sha256 mismatch',
    };

    render(<SettingsFirmware />);

    expect(await screen.findByText(/Phase: Failed/i)).toBeInTheDocument();
    expect(screen.getByText(/sha256 mismatch/i)).toBeInTheDocument();
  });

  it('cancel upgrade triggers cancelUpgrade RPC', async () => {
    apiState.systemInfo = capableInfo;
    apiState.initialStatus = {
      phase: Phase.DOWNLOADING,
      percent: 30,
      bytesDone: 100,
      bytesTotal: 200,
      releaseTag: 'v1.9.0',
    };

    render(<SettingsFirmware />);

    const cancelBtn = await screen.findByRole('button', { name: /cancel upgrade/i });
    fireEvent.click(cancelBtn);

    await waitFor(() => {
      expect(apiState.cancelCalls).toBe(1);
    });
  });

  it('check-for-updates calls listAvailableUpdates with forceRefresh=true', async () => {
    apiState.systemInfo = capableInfo;
    apiState.updates = [];

    const { listAvailableUpdates } = await import('../../services/sysupgradeApi.js');

    render(<SettingsFirmware />);

    await screen.findByRole('button', { name: /check for updates/i });
    listAvailableUpdates.mockClear();

    fireEvent.click(screen.getByRole('button', { name: /check for updates/i }));

    await waitFor(() => {
      expect(listAvailableUpdates).toHaveBeenCalledWith(
        expect.objectContaining({ forceRefresh: true }),
      );
    });
  });

  // ---- Local Image (upload) panel -----------------------------------------

  const stagedOK = {
    filename: 'openmanet-bcm27xx-bcm2711-custom.img.gz',
    sizeBytes: 12_345_678,
    sha256: 'd34db33fcafe',
    uploadedAt: new Date('2026-04-26T12:00:00Z'),
    metadataPresent: true,
    compatVersion: '1.0',
    compatMessage: '',
    supportedDevices: ['raspberrypi,4-model-b', 'brcm,bcm2711'],
    deviceCompat: 'raspberrypi,4-model-b',
    imageCompatible: true,
    preflightOk: true,
    preflightError: '',
  };

  const stagedFailed = {
    ...stagedOK,
    imageCompatible: false,
    preflightOk: false,
    preflightError: 'image bad magic',
  };

  it('renders the empty Local Image panel when no upload is staged', async () => {
    apiState.systemInfo = capableInfo;

    render(<SettingsFirmware />);

    expect(await screen.findByRole('heading', { name: /Local Image/i })).toBeInTheDocument();
    expect(screen.getByText(/No image staged/i)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Choose File/i })).toBeInTheDocument();
  });

  it('renders staged image metadata and the install button', async () => {
    apiState.systemInfo = capableInfo;
    apiState.staged = stagedOK;

    render(<SettingsFirmware />);

    expect(await screen.findByText(stagedOK.filename)).toBeInTheDocument();
    expect(screen.getByText(stagedOK.sha256)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Install Staged Image/i })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Discard/i })).toBeInTheDocument();
    // Replace button shows when an image is already staged.
    expect(screen.getByRole('button', { name: /Replace/i })).toBeInTheDocument();
  });

  it('shows a preflight-failed alert when the staged image preflight failed', async () => {
    apiState.systemInfo = capableInfo;
    apiState.staged = stagedFailed;

    render(<SettingsFirmware />);

    const matches = await screen.findAllByText(/Preflight failed/i);
    expect(matches.length).toBeGreaterThan(0);
    expect(screen.getByText(/image bad magic/)).toBeInTheDocument();
    expect(screen.getByText(/Incompatible image/i)).toBeInTheDocument();
  });

  it('discard staged image calls discardStagedImage', async () => {
    apiState.systemInfo = capableInfo;
    apiState.staged = stagedOK;

    render(<SettingsFirmware />);

    const discardBtn = await screen.findByRole('button', { name: /Discard/i });
    fireEvent.click(discardBtn);

    await waitFor(() => {
      expect(apiState.discardCalls).toBe(1);
    });
  });

  it('uploading a file updates the staged slot via uploadFirmware', async () => {
    apiState.systemInfo = capableInfo;
    apiState.uploadResult = stagedOK;

    render(<SettingsFirmware />);

    await screen.findByRole('heading', { name: /Local Image/i });

    // The file <input type=file> is hidden; access it directly.
    const fileInput = document.querySelector('input[type="file"]');
    const file = new File(['content'], 'openmanet-bcm27xx-bcm2711-custom.img.gz', {
      type: 'application/octet-stream',
    });

    fireEvent.change(fileInput, { target: { files: [file] } });

    await waitFor(() => {
      expect(apiState.uploadCalls).toHaveLength(1);
    });
    expect(apiState.uploadCalls[0].file).toBe(file);

    await waitFor(() => {
      expect(screen.getByText(stagedOK.filename)).toBeInTheDocument();
    });
  });

  it('install of a staged image calls startLocalUpgrade with options', async () => {
    apiState.systemInfo = capableInfo;
    apiState.staged = stagedOK;

    render(<SettingsFirmware />);

    fireEvent.click(await screen.findByRole('button', { name: /Install Staged Image/i }));

    // Confirm card opens for source=local.
    const confirmBtn = await screen.findByRole('button', {
      name: /install — device will reboot/i,
    });
    expect(confirmBtn).toBeInTheDocument();

    fireEvent.click(confirmBtn);

    await waitFor(() => {
      expect(apiState.startLocalCalls).toHaveLength(1);
    });
    expect(apiState.startLocalCalls[0]).toMatchObject({
      forceInstallUnknownCurrent: false,
      skipPreflight: false,
    });
  });

  it('install confirm is blocked when preflight failed unless skipPreflight is set', async () => {
    apiState.systemInfo = capableInfo;
    apiState.staged = stagedFailed;

    render(<SettingsFirmware />);

    fireEvent.click(await screen.findByRole('button', { name: /Install Staged Image/i }));

    const confirmBtn = await screen.findByRole('button', {
      name: /install — device will reboot/i,
    });
    expect(confirmBtn).toBeDisabled();

    // Tick the skip-preflight checkbox; the button enables.
    fireEvent.click(screen.getByLabelText(/Skip preflight/i));
    expect(confirmBtn).toBeEnabled();

    fireEvent.click(confirmBtn);

    await waitFor(() => {
      expect(apiState.startLocalCalls).toHaveLength(1);
    });
    expect(apiState.startLocalCalls[0].skipPreflight).toBe(true);
  });
});

describe('SettingsFirmware factory reset', () => {
  beforeEach(() => {
    resetApiState();
    apiState.systemInfo = capableInfo;
  });

  afterEach(() => {
    cleanup();
  });

  const capableReset = {
    capable: true,
    reason: 'ok',
    overlayMountpoint: 'overlayfs:/overlay /',
    backingFs: 'overlay',
    firstbootPath: '/sbin/firstboot',
    hostname: 'BCM2711-1003',
  };

  it('renders the panel with Begin button when capable', async () => {
    apiState.resetCap = capableReset;

    render(<SettingsFirmware />);

    expect(await screen.findByRole('heading', { name: /Factory Reset/i })).toBeInTheDocument();
    expect(await screen.findByRole('button', { name: /Begin reset/i })).toBeInTheDocument();
    expect(screen.getByText(/wipes configuration, not firmware/i)).toBeInTheDocument();
  });

  it('renders the not-capable advisory when capable=false', async () => {
    apiState.resetCap = {
      capable: false,
      reason: 'no rootfs_data partition or overlayfs mount',
      overlayMountpoint: '',
      backingFs: '',
      firstbootPath: '',
      hostname: 'dev',
    };

    render(<SettingsFirmware />);

    expect(await screen.findByText(/Factory reset unavailable/i)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Begin reset/i })).not.toBeInTheDocument();
  });

  it('expands the form when Begin reset is clicked', async () => {
    apiState.resetCap = capableReset;

    render(<SettingsFirmware />);

    fireEvent.click(await screen.findByRole('button', { name: /Begin reset/i }));

    expect(await screen.findByLabelText(/confirm hostname/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/I understand this will wipe/i)).toBeInTheDocument();
    expect(screen.getByRole('button', {
      name: /Reset to factory defaults/i,
    })).toBeDisabled();
  });

  it('keeps the danger button disabled until hostname matches and ack is checked', async () => {
    apiState.resetCap = capableReset;

    render(<SettingsFirmware />);

    fireEvent.click(await screen.findByRole('button', { name: /Begin reset/i }));

    const input = await screen.findByLabelText(/confirm hostname/i);
    const ack = screen.getByLabelText(/I understand this will wipe/i);
    const fireBtn = screen.getByRole('button', { name: /Reset to factory defaults/i });

    // Just hostname — still disabled.
    fireEvent.change(input, { target: { value: 'BCM2711-1003' } });
    expect(fireBtn).toBeDisabled();

    // Just ack — still disabled.
    fireEvent.change(input, { target: { value: '' } });
    fireEvent.click(ack);
    expect(fireBtn).toBeDisabled();

    // Both — enabled.
    fireEvent.change(input, { target: { value: 'BCM2711-1003' } });
    expect(fireBtn).toBeEnabled();
  });

  it('hostname match is case insensitive', async () => {
    apiState.resetCap = capableReset;

    render(<SettingsFirmware />);

    fireEvent.click(await screen.findByRole('button', { name: /Begin reset/i }));

    const input = await screen.findByLabelText(/confirm hostname/i);
    const ack = screen.getByLabelText(/I understand this will wipe/i);

    fireEvent.click(ack);
    fireEvent.change(input, { target: { value: 'bcm2711-1003' } });

    expect(screen.getByRole('button', { name: /Reset to factory defaults/i })).toBeEnabled();
  });

  it('confirm calls performFactoryReset with the typed hostname', async () => {
    apiState.resetCap = capableReset;

    render(<SettingsFirmware />);

    fireEvent.click(await screen.findByRole('button', { name: /Begin reset/i }));
    fireEvent.change(await screen.findByLabelText(/confirm hostname/i), {
      target: { value: 'BCM2711-1003' },
    });
    fireEvent.click(screen.getByLabelText(/I understand this will wipe/i));
    fireEvent.click(screen.getByRole('button', { name: /Reset to factory defaults/i }));

    await waitFor(() => {
      expect(apiState.resetCalls).toHaveLength(1);
    });
    expect(apiState.resetCalls[0]).toEqual({ confirmHostname: 'BCM2711-1003' });

    // Triggered state replaces the form with the rebooting advisory.
    expect(await screen.findByText(/Reset triggered/i)).toBeInTheDocument();
    expect(screen.getByText(/10\.41\.254\.1/)).toBeInTheDocument();
  });

  it('treats a transport-closed error as success (firstboot kills the daemon)', async () => {
    apiState.resetCap = capableReset;
    // Mimic a Connect transport error — no Connect-error code, so the
    // page should treat it as "reset initiated".
    apiState.resetErr = new Error('Failed to fetch');

    render(<SettingsFirmware />);

    fireEvent.click(await screen.findByRole('button', { name: /Begin reset/i }));
    fireEvent.change(await screen.findByLabelText(/confirm hostname/i), {
      target: { value: 'BCM2711-1003' },
    });
    fireEvent.click(screen.getByLabelText(/I understand this will wipe/i));
    fireEvent.click(screen.getByRole('button', { name: /Reset to factory defaults/i }));

    expect(await screen.findByText(/Reset triggered/i)).toBeInTheDocument();
  });

  it('surfaces explicit invalid_argument errors instead of declaring success', async () => {
    apiState.resetCap = capableReset;
    const err = new Error('confirm_hostname does not match');
    err.code = 'invalid_argument';
    apiState.resetErr = err;

    render(<SettingsFirmware />);

    fireEvent.click(await screen.findByRole('button', { name: /Begin reset/i }));
    fireEvent.change(await screen.findByLabelText(/confirm hostname/i), {
      target: { value: 'BCM2711-1003' },
    });
    fireEvent.click(screen.getByLabelText(/I understand this will wipe/i));
    fireEvent.click(screen.getByRole('button', { name: /Reset to factory defaults/i }));

    await waitFor(() => {
      expect(apiState.resetCalls).toHaveLength(1);
    });

    // Should surface the error, not the rebooting advisory.
    expect(await screen.findByText(/confirm_hostname does not match/i)).toBeInTheDocument();
    expect(screen.queryByText(/Reset triggered\./)).not.toBeInTheDocument();
  });

  it('Cancel collapses the form back to Begin', async () => {
    apiState.resetCap = capableReset;

    render(<SettingsFirmware />);

    fireEvent.click(await screen.findByRole('button', { name: /Begin reset/i }));
    expect(await screen.findByLabelText(/confirm hostname/i)).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: /^Cancel$/i }));

    await waitFor(() => {
      expect(screen.queryByLabelText(/confirm hostname/i)).not.toBeInTheDocument();
    });
    expect(screen.getByRole('button', { name: /Begin reset/i })).toBeInTheDocument();
  });
});

describe('SettingsFirmware mobile cards', () => {
  beforeEach(() => {
    resetApiState();
  });

  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it('renders the releases table as both table rows and cards', async () => {
    apiState.systemInfo = capableInfo;
    apiState.updates = [
      sampleUpdate,
      {
        release: {
          tag: 'v1.8.0',
          name: 'OpenMANET 1.8.0',
          body: '',
          publishedAt: new Date('2026-02-10T12:00:00Z'),
          prerelease: true,
          version: '1.8.0',
          assets: [],
        },
        matchedAsset: {
          name: 'openmanet-1.8.0-sysupgrade.img.gz',
          sizeBytes: 51_000_000,
          downloadUrl: 'https://example.com/v1.8.0',
        },
        newerThanCurrent: true,
      },
    ];
    apiState.fetchedAt = new Date('2026-04-25T12:00:00Z');

    const { container } = render(<SettingsFirmware />);

    await waitFor(() => {
      expect(container.querySelector('.lat-tabular')).toBeTruthy();
    });
    const tabular = container.querySelector('.lat-tabular');
    const tableRows = tabular.querySelectorAll('.lat-table tbody tr').length;
    expect(tableRows).toBeGreaterThan(0);
    expect(tabular.querySelectorAll('.lat-cardlist .lat-card')).toHaveLength(tableRows);
  });
});
