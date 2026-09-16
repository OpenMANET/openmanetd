package rtp

import (
	"testing"

	pionrtp "github.com/pion/rtp"
	"github.com/rs/zerolog"
)

func TestSSRCFromID_Deterministic(t *testing.T) {
	a := SSRCFromID("node-alpha")

	b := SSRCFromID("node-alpha")
	if a != b {
		t.Errorf("SSRCFromID not deterministic; got %d and %d", a, b)
	}
}

func TestSSRCFromID_Different(t *testing.T) {
	a := SSRCFromID("node-alpha")

	b := SSRCFromID("node-beta")
	if a == b {
		t.Error("different IDs should produce different SSRCs")
	}
}

func TestParseIncomingRTP_Valid(t *testing.T) {
	orig := &pionrtp.Packet{
		Header:  pionrtp.Header{Version: 2, PayloadType: PayloadTypeOpus, SequenceNumber: 42, Timestamp: 1000, SSRC: 0xDEADBEEF},
		Payload: []byte{1, 2, 3, 4},
	}

	raw, err := orig.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	parsed, err := ParseIncoming(raw)
	if err != nil {
		t.Fatal(err)
	}

	if parsed.SequenceNumber != 42 {
		t.Errorf("seq: got %d, want 42", parsed.SequenceNumber)
	}

	if parsed.SSRC != 0xDEADBEEF {
		t.Errorf("ssrc: got 0x%X", parsed.SSRC)
	}
}

func TestParseIncomingInto_Valid(t *testing.T) {
	orig := &pionrtp.Packet{
		Header:  pionrtp.Header{Version: 2, PayloadType: PayloadTypeOpus, SequenceNumber: 42, Timestamp: 1000, SSRC: 0xDEADBEEF},
		Payload: []byte{1, 2, 3, 4},
	}

	raw, err := orig.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	var pkt pionrtp.Packet
	if err := ParseIncomingInto(raw, &pkt); err != nil {
		t.Fatal(err)
	}

	if pkt.SequenceNumber != 42 {
		t.Errorf("seq: got %d, want 42", pkt.SequenceNumber)
	}

	if pkt.SSRC != 0xDEADBEEF {
		t.Errorf("ssrc: got 0x%X", pkt.SSRC)
	}

	if string(pkt.Payload) != string(orig.Payload) {
		t.Errorf("payload: got %v, want %v", pkt.Payload, orig.Payload)
	}
}

// TestParseIncomingInto_Reuse exercises the receiveLoop pattern: one Packet
// struct reused across parses. The second parse must fully overwrite the
// first — no stale header fields or payload bytes may survive.
func TestParseIncomingInto_Reuse(t *testing.T) {
	first := &pionrtp.Packet{
		Header:  pionrtp.Header{Version: 2, PayloadType: PayloadTypeOpus, SequenceNumber: 1, Timestamp: 100, SSRC: 0xAAAA},
		Payload: []byte{9, 9, 9, 9, 9, 9},
	}

	second := &pionrtp.Packet{
		Header:  pionrtp.Header{Version: 2, PayloadType: PayloadTypeOpus, SequenceNumber: 2, Timestamp: 200, SSRC: 0xBBBB},
		Payload: []byte{7},
	}

	rawFirst, err := first.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	rawSecond, err := second.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	var pkt pionrtp.Packet
	if err := ParseIncomingInto(rawFirst, &pkt); err != nil {
		t.Fatal(err)
	}

	if err := ParseIncomingInto(rawSecond, &pkt); err != nil {
		t.Fatal(err)
	}

	if pkt.SequenceNumber != 2 || pkt.Timestamp != 200 || pkt.SSRC != 0xBBBB {
		t.Errorf("stale header after reuse: seq=%d ts=%d ssrc=0x%X", pkt.SequenceNumber, pkt.Timestamp, pkt.SSRC)
	}

	if len(pkt.Payload) != 1 || pkt.Payload[0] != 7 {
		t.Errorf("stale payload after reuse: %v", pkt.Payload)
	}
}

func TestParseIncomingInto_Invalid(t *testing.T) {
	var pkt pionrtp.Packet
	if err := ParseIncomingInto([]byte{0xFF, 0x00, 0x01}, &pkt); err == nil {
		t.Error("expected error for invalid bytes")
	}
}

func TestParseIncomingRTP_Invalid(t *testing.T) {
	_, err := ParseIncoming([]byte{0xFF, 0x00, 0x01})
	if err == nil {
		t.Error("expected error for invalid bytes")
	}
}

func TestParseIncomingRTP_Nil(t *testing.T) {
	_, err := ParseIncoming(nil)
	if err == nil {
		t.Error("expected error for nil input")
	}
}

func TestPionRTPSession_Send(t *testing.T) {
	rtpW := &mockWriter{}
	rtcpW := &mockWriter{}

	sess, err := NewSession(0xABCDEF, rtpW, rtcpW, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close() //nolint:errcheck

	if err := sess.Send([]byte{1, 2, 3}); err != nil {
		t.Fatal(err)
	}

	if len(rtpW.Packets) == 0 {
		t.Fatal("expected at least one packet")
	}

	var pkt pionrtp.Packet
	if err := pkt.Unmarshal(rtpW.Packets[0]); err != nil {
		t.Fatalf("invalid RTP: %v", err)
	}

	if pkt.PayloadType != PayloadTypeOpus {
		t.Errorf("PT: got %d", pkt.PayloadType)
	}

	if pkt.SSRC != 0xABCDEF {
		t.Errorf("SSRC: got 0x%X", pkt.SSRC)
	}
}

func TestPionRTPSession_SequenceIncrement(t *testing.T) {
	rtpW := &mockWriter{}

	sess, err := NewSession(1, rtpW, &mockWriter{}, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close() //nolint:errcheck

	for range 3 {
		if err := sess.Send([]byte{1}); err != nil {
			t.Fatal(err)
		}
	}

	if len(rtpW.Packets) != 3 {
		t.Fatalf("expected 3 packets; got %d", len(rtpW.Packets))
	}

	var seqs [3]uint16

	for i, raw := range rtpW.Packets {
		var pkt pionrtp.Packet
		if err := pkt.Unmarshal(raw); err != nil {
			t.Fatalf("packet %d invalid: %v", i, err)
		}

		seqs[i] = pkt.SequenceNumber
	}

	if seqs[1]-seqs[0] != 1 {
		t.Errorf("seq[1]-seq[0]=%d want 1", seqs[1]-seqs[0])
	}

	if seqs[2]-seqs[1] != 1 {
		t.Errorf("seq[2]-seq[1]=%d want 1", seqs[2]-seqs[1])
	}
}

// TestPionRTPSession_WireFormat pins the full on-wire header contract of
// Send so the packetization internals can change without breaking
// interoperability with deployed nodes: RTP version 2, marker bit set (one
// Opus frame per packet), timestamp advancing by exactly FrameSamples per
// frame, and the payload delivered byte-for-byte.
func TestPionRTPSession_WireFormat(t *testing.T) {
	rtpW := &mockWriter{}

	sess, err := NewSession(0x1234, rtpW, &mockWriter{}, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close() //nolint:errcheck

	payloads := [][]byte{{0xA1, 0xA2, 0xA3}, {0xB1}, {0xC1, 0xC2}}
	for _, p := range payloads {
		if err := sess.Send(p); err != nil {
			t.Fatal(err)
		}
	}

	if len(rtpW.Packets) != len(payloads) {
		t.Fatalf("expected %d packets; got %d", len(payloads), len(rtpW.Packets))
	}

	var prev pionrtp.Packet

	for i, raw := range rtpW.Packets {
		var pkt pionrtp.Packet
		if err := pkt.Unmarshal(raw); err != nil {
			t.Fatalf("packet %d invalid: %v", i, err)
		}

		if pkt.Version != 2 {
			t.Errorf("packet %d: version got %d, want 2", i, pkt.Version)
		}

		if !pkt.Marker {
			t.Errorf("packet %d: marker bit not set", i)
		}

		if pkt.Padding || pkt.Extension {
			t.Errorf("packet %d: unexpected padding/extension flags", i)
		}

		if string(pkt.Payload) != string(payloads[i]) {
			t.Errorf("packet %d: payload got %v, want %v", i, pkt.Payload, payloads[i])
		}

		if i > 0 {
			if got := pkt.Timestamp - prev.Timestamp; got != FrameSamples {
				t.Errorf("packet %d: timestamp stride got %d, want %d", i, got, FrameSamples)
			}

			if got := pkt.SequenceNumber - prev.SequenceNumber; got != 1 {
				t.Errorf("packet %d: seq stride got %d, want 1", i, got)
			}
		}

		prev = pkt
	}
}

// TestPionRTPSession_EmptyPayload pins the empty-frame contract: nothing is
// written to the wire, but the timestamp still advances by FrameSamples so a
// receiver sees the gap in media time (RFC 3550 semantics preserved from
// pion's Packetize/SkipSamples behavior).
func TestPionRTPSession_EmptyPayload(t *testing.T) {
	rtpW := &mockWriter{}

	sess, err := NewSession(1, rtpW, &mockWriter{}, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close() //nolint:errcheck

	if err := sess.Send([]byte{1}); err != nil {
		t.Fatal(err)
	}

	if err := sess.Send(nil); err != nil {
		t.Fatal(err)
	}

	if err := sess.Send([]byte{2}); err != nil {
		t.Fatal(err)
	}

	if len(rtpW.Packets) != 2 {
		t.Fatalf("expected 2 packets (empty frame skipped); got %d", len(rtpW.Packets))
	}

	var first, second pionrtp.Packet
	if err := first.Unmarshal(rtpW.Packets[0]); err != nil {
		t.Fatal(err)
	}

	if err := second.Unmarshal(rtpW.Packets[1]); err != nil {
		t.Fatal(err)
	}

	if got := second.Timestamp - first.Timestamp; got != 2*FrameSamples {
		t.Errorf("timestamp stride across skipped frame: got %d, want %d", got, 2*FrameSamples)
	}
}

func TestPionRTPSession_Close(t *testing.T) {
	sess, err := NewSession(1, &mockWriter{}, &mockWriter{}, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}

	if err := sess.Close(); err != nil {
		t.Errorf("close error: %v", err)
	}
}
