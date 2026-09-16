// =============================================================================
// DeviceAudioPanel.jsx — Hardware mixer controls on the radio device itself
// =============================================================================
//
// Distinct from AudioControls ("Web Audio"), which only scales the
// browser's WebAudio graph: these sliders drive the ALSA mixer on the
// OpenVLM sound card via CommsService.GetAudioMixer / UpdateAudioMixer.
// State is polled so VOL+/VOL− hardware-button changes converge into the
// UI; slider writes commit on release, not per tick, to avoid hammering
// the config persist path.
//
// `pending` and `draggingRef` are keyed per control ('speaker' | 'mic' |
// 'agc') so that releasing one control never clobbers another control's
// in-flight drag or commit — see commit() below.
//
// A drag normally ends in commit() (pointerup / key-up) or cancelDrag()
// (pointercancel — e.g. a touch drag interrupted by scroll). Because a
// browser can drop both events (release outside the viewport, tab switch
// mid-drag), poll() additionally drops any drag flag that has suppressed
// MAX_SUPPRESSED_POLLS consecutive polls: a stuck flag would otherwise
// freeze the sliders at phantom values and block polling forever.

import React, { useState, useRef, useCallback } from 'react';
import { fetchAudioMixer, updateAudioMixer } from '../services/commsApi.js';
import { useVisibleInterval } from '../hooks/useVisibleInterval.js';

const POLL_MS = 5000;

// Consecutive polls a single drag may suppress before it is considered
// stuck; it is dropped on the poll after that (~20 s at POLL_MS). Any
// onChange tick resets the count, so a genuinely active drag is never
// dropped.
const MAX_SUPPRESSED_POLLS = 3;

// Keys that actually change a range input's value. onKeyUp fires for any
// key released while the slider has focus — including Tab, which merely
// moves focus onto the slider and must never commit the displayed value.
const SLIDER_COMMIT_KEYS = new Set([
  'ArrowUp',
  'ArrowDown',
  'ArrowLeft',
  'ArrowRight',
  'Home',
  'End',
  'PageUp',
  'PageDown',
]);

function isSliderCommitKey(key) {
  return SLIDER_COMMIT_KEYS.has(key);
}

export default function DeviceAudioPanel() {
  const [mixer, setMixer] = useState(null);     // last known AudioMixerState
  const [pending, setPending] = useState({});   // { speaker?, mic?, agc? } — optimistic per-control overrides
  const [error, setError] = useState(false);
  // Control keys currently mid-drag (sliders only), mapped to how many
  // polls each has suppressed since its last onChange tick.
  const draggingRef = useRef(new Map());

  // Discards one control's pending override and drag flag without
  // committing — the pointercancel path and the stale-drag safety net.
  const dropPending = useCallback((key) => {
    draggingRef.current.delete(key);
    setPending((p) => {
      if (!(key in p)) return p;
      const next = { ...p };
      delete next[key];
      return next;
    });
  }, []);

  const poll = useCallback(async () => {
    const state = await fetchAudioMixer();
    for (const [key, missed] of draggingRef.current) {
      if (missed >= MAX_SUPPRESSED_POLLS) {
        dropPending(key); // stuck drag — no release event ever arrived
      } else {
        draggingRef.current.set(key, missed + 1);
      }
    }
    if (draggingRef.current.size > 0) return; // never fight an active slider drag
    if (state === null) {
      setError(true);
      return;
    }
    setError(false);
    setMixer(state);
  }, [dropPending]);

  useVisibleInterval(poll, POLL_MS);

  // Commits fields for a single control. Only that control's drag flag and
  // pending override are cleared — a concurrent drag/commit on another
  // control (e.g. releasing the speaker slider while the mic slider is
  // still being dragged) must be left untouched.
  const commit = useCallback(async (key, fields) => {
    draggingRef.current.delete(key);
    const state = await updateAudioMixer(fields);
    setPending((p) => {
      if (!(key in p)) return p;
      const next = { ...p };
      delete next[key];
      return next;
    });
    if (state === null) {
      setError(true);
      return;
    }
    setError(false);
    setMixer(state);
  }, []);

  const onSpeakerDrag = (v) => {
    draggingRef.current.set('speaker', 0);
    setPending((p) => ({ ...p, speaker: v }));
  };
  const onMicDrag = (v) => {
    draggingRef.current.set('mic', 0);
    setPending((p) => ({ ...p, mic: v }));
  };
  const cancelSpeakerDrag = () => dropPending('speaker');
  const cancelMicDrag = () => dropPending('mic');

  const loading = mixer === null && !error;
  const unavailable = !loading && !error && !mixer?.available;

  const speakerVal = pending.speaker ?? mixer?.speakerVolume ?? 0;
  const micVal = pending.mic ?? mixer?.micVolume ?? 0;
  const hasSpeaker = mixer?.speakerVolume !== undefined;
  const hasMic = mixer?.micVolume !== undefined;
  const hasAgc = mixer?.agcEnabled !== undefined;
  // AGC is a single click, not a drag, but still needs an optimistic
  // pending value: otherwise a poll response landing after the click but
  // before the update RPC resolves would visually snap the toggle back to
  // its pre-click state until the RPC's own response arrives.
  const agcOn = pending.agc ?? (mixer?.agcEnabled === true);

  const commitSpeaker = () => commit('speaker', { speakerVolume: speakerVal });
  const commitMic = () => commit('mic', { micVolume: micVal });
  const onSpeakerKeyUp = (e) => {
    if (isSliderCommitKey(e.key)) commitSpeaker();
  };
  const onMicKeyUp = (e) => {
    if (isSliderCommitKey(e.key)) commitMic();
  };
  const toggleAgc = () => {
    const next = !agcOn;
    setPending((p) => ({ ...p, agc: next }));
    commit('agc', { agcEnabled: next });
  };

  return (
    <div className="lat-panel">
      <div className="panel-head"><h3>Device Audio</h3></div>

      {loading && <div className="comms-device-audio-empty">Loading…</div>}

      {error && <div className="lat-alert crit">Mixer unavailable — request failed.</div>}

      {unavailable && (
        <div className="comms-device-audio-empty">No audio device detected.</div>
      )}

      {!loading && !error && mixer?.available && (
        <>
          {hasSpeaker && (
            <div className="pbar-row">
              <span className="pbar-label">Speaker</span>
              <input
                type="range"
                className="lat-slider"
                min="0"
                max="100"
                value={speakerVal}
                onChange={(e) => onSpeakerDrag(Number(e.target.value))}
                onPointerUp={commitSpeaker}
                onPointerCancel={cancelSpeakerDrag}
                onKeyUp={onSpeakerKeyUp}
                aria-label="Device speaker volume"
              />
              <span className="pbar-val">{speakerVal}</span>
            </div>
          )}

          {hasMic && (
            <div className="pbar-row">
              <span className="pbar-label">Mic</span>
              <input
                type="range"
                className="lat-slider"
                min="0"
                max="100"
                value={micVal}
                onChange={(e) => onMicDrag(Number(e.target.value))}
                onPointerUp={commitMic}
                onPointerCancel={cancelMicDrag}
                onKeyUp={onMicKeyUp}
                aria-label="Device mic volume"
              />
              <span className="pbar-val">{micVal}</span>
            </div>
          )}

          {hasAgc && (
            <button
              type="button"
              className={`lat-toggle${agcOn ? ' on' : ''}`}
              aria-pressed={agcOn}
              onClick={toggleAgc}
            >
              <span className="track"><span className="thumb"></span></span>
              <span className="label">Auto Gain Control</span>
            </button>
          )}
        </>
      )}
    </div>
  );
}
