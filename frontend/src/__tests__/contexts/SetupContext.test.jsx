// =============================================================================
// SetupContext.test.jsx — Pure-reducer tests for the wizard state machine
// =============================================================================
//
// The reducer is exported so we can drive every action without a render
// tree. Tests assert (a) the action mutates the right slice, (b) the
// returned state object is a new reference (immutability), and (c)
// SET_ROLE clears the orthogonal mode so the resulting profile is
// consistent with the handler's role/mode validation.

import { describe, it, expect } from 'vitest';
import {
  reducer,
  initialState,
  SETUP_ACTIONS,
} from '../../contexts/SetupContext.jsx';
import {
  MeshRole,
  MeshPointMode,
  MeshGateMode,
} from '../../gen/openmanet/setup/v1/setup_pb.js';
import { WifiEncryption } from '../../gen/openmanet/wifi_config/v1/wifi_config_pb.js';

describe('SetupContext.reducer', () => {
  it('SET_HOSTNAME mutates only hostname', () => {
    const next = reducer(initialState, { type: SETUP_ACTIONS.SET_HOSTNAME, value: 'mynode' });
    expect(next.hostname).toBe('mynode');
    expect(next).not.toBe(initialState);
    expect(next.mesh).toBe(initialState.mesh); // mesh slice untouched
  });

  it('SET_ROLE flipping to MESH_GATE clears meshpoint_mode and seeds gate mode', () => {
    const next = reducer(initialState, { type: SETUP_ACTIONS.SET_ROLE, value: MeshRole.MESH_GATE });
    expect(next.role).toBe(MeshRole.MESH_GATE);
    expect(next.meshpointMode).toBe(MeshPointMode.UNSPECIFIED);
    expect(next.meshgateMode).toBe(MeshGateMode.ROUTER);
  });

  it('SET_ROLE flipping back to MESH_POINT clears meshgate_mode and seeds point mode', () => {
    const gate = reducer(initialState, { type: SETUP_ACTIONS.SET_ROLE, value: MeshRole.MESH_GATE });
    const back = reducer(gate, { type: SETUP_ACTIONS.SET_ROLE, value: MeshRole.MESH_POINT });
    expect(back.role).toBe(MeshRole.MESH_POINT);
    expect(back.meshgateMode).toBe(MeshGateMode.UNSPECIFIED);
    expect(back.meshpointMode).toBe(MeshPointMode.EXTENDER);
  });

  it('SET_MESH_FIELD mutates only the named mesh slice', () => {
    const next = reducer(initialState, {
      type: SETUP_ACTIONS.SET_MESH_FIELD, field: 'channel', value: 36,
    });
    expect(next.mesh.channel).toBe(36);
    expect(next.mesh.meshId).toBe(initialState.mesh.meshId);
    expect(next.mesh).not.toBe(initialState.mesh);
  });

  it('SET_AP appends a new AP entry on first set', () => {
    const next = reducer(initialState, {
      type: SETUP_ACTIONS.SET_AP,
      value: { radioName: 'radio0', enabled: true, ssid: 'home', passphrase: 'longenough' },
    });
    expect(next.aps).toHaveLength(1);
    expect(next.aps[0].radioName).toBe('radio0');
    expect(next.aps[0].ssid).toBe('home');
    expect(next.aps[0].encryption).toBe(WifiEncryption.PSK2);
  });

  it('SET_AP merges into existing AP by radioName', () => {
    const seeded = reducer(initialState, {
      type: SETUP_ACTIONS.SET_AP,
      value: { radioName: 'radio0', enabled: true, ssid: 'home', passphrase: 'longenough' },
    });
    const merged = reducer(seeded, {
      type: SETUP_ACTIONS.SET_AP,
      value: { radioName: 'radio0', ssid: 'changed' },
    });
    expect(merged.aps).toHaveLength(1);
    expect(merged.aps[0].ssid).toBe('changed');
    expect(merged.aps[0].passphrase).toBe('longenough');
    expect(merged.aps[0].enabled).toBe(true);
  });

  it('REMOVE_AP filters by radioName', () => {
    const seeded = reducer(initialState, {
      type: SETUP_ACTIONS.SET_AP,
      value: { radioName: 'radio0', enabled: true },
    });
    const removed = reducer(seeded, { type: SETUP_ACTIONS.REMOVE_AP, radioName: 'radio0' });
    expect(removed.aps).toHaveLength(0);
  });

  it('RESET returns a fresh initial state', () => {
    const dirty = reducer(initialState, { type: SETUP_ACTIONS.SET_HOSTNAME, value: 'foo' });
    const reset = reducer(dirty, { type: SETUP_ACTIONS.RESET });
    expect(reset).toEqual(initialState);
  });

  it('HYDRATE_FROM_STATUS pre-fills mesh radio with first HaLow', () => {
    const next = reducer(initialState, {
      type: SETUP_ACTIONS.HYDRATE_FROM_STATUS,
      status: {
        radios: [
          { name: 'radio0', isHalow: false },
          { name: 'radio1', isHalow: true  },
        ],
      },
    });
    expect(next.mesh.radioName).toBe('radio1');
  });

  it('HYDRATE_FROM_STATUS pre-fills hostname from currentHostname when empty', () => {
    const next = reducer(initialState, {
      type: SETUP_ACTIONS.HYDRATE_FROM_STATUS,
      status: { radios: [], currentHostname: 'BCM2711-97d6' },
    });
    expect(next.hostname).toBe('BCM2711-97d6');
  });

  it('HYDRATE_FROM_STATUS does not overwrite a hostname the user has already typed', () => {
    const dirty = reducer(initialState, { type: SETUP_ACTIONS.SET_HOSTNAME, value: 'mynode' });
    const next = reducer(dirty, {
      type: SETUP_ACTIONS.HYDRATE_FROM_STATUS,
      status: { radios: [], currentHostname: 'BCM2711-97d6' },
    });
    expect(next.hostname).toBe('mynode');
  });

  it('unknown action returns the same state reference', () => {
    const next = reducer(initialState, { type: 'NOT_A_REAL_ACTION' });
    expect(next).toBe(initialState);
  });

  it('SET_TIMEZONE stores the zone name', () => {
    const next = reducer(initialState, { type: SETUP_ACTIONS.SET_TIMEZONE, value: 'America/Denver' });
    expect(next.timezone).toBe('America/Denver');
    expect(next.mesh).toBe(initialState.mesh);
  });

  it('HYDRATE_FROM_STATUS prefers the browser zone when the device offers it', () => {
    const next = reducer(initialState, {
      type: SETUP_ACTIONS.HYDRATE_FROM_STATUS,
      status: { radios: [], timezones: ['America/Denver', 'UTC'] },
      browserTimezone: 'America/Denver',
    });
    expect(next.timezone).toBe('America/Denver');
  });

  it('HYDRATE_FROM_STATUS falls back to currentTimezone when the browser zone is unknown', () => {
    const next = reducer(initialState, {
      type: SETUP_ACTIONS.HYDRATE_FROM_STATUS,
      status: { radios: [], timezones: ['UTC'], currentTimezone: 'UTC' },
      browserTimezone: 'Mars/Olympus',
    });
    expect(next.timezone).toBe('UTC');
  });

  it('HYDRATE_FROM_STATUS keeps a user-chosen timezone', () => {
    const seeded = { ...initialState, timezone: 'Europe/Paris' };
    const next = reducer(seeded, {
      type: SETUP_ACTIONS.HYDRATE_FROM_STATUS,
      status: { radios: [], timezones: ['UTC'] },
      browserTimezone: 'UTC',
    });
    expect(next.timezone).toBe('Europe/Paris');
  });

  it('SET_RADIO_MODE backhaul disables the AP, seeds the mesh ID, and clears backhaul on other radios', () => {
    const withAp = reducer(initialState, {
      type: SETUP_ACTIONS.SET_AP,
      value: { radioName: 'radio0', enabled: true, ssid: 'home', passphrase: 'longenough' },
    });
    const first = reducer(withAp, { type: SETUP_ACTIONS.SET_RADIO_MODE, radioName: 'radio0', mode: 'backhaul' });
    const r0 = first.aps.find(a => a.radioName === 'radio0');
    expect(r0.enabled).toBe(false);
    expect(r0.meshBackhaul).toBe(true);
    expect(r0.backhaulMeshId).toBe(`${initialState.mesh.meshId}-2g`);
    expect(r0.ssid).toBe('home'); // AP fields survive a mode flip

    const second = reducer(first, { type: SETUP_ACTIONS.SET_RADIO_MODE, radioName: 'radio2', mode: 'backhaul' });
    expect(second.aps.find(a => a.radioName === 'radio0').meshBackhaul).toBe(false);
    expect(second.aps.find(a => a.radioName === 'radio2').meshBackhaul).toBe(true);
    expect(second.aps.filter(a => a.meshBackhaul)).toHaveLength(1);
  });

  it('SET_RADIO_MODE ap and off flip enabled and clear backhaul', () => {
    const bh = reducer(initialState, { type: SETUP_ACTIONS.SET_RADIO_MODE, radioName: 'radio0', mode: 'backhaul' });
    const ap = reducer(bh, { type: SETUP_ACTIONS.SET_RADIO_MODE, radioName: 'radio0', mode: 'ap' });
    expect(ap.aps[0].enabled).toBe(true);
    expect(ap.aps[0].meshBackhaul).toBe(false);

    const off = reducer(ap, { type: SETUP_ACTIONS.SET_RADIO_MODE, radioName: 'radio0', mode: 'off' });
    expect(off.aps[0].enabled).toBe(false);
    expect(off.aps[0].meshBackhaul).toBe(false);
    expect(off).not.toBe(ap);
  });

  it('SET_RADIO_MODE keeps a user-typed backhaul mesh ID across mode flips', () => {
    const bh = reducer(initialState, { type: SETUP_ACTIONS.SET_RADIO_MODE, radioName: 'radio0', mode: 'backhaul' });
    const typed = reducer(bh, { type: SETUP_ACTIONS.SET_AP, value: { radioName: 'radio0', backhaulMeshId: 'mine' } });
    const off = reducer(typed, { type: SETUP_ACTIONS.SET_RADIO_MODE, radioName: 'radio0', mode: 'off' });
    const again = reducer(off, { type: SETUP_ACTIONS.SET_RADIO_MODE, radioName: 'radio0', mode: 'backhaul' });
    expect(again.aps[0].backhaulMeshId).toBe('mine');
  });

  it('SET_AP defaults the backhaul fields on insert', () => {
    const next = reducer(initialState, { type: SETUP_ACTIONS.SET_AP, value: { radioName: 'radio0' } });
    expect(next.aps[0]).toMatchObject({ meshBackhaul: false, backhaulMeshId: '', backhaulPassphrase: '' });
  });

  it('SET_RADIO_MODE clamps the seeded backhaul mesh ID to 32 characters', () => {
    const longMeshId = 'a'.repeat(32);
    const withMeshId = reducer(initialState, {
      type: SETUP_ACTIONS.SET_MESH_FIELD,
      field: 'meshId',
      value: longMeshId,
    });
    const next = reducer(withMeshId, { type: SETUP_ACTIONS.SET_RADIO_MODE, radioName: 'radio0', mode: 'backhaul' });
    const seeded = next.aps.find(a => a.radioName === 'radio0').backhaulMeshId;
    expect(seeded.length).toBeLessThanOrEqual(32);
    expect(seeded.endsWith('-2g')).toBe(true);
  });

  const RADIOS = [
    { name: 'radio1', band: 's1g', isHalow: true },
    { name: 'radio0', band: '2g', isHalow: false, supportsMeshBackhaul: true },
    { name: 'radio2', band: '5g', isHalow: false, supportsMeshBackhaul: false },
  ];
  const PAYLOAD = {
    sourceHostname: 'alpha',
    halow: { meshId: 'field-mesh', passphrase: 'correct-horse', encryption: WifiEncryption.SAE, bandwidthMhz: 8, channel: 44, countryCode: 'US' },
    backhaul: { meshId: 'field-mesh-2g', passphrase: 'backhaul-pass', encryption: WifiEncryption.SAE, bandwidthMhz: 40, channel: 6, countryCode: 'US' },
  };

  it('APPLY_MESH_JOIN fills the mesh slice and marks it scanned', () => {
    const next = reducer(initialState, { type: SETUP_ACTIONS.APPLY_MESH_JOIN, payload: PAYLOAD, radios: RADIOS });
    expect(next.mesh).toMatchObject({ meshId: 'field-mesh', passphrase: 'correct-horse', bandwidthMhz: 8, channel: 44, countryCode: 'US', fromScan: true });
    expect(next.mesh.radioName).toBe(initialState.mesh.radioName);
    expect(next.meshJoin).toEqual({ sourceHostname: 'alpha', backhaulRadio: 'radio0', backhaulSkippedReason: '' });
  });

  it('APPLY_MESH_JOIN switches the first capable radio to backhaul with the scanned tuning', () => {
    const next = reducer(initialState, { type: SETUP_ACTIONS.APPLY_MESH_JOIN, payload: PAYLOAD, radios: RADIOS });
    const ap = next.aps.find(a => a.radioName === 'radio0');
    expect(ap).toMatchObject({
      enabled: false, meshBackhaul: true, backhaulMeshId: 'field-mesh-2g', backhaulPassphrase: 'backhaul-pass',
      backhaulBandwidthMhz: 40, backhaulChannel: 6, backhaulCountryCode: 'US', backhaulFromScan: true,
    });
  });

  it('APPLY_MESH_JOIN clears backhaul on other radios and skips the STA uplink radio', () => {
    const withOther = reducer(initialState, { type: SETUP_ACTIONS.SET_RADIO_MODE, radioName: 'radio2', mode: 'backhaul' });
    const uplinked = { ...withOther, uplink: { ...withOther.uplink, wireless: { ...withOther.uplink.wireless, radioName: 'radio0' } } };
    const next = reducer(uplinked, { type: SETUP_ACTIONS.APPLY_MESH_JOIN, payload: PAYLOAD, radios: RADIOS });
    expect(next.meshJoin.backhaulRadio).toBe('');
    expect(next.meshJoin.backhaulSkippedReason).toBe('no-capable-radio');
    expect(next.aps.find(a => a.radioName === 'radio2').meshBackhaul).toBe(true);
  });

  it('APPLY_MESH_JOIN without a backhaul leaves aps alone', () => {
    const next = reducer(initialState, { type: SETUP_ACTIONS.APPLY_MESH_JOIN, payload: { ...PAYLOAD, backhaul: undefined }, radios: RADIOS });
    expect(next.aps).toEqual([]);
    expect(next.meshJoin.backhaulRadio).toBe('');
  });

  it('SET_MESH_FIELD clears fromScan except for the radio pick', () => {
    const scanned = reducer(initialState, { type: SETUP_ACTIONS.APPLY_MESH_JOIN, payload: PAYLOAD, radios: RADIOS });
    expect(reducer(scanned, { type: SETUP_ACTIONS.SET_MESH_FIELD, field: 'radioName', value: 'radio1' }).mesh.fromScan).toBe(true);
    expect(reducer(scanned, { type: SETUP_ACTIONS.SET_MESH_FIELD, field: 'channel', value: 28 }).mesh.fromScan).toBe(false);
  });

  it('RESET drops the scan', () => {
    const scanned = reducer(initialState, { type: SETUP_ACTIONS.APPLY_MESH_JOIN, payload: PAYLOAD, radios: RADIOS });
    expect(reducer(scanned, { type: SETUP_ACTIONS.RESET }).meshJoin).toBeNull();
  });
});
