// =============================================================================
// StepIdentity.test.jsx — Hostname/role step, plus the timezone select
// =============================================================================
//
// StepIdentity reads/writes through SetupContext, so it needs a real
// <SetupProvider> in the tree (mirrors the provider-in-tree approach
// SetupWizard.test.jsx uses at the wizard level). The timezone "detected
// from this browser" tag depends on Intl.DateTimeFormat().resolvedOptions()
// .timeZone, which is stubbed per-test via vi.spyOn so we control what
// counts as "the browser's zone" independent of the CI host's TZ.

import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, screen, fireEvent, cleanup } from '@testing-library/react';

import StepIdentity from '../../../pages/setup/StepIdentity.jsx';
import { SetupProvider } from '../../../contexts/SetupContext.jsx';

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const STATUS = {
  timezones: ['America/Denver', 'UTC'],
  currentTimezone: 'UTC',
};

function stubBrowserTimezone(zone) {
  vi.spyOn(Intl, 'DateTimeFormat').mockReturnValue({
    resolvedOptions: () => ({ timeZone: zone }),
  });
}

function renderStep(status = STATUS) {
  return render(
    <SetupProvider>
      <StepIdentity status={status} />
    </SetupProvider>,
  );
}

describe('TestStepIdentity_TimezoneSelect', () => {
  it('renders the device-reported timezone list', () => {
    stubBrowserTimezone('America/Denver');
    renderStep();

    fireEvent.click(screen.getByRole('button', { name: 'Timezone' }));
    const options = screen.getAllByRole('option');
    expect(options.map(o => o.textContent)).toEqual(['America/Denver', 'UTC']);
  });

  it('does not show the detected tag before any zone is chosen', () => {
    stubBrowserTimezone('America/Denver');
    renderStep();

    expect(screen.queryByText(/detected from this browser/i)).toBeNull();
  });

  it('choosing the browser-matching zone dispatches and shows the detected tag', () => {
    stubBrowserTimezone('America/Denver');
    renderStep();

    fireEvent.click(screen.getByRole('button', { name: 'Timezone' }));
    fireEvent.click(screen.getByRole('option', { name: 'America/Denver' }));

    expect(screen.getByRole('button', { name: 'Timezone' }).textContent).toContain('America/Denver');
    expect(screen.getByText(/detected from this browser/i)).toBeInTheDocument();
  });

  it('choosing a zone that differs from the browser zone dispatches but hides the detected tag', () => {
    stubBrowserTimezone('America/Denver');
    renderStep();

    fireEvent.click(screen.getByRole('button', { name: 'Timezone' }));
    fireEvent.click(screen.getByRole('option', { name: 'UTC' }));

    expect(screen.getByRole('button', { name: 'Timezone' }).textContent).toContain('UTC');
    expect(screen.queryByText(/detected from this browser/i)).toBeNull();
  });
});
