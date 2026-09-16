package alsa_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openmanet/openmanetd/internal/comms/control"
	"github.com/openmanet/openmanetd/internal/comms/control/alsa"
)

// ─── Fakes ─────────────────────────────────────────────────────────────────────

type fakeCtl struct {
	mu sync.Mutex

	values     []int
	rangeMin   int
	rangeMax   int
	rangeMinEr error
	rangeMaxEr error
	valueErr   error
	setErr     error
	isBool     bool

	setCalls []struct {
		channel uint
		value   int
	}
}

func newFakeCtl(values []int, minVal, maxVal int) *fakeCtl {
	cp := make([]int, len(values))
	copy(cp, values)

	return &fakeCtl{values: cp, rangeMin: minVal, rangeMax: maxVal}
}

func (c *fakeCtl) NumValues() uint32 {
	c.mu.Lock()
	defer c.mu.Unlock()

	return uint32(len(c.values))
}

func (c *fakeCtl) Value(index uint) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.valueErr != nil {
		return 0, c.valueErr
	}

	if int(index) >= len(c.values) {
		return 0, errors.New("index out of range")
	}

	return c.values[index], nil
}

func (c *fakeCtl) SetValue(index uint, value int) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.setErr != nil {
		return c.setErr
	}

	if int(index) >= len(c.values) {
		return errors.New("index out of range")
	}

	c.values[index] = value
	c.setCalls = append(c.setCalls, struct {
		channel uint
		value   int
	}{index, value})

	return nil
}

func (c *fakeCtl) RangeMin() (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.rangeMin, c.rangeMinEr
}

func (c *fakeCtl) RangeMax() (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.rangeMax, c.rangeMaxEr
}

func (c *fakeCtl) IsBool() bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.isBool
}

func (c *fakeCtl) snapshotValues() []int {
	c.mu.Lock()
	defer c.mu.Unlock()

	out := make([]int, len(c.values))
	copy(out, c.values)

	return out
}

func (c *fakeCtl) setCallCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return len(c.setCalls)
}

type fakeMixer struct {
	ctls       map[string]alsa.Ctl
	closeCalls int
	closeErr   error
	ctlErr     error
}

func (m *fakeMixer) CtlByName(name string) (alsa.Ctl, error) {
	if m.ctlErr != nil {
		return nil, m.ctlErr
	}

	ctl, ok := m.ctls[name]
	if !ok {
		return nil, errors.New("control not found")
	}

	return ctl, nil
}

func (m *fakeMixer) Close() error {
	m.closeCalls++

	return m.closeErr
}

func (m *fakeMixer) ControlNames() []string {
	names := make([]string, 0, len(m.ctls))
	for name := range m.ctls {
		names = append(names, name)
	}

	return names
}

// fakeOpener returns an Opener that hands out the same mixer for every card,
// recording the card index it was asked to open.
type fakeOpener struct {
	mu sync.Mutex

	mixer    *fakeMixer
	openErr  error
	openCard uint
	openCnt  int
}

func (o *fakeOpener) opener() alsa.Opener {
	return func(card uint) (alsa.Mixer, error) {
		o.mu.Lock()
		defer o.mu.Unlock()

		o.openCnt++
		o.openCard = card

		if o.openErr != nil {
			return nil, o.openErr
		}

		return o.mixer, nil
	}
}

func (o *fakeOpener) calls() int {
	o.mu.Lock()
	defer o.mu.Unlock()

	return o.openCnt
}

// withCard sets ALSA_CARD for the duration of the test and restores the prior
// value (or unsets) on cleanup.
func withCard(t *testing.T, value string) {
	t.Helper()

	prev, hadPrev := os.LookupEnv("ALSA_CARD")

	t.Cleanup(func() {
		if hadPrev {
			_ = os.Setenv("ALSA_CARD", prev)

			return
		}

		_ = os.Unsetenv("ALSA_CARD")
	})

	require.NoError(t, os.Setenv("ALSA_CARD", value))
}

// ─── Tests ─────────────────────────────────────────────────────────────────────

func TestController_VolumeUp_IncrementsRawValue(t *testing.T) {
	withCard(t, "1")

	ctl := newFakeCtl([]int{10, 10}, 0, 38)
	mx := &fakeMixer{ctls: map[string]alsa.Ctl{"Master": ctl}}
	op := &fakeOpener{mixer: mx}

	c := &alsa.Controller{Log: zerolog.Nop(), Open: op.opener()}
	c.Handle(context.Background(), control.VolumeUpPressed)

	assert.Equal(t, []int{11, 11}, ctl.snapshotValues(), "both channels should bump by 1")
	assert.Equal(t, 1, op.calls(), "opener called once")
	assert.Equal(t, uint(1), op.openCard, "opener called with ALSA_CARD value")
	assert.Equal(t, 1, mx.closeCalls, "mixer closed after adjust")
}

func TestController_VolumeDown_DecrementsRawValue(t *testing.T) {
	withCard(t, "0")

	ctl := newFakeCtl([]int{10, 10}, 0, 38)
	mx := &fakeMixer{ctls: map[string]alsa.Ctl{"Master": ctl}}
	op := &fakeOpener{mixer: mx}

	c := &alsa.Controller{Log: zerolog.Nop(), Open: op.opener()}
	c.Handle(context.Background(), control.VolumeDownPressed)

	assert.Equal(t, []int{9, 9}, ctl.snapshotValues(), "both channels should drop by 1")
}

func TestController_ClampsAtMax(t *testing.T) {
	withCard(t, "0")

	ctl := newFakeCtl([]int{38}, 0, 38)
	mx := &fakeMixer{ctls: map[string]alsa.Ctl{"Master": ctl}}
	op := &fakeOpener{mixer: mx}

	c := &alsa.Controller{Log: zerolog.Nop(), Open: op.opener()}
	c.Handle(context.Background(), control.VolumeUpPressed)

	assert.Equal(t, []int{38}, ctl.snapshotValues(), "value should not exceed RangeMax")
	assert.Equal(t, 0, ctl.setCallCount(), "no SetValue when already at max")
}

func TestController_ClampsAtMin(t *testing.T) {
	withCard(t, "0")

	ctl := newFakeCtl([]int{0}, 0, 38)
	mx := &fakeMixer{ctls: map[string]alsa.Ctl{"Master": ctl}}
	op := &fakeOpener{mixer: mx}

	c := &alsa.Controller{Log: zerolog.Nop(), Open: op.opener()}
	c.Handle(context.Background(), control.VolumeDownPressed)

	assert.Equal(t, []int{0}, ctl.snapshotValues(), "value should not go below RangeMin")
	assert.Equal(t, 0, ctl.setCallCount(), "no SetValue when already at min")
}

func TestController_CustomStep(t *testing.T) {
	withCard(t, "0")

	ctl := newFakeCtl([]int{10}, 0, 100)
	mx := &fakeMixer{ctls: map[string]alsa.Ctl{"Master": ctl}}
	op := &fakeOpener{mixer: mx}

	c := &alsa.Controller{Log: zerolog.Nop(), Open: op.opener(), Step: 5}
	c.Handle(context.Background(), control.VolumeUpPressed)

	assert.Equal(t, []int{15}, ctl.snapshotValues(), "should increment by custom step")
}

func TestController_CustomControlName(t *testing.T) {
	withCard(t, "0")

	ctl := newFakeCtl([]int{10}, 0, 100)
	mx := &fakeMixer{ctls: map[string]alsa.Ctl{"Speaker": ctl}}
	op := &fakeOpener{mixer: mx}

	c := &alsa.Controller{Log: zerolog.Nop(), Open: op.opener(), ControlName: "Speaker"}
	c.Handle(context.Background(), control.VolumeUpPressed)

	assert.Equal(t, []int{11}, ctl.snapshotValues(), "non-default control should be adjusted")
}

func TestController_ALSACardUnset_NoOp(t *testing.T) {
	t.Cleanup(func() { _ = os.Unsetenv("ALSA_CARD") })
	require.NoError(t, os.Unsetenv("ALSA_CARD"))

	ctl := newFakeCtl([]int{10}, 0, 38)
	mx := &fakeMixer{ctls: map[string]alsa.Ctl{"Master": ctl}}
	op := &fakeOpener{mixer: mx}

	c := &alsa.Controller{Log: zerolog.Nop(), Open: op.opener()}
	c.Handle(context.Background(), control.VolumeUpPressed)

	assert.Equal(t, 0, op.calls(), "opener should not be called when ALSA_CARD unset")
	assert.Equal(t, []int{10}, ctl.snapshotValues(), "value unchanged")
}

func TestController_ALSACardInvalid_NoOp(t *testing.T) {
	withCard(t, "not-a-number")

	op := &fakeOpener{mixer: &fakeMixer{}}

	c := &alsa.Controller{Log: zerolog.Nop(), Open: op.opener()}
	c.Handle(context.Background(), control.VolumeUpPressed)

	assert.Equal(t, 0, op.calls(), "opener should not be called for invalid ALSA_CARD")
}

func TestController_OpenError_LogsAndReturns(t *testing.T) {
	withCard(t, "0")

	op := &fakeOpener{openErr: errors.New("device busy")}

	c := &alsa.Controller{Log: zerolog.Nop(), Open: op.opener()}
	// Must not panic.
	c.Handle(context.Background(), control.VolumeUpPressed)

	assert.Equal(t, 1, op.calls())
}

func TestController_ControlNotFound_LogsAndReturns(t *testing.T) {
	withCard(t, "0")

	mx := &fakeMixer{ctls: map[string]alsa.Ctl{}, ctlErr: errors.New("not found")}
	op := &fakeOpener{mixer: mx}

	c := &alsa.Controller{Log: zerolog.Nop(), Open: op.opener()}
	c.Handle(context.Background(), control.VolumeUpPressed)

	assert.Equal(t, 1, mx.closeCalls, "mixer must be closed even when control not found")
}

func TestController_ReleaseEvents_NoOp(t *testing.T) {
	withCard(t, "0")

	ctl := newFakeCtl([]int{10}, 0, 38)
	mx := &fakeMixer{ctls: map[string]alsa.Ctl{"Master": ctl}}
	op := &fakeOpener{mixer: mx}

	c := &alsa.Controller{Log: zerolog.Nop(), Open: op.opener()}
	c.Handle(context.Background(), control.VolumeUpReleased)
	c.Handle(context.Background(), control.VolumeDownReleased)

	assert.Equal(t, 0, op.calls(), "release events must not open the mixer")
	assert.Equal(t, []int{10}, ctl.snapshotValues(), "value unchanged on release")
}

func TestController_MultiChannel_AdjustsAll(t *testing.T) {
	withCard(t, "0")

	ctl := newFakeCtl([]int{5, 10, 15}, 0, 38)
	mx := &fakeMixer{ctls: map[string]alsa.Ctl{"Master": ctl}}
	op := &fakeOpener{mixer: mx}

	c := &alsa.Controller{Log: zerolog.Nop(), Open: op.opener()}
	c.Handle(context.Background(), control.VolumeUpPressed)

	assert.Equal(t, []int{6, 11, 16}, ctl.snapshotValues(), "every channel should bump")
	assert.Equal(t, 3, ctl.setCallCount())
}

func TestController_CandidateFallback_UsesSpeakerPlaybackVolume(t *testing.T) {
	withCard(t, "0")

	ctl := newFakeCtl([]int{10}, 0, 38)
	mx := &fakeMixer{ctls: map[string]alsa.Ctl{"Speaker Playback Volume": ctl}}
	op := &fakeOpener{mixer: mx}

	c := &alsa.Controller{Log: zerolog.Nop(), Open: op.opener()}
	c.Handle(context.Background(), control.VolumeUpPressed)

	assert.Equal(t, []int{11}, ctl.snapshotValues(),
		"button path must fall through candidate list when Master is absent")
}
