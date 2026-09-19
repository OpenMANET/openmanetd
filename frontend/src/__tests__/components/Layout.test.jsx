// =============================================================================
// Layout.test.jsx — Tests for responsive app shell layout
// =============================================================================

import { vi, describe, it, expect, afterEach, beforeEach } from 'vitest';
import { render, screen, fireEvent, act, cleanup } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import Layout from '../../Layout.jsx';
import { MOBILE_BREAKPOINT } from '../../constants.js';
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

describe('TestLayoutNavIcons', () => {
  function iconsIn(container, selector) {
    return Array.from(container.querySelectorAll(`${selector} svg[data-icon]`)).map((el) =>
      el.getAttribute('data-icon')
    );
  }

  it('gives every sidebar nav item its own icon', () => {
    const { container } = renderLayout(1024);
    expect(iconsIn(container, '.sidebar-nav')).toEqual([
      'dashboard',
      'comms',
      'topology',
      'gps',
      'blos',
      'settings',
    ]);
  });

  it('keeps sidebar icons visible when the sidebar is collapsed', () => {
    // Collapsing hides the labels, so the icon is the only remaining cue and
    // must survive the collapse.
    const { container } = renderLayout(1024);
    fireEvent.click(container.querySelector('.sidebar-toggle'));
    expect(container.querySelector('.sidebar').classList.contains('collapsed')).toBe(true);
    expect(container.querySelectorAll('.sidebar-nav .nav-label').length).toBe(0);
    expect(iconsIn(container, '.sidebar-nav')).toEqual([
      'dashboard',
      'comms',
      'topology',
      'gps',
      'blos',
      'settings',
    ]);
  });

  it('gives every bottom tab its own icon', () => {
    const { container } = renderLayout(500);
    expect(iconsIn(container, '.bottom-tab-bar')).toEqual([
      'dashboard',
      'comms',
      'topology',
      'gps',
      'more',
    ]);
  });

  it('gives every overflow sheet row its own icon', () => {
    const { container } = renderLayout(500);
    fireEvent.click(screen.getByText('More').closest('button'));
    expect(iconsIn(container, '.tab-sheet')).toEqual(['blos', 'settings', 'signout']);
  });

  it('leaves no icon slot empty in either shell', () => {
    // The sidebar shipped with an empty `<span className="nav-icon" />` for
    // every route; this pins that regression.
    const desktop = renderLayout(1024);
    for (const slot of desktop.container.querySelectorAll('.nav-icon, .tab-icon')) {
      expect(slot.querySelector('svg[data-icon]')).toBeTruthy();
    }
    cleanup();

    const mobile = renderLayout(500);
    fireEvent.click(screen.getByText('More').closest('button'));
    for (const slot of mobile.container.querySelectorAll('.nav-icon, .tab-icon')) {
      expect(slot.querySelector('svg[data-icon]')).toBeTruthy();
    }
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

describe('TestLayoutBreakpointBoundary', () => {
  // The CSS breakpoint is `max-width: 768px`, which is inclusive, so 768 must
  // get the mobile shell. Layout used a strict `<` here, which rendered the
  // desktop sidebar around mobile-styled content at exactly iPad portrait
  // width. These three cases pin both sides of the boundary and the boundary
  // itself, so the JS and CSS halves cannot drift apart again.
  it('renders the mobile shell at exactly MOBILE_BREAKPOINT', () => {
    const { container } = renderLayout(MOBILE_BREAKPOINT);
    expect(container.querySelector('.layout-mobile')).toBeTruthy();
    expect(container.querySelector('.bottom-tab-bar')).toBeTruthy();
    expect(container.querySelector('.sidebar')).toBeNull();
  });

  it('renders the mobile shell one pixel below MOBILE_BREAKPOINT', () => {
    const { container } = renderLayout(MOBILE_BREAKPOINT - 1);
    expect(container.querySelector('.layout-mobile')).toBeTruthy();
  });

  it('renders the desktop shell one pixel above MOBILE_BREAKPOINT', () => {
    const { container } = renderLayout(MOBILE_BREAKPOINT + 1);
    expect(container.querySelector('.layout-desktop')).toBeTruthy();
    expect(container.querySelector('.sidebar')).toBeTruthy();
    expect(container.querySelector('.bottom-tab-bar')).toBeNull();
  });
});

describe('TestLayoutBodyClass', () => {
  it('adds lat-shell-active to body while mounted', () => {
    renderLayout(1024);
    expect(document.body.classList.contains('lat-shell-active')).toBe(true);
  });

  it('removes lat-shell-active from body on unmount', () => {
    const { unmount } = renderLayout(1024);
    unmount();
    expect(document.body.classList.contains('lat-shell-active')).toBe(false);
  });
});
