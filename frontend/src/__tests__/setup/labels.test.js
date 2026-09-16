// =============================================================================
// labels.test.js — Verify every defined wizard enum value has a UI label
// =============================================================================
//
// The wizard's reducer state stores enum integers. Every dropdown
// renders the human label from `pages/setup/labels.js`. If a new enum
// value is added in proto without updating the label map, this test
// fails before the user ever sees a blank dropdown row.

import { describe, it, expect } from 'vitest';
import {
  MeshRole,
  MeshPointMode,
  MeshGateMode,
  UplinkType,
} from '../../gen/openmanet/setup/v1/setup_pb.js';
import { WifiEncryption } from '../../gen/openmanet/wifi_config/v1/wifi_config_pb.js';
import {
  ROLE_LABELS,
  MESH_POINT_MODE_LABELS,
  MESH_GATE_MODE_LABELS,
  UPLINK_TYPE_LABELS,
  ENCRYPTION_LABELS,
  optionsFromMap,
} from '../../pages/setup/labels.js';

// Every test passes (enum, labelMap) and asserts every defined enum
// value (excluding UNSPECIFIED=0) has a non-empty string label.
function assertLabelsCoverEnum(enumObj, labelMap, enumName) {
  // Connect-protobuf-es enums expose values via the `values` array on
  // their meta object; iterate via `Object.values(enumObj)` and skip
  // anything not a number, plus UNSPECIFIED=0.
  const definedValues = Object.values(enumObj).filter(
    v => typeof v === 'number' && v !== 0,
  );

  expect(definedValues.length, `${enumName} should have at least one defined value`)
    .toBeGreaterThan(0);

  for (const v of definedValues) {
    expect(labelMap[v], `${enumName} value ${v} missing label`).toBeTruthy();
    expect(typeof labelMap[v], `${enumName} label[${v}] should be a string`).toBe('string');
  }
}

describe('LabelMaps', () => {
  it('ROLE_LABELS covers every MeshRole', () => {
    assertLabelsCoverEnum(MeshRole, ROLE_LABELS, 'MeshRole');
  });

  it('MESH_POINT_MODE_LABELS covers every MeshPointMode', () => {
    assertLabelsCoverEnum(MeshPointMode, MESH_POINT_MODE_LABELS, 'MeshPointMode');
  });

  it('MESH_GATE_MODE_LABELS covers every MeshGateMode', () => {
    assertLabelsCoverEnum(MeshGateMode, MESH_GATE_MODE_LABELS, 'MeshGateMode');
  });

  it('UPLINK_TYPE_LABELS covers every UplinkType', () => {
    assertLabelsCoverEnum(UplinkType, UPLINK_TYPE_LABELS, 'UplinkType');
  });

  it('ENCRYPTION_LABELS covers every WifiEncryption', () => {
    assertLabelsCoverEnum(WifiEncryption, ENCRYPTION_LABELS, 'WifiEncryption');
  });

  it('labels the two mixed modes by what OpenWrt actually configures', () => {
    // psk-mixed = wpa=3 (WPA1+WPA2 PSK); sae-mixed = psk-sae (WPA2+WPA3).
    expect(ENCRYPTION_LABELS[WifiEncryption.PSK_MIXED]).toBe('WPA / WPA2 (mixed, legacy)');
    expect(ENCRYPTION_LABELS[WifiEncryption.SAE_MIXED]).toBe('WPA2 / WPA3 (mixed)');
  });
});

describe('optionsFromMap', () => {
  it('returns LatSelect-shaped options keyed by integer value', () => {
    const opts = optionsFromMap(ROLE_LABELS);
    expect(opts.length).toBe(Object.keys(ROLE_LABELS).length);
    for (const o of opts) {
      expect(typeof o.value).toBe('number');
      expect(typeof o.label).toBe('string');
    }
  });
});
