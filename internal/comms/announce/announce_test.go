package announce

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openmanet/openmanetd/internal/comms/audiopool"
)

func TestNew_DecodesEmbeddedClips(t *testing.T) {
	p, err := New(zerolog.Nop(), func([]int16) bool { return true })
	require.NoError(t, err)

	for ch := 1; ch <= 5; ch++ {
		frames, ok := p.clips[ch]
		require.True(t, ok, "clip for channel %d", ch)
		assert.NotEmpty(t, frames)

		for i, f := range frames {
			assert.Len(t, f, audiopool.FrameSize, "clip %d frame %d", ch, i)
		}
	}
}

func TestPlayer_PlaysClipThroughEnqueue(t *testing.T) {
	frames := make(chan []int16, 512)
	p, err := New(zerolog.Nop(), func(f []int16) bool {
		frames <- f

		return true
	})
	require.NoError(t, err)

	p.interval = time.Microsecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})

	go func() { p.Run(ctx); close(done) }()

	p.Announce(2)

	want := len(p.clips[2])
	for range want {
		select {
		case f := <-frames:
			assert.Len(t, f, audiopool.FrameSize)
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for announcement frames")
		}
	}

	cancel()
	<-done
	assert.Equal(t, int64(1), p.Plays())
}

func TestPlayer_LatestWins(t *testing.T) {
	p, err := New(zerolog.Nop(), func([]int16) bool { return true })
	require.NoError(t, err)

	// Run not started: both requests hit the depth-1 slot.
	p.Announce(1)
	p.Announce(4)

	select {
	case ch := <-p.req:
		assert.Equal(t, 4, ch, "later request replaces pending one")
	default:
		t.Fatal("request slot empty")
	}
}

func TestPlayer_MissingClipSkips(t *testing.T) {
	frames := make(chan []int16, 8)
	p, err := New(zerolog.Nop(), func(f []int16) bool {
		frames <- f

		return true
	})
	require.NoError(t, err)

	p.interval = time.Microsecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})

	go func() { p.Run(ctx); close(done) }()

	p.Announce(31) // provisionable channel, no clip embedded

	select {
	case <-frames:
		t.Fatal("no frames expected for a missing clip")
	case <-time.After(50 * time.Millisecond):
	}

	cancel()
	<-done
	assert.Zero(t, p.Plays())
}

func TestPlayer_SnapshotNilSafe(t *testing.T) {
	var p *Player

	var s Snapshot

	assert.NotPanics(t, func() { p.Snapshot(&s) })
	assert.Zero(t, s.Plays)
}

func BenchmarkClipDecodeStart(b *testing.B) {
	b.ReportAllocs()

	for range b.N {
		if _, err := New(zerolog.Nop(), func([]int16) bool { return true }); err != nil {
			b.Fatal(err)
		}
	}
}
