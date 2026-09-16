// =============================================================================
// commsApi.js — Comms service status via ConnectRPC
// =============================================================================
//
// Fetches comms status (active channel, codec, ptime, RTT) from the
// openmanetd ConnectRPC backend. Thin wrapper over CommsService.GetCommsStatus.

import { createClient } from '@connectrpc/connect';
import { transport } from './connectClient.js';
import { CommsService } from '../gen/openmanet/comms/v1/comms_service_pb.js';

const commsClient = createClient(CommsService, transport);

// Fetches comms status. Returns a mapped object on success, or null on error
// (caller is expected to render placeholders when null).
//
// Shape:
//   {
//     activeChannel: number,    // 0 when no channel is active
//     availableChannels: number[],
//     codec: string,            // "OPUS 32K" or '' when disabled
//     ptimeMs: number,          // 20 or 0 when disabled
//     rttMs: number,            // 0 when no measurement available
//     callsign: string,
//   }
export async function fetchCommsStatus() {
  try {
    const resp = await commsClient.getCommsStatus({});
    return {
      activeChannel: resp.activeTalkgroup ?? 0,
      availableChannels: resp.availableTalkgroups ?? [],
      codec: resp.codec ?? '',
      ptimeMs: resp.ptimeMs ?? 0,
      rttMs: resp.roundTripMs ?? 0,
      callsign: resp.callsign ?? '',
    };
  } catch {
    return null;
  }
}

// Fetches the device's hardware mixer state (AudioMixerState message:
// { available, speakerVolume?, micVolume?, agcEnabled?, speakerControl,
// micControl, agcControl }). Returns null on transport error.
export async function fetchAudioMixer() {
  try {
    const resp = await commsClient.getAudioMixer({});
    return resp.state ?? null;
  } catch {
    return null;
  }
}

// Applies a partial device mixer update. fields: { speakerVolume?,
// micVolume?, agcEnabled? } — omit a field to leave it unchanged.
// Returns the resulting state, or null on error.
export async function updateAudioMixer(fields) {
  try {
    const resp = await commsClient.updateAudioMixer(fields);
    return resp.state ?? null;
  } catch {
    return null;
  }
}
