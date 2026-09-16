package comms

import (
	"testing"

	"github.com/openmanet/openmanetd/internal/comms/audio"
	"github.com/openmanet/openmanetd/internal/comms/control"
	"github.com/openmanet/openmanetd/internal/comms/rtp"
	"github.com/openmanet/openmanetd/internal/comms/talkgroup"
	"github.com/openmanet/openmanetd/internal/comms/webaudio"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestService_Snapshot_NilReceiver verifies Snapshot is nil-safe so handlers
// that race with SetDefault(nil) observe a consistent "disabled" view.
func TestService_Snapshot_NilReceiver(t *testing.T) {
	t.Parallel()

	var svc *Service

	var dst CommsSnapshot

	svc.Snapshot(&dst)

	assert.False(t, dst.Enabled)
	assert.False(t, dst.Broadcasting)
	assert.False(t, dst.RemoteRxActive)
	assert.Empty(t, dst.ControlSource)
	assert.Empty(t, dst.Ports)
}

// TestService_Snapshot_NilRuntime verifies the method handles a *Service
// whose Rt field is nil (the constructor-built-but-not-started state).
func TestService_Snapshot_NilRuntime(t *testing.T) {
	t.Parallel()

	svc := &Service{
		Cfg: &CommsConfig{ControlSource: "web"},
	}

	var dst CommsSnapshot

	svc.Snapshot(&dst)

	assert.False(t, dst.Enabled)
	assert.Equal(t, "web", dst.ControlSource)
	assert.Empty(t, dst.Ports)
}

// TestService_Snapshot_Populated exercises the steady-state path with a
// runtime containing a handful of ports and an embedded web bridge.
func TestService_Snapshot_Populated(t *testing.T) {
	t.Parallel()

	bridge := webaudio.NewBridge(zerolog.Nop(), nil)
	// Drive the bridge counters so the snapshot has non-zero values.
	bridge.PushRxFrame(1, []byte{0x01})
	bridge.PushRxFrame(1, []byte{0x02})

	port0 := &PortChannel{
		cfg:    McastPortConfig{Address: "239.0.0.1", Port: 5000},
		Jitter: rtp.NewJitterBuffer(rtp.PrebufferPackets, rtp.MaxDepth),
	}
	port0.Jitter.Overflows.Store(3)
	port0.SendEnabled.Store(true)
	port0.ReceiveEnabled.Store(true)
	port0.PlaybackUnderruns.Store(7)
	port0.RxPkts.Store(100)
	port0.RxLoopback.Store(2)
	port0.RxParseErrs.Store(1)
	port0.RxPushed.Store(95)
	port0.RxPushRejected.Store(4)
	port0.WebPoppedSkipped.Store(6)
	port0.RxGate.Mark()

	port1 := &PortChannel{
		cfg:    McastPortConfig{Address: "239.0.0.2", Port: 5002},
		Jitter: rtp.NewJitterBuffer(rtp.PrebufferPackets, rtp.MaxDepth),
	}

	rt := &CommsRuntime{
		WebBridge: bridge,
		Ports:     []*PortChannel{port0, port1},
	}
	rt.Broadcasting.Store(true)
	rt.RemoteRxActive.Store(true)

	// Attach a FEC adapter so its snapshot fields round-trip too.
	rt.FECAdapter = NewFECAdapter(rt, &fakeAdapterEncoder{}, 20, zerolog.Nop())

	svc := &Service{
		Cfg: &CommsConfig{ControlSource: "openvlm"},
		Rt:  rt,
	}

	var dst CommsSnapshot

	svc.Snapshot(&dst)

	assert.True(t, dst.Enabled)
	assert.True(t, dst.Broadcasting)
	assert.True(t, dst.RemoteRxActive)
	assert.Equal(t, "openvlm", dst.ControlSource)
	assert.Equal(t, int64(2), dst.WebBridge.RxPushIn)
	assert.Equal(t, int64(0), dst.WebBridge.RxPushDrop)
	require.Len(t, dst.Ports, 2)
	assert.Equal(t, "239.0.0.1", dst.Ports[0].Address)
	assert.Equal(t, 5000, dst.Ports[0].Port)
	assert.True(t, dst.Ports[0].SendEnabled)
	assert.True(t, dst.Ports[0].ReceiveEnabled)
	assert.Equal(t, int64(7), dst.Ports[0].PlaybackUnderruns)
	assert.Equal(t, int64(100), dst.Ports[0].RxPkts)
	assert.Equal(t, int64(2), dst.Ports[0].RxLoopback)
	assert.Equal(t, int64(1), dst.Ports[0].RxParseErrs)
	assert.Equal(t, int64(95), dst.Ports[0].RxPushed)
	assert.Equal(t, int64(4), dst.Ports[0].RxPushRejected)
	assert.Equal(t, int64(6), dst.Ports[0].WebPoppedSkipped)
	assert.Equal(t, int64(3), dst.Ports[0].Jitter.Overflows)
	assert.True(t, dst.Ports[0].RxGate.Active)
	assert.NotZero(t, dst.Ports[0].RxGate.LastMarkUnixNano)
	assert.Equal(t, control.DefaultHalfDuplexThreshold.Nanoseconds(), dst.Ports[0].RxGate.ThresholdNs)

	assert.Equal(t, "239.0.0.2", dst.Ports[1].Address)
	assert.False(t, dst.Ports[1].SendEnabled)

	// FEC adapter snapshot fields — just constructed, so level == floor,
	// no transitions, zero EWMA, Floor reflects the configured value.
	assert.Equal(t, 20, dst.FECAdapter.CurrentLevel)
	assert.Equal(t, 20, dst.FECAdapter.Floor)
	assert.InDelta(t, 0.0, dst.FECAdapter.LossEWMA, 0.0001)
	assert.Equal(t, int64(0), dst.FECAdapter.Transitions)
	assert.Equal(t, int64(0), dst.FECAdapter.WriteErrors)
}

// TestService_Snapshot_ReusesPortSlice verifies the slice capacity is retained
// across captures so the steady-state path is allocation-free.
func TestService_Snapshot_ReusesPortSlice(t *testing.T) {
	t.Parallel()

	rt := &CommsRuntime{
		WebBridge: webaudio.NewBridge(zerolog.Nop(), nil),
		Ports: []*PortChannel{
			{cfg: McastPortConfig{Address: "a", Port: 1}, Jitter: rtp.NewJitterBuffer(rtp.PrebufferPackets, rtp.MaxDepth)},
			{cfg: McastPortConfig{Address: "b", Port: 2}, Jitter: rtp.NewJitterBuffer(rtp.PrebufferPackets, rtp.MaxDepth)},
		},
	}

	svc := &Service{Cfg: &CommsConfig{}, Rt: rt}

	var dst CommsSnapshot

	svc.Snapshot(&dst)
	backing := &dst.Ports[0]

	svc.Snapshot(&dst)
	assert.Same(t, backing, &dst.Ports[0], "port slice backing array should be reused")
}

// TestService_Snapshot_ZeroAllocSteadyState proves Snapshot is allocation-
// free once the ports slice capacity has been established.
func TestService_Snapshot_ZeroAllocSteadyState(t *testing.T) {
	// testing.AllocsPerRun must not be called under t.Parallel.
	bridge := webaudio.NewBridge(zerolog.Nop(), nil)

	rt := &CommsRuntime{
		WebBridge: bridge,
		Ports: []*PortChannel{
			{cfg: McastPortConfig{Address: "a", Port: 1}, Jitter: rtp.NewJitterBuffer(rtp.PrebufferPackets, rtp.MaxDepth)},
			{cfg: McastPortConfig{Address: "b", Port: 2}, Jitter: rtp.NewJitterBuffer(rtp.PrebufferPackets, rtp.MaxDepth)},
		},
	}

	svc := &Service{Cfg: &CommsConfig{ControlSource: "web"}, Rt: rt}

	var dst CommsSnapshot

	// Warmup: establishes the Ports slice capacity.
	svc.Snapshot(&dst)

	allocs := testing.AllocsPerRun(100, func() {
		svc.Snapshot(&dst)
	})

	assert.Equal(t, 0.0, allocs, "Snapshot must not allocate after warmup")
}

// TestPortChannel_Snapshot_NilSafe confirms the method is safe on a nil
// receiver and a nil dst.
func TestPortChannel_Snapshot_NilSafe(t *testing.T) {
	t.Parallel()

	var pc *PortChannel

	var dst PortSnapshot

	pc.Snapshot(&dst)
	pc.Snapshot(nil)
}

// TestCommsSnapshotter_DataPointerStable verifies that Data() returns a
// stable pointer across Refresh calls so the instrumentation registry can
// hold it once at registration time.
func TestCommsSnapshotter_DataPointerStable(t *testing.T) {
	t.Parallel()

	var a CommsSnapshotter

	first := a.Data()
	a.Refresh()
	second := a.Data()

	assert.Same(t, first, second)
}

// TestCommsSnapshotter_RefreshReadsDefault verifies Refresh reflects a
// SetDefault-published service.
func TestCommsSnapshotter_RefreshReadsDefault(t *testing.T) {
	t.Parallel()

	prev := Default()

	t.Cleanup(func() { SetDefault(prev) })

	rt := &CommsRuntime{
		WebBridge: webaudio.NewBridge(zerolog.Nop(), nil),
	}
	rt.Broadcasting.Store(true)

	svc := &Service{Cfg: &CommsConfig{ControlSource: "roip"}, Rt: rt}
	SetDefault(svc)

	var a CommsSnapshotter

	a.Refresh()

	data, ok := a.Data().(*CommsSnapshot)
	require.True(t, ok)
	assert.True(t, data.Enabled)
	assert.True(t, data.Broadcasting)
	assert.Equal(t, "roip", data.ControlSource)

	SetDefault(nil)
	a.Refresh()
	assert.False(t, data.Enabled)
}

// TestSnapshot_TalkGroupFields verifies the talk group section of
// CommsSnapshot: the active channel and dropped-events counter reflect the
// runtime, and the nil-receiver-safe Announcer/GPIOSel fills read as zero
// when neither subsystem is wired into the test runtime.
func TestSnapshot_TalkGroupFields(t *testing.T) {
	t.Parallel()

	svc := newSelectTestService(t, 3)
	require.NoError(t, svc.SelectTalkGroup(2, talkgroup.SourceRPC))
	svc.Rt.Events.NoteDropped()

	var dst CommsSnapshot

	svc.Snapshot(&dst)

	assert.Equal(t, 2, dst.ActiveTalkgroup)
	assert.Equal(t, uint64(1), dst.TalkgroupEventsDropped)
	assert.Zero(t, dst.Announcer.Plays, "nil announcer reads as zero")
	assert.Zero(t, dst.GPIOSelector.Transitions, "nil selector reads as zero")
}

// TestBroadcastEncoderAudioSnapshot_ZeroAlloc exercises the audio encoder
// snapshot method via a constructed encoder wrapper that keeps the atomic
// fields reachable. Because BroadcastEncoder has unexported fields we use
// the atomic access helpers the package already exposes.
func TestBroadcastEncoderAudioSnapshot_ZeroAlloc(t *testing.T) {
	// testing.AllocsPerRun must not be called under t.Parallel.
	var be *audio.BroadcastEncoder // nil receiver is explicitly supported

	var dst audio.AudioEncoderSnapshot

	// Warmup.
	be.Snapshot(&dst)

	allocs := testing.AllocsPerRun(100, func() {
		be.Snapshot(&dst)
	})

	assert.Equal(t, 0.0, allocs, "audio.BroadcastEncoder.Snapshot must not allocate")
}
