// =============================================================================
// Layout.test.jsx — Tests for responsive app shell layout
// =============================================================================

import { vi, describe, it, expect, afterEach, beforeEach } from 'vitest';
import { render, screen, fireEvent, act, cleanup } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import Layout from '../../Layout.jsx';
import { resumeSetup } from '../../services/setupDismiss.js';

vi.mock('../../contexts/useAuth.js', () => ({
  useAuth: () => ({ logout: vi.fn() }),
}));

const dismissState = { dismissed: false };

vi.mock('../../services/setupDismiss.js', () => ({
  isSetupDismissed: () => dismissState.dismissed,
  resumeSetup: vi.fn(),
}));

beforeEach(() => {
  dismissState.dismissed = false;
  resumeSetup.mockClear();
});

afterEach(() => {
  cleanup();
});

function renderLayout(initialWidth = 1024) {
  Object.defineProperty(window, 'innerWidth', { value: initialWidth, writable: true });
  return render(
    <MemoryRouter initialEntries={['/']}>
      <Layout />
    </MemoryRouter>
  );
}

describe('TestLayoutDesktop', () => {
  it('renders sidebar with brand on wide viewport', () => {
    const { container } = renderLayout(1024);
    expect(container.querySelector('.sidebar')).toBeTruthy();
    expect(screen.getByText(/◇ OpenMANET/)).toBeTruthy();
    expect(screen.getByText('Comms')).toBeTruthy();
    expect(screen.getByText('Settings')).toBeTruthy();
  });

  it('renders brand subtitle', () => {
    renderLayout(1024);
    expect(screen.getByText('Mesh Terminal')).toBeTruthy();
  });

  it('renders nav section headings', () => {
    renderLayout(1024);
    expect(screen.getByText('Operations')).toBeTruthy();
    expect(screen.getByText('System')).toBeTruthy();
  });
});

describe('TestLayoutMobile', () => {
  it('renders bottom tab bar on narrow viewport', () => {
    const { container } = renderLayout(500);
    expect(container.querySelector('.bottom-tab-bar')).toBeTruthy();
    expect(container.querySelector('.sidebar')).toBeNull();
    // "More" overflow tab is always present
    expect(screen.getByText('More')).toBeTruthy();
  });

  it('opens overflow sheet when More tab tapped', () => {
    const { container } = renderLayout(500);
    const moreTab = screen.getByText('More').closest('button');
    fireEvent.click(moreTab);
    expect(container.querySelector('.tab-sheet')).toBeTruthy();
    expect(screen.getByText('BLOS')).toBeTruthy();
    expect(screen.getByText('Settings')).toBeTruthy();
  });
});

describe('TestLayoutSidebarCollapse', () => {
  it('collapses and expands sidebar', () => {
    const { container } = renderLayout(1024);
    const sidebar = container.querySelector('.sidebar');
    expect(sidebar.classList.contains('collapsed')).toBe(false);

    // Collapse
    const toggleBtn = container.querySelector('.sidebar-toggle');
    fireEvent.click(toggleBtn);
    expect(sidebar.classList.contains('collapsed')).toBe(true);
    // Brand should be hidden when collapsed
    expect(screen.queryByText(/◇ OpenMANET/)).toBeNull();

    // Expand
    fireEvent.click(toggleBtn);
    expect(sidebar.classList.contains('collapsed')).toBe(false);
    expect(screen.getByText(/◇ OpenMANET/)).toBeTruthy();
  });
});

describe('TestLayoutResize', () => {
  it('switches from desktop to mobile on resize (debounced)', () => {
    vi.useFakeTimers();
    try {
      const { container } = renderLayout(1024);
      expect(container.querySelector('.sidebar')).toBeTruthy();

      act(() => {
        window.innerWidth = 500;
        window.dispatchEvent(new Event('resize'));
        vi.advanceTimersByTime(150);
      });

      expect(container.querySelector('.bottom-tab-bar')).toBeTruthy();
      expect(container.querySelector('.sidebar')).toBeNull();
    } finally {
      vi.useRealTimers();
    }
  });

  it('switches from mobile to desktop on resize (debounced)', () => {
    vi.useFakeTimers();
    try {
      const { container } = renderLayout(500);
      expect(container.querySelector('.bottom-tab-bar')).toBeTruthy();

      act(() => {
        window.innerWidth = 1024;
        window.dispatchEvent(new Event('resize'));
        vi.advanceTimersByTime(150);
      });

      expect(container.querySelector('.sidebar')).toBeTruthy();
      expect(container.querySelector('.bottom-tab-bar')).toBeNull();
    } finally {
      vi.useRealTimers();
    }
  });
});

describe('TestLayoutSetupDismissBanner', () => {
  it('renders the banner on desktop when setup is dismissed', () => {
    dismissState.dismissed = true;
    const { container } = renderLayout(1024);
    expect(container.querySelector('.setup-dismiss-banner')).toBeTruthy();
    expect(screen.getByText(/device not configured/i)).toBeTruthy();
    expect(screen.getByRole('link', { name: /resume setup/i })).toBeTruthy();
  });

  it('clicking Resume setup calls resumeSetup', () => {
    dismissState.dismissed = true;
    renderLayout(1024);
    fireEvent.click(screen.getByRole('link', { name: /resume setup/i }));
    expect(resumeSetup).toHaveBeenCalledTimes(1);
  });

  it('renders the banner on mobile when setup is dismissed', () => {
    dismissState.dismissed = true;
    const { container } = renderLayout(500);
    expect(container.querySelector('.setup-dismiss-banner')).toBeTruthy();
    expect(screen.getByRole('link', { name: /resume setup/i })).toBeTruthy();
  });

  it('omits the banner on desktop when setup is not dismissed', () => {
    dismissState.dismissed = false;
    const { container } = renderLayout(1024);
    expect(container.querySelector('.setup-dismiss-banner')).toBeNull();
  });

  it('omits the banner on mobile when setup is not dismissed', () => {
    dismissState.dismissed = false;
    const { container } = renderLayout(500);
    expect(container.querySelector('.setup-dismiss-banner')).toBeNull();
  });
});

describe('TestLayoutNavLinks', () => {
  it('includes all expected nav paths', () => {
    const { container } = renderLayout(1024);
    const links = container.querySelectorAll('.sidebar a');
    const hrefs = Array.from(links).map(a => a.getAttribute('href'));
    expect(hrefs).toContain('/');
    expect(hrefs).toContain('/comms');
    expect(hrefs).toContain('/topology');
    expect(hrefs).toContain('/gps');
    expect(hrefs).toContain('/blos');
    expect(hrefs).toContain('/settings');
  });
});
