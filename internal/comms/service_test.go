package comms

import (
	"errors"
	"sync"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openmanet/openmanetd/internal/comms/talkgroup"
	"github.com/openmanet/openmanetd/internal/config"
)

// newTestService constructs a *Service directly (bypassing Start) with a
// minimal runtime suitable for exercising Service methods in isolation. It
// publishes the service via SetDefault and registers cleanup to clear it.
func newTestService(t *testing.T, ports []McastPortConfig) *Service {
	t.Helper()

	cfg := &CommsConfig{McastPorts: ports}
	rt := &CommsRuntime{Ports: make([]*PortChannel, len(ports))}

	for i, pc := range ports {
		rt.Ports[i] = &PortChannel{cfg: pc}
		rt.Ports[i].SendEnabled.Store(true)
		rt.Ports[i].ReceiveEnabled.Store(true)
	}

	svc := &Service{Cfg: cfg, Rt: rt}
	SetDefault(svc)
	t.Cleanup(func() { SetDefault(nil) })

	return svc
}

func TestService_ActiveMulticastAccessors(t *testing.T) {
	svc := newTestService(t, []McastPortConfig{{Address: "239.1.2.3", Port: 5004}})

	if got := svc.ActiveMulticastAddr(); got != "239.1.2.3" {
		t.Errorf("ActiveMulticastAddr = %q, want 239.1.2.3", got)
	}

	if got := svc.ActiveMulticastPort(); got != 5004 {
		t.Errorf("ActiveMulticastPort = %d, want 5004", got)
	}

	// Default() must reach the same data so handlers that resolve the
	// service via the package-level lookup observe the published instance.
	if Default().ActiveMulticastAddr() != "239.1.2.3" {
		t.Error("Default().ActiveMulticastAddr() mismatch")
	}

	if Default().ActiveMulticastPort() != 5004 {
		t.Error("Default().ActiveMulticastPort() mismatch")
	}
}

func TestService_NilDefault(t *testing.T) {
	SetDefault(nil)

	if got := Default().ActiveMulticastAddr(); got != "" {
		t.Errorf("nil default addr = %q, want empty", got)
	}

	if got := Default().ActiveMulticastPort(); got != 0 {
		t.Errorf("nil default port = %d, want 0", got)
	}

	// Service methods on a nil default must surface ErrNotRunning so callers
	// (handlers, manager, tests) can use errors.Is to distinguish "not yet
	// started" from other failures.
	if _, err := Default().TalkGroupStates(); !errors.Is(err, ErrNotRunning) {
		t.Errorf("TalkGroupStates on nil default: want ErrNotRunning, got %v", err)
	}

	if err := Default().EnableTalkGroupSend(0, true); !errors.Is(err, ErrNotRunning) {
		t.Errorf("EnableTalkGroupSend on nil default: want ErrNotRunning, got %v", err)
	}

	if err := Default().EnableTalkGroupReceive(0, true); !errors.Is(err, ErrNotRunning) {
		t.Errorf("EnableTalkGroupReceive on nil default: want ErrNotRunning, got %v", err)
	}
}

func TestService_EnableTalkGroupAndStates(t *testing.T) {
	svc := newTestService(t, []McastPortConfig{
		{Address: "239.1.1.1", Port: 5004},
		{Address: "239.1.1.2", Port: 5006},
	})

	if err := svc.EnableTalkGroupSend(1, false); err != nil {
		t.Fatalf("EnableTalkGroupSend: %v", err)
	}

	if err := svc.EnableTalkGroupReceive(0, false); err != nil {
		t.Fatalf("EnableTalkGroupReceive: %v", err)
	}

	states, err := svc.TalkGroupStates()
	if err != nil {
		t.Fatalf("TalkGroupStates: %v", err)
	}

	if len(states) != 2 {
		t.Fatalf("states len = %d, want 2", len(states))
	}

	if states[0].ReceiveEnabled || !states[0].SendEnabled {
		t.Errorf("port 0 state = %+v", states[0])
	}

	if !states[1].ReceiveEnabled || states[1].SendEnabled {
		t.Errorf("port 1 state = %+v", states[1])
	}

	if err := svc.EnableTalkGroupSend(42, true); err == nil {
		t.Error("out-of-range EnableTalkGroupSend: want error")
	}
}

// ─── SelectTalkGroup / ActiveTalkGroup / Events / seedActiveChannel tests ─────

func newSelectTestService(t *testing.T, numPorts int) *Service {
	t.Helper()

	ports := make([]*PortChannel, numPorts)
	mcast := make([]McastPortConfig, numPorts)

	for i := range numPorts {
		port, err := config.TalkGroupPort(i + 1)
		require.NoError(t, err)

		ports[i] = &PortChannel{cfg: McastPortConfig{Address: "239.192.41.1", Port: port}}
		mcast[i] = ports[i].cfg
	}

	rt := &CommsRuntime{Ports: ports, Events: talkgroup.NewRegistry(zerolog.Nop())}

	return &Service{Cfg: &CommsConfig{Log: zerolog.Nop(), McastPorts: mcast}, Rt: rt}
}

func TestSelectTalkGroup_ExclusiveFlip(t *testing.T) {
	svc := newSelectTestService(t, 5)
	// seed: channel 1 active
	svc.Rt.Ports[0].SendEnabled.Store(true)
	svc.Rt.Ports[0].ReceiveEnabled.Store(true)
	svc.Rt.ActiveChannel.Store(1)

	var events []talkgroup.Event

	svc.Rt.Events.Add(func(ev talkgroup.Event) { events = append(events, ev) })

	require.NoError(t, svc.SelectTalkGroup(3, talkgroup.SourceRPC))

	for i, pc := range svc.Rt.Ports {
		want := i == 2
		assert.Equal(t, want, pc.SendEnabled.Load(), "port %d send", i)
		assert.Equal(t, want, pc.ReceiveEnabled.Load(), "port %d receive", i)
	}

	assert.Equal(t, 3, svc.ActiveTalkGroup())
	require.Len(t, events, 1, "one KindSelected event, no KindDirection noise")
	assert.Equal(t, talkgroup.KindSelected, events[0].Kind)
	assert.Equal(t, 3, events[0].Channel)
	assert.Equal(t, 1, events[0].Prev)
	assert.Equal(t, talkgroup.SourceRPC, events[0].Source)
}

func TestSelectTalkGroup_NoOpEmitsNoEvent(t *testing.T) {
	svc := newSelectTestService(t, 5)
	svc.Rt.Ports[1].SendEnabled.Store(true)
	svc.Rt.Ports[1].ReceiveEnabled.Store(true)
	svc.Rt.ActiveChannel.Store(2)

	var events []talkgroup.Event

	svc.Rt.Events.Add(func(ev talkgroup.Event) { events = append(events, ev) })

	require.NoError(t, svc.SelectTalkGroup(2, talkgroup.SourceGPIO))
	assert.Empty(t, events, "re-selecting the active group with no state delta is silent")
}

func TestSelectTalkGroup_Unprovisioned(t *testing.T) {
	svc := newSelectTestService(t, 5)

	err := svc.SelectTalkGroup(9, talkgroup.SourceRPC)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not provisioned")
}

func TestSelectTalkGroup_NotRunning(t *testing.T) {
	var svc *Service

	require.ErrorIs(t, svc.SelectTalkGroup(1, talkgroup.SourceRPC), ErrNotRunning)
	assert.Zero(t, svc.ActiveTalkGroup())
	assert.Nil(t, svc.Events())
}

func TestEnableTalkGroupSend_EmitsDirectionEvent(t *testing.T) {
	svc := newSelectTestService(t, 2)

	var events []talkgroup.Event

	svc.Rt.Events.Add(func(ev talkgroup.Event) { events = append(events, ev) })

	require.NoError(t, svc.EnableTalkGroupSend(1, true))
	require.Len(t, events, 1)
	assert.Equal(t, talkgroup.KindDirection, events[0].Kind)
	assert.Equal(t, 2, events[0].Channel)
	assert.True(t, events[0].Send)

	// Redundant set: no state change, no event.
	require.NoError(t, svc.EnableTalkGroupSend(1, true))
	assert.Len(t, events, 1)
}

func TestActiveMulticastPort_TracksSelection(t *testing.T) {
	svc := newSelectTestService(t, 5)

	first, err := config.TalkGroupPort(1)
	require.NoError(t, err)
	assert.Equal(t, first, svc.ActiveMulticastPort(), "falls back to first port before any selection")

	require.NoError(t, svc.SelectTalkGroup(4, talkgroup.SourceRPC))

	want, err := config.TalkGroupPort(4)
	require.NoError(t, err)
	assert.Equal(t, want, svc.ActiveMulticastPort())
}

func TestSeedActiveChannel(t *testing.T) {
	svc := newSelectTestService(t, 5)
	svc.Rt.Ports[2].SendEnabled.Store(true)
	svc.Rt.Ports[2].ReceiveEnabled.Store(true)

	var events []talkgroup.Event

	svc.Rt.Events.Add(func(ev talkgroup.Event) { events = append(events, ev) })

	svc.Cfg.seedActiveChannel(svc.Rt)

	assert.Equal(t, 3, svc.ActiveTalkGroup())
	require.Len(t, events, 1)
	assert.Equal(t, talkgroup.SourceInit, events[0].Source)
	assert.Equal(t, talkgroup.KindSelected, events[0].Kind)
}

// TestSelectTalkGroup_ConcurrentConverges proves the phase-split locking
// (atomic flips under selectMu, playback reconciled unlocked afterward)
// still keeps the exclusive-selection invariant when multiple selection
// sources race: whichever channel wins is the only one left with a
// receive-enabled port.
func TestSelectTalkGroup_ConcurrentConverges(t *testing.T) {
	svc := newSelectTestService(t, 5)

	var wg sync.WaitGroup

	for g := range 8 {
		wg.Add(1)

		ch := g%5 + 1

		go func() {
			defer wg.Done()

			_ = svc.SelectTalkGroup(ch, talkgroup.SourceRPC)
		}()
	}

	wg.Wait()

	active := svc.ActiveTalkGroup()
	require.NotZero(t, active)

	wantPort, err := config.TalkGroupPort(active)
	require.NoError(t, err)

	enabled := 0

	for _, pc := range svc.Rt.Ports {
		if pc.ReceiveEnabled.Load() {
			enabled++

			assert.Equal(t, wantPort, pc.cfg.Port, "the single receive-enabled port must be the active channel")
		}
	}

	assert.Equal(t, 1, enabled, "exactly one receive-enabled port after concurrent selects")
}

func TestForwardSelections_FirstEmissionIsSourceInit(t *testing.T) {
	svc := newSelectTestService(t, 5)

	var srcs []talkgroup.Source

	svc.Rt.Events.Add(func(ev talkgroup.Event) {
		if ev.Kind == talkgroup.KindSelected {
			srcs = append(srcs, ev.Source)
		}
	})

	events := make(chan int, 2)
	events <- 2 // selector's boot-time read of the physical switch position

	events <- 3 // a subsequent operator turn

	close(events)

	svc.forwardSelections(events, zerolog.Nop())

	require.Len(t, srcs, 2)
	assert.Equal(t, talkgroup.SourceInit, srcs[0], "boot read forwarded as SourceInit so the announcer stays silent")
	assert.Equal(t, talkgroup.SourceGPIO, srcs[1], "later turns forwarded as SourceGPIO")
}
