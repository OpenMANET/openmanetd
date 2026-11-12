#!/usr/bin/env bash
# InstructionsLoaded: with OPENMANETD_TRACE_INSTRUCTIONS=1, append one JSON line
# per CLAUDE.md / rules file Claude Code loads, so path-scoped loading can be
# checked. Log: $OPENMANETD_TRACE_LOG, default
# ~/.cache/openmanetd-claude/trace/instructions.log (capped at 2000 lines).
set -u
[ "${OPENMANETD_TRACE_INSTRUCTIONS:-}" = 1 ] || exit 0
# shellcheck source=lib.sh
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

read_input
log=${OPENMANETD_TRACE_LOG:-$(state_dir trace)/instructions.log}
jq -c --arg ts "$(date -u +%FT%TZ)" '{ts: $ts} + del(.transcript_path)' \
	<<<"$HOOK_INPUT" >>"$log" 2>/dev/null

if [ "$(wc -l <"$log")" -gt 2000 ]; then
	tail -n 1000 "$log" >"$log.tmp" && mv "$log.tmp" "$log"
fi

exit 0
