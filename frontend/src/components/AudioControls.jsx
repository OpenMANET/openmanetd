// =============================================================================
// AudioControls.jsx — Speaker/mic volume sliders, device selectors, VOX
// =============================================================================

import React from 'react';
import LatSelect from './LatSelect.jsx';

export default React.memo(function AudioControls({
  speakerVol,
  micVol,
  onSpeakerChange,
  onMicChange,
  voxEnabled,
  voxThreshold,
  onVoxToggle,
  onVoxThresholdChange,
  audioDevices,
  selectedOutput,
  selectedMic,
  onOutputChange,
  onMicDeviceChange,
}) {
  const voxPct = Math.round((voxThreshold || 0.15) * 100);
  const hasOutputs = audioDevices?.outputs?.length > 0;
  const hasInputs = audioDevices?.inputs?.length > 0;

  return (
    <div className="audio-controls">
      {/* 0-200: values past 100 amplify past unity, kept clean by the
          audio engine's limiters. 100 = unity gain. */}
      <div className="pbar-row">
        <span className="pbar-label">Speaker</span>
        <input
          type="range"
          className="lat-slider"
          min="0"
          max="200"
          value={speakerVol}
          onChange={(e) => onSpeakerChange(Number(e.target.value))}
          aria-label="Speaker"
        />
        <span className="pbar-val">{speakerVol}</span>
      </div>

      <div className="pbar-row">
        <span className="pbar-label">Mic Gain</span>
        <input
          type="range"
          className="lat-slider"
          min="0"
          max="200"
          value={micVol}
          onChange={(e) => onMicChange(Number(e.target.value))}
          aria-label="Mic Gain"
        />
        <span className="pbar-val">{micVol}</span>
      </div>

      {hasOutputs && (
        <div className="lat-field">
          <label>Output</label>
          <LatSelect
            ariaLabel="Output"
            value={selectedOutput || ''}
            onChange={(v) => onOutputChange && onOutputChange(v)}
            options={audioDevices.outputs.map((d) => ({
              value: d.deviceId,
              label: d.label || `Speaker ${d.deviceId.slice(0, 8)}`,
            }))}
          />
        </div>
      )}

      {hasInputs && (
        <div className="lat-field">
          <label>Input</label>
          <LatSelect
            ariaLabel="Input"
            value={selectedMic || ''}
            onChange={(v) => onMicDeviceChange && onMicDeviceChange(v)}
            options={audioDevices.inputs.map((d) => ({
              value: d.deviceId,
              label: d.label || `Mic ${d.deviceId.slice(0, 8)}`,
            }))}
          />
        </div>
      )}

      <div className="audio-vox-row">
        <button
          type="button"
          role="switch"
          aria-checked={voxEnabled}
          aria-label="VOX"
          className={`lat-toggle${voxEnabled ? ' on' : ''}`}
          onClick={() => onVoxToggle && onVoxToggle(!voxEnabled)}
        >
          <span className="track">
            <span className="thumb" />
          </span>
          <span className="label">VOX</span>
        </button>
        {voxEnabled && (
          <div className="pbar-row audio-vox-threshold">
            <span className="pbar-label">Threshold</span>
            <input
              type="range"
              className="lat-slider"
              min="0"
              max="50"
              value={voxPct}
              onChange={(e) =>
                onVoxThresholdChange && onVoxThresholdChange(Number(e.target.value) / 100)
              }
              aria-label="VOX Threshold"
            />
            <span className="pbar-val">{voxPct}</span>
          </div>
        )}
      </div>
    </div>
  );
});
