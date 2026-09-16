package alsa

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/rs/zerolog"

	"github.com/openmanet/openmanetd/internal/comms/control"
)

// Names carries per-role candidate control-name lists for a Volume. An
// empty list falls back to the package defaults (PlaybackVolumeNames and
// friends); a single-entry list pins an explicit override from config.
type Names struct {
	Playback       []string
	Capture        []string
	AGC            []string
	PlaybackSwitch []string
	CaptureSwitch  []string
}

// NamesFromOverrides builds a Names whose per-role lists pin the given
// explicit control names; empty strings keep the package defaults.
func NamesFromOverrides(playback, capture, agc string) Names {
	var n Names

	if playback != "" {
		n.Playback = []string{playback}
	}

	if capture != "" {
		n.Capture = []string{capture}
	}

	if agc != "" {
		n.AGC = []string{agc}
	}

	return n
}

// State is a point-in-time reading of the card's level controls. Percent
// values are -1 when the corresponding control does not exist on the card.
type State struct {
	SpeakerControl string
	MicControl     string
	AGCControl     string
	SpeakerPct     int
	MicPct         int
	Available      bool
	AGCPresent     bool
	AGCEnabled     bool
}

// Update is a partial write; nil fields are left untouched.
type Update struct {
	SpeakerPct *int
	MicPct     *int
	AGC        *bool
}

// Volume provides absolute read/write access to the sound card's mixer
// levels. The zero value plus a Log is usable: Open defaults to the
// gen2brain/alsa opener, Names to the package candidate lists, and
// DetectCard to control.DetectAndSetALSACard. Unlike Controller (the
// VOL+/VOL− button handler, which swallows errors), Volume returns errors
// so the RPC layer can map them to response codes. Methods open the kernel
// mixer per call and close it on return; operations complete in
// microseconds and never touch the audio hot path.
type Volume struct {
	Log  zerolog.Logger
	Open Opener
	// DetectCard runs ALSA card auto-detection when ALSA_CARD is unset —
	// the card may have enumerated after comms startup detection ran.
	// Defaults to control.DetectAndSetALSACard; tests inject a stub.
	DetectCard func(zerolog.Logger)
	Names      Names

	// Atomic caches of the most recent daemon-side reading, consumed by
	// the instrumentation snapshot (atomic-load-only contract). Encoding:
	// 0 = never read (unknown); volume fields store pct+1 (1..101); the
	// AGC field stores 1 = off, 2 = on. Out-of-band alsamixer or VOL
	// button changes are not tracked until the next API read.
	lastSpeakerPct atomic.Int64
	lastMicPct     atomic.Int64
	lastAGC        atomic.Int64
}

func (v *Volume) opener() Opener {
	if v.Open != nil {
		return v.Open
	}

	return DefaultOpener
}

func (v *Volume) names() Names {
	n := v.Names

	if len(n.Playback) == 0 {
		n.Playback = PlaybackVolumeNames
	}

	if len(n.Capture) == 0 {
		n.Capture = CaptureVolumeNames
	}

	if len(n.AGC) == 0 {
		n.AGC = AGCNames
	}

	if len(n.PlaybackSwitch) == 0 {
		n.PlaybackSwitch = PlaybackSwitchNames
	}

	if len(n.CaptureSwitch) == 0 {
		n.CaptureSwitch = CaptureSwitchNames
	}

	return n
}

// openMixer resolves the card — running detection once if ALSA_CARD is
// unset — and opens its kernel mixer.
func (v *Volume) openMixer() (Mixer, error) {
	card, err := CardFromEnv()
	if err != nil {
		detect := v.DetectCard
		if detect == nil {
			detect = control.DetectAndSetALSACard
		}

		detect(v.Log)

		card, err = CardFromEnv()
		if err != nil {
			return nil, err
		}
	}

	m, mErr := v.opener()(card)
	if mErr != nil {
		return nil, fmt.Errorf("open mixer card=%d: %w", card, mErr)
	}

	return m, nil
}

func (v *Volume) closeMixer(m Mixer) {
	if err := m.Close(); err != nil {
		v.Log.Debug().Err(err).Msg("alsa-vol: error closing mixer")
	}
}

// State reads the current mixer levels. It returns an error wrapping
// ErrNoCard when no ALSA card is available; controls that are merely
// absent on the card report -1 / AGCPresent=false without an error.
func (v *Volume) State(_ context.Context) (State, error) {
	st := State{SpeakerPct: -1, MicPct: -1}

	m, err := v.openMixer()
	if err != nil {
		return st, err
	}

	defer v.closeMixer(m)

	st.Available = true
	n := v.names()

	if ctl, name, rErr := ResolveCtl(m, n.Playback); rErr == nil {
		if pct, pErr := readPercent(ctl); pErr == nil {
			st.SpeakerPct = pct
			st.SpeakerControl = name
		} else {
			v.Log.Warn().Err(pErr).Str("control", name).Msg("alsa-vol: speaker read failed")
		}
	}

	if ctl, name, rErr := ResolveCtl(m, n.Capture); rErr == nil {
		if pct, pErr := readPercent(ctl); pErr == nil {
			st.MicPct = pct
			st.MicControl = name
		} else {
			v.Log.Warn().Err(pErr).Str("control", name).Msg("alsa-vol: mic read failed")
		}
	}

	if ctl, name, rErr := ResolveCtl(m, n.AGC); rErr == nil && ctl.IsBool() {
		if val, vErr := ctl.Value(0); vErr == nil {
			st.AGCPresent = true
			st.AGCEnabled = val != 0
			st.AGCControl = name
		} else {
			v.Log.Warn().Err(vErr).Str("control", name).Msg("alsa-vol: agc read failed")
		}
	}

	v.cacheState(st)

	return st, nil
}

// cacheState records st into the atomic instrumentation cache.
func (v *Volume) cacheState(st State) {
	if st.SpeakerPct >= 0 {
		v.lastSpeakerPct.Store(int64(st.SpeakerPct) + 1)
	}

	if st.MicPct >= 0 {
		v.lastMicPct.Store(int64(st.MicPct) + 1)
	}

	if st.AGCPresent {
		if st.AGCEnabled {
			v.lastAGC.Store(2)
		} else {
			v.lastAGC.Store(1)
		}
	}
}

// Apply writes the non-nil fields of u to hardware, then reads back and
// returns the resulting state. A missing target control is an error
// (wrapping ErrControlNotFound) — unlike State, a write to a control that
// does not exist must not be silently dropped.
func (v *Volume) Apply(ctx context.Context, u Update) (State, error) {
	if err := v.applyWrites(u); err != nil {
		return State{SpeakerPct: -1, MicPct: -1}, err
	}

	return v.State(ctx)
}

func (v *Volume) applyWrites(u Update) error {
	if u.SpeakerPct == nil && u.MicPct == nil && u.AGC == nil {
		return nil
	}

	m, err := v.openMixer()
	if err != nil {
		return err
	}

	defer v.closeMixer(m)

	n := v.names()

	if u.SpeakerPct != nil {
		ctl, _, rErr := ResolveCtl(m, n.Playback)
		if rErr != nil {
			return fmt.Errorf("speaker volume: %w", rErr)
		}

		if wErr := writePercent(ctl, *u.SpeakerPct); wErr != nil {
			return fmt.Errorf("speaker volume: %w", wErr)
		}
	}

	if u.MicPct != nil {
		ctl, _, rErr := ResolveCtl(m, n.Capture)
		if rErr != nil {
			return fmt.Errorf("mic volume: %w", rErr)
		}

		if wErr := writePercent(ctl, *u.MicPct); wErr != nil {
			return fmt.Errorf("mic volume: %w", wErr)
		}
	}

	if u.AGC != nil {
		ctl, name, rErr := ResolveCtl(m, n.AGC)
		if rErr != nil {
			return fmt.Errorf("agc: %w", rErr)
		}

		if !ctl.IsBool() {
			return fmt.Errorf("agc: control %q is not boolean: %w", name, ErrControlNotFound)
		}

		if wErr := writeBool(ctl, *u.AGC); wErr != nil {
			return fmt.Errorf("agc: %w", wErr)
		}
	}

	return nil
}

// readPercent converts ctl's channel-0 value to 0..100 over its range.
func readPercent(ctl Ctl) (int, error) {
	minV, err := ctl.RangeMin()
	if err != nil {
		return 0, fmt.Errorf("range min: %w", err)
	}

	maxV, err := ctl.RangeMax()
	if err != nil {
		return 0, fmt.Errorf("range max: %w", err)
	}

	if maxV <= minV {
		return 0, fmt.Errorf("invalid range [%d, %d]", minV, maxV)
	}

	val, err := ctl.Value(0)
	if err != nil {
		return 0, fmt.Errorf("read value: %w", err)
	}

	return (100 * (val - minV)) / (maxV - minV), nil
}

// writePercent maps pct (clamped to 0..100) into ctl's range and writes
// every channel.
func writePercent(ctl Ctl, pct int) error {
	if pct < 0 {
		pct = 0
	}

	if pct > 100 {
		pct = 100
	}

	minV, err := ctl.RangeMin()
	if err != nil {
		return fmt.Errorf("range min: %w", err)
	}

	maxV, err := ctl.RangeMax()
	if err != nil {
		return fmt.Errorf("range max: %w", err)
	}

	if maxV <= minV {
		return fmt.Errorf("invalid range [%d, %d]", minV, maxV)
	}

	val := minV + (maxV-minV)*pct/100

	for i := uint(0); i < uint(ctl.NumValues()); i++ {
		if sErr := ctl.SetValue(i, val); sErr != nil {
			return fmt.Errorf("set value channel %d: %w", i, sErr)
		}
	}

	return nil
}

// writeBool writes b to every channel of a boolean control.
func writeBool(ctl Ctl, b bool) error {
	val := 0
	if b {
		val = 1
	}

	for i := uint(0); i < uint(ctl.NumValues()); i++ {
		if err := ctl.SetValue(i, val); err != nil {
			return fmt.Errorf("set value channel %d: %w", i, err)
		}
	}

	return nil
}

// ApplyStartup re-applies persisted mixer levels after ALSA card
// detection (comms startup and in-run audio recovery — a USB replug
// resets the card's mixer state). Beyond Apply, it applies each field
// independently so one missing control cannot block the others, forces
// every resolvable playback/capture switch on — with no mute in the API,
// this is the only recovery from an out-of-band alsamixer mute — and logs
// the card's control list at Debug for name-variance diagnosis. All
// errors are logged and swallowed: a mixer failure must never block
// audio startup.
func (v *Volume) ApplyStartup(ctx context.Context, u Update) {
	parts := []Update{
		{SpeakerPct: u.SpeakerPct},
		{MicPct: u.MicPct},
		{AGC: u.AGC},
	}

	for _, part := range parts {
		if part.SpeakerPct == nil && part.MicPct == nil && part.AGC == nil {
			continue
		}

		if _, err := v.Apply(ctx, part); err != nil {
			ev := v.Log.Warn()
			if errors.Is(err, ErrControlNotFound) {
				// A card simply lacking this control is expected hardware
				// variance; with AGC applied on every startup, warning here
				// would recur on each boot and recovery of AGC-less cards.
				ev = v.Log.Debug()
			}

			ev.Err(err).Msg("alsa-vol: startup mixer apply failed")
		}
	}

	v.unmuteSwitches()
	v.logControlNames()
}

// unmuteSwitches sets every resolvable playback/capture switch control to
// on. Failures are logged and swallowed.
func (v *Volume) unmuteSwitches() {
	m, err := v.openMixer()
	if err != nil {
		return
	}

	defer v.closeMixer(m)

	n := v.names()

	for _, role := range [][]string{n.PlaybackSwitch, n.CaptureSwitch} {
		ctl, name, rErr := ResolveCtl(m, role)
		if rErr != nil || !ctl.IsBool() {
			continue
		}

		if wErr := writeBool(ctl, true); wErr != nil {
			v.Log.Warn().Err(wErr).Str("control", name).Msg("alsa-vol: startup unmute failed")

			continue
		}

		v.Log.Debug().Str("control", name).Msg("alsa-vol: startup unmute applied")
	}
}

// logControlNames logs the card's full mixer control enumeration once at
// Debug — the field diagnostic for control-name variance.
func (v *Volume) logControlNames() {
	m, err := v.openMixer()
	if err != nil {
		return
	}

	defer v.closeMixer(m)

	v.Log.Debug().Strs("controls", m.ControlNames()).Msg("alsa-vol: card mixer controls")
}
