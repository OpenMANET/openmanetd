// =============================================================================
// setupDismiss.test.js — Session-only "Skip for now" flag helpers
// =============================================================================
//
// isSetupDismissed/dismissSetup/resumeSetup wrap sessionStorage and must
// fail open (never throw) when storage is unavailable — private browsing
// or a locked-down environment should degrade to "not dismissed", not
// crash the wizard gate.

import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest';
import { isSetupDismissed, dismissSetup, resumeSetup } from '../../services/setupDismiss.js';

const KEY = 'omd-setup-dismissed';

beforeEach(() => {
  sessionStorage.clear();
});

afterEach(() => {
  sessionStorage.clear();
  vi.restoreAllMocks();
});

describe('TestSetupDismissRoundTrip', () => {
  it('is not dismissed by default', () => {
    expect(isSetupDismissed()).toBe(false);
  });

  it('dismissSetup sets the flag and isSetupDismissed reflects it', () => {
    dismissSetup();
    expect(sessionStorage.getItem(KEY)).toBe('1');
    expect(isSetupDismissed()).toBe(true);
  });

  it('resumeSetup clears the flag', () => {
    dismissSetup();
    resumeSetup();
    expect(sessionStorage.getItem(KEY)).toBeNull();
    expect(isSetupDismissed()).toBe(false);
  });
});

describe('TestSetupDismissFailOpen', () => {
  it('isSetupDismissed returns false when storage throws', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => { throw new Error('blocked'); });
    expect(isSetupDismissed()).toBe(false);
  });

  it('dismissSetup does not throw when storage throws', () => {
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('blocked'); });
    expect(() => dismissSetup()).not.toThrow();
  });

  it('resumeSetup does not throw when storage throws', () => {
    vi.spyOn(Storage.prototype, 'removeItem').mockImplementation(() => { throw new Error('blocked'); });
    expect(() => resumeSetup()).not.toThrow();
  });
});
