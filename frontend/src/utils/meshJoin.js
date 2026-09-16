// =============================================================================
// meshJoin.js — decode and sanity-check a "share mesh" QR payload
// =============================================================================
//
// The QR text is "OPENMANET1:" + base64url(MeshJoinPayload). Decoding
// happens on the phone, never on the node, so this file has no transport
// dependency. checkMeshCredentials gives the UI a per-field issue list to
// render before anything is applied; the node re-validates on apply
// (ApplyMeshJoin / ApplySetup) against the regulatory database.

import { fromBinary } from '@bufbuild/protobuf';
import { base64Decode } from '@bufbuild/protobuf/wire';
import { MeshJoinPayloadSchema } from '../gen/openmanet/mesh_join/v1/mesh_join_pb.js';
import { WifiEncryption, WifiHTMode } from '../gen/openmanet/wifi_config/v1/wifi_config_pb.js';

export const MESH_JOIN_PREFIX = 'OPENMANET1:';
const VERSION_MARKER = /^OPENMANET\d+:/;

export class MeshJoinError extends Error {
  constructor(code, message) {
    super(message);
    this.name = 'MeshJoinError';
    this.code = code;
  }
}

export const MESH_JOIN_ERROR_MESSAGES = {
  'no-qr':               'No QR code found. Retake the photo with the whole code in frame.',
  'not-mesh-join':       'This QR code is not an OpenMANET mesh code.',
  'unsupported-version': 'This code is from a newer OpenMANET build. Update this node first.',
  'corrupt':             'Code unreadable. Retake the photo or paste the code text.',
  'no-canvas':           'This browser cannot decode photos here. Paste the code text instead.',
};

function fail(code) {
  return new MeshJoinError(code, MESH_JOIN_ERROR_MESSAGES[code]);
}

// decodeMeshJoinText parses QR text into a MeshJoinPayload message.
export function decodeMeshJoinText(text) {
  const trimmed = (text ?? '').trim();
  if (!trimmed.startsWith(MESH_JOIN_PREFIX)) {
    throw fail(VERSION_MARKER.test(trimmed) ? 'unsupported-version' : 'not-mesh-join');
  }

  const body = trimmed.slice(MESH_JOIN_PREFIX.length);
  if (!body) throw fail('corrupt');

  let bytes;
  try {
    bytes = base64Decode(body);
  } catch {
    throw fail('corrupt');
  }

  let payload;
  try {
    payload = fromBinary(MeshJoinPayloadSchema, bytes);
  } catch {
    throw fail('corrupt');
  }

  if (!payload.halow) throw fail('corrupt');
  return payload;
}

const HALOW_WIDTHS = [1, 2, 4, 8];
const BACKHAUL_WIDTHS = [20, 40];

// checkMeshCredentials returns [{ field, message }] for everything the
// target radio cannot accept. Lists are optional: an empty channel or
// country list means "unknown here, let the node decide".
export function checkMeshCredentials(creds, opts = {}) {
  if (!creds) return [{ field: 'payload', message: 'Missing mesh credentials.' }];

  const issues = [];
  const passphrase = creds.passphrase ?? '';
  const meshId = creds.meshId ?? '';
  const country = creds.countryCode ?? '';
  const bw = creds.bandwidthMhz;

  if (creds.encryption !== WifiEncryption.SAE) {
    issues.push({ field: 'encryption', message: 'Mesh must be WPA3 (SAE).' });
  }
  if (passphrase.length < 8 || passphrase.length > 63) {
    issues.push({ field: 'passphrase', message: 'Passphrase must be 8–63 characters.' });
  }
  if (meshId.length < 1 || meshId.length > 32) {
    issues.push({ field: 'meshId', message: 'Mesh ID must be 1–32 characters.' });
  }

  const widths = opts.bandwidths?.length ? opts.bandwidths : (opts.isHalow ? HALOW_WIDTHS : BACKHAUL_WIDTHS);
  if (!widths.includes(bw)) {
    issues.push({ field: 'bandwidthMhz', message: `${bw} MHz is not available on this radio.` });
  } else if (opts.channels?.length) {
    const legal = opts.channels.map(Number);
    if (!legal.includes(Number(creds.channel))) {
      const where = country ? ` in ${country}` : '';
      issues.push({ field: 'channel', message: `Channel ${creds.channel} is not legal at ${bw} MHz${where}.` });
    }
  }

  if (country && opts.countries?.length && !opts.countries.includes(country)) {
    issues.push({ field: 'countryCode', message: `Country ${country} is not in this device's regulatory list.` });
  }
  if (country && opts.countryMaxLen && country.length > opts.countryMaxLen) {
    issues.push({ field: 'countryCode', message: `Country code ${country} cannot be set here (${opts.countryMaxLen} letters max).` });
  }

  return issues;
}

// WifiHTMode's value names ("WIFI_HT_MODE_HE20", ...) share a prefix that
// protobuf-es's naive camelCase->snake_case splitter can't reconstruct from
// the type name "WifiHTMode" (it treats "HT" as two separate words), so no
// short-name aliases are generated for this enum — every value must be
// referenced by its full WIFI_HT_MODE_* key.
const S1G_MODES = {
  1: WifiHTMode.WIFI_HT_MODE_S1G_1MHZ,
  2: WifiHTMode.WIFI_HT_MODE_S1G_2MHZ,
  4: WifiHTMode.WIFI_HT_MODE_S1G_4MHZ,
  8: WifiHTMode.WIFI_HT_MODE_S1G_8MHZ,
};

// Candidates per width, newest standard first.
const WIDTH_MODES = {
  20:  [WifiHTMode.WIFI_HT_MODE_HE20, WifiHTMode.WIFI_HT_MODE_VHT20, WifiHTMode.WIFI_HT_MODE_HT20, WifiHTMode.WIFI_HT_MODE_NOHT],
  40:  [WifiHTMode.WIFI_HT_MODE_HE40, WifiHTMode.WIFI_HT_MODE_VHT40, WifiHTMode.WIFI_HT_MODE_HT40, WifiHTMode.WIFI_HT_MODE_HT40_PLUS, WifiHTMode.WIFI_HT_MODE_HT40_MINUS],
  80:  [WifiHTMode.WIFI_HT_MODE_HE80, WifiHTMode.WIFI_HT_MODE_VHT80],
  160: [WifiHTMode.WIFI_HT_MODE_HE160, WifiHTMode.WIFI_HT_MODE_VHT160],
};

// htModeForBandwidth picks the WifiHTMode for a width: S1G widths map
// directly; other widths prefer whatever the radio advertises, newest
// standard first, and fall back to HE when nothing is advertised.
export function htModeForBandwidth(mhz, available = []) {
  if (S1G_MODES[mhz] !== undefined) return S1G_MODES[mhz];
  const candidates = WIDTH_MODES[mhz] ?? [];
  return candidates.find(m => available.includes(m)) ?? candidates[0] ?? WifiHTMode.WIFI_HT_MODE_UNSPECIFIED;
}

const MODE_WIDTHS = (() => {
  const out = {};
  for (const [mhz, mode] of Object.entries(S1G_MODES)) out[mode] = Number(mhz);
  for (const [mhz, modes] of Object.entries(WIDTH_MODES)) for (const m of modes) out[m] = Number(mhz);
  return out;
})();

// bandwidthMhzForHTMode is the inverse of htModeForBandwidth; 0 when unknown.
export function bandwidthMhzForHTMode(mode) {
  return MODE_WIDTHS[mode] ?? 0;
}
