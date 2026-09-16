package control

import (
	"context"
	"fmt"
	"strconv"
	"sync"

	evdev "github.com/gvalkov/golang-evdev"
	"github.com/rs/zerolog"
)

// nanoPTTErrStreakThreshold is the number of consecutive read failures
// after which the event goroutine gives up and terminates. Real evdev
// read errors (ENODEV on unplug, EBADF/closed-file after our own
// close-on-cancel) are all persistent, so the streak exists only to
// absorb a hypothetical transient; without a bound, a dead device fd
// returning instantly would busy-spin this goroutine at 100% CPU.
const nanoPTTErrStreakThreshold = 3

// NanoPTTSource reads Linux input events from an evdev device and emits
// PTTToggle on each key-press that matches the configured key code.
//
// The source owns the device: each enable cycle opens a fresh
// *evdev.InputDevice (control_register.go → findCommDevice), and the
// Events goroutine closes it on context cancellation or terminal read
// failure. Closing the underlying file is also what unblocks a read(2)
// parked between key events — the evdev fd is registered with the Go
// runtime poller, mirroring the OpenVLM HID close-on-cancel pattern.
type NanoPTTSource struct {
	log      zerolog.Logger
	dev      *evdev.InputDevice
	readOne  func() (*evdev.InputEvent, error)
	closeDev func() error
	pttKey   string
}

// NewNanoPTTSource constructs a NanoPTTSource that reads from dev and emits
// a PTTToggle on each press of pttKey ("any" matches any key code, otherwise
// the decimal evdev key code). The source takes ownership of dev and closes
// it when the Events goroutine exits.
func NewNanoPTTSource(dev *evdev.InputDevice, pttKey string, log zerolog.Logger) EventSource {
	return &NanoPTTSource{dev: dev, pttKey: pttKey, log: log}
}

// readOneEvent resolves the read through the test seam, falling back to
// the real device.
func (s *NanoPTTSource) readOneEvent() (*evdev.InputEvent, error) {
	if s.readOne != nil {
		return s.readOne()
	}

	ev, err := s.dev.ReadOne()
	if err != nil {
		return nil, fmt.Errorf("evdev read: %w", err)
	}

	return ev, nil
}

// matchesKey reports whether code matches the configured pttKey ("any"
// matches every code, otherwise the decimal evdev key code).
func (s *NanoPTTSource) matchesKey(code uint16) bool {
	if s.pttKey == "any" {
		return true
	}

	kc, err := strconv.Atoi(s.pttKey)

	return err == nil && kc >= 0 && kc <= 65535 && code == uint16(kc)
}

// handleKeyEvent emits PTTToggle for a matching key press. Returns false
// when a pending send was aborted by ctx cancellation (the caller should
// exit); true otherwise.
func (s *NanoPTTSource) handleKeyEvent(ctx context.Context, ev *evdev.InputEvent, ch chan<- PTTEvent) bool {
	if ev.Type != evdev.EV_KEY || !s.matchesKey(ev.Code) {
		return true
	}

	switch ev.Value {
	case 1:
		s.log.Debug().Msgf("Comm key press (code=%d)", ev.Code)

		select {
		case ch <- PTTToggle:
		case <-ctx.Done():
			return false
		}
	case 0:
		s.log.Debug().Msgf("Comm key release (code=%d)", ev.Code)
	}

	return true
}

// closeDevice resolves the close through the test seam, falling back to
// closing the real device's file (which unblocks a pending ReadOne).
func (s *NanoPTTSource) closeDevice() error {
	if s.closeDev != nil {
		return s.closeDev()
	}

	if s.dev != nil && s.dev.File != nil {
		return s.dev.File.Close()
	}

	return nil
}

// Events returns the PTT event channel for this source. The channel is
// closed when ctx is canceled or when the device fails persistently
// (e.g. the dongle is unplugged); either way the device is closed exactly
// once, so no goroutine or fd survives a disable/enable cycle.
func (s *NanoPTTSource) Events(ctx context.Context) <-chan PTTEvent {
	ch := make(chan PTTEvent, 4)

	go func() {
		defer close(ch)

		var closeOnce sync.Once

		closeDev := func() {
			closeOnce.Do(func() {
				if err := s.closeDevice(); err != nil {
					s.log.Debug().Err(err).Msg("comms: error closing PTT device")
				}
			})
		}

		// Close-on-cancel: ReadOne blocks in read(2) between key events,
		// and the context is only checked between reads — closing the
		// file is the only way to unblock a parked read promptly.
		stop := context.AfterFunc(ctx, closeDev)

		defer func() {
			stop()
			closeDev()
		}()

		errStreak := 0

		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			ev, err := s.readOneEvent()
			if err != nil {
				if ctx.Err() != nil {
					return
				}

				errStreak++
				if errStreak >= nanoPTTErrStreakThreshold {
					s.log.Error().Err(err).Msg("comms: PTT device read failed repeatedly; stopping event source")

					return
				}

				continue
			}

			errStreak = 0

			if !s.handleKeyEvent(ctx, ev, ch) {
				return
			}
		}
	}()

	return ch
}
