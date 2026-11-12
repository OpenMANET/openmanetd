import React, { useEffect, useRef } from 'react';
import { Terminal as XTerm } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import { WebLinksAddon } from '@xterm/addon-web-links';
import '@xterm/xterm/css/xterm.css';

// Lattice design-token palette for the xterm theme.
const LATTICE_THEME = {
  background:          '#05080a',
  foreground:          '#d9e6ed',
  cursor:              '#00e5ff',
  cursorAccent:        '#05080a',
  selectionBackground: 'rgba(0, 229, 255, 0.35)',
  black:               '#0a0f13',
  red:                 '#ff3b4d',
  green:               '#00e676',
  yellow:              '#ffb300',
  blue:                '#00b8cc',
  magenta:             '#c678dd',
  cyan:                '#00e5ff',
  white:               '#d9e6ed',
  brightBlack:         '#5c7682',
  brightRed:           '#ff3b4d',
  brightGreen:         '#00e676',
  brightYellow:        '#ffb300',
  brightBlue:          '#00e5ff',
  brightMagenta:       '#c678dd',
  brightCyan:          '#00e5ff',
  brightWhite:         '#ffffff',
};

// The server ends the session on any message over 64 KiB (MaxMessageSize in
// internal/terminal/config.go), and xterm delivers a whole paste as one
// onData event, so input is sent in chunks well under that limit. Splitting
// UTF-8 mid-character is safe: the server writes the bytes to the PTY in order.
const INPUT_CHUNK_BYTES = 16 * 1024;

function sendInput(ws, text) {
  const bytes = new TextEncoder().encode(text);
  for (let offset = 0; offset < bytes.length; offset += INPUT_CHUNK_BYTES) {
    ws.send(bytes.subarray(offset, offset + INPUT_CHUNK_BYTES));
  }
}

function sendResize(ws, cols, rows) {
  if (ws.readyState !== WebSocket.OPEN) return;
  ws.send(JSON.stringify({ type: 'resize', cols, rows }));
}

/**
 * Terminal — dumb xterm.js host component.
 *
 * Props:
 *   wsUrl   {string}   WebSocket URL to connect to.
 *   onClose {function} Optional callback invoked with the CloseEvent.
 */
export default function Terminal({ wsUrl, onClose }) {
  const containerRef = useRef(null);

  useEffect(() => {
    const term = new XTerm({
      fontFamily: 'JetBrains Mono, ui-monospace, SFMono-Regular, Menlo, monospace',
      fontSize: 13,
      cursorBlink: true,
      scrollback: 5000,
      theme: LATTICE_THEME,
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.loadAddon(new WebLinksAddon());
    term.open(containerRef.current);
    try { fit.fit(); } catch { /* container may not be sized yet in tests */ }
    try { term.focus(); } catch { /* term may not be focusable in tests */ }

    const ws = new WebSocket(wsUrl);
    ws.binaryType = 'arraybuffer';

    ws.onopen = () => sendResize(ws, term.cols, term.rows);

    ws.onmessage = (e) => {
      if (typeof e.data === 'string') return; // ignore unsolicited text frames
      term.write(new Uint8Array(e.data));
    };

    ws.onclose = (e) => {
      try {
        term.write(`\r\n[session closed: ${e.reason || e.code}]\r\n`);
      } catch { /* term may already be disposed */ }
      onClose?.(e);
    };

    const dataDisposer = term.onData((d) => {
      if (ws.readyState === WebSocket.OPEN) {
        sendInput(ws, d);
      }
    });

    let ro;
    if (typeof ResizeObserver !== 'undefined') {
      ro = new ResizeObserver(() => {
        try { fit.fit(); } catch { /* noop */ }
        sendResize(ws, term.cols, term.rows);
      });
      ro.observe(containerRef.current);
    }

    return () => {
      ro?.disconnect();
      dataDisposer?.dispose?.();
      try { ws.close(1000, 'unmount'); } catch { /* noop */ }
      term.dispose();
    };
  }, [wsUrl, onClose]);

  return <div ref={containerRef} className="lat-terminal" />;
}
