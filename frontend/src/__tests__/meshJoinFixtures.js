// meshJoinFixtures.js — shared fixtures for mesh-join tests. NOT a .test.js
// file, so Vitest never collects it as a suite; importing it into a test
// file therefore does not re-register any describe/it blocks.
import { create, toBinary } from '@bufbuild/protobuf';
import { base64Encode } from '@bufbuild/protobuf/wire';
import { MeshJoinPayloadSchema } from '../gen/openmanet/mesh_join/v1/mesh_join_pb.js';
import { WifiEncryption } from '../gen/openmanet/wifi_config/v1/wifi_config_pb.js';
import { MESH_JOIN_PREFIX } from '../utils/meshJoin.js';

export function samplePayload(overrides = {}) {
  return create(MeshJoinPayloadSchema, {
    sourceHostname: 'alpha',
    halow: {
      meshId: 'field-mesh', passphrase: 'correct-horse', encryption: WifiEncryption.SAE,
      bandwidthMhz: 8, channel: 44, countryCode: 'US',
    },
    backhaul: {
      meshId: 'field-mesh-2g', passphrase: 'backhaul-pass', encryption: WifiEncryption.SAE,
      bandwidthMhz: 20, channel: 8, countryCode: 'US',
    },
    ...overrides,
  });
}

export function encodePayload(payload) {
  return MESH_JOIN_PREFIX + base64Encode(toBinary(MeshJoinPayloadSchema, payload), 'url');
}
