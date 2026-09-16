// Package alsa provides an AuxEventHandler that adjusts the system ALSA
// playback volume in response to volume up/down aux events. It is wired
// into CommsConfig in the production daemon and can be attached to any
// control source that implements control.AuxEventSource.
//
// The controller talks to ALSA via the pure-Go github.com/gen2brain/alsa
// binding (no CGO, cross-compiles cleanly to linux/amd64, linux/arm64,
// and linux/mipsle). It does not depend on alsa-utils being installed
// on the target.
package alsa

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/gen2brain/alsa"
	"github.com/rs/zerolog"

	"github.com/openmanet/openmanetd/internal/comms/control"
)

// DefaultControlName is the first playback candidate the controller tries
// when Controller.ControlName is empty (see PlaybackVolumeNames).
const DefaultControlName = "Master"

// DefaultStep is the per-press change applied to the raw mixer-control
// value when Controller.Step is zero. On a CM108B Master control (38
// steps from -37 dB to 0 dB) one raw step is approximately one dB; this
// approximation does not generalize to all cards.
const DefaultStep = 1

// Mixer is the minimal subset of github.com/gen2brain/alsa.Mixer that the
// controller uses, exposed as an interface so tests can inject a fake
// without requiring a real /dev/snd/controlC* device.
type Mixer interface {
	CtlByName(name string) (Ctl, error)
	ControlNames() []string
	Close() error
}

// Ctl is the minimal subset of github.com/gen2brain/alsa.MixerCtl used by
// the controller and Volume.
type Ctl interface {
	NumValues() uint32
	Value(index uint) (int, error)
	SetValue(index uint, value int) error
	RangeMin() (int, error)
	RangeMax() (int, error)
	IsBool() bool
}

// Opener opens an ALSA mixer for the given card index. Production callers
// use DefaultOpener; tests inject a fake.
type Opener func(card uint) (Mixer, error)

// Controller is an AuxEventHandler that increments or decrements the raw
// value of a named ALSA mixer control on volume up/down press events.
// Release events are ignored. The zero value is usable: the default opener
// uses gen2brain/alsa, the default control name is "Master", and the
// default step is 1.
type Controller struct {
	Log         zerolog.Logger
	Open        Opener
	ControlName string
	Step        int
}

// Handle implements control.AuxEventHandler. It dispatches on event kind;
// only press events trigger an ALSA write.
func (c *Controller) Handle(ctx context.Context, ev control.AuxEvent) {
	switch ev {
	case control.VolumeUpPressed:
		c.adjust(ctx, +1)
	case control.VolumeDownPressed:
		c.adjust(ctx, -1)
	case control.VolumeUpReleased, control.VolumeDownReleased:
		// Releases are intentionally ignored in v1; held buttons do not
		// auto-repeat.
	}
}

func (c *Controller) step() int {
	if c.Step > 0 {
		return c.Step
	}

	return DefaultStep
}

func (c *Controller) opener() Opener {
	if c.Open != nil {
		return c.Open
	}

	return DefaultOpener
}

// adjust applies dir * Step to every channel of the named mixer control,
// clamped to [RangeMin, RangeMax]. Errors at any step are logged and
// swallowed so a transient ALSA failure cannot crash the daemon.
func (c *Controller) adjust(_ context.Context, dir int) {
	card, err := CardFromEnv()
	if err != nil {
		ev := c.Log.Debug()
		if os.Getenv("ALSA_CARD") != "" {
			ev = c.Log.Warn()
		}

		ev.Err(err).Msg("alsa-vol: volume event ignored")

		return
	}

	m, err := c.opener()(card)
	if err != nil {
		c.Log.Warn().Err(err).Uint("card", card).Msg("alsa-vol: failed to open mixer")

		return
	}

	defer func() {
		if cerr := m.Close(); cerr != nil {
			c.Log.Debug().Err(cerr).Msg("alsa-vol: error closing mixer")
		}
	}()

	var (
		ctl  Ctl
		name string
	)

	if c.ControlName != "" {
		name = c.ControlName
		ctl, err = m.CtlByName(name)
	} else {
		ctl, name, err = ResolveCtl(m, PlaybackVolumeNames)
	}

	if err != nil || ctl == nil {
		c.Log.Warn().Err(err).Str("control", name).Msg("alsa-vol: control not found")

		return
	}

	minVal, err := ctl.RangeMin()
	if err != nil {
		c.Log.Warn().Err(err).Str("control", name).Msg("alsa-vol: RangeMin failed")

		return
	}

	maxVal, err := ctl.RangeMax()
	if err != nil {
		c.Log.Warn().Err(err).Str("control", name).Msg("alsa-vol: RangeMax failed")

		return
	}

	if maxVal < minVal {
		c.Log.Warn().Int("min", minVal).Int("max", maxVal).Str("control", name).
			Msg("alsa-vol: control reports inverted range; ignoring")

		return
	}

	delta := dir * c.step()
	n := ctl.NumValues()

	if n == 0 {
		c.Log.Warn().Str("control", name).Msg("alsa-vol: control has no values")

		return
	}

	for i := uint(0); i < uint(n); i++ {
		cur, vErr := ctl.Value(i)
		if vErr != nil {
			c.Log.Warn().Err(vErr).Str("control", name).Uint("channel", i).
				Msg("alsa-vol: read value failed")

			return
		}

		next := clamp(cur+delta, minVal, maxVal)
		if next == cur {
			c.Log.Debug().Str("control", name).Uint("channel", i).
				Int("value", cur).Int("min", minVal).Int("max", maxVal).
				Msg("alsa-vol: at range bound; no change")

			continue
		}

		if sErr := ctl.SetValue(i, next); sErr != nil {
			c.Log.Warn().Err(sErr).Str("control", name).Uint("channel", i).
				Int("value", next).Msg("alsa-vol: set value failed")

			return
		}

		c.Log.Debug().Str("control", name).Uint("channel", i).
			Int("from", cur).Int("to", next).Msg("alsa-vol: adjusted")
	}
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}

	if v > hi {
		return hi
	}

	return v
}

// ─── gen2brain/alsa adapter ──────────────────────────────────────────────────

// DefaultOpener is the production Opener: it opens the kernel mixer for the
// given card via gen2brain/alsa and wraps the result so the controller's
// Mixer/Ctl interfaces are satisfied. A second handle to the same control
// node backs elemIO, which replaces the library's element value accessors
// for INTEGER/BOOLEAN controls (see elemio.go for why).
func DefaultOpener(card uint) (Mixer, error) {
	m, err := alsa.MixerOpen(card)
	if err != nil {
		return nil, fmt.Errorf("alsa.MixerOpen card=%d: %w", card, err)
	}

	eio, err := openElemIO(card)
	if err != nil {
		_ = m.Close() // best-effort; the open error is the one worth reporting

		return nil, fmt.Errorf("elem io card=%d: %w", card, err)
	}

	return &mixerWrap{m: m, eio: eio}, nil
}

type mixerWrap struct {
	m   *alsa.Mixer
	eio *elemIO
}

func (w *mixerWrap) CtlByName(name string) (Ctl, error) {
	c, err := w.m.CtlByName(name)
	if err != nil {
		return nil, fmt.Errorf("CtlByName %q: %w", name, err)
	}

	if c == nil {
		return nil, errors.New("CtlByName returned nil")
	}

	return &ctlWrap{c: c, eio: w.eio}, nil
}

func (w *mixerWrap) Close() error {
	var errs []error

	if err := w.m.Close(); err != nil {
		errs = append(errs, fmt.Errorf("mixer close: %w", err))
	}

	if w.eio != nil {
		if err := w.eio.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

func (w *mixerWrap) ControlNames() []string {
	names := make([]string, 0, len(w.m.Ctls))
	for _, c := range w.m.Ctls {
		if c != nil {
			names = append(names, c.Name())
		}
	}

	return names
}

type ctlWrap struct {
	c   *alsa.MixerCtl
	eio *elemIO
}

// routesThroughElemIO reports whether value access must bypass the
// library: its INTEGER/BOOLEAN accessors overlay the kernel's long array
// as int32, so channels past the first are unreachable on 64-bit targets.
// ENUMERATED (u32 array) and INTEGER64/BYTES (other union members) keep
// the library accessors.
func (w *ctlWrap) routesThroughElemIO() bool {
	if w.eio == nil {
		return false
	}

	t := w.c.Type()

	return t == alsa.SNDRV_CTL_ELEM_TYPE_BOOLEAN || t == alsa.SNDRV_CTL_ELEM_TYPE_INTEGER
}

func (w *ctlWrap) NumValues() uint32 { return w.c.NumValues() }

func (w *ctlWrap) Value(index uint) (int, error) {
	if w.routesThroughElemIO() {
		v, err := w.eio.value(w.c.ID(), w.c.NumValues(), index)
		if err != nil {
			return 0, fmt.Errorf("ctl value: %w", err)
		}

		return v, nil
	}

	v, err := w.c.Value(index)
	if err != nil {
		return 0, fmt.Errorf("ctl value: %w", err)
	}

	return v, nil
}

func (w *ctlWrap) SetValue(index uint, value int) error {
	if w.routesThroughElemIO() {
		if err := w.eio.setValue(w.c.ID(), w.c.NumValues(), index, value); err != nil {
			return fmt.Errorf("ctl set value: %w", err)
		}

		return nil
	}

	if err := w.c.SetValue(index, value); err != nil {
		return fmt.Errorf("ctl set value: %w", err)
	}

	return nil
}

func (w *ctlWrap) RangeMin() (int, error) {
	v, err := w.c.RangeMin()
	if err != nil {
		return 0, fmt.Errorf("ctl range min: %w", err)
	}

	return v, nil
}

func (w *ctlWrap) RangeMax() (int, error) {
	v, err := w.c.RangeMax()
	if err != nil {
		return 0, fmt.Errorf("ctl range max: %w", err)
	}

	return v, nil
}

func (w *ctlWrap) IsBool() bool {
	return w.c.Type() == alsa.SNDRV_CTL_ELEM_TYPE_BOOLEAN
}
