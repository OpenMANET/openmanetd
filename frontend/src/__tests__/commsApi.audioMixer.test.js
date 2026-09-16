// =============================================================================
// commsApi.audioMixer.test.js — Device mixer API wrappers (ConnectRPC)
// =============================================================================

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { createRouterTransport } from '@connectrpc/connect';
import { CommsService } from '../gen/openmanet/comms/v1/comms_service_pb.js';

vi.mock('../services/connectClient.js', () => ({ transport: {} }));

describe('TestAudioMixerApi', () => {
  beforeEach(() => {
    vi.resetModules();
  });

  async function importWithTransport(transport) {
    vi.doMock('../services/connectClient.js', () => ({ transport }));
    return import('../services/commsApi.js');
  }

  it('fetchAudioMixer returns the state message', async () => {
    const transport = createRouterTransport(({ service }) => {
      service(CommsService, {
        getAudioMixer() {
          return {
            state: {
              available: true,
              speakerVolume: 70,
              micVolume: 55,
              agcEnabled: true,
              speakerControl: 'Master',
            },
          };
        },
      });
    });

    const { fetchAudioMixer } = await importWithTransport(transport);
    const state = await fetchAudioMixer();

    expect(state.available).toBe(true);
    expect(state.speakerVolume).toBe(70);
    expect(state.agcEnabled).toBe(true);
  });

  it('fetchAudioMixer returns null on transport error', async () => {
    const transport = createRouterTransport(({ service }) => {
      service(CommsService, {
        getAudioMixer() {
          throw new Error('boom');
        },
      });
    });

    const { fetchAudioMixer } = await importWithTransport(transport);
    expect(await fetchAudioMixer()).toBeNull();
  });

  it('updateAudioMixer sends only provided fields and returns state', async () => {
    let received = null;
    const transport = createRouterTransport(({ service }) => {
      service(CommsService, {
        updateAudioMixer(req) {
          received = req;
          return { state: { available: true, speakerVolume: 45 } };
        },
      });
    });

    const { updateAudioMixer } = await importWithTransport(transport);
    const state = await updateAudioMixer({ speakerVolume: 45 });

    expect(received.speakerVolume).toBe(45);
    expect(received.micVolume).toBeUndefined();
    expect(state.speakerVolume).toBe(45);
  });
});
