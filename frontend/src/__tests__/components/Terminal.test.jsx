import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render } from '@testing-library/react';

const writeMock = vi.fn();
const onDataMock = vi.fn();
const disposeMock = vi.fn();
const fitMock = vi.fn();

vi.mock('@xterm/xterm', () => ({
  Terminal: vi.fn().mockImplementation(function () {
    this.write = writeMock;
    this.onData = onDataMock;
    this.dispose = disposeMock;
    this.loadAddon = vi.fn();
    this.open = vi.fn();
    this.cols = 80;
    this.rows = 24;
  }),
}));
vi.mock('@xterm/addon-fit', () => ({
  FitAddon: vi.fn().mockImplementation(function () {
    this.fit = fitMock;
  }),
}));
vi.mock('@xterm/addon-web-links', () => ({
  WebLinksAddon: vi.fn(),
}));

let socketInstances = [];

class MockWebSocket {
  static OPEN = 1;
  static CLOSED = 3;
  constructor(url) {
    this.url = url;
    this.readyState = 0;
    this.binaryType = '';
    this.send = vi.fn();
    this.close = vi.fn(function (code, reason) {
      this.readyState = 3;
      this.onclose?.({ code, reason: reason || '', wasClean: true });
    });
    socketInstances.push(this);
  }
}

import Terminal from '../../components/Terminal.jsx';

describe('Terminal component', () => {
  beforeEach(() => {
    socketInstances = [];
    writeMock.mockClear();
    onDataMock.mockClear();
    disposeMock.mockClear();
    fitMock.mockClear();
    vi.stubGlobal('WebSocket', MockWebSocket);
  });
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('opens a websocket using the provided URL', () => {
    render(<Terminal wsUrl="ws://example/test" />);
    expect(socketInstances).toHaveLength(1);
    expect(socketInstances[0].url).toBe('ws://example/test');
  });

  it('sends a resize JSON frame when the WS opens', () => {
    render(<Terminal wsUrl="ws://example/test" />);
    socketInstances[0].readyState = MockWebSocket.OPEN;
    socketInstances[0].onopen?.();
    expect(socketInstances[0].send).toHaveBeenCalledWith(
      JSON.stringify({ type: 'resize', cols: 80, rows: 24 }),
    );
  });

  it('writes incoming binary frames to the terminal', () => {
    render(<Terminal wsUrl="ws://example/test" />);
    const bytes = new TextEncoder().encode('hi');
    socketInstances[0].onmessage?.({ data: bytes.buffer });
    expect(writeMock).toHaveBeenCalled();
    const arg = writeMock.mock.calls[0][0];
    expect(arg).toBeInstanceOf(Uint8Array);
    expect(Array.from(arg)).toEqual([0x68, 0x69]);
  });

  it('closes the websocket and disposes the terminal on unmount', () => {
    const { unmount } = render(<Terminal wsUrl="ws://example/test" />);
    unmount();
    expect(socketInstances[0].close).toHaveBeenCalledWith(1000, 'unmount');
    expect(disposeMock).toHaveBeenCalled();
  });

  // Mirrors the server's terminal MaxMessageSize (internal/terminal/config.go);
  // a larger message closes the connection and ends the shell.
  const SERVER_MAX_MESSAGE_BYTES = 64 * 1024;

  function typeIntoOpenTerminal(text) {
    render(<Terminal wsUrl="ws://example/test" />);
    const ws = socketInstances[0];
    ws.readyState = MockWebSocket.OPEN;
    const onData = onDataMock.mock.calls[0][0];
    onData(text);
    return ws.send.mock.calls.map(([payload]) => payload);
  }

  it('sends typed input as one binary message', () => {
    const sent = typeIntoOpenTerminal('ls\n');
    expect(sent).toHaveLength(1);
    expect(Array.from(sent[0])).toEqual(Array.from(new TextEncoder().encode('ls\n')));
  });

  it('splits a large paste into messages within the server limit', () => {
    // Multi-byte characters make the UTF-8 size exceed the string length.
    const paste = 'é€'.repeat(20 * 1024) + 'tail';
    const expected = new TextEncoder().encode(paste);
    expect(expected.length).toBeGreaterThan(SERVER_MAX_MESSAGE_BYTES);

    const sent = typeIntoOpenTerminal(paste);

    expect(sent.length).toBeGreaterThan(1);
    for (const chunk of sent) {
      // jsdom's TextEncoder returns another realm's Uint8Array.
      expect(ArrayBuffer.isView(chunk)).toBe(true);
      expect(chunk.length).toBeLessThanOrEqual(SERVER_MAX_MESSAGE_BYTES);
    }
    const joined = new Uint8Array(expected.length);
    let offset = 0;
    for (const chunk of sent) {
      joined.set(chunk, offset);
      offset += chunk.length;
    }
    expect(offset).toBe(expected.length);
    expect(joined.every((b, i) => b === expected[i])).toBe(true);
  });

  it('invokes onClose with the close event', () => {
    const onClose = vi.fn();
    render(<Terminal wsUrl="ws://example/test" onClose={onClose} />);
    socketInstances[0].onclose?.({ code: 1008, reason: 'in use' });
    expect(onClose).toHaveBeenCalledWith({ code: 1008, reason: 'in use' });
  });
});
