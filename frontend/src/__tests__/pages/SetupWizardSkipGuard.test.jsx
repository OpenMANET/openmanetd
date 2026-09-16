// =============================================================================
// SetupWizardSkipGuard.test.jsx — Skip for now disarms the beforeunload guard
// =============================================================================
//
// SetupWizard.test.jsx mocks SetupBeforeUnloadGuard out entirely so its
// tests stay focused on the wizard shell. This file renders the REAL
// guard alongside the wizard to pin the fix for a real-hardware bug: a
// HaLow radio is auto-filled into mesh.radioName by HYDRATE_FROM_STATUS
// on mount, which makes hasMeaningfulInput() true even when the user has
// typed nothing — so without disarming the guard, confirming "Skip for
// now" leaves the native "leave site?" prompt armed and it fires right
// after the user answers this modal's own confirmation. onSkip must
// dispatch the same 'setup-applied' event StepReview.jsx uses on a
// successful apply (see SetupBeforeUnloadGuard.jsx's `onApplied`
// listener) before navigating away.

import React from 'react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, cleanup, within, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';

const setupState = { getSetupStatus: vi.fn() };

vi.mock('../../services/setupClient.js', () => ({
  setupClient: {
    getSetupStatus: (...args) => setupState.getSetupStatus(...args),
  },
}));

// Step bodies stubbed exactly like SetupWizard.test.jsx to keep the
// render focused — SetupBeforeUnloadGuard.jsx is deliberately left
// unmocked, that's the point of this file.
vi.mock('../../pages/setup/StepIdentity.jsx', () => ({
  default: () => <div data-testid="step-identity">IDENTITY</div>,
}));
vi.mock('../../pages/setup/StepMesh.jsx', () => ({
  default: () => <div data-testid="step-mesh">MESH</div>,
}));
vi.mock('../../pages/setup/StepUplink.jsx', () => ({
  default: () => <div data-testid="step-uplink">UPLINK</div>,
}));
vi.mock('../../pages/setup/StepAPs.jsx', () => ({
  default: () => <div data-testid="step-aps">APS</div>,
}));
vi.mock('../../pages/setup/StepPassword.jsx', () => ({
  default: () => <div data-testid="step-password">PASSWORD</div>,
}));
vi.mock('../../pages/setup/StepReview.jsx', () => ({
  default: () => <div data-testid="step-review">REVIEW</div>,
}));

import SetupWizard from '../../pages/SetupWizard.jsx';

beforeEach(() => {
  // A real HaLow radio reproduces the on-hardware auto-fill that makes
  // hasMeaningfulInput() true without the user typing anything.
  setupState.getSetupStatus = vi.fn(async () => ({
    alreadyConfigured: false,
    radios: [{ name: 'wlan0', isHalow: true }],
    ethernetPorts: [],
  }));
  sessionStorage.clear();
});

afterEach(() => {
  cleanup();
  sessionStorage.clear();
});

function renderWizard() {
  return render(
    <MemoryRouter>
      <SetupWizard />
    </MemoryRouter>,
  );
}

// Fires a real, cancelable beforeunload event and reports whether the
// guard called preventDefault() on it — the same signal browsers use to
// decide whether to show the native "leave site?" prompt.
function fireBeforeUnload() {
  const event = new Event('beforeunload', { cancelable: true });
  window.dispatchEvent(event);
  return event;
}

// The guard re-registers its beforeunload listener in a passive effect
// keyed on wizard state. findByTestId resolves on the commit that
// hydrates the HaLow radio, which can land a tick before that effect
// runs (React's scheduler vs RTL's settle timer — it lost under CI
// load). Poll until the listener is really armed instead of assuming
// the first render after hydration is enough.
async function waitForGuardArmed() {
  await waitFor(() => expect(fireBeforeUnload().defaultPrevented).toBe(true));
}

describe('TestSetupWizardSkipDisarmsGuard', () => {
  it('control: the guard is armed once a HaLow radio is auto-filled', async () => {
    renderWizard();
    await screen.findByTestId('step-identity');

    await waitForGuardArmed();
  });

  it('confirming "Skip for now" disarms the guard before navigating away', async () => {
    const originalLocation = window.location;
    // jsdom convention: delete + replace so window.location.assign can be
    // spied on without triggering an actual navigation.
    delete window.location;
    window.location = { ...originalLocation, assign: vi.fn() };

    try {
      renderWizard();
      await screen.findByTestId('step-identity');
      // Prove the guard was armed before Skip, otherwise a never-armed
      // guard would pass the disarm assertion vacuously.
      await waitForGuardArmed();

      fireEvent.click(screen.getByRole('button', { name: /skip for now/i }));
      const dialog = screen.getByRole('alertdialog', { name: /skip setup confirmation/i });
      fireEvent.click(within(dialog).getByRole('button', { name: /skip for now/i }));

      expect(window.location.assign).toHaveBeenCalledWith('/');

      const event = fireBeforeUnload();
      expect(event.defaultPrevented).toBe(false);
    } finally {
      window.location = originalLocation;
    }
  });
});
