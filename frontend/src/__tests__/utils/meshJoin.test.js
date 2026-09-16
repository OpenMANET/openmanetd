// =============================================================================
// meshJoin.test.js — payload decode + credential checks
// =============================================================================

import { describe, it, expect } from 'vitest';
import { create } from '@bufbuild/protobuf';
import { MeshJoinPayloadSchema } from '../../gen/openmanet/mesh_join/v1/mesh_join_pb.js';
import { WifiEncryption, WifiHTMode } from '../../gen/openmanet/wifi_config/v1/wifi_config_pb.js';
import {
  MeshJoinError,
  decodeMeshJoinText,
  checkMeshCredentials,
  htModeForBandwidth,
  bandwidthMhzForHTMode,
} from '../../utils/meshJoin.js';
import { samplePayload, encodePayload } from '../meshJoinFixtures.js';

describe('DecodeMeshJoinText', () => {
  it('round-trips a payload', () => {
    const decoded = decodeMeshJoinText(encodePayload(samplePayload()));
    expect(decoded.sourceHostname).toBe('alpha');
    expect(decoded.halow.meshId).toBe('field-mesh');
    expect(decoded.halow.channel).toBe(44);
    expect(decoded.backhaul.meshId).toBe('field-mesh-2g');
  });

  it('tolerates surrounding whitespace', () => {
    const decoded = decodeMeshJoinText(`  ${encodePayload(samplePayload())}\n`);
    expect(decoded.halow.meshId).toBe('field-mesh');
  });

  it('rejects text without the marker', () => {
    expect(() => decodeMeshJoinText('WIFI:S:x;P:y;;')).toThrow(MeshJoinError);
    try { decodeMeshJoinText('WIFI:S:x;P:y;;'); } catch (e) { expect(e.code).toBe('not-mesh-join'); }
  });

  it('flags a newer payload version', () => {
    try { decodeMeshJoinText('OPENMANET2:AAAA'); } catch (e) { expect(e.code).toBe('unsupported-version'); }
  });

  it('flags corrupt bodies', () => {
    try { decodeMeshJoinText('OPENMANET1:%%%'); } catch (e) { expect(e.code).toBe('corrupt'); }
    try { decodeMeshJoinText('OPENMANET1:'); } catch (e) { expect(e.code).toBe('corrupt'); }
  });

  it('treats a payload without halow credentials as corrupt', () => {
    const text = encodePayload(create(MeshJoinPayloadSchema, { sourceHostname: 'alpha' }));
    try { decodeMeshJoinText(text); } catch (e) { expect(e.code).toBe('corrupt'); }
  });
});

describe('CheckMeshCredentials', () => {
  const halow = () => samplePayload().halow;

  it('accepts legal credentials', () => {
    expect(checkMeshCredentials(halow(), { isHalow: true, channels: [12, 28, 44], bandwidths: [1, 2, 4, 8], countries: ['US'] })).toEqual([]);
  });

  it('flags non-SAE encryption', () => {
    const c = halow(); c.encryption = WifiEncryption.PSK2;
    expect(checkMeshCredentials(c, { isHalow: true }).map(i => i.field)).toContain('encryption');
  });

  it('flags a short passphrase', () => {
    const c = halow(); c.passphrase = 'short';
    expect(checkMeshCredentials(c, { isHalow: true }).map(i => i.field)).toContain('passphrase');
  });

  it('flags an empty mesh id', () => {
    const c = halow(); c.meshId = '';
    expect(checkMeshCredentials(c, { isHalow: true }).map(i => i.field)).toContain('meshId');
  });

  it('flags a bandwidth the radio cannot do', () => {
    const c = halow(); c.bandwidthMhz = 8;
    const issues = checkMeshCredentials(c, { isHalow: true, bandwidths: [1, 2] });
    expect(issues.map(i => i.field)).toContain('bandwidthMhz');
    expect(issues.map(i => i.field)).not.toContain('channel');
  });

  it('flags an illegal channel and names bandwidth + country', () => {
    const issues = checkMeshCredentials(halow(), { isHalow: true, channels: [12, 28], bandwidths: [1, 2, 4, 8] });
    expect(issues).toEqual([{ field: 'channel', message: 'Channel 44 is not legal at 8 MHz in US.' }]);
  });

  it('accepts string channel lists from the settings API', () => {
    expect(checkMeshCredentials(halow(), { isHalow: true, channels: ['12', '28', '44'], bandwidths: [8] })).toEqual([]);
  });

  it('flags a country missing from the device list', () => {
    const issues = checkMeshCredentials(halow(), { isHalow: true, countries: ['GB', 'EU'] });
    expect(issues.map(i => i.field)).toContain('countryCode');
  });

  it('flags a country too long for the target input', () => {
    const c = halow(); c.countryCode = 'EUR';
    expect(checkMeshCredentials(c, { isHalow: true, countryMaxLen: 2 }).map(i => i.field)).toContain('countryCode');
  });

  it('uses 2.4 GHz defaults for the backhaul', () => {
    const b = samplePayload().backhaul;
    expect(checkMeshCredentials(b, { isHalow: false })).toEqual([]);
    b.bandwidthMhz = 8;
    expect(checkMeshCredentials(b, { isHalow: false }).map(i => i.field)).toContain('bandwidthMhz');
  });

  it('returns one issue for missing credentials', () => {
    expect(checkMeshCredentials(undefined, {})).toHaveLength(1);
  });
});

describe('HtModeHelpers', () => {
  it('maps S1G widths regardless of availability', () => {
    expect(htModeForBandwidth(1, [])).toBe(WifiHTMode.WIFI_HT_MODE_S1G_1MHZ);
    expect(htModeForBandwidth(8, [])).toBe(WifiHTMode.WIFI_HT_MODE_S1G_8MHZ);
  });

  it('prefers the newest available mode of a width', () => {
    expect(htModeForBandwidth(20, [WifiHTMode.WIFI_HT_MODE_NOHT, WifiHTMode.WIFI_HT_MODE_HT20, WifiHTMode.WIFI_HT_MODE_HT40])).toBe(WifiHTMode.WIFI_HT_MODE_HT20);
    expect(htModeForBandwidth(40, [WifiHTMode.WIFI_HT_MODE_HT40, WifiHTMode.WIFI_HT_MODE_HE40])).toBe(WifiHTMode.WIFI_HT_MODE_HE40);
  });

  it('falls back to HE when nothing is advertised', () => {
    expect(htModeForBandwidth(20, [])).toBe(WifiHTMode.WIFI_HT_MODE_HE20);
    expect(htModeForBandwidth(999, [])).toBe(WifiHTMode.WIFI_HT_MODE_UNSPECIFIED);
  });

  it('inverts to MHz', () => {
    expect(bandwidthMhzForHTMode(WifiHTMode.WIFI_HT_MODE_S1G_8MHZ)).toBe(8);
    expect(bandwidthMhzForHTMode(WifiHTMode.WIFI_HT_MODE_HE40)).toBe(40);
    expect(bandwidthMhzForHTMode(WifiHTMode.WIFI_HT_MODE_VHT160)).toBe(160);
    expect(bandwidthMhzForHTMode(WifiHTMode.WIFI_HT_MODE_UNSPECIFIED)).toBe(0);
  });
});
