package audio

import (
	"sync/atomic"

	"github.com/openmanet/openmanetd/internal/comms/audiopool"
	"github.com/openmanet/openmanetd/internal/comms/device"
)

// sharedPlaybackPort gates one talkgroup within the Bluetooth output mix.
// The hardware owner starts and closes the single physical stream. Port
// toggles only gate samples, so disabling one receiver cannot stop another.
type sharedPlaybackPort struct {
	active atomic.Bool
	volume interface{ SetBluetoothVolume(int) } // immutable after publication
}

func (p *sharedPlaybackPort) SetBluetoothVolume(gain int) {
	if p.volume != nil {
		p.volume.SetBluetoothVolume(gain)
	}
}

func (p *sharedPlaybackPort) Start() error { p.active.Store(true); return nil }
func (p *sharedPlaybackPort) Stop() error  { p.active.Store(false); return nil }
func (p *sharedPlaybackPort) Close() error { p.active.Store(false); return nil }

func sharedPlaybackSlot(slots []PortSlot) PortSlot {
	ports := make([]*sharedPlaybackPort, 0, len(slots))
	chunks := make([]*playbackChunker, 0, len(slots))
	receivers := make([]PortSlot, 0, len(slots))
	for _, slot := range slots {
		if !slot.HasReceiver {
			continue
		}
		p := &sharedPlaybackPort{}
		p.active.Store(true)
		ports = append(ports, p)
		chunks = append(chunks, newPlaybackChunker(slot.BeepBuf, slot.PlayoutFrame))
		receivers = append(receivers, slot)
	}
	scratch := make([]int16, audiopool.FrameSize)
	sum := make([]int32, audiopool.FrameSize)
	return PortSlot{
		HasReceiver: len(ports) > 0,
		SetStream: func(stream device.AudioStream) {
			for i, slot := range receivers {
				ports[i].volume, _ = stream.(interface{ SetBluetoothVolume(int) })
				slot.SetStream(ports[i])
			}
		},
		PlayoutFrame: func(out []int16) {
			clear(sum)
			for i, p := range ports {
				if !p.active.Load() {
					continue
				}
				clear(scratch)
				chunks[i].drain(scratch)
				for j, sample := range scratch {
					sum[j] += int32(sample)
				}
			}
			for i := range out {
				out[i] = int16(max(-32768, min(32767, sum[i])))
			}
		},
	}
}
