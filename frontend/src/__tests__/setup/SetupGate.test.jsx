// =============================================================================
// SetupGate.test.jsx — Branching logic for the wizard route gate
// =============================================================================
//
// Two layers: the pure helper `gateStateFromStatus` is table-tested so
// every branch is pinned without a router, then the component itself is
// rendered inside a MemoryRouter with setupClient mocked so the
// redirect / passthrough / fail-closed / dismissed behaviour is covered
// end to end.

import React from 'react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, act } from '@testing-library/react';
import { MemoryRouter, useLocation } from 'react-router-dom';

const setupState = { getSetupStatus: vi.fn() };

vi.mock('../../services/setupClient.js', () => ({
  setupClient: {
    getSetupStatus: (...args) => setupState.getSetupStatus(...args),
  },
}));

vi.mock('../../pages/setup/StepNoHalowRadio.jsx', () => ({
  default: () => <div data-testid="no-halow">NO HALOW</div>,
}));

import SetupGate, { gateStateFromStatus } from '../../components/SetupGate.jsx';
import { dismissSetup } from '../../services/setupDismiss.js';

describe('gateStateFromStatus', () => {
  it('returns "wizard-hidden" when setup is disabled', () => {
    expect(gateStateFromStatus({ isEnabled: false, isSetupComplete: false, hasHalowRadio: true }))
      .toBe('wizard-hidden');
  });

  it('returns "wizard-hidden" when setup is complete', () => {
    expect(gateStateFromStatus({ isEnabled: true, isSetupComplete: true, hasHalowRadio: true }))
      .toBe('wizard-hidden');
  });

  it('returns "no-halow" when enabled+incomplete but no HaLow radio', () => {
    expect(gateStateFromStatus({ isEnabled: true, isSetupComplete: false, hasHalowRadio: false }))
      .toBe('no-halow');
  });

  it('returns "wizard-active" when enabled+incomplete with HaLow radio', () => {
    expect(gateStateFromStatus({ isEnabled: true, isSetupComplete: false, hasHalowRadio: true }))
      .toBe('wizard-active');
  });

  it('returns "wizard-hidden" for an undefined status (fail-closed)', () => {
    expect(gateStateFromStatus(undefined)).toBe('wizard-hidden');
    expect(gateStateFromStatus(null)).toBe('wizard-hidden');
  });
});

describe('dismissed', () => {
  const active = { isEnabled: true, isSetupComplete: false, hasHalowRadio: true };

  it('wizard-active + dismissed opens routes', () => {
    expect(gateStateFromStatus(active, true)).toBe('wizard-dismissed');
  });

  it('no-halow + dismissed opens routes', () => {
    expect(gateStateFromStatus({ ...active, hasHalowRadio: false }, true)).toBe('wizard-dismissed');
  });

  it('wizard-hidden ignores dismissed', () => {
    expect(gateStateFromStatus({ ...active, isSetupComplete: true }, true)).toBe('wizard-hidden');
  });

  it('fail-closed ignores dismissed', () => {
    expect(gateStateFromStatus(undefined, true)).toBe('wizard-hidden');
  });

  it('default is not dismissed', () => {
    expect(gateStateFromStatus(active)).toBe('wizard-active');
  });
});

// ---------------------------------------------------------------------------
// Component: routing behaviour
// ---------------------------------------------------------------------------

const HIDDEN = { isEnabled: false, isSetupComplete: false, hasHalowRadio: true };
const COMPLETE = { isEnabled: true, isSetupComplete: true, hasHalowRadio: true };
const NO_HALOW = { isEnabled: true, isSetupComplete: false, hasHalowRadio: false };
const ACTIVE = { isEnabled: true, isSetupComplete: false, hasHalowRadio: true };

// LocationProbe exposes the router's current path so a <Navigate>
// issued by the gate can be asserted on.
function LocationProbe() {
  return <div data-testid="loc">{useLocation().pathname}</div>;
}

function renderGate(path) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <LocationProbe />
      <SetupGate>
        <div data-testid="app">APP</div>
      </SetupGate>
    </MemoryRouter>,
  );
}

function resolveStatus(status) {
  setupState.getSetupStatus = vi.fn(async () => status);
}

describe('SetupGate', () => {
  beforeEach(() => {
    sessionStorage.clear();
  });

  afterEach(() => {
    cleanup();
    sessionStorage.clear();
    vi.restoreAllMocks();
  });

  it('shows the loading panel until the status RPC settles', () => {
    setupState.getSetupStatus = vi.fn(() => new Promise(() => {}));

    renderGate('/');

    expect(screen.getByText('Loading…')).toBeTruthy();
    expect(screen.queryByTestId('app')).toBeNull();
    expect(setupState.getSetupStatus).toHaveBeenCalledTimes(1);
    expect(setupState.getSetupStatus).toHaveBeenCalledWith({});
  });

  describe('wizard-hidden', () => {
    it('renders children on a normal route when setup is disabled', async () => {
      resolveStatus(HIDDEN);

      renderGate('/dashboard');

      expect(await screen.findByTestId('app')).toBeTruthy();
      expect(screen.getByTestId('loc').textContent).toBe('/dashboard');
    });

    it('redirects /setup to / when setup is disabled', async () => {
      resolveStatus(HIDDEN);

      renderGate('/setup');

      expect(await screen.findByTestId('app')).toBeTruthy();
      expect(screen.getByTestId('loc').textContent).toBe('/');
    });

    it('redirects nested /setup/* paths to / once setup is complete', async () => {
      resolveStatus(COMPLETE);

      renderGate('/setup/mesh');

      expect(await screen.findByTestId('app')).toBeTruthy();
      expect(screen.getByTestId('loc').textContent).toBe('/');
    });
  });

  describe('no-halow', () => {
    it('renders the no-HaLow page on /setup instead of children', async () => {
      resolveStatus(NO_HALOW);

      renderGate('/setup');

      expect(await screen.findByTestId('no-halow')).toBeTruthy();
      expect(screen.queryByTestId('app')).toBeNull();
      expect(screen.getByTestId('loc').textContent).toBe('/setup');
    });

    it('traps any other route onto /setup', async () => {
      resolveStatus(NO_HALOW);

      renderGate('/dashboard');

      expect(await screen.findByTestId('no-halow')).toBeTruthy();
      expect(screen.queryByTestId('app')).toBeNull();
      expect(screen.getByTestId('loc').textContent).toBe('/setup');
    });
  });

  describe('wizard-active', () => {
    it('renders children on /setup', async () => {
      resolveStatus(ACTIVE);

      renderGate('/setup');

      expect(await screen.findByTestId('app')).toBeTruthy();
      expect(screen.getByTestId('loc').textContent).toBe('/setup');
    });

    it('traps any other route onto /setup', async () => {
      resolveStatus(ACTIVE);

      renderGate('/');

      expect(await screen.findByTestId('app')).toBeTruthy();
      expect(screen.getByTestId('loc').textContent).toBe('/setup');
    });
  });

  describe('wizard-dismissed', () => {
    it('leaves normal routes open when the wizard was skipped this session', async () => {
      dismissSetup();
      resolveStatus(ACTIVE);

      renderGate('/dashboard');

      expect(await screen.findByTestId('app')).toBeTruthy();
      expect(screen.getByTestId('loc').textContent).toBe('/dashboard');
    });

    it('still lets /setup through to the wizard route', async () => {
      dismissSetup();
      resolveStatus(ACTIVE);

      renderGate('/setup');

      expect(await screen.findByTestId('app')).toBeTruthy();
      expect(screen.getByTestId('loc').textContent).toBe('/setup');
    });

    it('opens routes for a no-HaLow device too', async () => {
      dismissSetup();
      resolveStatus(NO_HALOW);

      renderGate('/dashboard');

      expect(await screen.findByTestId('app')).toBeTruthy();
      expect(screen.queryByTestId('no-halow')).toBeNull();
      expect(screen.getByTestId('loc').textContent).toBe('/dashboard');
    });
  });

  describe('fail-closed on RPC error', () => {
    it('renders children on a normal route', async () => {
      setupState.getSetupStatus = vi.fn(async () => { throw new Error('boom'); });

      renderGate('/dashboard');

      expect(await screen.findByTestId('app')).toBeTruthy();
      expect(screen.getByTestId('loc').textContent).toBe('/dashboard');
    });

    it('redirects /setup to / so a glitch cannot trap a configured device', async () => {
      setupState.getSetupStatus = vi.fn(async () => { throw new Error('boom'); });

      renderGate('/setup');

      expect(await screen.findByTestId('app')).toBeTruthy();
      expect(screen.getByTestId('loc').textContent).toBe('/');
    });

    it('ignores the dismissed flag', async () => {
      dismissSetup();
      setupState.getSetupStatus = vi.fn(async () => { throw new Error('boom'); });

      renderGate('/setup');

      expect(await screen.findByTestId('app')).toBeTruthy();
      expect(screen.getByTestId('loc').textContent).toBe('/');
    });
  });

  it('ignores a status that resolves after unmount', async () => {
    let resolve;
    setupState.getSetupStatus = vi.fn(() => new Promise((r) => { resolve = r; }));
    const errorSpy = vi.spyOn(console, 'error').mockImplementation(() => {});

    const { unmount } = renderGate('/setup');
    expect(screen.getByText('Loading…')).toBeTruthy();

    unmount();

    await act(async () => {
      resolve(ACTIVE);
      await Promise.resolve();
    });

    expect(screen.queryByTestId('app')).toBeNull();
    expect(errorSpy).not.toHaveBeenCalled();
  });

  it('ignores an error that arrives after unmount', async () => {
    let reject;
    setupState.getSetupStatus = vi.fn(() => new Promise((_, r) => { reject = r; }));
    const errorSpy = vi.spyOn(console, 'error').mockImplementation(() => {});

    const { unmount } = renderGate('/setup');
    unmount();

    await act(async () => {
      reject(new Error('late failure'));
      await Promise.resolve();
    });

    expect(screen.queryByTestId('app')).toBeNull();
    expect(errorSpy).not.toHaveBeenCalled();
  });
});
