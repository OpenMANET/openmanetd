// Package announce plays short pre-decoded voice clips ("talk group
// one" …) into the local speaker path when the active talk group
// changes. It is a plain consumer of the talkgroup event registry.
package announce

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
)

// frameInterval paces frame handoff to the playback buffer: one 20 ms
// frame per 20 ms, matching the malgo callback's consumption rate so
// the depth-4 PlaybackBuffer never fills in steady state.
const frameInterval = 20 * time.Millisecond

// Player owns clip playback. One goroutine (Run) paces frames into the
// injected enqueue; Announce is the non-blocking entry point.
type Player struct {
	log     zerolog.Logger
	clips   map[int][][]int16
	enqueue func([]int16) bool
	// req is the depth-1 latest-wins request slot: a rapid selector spin
	// announces only the final position.
	req chan int
	// interval overrides frame pacing in tests; 0 means frameInterval.
	interval time.Duration

	plays atomic.Int64
	drops atomic.Int64
}

// New decodes the embedded clips and returns a ready Player. enqueue
// hands one 960-sample frame to the playback path and reports whether
// it was accepted; it must not block.
func New(log zerolog.Logger, enqueue func([]int16) bool) (*Player, error) {
	clips, err := decodeClips(log)
	if err != nil {
		return nil, err
	}

	return &Player{
		log:     log,
		clips:   clips,
		enqueue: enqueue,
		req:     make(chan int, 1),
	}, nil
}

// Announce requests playback of channel's clip. Non-blocking: a pending
// request is replaced. Safe from any goroutine.
func (p *Player) Announce(channel int) {
	for {
		select {
		case p.req <- channel:
			return
		default:
			// Slot full: evict the stale request and retry.
			select {
			case <-p.req:
			default:
			}
		}
	}
}

// Run feeds clip frames into enqueue one per frame interval until ctx
// ends. A new request aborts the in-flight clip. The pacing ticker only
// exists while a clip is playing, so an idle announcer costs nothing.
func (p *Player) Run(ctx context.Context) {
	iv := p.interval
	if iv <= 0 {
		iv = frameInterval
	}

	var (
		frames [][]int16
		idx    int
		tick   *time.Ticker
		tickC  <-chan time.Time
	)

	stopTick := func() {
		if tick != nil {
			tick.Stop()

			tick, tickC = nil, nil
		}
	}
	defer stopTick()

	for {
		select {
		case <-ctx.Done():
			return
		case ch := <-p.req:
			clip, ok := p.clips[ch]
			if !ok {
				p.log.Warn().Int("channel", ch).Msg("announce: no clip for channel")

				continue
			}

			frames, idx = clip, 0

			p.plays.Add(1)

			if tick == nil {
				tick = time.NewTicker(iv)
				tickC = tick.C
			}
		case <-tickC:
			if frames == nil {
				stopTick()

				continue
			}

			if !p.enqueue(frames[idx]) {
				p.drops.Add(1)
			}

			idx++
			if idx == len(frames) {
				frames = nil

				stopTick()
			}
		}
	}
}

// Plays returns the number of clip playbacks started. Nil-safe.
func (p *Player) Plays() int64 {
	if p == nil {
		return 0
	}

	return p.plays.Load()
}

// Snapshot is the announcer's instrumentation section.
type Snapshot struct {
	// Plays counts clip playbacks started since comms start.
	Plays int64 `json:"plays"`
	// FrameDrops counts frames the playback buffer refused (full).
	FrameDrops int64 `json:"frame_drops"`
}

// Snapshot fills dst. Nil-receiver safe, zero-alloc.
func (p *Player) Snapshot(dst *Snapshot) {
	if dst == nil {
		return
	}

	if p == nil {
		*dst = Snapshot{}

		return
	}

	dst.Plays = p.plays.Load()
	dst.FrameDrops = p.drops.Load()
}
