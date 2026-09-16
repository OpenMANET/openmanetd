#!/usr/bin/env bash
# Regenerates internal/comms/announce/clips/tg_NN.opus.
#
# Preferred engine: Piper neural TTS with a permissively licensed voice.
#   PIPER_VOICE points at the .onnx model; default en_US-libritts_r-medium
#   (CC BY 4.0 — keep the NOTICE file next to the clips in sync).
# Fallback engine: espeak-ng (development only; regenerate with Piper
#   before release).
#
# Pipeline per clip: synth -> 48 kHz mono -> trim leading/trailing silence ->
# pad 60 ms head / 100 ms tail -> peak-limit to -3 dBFS (alimiter) ->
# opusenc 32 kbps.
set -euo pipefail

cd "$(dirname "$0")/.."
OUT=internal/comms/announce/clips
mkdir -p "$OUT"

WORDS=(one two three four five)
PIPER_VOICE="${PIPER_VOICE:-$HOME/.local/share/piper/en_US-libritts_r-medium.onnx}"

synth() { # $1 = text, $2 = raw wav out
  if command -v piper >/dev/null && [ -f "$PIPER_VOICE" ]; then
    echo "$1" | piper --model "$PIPER_VOICE" --output_file "$2"
    echo "engine: piper ($PIPER_VOICE)" >&2
  else
    espeak-ng -v en-us -s 150 -w "$2" "$1"
    echo "engine: espeak-ng (DEV FALLBACK — regenerate with piper before release)" >&2
  fi
}

for i in "${!WORDS[@]}"; do
  n=$((i + 1))
  raw=$(mktemp --suffix=.wav) trimmed=$(mktemp --suffix=.wav)
  synth "talk group ${WORDS[$i]}" "$raw"
  ffmpeg -y -loglevel error -i "$raw" -ac 1 -ar 48000 \
    -af "silenceremove=start_periods=1:start_threshold=-45dB,areverse,silenceremove=start_periods=1:start_threshold=-45dB,areverse,adelay=60,apad=pad_dur=0.1,alimiter=limit=0.707" \
    "$trimmed"
  opusenc --quiet --bitrate 32 --hard-cbr --framesize 20 "$trimmed" \
    "$OUT/$(printf 'tg_%02d.opus' "$n")"
  rm -f "$raw" "$trimmed"
done

ls -la "$OUT"
