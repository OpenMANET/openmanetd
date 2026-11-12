#!/usr/bin/env bash
# Shared helpers for the Claude Code hooks in this directory.
#
# Claude Code passes hook input as JSON on stdin; there is no
# CLAUDE_TOOL_INPUT environment variable. Source this file, call read_input
# once, then pull fields out with input_field.

HOOK_INPUT=""

read_input() {
	HOOK_INPUT=$(cat)
}

# input_field <jq-filter>: prints the value, or nothing when null or missing.
input_field() {
	jq -r "($1) // empty" <<<"$HOOK_INPUT" 2>/dev/null
}

project_dir() {
	if [ -n "${CLAUDE_PROJECT_DIR:-}" ]; then
		printf '%s' "$CLAUDE_PROJECT_DIR"
		return
	fi
	git rev-parse --show-toplevel 2>/dev/null || pwd
}

# common_dir <dir>: absolute git common directory of the checkout at dir.
common_dir() {
	(cd "$1" 2>/dev/null && cd "$(git rev-parse --git-common-dir 2>/dev/null)" 2>/dev/null && pwd -P)
}

# checkout_root [dir]: top level of the checkout containing dir when it is the
# project's own checkout or one of its git worktrees, otherwise the project
# directory. A session that has moved into a worktree reports it as its cwd,
# so hooks act on that worktree instead of always on the main checkout.
checkout_root() {
	local proj top
	proj=$(project_dir)
	top=$(git -C "${1:-$proj}" rev-parse --show-toplevel 2>/dev/null) || {
		printf '%s' "$proj"
		return
	}

	if [ "$(common_dir "$top")" = "$(common_dir "$proj")" ]; then
		printf '%s' "$top"
	else
		printf '%s' "$proj"
	fi
}

# rel_path <path> [root]: path relative to root (default: the project
# directory), unchanged if outside it.
rel_path() {
	local root=${2:-$(project_dir)}
	case $1 in
	"$root"/*) printf '%s' "${1#"$root"/}" ;;
	*) printf '%s' "$1" ;;
	esac
}

# is_generated <path>: true for code owned by make buf / make sqlc-gen.
is_generated() {
	case $1 in
	internal/api/* | */internal/api/* | \
		frontend/src/gen/* | */frontend/src/gen/* | \
		internal/database/models/* | */internal/database/models/*)
		return 0
		;;
	esac

	return 1
}

# state_dir <name>: per-user hook state, kept outside the repository.
state_dir() {
	local d="${OPENMANETD_HOOK_STATE:-${XDG_CACHE_HOME:-$HOME/.cache}/openmanetd-claude}/$1"
	mkdir -p "$d"
	printf '%s' "$d"
}

# emit_context <event> <text>: JSON output that adds text to Claude's context.
emit_context() {
	jq -cn --arg ev "$1" --arg ctx "$2" \
		'{hookSpecificOutput: {hookEventName: $ev, additionalContext: $ctx}}'
}
