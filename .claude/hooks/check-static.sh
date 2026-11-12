#!/usr/bin/env bash
# PreToolUse (Bash): warn before a raw `go build` when static/ is empty, because
# the //go:embed static/* directive in static.go cannot compile without it.
set -u
# shellcheck source=lib.sh
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

read_input
cmd=$(input_field '.tool_input.command')
grep -qE '(^|[^[:alnum:]_-])go[[:space:]]+build([[:space:]]|$)' <<<"$cmd" || exit 0
grep -qE '(^|[^[:alnum:]_-])make([[:space:]]|$)' <<<"$cmd" && exit 0
[ -n "$(ls -A "$(checkout_root "$(input_field '.cwd')")/static" 2>/dev/null)" ] && exit 0

emit_context PreToolUse "static/ is empty, so building the root package fails on the //go:embed static/* directive in static.go. Run make frontend first, or use make build / make build-lite."
