// =============================================================================
// meshChannels.js — regulatory helpers shared by the mesh, Wi-Fi, review
//                   and shell components of the wizard
// =============================================================================

import { checkMeshCredentials } from '../../utils/meshJoin.js';
import { WifiEncryption } from '../../gen/openmanet/wifi_config/v1/wifi_config_pb.js';

// Fallback US S1G channel allocations used only when the device's
// regulatory database (/usr/share/morse-regdb/channels.csv) was not
// loaded — e.g. on a developer machine without the Morse userspace
// package, or in unit tests that don't pass a fixture. Real devices
// always pull these per-country from GetSetupStatusResponse.countries.
//
// Reference: IEEE 802.11ah-2020 Annex E / Morse Micro firmware default
// regdom for US.
export const FALLBACK_US_CHANNELS = {
  1: [1, 3, 5, 7, 9, 11, 13, 15, 17, 19, 21, 23, 25, 27, 29, 31,
      33, 35, 37, 39, 41, 43, 45, 47, 49, 51],
  2: [2, 6, 10, 14, 18, 22, 26, 30, 34, 38, 42, 46, 50],
  4: [8, 16, 24, 32, 40, 48],
  8: [12, 28, 44],
};

// findCountryEntry returns the SetupCountry message for the given code,
// or undefined if not present.
export function findCountryEntry(countries, code) {
  if (!countries || !code) return undefined;
  return countries.find(c => c.code === code);
}

// channelsForCountryBandwidth returns the legal channel list for the
// chosen (country, bandwidth) tuple, falling back to a baked-in US
// allocation when the regdb is absent.
export function channelsForCountryBandwidth(countryEntry, bandwidthMhz) {
  if (countryEntry?.bandwidths) {
    const entry = countryEntry.bandwidths.find(b => b.mhz === bandwidthMhz);
    if (entry?.channels?.length) return Array.from(entry.channels);
  }
  return FALLBACK_US_CHANNELS[bandwidthMhz] ?? [];
}

// bandwidthsForCountry returns the bandwidths legal in this regulatory
// domain. Falls back to the four S1G widths when the regdb is empty.
export function bandwidthsForCountry(countryEntry) {
  if (countryEntry?.bandwidths?.length) {
    return countryEntry.bandwidths.map(b => b.mhz).sort((a, b) => a - b);
  }
  return [1, 2, 4, 8];
}

// The daemon pins the 2.4 GHz backhaul to the static 2.4 GHz list the
// settings API advertises; SetupRadio.bandwidths is not populated for
// non-HaLow radios.
export const BACKHAUL_BANDWIDTHS = [20, 40];
export const BACKHAUL_CHANNELS_2G = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11];

// backhaulFootprint describes the 2.4 GHz spectrum a backhaul choice
// occupies. channel is 1..11; bandwidthMhz is 20 or 40. At 40 MHz the
// primary pairs with a secondary four channels away: channels 1-7 pair
// upward (HT40+), 8-11 pair downward (HT40-), so any 40 MHz pair covers
// half the band. Returns null for any other input. The pairing is what
// OpenWrt's mesh path is expected to pick under noscan; the driver makes
// the final call on air.
export function backhaulFootprint(channel, bandwidthMhz) {
  if (!BACKHAUL_CHANNELS_2G.includes(channel) || !BACKHAUL_BANDWIDTHS.includes(bandwidthMhz)) return null;
  const primaryMhz = 2407 + 5 * channel;
  if (bandwidthMhz === 20) {
    return { channel, secondary: null, startMhz: primaryMhz - 10, endMhz: primaryMhz + 10 };
  }
  const secondary = channel <= 7 ? channel + 4 : channel - 4;
  const secondaryMhz = 2407 + 5 * secondary;
  return {
    channel,
    secondary,
    startMhz: Math.min(primaryMhz, secondaryMhz) - 10,
    endMhz: Math.max(primaryMhz, secondaryMhz) + 10,
  };
}

// formatBackhaulFootprint renders backhaulFootprint for the wizard:
// "ch 8 + ch 4 · 2417–2457 MHz" at 40 MHz, "ch 8 · 2437–2457 MHz" at 20.
export function formatBackhaulFootprint(fp) {
  if (!fp) return '';
  const chans = fp.secondary == null ? `ch ${fp.channel}` : `ch ${fp.channel} + ch ${fp.secondary}`;
  return `${chans} · ${fp.startMhz}–${fp.endMhz} MHz`;
}

// meshJoinIssues lists what a scanned code asks for that this device
// cannot accept. Only scanned values are checked (mesh.fromScan /
// ap.backhaulFromScan); manual entry is guarded by the snap effects.
export function meshJoinIssues(state, status) {
  const out = [];
  const countries = status?.countries ?? [];

  if (state.mesh.fromScan) {
    const entry = findCountryEntry(countries, state.mesh.countryCode);
    const creds = {
      meshId: state.mesh.meshId, passphrase: state.mesh.passphrase, encryption: state.mesh.encryption,
      bandwidthMhz: state.mesh.bandwidthMhz, channel: state.mesh.channel, countryCode: state.mesh.countryCode,
    };
    const opts = {
      isHalow: true,
      bandwidths: bandwidthsForCountry(entry),
      channels: channelsForCountryBandwidth(entry, state.mesh.bandwidthMhz),
      countries: countries.map(c => c.code),
    };
    for (const issue of checkMeshCredentials(creds, opts)) out.push(issue.message);
  }

  for (const ap of state.aps) {
    if (!ap.meshBackhaul || !ap.backhaulFromScan) continue;
    const creds = {
      meshId: ap.backhaulMeshId, passphrase: ap.backhaulPassphrase, encryption: WifiEncryption.SAE,
      bandwidthMhz: ap.backhaulBandwidthMhz || 40, channel: ap.backhaulChannel || 8, countryCode: ap.backhaulCountryCode,
    };
    const opts = { isHalow: false, bandwidths: BACKHAUL_BANDWIDTHS, channels: BACKHAUL_CHANNELS_2G };
    for (const issue of checkMeshCredentials(creds, opts)) out.push(`${ap.radioName}: ${issue.message}`);
  }

  return out;
}
