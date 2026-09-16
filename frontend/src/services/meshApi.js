// =============================================================================
// meshApi.js — Mesh network status via ConnectRPC
// =============================================================================
//
// Fetches mesh network status directly from the openmanetd ConnectRPC backend.
//
// Primary path: a single MeshTopologyService.GetMeshSnapshot call bundles
// service status, known nodes, direct neighbors, and wireless interfaces
// into one round-trip.
//
// Fallback path: when the daemon does not implement the snapshot RPC (older
// firmware), we fall back to the four parallel service RPCs:
//   StatusService.GetServiceStatus
//   NodeService.ListNodes
//   MeshNeighborService.ListMeshNeighbors
//   InterfaceService.ListWirelessInterfaces

import { Code, ConnectError, createClient } from "@connectrpc/connect";
import { transport } from "./connectClient.js";
import { StatusService } from "../gen/openmanet/service/v1/status_pb.js";
import { NodeService } from "../gen/openmanet/service/v1/node_pb.js";
import { MeshNeighborService } from "../gen/openmanet/service/v1/mesh_pb.js";
import { InterfaceService } from "../gen/openmanet/service/v1/interface_pb.js";
import { MeshTopologyService } from "../gen/openmanet/mesh_topology/v1/mesh_topology_service_pb.js";

const statusClient = createClient(StatusService, transport);
const nodeClient = createClient(NodeService, transport);
const meshClient = createClient(MeshNeighborService, transport);
const ifaceClient = createClient(InterfaceService, transport);
const topologyClient = createClient(MeshTopologyService, transport);

// Cache the "this daemon does not implement GetMeshSnapshot" decision so we
// stop hitting the new RPC every tick once we've confirmed it is missing.
let snapshotUnsupported = false;

function mapStatus(st) {
  return {
    connected: st?.isConnected ?? false,
    neighbors: st?.connectedNeighbors ?? 0,
    mesh_interfaces: st?.activeMeshInterfaces ?? 0,
    is_gateway: st?.isMeshGateway ?? false,
    selected_gateway_mac: st?.selectedGatewayMac ?? '',
  };
}

function mapNodes(nodes) {
  return (nodes ?? []).map((n) => ({ hostname: n.hostname, ip: n.ipaddr }));
}

// mapLinkRate flattens a MeshNeighbor.LinkRate; null when the daemon
// predates the field. phy is the numeric LinkRate.Phy enum
// (0 unspecified, 1 legacy, 2 HT, 3 VHT, 4 HE, 5 EHT); mcs/nss are -1
// when the driver did not report them.
function mapLinkRate(r) {
  if (!r) return null;
  return {
    bitrateKbps: r.bitrateKbps ?? 0,
    phy: r.phy ?? 0,
    widthMhz: r.widthMhz ?? 0,
    mcs: r.mcs ?? -1,
    nss: r.nss ?? -1,
  };
}

function mapNeighbors(neighbors) {
  return (neighbors ?? []).map((n) => ({
    name: n.neighbor,
    mac: n.hardwareAddress,
    signal: n.signal,
    throughput: n.throughput,
    iface: n.interface ?? '',
    tx: mapLinkRate(n.tx),
    rx: mapLinkRate(n.rx),
  }));
}

function mapInterfaces(interfaces) {
  return (interfaces ?? []).map((i) => ({
    name: i.name,
    type: i.interfaceType,
    frequency: i.frequency,
    channel_width: i.channelWidth,
  }));
}

// fetchMeshStatus() — single-RPC primary path with four-RPC fallback.
//
// Returns an object with four keys (status, nodes, neighbors, interfaces).
// Each value is the mapped response if the call succeeded, or null if it
// failed.  The caller is responsible for handling null values gracefully.
export async function fetchMeshStatus() {
  if (!snapshotUnsupported) {
    try {
      const resp = await topologyClient.getMeshSnapshot({});
      return {
        status: resp.status ? mapStatus(resp.status) : null,
        nodes: mapNodes(resp.nodes),
        neighbors: mapNeighbors(resp.neighbors),
        interfaces: mapInterfaces(resp.interfaces),
      };
    } catch (err) {
      if (err instanceof ConnectError && err.code === Code.Unimplemented) {
        // Older daemon — remember and don't retry the new RPC.
        snapshotUnsupported = true;
      } else {
        // Any other failure: fall through to the legacy fan-out so the
        // caller still gets best-effort partial data.
      }
    }
  }

  return fetchMeshStatusLegacy();
}

async function fetchMeshStatusLegacy() {
  const [statusRes, nodesRes, neighborsRes, ifacesRes] = await Promise.allSettled([
    statusClient.getServiceStatus({}).then((resp) => mapStatus(resp.status)),
    nodeClient.listNodes({}).then((resp) => mapNodes(resp.nodes)),
    meshClient.listMeshNeighbors({}).then((resp) => mapNeighbors(resp.neighbors)),
    ifaceClient.listWirelessInterfaces({}).then((resp) => mapInterfaces(resp.interfaces)),
  ]);

  return {
    status: statusRes.status === 'fulfilled' ? statusRes.value : null,
    nodes: nodesRes.status === 'fulfilled' ? nodesRes.value : null,
    neighbors: neighborsRes.status === 'fulfilled' ? neighborsRes.value : null,
    interfaces: ifacesRes.status === 'fulfilled' ? ifacesRes.value : null,
  };
}

// __resetSnapshotSupportForTests is a hatch for unit tests that exercise
// both the primary and fallback paths within a single test file.
export function __resetSnapshotSupportForTests() {
  snapshotUnsupported = false;
}

// -----------------------------------------------------------------------------
// fetchMeshTopology()
// -----------------------------------------------------------------------------
// Calls MeshTopologyService.GetMeshTopology and returns a plain-JS object
// suitable for the TopologyMap component. The wire format is the full
// mesh graph from batadv-vis + Alfred, with each vis node enriched by
// this node's bat-hosts entries and originator-table overlay:
//
//   {
//     selfMac, selfHostname, algorithm,            // who we are + batman version
//     collectedAt: Date | null,
//     gossipCoverage: { published, total },        // how many peers publish neighbor gossip
//     nodes: [
//       {
//         mac, secondaryMacs, hostname,
//         segment,                                   // "local" | "remote"
//         hopsFromSelf,                              // 0 self, 99 unknown
//         myHardIfname,                              // local ifname I'd forward on, "" if unknown
//         isSelf,
//         gossipStale,                               // true when the node's gossip record is missing/stale
//         gossipAgeSeconds,                          // seconds since publisher's collected_at, or -1 if no record
//       }
//     ],
//     edges: [
//       {
//         fromMac, toMac, metric,
//         blos,                                      // endpoints in different segments
//         onMyPath,                                  // matches my (orig, next-hop) pair
//       }
//     ],
//   }
//
// Returns null on failure. Callers treat null as "topology temporarily
// unavailable" and render accordingly.
export async function fetchMeshTopology() {
  try {
    const resp = await topologyClient.getMeshTopology({});
    const t = resp.topology;
    if (!t) return null;

    const collectedAt = t.collectedAt
      ? new Date(Number(t.collectedAt.seconds) * 1000)
      : null;

    return {
      selfMac: t.selfMac ?? '',
      selfHostname: t.selfHostname ?? '',
      algorithm: t.algorithm ?? '',
      collectedAt,
      gossipCoverage: t.gossipCoverage
        ? {
            published: t.gossipCoverage.published ?? 0,
            total: t.gossipCoverage.total ?? 0,
          }
        : null,
      nodes: (t.nodes ?? []).map((n) => ({
        mac: n.mac ?? '',
        secondaryMacs: n.secondaryMacs ?? [],
        hostname: n.hostname ?? '',
        segment: n.segment ?? 'local',
        remoteGatewayMac: n.remoteGatewayMac ?? '',
        hopsFromSelf: n.hopsFromSelf ?? 0,
        myHardIfname: n.myHardIfname ?? '',
        isSelf: n.isSelf ?? false,
        isGateway: n.isGateway ?? false,
        gossipStale: n.gossipStale ?? false,
        gossipAgeSeconds: Number.isFinite(n.gossipAgeSeconds) ? n.gossipAgeSeconds : -1,
      })),
      edges: (t.edges ?? []).map((e) => ({
        fromMac: e.fromMac ?? '',
        toMac: e.toMac ?? '',
        metric: e.metric ?? 0,
        blos: e.blos ?? false,
        onMyPath: e.onMyPath ?? false,
      })),
    };
  } catch {
    return null;
  }
}

// -----------------------------------------------------------------------------
// fetchMeshTopologyDelta()
// -----------------------------------------------------------------------------
// Calls MeshTopologyService.GetMeshTopologyDelta and returns the aggregated
// churn counters over the requested window (default 60s). Returns null when
// the call fails or the delta tracker has not yet collected enough samples
// (actualWindow <= 1s). Callers should render em-dashes when null is
// returned.
//
// Shape:
//   {
//     routesAdded: number,
//     routesLost: number,
//     gatewayChanges: number,
//     reconvergeMs: number,
//     actualWindowMs: number,
//   }
export async function fetchMeshTopologyDelta(windowSeconds = 60) {
  try {
    const resp = await topologyClient.getMeshTopologyDelta({
      window: { seconds: BigInt(windowSeconds), nanos: 0 },
    });

    const actualWindowMs = durationToMs(resp.actualWindow);
    if (actualWindowMs < 1000) return null;

    return {
      routesAdded: resp.routesAdded ?? 0,
      routesLost: resp.routesLost ?? 0,
      gatewayChanges: resp.gatewayChanges ?? 0,
      reconvergeMs: durationToMs(resp.reconverge),
      actualWindowMs,
    };
  } catch {
    return null;
  }
}

function durationToMs(d) {
  if (!d) return 0;
  const seconds = Number(d.seconds ?? 0);
  const nanos = Number(d.nanos ?? 0);
  return seconds * 1000 + Math.floor(nanos / 1_000_000);
}
