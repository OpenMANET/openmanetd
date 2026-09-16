// =============================================================================
// Comms.jsx — Push-to-talk, channels, transcript (Lattice layout)
// =============================================================================
// Top-level Comms page component. Wires the services layer (WebSocket, audio
// engine, whisper, mesh API) to the mockup-matching Lattice UI: two-column
// body with PTT ring + mic meter + RX waveform + channel tiles on the left,
// transcript + system log on the right.

import React, { useState, useEffect, useRef, useCallback, useMemo } from 'react';
import './Comms.css';
import { CHANNELS_DEF, MSG_TYPE, RX_WAVE_HISTORY, VOX_HANGTIME_MS } from '../constants.js';
import { connect as wsConnect, disconnect as wsDisconnect, setCallbacks as wsSetCallbacks, sendToggle as wsSendToggle, sendByte as wsSendByte, send as wsSend, isOpen as wsIsOpen } from '../services/websocketService.js';
import { initAudio, decodeAndPlay, resetTxTimestamp, startMic, stopMic, setVolume, setMicGain, playBuffer, startMicMonitor, enumerateDevices, setOutputDevice, setMicDevice, setEncoderCallback, clearEncoderCallback } from '../services/audioEngine.js';
import { isReady as whisperIsReady, initWhisper, feedAudio as whisperFeedAudio, checkSilenceAndTranscribe, checkWhisperAvailable } from '../services/whisperService.js';
import { fetchCommsStatus } from '../services/commsApi.js';
import { useVisibleInterval } from '../hooks/useVisibleInterval.js';
import { useMeshStatus } from '../hooks/useMeshStatus.js';
import { getReplayPcm } from '../services/replayBuffer.js';
import ChannelGrid from '../components/ChannelGrid.jsx';
import AudioControls from '../components/AudioControls.jsx';
import AudioFileTxPanel from '../components/AudioFileTx.jsx';
import DeviceAudioPanel from './DeviceAudioPanel.jsx';
import Transcript from '../components/Transcript.jsx';
import RxWaveform from '../components/RxWaveform.jsx';
import MicMeter from '../components/MicMeter.jsx';

// Aligned with the mesh / batctl snapshot cadence — the CommsService
// status only reflects enable/disable + talkgroup state, which changes
// on operator action, so a 10s refresh is more than fast enough.
const COMMS_STATUS_POLL_INTERVAL = 10000;
const MESH_STATUS_POLL_INTERVAL = 10000;
const MAX_LOGS = 80;
const MAX_CHAT = 200;

export default function CommsPage() {
  // ---------------------------------------------------------------------------
  // State
  // ---------------------------------------------------------------------------

  const [wsStatus, setWsStatus] = useState('disconnected');
  const [audioStatus, setAudioStatus] = useState('Audio off');
  const [logs, setLogs] = useState([]);
  const [chatMessages, setChatMessages] = useState([]);
  const meshData = useMeshStatus(MESH_STATUS_POLL_INTERVAL);
  const [commsStatus, setCommsStatus] = useState(null);
  const [pttActive, setPttActive] = useState(false);
  const [whisperEnabled, setWhisperEnabled] = useState(false);
  const [whisperStatus, setWhisperStatus] = useState('');
  const [speakerVol, setSpeakerVol] = useState(80);
  const [speakerVolPrev, setSpeakerVolPrev] = useState(80);
  const [muted, setMuted] = useState(false);
  const [micVol, setMicVol] = useState(80);
  const [micLevel, setMicLevel] = useState(0);
  const [audioExpanded, setAudioExpanded] = useState(() => {
    if (typeof window === 'undefined') return false;
    return window.matchMedia && window.matchMedia('(min-width: 900px)').matches;
  });
  const [scrollLocked, setScrollLocked] = useState(false);

  // VOX
  const [voxEnabled, setVoxEnabled] = useState(false);
  const [voxThreshold, setVoxThreshold] = useState(0.15);
  const voxTimerRef = useRef(null);
  const voxActiveRef = useRef(false);
  const micLevelRef = useRef(0);

  // Channel aliases
  const [channelAliases, setChannelAliases] = useState(() => {
    try {
      return JSON.parse(localStorage.getItem('channelAliases')) || {};
    } catch { return {}; }
  });

  // Replay availability
  const [replayAvailable, setReplayAvailable] = useState({});

  // Audio device selection
  const [audioDevices, setAudioDevices] = useState({ inputs: [], outputs: [] });
  const [selectedOutput, setSelectedOutput] = useState('');
  const [selectedMic, setSelectedMic] = useState('');

  // Channel enable/disable
  const [rxEnabled, setRxEnabled] = useState(() => {
    const init = {};
    CHANNELS_DEF.forEach((c) => { init[c.ch] = (c.ch === 1); });
    return init;
  });
  const [txEnabled, setTxEnabled] = useState(() => {
    const init = {};
    CHANNELS_DEF.forEach((c) => { init[c.ch] = (c.ch === 1); });
    return init;
  });

  // RX tracking
  const rxLastTimeRef = useRef({});
  const rxLastSourceRef = useRef(null); // { ch, ip, ts } most recent RX
  const [rxSource, setRxSource] = useState(null);

  // RX waveform — buffer + write position are refs so audio frames (~50/s)
  // don't drive React re-renders. RxWaveform reads both refs inside its own
  // rAF loop.
  const rxWaveDataRef = useRef(new Float32Array(RX_WAVE_HISTORY));
  const rxWaveWritePosRef = useRef(0);

  // Callback refs
  const pttActiveRef = useRef(false);
  const whisperEnabledRef = useRef(false);
  const audioInitRef = useRef(false);
  const spaceDownRef = useRef(false);
  const rxEnabledRef = useRef(rxEnabled);
  const txEnabledRef = useRef(txEnabled);
  const voxEnabledRef = useRef(false);
  const scrollLockedRef = useRef(false);

  useEffect(() => { pttActiveRef.current = pttActive; }, [pttActive]);
  useEffect(() => { whisperEnabledRef.current = whisperEnabled; }, [whisperEnabled]);
  useEffect(() => { rxEnabledRef.current = rxEnabled; }, [rxEnabled]);
  useEffect(() => { txEnabledRef.current = txEnabled; }, [txEnabled]);
  useEffect(() => { voxEnabledRef.current = voxEnabled; }, [voxEnabled]);
  useEffect(() => { micLevelRef.current = micLevel; }, [micLevel]);
  useEffect(() => { scrollLockedRef.current = scrollLocked; }, [scrollLocked]);

  // ---------------------------------------------------------------------------
  // Logging
  // ---------------------------------------------------------------------------

  const addLog = useCallback((msg, cls) => {
    const ts = new Date().toLocaleTimeString('en-US', { hour12: false });
    setLogs((prev) => {
      const next = [...prev, { msg: `[${ts}] ${msg}`, cls: cls || '' }];
      if (next.length > MAX_LOGS) return next.slice(-MAX_LOGS);
      return next;
    });
  }, []);

  // ---------------------------------------------------------------------------
  // Audio init — one-shot
  // ---------------------------------------------------------------------------

  const initAudioOnce = useCallback(async () => {
    if (audioInitRef.current) return;
    audioInitRef.current = true;

    await initAudio(addLog, {
      onPcm: (pcm, ch, srcIP) => {
        let peak = 0;
        for (let i = 0; i < pcm.length; i++) {
          const v = Math.abs(pcm[i]);
          if (v > peak) peak = v;
        }
        const wp = rxWaveWritePosRef.current;
        rxWaveDataRef.current[wp % RX_WAVE_HISTORY] = peak;
        rxWaveWritePosRef.current = wp + 1;

        rxLastTimeRef.current[ch] = Date.now();
        rxLastSourceRef.current = { ch, ip: srcIP, ts: Date.now() };
        setRxSource({ ch, ip: srcIP });

        setReplayAvailable((prev) => (prev[ch] ? prev : { ...prev, [ch]: true }));

        if (whisperEnabledRef.current && whisperIsReady() && ch) {
          whisperFeedAudio(ch, pcm, srcIP);
        }
      },
    });

    setAudioStatus('Audio ready');
  }, [addLog]);

  // ---------------------------------------------------------------------------
  // WebSocket setup
  // ---------------------------------------------------------------------------

  useEffect(() => {
    wsSetCallbacks({
      statusChange: (status) => {
        setWsStatus(status);
        if (status === 'connected') {
          CHANNELS_DEF.forEach((c) => {
            wsSendToggle(MSG_TYPE.RX_TOGGLE, c.ch, rxEnabledRef.current[c.ch]);
            wsSendToggle(MSG_TYPE.TX_TOGGLE, c.ch, txEnabledRef.current[c.ch]);
          });
        }
      },
      rxAudio: (ch, srcIP, opusData) => {
        rxLastTimeRef.current[ch] = Date.now();
        decodeAndPlay(opusData, ch, srcIP);
      },
      error: (msg) => addLog(msg, 'err'),
      log: (msg, cls) => addLog(msg, cls),
    });

    // Mount-time log line: addLog appends to the on-screen log, which is local
    // React state. No external system applies here; an effect is the right home.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    addLog('Comms Bridge (API Mode) starting...', 'info');
    wsConnect();

    return () => {
      wsDisconnect();
    };
  }, [addLog]);

  useEffect(() => {
    const handler = () => initAudioOnce();
    document.addEventListener('click', handler, { once: true });
    document.addEventListener('touchstart', handler, { once: true });
    return () => {
      document.removeEventListener('click', handler);
      document.removeEventListener('touchstart', handler);
    };
  }, [initAudioOnce]);

  // ---------------------------------------------------------------------------
  // PTT
  // ---------------------------------------------------------------------------

  const pttDown = useCallback(async () => {
    if (pttActiveRef.current) return;
    if (!CHANNELS_DEF.some((c) => txEnabledRef.current[c.ch])) {
      addLog('No TX channels!', 'err');
      return;
    }
    setPttActive(true);
    pttActiveRef.current = true;

    wsSendByte(MSG_TYPE.PTT_DOWN);
    await initAudioOnce();
    resetTxTimestamp();

    await startMic(
      (encodedBuf) => {
        if (wsIsOpen() && pttActiveRef.current) {
          const msg = new Uint8Array(1 + encodedBuf.byteLength);
          msg[0] = MSG_TYPE.TX_AUDIO;
          msg.set(new Uint8Array(encodedBuf), 1);
          wsSend(msg.buffer);
        }
      },
      (gainedSamples) => {
        let sum = 0;
        for (let i = 0; i < gainedSamples.length; i++) {
          sum += gainedSamples[i] * gainedSamples[i];
        }
        setMicLevel(Math.sqrt(sum / gainedSamples.length));
      }
    );

    addLog('TX start', 'tx');
  }, [addLog, initAudioOnce]);

  const pttUp = useCallback(() => {
    if (!pttActiveRef.current) return;
    setPttActive(false);
    pttActiveRef.current = false;

    if (voxEnabledRef.current) {
      stopMic();
      startMicMonitor((gainedSamples) => {
        let sum = 0;
        for (let i = 0; i < gainedSamples.length; i++) {
          sum += gainedSamples[i] * gainedSamples[i];
        }
        setMicLevel(Math.sqrt(sum / gainedSamples.length));
      });
    } else {
      stopMic();
    }

    wsSendByte(MSG_TYPE.PTT_UP);
    addLog('TX end', 'tx');
  }, [addLog]);

  useEffect(() => {
    const handleKeyDown = async (e) => {
      if (e.code === 'Space' && !e.repeat && !spaceDownRef.current) {
        // Avoid capturing space when the user is typing into an input/textarea.
        const t = e.target;
        if (t && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.isContentEditable)) return;
        e.preventDefault();
        spaceDownRef.current = true;
        await initAudioOnce();
        pttDown();
      }
    };
    const handleKeyUp = (e) => {
      if (e.code === 'Space') {
        e.preventDefault();
        spaceDownRef.current = false;
        pttUp();
      }
    };

    document.addEventListener('keydown', handleKeyDown);
    document.addEventListener('keyup', handleKeyUp);
    return () => {
      document.removeEventListener('keydown', handleKeyDown);
      document.removeEventListener('keyup', handleKeyUp);
    };
  }, [pttDown, pttUp, initAudioOnce]);

  // ---------------------------------------------------------------------------
  // VOX
  // ---------------------------------------------------------------------------

  const handleVoxToggle = useCallback(async (enabled) => {
    setVoxEnabled(enabled);
    voxEnabledRef.current = enabled;

    if (enabled) {
      await initAudioOnce();
      await startMicMonitor((gainedSamples) => {
        let sum = 0;
        for (let i = 0; i < gainedSamples.length; i++) {
          sum += gainedSamples[i] * gainedSamples[i];
        }
        setMicLevel(Math.sqrt(sum / gainedSamples.length));
      });
      addLog('VOX enabled', 'info');
    } else {
      if (voxActiveRef.current) {
        voxActiveRef.current = false;
        if (pttActiveRef.current) {
          setPttActive(false);
          pttActiveRef.current = false;
          stopMic();
          wsSendByte(MSG_TYPE.PTT_UP);
          addLog('TX end (VOX off)', 'tx');
        }
      } else if (!pttActiveRef.current) {
        stopMic();
      }
      if (voxTimerRef.current) {
        clearTimeout(voxTimerRef.current);
        voxTimerRef.current = null;
      }
      setMicLevel(0);
      addLog('VOX disabled', 'info');
    }
  }, [addLog, initAudioOnce]);

  const voxThresholdRef = useRef(voxThreshold);
  useEffect(() => { voxThresholdRef.current = voxThreshold; }, [voxThreshold]);
  const pttDownRef = useRef(pttDown);
  const pttUpRef = useRef(pttUp);
  useEffect(() => { pttDownRef.current = pttDown; }, [pttDown]);
  useEffect(() => { pttUpRef.current = pttUp; }, [pttUp]);

  const voxTick = useCallback(() => {
    const level = micLevelRef.current;
    const thresh = voxThresholdRef.current;
    if (level > thresh) {
      if (voxTimerRef.current) { clearTimeout(voxTimerRef.current); voxTimerRef.current = null; }
      if (!pttActiveRef.current) {
        voxActiveRef.current = true;
        pttDownRef.current();
      }
    } else if (voxActiveRef.current && pttActiveRef.current) {
      if (!voxTimerRef.current) {
        voxTimerRef.current = setTimeout(() => {
          voxTimerRef.current = null;
          if (voxActiveRef.current && pttActiveRef.current) {
            voxActiveRef.current = false;
            pttUpRef.current();
          }
        }, VOX_HANGTIME_MS);
      }
    }
  }, []);
  useVisibleInterval(voxTick, voxEnabled ? 50 : 0);

  // ---------------------------------------------------------------------------
  // Volume + mute
  // ---------------------------------------------------------------------------

  const handleSpeakerChange = useCallback((val) => {
    setSpeakerVol(val);
    setVolume(val);
    if (val > 0 && muted) setMuted(false);
  }, [muted]);

  const handleMicChange = useCallback((val) => {
    setMicVol(val);
    setMicGain(val);
  }, []);

  const toggleMute = useCallback(() => {
    if (muted) {
      setMuted(false);
      setSpeakerVol(speakerVolPrev);
      setVolume(speakerVolPrev);
    } else {
      setSpeakerVolPrev(speakerVol);
      setMuted(true);
      setSpeakerVol(0);
      setVolume(0);
    }
  }, [muted, speakerVol, speakerVolPrev]);

  // ---------------------------------------------------------------------------
  // Audio devices
  // ---------------------------------------------------------------------------

  useEffect(() => {
    if (audioStatus !== 'Audio ready') return;
    enumerateDevices().then(setAudioDevices);
    const handler = () => enumerateDevices().then(setAudioDevices);
    if (!navigator.mediaDevices) return;
    navigator.mediaDevices.addEventListener('devicechange', handler);
    return () => navigator.mediaDevices.removeEventListener('devicechange', handler);
  }, [audioStatus]);

  const handleOutputChange = useCallback(async (deviceId) => {
    setSelectedOutput(deviceId);
    const ok = await setOutputDevice(deviceId);
    if (ok) addLog('Output: ' + (audioDevices.outputs.find((d) => d.deviceId === deviceId)?.label || deviceId), 'info');
  }, [addLog, audioDevices]);

  const handleMicDeviceChange = useCallback((deviceId) => {
    setSelectedMic(deviceId);
    setMicDevice(deviceId);
    addLog('Mic: ' + (audioDevices.inputs.find((d) => d.deviceId === deviceId)?.label || deviceId), 'info');
  }, [addLog, audioDevices]);

  // ---------------------------------------------------------------------------
  // Channel toggles + aliases
  // ---------------------------------------------------------------------------

  const handleToggleRx = useCallback((ch) => {
    setRxEnabled((prev) => {
      const next = { ...prev, [ch]: !prev[ch] };
      wsSendToggle(MSG_TYPE.RX_TOGGLE, ch, next[ch]);
      return next;
    });
  }, []);

  const handleToggleTx = useCallback((ch) => {
    setTxEnabled((prev) => {
      const next = { ...prev, [ch]: !prev[ch] };
      wsSendToggle(MSG_TYPE.TX_TOGGLE, ch, next[ch]);
      return next;
    });
  }, []);

  const handleRxAll = useCallback((on) => {
    setRxEnabled(() => {
      const next = {};
      CHANNELS_DEF.forEach((c) => { next[c.ch] = on; });
      wsSendByte(on ? MSG_TYPE.RX_ALL_ON : MSG_TYPE.RX_ALL_OFF);
      return next;
    });
  }, []);

  const handleTxAll = useCallback((on) => {
    setTxEnabled(() => {
      const next = {};
      CHANNELS_DEF.forEach((c) => { next[c.ch] = on; });
      wsSendByte(on ? MSG_TYPE.TX_ALL_ON : MSG_TYPE.TX_ALL_OFF);
      return next;
    });
  }, []);

  const handleAliasChange = useCallback((ch, name) => {
    setChannelAliases((prev) => {
      const next = { ...prev };
      const trimmed = name.trim();
      if (trimmed && trimmed !== `Ch ${ch}`) {
        next[ch] = trimmed;
      } else {
        delete next[ch];
      }
      localStorage.setItem('channelAliases', JSON.stringify(next));
      return next;
    });
  }, []);

  // ---------------------------------------------------------------------------
  // Replay
  // ---------------------------------------------------------------------------

  const handleReplay = useCallback((ch) => {
    const pcm = getReplayPcm(ch);
    if (!pcm) {
      addLog(`No audio to replay on Ch ${ch}`, 'info');
      return;
    }
    playBuffer(pcm);
    const label = channelAliases[ch] || `Ch ${ch}`;
    addLog(`Replaying ${(pcm.length / 48000).toFixed(1)}s on ${label}`, 'info');
  }, [addLog, channelAliases]);

  // ---------------------------------------------------------------------------
  // Whisper
  // ---------------------------------------------------------------------------

  const handleWhisperToggle = useCallback(async () => {
    const enabled = !whisperEnabledRef.current;
    setWhisperEnabled(enabled);
    whisperEnabledRef.current = enabled;

    if (enabled && !whisperIsReady()) {
      const serverStatus = await checkWhisperAvailable();
      if (!serverStatus.available) {
        setWhisperStatus('Whisper model not downloaded — go to Settings to download');
        setWhisperEnabled(false);
        whisperEnabledRef.current = false;
        return;
      }
      const ok = await initWhisper(
        (msg) => setWhisperStatus(msg),
        addLog,
        () => {},
      );
      if (!ok) {
        setWhisperEnabled(false);
        whisperEnabledRef.current = false;
        return;
      }
    }
    setWhisperStatus(enabled ? (whisperIsReady() ? 'Whisper ready' : 'Loading model...') : '');
  }, [addLog]);

  const whisperTick = useCallback(() => {
    if (!whisperEnabledRef.current || !whisperIsReady()) return;
    checkSilenceAndTranscribe((ch, ip, text) => {
      const ts = new Date().toLocaleTimeString('en-US', { hour12: false });
      setChatMessages((prev) => {
        const next = [...prev, { ch, ip, text, ts }];
        if (next.length > MAX_CHAT) return next.slice(-MAX_CHAT);
        return next;
      });
    });
  }, []);
  useVisibleInterval(whisperTick, 500);

  // ---------------------------------------------------------------------------
  // Polling — mesh + comms status
  // ---------------------------------------------------------------------------

  const pollCommsStatus = useCallback(async () => {
    const data = await fetchCommsStatus();
    setCommsStatus(data);
  }, []);
  useVisibleInterval(pollCommsStatus, COMMS_STATUS_POLL_INTERVAL);

  // ---------------------------------------------------------------------------
  // File TX PTT
  // ---------------------------------------------------------------------------

  const handleFilePttSet = useCallback((active) => {
    setPttActive(active);
    pttActiveRef.current = active;
    if (active) {
      setEncoderCallback((encodedBuf) => {
        if (wsIsOpen()) {
          const msg = new Uint8Array(1 + encodedBuf.byteLength);
          msg[0] = MSG_TYPE.TX_AUDIO;
          msg.set(new Uint8Array(encodedBuf), 1);
          wsSend(msg.buffer);
        }
      });
      wsSendByte(MSG_TYPE.PTT_DOWN);
    } else {
      clearEncoderCallback();
      wsSendByte(MSG_TYPE.PTT_UP);
    }
  }, []);

  // ---------------------------------------------------------------------------
  // Log controls
  // ---------------------------------------------------------------------------

  const clearLogs = useCallback(() => setLogs([]), []);
  const toggleScrollLock = useCallback(() => setScrollLocked((v) => !v), []);

  // ---------------------------------------------------------------------------
  // Derived display values
  // ---------------------------------------------------------------------------

  const activeCh = useMemo(() => {
    const txCh = CHANNELS_DEF.find((c) => txEnabled[c.ch]);
    if (txCh) return txCh.ch;
    const rxCh = CHANNELS_DEF.find((c) => rxEnabled[c.ch]);
    return rxCh ? rxCh.ch : 1;
  }, [txEnabled, rxEnabled]);

  const activeChName = useMemo(() => {
    const alias = channelAliases[activeCh];
    if (alias) return alias.toUpperCase();
    const def = CHANNELS_DEF.find((c) => c.ch === activeCh);
    return def ? (def.name || `CH ${activeCh}`).toUpperCase() : `CH ${activeCh}`;
  }, [activeCh, channelAliases]);

  const channelCount = CHANNELS_DEF.length;
  const peersOnChannel = meshData?.status?.neighbors ?? 0;
  const wsConnected = wsStatus === 'connected';
  const audioOn = audioStatus === 'Audio ready';
  const codec = commsStatus?.codec || '';
  const ptimeMs = commsStatus?.ptimeMs || 0;
  const rttMs = commsStatus?.rttMs || 0;

  // Hostname/node tag from mesh status (first self node), falling back to
  // a short placeholder so the topbar always renders something.
  const hostTag = useMemo(() => {
    const nodes = meshData?.nodes;
    if (!nodes || !nodes.length) return '—';
    const first = nodes[0];
    const hn = (first?.hostname || '').toUpperCase();
    return hn || '—';
  }, [meshData]);

  // Auto-scroll logs unless locked.
  const logRef = useRef(null);
  useEffect(() => {
    if (!scrollLockedRef.current && logRef.current) {
      logRef.current.scrollTop = logRef.current.scrollHeight;
    }
  }, [logs]);

  // Keep RX source marker fresh for a short window; age it out after 2s.
  const rxSourceTag = useMemo(() => {
    if (!rxSource) return null;
    const label = (channelAliases[rxSource.ch] || '').toUpperCase()
      || `CH ${rxSource.ch}`;
    return label;
  }, [rxSource, channelAliases]);

  // Expire rxSource if no audio for 2 seconds.
  const rxSourceExpireTick = useCallback(() => {
    const last = rxLastSourceRef.current;
    if (last && Date.now() - last.ts > 2000) {
      rxLastSourceRef.current = null;
      setRxSource(null);
    }
  }, []);
  useVisibleInterval(rxSourceExpireTick, 500);

  const pttLabel = pttActive
    ? `TRANSMITTING · CH ${activeCh} ${activeChName}`
    : `TX · HOLD`;

  // PTT mouse/touch handlers
  const pttBtnRef = useRef(null);
  useEffect(() => {
    const btn = pttBtnRef.current;
    if (!btn) return;
    const ts = (e) => { e.preventDefault(); pttDown(); };
    const te = (e) => { e.preventDefault(); pttUp(); };
    btn.addEventListener('touchstart', ts, { passive: false });
    btn.addEventListener('touchend', te, { passive: false });
    btn.addEventListener('touchcancel', pttUp);
    return () => {
      btn.removeEventListener('touchstart', ts);
      btn.removeEventListener('touchend', te);
      btn.removeEventListener('touchcancel', pttUp);
    };
  }, [pttDown, pttUp]);

  return (
    <>
      <div className="lat-topbar">
        <div className="node-id">
          NODE-{hostTag}
          <span className="ip">
            CH {activeCh} · {activeChName}
            {codec ? ` · ${codec}` : ' · —'}
            {' · PTIME '}{ptimeMs || '—'}MS
          </span>
        </div>
        <div className="chips">
          <span className={`lat-chip ${wsConnected ? 'ok' : 'crit'}`}>
            <span className="dot" /> WS {wsConnected ? 'UP' : 'DOWN'}
          </span>
          <span className={`lat-chip ${audioOn ? 'ok' : 'warn'}`}>
            <span className="dot" /> AUDIO {audioOn ? 'ON' : 'OFF'}
          </span>
          <span className={`lat-chip ${peersOnChannel > 0 ? 'ok' : 'warn'}`}>
            <span className="dot" /> {peersOnChannel} PEERS ON CH
          </span>
          <span className={`lat-chip ${rttMs > 0 && rttMs < 500 ? 'ok' : 'warn'}`}>
            <span className="dot" /> LAT {rttMs > 0 ? `${rttMs}MS` : '—'}
          </span>
        </div>
      </div>

      <div className="lat-view-header">
        <div>
          <h2>◇ Comms</h2>
          <div className="crumb">Push-to-talk · Live transcript · {channelCount} channels</div>
        </div>
        <div className="lat-view-toolbar">
          <button
            className={`lat-btn ${audioExpanded ? '' : 'ghost'}`}
            type="button"
            onClick={() => setAudioExpanded((v) => !v)}
          >AUDIO</button>
          <button
            className={`lat-btn ${whisperEnabled ? '' : 'ghost'}`}
            type="button"
            onClick={handleWhisperToggle}
          >WHISPER</button>
          <button
            className={`lat-btn ${muted ? 'danger solid' : 'ghost'}`}
            type="button"
            onClick={toggleMute}
          >{muted ? 'UNMUTE' : 'MUTE'}</button>
        </div>
      </div>

      <div className="lat-body comms-body">
        <div className="comms-left">
          <div className="lat-panel comms-ptt-panel">
            <button
              ref={pttBtnRef}
              className={`ptt-ring${pttActive ? ' tx' : ''}`}
              type="button"
              onMouseDown={(e) => { e.preventDefault(); pttDown(); }}
              onMouseUp={(e) => { e.preventDefault(); pttUp(); }}
              onMouseLeave={() => pttActiveRef.current && pttUp()}
            >
              {pttActive ? 'TX · ON' : 'TX · HOLD'}
            </button>
            <div className="ptt-label">{pttLabel}</div>
            <MicMeter level={micLevel} active={pttActive} voxEnabled={voxEnabled} segments={16} />
            <RxWaveform
              rxWaveDataRef={rxWaveDataRef}
              rxWaveWritePosRef={rxWaveWritePosRef}
              sourceTag={rxSourceTag}
              lattice
            />
            <ChannelGrid
              channels={CHANNELS_DEF}
              rxEnabled={rxEnabled}
              txEnabled={txEnabled}
              rxLastTimeRef={rxLastTimeRef}
              onToggleRx={handleToggleRx}
              onToggleTx={handleToggleTx}
              onRxAll={handleRxAll}
              onTxAll={handleTxAll}
              channelAliases={channelAliases}
              onAliasChange={handleAliasChange}
              onReplay={handleReplay}
              replayAvailable={replayAvailable}
              tiles
            />
          </div>
          {audioExpanded && (
            <>
              <div className="lat-panel comms-audio-panel">
                <div className="panel-head"><h3>Web Audio</h3></div>
                <AudioControls
                  speakerVol={speakerVol}
                  micVol={micVol}
                  onSpeakerChange={handleSpeakerChange}
                  onMicChange={handleMicChange}
                  voxEnabled={voxEnabled}
                  voxThreshold={voxThreshold}
                  onVoxToggle={handleVoxToggle}
                  onVoxThresholdChange={setVoxThreshold}
                  audioDevices={audioDevices}
                  selectedOutput={selectedOutput}
                  selectedMic={selectedMic}
                  onOutputChange={handleOutputChange}
                  onMicDeviceChange={handleMicDeviceChange}
                />
                <AudioFileTxPanel
                  onLog={addLog}
                  onPttSet={handleFilePttSet}
                  txEnabled={txEnabled}
                />
              </div>
              <DeviceAudioPanel />
            </>
          )}
        </div>

        <div className="comms-right">
          <div className="lat-panel comms-transcript-panel">
            <div className="panel-head">
              <h3>Live Transcript · CH {activeCh} {activeChName}</h3>
              {whisperStatus && <div className="panel-note">{whisperStatus}</div>}
            </div>
            {chatMessages.length === 0 ? (
              <div className="tr-empty">
                {whisperEnabled
                  ? 'Listening... transcript will appear here.'
                  : 'Press WHISPER to enable captions.'}
              </div>
            ) : (
              <Transcript
                messages={chatMessages}
                channelAliases={channelAliases}
                compact
              />
            )}
          </div>
          <div className="lat-panel comms-log-panel">
            <div className="panel-head">
              <h3>System Log</h3>
              <div className="panel-actions">
                <button className="lat-btn ghost" type="button" onClick={clearLogs}>CLEAR</button>
                <button
                  className={`lat-btn ${scrollLocked ? '' : 'ghost'}`}
                  type="button"
                  onClick={toggleScrollLock}
                >SCROLL LOCK</button>
              </div>
            </div>
            <div className="comms-log" ref={logRef}>
              {logs.map((entry, i) => (
                <div key={i} className={entry.cls || ''}>
                  {entry.msg}
                </div>
              ))}
            </div>
          </div>
        </div>
      </div>
    </>
  );
}
