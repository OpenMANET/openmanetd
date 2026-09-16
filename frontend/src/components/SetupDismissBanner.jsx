import { Link } from 'react-router-dom';
import { isSetupDismissed, resumeSetup } from '../services/setupDismiss.js';

// Persistent warning while setup is dismissed for the session. Lives
// in both Layout shells; Resume clears the session flag and routes
// to the wizard. min-height keeps the touch target ≥44px on mobile.
//
// isSetupDismissed() is read once, at mount/render, not subscribed to.
// That's only safe because every mutation of the flag happens on
// /setup — dismissSetup() in SetupWizard.jsx's skip confirm, and
// resumeSetup() in StepReview.jsx on apply success — which is outside
// Layout entirely (see AppRouter.jsx: /setup/* renders SetupWizard
// directly, not through Layout). Any navigation between /setup and a
// Layout-wrapped route forces a fresh mount of this component, so the
// read is never stale. If a future change ever mutates the flag from
// inside a Layout-wrapped page, this component would need to subscribe
// (e.g. re-read on a storage/focus event) instead of reading once.
export default function SetupDismissBanner() {
  if (!isSetupDismissed()) return null;

  return (
    <div className="lat-alert warn setup-dismiss-banner" role="alert">
      <span>Device not configured — mesh, network and password are not set.</span>
      <Link to="/setup" onClick={resumeSetup} className="lat-btn ghost">Resume setup</Link>
    </div>
  );
}
