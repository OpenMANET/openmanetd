import { describe, it, expect } from 'vitest';
import {
  channelsForCountryBandwidth, bandwidthsForCountry, findCountryEntry,
  BACKHAUL_CHANNELS_2G, BACKHAUL_BANDWIDTHS, meshJoinIssues,
  backhaulFootprint, formatBackhaulFootprint,
} from '../../pages/setup/meshChannels.js';
import { initialState } from '../../contexts/SetupContext.jsx';
import { WifiEncryption } from '../../gen/openmanet/wifi_config/v1/wifi_config_pb.js';

const COUNTRIES = [
  { code: 'US', name: 'USA', bandwidths: [{ mhz: 2, channels: [2, 6, 42] }, { mhz: 8, channels: [12, 28, 44] }] },
  { code: 'EU', name: 'Europe', bandwidths: [{ mhz: 1, channels: [1, 3] }, { mhz: 2, channels: [2] }] },
];
const STATUS = { radios: [{ name: 'radio1', band: 's1g', isHalow: true }], countries: COUNTRIES };

describe('MeshChannels', () => {
  it('reads channels per country and bandwidth with a US fallback', () => {
    expect(channelsForCountryBandwidth(findCountryEntry(COUNTRIES, 'US'), 8)).toEqual([12, 28, 44]);
    expect(channelsForCountryBandwidth(undefined, 8)).toEqual([12, 28, 44]);
    expect(bandwidthsForCountry(findCountryEntry(COUNTRIES, 'EU'))).toEqual([1, 2]);
    expect(bandwidthsForCountry(undefined)).toEqual([1, 2, 4, 8]);
  });

  it('exposes the static 2.4 GHz backhaul lists', () => {
    expect(BACKHAUL_CHANNELS_2G).toEqual([1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11]);
    expect(BACKHAUL_BANDWIDTHS).toEqual([20, 40]);
  });

  it('describes the 40 MHz footprint with upward pairing on channels 1-7 and downward on 8-11', () => {
    expect(backhaulFootprint(8, 40)).toEqual({ channel: 8, secondary: 4, startMhz: 2417, endMhz: 2457 });
    expect(backhaulFootprint(1, 40)).toEqual({ channel: 1, secondary: 5, startMhz: 2402, endMhz: 2442 });
    expect(backhaulFootprint(7, 40)).toEqual({ channel: 7, secondary: 11, startMhz: 2432, endMhz: 2472 });
    expect(backhaulFootprint(11, 40)).toEqual({ channel: 11, secondary: 7, startMhz: 2432, endMhz: 2472 });
  });

  it('describes a 20 MHz footprint with no secondary channel', () => {
    expect(backhaulFootprint(6, 20)).toEqual({ channel: 6, secondary: null, startMhz: 2427, endMhz: 2447 });
    expect(backhaulFootprint(1, 20)).toEqual({ channel: 1, secondary: null, startMhz: 2402, endMhz: 2422 });
  });

  it('returns null for a channel or width outside the 2.4 GHz backhaul lists', () => {
    expect(backhaulFootprint(12, 40)).toBeNull();
    expect(backhaulFootprint(0, 40)).toBeNull();
    expect(backhaulFootprint(6, 80)).toBeNull();
  });

  it('formats a footprint for the wizard', () => {
    expect(formatBackhaulFootprint(backhaulFootprint(8, 40))).toBe('ch 8 + ch 4 · 2417–2457 MHz');
    expect(formatBackhaulFootprint(backhaulFootprint(6, 20))).toBe('ch 6 · 2427–2447 MHz');
    expect(formatBackhaulFootprint(null)).toBe('');
  });
});

describe('MeshJoinIssues', () => {
  const scanned = (mesh) => ({
    ...initialState,
    mesh: { ...initialState.mesh, meshId: 'field-mesh', passphrase: 'correct-horse', encryption: WifiEncryption.SAE, ...mesh, fromScan: true },
  });

  it('is empty before any scan', () => {
    expect(meshJoinIssues(initialState, STATUS)).toEqual([]);
  });

  it('is empty for a legal scanned tuple', () => {
    expect(meshJoinIssues(scanned({ countryCode: 'US', bandwidthMhz: 8, channel: 44 }), STATUS)).toEqual([]);
  });

  it('names an illegal channel for the scanned country', () => {
    const issues = meshJoinIssues(scanned({ countryCode: 'EU', bandwidthMhz: 8, channel: 44 }), STATUS);
    expect(issues).toHaveLength(1);
    expect(issues[0]).toMatch(/8 MHz is not available/);
  });

  it('checks a scanned backhaul entry', () => {
    const state = {
      ...scanned({ countryCode: 'US', bandwidthMhz: 8, channel: 44 }),
      aps: [{ radioName: 'radio0', enabled: false, meshBackhaul: true, backhaulMeshId: 'x', backhaulPassphrase: 'backhaul-pass',
        backhaulBandwidthMhz: 20, backhaulChannel: 14, backhaulCountryCode: 'US', backhaulFromScan: true }],
    };
    const issues = meshJoinIssues(state, STATUS);
    expect(issues).toHaveLength(1);
    expect(issues[0]).toMatch(/radio0: Channel 14/);
  });

  it('validates a scanned backhaul with a zero width as the 40 MHz default', () => {
    const state = {
      ...scanned({ countryCode: 'US', bandwidthMhz: 8, channel: 44 }),
      aps: [{ radioName: 'radio0', enabled: false, meshBackhaul: true, backhaulMeshId: 'x', backhaulPassphrase: 'backhaul-pass',
        backhaulBandwidthMhz: 0, backhaulChannel: 0, backhaulCountryCode: 'US', backhaulFromScan: true }],
    };
    expect(meshJoinIssues(state, STATUS)).toEqual([]);
  });
});
