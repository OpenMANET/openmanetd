package audio

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/openmanet/openmanetd/internal/comms/audiopool"
	"github.com/openmanet/openmanetd/internal/comms/device"
)

const (
	speakerQueueFrames = 50 // One second bounds memory and delayed audio on control failure.
	speakerTailFrames  = 15 // Let the hardware playback ring drain before silencing its amplifier.
	speakerSettle      = 80 * time.Millisecond
	// BS-22 remains quiet at its lowest nonzero gain, but can report both
	// volume directions. Zero suppresses its volume-down button entirely.
	speakerIdleGain uint16 = 1 << 8
)

// A short, tapered local tone plays through the same hardware gain as speech.
var speakerVolumeTone = func() [3 * audiopool.FrameSize]int16 {
	var tone [3 * audiopool.FrameSize]int16
	const ramp = audiopool.SampleRate / 200 // 5 ms at either end.
	for i := range tone {
		envelope := float64(min(i, len(tone)-1-i, ramp)) / float64(ramp)
		tone[i] = int16(6553 * envelope * math.Sin(2*math.Pi*880*float64(i)/float64(audiopool.SampleRate)))
	}
	return tone
}()

// speakerGate buffers onset while the asynchronous volume command settles.
// Queue fields and toneRemaining belong exclusively to the playback callback.
// No D-Bus operations or locks run on the audio thread.
type speakerGate struct {
	volumeChanges chan uint16
	volumeChanged func(uint16, bool)

	wanted             atomic.Bool
	ready              atomic.Bool
	tonePending        atomic.Bool
	toneRemaining      int // Samples of the volume tone left to play.
	wake               chan struct{}
	frames             [speakerQueueFrames][audiopool.FrameSize]int16
	head, count, quiet int
}

func (g *speakerGate) request(on bool) {
	if !on {
		g.ready.Store(false)
	}
	if g.wanted.Swap(on) != on {
		select {
		case g.wake <- struct{}{}:
		default:
		}
	}
}

func (g *speakerGate) process(out []int16) {
	if g.tonePending.Swap(false) {
		g.toneRemaining = len(speakerVolumeTone)
	}
	for i := range min(len(out), g.toneRemaining) {
		sample := int(out[i]) + int(speakerVolumeTone[len(speakerVolumeTone)-g.toneRemaining])
		out[i] = int16(max(-32768, min(32767, sample)))
		g.toneRemaining--
	}
	audible := false
	for _, v := range out {
		if v != 0 {
			audible = true
			break
		}
	}
	if audible {
		g.quiet = 0
		g.request(true)
	} else {
		g.quiet++
	}
	// All queued frames are silence once this tail expires. Reset the queue
	// rather than carrying startup latency into the next transmission.
	if !audible && g.quiet >= speakerTailFrames+g.count {
		g.count, g.head = 0, 0
		g.request(false)
		clear(out)
		return
	}
	if !g.wanted.Load() {
		clear(out)
		return
	}
	if g.count == speakerQueueFrames {
		// A failed/slow control link must not accumulate unbounded stale speech.
		g.head = (g.head + 1) % speakerQueueFrames
		g.count--
	}
	copy(g.frames[(g.head+g.count)%speakerQueueFrames][:], out)
	g.count++
	if !g.ready.Load() {
		clear(out)
		return
	}
	copy(out, g.frames[g.head][:])
	g.head = (g.head + 1) % speakerQueueFrames
	g.count--
}

// run is the only writer to the hardware volume. The initial hardware state
// must already be at the quiet idle gain. A failed command stays closed and retries, preserving
// the bounded onset queue rather than playing speech at an unknown gain.
func (g *speakerGate) run(ctx context.Context, set func(context.Context, bool) error) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	applied := false
	gainDirty := false
	for {
		select {
		case <-ctx.Done():
			return
		case gain := <-g.volumeChanges:
			if g.volumeChanged != nil {
				g.volumeChanged(gain, applied)
			}
			// Buffer the tone until the new listening gain has taken effect.
			g.ready.Store(false)
			g.tonePending.Store(true)
			gainDirty = true
		case <-g.wake:
		case <-ticker.C:
		}
		want := g.wanted.Load()
		if want == applied && !gainDirty {
			g.ready.Store(want)
			continue
		}
		g.ready.Store(false)
		commandCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		err := set(commandCtx, want)
		cancel()
		if err != nil {
			continue
		}
		applied = want
		gainDirty = false
		if want {
			timer := time.NewTimer(speakerSettle)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			g.ready.Store(g.wanted.Load())
		}
	}
}

type bluetoothSpeaker struct {
	gate    *speakerGate
	conn    *dbus.Conn
	path    dbus.ObjectPath
	volume  uint16
	address string
	cancel  context.CancelFunc
	done    chan struct{}
	once    sync.Once
}

func openBluetoothSpeaker(deviceName string) (*bluetoothSpeaker, error) {
	// Exact configured identity only: never change another accessory's volume.
	var address string
	for _, part := range strings.Split(deviceName, ",") {
		if _, value, ok := strings.Cut(part, "DEV="); ok {
			address = value
		}
	}
	if address == "" {
		return nil, fmt.Errorf("Bluetooth speaker requires an exact device address")
	}
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*bluetoothSpeaker, error) { _ = conn.Close(); return nil, err }
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var objects map[dbus.ObjectPath]map[string]map[string]dbus.Variant
	if err := conn.Object("org.bluealsa", "/org/bluealsa").CallWithContext(ctx, "org.freedesktop.DBus.ObjectManager.GetManagedObjects", 0).Store(&objects); err != nil {
		return fail(err)
	}
	suffix := "/dev_" + strings.ReplaceAll(strings.ToUpper(address), ":", "_")
	for path, interfaces := range objects {
		props, ok := interfaces["org.bluealsa.PCM1"]
		if !ok || props["Mode"].Value() != "sink" || props["Transport"].Value() != "HFP-AG" {
			continue
		}
		dev, ok := props["Device"].Value().(dbus.ObjectPath)
		if !ok || !strings.HasSuffix(strings.ToUpper(string(dev)), strings.ToUpper(suffix)) {
			continue
		}
		volume, ok := props["Volume"].Value().(uint16)
		if !ok {
			return fail(fmt.Errorf("Bluetooth speaker volume unavailable"))
		}
		volume, err = rememberBluetoothVolume(bluetoothVolumePath, address, volume)
		if err != nil {
			return fail(fmt.Errorf("preserve Bluetooth playback volume: %w", err))
		}
		s := &bluetoothSpeaker{gate: &speakerGate{wake: make(chan struct{}, 1)}, conn: conn, path: path, volume: volume, address: address, done: make(chan struct{})}
		s.gate.volumeChanges = make(chan uint16, 16)
		s.gate.volumeChanged = func(gain uint16, audible bool) {
			s.volume = preferredSpeakerGain(s.volume, gain, audible)
		}
		if err := s.set(ctx, false); err != nil {
			return fail(err)
		}
		work, stop := context.WithCancel(context.Background())
		s.cancel = stop
		go func() { defer close(s.done); s.gate.run(work, s.set) }()
		return s, nil
	}
	return fail(fmt.Errorf("selected Bluetooth speaker PCM unavailable"))
}

func (s *bluetoothSpeaker) set(ctx context.Context, audible bool) error {
	_, persistErr := writeBluetoothVolume(bluetoothVolumePath, s.address, s.volume, false)
	volume := speakerIdleGain
	if audible {
		volume = s.volume
	}
	// Storage failure must not prevent the physical idle mute.
	hardwareErr := s.conn.Object("org.bluealsa", s.path).CallWithContext(ctx, "org.freedesktop.DBus.Properties.Set", 0, "org.bluealsa.PCM1", "Volume", dbus.MakeVariant(volume)).Err
	return errors.Join(persistErr, hardwareErr)
}

func (s *bluetoothSpeaker) close() {
	s.once.Do(func() {
		s.cancel()
		<-s.done
		// Restore the user's gain after playback is closed, so a later session
		// does not mistake our temporary idle mute for their chosen volume.
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		_ = s.set(ctx, true)
		_ = s.conn.Close()
	})
}

type speakerStream struct {
	device.AudioStream
	speaker *bluetoothSpeaker
}

func (s *speakerStream) Close() error {
	err := s.AudioStream.Close()
	s.speaker.close()
	return err
}

// Gain is packed in the high byte for this mono BlueALSA PCM.
func preferredSpeakerGain(preferred, received uint16, audible bool) uint16 {
	if audible {
		return received
	}
	// Idle reports are relative to our quiet reference, not the saved
	// listening gain. Signed arithmetic prevents underflow at minimum volume.
	return uint16(max(0, min(15<<8, int(preferred)+int(received)-int(speakerIdleGain))))
}

func (s *speakerStream) SetBluetoothVolume(gain int) {
	if gain < 0 || gain > 15 {
		return
	}
	select {
	case <-s.speaker.done:
		return
	default:
	}
	select {
	case s.speaker.gate.volumeChanges <- uint16(gain) << 8:
	default:
	}
}
