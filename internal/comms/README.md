# Comms (Push-to-Talk) internals

This directory implements a multicast PTT audio pipeline in Go using malgo
(miniaudio), Opus, and the [pion](https://github.com/pion) RTP/RTCP stack. It
supports multiple parallel multicast talk groups, half-duplex enforcement,
four selectable PTT control sources (OpenVLM HID dongle, ROIP analog-radio
bridge, web/RPC, and Linux evdev keys), an adaptive Opus FEC controller, and
a browser-driven web mode that bypasses the audio backend entirely. See
[ARCHITECTURE.md](ARCHITECTURE.md) for the full internal walkthrough; this
README is the field-operator reference.

Build with `-tags omd_omit_comms` to exclude the entire package from the
binary (a stub `doc.go` kept behind the matching positive tag satisfies the
import graph). The full build requires CGo (libopus, hidapi, libasound;
miniaudio is vendored via malgo).

---

## High-level flow

1. **Configuration** is loaded via [`config`](../config/) (`comms.*` keys).
   `McastPorts` is sourced from `config.GetMulticastTalkGroups()` and carries
   one entry per multicast talk group; per-port send / receive direction can
   be toggled at runtime via `Service.EnableTalkGroupSend` /
   `EnableTalkGroupReceive`. Only the first talk group is active at startup
   — sockets are still opened for the rest so any port can be enabled later
   without a restart.
2. **Audio**:
   - Opus encoder/decoder are created with the VoIP profile.
   - In hardware modes, one shared malgo capture stream feeds an
     [`audio.BroadcastEncoder`](audio/encoder.go) whose dedicated encode
     goroutine runs Opus + UDP send off the audio callback thread. The
     capture device is opened **once at StartHardware** and stays open for
     the lifetime of the comms run; an atomic TX gate
     (`BroadcastEncoder.SetTxEnabled`) decides per-PTT whether captured
     frames reach the encoder. The VOX tap runs regardless of the gate.
   - One malgo playback stream is opened **per port**; each runs its
     own int16-native callback that drains a TX-beep side channel ahead
     of falling through to `playoutOneFrame`.
   - In `web` mode the malgo pipeline is skipped entirely; the browser is
     the I/O device and all audio crosses through
     [`webaudio.Bridge`](webaudio/bridge.go).
3. **Network**:
   - Each `McastPortConfig` entry opens its own UDP sender (dialled from the
     interface IP), receiver (`SO_REUSEPORT` listener on `0.0.0.0:<port>`),
     and RTCP sender on `<port>+1`. All three are wrapped in
     [`rtp.SwappableSender`](rtp/transport.go) /
     [`rtp.SwappableReceiver`](rtp/transport.go) so the underlying connections
     can be replaced without locking the hot path.
   - Each port also gets its own [`rtp.JitterBuffer`](rtp/jitter.go) and
     [`rtp.Session`](rtp/session.go) (one local SSRC per node).
4. **PTT control source** (selected by `controlSource`, dispatched via the
   [`control.Register`](control/source.go) registry):
   - `openvlm` (default): GPIO3 on the OpenVLM USB HID dongle —
     `PTTDown` on press, `PTTUp` on release (hold-to-talk). Also emits
     `AuxEvent` values for VOL+/VOL− button transitions.
   - `roip`: same dongle, no manual button — squelch GPIO (COS) with VOX
     fallback for analog radio bridging.
   - `web`: events injected via RPC handlers; the browser owns audio I/O.
   - `nanoptt`: Linux evdev key press → `PTTToggle` (press-to-toggle).
5. **Adaptive FEC**:
   - [`FECAdapter`](fec_adapter.go) runs as a separate goroutine, observes
     each port's jitter-buffer gap-run histogram, and adjusts the Opus
     encoder's packet-loss-perc level (20 / 30 / 40) in response to the
     measured loss EWMA. The configured `comms.packetLossPerc` is the
     **floor**; the adapter only ramps up above it.

---

## Audio/codec parameters

The constants live in [`audiopool/audiopool.go`](audiopool/audiopool.go):

- `audiopool.SampleRate` = **48 000 Hz**
- `audiopool.Channels` = **1 (mono)**
- `audiopool.FrameSize` = **960 samples** (20 ms at 48 kHz)
- `audiopool.EncBufSize` = **1450 bytes** (matches UDP MTU)
- Opus bitrate (`comms.go` `targetBitrate`, also exported as
  `comms.TargetBitrate`) = **32 000 bps**
- Opus complexity (`comms.go` `encoderComplexity`) = **5** (the libopus
  reference VoIP default), capped at 10 and overridable per
  `CommsConfig.EncoderComplexity` (1..10). The previous default of 10 was
  too expensive on `linux/mipsle` edge routers — at complexity 10, Opus
  encode at 48 kHz mono regularly took 20–30 ms per 20 ms frame, saturating
  the per-frame budget and dropping captured frames. Operators on faster
  CPUs can opt back in via `comms.encoderComplexity`.
- Initial packet loss percent (`comms.go` `packetLossPerc`) = **20**.
  Clamped to `[10, 40]` and used as the floor by `FECAdapter`.
- In-band FEC: **enabled**
- DTX: **disabled**

`audiopool` also exports the `Float32Pool` / `Int16Pool` / `EncBufPool`
`sync.Pool`s used by the capture / playback / encode hot paths to avoid
per-frame allocations.

### Playback device latency

Each port's malgo playback stream is opened with its
`DeviceConfig.PeriodSizeInFrames` derived from `comms.playbackLatencyMs` by
[`audio.Init.BuildAudio`](audio/init.go). When the config value is ≤ 0 the
code uses `audiopool.FrameSize` (one Opus frame, 20 ms) as a safe default;
otherwise it computes the equivalent frame count at 48 kHz. The
`playoutOneFrame` closure is re-aligned onto 20 ms chunks by the
[playback chunker](audio/malgo_playback.go) regardless of which period ALSA
ultimately picks, so the downstream encoder never sees the discrepancy.
Per port one malgo playback stream runs on its own audio thread; their
state is fully independent.

This is the only layer of buffering that protects against playback-side OS
scheduling stalls — the Go-side jitter buffer (`pc.Jitter`) sits upstream of
the DAC and cannot help once the audio thread is preempted. The four layers
absorb different classes of stutter:

| Class of stutter | Heard by | Mitigated by |
|---|---|---|
| Network arrival jitter (out-of-order, late packets) | Local listener | Go jitter prebuffer |
| Brief packet loss bursts | Local listener | Opus PLC + jitter buffer |
| Playback-side OS scheduling stalls | Local listener | malgo playback period buffer |
| Capture-side OS scheduling stalls | **Remote** listeners | malgo capture period buffer |

#### Tuning `comms.playbackLatencyMs`

Some hardware (e.g. the OpenVLM USB audio class device) wants a very small
period — ~20 ms or one full Opus frame per callback — which gives the audio
thread effectively zero scheduling slack and on-device stutter persists even
with a healthy Go-side jitter buffer. The `comms.playbackLatencyMs` config
knob lets you suggest a larger period directly (default: **60 ms** = three
Opus frames per callback, two frames of slack).

miniaudio (the C library behind malgo) may still round the period to a
backend-preferred value at `InitDevice` time — for example ALSA's USB audio
class driver typically rounds the request up to a power of two and gives us
1024 frames when we ask for 960. The TX path models that rounding via
`nextPow2` and multiplies by `malgoLowLatencyPeriods` (3, matching
miniaudio's LowLatency profile) to produce `Init.PlaybackOutputLatency` —
the value the beep-emergence settle wait anchors on. The requested period
is logged at Info level on stream open as `comms: playback stream opened`
with:

- `configured_latency_ms` — the value from `comms.playbackLatencyMs`
- `requested_period_frames` — what we passed to
  `malgo.DeviceConfig.PeriodSizeInFrames`
- `effective_period_frames` — the same value rounded up to the next
  power of two (the model of what ALSA actually uses)
- `requested_period_duration` — the equivalent duration at 48 kHz
- `periods` — `malgoLowLatencyPeriods` (3); the ALSA ring depth in periods
- `ring_latency` — `effective_period_frames * periods` converted to a
  duration; this is what `PlaybackOutputLatency` carries forward into
  `transmitSettleWait`
- `port` — the multicast port the playback stream belongs to
- `device` — the resolved malgo device name

miniaudio does not expose the negotiated runtime period after `InitDevice`,
so we log what we requested (and what we model) rather than what we got.
If audio-thread underruns persist after raising `comms.playbackLatencyMs`,
the next knob is the per-period count in `audio/malgo_playback.go` (which
would require restructuring `playoutOneFrame` to loop).

### Capture device latency

The shared malgo capture stream is opened with its
`DeviceConfig.PeriodSizeInFrames` derived from the
`comms.captureFramesPerBuffer` / `comms.captureLatencyMs` pair by
[`audio.Init.OpenBroadcastStream`](audio/init.go): `PeriodSizeInFrames`
defaults to `audiopool.FrameSize` (960, one 20 ms Opus frame) unless an
operator overrides it, and the ALSA periods count (the ring depth) is
derived from `captureLatencyMs` via `buildCapturePeriods` (clamped to
`[3, 16]`). The encode pipeline is unchanged because the captureChunker in
[`audio/malgo_capture.go`](audio/malgo_capture.go) re-aligns whatever period
ALSA picks back onto 20 ms chunks before frames reach the encoder.

This is the symmetric counterpart of `comms.playbackLatencyMs` for the
capture side. The failure mode is different in *whose* speaker stutters:

- **Playback preemption**: thread is late → DAC underruns → *this device's*
  local listener hears a click.
- **Capture preemption**: thread is late → ADC device buffer **overruns** →
  samples are silently dropped → the RTP stream sent over the air has a gap
  → **remote listeners** hear stutter.

So unlike playback, the on-device user is not the one who hears capture-side
underruns — the people you're talking to are. This makes capture-side stalls
much harder to detect by ear: a transmitter can sound fine to itself while
every receiver hears it stuttering.

#### Tuning `comms.captureLatencyMs`

Same logic as `comms.playbackLatencyMs`: hardware that needs a tiny period
leaves the capture audio thread with effectively zero scheduling slack.
Default is **60 ms** of total ring depth (three 20 ms periods), which
`buildCapturePeriods` translates into the matching `Periods` count handed
to miniaudio.

The requested period is logged at Info level on stream open as
`comms: broadcast stream opened` with:

- `configured_latency_ms` — the value from `comms.captureLatencyMs`
- `requested_period_frames` — what we passed to
  `malgo.DeviceConfig.PeriodSizeInFrames`
- `requested_period_duration` — the equivalent duration at 48 kHz
- `device` — the resolved malgo capture device name
- `encode_chan_depth` — `broadcastEncoderChanDepth` (currently 10)

If audio-thread overruns persist after raising `comms.captureLatencyMs`,
the next knob is `comms.captureFramesPerBuffer` (the period size itself);
changing it requires the encoder to keep consuming multi-frame chunks,
which the chunker already handles transparently.

### Always-on capture + TX gate

Under the unified design the capture device is **opened once** at
StartHardware and stays open for the lifetime of the comms run. The malgo
capture callback fires every 20 ms regardless of PTT state — what changes
per PTT cycle is whether the callback's output reaches the Opus encoder.

The pivot is `BroadcastEncoder.SetTxEnabled(bool)`, an atomic gate flipped
by `beginTransmission` / `endTransmission`. When closed:

- The audio callback still runs — `framesCaptured` and the inter-arrival
  gap stats still advance.
- The VOX tap (if subscribed) still receives float32 frames so the ROIP
  control source can make PTT decisions while otherwise idle.
- The Int16 → encCh hand-off is skipped; no Opus encode, no UDP send.

This eliminates the per-PTT device open/close that previously caused
"first-cycle" capture stalls on USB audio class devices.

### Encode + send goroutine

The malgo capture callback **does not** run the Opus encoder or
`sendToAllPorts`. Both moved off the audio callback thread into a dedicated
goroutine inside [`audio.BroadcastEncoder`](audio/encoder.go), mirroring how
the receive side already isolates Opus decode from each port's malgo
playback callback.

Why: the audio callback fires every 20 ms with no scheduling slack beyond
the device buffer above. If the work it does ever takes longer than that —
Opus encode at complexity 10 with FEC, blocking UDP multicast write to
multiple ports, GC pause, cgo thread handoff — the next callback misses
its deadline, the ADC ring buffer overruns, and samples are silently
dropped at the device. Web mode (`controlSource: web`) sidesteps this
because the browser does the encoding and the server only receives
pre-encoded bytes; hardware modes did not, until this change.

The hot path is **int16-native**: malgo delivers samples as a byte slice
which the wrapper interprets as `[]int16` directly, the encode goroutine
calls `EncodeS16` directly, and there is no float32↔int16 round-trip on
mipsle softfloat targets.

Pipeline:

```
malgo capture callback (audio thread, every 20 ms) — func(in []int16)
  ├─ recordCaptureArrival: update captureGapMaxNs / captureLateCount
  ├─ Optional VOX tap (always; not gated by TX): float32 conversion +
  │     non-blocking send to atomic-pointer chan via audiopool.Float32Pool
  ├─ if !txEnabled.Load(): return  ← TX gate closed
  ├─ audiopool.Int16Pool.Get(); copy(in)               ← cheap
  └─ non-blocking send → encCh (cap = broadcastEncoderChanDepth = 10)
       on full: framesDropped++, return slice to pool

encodeLoop goroutine (separate goroutine)
  └─ for fp := range encCh:
       ├─ apply MicGain in integer Q8 (1/256 steps), soft-knee limited:
       │     samples inside ±24576 (0.75 FS) pass through; above the knee
       │     a rational curve compresses toward the rail instead of
       │     flat-topping
       ├─ recordEncodeDuration around deps.Encoder.EncodeS16(pcm, buf)
       │     on error: encodeErrors++, log Debug, drop frame
       │     on first over-budget cycle: one-shot Warn
       └─ deps.Send(buf[:n])                       ← cfg.sendToAllPorts
             walks rt.Ports; for each pc with SendEnabled and
             RTPSess != nil:
               pc.RTPSess.Send(payload)
                 ├─ pion Packetizer + RTCP SR interceptor
                 └─ rtp.SwappableSender.Write → net.UDPConn.Write
```

`broadcastEncoderChanDepth = 10` frames = **200 ms** of slack, sized to
match the receive-side jitter prebuffer. Any encoder spike the receiver can
absorb downstream the producer can absorb upstream too. The previous depth
of 3 (60 ms) was too tight for slow MIPS targets where Opus encode plus GC
pauses regularly crossed the per-frame budget. To raise the slack, edit
`broadcastEncoderChanDepth` in [`audio/encoder.go`](audio/encoder.go) and
re-bench. Drops are counted, not silenced.

### Per-cycle cycle stats

Every PTT cycle, when `SetTxEnabled(false)` is called the
[`audio.BroadcastEncoder`](audio/encoder.go) logs a Debug line summarizing
what happened during the transmission:

```
comms: broadcast cycle stats captured=1500 encoded=1500 dropped=0
  encode_errors=0 encode_dur_max=8ms encode_dur_avg=4ms frame_budget=20ms
  capture_gap_max=21ms capture_late=0
```

- `captured` — frames delivered by malgo to the capture callback
  (≈ 50 × seconds-of-PTT at `audiopool.FrameSize` / `audiopool.SampleRate`)
- `encoded` — frames the consumer goroutine successfully Opus-encoded and
  shipped via `sendToAllPorts`
- `dropped` — frames the producer dropped because `encCh` was full (the
  consumer fell more than 200 ms behind cumulatively)
- `encode_errors` — frames where `EncodeS16` returned an error;
  the underlying error is also logged at Debug level
- `encode_dur_max` / `encode_dur_avg` — peak / mean libopus encode time
  during the cycle. Compare against `frame_budget` (20 ms).
- `frame_budget` — the per-frame deadline (one Opus frame at 48 kHz mono).
  The first frame to cross it within a cycle also triggers a one-shot
  `comms: opus encode exceeded per-frame budget` Warn.
- `capture_gap_max` — peak inter-arrival between successive callbacks. A
  value substantially above 20 ms indicates audio-thread preemption.
- `capture_late` — count of callbacks whose arrival was ≥ 2 × frame_budget
  late.

Diagnostic decision tree from the cycle stats:

| Pattern | Meaning | Next lever |
|---------|---------|------------|
| `dropped=0 encode_errors=0`, no stutter at remote | Pipeline healthy. | Done. |
| `dropped=0 encode_errors=0`, remote still stutters | Bottleneck is downstream of the encode goroutine. | Tune `SO_SNDBUF` on the multicast sockets; pcap on the wire to look for jitter or loss. |
| `dropped > 0` | Encode-and-send loop occasionally takes >200 ms cumulative. | Lower `EncoderComplexity` (try 3), grow `broadcastEncoderChanDepth`, or check `actual_input_latency` vs. requested. |
| `encode_dur_max ≈ frame_budget` | libopus is starving the audio thread. | Lower `EncoderComplexity`. |
| `capture_late > 0` or `capture_gap_max ≫ 20ms` | Audio callback thread is being preempted. | Raise `comms.captureLatencyMs`; check CPU contention. |
| `encode_errors > 0` | libopus is failing under pressure; the surfaced error message says why. | Address the specific error. |

The same counters are exported atomically via
[`audio.AudioEncoderSnapshot`](audio/snapshot.go) and surface in the
periodic instrumentation snapshot under `comms.broadcast_encoder` — see
[docs/instrumentation-snapshot.md](../../docs/instrumentation-snapshot.md).

### Receive path

1. One [`receiveLoop`](receive.go) goroutine per receive-capable port reads UDP
   datagrams from `pc.Receiver` (a `*rtp.SwappableReceiver`).
2. Parses them as RTP via [`rtp.ParseIncoming`](rtp/session.go).
3. Calls [`pc.MarkRemoteRx(rt)`](port_channel.go) to stamp the per-port
   `HalfDuplexGate` and prime `rt.RemoteRxActive`.
4. Pushes the payload via [`jitter.PushWithSSRC`](rtp/jitter.go) so an SSRC
   change (new talker) cleanly resets the buffer rather than silently
   dropping the new stream.
5. **Hardware mode**: each port's malgo playback callback calls
   `playoutOneFrame` once per ~20 ms, decoding into an int16 buffer; the
   callback first drains the per-port `PlaybackBuffer` (TX-beep side
   channel) before falling through.
6. **Web mode**: a `webPlayoutLoop` per port uses
   [`jitter.EnableNotify`](rtp/jitter.go) for edge-triggered wakeups and
   forwards raw Opus payloads to `rt.WebBridge.PushRxFrame`, tagged with
   the port's 1-based talk group channel, for the browser to decode,
   play, and attribute to the right talk group.

[`halfDuplexDecayLoop`](receive.go) runs alongside the receive loops on a
100 ms ticker (`halfDuplexDecayInterval`). It walks every send-enabled
port's `RxGate.Active()` and clears `rt.RemoteRxActive` when no gate is
within its window — so the PTT TX path can read the half-duplex flag in
O(1) via `isReceivingRemote(rt)`.

The `PortChannel` carries per-port `atomic.Int64` counters
(`RxPkts`, `RxLoopback`, `RxParseErrs`, `RxPushed`, `RxPushRejected`,
`PlaybackUnderruns`, `WebPoppedSkipped`) used both by tests and by the
periodic instrumentation snapshot — see [snapshot.go](snapshot.go).

---

## Multicast UDP

For each `McastPortConfig` entry, [`buildSinglePortChannel`](network.go)
opens:

- **RTP sender** — `net.DialUDP("udp4", localIP, mcastAddr:port)`. Dialling
  from the interface IP guarantees outbound multicast egresses the chosen
  interface. Wrapped in `*rtp.SwappableSender`. Multicast TTL =
  `rtpMulticastTTL` (currently **32**) — generous enough for any realistic
  mesh diameter through the full batman-adv + VXLAN + Tailscale path,
  where each hop and bridge decrements TTL. The prior value of 1
  silently black-holed voice on multi-hop deployments.
- **RTCP sender** — same address with `port+1`. Standard RTP port-pairing.
  Wrapped in a separate `*rtp.SwappableSender` and configured with the
  same multicast TTL.
- **RTP receiver** — `net.ListenConfig{Control: SO_REUSEPORT} →
  ListenPacket("udp4", "0.0.0.0:port")`, then
  [`device.JoinMulticastGroup`](device/network.go). `SO_REUSEPORT` lets a
  replacement socket bind to the same port while the current receiver is
  still open (so swap plumbing can acquire the new socket before closing
  the old). Wrapped in `*rtp.SwappableReceiver`. The socket requests
  `SO_RCVBUF = 1 MiB` (`rxSocketBufBytes` in [network.go](network.go))
  and logs the actual granted value plus the current per-socket kernel
  drop counter from `/proc/net/udp` at Debug level — Linux clamps
  `SO_RCVBUF` at `net.core.rmem_max`, so an undersized sysctl is
  observable in the startup log without external tools.

Loopback suppression:

- If `loopback` is `false`, packets from any loopback address or from the
  local interface IP are silently dropped. The receive loop caches the
  parsed `net.IP` so the comparison is allocation-free per packet.

Trace logging:

- When `trace` is `true`, each incoming RTP packet is logged with source
  address, sequence number, timestamp, SSRC, and payload size.

### Runtime endpoint changes

Per-port direction toggling at runtime is exposed via the public Service
API:

```go
svc := comms.Default()
_ = svc.EnableTalkGroupSend(idx, true)
_ = svc.EnableTalkGroupReceive(idx, false)
states, _ := svc.TalkGroupStates()
```

The `SendEnabled` / `ReceiveEnabled` atomics on each `PortChannel` are
checked by `sendToAllPorts`, `receiveLoop`, and `halfDuplexDecayLoop`,
so direction changes take effect on the next packet without restarting
any goroutine or socket.

The lower-level endpoint-swap plumbing
([`network.replaceNetwork`](network.go) +
[`rtp.SwappableSender.SwapAndDeferClose`](rtp/transport.go)) still
exists for future use. The previous public `UpdateMulticastEndpoint`
helper was removed during the refactor; if it returns, the
`SwapCloseGrace` window in the swap path means callers do not have to
synchronize against in-flight `Write` calls.

---

## RTP / RTCP stack

The comms package uses [pion](https://github.com/pion) for all RTP/RTCP work
through the [`rtp`](rtp/) sub-package:

- [`rtp.Session`](rtp/session.go) wraps a pion `Packetizer` and an
  interceptor chain. One session per `PortChannel` (one local SSRC).
- The interceptor chain registers `report.SenderInterceptor` (interval:
  5 s) which generates outbound RTCP Sender Reports that give receivers
  clock reference and packet count.
- Inbound RTCP is **not** processed: in a multicast PTT topology there is
  no single feedback path and each transmission may have many simultaneous
  receivers.
- Inbound RTP packets are parsed by `rtp.ParseIncoming` (a thin wrapper
  around `pionrtp.Packet.Unmarshal`).

RTP encapsulation details:

```
0               1               2               3
0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|V=2|P|X| CC=0 |M|  PT=111      |       sequence number         |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                         timestamp                             |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                            SSRC                               |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                       Opus payload…                           |
```

- **Payload type**: `rtp.PayloadTypeOpus = 111` (standard dynamic PT for Opus)
- **Clock rate**: `48 000 Hz`
- **MTU**: `rtp.MTU = 1400` bytes
- **Frame samples**: `rtp.FrameSamples = 960` (20 ms)
- **SSRC**: `rtp.SSRCFromID(RtpID)` (FNV-1a 32-bit hash; falls back to
  hostname, then local IP)

### RTP ID and SSRC

`comms.rtpId` controls SSRC derivation:

1. Uses `comms.rtpId` if set.
2. Otherwise uses the system hostname (filled in by `applyDefaults`).
3. If neither is available, falls back to the local interface IP.

SSRC is computed as the **FNV-1a 32-bit hash** of the chosen string via
`rtp.SSRCFromID`.

---

## RTP jitter buffer

[`rtp.JitterBuffer`](rtp/jitter.go) is a sequence-number-ordered buffer
backed by a **fixed-size ring of slots** (no map allocations on the hot
path). It smooths network reordering and provides Packet Loss Concealment.

- **Prebuffer**: `rtp.PrebufferPackets = 5` frames before playout begins
  (≈100 ms safety margin).
- **Max depth**: `rtp.MaxDepth = 24`. Newly arriving packets that find a
  full buffer increment `Overflows` and are dropped.
- **Slot indexing**: `seq % maxDepth` — duplicates and stale slots are
  detected without iterating the buffer.
- **Payload pool**: a per-buffer `sync.Pool` of `[]byte` sized to
  `MaxOpusPayloadSize = 1275` (RFC 6716 §3.2.1 maximum) eliminates
  per-packet heap allocations. Consumers **must** call
  `jitter.ReleasePayload(p)` after `DecodeS16` finishes with the buffer.
- **SSRC tracking**: `PushWithSSRC(ssrc, seq, payload, onChange)` resets
  the buffer cleanly when a new talker arrives, calling the change
  callback for logging. Without this, a new talker whose starting
  sequence number happens to lie in the "past half" of the previous
  talker's frozen cursor would be silently rejected forever.
- **Idle reset**: a 2 s gap (`jitterIdleResetThreshold`) since the last
  push triggers a fresh-stream reset on the next packet, catching edge
  cases the SSRC check cannot (sender resets seq without rotating SSRC,
  RFC 3550 §8.2 collision-driven SSRC rotation, …).
- **Gap detection**: `PopOrConceal(window)` returns `(payload, false)`
  for an in-order frame, `(nil, true)` for a recent-stream gap that
  warrants PLC, or `(nil, false)` for genuine silence. The PLC concealment
  cap `maxConsecutivePLC = 10` (≈200 ms) lives in [receive.go](receive.go).
- **Edge-triggered notify**: `EnableNotify()` returns a coalesced
  wakeup channel for push-driven consumers (web mode); malgo-driven
  consumers do not call it and pay only a nil-check on the push hot path.
- **Gap-run histogram** (`atomic.Int64`): `GapRuns1`, `GapRuns2to5`,
  `GapRuns6to10`, `GapRuns11to20`, `GapRuns21to50`, `GapRunsOver50`.
  Bucket counts of consecutive missing frames; consumed by `FECAdapter`
  to compute the loss EWMA (see [Adaptive FEC](#adaptive-fec) below).
- **Counters** (`atomic.Int64`): `Overflows`, `SSRCResets`, `IdleResets`.

Sequence numbers use **uint16 wrap-around-aware** comparison (`seqLess`)
so streams that cross the 65 535 → 0 boundary are handled correctly.

The playout clock for hardware mode is the per-port malgo playback
callback — calling `playoutOneFrame` once per audio period (20 ms). One
frame is produced per call regardless of whether a real payload or a
PLC frame is used.

---

## Adaptive FEC

[`FECAdapter`](fec_adapter.go) is a damped control loop that observes the
receive-side jitter-buffer gap-run histogram and adjusts the Opus encoder's
packet-loss-perc level in response. It runs as a single goroutine per
comms runtime, spawned during `CommsConfig.Run`, and exits on `ctx.Done()`.

State machine: the controller moves through three discrete levels
(`fecLevel20 = 20`, `fecLevel30 = 30`, `fecLevel40 = 40`), clamped at the
configured floor (`comms.packetLossPerc`). The configured value is the
**lower bound only**; the adapter is free to raise above it but never
drops below.

- **Tick cadence**: `fecTickInterval = 2 s`. On each tick the adapter
  reads each port's `RxPushed` and gap-run buckets, computes deltas
  against the previous tick, and converts the bucket midpoints
  (`gapBucketMidpoints = [1, 3, 8, 15, 35, 75]`) into an estimated count
  of missing frames.
- **EWMA**: `loss_ewma = 0.2 × raw + 0.8 × prev` (`fecEWMAAlpha = 0.2`).
  At 2 s ticks the 63 % response time is roughly 10 s — long enough to
  ride out a single noisy window, short enough to catch sustained loss.
- **Upgrade thresholds** (loss ratio): 20→30 at `0.08`, 30→40 at `0.20`.
  Requires `fecUpgradeDwell = 2` consecutive ticks above the threshold
  before transitioning.
- **Downgrade thresholds**: 40→30 at `0.10`, 30→20 at `0.03`. Requires
  `fecDowngradeDwell = 15` consecutive ticks below — much longer than
  upgrade dwell so the adapter does not flap on a brief recovery window.
- **Idle stall reset**: after `fecSilentStallLimit = 30` ticks (≈ 60 s)
  with zero pushed and zero gap activity the EWMA is reset to zero. This
  prevents a stale loss estimate from carrying across a long inter-PTT
  gap.

Snapshot fields (`FECAdapterSnapshot`, exposed under `comms.fec_adapter`):
`current_level`, `loss_ewma`, `last_change_unix_nano`, `transitions`,
`write_errors`, `floor`. See
[docs/instrumentation-snapshot.md](../../docs/instrumentation-snapshot.md)
for interpretation heuristics.

The design assumes link symmetry — every node reads its own RX loss and
applies the result to its own TX encoder. This is sound for the
omnidirectional-antenna deployment but would need rethinking for
asymmetric links.

---

## PTT control handling

### `openvlm` backend (default)

The OpenVLM (Open Voice Link Module) is a USB HID audio dongle widely used as a
push-to-talk controller. [`control.OpenVLMSource`](control/openvlm.go) reads
HID input reports and decodes:

| Source bits | Transition | Event emitted |
|---|---|---|
| IR1 bit 2 (GPIO3) LOW → HIGH | Button pressed | `PTTDown` (PTT channel) |
| IR1 bit 2 (GPIO3) HIGH → LOW | Button released | `PTTUp` (PTT channel) |
| IR0 bit 0 (`OpenVLMVolUpMask`) edge | VOL+ press / release | `VolumeUpPressed` / `VolumeUpReleased` (aux channel) |
| IR0 bit 1 (`OpenVLMVolDnMask`) edge | VOL− press / release | `VolumeDownPressed` / `VolumeDownReleased` (aux channel) |

The HID report structure:

```
Byte 0: Report ID (prepended by OS — shifted by 1 when n ≥ 5)
Byte 1: IR0 (GPIO8–GPIO5, plus VOL+/VOL− on bits 0/1)
Byte 2: IR1 (GPIO4–GPIO1) ← bit 2 = GPIO3 = PTT, bit 0 = GPIO1 = OpenVLM strap
Byte 3: IR2
Byte 4: IR3
```

Aux events flow on a separate buffered channel
([`control.AuxEventSource`](control/aux_event.go)) with non-blocking sends —
a missing or slow consumer cannot stall the HID read loop. The comms run
loop dispatches received aux events to `CommsConfig.AuxHandler` (typically
`alsa.Controller`; see [Volume control](#volume-control-via-alsa) below).

### OpenVLM identification and ALSA card detection

The `0x0D8C:0x0012` VID/PID is shared with generic CM108 USB audio dongles.
To distinguish a real OpenVLM from a generic dongle that happens to be
plugged in alongside one, the unified discovery scan in
[`device.DiscoverCM108`](device/cm108.go) walks `/sys/bus/usb/devices/`
and produces `CM108Descriptor` entries with the device's HID path,
ALSA card index, USB serial, and SysPath.
[`device.CheckOpenVLMIdentity`](device/openvlm_identity.go) then issues a
HIDIOCGINPUT ioctl and inspects **GPIO1** (bit 0 of HID_IR1): OpenVLM
hardware wires GPIO1 high via a board strap, generic CM108 dongles leave
it low. The CM108B datasheet (§7.4) requires `IR0[7:6] == 0` for IR1[3:0]
to reflect live GPIO state; the helper checks this and returns an error
otherwise.

`OpenVLMSource.Events` runs the GPIO1 probe at startup and prefers a
descriptor whose `IsOpenVLM` is set when opening the HID device, falling
back to "any matching VID/PID" if no positively-identified unit is found.

ALSA card auto-detection runs before the malgo context is initialized when
`controlSource` is `openvlm` or `roip`. It uses the same `DiscoverCM108`
walk to map the OpenVLM to an ALSA card index and sets `ALSA_CARD` so
malgo selects the correct sound card. If `ALSA_CARD` is already set, it
is left unchanged. A fallback path scans `/proc/asound/card*/usbid` for
`0d8c:0012` when sysfs discovery fails.

### Audio init retry and in-run recovery

Hardware audio init is retried up to 3 times at startup (750 ms apart,
context-aware) to absorb transient ALSA/USB failures while the OpenVLM
dongle settles after boot-time enumeration (observed as `miniaudio:
Broken pipe` from the dmix slave start). If all startup attempts fail,
comms stays up (RTP relay and WebUI toggles keep working) and the Run
loop re-attempts init every 10 seconds — including re-running ALSA card
detection when `ALSA_CARD` is still unset — so plugging the dongle in
later brings local mic/speaker up without a daemon restart. Both log
paths carry an `alsa_card` field naming the card index the `default`
ALSA PCM resolves to.

`PTTDown` → `beginTransmission`; `PTTUp` → `endTransmission`.

### `roip` backend

[`control.ROIPSource`](control/roip.go) uses the **same** OpenVLM USB
audio dongle but operates without a manual PTT button — it
automatically bridges an analog handheld radio into the multicast comms
network. Detection strategy (half-duplex enforced throughout):

1. **COS** (Carrier-Operated Squelch): the radio squelch output is
   wired to an OpenVLM GPIO pin. The HID report is polled;
   `ROIPCOSGPIOMask` selects the IR1 bit. PTTDown on the HIGH→LOW
   squelch edge, PTTUp on LOW→HIGH.
2. **VOX fallback**: if the HID device is unavailable or
   `ROIPCOSGPIOMask` is 0, an audio energy threshold
   (`audiopool.RMSEnergy`) is applied to the always-on broadcast capture
   stream's VOX tap. `ROIPVOXOnsetFrames = 3` (60 ms) prevents false
   triggers. The tap pointer is published on `rt.BroadcastTap`
   (`atomic.Pointer[chan []float32]`) so the capture callback can
   forward float32 frames whenever a tap is registered, regardless of
   the TX gate.

`ROIPMaxTXDuration` caps a single transmission as a safety ceiling.
The half-duplex callbacks (`isReceiving` / `isBroadcasting`) and the
broadcast tap setters (`setTap` / `clearTap`) are passed in as plain
function values from [`control_register.go`](control_register.go) so
the `control` package never imports the parent.

### `web` backend

[`control.WebEventSource`](control/web_event_source.go) is a lightweight
buffered channel backend whose `Push(ev PTTEvent)` method is called from
the RPC handler when a browser client presses or releases the on-screen
PTT button. `Service.WebEventSource()` returns the live instance for
the handler to inject events into.

In web mode the entire malgo pipeline is **bypassed**:

- `rt.BroadcastStream` is left nil; `beginTransmission` and
  `endTransmission` short-circuit on `rt.WebBridge != nil`.
- Per-port playback streams are not opened.
- Inbound audio is forwarded raw via [`webaudio.Bridge.PushRxFrame`](webaudio/bridge.go)
  (tagged with the port's talk group channel) → `RxFrames()` channel →
  RPC stream (`StreamAudioRxResponse.talkgroup`) → browser decoder.
- Outbound audio is injected via `webaudio.Bridge.InjectTxFrame`
  → bound `SendFn` → `cfg.sendToAllPorts(rt, payload)`.

This makes web mode usable on hardware without a sound card — the malgo
context never gets initialized, so device-open failures cannot stop the
daemon from starting.

### `nanoptt` backend

[`control.NanoPTTSource`](control/nanoptt.go) reads from a Linux evdev
input device matched by `NanoPTTDeviceName` within the
`NanoPTTDevicePath` glob (handled by
[`device.FindEvdev`](device/evdev.go)).

- If `commKey` is `any`, any key press emits `PTTToggle`.
- Otherwise the key code must match the decimal `EV_KEY` code.

On each matching **key press** (`EV_KEY` value = 1), a `PTTToggle`
event is emitted. The `Run` loop checks current `Broadcasting` state
and calls `beginTransmission` or `endTransmission` accordingly — a
**press-to-toggle** model. Key releases are logged at debug level but
produce no event.

### Transmission lifecycle

`beginTransmission` (in [transmit.go](transmit.go)):

1. Returns immediately if `rt.Broadcasting` is already true.
2. Returns immediately if `cfg.isReceivingRemote(rt)` is true
   (half-duplex; reads `rt.RemoteRxActive` in O(1)).
3. `rt.Broadcasting.Store(true)`.
4. Web mode short-circuit: if `rt.WebBridge != nil`, log "Begin web
   transmission" and return.
5. `drainPlaybackBuffer(rt)` — discards stale beep frames in every port.
6. Queues the 1 000 Hz start-tone (`rt.BeepBufferStart`, `[]int16`,
   amplitude `0.2 * 32767`) into every port's `PlaybackBuffer`.
7. Sleeps `cfg.transmitSettleWait(rt)` — the greater of
   `cfg.pttStartDelay()` (default 50 ms; configurable via
   `PttStartDelayMs`; set negative to skip entirely) and
   `rt.PlaybackOutputLatency + 20 ms beep + beepSettleMargin` (40 ms).
   The second term is required so the start tone has fully emerged from
   the speaker before the TX gate opens; without it any acoustic or
   device sidetone path from speaker → mic captures the beep and the
   remote side hears it on the next transmission. The first term covers
   USB audio class capture devices that need extra time to commit their
   first DMA cycle. `PlaybackOutputLatency` is computed in
   `audio.Init.BuildAudio` from the rounded-up effective period
   (`nextPow2(playbackPeriod)`) multiplied by `malgoLowLatencyPeriods`
   (3) — modeling miniaudio's LowLatency profile so the wait covers the
   full ALSA ring, not just one period.
8. Verifies `rt.BroadcastStream` is non-nil (the always-on capture
   stream opened at StartHardware); if it is unexpectedly nil, logs an
   Error, clears `Broadcasting`, and returns.
9. Calls `rt.BroadcastStream.SetTxEnabled(true)` to open the TX gate.
   The capture device itself is already running; the gate just lets
   captured frames reach the Opus encoder.

`endTransmission`:

1. Returns immediately if `rt.Broadcasting` is already false.
2. Web mode short-circuit: clear `Broadcasting`, log, return.
3. Calls `rt.BroadcastStream.SetTxEnabled(false)` to close the TX gate.
   The capture device keeps running so the VOX tap (if any) keeps
   observing the mic. The `SetTxEnabled(false)` call also emits the
   per-cycle stats Debug log.
4. `drainPlaybackBuffer(rt)`.
5. Queues the 600 Hz stop-tone into every port's `PlaybackBuffer`.
6. `rt.Broadcasting.Store(false)`.

---

## Volume control via ALSA

[`control/alsa`](control/alsa/) is a pure-Go ALSA mixer binding
(`github.com/gen2brain/alsa`) shared by two independent consumers: the
OpenVLM's physical VOL+/VOL− buttons, and the daemon's own hardware
mixer RPCs. No CGO is required, the package cross-compiles cleanly to
`linux/amd64`, `linux/arm64`, and `linux/mipsle`, and it does not depend
on alsa-utils being installed on the target.

### `Controller` — the VOL+/VOL− button handler

[`control/alsa.Controller`](control/alsa/controller.go) is the
`AuxEventHandler` wired into `CommsConfig.AuxHandler` by the manager. The
dispatch loop (`runAuxPump` in [transmit.go](transmit.go)) forwards each
aux event from the control source's `AuxEvents()` channel directly to the
handler's `Handle(ctx, ev)`, invoked synchronously on the aux pump
goroutine — long-running work inside `Handle` would block the pump, but
the controller's mixer transactions complete in microseconds. `Handle`
adjusts a mixer control's raw value by ±`Controller.Step` (default 1) on
`VolumeUpPressed` / `VolumeDownPressed` and ignores release events, so
holding a button does not auto-repeat. If `Controller.ControlName` is
left empty the controller resolves the control by trying the playback
candidate list in order (see below) instead of hard-coding `Master`,
which is also what fixes button presses on cards that expose the same
control under a different raw element name. On a CM108B Master control
(38 raw steps from −37 dB to 0 dB) one raw step is approximately one dB;
this approximation does not generalize to all cards. Every ALSA failure
along the way — mixer open, control resolution, range query, value
read/write — is logged at Warn or Debug and swallowed rather than
propagated: the volume button must never crash the daemon.

### `Volume` — absolute get/set for the mixer RPCs

[`control/alsa.Volume`](control/alsa/volume.go) is the counterpart used
by the `CommsService.GetAudioMixer` / `UpdateAudioMixer` RPCs and by the
startup mixer re-apply. Where `Controller` nudges a raw value by a
relative step and swallows every error, `Volume` reads and writes
absolute percentages (0–100, mapped onto each control's own
`RangeMin`/`RangeMax`) and returns typed errors — `alsa.ErrNoCard` when
no ALSA card is available, `alsa.ErrControlNotFound` when none of a
role's candidate names resolve — so the RPC layer can map failures to
the right response code instead of guessing from a log line. `State`
reads the current speaker volume, mic volume, and AGC switch in one
mixer session; controls that are simply absent from the card report
`-1` (or `AGCPresent: false`) rather than an error. `Apply` writes the
non-nil fields of an `Update` and then re-reads `State` so the RPC
response always reflects what actually landed on hardware. A single
`*alsa.Volume` is shared across the comms manager, the API server, and
the instrumentation registry so all three observe the same
last-known-good reading.

### Candidate-list resolution

`Volume` resolves a logical role (speaker volume, mic volume, AGC
switch, and — startup-unmute only — the playback/capture mute switches)
against an ordered list of raw ALSA element names via the shared
[`ResolveCtl`](control/alsa/resolve.go) helper, which returns the first
exact match; `Controller` uses the same helper but only ever resolves
the playback role, since VOL+/VOL− only ever touches speaker volume.
`gen2brain/alsa` matches raw kernel element names exactly, which differ
from `amixer`'s simple names, so both spellings are listed where cards
disagree:

- `PlaybackVolumeNames`: `Master`, `Speaker Playback Volume`,
  `PCM Playback Volume`, `Headphone Playback Volume`
- `CaptureVolumeNames`: `Mic Capture Volume`, `Capture Volume`, `Mic`
- `AGCNames`: `Auto Gain Control`
- `PlaybackSwitchNames`: `Master Playback Switch`,
  `Speaker Playback Switch`, `PCM Playback Switch`
- `CaptureSwitchNames`: `Mic Capture Switch`, `Capture Switch`

`Master` stays first on the playback list so the deployed VOL+/VOL−
button behavior is unchanged on cards where it exists. An operator can
pin an exact raw element name per role instead of trusting the
candidate list via `comms.audio.speakerControl`, `comms.audio.micControl`,
and `comms.audio.agcControl` — each becomes the sole entry in that
role's list when set (`alsa.NamesFromOverrides`), which also lets a
future card with an unlisted name work without a code change. **These
overrides reach only the `Volume` path** — the `GetAudioMixer`/
`UpdateAudioMixer` RPCs and the startup re-apply. The VOL+/VOL− button
path always resolves against the built-in `PlaybackVolumeNames` list:
`Controller.ControlName` exists as a code-level override field, but
production wiring (`CommsManager.buildCommsConfig`) never sets it from
config, so pinning `comms.audio.speakerControl` has no effect on button
behavior — only on what `GetAudioMixer`/`UpdateAudioMixer` read and
write. The switch-name lists have no config override at all; they only
matter for the defensive startup unmute described below. The three
override keys are read once when `alsa.Volume.Names` is built in
`Start()` — changing `comms.audio.speakerControl`, `comms.audio.micControl`,
or `comms.audio.agcControl` requires a daemon restart to take effect; a
comms disable/re-enable cycle alone will not pick up the new value.

### Persistence semantics

`UpdateAudioMixer` writes to hardware first and only persists to
`comms.audio.speakerVolume` / `comms.audio.micVolume` / `comms.audio.agc`
once the hardware write has succeeded, so a failed persist can never
leave the config file claiming a level the card rejected. VOL+/VOL−
button presses handled by `Controller` are deliberately **not**
persisted — they are momentary hardware nudges, not configuration
changes. The practical effect: `comms.audio.*` is the boot baseline
re-applied on startup, while `GetAudioMixer` always reports whatever the
hardware currently holds, which may have drifted from that baseline via
button presses or an out-of-band `alsamixer` session.

One consequence worth flagging for future gain-staging work (2026-08-30
bench): every daemon start and audio recovery writes "Mic Capture
Volume" — the 100% policy default when `comms.audio.micVolume` is
unset, the persisted value once a UI slider has touched it — so the
OpenVLM EEPROM's ADC boot value (`adc-init-volume`) never survives past
comms startup. Any future scheme that tunes capture gain through the
EEPROM must change this policy default, or the daemon will clamp it
back on every boot.

### Startup behavior

The manager wires `CommsConfig.AudioMixerStartup` to re-apply the
persisted `comms.audio.*` values via `Volume.ApplyStartup`. All three
fields are policy rather than passthrough — applied on every startup.
Speaker and mic volume default to **100%** when unset, so hardware
levels are deterministic regardless of EEPROM vintage or prior
alsamixer state: for the speaker this closes the fleet split where
units provisioned with OpenVLM ≤ 1.0.2 boot at −10 dB from the EEPROM
while ≥ 1.0.3 units boot at 0 dB (the DAC maxes out at 0 dB, so 100%
cannot over-drive); for the mic, 100% maps to the CM108B ADC's +23 dB
maximum, overriding the EEPROM's +8 dB boot value by design. AGC
defaults to **disabled** when `comms.audio.agc` is unset, so the
CM108B's automatic capture gain never rides along silently on a fresh
install or after a USB replug resets the chip. The re-apply runs once
after `control.DetectAndSetALSACard` in `Start()`, and again after every
successful in-run audio recovery (a USB replug resets the card's mixer
state). `ApplyStartup` applies the speaker, mic, and AGC fields as three
independent `Apply` calls rather than one combined write, so a missing
control for one field cannot block the others from landing. It then
forces every resolvable playback/capture switch control on — with no
mute exposed anywhere in the API, this is the only recovery from an
out-of-band `alsamixer` mute — and finally logs the card's full mixer
control enumeration at Debug, which is the field diagnostic for
matching an unfamiliar card's raw element names against the candidate
lists above. Every step swallows and logs its own errors; a mixer
failure at startup must never block audio.

### AGC and manual mic volume

When `comms.audio.agc` is enabled, the CM108B's own Auto Gain Control
adjusts capture gain continuously, so a manual `micVolume` change made
while AGC is on may appear to have little or no audible effect — the
hardware is actively working against it. This is expected behavior, not
a bug in `Volume.Apply`.

---

## Instrumentation snapshots

Every counter mentioned above also surfaces in the periodic JSON snapshot
the daemon writes when `instrumentation.enable: true`. The comms section
is emitted by [`CommsSnapshotter`](snapshot.go) under `comms` and contains:

- `enabled`, `broadcasting`, `remote_rx_active`, `control_source`
- `ports[]` — per-talk-group with `rx_pkts`, `rx_loopback`,
  `rx_parse_errs`, `rx_pushed`, `rx_push_rejected`, `playback_underruns`,
  `web_popped_skipped`, `send_enabled`, `receive_enabled`, plus nested
  `jitter` and `rx_gate` snapshots.
- `broadcast_encoder` — `audio.AudioEncoderSnapshot` (frames captured /
  encoded / dropped, encode-duration histogram, capture gap stats, TX
  gate state).
- `web_bridge` — `webaudio.BridgeSnapshot` (RX frame queue depth, dropped
  frames, web TX inject counts).
- `fec_adapter` — `FECAdapterSnapshot` (current level, loss EWMA, last
  change time, transitions, write errors, floor).

Field semantics, unit annotations, and triage heuristics live in
[docs/instrumentation-snapshot.md](../../docs/instrumentation-snapshot.md).
That document MUST be updated in the same changeset as any addition,
removal, or rename of a snapshot field — the framework's whole point is
that operators and LLMs can read the JSON without reading Go source.

---

## ALSA log filtering

The `logProc` callback passed to `malgo.InitContext` in
[`audio.Init.StartHardware`](audio/init.go) drops `poll() failed` and
`EPIPE` lines emitted by miniaudio during USB audio class startup and
under normal scheduling jitter — both are recovered internally via
`snd_pcm_recover` and do not correspond to lost audio. Anything else from
miniaudio still lands at Trace level prefixed with `source=malgo`.

The full build requires libasound for the rest of the malgo ALSA backend;
the `omd_omit_comms` lite build skips the package entirely.

---

## Config keys

The minimal `example_config.yml` shipped with the repo is intentionally
terse — most operators rely on the in-code defaults. The full set of
`comms.*` keys recognised by [`internal/config`](../config/config.go) is:

```yaml
comms:
  enable: false
  controlSource: openvlm     # openvlm | roip | web | nanoptt
  debug: false
  trace: false
  loopback: true
  micGain: 2.0               # >1 amplifies, <1 attenuates; applied in
                             # Q8 fixed point (1/256 resolution) with a
                             # soft-knee limiter above 0.75 full scale.
                             # Default 2.0 is the 2026-08-30 bench
                             # residual for the OpenVLM at its shipped
                             # +20 dB analog gain
  encoderComplexity: 5       # 1..10 (defaults to 5)
  packetLossPerc: 20         # initial Opus FEC level; clamped [10,40].
                             # Used as the FEC adapter's lower bound; the
                             # adapter ramps up to 30 / 40 under loss.
  playbackLatencyMs: 60      # malgo playback period hint (ms);
                             # translated to PeriodSizeInFrames — default
                             # 20 ms when ≤ 0
  captureLatencyMs: 60       # malgo capture period hint (ms); translated
                             # to the ALSA periods count (ring depth)
                             # via buildCapturePeriods (clamped [3,16])
  captureFramesPerBuffer: 0  # malgo capture period frames; 0 → 960 (one
                             # 20 ms Opus frame), <0 → let miniaudio pick

  nanoPTT:
    enable: false
    devicePath: /dev/input/event*    # glob for evdev device enumeration
    deviceName: AllInOneCable        # exact evdev device name to match

  bluetoothPtt:
    enable: false
    BluetoothAudioDeviceHint: ""     # optional shared substring (e.g. "OpenVLM")
    BluetoothInputDevice: ""         # capture device substring or index
    BluetoothOutputDevice: ""        # playback device substring or index
```

The interface comes from the global `meshNet.interface` key (read by
`CommsManager.buildCommsConfig` via `cfg.GetMeshNetInterface()`), and
multicast talk-group entries (`McastPorts`) are sourced from the global
`config.GetMulticastTalkGroups()` helper rather than living under the
`comms:` namespace, so a single config defines them once for all
subsystems that need them.

`nanoPTT.*` keys are only relevant when `controlSource: nanoptt`. The
`bluetoothPtt.*` keys carry the audio device names used by both
`openvlm` and `roip` modes (the historical name predates the wider
audio refactor). ALSA card auto-detection runs for both `openvlm` and
`roip`. `CommsConfig.AuxHandler` is wired by the manager to a fresh
`alsa.Controller` so VOL+/VOL− on the OpenVLM updates the system mixer.

> **Configurable Go fields without yaml keys** (yet): `CommKey`,
> `RtpID`, `HalfDuplexThreshold`, `PttStartDelayMs`, and the entire
> `ROIP*` group are present on `CommsConfig` but not currently loaded
> by `CommsManager.buildCommsConfig`. They use their compile-time
> defaults today; wire them through `internal/config/config.go` if you
> need them externally tunable.

---

## Source files

### Top-level orchestration

| File | Responsibility |
|---|---|
| [comms.go](comms.go) | `buildCodec`, `sendToAllPorts`, `buildEventSource`, `TargetBitrate`, package-level constants |
| [config.go](config.go) | `CommsConfig`, `CommsRuntime`, `BroadcastCapture` interface, `NewComms`, `applyDefaults` |
| [lifecycle.go](lifecycle.go) | `Start`, `startHardwareAudio` (`audio.Init` wiring) |
| [manager.go](manager.go) | `CommsManager` (`Enable`/`Disable`/`IsRunning`); wires `alsa.Controller` as the default `AuxHandler` |
| [service.go](service.go) | `Service` + `Default()`/`SetDefault()` singleton; per-handler accessors |
| [transmit.go](transmit.go) | `Run`, `beginTransmission`, `endTransmission`, `drainPlaybackBuffer`, `pttStartDelay`, `transmitSettleWait`, `runAuxPump` |
| [receive.go](receive.go) | `receiveLoop`, `playoutOneFrame`, `webPlayoutLoop`, `halfDuplexDecayLoop`, `isReceivingRemote` |
| [network.go](network.go) | `buildNetwork`, `buildSinglePortChannel`, `replaceNetwork`, `listenRTPReceiver`, multicast TTL + `SO_RCVBUF` setup |
| [port_channel.go](port_channel.go) | `McastPortConfig`, `McastPortState`, `PortChannel`, `MarkRemoteRx`, `closePartial` |
| [fec_adapter.go](fec_adapter.go) | `FECAdapter`, `FECAdapterSnapshot`, EWMA + state machine |
| [snapshot.go](snapshot.go) | `CommsSnapshot`, `PortSnapshot`, `CommsSnapshotter` (instrumentation registry adapter) |
| [control_register.go](control_register.go) | `init()` registering the four backends; `buildControlDeps`; `Validate` |
| [device.go](device.go) | `normalizeControlSource`, `findCommDevice` |
| [doc.go](doc.go) | Package doc + `omd_omit_comms` build stub |

### Sub-packages

| Path | Responsibility |
|---|---|
| [audio/](audio/) | `BroadcastEncoder` (always-on malgo capture + dedicated encode goroutine + TX gate), `Init` (hardware startup), `PortSlot`, `Deps`, `SendFn`, `AudioEncoderSnapshot` |
| [audiopool/](audiopool/) | Audio constants (`FrameSize`, `SampleRate`, `Channels`, `EncBufSize`), buffer pools (`Float32Pool`, `Int16Pool`, `EncBufPool`), `RMSEnergy` |
| [codec/](codec/) | `AudioEncoder`/`AudioDecoder` interfaces, Opus implementation (`NewOpusEncoder`/`NewOpusDecoder`/`EncodeS16`/`DecodeS16`/`DecodeFloat32`/`SetPacketLossPerc`) |
| [control/](control/) | `EventSource`, `PTTEvent`, four backends (`OpenVLMSource`, `ROIPSource`, `NanoPTTSource`, `WebEventSource`), `AuxEvent`/`AuxEventSource`/`AuxEventHandler`, `HalfDuplexGate`, registry (`Register`/`Lookup`/`Factory`/`ControlDeps`), `HIDDevice`/`HIDOpener`, `DetectAndSetALSACard` |
| [control/alsa/](control/alsa/) | `Controller` — pure-Go ALSA mixer `AuxEventHandler` for VOL+/VOL− on the OpenVLM; `Volume` — absolute State/Apply used by `GetAudioMixer`/`UpdateAudioMixer` and the startup mixer re-apply; shared candidate-list resolution (`ResolveCtl`) |
| [device/](device/) | `AudioStream` interface + `NewMalgoStream`, `DiscoverCM108` (unified sysfs walk) + `CM108Descriptor`, `CheckOpenVLMIdentity` (HID GPIO1 strap probe), `Cache`, `FindEvdev`, `IfaceIPv4`/`JoinMulticastGroup`, `ResolveAudio`/`LogAudioDevices` |
| [rtp/](rtp/) | `Session` (pion Packetizer + RTCP SR), `Sender`, `JitterBuffer` (ring buffer + SSRC tracking + `EnableNotify` + gap-run histogram), `JitterBufferSnapshot`, `PacketWriter`/`PacketReader`, `SwappableSender` (lock-free + `SwapAndDeferClose`)/`SwappableReceiver`, `SSRCFromID`, `ParseIncoming` |
| [webaudio/](webaudio/) | `Bridge` (RPC ↔ comms runtime plumbing for web mode), `NewBridge`, `SendFn`, `InjectTxFrame`, `PushRxFrame`, `RxFrames`, `BridgeSnapshot` |
