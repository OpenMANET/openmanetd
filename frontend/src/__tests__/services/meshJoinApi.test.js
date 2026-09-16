// =============================================================================
// meshJoinApi.test.js — MeshJoinService client wrappers
// =============================================================================

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { createRouterTransport } from '@connectrpc/connect';
import { MeshJoinService } from '../../gen/openmanet/mesh_join/v1/mesh_join_service_pb.js';

vi.mock('../../services/connectClient.js', () => ({ transport: {} }));

describe('TestMeshJoinApi', () => {
  beforeEach(() => {
    vi.resetModules();
  });

  async function withTransport(transport) {
    vi.doMock('../../services/connectClient.js', () => ({ transport }));
    return import('../../services/meshJoinApi.js');
  }

  it('getMeshJoinQR returns the response untouched', async () => {
    const transport = createRouterTransport(({ service }) => {
      service(MeshJoinService, {
        getMeshJoinQR() {
          return { payloadText: 'OPENMANET1:AAAA', svg: '<svg />', payload: { sourceHostname: 'alpha' } };
        },
      });
    });
    const { getMeshJoinQR } = await withTransport(transport);
    const resp = await getMeshJoinQR();
    expect(resp.payloadText).toBe('OPENMANET1:AAAA');
    expect(resp.payload.sourceHostname).toBe('alpha');
  });

  it('applyMeshJoin forwards the request and returns radios', async () => {
    const seen = [];
    const transport = createRouterTransport(({ service }) => {
      service(MeshJoinService, {
        applyMeshJoin(req) {
          seen.push(req);
          return { radios: [{ radioName: 'radio3', role: 1, status: 1 }] };
        },
      });
    });
    const { applyMeshJoin } = await withTransport(transport);
    const resp = await applyMeshJoin({ halowRadio: 'radio3', payload: { sourceHostname: 'alpha', halow: { meshId: 'm', passphrase: 'correct-horse', encryption: 1, bandwidthMhz: 8, channel: 44 } } });
    expect(seen[0].halowRadio).toBe('radio3');
    expect(resp.radios[0].radioName).toBe('radio3');
  });

  it('propagates RPC errors', async () => {
    const transport = createRouterTransport(({ service }) => {
      service(MeshJoinService, {
        getMeshJoinQR() { throw new Error('no HaLow mesh interface configured'); },
      });
    });
    const { getMeshJoinQR } = await withTransport(transport);
    // A handler throwing a plain Error (not a ConnectError) is deliberately
    // sanitized by @connectrpc/connect's server protocol to a generic
    // "internal error" ConnectError before it crosses the wire, so the
    // original message text does not survive — see
    // protocol-connect/handler-factory.js and the same assertion style in
    // src/__tests__/sysupgradeApi.test.js's "propagates errors from the RPC".
    await expect(getMeshJoinQR()).rejects.toThrow();
  });
});
