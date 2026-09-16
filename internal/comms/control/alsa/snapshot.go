package alsa

// MixerSnapshot is the audio_mixer instrumentation section. Values
// reflect the daemon's last successful mixer read/write, not live
// hardware: out-of-band alsamixer or VOL button changes appear only
// after the next API read. Field semantics are documented in
// docs/instrumentation-snapshot.md — keep that file in sync when adding
// or renaming fields here.
type MixerSnapshot struct {
	// SpeakerVolumePct is the last known playback volume percent;
	// -1 = never read or control absent.
	SpeakerVolumePct int `json:"speaker_volume_pct"`
	// MicVolumePct is the last known capture volume percent;
	// -1 = never read or control absent.
	MicVolumePct int `json:"mic_volume_pct"`
	// AGCKnown reports whether the AGC switch has been observed at all;
	// when false, AGCEnabled is meaningless.
	AGCKnown bool `json:"agc_known"`
	// AGCEnabled is the last known Auto Gain Control state.
	AGCEnabled bool `json:"agc_enabled"`
}

// Snapshot fills dst from the atomic cache. Zero allocations.
func (v *Volume) Snapshot(dst *MixerSnapshot) {
	dst.SpeakerVolumePct = int(v.lastSpeakerPct.Load()) - 1
	dst.MicVolumePct = int(v.lastMicPct.Load()) - 1

	agc := v.lastAGC.Load()
	dst.AGCKnown = agc != 0
	dst.AGCEnabled = agc == 2
}

// MixerSnapshotter adapts a Volume to instrumentation.Snapshotter.
type MixerSnapshotter struct {
	V    *Volume
	data MixerSnapshot
}

// Refresh implements instrumentation.Snapshotter via atomic loads only.
func (s *MixerSnapshotter) Refresh() {
	if s.V == nil {
		s.data = MixerSnapshot{SpeakerVolumePct: -1, MicVolumePct: -1}

		return
	}

	s.V.Snapshot(&s.data)
}

// Data implements instrumentation.Snapshotter.
func (s *MixerSnapshotter) Data() any { return &s.data }
