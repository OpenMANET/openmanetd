// =============================================================================
// SettingsFirmware.jsx — Firmware (sysupgrade) settings tab
// =============================================================================
//
// Three stacked panels: SystemInfoPanel, AvailableUpdatesPanel, and a
// conditionally-rendered UpgradeProgressPanel that owns the streaming
// subscription. Phase enum drives every visible state.

import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  fetchSystemInfo,
  listAvailableUpdates,
  getReleaseDetail,
  startUpgrade,
  cancelUpgrade,
  getUpgradeStatus,
  streamUpgradeProgress,
  getStagedImage,
  discardStagedImage,
  startLocalUpgrade,
  uploadFirmware,
  fetchFactoryResetCapability,
  performFactoryReset,
} from '../services/sysupgradeApi.js';
import { Phase } from '../gen/openmanet/sysupgrade/v1/sysupgrade_pb.js';
import { renderReleaseNotes } from '../util/renderReleaseNotes.js';
import DataTable from '../components/DataTable.jsx';
import './SettingsFirmware.css';

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

const ACTIVE_PHASES = new Set([
  Phase.CHECKING,
  Phase.DOWNLOADING,
  Phase.VERIFYING,
  Phase.READY,
  Phase.UPGRADING,
]);

function phaseLabel(phase) {
  switch (phase) {
    case Phase.CHECKING: return 'Checking';
    case Phase.DOWNLOADING: return 'Downloading';
    case Phase.VERIFYING: return 'Verifying';
    case Phase.READY: return 'Ready';
    case Phase.UPGRADING: return 'Upgrading';
    case Phase.FAILED: return 'Failed';
    case Phase.IDLE: return 'Idle';
    default: return '—';
  }
}

function formatBytes(n) {
  if (n == null) return '—';
  const v = Number(n);
  if (!Number.isFinite(v) || v <= 0) return '0 B';
  if (v < 1024) return `${v} B`;
  if (v < 1024 * 1024) return `${(v / 1024).toFixed(1)} KB`;
  if (v < 1024 * 1024 * 1024) return `${(v / (1024 * 1024)).toFixed(1)} MB`;
  return `${(v / (1024 * 1024 * 1024)).toFixed(2)} GB`;
}

// compatVerdictLabel returns the short status string shown in the
// staged-image strip. Tracks OpenWrt fwtool_check_image semantics:
// metadata absent → "no metadata"; metadata present and the device
// compat string appears in supported_devices → "compatible"; otherwise
// → "incompatible".
function compatVerdictLabel(staged) {
  if (!staged?.metadataPresent) return 'no metadata';
  return staged.imageCompatible ? 'compatible' : 'incompatible';
}

function compatVerdictVariant(staged) {
  if (!staged?.metadataPresent) return 'warn';
  return staged.imageCompatible ? 'ok' : 'crit';
}

function formatRelative(date) {
  if (!date) return '—';
  const ms = Date.now() - date.getTime();
  if (ms < 0) return date.toISOString().slice(0, 10);
  const days = Math.floor(ms / (24 * 3600 * 1000));
  if (days >= 1) return `${days}d ago`;
  const hours = Math.floor(ms / (3600 * 1000));
  if (hours >= 1) return `${hours}h ago`;
  const mins = Math.floor(ms / 60000);
  if (mins >= 1) return `${mins}m ago`;
  return 'just now';
}

function formatDate(date) {
  if (!date) return '—';
  return date.toISOString().slice(0, 10);
}

function errorMessage(err) {
  if (!err) return '';
  if (typeof err === 'string') return err;
  return err.message || String(err);
}

// ---------------------------------------------------------------------------
// SystemInfoPanel
// ---------------------------------------------------------------------------

function SystemInfoPanel({ info, loading, error, onRefresh }) {
  const capable = info?.sysupgradeCapable === true;
  return (
    <section className="lat-panel firmware-panel">
      <div className="panel-head">
        <h3>Current Firmware</h3>
        <div className="panel-head-right">
          {info && (
            <span className={`lat-chip ${capable ? 'ok' : 'crit'}`}>
              <span className="dot" />
              {capable ? 'Sysupgrade Capable' : 'Not Sysupgrade Capable'}
            </span>
          )}
          <div className="actions">
            <button type="button" className="lat-btn" onClick={onRefresh} disabled={loading}>
              Refresh
            </button>
          </div>
        </div>
      </div>

      {error && <div className="lat-alert crit">{errorMessage(error)}</div>}

      {loading && !info ? (
        <div className="firmware-empty">Loading…</div>
      ) : !info ? (
        <div className="firmware-empty">No system info available.</div>
      ) : (
        <>
          <div className="status-strip">
            <KV k="Hostname" v={info.hostname || '—'} />
            <KV k="Model" v={info.model || '—'} />
            <KV k="Board" v={info.boardName || '—'} mono />
            <KV k="Target" v={info.target || '—'} mono />
            <KV
              k="OpenMANET"
              v={info.openmanetVersion || 'unknown'}
              variant={info.openmanetVersion ? 'accent' : 'muted'}
            />
            <KV
              k="Distribution"
              v={[info.distribution, info.release].filter(Boolean).join(' ') || '—'}
            />
            <KV k="Kernel" v={info.kernel || '—'} />
            <KV k="Architecture" v={info.architecture || '—'} />
            <KV k="Build Date" v={info.buildDate || '—'} />
            <KV
              k="Rootfs"
              v={info.rootfsType || '—'}
              variant={capable ? 'ok' : 'crit'}
            />
            {!capable && info.sysupgradeCapableReason && (
              <KV k="Reason" v={info.sysupgradeCapableReason} variant="crit" />
            )}
          </div>

          {!capable && (
            <div className="lat-alert crit">
              <strong>Sysupgrade unavailable.</strong>{' '}
              {info.sysupgradeCapableReason ||
                'This device cannot run sysupgrade.'}{' '}
              The firmware update flow is disabled.
            </div>
          )}
        </>
      )}
    </section>
  );
}

function KV({ k, v, mono, variant }) {
  const cls = ['v'];
  if (mono) cls.push('mono');
  if (variant) cls.push(variant);
  return (
    <div className="kv">
      <span className="k">{k}</span>
      <span className={cls.join(' ')}>{v}</span>
    </div>
  );
}

// ---------------------------------------------------------------------------
// AvailableUpdatesPanel
// ---------------------------------------------------------------------------

// Column spec for the releases table. Built per-render (behind useMemo in
// AvailableUpdatesPanel below) rather than as a module-level const because
// the Action column and the selected-row highlight both close over
// component props (selectedTag, onSelect, disabled).
function releaseColumns({ selectedTag, onSelect, disabled }) {
  const selectedClass = (u) => {
    const tag = u?.release?.tag ?? '';
    return selectedTag && tag === selectedTag ? 'selected' : '';
  };
  return [
    {
      key: 'tag',
      label: 'Tag',
      className: 'release-tag',
      cellClass: selectedClass,
      render: (u) => u?.release?.tag ?? '',
    },
    {
      key: 'published',
      label: 'Published',
      className: 'mono',
      cellClass: selectedClass,
      render: (u) => (u.release?.publishedAt
        ? `${formatDate(u.release.publishedAt)} · ${formatRelative(u.release.publishedAt)}`
        : '—'),
    },
    {
      key: 'type',
      label: 'Type',
      cellClass: selectedClass,
      render: (u) => {
        const prerelease = u.release?.prerelease;
        return (
          <span className={`lat-chip ${prerelease ? 'warn' : 'ok'}`}>
            <span className="dot" />
            {prerelease ? 'Pre-release' : 'Stable'}
          </span>
        );
      },
    },
    {
      key: 'asset',
      label: 'Asset',
      className: 'mono asset-cell',
      cellClass: selectedClass,
      render: (u) => u.matchedAsset?.name || '—',
    },
    {
      key: 'size',
      label: 'Size',
      className: 'num',
      cellClass: selectedClass,
      render: (u) => formatBytes(u.matchedAsset?.sizeBytes ?? 0),
    },
    {
      key: 'action',
      label: 'Action',
      className: 'act',
      cellClass: selectedClass,
      render: (u) => {
        const tag = u?.release?.tag ?? '';
        const isSelected = selectedTag && tag === selectedTag;
        return isSelected ? (
          <span className="lat-chip"><span className="dot" />Selected</span>
        ) : (
          <button
            type="button"
            className="lat-btn primary sm"
            onClick={() => onSelect(u)}
            disabled={disabled}
          >
            Install
          </button>
        );
      },
    },
  ];
}

function AvailableUpdatesPanel({
  updates,
  fetchedAt,
  loading,
  includePrerelease,
  onTogglePrerelease,
  onCheck,
  selectedTag,
  onSelect,
  disabled,
  inFlight,
  busyMessage,
  error,
}) {
  const count = updates?.length ?? 0;
  const columns = useMemo(
    () => releaseColumns({ selectedTag, onSelect, disabled }),
    [selectedTag, onSelect, disabled],
  );
  let chip;
  if (loading) {
    chip = (
      <span className="lat-chip"><span className="dot" />Checking…</span>
    );
  } else if (inFlight) {
    chip = (
      <span className="lat-chip warn"><span className="dot" />Upgrade in progress</span>
    );
  } else if (count === 0) {
    chip = (
      <span className="lat-chip muted"><span className="dot" />Up to date</span>
    );
  } else {
    chip = (
      <span className="lat-chip ok"><span className="dot" />{count} newer</span>
    );
  }

  return (
    <section className="lat-panel firmware-panel">
      <div className="panel-head">
        <h3>Available Updates</h3>
        <div className="panel-head-right">
          {chip}
          <label className="lat-check">
            <input
              type="checkbox"
              checked={includePrerelease}
              onChange={(e) => onTogglePrerelease(e.target.checked)}
              disabled={disabled || loading}
            />
            Pre-releases
          </label>
          <button
            type="button"
            className="lat-btn primary"
            onClick={onCheck}
            disabled={disabled || loading}
          >
            {loading ? 'Checking…' : 'Check for Updates'}
          </button>
        </div>
      </div>

      {fetchedAt && (
        <div className="kv firmware-fetched-row">
          <span className="k">Last Fetched</span>
          <span className="v muted">
            {formatDate(fetchedAt)} · {formatRelative(fetchedAt)}
          </span>
        </div>
      )}

      {busyMessage && (
        <div className="lat-alert">{busyMessage}</div>
      )}
      {error && <div className="lat-alert crit">{errorMessage(error)}</div>}

      {count === 0 && !loading ? (
        <div className="firmware-empty">
          {fetchedAt
            ? 'Up to date — no newer firmware available for this hardware.'
            : 'Click “Check for Updates” to query GitHub.'}
        </div>
      ) : (
        <DataTable
          ariaLabel="Available firmware updates"
          columns={columns}
          rows={updates}
          rowKey={(u) => u?.release?.tag ?? ''}
          emptyLabel="No updates."
        />
      )}
    </section>
  );
}

// ---------------------------------------------------------------------------
// UploadFirmwarePanel
// ---------------------------------------------------------------------------

function UploadFirmwarePanel({
  staged,
  loading,
  uploadState,
  onPickFile,
  onCancel,
  onDiscard,
  onSelectInstall,
  installSelected,
  disabled,
  inFlight,
  error,
  fileInputRef,
  options,
  onChangeOption,
  forceUnknown,
  onChangeForceUnknown,
  unknownVersion,
  skipPreflight,
  onChangeSkipPreflight,
  onCancelSelection,
  onConfirm,
  startBusy,
  startError,
}) {
  const uploading = uploadState.status === 'uploading';
  const showStaged = !!staged;

  let chip;
  if (uploading) {
    chip = <span className="lat-chip"><span className="dot" />Uploading…</span>;
  } else if (showStaged && staged.preflightOk) {
    chip = <span className="lat-chip ok"><span className="dot" />Image staged</span>;
  } else if (showStaged && !staged.preflightOk) {
    chip = <span className="lat-chip warn"><span className="dot" />Staged · preflight failed</span>;
  } else {
    chip = <span className="lat-chip muted"><span className="dot" />Empty</span>;
  }

  const percent = uploading && uploadState.total > 0
    ? Math.round((uploadState.loaded / uploadState.total) * 100)
    : 0;

  return (
    <section className="lat-panel firmware-panel firmware-upload-panel">
      <div className="panel-head">
        <h3>Local Image</h3>
        <div className="panel-head-right">
          {chip}
          <input
            ref={fileInputRef}
            type="file"
            className="firmware-upload-hidden"
            accept=".img,.bin,.gz,.tar,application/octet-stream"
            onChange={(e) => {
              const f = e.target.files?.[0];
              if (f) onPickFile(f);
              // Reset so re-uploading the same file re-fires onChange.
              e.target.value = '';
            }}
            disabled={disabled || uploading}
          />
          {!uploading && (
            <button
              type="button"
              className="lat-btn"
              onClick={() => fileInputRef.current?.click()}
              disabled={disabled}
            >
              {showStaged ? 'Replace…' : 'Choose File…'}
            </button>
          )}
          {uploading && (
            <button
              type="button"
              className="lat-btn ghost"
              onClick={onCancel}
            >
              Cancel Upload
            </button>
          )}
        </div>
      </div>

      {error && !uploading && (
        <div className="lat-alert crit">{errorMessage(error)}</div>
      )}

      {uploading && (
        <>
          <div className="firmware-upload-progress">
            <div className="progress-headline">
              <span>◇ Uploading {uploadState.filename}</span>
              <span className="progress-percent">{percent}%</span>
            </div>
            <div className="pbar">
              <span style={{ width: `${Math.max(0, Math.min(100, percent))}%` }} />
            </div>
            <div className="progress-meta">
              {uploadState.total > 0 && (
                <span>
                  Bytes:{' '}
                  <span className="v">
                    {formatBytes(uploadState.loaded)} / {formatBytes(uploadState.total)}
                  </span>
                </span>
              )}
            </div>
          </div>
          {uploadState.error && (
            <div className="lat-alert crit">{errorMessage(uploadState.error)}</div>
          )}
        </>
      )}

      {!uploading && !showStaged && (
        <div className="firmware-empty">
          {loading ? 'Loading…' : 'No image staged. Pick a local .img / .img.gz / .bin to upload.'}
        </div>
      )}

      {!uploading && showStaged && (
        <>
          <div className="status-strip">
            <KV k="Filename" v={staged.filename || '—'} mono />
            <KV k="Size" v={formatBytes(staged.sizeBytes)} mono />
            <KV
              k="SHA-256"
              v={staged.sha256 || '—'}
              mono
              variant="accent"
            />
            <KV
              k="Compatibility"
              v={compatVerdictLabel(staged)}
              variant={compatVerdictVariant(staged)}
              mono
            />
            <KV
              k="Preflight"
              v={staged.preflightOk ? 'pass' : 'fail'}
              variant={staged.preflightOk ? 'ok' : 'crit'}
            />
            <KV
              k="Uploaded"
              v={staged.uploadedAt ? formatRelative(staged.uploadedAt) : '—'}
            />
          </div>

          {staged.metadataPresent && (
            <div className="firmware-compat-detail">
              <KV
                k="Device"
                v={staged.deviceCompat || '—'}
                mono
              />
              <KV
                k="Image supports"
                v={(staged.supportedDevices ?? []).join(', ') || '—'}
                mono
              />
              {staged.compatVersion && staged.compatVersion !== '1.0' && (
                <KV
                  k="compat_version"
                  v={staged.compatVersion}
                  mono
                  variant="warn"
                />
              )}
            </div>
          )}

          {!staged.metadataPresent && (
            <div className="lat-alert warn">
              <strong>No image metadata.</strong> The uploaded image carries
              no FWx0 trailer, so hardware compatibility cannot be verified
              from the image alone. This is normal for factory images and
              third-party builds. Preflight (<code>sysupgrade -T</code>) is
              still run as a second gate.
            </div>
          )}

          {staged.metadataPresent && !staged.imageCompatible && (
            <div className="lat-alert crit">
              <strong>Incompatible image.</strong> Image declares support
              for <code>{(staged.supportedDevices ?? []).join(', ') || 'no devices'}</code>;
              this device reports <code>{staged.deviceCompat || 'unknown'}</code>.
              {staged.compatMessage ? <> {staged.compatMessage}</> : null}
            </div>
          )}

          {staged.compatMessage && staged.imageCompatible && (
            <div className="lat-alert warn">
              <strong>Compat note.</strong> {staged.compatMessage}
            </div>
          )}

          {!staged.preflightOk && (
            <div className="lat-alert crit">
              <strong>Preflight failed.</strong>{' '}
              {staged.preflightError || 'sysupgrade -T returned a non-zero exit.'}{' '}
              Re-upload a correct image, or tick &quot;Skip preflight&quot; on
              the install form to override.
            </div>
          )}

          {!installSelected && (
            <div className="confirm-actions">
              <button
                type="button"
                className="lat-btn ghost"
                onClick={onDiscard}
                disabled={disabled || inFlight}
              >
                Discard
              </button>
              <button
                type="button"
                className="lat-btn primary"
                onClick={onSelectInstall}
                disabled={disabled || inFlight}
              >
                Install Staged Image
              </button>
            </div>
          )}

          {installSelected && (
            <StagedImageInstallForm
              staged={staged}
              options={options}
              onChangeOption={onChangeOption}
              forceUnknown={forceUnknown}
              onChangeForceUnknown={onChangeForceUnknown}
              unknownVersion={unknownVersion}
              skipPreflight={skipPreflight}
              onChangeSkipPreflight={onChangeSkipPreflight}
              onCancel={onCancelSelection}
              onConfirm={onConfirm}
              busy={startBusy}
              startError={startError}
            />
          )}
        </>
      )}
    </section>
  );
}

// ---------------------------------------------------------------------------
// StagedImageInstallForm — inline confirm form rendered inside
// UploadFirmwarePanel once the operator has chosen to install. Keeping
// the form inside the panel (rather than a sibling card) means the
// entire upload-→-install flow is anchored to one visual element, so
// clicking "Install Staged Image" never leaves the operator wondering
// whether anything happened.
// ---------------------------------------------------------------------------

function StagedImageInstallForm({
  staged,
  options,
  onChangeOption,
  forceUnknown,
  onChangeForceUnknown,
  unknownVersion,
  skipPreflight,
  onChangeSkipPreflight,
  onCancel,
  onConfirm,
  busy,
  startError,
}) {
  const optionConflict = options.testOnly && options.force;
  const preflightBlock = staged && !staged.preflightOk && !skipPreflight;

  return (
    <div className="firmware-confirm staged-install-form">
      <div className="confirm-summary">
        Install <strong>uploaded image</strong><br />
        File: <code>{staged?.filename ?? ''}</code>
        {staged?.sizeBytes > 0 && <> · {formatBytes(staged.sizeBytes)}</>}
        {staged?.sha256 && <><br />SHA-256: <code>{staged.sha256}</code></>}
      </div>

      <div className="options-row">
        <label className="lat-check">
          <input
            type="checkbox"
            checked={!options.noPreserveConfig}
            onChange={(e) => onChangeOption('noPreserveConfig', !e.target.checked)}
            disabled={busy}
          />
          Preserve configuration
        </label>
        <label className="lat-check">
          <input
            type="checkbox"
            checked={options.testOnly}
            onChange={(e) => onChangeOption('testOnly', e.target.checked)}
            disabled={busy}
          />
          Test only (-T)
        </label>
        <label className="lat-check">
          <input
            type="checkbox"
            checked={options.verbose}
            onChange={(e) => onChangeOption('verbose', e.target.checked)}
            disabled={busy}
          />
          Verbose (-v)
        </label>
        <label className="lat-check warn-text">
          <input
            type="checkbox"
            checked={options.quiet}
            onChange={(e) => onChangeOption('quiet', e.target.checked)}
            disabled={busy}
          />
          Quiet (-q)
        </label>
        <label className="lat-check crit-text">
          <input
            type="checkbox"
            checked={options.force}
            onChange={(e) => onChangeOption('force', e.target.checked)}
            disabled={busy}
          />
          Force (-F)
        </label>
        {unknownVersion && (
          <label className="lat-check warn-text">
            <input
              type="checkbox"
              checked={forceUnknown}
              onChange={(e) => onChangeForceUnknown(e.target.checked)}
              disabled={busy}
            />
            Allow install with unknown current version
          </label>
        )}
        {staged && !staged.preflightOk && (
          <label className="lat-check crit-text">
            <input
              type="checkbox"
              checked={skipPreflight}
              onChange={(e) => onChangeSkipPreflight(e.target.checked)}
              disabled={busy}
            />
            Skip preflight (install despite -T failure)
          </label>
        )}
      </div>

      {optionConflict && (
        <div className="lat-alert crit">
          <strong>Conflict.</strong> Test-only and Force are mutually exclusive.
        </div>
      )}

      {preflightBlock && (
        <div className="lat-alert crit">
          <strong>Preflight failed.</strong> sysupgrade -T rejected this image
          ({staged?.preflightError || 'unknown'}). Tick &quot;Skip preflight&quot;
          to override at your own risk.
        </div>
      )}

      {options.noPreserveConfig ? (
        <div className="lat-alert crit">
          <strong>Wipe configuration.</strong> The device will reboot with default
          settings. Network access may require a reset to defaults.
        </div>
      ) : (
        <div className="lat-alert warn">
          <strong>Heads up.</strong> This will reboot the device. Configuration
          is preserved; toggle off above to wipe.
        </div>
      )}

      {startError && (
        <div className="lat-alert crit">{errorMessage(startError)}</div>
      )}

      <div className="confirm-actions">
        <button
          type="button"
          className="lat-btn ghost"
          onClick={onCancel}
          disabled={busy}
        >
          Cancel
        </button>
        <button
          type="button"
          className="lat-btn danger"
          onClick={onConfirm}
          disabled={busy || optionConflict || preflightBlock}
        >
          Install — Device Will Reboot
        </button>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// ConfirmUpgradeCard — inline expansion below the selected release row
// (the staged-image flow uses StagedImageInstallForm inside the upload
// panel instead).
// ---------------------------------------------------------------------------

function ConfirmUpgradeCard({
  update,
  release,
  releaseLoading,
  releaseError,
  options,
  onChangeOption,
  onCancel,
  onConfirm,
  busy,
  startError,
  forceUnknown,
  onChangeForceUnknown,
  unknownVersion,
}) {
  const tag = update?.release?.tag ?? '';
  const assetName = update?.matchedAsset?.name ?? '';
  const sizeBytes = update?.matchedAsset?.sizeBytes ?? 0;

  const notes = useMemo(() => {
    const body = release?.body ?? update?.release?.body ?? '';
    return renderReleaseNotes(body);
  }, [release, update]);

  const optionConflict = options.testOnly && options.force;

  return (
    <div className="firmware-confirm">
      <div className="confirm-summary">
        Install <strong>{tag}</strong><br />
        Asset: <code>{assetName}</code>
        {sizeBytes > 0 && <> · {formatBytes(sizeBytes)}</>}
      </div>

      <div className="release-notes">
        {releaseLoading && (
          <p className="release-empty"><em>Loading release notes…</em></p>
        )}
        {!releaseLoading && releaseError && (
          <p className="release-empty"><em>{errorMessage(releaseError)}</em></p>
        )}
        {!releaseLoading && !releaseError && notes.length > 0 && notes}
        {!releaseLoading && !releaseError && notes.length === 0 && (
          <p className="release-empty"><em>No release notes provided.</em></p>
        )}
      </div>

      <div className="options-row">
        <label className="lat-check">
          <input
            type="checkbox"
            checked={!options.noPreserveConfig}
            onChange={(e) => onChangeOption('noPreserveConfig', !e.target.checked)}
            disabled={busy}
          />
          Preserve configuration
        </label>
        <label className="lat-check">
          <input
            type="checkbox"
            checked={options.testOnly}
            onChange={(e) => onChangeOption('testOnly', e.target.checked)}
            disabled={busy}
          />
          Test only (-T)
        </label>
        <label className="lat-check">
          <input
            type="checkbox"
            checked={options.verbose}
            onChange={(e) => onChangeOption('verbose', e.target.checked)}
            disabled={busy}
          />
          Verbose (-v)
        </label>
        <label className="lat-check warn-text">
          <input
            type="checkbox"
            checked={options.quiet}
            onChange={(e) => onChangeOption('quiet', e.target.checked)}
            disabled={busy}
          />
          Quiet (-q)
        </label>
        <label className="lat-check crit-text">
          <input
            type="checkbox"
            checked={options.force}
            onChange={(e) => onChangeOption('force', e.target.checked)}
            disabled={busy}
          />
          Force (-F)
        </label>
        {unknownVersion && (
          <label className="lat-check warn-text">
            <input
              type="checkbox"
              checked={forceUnknown}
              onChange={(e) => onChangeForceUnknown(e.target.checked)}
              disabled={busy}
            />
            Allow install with unknown current version
          </label>
        )}
      </div>

      {optionConflict && (
        <div className="lat-alert crit">
          <strong>Conflict.</strong> Test-only and Force are mutually exclusive.
        </div>
      )}

      {options.noPreserveConfig ? (
        <div className="lat-alert crit">
          <strong>Wipe configuration.</strong> The device will reboot with default
          settings. Network access may require a reset to defaults.
        </div>
      ) : (
        <div className="lat-alert warn">
          <strong>Heads up.</strong> This will reboot the device. Configuration
          is preserved; toggle off above to wipe.
        </div>
      )}

      {startError && (
        <div className="lat-alert crit">{errorMessage(startError)}</div>
      )}

      <div className="confirm-actions">
        <button type="button" className="lat-btn ghost" onClick={onCancel} disabled={busy}>
          Cancel
        </button>
        <button
          type="button"
          className="lat-btn danger"
          onClick={onConfirm}
          disabled={busy || optionConflict}
        >
          Install — Device Will Reboot
        </button>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// UpgradeProgressPanel
// ---------------------------------------------------------------------------

function UpgradeProgressPanel({ event, streamLost, onCancel, cancelBusy, cancelError }) {
  if (!event) return null;
  const phase = event.phase;
  if (phase === Phase.IDLE || phase === Phase.UNSPECIFIED) return null;

  const cancelable = phase === Phase.DOWNLOADING || phase === Phase.VERIFYING;
  const failed = phase === Phase.FAILED;
  const upgrading = phase === Phase.UPGRADING;

  let alert = null;
  if (streamLost && upgrading) {
    alert = (
      <div className="lat-alert ok">
        <strong>Connection lost.</strong> The device is rebooting after the
        flash. Try refreshing this page in 60 seconds.
      </div>
    );
  } else if (upgrading) {
    alert = (
      <div className="lat-alert ok">
        <strong>Sysupgrade is running.</strong> The device will reboot when the
        flash completes. This connection will drop.
      </div>
    );
  } else if (phase === Phase.READY) {
    alert = (
      <div className="lat-alert ok">
        <strong>Image verified.</strong> Launching sysupgrade…
      </div>
    );
  } else if (failed) {
    alert = (
      <div className="lat-alert crit">
        <strong>Upgrade failed.</strong> {event.error || event.message || 'Unknown error.'}
      </div>
    );
  }

  const cls = ['lat-panel', 'firmware-progress-card'];
  if (upgrading) cls.push('firmware-rebooting');
  if (failed) cls.push('firmware-failed');

  return (
    <section className={cls.join(' ')}>
      <div className="progress-headline">
        <span>◇ Phase: {phaseLabel(phase)}{upgrading ? ' — Detached' : ''}</span>
        <span className="progress-percent">{event.percent ?? 0}%</span>
      </div>
      <div className={`pbar${failed ? ' crit' : ''}`}>
        <span style={{ width: `${Math.max(0, Math.min(100, event.percent ?? 0))}%` }} />
      </div>
      <div className="progress-meta">
        {event.releaseTag && (
          <span>Release: <span className="v">{event.releaseTag}</span></span>
        )}
        {event.assetName && (
          <span>Asset: <span className="v">{event.assetName}</span></span>
        )}
        {event.bytesTotal > 0 && (
          <span>
            Bytes:{' '}
            <span className="v">
              {formatBytes(event.bytesDone)} / {formatBytes(event.bytesTotal)}
            </span>
          </span>
        )}
        {upgrading && event.childPid > 0 && (
          <span>Child PID: <span className="v">{event.childPid}</span></span>
        )}
        {event.message && !upgrading && !failed && (
          <span>Status: <span className="v">{event.message}</span></span>
        )}
      </div>

      {alert}

      {failed && event.logTail && (
        <pre className="firmware-log-tail" aria-label="sysupgrade log tail">
          {event.logTail}
        </pre>
      )}

      {cancelError && (
        <div className="lat-alert crit">{errorMessage(cancelError)}</div>
      )}

      {cancelable && (
        <div className="confirm-actions">
          <button
            type="button"
            className="lat-btn ghost"
            onClick={onCancel}
            disabled={cancelBusy}
          >
            {cancelBusy ? 'Cancelling…' : 'Cancel Upgrade'}
          </button>
        </div>
      )}
    </section>
  );
}

// ---------------------------------------------------------------------------
// Page
// ---------------------------------------------------------------------------

const DEFAULT_OPTIONS = {
  noPreserveConfig: false,
  testOnly: false,
  force: false,
  quiet: false,
  verbose: false,
};

// ---------------------------------------------------------------------------
// FactoryResetPanel
//
// Destructive recovery action — equivalent to LuCI's "Perform Reset"
// button. Wipes the overlay (everything in /etc/config, host keys,
// stored credentials, etc.) and reboots the device to first-boot
// defaults. The currently-installed firmware image stays exactly as
// it is.
//
// Three-stage gate: (1) "Begin reset…" expands the form, (2) operator
// types the device hostname, (3) operator ticks the acknowledge box.
// Final solid-red button is disabled until 2 and 3 clear. Stronger
// than LuCI's single confirm() dialog; mirrors the GitHub
// repo-deletion pattern.
// ---------------------------------------------------------------------------

function FactoryResetPanel({
  capability,
  loading,
  error,
  expanded,
  typedHostname,
  acknowledged,
  busy,
  triggered,
  resetError,
  onBegin,
  onCancel,
  onChangeTypedHostname,
  onChangeAcknowledged,
  onConfirm,
}) {
  if (loading && !capability) {
    return (
      <section className="lat-panel firmware-panel firmware-reset-panel">
        <div className="panel-head"><h3>Factory Reset</h3></div>
        <div className="firmware-empty">Loading…</div>
      </section>
    );
  }

  if (error && !capability) {
    return (
      <section className="lat-panel firmware-panel firmware-reset-panel">
        <div className="panel-head"><h3>Factory Reset</h3></div>
        <div className="lat-alert crit">{errorMessage(error)}</div>
      </section>
    );
  }

  const cap = capability;
  const capable = cap?.capable ?? false;
  const hostname = cap?.hostname ?? '';

  const hostnameMatches =
    hostname.length > 0 &&
    typedHostname.trim().toLowerCase() === hostname.trim().toLowerCase();
  const armed = capable && hostnameMatches && acknowledged && !busy;

  const cls = ['lat-panel', 'firmware-panel', 'firmware-reset-panel'];
  if (triggered) cls.push('firmware-reset-rebooting');

  return (
    <section className={cls.join(' ')}>
      <div className="panel-head">
        <h3>Factory Reset</h3>
        <div className="panel-head-right">
          {triggered ? (
            <span className="lat-chip crit"><span className="dot" />Resetting</span>
          ) : capable ? (
            <>
              <span className="lat-chip ok"><span className="dot" />Available</span>
              <span className="lat-chip crit"><span className="dot" />{armed ? 'Armed' : 'Destructive'}</span>
            </>
          ) : (
            <span className="lat-chip crit"><span className="dot" />Not Available</span>
          )}
        </div>
      </div>

      {!triggered && (
        <div className="status-strip">
          <KV k="Hostname" v={hostname || '—'} mono />
          <KV
            k="Overlay"
            v={cap?.overlayMountpoint || (capable ? '' : 'none')}
            mono
            variant={capable ? 'ok' : 'crit'}
          />
          <KV k="Backing FS" v={cap?.backingFs || '—'} mono />
          <KV
            k="Helper"
            v={cap?.firstbootPath || (capable ? '/sbin/firstboot' : '/sbin/firstboot (missing)')}
            mono
            variant={capable ? undefined : 'muted'}
          />
          <KV k="Mode" v={capable ? 'wipe-in-place + reboot' : '—'} mono />
          <KV
            k={capable ? 'Capability' : 'Reason'}
            v={capable ? 'ok' : (cap?.reason || 'unknown')}
            mono
            variant={capable ? 'ok' : 'crit'}
          />
        </div>
      )}

      {triggered ? (
        <div className="lat-alert crit">
          <strong>Reset triggered.</strong> The device is wiping its overlay and
          rebooting now. This connection has dropped. Reconnect at the default
          IP (<code>10.41.254.1</code>) in 60–90 seconds. The hostname, wifi
          config, and OpenMANET settings have all been erased — you will be
          back at first-boot defaults.
        </div>
      ) : !capable ? (
        <div className="lat-alert warn">
          <strong>Factory reset unavailable.</strong> This system isn&apos;t running
          an OpenWrt squashfs+overlay image, so there is no overlay to wipe.
          The destructive action is intentionally hidden on incapable devices
          to prevent accidental misuse on a developer laptop or single-rootfs
          factory image.
        </div>
      ) : (
        <>
          <div className="lat-alert crit">
            <strong>This wipes configuration, not firmware.</strong>{' '}
            The currently-installed firmware image stays exactly as it is;
            only its persistent overlay is erased. After reboot the device
            returns to first-boot defaults.
            <ul>
              <li><b>Wiped:</b> all UCI settings (<code>/etc/config/*</code>),
                dropbear host keys, the LuCI SSL cert, hostname, every wifi
                password, every stored credential, all OpenMANET runtime state.</li>
              <li><b>Survives:</b> the firmware image itself (<code>/rom</code>),
                the kernel, bootloader env, MAC addresses, board calibration data.</li>
              <li><b>You will likely need physical access to recover.</b>{' '}
                The device returns at <code>10.41.254.1</code> with default
                credentials.</li>
            </ul>
          </div>

          {!expanded ? (
            <div className="confirm-actions">
              <button
                type="button"
                className="lat-btn danger"
                onClick={onBegin}
                disabled={busy}
              >
                Begin reset…
              </button>
            </div>
          ) : (
            <div className="reset-confirm-form">
              <div className="reset-row">
                <span className="reset-label crit">Type hostname</span>
                <div>
                  <input
                    type="text"
                    className={
                      'lat-input' +
                      (hostnameMatches
                        ? ' ok-match'
                        : typedHostname.length > 0
                          ? ' crit'
                          : '')
                    }
                    placeholder={hostname}
                    value={typedHostname}
                    onChange={(e) => onChangeTypedHostname(e.target.value)}
                    disabled={busy}
                    aria-label="confirm hostname"
                  />
                  <div className={
                    'reset-hint' +
                    (hostnameMatches ? ' ok' : typedHostname.length > 0 ? ' crit' : '')
                  }>
                    {hostnameMatches
                      ? 'Match.'
                      : <>Type <span className="v">{hostname}</span> exactly to confirm.</>}
                  </div>
                </div>
              </div>

              <div className="reset-row">
                <span className="reset-label crit">Acknowledge</span>
                <label className="lat-check crit-text">
                  <input
                    type="checkbox"
                    checked={acknowledged}
                    onChange={(e) => onChangeAcknowledged(e.target.checked)}
                    disabled={busy}
                  />
                  I understand this will wipe all configuration.
                </label>
              </div>

              {resetError && (
                <div className="lat-alert crit">{errorMessage(resetError)}</div>
              )}

              <div className="reset-actions">
                <button
                  type="button"
                  className="lat-btn ghost"
                  onClick={onCancel}
                  disabled={busy}
                >
                  Cancel
                </button>
                <button
                  type="button"
                  className="lat-btn danger filled"
                  onClick={onConfirm}
                  disabled={!armed}
                >
                  {busy ? 'Resetting…' : 'Reset to factory defaults'}
                </button>
              </div>
            </div>
          )}
        </>
      )}
    </section>
  );
}

export default function SettingsFirmware() {
  const [info, setInfo] = useState(null);
  const [infoLoading, setInfoLoading] = useState(true);
  const [infoError, setInfoError] = useState(null);

  const [updates, setUpdates] = useState([]);
  const [fetchedAt, setFetchedAt] = useState(null);
  const [updatesLoading, setUpdatesLoading] = useState(false);
  const [updatesError, setUpdatesError] = useState(null);
  const [includePrerelease, setIncludePrerelease] = useState(false);

  const [selected, setSelected] = useState(null);
  const [release, setRelease] = useState(null);
  const [releaseLoading, setReleaseLoading] = useState(false);
  const [releaseError, setReleaseError] = useState(null);

  const [options, setOptions] = useState(DEFAULT_OPTIONS);
  const [forceUnknown, setForceUnknown] = useState(false);
  const [startError, setStartError] = useState(null);
  const [startBusy, setStartBusy] = useState(false);

  const [progress, setProgress] = useState(null);
  const [streamLost, setStreamLost] = useState(false);
  const [cancelBusy, setCancelBusy] = useState(false);
  const [cancelError, setCancelError] = useState(null);

  // ---- staged uploaded image ----
  const [staged, setStaged] = useState(null);
  const [stagedLoading, setStagedLoading] = useState(true);
  const [stagedError, setStagedError] = useState(null);
  const [skipPreflight, setSkipPreflight] = useState(false);
  const [uploadState, setUploadState] = useState({
    status: 'idle',
    filename: '',
    loaded: 0,
    total: 0,
    error: null,
  });
  const uploadAbortRef = useRef(null);
  const fileInputRef = useRef(null);

  // ---- factory reset ----
  const [resetCap, setResetCap] = useState(null);
  const [resetCapLoading, setResetCapLoading] = useState(true);
  const [resetCapError, setResetCapError] = useState(null);
  const [resetExpanded, setResetExpanded] = useState(false);
  const [resetTypedHostname, setResetTypedHostname] = useState('');
  const [resetAck, setResetAck] = useState(false);
  const [resetBusy, setResetBusy] = useState(false);
  const [resetTriggered, setResetTriggered] = useState(false);
  const [resetError, setResetError] = useState(null);

  // selectionSource is 'release' | 'local' | null. 'release' uses the
  // existing AvailableUpdates table selection; 'local' uses the staged
  // upload as the install source. Both flow through ConfirmUpgradeCard
  // and StartLocal/StartUpgrade respectively.
  const [selectionSource, setSelectionSource] = useState(null);

  // ---- system info ----
  const reloadInfo = useCallback(async () => {
    setInfoLoading(true);
    try {
      const i = await fetchSystemInfo();
      setInfo(i);
      setInfoError(null);
    } catch (e) {
      setInfoError(e);
    } finally {
      setInfoLoading(false);
    }
  }, []);

  useEffect(() => { reloadInfo(); }, [reloadInfo]);

  // ---- streaming progress + initial snapshot ----
  useEffect(() => {
    let cancelled = false;
    const controller = new AbortController();

    (async () => {
      try {
        const snap = await getUpgradeStatus();
        if (!cancelled) setProgress(snap);
      } catch {
        // first snapshot is best-effort; the stream will fill it in.
      }

      try {
        for await (const ev of streamUpgradeProgress(controller.signal)) {
          if (cancelled) break;
          setProgress(ev);
          setStreamLost(false);
        }
      } catch {
        if (!cancelled) setStreamLost(true);
      }
    })();

    return () => {
      cancelled = true;
      controller.abort();
    };
  }, []);

  const inFlight = progress != null && ACTIVE_PHASES.has(progress.phase);

  // ---- list available updates ----
  const reloadUpdates = useCallback(async ({ forceRefresh = false } = {}) => {
    setUpdatesLoading(true);
    try {
      const result = await listAvailableUpdates({ forceRefresh, includePrerelease });
      setUpdates(result.updates);
      setFetchedAt(result.fetchedAt);
      setUpdatesError(null);
    } catch (e) {
      setUpdatesError(e);
    } finally {
      setUpdatesLoading(false);
    }
  }, [includePrerelease]);

  useEffect(() => {
    if (info?.sysupgradeCapable) {
      reloadUpdates({ forceRefresh: false });
    }
  }, [info?.sysupgradeCapable, reloadUpdates]);

  const handleCheck = useCallback(() => {
    reloadUpdates({ forceRefresh: true });
  }, [reloadUpdates]);

  const handleTogglePrerelease = useCallback((checked) => {
    setIncludePrerelease(checked);
  }, []);

  // ---- selection + release detail ----
  const lastFetchedTagRef = useRef(null);
  useEffect(() => {
    if (!selected) return undefined;
    const tag = selected.release?.tag;
    if (!tag) return undefined;
    if (lastFetchedTagRef.current === tag) return undefined;
    lastFetchedTagRef.current = tag;

    let cancelled = false;
    setReleaseLoading(true);
    setRelease(null);
    setReleaseError(null);
    (async () => {
      try {
        const r = await getReleaseDetail(tag);
        if (!cancelled) setRelease(r);
      } catch (e) {
        if (!cancelled) setReleaseError(e);
      } finally {
        if (!cancelled) setReleaseLoading(false);
      }
    })();
    return () => { cancelled = true; };
  }, [selected]);

  const handleSelect = useCallback((update) => {
    setSelected(update);
    setSelectionSource('release');
    setStartError(null);
    setOptions(DEFAULT_OPTIONS);
    setForceUnknown(false);
    setSkipPreflight(false);
  }, []);

  const handleSelectLocal = useCallback(() => {
    setSelected(null);
    setSelectionSource('local');
    setStartError(null);
    setOptions(DEFAULT_OPTIONS);
    setForceUnknown(false);
    setSkipPreflight(false);
  }, []);

  const handleCancelSelection = useCallback(() => {
    setSelected(null);
    setSelectionSource(null);
    setRelease(null);
    setReleaseError(null);
    setStartError(null);
    setSkipPreflight(false);
    lastFetchedTagRef.current = null;
  }, []);

  const handleChangeOption = useCallback((key, value) => {
    setOptions((prev) => ({ ...prev, [key]: value }));
  }, []);

  // ---- confirm install ----
  const handleConfirm = useCallback(async () => {
    setStartBusy(true);
    setStartError(null);
    try {
      if (selectionSource === 'local') {
        if (!staged) {
          throw new Error('No staged image available.');
        }
        await startLocalUpgrade({
          options,
          forceInstallUnknownCurrent: forceUnknown,
          skipPreflight,
        });
      } else {
        if (!selected) return;
        const tag = selected.release?.tag;
        const assetName = selected.matchedAsset?.name;
        if (!tag || !assetName) {
          throw new Error('Selected release is missing a matching asset.');
        }
        await startUpgrade({
          releaseTag: tag,
          assetName,
          options,
          forceInstallUnknownCurrent: forceUnknown,
        });
      }
    } catch (e) {
      setStartError(e);
    } finally {
      setStartBusy(false);
    }
  }, [selectionSource, selected, staged, options, forceUnknown, skipPreflight]);

  const handleCancelUpgrade = useCallback(async () => {
    setCancelBusy(true);
    setCancelError(null);
    try {
      await cancelUpgrade();
    } catch (e) {
      setCancelError(e);
    } finally {
      setCancelBusy(false);
    }
  }, []);

  // ---- staged image fetch + upload + discard ----
  const reloadStaged = useCallback(async () => {
    setStagedLoading(true);
    try {
      const s = await getStagedImage();
      setStaged(s);
      setStagedError(null);
    } catch (e) {
      setStagedError(e);
    } finally {
      setStagedLoading(false);
    }
  }, []);

  useEffect(() => {
    if (info?.sysupgradeCapable) {
      reloadStaged();
    }
  }, [info?.sysupgradeCapable, reloadStaged]);

  // ---- factory reset capability load ----
  const reloadResetCap = useCallback(async () => {
    setResetCapLoading(true);
    try {
      const cap = await fetchFactoryResetCapability();
      setResetCap(cap);
      setResetCapError(null);
    } catch (e) {
      setResetCapError(e);
    } finally {
      setResetCapLoading(false);
    }
  }, []);

  useEffect(() => {
    reloadResetCap();
  }, [reloadResetCap]);

  const handleBeginReset = useCallback(() => {
    setResetExpanded(true);
    setResetTypedHostname('');
    setResetAck(false);
    setResetError(null);
  }, []);

  const handleCancelReset = useCallback(() => {
    setResetExpanded(false);
    setResetTypedHostname('');
    setResetAck(false);
    setResetError(null);
  }, []);

  const handleConfirmReset = useCallback(async () => {
    setResetBusy(true);
    setResetError(null);
    try {
      await performFactoryReset({ confirmHostname: resetTypedHostname });
      // The RPC may have actually returned 200 OK before firstboot
      // killed the daemon — either way the device is now rebooting.
      setResetTriggered(true);
    } catch (e) {
      // A transport-closed error is normal — firstboot kills the
      // daemon mid-response. Treat that the same as success: the
      // reset was initiated. Differentiate it from explicit RPC
      // errors (InvalidArgument hostname mismatch, FailedPrecondition
      // not capable, etc.) by checking for a Connect error code.
      const code = e?.code;
      const isExplicitRPCError =
        code === 'invalid_argument' ||
        code === 'failed_precondition' ||
        code === 'unauthenticated' ||
        code === 'permission_denied';

      if (isExplicitRPCError) {
        setResetError(e);
      } else {
        setResetTriggered(true);
      }
    } finally {
      setResetBusy(false);
    }
  }, [resetTypedHostname]);

  const handlePickFile = useCallback(async (file) => {
    if (!file) return;
    if (uploadAbortRef.current) {
      uploadAbortRef.current.abort();
    }
    const controller = new AbortController();
    uploadAbortRef.current = controller;

    setUploadState({
      status: 'uploading',
      filename: file.name || 'uploaded-image.bin',
      loaded: 0,
      total: file.size || 0,
      error: null,
    });
    setStagedError(null);

    try {
      const result = await uploadFirmware(file, {
        signal: controller.signal,
        onProgress: ({ loaded, total }) => {
          setUploadState((prev) =>
            prev.status === 'uploading' ? { ...prev, loaded, total } : prev,
          );
        },
      });
      setStaged(result);
      setUploadState({ status: 'idle', filename: '', loaded: 0, total: 0, error: null });
      // Auto-clear any stale GitHub selection so the operator's
      // attention moves to the freshly uploaded image.
      if (selectionSource === 'release') {
        setSelected(null);
        setSelectionSource(null);
        lastFetchedTagRef.current = null;
      }
    } catch (e) {
      if (e?.name === 'AbortError') {
        setUploadState({ status: 'idle', filename: '', loaded: 0, total: 0, error: null });
        return;
      }
      setUploadState((prev) => ({ ...prev, status: 'error', error: e }));
      // Surface the error in the panel header alert too.
      setStagedError(e);
      setTimeout(() => {
        setUploadState({ status: 'idle', filename: '', loaded: 0, total: 0, error: null });
      }, 4000);
    } finally {
      if (uploadAbortRef.current === controller) {
        uploadAbortRef.current = null;
      }
    }
  }, [selectionSource]);

  const handleCancelUpload = useCallback(() => {
    if (uploadAbortRef.current) {
      uploadAbortRef.current.abort();
      uploadAbortRef.current = null;
    }
  }, []);

  const handleDiscardStaged = useCallback(async () => {
    setStagedError(null);
    try {
      await discardStagedImage();
      setStaged(null);
      if (selectionSource === 'local') {
        setSelectionSource(null);
        setStartError(null);
      }
    } catch (e) {
      setStagedError(e);
    }
  }, [selectionSource]);

  const unknownVersion = !info?.openmanetVersion;

  // updates panel disables row Install buttons when an upgrade is already
  // running or when the device is incapable. The panel itself stays visible
  // for context; the individual buttons are gated.
  const installDisabled = inFlight || !info?.sysupgradeCapable;

  return (
    <div className="settings-firmware">
      <h2 className="settings-h2">◇ Firmware</h2>
      <div className="settings-firmware-list">
        <SystemInfoPanel
          info={info}
          loading={infoLoading}
          error={infoError}
          onRefresh={reloadInfo}
        />

        {info?.sysupgradeCapable && (
          <AvailableUpdatesPanel
            updates={updates}
            fetchedAt={fetchedAt}
            loading={updatesLoading}
            includePrerelease={includePrerelease}
            onTogglePrerelease={handleTogglePrerelease}
            onCheck={handleCheck}
            selectedTag={selectionSource === 'release' ? (selected?.release?.tag ?? null) : null}
            onSelect={handleSelect}
            disabled={installDisabled}
            inFlight={inFlight}
            error={updatesError}
          />
        )}

        {info?.sysupgradeCapable && (
          <UploadFirmwarePanel
            staged={staged}
            loading={stagedLoading}
            uploadState={uploadState}
            onPickFile={handlePickFile}
            onCancel={handleCancelUpload}
            onDiscard={handleDiscardStaged}
            onSelectInstall={handleSelectLocal}
            installSelected={selectionSource === 'local'}
            disabled={installDisabled}
            inFlight={inFlight}
            error={stagedError}
            fileInputRef={fileInputRef}
            options={options}
            onChangeOption={handleChangeOption}
            forceUnknown={forceUnknown}
            onChangeForceUnknown={setForceUnknown}
            unknownVersion={unknownVersion}
            skipPreflight={skipPreflight}
            onChangeSkipPreflight={setSkipPreflight}
            onCancelSelection={handleCancelSelection}
            onConfirm={handleConfirm}
            startBusy={startBusy}
            startError={startError}
          />
        )}

        {selectionSource === 'release' && selected && info?.sysupgradeCapable && !inFlight && (
          <ConfirmUpgradeCard
            update={selected}
            release={release}
            releaseLoading={releaseLoading}
            releaseError={releaseError}
            options={options}
            onChangeOption={handleChangeOption}
            onCancel={handleCancelSelection}
            onConfirm={handleConfirm}
            busy={startBusy}
            startError={startError}
            forceUnknown={forceUnknown}
            onChangeForceUnknown={setForceUnknown}
            unknownVersion={unknownVersion}
          />
        )}

        <UpgradeProgressPanel
          event={progress}
          streamLost={streamLost}
          onCancel={handleCancelUpgrade}
          cancelBusy={cancelBusy}
          cancelError={cancelError}
        />

        <FactoryResetPanel
          capability={resetCap}
          loading={resetCapLoading}
          error={resetCapError}
          expanded={resetExpanded}
          typedHostname={resetTypedHostname}
          acknowledged={resetAck}
          busy={resetBusy}
          triggered={resetTriggered}
          resetError={resetError}
          onBegin={handleBeginReset}
          onCancel={handleCancelReset}
          onChangeTypedHostname={setResetTypedHostname}
          onChangeAcknowledged={setResetAck}
          onConfirm={handleConfirmReset}
        />
      </div>
    </div>
  );
}
