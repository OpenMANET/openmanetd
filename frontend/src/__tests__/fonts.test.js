// =============================================================================
// fonts.test.js — Guards that the UI's typography works with no uplink
// =============================================================================
// The webfonts used to load from fonts.googleapis.com via a <link> in
// index.html. A mesh node in the field has no uplink, so that request never
// resolved and every face fell back to whatever the device shipped — the
// whole Lattice type scale, silently, only in production. These tests pin the
// self-hosted replacement so the remote form cannot come back.

import { describe, it, expect } from 'vitest';
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const srcDir = join(dirname(fileURLToPath(import.meta.url)), '..');
const frontendDir = join(srcDir, '..');
const latticePath = join(srcDir, 'styles', 'lattice.css');
const lattice = readFileSync(latticePath, 'utf8');

function listCssFiles(dir) {
  const out = [];
  for (const entry of readdirSync(dir)) {
    if (entry === 'gen' || entry === 'node_modules') continue;
    const path = join(dir, entry);
    if (statSync(path).isDirectory()) out.push(...listCssFiles(path));
    else if (entry.endsWith('.css')) out.push(path);
  }
  return out;
}

// Each @font-face block in lattice.css, parsed into its descriptors.
function fontFaces(css) {
  return [...css.matchAll(/@font-face\s*\{([^}]*)\}/g)].map((m) => {
    const body = m[1];
    const descriptor = (name) => {
      const hit = body.match(new RegExp(`${name}\\s*:\\s*([^;]+);`));
      return hit ? hit[1].trim() : null;
    };
    return {
      family: (descriptor('font-family') ?? '').replace(/['"]/g, ''),
      weight: descriptor('font-weight'),
      display: descriptor('font-display'),
      src: descriptor('src') ?? '',
    };
  });
}

const faces = fontFaces(lattice);

describe('TestFontsAreSelfHosted', () => {
  it('index.html requests nothing from a font CDN', () => {
    const html = readFileSync(join(frontendDir, 'index.html'), 'utf8');
    expect(html).not.toContain('fonts.googleapis.com');
    expect(html).not.toContain('fonts.gstatic.com');
  });

  it('index.html pulls in no remote stylesheet at all', () => {
    const html = readFileSync(join(frontendDir, 'index.html'), 'utf8');
    const remote = [...html.matchAll(/<link[^>]*href=["'](https?:)?\/\/[^"']+["'][^>]*>/g)].map(
      (m) => m[0]
    );
    expect(remote).toEqual([]);
  });

  it('no stylesheet in src/ references a remote url()', () => {
    const offenders = listCssFiles(srcDir).flatMap((path) => {
      const urls = [...readFileSync(path, 'utf8').matchAll(/url\(\s*['"]?([^'")]+)/g)].map(
        (m) => m[1]
      );
      return urls.filter((u) => /^(https?:)?\/\//.test(u)).map((u) => `${path}: ${u}`);
    });
    expect(offenders).toEqual([]);
  });
});

describe('TestFontFaceDeclarations', () => {
  it('declares a face for both families the tokens name first', () => {
    // --font-mono and --font-sans each start with the self-hosted family and
    // then fall back to system stacks. The first entry is the one that has to
    // be shipped.
    for (const token of ['--font-mono', '--font-sans']) {
      const value = lattice.match(new RegExp(`${token}:\\s*([^;]+);`))[1];
      const first = value.split(',')[0].trim().replace(/['"]/g, '');
      expect(faces.map((f) => f.family), token).toContain(first);
    }
  });

  it('covers every weight the stylesheets ask for', () => {
    const used = new Set(
      listCssFiles(srcDir).flatMap((path) =>
        [...readFileSync(path, 'utf8').matchAll(/font-weight:\s*(\d+)\s*;/g)].map((m) =>
          Number(m[1])
        )
      )
    );
    expect(used.size).toBeGreaterThan(0);

    for (const face of faces) {
      const [lo, hi] = face.weight.split(/\s+/).map(Number);
      expect(Number.isFinite(lo) && Number.isFinite(hi), face.family).toBe(true);
      for (const weight of used) {
        expect(weight, `${face.family} does not cover ${weight}`).toBeGreaterThanOrEqual(lo);
        expect(weight, `${face.family} does not cover ${weight}`).toBeLessThanOrEqual(hi);
      }
    }
  });

  it('swaps rather than blocking first paint', () => {
    for (const face of faces) {
      expect(face.display, face.family).toBe('swap');
    }
  });

  it('ships every referenced file as a real woff2', () => {
    expect(faces.length).toBeGreaterThan(0);
    for (const face of faces) {
      const href = face.src.match(/url\(\s*['"]?([^'")]+)/)[1];
      expect(href, face.family).toMatch(/\.woff2$/);

      const path = resolve(dirname(latticePath), href);
      // WOFF2's magic number. A truncated or wrong-format download would
      // still satisfy the extension check above.
      expect(readFileSync(path).subarray(0, 4).toString('latin1'), face.family).toBe('wOF2');
    }
  });

  it('ships a license beside the fonts', () => {
    // Both faces are SIL Open Font License 1.1, which requires the license
    // travel with the font.
    const dir = join(srcDir, 'assets', 'fonts');
    const licenses = readdirSync(dir).filter((name) => name.endsWith('LICENSE.txt'));
    expect(licenses.length).toBe(faces.length);
    for (const name of licenses) {
      expect(readFileSync(join(dir, name), 'utf8'), name).toContain(
        'SIL Open Font License, Version 1.1'
      );
    }
  });
});
