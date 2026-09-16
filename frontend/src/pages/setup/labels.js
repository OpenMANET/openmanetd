// =============================================================================
// labels.js — Human-readable labels for every wizard enum
// =============================================================================
//
// The wizard's reducer state stores enum INTEGERS only (e.g.
// MeshRole.MESH_GATE === 2). UCI strings live exclusively in the backend
// translators. The frontend's only job is to convert each enum integer to
// the user-facing label that gets rendered in the LatSelect popup. Keep
// this file thin: every UI surface that needs a human string for an enum
// imports the corresponding map here.
//
// A test in __tests__/setup/labels.test.js iterates over every defined
// enum value (excluding UNSPECIFIED) and asserts a label entry exists,
// so adding a new enum value in proto without updating the label map
// fails CI before the user ever sees a blank dropdown row.

import {
  MeshRole,
  MeshPointMode,
  MeshGateMode,
  UplinkType,
} from '../../gen/openmanet/setup/v1/setup_pb.js';
import { WifiEncryption } from '../../gen/openmanet/wifi_config/v1/wifi_config_pb.js';

export const ROLE_LABELS = {
  [MeshRole.MESH_POINT]: 'Mesh Point',
  [MeshRole.MESH_GATE]:  'Mesh Gate',
};

export const MESH_POINT_MODE_LABELS = {
  [MeshPointMode.NONE]:     'No uplink (mesh-only)',
  [MeshPointMode.EXTENDER]: 'Extender (bridges Wi-Fi clients onto the mesh)',
};

export const MESH_GATE_MODE_LABELS = {
  [MeshGateMode.ROUTER]:          'Router (NAT to upstream)',
  [MeshGateMode.ROUTER_FIREWALL]: 'Router + firewall (untrusted upstream)',
};

export const UPLINK_TYPE_LABELS = {
  [UplinkType.ETHERNET]:     'Ethernet',
  [UplinkType.WIRELESS_STA]: 'Wireless (Wi-Fi STA)',
};

export const ENCRYPTION_LABELS = {
  [WifiEncryption.SAE]:       'WPA3 (SAE)',
  [WifiEncryption.PSK2]:      'WPA2 (PSK2)',
  [WifiEncryption.PSK]:       'WPA (PSK)',
  // psk-mixed is WPA1+WPA2 in OpenWrt (wpa=3), not WPA2/WPA3.
  [WifiEncryption.PSK_MIXED]: 'WPA / WPA2 (mixed, legacy)',
  // sae-mixed is OpenWrt's WPA2/WPA3 transition mode (psk-sae).
  [WifiEncryption.SAE_MIXED]: 'WPA2 / WPA3 (mixed)',
  [WifiEncryption.OWE]:       'OWE (open, encrypted)',
  [WifiEncryption.NONE]:      'None (open)',
};

// optionsFromMap converts a label map into the {value, label} array shape
// LatSelect expects. Values are kept as the underlying enum integer.
export function optionsFromMap(labelMap) {
  return Object.entries(labelMap).map(([value, label]) => ({
    value: Number(value),
    label,
  }));
}
