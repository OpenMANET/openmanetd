// =============================================================================
// check-css-target.mjs — build guard against CSS media query range syntax
// =============================================================================
// Vite's CSS minifier rewrites `@media (max-width: 640px)` to the newer range
// form `@media (width<=640px)` — or, for a bounded range, the two-sided form
// `@media (400px <= width <= 700px)` — unless build.cssTarget constrains it.
// Browsers older than Chrome 104 / Safari 16.4 cannot parse either range form
// and discard the entire at-rule, silently disabling every responsive rule in
// the bundle. This guard fails the build so that regression can never ship
// unnoticed.

import { readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';

// Matches both the property-first one-sided form (width<=640px) and the
// comparator-first form that appears in a two-sided range like
// (400px <= width <= 700px) — the leading "400px <= " doesn't need to be
// captured, just the "<= width" (or ">= height", etc.) half next to it.
const RANGE_SYNTAX = /@media[^{]*(?:(?:width|height)\s*[<>]=?|[<>]=?\s*(?:width|height))/;

const dir = process.argv[2];
if (!dir) {
  console.error('usage: node scripts/check-css-target.mjs <css-dir>');
  process.exit(1);
}

const cssFiles = readdirSync(dir).filter((name) => name.endsWith('.css'));

// A guard that passes on an empty input set is not a guard — it means the
// build output moved, the glob broke, or nothing was built at all. Fail
// loudly instead of reporting a false "ok".
if (cssFiles.length === 0) {
  console.error(`No .css files found in ${dir} — expected at least one build output file.`);
  process.exit(1);
}

const offenders = cssFiles
  .filter((name) => RANGE_SYNTAX.test(readFileSync(join(dir, name), 'utf8')));

if (offenders.length > 0) {
  console.error(
    'CSS media query range syntax found in build output:\n  ' +
    offenders.join('\n  ') +
    '\n\nOlder browsers discard these at-rules, disabling all responsive CSS.' +
    '\nFix: set build.cssTarget in frontend/vite.config.js.'
  );
  process.exit(1);
}

console.log('css-target guard: ok');
