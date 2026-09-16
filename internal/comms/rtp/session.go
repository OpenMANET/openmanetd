package rtp

import (
	"fmt"
	"hash/fnv"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/interceptor/pkg/report"
	pionrtcp "github.com/pion/rtcp"
	pionrtp "github.com/pion/rtp"
	"github.com/rs/zerolog"
)

const (
	PayloadTypeOpus = uint8(111)
	rtpVersion      = 2
	rtpClockRate    = uint32(48000)
	FrameSamples    = uint32(960) // 20 ms at 48 kHz
	MTU             = uint16(1400)

	// rtpBufSize is the pool buffer capacity for RTP packet serialization.
	// Must be >= MTU + maximum RTP header size (~60 bytes for 15 CSRCs).
	rtpBufSize = 1500

	// streamMIMETypeOpus is the MIME type for Opus audio streams, used in StreamInfo.
	streamMIMETypeOpus = "audio/opus"
)

// rtpMarshalPool pools serialization buffers for the baseRTPWriter hot path,
// eliminating both the pionrtp.Packet struct allocation and the Marshal()
// output slice allocation that would otherwise occur on every RTP send.
var rtpMarshalPool = sync.Pool{ //nolint:gochecknoglobals
	New: func() any {
		s := make([]byte, rtpBufSize)

		return &s
	},
}

// Sender is the interface the broadcast encoder uses to ship an
// encoded Opus frame over the network. Backed by Session in production;
// tests inject mockRTPSender.
type Sender interface {
	Send(payload []byte) error
}

// Session frames outbound Opus payloads as RTP packets and feeds them
// through an interceptor chain that generates periodic RTCP Sender Reports.
// One session represents one local SSRC (the node running this software).
//
// The RTCP path is one-way outbound: the SR generator fires every 5 seconds
// and writes to the provided rtcpTransport. Inbound RTP is parsed externally
// with ParseIncoming; the session does not handle receive stats (each
// remote SSRC in a multicast group would require a separate receiver).
//
// Concurrency: Send is the sole writer to sequencer, timestamp, hdr, and
// rtpWriter. In production each *Session is owned by exactly one PortChannel,
// and Send is only called from the per-encoder broadcastEncoder.encodeLoop
// goroutine (one writer per encodeLoop, one encodeLoop per process). The pion
// SenderInterceptor's RTCP timer runs on its own goroutine but only writes
// to the RTCP transport (bound separately at NewSession), not the RTP
// header state or rtpWriter — so there is no contention with Send. Adding a
// second concurrent Send caller without external synchronization would be a
// bug; the call invariant is enforced by code review, not a mutex.
type Session struct {
	log       zerolog.Logger
	sequencer pionrtp.Sequencer
	rtpWriter interceptor.RTPWriter
	intercept interceptor.Interceptor
	hdr       pionrtp.Header
	ssrc      uint32
	timestamp uint32
}

// SSRCFromID returns a deterministic uint32 SSRC derived from an ID string
// using the FNV-1a 32-bit hash.
func SSRCFromID(id string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))

	return h.Sum32()
}

// NewSession creates a Session that sends RTP via rtpTransport
// and RTCP Sender Reports via rtcpTransport.
//
// The interceptor chain contains a single report.SenderInterceptor that
// generates an outbound RTCP SR every 5 seconds. Inbound RTCP (e.g. Receiver
// Reports from peers) is not processed — in a multicast PTT topology each
// transmission has multiple receivers and no single feedback path is meaningful.
func NewSession(
	ssrc uint32,
	rtpTransport PacketWriter,
	rtcpTransport PacketWriter,
	log zerolog.Logger,
) (*Session, error) {
	registry := &interceptor.Registry{}

	// Sender report: generates RTCP SR packets on a 5-second timer.
	senderReport, err := report.NewSenderInterceptor(
		report.SenderInterval(5 * time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("report.NewSenderInterceptor: %w", err)
	}

	registry.Add(senderReport)

	i, err := registry.Build("")
	if err != nil {
		return nil, fmt.Errorf("interceptor.Registry.Build: %w", err)
	}

	// Bind the RTCP writer. The SenderInterceptor stores this internally and
	// calls it on its timer tick to deliver SR packets.
	_ = i.BindRTCPWriter(interceptor.RTCPWriterFunc(
		func(pkts []pionrtcp.Packet, _ interceptor.Attributes) (int, error) {
			var totalBytes int

			for _, pkt := range pkts {
				data, marshalErr := pkt.Marshal()
				if marshalErr != nil {
					log.Warn().Err(marshalErr).Msg("comms: RTCP marshal error")

					continue
				}

				n, writeErr := rtcpTransport.Write(data)
				totalBytes += n

				if writeErr != nil {
					log.Debug().Err(writeErr).Msg("comms: RTCP write error")
				}
			}

			return totalBytes, nil
		},
	))

	// StreamInfo describes our outbound Opus stream to the interceptors.
	streamInfo := &interceptor.StreamInfo{
		SSRC:        ssrc,
		PayloadType: PayloadTypeOpus,
		ClockRate:   rtpClockRate,
		MimeType:    streamMIMETypeOpus,
	}

	// baseRTPWriter serializes the RTP header + payload into a pooled buffer
	// and writes it to the UDP transport. MarshalPacketTo avoids both the
	// Packet struct allocation and the output slice allocation from Marshal().
	baseRTPWriter := interceptor.RTPWriterFunc(
		func(header *pionrtp.Header, payload []byte, _ interceptor.Attributes) (int, error) {
			var pkt pionrtp.Packet

			pkt.Header = *header
			pkt.Payload = payload

			size := pkt.MarshalSize()
			bufPtr := rtpMarshalPool.Get().(*[]byte) //nolint:forcetypeassert
			buf := (*bufPtr)[:size]

			n, marshalErr := pkt.MarshalTo(buf)
			if marshalErr != nil {
				rtpMarshalPool.Put(bufPtr)

				return 0, fmt.Errorf("rtp.Packet.MarshalTo: %w", marshalErr)
			}

			wrote, writeErr := rtpTransport.Write(buf[:n])

			rtpMarshalPool.Put(bufPtr)

			return wrote, writeErr
		},
	)

	// Bind the local stream — this connects Send through the interceptor
	// chain down to baseRTPWriter.
	rtpWriter := i.BindLocalStream(streamInfo, baseRTPWriter)

	s := &Session{
		log:       log,
		sequencer: pionrtp.NewRandomSequencer(),
		rtpWriter: rtpWriter,
		intercept: i,
		ssrc:      ssrc,
		timestamp: rand.Uint32(),
	}

	// Fields constant for the session lifetime are set once; Send only
	// touches SequenceNumber and Timestamp. Marker is always set: Opus is
	// one frame per packet, so every packet ends a talkspurt frame
	// (matches pion Packetize, which marks the last packet of each frame).
	s.hdr.Version = rtpVersion
	s.hdr.Marker = true
	s.hdr.PayloadType = PayloadTypeOpus
	s.hdr.SSRC = ssrc

	return s, nil
}

// Send frames payload as a single RTP packet and writes it through the
// interceptor chain (and ultimately the UDP socket).
//
// This bypasses pion's Packetizer, which costs 4 heap allocations plus a
// payload copy per frame (OpusPayloader.Payload copies into a fresh slice
// inside a [][]byte, Packetize allocates the []*Packet slice and the Packet
// struct). Building the header in the reused s.hdr field and handing the
// payload straight to the pooled writer path makes Send allocation-free;
// the wire format is pinned byte-for-byte by TestPionRTPSession_WireFormat.
//
// An empty payload emits nothing but still advances the media clock,
// preserving pion's Packetize/SkipSamples semantics.
//
// Send is single-writer; see Session's type comment for the call invariant.
func (s *Session) Send(payload []byte) error {
	if len(payload) == 0 {
		s.timestamp += FrameSamples

		return nil
	}

	s.hdr.SequenceNumber = s.sequencer.NextSequenceNumber()
	s.hdr.Timestamp = s.timestamp
	s.timestamp += FrameSamples

	if _, err := s.rtpWriter.Write(&s.hdr, payload, nil); err != nil {
		s.log.Debug().Err(err).Msg("comms: RTP send error")

		return fmt.Errorf("rtp write: %w", err)
	}

	return nil
}

// Close shuts down the interceptor chain, stopping internal timer goroutines.
func (s *Session) Close() error {
	return s.intercept.Close()
}

// ParseIncoming parses a raw UDP datagram into a pion RTP Packet.
// Returns an error if the bytes are not a valid RTP packet.
//
// The returned Packet escapes to the heap; hot paths should use
// ParseIncomingInto with a caller-owned reused Packet instead.
func ParseIncoming(raw []byte) (*pionrtp.Packet, error) {
	var pkt pionrtp.Packet
	if err := ParseIncomingInto(raw, &pkt); err != nil {
		return nil, err
	}

	return &pkt, nil
}

// ParseIncomingInto parses a raw UDP datagram into pkt, which may be reused
// across calls: pion's Header.Unmarshal reuses CSRC capacity and resets
// Extensions, so every field is overwritten on each parse. pkt.Payload
// aliases raw — the caller must copy it before raw is reused (the jitter
// buffer's push already does).
func ParseIncomingInto(raw []byte, pkt *pionrtp.Packet) error {
	if err := pkt.Unmarshal(raw); err != nil {
		return fmt.Errorf("rtp.Packet.Unmarshal: %w", err)
	}

	return nil
}
