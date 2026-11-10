import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest';
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/react';
import { Code, ConnectError } from '@connectrpc/connect';
import SettingsBluetooth from '../../pages/SettingsBluetooth.jsx';
import { fetchBluetoothStatus, executeBluetoothAction, updateBluetoothPreferences, BluetoothAction as Action } from '../../services/bluetoothApi.js';

vi.mock('../../services/bluetoothApi.js', async () => {
  const { BluetoothAction } = await import('../../gen/openmanet/bluetooth/v1/bluetooth_service_pb.js');
  return { BluetoothAction, fetchBluetoothStatus: vi.fn(), executeBluetoothAction: vi.fn(), updateBluetoothPreferences: vi.fn() };
});

const offStatus = { available: true, enabled: false, autoEnable: false, controllers: [{ path: '/org/bluez/hci0', address: 'AA:BB', name: 'Node' }], accessories: [] };
const readyStatus = { ...offStatus, enabled: true, connectionAllowed: true, ready: true, audioReady: true, hfpReady: true, buttonsReady: true, savedDeviceAddress: '41:42:86:99:1D:61', savedDeviceName: 'BS-22' };

beforeEach(() => {
  vi.resetAllMocks();
  vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible');
  fetchBluetoothStatus.mockResolvedValue(offStatus);
  executeBluetoothAction.mockResolvedValue({});
  updateBluetoothPreferences.mockResolvedValue({});
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); });

describe('SettingsBluetooth', () => {
  it('checks a timed-out pairing against the selected device before offering another attempt', async () => {
    const device = { path: '/org/bluez/hci0/dev_41_42_86_99_1D_61', controllerPath: '/org/bluez/hci0', name: 'BS-22', supported: true };
    const setup = { ...offStatus, enabled: true, connectionAllowed: true, accessories: [device] };
    fetchBluetoothStatus.mockResolvedValue(setup);
    executeBluetoothAction.mockRejectedValue(new ConnectError('operation timed out', Code.DeadlineExceeded));
    render(<SettingsBluetooth />);
    fireEvent.click(await screen.findByRole('button', { name: 'Pair and connect' }));
    await screen.findByText('The radio did not confirm that action.', { exact: false });
    fetchBluetoothStatus.mockResolvedValue({
      ...readyStatus, selectedDevicePath: device.path, accessories: [{ ...device, paired: true, connected: true }],
    });
    await screen.findByText('Ready to test', {}, { timeout: 3500 });
    expect(screen.queryByText(/did not confirm/)).toBeNull();
    expect(screen.queryByRole('button', { name: 'Connect', exact: true })).toBeNull();
    expect(screen.getByRole('button', { name: 'Play test tone' }).disabled).toBe(false);
    expect(executeBluetoothAction).toHaveBeenCalledTimes(1);
  });

  it('keeps a status timeout separate from the outcome of pairing', async () => {
    fetchBluetoothStatus.mockRejectedValueOnce(new ConnectError('operation timed out', Code.DeadlineExceeded));
    render(<SettingsBluetooth />);
    await screen.findByText(/Bluetooth status may be out of date/);
    expect(screen.queryByRole('alert')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    await screen.findByRole('button', { name: 'Start Bluetooth setup' });
    expect(screen.queryByText(/Bluetooth status may be out of date/)).toBeNull();
    expect(executeBluetoothAction).not.toHaveBeenCalled();
  });

  it('reads status without powering on or scanning', async () => {
    render(<SettingsBluetooth />);
    await screen.findByRole('button', { name: 'Start Bluetooth setup' });
    expect(executeBluetoothAction).not.toHaveBeenCalled();
    expect(screen.queryByRole('button', { name: 'Forget PTT' })).toBeNull();
    expect(screen.getByRole('switch').getAttribute('aria-checked')).toBe('false');
    expect(screen.getByRole('button', { name: 'Use this PTT' }).disabled).toBe(true);
  });

  it('offers Forget PTT for a saved pairing even while Bluetooth is off', async () => {
    fetchBluetoothStatus.mockResolvedValue({
      ...offStatus, savedDeviceName: 'BS-22', savedDeviceAddress: readyStatus.savedDeviceAddress,
    });
    executeBluetoothAction.mockImplementation(async () => {
      fetchBluetoothStatus.mockResolvedValue(offStatus);
      return {};
    });
    render(<SettingsBluetooth />);
    fireEvent.click(await screen.findByRole('button', { name: 'Forget PTT' }));
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Forget PTT' })).toBeNull());
    expect(executeBluetoothAction).toHaveBeenCalledExactlyOnceWith(Action.FORGET, undefined, expect.any(AbortSignal));
  });

  it('explains absent hardware without offering a power action', async () => {
    fetchBluetoothStatus.mockResolvedValue({ available: false, controllers: [], accessories: [] });
    render(<SettingsBluetooth />);
    await screen.findByText('No Bluetooth controller found');
    expect(screen.queryByRole('button', { name: 'Start Bluetooth setup' })).toBeNull();
    expect(executeBluetoothAction).not.toHaveBeenCalled();
  });

  it('cancels a running scan when Bluetooth off is selected', async () => {
    let scanSignal;
    executeBluetoothAction.mockImplementation((action, fields, signal) => {
      if (action === Action.BEGIN_SETUP) {
        fetchBluetoothStatus.mockResolvedValue({ ...offStatus, enabled: true, connectionAllowed: true });
      }
      if (action === Action.DISCOVER) {
        scanSignal = signal;
        return new Promise((resolve, reject) => signal.addEventListener('abort', () => reject(new Error('cancelled')), { once: true }));
      }
      return Promise.resolve({});
    });
    render(<SettingsBluetooth />);
    fireEvent.click(await screen.findByRole('button', { name: 'Start Bluetooth setup' }));
    await waitFor(() => expect(scanSignal).toBeDefined());
    fireEvent.click(screen.getByRole('button', { name: 'Bluetooth off' }));
    await waitFor(() => expect(executeBluetoothAction).toHaveBeenCalledWith(Action.OFF, undefined, expect.any(AbortSignal)));
    expect(scanSignal.aborted).toBe(true);
  });

  it('lets the user choose a PTT before the scan ends, then shows audio setup progress', async () => {
    const device = { path: '/org/bluez/hci0/dev_41_42_86_99_1D_61', controllerPath: '/org/bluez/hci0', name: 'BS-22', supported: true };
    const setup = { ...offStatus, enabled: true, connectionAllowed: true, accessories: [device] };
    fetchBluetoothStatus.mockResolvedValue(setup);
    executeBluetoothAction.mockImplementation((action, fields, signal) => {
      if (action === Action.DISCOVER) {
        return new Promise((resolve, reject) => signal.addEventListener('abort', () => reject(new Error('cancelled')), { once: true }));
      }
      fetchBluetoothStatus.mockResolvedValue({
        ...setup, selectedDevicePath: device.path, savedDeviceAddress: readyStatus.savedDeviceAddress,
        accessories: [{ ...device, paired: true, connected: true }],
      });
      return Promise.resolve({});
    });
    render(<SettingsBluetooth />);
    fireEvent.click(await screen.findByRole('button', { name: 'Scan again' }));
    await screen.findByText('PTT found. Choose it below to continue.');
    fireEvent.click(screen.getByRole('button', { name: 'Pair and connect' }));
    await screen.findByText('PTT connected. Getting audio and buttons ready…');
    expect(screen.getByRole('button', { name: 'Use this PTT' }).disabled).toBe(true);
  });

  it('tests audio without activating and requires an explicit activation click', async () => {
    fetchBluetoothStatus.mockResolvedValue(readyStatus);
    render(<SettingsBluetooth />);
    fireEvent.click(await screen.findByRole('button', { name: 'Play test tone' }));
    await screen.findByText('Listen for a tone from the PTT speaker.');
    expect(executeBluetoothAction).toHaveBeenCalledWith(Action.TEST_TONE, {}, expect.any(AbortSignal));
    expect(executeBluetoothAction.mock.calls.some(([action]) => action === Action.ACTIVATE)).toBe(false);
    await waitFor(() => expect(screen.getByRole('button', { name: 'Use this PTT' }).disabled).toBe(false));
    fireEvent.click(screen.getByRole('button', { name: 'Use this PTT' }));
    await waitFor(() => expect(executeBluetoothAction).toHaveBeenCalledWith(Action.ACTIVATE, undefined, expect.any(AbortSignal)));
  });

  it('saves the boot opt-in only after the user selects it', async () => {
    fetchBluetoothStatus.mockResolvedValue(readyStatus);
    render(<SettingsBluetooth />);
    const toggle = await screen.findByRole('switch');
    expect(updateBluetoothPreferences).not.toHaveBeenCalled();
    fireEvent.click(toggle);
    await waitFor(() => expect(updateBluetoothPreferences).toHaveBeenCalledWith(true, expect.any(AbortSignal)));
  });
});
