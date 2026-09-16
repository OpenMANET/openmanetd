//go:build integration

package comms

import (
	"context"
	"net/netip"
	"testing"
	"time"

	pionrtp "github.com/pion/rtp"
	"github.com/rs/zerolog"

	"github.com/openmanet/openmanetd/internal/comms/audiopool"
	"github.com/openmanet/openmanetd/internal/comms/rtp"
)

// These tests exercise the receive path end-to-end:
//
//   mockReader → receiveLoop → rtp.ParseIncoming → pc.Jitter →
//     playoutOneFrame → mockDecoder → caller-supplied PCM buffer
//
// They cannot exercise the TX path end-to-end because the Opus encode lives
// inside the malgo capture callback closure (comms.go:684) which cannot
// be driven without real audio hardware. Instead the tests synthesize RTP
// packets with pion/rtp — the same library production code uses to parse
// them — and inject them into the receiver. This covers the bug surface
// (jitter buffer SSRC handling and sequence-number arithmetic).
//
// The decoder is faked: a real Opus decoder requires real Opus payloads, and
// generating those would mean importing the encoder just to test the
// receive-path plumbing. The integration value here is in the loop wiring,
// jitter buffer behavior, and reset semantics — not in the decoder itself,
// which is well-covered by codec_test.go.
//
// In production the malgo playback callback drives playoutOneFrame at the
// audio hardware clock rate. These tests drive playoutOneFrame manually
// against a synthetic float32 output buffer, which is the same primitive the
// production callback calls.

// buildRTPPacket marshals an RTP packet with the given SSRC, seq, and payload.
// It uses the production pion/rtp library directly so the bytes are bit-for-bit
// what production parsing code (rtp.ParseIncoming) expects.
func buildRTPPacket(t *testing.T, ssrc uint32, seq uint16, payload []byte) []byte {
	t.Helper()

	pkt := &pionrtp.Packet{
		Header: pionrtp.Header{
			Version:        2,
			PayloadType:    111,
			SequenceNumber: seq,
			Timestamp:      uint32(seq) * rtp.FrameSamples,
			SSRC:           ssrc,
		},
		Payload: payload,
	}

	raw, err := pkt.Marshal()
	if err != nil {
		t.Fatalf("rtp.Marshal: %v", err)
	}

	return raw
}

// newIntegrationReceiver builds a minimal PortChannel + CommsRuntime backed
// by a mockReader so a test can push RTP datagrams through the real
// receiveLoop and observe decoded frames via playoutOneFrame.
func newIntegrationReceiver(t *testing.T) (*CommsConfig, *PortChannel, *CommsRuntime, *mockReader) {
	t.Helper()

	reader := newMockReader()

	pc := &PortChannel{
		cfg:      McastPortConfig{Send: true, Receive: true},
		Receiver: rtp.NewSwappableReceiver(reader),
		Jitter:   rtp.NewJitterBuffer(rtp.PrebufferPackets, rtp.MaxDepth),
	}
	pc.SendEnabled.Store(true)
	pc.ReceiveEnabled.Store(true)
	pc.Decoder = &mockDecoder{fillValue: 1234, returnN: int(rtp.FrameSamples)}

	rt := &CommsRuntime{
		Ports: []*PortChannel{pc},
	}

	cfg := &CommsConfig{Log: zerolog.Nop(), Loopback: true}

	return cfg, pc, rt, reader
}

// pollDecodedFrame repeatedly calls playoutOneFrame against a fresh PCM
// buffer until non-zero samples appear or the deadline is reached. It mirrors
// what the malgo playback callback does in production at the 20 ms
// hardware tick rate.
func pollDecodedFrame(cfg *CommsConfig, pc *PortChannel, rt *CommsRuntime, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	out := make([]int16, audiopool.FrameSize)

	for time.Now().Before(deadline) {
		for i := range out {
			out[i] = 0
		}

		cfg.playoutOneFrame(pc, rt, pc.Jitter, out)

		for _, v := range out {
			if v != 0 {
				return true
			}
		}

		time.Sleep(20 * time.Millisecond)
	}

	return false
}

// drainConcealmentFrames calls playoutOneFrame in a loop until enough time
// has elapsed for the conceal window (~100 ms) plus a margin to expire, so
// the next pollDecodedFrame call can distinguish "fresh decoded audio" from
// "leftover concealment". consecutivePLC is also reset.
func drainConcealmentFrames(cfg *CommsConfig, pc *PortChannel, rt *CommsRuntime) {
	deadline := time.Now().Add(500 * time.Millisecond)
	out := make([]int16, audiopool.FrameSize)

	for time.Now().Before(deadline) {
		cfg.playoutOneFrame(pc, rt, pc.Jitter, out)
		time.Sleep(20 * time.Millisecond)
	}

	pc.ConsecutivePLC = 0
}

// pushPackets enqueues raw datagrams onto the mockReader so the next
// ReadFromUDP calls return them. Safe to call concurrently with receiveLoop.
func pushPackets(reader *mockReader, raws ...[]byte) {
	src := netip.MustParseAddrPort("1.2.3.4:5004")

	reader.mu.Lock()
	defer reader.mu.Unlock()

	for _, raw := range raws {
		reader.packets = append(reader.packets, mockPacket{data: raw, src: src})
	}
}

// TestIntegration_RTPReceivePath_BasicFlow drives a small burst of RTP
// packets from a single SSRC through the receive path and asserts that
// decoded PCM frames are produced by playoutOneFrame.
func TestIntegration_RTPReceivePath_BasicFlow(t *testing.T) {
	cfg, pc, rt, reader := newIntegrationReceiver(t)

	const ssrc = uint32(0xAAAAAAAA)

	var raws [][]byte

	for i := 0; i < rtp.PrebufferPackets+3; i++ {
		raws = append(raws, buildRTPPacket(t, ssrc, uint16(i), []byte{0xAA, 0xBB, byte(i)}))
	}

	pushPackets(reader, raws...)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan struct{})

	go func() {
		defer close(done)
		cfg.receiveLoop(ctx, pc, rt)
	}()

	if !pollDecodedFrame(cfg, pc, rt, 500*time.Millisecond) {
		t.Fatal("expected a decoded frame from the basic-flow burst")
	}

	cancel()
	pc.Receiver.Close()
	<-done
}

// TestIntegration_RTPReceivePath_SSRCChangeRecovery is the regression test
// for the silent-stall bug. Talker A streams a few packets, drains, then
// Talker B begins with an SSRC change AND a starting sequence number that
// is "less than" Talker A's last expected per signed-int16 wrap math. On a
// SSRC-blind buffer Talker B's packets would be silently rejected forever.
// With SSRC tracking the buffer must reset and deliver Talker B's frames.
func TestIntegration_RTPReceivePath_SSRCChangeRecovery(t *testing.T) {
	cfg, pc, rt, reader := newIntegrationReceiver(t)

	const (
		ssrcA = uint32(0x11111111)
		ssrcB = uint32(0x22222222)
	)

	// Talker A: seqs 0..4 → expected = 5 after drain.
	var rawsA [][]byte

	for i := uint16(0); i <= 4; i++ {
		rawsA = append(rawsA, buildRTPPacket(t, ssrcA, i, []byte{0xA, byte(i)}))
	}

	pushPackets(reader, rawsA...)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	done := make(chan struct{})

	go func() {
		defer close(done)
		cfg.receiveLoop(ctx, pc, rt)
	}()

	// Wait for talker A's first frame to confirm the path is alive.
	if !pollDecodedFrame(cfg, pc, rt, 500*time.Millisecond) {
		t.Fatal("expected at least one frame from talker A")
	}

	// Drain whatever's left from talker A AND let the conceal window
	// (~100 ms after the last push) expire so the next pollDecodedFrame
	// call can distinguish a real talker-B frame from background PLC.
	drainConcealmentFrames(cfg, pc, rt)

	// Talker B: starting seq 0x8005. With talker A's expected = 5, we have
	// int16(0x8005 - 5) = int16(0x8000) = -32768, so seqLess(0x8005, 5) is
	// true. On a SSRC-blind buffer every Talker B packet is "stale" by
	// signed-int16 wrap math and gets silently dropped — the receive path
	// stalls forever despite tcpdump showing packets.
	var rawsB [][]byte

	for i := uint16(0); i < rtp.PrebufferPackets+3; i++ {
		rawsB = append(rawsB, buildRTPPacket(t, ssrcB, 0x8005+i, []byte{0xB, byte(i)}))
	}

	pushPackets(reader, rawsB...)

	if !pollDecodedFrame(cfg, pc, rt, 1*time.Second) {
		t.Fatal("BUG: receive stalled after SSRC change — talker B frames never reached playback")
	}

	cancel()
	pc.Receiver.Close()
	<-done
}

// TestIntegration_RTPReceivePath_SameSSRCStaleStillDropped is a regression
// guard: the SSRC-tracking fix must NOT undo the existing reorder protection
// for stale packets that share the same SSRC.
func TestIntegration_RTPReceivePath_SameSSRCStaleStillDropped(t *testing.T) {
	cfg, pc, rt, reader := newIntegrationReceiver(t)

	const ssrc = uint32(0x33333333)

	// Push a burst, drain.
	var raws [][]byte

	for i := 0; i < rtp.PrebufferPackets+2; i++ {
		raws = append(raws, buildRTPPacket(t, ssrc, uint16(100+i), []byte{0xC, byte(i)}))
	}

	pushPackets(reader, raws...)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan struct{})

	go func() {
		defer close(done)
		cfg.receiveLoop(ctx, pc, rt)
	}()

	// Confirm at least one frame from the burst.
	if !pollDecodedFrame(cfg, pc, rt, 500*time.Millisecond) {
		t.Fatal("expected a decoded frame from the same-SSRC burst")
	}

	// Push a stale packet (same SSRC, old seq) and verify SSRC tracking
	// did not reset. The stale packet itself is silently dropped at the
	// seqLess gate inside pushLocked, which is the existing reorder
	// protection. We assert no SSRC reset was counted.
	resetsBefore := pc.Jitter.SSRCResets.Load()
	pushPackets(reader, buildRTPPacket(t, ssrc, 50, []byte{0xDE}))

	// Give the receive loop a moment to process the stale packet.
	time.Sleep(100 * time.Millisecond)

	cancel()
	pc.Receiver.Close()
	<-done

	if got := pc.Jitter.SSRCResets.Load(); got != resetsBefore {
		t.Errorf("stale same-SSRC packet should not trigger an SSRC reset; got resets=%d (was %d)",
			got, resetsBefore)
	}
}
