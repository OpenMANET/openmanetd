// =============================================================================
// QrScanInput.jsx — photograph or paste a "share mesh" QR code
// =============================================================================
//
// Live camera (getUserMedia) needs a secure context and the WebUI is plain
// HTTP, so the camera is reached through <input type="file" capture>, which
// iOS Safari and Android Chrome honour over HTTP (and which also accepts a
// screenshot from the gallery). The photo is downscaled on a canvas and
// decoded with jsQR — the phone does the work, never the node. A "Paste
// code" path covers codes shared as text. jsqr is imported lazily so the
// main bundle does not carry it.

import { useId, useRef, useState } from 'react';
import { decodeMeshJoinText, MeshJoinError, MESH_JOIN_ERROR_MESSAGES } from '../utils/meshJoin.js';
import './QrScanInput.css';

// Longest edge after downscale. Enough for a phone photo of a QR code,
// small enough that jsQR runs in well under a second on a mid-range phone.
const MAX_EDGE = 1024;

async function loadBitmap(file) {
  if (typeof createImageBitmap === 'function') return createImageBitmap(file);
  const url = URL.createObjectURL(file);
  try {
    const img = new Image();
    await new Promise((resolve, reject) => {
      img.onload = resolve;
      img.onerror = () => reject(new Error('image decode failed'));
      img.src = url;
    });
    return img;
  } finally {
    URL.revokeObjectURL(url);
  }
}

// decodeImageFile returns the QR text found in an image file, or throws
// MeshJoinError('no-qr').
export async function decodeImageFile(file) {
  const [{ default: jsQR }, bitmap] = await Promise.all([import('jsqr'), loadBitmap(file)]);
  const scale = Math.min(1, MAX_EDGE / Math.max(bitmap.width, bitmap.height));
  const w = Math.max(1, Math.round(bitmap.width * scale));
  const h = Math.max(1, Math.round(bitmap.height * scale));

  const canvas = document.createElement('canvas');
  canvas.width = w;
  canvas.height = h;
  const ctx = canvas.getContext('2d');
  if (!ctx) {
    // Canvas disabled or the browser is out of 2d contexts; without one
    // there is nothing to decode, so say so instead of a null deref.
    if (typeof bitmap.close === 'function') bitmap.close();
    throw new MeshJoinError('no-canvas', MESH_JOIN_ERROR_MESSAGES['no-canvas']);
  }
  ctx.drawImage(bitmap, 0, 0, w, h);
  const { data } = ctx.getImageData(0, 0, w, h);
  if (typeof bitmap.close === 'function') bitmap.close();

  const code = jsQR(data, w, h, { inversionAttempts: 'attemptBoth' });
  // Release the backing store promptly on memory-constrained phones.
  canvas.width = 0;
  canvas.height = 0;

  if (!code) throw new MeshJoinError('no-qr', MESH_JOIN_ERROR_MESSAGES['no-qr']);
  return code.data;
}

function messageFor(err) {
  if (err instanceof MeshJoinError) return MESH_JOIN_ERROR_MESSAGES[err.code] ?? err.message;
  return `Could not read the photo: ${err.message}`;
}

export default function QrScanInput({ onDecoded, onError, label = 'Scan QR', disabled = false }) {
  const [busy, setBusy] = useState(false);
  const [pasteOpen, setPasteOpen] = useState(false);
  const [pasteText, setPasteText] = useState('');
  const [error, setError] = useState(null);
  const inputRef = useRef(null);
  const pasteId = useId();

  const fail = (err) => {
    const message = messageFor(err);
    setError(message);
    onError?.(message);
  };

  const deliver = (text) => {
    const payload = decodeMeshJoinText(text);
    setError(null);
    onDecoded(payload);
  };

  const onFile = async (e) => {
    const file = e.target.files?.[0];
    // Reset so re-taking the same photo re-fires onChange.
    e.target.value = '';
    if (!file) return;
    setBusy(true);
    setError(null);
    try {
      deliver(await decodeImageFile(file));
    } catch (err) {
      fail(err);
    } finally {
      setBusy(false);
    }
  };

  const usePasted = () => {
    try {
      deliver(pasteText);
      setPasteText('');
      setPasteOpen(false);
    } catch (err) {
      fail(err);
    }
  };

  const locked = disabled || busy;
  const canUsePaste = pasteText.trim() !== '' && !busy;

  return (
    <div className="qr-scan">
      <input
        ref={inputRef}
        type="file"
        accept="image/*"
        capture="environment"
        className="qr-scan-hidden"
        aria-label="Photograph a QR code"
        onChange={onFile}
        disabled={locked}
      />
      <div className="qr-scan-row">
        <button
          type="button"
          className="lat-btn primary"
          onClick={() => inputRef.current?.click()}
          disabled={locked}
        >
          {busy ? 'Decoding…' : label}
        </button>
        <button
          type="button"
          className="lat-btn ghost"
          onClick={() => setPasteOpen(o => !o)}
          disabled={locked}
          aria-expanded={pasteOpen}
        >
          Paste code
        </button>
      </div>

      {pasteOpen && (
        <div className="lat-field">
          <label htmlFor={pasteId}>Code text</label>
          <textarea
            id={pasteId}
            className="lat-textarea"
            rows={3}
            value={pasteText}
            onChange={e => setPasteText(e.target.value)}
            placeholder="OPENMANET1:…"
            spellCheck={false}
            autoComplete="off"
          />
          <button type="button" className="lat-btn qr-scan-use" onClick={usePasted} disabled={!canUsePaste}>
            Use code
          </button>
        </div>
      )}

      {error && <div className="lat-alert crit" role="alert">{error}</div>}
    </div>
  );
}
