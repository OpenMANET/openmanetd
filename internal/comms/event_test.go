package comms

import (
	"context"
	"sync"
	"testing"
	"time"

	evdev "github.com/gvalkov/golang-evdev"
	"github.com/openmanet/openmanetd/internal/comms/control"
	"github.com/rs/zerolog"
)

func TestEvdevSource_PTTToggle(t *testing.T) {
	src := &mockEventSource{ch: make(chan control.PTTEvent, 4)}
	src.ch <- control.PTTToggle

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	ch := src.Events(ctx)
	select {
	case ev := <-ch:
		if ev != control.PTTToggle {
			t.Errorf("expected PTTToggle; got %v", ev)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timed out")
	}
}

func TestNewNanoPTTSource_NonNil(t *testing.T) {
	var dev *evdev.InputDevice

	src := control.NewNanoPTTSource(dev, "any", zerolog.Nop())
	if src == nil {
		t.Error("NewNanoPTTSource should not return nil")
	}
}

func TestNormalizeControlSource(t *testing.T) {
	cases := []struct{ input, want string }{
		{"openvlm", "openvlm"},
		{"OPENVLM", "openvlm"},
		{"  openvlm  ", "openvlm"},
		{"nanoptt", "nanoptt"},
		{"NANOPTT", "nanoptt"},
		{"bs22", "bs22"},
		{"BS22", "bs22"},
		{"bluealsa_xevent", "bluealsa_xevent"},
		{"BLUEALSA_XEVENT", "bluealsa_xevent"},
		{"roip", "roip"},
		{"ROIP", "roip"},
		{"  roip  ", "roip"},
		{"web", "web"},
		{"WEB", "web"},
		{"  web  ", "web"},
		{"", "openvlm"},
		{"unknown", "openvlm"},
	}
	for _, tc := range cases {
		got := normalizeControlSource(tc.input)
		if got != tc.want {
			t.Errorf("normalizeControlSource(%q) = %q; want %q", tc.input, got, tc.want)
		}
	}
}

func TestPTTEvent_Values(t *testing.T) {
	if control.PTTDown != 0 {
		t.Errorf("PTTDown = %d; want 0", control.PTTDown)
	}

	if control.PTTUp != 1 {
		t.Errorf("PTTUp = %d; want 1", control.PTTUp)
	}

	if control.PTTToggle != 2 {
		t.Errorf("PTTToggle = %d; want 2", control.PTTToggle)
	}
}

func TestMockEventSource_ClosedChannel(t *testing.T) {
	src := &mockEventSource{ch: make(chan control.PTTEvent)}
	close(src.ch)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	ch := src.Events(ctx)
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("expected closed channel")
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timed out")
	}
}

func TestWebEventSource_RunIntegration(t *testing.T) {
	ws := control.NewWebEventSource(zerolog.Nop())
	stream := &mockStream{}
	rt := newRunRuntime(stream)
	cfg := &CommsConfig{Log: zerolog.Nop()}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		defer close(done)

		cfg.Run(ctx, rt, ws)
	}()

	// Give the Run loop time to start before pushing events.
	time.Sleep(50 * time.Millisecond)

	ws.Push(control.PTTDown)

	// Wait for the broadcast stream to be started (beginTransmission sleeps 200ms).
	time.Sleep(300 * time.Millisecond)

	ws.Push(control.PTTUp)

	// Wait for the Run loop to process PTTUp.
	time.Sleep(100 * time.Millisecond)

	// Cancel context to terminate Run.
	cancel()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Error("Run did not exit in time")
	}

	rt.Ports[0].Receiver.Close()

	if stream.txEnableCalls != 1 {
		t.Errorf("SetTxEnabled(true) called %d times, want 1", stream.txEnableCalls)
	}

	if stream.txDisableCalls != 1 {
		t.Errorf("SetTxEnabled(false) called %d times, want 1", stream.txDisableCalls)
	}
}

func TestMockEventSource_ConcurrentDelivery(t *testing.T) {
	const n = 50

	src := &mockEventSource{ch: make(chan control.PTTEvent, n)}
	for i := 0; i < n; i++ {
		src.ch <- control.PTTToggle
	}

	close(src.ch)
	ch := src.Events(context.Background())

	var (
		wg    sync.WaitGroup
		count int
		mu    sync.Mutex
	)

	wg.Add(1)

	go func() {
		defer wg.Done()

		for range ch {
			mu.Lock()
			count++
			mu.Unlock()
		}
	}()

	wg.Wait()

	if count != n {
		t.Errorf("expected %d; got %d", n, count)
	}
}
