package comms

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"time"

	pionrtp "github.com/pion/rtp"
	"github.com/rs/zerolog"

	"github.com/openmanet/openmanetd/internal/comms/control"
	"github.com/openmanet/openmanetd/internal/comms/rtp"
	"github.com/openmanet/openmanetd/internal/config"
)

// maxConsecutivePLC caps the number of consecutive Packet-Loss-Concealment
// frames the playout path will emit before falling back to silence. Opus PLC
// is designed for short losses but stays acceptable up to ~200 ms; beyond
// that the synthesized output becomes increasingly robotic, so silence is
// preferable. With a 20 ms frame, 10 frames ≈ 200 ms. The previous 100 ms
// cap was conservative and bailed out of PLC mid-burst on transient mesh
// losses, producing audible muted gaps.
const maxConsecutivePLC = 10

// concealRecentWindow is the inter-arrival window during which a missing
// frame is treated as a transient gap (PLC) rather than the end of the
// stream (silence). It matches maxConsecutivePLC × 20 ms so the two
// concealment heuristics agree on what counts as "recent": shorter, and
// popOrConceal would hand back genuine silence even though playoutOneFrame
// is still willing to PLC.
const concealRecentWindow = 200 * time.Millisecond

// webStatDefaultInterval is the webPlayoutLoop stat-reporting period.
// Overridable per-config for tests via CommsConfig.webStatInterval.
const webStatDefaultInterval = 2 * time.Second

const (
	// receiveErrStreakThreshold is the number of consecutive ReadFromUDP
	// failures after which receiveLoop starts backing off between attempts.
	// The first errors stay instant so the socket-swap path (a single
	// net.ErrClosed while UpdateMulticastEndpoint closes the old socket to
	// unblock the read) never pays the backoff.
	receiveErrStreakThreshold = 3

	// receiveErrBackoff bounds the retry rate on a persistently failing
	// socket to ~100 attempts/s instead of a busy spin that would pin a
	// core on the embedded targets.
	receiveErrBackoff = 10 * time.Millisecond
)

// zeroInt16 fills an int16 slice with zeros. Used by the playout callback to
// emit silence into the malgo int16 playback buffer.
func zeroInt16(out []int16) {
	for i := range out {
		out[i] = 0
	}
}

// rxActiveThreshold is retained as the historical alias for the default
// half-duplex threshold. New code should use control.DefaultHalfDuplexThreshold
// or the per-port HalfDuplexGate threshold.
const rxActiveThreshold = control.DefaultHalfDuplexThreshold

// halfDuplexDecayInterval is the period at which halfDuplexDecayLoop walks
// every port's HalfDuplexGate and clears CommsRuntime.RemoteRxActive when no
// gate is within its receive window. 100 ms is fine-grained enough that the
// PTT path observes a quiet channel within roughly one decay tick after the
// last inbound packet, which is well below the 400 ms half-duplex threshold.
const halfDuplexDecayInterval = 100 * time.Millisecond

// isReceivingRemote returns true when a valid RTP packet was received from a
// remote peer within the half-duplex window on any send-enabled port. This is
// the receive-side component of half-duplex: transmission must not begin
// while a shared send+receive channel is actively carrying incoming audio.
//
// The result is cached in rt.RemoteRxActive so the PTT path runs in O(1)
// rather than walking every PortChannel on each call. The cache is set by
// receiveLoop the moment a packet arrives on a send-enabled port (so the
// start of a stream cannot be missed) and cleared by halfDuplexDecayLoop on
// a coarse ticker once every gate's window has expired.
func (cfg *CommsConfig) isReceivingRemote(rt *CommsRuntime) bool {
	return rt.RemoteRxActive.Load()
}

// halfDuplexDecayLoop clears rt.RemoteRxActive when no send-enabled port has
// an active HalfDuplexGate. It runs on a halfDuplexDecayInterval ticker so
// the PTT path observes a quiet channel within ~100 ms of the last inbound
// packet on the previously active gate. The set side is handled by
// receiveLoop on every inbound packet — the decay loop only ever clears.
func (cfg *CommsConfig) halfDuplexDecayLoop(ctx context.Context, rt *CommsRuntime) {
	ticker := time.NewTicker(halfDuplexDecayInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		anyActive := false

		for _, pc := range rt.Ports {
			if !pc.SendEnabled.Load() {
				continue
			}

			if pc.RxGate.Active() {
				anyActive = true

				break
			}
		}

		if !anyActive {
			rt.RemoteRxActive.Store(false)
		}
	}
}

// ─── Receive path ─────────────────────────────────────────────────────────────

// receiveLoop reads datagrams from pc.Receiver, parses them as RTP packets
// using pion/rtp, and pushes the payloads into the per-port jitter buffer.
//
// In non-web mode the malgo playback callback is the consumer (driven by
// the audio hardware clock); no playout goroutine is spawned. In web mode a
// stripped-down webPlayoutLoop forwards raw Opus payloads to the WebAudioBridge.
//
// The loop discards packets from our own IP (loopback prevention) unless
// cfg.Loopback is true, and skips queuing frames when pc.ReceiveEnabled is false.
func (cfg *CommsConfig) receiveLoop(ctx context.Context, pc *PortChannel, rt *CommsRuntime) { //nolint:gocognit
	buf := make([]byte, 1500)

	// pc.Jitter is allocated in buildSinglePortChannel for receive-capable
	// ports. Test code that constructs PortChannel directly may leave it
	// nil, in which case we allocate one here so the loop is self-sufficient.
	if pc.Jitter == nil {
		pc.Jitter = rtp.NewJitterBuffer(rtp.PrebufferPackets, rtp.MaxDepth)
	}

	jitter := pc.Jitter

	if rt.WebBridge != nil {
		go cfg.webPlayoutLoop(ctx, pc, jitter, rt)
	}

	// cachedLocalIP caches the parsed form of rt.LocalIP so the loopback
	// filter is a value comparison on every received packet. The cached
	// value is refreshed only when the string changes (i.e. on endpoint
	// swap). Stored unmapped so it compares equal to an unmapped source
	// address regardless of 4-in-6 representation.
	var (
		cachedLocalIPStr string
		cachedLocalIP    netip.Addr
	)

	// errStreak counts consecutive read failures. A handful of instant
	// retries covers the legitimate transients (socket swap); beyond the
	// threshold the loop backs off so a permanently dead socket cannot
	// busy-spin this goroutine.
	errStreak := 0

	// pkt is reused across iterations so the parsed packet does not escape
	// to the heap once per datagram (ParseIncomingInto overwrites every
	// field; the payload is copied by the jitter push before buf is reused).
	var pkt pionrtp.Packet

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		n, src, err := pc.Receiver.ReadFromUDPAddrPort(buf)
		if err == nil {
			pc.RxPkts.Add(1)

			errStreak = 0
		}

		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				// When UpdateMulticastEndpoint swaps the receiver, the old
				// socket is closed which unblocks this ReadFromUDP with a
				// "use of closed network connection" error. This is expected
				// and the next iteration will read from the new socket, so
				// log at Debug rather than Error.
				if errors.Is(err, net.ErrClosed) {
					cfg.Log.Debug().Msg("comms: recv socket swapped; resetting jitter buffer")
					jitter.Reset()
				} else {
					cfg.Log.Error().Err(err).Msg("comms: recv error")
				}

				errStreak++
				if errStreak >= receiveErrStreakThreshold {
					select {
					case <-ctx.Done():
						return
					case <-time.After(receiveErrBackoff):
					}
				}

				continue
			}
		}

		if p := rt.LocalIP.Load(); p != nil {
			if s := *p; s != cachedLocalIPStr {
				cachedLocalIPStr = s

				// A parse failure leaves the zero Addr, which compares
				// unequal to every source — the own-IP filter simply
				// stays inert until a valid LocalIP is published.
				addr, parseErr := netip.ParseAddr(s)
				if parseErr != nil {
					addr = netip.Addr{}
				}

				cachedLocalIP = addr.Unmap()
			}
		}

		// Unmap so a 4-in-6 source (::ffff:a.b.c.d) matches both the
		// loopback check and the cached v4 local address.
		srcAddr := src.Addr().Unmap()
		loopbackDrop := !cfg.Loopback && (srcAddr.IsLoopback() || srcAddr == cachedLocalIP)

		if loopbackDrop {
			pc.RxLoopback.Add(1)

			if cfg.Trace {
				cfg.Log.Trace().Str("src", src.String()).Msg("comms: dropping own packet")
			}

			continue
		}

		// Muted port: the packet is discarded regardless of content, so
		// skip the RTP unmarshal (and its parse-error accounting)
		// entirely. RxPkts and RxLoopback above still count while muted;
		// MarkRemoteRx below must not run for muted ports (unchanged —
		// it already sat below this check before the hoist).
		if !pc.ReceiveEnabled.Load() {
			continue
		}

		// Parse using pion/rtp for proper header validation.
		if parseErr := rtp.ParseIncomingInto(buf[:n], &pkt); parseErr != nil {
			pc.RxParseErrs.Add(1)
			cfg.Log.Debug().Err(parseErr).Int("bytes", n).Msg("comms: dropping non-RTP datagram")

			continue
		}

		if cfg.Trace {
			cfg.Log.Trace().
				Str("src", src.String()).
				Uint16("seq", pkt.Header.SequenceNumber).
				Uint32("ts", pkt.Header.Timestamp).
				Uint32("ssrc", pkt.Header.SSRC).
				Int("payload_bytes", len(pkt.Payload)).
				Msg("comms: RTP packet received")
		}

		// Record the arrival time for half-duplex enforcement and prime the
		// cached half-duplex flag immediately so a TX attempt that races
		// the next decay tick still observes the channel as busy.
		pc.MarkRemoteRx(rt)

		// Pass the pion payload directly to the jitter buffer; push()
		// performs its own defensive copy so a separate copy here is
		// unnecessary — the receive buffer (buf) is reused on the next
		// iteration anyway. The SSRC is tracked so that a new talker
		// joining the multicast group does not get silently dropped
		// because their starting sequence number happens to lie in the
		// "past half" of the previous talker's frozen cursor.
		if jitter.PushWithSSRC(pkt.SSRC, pkt.SequenceNumber, pkt.Payload, func(oldSSRC, newSSRC uint32) {
			cfg.Log.Info().
				Uint32("old_ssrc", oldSSRC).
				Uint32("new_ssrc", newSSRC).
				Msg("comms: RTP SSRC changed; jitter buffer reset")
		}) {
			pc.RxPushed.Add(1)
		} else {
			pc.RxPushRejected.Add(1)

			if n := jitter.Overflows.Load(); n > 0 && n%50 == 0 {
				cfg.Log.Warn().Int64("total_overflows", n).Msg("comms: jitter buffer overflow")
			}
		}
	}
}

// playoutOneFrame produces exactly one frame of PCM audio into out. It is
// the per-tick playout primitive: in production it is invoked from the
// malgo playback callback once per audio period (one call per ~20 ms), and
// in tests it can be driven directly with a synthetic []float32 buffer.
//
// Driving playout from the consumer (the audio hardware clock) eliminates
// the producer/consumer clock mismatch that the previous time.Ticker-based
// playoutLoop suffered from, and removes the playback buffer entirely as a
// carrier for decoded audio.
//
// Half-duplex: on send-capable ports the function returns silence while the
// node is broadcasting, to prevent local echo. Receive-only ports always
// play back. Web mode is irrelevant here because the malgo playback callback is
// not opened in web mode at all (web RX uses webPlayoutLoop instead).
//
// State: pc.ConsecutivePLC is owned exclusively by the callback closure for
// this port. Each port has its own malgo playback stream running on its own
// audio thread, so the field is single-writer. Tests must respect this by
// not invoking playoutOneFrame concurrently with the production callback.
func (cfg *CommsConfig) playoutOneFrame(pc *PortChannel, rt *CommsRuntime, jitter *rtp.JitterBuffer, out []int16) {
	// Half-duplex: emit silence while broadcasting on a send-capable port.
	if cfg.isBroadcasting(rt) && pc.SendEnabled.Load() {
		zeroInt16(out)

		return
	}

	if !pc.ReceiveEnabled.Load() {
		zeroInt16(out)

		return
	}

	if jitter == nil || pc.Decoder == nil {
		zeroInt16(out)

		return
	}

	payload, conceal := jitter.PopOrConceal(concealRecentWindow)
	if payload != nil {
		n, err := pc.Decoder.DecodeS16(payload, out)
		jitter.ReleasePayload(payload)

		if err != nil {
			cfg.Log.Debug().Err(err).Msg("comms: opus decode error; falling back to PLC")

			// Try PLC into the same buffer.
			n, err = pc.Decoder.DecodeS16(nil, out)
			if err != nil || n != len(out) {
				zeroInt16(out)
				pc.PlaybackUnderruns.Add(1)

				return
			}

			pc.ConsecutivePLC = 0

			return
		}

		if n != len(out) {
			for i := n; i < len(out); i++ {
				out[i] = 0
			}
		}

		if cfg.Trace {
			cfg.Log.Trace().Msgf("comms: decoded %d samples", n)
		}

		pc.ConsecutivePLC = 0

		return
	}

	if conceal {
		pc.ConsecutivePLC++
		if pc.ConsecutivePLC <= maxConsecutivePLC {
			if cfg.Trace {
				cfg.Log.Trace().Int("consecutive_plc", pc.ConsecutivePLC).Msg("comms: jitter buffer gap → PLC")
			}

			n, err := pc.Decoder.DecodeS16(nil, out)
			if err != nil || n != len(out) {
				zeroInt16(out)
			}

			return
		}

		// Sustained loss: emit clean silence rather than degraded PLC.
		zeroInt16(out)

		return
	}

	// Buffer empty and stream not active → genuine silence (no underrun).
	zeroInt16(out)
}

// webPlayoutLoop is the receive-side consumer used in web mode (rt.WebBridge
// non-nil). The malgo playback stream is not opened in web mode, so the loop is signal-driven:
// it parks on the jitter buffer's edge-triggered notify channel and forwards
// every queued raw Opus payload to the WebAudioBridge as soon as one arrives.
// The browser handles PLC, half-duplex display, and decoding, so this side is
// pure plumbing — no ticker, no decode, no concealment.
//
// A safety-net poll fires every 100 ms so the loop can still flush a frame
// in the unlikely event that a notify signal coalesced with a missed wake.
// In steady state the ticker case should never run.
func (cfg *CommsConfig) webPlayoutLoop(ctx context.Context, pc *PortChannel, jitter *rtp.JitterBuffer, rt *CommsRuntime) { //nolint:gocognit
	notify := jitter.EnableNotify()

	// webChannel tags every frame handed to the bridge with this port's
	// 1-based talk group channel so the RPC layer (and ultimately the
	// browser) can attribute RX audio to the right talk group. Resolved
	// once: the port never changes for the life of the loop. A port
	// outside the talk group plan tags 0 (unknown); consumers fall back
	// to channel 1. TalkGroupChannel caps channels at 32, so the byte
	// conversion cannot truncate.
	var webChannel byte
	if ch, chErr := config.TalkGroupChannel(pc.cfg.Port); chErr == nil {
		webChannel = byte(ch)
	}

	const safetyPoll = 100 * time.Millisecond

	ticker := time.NewTicker(safetyPoll)
	defer ticker.Stop()

	// Diagnostic counters local to this loop. popped tracks frames the
	// jitter buffer handed us; poppedSkipped tracks PopReady returning a
	// skippedMissing=true tick (out-of-order gap wide enough that the
	// jitter buffer advanced the cursor past the hole). Combined with
	// the per-port RxPkts/RxLoopback/RxParseErrs/RxPushed/RxPushRejected
	// counters and the jitter buffer's Overflows/SSRCResets/IdleResets,
	// they localize where RX frames are being lost on the server side.
	var popped, poppedSkipped int64

	statInterval := webStatDefaultInterval
	if cfg.webStatInterval > 0 {
		statInterval = cfg.webStatInterval
	}

	statTicker := time.NewTicker(statInterval)
	defer statTicker.Stop()

	var (
		lastPopped, lastPoppedSkipped                int64
		lastPushIn, lastPushDrop                     int64
		lastOverflows, lastSSRCResets, lastIdleReset int64
		lastRxPkts, lastRxLoopback, lastRxParseErrs  int64
		lastRxPushed, lastRxPushRejected             int64
		lastKernelDrops                              int64
		lastGap1, lastGap2to5, lastGap6to10          int64
		lastGap11to20, lastGap21to50, lastGapOver50  int64
	)

	// drain uses PopReady (not PopOrConceal) because the web consumer has
	// no sample-clocked output that requires phase-locked playout. A
	// safety-poll tick that fires against an empty-but-recent buffer must
	// NOT advance the jitter buffer cursor: that was the round-4 bug where
	// advancePastLocked ran every 100 ms and caused late-but-correct
	// arrivals to be rejected by pushLocked's seqLess(seq, expected)
	// check. PopReady never advances the cursor without popping a frame.
	// The only legitimate cursor advance is PopReady's internal "buffer
	// half-full of out-of-order packets, skip the missing one" branch,
	// which bumps poppedSkipped.
	drain := func() {
		for {
			payload, ready, skippedMissing := jitter.PopReady()
			if skippedMissing {
				poppedSkipped++

				pc.WebPoppedSkipped.Add(1)

				continue
			}

			if !ready {
				return
			}

			popped++

			// No browser stream attached: still pop (the cursor must
			// advance and the pooled payload must recycle) but skip the
			// copy and the bridge hand-off entirely. This is web mode's
			// common idle state — an unattended node receiving traffic.
			if !rt.WebBridge.HasConsumer() {
				rt.WebBridge.RxGatedNoConsumer.Add(1)
				jitter.ReleasePayload(payload)

				continue
			}

			// PushRxFrame copies into a bridge-pooled buffer, so the
			// jitter payload can be released immediately — the whole
			// hand-off is allocation-free.
			rt.WebBridge.PushRxFrame(webChannel, payload)
			jitter.ReleasePayload(payload)
		}
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-notify:
			drain()
		case <-ticker.C:
			drain()
		case <-statTicker.C:
			pushIn := rt.WebBridge.RxPushIn.Load()
			pushDrop := rt.WebBridge.RxPushDrop.Load()
			overflows := jitter.Overflows.Load()
			ssrcResets := jitter.SSRCResets.Load()
			idleResets := jitter.IdleResets.Load()

			rxPkts := pc.RxPkts.Load()
			rxLoopback := pc.RxLoopback.Load()
			rxParseErrs := pc.RxParseErrs.Load()
			rxPushed := pc.RxPushed.Load()
			rxPushRejected := pc.RxPushRejected.Load()

			gap1 := jitter.GapRuns1.Load()
			gap2to5 := jitter.GapRuns2to5.Load()
			gap6to10 := jitter.GapRuns6to10.Load()
			gap11to20 := jitter.GapRuns11to20.Load()
			gap21to50 := jitter.GapRuns21to50.Load()
			gapOver50 := jitter.GapRunsOver50.Load()

			dGap1 := gap1 - lastGap1
			dGap2to5 := gap2to5 - lastGap2to5
			dGap6to10 := gap6to10 - lastGap6to10
			dGap11to20 := gap11to20 - lastGap11to20
			dGap21to50 := gap21to50 - lastGap21to50
			dGapOver50 := gapOver50 - lastGapOver50

			dPopped := popped - lastPopped
			dPoppedSkipped := poppedSkipped - lastPoppedSkipped
			dPushIn := pushIn - lastPushIn
			dPushDrop := pushDrop - lastPushDrop
			dOverflows := overflows - lastOverflows
			dSSRCResets := ssrcResets - lastSSRCResets
			dIdleResets := idleResets - lastIdleReset
			dRxPkts := rxPkts - lastRxPkts
			dRxLoopback := rxLoopback - lastRxLoopback
			dRxParseErrs := rxParseErrs - lastRxParseErrs
			dRxPushed := rxPushed - lastRxPushed
			dRxPushRejected := rxPushRejected - lastRxPushRejected

			// The /proc/net/udp kernel-drop scan and the stat line are
			// debug-only telemetry, so both are gated: on RX activity in
			// this window (per in-process counters — an idle port skips
			// everything, eliminating the 5-port all-zero spam), and on
			// Debug logging actually being enabled (the log line is the
			// scan's only consumer, so scanning with Debug off is pure
			// waste). The one signal this can miss: a port whose ONLY
			// activity is kernel-side drops with zero successful reads —
			// that means the receive goroutine is stalled outright, which
			// surfaces far louder elsewhere (PLC, jitter underruns).
			active := dRxPkts > 0 || dPopped > 0 || dPoppedSkipped > 0
			if active && cfg.Log.GetLevel() <= zerolog.DebugLevel {
				// kernel_drops is the per-socket drop counter from
				// /proc/net/udp. readUDPDrops returns -1 with no error
				// when no row matches (e.g. on a non-Linux test host) —
				// treat that as zero so the delta arithmetic stays sane.
				kernelDrops, _ := cfg.readUDPDrops(pc.cfg.Port)
				if kernelDrops < 0 {
					kernelDrops = 0
				}

				dKernelDrops := kernelDrops - lastKernelDrops
				lastKernelDrops = kernelDrops

				cfg.Log.Debug().
					Int("port", pc.cfg.Port).
					Int64("pkt_rx", dRxPkts).
					Int64("pkt_loopback", dRxLoopback).
					Int64("pkt_parse_err", dRxParseErrs).
					Int64("pkt_pushed", dRxPushed).
					Int64("push_rejected", dRxPushRejected).
					Int64("popped", dPopped).
					Int64("popped_skipped", dPoppedSkipped).
					Int64("push_in", dPushIn).
					Int64("push_drop", dPushDrop).
					Int64("jitter_overflow", dOverflows).
					Int64("ssrc_resets", dSSRCResets).
					Int64("idle_resets", dIdleResets).
					Int64("kernel_drops", dKernelDrops).
					Int64("gap_runs_1", dGap1).
					Int64("gap_runs_2_5", dGap2to5).
					Int64("gap_runs_6_10", dGap6to10).
					Int64("gap_runs_11_20", dGap11to20).
					Int64("gap_runs_21_50", dGap21to50).
					Int64("gap_runs_over_50", dGapOver50).
					Msg("comms: web rx stats 2s")
			}

			lastPopped = popped
			lastPoppedSkipped = poppedSkipped
			lastPushIn = pushIn
			lastPushDrop = pushDrop
			lastOverflows = overflows
			lastSSRCResets = ssrcResets
			lastIdleReset = idleResets
			lastRxPkts = rxPkts
			lastRxLoopback = rxLoopback
			lastRxParseErrs = rxParseErrs
			lastRxPushed = rxPushed
			lastRxPushRejected = rxPushRejected
			lastGap1 = gap1
			lastGap2to5 = gap2to5
			lastGap6to10 = gap6to10
			lastGap11to20 = gap11to20
			lastGap21to50 = gap21to50
			lastGapOver50 = gapOver50
		}
	}
}
