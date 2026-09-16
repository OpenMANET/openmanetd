// setupDismiss.js — session-only "Skip for now" state for the setup
// wizard. Deliberately sessionStorage (not a config write, not an
// RPC): the wizard must return on the next visit until completed.
const KEY = 'omd-setup-dismissed';

export function isSetupDismissed() {
  try {
    return sessionStorage.getItem(KEY) === '1';
  } catch {
    return false;
  }
}

export function dismissSetup() {
  try {
    sessionStorage.setItem(KEY, '1');
  } catch { /* storage unavailable: dismiss just won't persist */ }
}

export function resumeSetup() {
  try {
    sessionStorage.removeItem(KEY);
  } catch { /* ignore */ }
}
