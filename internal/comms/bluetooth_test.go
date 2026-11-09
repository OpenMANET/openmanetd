package comms

import (
	"context"
	"github.com/openmanet/openmanetd/internal/bluetooth"
	"github.com/rs/zerolog"
	"testing"

	"github.com/openmanet/openmanetd/internal/comms/control"
	"github.com/stretchr/testify/require"
)

func TestBluetoothSetupCannotTransmit(t *testing.T) {
	session := &fakeBluetoothSession{connected: true}
	cfg := newSilentComms()
	cfg.ControlSource = controlSourceBS22
	cfg.BluetoothSession = session
	cfg.PttStartDelayMs = -1
	stream := &mockStream{}
	rt := newTestRuntime(stream)
	src := &mockEventSource{ch: make(chan control.PTTEvent, 2)}
	src.ch <- control.PTTDown
	src.ch <- control.PTTUp
	close(src.ch)
	cfg.Run(context.Background(), rt, src)
	require.Equal(t, []bool{true, false, false}, session.presses)
	require.Zero(t, stream.txEnableCalls, "setup opened microphone TX gate")
	require.False(t, rt.Broadcasting.Load())
	// Even an already encoded frame must not bypass the setup gate.
	cfg.sendToAllPorts(rt, []byte{1, 2, 3})
	sender := rt.Ports[0].RTPSess.(*mockRTPSender)
	require.Empty(t, sender.Payloads)
}

func TestHardwareAudioCleanupRevokesReadinessBeforeClosingStreams(t *testing.T) {
	session := bluetooth.NewSession(true, false)
	_, err := session.Begin()
	require.NoError(t, err)
	lease := session.Lease()
	lease.ObserveAudioReady(true)
	cfg := &CommsConfig{BluetoothSession: lease}
	port := &PortChannel{}
	port.setPlaybackStream(&fakeAudioStream{})
	rt := &CommsRuntime{Ports: []*PortChannel{port}}
	calls := 0
	cleanup := cfg.hardwareAudioCleanup(rt, func() {
		calls++
		require.False(t, session.Snapshot().AudioReady)
		require.False(t, port.playbackStreamInstalled(), "closed hardware must be detached first")
	})
	cleanup()
	require.Equal(t, 1, calls, "cleanup must reach the hardware closer without recursion")
}

func TestBluetoothTransmissionRequiresExplicitActivation(t *testing.T) {
	cfg := newSilentComms()
	cfg.ControlSource = controlSourceBS22
	cfg.PttStartDelayMs = -1
	stream := &mockStream{}
	rt := newTestRuntime(stream)
	cfg.beginTransmission(rt)
	require.Zero(t, stream.txEnableCalls, "missing lifecycle must fail closed")
	cfg.BluetoothSession = &fakeBluetoothSession{connected: true, active: true}
	cfg.beginTransmission(rt)
	require.Equal(t, 1, stream.txEnableCalls)
	cfg.sendToAllPorts(rt, []byte{1})
	require.Len(t, rt.Ports[0].RTPSess.(*mockRTPSender).Payloads, 1)
}

func TestBluetoothStartRequiresConsentBeforeHardware(t *testing.T) {
	cfg := newSilentComms()
	cfg.Enable = true
	cfg.ControlSource = controlSourceBS22
	require.ErrorIs(t, cfg.Start(context.Background()), ErrBluetoothDisabled)
}

func TestSelectedBluetoothAudioUsesExactAccessory(t *testing.T) {
	cfg := NewComms(CommsConfig{ControlSource: controlSourceBS22, BluetoothDevicePath: "/org/bluez/hci0/dev_41_42_86_99_1D_61"})
	cfg.applyDefaults()
	require.Equal(t, "bluealsa:DEV=41:42:86:99:1D:61,PROFILE=sco", cfg.BluetoothInputDevice)
	require.Equal(t, cfg.BluetoothInputDevice, cfg.BluetoothOutputDevice)
	cfg.BluetoothDevicePath = "/org/bluez/hci0/dev_41_42_86_99_1D_61/service1"
	_, err := cfg.selectedBluetoothAudioSpec()
	require.Error(t, err)
}

func TestBluetoothRuntimeRestoresPreviousComms(t *testing.T) {
	started := make(chan *CommsConfig, 3)
	m := &CommsManager{logger: zerolog.Nop(), buildFn: func() *CommsConfig { return NewComms(CommsConfig{ControlSource: defaultCtrlSrc}) }, startFn: func(cc *CommsConfig) startFunc {
		started <- cc
		return func(ctx context.Context) error { <-ctx.Done(); return nil }
	}}
	t.Cleanup(m.Disable)
	require.NoError(t, m.Enable())
	require.Equal(t, defaultCtrlSrc, (<-started).ControlSource)
	session := bluetooth.NewSession(true, false)
	_, err := session.Begin()
	require.NoError(t, err)
	require.NoError(t, m.StartBluetooth(context.Background(), bluetooth.Accessory{Path: "/org/bluez/hci0/dev_41_42_86_99_1D_61"}, session.Lease()))
	cfg := <-started
	require.Equal(t, controlSourceBS22, cfg.ControlSource)
	require.False(t, cfg.transmissionAllowed())
	require.Nil(t, cfg.AudioMixerStartup)
	session.Off()
	require.NoError(t, m.StopBluetooth())
	require.Equal(t, defaultCtrlSrc, (<-started).ControlSource)
}

func TestBluetoothSetupToneDoesNotOpenTX(t *testing.T) {
	cfg := newSilentComms()
	cfg.ControlSource = controlSourceBS22
	cfg.BluetoothSession = &fakeBluetoothSession{connected: true}
	requests := make(chan chan error, 1)
	cfg.testToneC = requests
	rt := newTestRuntime(&mockStream{})
	events := make(chan control.PTTEvent)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); cfg.Run(ctx, rt, &mockEventSource{ch: events}) }()
	t.Cleanup(func() { cancel(); close(events); <-done })
	reply := make(chan error, 1)
	requests <- reply
	require.NoError(t, <-reply)
	require.Equal(t, rt.BeepBufferStart, <-rt.Ports[0].PlaybackBuffer)
	require.False(t, rt.Broadcasting.Load())
	cfg.sendToAllPorts(rt, []byte{1})
	require.Empty(t, rt.Ports[0].RTPSess.(*mockRTPSender).Payloads)
}

func TestBluetoothChannelSteps(t *testing.T) {
	for _, tc := range []struct {
		name                string
		current, step, want int
		active, held        bool
	}{
		{"next", 1, 1, 2, true, false}, {"previous", 2, -1, 1, true, false},
		{"wrap next", 3, 1, 1, true, false}, {"wrap previous", 1, -1, 3, true, false},
		{"setup ignored", 1, 1, 1, false, false}, {"held PTT ignored", 1, 1, 1, true, true},
		{"invalid ignored", 1, 5, 1, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := newSelectTestService(t, 3)
			svc.Cfg.ControlSource = controlSourceBS22
			svc.Cfg.BluetoothSession = &fakeBluetoothSession{connected: true, active: tc.active}
			svc.Rt.ActiveChannel.Store(int32(tc.current))
			svc.Rt.Broadcasting.Store(tc.held)
			require.NoError(t, svc.Cfg.stepBluetoothChannel(svc.Rt, tc.step))
			require.Equal(t, tc.want, svc.ActiveTalkGroup())
			require.Equal(t, tc.held, svc.Rt.Broadcasting.Load())
			if tc.want != tc.current {
				for i, p := range svc.Rt.Ports {
					require.Equal(t, i+1 == tc.want, p.SendEnabled.Load())
					require.Equal(t, i+1 == tc.want, p.ReceiveEnabled.Load())
				}
			}
		})
	}
}
