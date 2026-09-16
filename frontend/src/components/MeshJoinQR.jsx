// =============================================================================
// MeshJoinQR.jsx — this node's mesh credentials as a QR code + summary
// =============================================================================
//
// Fetches GetMeshJoinQR once on mount and again whenever refreshKey
// changes (the parent bumps it after a join or on Refresh). No polling:
// credentials change only through this UI. Passphrases are masked until
// the operator presses Reveal.

import { useCallback, useEffect, useState } from 'react';
import { Code, ConnectError } from '@connectrpc/connect';
import { getMeshJoinQR } from '../services/meshJoinApi.js';
import './MeshJoinQR.css';

const MASK = '••••••••';

function tuning(c) {
  return `${c.channel} · ${c.bandwidthMhz} MHz · ${c.countryCode || '—'}`;
}

export default function MeshJoinQR({ refreshKey = 0 }) {
  const [state, setState] = useState({ loading: true, error: null, empty: false, resp: null });
  const [reveal, setReveal] = useState(false);

  const load = useCallback(async () => {
    setState(s => ({ ...s, loading: true, error: null }));
    try {
      const resp = await getMeshJoinQR();
      setState({ loading: false, error: null, empty: false, resp });
    } catch (err) {
      if (err instanceof ConnectError && err.code === Code.FailedPrecondition) {
        setState({ loading: false, error: null, empty: true, resp: null });
      } else {
        setState({ loading: false, error: err.message, empty: false, resp: null });
      }
    }
  }, []);

  useEffect(() => {
    // Fetch-on-mount and on refreshKey bumps.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    load();
  }, [load, refreshKey]);

  if (state.loading && !state.resp) return <div className="mesh-qr-status">Loading…</div>;
  if (state.empty) return <div className="mesh-qr-status">No HaLow mesh configured.</div>;
  if (state.error) return <div className="lat-alert crit">{state.error}</div>;
  if (!state.resp) return <div className="mesh-qr-status">Loading…</div>;

  const { payload, svg } = state.resp;
  const halow = payload.halow;
  const backhaul = payload.backhaul;
  const secret = (value) => (reveal ? value : MASK);

  return (
    <div className="mesh-qr">
      {/* The SVG is this node's own authenticated API output, not user content. */}
      <div className="lat-qr" dangerouslySetInnerHTML={{ __html: svg }} />
      <div>
        <div className="kv"><span className="k">Source</span><span className="v accent">{payload.sourceHostname || '—'}</span></div>
        <div className="kv"><span className="k">Mesh ID</span><span className="v">{halow.meshId}</span></div>
        <div className="kv">
          <span className="k">Passphrase</span>
          <span className="v">
            {secret(halow.passphrase)}
            <button type="button" className="lat-btn ghost sm" onClick={() => setReveal(r => !r)}>
              {reveal ? 'Hide' : 'Reveal'}
            </button>
          </span>
        </div>
        <div className="kv"><span className="k">Encryption</span><span className="v">WPA3 (SAE)</span></div>
        <div className="kv"><span className="k">Channel</span><span className="v">{tuning(halow)}</span></div>
        <div className="kv"><span className="k">Backhaul</span><span className={backhaul ? 'v' : 'v muted'}>{backhaul ? backhaul.meshId : 'none'}</span></div>
        {backhaul && (
          <>
            <div className="kv"><span className="k">Backhaul passphrase</span><span className="v">{secret(backhaul.passphrase)}</span></div>
            <div className="kv"><span className="k">Backhaul channel</span><span className="v">{tuning(backhaul)}</span></div>
          </>
        )}
      </div>
    </div>
  );
}
