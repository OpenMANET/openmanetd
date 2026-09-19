// =============================================================================
// NavIcon.test.jsx — Tests for the inline SVG navigation glyph set
// =============================================================================

import { describe, it, expect, afterEach } from 'vitest';
import { render, cleanup } from '@testing-library/react';
import NavIcon from '../../components/NavIcon.jsx';
import { ICONS, ICON_NAMES } from '../../components/navIcons.jsx';

afterEach(() => {
  cleanup();
});

describe('TestNavIcon', () => {
  it('exports the full set of shell icon names', () => {
    expect(ICON_NAMES).toEqual([
      'dashboard',
      'comms',
      'topology',
      'gps',
      'blos',
      'settings',
      'more',
      'signout',
    ]);
  });

  it('renders an svg tagged with its own name for every icon', () => {
    for (const name of ICON_NAMES) {
      const { container } = render(<NavIcon name={name} />);
      const svg = container.querySelector('svg');
      expect(svg, name).toBeTruthy();
      expect(svg.getAttribute('data-icon')).toBe(name);
      cleanup();
    }
  });

  it('draws every icon on the shared 16x16 grid with a 1px stroke', () => {
    for (const name of ICON_NAMES) {
      const { container } = render(<NavIcon name={name} />);
      const svg = container.querySelector('svg');
      expect(svg.getAttribute('viewBox'), name).toBe('0 0 16 16');
      expect(svg.getAttribute('stroke'), name).toBe('currentColor');
      expect(svg.getAttribute('stroke-width'), name).toBe('1');
      expect(svg.getAttribute('fill'), name).toBe('none');
      cleanup();
    }
  });

  it('hides icons from assistive technology', () => {
    // The icons are decorative — every call site pairs one with a text label,
    // so exposing them would double-announce each nav item.
    for (const name of ICON_NAMES) {
      const { container } = render(<NavIcon name={name} />);
      const svg = container.querySelector('svg');
      expect(svg.getAttribute('aria-hidden'), name).toBe('true');
      expect(svg.getAttribute('focusable'), name).toBe('false');
      cleanup();
    }
  });

  it('gives each icon distinct artwork', () => {
    const seen = new Map();
    for (const name of ICON_NAMES) {
      const { container } = render(<NavIcon name={name} />);
      const art = container.querySelector('svg').innerHTML;
      expect(art.length, name).toBeGreaterThan(0);
      expect(seen.has(art), `${name} duplicates ${seen.get(art)}`).toBe(false);
      seen.set(art, name);
      cleanup();
    }
  });

  it('returns null for an unknown icon name', () => {
    const { container } = render(<NavIcon name="does-not-exist" />);
    expect(container.querySelector('svg')).toBeNull();
  });

  it.each(['toString', 'constructor', 'hasOwnProperty', 'valueOf', '__proto__'])(
    'returns null for the inherited key %s',
    (name) => {
      // A plain object literal inherits from Object.prototype, so these names
      // would resolve to functions and sail past the `?? null` fallback into
      // React's renderer. ICONS is null-prototype to stop that.
      expect(NavIcon({ name })).toBeNull();

      const { container } = render(<NavIcon name={name} />);
      expect(container.querySelector('svg')).toBeNull();
      expect(container.textContent).toBe('');
    }
  );

  it('exposes no inherited keys on the icon table itself', () => {
    expect(Object.getPrototypeOf(ICONS)).toBeNull();
    expect(Object.keys(ICONS)).toEqual(ICON_NAMES);
  });

  it('reuses the same element instance across renders', () => {
    // The icons are hoisted to module scope precisely so they are not rebuilt
    // on every render of the shell.
    expect(NavIcon({ name: 'comms' })).toBe(NavIcon({ name: 'comms' }));
  });
});
