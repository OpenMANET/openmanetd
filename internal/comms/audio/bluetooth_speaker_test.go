package audio

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/openmanet/openmanetd/internal/comms/audiopool"
	"github.com/stretchr/testify/require"
)

func TestSpeakerGatePreservesOnsetAndMutesAfterTail(t *testing.T) {
	g := &speakerGate{wake: make(chan struct{}, 1)}
	frame := make([]int16, audiopool.FrameSize)
	for n := int16(1); n <= 5; n++ {
		for i := range frame {
			frame[i] = n
		}
		g.process(frame)
		require.True(t, g.wanted.Load())
		require.Zero(t, frame[0], "must buffer while the speaker is muted")
	}
	g.ready.Store(true)
	for n := int16(1); n <= 5; n++ {
		clear(frame)
		g.process(frame)
		require.Equal(t, n, frame[0], "first speech/tone frames must survive volume startup")
	}
	for range speakerTailFrames + speakerQueueFrames {
		clear(frame)
		g.process(frame)
	}
	require.False(t, g.wanted.Load())
	require.False(t, g.ready.Load())
	require.Zero(t, g.count)
}

func TestSpeakerGateDoesNotMuteBriefSpeechPause(t *testing.T) {
	g := &speakerGate{wake: make(chan struct{}, 1)}
	g.ready.Store(true)
	frame := make([]int16, audiopool.FrameSize)
	frame[0] = 1
	g.process(frame)
	for range speakerTailFrames - 1 {
		clear(frame)
		g.process(frame)
	}
	require.True(t, g.wanted.Load())
	frame[0] = 2
	g.process(frame)
	require.Equal(t, int16(2), frame[0])
	require.True(t, g.wanted.Load())
}

func TestSpeakerGateBoundsBacklogOnControlFailure(t *testing.T) {
	g := &speakerGate{wake: make(chan struct{}, 1)}
	frame := make([]int16, audiopool.FrameSize)
	for range speakerQueueFrames * 3 {
		frame[0] = 200
		g.process(frame)
		require.Zero(t, frame[0])
		require.LessOrEqual(t, g.count, speakerQueueFrames)
	}
	for range speakerQueueFrames + speakerTailFrames {
		clear(frame)
		g.process(frame)
	}
	require.Zero(t, g.count, "stale speech must expire when control remains unavailable")
	require.False(t, g.wanted.Load())
}

func TestSpeakerWorkerSettlesRetriesAndJoins(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		g := &speakerGate{wake: make(chan struct{}, 1)}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		calls := make(chan bool, 8)
		first := true
		go func() {
			defer close(done)
			g.run(ctx, func(_ context.Context, on bool) error {
				calls <- on
				if first {
					first = false
					return errors.New("transient control failure")
				}
				return nil
			})
		}()
		t.Cleanup(func() { cancel(); <-done })
		g.request(true)
		synctest.Wait()
		require.True(t, <-calls)
		require.False(t, g.ready.Load())
		time.Sleep(100 * time.Millisecond)
		synctest.Wait()
		require.True(t, <-calls)
		require.False(t, g.ready.Load(), "a D-Bus reply alone does not mean the headset has applied gain")
		time.Sleep(speakerSettle)
		synctest.Wait()
		require.True(t, g.ready.Load())
		g.request(false)
		require.False(t, g.ready.Load(), "close the gate before asynchronous mute")
		synctest.Wait()
		require.False(t, <-calls)
		g.request(true)
		synctest.Wait()
		require.True(t, <-calls)
		time.Sleep(speakerSettle)
		synctest.Wait()
		require.True(t, g.ready.Load())
	})
}

func TestSpeakerWorkerCoalescedIdleWakeDoesNotStickMuted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		g := &speakerGate{wake: make(chan struct{}, 1)}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { defer close(done); g.run(ctx, func(context.Context, bool) error { return nil }) }()
		t.Cleanup(func() { cancel(); <-done })
		g.request(true)
		synctest.Wait()
		time.Sleep(speakerSettle)
		synctest.Wait()
		require.True(t, g.ready.Load())
		g.request(false)
		g.request(true)
		synctest.Wait()
		time.Sleep(speakerSettle)
		synctest.Wait()
		require.True(t, g.ready.Load())
	})
}

func TestSpeakerVolumeChangeRemutesIdleAndKeepsListeningGain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		preferred := uint16(10 << 8)
		g := &speakerGate{wake: make(chan struct{}, 1), volumeChanges: make(chan uint16, 16)}
		g.volumeChanged = func(gain uint16, audible bool) { preferred = preferredSpeakerGain(preferred, gain, audible) }
		calls := make(chan uint16, 16)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			defer close(done)
			g.run(ctx, func(_ context.Context, on bool) error {
				gain := speakerIdleGain
				if on {
					gain = preferred
				}
				calls <- gain
				return nil
			})
		}()
		t.Cleanup(func() { cancel(); <-done })
		g.volumeChanges <- 2 << 8
		synctest.Wait()
		require.Equal(t, speakerIdleGain, <-calls, "idle button restores quiet reference")
		g.volumeChanges <- 0
		synctest.Wait()
		require.Equal(t, speakerIdleGain, <-calls, "down press also restores the reference")
		g.volumeChanges <- 2 << 8
		synctest.Wait()
		require.Equal(t, speakerIdleGain, <-calls)
		g.request(true)
		synctest.Wait()
		require.Equal(t, uint16(11<<8), <-calls)
		time.Sleep(speakerSettle)
		synctest.Wait()
		g.volumeChanges <- 7 << 8
		synctest.Wait()
		require.Equal(t, uint16(7<<8), <-calls)
		time.Sleep(speakerSettle)
		synctest.Wait()
		g.request(false)
		synctest.Wait()
		require.Equal(t, speakerIdleGain, <-calls)
		g.request(true)
		synctest.Wait()
		require.Equal(t, uint16(7<<8), <-calls, "next burst keeps user's level")
	})
}
