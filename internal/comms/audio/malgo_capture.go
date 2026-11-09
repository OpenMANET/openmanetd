package audio

import (
	"fmt"
	"time"
	"unsafe"

	"github.com/gen2brain/malgo"

	"github.com/openmanet/openmanetd/internal/comms/audiopool"
	"github.com/openmanet/openmanetd/internal/comms/device"
)

// malgoCaptureStream wraps a malgo input device as a captureStream. It
// owns no goroutines of its own — all audio work runs on the miniaudio
// callback thread, which invokes the configured onFrame with an int16
// view of the same buffer miniaudio allocated (reinterpret via
// unsafe.Slice; safe because FormatS16 guarantees 2-byte alignment, the
// host is little-endian, and the buffer outlives the callback call).
//
// The stream owns a small int16 accumulator that re-aligns variable-
// length malgo callbacks onto fixed audiopool.FrameSize chunks. ALSA's
// USB audio class driver typically rounds period sizes up to a power of
// two (e.g. it gives us 1024-frame periods when we ask for 960), so the
// raw malgo callback length cannot be assumed to equal the Opus encoder
// frame. The accumulator hides that mismatch from BroadcastEncoder,
// which still sees one 960-sample frame per onFrame call.
type malgoCaptureStream struct {
	dev       *malgo.Device
	accum     []int16
	info      streamInfo
	accumLen  int
	chunkSize int
}

// openMalgoCapture configures and initializes a malgo capture device in
// FormatS16. The returned captureStream has not been started yet —
// audio.BroadcastEncoder.Start kicks off the callback thread.
func openMalgoCapture(
	ctx *malgo.AllocatedContext,
	dev device.AudioDeviceInfo,
	sampleRate uint32,
	channels uint32,
	periodFrames uint32,
	periods uint32,
	onFrame func(samples []int16),
) (*malgoCaptureStream, error) {
	if ctx == nil {
		return nil, fmt.Errorf("malgo capture: nil context")
	}

	cfg := malgo.DefaultDeviceConfig(malgo.Capture)
	cfg.Capture.Format = malgo.FormatS16
	cfg.Capture.Channels = channels
	cfg.Capture.DeviceID = dev.ID.Pointer()
	cfg.SampleRate = sampleRate
	cfg.PeriodSizeInFrames = periodFrames
	// Conservative profile (vs. LowLatency) lets miniaudio honor the
	// requested period size instead of clamping it to its internal
	// low-latency ceiling. On USB audio class devices the LowLatency
	// profile produced a 1024*3 ring regardless of the requested period,
	// causing poll() failures and capture gaps far over the 20 ms frame
	// budget. Periods=4 gives ALSA a deeper ring (period * 4) so USB
	// scheduling jitter has somewhere to absorb without underrunning.
	cfg.PerformanceProfile = malgo.Conservative
	cfg.Periods = periods
	cfg.Alsa.NoMMap = 0
	applyBluetoothTiming(&cfg, dev.Name)

	stream := &malgoCaptureStream{
		accum:     make([]int16, audiopool.FrameSize),
		chunkSize: audiopool.FrameSize,
		info: streamInfo{
			InputLatency: periodFramesToDuration(periodFrames, sampleRate),
		},
	}

	onData := func(_, in []byte, frameCount uint32) {
		if len(in) == 0 || frameCount == 0 {
			return
		}

		src := int16View(in, int(frameCount)*int(channels))
		captureChunk(stream, src, onFrame)
	}

	d, err := malgo.InitDevice(ctx.Context, cfg, malgo.DeviceCallbacks{Data: onData})
	if err != nil {
		return nil, fmt.Errorf("malgo init capture device: %w", err)
	}

	stream.dev = d

	return stream, nil
}

// captureChunk re-aligns a single malgo callback's worth of samples onto
// stream.chunkSize boundaries, invoking onFrame for each complete chunk.
// Pulled out of the onData closure so the chunker can be unit-tested
// against synthetic input without opening a real malgo device.
//
// Single-producer: must only ever be called from the miniaudio worker
// thread that owns stream.accum / stream.accumLen.
func captureChunk(stream *malgoCaptureStream, src []int16, onFrame func(samples []int16)) {
	if len(src) == 0 {
		return
	}

	// Fast path: nothing buffered AND src is an exact multiple of the
	// chunk size. Emit each chunk in place without copying through the
	// accumulator. This is what fires when the underlying ALSA period
	// happens to be 960 (or any multiple), so a well-behaved backend
	// pays no extra cost.
	if stream.accumLen == 0 && len(src)%stream.chunkSize == 0 {
		for off := 0; off < len(src); off += stream.chunkSize {
			onFrame(src[off : off+stream.chunkSize])
		}

		return
	}

	// Slow path: glue accum + src and emit chunks until src is drained.
	// onFrame is BroadcastEncoder.captureCallback, which copies the slice
	// into its own pooled frame before returning, so reusing
	// stream.accum across iterations is safe.
	for len(src) > 0 {
		space := stream.chunkSize - stream.accumLen

		n := min(len(src), space)

		copy(stream.accum[stream.accumLen:], src[:n])
		stream.accumLen += n
		src = src[n:]

		if stream.accumLen == stream.chunkSize {
			onFrame(stream.accum)
			stream.accumLen = 0
		}
	}
}

// Start kicks off the miniaudio callback thread for this capture device.
func (m *malgoCaptureStream) Start() error {
	if err := m.dev.Start(); err != nil {
		return fmt.Errorf("malgo capture start: %w", err)
	}

	return nil
}

// Stop halts the capture device without releasing its resources.
func (m *malgoCaptureStream) Stop() error {
	if err := m.dev.Stop(); err != nil {
		return fmt.Errorf("malgo capture stop: %w", err)
	}

	return nil
}

// Close uninitializes the capture device. malgo's Uninit implicitly
// stops the device, so calling Stop first is optional.
func (m *malgoCaptureStream) Close() error {
	m.dev.Uninit()

	return nil
}

// Info returns the configured period-derived latency. miniaudio does
// not expose a negotiated runtime latency so this is the value we asked
// for at open time, not a value read back from ALSA.
func (m *malgoCaptureStream) Info() streamInfo { return m.info }

// int16View reinterprets a byte slice as an int16 slice for the
// duration of a single audio callback. The callback contract with
// miniaudio is that the buffer pointer and length are stable for the
// call, so the returned view is valid for the rest of the callback but
// MUST NOT be retained past return.
//
// Alignment: miniaudio allocates the capture buffer from its own pool
// with at least int16 (2-byte) alignment when FormatS16 is in effect,
// so casting the first byte pointer to *int16 is safe on every
// architecture we build for (amd64, arm64, mipsle).
func int16View(b []byte, samples int) []int16 {
	if len(b) == 0 || samples == 0 {
		return nil
	}

	return unsafe.Slice((*int16)(unsafe.Pointer(&b[0])), samples)
}

// periodFramesToDuration converts a PeriodSizeInFrames value to the
// equivalent wall-clock duration at the given sample rate. Used to fill
// streamInfo.InputLatency / OutputLatency with a diagnostic that maps
// directly onto the configured period size.
func periodFramesToDuration(frames, sampleRate uint32) time.Duration {
	if sampleRate == 0 {
		return 0
	}

	return time.Duration(frames) * time.Second / time.Duration(sampleRate)
}
