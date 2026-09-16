package comms

import (
	"context"
	"errors"
	"net/netip"
	"sync"
	"sync/atomic"

	"github.com/openmanet/openmanetd/internal/comms/control"
)

// ─── mockStream ───────────────────────────────────────────────────────────────

// mockStream satisfies BroadcastCapture. Start/Stop/Close failures can be
// injected by setting the corresponding error fields before the call. The
// TX gate state is recorded separately so tests can assert that
// beginTransmission / endTransmission toggle it correctly.
//
// Under the unified-capture design, Start/Stop/Close are called at
// StartHardware / cleanup time (not per PTT cycle). The per-PTT gate is
// driven through SetTxEnabled, and txEnableCalls / txDisableCalls are
// the counters individual tests should assert against.
type mockStream struct {
	startErr        error
	stopErr         error
	closeErr        error
	startCalls      int
	stopCalls       int
	closeCalls      int
	txEnableCalls   int
	txDisableCalls  int
	txEnabledLatest bool
}

func (m *mockStream) Start() error {
	m.startCalls++

	return m.startErr
}

func (m *mockStream) Stop() error {
	m.stopCalls++

	return m.stopErr
}

func (m *mockStream) Close() error {
	m.closeCalls++

	return m.closeErr
}

func (m *mockStream) SetTxEnabled(v bool) {
	if v {
		m.txEnableCalls++
	} else {
		m.txDisableCalls++
	}

	m.txEnabledLatest = v
}

// ─── mockDecoder ─────────────────────────────────────────────────────────────

// mockDecoder satisfies AudioDecoder. It fills pcm with a fixed repeating value.
type mockDecoder struct {
	decodeErr error
	returnN   int
	fillValue int16
	forceN    bool
	// plcOK makes DecodeFloat32 succeed when payload is nil (PLC) even if
	// decodeErr is set. This allows testing the PLC-fallback-on-decode-error
	// path introduced in Change 3.
	plcOK bool
}

func (m *mockDecoder) Decode(payload []byte, pcm []int16) (int, error) {
	return m.DecodeS16(payload, pcm)
}

func (m *mockDecoder) DecodeS16(payload []byte, pcm []int16) (int, error) {
	if m.decodeErr != nil && !(m.plcOK && payload == nil) {
		return 0, m.decodeErr
	}

	for i := range pcm {
		pcm[i] = m.fillValue
	}

	if m.returnN > 0 || m.forceN {
		return m.returnN, nil
	}

	return len(pcm), nil
}

func (m *mockDecoder) Close() error { return nil }

func (m *mockDecoder) DecodeFloat32(payload []byte, pcm []float32) (int, error) {
	if m.decodeErr != nil && !(m.plcOK && payload == nil) {
		return 0, m.decodeErr
	}

	fv := float32(m.fillValue) / 32768

	for i := range pcm {
		pcm[i] = fv
	}

	if m.returnN > 0 || m.forceN {
		return m.returnN, nil
	}

	return len(pcm), nil
}

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

// mockClosingWriter adds a Close() method to mockWriter for testing the
// "close old sender if it implements io.Closer" swap path. closeCalled is
// accessed via atomic.Bool so tests that assert deferred closes (scheduled
// via time.AfterFunc by swapAndDeferClose) do not data-race under -race.
type mockClosingWriter struct {
	closeErr error
	mockWriter
	closeCalled atomicBool
}

func (m *mockClosingWriter) Close() error {
	m.closeCalled.Store(true)

	return m.closeErr
}

// atomicBool is a minimal atomic flag (a local alias so we don't pull a new
// import into mocks_test.go; callers use .Store/.Load).
type atomicBool struct{ v atomic.Bool }

func (a *atomicBool) Store(b bool) { a.v.Store(b) }
func (a *atomicBool) Load() bool   { return a.v.Load() }

// ─── trackingReader ───────────────────────────────────────────────────────────

// trackingReader is a minimal PacketReader that only tracks Close() calls.
type trackingReader struct {
	closed bool
	mu     sync.Mutex
}

func (r *trackingReader) ReadFromUDPAddrPort(_ []byte) (int, netip.AddrPort, error) {
	// Block forever until closed – tests drive this via Close().
	select {} //nolint:staticcheck
}

func (r *trackingReader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.closed = true

	return nil
}

// ─── mockPacket / mockReader ──────────────────────────────────────────────────

type mockPacket struct {
	src  netip.AddrPort
	data []byte
}

// mockReader satisfies PacketReader. Pre-loaded packets are returned one per
// call; when the queue is empty it blocks until Close is called.
type mockReader struct {
	closed  chan struct{}
	packets []mockPacket
	once    sync.Once
	mu      sync.Mutex
}

func newMockReader(pkts ...mockPacket) *mockReader {
	return &mockReader{
		packets: pkts,
		closed:  make(chan struct{}),
	}
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

// remaining returns the number of un-consumed packets (for test assertions).
func (m *mockReader) remaining() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return len(m.packets)
}

// ─── fakeErrReader ───────────────────────────────────────────────────────────

// fakeErrReader is a PacketReader whose reads always fail with err. reads
// counts attempts so tests can assert receiveLoop backs off instead of
// busy-spinning on a permanently failing socket.
type fakeErrReader struct {
	err   error
	reads atomic.Int64
}

func (f *fakeErrReader) ReadFromUDPAddrPort(_ []byte) (int, netip.AddrPort, error) {
	f.reads.Add(1)

	return 0, netip.AddrPort{}, f.err
}

func (f *fakeErrReader) Close() error { return nil }

// ─── mockEventSource ─────────────────────────────────────────────────────────

// mockEventSource satisfies control.EventSource. Pre-loaded events are sent
// on ch; closing ch causes Events to return a closed channel so Run exits
// cleanly.
type mockEventSource struct {
	ch chan control.PTTEvent
}

func (m *mockEventSource) Events(_ context.Context) <-chan control.PTTEvent {
	return m.ch
}

// ─── mockRTPSender ───────────────────────────────────────────────────────────

// mockRTPSender satisfies RTPSender. Sent payloads accumulate in Payloads.
type mockRTPSender struct {
	sendErr  error
	Payloads [][]byte
	mu       sync.Mutex
}

func (m *mockRTPSender) Send(payload []byte) error {
	if m.sendErr != nil {
		return m.sendErr
	}

	m.mu.Lock()
	cp := make([]byte, len(payload))
	copy(cp, payload)
	m.Payloads = append(m.Payloads, cp)
	m.mu.Unlock()

	return nil
}
