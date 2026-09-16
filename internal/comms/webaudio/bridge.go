// Package webaudio hosts the web-mode audio bridge that replaces the malgo
// pipeline when the comms control source is "web". The browser becomes the audio
// I/O device: it sends Opus frames over RPC (TX) and receives Opus frames
// popped from the per-port jitter buffer (RX). The bridge is a small plumbing
// layer between those RPC streams and the parent comms runtime — it never
// imports the parent package, keeping the dependency direction one-way.
package webaudio

import (
	"sync"
	"sync/atomic"

	"github.com/rs/zerolog"
)

// SendFn delivers an Opus payload from the browser to every send-enabled
// multicast port. The parent package captures this callback at bridge
// construction so webaudio has no knowledge of ports or runtime state.
type SendFn func(opusData []byte)

// maxFrameBytes is the pool buffer capacity for RX frames. RFC 6716 caps a
// single Opus frame at 1275 bytes, and the jitter buffer upstream rejects
// anything larger, so every payload reaching PushRxFrame fits.
const maxFrameBytes = 1275

// rxFrameDepth bounds the RX channel at ~200 ms of slack (10 frames at
// 50 fps), matching the pipeline's latency budget. Depth beyond that only
// adds staleness: the jitter buffer upstream already owns ordering and
// depth, and PushRxFrame evicts the oldest frame when full, so a stalled
// browser consumer resumes at most 200 ms behind live audio instead of
// pinned a full second back.
const rxFrameDepth = 10

// Frame carries one Opus payload from the mesh through the bridge to an
// RPC consumer. The backing buffer is pooled: the consumer must call
// Release exactly once when done with Data (after stream.Send has
// marshaled it), and must not retain the slice afterwards. The zero Frame
// is inert — Data returns nil, Channel returns 0, and Release is a no-op.
type Frame struct {
	b   *Bridge
	buf *[]byte
	n   int
	ch  byte
}

// Data returns the Opus payload. Valid until Release is called.
func (f Frame) Data() []byte {
	if f.buf == nil {
		return nil
	}

	return (*f.buf)[:f.n]
}

// Channel returns the 1-based talk group channel the payload was received
// on, or 0 when the producing port had no talk group mapping.
func (f Frame) Channel() byte {
	return f.ch
}

// Release returns the frame's pooled buffer to the bridge. Safe on the
// zero Frame; must not be called twice.
func (f Frame) Release() {
	if f.b == nil || f.buf == nil {
		return
	}

	f.b.framePool.Put(f.buf)
}

// Bridge connects the web RPC streaming handlers to the comms runtime.
//
//   - TX path: the RPC handler calls InjectTxFrame with Opus bytes from
//     the browser; the bridge forwards them via SendFn to all
//     send-enabled multicast ports.
//   - RX path: webPlayoutLoop calls PushRxFrame with raw Opus payloads
//     from the jitter buffer; the RPC handler reads them from RxFrames
//     and streams them back to the browser.
type Bridge struct {
	log      zerolog.Logger
	send     SendFn
	rxFrames chan Frame

	// framePool recycles RX frame buffers so the steady-state push →
	// consume → release cycle is allocation-free. Buffers are fixed at
	// maxFrameBytes capacity; Frame.n carries the payload length.
	framePool sync.Pool

	// consumers counts the RPC streams currently reading RxFrames. The
	// producer side (webPlayoutLoop) checks HasConsumer before doing any
	// per-frame work, so an idle web node (no browser tab attached) pays
	// nothing for RX traffic beyond draining the jitter buffer.
	consumers atomic.Int32

	// Diagnostic counters for the RX side. All are monotonic since
	// bridge construction; consumers compute deltas across reporting
	// windows. RxPushIn counts every PushRxFrame call (frames offered
	// by webPlayoutLoop); RxPushDrop counts the subset that the
	// non-blocking channel send dropped because rxFrames was full.
	// RxGatedNoConsumer counts frames the playout drain discarded
	// without offering because no consumer was attached.
	RxPushIn          atomic.Int64
	RxPushDrop        atomic.Int64
	RxGatedNoConsumer atomic.Int64
}

// NewBridge creates a bridge wired to the given send callback and logger.
func NewBridge(log zerolog.Logger, send SendFn) *Bridge {
	b := &Bridge{
		log:      log,
		send:     send,
		rxFrames: make(chan Frame, rxFrameDepth),
	}

	b.framePool.New = func() any {
		s := make([]byte, maxFrameBytes)

		return &s
	}

	return b
}

// InjectTxFrame forwards a raw Opus frame from the web client to all
// send-enabled multicast ports via the bound SendFn. If the SendFn is
// nil the call is a no-op so a partially constructed bridge does not
// panic during early shutdown.
func (b *Bridge) InjectTxFrame(opusData []byte) {
	if b == nil || b.send == nil {
		return
	}

	b.send(opusData)
}

// AddConsumer registers an RPC stream as a reader of RxFrames. The producer
// side only does per-frame work while at least one consumer is registered.
// Callers must pair every AddConsumer with exactly one RemoveConsumer
// (typically via defer) when the stream ends.
//
// The first consumer to attach (0 → 1 transition) flushes any frames still
// queued from before it attached: those were pushed while the previous
// consumer was detaching and may be arbitrarily old, and a new browser
// session must start on live audio. A frame a concurrent producer pushes
// during the flush may be discarded with them — one frame, indistinguishable
// from an eviction. Non-first consumers never flush, so an active stream is
// not robbed of frames it is about to read.
func (b *Bridge) AddConsumer() {
	if b == nil {
		return
	}

	if b.consumers.Add(1) != 1 {
		return
	}

	for {
		select {
		case f := <-b.rxFrames:
			b.RxPushDrop.Add(1)
			f.Release()
		default:
			return
		}
	}
}

// RemoveConsumer deregisters an RPC stream previously registered with
// AddConsumer.
func (b *Bridge) RemoveConsumer() {
	if b == nil {
		return
	}

	b.consumers.Add(-1)
}

// HasConsumer reports whether at least one RPC stream is currently reading
// RxFrames.
func (b *Bridge) HasConsumer() bool {
	return b != nil && b.consumers.Load() > 0
}

// RxFrames returns a receive-only channel that delivers Opus frames from
// the mesh to the RPC handler. The consumer must Release every frame it
// receives.
func (b *Bridge) RxFrames() <-chan Frame {
	if b == nil {
		return nil
	}

	return b.rxFrames
}

// PushRxFrame delivers a raw Opus payload for the web client, tagged with
// the 1-based talk group channel it was received on (0 = unknown). The
// payload is copied into a pooled buffer, so the caller's slice may be
// reused the moment the call returns. Non-blocking; if the channel is full
// the frame is dropped and its buffer recycled.
func (b *Bridge) PushRxFrame(ch byte, opusData []byte) {
	if b == nil {
		return
	}

	// RxPushIn counts every invocation, including rejected ones — the
	// snapshot doc defines it that way, and it keeps the
	// rx_push_drop / rx_push_in triage ratio bounded at 1.
	b.RxPushIn.Add(1)

	// Cannot happen with a conforming upstream (the jitter buffer rejects
	// payloads over the RFC 6716 cap); guard so a future caller cannot
	// overrun the pooled buffer.
	if len(opusData) > maxFrameBytes {
		b.RxPushDrop.Add(1)

		return
	}

	bufPtr := b.framePool.Get().(*[]byte) //nolint:forcetypeassert
	copy((*bufPtr)[:len(opusData)], opusData)

	f := Frame{b: b, buf: bufPtr, n: len(opusData), ch: ch}

	select {
	case b.rxFrames <- f:
	default:
		// Full: evict the oldest frame (drop-oldest) so the consumer
		// resumes on the freshest audio — for PTT voice, fresh beats
		// stale — then retry the send once. Each branch logs what
		// actually happened; a consumer draining between the selects
		// means nothing is dropped at all, which must not log as a drop.
		select {
		case old := <-b.rxFrames:
			b.RxPushDrop.Add(1)
			old.Release()
			b.log.Debug().Msg("web: RX frame channel full; evicted oldest")
		default:
			// A consumer drained the channel between our two selects;
			// nothing to evict.
		}

		select {
		case b.rxFrames <- f:
		default:
			// Concurrent producers refilled the channel first; drop the
			// new frame rather than looping.
			b.RxPushDrop.Add(1)
			b.framePool.Put(bufPtr)
			b.log.Debug().Msg("web: RX frame channel contended; dropped newest")
		}
	}
}
