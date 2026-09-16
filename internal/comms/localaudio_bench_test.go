package comms

import (
	"testing"

	"github.com/rs/zerolog"

	"github.com/openmanet/openmanetd/internal/comms/audiopool"
	"github.com/openmanet/openmanetd/internal/config"
)

func BenchmarkAnnouncerEnqueue(b *testing.B) {
	port, err := config.TalkGroupPort(1)
	if err != nil {
		b.Fatal(err)
	}

	pc := &PortChannel{cfg: McastPortConfig{Port: port}, PlaybackBuffer: make(chan []int16, 4)}
	pc.setPlaybackStream(&fakeAudioStream{})
	pc.markPlaybackRunning()

	rt := &CommsRuntime{Ports: []*PortChannel{pc}}
	rt.ActiveChannel.Store(1)

	cfg := &CommsConfig{Log: zerolog.Nop()}
	frame := make([]int16, audiopool.FrameSize)

	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		cfg.queueLocalAudioFrame(rt, frame)

		select {
		case <-pc.PlaybackBuffer:
		default:
		}
	}
}
