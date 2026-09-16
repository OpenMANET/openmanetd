package talkgroup_test

import (
	"testing"

	"github.com/rs/zerolog"

	"github.com/openmanet/openmanetd/internal/comms/talkgroup"
)

func BenchmarkRegistryNotify(b *testing.B) {
	for _, n := range []int{0, 1, 8} {
		b.Run(map[int]string{0: "0-listeners", 1: "1-listener", 8: "8-listeners"}[n], func(b *testing.B) {
			r := talkgroup.NewRegistry(zerolog.Nop())
			for range n {
				r.Add(func(talkgroup.Event) {})
			}

			ev := talkgroup.Event{Kind: talkgroup.KindSelected, Channel: 2}

			b.ReportAllocs()
			b.ResetTimer()

			for range b.N {
				r.Notify(ev)
			}
		})
	}
}
