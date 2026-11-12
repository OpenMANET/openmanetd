#!/usr/bin/env bash
# PostToolUse (Edit|Write|MultiEdit): keep derived files in sync with the file
# Claude just changed.
#
#   *.go                         gofmt -w; instrumentation doc reminder
#   proto/**/*.proto             make buf
#   internal/database/*.sql      make sqlc-gen
#   frontend/src/**/*.{js,jsx}   eslint --fix
#
# Exit 2 sends stderr to Claude; used when regeneration fails.
set -u
# shellcheck source=lib.sh
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

read_input
file=$(input_field '.tool_input.file_path')
[ -n "$file" ] && [ -f "$file" ] || exit 0
root=$(checkout_root "$(dirname "$file")")
rel=$(rel_path "$file" "$root")
is_generated "$rel" && exit 0

# run_make <target>: run a make target and report a failure to Claude.
run_make() {
	local out
	if ! out=$(make -C "$root" "$1" 2>&1); then
		printf 'make %s failed after editing %s:\n%s\n' "$1" "$rel" "$(tail -n 40 <<<"$out")" >&2
		exit 2
	fi
}

# snapshot_reminder: instrumentation snapshot fields are documented in the
# same change (.claude/rules/instrumentation.md). Fires only when the edit
# touched JSON-tagged lines and the doc has no pending changes yet.
snapshot_reminder() {
	case $rel in
	*_test.go) return ;;
	internal/comms/* | internal/blos/* | internal/sysupgrade/* | internal/wireless/* | internal/instrumentation/*) ;;
	*) return ;;
	esac

	grep -q 'Snapshot struct' "$file" || return
	input_field '[.tool_input.new_string, .tool_input.content, .tool_input.edits[]?.new_string] | map(select(. != null)) | join("\n")' |
		grep -q 'json:"' || return
	git -C "$root" diff --quiet HEAD -- docs/instrumentation-snapshot.md 2>/dev/null || return

	emit_context PostToolUse "$rel defines an instrumentation snapshot struct and this edit touched JSON-tagged fields. If snapshot fields were added, renamed, or removed, update docs/instrumentation-snapshot.md in the same change (.claude/rules/instrumentation.md)."
}

case $rel in
*.go)
	gofmt -w "$file" 2>/dev/null
	snapshot_reminder
	;;
proto/*.proto) run_make buf ;;
internal/database/schema.sql | internal/database/query.sql) run_make sqlc-gen ;;
frontend/src/*.js | frontend/src/*.jsx)
	eslint="$root/frontend/node_modules/.bin/eslint"
	if [ -x "$eslint" ]; then
		(cd "$root/frontend" && "$eslint" --fix "$file" >/dev/null 2>&1)
	fi
	;;
esac

exit 0
