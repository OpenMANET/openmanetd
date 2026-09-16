package comms

import (
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"

	"github.com/openmanet/openmanetd/internal/comms/announce"
	"github.com/openmanet/openmanetd/internal/comms/codec"
	"github.com/openmanet/openmanetd/internal/comms/control"
	"github.com/openmanet/openmanetd/internal/comms/device"
	"github.com/openmanet/openmanetd/internal/comms/gpio"
	"github.com/openmanet/openmanetd/internal/comms/talkgroup"
	"github.com/openmanet/openmanetd/internal/comms/webaudio"
	"github.com/openmanet/openmanetd/internal/config"
)

// ─── CommsRuntime ─────────────────────────────────────────────────────────────

// BroadcastCapture is the unified capture stream interface the runtime
// exposes to the TX path. It extends device.AudioStream (lifecycle
// Start/Stop/Close, called once per StartHardware cycle) with a per-PTT
// SetTxEnabled gate. Captured frames always flow to the optional VOX tap;
// they only flow to the Opus encoder + RTP send when SetTxEnabled(true)
// is the most recent call. The stream is opened once at StartHardware
// and stays open for the lifetime of the comms run.
type BroadcastCapture interface {
	device.AudioStream
	SetTxEnabled(bool)
}

// CommsRuntime holds live resources allocated by Start. All audio/network
// fields are interfaces so that unit tests can inject fakes without hardware.
//
// RemoteRxActive is the cached half-duplex receive flag. The PTT TX path
// reads it via isReceivingRemote in O(1) instead of walking every port's
// HalfDuplexGate. It is set immediately by receiveLoop on every inbound
// packet from a send-enabled port (no false negatives at the start of an
// incoming stream) and cleared by halfDuplexDecayLoop on a coarse 100 ms
// ticker once every gate's window has expired.
type CommsRuntime struct { //nolint:govet // fieldalignment: mu must sit directly above the broadcastStream field it guards (.claude/rules/concurrency.md); the pointer-scan-optimal layout would separate them.
	Encoder         codec.AudioEncoder
	FECAdapter      *FECAdapter
	WebBridge       *webaudio.Bridge
	WebEvtSrc       *control.WebEventSource
	LocalIP         atomic.Pointer[string]
	BroadcastTap    atomic.Pointer[chan []float32]
	Ports           []*PortChannel
	BeepBufferStart []int16
	BeepBufferStop  []int16
	// PlaybackOutputLatency is the actual output latency the backend
	// granted when the per-port playback streams were opened. The TX
	// path uses it in beginTransmission to delay SetTxEnabled(true)
	// until the start-tone beep has fully emerged from the speaker so
	// an acoustic (or device sidetone) path from speaker → mic cannot
	// pick the beep up and transmit it.
	PlaybackOutputLatency time.Duration
	Broadcasting          atomic.Bool
	RemoteRxActive        atomic.Bool

	// ActiveChannel is the 1-based talk group most recently applied by
	// SelectTalkGroup (or seeded from the boot-time toggles). 0 until a
	// selection or seed happens. Read lock-free by status and snapshot.
	ActiveChannel atomic.Int32
	// Events fans talk group changes out to the announcer, streaming
	// RPC subscribers, and any future listeners. Allocated once in
	// Start; nil in minimal test runtimes (Notify is nil-safe).
	Events *talkgroup.Registry
	// Announcer plays talk group voice clips; nil in web mode, when clip
	// decode failed, or in minimal test runtimes.
	Announcer *announce.Player
	// GPIOSel is the hardware talk group selector; nil when the board
	// doesn't wire one, the operator disabled it, or open failed.
	GPIOSel *gpio.Selector

	// selectMu serializes SelectTalkGroup's multi-port flip so two
	// concurrent selections cannot interleave partial port states. Never
	// taken on the audio or packet hot paths.
	selectMu sync.Mutex

	// mu protects broadcastStream. It is written at startup by
	// initAudioIO/startHardwareAudio and again by the Run loop's audio
	// recovery path; it is read from the Run goroutine (transmit paths)
	// and from the instrumentation snapshot goroutine, so a plain field
	// is not enough once recovery can install it mid-run.
	mu              sync.RWMutex
	broadcastStream BroadcastCapture

	// audioCleanup is the malgo teardown produced by a successful hardware
	// audio init. Written by Start (startup path) and by the Run loop's
	// recovery path, read by Start's deferred teardown after Run returns.
	// Run executes synchronously on the Start goroutine, so all accesses
	// are sequential and no lock is needed.
	audioCleanup func()
}

// Broadcast returns the live capture stream, or nil when hardware audio is
// not (yet) up. Reads outnumber writes by orders of magnitude, hence RWMutex.
func (rt *CommsRuntime) Broadcast() BroadcastCapture {
	rt.mu.RLock()
	defer rt.mu.RUnlock()

	return rt.broadcastStream
}

// SetBroadcast installs the capture stream produced by a successful
// hardware audio init (startup or in-run recovery).
func (rt *CommsRuntime) SetBroadcast(bs BroadcastCapture) {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	rt.broadcastStream = bs
}

// ─── CommsConfig ──────────────────────────────────────────────────────────────

// CommsConfig holds the static configuration for the comms subsystem.
// Allocate one with NewComms and call Start to begin operation. All
// exported fields must be set before Start is called.
//
// CommsConfig is treated as immutable after Start: the live runtime is
// owned by *Service (returned by Start via SetDefault) so the static
// config and the per-startup runtime have distinct lifetimes.
type CommsConfig struct {
	Log        zerolog.Logger
	AuxHandler control.AuxEventHandler
	Interrupt  chan os.Signal
	// AudioMixerStartup, when non-nil, re-applies persisted hardware mixer
	// levels (speaker/mic volume), enforces the AGC policy (persisted
	// value, defaulting to disabled), and clears mute switches. Invoked
	// after ALSA card detection in Start and again after every successful
	// in-run audio recovery — a USB replug resets the card's mixer state.
	// The manager wires it whenever a hardware mixer accessor exists; the
	// closure re-reads config at every invocation, so levels first
	// persisted mid-run still reach later recoveries. Nil only when no
	// mixer is wired (tests, frontend-only mode).
	AudioMixerStartup    func()
	startHardwareAudioFn func(rt *CommsRuntime) (func(), error)
	// detectALSACardFn overrides ALSA card auto-detection for tests. When
	// nil, detectALSACard falls back to control.DetectAndSetALSACard(cfg.Log).
	detectALSACardFn func()
	// readUDPDropsFn overrides the /proc/net/udp kernel-drop scan for
	// tests. When nil, readUDPDrops falls back to readUDPSocketDrops.
	readUDPDropsFn func(localPort int) (int64, error)
	// gpioSelectorSupportedFn overrides the board capability check for
	// tests. Nil means board.GPIOSelectorSupported.
	gpioSelectorSupportedFn  func() bool
	BluetoothOutputDevice    string
	NanoPTTDevicePath        string
	CommKey                  string
	BluetoothInputDevice     string
	Iface                    string
	BluetoothAudioDeviceHint string
	ControlSource            string
	NanoPTTDeviceName        string
	RtpID                    string
	McastPorts               []McastPortConfig
	HalfDuplexThreshold      time.Duration
	audioInitRetryDelay      time.Duration
	ROIPMaxTXDuration        time.Duration
	ROIPVOXHoldTime          time.Duration
	EncoderComplexity        int
	PttStartDelayMs          int
	CaptureFramesPerBuffer   int
	CaptureLatencyMs         int
	PacketLossPerc           int
	// DSCP is applied to both sender sockets of every Send-enabled port
	// at build time (IP_TOS = DSCP<<2, SO_PRIORITY = 256 + DSCP>>3). The
	// config layer resolves absent-vs-zero before this struct is built:
	// 0 always means "marking off" here and applyDefaults must not
	// overwrite it, or the operator's `dscp: 0` kill switch would break.
	DSCP              int
	PlaybackLatencyMs int
	// audioRecoveryInterval is the Run-loop ticker period for re-attempting
	// hardware audio init after startup failed (OpenVLM unplugged at boot,
	// transient ALSA error). <= 0 disables in-run recovery; applyDefaults
	// sets the production value.
	audioRecoveryInterval time.Duration
	// webStatInterval overrides the webPlayoutLoop stat-reporting ticker
	// period for tests. <= 0 (production) uses webStatDefaultInterval.
	webStatInterval    time.Duration
	ROIPVOXThreshold   float32
	MicGain            float32
	EnableNanoPTT      bool
	EnableBluetoothPtt bool
	Enable             bool
	Trace              bool
	Loopback           bool
	Debug              bool
	GPIOSelectorEnable bool
	ROIPCOSGPIOMask    byte
}

// NewComms copies cfg and returns a pointer ready for Start.
func NewComms(cfg CommsConfig) *CommsConfig {
	mcastPorts := make([]McastPortConfig, len(cfg.McastPorts))
	copy(mcastPorts, cfg.McastPorts)

	return &CommsConfig{
		Log:                      cfg.Log,
		Interrupt:                cfg.Interrupt,
		Enable:                   cfg.Enable,
		Iface:                    cfg.Iface,
		McastPorts:               mcastPorts,
		CommKey:                  cfg.CommKey,
		RtpID:                    cfg.RtpID,
		Debug:                    cfg.Debug,
		GPIOSelectorEnable:       cfg.GPIOSelectorEnable,
		Loopback:                 cfg.Loopback,
		Trace:                    cfg.Trace,
		ControlSource:            cfg.ControlSource,
		MicGain:                  cfg.MicGain,
		EnableNanoPTT:            cfg.EnableNanoPTT,
		NanoPTTDevicePath:        cfg.NanoPTTDevicePath,
		NanoPTTDeviceName:        cfg.NanoPTTDeviceName,
		EnableBluetoothPtt:       cfg.EnableBluetoothPtt,
		BluetoothAudioDeviceHint: cfg.BluetoothAudioDeviceHint,
		BluetoothInputDevice:     cfg.BluetoothInputDevice,
		BluetoothOutputDevice:    cfg.BluetoothOutputDevice,
		ROIPCOSGPIOMask:          cfg.ROIPCOSGPIOMask,
		ROIPVOXThreshold:         cfg.ROIPVOXThreshold,
		ROIPVOXHoldTime:          cfg.ROIPVOXHoldTime,
		ROIPMaxTXDuration:        cfg.ROIPMaxTXDuration,
		HalfDuplexThreshold:      cfg.HalfDuplexThreshold,
		EncoderComplexity:        cfg.EncoderComplexity,
		PacketLossPerc:           cfg.PacketLossPerc,
		DSCP:                     cfg.DSCP,
		PlaybackLatencyMs:        cfg.PlaybackLatencyMs,
		CaptureLatencyMs:         cfg.CaptureLatencyMs,
		CaptureFramesPerBuffer:   cfg.CaptureFramesPerBuffer,
		PttStartDelayMs:          cfg.PttStartDelayMs,
		AuxHandler:               cfg.AuxHandler,
		AudioMixerStartup:        cfg.AudioMixerStartup,
	}
}

// ─── applyDefaults ────────────────────────────────────────────────────────────

func (cfg *CommsConfig) applyDefaults() {
	if cfg.Iface == "" {
		cfg.Iface = defaultIface
	}

	if len(cfg.McastPorts) == 0 {
		tgs := config.GetMulticastTalkGroups()
		cfg.McastPorts = make([]McastPortConfig, len(tgs))

		for i, tg := range tgs {
			// Open sockets for every talk group so that EnableTalkGroupSend /
			// EnableTalkGroupReceive can activate any port at runtime without
			// a restart. Only port 0 is active on first startup.
			active := i == 0
			cfg.McastPorts[i] = McastPortConfig{
				Address:            tg.Address,
				Port:               tg.Port,
				Send:               true,
				Receive:            true,
				InitSendEnabled:    &active,
				InitReceiveEnabled: &active,
			}
		}
	}

	if cfg.CommKey == "" {
		cfg.CommKey = defaultKey
	}

	if cfg.NanoPTTDevicePath == "" {
		cfg.NanoPTTDevicePath = defaultCommDevice
	}

	if cfg.NanoPTTDeviceName == "" {
		cfg.NanoPTTDeviceName = defaultCommName
	}

	cfg.ControlSource = normalizeControlSource(cfg.ControlSource)

	// Apply ROIP-specific defaults after ControlSource is normalised.
	if cfg.ControlSource == controlSourceROIP {
		if cfg.ROIPCOSGPIOMask == 0 && cfg.ROIPVOXThreshold == 0 {
			// Neither explicitly configured: default to COS-primary, VOX fallback.
			cfg.ROIPCOSGPIOMask = control.ROIPDefaultCOSMask
			cfg.ROIPVOXThreshold = control.ROIPDefaultVOXThresh
		}

		if cfg.ROIPVOXThreshold > 0 && cfg.ROIPVOXHoldTime == 0 {
			cfg.ROIPVOXHoldTime = control.ROIPDefaultVOXHold
		}

		if cfg.ROIPMaxTXDuration == 0 {
			cfg.ROIPMaxTXDuration = control.ROIPDefaultMaxTX
		}
	}

	if cfg.RtpID == "" {
		if hostname, err := os.Hostname(); err == nil && hostname != "" {
			cfg.RtpID = hostname
		}
	}

	if cfg.BluetoothAudioDeviceHint != "" {
		if cfg.BluetoothInputDevice == "" {
			cfg.BluetoothInputDevice = cfg.BluetoothAudioDeviceHint
		}

		if cfg.BluetoothOutputDevice == "" {
			cfg.BluetoothOutputDevice = cfg.BluetoothAudioDeviceHint
		}
	}

	if cfg.audioInitRetryDelay == 0 {
		cfg.audioInitRetryDelay = defaultAudioInitRetryDelay
	}

	if cfg.audioRecoveryInterval == 0 {
		cfg.audioRecoveryInterval = defaultAudioRecoveryInterval
	}
}
