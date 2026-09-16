package comms

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestInitAudioIO_HardwareFailureIsNonFatal exercises the lifecycle invariant
// that powers the OpenVLM "RX/TX toggles must work no matter the control
// source" requirement: when the local audio hardware fails to initialize,
// the comms subsystem still stands up enough state for the WebUI to toggle
// per-channel send/receive flags via the RPC path.
//
// Before the fix in lifecycle.go, a startHardwareAudio error would cause
// Start to return early; the deferred SetDefault(nil) at the top of Start
// would clear the published service, and every subsequent
// SetSendTalkGroup / SetReceiveTalkGroup RPC would return ErrNotRunning.
// The WebUI bridge silently swallows that error, so the user saw a UI
// that "did nothing".
func TestInitAudioIO_HardwareFailureIsNonFatal(t *testing.T) {
	cfg := &CommsConfig{
		Log:           zerolog.Nop(),
		ControlSource: defaultCtrlSrc, // openvlm — the user-reported case
		startHardwareAudioFn: func(_ *CommsRuntime) (func(), error) {
			return nil, errors.New("simulated: no ALSA card / dongle unplugged")
		},
	}

	rt := &CommsRuntime{
		Ports: []*PortChannel{
			{cfg: McastPortConfig{Address: "239.0.0.1", Port: 5004, Send: true, Receive: true}},
			{cfg: McastPortConfig{Address: "239.0.0.2", Port: 5006, Send: true, Receive: true}},
		},
	}

	rt.Ports[0].SendEnabled.Store(true)
	rt.Ports[0].ReceiveEnabled.Store(true)
	rt.Ports[1].SendEnabled.Store(false)
	rt.Ports[1].ReceiveEnabled.Store(false)

	cleanup := cfg.initAudioIO(context.Background(), rt)

	assert.Nil(t, cleanup, "audio failure must not return a cleanup func to defer")
	assert.Nil(t, rt.Broadcast(), "BroadcastStream must remain nil so transmit.go's nil guards engage")
	assert.Nil(t, rt.WebBridge, "WebBridge must stay nil for non-web control sources")

	// Publish the service the same way Start does so Default()/handler paths see it.
	svc := &Service{Cfg: cfg, Rt: rt}
	SetDefault(svc)
	t.Cleanup(func() { SetDefault(nil) })

	require.NoError(t, svc.EnableTalkGroupSend(1, true), "TX toggle must succeed despite missing hardware")
	require.NoError(t, svc.EnableTalkGroupReceive(0, false), "RX toggle must succeed despite missing hardware")

	states, err := svc.TalkGroupStates()
	require.NoError(t, err)
	require.Len(t, states, 2)
	assert.True(t, states[1].SendEnabled, "EnableTalkGroupSend(1, true) must be observable in TalkGroupStates")
	assert.False(t, states[0].ReceiveEnabled, "EnableTalkGroupReceive(0, false) must be observable in TalkGroupStates")
}

// TestInitAudioIO_HardwareSuccessReturnsCleanup verifies the happy-path
// counterpart of the failure test: a successful audio init returns the
// cleanup func so Start can defer it.
func TestInitAudioIO_HardwareSuccessReturnsCleanup(t *testing.T) {
	called := 0

	cfg := &CommsConfig{
		Log:           zerolog.Nop(),
		ControlSource: defaultCtrlSrc,
		startHardwareAudioFn: func(_ *CommsRuntime) (func(), error) {
			return func() { called++ }, nil
		},
	}

	rt := &CommsRuntime{}

	cleanup := cfg.initAudioIO(context.Background(), rt)
	require.NotNil(t, cleanup)

	cleanup()
	assert.Equal(t, 1, called)
}

// TestInitAudioIO_WebModeBuildsBridge verifies that web mode bypasses the
// hardware audio path entirely and constructs a WebBridge instead — the
// existing behavior, locked down so the refactor in initAudioIO does not
// regress it.
func TestInitAudioIO_WebModeBuildsBridge(t *testing.T) {
	cfg := &CommsConfig{
		Log:           zerolog.Nop(),
		ControlSource: controlSourceWeb,
		startHardwareAudioFn: func(_ *CommsRuntime) (func(), error) {
			t.Fatal("startHardwareAudioFn must not be called in web mode")

			return nil, nil
		},
	}

	rt := &CommsRuntime{}

	cleanup := cfg.initAudioIO(context.Background(), rt)

	assert.Nil(t, cleanup, "web mode has no malgo lifecycle to clean up")
	assert.NotNil(t, rt.WebBridge, "web mode must construct a WebBridge")
}

// TestInitAudioIO_RetriesThenSucceeds verifies the bounded startup retry:
// a transient ALSA failure (e.g. dmix EPIPE while USB settles at boot) on
// the first attempts must not permanently disable local audio.
func TestInitAudioIO_RetriesThenSucceeds(t *testing.T) {
	calls := 0

	cfg := &CommsConfig{
		Log:           zerolog.Nop(),
		ControlSource: defaultCtrlSrc,
		startHardwareAudioFn: func(_ *CommsRuntime) (func(), error) {
			calls++
			if calls < 3 {
				return nil, errors.New("simulated: miniaudio: Broken pipe")
			}

			return func() {}, nil
		},
	}

	rt := &CommsRuntime{}

	cleanup := cfg.initAudioIO(context.Background(), rt)

	require.NotNil(t, cleanup, "third attempt succeeds; cleanup must be returned")
	assert.Equal(t, 3, calls)
}

// TestInitAudioIO_AllAttemptsFail verifies the retry loop is bounded at
// audioInitAttempts and that exhaustion preserves the existing non-fatal
// contract (nil cleanup, nil broadcast stream).
func TestInitAudioIO_AllAttemptsFail(t *testing.T) {
	calls := 0

	cfg := &CommsConfig{
		Log:           zerolog.Nop(),
		ControlSource: defaultCtrlSrc,
		startHardwareAudioFn: func(_ *CommsRuntime) (func(), error) {
			calls++

			return nil, errors.New("simulated: persistent failure")
		},
	}

	rt := &CommsRuntime{}

	cleanup := cfg.initAudioIO(context.Background(), rt)

	assert.Nil(t, cleanup)
	assert.Equal(t, audioInitAttempts, calls)
}

// TestInitAudioIO_ContextCanceledStopsRetry verifies shutdown during the
// inter-attempt delay aborts immediately instead of finishing the retry
// budget. The delay is deliberately huge: if cancellation were broken the
// test would hang and the suite timeout would catch it.
func TestInitAudioIO_ContextCanceledStopsRetry(t *testing.T) {
	calls := 0

	cfg := &CommsConfig{
		Log:                 zerolog.Nop(),
		ControlSource:       defaultCtrlSrc,
		audioInitRetryDelay: time.Hour,
		startHardwareAudioFn: func(_ *CommsRuntime) (func(), error) {
			calls++

			return nil, errors.New("simulated: failure")
		},
	}

	rt := &CommsRuntime{}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cleanup := cfg.initAudioIO(ctx, rt)

	assert.Nil(t, cleanup)
	assert.Equal(t, 1, calls, "canceled context must stop after the first attempt")
}

// TestInitAudioIO_FailureLogIncludesALSACard verifies the operator-facing
// failure log names the ALSA card the daemon targeted — without it, "audio
// out=Default Audio Device" hides which card dmix actually resolved to.
func TestInitAudioIO_FailureLogIncludesALSACard(t *testing.T) {
	t.Setenv("ALSA_CARD", "1")

	var buf bytes.Buffer

	cfg := &CommsConfig{
		Log:           zerolog.New(&buf),
		ControlSource: defaultCtrlSrc,
		startHardwareAudioFn: func(_ *CommsRuntime) (func(), error) {
			return nil, errors.New("simulated: miniaudio: Broken pipe")
		},
	}

	rt := &CommsRuntime{}

	cleanup := cfg.initAudioIO(context.Background(), rt)

	assert.Nil(t, cleanup)
	assert.Contains(t, buf.String(), `"alsa_card":"1"`)
}

func TestTryAudioRecovery_InvokesMixerStartupOnSuccess(t *testing.T) {
	calls := 0
	cfg := &CommsConfig{
		Log: zerolog.Nop(),
		startHardwareAudioFn: func(*CommsRuntime) (func(), error) {
			return func() {}, nil
		},
		AudioMixerStartup: func() { calls++ },
	}

	rt := &CommsRuntime{}
	ok := cfg.tryAudioRecovery(rt, 1)

	require.True(t, ok)
	assert.Equal(t, 1, calls, "successful recovery must re-apply mixer levels (USB replug resets the card)")
}

func TestTryAudioRecovery_SkipsMixerStartupOnFailure(t *testing.T) {
	calls := 0
	cfg := &CommsConfig{
		Log: zerolog.Nop(),
		startHardwareAudioFn: func(*CommsRuntime) (func(), error) {
			return nil, errors.New("no device")
		},
		detectALSACardFn:  func() {},
		AudioMixerStartup: func() { calls++ },
	}

	rt := &CommsRuntime{}
	ok := cfg.tryAudioRecovery(rt, 1)

	require.False(t, ok)
	assert.Equal(t, 0, calls)
}

func TestApplyMixerStartup_NilSafe(t *testing.T) {
	cfg := &CommsConfig{Log: zerolog.Nop()}
	cfg.applyMixerStartup() // must not panic
}

func TestStartGPIOSelector_SkipsWhenUnsupported(t *testing.T) {
	svc := newSelectTestService(t, 2)
	svc.Cfg.GPIOSelectorEnable = true
	svc.Cfg.gpioSelectorSupportedFn = func() bool { return false }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	svc.Cfg.startGPIOSelector(ctx, svc)
	assert.Nil(t, svc.Rt.GPIOSel)
}

func TestStartGPIOSelector_SkipsWhenDisabled(t *testing.T) {
	svc := newSelectTestService(t, 2)
	svc.Cfg.GPIOSelectorEnable = false
	svc.Cfg.gpioSelectorSupportedFn = func() bool { return true }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	svc.Cfg.startGPIOSelector(ctx, svc)
	assert.Nil(t, svc.Rt.GPIOSel)
}
