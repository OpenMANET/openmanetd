// =============================================================================
// SetupWizard.jsx — First-boot mesh-node setup flow
// =============================================================================
//
// 6-step wizard: Identity & Role / Mesh / Uplink / APs / Password / Review.
// Mesh Point users skip the Uplink step automatically (5 steps in practice).
// State lives in <SetupProvider>; this component only owns currentIndex
// and the GetSetupStatus payload (so step components can render radio +
// ethernet-port lists without re-fetching).
//
// On apply (StepReview), a streaming ApplySetup runs through all 15
// phases. The terminal event is success/failure; if the stream drops
// before the terminal arrives, StepReview falls back to polling
// GetSetupStatus.

import { useEffect, useMemo, useState } from 'react';
import { setupClient } from '../services/setupClient.js';
import { dismissSetup } from '../services/setupDismiss.js';
import {
  SetupProvider,
  useSetup,
} from '../contexts/SetupContext.jsx';
import { MeshRole } from '../gen/openmanet/setup/v1/setup_pb.js';
import Stepper from '../components/Stepper.jsx';
import StepIdentity from './setup/StepIdentity.jsx';
import StepMesh from './setup/StepMesh.jsx';
import StepUplink from './setup/StepUplink.jsx';
import StepAPs from './setup/StepAPs.jsx';
import StepPassword from './setup/StepPassword.jsx';
import StepReview from './setup/StepReview.jsx';
import SetupBeforeUnloadGuard from './setup/SetupBeforeUnloadGuard.jsx';
import { meshJoinIssues } from './setup/meshChannels.js';
import './SetupWizard.css';

const STEP_DEFS = [
  { key: 'identity', label: 'Identity', component: StepIdentity, role: 'all'   },
  { key: 'mesh',     label: 'Mesh',     component: StepMesh,     role: 'all'   },
  { key: 'uplink',   label: 'Uplink',   component: StepUplink,   role: 'gate'  },
  { key: 'aps',      label: 'APs',      component: StepAPs,      role: 'all'   },
  { key: 'password', label: 'Password', component: StepPassword, role: 'all'   },
  { key: 'review',   label: 'Review',   component: StepReview,   role: 'all'   },
];

export default function SetupWizard() {
  return (
    <SetupProvider>
      <SetupBeforeUnloadGuard />
      <SetupWizardShell />
    </SetupProvider>
  );
}

function SetupWizardShell() {
  const { state, dispatch } = useSetup();
  const [currentIndex, setCurrentIndex] = useState(0);
  const [status, setStatus] = useState(null);
  const [statusError, setStatusError] = useState(null);
  const [confirmReset, setConfirmReset] = useState(false);
  const [confirmSkip, setConfirmSkip] = useState(false);

  // Load the status payload on mount so step components have radio +
  // ethernet-port data available without re-fetching.
  useEffect(() => {
    let cancelled = false;
    setupClient.getSetupStatus({})
      .then((resp) => {
        if (cancelled) return;
        setStatus(resp);
        // Pre-fill the mesh radio with the first HaLow radio so the
        // user doesn't have to pick when there's an obvious choice.
        // browserTimezone rides on the action (not read inside the
        // reducer) so the reducer stays pure.
        dispatch({
          type: 'HYDRATE_FROM_STATUS',
          status: resp,
          browserTimezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
        });
      })
      .catch((err) => { if (!cancelled) setStatusError(err); });
    return () => { cancelled = true; };
  }, [dispatch]);

  // Filter steps by role: Mesh Point hides the Uplink step.
  const steps = useMemo(() => {
    const role = state.role;
    return STEP_DEFS.filter(s => {
      if (s.role === 'all') return true;
      if (s.role === 'gate') return role === MeshRole.MESH_GATE;
      return false;
    });
  }, [state.role]);

  // Clamp currentIndex if the role flip removed the active step. This is a
  // legitimate state-sync to derived data (steps changes mid-flow), so the
  // setState call here is intentional.
  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect
    if (currentIndex >= steps.length) setCurrentIndex(steps.length - 1);
  }, [currentIndex, steps.length]);

  if (statusError) {
    return (
      <div className="lat-body grid-3">
        <div className="lat-panel col-span-all">
          <div className="lat-alert crit">
            Failed to contact the setup service: {String(statusError.message || statusError)}
          </div>
        </div>
      </div>
    );
  }

  if (!status) {
    return <div className="lat-panel">Loading…</div>;
  }

  const step = steps[currentIndex];
  const StepBody = step.component;

  const goPrev = () => setCurrentIndex(i => Math.max(0, i - 1));
  const advance = () => setCurrentIndex(i => Math.min(steps.length - 1, i + 1));

  // Identity step (index 0) gates Next on a "looks already configured"
  // confirmation when the device's UCI state suggests prior setup. We
  // surface the confirmation from the shell rather than the step body
  // so there's exactly one Next button on every step.
  const goNext = () => {
    const onIdentity = step.key === 'identity';
    if (onIdentity && status?.alreadyConfigured) {
      setConfirmReset(true);
      return;
    }
    advance();
  };

  // Full reload (not `navigate`): SetupGate computed its state on mount,
  // so a reload re-evaluates the gate with the flag set. Simple and
  // correct for a once-per-session action.
  //
  // Disarm SetupBeforeUnloadGuard first, the same way StepReview does on
  // a successful apply (StepReview.jsx): mesh.radioName is auto-filled
  // from the detected HaLow radio on mount (HYDRATE_FROM_STATUS), so
  // hasMeaningfulInput() is true on real hardware even when the user has
  // typed nothing — without this the native "leave site?" prompt fires
  // right after the user answers this modal's own confirmation.
  const onSkip = () => {
    window.dispatchEvent(new Event('setup-applied'));
    dismissSetup();
    window.location.assign('/');
  };

  const isFirst = currentIndex === 0;
  const isLast  = currentIndex === steps.length - 1;
  const meshBlocked = step.key === 'mesh' && meshJoinIssues(state, status).length > 0;

  const labels = steps.map(s => s.label);

  return (
    <div className="setup-wizard">
      <div className="lat-view-header">
        <div>
          <h2>OpenMANET Setup Wizard</h2>
          <div className="crumb">Step {currentIndex + 1} of {steps.length}: {step.label}</div>
        </div>
        <div className="lat-view-toolbar">
          <button type="button" className="lat-btn ghost" onClick={() => setConfirmSkip(true)}>
            Skip for now
          </button>
        </div>
      </div>

      <Stepper steps={labels} currentIndex={currentIndex} />

      <div className="lat-body grid-3">
        <div className="lat-panel col-span-all">
          <StepBody status={status} onAdvance={isLast ? undefined : goNext} />
        </div>
      </div>

      <div className="setup-nav">
        <button
          type="button"
          className="lat-btn ghost"
          onClick={goPrev}
          disabled={isFirst}
        >
          Back
        </button>
        {!isLast && (
          <button
            type="button"
            className="lat-btn primary"
            onClick={goNext}
            disabled={meshBlocked}
            title={meshBlocked ? 'Fix the scanned values first' : undefined}
          >
            Next
          </button>
        )}
      </div>

      {confirmReset && (
        <div className="lat-alert crit" role="alertdialog">
          <p>
            This device looks like it&apos;s already configured. Continuing
            will reset its wireless, network, firewall, DHCP, and batman
            state.
          </p>
          <div className="setup-nav">
            <button type="button" className="lat-btn ghost"
              onClick={() => setConfirmReset(false)}>
              Cancel
            </button>
            <button type="button" className="lat-btn danger solid"
              onClick={() => { setConfirmReset(false); advance(); }}>
              Reset and continue
            </button>
          </div>
        </div>
      )}

      {confirmSkip && (
        <div className="lat-panel" role="alertdialog" aria-label="Skip setup confirmation">
          <div className="lat-alert warn" role="alert">Skip Setup?</div>
          <p>This device stays unconfigured:</p>
          <ul>
            <li>No mesh, network, or firewall configuration applied</li>
            <li>No admin password — the interface is unprotected</li>
            <li>Setup reopens on your next visit until completed</li>
          </ul>
          <div className="setup-nav">
            <button type="button" className="lat-btn ghost" onClick={() => setConfirmSkip(false)}>Cancel</button>
            <button type="button" className="lat-btn" onClick={onSkip}>Skip for now</button>
          </div>
        </div>
      )}
    </div>
  );
}
