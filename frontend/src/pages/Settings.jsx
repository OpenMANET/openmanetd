// =============================================================================
// Settings.jsx — Device settings page
// =============================================================================
// Rewritten for Lattice: section-nav header, multi-card grid, Lattice-styled
// inputs/toggles/buttons. Label copy is preserved so existing tests continue
// to assert against the same strings.

import React, { useState, useEffect, useCallback, useMemo } from 'react';
import { createClient } from "@connectrpc/connect";
import { transport } from "../services/connectClient.js";
import authFetch from "../services/authFetch.js";
import { DashboardService } from "../gen/openmanet/dashboard/v1/dashboard_service_pb.js";
import { QuickAction } from "../gen/openmanet/dashboard/v1/dashboard_pb.js";
import { CommsService } from "../gen/openmanet/comms/v1/comms_service_pb.js";
import { ControlSource } from "../gen/openmanet/comms/v1/config_pb.js";
import WhisperManager from "../components/WhisperManager.jsx";
import LatSelect from "../components/LatSelect.jsx";
import { useAuth } from '../contexts/useAuth.js';
import './Settings.css';

const dashboardClient = createClient(DashboardService, transport);
const commsClient = createClient(CommsService, transport);

function controlSourceToString(cs) {
  switch (cs) {
    case ControlSource.OPENVLM: return 'openvlm';
    case ControlSource.NANOPTT: return 'nanoptt';
    case ControlSource.BS22: return 'bs22';
    case ControlSource.BLUEALSA_XEVENT: return 'bluealsa_xevent';
    default:                    return 'web';
  }
}

function stringToControlSource(s) {
  switch (s) {
    case 'openvlm': return ControlSource.OPENVLM;
    case 'nanoptt': return ControlSource.NANOPTT;
    case 'bs22': return ControlSource.BS22;
    case 'bluealsa_xevent': return ControlSource.BLUEALSA_XEVENT;
    default:        return ControlSource.WEB;
  }
}

// Simple YAML formatter for preview display (no external library needed).
function formatYaml(obj, indent) {
  if (!obj || typeof obj !== 'object') return '';
  const pad = '  '.repeat(indent);
  let out = '';
  for (const [key, val] of Object.entries(obj)) {
    if (val === null || val === undefined) {
      out += `${pad}${key}:\n`;
    } else if (Array.isArray(val)) {
      out += `${pad}${key}:\n`;
      val.forEach((item) => { out += `${pad}  - ${item}\n`; });
    } else if (typeof val === 'object') {
      out += `${pad}${key}:\n${formatYaml(val, indent + 1)}`;
    } else {
      out += `${pad}${key}: ${val}\n`;
    }
  }
  return out;
}

function LatToggle({ checked, onChange, labelOn = 'On', labelOff = 'Off' }) {
  return (
    <button
      type="button"
      className={`lat-toggle${checked ? ' on' : ''}`}
      onClick={() => onChange(!checked)}
    >
      <span className="track"><span className="thumb" /></span>
      <span className="label">{checked ? labelOn : labelOff}</span>
    </button>
  );
}

export default function SettingsPage() {
  const { changePassword, authEnabled } = useAuth();
  const [hostname, setHostname] = useState('');
  const [originalHostname, setOriginalHostname] = useState('');
  const [config, setConfig] = useState({
    comms_enabled: true,
    control_source: 'web',
    debug: false,
  });
  const [originalConfig, setOriginalConfig] = useState(null);
  const [fullConfig, setFullConfig] = useState(null);
  const [showRaw, setShowRaw] = useState(false);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [restarting, setRestarting] = useState(false);
  const [error, setError] = useState(null);
  const [success, setSuccess] = useState(null);

  // Passphrase panel state.
  const [currentPw, setCurrentPw] = useState('');
  const [newPw, setNewPw] = useState('');
  const [confirmPw, setConfirmPw] = useState('');
  const [pwSaving, setPwSaving] = useState(false);
  const [pwError, setPwError] = useState(null);
  const [pwSuccess, setPwSuccess] = useState(null);

  const fetchSettings = useCallback(async () => {
    try {
      const results = await Promise.allSettled([
        authFetch('/api/settings/hostname').then(r => r.ok ? r.json() : Promise.reject(r.status)),
        authFetch('/api/settings/config').then(r => r.ok ? r.json() : Promise.reject(r.status)),
        commsClient.getCommsConfig({}),
      ]);

      if (results[0].status === 'fulfilled') {
        const h = results[0].value.hostname || results[0].value.name || '';
        setHostname(h);
        setOriginalHostname(h);
      }

      if (results[1].status === 'fulfilled') {
        setFullConfig(results[1].value);
      }

      // Prefer live RPC status; fall back to YAML config if RPC failed.
      let cfg;
      if (results[2].status === 'fulfilled') {
        const commsResp = results[2].value;
        cfg = {
          comms_enabled: commsResp.commsEnabled,
          control_source: controlSourceToString(commsResp.controlSource),
          debug: results[1].status === 'fulfilled' ? (results[1].value.comms?.debug ?? false) : false,
        };
      } else if (results[1].status === 'fulfilled') {
        const comms = results[1].value.comms || {};
        cfg = {
          comms_enabled: comms.enable ?? true,
          control_source: comms.controlSource ?? 'web',
          debug: comms.debug ?? false,
        };
      }

      if (cfg) {
        setConfig(cfg);
        setOriginalConfig(cfg);
      }

      setError(null);
    } catch (e) {
      setError('Failed to load settings: ' + e.message);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    // Fetch-on-mount: pull persisted config from the daemon to seed the form.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    fetchSettings();
  }, [fetchSettings]);

  // Build a live YAML preview by merging current UI state into the full config.
  const liveYaml = useMemo(() => {
    if (!fullConfig) return '';
    const merged = JSON.parse(JSON.stringify(fullConfig));
    if (!merged.comms) merged.comms = {};
    merged.comms.enable = config.comms_enabled;
    merged.comms.controlSource = config.control_source;
    merged.comms.debug = config.debug;
    return formatYaml(merged, 0);
  }, [fullConfig, config]);

  const clearMessages = () => { setError(null); setSuccess(null); };

  const handleSaveHostname = async () => {
    clearMessages();
    setSaving(true);
    try {
      const res = await authFetch('/api/settings/hostname', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ hostname }),
      });
      if (!res.ok) throw new Error('HTTP ' + res.status);
      setOriginalHostname(hostname);
      setSuccess('Hostname saved.');
    } catch (e) {
      setError('Failed to save hostname: ' + e.message);
    } finally {
      setSaving(false);
    }
  };

  const handleSaveConfig = async () => {
    clearMessages();
    setSaving(true);
    try {
      const enableChanged = config.comms_enabled !== originalConfig?.comms_enabled;
      const sourceChanged = config.control_source !== originalConfig?.control_source;
      const debugChanged = config.debug !== originalConfig?.debug;

      // Live-apply enable + control source via the comms RPC. The handler
      // persists to YAML and bounces only the comms subsystem — no daemon
      // restart needed.
      if (enableChanged || sourceChanged) {
        await commsClient.updateCommsConfig({
          enableComms: config.comms_enabled,
          controlSource: stringToControlSource(config.control_source),
        });
      }

      // Debug has no live-apply path; written to YAML and takes effect
      // after a daemon restart.
      if (debugChanged) {
        const res = await authFetch('/api/settings/config', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            comms: {
              debug: config.debug,
            },
          }),
        });
        if (!res.ok) throw new Error('HTTP ' + res.status);
      }

      setOriginalConfig({ ...config });
      setSuccess('Configuration saved.');
    } catch (e) {
      setError('Failed to save config: ' + e.message);
    } finally {
      setSaving(false);
    }
  };

  const handleRestart = async () => {
    setRestarting(true);
    clearMessages();
    try {
      const resp = await dashboardClient.executeQuickAction({
        action: QuickAction.RESTART_OPENMANETD,
      });
      if (!resp.success) throw new Error(resp.message || 'action failed');
      setSuccess('openmanetd restart requested.');
    } catch (e) {
      setError('Failed to restart: ' + e.message);
    } finally {
      setTimeout(() => setRestarting(false), 2000);
    }
  };

  const handleChangePassword = async (e) => {
    e?.preventDefault?.();
    setPwError(null);
    setPwSuccess(null);

    if (!newPw) {
      setPwError('New passphrase is required.');
      return;
    }
    if (newPw !== confirmPw) {
      setPwError('Passphrases do not match.');
      return;
    }

    setPwSaving(true);
    try {
      await changePassword(currentPw, newPw);
      setPwSuccess('Passphrase updated.');
      setCurrentPw('');
      setNewPw('');
      setConfirmPw('');
    } catch (err) {
      setPwError(err?.message || 'Failed to change passphrase.');
    } finally {
      setPwSaving(false);
    }
  };

  if (loading) {
    return (
      <div className="settings-wrapper">
        <h2 className="settings-h2">Settings</h2>
        <div className="lat-panel settings-loading">
          <div className="spinner" />
          <div>Loading settings...</div>
        </div>
      </div>
    );
  }

  const hostnameChanged = hostname !== originalHostname;
  const configChanged = originalConfig && (
    config.comms_enabled !== originalConfig.comms_enabled ||
    config.control_source !== originalConfig.control_source ||
    config.debug !== originalConfig.debug
  );

  return (
    <div className="settings-wrapper">
      <div className="lat-topbar">
        <div className="node-id">
          SETTINGS
          <span className="ip">openmanetd</span>
        </div>
        {configChanged && (
          <div className="chips">
            <span className="lat-chip warn"><span className="dot" /> UNSAVED CHANGES</span>
          </div>
        )}
      </div>
      <div className="lat-view-header">
        <div>
          <h2>◇ Settings</h2>
          <div className="crumb">Hostname · Service · Configuration · Raw YAML</div>
        </div>
      </div>

      {error && <div className="settings-banner crit">{error}</div>}
      {success && <div className="settings-banner ok">{success}</div>}

      <div className="lat-body settings-grid">

        {/* Hostname */}
        <div className="lat-panel">
          <div className="panel-head"><h3>Hostname</h3></div>
          <div className="settings-row">
            <input
              className="lat-input"
              type="text"
              value={hostname}
              onChange={(e) => setHostname(e.target.value)}
              placeholder="device-hostname"
            />
            <button
              className="lat-btn primary"
              onClick={handleSaveHostname}
              disabled={saving || !hostnameChanged}
              type="button"
            >
              {saving ? 'Saving...' : 'Save'}
            </button>
          </div>
        </div>

        {/* Service Control */}
        <div className="lat-panel">
          <div className="panel-head"><h3>Service Control</h3></div>
          <button
            className="lat-btn danger solid"
            onClick={handleRestart}
            disabled={restarting}
            type="button"
          >
            {restarting ? 'Restarting...' : 'Restart openmanetd'}
          </button>
        </div>

        {/* Passphrase (operator account) */}
        {authEnabled && (
          <div className="lat-panel">
            <div className="panel-head"><h3>Passphrase</h3></div>
            <form className="settings-password-form" onSubmit={handleChangePassword}>
              <div className="lat-field">
                <label htmlFor="current-pw">Current Passphrase</label>
                <input
                  id="current-pw"
                  className="lat-input"
                  type="password"
                  autoComplete="current-password"
                  value={currentPw}
                  onChange={(e) => setCurrentPw(e.target.value)}
                  disabled={pwSaving}
                />
              </div>
              <div className="lat-field">
                <label htmlFor="new-pw">New Passphrase</label>
                <input
                  id="new-pw"
                  className="lat-input"
                  type="password"
                  autoComplete="new-password"
                  value={newPw}
                  onChange={(e) => setNewPw(e.target.value)}
                  disabled={pwSaving}
                />
              </div>
              <div className="lat-field">
                <label htmlFor="confirm-pw">Confirm New Passphrase</label>
                <input
                  id="confirm-pw"
                  className="lat-input"
                  type="password"
                  autoComplete="new-password"
                  value={confirmPw}
                  onChange={(e) => setConfirmPw(e.target.value)}
                  disabled={pwSaving}
                />
              </div>

              {pwError && <div className="lat-alert crit" role="alert">{pwError}</div>}
              {pwSuccess && <div className="lat-alert ok" role="status">{pwSuccess}</div>}

              <button
                type="submit"
                className="lat-btn primary"
                disabled={pwSaving || !newPw || !confirmPw}
              >
                {pwSaving ? 'Updating…' : 'Update Passphrase'}
              </button>
            </form>
          </div>
        )}

        {/* Whisper Speech-to-Text (its own card) */}
        <WhisperManager />

        {/* OpenMANETd Config */}
        <div className="lat-panel col-span-all">
          <div className="panel-head"><h3>OpenMANETd Configuration</h3></div>

          <div className="settings-config-grid">
            <div className="lat-field">
              <label>Comms Radio</label>
              <LatToggle
                checked={config.comms_enabled}
                onChange={(v) => setConfig(c => ({ ...c, comms_enabled: v }))}
                labelOn="Enabled"
                labelOff="Disabled"
              />
            </div>

            <div className="lat-field">
              <label>Control Source</label>
              <LatSelect
                ariaLabel="Control Source"
                value={config.control_source}
                onChange={(v) => setConfig(c => ({ ...c, control_source: v }))}
                options={[
                  { value: 'openvlm', label: 'OpenVLM (default)' },
                  { value: 'web',     label: 'Web UI' },
                  { value: 'nanoptt', label: 'nanoPTT' },
                  { value: 'bs22', label: 'BTECH BS-22 (native Bluetooth)' },
                  { value: 'bluealsa_xevent', label: 'BlueALSA XEVENT (Bluetooth audio)' },
                ]}
              />
            </div>

            <div className="lat-field">
              <label>Debug Mode</label>
              <LatToggle
                checked={config.debug}
                onChange={(v) => setConfig(c => ({ ...c, debug: v }))}
                labelOn="On"
                labelOff="Off"
              />
            </div>
          </div>

          <div className="settings-save-row">
            <button
              className="lat-btn primary"
              onClick={handleSaveConfig}
              disabled={saving || !configChanged}
              type="button"
            >
              {saving ? 'Saving...' : 'Save Configuration'}
            </button>
            {configChanged && (
              <span className="settings-unsaved">Unsaved changes</span>
            )}
          </div>
        </div>

        {/* Raw Config View */}
        <div className="lat-panel col-span-all">
          <button
            type="button"
            className="settings-raw-toggle"
            onClick={() => setShowRaw(!showRaw)}
          >
            <span className="settings-chevron">{showRaw ? '\u25BC' : '\u25B6'}</span>
            <h3>Raw Configuration (YAML)</h3>
          </button>
          {showRaw && (
            <pre className="settings-yaml">
              {liveYaml || 'No configuration data available.'}
            </pre>
          )}
        </div>
      </div>
    </div>
  );
}
