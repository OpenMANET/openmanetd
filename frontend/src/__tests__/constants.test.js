// =============================================================================
// constants.test.js — Tests for shared constants
// =============================================================================
// Mirrors the Go test convention: each constant is verified for correctness
// and internal consistency.

import { describe, it, expect } from 'vitest';
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import {
  SAMPLE_RATE,
  WHISPER_RATE,
  FRAME_SIZE,
  CHANNELS_DEF,
  PCM_RING_SIZE,
  JITTER_PREFILL,
  MSG_TYPE,
  VOX_HANGTIME_MS,
  NEIGHBOR_HISTORY_LENGTH,
  MOBILE_BREAKPOINT,
} from '../constants.js';

describe('TestAudioConstants', () => {
  it('SAMPLE_RATE is 48000', () => {
    expect(SAMPLE_RATE).toBe(48000);
  });

  it('WHISPER_RATE is 16000', () => {
    expect(WHISPER_RATE).toBe(16000);
  });

  it('FRAME_SIZE is 960 (20ms at 48kHz)', () => {
    expect(FRAME_SIZE).toBe(960);
    expect(FRAME_SIZE).toBe(SAMPLE_RATE * 0.020);
  });
});

describe('TestChannelsDef', () => {
  it('has 5 channels', () => {
    expect(CHANNELS_DEF).toHaveLength(5);
  });

  it('channels are numbered 1 through 5', () => {
    const chNumbers = CHANNELS_DEF.map((c) => c.ch);
    expect(chNumbers).toEqual([1, 2, 3, 4, 5]);
  });

  it('ports are correct odd-numbered values', () => {
    const expectedPorts = [38801, 38803, 38805, 38807, 38809];
    const ports = CHANNELS_DEF.map((c) => c.port);
    expect(ports).toEqual(expectedPorts);
  });

  it('each channel entry has ch and port keys', () => {
    for (const def of CHANNELS_DEF) {
      expect(def).toHaveProperty('ch');
      expect(def).toHaveProperty('port');
    }
  });
});

describe('TestRingBufferConstants', () => {
  it('PCM_RING_SIZE is a power of 2', () => {
    expect(PCM_RING_SIZE).toBeGreaterThan(0);
    expect(PCM_RING_SIZE & (PCM_RING_SIZE - 1)).toBe(0);
  });

  it('JITTER_PREFILL is less than PCM_RING_SIZE', () => {
    expect(JITTER_PREFILL).toBeLessThan(PCM_RING_SIZE);
  });

  it('JITTER_PREFILL is a multiple of FRAME_SIZE', () => {
    expect(JITTER_PREFILL % FRAME_SIZE).toBe(0);
  });
});

describe('TestMsgType', () => {
  const expectedKeys = [
    'RX_AUDIO',
    'TX_AUDIO',
    'TX_TOGGLE',
    'RX_TOGGLE',
    'RX_ALL_ON',
    'RX_ALL_OFF',
    'TX_ALL_ON',
    'TX_ALL_OFF',
    'PTT_DOWN',
    'PTT_UP',
  ];

  it('has all expected keys', () => {
    for (const key of expectedKeys) {
      expect(MSG_TYPE).toHaveProperty(key);
    }
  });

  it('has no extra keys', () => {
    expect(Object.keys(MSG_TYPE)).toHaveLength(expectedKeys.length);
  });

  it('all values are unique', () => {
    const values = Object.values(MSG_TYPE);
    const unique = new Set(values);
    expect(unique.size).toBe(values.length);
  });

  it('values match expected protocol bytes', () => {
    expect(MSG_TYPE.RX_AUDIO).toBe(0x01);
    expect(MSG_TYPE.TX_AUDIO).toBe(0x02);
    expect(MSG_TYPE.TX_TOGGLE).toBe(0x03);
    expect(MSG_TYPE.RX_TOGGLE).toBe(0x04);
    expect(MSG_TYPE.PTT_DOWN).toBe(0x09);
    expect(MSG_TYPE.PTT_UP).toBe(0x0a);
  });
});

describe('TestVoxConstants', () => {
  it('VOX_HANGTIME_MS is a positive number', () => {
    expect(VOX_HANGTIME_MS).toBeGreaterThan(0);
  });

  it('VOX_HANGTIME_MS is reasonable (100-2000ms)', () => {
    expect(VOX_HANGTIME_MS).toBeGreaterThanOrEqual(100);
    expect(VOX_HANGTIME_MS).toBeLessThanOrEqual(2000);
  });
});

describe('TestNeighborHistoryConstants', () => {
  it('NEIGHBOR_HISTORY_LENGTH is a positive number', () => {
    expect(NEIGHBOR_HISTORY_LENGTH).toBeGreaterThan(0);
  });
});

describe('TestMobileBreakpoint', () => {
  it('is 768 to match the CSS grid collapse breakpoint', () => {
    expect(MOBILE_BREAKPOINT).toBe(768);
  });
});

// -----------------------------------------------------------------------------
// CSS cannot import MOBILE_BREAKPOINT, so every stylesheet that mirrors the
// shell's collapse breakpoint repeats it as a literal `768px`. The test above
// only pins the JS half; this one reads every stylesheet under src/ and pins
// the CSS half too, so a change to MOBILE_BREAKPOINT that isn't mirrored into
// every `@media (max-width: 768px)` block fails here instead of shipping a
// page whose shell and grid collapse at a different width than its panels.
function listCssFiles(dir) {
  const files = [];
  for (const name of readdirSync(dir)) {
    const full = join(dir, name);
    if (statSync(full).isDirectory()) {
      files.push(...listCssFiles(full));
    } else if (name.endsWith('.css')) {
      files.push(full);
    }
  }
  return files;
}

describe('TestMobileBreakpointCssSync', () => {
  const srcDir = join(dirname(fileURLToPath(import.meta.url)), '..');
  const cssFiles = listCssFiles(srcDir);

  it('found stylesheets to check (guards against a broken scan)', () => {
    expect(cssFiles.length).toBeGreaterThan(0);
  });

  // Every `@media` query in the file, one entry per query — not per file.
  // A file-level substring scan would pass a stylesheet that carried both a
  // correct `max-width: 768px` and a wrong `min-width: 768px`, since the
  // correct spelling appears somewhere in the text either way.
  function mediaQueries(path) {
    const content = readFileSync(path, 'utf8');
    return [...content.matchAll(/@media([^{]*)\{/g)].map((m) => ({
      path,
      query: m[1].trim(),
    }));
  }

  it('every @media query mentioning 768px is the max-width form', () => {
    const target = `max-width: ${MOBILE_BREAKPOINT}px`;
    const drifted = cssFiles
      .flatMap(mediaQueries)
      .filter(({ query }) => query.includes(`${MOBILE_BREAKPOINT}px`) && !query.includes(target))
      .map(({ path, query }) => `${path}: @media ${query}`);
    expect(drifted).toEqual([]);
  });

  it('rejects a drifted query even when a correct one sits in the same file', () => {
    // Pins the bug the file-level scan had: this input contains the expected
    // substring, so the old check passed it.
    const mixed = `
      @media (max-width: ${MOBILE_BREAKPOINT}px) { .a { color: red } }
      @media (min-width: ${MOBILE_BREAKPOINT}px) { .b { color: blue } }
    `;
    const target = `max-width: ${MOBILE_BREAKPOINT}px`;
    expect(mixed.includes(target)).toBe(true);

    const drifted = [...mixed.matchAll(/@media([^{]*)\{/g)]
      .map((m) => m[1].trim())
      .filter((query) => query.includes(`${MOBILE_BREAKPOINT}px`) && !query.includes(target));
    expect(drifted).toEqual([`(min-width: ${MOBILE_BREAKPOINT}px)`]);
  });

  it('rejects the media-query range syntax this project builds against', () => {
    // `(width<=768px)` is what an unconstrained minifier emits and what the
    // target device's browser cannot parse — the defect the cssTarget guard
    // exists for. It must not slip into hand-written CSS either.
    const target = `max-width: ${MOBILE_BREAKPOINT}px`;
    const range = `@media (width<=${MOBILE_BREAKPOINT}px) { .a { color: red } }`;
    const drifted = [...range.matchAll(/@media([^{]*)\{/g)]
      .map((m) => m[1].trim())
      .filter((query) => query.includes(`${MOBILE_BREAKPOINT}px`) && !query.includes(target));
    expect(drifted).toEqual([`(width<=${MOBILE_BREAKPOINT}px)`]);
  });
});
