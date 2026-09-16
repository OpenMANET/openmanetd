package comms

import (
	"math"
	"net"
	"testing"
	"time"

	pionrtp "github.com/pion/rtp"
	"github.com/rs/zerolog"

	"github.com/openmanet/openmanetd/internal/comms/audiopool"
	"github.com/openmanet/openmanetd/internal/comms/codec"
	"github.com/openmanet/openmanetd/internal/comms/rtp"
)

// ─── RTP hot-path benchmarks ─────────────────────────────────────────────────

// BenchmarkRTPSessionSend measures the per-frame cost of the pion-backed RTP
// send path: Packetize → interceptor chain → baseRTPWriter → MarshalTo into
// pooled buffer → PacketWriter.Write. The mockWriter is a no-op sink so only
// the framing/marshal/pool path is on the critical path.
// discardWriter is an allocation-free PacketWriter sink used in benchmarks.
type discardWriter struct{}

func (discardWriter) Write(b []byte) (int, error) { return len(b), nil }

func BenchmarkRTPSessionSend(b *testing.B) {
	sess, err := rtp.NewSession(0xDEADBEEF, discardWriter{}, discardWriter{}, zerolog.Nop())
	if err != nil {
		b.Fatal(err)
	}

	defer func() { _ = sess.Close() }()

	payload := make([]byte, 160) // typical Opus 20ms frame

	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		if err := sess.Send(payload); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSwappableSenderWrite measures the lock-free Write path on
// SwappableSender. Expected: zero allocs, no mutex contention.
func BenchmarkSwappableSenderWrite(b *testing.B) {
	s := rtp.NewSwappableSender(discardWriter{})
	buf := make([]byte, 200)

	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		if _, err := s.Write(buf); err != nil {
			b.Fatal(err)
		}
	}
}

// ─── Codec benchmarks ────────────────────────────────────────────────────────

func BenchmarkEncodeOpus(b *testing.B) {
	enc, err := codec.NewOpusEncoder(audiopool.SampleRate, audiopool.Channels, targetBitrate, encoderComplexity, packetLossPerc)
	if err != nil {
		b.Fatal(err)
	}

	pcm := make([]int16, audiopool.FrameSize)
	for i := range pcm {
		pcm[i] = int16(math.Sin(2*math.Pi*440*float64(i)/float64(audiopool.SampleRate)) * 16000)
	}

	buf := make([]byte, 1500)

	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		if _, err := enc.Encode(pcm, buf); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkDecodeOpus measures the Encode→Decode (int16) round-trip used by
// the send path.
func BenchmarkDecodeOpus(b *testing.B) {
	enc, err := codec.NewOpusEncoder(audiopool.SampleRate, audiopool.Channels, targetBitrate, encoderComplexity, packetLossPerc)
	if err != nil {
		b.Fatal(err)
	}

	dec, err := codec.NewOpusDecoder(audiopool.SampleRate, audiopool.Channels)
	if err != nil {
		b.Fatal(err)
	}

	pcm := make([]int16, audiopool.FrameSize)
	for i := range pcm {
		pcm[i] = int16(math.Sin(2*math.Pi*440*float64(i)/float64(audiopool.SampleRate)) * 16000)
	}

	buf := make([]byte, 1500)

	n, err := enc.Encode(pcm, buf)
	if err != nil {
		b.Fatal(err)
	}

	encoded := buf[:n]
	out := make([]int16, audiopool.FrameSize)

	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		if _, err := dec.Decode(encoded, out); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkDecodeOpusS16 measures the receive hot path: DecodeS16 decodes
// directly into an int16 output buffer (Phase 5 int16-native pipeline).
func BenchmarkDecodeOpusS16(b *testing.B) {
	enc, err := codec.NewOpusEncoder(audiopool.SampleRate, audiopool.Channels, targetBitrate, encoderComplexity, packetLossPerc)
	if err != nil {
		b.Fatal(err)
	}

	dec, err := codec.NewOpusDecoder(audiopool.SampleRate, audiopool.Channels)
	if err != nil {
		b.Fatal(err)
	}

	pcm := make([]int16, audiopool.FrameSize)
	for i := range pcm {
		pcm[i] = int16(math.Sin(2*math.Pi*440*float64(i)/float64(audiopool.SampleRate)) * 16000)
	}

	buf := make([]byte, 1500)

	n, err := enc.Encode(pcm, buf)
	if err != nil {
		b.Fatal(err)
	}

	encoded := buf[:n]
	out := make([]int16, audiopool.FrameSize)

	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		if _, err := dec.DecodeS16(encoded, out); err != nil {
			b.Fatal(err)
		}
	}
}

// ─── Jitter buffer benchmarks ────────────────────────────────────────────────

// BenchmarkJitterPush measures steady-state push throughput with a warmed pool.
// The buffer is created once outside the loop so pool slots are reused across
// iterations, reflecting production behavior.
func BenchmarkJitterPush(b *testing.B) {
	payload := make([]byte, 100) // typical Opus frame size
	jb := rtp.NewJitterBuffer(3, rtp.MaxDepth)

	b.ResetTimer()
	b.ReportAllocs()

	for i := range b.N {
		seq := uint16(i % rtp.MaxDepth)
		jb.Push(seq, payload)
	}
}

// BenchmarkJitterPushPop measures the push→pop cycle with pool recycling.
// releasePayload is called after each pop to return the buffer to the pool,
// mirroring what playoutLoop does in production.
func BenchmarkJitterPushPop(b *testing.B) {
	payload := make([]byte, 100)
	jb := rtp.NewJitterBuffer(1, rtp.MaxDepth)

	b.ResetTimer()
	b.ReportAllocs()

	for i := range b.N {
		seq := uint16(i)
		jb.Push(seq, payload)

		p, _, _ := jb.PopReady()
		if p != nil {
			jb.ReleasePayload(p)
		}
	}
}

// BenchmarkJitterPopReady fills the buffer once, then measures sustained pop
// performance. Each popped payload is returned to the pool so subsequent pushes
// reuse the same allocations.
func BenchmarkJitterPopReady(b *testing.B) {
	payload := make([]byte, 100)
	jb := rtp.NewJitterBuffer(1, rtp.MaxDepth)

	// Pre-fill to warm the pool and prime the playout cursor.
	for i := range uint16(rtp.MaxDepth) {
		jb.Push(i, payload)
	}

	b.ResetTimer()
	b.ReportAllocs()

	seq := uint16(rtp.MaxDepth)

	for b.Loop() {
		p, _, _ := jb.PopReady()
		if p != nil {
			jb.ReleasePayload(p)
		}

		jb.Push(seq, payload)
		seq++
	}
}

// ─── RTP parse benchmark ─────────────────────────────────────────────────────

func BenchmarkParseIncomingRTP(b *testing.B) {
	orig := &pionrtp.Packet{
		Header: pionrtp.Header{
			Version:        2,
			PayloadType:    rtp.PayloadTypeOpus,
			SequenceNumber: 42,
			Timestamp:      1000,
			SSRC:           0xDEADBEEF,
		},
		Payload: make([]byte, 100),
	}

	raw, err := orig.Marshal()
	if err != nil {
		b.Fatal(err)
	}

	// Mirror the receiveLoop hot path: one Packet reused across parses.
	var pkt pionrtp.Packet

	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		if err := rtp.ParseIncomingInto(raw, &pkt); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkReceiverRead measures the per-packet cost of the receive socket
// read exactly as receiveLoop performs it: through the PacketReader interface
// with a SwappableReceiver wrapping a real *net.UDPConn on loopback. The
// sender write inside the loop is the same single sendto(2) both before and
// after any read-path change, so alloc deltas isolate the read side.
func BenchmarkReceiverRead(b *testing.B) {
	recvConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		b.Fatal(err)
	}

	b.Cleanup(func() { _ = recvConn.Close() })

	sendConn, err := net.DialUDP("udp4", nil, recvConn.LocalAddr().(*net.UDPAddr))
	if err != nil {
		b.Fatal(err)
	}

	b.Cleanup(func() { _ = sendConn.Close() })

	var r rtp.PacketReader = rtp.NewSwappableReceiver(recvConn)

	buf := make([]byte, 1500)
	msg := make([]byte, 160) // typical Opus 20ms frame + RTP header

	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		if _, err := sendConn.Write(msg); err != nil {
			b.Fatal(err)
		}

		if _, _, err := r.ReadFromUDPAddrPort(buf); err != nil {
			b.Fatal(err)
		}
	}
}

// ─── Conversion benchmarks ───────────────────────────────────────────────────

// BenchmarkMicGainInt16 measures the broadcast encoder's in-place int16 gain
// stage after Phase 5. No float32↔int16 conversion runs on the hot path.
func BenchmarkMicGainInt16(b *testing.B) {
	in := make([]int16, audiopool.FrameSize)
	for i := range in {
		in[i] = int16(math.Sin(2*math.Pi*440*float64(i)/float64(audiopool.SampleRate)) * 16000)
	}

	const gain = float32(1.5)

	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		for i, v := range in {
			scaled := float32(v) * gain
			if scaled > 32767 {
				scaled = 32767
			} else if scaled < -32768 {
				scaled = -32768
			}

			in[i] = int16(scaled)
		}
	}
}

// ─── playoutOneFrame benchmarks ──────────────────────────────────────────────

// BenchmarkPlayoutOneFrame_Mock measures the playout primitive with a no-op
// decoder. The PCM output buffer is reused across iterations so 0 allocs/op
// confirms that the decode path is allocation-free.
func BenchmarkPlayoutOneFrame_Mock(b *testing.B) {
	cfg := &CommsConfig{Log: zerolog.Nop()}

	pc := &PortChannel{cfg: McastPortConfig{Send: true, Receive: true}}
	pc.SendEnabled.Store(true)
	pc.ReceiveEnabled.Store(true)

	pc.Decoder = &mockDecoder{returnN: audiopool.FrameSize}
	rt := &CommsRuntime{}

	jb := rtp.NewJitterBuffer(1, rtp.MaxDepth)
	payload := make([]byte, 100)
	out := make([]int16, audiopool.FrameSize)

	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		// Push a fresh payload each iteration so the buffer never empties.
		jb.Push(0, payload)
		cfg.playoutOneFrame(pc, rt, jb, out)
	}
}

// BenchmarkPlayoutOneFrame_Real measures playoutOneFrame using the real Opus
// decoder, confirming that DecodeFloat32 directly into a caller-supplied
// buffer yields 0 allocs/op.
func BenchmarkPlayoutOneFrame_Real(b *testing.B) {
	enc, err := codec.NewOpusEncoder(audiopool.SampleRate, audiopool.Channels, targetBitrate, encoderComplexity, packetLossPerc)
	if err != nil {
		b.Fatal(err)
	}

	dec, err := codec.NewOpusDecoder(audiopool.SampleRate, audiopool.Channels)
	if err != nil {
		b.Fatal(err)
	}

	pcm := make([]int16, audiopool.FrameSize)
	for i := range pcm {
		pcm[i] = int16(math.Sin(2*math.Pi*440*float64(i)/float64(audiopool.SampleRate)) * 16000)
	}

	encBuf := make([]byte, 1500)

	n, err := enc.Encode(pcm, encBuf)
	if err != nil {
		b.Fatal(err)
	}

	encoded := encBuf[:n]

	cfg := &CommsConfig{Log: zerolog.Nop()}

	pc := &PortChannel{cfg: McastPortConfig{Send: true, Receive: true}}
	pc.SendEnabled.Store(true)
	pc.ReceiveEnabled.Store(true)
	pc.Decoder = dec

	rt := &CommsRuntime{}

	jb := rtp.NewJitterBuffer(1, rtp.MaxDepth)
	out := make([]int16, audiopool.FrameSize)

	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		jb.Push(0, encoded)
		cfg.playoutOneFrame(pc, rt, jb, out)
	}
}

// ─── PLC decode benchmark ───────────────────────────────────────────────────

// BenchmarkPlayoutOneFrame_PLC measures the PLC decode path through
// playoutOneFrame using the real Opus decoder. The jitter buffer is set up
// so that every call hits the conceal branch and invokes DecodePLCFloat32.
func BenchmarkPlayoutOneFrame_PLC(b *testing.B) {
	enc, err := codec.NewOpusEncoder(audiopool.SampleRate, audiopool.Channels, targetBitrate, encoderComplexity, packetLossPerc)
	if err != nil {
		b.Fatal(err)
	}

	dec, err := codec.NewOpusDecoder(audiopool.SampleRate, audiopool.Channels)
	if err != nil {
		b.Fatal(err)
	}

	// Feed the decoder one real frame first so PLC has state to work with.
	pcm := make([]int16, audiopool.FrameSize)
	for i := range pcm {
		pcm[i] = int16(math.Sin(2*math.Pi*440*float64(i)/float64(audiopool.SampleRate)) * 16000)
	}

	encBuf := make([]byte, 1500)

	n, err := enc.Encode(pcm, encBuf)
	if err != nil {
		b.Fatal(err)
	}

	warmup := make([]int16, audiopool.FrameSize)
	if _, err := dec.DecodeS16(encBuf[:n], warmup); err != nil {
		b.Fatal(err)
	}

	cfg := &CommsConfig{Log: zerolog.Nop()}

	pc := &PortChannel{cfg: McastPortConfig{Send: true, Receive: true}}
	pc.SendEnabled.Store(true)
	pc.ReceiveEnabled.Store(true)
	pc.Decoder = dec

	rt := &CommsRuntime{}

	// Prime the jitter buffer with a single push+pop so started=true and
	// lastPush is recent; subsequent playoutOneFrame calls will hit conceal.
	jb := rtp.NewJitterBuffer(1, rtp.MaxDepth)
	jb.Push(0, encBuf[:n])
	jb.PopReady()

	out := make([]int16, audiopool.FrameSize)

	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		// Reset consecutivePLC each iteration so we always exercise the
		// PLC decode path rather than falling through to silence.
		pc.ConsecutivePLC = 0
		cfg.playoutOneFrame(pc, rt, jb, out)
	}
}

// ─── Burst loss conceal benchmark ───────────────────────────────────────────

// BenchmarkPopOrConceal_BurstLoss simulates a burst loss scenario: frames are
// pushed and consumed, then the buffer is left empty while the stream is still
// active (recent lastPush). Repeated popOrConceal calls exercise the
// shouldConceal path, measuring conceal decision throughput during burst loss.
func BenchmarkPopOrConceal_BurstLoss(b *testing.B) {
	payload := make([]byte, 100)
	jb := rtp.NewJitterBuffer(1, rtp.MaxDepth)

	// Push and pop one frame to start the buffer and set lastPush.
	jb.Push(0, payload)

	if p, _, _ := jb.PopReady(); p != nil {
		jb.ReleasePayload(p)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		// Refresh lastPush to keep shouldConceal returning true.
		jb.Push(1, payload)

		if p, _, _ := jb.PopReady(); p != nil {
			jb.ReleasePayload(p)
		}

		// Simulate 5 consecutive conceal decisions on empty buffer.
		for range 5 {
			p, _ := jb.PopOrConceal(100 * time.Millisecond)
			if p != nil {
				jb.ReleasePayload(p)
			}
		}
	}
}

// ─── popOrConceal benchmark ──────────────────────────────────────────────────

// BenchmarkPopOrConceal measures steady-state popOrConceal with a warmed pool.
// Setup (push) is done outside b.Loop(); releasePayload mirrors playoutLoop.
func BenchmarkPopOrConceal(b *testing.B) {
	payload := make([]byte, 100)
	jb := rtp.NewJitterBuffer(1, rtp.MaxDepth)

	// Prime the playout cursor: push seq=0, pop it to start the buffer.
	jb.Push(0, payload)

	if p, _, _ := jb.PopReady(); p != nil {
		jb.ReleasePayload(p)
	}

	b.ReportAllocs()

	seq := uint16(1)

	for b.Loop() {
		// Keep 5 frames ahead of the playout cursor.
		for range 5 {
			jb.Push(seq, payload)
			seq++
		}

		for range 5 {
			p, _ := jb.PopOrConceal(100 * time.Millisecond)
			if p != nil {
				jb.ReleasePayload(p)
			}
		}
	}
}
