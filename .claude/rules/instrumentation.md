---
paths:
  - "internal/{comms,blos,sysupgrade,wireless,instrumentation}/**/*.go"
  - "docs/instrumentation-snapshot.md"
---

# Instrumentation Snapshot Doc

`internal/instrumentation` publishes a periodic JSON snapshot of the daemon's runtime counters. `docs/instrumentation-snapshot.md` is its field reference: what operators and LLMs read to triage audio glitches, goroutine leaks, and jitter-buffer pressure without reading source. When it drifts from the Go types, the snapshot becomes opaque.

**Any addition, removal, or rename of a field on a snapshot struct updates `docs/instrumentation-snapshot.md` in the same changeset.** The PostToolUse hook reminds you when an edit touches JSON-tagged fields; the Stop hook blocks until the doc changes.

Registered sections (`internal/openmanet/openmanet.go`):

- `comms` — `internal/comms/snapshot.go` (`CommsSnapshot`, `PortSnapshot`), `rtp/snapshot.go` (`JitterBufferSnapshot`), `audio/snapshot.go` (`AudioEncoderSnapshot`), `webaudio/snapshot.go` (`BridgeSnapshot`), `control/snapshot.go`
- `audio_mixer` — `internal/comms/control/alsa/snapshot.go`
- `blos` — `internal/blos/snapshot.go`
- `sysupgrade` — `internal/sysupgrade/snapshot.go`
- `wireless` — `internal/wireless/snapshot.go`
- any new `Snapshotter` registered with the registry

## Each doc update

1. A table row per new field under its subsection (`comms.ports[*]`, `comms.broadcast_encoder`, `comms.web_bridge`, ...).
2. The JSON key exactly as in the struct tag (lowercase snake_case).
3. The unit (count, bytes, nanoseconds, ...) and a one-sentence meaning an operator can act on without reading Go.
4. A new or extended heuristic under "Interpretation heuristics for LLM triage" when the meaning is non-obvious or two fields should be read together.
5. Removed or renamed fields are deleted from the doc, not left with stale text.
6. `schema_version` and the doc's semver note agree: additive fields are a minor bump, renames and removals a major one.

Not needed: changelog entries, migration notes, or per-field allocation caveats — the atomic-load-only contract lives in the package GoDoc.

New fields must not add allocations: the `Snapshot` method still passes its zero-allocation test.
