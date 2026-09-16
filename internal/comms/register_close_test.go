package comms

import (
	"errors"
	"net/netip"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openmanet/openmanetd/internal/comms/control"
	"github.com/openmanet/openmanetd/internal/comms/rtp"
)

// ─── controlLookup ────────────────────────────────────────────────────────────

// TestControlLookup_KnownNames verifies that controlLookup is a thin pass-
// through to control.Lookup for every name registered in init().
func TestControlLookup_KnownNames(t *testing.T) {
	for _, name := range []string{
		defaultCtrlSrc,
		controlSourceROIP,
		controlSourceWeb,
		defaultControlSourceNanoPTT,
	} {
		t.Run(name, func(t *testing.T) {
			f, ok := controlLookup(name)
			require.True(t, ok, "controlLookup(%q) miss", name)
			assert.NotNil(t, f)
		})
	}
}

func TestControlLookup_UnknownName(t *testing.T) {
	f, ok := controlLookup("does-not-exist")
	assert.False(t, ok)
	assert.Nil(t, f)
}

// ─── buildControlDeps ─────────────────────────────────────────────────────────

func TestBuildControlDeps_OpenVLMBackend(t *testing.T) {
	cfg := &CommsConfig{Log: zerolog.Nop(), ControlSource: defaultCtrlSrc}
	rt := &CommsRuntime{}

	deps, err := cfg.buildControlDeps(rt)
	require.NoError(t, err)

	_, ok := deps.Backend.(*openvlmBackend)
	assert.True(t, ok, "openvlm backend should have type *openvlmBackend, got %T", deps.Backend)
}

func TestBuildControlDeps_ROIPBackend(t *testing.T) {
	cfg := &CommsConfig{
		Log:               zerolog.Nop(),
		ControlSource:     controlSourceROIP,
		ROIPCOSGPIOMask:   0x04,
		ROIPVOXThreshold:  0.05,
		ROIPVOXHoldTime:   0,
		ROIPMaxTXDuration: 0,
	}
	rt := &CommsRuntime{}

	deps, err := cfg.buildControlDeps(rt)
	require.NoError(t, err)

	b, ok := deps.Backend.(*roipBackend)
	require.True(t, ok, "roip backend wrong type %T", deps.Backend)

	assert.Equal(t, byte(0x04), b.COSGPIOMask)
	assert.InDelta(t, 0.05, b.VOXThreshold, 1e-6)
	assert.NotNil(t, b.IsReceiving)
	assert.NotNil(t, b.IsBroadcasting)
	assert.NotNil(t, b.SetTap)
	assert.NotNil(t, b.ClearTap)

	// Smoke-test the closures so the captured runtime references compile and
	// produce the expected zero-value behavior on a fresh CommsRuntime.
	assert.False(t, b.IsBroadcasting(), "Broadcasting atomic defaults to false")

	ch := make(chan []float32, 1)
	b.SetTap(ch)
	assert.NotNil(t, rt.BroadcastTap.Load(), "SetTap must publish the channel pointer")

	b.ClearTap()
	assert.Nil(t, rt.BroadcastTap.Load(), "ClearTap must reset the pointer")
}

func TestBuildControlDeps_WebBackendSinkPublishes(t *testing.T) {
	cfg := &CommsConfig{Log: zerolog.Nop(), ControlSource: controlSourceWeb}
	rt := &CommsRuntime{}

	deps, err := cfg.buildControlDeps(rt)
	require.NoError(t, err)

	b, ok := deps.Backend.(*webBackend)
	require.True(t, ok, "web backend wrong type %T", deps.Backend)
	require.NotNil(t, b.Sink)

	// Sink writes the constructed event source back into the runtime.
	src := control.NewWebEventSource(zerolog.Nop())
	b.Sink(src)
	assert.Same(t, src, rt.WebEvtSrc, "Sink should populate rt.WebEvtSrc")
}

func TestBuildControlDeps_NanoPTTBackend(t *testing.T) {
	cfg := &CommsConfig{Log: zerolog.Nop(), ControlSource: defaultControlSourceNanoPTT}
	rt := &CommsRuntime{}

	deps, err := cfg.buildControlDeps(rt)
	require.NoError(t, err)

	b, ok := deps.Backend.(*nanopttBackend)
	require.True(t, ok, "nanoptt backend wrong type %T", deps.Backend)
	assert.Same(t, cfg, b.Cfg)
}

func TestBuildControlDeps_UnknownSourceErrors(t *testing.T) {
	cfg := &CommsConfig{Log: zerolog.Nop(), ControlSource: "no-such-source"}
	rt := &CommsRuntime{}

	_, err := cfg.buildControlDeps(rt)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no-such-source")
}

// ─── PortChannel.closePartial ─────────────────────────────────────────────────

// TestClosePartial_NilReceiver_NoOp verifies that closePartial is safe to
// invoke on a nil *PortChannel — the bulk-cleanup path in buildNetwork
// relies on this when an entry in rt.Ports is left nil after a partial
// build failure.
func TestClosePartial_NilReceiver_NoOp(t *testing.T) {
	var pc *PortChannel

	// Must not panic.
	pc.closePartial()
}

// TestClosePartial_AllNilFields_NoOp verifies that closePartial tolerates
// any combination of unset fields — the rollback path inside
// buildSinglePortChannel calls closePartial as soon as the first error
// occurs, regardless of how much state has actually been built.
func TestClosePartial_AllNilFields_NoOp(t *testing.T) {
	pc := &PortChannel{}

	// Must not panic and must not write to any field.
	pc.closePartial()
}

// TestClosePartial_ClosesAllSetFields installs mock readers/writers for
// every closeable PortChannel field and verifies each one is closed exactly
// once. The non-Session RTPSess branch is exercised by leaving RTPSess nil;
// the Session branch is impossible to fake without opening a real socket
// (rtp.Session.Close walks the pion API), so the type-assertion safety net
// is verified by leaving the field nil.
func TestClosePartial_ClosesAllSetFields(t *testing.T) {
	receiverReader := &trackingReader{}
	senderWriter := &mockClosingWriter{}
	rtcpWriter := &mockClosingWriter{}

	pc := &PortChannel{
		Receiver: rtp.NewSwappableReceiver(receiverReader),
		Sender:   rtp.NewSwappableSender(senderWriter),
		RTCPSend: rtp.NewSwappableSender(rtcpWriter),
	}

	pc.closePartial()

	assert.True(t, receiverReader.closed, "Receiver close should propagate to underlying reader")
	assert.True(t, senderWriter.closeCalled.Load(), "Sender close should propagate to underlying writer")
	assert.True(t, rtcpWriter.closeCalled.Load(), "RTCPSend close should propagate to underlying writer")
}

// TestClosePartial_NonSessionRTPSess verifies the type-assertion-safe
// branch in closePartial: when RTPSess is set but is NOT a *rtp.Session
// (e.g. a unit-test fake), closePartial must skip it without panicking.
func TestClosePartial_NonSessionRTPSess(t *testing.T) {
	pc := &PortChannel{
		// mockRTPSender satisfies the rtp.Sender interface but is not
		// *rtp.Session — the type assertion in closePartial must fail
		// safely and leave the sender alone.
		RTPSess: &mockRTPSender{},
	}

	// Must not panic and must not require RTPSess.Close to exist.
	pc.closePartial()
}

// TestClosePartial_DoesNotPanicOnFailedReceiverClose makes sure errors
// returned by the underlying close are silently swallowed (closePartial is
// best-effort cleanup, not error-reporting cleanup).
func TestClosePartial_DoesNotPanicOnFailedReceiverClose(t *testing.T) {
	rcv := &errClosingReader{closeErr: errors.New("simulated close failure")}

	pc := &PortChannel{
		Receiver: rtp.NewSwappableReceiver(rcv),
	}

	// Must not panic, and must not propagate the error.
	pc.closePartial()
	assert.True(t, rcv.closed, "Close should still be invoked even if it errors")
}

// errClosingReader is a tiny PacketReader whose Close returns a configured
// error so we can exercise the swallow-error branch in closePartial. It is
// not exported and lives only in this test file.
type errClosingReader struct {
	closeErr error
	closed   bool
}

func (r *errClosingReader) ReadFromUDPAddrPort(_ []byte) (int, netip.AddrPort, error) {
	select {} //nolint:staticcheck
}

func (r *errClosingReader) Close() error {
	r.closed = true

	return r.closeErr
}
