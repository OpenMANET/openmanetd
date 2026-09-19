// =============================================================================
// NavIcon.jsx — Inline SVG glyph for an app shell navigation entry
// =============================================================================
//
// The artwork lives in navIcons.jsx; this file is only the lookup. Unknown
// names render nothing rather than throwing, so a nav entry that forgets its
// icon degrades to the label alone instead of taking down the shell.

import { ICONS } from './navIcons.jsx';

export default function NavIcon({ name }) {
  return ICONS[name] ?? null;
}
