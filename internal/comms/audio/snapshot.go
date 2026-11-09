package audio

// AudioEncoderSnapshot is a point-in-time copy of a BroadcastEncoder's
// runtime counters. Field semantics are documented in
// docs/instrumentation-snapshot.md — keep that file in sync when
// adding or renaming fields here.
type AudioEncoderSnapshot struct {
	// CapturePeak and PreEncodePeak are maximum PCM magnitudes before and
	// after gain for queued TX frames in the current PTT cycle (0..32768).
	CapturePeak   int32 `json:"capture_peak"`
	PreEncodePeak int32 `json:"pre_encode_peak"`

	// FramesCaptured is the monotonic count of audio frames delivered by
	// the capture callback since the start of the current PTT cycle
	// (reset in SetTxEnabled(true)). Approximately 50 frames per second
	// at a 20 ms frame rate.
	FramesCaptured int64 `json:"frames_captured"`
	// FramesDropped is the count of captured frames that the audio
	// callback could not hand off to the encode goroutine because the
	// encCh channel was full. Any non-zero value during active TX
	// indicates the encode loop is behind.
	FramesDropped int64 `json:"frames_dropped"`
	// FramesEncoded is the count of successfully Opus-encoded and
	// forwarded frames since the start of the current PTT cycle.
	FramesEncoded int64 `json:"frames_encoded"`
	// EncodeErrors is the count of Opus encode failures since the start
	// of the current PTT cycle.
	EncodeErrors int64 `json:"encode_errors"`
	// CaptureGapMaxNs is the maximum observed inter-arrival gap between
	// successive capture callbacks, in nanoseconds. Values meaningfully
	// larger than the 20 ms frame budget (20_000_000) indicate audio
	// thread preemption.
	CaptureGapMaxNs int64 `json:"capture_gap_max_ns"`
	// CaptureLateCount is the number of capture callbacks that arrived
	// late relative to the previous callback timestamp.
	CaptureLateCount int64 `json:"capture_late_count"`
	// EncodeDurMaxNs is the maximum Opus encode duration observed since
	// the start of the current PTT cycle, in nanoseconds. Compare against
	// the 20 ms frame budget.
	EncodeDurMaxNs int64 `json:"encode_dur_max_ns"`
	// EncodeDurSumNs is the sum of Opus encode durations in nanoseconds.
	// Use EncodeDurSumNs / EncodeDurCount for a mean encode time.
	EncodeDurSumNs int64 `json:"encode_dur_sum_ns"`
	// EncodeDurCount is the number of encode durations summed into
	// EncodeDurSumNs.
	EncodeDurCount int64 `json:"encode_dur_count"`
	// LastCaptureNs is the monotonic-clock timestamp (time.Now().UnixNano())
	// of the most recent capture callback. 0 means "no callback since
	// the last cycle reset".
	LastCaptureNs int64 `json:"last_capture_ns"`
	// TxEnabled reflects whether the encoder is currently gated ON. When
	// false, captured frames never reach the Opus encoder.
	TxEnabled bool `json:"tx_enabled"`
	// OverBudgetWarned is set the first time the encoder observes an
	// encode duration that exceeds the frame budget in the current PTT
	// cycle. It is reset on the next SetTxEnabled(true).
	OverBudgetWarned bool `json:"over_budget_warned"`
}

// Snapshot copies the current counter values into dst using atomic loads.
// Safe to call from any goroutine concurrently with the audio callback
// and the encode worker. Nil-safe on both receiver and dst.
//
// Snapshot MUST NOT allocate — this is verified by
// TestBroadcastEncoder_SnapshotZeroAlloc via testing.AllocsPerRun.
func (be *BroadcastEncoder) Snapshot(dst *AudioEncoderSnapshot) {
	if be == nil || dst == nil {
		return
	}

	dst.CapturePeak = be.capturePeak.Load()
	dst.PreEncodePeak = be.preEncodePeak.Load()
	dst.FramesCaptured = be.framesCaptured.Load()
	dst.FramesDropped = be.framesDropped.Load()
	dst.FramesEncoded = be.framesEncoded.Load()
	dst.EncodeErrors = be.encodeErrors.Load()
	dst.CaptureGapMaxNs = be.captureGapMaxNs.Load()
	dst.CaptureLateCount = be.captureLateCount.Load()
	dst.EncodeDurMaxNs = be.encodeDurMaxNs.Load()
	dst.EncodeDurSumNs = be.encodeDurSumNs.Load()
	dst.EncodeDurCount = be.encodeDurCount.Load()
	dst.LastCaptureNs = be.lastCaptureNs.Load()
	dst.TxEnabled = be.txEnabled.Load()
	dst.OverBudgetWarned = be.overBudgetWarned.Load()
}
