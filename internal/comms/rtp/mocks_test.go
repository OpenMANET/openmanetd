package rtp

import (
	"errors"
	"net/netip"
	"sync"
	"sync/atomic"
)

// ─── mockWriter ──────────────────────────────────────────────────────────────

// mockWriter satisfies PacketWriter. Written packets accumulate in Packets.
type mockWriter struct {
	writeErr error
	Packets  [][]byte
}

func (m *mockWriter) Write(b []byte) (int, error) {
	if m.writeErr != nil {
		return 0, m.writeErr
	}

	cp := make([]byte, len(b))
	copy(cp, b)
	m.Packets = append(m.Packets, cp)

	return len(b), nil
}

// safeMockWriter is a goroutine-safe PacketWriter for race-detector tests.
type safeMockWriter struct {
	Packets [][]byte
	mu      sync.Mutex
}

func (m *safeMockWriter) Write(b []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cp := make([]byte, len(b))
	copy(cp, b)
	m.Packets = append(m.Packets, cp)

	return len(b), nil
}

func (m *safeMockWriter) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return len(m.Packets)
}

// mockClosingWriter adds a Close() method to mockWriter for testing the
// "close old sender if it implements io.Closer" swap path.
type mockClosingWriter struct {
	closeErr error
	mockWriter
	closeCalled atomicBool
}

func (m *mockClosingWriter) Close() error {
	m.closeCalled.Store(true)

	return m.closeErr
}

type atomicBool struct{ v atomic.Bool }

func (a *atomicBool) Store(b bool) { a.v.Store(b) }
func (a *atomicBool) Load() bool   { return a.v.Load() }

// ─── trackingReader ──────────────────────────────────────────────────────────

// trackingReader is a minimal PacketReader that only tracks Close() calls.
type trackingReader struct {
	closed bool
	mu     sync.Mutex
}

func (r *trackingReader) ReadFromUDPAddrPort(_ []byte) (int, netip.AddrPort, error) {
	select {} //nolint:staticcheck
}

func (r *trackingReader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.closed = true

	return nil
}

// ─── safeCountingReader ──────────────────────────────────────────────────────

type safeCountingReader struct {
	calls atomic.Int64
}

func (r *safeCountingReader) ReadFromUDPAddrPort(b []byte) (int, netip.AddrPort, error) {
	r.calls.Add(1)

	if len(b) > 0 {
		b[0] = 0xAB
	}

	return 1, netip.AddrPort{}, nil
}

func (r *safeCountingReader) Close() error { return nil }

func (r *safeCountingReader) count() int64 { return r.calls.Load() }

// ─── mockReader (minimal version for rtp tests) ──────────────────────────────

type mockPacket struct {
	src  netip.AddrPort
	data []byte
}

type mockReader struct {
	closed  chan struct{}
	packets []mockPacket
	once    sync.Once
	mu      sync.Mutex
}

//nolint:unused // kept in sync with parent package surface
func newMockReader(pkts ...mockPacket) *mockReader {
	return &mockReader{packets: pkts, closed: make(chan struct{})}
}

func (m *mockReader) ReadFromUDPAddrPort(b []byte) (int, netip.AddrPort, error) {
	for {
		m.mu.Lock()
		if len(m.packets) > 0 {
			pkt := m.packets[0]
			m.packets = m.packets[1:]
			n := copy(b, pkt.data)
			m.mu.Unlock()

			return n, pkt.src, nil
		}
		m.mu.Unlock()

		select {
		case <-m.closed:
			return 0, netip.AddrPort{}, errors.New("reader closed")
		default:
		}
	}
}

func (m *mockReader) Close() error {
	m.once.Do(func() { close(m.closed) })

	return nil
}
