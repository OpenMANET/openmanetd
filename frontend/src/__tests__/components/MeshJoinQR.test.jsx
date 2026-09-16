// =============================================================================
// MeshJoinQR.test.jsx — QR panel states
// =============================================================================

import React from 'react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/react';
import { Code, ConnectError, createRouterTransport } from '@connectrpc/connect';
import { MeshJoinService } from '../../gen/openmanet/mesh_join/v1/mesh_join_service_pb.js';

vi.mock('../../services/connectClient.js', () => ({ transport: {} }));

const RESPONSE = {
  payloadText: 'OPENMANET1:AAAA',
  svg: '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><path fill="currentColor" d="M0 0h1v1h-1z"/></svg>',
  payload: {
    sourceHostname: 'alpha',
    halow: { meshId: 'field-mesh', passphrase: 'correct-horse', encryption: 1, bandwidthMhz: 8, channel: 44, countryCode: 'US' },
    backhaul: { meshId: 'field-mesh-2g', passphrase: 'backhaul-pass', encryption: 1, bandwidthMhz: 20, channel: 8, countryCode: 'US' },
  },
};

async function renderWith(handlers, props = {}) {
  vi.resetModules();
  const transport = createRouterTransport(({ service }) => { service(MeshJoinService, handlers); });
  vi.doMock('../../services/connectClient.js', () => ({ transport }));
  const { default: MeshJoinQR } = await import('../../components/MeshJoinQR.jsx');
  return render(<MeshJoinQR {...props} />);
}

beforeEach(() => vi.resetModules());
afterEach(() => { cleanup(); vi.restoreAllMocks(); });

describe('MeshJoinQR', () => {
  it('shows a loading state first', async () => {
    let release;
    await renderWith({ getMeshJoinQR: () => new Promise(r => { release = r; }) });
    expect(screen.getByText('Loading…')).toBeInTheDocument();
    // createRouterTransport dispatches through a Fetch-like Request/Response
    // internally, so the handler isn't invoked until a later macrotask —
    // wait for it before resolving, rather than assuming synchronous dispatch.
    await waitFor(() => expect(release).toBeDefined());
    release(RESPONSE);
    await waitFor(() => screen.getByText('field-mesh'));
  });

  it('renders the QR and a masked summary', async () => {
    const { container } = await renderWith({ getMeshJoinQR: () => RESPONSE });
    await waitFor(() => screen.getByText('field-mesh'));
    expect(container.querySelector('.lat-qr svg')).not.toBeNull();
    expect(screen.getByText('alpha')).toBeInTheDocument();
    expect(screen.getByText('44 · 8 MHz · US')).toBeInTheDocument();
    expect(screen.getByText('field-mesh-2g')).toBeInTheDocument();
    expect(screen.getByText('8 · 20 MHz · US')).toBeInTheDocument();
    expect(screen.queryByText('correct-horse')).toBeNull();
    expect(screen.getAllByText('••••••••')).toHaveLength(2);
  });

  it('reveals passphrases on demand', async () => {
    await renderWith({ getMeshJoinQR: () => RESPONSE });
    await waitFor(() => screen.getByText('field-mesh'));
    fireEvent.click(screen.getByRole('button', { name: 'Reveal' }));
    expect(screen.getByText('correct-horse')).toBeInTheDocument();
    expect(screen.getByText('backhaul-pass')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Hide' }));
    expect(screen.queryByText('correct-horse')).toBeNull();
  });

  it('shows "none" when there is no backhaul', async () => {
    const resp = { ...RESPONSE, payload: { ...RESPONSE.payload, backhaul: undefined } };
    await renderWith({ getMeshJoinQR: () => resp });
    await waitFor(() => screen.getByText('field-mesh'));
    expect(screen.getByText('none')).toBeInTheDocument();
  });

  it('shows the empty state when the node has no HaLow mesh', async () => {
    await renderWith({
      getMeshJoinQR: () => { throw new ConnectError('no HaLow mesh interface configured', Code.FailedPrecondition); },
    });
    await waitFor(() => screen.getByText('No HaLow mesh configured.'));
  });

  it('shows other errors in a crit alert', async () => {
    // A real server handler wraps failures with connect.NewError(...), so the
    // client sees a ConnectError carrying the original message (see
    // .claude/rules/api-design.md). createRouterTransport sanitizes any
    // non-ConnectError thrown by a handler into a generic "internal error"
    // (connect-es protocol-connect/handler-factory.js), so a plain Error here
    // would not exercise the real wire behavior — throw ConnectError instead.
    await renderWith({ getMeshJoinQR: () => { throw new ConnectError('uci read failed', Code.Internal); } });
    await waitFor(() => expect(screen.getByText(/uci read failed/)).toBeInTheDocument());
  });

  it('refetches when refreshKey changes', async () => {
    const getMeshJoinQR = vi.fn(() => RESPONSE);
    const view = await renderWith({ getMeshJoinQR }, { refreshKey: 0 });
    await waitFor(() => screen.getByText('field-mesh'));
    const { default: MeshJoinQR } = await import('../../components/MeshJoinQR.jsx');
    view.rerender(<MeshJoinQR refreshKey={1} />);
    await waitFor(() => expect(getMeshJoinQR).toHaveBeenCalledTimes(2));
  });
});
