package comms

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/openmanet/openmanetd/internal/comms/audiopool"
	"github.com/openmanet/openmanetd/internal/comms/rtp"
)

// ─── sendToAllPorts tests ─────────────────────────────────────────────────────

// TestSendToAllPorts_SendsToEnabledPorts verifies that sendToAllPorts sends to
// every port where sendEnabled==true and rtpSess is set, and skips ports where
// sendEnabled==false.
func TestSendToAllPorts_SendsToEnabledPorts(t *testing.T) {
	enabledSess := &mockRTPSender{}
	disabledSess := &mockRTPSender{}

	pc0 := &PortChannel{cfg: McastPortConfig{Send: true, Receive: true}, RTPSess: enabledSess}
	pc0.SendEnabled.Store(true)

	pc1 := &PortChannel{cfg: McastPortConfig{Send: true, Receive: true}, RTPSess: disabledSess}
	pc1.SendEnabled.Store(false) // disabled at runtime

	rt := &CommsRuntime{Ports: []*PortChannel{pc0, pc1}}
	cfg := &CommsConfig{Log: zerolog.Nop()}

	cfg.sendToAllPorts(rt, []byte{0xAA, 0xBB})

	if len(enabledSess.Payloads) != 1 {
		t.Errorf("enabled port: got %d sends, want 1", len(enabledSess.Payloads))
	}

	if len(disabledSess.Payloads) != 0 {
		t.Errorf("disabled port: got %d sends, want 0", len(disabledSess.Payloads))
	}
}

// TestSendToAllPorts_SkipsNilSession verifies that ports with nil rtpSess are
// skipped without panicking.
func TestSendToAllPorts_SkipsNilSession(t *testing.T) {
	pc := &PortChannel{cfg: McastPortConfig{Send: true, Receive: true}, RTPSess: nil}
	pc.SendEnabled.Store(true)

	rt := &CommsRuntime{Ports: []*PortChannel{pc}}
	cfg := &CommsConfig{Log: zerolog.Nop()}

	// Must not panic.
	cfg.sendToAllPorts(rt, []byte{0x01})
}

// TestSendToAllPorts_MultiplePortsAllEnabled verifies payload is delivered to
// all send-enabled ports in a multi-port configuration.
func TestSendToAllPorts_MultiplePortsAllEnabled(t *testing.T) {
	sessions := [3]*mockRTPSender{}
	ports := make([]*PortChannel, 3)

	for i := range sessions {
		sessions[i] = &mockRTPSender{}
		ports[i] = &PortChannel{cfg: McastPortConfig{Send: true}, RTPSess: sessions[i]}
		ports[i].SendEnabled.Store(true)
	}

	rt := &CommsRuntime{Ports: ports}
	cfg := &CommsConfig{Log: zerolog.Nop()}

	cfg.sendToAllPorts(rt, []byte{0xDE, 0xAD})

	for i, sess := range sessions {
		if len(sess.Payloads) != 1 {
			t.Errorf("port %d: got %d sends, want 1", i, len(sess.Payloads))
		}
	}
}

// ─── EnableTalkGroupSend / EnableTalkGroupReceive / GetTalkGroupStates tests ────

func setupRuntimeConfig(t *testing.T) (*CommsConfig, *CommsRuntime) {
	t.Helper()

	pc0 := &PortChannel{cfg: McastPortConfig{Address: "239.0.0.1", Port: 5004, Send: true, Receive: true}}
	pc0.SendEnabled.Store(true)
	pc0.ReceiveEnabled.Store(true)

	pc1 := &PortChannel{cfg: McastPortConfig{Address: "239.0.0.2", Port: 5006, Send: true, Receive: false}}
	pc1.SendEnabled.Store(true)
	pc1.ReceiveEnabled.Store(false)

	rt := &CommsRuntime{Ports: []*PortChannel{pc0, pc1}}
	cfg := &CommsConfig{
		Log:        zerolog.Nop(),
		McastPorts: []McastPortConfig{pc0.cfg, pc1.cfg},
	}

	SetDefault(&Service{Cfg: cfg, Rt: rt})
	t.Cleanup(func() { SetDefault(nil) })

	return cfg, rt
}

func TestEnableTalkGroupSend_TogglesEnabled(t *testing.T) {
	_, rt := setupRuntimeConfig(t)
	svc := Default()

	if err := svc.EnableTalkGroupSend(0, false); err != nil {
		t.Fatalf("EnableTalkGroupSend(0, false): %v", err)
	}

	if rt.Ports[0].SendEnabled.Load() {
		t.Error("expected port 0 sendEnabled=false after EnableTalkGroupSend(0, false)")
	}

	if err := svc.EnableTalkGroupSend(0, true); err != nil {
		t.Fatalf("EnableTalkGroupSend(0, true): %v", err)
	}

	if !rt.Ports[0].SendEnabled.Load() {
		t.Error("expected port 0 sendEnabled=true after EnableTalkGroupSend(0, true)")
	}
}

func TestEnableTalkGroupSend_OutOfRange(t *testing.T) {
	setupRuntimeConfig(t)

	svc := Default()

	if err := svc.EnableTalkGroupSend(99, true); err == nil {
		t.Error("expected error for out-of-range port index")
	}

	if err := svc.EnableTalkGroupSend(-1, true); err == nil {
		t.Error("expected error for negative port index")
	}
}

func TestEnableTalkGroupSend_NotRunning(t *testing.T) {
	SetDefault(nil)

	if err := Default().EnableTalkGroupSend(0, true); err == nil {
		t.Error("expected error when comms is not running")
	}
}

func TestEnableTalkGroupReceive_TogglesEnabled(t *testing.T) {
	_, rt := setupRuntimeConfig(t)
	svc := Default()

	if err := svc.EnableTalkGroupReceive(0, false); err != nil {
		t.Fatalf("EnableTalkGroupReceive(0, false): %v", err)
	}

	if rt.Ports[0].ReceiveEnabled.Load() {
		t.Error("expected port 0 receiveEnabled=false")
	}

	if err := svc.EnableTalkGroupReceive(0, true); err != nil {
		t.Fatalf("EnableTalkGroupReceive(0, true): %v", err)
	}

	if !rt.Ports[0].ReceiveEnabled.Load() {
		t.Error("expected port 0 receiveEnabled=true")
	}
}

func TestEnableTalkGroupReceive_OutOfRange(t *testing.T) {
	setupRuntimeConfig(t)

	if err := Default().EnableTalkGroupReceive(5, false); err == nil {
		t.Error("expected error for out-of-range port index")
	}
}

func TestEnableTalkGroupReceive_NotRunning(t *testing.T) {
	SetDefault(nil)

	if err := Default().EnableTalkGroupReceive(0, false); err == nil {
		t.Error("expected error when comms is not running")
	}
}

func TestGetTalkGroupStates_ReturnsSnapshot(t *testing.T) {
	_, rt := setupRuntimeConfig(t)

	states, err := Default().TalkGroupStates()
	if err != nil {
		t.Fatalf("TalkGroupStates: %v", err)
	}

	if len(states) != len(rt.Ports) {
		t.Fatalf("got %d states, want %d", len(states), len(rt.Ports))
	}

	if states[0].Address != "239.0.0.1" || states[0].Port != 5004 {
		t.Errorf("port 0: got %s:%d, want 239.0.0.1:5004", states[0].Address, states[0].Port)
	}

	if !states[0].SendEnabled || !states[0].ReceiveEnabled {
		t.Errorf("port 0: want SendEnabled=true ReceiveEnabled=true; got %v %v",
			states[0].SendEnabled, states[0].ReceiveEnabled)
	}

	if states[1].Address != "239.0.0.2" || states[1].Port != 5006 {
		t.Errorf("port 1: got %s:%d, want 239.0.0.2:5006", states[1].Address, states[1].Port)
	}

	if !states[1].SendEnabled || states[1].ReceiveEnabled {
		t.Errorf("port 1: want SendEnabled=true ReceiveEnabled=false; got %v %v",
			states[1].SendEnabled, states[1].ReceiveEnabled)
	}
}

func TestGetTalkGroupStates_ReflectsRuntimeChanges(t *testing.T) {
	setupRuntimeConfig(t)

	svc := Default()

	if err := svc.EnableTalkGroupSend(1, false); err != nil {
		t.Fatal(err)
	}

	states, err := svc.TalkGroupStates()
	if err != nil {
		t.Fatal(err)
	}

	if states[1].SendEnabled {
		t.Error("expected port 1 SendEnabled=false after SetPortSend(1, false)")
	}
}

func TestGetTalkGroupStates_NotRunning(t *testing.T) {
	SetDefault(nil)

	if _, err := Default().TalkGroupStates(); err == nil {
		t.Error("expected error when comms is not running")
	}
}

// ─── isReceivingRemote multi-port tests ───────────────────────────────────────

// TestIsReceivingRemote_SendDisabledPortNotChecked verifies that a port with
// sendEnabled=false does not contribute to the half-duplex block even when it
// has recently received audio.
func TestIsReceivingRemote_SendDisabledPortNotChecked(t *testing.T) {
	cfg := &CommsConfig{Log: zerolog.Nop()}

	// Port with sendEnabled=false has recent rx – should NOT block transmission.
	pc := &PortChannel{cfg: McastPortConfig{Send: true, Receive: true}}
	pc.SendEnabled.Store(false)

	rt := &CommsRuntime{Ports: []*PortChannel{pc}}
	pc.MarkRemoteRx(rt)

	if cfg.isReceivingRemote(rt) {
		t.Error("sendEnabled=false port should not trigger half-duplex block")
	}
}

// TestIsReceivingRemote_MultiPortFirstEnabled verifies that a recent rx on any
// send-enabled port returns true.
func TestIsReceivingRemote_MultiPortFirstEnabled(t *testing.T) {
	cfg := &CommsConfig{Log: zerolog.Nop()}

	pc0 := &PortChannel{cfg: McastPortConfig{Send: true, Receive: true}}
	pc0.SendEnabled.Store(true)

	pc1 := &PortChannel{cfg: McastPortConfig{Send: true, Receive: false}}
	pc1.SendEnabled.Store(true)
	// pc1 has never received

	rt := &CommsRuntime{Ports: []*PortChannel{pc0, pc1}}
	pc0.MarkRemoteRx(rt)

	if !cfg.isReceivingRemote(rt) {
		t.Error("expected true when first port has recent rx and sendEnabled=true")
	}
}

// ─── receiveEnabled runtime toggle tests ─────────────────────────────────────

// TestReceiveLoop_SkipsDeliveryWhenReceiveDisabled verifies that when
// pc.ReceiveEnabled is false, incoming RTP packets are read but not queued
// to the jitter buffer (the playback buffer stays empty).
func TestReceiveLoop_SkipsDeliveryWhenReceiveDisabled(t *testing.T) {
	cfg := &CommsConfig{Log: zerolog.Nop(), Loopback: true}

	// Pre-load one valid RTP packet.
	raw := makeRTPBytes(t, 0)
	src := netip.MustParseAddrPort("1.2.3.4:5004")
	reader := newMockReader(
		mockPacket{data: raw, src: src},
	)

	pc := &PortChannel{
		cfg:      McastPortConfig{Send: false, Receive: true},
		Receiver: rtp.NewSwappableReceiver(reader),
	}
	pc.SendEnabled.Store(false)
	pc.ReceiveEnabled.Store(false) // ← disabled
	pc.PlaybackBuffer = make(chan []int16, 8)

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

	// Wait for the packet to be consumed by the read loop.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if reader.remaining() == 0 {
			break
		}

		time.Sleep(5 * time.Millisecond)
	}

	time.Sleep(60 * time.Millisecond) // allow one playout tick interval

	cancel()
	pc.Receiver.Close()

	<-done

	if len(pc.PlaybackBuffer) != 0 {
		t.Errorf("receive disabled: got %d frames queued, want 0", len(pc.PlaybackBuffer))
	}
}

// ─── per-port drainPlaybackBuffer tests ──────────────────────────────────────

func TestDrainPlaybackBuffer_MultiPort(t *testing.T) {
	pc0 := &PortChannel{}

	pc0.PlaybackBuffer = make(chan []int16, 4)
	pc0.PlaybackBuffer <- []int16{1}

	pc0.PlaybackBuffer <- []int16{2}

	pc1 := &PortChannel{}

	pc1.PlaybackBuffer = make(chan []int16, 4)
	pc1.PlaybackBuffer <- []int16{3}

	rt := &CommsRuntime{Ports: []*PortChannel{pc0, pc1}}
	cfg := &CommsConfig{Log: zerolog.Nop()}
	cfg.drainPlaybackBuffer(rt)

	if len(pc0.PlaybackBuffer) != 0 {
		t.Errorf("port 0: expected empty buffer; got %d items", len(pc0.PlaybackBuffer))
	}

	if len(pc1.PlaybackBuffer) != 0 {
		t.Errorf("port 1: expected empty buffer; got %d items", len(pc1.PlaybackBuffer))
	}
}

// TestBeginTransmission_BeepSentToOnePort verifies the single-beep
// contract: beginTransmission queues the start-beep to exactly one port —
// the pre-P4 fan-out played N overlapping copies of the tone through dmix
// into the same physical output device.
func TestBeginTransmission_BeepSentToOnePort(t *testing.T) {
	pc0 := &PortChannel{cfg: McastPortConfig{Send: true, Receive: true}}
	pc0.SendEnabled.Store(true)
	pc0.ReceiveEnabled.Store(true)
	pc0.PlaybackBuffer = make(chan []int16, 4)

	pc1 := &PortChannel{cfg: McastPortConfig{Send: true, Receive: true}}
	pc1.SendEnabled.Store(true)
	pc1.ReceiveEnabled.Store(true)
	pc1.PlaybackBuffer = make(chan []int16, 4)

	rt := &CommsRuntime{
		Ports:           []*PortChannel{pc0, pc1},
		BeepBufferStart: []int16{100, 200},
		BeepBufferStop:  []int16{300, 400},
	}
	rt.SetBroadcast(&mockStream{})

	cfg := &CommsConfig{Log: zerolog.Nop()}
	cfg.beginTransmission(rt)

	if len(pc0.PlaybackBuffer) != 1 {
		t.Errorf("port 0: beeps queued = %d, want 1", len(pc0.PlaybackBuffer))
	}

	if len(pc1.PlaybackBuffer) != 0 {
		t.Errorf("port 1: beeps queued = %d, want 0 (single-beep contract)", len(pc1.PlaybackBuffer))
	}
}

// ─── sendToAllPorts error handling ───────────────────────────────────────────

// TestSendToAllPorts_SendErrorDoesNotPanic verifies that a send error on a
// port's RTP session is logged and skipped without panicking.
func TestSendToAllPorts_SendErrorDoesNotPanic(t *testing.T) {
	sess := &mockRTPSender{sendErr: errors.New("network unreachable")}

	pc := &PortChannel{cfg: McastPortConfig{Send: true, Receive: true}, RTPSess: sess}
	pc.SendEnabled.Store(true)

	rt := &CommsRuntime{Ports: []*PortChannel{pc}}
	cfg := &CommsConfig{Log: zerolog.Nop()}

	// Must not panic.
	cfg.sendToAllPorts(rt, []byte{0xAA, 0xBB})
}

// ─── playoutOneFrame receive-only port tests ──────────────────────────────────

// TestPlayoutOneFrame_ReceiveOnlyPortNotSuppressedDuringBroadcast verifies
// that half-duplex suppression in playoutOneFrame only applies to
// send-capable ports. A receive-only port (sendEnabled=false) must continue
// emitting decoded audio even while rt.Broadcasting==true.
func TestPlayoutOneFrame_ReceiveOnlyPortNotSuppressedDuringBroadcast(t *testing.T) {
	// Receive-only port: sendEnabled=false.
	pc := &PortChannel{
		cfg: McastPortConfig{Send: false, Receive: true},
	}
	pc.SendEnabled.Store(false)
	pc.ReceiveEnabled.Store(true)

	pc.Decoder = &mockDecoder{fillValue: 42, returnN: audiopool.FrameSize}
	rt := &CommsRuntime{
		Ports: []*PortChannel{pc},
	}
	rt.Broadcasting.Store(true) // simulate active broadcast on another port

	jb := rtp.NewJitterBuffer(1, 16)
	jb.Push(0, []byte{0xAA, 0xBB}) // prebuffer=1: immediately ready

	cfg := &CommsConfig{Log: zerolog.Nop()}

	out := make([]int16, audiopool.FrameSize)
	cfg.playoutOneFrame(pc, rt, jb, out)

	// The decoder fills with fillValue/32768; receive-only ports bypass
	// the half-duplex check so we should see non-zero samples even though
	// rt.Broadcasting is true.
	allZero := true

	for _, v := range out {
		if v != 0 {
			allZero = false

			break
		}
	}

	if allZero {
		t.Error("receive-only port should emit decoded samples during broadcast; got silence")
	}
}
