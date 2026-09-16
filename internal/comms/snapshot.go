package comms

import (
	"github.com/openmanet/openmanetd/internal/comms/announce"
	"github.com/openmanet/openmanetd/internal/comms/audio"
	"github.com/openmanet/openmanetd/internal/comms/control"
	"github.com/openmanet/openmanetd/internal/comms/gpio"
	"github.com/openmanet/openmanetd/internal/comms/rtp"
	"github.com/openmanet/openmanetd/internal/comms/webaudio"
)

// CommsSnapshot is the full comms subsystem section that the instrumentation
// registry publishes under the "comms" name. Field semantics are documented
// in docs/instrumentation-snapshot.md — keep that file in sync when adding
// or renaming fields here.
type CommsSnapshot struct {
	ControlSource    string                     `json:"control_source"`
	Ports            []PortSnapshot             `json:"ports"`
	BroadcastEncoder audio.AudioEncoderSnapshot `json:"broadcast_encoder"`
	WebBridge        webaudio.BridgeSnapshot    `json:"web_bridge"`
	FECAdapter       FECAdapterSnapshot         `json:"fec_adapter"`
	// Announcer is the voice-announcement player section.
	Announcer announce.Snapshot `json:"announcer"`
	// GPIOSelector is the hardware selector section (zeros off-Raven).
	GPIOSelector gpio.SelectorSnapshot `json:"gpio_selector"`
	// ActiveTalkgroup is the 1-based active talk group (0 when nothing
	// has been selected yet or comms is down).
	ActiveTalkgroup int `json:"active_talkgroup"`
	// TalkgroupEventsDropped counts events shed by bounded-buffer
	// listeners (streaming RPC subscribers) since comms start.
	TalkgroupEventsDropped uint64 `json:"talkgroup_events_dropped"`
	Enabled                bool   `json:"enabled"`
	Broadcasting           bool   `json:"broadcasting"`
	RemoteRxActive         bool   `json:"remote_rx_active"`
}

// PortSnapshot is the per-talk-group section of a CommsSnapshot.
type PortSnapshot struct {
	// Address is the multicast group address (e.g. "239.0.0.1").
	Address string `json:"address"`
	// Jitter is the per-port receive jitter buffer snapshot.
	Jitter rtp.JitterBufferSnapshot `json:"jitter"`
	// RxGate is the per-port half-duplex gate state.
	RxGate control.HalfDuplexGateSnapshot `json:"rx_gate"`
	// Port is the multicast UDP port.
	Port int `json:"port"`
	// QoSDSCP is the DSCP the kernel actually holds on this port's RTP
	// sender socket (read back at socket build time; RTCP carries the same
	// marking). 0 means unmarked: comms.dscp 0, a receive-only port, or a
	// marking failure.
	QoSDSCP int `json:"qos_dscp"`
	// QoSSOPriority is the kernel's SO_PRIORITY read-back for the same
	// socket. 256 + QoSDSCP>>3 when fully applied; a legacy TC_PRIO value
	// (0-6, derived by the kernel from IP_TOS) when the SO_PRIORITY
	// setsockopt failed and the socket is running TOS-only.
	QoSSOPriority int `json:"qos_so_priority"`
	// PlaybackUnderruns counts playback-side decode failures that the
	// port audio callback had to recover from via PLC.
	PlaybackUnderruns int64 `json:"playback_underruns"`
	// RxPkts is the monotonic count of successful ReadFromUDP returns on
	// this port's receive socket (packets the kernel handed us).
	RxPkts int64 `json:"rx_pkts"`
	// RxLoopback counts packets dropped by the loopback filter (own-IP
	// suppression) before they reached the RTP parser.
	RxLoopback int64 `json:"rx_loopback"`
	// RxParseErrs counts packets that failed rtp.ParseIncoming.
	RxParseErrs int64 `json:"rx_parse_errs"`
	// RxPushed counts packets that PushWithSSRC accepted into the jitter
	// buffer. In a healthy stream, RxPushed ≈ RxPkts - RxLoopback -
	// RxParseErrs.
	RxPushed int64 `json:"rx_pushed"`
	// RxPushRejected counts packets that PushWithSSRC rejected as stale,
	// duplicate, or overflow. A sustained nonzero delta while
	// jitter.ssrc_resets stays flat indicates a consumer-side
	// cursor-advance bug or severe sender reordering. See
	// "Interpretation heuristics" in docs/instrumentation-snapshot.md.
	RxPushRejected int64 `json:"rx_push_rejected"`
	// WebPoppedSkipped counts PopReady skippedMissing=true returns from
	// webPlayoutLoop: the jitter buffer had enough queued out-of-order
	// packets to advance the cursor past a missing frame. Zero on the
	// hardware playout path.
	WebPoppedSkipped int64 `json:"web_popped_skipped"`
	// SendEnabled is the runtime send-direction toggle. A port with
	// SendEnabled=false will not open an encoder or publish TX frames.
	SendEnabled bool `json:"send_enabled"`
	// ReceiveEnabled is the runtime receive-direction toggle. A port
	// with ReceiveEnabled=false will not push incoming RTP frames into
	// its jitter buffer.
	ReceiveEnabled bool `json:"receive_enabled"`
}

// Snapshot fills dst with a consistent view of the comms runtime. The
// method is nil-safe on the receiver and dereferences s.Rt exactly once
// so it observes a single consistent runtime pointer across the call,
// even if SetDefault races with it.
//
// dst.Ports is reused across captures: the slice capacity is retained
// and only resized when the number of ports changes. After the first
// call with the steady-state runtime present, Snapshot is allocation-
// free (verified by TestService_SnapshotZeroAlloc).
func (s *Service) Snapshot(dst *CommsSnapshot) {
	if dst == nil {
		return
	}

	if s == nil {
		dst.Enabled = false
		dst.Broadcasting = false
		dst.RemoteRxActive = false
		dst.ControlSource = ""
		dst.BroadcastEncoder = audio.AudioEncoderSnapshot{}
		dst.WebBridge = webaudio.BridgeSnapshot{}
		dst.Ports = dst.Ports[:0]
		dst.ActiveTalkgroup = 0
		dst.TalkgroupEventsDropped = 0
		dst.Announcer = announce.Snapshot{}
		dst.GPIOSelector = gpio.SelectorSnapshot{}

		return
	}

	if s.Cfg != nil {
		dst.ControlSource = s.Cfg.ControlSource
	} else {
		dst.ControlSource = ""
	}

	rt := s.Rt
	if rt == nil {
		dst.Enabled = false
		dst.Broadcasting = false
		dst.RemoteRxActive = false
		dst.BroadcastEncoder = audio.AudioEncoderSnapshot{}
		dst.WebBridge = webaudio.BridgeSnapshot{}
		dst.Ports = dst.Ports[:0]
		dst.ActiveTalkgroup = 0
		dst.TalkgroupEventsDropped = 0
		dst.Announcer = announce.Snapshot{}
		dst.GPIOSelector = gpio.SelectorSnapshot{}

		return
	}

	dst.Enabled = true
	dst.Broadcasting = rt.Broadcasting.Load()
	dst.RemoteRxActive = rt.RemoteRxActive.Load()
	dst.ActiveTalkgroup = int(rt.ActiveChannel.Load())
	dst.TalkgroupEventsDropped = rt.Events.Dropped()
	rt.Announcer.Snapshot(&dst.Announcer)
	rt.GPIOSel.Snapshot(&dst.GPIOSelector)

	// BroadcastStream is an interface. In production the live instance is
	// always a *audio.BroadcastEncoder; test fakes may substitute a
	// minimal stub that does not carry counters. The type assertion falls
	// back to a zero snapshot in that case.
	if be, ok := rt.Broadcast().(*audio.BroadcastEncoder); ok {
		be.Snapshot(&dst.BroadcastEncoder)
	} else {
		dst.BroadcastEncoder = audio.AudioEncoderSnapshot{}
	}

	rt.WebBridge.Snapshot(&dst.WebBridge)

	rt.FECAdapter.Snapshot(&dst.FECAdapter)

	n := len(rt.Ports)
	if cap(dst.Ports) < n {
		dst.Ports = make([]PortSnapshot, n)
	} else {
		dst.Ports = dst.Ports[:n]
	}

	for i, pc := range rt.Ports {
		pc.Snapshot(&dst.Ports[i])
	}
}

// Snapshot fills dst with the port's counter state. Nil-safe. Zero-alloc.
func (pc *PortChannel) Snapshot(dst *PortSnapshot) {
	if pc == nil || dst == nil {
		return
	}

	dst.Address = pc.cfg.Address
	dst.Port = pc.cfg.Port
	dst.QoSDSCP = int(pc.QoSDSCP.Load())
	dst.QoSSOPriority = int(pc.QoSSOPriority.Load())
	dst.SendEnabled = pc.SendEnabled.Load()
	dst.ReceiveEnabled = pc.ReceiveEnabled.Load()
	dst.PlaybackUnderruns = pc.PlaybackUnderruns.Load()
	dst.RxPkts = pc.RxPkts.Load()
	dst.RxLoopback = pc.RxLoopback.Load()
	dst.RxParseErrs = pc.RxParseErrs.Load()
	dst.RxPushed = pc.RxPushed.Load()
	dst.RxPushRejected = pc.RxPushRejected.Load()
	dst.WebPoppedSkipped = pc.WebPoppedSkipped.Load()
	pc.Jitter.Snapshot(&dst.Jitter)
	pc.RxGate.Snapshot(&dst.RxGate)
}

// CommsSnapshotter is an instrumentation.Snapshotter adapter that wires the
// comms subsystem into the instrumentation registry. It holds an internal
// CommsSnapshot that is refreshed in place on every Refresh() call. The
// adapter looks up the live comms service on each call so it transparently
// handles enable/disable transitions.
type CommsSnapshotter struct {
	data CommsSnapshot
}

// Refresh implements instrumentation.Snapshotter. Zero-alloc after the
// first call that establishes the Ports slice capacity.
func (c *CommsSnapshotter) Refresh() {
	Default().Snapshot(&c.data)
}

// Data implements instrumentation.Snapshotter. Returns a pointer that is
// stable across Refresh calls.
func (c *CommsSnapshotter) Data() any {
	return &c.data
}
