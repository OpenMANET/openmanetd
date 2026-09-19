// =============================================================================
// navIcons.jsx — Artwork for the app shell navigation glyphs
// =============================================================================
//
// Hand-drawn rather than pulled from an icon library for three reasons:
//
//  1. `.claude/rules/frontend.md` forbids third-party UI libraries.
//  2. A text-glyph icon set would depend on the monospace font actually
//     arriving. The webfonts are self-hosted now, but the fallback stack is
//     still whatever the device ships, and several otherwise-ideal glyphs
//     are missing from common stock monospace faces anyway — U+2B21 hexagon
//     and U+2699 gear are absent from DejaVu Sans Mono and Menlo, and the
//     gear renders as a color emoji on iOS and Android, which would break
//     the currentColor accent cascade even where it does render.
//  3. Eight hand-written icons cost ~1.7KB; the smallest library option
//     measured 4.5KB for six of them, and static/ is served and embedded
//     uncompressed.
//
// All artwork is a 16x16 viewBox on integer coordinates with a 1px stroke.
// Strokes stay exactly 1 device pixel at any box size via the
// `vector-effect: non-scaling-stroke` rule in Layout.css.
//
// Every icon is built once at module load and reused by reference — these are
// static nodes with no props, so there is nothing to rebuild per render. The
// `data-icon` attribute is what tests and DOM inspection identify them by.
//
// This module deliberately exports no component. Keeping the artwork apart
// from `NavIcon.jsx` lets that file export a component and nothing else, which
// is what react-refresh needs to hot-reload it.

const SVG_PROPS = {
  viewBox: '0 0 16 16',
  width: '100%',
  height: '100%',
  fill: 'none',
  stroke: 'currentColor',
  strokeWidth: '1',
  'aria-hidden': 'true',
  focusable: 'false',
};

// A filled node dot. Shared by the topology, BLOS, GPS and overflow icons.
function dot(cx, cy, r) {
  return <circle key={`${cx}-${cy}`} cx={cx} cy={cy} r={r} fill="currentColor" stroke="none" />;
}

export const ICONS = {
  // Four panels in a 2x2 grid — the dashboard's own layout.
  dashboard: (
    <svg {...SVG_PROPS} data-icon="dashboard">
      <path d="M2 2h5v5H2zM9 2h5v5H9zM2 9h5v5H2zM9 9h5v5H9z" />
    </svg>
  ),

  // A transmitting source: centre dot with two pairs of radiating arcs.
  comms: (
    <svg {...SVG_PROPS} data-icon="comms">
      <path d="M3 3a7 7 0 000 10M13 3a7 7 0 010 10M5 5a4 4 0 000 6M11 5a4 4 0 010 6" />
      {dot(8, 8, 1.5)}
    </svg>
  ),

  // Three mesh nodes, every pair linked — a mesh, not a hub and spoke.
  topology: (
    <svg {...SVG_PROPS} data-icon="topology">
      <path d="M8 3L3 12M8 3L13 12M3 12H13" />
      {dot(8, 3, 1.5)}
      {dot(3, 12, 1.5)}
      {dot(13, 12, 1.5)}
    </svg>
  ),

  // Crosshair over a fix.
  gps: (
    <svg {...SVG_PROPS} data-icon="gps">
      <path d="M8 1v3M8 12v3M1 8h3M12 8h3" />
      <circle cx="8" cy="8" r="3" />
      {dot(8, 8, 1)}
    </svg>
  ),

  // Two ground stations linked by a hop arcing over the horizon.
  blos: (
    <svg {...SVG_PROPS} data-icon="blos">
      <path d="M3 11a7 7 0 0110 0M1 14h14" />
      {dot(3, 11, 1.5)}
      {dot(13, 11, 1.5)}
    </svg>
  ),

  // Three sliders at different positions.
  settings: (
    <svg {...SVG_PROPS} data-icon="settings">
      <path d="M2 4h12M2 8h12M2 12h12M6 2v4M11 6v4M5 10v4" />
    </svg>
  ),

  // Horizontal ellipsis — the conventional overflow affordance.
  more: (
    <svg {...SVG_PROPS} data-icon="more">
      {dot(3, 8, 1.5)}
      {dot(8, 8, 1.5)}
      {dot(13, 8, 1.5)}
    </svg>
  ),

  // Arrow leaving an open door.
  signout: (
    <svg {...SVG_PROPS} data-icon="signout">
      <path d="M6 2H2v12h4M10 5l3 3-3 3M13 8H6" />
    </svg>
  ),
};

export const ICON_NAMES = Object.keys(ICONS);
