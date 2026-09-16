// =============================================================================
// SetupGate.jsx — Routes the app based on setup wizard status
// =============================================================================
//
// Mounted around <Routes> in AppRouter. On mount calls
// setupClient.getSetupStatus({}) and decides which view the user sees:
//
//   is_enabled=false                            → wizard hidden, /setup → /
//   is_enabled=true, complete=true              → wizard locked, /setup → /
//   enabled, !complete, has_halow_radio=false   → no-HaLow error page at /setup
//                                                 (and any non-/setup → /setup)
//   enabled, !complete, has_halow_radio=true    → wizard at /setup
//                                                 (and any non-/setup → /setup)
//   above two + session "Skip for now" flag set → routes open, /setup still
//                                                 shows the wizard
//
// On RPC error the gate FAILS CLOSED — i.e. treats the wizard as
// unavailable so a transient glitch doesn't trap users in the wizard on
// an already-configured device. The dismissed flag never overrides that:
// it only ever opens routes that would otherwise be trapped.

import { useEffect, useState } from 'react';
import { Navigate, useLocation } from 'react-router-dom';
import { setupClient } from '../services/setupClient.js';
import { isSetupDismissed } from '../services/setupDismiss.js';
import StepNoHalowRadio from '../pages/setup/StepNoHalowRadio.jsx';

const SETUP_PATH_PREFIX = '/setup';

// gateStateFromStatus converts a GetSetupStatusResponse into one of five
// branches the gate's render switches over. Pulled out into a pure
// function so SetupGate.test.jsx can table-test it without standing up
// the whole router. `dismissed` reflects the session-only "Skip for now"
// flag; it only ever opens routes that would otherwise be trapped
// (wizard-active / no-halow) and never affects the fail-closed or
// setup-complete branches.
export function gateStateFromStatus(status, dismissed = false) {
  if (!status?.isEnabled) return 'wizard-hidden';
  if (status.isSetupComplete) return 'wizard-hidden';

  const trapped = !status.hasHalowRadio ? 'no-halow' : 'wizard-active';
  if (dismissed) return 'wizard-dismissed';

  return trapped;
}

export default function SetupGate({ children }) {
  const [gate, setGate] = useState(null); // null = loading
  const location = useLocation();

  useEffect(() => {
    let cancelled = false;

    setupClient.getSetupStatus({})
      .then((resp) => {
        if (cancelled) return;
        setGate(gateStateFromStatus(resp, isSetupDismissed()));
      })
      .catch(() => {
        if (cancelled) return;
        // Fail closed: treat the wizard as unreachable.
        setGate('wizard-hidden');
      });

    return () => { cancelled = true; };
  }, []);

  if (gate === null) {
    return <div className="lat-panel">Loading…</div>;
  }

  const onSetupRoute = location.pathname.startsWith(SETUP_PATH_PREFIX);

  if (gate === 'wizard-hidden') {
    if (onSetupRoute) return <Navigate to="/" replace />;
    return children;
  }

  if (gate === 'no-halow') {
    if (onSetupRoute) return <StepNoHalowRadio />;
    return <Navigate to={SETUP_PATH_PREFIX} replace />;
  }

  if (gate === 'wizard-dismissed') {
    // Routes open; visiting /setup still shows the wizard (its route
    // is part of children). Never traps.
    return children;
  }

  // gate === 'wizard-active'
  if (!onSetupRoute) return <Navigate to={SETUP_PATH_PREFIX} replace />;
  return children;
}

// useSetupStatus is unused — SetupWizard fetches its own status. Left
// here as a stub for future cross-component sharing if perf demands it.
