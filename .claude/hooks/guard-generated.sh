#!/usr/bin/env bash
# PreToolUse (Edit|Write|MultiEdit|NotebookEdit): deny edits to generated code.
set -u
# shellcheck source=lib.sh
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

read_input
file=$(input_field '.tool_input.file_path // .tool_input.notebook_path')
[ -n "$file" ] || exit 0
is_generated "$(rel_path "$file")" || exit 0

reason="$file is generated. Edit the .proto source under proto/ (then make buf) or internal/database/schema.sql / query.sql (then make sqlc-gen) instead."
jq -cn --arg r "$reason" \
	'{hookSpecificOutput: {hookEventName: "PreToolUse", permissionDecision: "deny", permissionDecisionReason: $r}}'
