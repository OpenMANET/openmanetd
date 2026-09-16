// =============================================================================
// meshJoinApi.js — MeshJoinService client (share / join a mesh by QR)
// =============================================================================

import { createClient } from '@connectrpc/connect';
import { transport } from './connectClient.js';
import { MeshJoinService } from '../gen/openmanet/mesh_join/v1/mesh_join_service_pb.js';

const client = createClient(MeshJoinService, transport);

// getMeshJoinQR returns { payload, payloadText, svg } for this node.
export function getMeshJoinQR() {
  return client.getMeshJoinQR({});
}

// applyMeshJoin writes the scanned credentials to this node's radios.
// request: { payload, halowRadio, backhaulRadio }.
export function applyMeshJoin(request) {
  return client.applyMeshJoin(request);
}
