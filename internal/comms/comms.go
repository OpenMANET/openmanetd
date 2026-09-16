package comms

import (
	"fmt"

	"github.com/openmanet/openmanetd/internal/comms/audiopool"
	"github.com/openmanet/openmanetd/internal/comms/codec"
	"github.com/openmanet/openmanetd/internal/comms/control"
)

// ─── Package-level constants ──────────────────────────────────────────────────

// TargetBitrate is the Opus encoder bitrate in bits-per-second used for all
// comms TX. Exported so the RPC layer can report it to UI clients.
const TargetBitrate int = 32000

const (
	targetBitrate int = TargetBitrate
	// encoderComplexity is the default Opus encoder complexity used when
	// CommsConfig.EncoderComplexity is unset or out of range. Complexity is
	// the libopus quality/CPU tradeoff knob: 0 is fastest, 10 is highest
	// quality. The libopus reference default for VoIP is 5.
	//
	// We default to 5 because the deployment target includes constrained
	// MIPS/ARM edge routers (per CLAUDE.md the binary must run on
	// linux/mipsle). Empirically, complexity 10 at 48 kHz mono can take
	// 20-30 ms per 20 ms frame on those CPUs, which saturates the
	// per-frame budget and causes the malgo capture callback to
	// overflow encCh and drop frames — surfacing as audible stutter on
	// the receive side. Web mode bypasses this entirely (the browser
	// supplies pre-encoded Opus), which is why the symptom is hardware-
	// mode only. Operators on faster CPUs can opt back in to complexity 10
	// via cfg.EncoderComplexity.
	encoderComplexity int    = 5
	packetLossPerc    int    = 20
	defaultKey        string = "any"
	defaultIface      string = "br-ahwlan"
	defaultCommDevice string = "/dev/hidraw0/*"
	defaultCommName   string = "AllInOneCable"
	defaultCtrlSrc    string = "openvlm"
)

// ─── codec construction ──────────────────────────────────────────────────────

func (cfg *CommsConfig) buildEncoder() (codec.AudioEncoder, error) {
	complexity := cfg.EncoderComplexity
	if complexity <= 0 || complexity > 10 {
		complexity = encoderComplexity
	}

	perc := cfg.PacketLossPerc
	if perc < 10 || perc > 40 {
		perc = packetLossPerc
	}

	enc, err := codec.NewOpusEncoder(audiopool.SampleRate, audiopool.Channels, targetBitrate, complexity, perc)
	if err != nil {
		return nil, err
	}

	return enc, nil
}

// buildPortDecoders allocates a private Opus decoder for every receive-capable
// port. Decoders are stateful and NOT safe for concurrent use, and any number
// of ports can be receive-enabled at once (each with its own playback thread),
// so one decoder per port is a correctness requirement, not an optimization.
// libopus decoder state is ~27 KB per instance — affordable even on the MIPS
// targets at the default five talk groups.
func buildPortDecoders(ports []*PortChannel) error {
	for _, pc := range ports {
		if pc.Receiver == nil {
			continue
		}

		dec, err := codec.NewOpusDecoder(audiopool.SampleRate, audiopool.Channels)
		if err != nil {
			return fmt.Errorf("port decoder: %w", err)
		}

		pc.Decoder = dec
	}

	return nil
}

// sendToAllPorts sends an encoded RTP payload to every port where sendEnabled
// is true and an rtpSess is configured. Send errors are logged at Debug level
// and do not abort remaining ports.
func (cfg *CommsConfig) sendToAllPorts(rt *CommsRuntime, payload []byte) {
	for _, pc := range rt.Ports {
		if !pc.SendEnabled.Load() || pc.RTPSess == nil {
			continue
		}

		if err := pc.RTPSess.Send(payload); err != nil {
			cfg.Log.Debug().Err(err).
				Str("addr", pc.cfg.Address).
				Int("port", pc.cfg.Port).
				Msg("comms: RTP send failed")
		}
	}
}

// ─── buildEventSource ─────────────────────────────────────────────────────────

// buildEventSource constructs the PTT EventSource for cfg.ControlSource by
// looking the name up in the control-source registry. The four supported
// backends — "openvlm", "roip", "web", "nanoptt" — register themselves via
// init() in control_register.go. Validate() (called from CommsManager.Enable)
// rejects unknown sources up front; this function returns an error if a
// caller still reaches it with an unregistered source.
func (cfg *CommsConfig) buildEventSource(rt *CommsRuntime) (control.EventSource, error) {
	factory, ok := controlLookup(cfg.ControlSource)
	if !ok {
		return nil, fmt.Errorf("comms: unknown ControlSource %q", cfg.ControlSource)
	}

	deps, err := cfg.buildControlDeps(rt)
	if err != nil {
		return nil, err
	}

	es, err := factory(deps)
	if err != nil {
		return nil, err
	}

	return es, nil
}
