package audio

import (
	"github.com/openmanet/openmanetd/internal/comms/audiopool"
	"github.com/openmanet/openmanetd/internal/comms/device"
	"testing"
)

func TestSharedPlaybackIndependentGatesAndClipping(t *testing.T) {
	streams := make([]device.AudioStream, 2)
	slots := make([]PortSlot, 2)
	for i := range slots {
		slots[i] = PortSlot{HasReceiver: true, SetStream: func(s device.AudioStream) { streams[i] = s }, PlayoutFrame: func(out []int16) {
			for j := range out {
				out[j] = 20000
			}
		}}
	}
	slot := sharedPlaybackSlot(slots)
	slot.SetStream(nil)
	out := make([]int16, audiopool.FrameSize)
	slot.PlayoutFrame(out)
	if out[0] != 32767 {
		t.Fatalf("mixed sample %d", out[0])
	}
	if err := streams[0].Stop(); err != nil {
		t.Fatal(err)
	}
	slot.PlayoutFrame(out)
	if out[0] != 20000 {
		t.Fatalf("stopping one port muted another: %d", out[0])
	}
	if err := streams[1].Stop(); err != nil {
		t.Fatal(err)
	}
	slot.PlayoutFrame(out)
	if out[0] != 0 {
		t.Fatalf("disabled ports emitted %d", out[0])
	}
}
