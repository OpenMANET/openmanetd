// =============================================================================
// AudioFileTx.jsx — Audio file TX panel
// =============================================================================
// Manages loading an audio file for transmission. Handles its own internal
// state (file loaded, playing, loop) and uses the audioFileTx service for
// actual playback/encoding.
//
// Props:
//   onLog      — function(msg, cls) for logging
//   onPttSet   — function(active) to control PTT state during file playback
//   txEnabled  — object {ch: boolean} to check if any TX channels are active

import React, { useState, useEffect, useRef, useCallback } from 'react';

import { loadFile, startPlayback, stopPlayback, isPlaying } from '../services/audioFileTx.js';
import { getAudioContext, getEncoder, resetTxTimestamp } from '../services/audioEngine.js';
import { CHANNELS_DEF } from '../constants.js';

export default function AudioFileTxPanel({ onLog, onPttSet, txEnabled }) {
  const [fileLoaded, setFileLoaded] = useState(false);
  const [playing, setPlaying] = useState(false);
  const [loop, setLoop] = useState(false);
  const [statusText, setStatusText] = useState('');
  const audioBufferRef = useRef(null);
  const stopFnRef = useRef(null);
  const handleStopRef = useRef(null);
  const pollIdRef = useRef(null);

  // Stop the completion-poll interval. Shared by handleStop and the unmount
  // cleanup: a poll that survives unmount fires against a torn-down page
  // (in tests, a destroyed jsdom window) and crashes the run.
  const clearPoll = useCallback(() => {
    if (pollIdRef.current != null) {
      clearInterval(pollIdRef.current);
      pollIdRef.current = null;
    }
  }, []);

  useEffect(() => clearPoll, [clearPoll]);

  // Handle file selection and decode.
  const handleFileChange = useCallback(async (e) => {
    const file = e.target.files[0];
    if (!file) {
      setFileLoaded(false);
      return;
    }

    setStatusText('Decoding...');
    try {
      const ctx = getAudioContext();
      if (!ctx) {
        setStatusText('Audio not initialized');
        onLog('Audio not initialized for file TX', 'err');
        return;
      }

      const result = await loadFile(file, ctx);
      audioBufferRef.current = result.audioBuffer;
      setStatusText(`${result.name} \u2014 ${result.duration.toFixed(1)}s, ${result.sampleRate}Hz`);
      setFileLoaded(true);
      onLog(`File loaded: ${result.name} (${result.duration.toFixed(1)}s)`, 'info');
    } catch (err) {
      setStatusText('Error: ' + err.message);
      onLog('File decode error: ' + err.message, 'err');
      setFileLoaded(false);
      audioBufferRef.current = null;
    }
  }, [onLog]);

  // Stop file playback.
  const handleStop = useCallback(() => {
    clearPoll();
    stopPlayback();
    onPttSet(false);
    setPlaying(false);
    if (audioBufferRef.current) {
      setStatusText(`${audioBufferRef.current.duration.toFixed(1)}s ready`);
    }
    onLog('File TX stop', 'tx');
  }, [clearPoll, onLog, onPttSet]);

  // Keep ref in sync so polling interval always calls the latest version.
  useEffect(() => {
    handleStopRef.current = handleStop;
  }, [handleStop]);

  // Start file playback.
  const handlePlay = useCallback(() => {
    if (!audioBufferRef.current || playing) return;

    // Check that at least one TX channel is active.
    if (!CHANNELS_DEF.some((c) => txEnabled[c.ch])) {
      onLog('No TX channels!', 'err');
      return;
    }

    const encoder = getEncoder();
    if (!encoder || encoder.state === 'closed') {
      onLog('Encoder not available', 'err');
      return;
    }

    // Activate PTT for file transmission.
    onPttSet(true);
    setPlaying(true);

    const dur = audioBufferRef.current.duration.toFixed(1);
    setStatusText(`Playing${loop ? ' (looping)' : ''} \u2014 ${dur}s`);

    resetTxTimestamp();
    const ctx = getAudioContext();
    stopFnRef.current = startPlayback(
      audioBufferRef.current,
      encoder,
      loop,
      onLog,
      ctx
    );

    // Poll for playback completion (non-looping). handleStop clears the
    // interval via clearPoll, so the callback needs no clearInterval of
    // its own.
    if (!loop) {
      clearPoll();
      pollIdRef.current = setInterval(() => {
        if (!isPlaying()) {
          handleStopRef.current();
        }
      }, 100);
    }
  }, [playing, loop, txEnabled, clearPoll, onLog, onPttSet]);

  return (
    <div className="audio-file-tx">
      <div className="audio-file-tx-title">Audio File TX</div>
      <div className="audio-file-tx-row">
        <input
          type="file"
          accept="audio/*"
          onChange={handleFileChange}
          className="audio-file-tx-input"
        />
        <button
          className="lat-btn ghost"
          type="button"
          disabled={!fileLoaded || playing}
          onClick={handlePlay}
        >
          PLAY
        </button>
        <button
          className="lat-btn ghost"
          type="button"
          disabled={!playing}
          onClick={handleStop}
        >
          STOP
        </button>
        <label
          className={`lat-toggle audio-file-tx-loop${loop ? ' on' : ''}`}
        >
          <span
            className="track"
            onClick={() => setLoop(!loop)}
          >
            <span className="thumb" />
          </span>
          <span className="label">Loop</span>
        </label>
      </div>
      <div className="audio-file-tx-status">{statusText}</div>
    </div>
  );
}
