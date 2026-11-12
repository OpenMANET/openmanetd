---
paths:
  - "frontend/src/**/*.{js,jsx,css}"
  - "frontend/index.html"
  - "frontend/vite.config.js"
---

# Frontend — React + Lattice

React 18 SPA built with Vite. **Lattice** is the design system: a tactical C2 look with near-black surfaces, one cyan accent with glow, monospace for data, sans-serif only for prose, and sharp corners. Tokens live in `frontend/src/styles/lattice.css`, app-shell primitives in `frontend/src/Layout.css`. Page and component CSS is layout glue only. These rules are non-negotiable — if a design ask conflicts with Lattice, raise it rather than inventing a style.

## Required skills

- **`react-best-practices`** — whenever React code under `frontend/src/` is written, reviewed, or refactored. Target devices (MIPS routers, embedded ARM) make its performance rules mandatory.
- **`react-view-transitions`** — whenever UI states animate: route changes, panel enter/exit, list reorder, shared elements, `<ViewTransition>` / `startViewTransition`. The native View Transition API is the only sanctioned animation approach; honor `prefers-reduced-motion`.

If a skill conflicts with Lattice (a hard-coded colour, a third-party library), Lattice and the user win — raise the conflict.

## Principles

1. **Primitive first** — `.lat-panel`, `.lat-btn`, `.lat-chip`, `.lat-table`, `.lat-field`, `.kv`, `.big-num`, `.pbar`, .... Close but not quite? Add a modifier class to `lattice.css`; never fork the style in page CSS.
2. **Tokens, not literals** — no hard-coded hex/rgb colours, font stacks, or px values that duplicate a token. The Stop hook flags colours and radii on added lines.
3. **Mobile-first, field-usable** — usable on a 360px phone in direct sunlight, touch targets ≥ 44px, no horizontal page scroll. Test the breakpoint; don't guess.
4. **Sharp corners** — `--radius` is `0`. Only status dots, signal/meter pips, and the PTT ring are round.
5. **Restraint** — glow only for live/active state; colour only for status; no gradients except the grid overlay.
6. **No UI libraries** — no MUI, Chakra, Tailwind, daisyUI, shadcn.

## Tokens (use; never redefine)

- Surfaces: `--bg`, `--bg-2` (page, deepest); `--surface`, `--surface-2`, `--surface-hi` (panels, inputs, hover).
- Borders: `--border` (default 1px), `--border-hi` (active, focused, interactive), `--border-subtle` (hairlines inside panels).
- Text: `--text` (primary), `--muted` (labels, secondary, inactive), `--dim` (tertiary, disabled, ticks).
- Accent and status: `--accent`, `--accent-2` (primary action, focus, live data, glow); `--ok`, `--warn`, `--crit` (status only).
- Fonts: `--font-mono` (data, labels, tables, headings, buttons); `--font-sans` (long prose, rare).
- Legacy `--green`, `--red`, `--yellow`, `--orange`, `--blue` still resolve; don't add uses.

## Typography

- `body` sets `--font-mono` at 13px — inherit it.
- Section labels and panel titles: uppercase mono, `letter-spacing: 0.18em`, `--muted`, 9–11px. Use `<h3>` inside `.lat-panel` and let the cascade style it.
- Big numeric readouts: `.big-num` (36px, `--accent`, optional `.unit` child). Body copy: 11–13px mono.
- No `font-weight: bold` on body copy; 600/700 only for primary buttons and the brand title.
- Uppercase text always has letter-spacing > 0.08em; lowercase keeps default tracking.

## Layout

The shell (`.layout-desktop`, `.layout-mobile`, `.sidebar`, `.bottom-tab-bar`, `.lat-topbar`, `.lat-view-header`, `.lat-body`) is owned by `Layout.css` / `Layout.jsx`; pages don't redefine it. A page is:

```jsx
<>
  <div className="lat-topbar"> … node id + status chips … </div>
  <div className="lat-view-header">
    <div>
      <h2>Page Title</h2>
      <div className="crumb">Breadcrumb / context</div>
    </div>
    <div className="lat-view-toolbar"> … action buttons … </div>
  </div>
  <div className="lat-body grid-3">
    <div className="lat-panel"> … </div>
    <div className="lat-panel col-span-2"> … </div>
  </div>
</>
```

- Body grids `.lat-body.grid-2x | .grid-3 | .grid-4`; panels span with `.col-span-2 | -3 | -all`. They collapse to 2 columns at 900px and 1 at 640px on their own — don't duplicate that.
- A custom inner layout gets a small page-prefixed grid (`.dashboard-kpi-row`, `.settings-grid`) with its own 900px and 640px media queries.
- Below 768px the shell switches to the bottom-tab layout (`Layout.jsx`).
- Tables wider than the viewport go in `.table-scroll`. Chip rows scroll horizontally under 640px, following `.lat-topbar .chips`.

## Panels and states

```jsx
<div className="lat-panel">
  <div className="panel-head">
    <h3>Panel Title</h3>
    <div className="actions"><button>refresh</button></div>
  </div>
  <div className="kv"><span className="k">Field</span><span className="v">Value</span></div>
</div>
```

- `.lat-panel` is the only container for grouped content. `.kv` rows for label/value pairs, status via `.v.ok | warn | crit | accent`. `.lat-table` inside `.table-scroll`.
- Loading: muted, uppercase, mono, centred — `<div className="lat-panel">Loading…</div>` or a page-local class (`.dashboard-loading` is dashboard-only).
- Empty: a short uppercase muted line (`No mesh peers.`), no icons or illustrations.
- Error: `.lat-alert.crit` with a one-line cause.

## Buttons and inputs

- Every clickable action is `.lat-btn` (`.primary`, `.ghost`, `.danger`, `.danger.solid`), min 32px tall. Primary actions used on mobile reach 44px through a page-local padding bump — don't change the primitive.
- Form controls: `.lat-input`, `.lat-textarea`, `.lat-select`, each in a `.lat-field` with a `<label>`.
- Booleans: `.lat-toggle` (track + thumb) on a `<button>` whose `.on` class follows state — not `<input type="checkbox">`. Sliders: `input[type="range"].lat-slider`.
- Never style a raw `<button>` or `<input>` per page. A missing variant goes into `lattice.css`.

## Status indicators

| Use | Primitive |
|-----|-----------|
| Status pill in a topbar | `.lat-chip` |
| Status dot in a row | `.dot-i` |
| Numeric readout | `.big-num` |
| Bar gauge (CPU, mem, signal %) | `.pbar` with `.pbar-label` / `.pbar-val` |
| 5-bar signal | `.sig-bars`, per-pip `.on` / `.warn` |
| Audio level meter | `.meter-row`, per-segment `.on` / `.hot` / `.peak` |
| Sparkline | `.spark` (optional `.warn`) |
| One-line callout | `.lat-alert` |

Status modifiers are `.ok` / `.warn` / `.crit`, with fixed meanings never remapped per page: `--ok` healthy, connected, link-up; `--warn` degraded but working, partial, retrying; `--crit` failure, disconnected, error; `--accent` live, active, selected — never "good".

## Page CSS

Allowed: the page grid that arranges panels; a few page-prefixed classes that compose primitives (`.dashboard-kpi-row`, `.settings-grid`, `.power-selector`); 900px and 640px breakpoints matching the body grid.

Not allowed: colours, fonts, or font sizes outside the tokens; descendant selectors that restyle `.lat-panel`, `.lat-btn`, `.lat-chip`, `.lat-table`, `.kv`, ...; border-radius, drop shadows, gradients; `!important` outside a `prefers-reduced-motion` block. Fighting a primitive means extending `lattice.css`.

## React conventions

- Function components only. Default export for the page or component, named exports for utilities. File name matches the component (`PttButton.jsx` exports `PttButton`); co-located CSS shares the base name.
- Hooks at the top in this order: `useState`, `useRef`, custom hooks, `useMemo`, `useCallback`, `useEffect`.
- Polling uses `useVisibleInterval`, never `setInterval` — it pauses when the tab is hidden, which field-device batteries need.
- Shared real-time state comes from `useMeshStatus`, `useMeshTopology`, `useGnssStatus`, not per-page polling.
- `useMemo` for non-trivial derivations of polled data (sorting, filtering, table formatting).
- One effect per concern; every effect that subscribes or starts a timer returns its cleanup.
- Global state through `contexts/` providers (`useAuth`, ...) or `services/` modules — no prop drilling.
- No logic inside JSX expressions; compute into a const above the `return`.

## Files

- `pages/` — one file per route, plus page CSS.
- `components/` — widgets shared by two or more pages; a single-consumer widget lives with its page until a second consumer appears.
- `hooks/` (no JSX), `services/` (transport, stores, websocket, audio engine; no JSX), `contexts/`, `utils/` (pure, no React), `styles/`.
- `__tests__/` mirrors `src/` (see `frontend-testing.md`).
- `gen/` is generated — never edit; regenerate with `make buf`.

## Accessibility

- Interactive elements are `<button>` or `<a>`, or carry a `role`; no `onClick` on a `<div>`.
- Every form control has a `<label>` (preferred) or `aria-label`.
- Keep Lattice's `:focus-visible` rings.
- Custom toggles carry `aria-checked`, `aria-pressed`, `role="radiogroup"`, ... (pattern: `PowerSelector` in `SettingsWireless.jsx`).
- Colour is never the only signal — pair `--crit` with a "FAIL" label.
- Honor `prefers-reduced-motion`; don't re-enable glow or transitions Lattice turns off there.

## Workflow

- Hot-reload dev server against a remote daemon: `cd frontend && VITE_API_TARGET=http://<host>:8081 pnpm run dev`. `vite.config.js` proxies `/ws`, `/api`, `/auth`, `/rpc`, and `/whisper` unchanged to `VITE_API_TARGET` (default `http://localhost:8080`); the frontend daemon forwards `/rpc` and `/auth` to the API server, so dev mirrors production's single origin.
- ESLint (`frontend/eslint.config.js`) covers `src/**/*.{js,jsx}` except `src/gen/`. The PostToolUse hook runs `eslint --fix` on each edited file; `pnpm -C frontend run lint:fix` fixes the tree.
- The daemon serves `static/`, embedded at compile time — run `make frontend` before exercising a change through the binary.
- Done means `make lint-frontend` and `make test-frontend` pass and the page has been checked by hand at 360px and at desktop width; layout isn't tested automatically.
