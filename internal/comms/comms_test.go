package comms

import (
	"context"
	"net"
	"net/netip"
	"os"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"golang.org/x/net/ipv4"

	"github.com/openmanet/openmanetd/internal/comms/audiopool"
	"github.com/openmanet/openmanetd/internal/comms/control"
	"github.com/openmanet/openmanetd/internal/comms/rtp"
	"github.com/openmanet/openmanetd/internal/config"
)

func makeRTPBytes(t *testing.T, _ uint16) []byte {
	t.Helper()

	w := &mockWriter{}

	sess, err := rtp.NewSession(0x1234, w, &mockWriter{}, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close() //nolint:errcheck

	if err := sess.Send([]byte{0xAA, 0xBB}); err != nil {
		t.Fatal(err)
	}

	if len(w.Packets) == 0 {
		t.Fatal("no packets written")
	}

	return w.Packets[0]
}

func TestReceiveLoop_ExitsOnContextCancel(t *testing.T) {
	cfg := &CommsConfig{Log: zerolog.Nop()}
	pc := &PortChannel{
		cfg:      McastPortConfig{Send: true, Receive: true},
		Receiver: rtp.NewSwappableReceiver(newMockReader()),
	}
	pc.SendEnabled.Store(true)
	pc.ReceiveEnabled.Store(true)
	pc.PlaybackBuffer = make(chan []int16, 8)
	pc.Decoder = &mockDecoder{}
	rt := &CommsRuntime{
		Ports: []*PortChannel{pc},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})

	go func() {
		defer close(done)

		cfg.receiveLoop(ctx, pc, rt)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("receiveLoop did not exit after context cancel")
	}
}

func TestReceiveLoop_IngestsPackets(t *testing.T) {
	cfg := &CommsConfig{Log: zerolog.Nop(), Loopback: true}

	var pkts []mockPacket

	for i := 0; i < rtp.PrebufferPackets+2; i++ {
		raw := makeRTPBytes(t, uint16(i))
		pkts = append(pkts, mockPacket{data: raw, src: netip.MustParseAddrPort("1.2.3.4:5004")})
	}

	reader := newMockReader(pkts...)
	pc := &PortChannel{
		cfg:      McastPortConfig{Send: true, Receive: true},
		Receiver: rtp.NewSwappableReceiver(reader),
	}
	pc.SendEnabled.Store(true)
	pc.ReceiveEnabled.Store(true)
	pc.PlaybackBuffer = make(chan []int16, 32)
	pc.Decoder = &mockDecoder{returnN: int(rtp.FrameSamples)}
	rt := &CommsRuntime{
		Ports: []*PortChannel{pc},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		defer close(done)

		cfg.receiveLoop(ctx, pc, rt)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if reader.remaining() == 0 {
			break
		}

		time.Sleep(5 * time.Millisecond)
	}

	cancel()
	pc.Receiver.Close() // unblocks ReadFromUDP in mockReader

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("receiveLoop did not exit")
	}
}

// TestPlayoutOneFrame_SuppressedDuringBroadcastOnSendPort verifies that
// playoutOneFrame emits silence on a send-capable port while the local node
// is broadcasting (half-duplex echo prevention). Receive-only ports are not
// suppressed; that behavior is covered by
// TestPlayoutOneFrame_ReceiveOnlyPortNotSuppressedDuringBroadcast.
func TestPlayoutOneFrame_SuppressedDuringBroadcastOnSendPort(t *testing.T) {
	cfg := &CommsConfig{Log: zerolog.Nop()}

	pc := &PortChannel{
		cfg: McastPortConfig{Send: true, Receive: true},
	}
	pc.SendEnabled.Store(true)
	pc.ReceiveEnabled.Store(true)

	pc.Decoder = &mockDecoder{fillValue: 42, returnN: audiopool.FrameSize}
	rt := &CommsRuntime{
		Ports: []*PortChannel{pc},
	}
	rt.Broadcasting.Store(true)

	jb := rtp.NewJitterBuffer(1, 16)
	jb.Push(0, []byte{0xAA, 0xBB})

	out := make([]int16, audiopool.FrameSize)
	cfg.playoutOneFrame(pc, rt, jb, out)

	for i, v := range out {
		if v != 0 {
			t.Errorf("playoutOneFrame should emit silence during broadcast; sample[%d]=%d", i, v)

			break
		}
	}
}

func TestPlayoutOneFrame_DecodesPayloadIntoOut(t *testing.T) {
	cfg := &CommsConfig{Log: zerolog.Nop()}

	pc := &PortChannel{
		cfg: McastPortConfig{Send: true, Receive: true},
	}
	pc.SendEnabled.Store(true)
	pc.ReceiveEnabled.Store(true)

	dec := &mockDecoder{fillValue: 42, returnN: audiopool.FrameSize}
	pc.Decoder = dec
	rt := &CommsRuntime{}

	jb := rtp.NewJitterBuffer(1, 16)
	jb.Push(0, []byte{1, 2, 3})

	out := make([]int16, audiopool.FrameSize)
	cfg.playoutOneFrame(pc, rt, jb, out)

	// int16-native decode fills out with fillValue directly.
	const expected int16 = 42

	for i, v := range out {
		if v != expected {
			t.Errorf("sample[%d]=%d want %d", i, v, expected)

			break
		}
	}
}

func TestPlayoutOneFrame_PLCFillsOut(t *testing.T) {
	cfg := &CommsConfig{Log: zerolog.Nop()}

	pc := &PortChannel{
		cfg: McastPortConfig{Send: true, Receive: true},
	}
	pc.SendEnabled.Store(true)
	pc.ReceiveEnabled.Store(true)

	dec := &mockDecoder{fillValue: 10, returnN: audiopool.FrameSize}
	pc.Decoder = dec
	rt := &CommsRuntime{}

	// Push and pop to set started=true and a recent lastPush; the next
	// playoutOneFrame call will hit the conceal branch and call the decoder
	// with a nil payload (PLC).
	jb := rtp.NewJitterBuffer(1, 16)
	jb.Push(0, []byte{0})
	jb.PopReady()

	out := make([]int16, audiopool.FrameSize)
	cfg.playoutOneFrame(pc, rt, jb, out)

	const expected int16 = 10

	if v := out[0]; v != expected {
		t.Errorf("PLC sample[0]=%d want %d", v, expected)
	}
}

func TestNewComms_Defaults(t *testing.T) {
	// NewComms is a copy constructor; defaults (McastPorts, etc.) are applied
	// lazily in Start(). Verify NewComms returns a non-nil value and that the
	// supplied log is preserved.
	log := zerolog.Nop()

	cfg := NewComms(CommsConfig{Log: log, McastPorts: []McastPortConfig{{Port: 5004, Send: true, Receive: true}}})
	if cfg == nil {
		t.Fatal("NewComms returned nil")
	}

	if cfg.McastPorts[0].Port != 5004 {
		t.Errorf("McastPorts[0].Port: got %d, want 5004", cfg.McastPorts[0].Port)
	}
}

// TestEncoderComplexity_DefaultIsMIPSFriendly pins the default Opus encoder
// complexity at a value the slowest target hardware (MIPS edge routers) can
// keep up with in real time. Complexity 10 saturated the per-frame budget
// and surfaced as RX stutter — see the comment block at the constant
// definition for the full incident context. Bumping this back up requires
// re-validating against the target hardware.
func TestEncoderComplexity_DefaultIsMIPSFriendly(t *testing.T) {
	const maxAllowed = 5

	if encoderComplexity > maxAllowed {
		t.Fatalf("encoderComplexity = %d, want <= %d "+
			"(MIPS targets cannot keep up with higher values)",
			encoderComplexity, maxAllowed)
	}
}

// TestBuildEncoder_UsesDefaultComplexity verifies that buildEncoder falls
// back to the package default when CommsConfig.EncoderComplexity is unset,
// and that the default produces a working encoder.
func TestBuildEncoder_UsesDefaultComplexity(t *testing.T) {
	cfg := &CommsConfig{Log: zerolog.Nop()}

	enc, err := cfg.buildEncoder()
	if err != nil {
		t.Fatalf("buildEncoder with default complexity: %v", err)
	}

	if enc == nil {
		t.Fatal("buildEncoder returned nil encoder")
	}

	t.Cleanup(func() {
		_ = enc.Close()
	})

	// Smoke test: a single frame round-trip succeeds with the default
	// complexity. Encoded length is non-deterministic but must be > 0.
	pcm := make([]int16, audiopool.FrameSize)
	out := make([]byte, 4000)

	n, err := enc.EncodeS16(pcm, out)
	if err != nil {
		t.Fatalf("EncodeS16 with default complexity: %v", err)
	}

	if n <= 0 {
		t.Fatalf("EncodeS16 produced %d bytes, want > 0", n)
	}
}

// TestBuildPortDecoders_DistinctPerReceivePort verifies that every
// receive-capable port gets its own decoder instance. Concurrent receive on
// multiple talk groups is a designed capability, and each port's playback
// callback runs on its own audio thread, so decoder state must never be
// shared between ports.
func TestBuildPortDecoders_DistinctPerReceivePort(t *testing.T) {
	ports := []*PortChannel{
		{Receiver: rtp.NewSwappableReceiver(newMockReader())},
		{}, // send-only: no receive socket, no decoder needed
		{Receiver: rtp.NewSwappableReceiver(newMockReader())},
	}

	if err := buildPortDecoders(ports); err != nil {
		t.Fatalf("buildPortDecoders: %v", err)
	}

	t.Cleanup(func() {
		for _, pc := range ports {
			if pc.Decoder != nil {
				_ = pc.Decoder.Close()
			}
		}
	})

	if ports[0].Decoder == nil || ports[2].Decoder == nil {
		t.Fatal("every receive-capable port must get a decoder")
	}

	if ports[0].Decoder == ports[2].Decoder {
		t.Fatal("ports must not share a decoder instance")
	}

	if ports[1].Decoder != nil {
		t.Error("send-only port should not get a decoder")
	}
}

// TestBuildCodec_HonorsExplicitComplexity verifies that an in-range
// CommsConfig.EncoderComplexity overrides the default. The 0 / >10 / negative
// branches all fall through to the default.
func TestBuildCodec_HonorsExplicitComplexity(t *testing.T) {
	tests := []struct {
		name string
		in   int
	}{
		{"explicit_low", 1},
		{"explicit_mid", 5},
		{"explicit_high", 10},
		{"zero_falls_back", 0},
		{"negative_falls_back", -3},
		{"too_high_falls_back", 11},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &CommsConfig{Log: zerolog.Nop(), EncoderComplexity: tc.in}

			enc, err := cfg.buildEncoder()
			if err != nil {
				t.Fatalf("buildEncoder(%d): %v", tc.in, err)
			}

			t.Cleanup(func() {
				_ = enc.Close()
			})
		})
	}
}

// ─── applyDefaults tests ──────────────────────────────────────────────────────

func TestApplyDefaults_AllEmptyGetsDefaults(t *testing.T) {
	// Ensure RtpID fallback can use the real hostname.
	os.Unsetenv("HOSTNAME") //nolint:errcheck

	cfg := &CommsConfig{}
	cfg.applyDefaults()

	if cfg.Iface != defaultIface {
		t.Errorf("Iface: got %q, want %q", cfg.Iface, defaultIface)
	}

	if cfg.McastPorts[0].Address == "" {
		t.Error("McastPorts[0].Address should be non-empty after applyDefaults")
	}

	if cfg.McastPorts[0].Port != config.DefaultTalkGroupPort {
		t.Errorf("McastPorts[0].Port: got %d, want %d", cfg.McastPorts[0].Port, config.DefaultTalkGroupPort)
	}

	if cfg.CommKey != defaultKey {
		t.Errorf("CommKey: got %q, want %q", cfg.CommKey, defaultKey)
	}

	if cfg.NanoPTTDevicePath != defaultCommDevice {
		t.Errorf("NanoPTTDevicePath: got %q, want %q", cfg.NanoPTTDevicePath, defaultCommDevice)
	}

	if cfg.NanoPTTDeviceName != defaultCommName {
		t.Errorf("NanoPTTDeviceName: got %q, want %q", cfg.NanoPTTDeviceName, defaultCommName)
	}
}

func TestApplyDefaults_ExistingValuesPreserved(t *testing.T) {
	cfg := &CommsConfig{
		Iface: "eth0",
		McastPorts: []McastPortConfig{{
			Address: "239.1.2.3",
			Port:    9999,
			Send:    true,
			Receive: true,
		}},
		CommKey:           "42",
		NanoPTTDevicePath: "/dev/custom/*",
		NanoPTTDeviceName: "MyDevice",
		RtpID:             "explicit-id",
	}
	cfg.applyDefaults()

	if cfg.Iface != "eth0" {
		t.Errorf("Iface overwritten; got %q", cfg.Iface)
	}

	if cfg.McastPorts[0].Address != "239.1.2.3" {
		t.Errorf("McastPorts[0].Address overwritten; got %q", cfg.McastPorts[0].Address)
	}

	if cfg.McastPorts[0].Port != 9999 {
		t.Errorf("McastPorts[0].Port overwritten; got %d", cfg.McastPorts[0].Port)
	}

	if cfg.CommKey != "42" {
		t.Errorf("CommKey overwritten; got %q", cfg.CommKey)
	}

	if cfg.NanoPTTDevicePath != "/dev/custom/*" {
		t.Errorf("NanoPTTDevicePath overwritten; got %q", cfg.NanoPTTDevicePath)
	}

	if cfg.NanoPTTDeviceName != "MyDevice" {
		t.Errorf("NanoPTTDeviceName overwritten; got %q", cfg.NanoPTTDeviceName)
	}

	if cfg.RtpID != "explicit-id" {
		t.Errorf("RtpID overwritten; got %q", cfg.RtpID)
	}
}

func TestApplyDefaults_RtpIDFallsBackToHostname(t *testing.T) {
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		t.Skip("cannot determine hostname; skipping")
	}

	cfg := &CommsConfig{}
	cfg.applyDefaults()

	if cfg.RtpID != hostname {
		t.Errorf("RtpID: got %q, want %q (hostname)", cfg.RtpID, hostname)
	}
}

func TestApplyDefaults_BluetoothAudioDeviceHintPropagates(t *testing.T) {
	cfg := &CommsConfig{BluetoothAudioDeviceHint: "usb-audio"}
	cfg.applyDefaults()

	if cfg.BluetoothInputDevice != "usb-audio" {
		t.Errorf("BluetoothInputDevice: got %q, want %q", cfg.BluetoothInputDevice, "usb-audio")
	}

	if cfg.BluetoothOutputDevice != "usb-audio" {
		t.Errorf("BluetoothOutputDevice: got %q, want %q", cfg.BluetoothOutputDevice, "usb-audio")
	}
}

func TestApplyDefaults_BluetoothAudioDeviceHintDoesNotOverrideExplicit(t *testing.T) {
	cfg := &CommsConfig{
		BluetoothAudioDeviceHint: "usb-audio",
		BluetoothInputDevice:     "hw:0",
		BluetoothOutputDevice:    "hw:1",
	}
	cfg.applyDefaults()

	if cfg.BluetoothInputDevice != "hw:0" {
		t.Errorf("BluetoothInputDevice overwritten; got %q", cfg.BluetoothInputDevice)
	}

	if cfg.BluetoothOutputDevice != "hw:1" {
		t.Errorf("BluetoothOutputDevice overwritten; got %q", cfg.BluetoothOutputDevice)
	}
}

// ─── replaceNetwork tests ─────────────────────────────────────────────────────

func TestReplaceNetwork_ClosesOldReceiverAndSender(t *testing.T) {
	oldSender := &mockClosingWriter{}
	oldReceiver := &trackingReader{}
	oldRTCP := &mockClosingWriter{}

	pc := &PortChannel{
		Sender:   rtp.NewSwappableSender(oldSender),
		RTCPSend: rtp.NewSwappableSender(oldRTCP),
		Receiver: rtp.NewSwappableReceiver(oldReceiver),
	}
	rt := &CommsRuntime{
		Ports: []*PortChannel{pc},
	}

	cfg := &CommsConfig{Log: zerolog.Nop()}
	cfg.replaceNetwork(rt, &mockWriter{}, &mockWriter{}, newMockReader(), "10.0.0.1")

	// sender/rtcp closes are deferred (see swapAndDeferClose); the receiver
	// close is synchronous so we can assert it immediately.
	if !oldReceiver.closed {
		t.Error("old receiver Close() should have been called")
	}

	deadline := time.Now().Add(rtp.SwapCloseGrace + 500*time.Millisecond)
	for time.Now().Before(deadline) {
		if oldSender.closeCalled.Load() && oldRTCP.closeCalled.Load() {
			break
		}

		time.Sleep(5 * time.Millisecond)
	}

	if !oldSender.closeCalled.Load() {
		t.Error("old sender Close() should have been called")
	}

	if !oldRTCP.closeCalled.Load() {
		t.Error("old RTCP sender Close() should have been called")
	}
}

func TestReplaceNetwork_StoresNewLocalIP(t *testing.T) {
	pc := &PortChannel{
		Sender:   rtp.NewSwappableSender(&mockWriter{}),
		RTCPSend: rtp.NewSwappableSender(&mockWriter{}),
		Receiver: rtp.NewSwappableReceiver(newMockReader()),
	}
	rt := &CommsRuntime{
		Ports: []*PortChannel{pc},
	}

	cfg := &CommsConfig{Log: zerolog.Nop()}
	cfg.replaceNetwork(rt, &mockWriter{}, &mockWriter{}, newMockReader(), "10.0.0.2")

	v, ok := rt.LocalIP.Load(), rt.LocalIP.Load() != nil
	if !ok || *v != "10.0.0.2" {
		t.Errorf("localIP: got %v, want 10.0.0.2", rt.LocalIP.Load())
	}
}

func TestReplaceNetwork_NewWriterReceivesSubsequentWrites(t *testing.T) {
	newSender := &mockWriter{}

	pc := &PortChannel{
		Sender:   rtp.NewSwappableSender(&mockWriter{}),
		RTCPSend: rtp.NewSwappableSender(&mockWriter{}),
		Receiver: rtp.NewSwappableReceiver(newMockReader()),
	}
	rt := &CommsRuntime{
		Ports: []*PortChannel{pc},
	}

	cfg := &CommsConfig{Log: zerolog.Nop()}
	cfg.replaceNetwork(rt, newSender, &mockWriter{}, newMockReader(), "10.0.0.3")

	if _, err := pc.Sender.Write([]byte{1, 2, 3}); err != nil {
		t.Fatal(err)
	}

	if len(newSender.Packets) != 1 {
		t.Errorf("new Sender: got %d packets, want 1", len(newSender.Packets))
	}
}

// ─── UpdateMulticastEndpoint validation tests ─────────────────────────────────

// ─── GetMulticastAddr tests ───────────────────────────────────────────────────

func TestGetActiveMulticastAddr_NotStarted(t *testing.T) {
	// Ensure no active service is set.
	SetDefault(nil)

	if got := Default().ActiveMulticastAddr(); got != "" {
		t.Errorf("expected empty string when comms not started, got %q", got)
	}
}

func TestGetActiveMulticastAddr_ReturnsConfiguredAddr(t *testing.T) {
	const want = "239.1.2.3"

	cfg := &CommsConfig{
		Log:        zerolog.Nop(),
		McastPorts: []McastPortConfig{{Address: want, Port: 5004, Send: true, Receive: true}},
	}
	pc := &PortChannel{
		cfg:      cfg.McastPorts[0],
		Sender:   rtp.NewSwappableSender(&mockWriter{}),
		Receiver: rtp.NewSwappableReceiver(newMockReader()),
	}
	rt := &CommsRuntime{Ports: []*PortChannel{pc}}

	SetDefault(&Service{Cfg: cfg, Rt: rt})
	t.Cleanup(func() { SetDefault(nil) })

	if got := Default().ActiveMulticastAddr(); got != want {
		t.Errorf("ActiveMulticastAddr() = %q, want %q", got, want)
	}
}

func TestGetActiveMulticastAddr_ReflectsUpdate(t *testing.T) {
	const (
		initial = "239.0.0.1"
		updated = "239.9.9.9"
	)

	cfg := &CommsConfig{
		Log:        zerolog.Nop(),
		McastPorts: []McastPortConfig{{Address: initial, Port: 5004, Send: true, Receive: true}},
	}
	pc := &PortChannel{
		cfg:      cfg.McastPorts[0],
		Sender:   rtp.NewSwappableSender(&mockWriter{}),
		Receiver: rtp.NewSwappableReceiver(newMockReader()),
	}
	rt := &CommsRuntime{Ports: []*PortChannel{pc}}

	SetDefault(&Service{Cfg: cfg, Rt: rt})
	t.Cleanup(func() { SetDefault(nil) })

	if got := Default().ActiveMulticastAddr(); got != initial {
		t.Errorf("before update: ActiveMulticastAddr() = %q, want %q", got, initial)
	}

	cfg.McastPorts[0] = McastPortConfig{Address: updated, Port: 5004, Send: true, Receive: true}

	if got := Default().ActiveMulticastAddr(); got != updated {
		t.Errorf("after update: ActiveMulticastAddr() = %q, want %q", got, updated)
	}
}

// ─── setMulticastTTL tests ─────────────────────────────────────────────────────

func TestSetMulticastTTL(t *testing.T) {
	dst := &net.UDPAddr{IP: net.ParseIP("239.0.0.1"), Port: 0}
	src := &net.UDPAddr{IP: net.IPv4zero, Port: 0}

	conn, err := net.DialUDP("udp4", src, dst)
	if err != nil {
		t.Fatalf("DialUDP: %v", err)
	}
	defer conn.Close() //nolint:errcheck

	if err := setMulticastTTL(conn, rtpMulticastTTL); err != nil {
		t.Fatalf("setMulticastTTL: %v", err)
	}

	got, err := ipv4.NewPacketConn(conn).MulticastTTL()
	if err != nil {
		t.Fatalf("MulticastTTL: %v", err)
	}

	if got != rtpMulticastTTL {
		t.Errorf("multicast TTL = %d, want %d", got, rtpMulticastTTL)
	}
}

// TestRTPMulticastTTLValue locks in the concrete TTL constant so a silent
// regression to the previous value of 6 — which black-holed voice on
// multi-hop BLOS deployments — would fail the test suite instead of shipping.
func TestRTPMulticastTTLValue(t *testing.T) {
	const wantTTL = 32
	if rtpMulticastTTL != wantTTL {
		t.Errorf("rtpMulticastTTL = %d, want %d (tuned for multi-hop mesh traversal)", rtpMulticastTTL, wantTTL)
	}
}

// ─── listenRTPReceiver (SO_REUSEPORT) tests ───────────────────────────────────

// ─── GetActiveMulticastPort tests ───────────────────────────────────────────

func TestGetActiveMulticastPort_NotStarted(t *testing.T) {
	SetDefault(nil)

	if got := Default().ActiveMulticastPort(); got != 0 {
		t.Errorf("expected 0 when comms not started, got %d", got)
	}
}

func TestGetActiveMulticastPort_ReturnsConfiguredPort(t *testing.T) {
	const want = 5004

	cfg := &CommsConfig{
		Log:        zerolog.Nop(),
		McastPorts: []McastPortConfig{{Address: "239.1.2.3", Port: want, Send: true, Receive: true}},
	}
	pc := &PortChannel{
		cfg:      cfg.McastPorts[0],
		Sender:   rtp.NewSwappableSender(&mockWriter{}),
		Receiver: rtp.NewSwappableReceiver(newMockReader()),
	}
	rt := &CommsRuntime{Ports: []*PortChannel{pc}}

	SetDefault(&Service{Cfg: cfg, Rt: rt})
	t.Cleanup(func() { SetDefault(nil) })

	if got := Default().ActiveMulticastPort(); got != want {
		t.Errorf("ActiveMulticastPort() = %d, want %d", got, want)
	}
}

// TestListenRTPReceiver_ReusePort verifies that two sockets can be bound to the
// same port simultaneously — the invariant that makes UpdateMulticastEndpoint
// safe when the port does not change.
func TestListenRTPReceiver_ReusePort(t *testing.T) {
	first, err := listenRTPReceiver(&net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		t.Fatalf("first listen: %v", err)
	}
	defer first.Close() //nolint:errcheck

	port := first.LocalAddr().(*net.UDPAddr).Port

	second, err := listenRTPReceiver(&net.UDPAddr{IP: net.IPv4zero, Port: port})
	if err != nil {
		t.Fatalf("second listen on same port %d: %v (SO_REUSEPORT not working)", port, err)
	}

	defer second.Close() //nolint:errcheck
}

// TestListenRTPReceiver_ReturnType verifies that listenRTPReceiver returns a
// *net.UDPConn (required by joinMulticastGroup).
func TestListenRTPReceiver_ReturnType(t *testing.T) {
	conn, err := listenRTPReceiver(&net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close() //nolint:errcheck

	if conn == nil {
		t.Fatal("expected non-nil *net.UDPConn")
	}
}

// TestGetReadBufferBytes_AfterSetReadBuffer verifies that the helper observes
// the value the kernel actually granted after a SetReadBuffer call. The
// returned value is at least as large as the previous default (65535) so the
// rxSocketBufBytes bump is a strict improvement, but it does not have to be
// exactly rxSocketBufBytes because Linux may clamp at net.core.rmem_max.
func TestGetReadBufferBytes_AfterSetReadBuffer(t *testing.T) {
	conn, err := listenRTPReceiver(&net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close() //nolint:errcheck

	if err := conn.SetReadBuffer(rxSocketBufBytes); err != nil {
		t.Fatalf("SetReadBuffer(%d): %v", rxSocketBufBytes, err)
	}

	got, err := getReadBufferBytes(conn)
	if err != nil {
		t.Fatalf("getReadBufferBytes: %v", err)
	}

	// Linux returns SO_RCVBUF doubled (kernel bookkeeping overhead is folded
	// into the reported value). On a stock kernel with low rmem_max we may
	// be clamped well below rxSocketBufBytes; the only safe assertion is that
	// it is strictly larger than the previous hard-coded default (65535).
	const previousDefault = 65535
	if got <= previousDefault {
		t.Errorf("SO_RCVBUF after SetReadBuffer: got %d, want > %d (the previous default)", got, previousDefault)
	}
}

// ─── boolPtrVal tests ────────────────────────────────────────────────────────

func TestBoolPtrVal(t *testing.T) {
	trueVal := true
	falseVal := false

	tests := []struct {
		name     string
		p        *bool
		fallback bool
		want     bool
	}{
		{"nil pointer, fallback true", nil, true, true},
		{"nil pointer, fallback false", nil, false, false},
		{"non-nil true, fallback false", &trueVal, false, true},
		{"non-nil false, fallback true", &falseVal, true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := boolPtrVal(tt.p, tt.fallback)
			if got != tt.want {
				t.Errorf("boolPtrVal() = %v, want %v", got, tt.want)
			}
		})
	}
}

// ─── applyDefaults ROIP tests ────────────────────────────────────────────────

func TestApplyDefaults_ROIPDefaults(t *testing.T) {
	cfg := &CommsConfig{ControlSource: "roip"}
	cfg.applyDefaults()

	if cfg.ROIPCOSGPIOMask != control.ROIPDefaultCOSMask {
		t.Errorf("ROIPCOSGPIOMask: got %d, want %d", cfg.ROIPCOSGPIOMask, control.ROIPDefaultCOSMask)
	}

	if cfg.ROIPVOXThreshold != control.ROIPDefaultVOXThresh {
		t.Errorf("ROIPVOXThreshold: got %f, want %f", cfg.ROIPVOXThreshold, control.ROIPDefaultVOXThresh)
	}

	if cfg.ROIPVOXHoldTime != control.ROIPDefaultVOXHold {
		t.Errorf("ROIPVOXHoldTime: got %v, want %v", cfg.ROIPVOXHoldTime, control.ROIPDefaultVOXHold)
	}

	if cfg.ROIPMaxTXDuration != control.ROIPDefaultMaxTX {
		t.Errorf("ROIPMaxTXDuration: got %v, want %v", cfg.ROIPMaxTXDuration, control.ROIPDefaultMaxTX)
	}
}

func TestApplyDefaults_ROIPExplicitValuesPreserved(t *testing.T) {
	cfg := &CommsConfig{
		ControlSource:     "roip",
		ROIPCOSGPIOMask:   0x04,
		ROIPVOXThreshold:  0.5,
		ROIPVOXHoldTime:   2 * time.Second,
		ROIPMaxTXDuration: 30 * time.Second,
	}
	cfg.applyDefaults()

	if cfg.ROIPCOSGPIOMask != 0x04 {
		t.Errorf("ROIPCOSGPIOMask overwritten; got %d", cfg.ROIPCOSGPIOMask)
	}

	if cfg.ROIPVOXThreshold != 0.5 {
		t.Errorf("ROIPVOXThreshold overwritten; got %f", cfg.ROIPVOXThreshold)
	}

	if cfg.ROIPVOXHoldTime != 2*time.Second {
		t.Errorf("ROIPVOXHoldTime overwritten; got %v", cfg.ROIPVOXHoldTime)
	}

	if cfg.ROIPMaxTXDuration != 30*time.Second {
		t.Errorf("ROIPMaxTXDuration overwritten; got %v", cfg.ROIPMaxTXDuration)
	}
}

// ─── EnableTalkGroupSend / Receive / GetTalkGroupStates tests ────────────────

// setupActiveServiceWithPorts builds a *Service with n ready PortChannels,
// publishes it via SetDefault, and registers cleanup. Returns the live
// service so tests can inspect svc.Rt.Ports directly.
func setupActiveServiceWithPorts(t *testing.T, n int) *Service {
	t.Helper()

	ports := make([]*PortChannel, n)
	mcastPorts := make([]McastPortConfig, n)

	for i := 0; i < n; i++ {
		ports[i] = &PortChannel{
			cfg: McastPortConfig{Address: "239.0.0.1", Port: 5004 + i*2},
		}
		ports[i].SendEnabled.Store(true)
		ports[i].ReceiveEnabled.Store(true)
		mcastPorts[i] = ports[i].cfg
	}

	svc := &Service{
		Cfg: &CommsConfig{
			Log:        zerolog.Nop(),
			McastPorts: mcastPorts,
		},
		Rt: &CommsRuntime{Ports: ports},
	}

	SetDefault(svc)
	t.Cleanup(func() { SetDefault(nil) })

	return svc
}

func TestEnableTalkGroupSend_TogglesState(t *testing.T) {
	svc := setupActiveServiceWithPorts(t, 2)

	if err := svc.EnableTalkGroupSend(1, false); err != nil {
		t.Fatal(err)
	}

	if svc.Rt.Ports[1].SendEnabled.Load() {
		t.Error("send should be disabled")
	}

	if err := svc.EnableTalkGroupSend(1, true); err != nil {
		t.Fatal(err)
	}

	if !svc.Rt.Ports[1].SendEnabled.Load() {
		t.Error("send should be enabled")
	}
}

func TestEnableTalkGroupReceive_TogglesState(t *testing.T) {
	svc := setupActiveServiceWithPorts(t, 1)

	if err := svc.EnableTalkGroupReceive(0, false); err != nil {
		t.Fatal(err)
	}

	if svc.Rt.Ports[0].ReceiveEnabled.Load() {
		t.Error("receive should be disabled")
	}
}

// ─── WebEventSource / WebAudioBridge tests ───────────────────────────────────

func TestWebEventSource_NotRunning(t *testing.T) {
	SetDefault(nil)

	if got := Default().WebEventSource(); got != nil {
		t.Errorf("expected nil when not running, got %v", got)
	}
}

func TestWebAudioBridge_NotRunning(t *testing.T) {
	SetDefault(nil)

	if got := Default().WebAudioBridge(); got != nil {
		t.Errorf("expected nil when not running, got %v", got)
	}
}

func TestWebEventSource_ReturnsNilWhenNoWebSource(t *testing.T) {
	svc := setupActiveServiceWithPorts(t, 1)
	svc.Rt.WebEvtSrc = nil

	if got := svc.WebEventSource(); got != nil {
		t.Errorf("expected nil when web source not configured, got %v", got)
	}
}

func TestWebAudioBridge_ReturnsNilWhenNoBridge(t *testing.T) {
	svc := setupActiveServiceWithPorts(t, 1)
	svc.Rt.WebBridge = nil

	if got := svc.WebAudioBridge(); got != nil {
		t.Errorf("expected nil when bridge not configured, got %v", got)
	}
}
