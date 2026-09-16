// =============================================================================
// SetupWizard.test.jsx — smoke tests for the first-boot setup wizard shell
// =============================================================================

import React from 'react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, waitFor, cleanup, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';

const setupState = {
  getSetupStatus: vi.fn(),
};

vi.mock('../../services/setupClient.js', () => ({
  setupClient: {
    getSetupStatus: (...args) => setupState.getSetupStatus(...args),
  },
}));

// SetupBeforeUnloadGuard registers a beforeunload listener; stub it so the
// test environment doesn't accumulate listeners across runs.
vi.mock('../../pages/setup/SetupBeforeUnloadGuard.jsx', () => ({
  default: () => null,
}));

// The individual step bodies pull in their own services; replacing them with
// minimal stubs keeps this test focused on the wizard shell.
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
  setupState.getSetupStatus = vi.fn(async () => ({
    alreadyConfigured: false,
    radios: [],
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

describe('TestSetupWizardLoading', () => {
  it('shows a loading state until the status RPC resolves', () => {
    let resolveStatus;
    setupState.getSetupStatus = vi.fn(() => new Promise((r) => { resolveStatus = r; }));
    renderWizard();
    expect(screen.getByText(/loading/i)).toBeInTheDocument();
    resolveStatus({ alreadyConfigured: false, radios: [], ethernetPorts: [] });
  });

  it('renders the identity step once status resolves', async () => {
    renderWizard();
    expect(await screen.findByTestId('step-identity')).toBeInTheDocument();
    expect(screen.getByText(/Step 1 of/i)).toBeInTheDocument();
  });
});

describe('TestSetupWizardNavigation', () => {
  it('Back is disabled on the first step', async () => {
    renderWizard();
    await screen.findByTestId('step-identity');
    expect(screen.getByRole('button', { name: /back/i })).toBeDisabled();
  });

  it('advances from identity to mesh on Next when not already configured', async () => {
    renderWizard();
    await screen.findByTestId('step-identity');
    fireEvent.click(screen.getByRole('button', { name: /next/i }));
    expect(await screen.findByTestId('step-mesh')).toBeInTheDocument();
  });

  it('shows the reset confirmation when status reports already configured', async () => {
    setupState.getSetupStatus = vi.fn(async () => ({
      alreadyConfigured: true,
      radios: [],
      ethernetPorts: [],
    }));
    renderWizard();
    await screen.findByTestId('step-identity');
    fireEvent.click(screen.getByRole('button', { name: /next/i }));
    await waitFor(() => {
      expect(screen.getByRole('alertdialog')).toBeInTheDocument();
    });
    expect(screen.getByText(/already configured/i)).toBeInTheDocument();
  });
});

describe('TestSetupWizardError', () => {
  it('renders a crit alert when the status RPC fails', async () => {
    setupState.getSetupStatus = vi.fn(async () => { throw new Error('boom'); });
    renderWizard();
    await waitFor(() => {
      expect(screen.getByText(/Failed to contact the setup service/i)).toBeInTheDocument();
    });
  });
});

describe('TestSetupWizardSkip', () => {
  let assignSpy;
  let originalLocation;

  beforeEach(() => {
    originalLocation = window.location;
    // jsdom convention: delete + replace so window.location.assign can be
    // spied on without triggering an actual navigation.
    delete window.location;
    window.location = { ...originalLocation, assign: vi.fn() };
    assignSpy = window.location.assign;
  });

  afterEach(() => {
    window.location = originalLocation;
  });

  it('shows the skip confirmation dialog when "Skip for now" is clicked', async () => {
    renderWizard();
    await screen.findByTestId('step-identity');
    fireEvent.click(screen.getByRole('button', { name: /skip for now/i }));
    const dialog = screen.getByRole('alertdialog', { name: /skip setup confirmation/i });
    expect(dialog).toBeInTheDocument();
    expect(within(dialog).getByText(/this device stays unconfigured/i)).toBeInTheDocument();
    expect(within(dialog).getByText(/no mesh, network, or firewall configuration applied/i)).toBeInTheDocument();
    expect(within(dialog).getByText(/no admin password/i)).toBeInTheDocument();
    expect(within(dialog).getByText(/setup reopens on your next visit until completed/i)).toBeInTheDocument();
  });

  it('Cancel closes the skip confirmation without dismissing setup', async () => {
    renderWizard();
    await screen.findByTestId('step-identity');
    fireEvent.click(screen.getByRole('button', { name: /skip for now/i }));
    const dialog = screen.getByRole('alertdialog', { name: /skip setup confirmation/i });
    fireEvent.click(within(dialog).getByRole('button', { name: /cancel/i }));
    expect(screen.queryByRole('alertdialog', { name: /skip setup confirmation/i })).toBeNull();
    expect(sessionStorage.getItem('omd-setup-dismissed')).toBeNull();
    expect(assignSpy).not.toHaveBeenCalled();
  });

  it('confirming skip writes the session flag and navigates to /', async () => {
    renderWizard();
    await screen.findByTestId('step-identity');
    fireEvent.click(screen.getByRole('button', { name: /skip for now/i }));
    const dialog = screen.getByRole('alertdialog', { name: /skip setup confirmation/i });
    fireEvent.click(within(dialog).getByRole('button', { name: /skip for now/i }));
    expect(sessionStorage.getItem('omd-setup-dismissed')).toBe('1');
    expect(assignSpy).toHaveBeenCalledWith('/');
  });
});
