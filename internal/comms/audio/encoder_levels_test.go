package audio

import (
	"sync/atomic"
	"testing"

	"github.com/openmanet/openmanetd/internal/comms/audiopool"
)

func TestRecordPCM16Peak_fullRange(t *testing.T) {
	var peak atomic.Int32
	recordPCM16Peak(&peak, []int16{0, 32767, -32768})
	if got := peak.Load(); got != 32768 {
		t.Fatalf("peak = %d, want 32768", got)
	}
	recordPCM16Peak(&peak, []int16{12, -40})
	if got := peak.Load(); got != 32768 {
		t.Fatalf("quieter frame lowered peak to %d", got)
	}
}

func TestBroadcastEncoder_levelsBeforeAndAfterGain(t *testing.T) {
	be, _ := newGainTestEncoder(t, 0.5)
	fp := audiopool.Int16Pool.Get().(*[]int16)
	*fp = (*fp)[:audiopool.FrameSize]
	clear(*fp)
	(*fp)[0] = -32768
	be.encodeOne(fp)
	var s AudioEncoderSnapshot
	be.Snapshot(&s)
	if s.CapturePeak != 32768 || s.PreEncodePeak != 16384 {
		t.Fatalf("peaks = %d/%d, want 32768/16384", s.CapturePeak, s.PreEncodePeak)
	}
	be.SetTxEnabled(false)
	be.SetTxEnabled(true)
	be.Snapshot(&s)
	if s.CapturePeak != 0 || s.PreEncodePeak != 0 {
		t.Fatal("new PTT cycle retained old peaks")
	}
}
