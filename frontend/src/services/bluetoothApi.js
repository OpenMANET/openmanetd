import { createClient } from '@connectrpc/connect';
import { transport } from './connectClient.js';
import { BluetoothService, BluetoothAction } from '../gen/openmanet/bluetooth/v1/bluetooth_service_pb.js';

const client = createClient(BluetoothService, transport);
export { BluetoothAction };
export function fetchBluetoothStatus(signal) {
  return client.getBluetoothStatus({}, { signal, timeoutMs: 10_000 });
}
export function executeBluetoothAction(action, fields = {}, signal) {
  return client.executeBluetoothAction({ action, ...fields }, { signal, timeoutMs: 65_000 });
}
export function updateBluetoothPreferences(autoEnable, signal) {
  return client.updateBluetoothPreferences({ autoEnable }, { signal, timeoutMs: 10_000 });
}
