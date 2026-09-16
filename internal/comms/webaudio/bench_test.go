package webaudio

import (
	"testing"

	"github.com/rs/zerolog"
)

// BenchmarkWebDrain measures the per-frame cost of delivering one Opus
// payload from the web playout drain to the RPC consumer: the defensive
// copy webPlayoutLoop makes so the jitter buffer's pooled payload can be
// released, the bridge hand-off, and the consumer receive.
func BenchmarkWebDrain(b *testing.B) {
	bridge := NewBridge(zerolog.Nop(), func(_ []byte) {})

	payload := make([]byte, 100) // typical Opus 20 ms frame

	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		// Mirror the webPlayoutLoop drain and RPC consumer: the bridge
		// copies into a pooled buffer, the consumer releases after use.
		bridge.PushRxFrame(1, payload)

		f := <-bridge.RxFrames()
		f.Release()
	}
}
