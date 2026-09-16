// =============================================================================
// meshApi.test.js — Tests for mesh network status API (ConnectRPC)
// =============================================================================

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { createRouterTransport } from '@connectrpc/connect';
import { StatusService } from '../gen/openmanet/service/v1/status_pb.js';
import { NodeService } from '../gen/openmanet/service/v1/node_pb.js';
import { MeshNeighborService } from '../gen/openmanet/service/v1/mesh_pb.js';
import { InterfaceService } from '../gen/openmanet/service/v1/interface_pb.js';

// We mock the connectClient module so fetchMeshStatus uses our test transport.
vi.mock('../services/connectClient.js', () => ({ transport: {} }));

describe('TestFetchMeshStatus', () => {
  beforeEach(() => {
    vi.resetModules();
  });

  async function fetchWithTransport(transport) {
    // Mock the module with the given transport, then dynamically import.
    vi.doMock('../services/connectClient.js', () => ({ transport }));
    const { fetchMeshStatus } = await import('../services/meshApi.js');
    return fetchMeshStatus();
  }

  it('returns mapped data from all 4 services', async () => {
    const transport = createRouterTransport(({ service }) => {
      service(StatusService, {
        getServiceStatus() {
          return {
            status: {
              isConnected: true,
              connectedNeighbors: 3,
              activeMeshInterfaces: 2,
              isMeshGateway: true,
            },
          };
        },
      });
      service(NodeService, {
        listNodes() {
          return {
            nodes: [{ hostname: 'node1', ipaddr: '10.0.0.1' }],
          };
        },
      });
      service(MeshNeighborService, {
        listMeshNeighbors() {
          return {
            neighbors: [{
              neighbor: 'node2',
              hardwareAddress: 'aa:bb:cc:dd:ee:ff',
              signal: -50,
              throughput: 100,
            }],
          };
        },
      });
      service(InterfaceService, {
        listWirelessInterfaces() {
          return {
            interfaces: [{
              name: 'wlh0',
              interfaceType: 'mesh',
              frequency: 5180,
              channelWidth: 80,
            }],
          };
        },
      });
    });

    const result = await fetchWithTransport(transport);

    expect(result.status).toEqual({
      connected: true,
      neighbors: 3,
      mesh_interfaces: 2,
      is_gateway: true,
      selected_gateway_mac: '',
    });
    expect(result.nodes).toEqual([{ hostname: 'node1', ip: '10.0.0.1' }]);
    expect(result.neighbors).toEqual([{
      name: 'node2', mac: 'aa:bb:cc:dd:ee:ff', signal: -50, throughput: 100,
      iface: '', tx: null, rx: null,
    }]);
    expect(result.interfaces).toEqual([{
      name: 'wlh0', type: 'mesh', frequency: 5180, channel_width: 80,
    }]);
  });

  it('maps link rates and the interface name when present', async () => {
    const transport = createRouterTransport(({ service }) => {
      service(StatusService, { getServiceStatus() { return { status: {} }; } });
      service(NodeService, { listNodes() { return { nodes: [] }; } });
      service(InterfaceService, { listWirelessInterfaces() { return { interfaces: [] }; } });
      service(MeshNeighborService, {
        listMeshNeighbors() {
          return {
            neighbors: [{
              neighbor: 'node2', hardwareAddress: 'aa:bb:cc:dd:ee:ff', signal: -50, throughput: 100,
              interface: 'mesh1',
              tx: { bitrateKbps: 86700, phy: 4, widthMhz: 40, mcs: 7, nss: 2 },
              rx: { bitrateKbps: 72200, phy: 2, widthMhz: 20, mcs: 7, nss: 1 },
            }],
          };
        },
      });
    });

    const result = await fetchWithTransport(transport);

    expect(result.neighbors).toEqual([{
      name: 'node2', mac: 'aa:bb:cc:dd:ee:ff', signal: -50, throughput: 100,
      iface: 'mesh1',
      tx: { bitrateKbps: 86700, phy: 4, widthMhz: 40, mcs: 7, nss: 2 },
      rx: { bitrateKbps: 72200, phy: 2, widthMhz: 20, mcs: 7, nss: 1 },
    }]);
  });

  it('returns null for failed services', async () => {
    const transport = createRouterTransport(({ service }) => {
      service(StatusService, {
        getServiceStatus() {
          return {
            status: {
              isConnected: true,
              connectedNeighbors: 1,
              activeMeshInterfaces: 1,
              isMeshGateway: false,
            },
          };
        },
      });
      // Other services not registered — calls will fail.
    });

    const result = await fetchWithTransport(transport);

    expect(result.status).toEqual({
      connected: true,
      neighbors: 1,
      mesh_interfaces: 1,
      is_gateway: false,
      selected_gateway_mac: '',
    });
    expect(result.nodes).toBeNull();
    expect(result.neighbors).toBeNull();
    expect(result.interfaces).toBeNull();
  });

  it('handles all services failing gracefully', async () => {
    // Empty router — no services registered.
    const transport = createRouterTransport(() => {});

    const result = await fetchWithTransport(transport);

    expect(result.status).toBeNull();
    expect(result.nodes).toBeNull();
    expect(result.neighbors).toBeNull();
    expect(result.interfaces).toBeNull();
  });
});
