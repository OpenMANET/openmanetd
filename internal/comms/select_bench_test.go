package comms

import (
	"testing"

	"github.com/rs/zerolog"

	"github.com/openmanet/openmanetd/internal/comms/talkgroup"
	"github.com/openmanet/openmanetd/internal/config"
)

func BenchmarkSelectTalkGroup(b *testing.B) {
	ports := make([]*PortChannel, 5)
	mcast := make([]McastPortConfig, 5)

	for i := range 5 {
		port, err := config.TalkGroupPort(i + 1)
		if err != nil {
			b.Fatal(err)
		}

		ports[i] = &PortChannel{cfg: McastPortConfig{Port: port}}
		mcast[i] = ports[i].cfg
	}

	svc := &Service{
		Cfg: &CommsConfig{Log: zerolog.Nop(), McastPorts: mcast},
		Rt:  &CommsRuntime{Ports: ports, Events: talkgroup.NewRegistry(zerolog.Nop())},
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := range b.N {
		if err := svc.SelectTalkGroup(i%5+1, talkgroup.SourceRPC); err != nil {
			b.Fatal(err)
		}
	}
}
