// =============================================================================
// StepReview.test.jsx — Apply stream terminal handling
// =============================================================================
//
// Focused on the "Skip for now" dismiss-flag interaction: a completed
// wizard (terminal success, either via the live stream event or the
// ambiguous-reconnect poll fallback) must clear the session
// 'omd-setup-dismissed' flag so a stale banner doesn't survive a
// successful apply in the same session. Full apply-stream phase
// rendering is out of scope here — see the wizard-shell smoke tests in
// SetupWizard.test.jsx.

import React, { useEffect } from 'react';
import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/react';

const applyState = { applySetup: vi.fn() };

vi.mock('../../../services/setupClient.js', () => ({
  setupClient: {
    applySetup: (...args) => applyState.applySetup(...args),
    getSetupStatus: vi.fn(async () => ({ isSetupComplete: true })),
  },
}));

import StepReview from '../../../pages/setup/StepReview.jsx';
import { setupClient } from '../../../services/setupClient.js';
import { SetupProvider, useSetup } from '../../../contexts/SetupContext.jsx';
import { SETUP_ACTIONS } from '../../../contexts/SetupContext.jsx';
import {
  ApplySetupResponse_Phase as Phase,
  ApplySetupResponse_Status as Status,
} from '../../../gen/openmanet/setup/v1/setup_pb.js';

const KEY = 'omd-setup-dismissed';

// Fills in the fields applyBlockers() requires so the Apply button is
// enabled, then renders StepReview inside a real SetupProvider.
function Harness() {
  const { dispatch } = useSetup();
  useEffect(() => {
    dispatch({ type: SETUP_ACTIONS.SET_HOSTNAME, value: 'node1' });
    dispatch({ type: SETUP_ACTIONS.SET_MESH_FIELD, field: 'radioName', value: 'radio0' });
    dispatch({ type: SETUP_ACTIONS.SET_MESH_FIELD, field: 'meshId', value: 'mesh1' });
    dispatch({ type: SETUP_ACTIONS.SET_MESH_FIELD, field: 'countryCode', value: 'US' });
    dispatch({ type: SETUP_ACTIONS.SET_ADMIN_PASSWORD, value: 'longenough123' });
    dispatch({ type: SETUP_ACTIONS.SET_ADMIN_PASSWORD_CONFIRM, value: 'longenough123' });
  }, [dispatch]);
  return <StepReview />;
}

function renderStep() {
  return render(
    <SetupProvider>
      <Harness />
    </SetupProvider>,
  );
}

function fakeStream(events) {
  return {
    [Symbol.asyncIterator]: async function* () {
      for (const ev of events) yield ev;
    },
  };
}

// Like fakeStream, but never completes after the given events — keeps
// the apply loop (and therefore the in-progress checklist) mounted so
// a test can assert on the intermediate "Apply progress" list instead
// of a terminal panel.
function fakeHangingStream(events) {
  return {
    [Symbol.asyncIterator]: async function* () {
      for (const ev of events) yield ev;
      await new Promise(() => {});
    },
  };
}

beforeEach(() => {
  sessionStorage.clear();
});

afterEach(() => {
  cleanup();
  sessionStorage.clear();
  vi.restoreAllMocks();
});

describe('TestStepReviewDismissFlag', () => {
  it('clears the session dismiss flag on terminal success', async () => {
    sessionStorage.setItem(KEY, '1');
    applyState.applySetup = vi.fn(() => fakeStream([
      { phase: Phase.TERMINAL, result: { success: true, expectedUrl: 'https://node1.local:8081/login' } },
    ]));

    renderStep();
    fireEvent.click(await screen.findByRole('button', { name: /^apply$/i }));

    await waitFor(() => {
      expect(screen.getByText(/setup complete/i)).toBeInTheDocument();
    });
    expect(sessionStorage.getItem(KEY)).toBeNull();
  });

  it('leaves the session dismiss flag set on terminal failure', async () => {
    sessionStorage.setItem(KEY, '1');
    applyState.applySetup = vi.fn(() => fakeStream([
      { phase: Phase.TERMINAL, result: { success: false } },
    ]));

    renderStep();
    fireEvent.click(await screen.findByRole('button', { name: /^apply$/i }));

    await waitFor(() => {
      expect(screen.getByText(/setup failed/i)).toBeInTheDocument();
    });
    expect(sessionStorage.getItem(KEY)).toBe('1');
  });

  it('clears the flag via the ambiguous-reconnect poll fallback', async () => {
    sessionStorage.setItem(KEY, '1');
    // Stream drops before any terminal event arrives.
    applyState.applySetup = vi.fn(() => ({
      [Symbol.asyncIterator]: () => ({
        next: () => Promise.reject(new Error('connection lost')),
      }),
    }));

    renderStep();
    fireEvent.click(await screen.findByRole('button', { name: /^apply$/i }));

    await waitFor(() => {
      expect(screen.getByText(/setup complete/i)).toBeInTheDocument();
    });
    expect(sessionStorage.getItem(KEY)).toBeNull();
  });
});

describe('TestStepReviewApplyProgress', () => {
  it('renders a checklist row for the SET_TIMEZONE phase', async () => {
    applyState.applySetup = vi.fn(() => fakeHangingStream([
      { phase: Phase.HOSTNAME, status: Status.DONE },
      { phase: Phase.SET_TIMEZONE, status: Status.DONE },
    ]));

    renderStep();
    fireEvent.click(await screen.findByRole('button', { name: /^apply$/i }));

    const list = await screen.findByLabelText(/apply progress/i);
    await waitFor(() => {
      expect(list).toHaveTextContent('Timezone');
    });
  });
});

// Ledger §08 P2 / §04 F3: after the wizard's own network reload,
// openmanetd's address-reservation worker claims the node's final mesh
// address on its first tick (125 s after bat0 comes up) and reboots the
// device. The operator must be told on every screen they might be
// looking at when that second outage lands.
describe('TestStepReviewRebootNotice', () => {
  const NOTICE = /reboots itself about 2.3 minutes after apply/i;

  it('warns on the review screen that the device reboots itself', async () => {
    renderStep();

    expect(await screen.findByText(NOTICE)).toBeInTheDocument();
    // Harness dispatches the hostname in an effect; wait for the re-render.
    expect(await screen.findByText(/node1\.local keeps working/i)).toBeInTheDocument();
  });

  it('repeats the reboot notice on the success panel', async () => {
    applyState.applySetup = vi.fn(() => fakeStream([
      { phase: Phase.TERMINAL, result: { success: true, expectedUrl: 'https://node1.local:8081/login' } },
    ]));

    renderStep();
    fireEvent.click(await screen.findByRole('button', { name: /^apply$/i }));

    expect(await screen.findByText(/setup complete/i)).toBeInTheDocument();
    expect(screen.getByText(NOTICE)).toBeInTheDocument();
    expect(screen.queryByText(/only device on the mesh/i)).toBeNull();
  });

  it('repeats the reboot notice on the reconnecting panel', async () => {
    // Stream drops before a terminal event and the completion poll never
    // answers, so StepReview stays on the AmbiguousPanel.
    setupClient.getSetupStatus.mockImplementationOnce(() => new Promise(() => {}));
    applyState.applySetup = vi.fn(() => ({
      [Symbol.asyncIterator]: () => ({
        next: () => Promise.reject(new Error('connection lost')),
      }),
    }));

    renderStep();
    fireEvent.click(await screen.findByRole('button', { name: /^apply$/i }));

    expect(await screen.findByText(/confirming/i)).toBeInTheDocument();
    expect(screen.getByText(NOTICE)).toBeInTheDocument();
  });
});

describe('TestStepReviewMeshBackhaul', () => {
  function BackhaulHarness({ passphrase }) {
    const { dispatch } = useSetup();
    useEffect(() => {
      dispatch({ type: SETUP_ACTIONS.SET_HOSTNAME, value: 'node1' });
      dispatch({ type: SETUP_ACTIONS.SET_MESH_FIELD, field: 'radioName', value: 'radio1' });
      dispatch({ type: SETUP_ACTIONS.SET_MESH_FIELD, field: 'meshId', value: 'mesh1' });
      dispatch({ type: SETUP_ACTIONS.SET_MESH_FIELD, field: 'countryCode', value: 'US' });
      dispatch({ type: SETUP_ACTIONS.SET_ADMIN_PASSWORD, value: 'longenough123' });
      dispatch({ type: SETUP_ACTIONS.SET_ADMIN_PASSWORD_CONFIRM, value: 'longenough123' });
      dispatch({ type: SETUP_ACTIONS.SET_RADIO_MODE, radioName: 'radio0', mode: 'backhaul' });
      dispatch({
        type: SETUP_ACTIONS.SET_AP,
        value: { radioName: 'radio0', backhaulMeshId: 'bh-2g', backhaulPassphrase: passphrase },
      });
    }, [dispatch, passphrase]);
    return <StepReview />;
  }

  it('lists the backhaul radio and sends mesh_backhaul with its own credentials', async () => {
    applyState.applySetup = vi.fn(() => fakeStream([
      { phase: Phase.TERMINAL, result: { success: true } },
    ]));

    render(<SetupProvider><BackhaulHarness passphrase="backhaulpass" /></SetupProvider>);

    expect(await screen.findByText('radio0: bh-2g')).toBeInTheDocument();
    fireEvent.click(await screen.findByRole('button', { name: /^apply$/i }));

    await waitFor(() => expect(applyState.applySetup).toHaveBeenCalledTimes(1));
    const req = applyState.applySetup.mock.calls[0][0];
    const entry = req.profile.aps.find(a => a.radioName === 'radio0');
    expect(entry.enabled).toBe(false);
    expect(entry.meshBackhaul).toMatchObject({ meshId: 'bh-2g', passphrase: 'backhaulpass' });
  });

  it('blocks Apply while the backhaul passphrase is too short', async () => {
    render(<SetupProvider><BackhaulHarness passphrase="short" /></SetupProvider>);

    expect(await screen.findByText(/mesh backhaul on radio0 needs a passphrase/i)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /^apply$/i })).toBeDisabled();
  });

  function LongMeshIdHarness() {
    const { dispatch } = useSetup();
    useEffect(() => {
      dispatch({ type: SETUP_ACTIONS.SET_HOSTNAME, value: 'node1' });
      dispatch({ type: SETUP_ACTIONS.SET_MESH_FIELD, field: 'radioName', value: 'radio1' });
      dispatch({ type: SETUP_ACTIONS.SET_MESH_FIELD, field: 'meshId', value: 'mesh1' });
      dispatch({ type: SETUP_ACTIONS.SET_MESH_FIELD, field: 'countryCode', value: 'US' });
      dispatch({ type: SETUP_ACTIONS.SET_ADMIN_PASSWORD, value: 'longenough123' });
      dispatch({ type: SETUP_ACTIONS.SET_ADMIN_PASSWORD_CONFIRM, value: 'longenough123' });
      dispatch({ type: SETUP_ACTIONS.SET_RADIO_MODE, radioName: 'radio0', mode: 'backhaul' });
      // Force a value past the reducer's seed-time clamp — this is the
      // shape a pre-clamp seed (or a hand-edited long mesh ID) produces.
      dispatch({
        type: SETUP_ACTIONS.SET_AP,
        value: { radioName: 'radio0', backhaulMeshId: 'x'.repeat(33), backhaulPassphrase: 'longenough123' },
      });
    }, [dispatch]);
    return <StepReview />;
  }

  it('blocks Apply when the backhaul mesh ID exceeds 32 characters', async () => {
    render(<SetupProvider><LongMeshIdHarness /></SetupProvider>);

    expect(await screen.findByText(/32 characters or fewer/i)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /^apply$/i })).toBeDisabled();
  });

  function TunedBackhaulHarness() {
    const { dispatch } = useSetup();
    useEffect(() => {
      dispatch({ type: SETUP_ACTIONS.SET_HOSTNAME, value: 'node1' });
      dispatch({ type: SETUP_ACTIONS.SET_MESH_FIELD, field: 'radioName', value: 'radio1' });
      dispatch({ type: SETUP_ACTIONS.SET_MESH_FIELD, field: 'meshId', value: 'mesh1' });
      dispatch({ type: SETUP_ACTIONS.SET_MESH_FIELD, field: 'countryCode', value: 'US' });
      dispatch({ type: SETUP_ACTIONS.SET_ADMIN_PASSWORD, value: 'longenough123' });
      dispatch({ type: SETUP_ACTIONS.SET_ADMIN_PASSWORD_CONFIRM, value: 'longenough123' });
      dispatch({ type: SETUP_ACTIONS.SET_RADIO_MODE, radioName: 'radio0', mode: 'backhaul' });
      dispatch({
        type: SETUP_ACTIONS.SET_AP,
        value: {
          radioName: 'radio0', backhaulMeshId: 'bh-2g', backhaulPassphrase: 'backhaulpass',
          backhaulBandwidthMhz: 40, backhaulChannel: 6, backhaulCountryCode: 'GB',
        },
      });
    }, [dispatch]);
    return <StepReview />;
  }

  it('serialises the backhaul tuning fields and shows them in the summary', async () => {
    applyState.applySetup = vi.fn(() => fakeStream([
      { phase: Phase.TERMINAL, result: { success: true } },
    ]));

    render(<SetupProvider><TunedBackhaulHarness /></SetupProvider>);

    expect(await screen.findByText('6 · 40 MHz · GB')).toBeInTheDocument();
    fireEvent.click(await screen.findByRole('button', { name: /^apply$/i }));

    await waitFor(() => expect(applyState.applySetup).toHaveBeenCalledTimes(1));
    const entry = applyState.applySetup.mock.calls[0][0].profile.aps.find(a => a.radioName === 'radio0');
    expect(entry.meshBackhaul).toMatchObject({ meshId: 'bh-2g', passphrase: 'backhaulpass', bandwidthMhz: 40, channel: 6, countryCode: 'GB' });
  });

  it('sends zero tuning fields when the operator kept the defaults', async () => {
    applyState.applySetup = vi.fn(() => fakeStream([
      { phase: Phase.TERMINAL, result: { success: true } },
    ]));

    render(<SetupProvider><BackhaulHarness passphrase="backhaulpass" /></SetupProvider>);

    expect(await screen.findByText(/^8 · 40 MHz · /)).toBeInTheDocument();
    fireEvent.click(await screen.findByRole('button', { name: /^apply$/i }));
    await waitFor(() => expect(applyState.applySetup).toHaveBeenCalledTimes(1));
    const entry = applyState.applySetup.mock.calls[0][0].profile.aps.find(a => a.radioName === 'radio0');
    expect(entry.meshBackhaul).toMatchObject({ bandwidthMhz: 0, channel: 0, countryCode: '' });
  });

  it('blocks Apply when only one of channel / bandwidth is set', async () => {
    function HalfTunedHarness() {
      const { dispatch } = useSetup();
      useEffect(() => {
        dispatch({ type: SETUP_ACTIONS.SET_HOSTNAME, value: 'node1' });
        dispatch({ type: SETUP_ACTIONS.SET_MESH_FIELD, field: 'radioName', value: 'radio1' });
        dispatch({ type: SETUP_ACTIONS.SET_MESH_FIELD, field: 'countryCode', value: 'US' });
        dispatch({ type: SETUP_ACTIONS.SET_ADMIN_PASSWORD, value: 'longenough123' });
        dispatch({ type: SETUP_ACTIONS.SET_ADMIN_PASSWORD_CONFIRM, value: 'longenough123' });
        dispatch({ type: SETUP_ACTIONS.SET_RADIO_MODE, radioName: 'radio0', mode: 'backhaul' });
        dispatch({ type: SETUP_ACTIONS.SET_AP, value: { radioName: 'radio0', backhaulMeshId: 'bh-2g', backhaulPassphrase: 'backhaulpass', backhaulChannel: 6 } });
      }, [dispatch]);
      return <StepReview />;
    }

    render(<SetupProvider><HalfTunedHarness /></SetupProvider>);

    expect(await screen.findByText(/set bandwidth and channel together/i)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /^apply$/i })).toBeDisabled();
  });
});
