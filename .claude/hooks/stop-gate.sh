#!/usr/bin/env bash
# Stop: definition-of-done gate.
#
# Checks everything that changed on this branch (commits since the merge base
# with main, plus staged, unstaged, and untracked files) and blocks the end of
# the turn with exit 2 until it is clean:
#
#   Go        gofmt, golangci-lint (new issues in changed packages), go build
#   Docs      instrumentation snapshot fields documented
#   Frontend  eslint on changed files; Lattice colour/radius checks on added lines
#   Proto     buf lint, buf format
#   SQL       sqlc diff (generated models in sync)
#
# Edits made through Bash (sed -i, heredocs) are caught too, because the gate
# reads git state instead of tool calls.
#
# A tree state that passed is cached and not checked again. A tree state that
# failed blocks at most once per session, so Claude can still stop and explain
# a failure it cannot fix instead of looping.
#
# Env: OPENMANETD_STOP_GATE=off disables the gate; OPENMANETD_GATE_BASE
# overrides the comparison base.
set -u
self="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/$(basename "${BASH_SOURCE[0]}")"
# shellcheck source=lib.sh
. "$(dirname "$self")/lib.sh"

[ "${OPENMANETD_STOP_GATE:-on}" = off ] && exit 0

read_input
cd "$(project_dir)" 2>/dev/null || exit 0
git rev-parse --git-dir >/dev/null 2>&1 || exit 0
session=$(input_field '.session_id')
session=${session:-unknown}
session=${session//[^A-Za-z0-9_-]/_}

# merge_base: the newest merge base of HEAD with main or origin/main.
merge_base() {
	local ref mb best=""
	if [ -n "${OPENMANETD_GATE_BASE:-}" ]; then
		git rev-parse "$OPENMANETD_GATE_BASE"
		return
	fi

	for ref in main origin/main; do
		mb=$(git merge-base HEAD "$ref" 2>/dev/null) || continue
		if [ -z "$best" ] || git merge-base --is-ancestor "$best" "$mb"; then
			best=$mb
		fi
	done

	if [ -n "$best" ]; then
		printf '%s\n' "$best"
	else
		git rev-parse HEAD
	fi
}

has_proto_checkout() {
	[ -e proto/.git ]
}

changed_files() {
	{
		git diff --name-only "$base"
		git ls-files --others --exclude-standard
		if has_proto_checkout; then
			git -C proto diff --name-only HEAD | sed 's|^|proto/|'
			git -C proto ls-files --others --exclude-standard | sed 's|^|proto/|'
		fi
	} 2>/dev/null | sort -u
}

# tree_state: hash of everything the gate looks at, including the gate itself.
tree_state() {
	{
		printf '%s\n' "$base"
		sha256sum "$self"
		git diff --binary "$base"
		git ls-files -z --others --exclude-standard | xargs -0 -r sha256sum
		if has_proto_checkout; then
			git -C proto diff --binary HEAD
			(cd proto && git ls-files -z --others --exclude-standard | xargs -0 -r sha256sum)
		fi
	} 2>/dev/null | sha256sum | cut -d' ' -f1
}

existing() {
	local f
	while IFS= read -r f; do
		[ -f "$f" ] && printf '%s\n' "$f"
	done
}

# added_lines <file>: lines this branch added (the whole file if untracked).
added_lines() {
	if git ls-files --error-unmatch -- "$1" >/dev/null 2>&1; then
		git diff -U0 "$base" -- "$1" | grep -vE '^\+\+\+ ' | grep -E '^\+' | cut -c2-
	else
		cat "$1"
	fi
}

report=""

# fail <heading> <details>
fail() {
	report+=$'\n'"### $1"$'\n'"$2"$'\n'
}

clip() {
	tail -n "${2:-40}" <<<"$1"
}

check_go() {
	local out files pkgs
	[ -n "$go_files" ] || return
	mapfile -t files <<<"$go_files"

	out=$(gofmt -l "${files[@]}" 2>&1)
	[ -n "$out" ] && fail "gofmt: run make lint-go (or gofmt -w)" "$out"

	if command -v golangci-lint >/dev/null; then
		mapfile -t pkgs < <(for f in "${files[@]}"; do printf './%s\n' "$(dirname "$f")"; done | sort -u)
		if ! out=$(golangci-lint run --timeout 5m --new-from-rev="$base" "${pkgs[@]}" 2>&1); then
			fail "golangci-lint (new issues in changed packages; make lint-go auto-fixes some)" "$(clip "$out" 60)"
		fi
	fi

	# The root package embeds static/; without it go build cannot succeed.
	if [ -n "$(ls -A static 2>/dev/null)" ]; then
		if ! out=$(go build ./... 2>&1); then
			fail "go build ./..." "$(clip "$out")"
		fi
	fi
}

check_snapshot_doc() {
	local f flagged=""
	grep -qx 'docs/instrumentation-snapshot.md' <<<"$changed" && return

	while IFS= read -r f; do
		case $f in
		*_test.go) continue ;;
		internal/comms/* | internal/blos/* | internal/sysupgrade/* | internal/wireless/* | internal/instrumentation/*) ;;
		*) continue ;;
		esac

		grep -q 'Snapshot struct' "$f" || continue
		if git ls-files --error-unmatch -- "$f" >/dev/null 2>&1; then
			git diff -U0 "$base" -- "$f" | grep -E '^[+-][^+-]' | grep -q 'json:"' || continue
		fi
		flagged+="$f"$'\n'
	done <<<"$go_files"

	[ -n "$flagged" ] && fail "docs/instrumentation-snapshot.md not updated (.claude/rules/instrumentation.md)" \
		"JSON-tagged lines changed in files that define snapshot structs. Document added, renamed, or removed snapshot fields; if these are not snapshot fields, say so."$'\n'"$flagged"
}

check_frontend() {
	local js out files
	js=$(grep -E '^frontend/src/.*\.(js|jsx)$' <<<"$changed" | grep -v '^frontend/src/gen/' | existing)
	if [ -n "$js" ] && [ -x frontend/node_modules/.bin/eslint ]; then
		mapfile -t files < <(sed 's|^frontend/||' <<<"$js")
		if ! out=$(cd frontend && node_modules/.bin/eslint "${files[@]}" 2>&1); then
			fail "eslint (pnpm -C frontend exec eslint --fix <file> fixes some)" "$(clip "$out")"
		fi
	fi
}

check_lattice() {
	local f hits=""
	while IFS= read -r f; do
		case $f in
		frontend/src/styles/lattice.css | frontend/src/Layout.css | frontend/src/gen/* | frontend/src/__tests__/*) continue ;;
		*.css)
			hits+=$(added_lines "$f" |
				grep -E ':[^;{]*#[0-9a-fA-F]{3,8}([^0-9a-zA-Z_-]|$)|(^|[^a-z-])(rgba?|hsla?)\([[:space:]]*[0-9.]|border-radius[[:space:]]*:' |
				grep -vE 'border-radius[[:space:]]*:[[:space:]]*(0|0px|var\(--radius\)|50%)[[:space:]]*(;|!|$)' |
				sed "s|^|$f: |")$'\n'
			;;
		*.js | *.jsx)
			hits+=$(added_lines "$f" |
				grep -E "['\"\`]#[0-9a-fA-F]{3,8}['\"\`]|borderRadius[[:space:]]*:[[:space:]]*['\"]?[1-9]" |
				sed "s|^|$f: |")$'\n'
			;;
		esac
	done < <(grep -E '^frontend/src/.*\.(css|js|jsx)$' <<<"$changed" | existing)

	hits=$(grep -v '^[[:space:]]*$' <<<"$hits")
	[ -n "$hits" ] && fail "Lattice: hard-coded colour or border-radius (.claude/rules/frontend.md)" \
		"Use the tokens in frontend/src/styles/lattice.css (var(--accent), var(--ok), ...). Corners stay square; only dots, pips, and the PTT ring use 50%. Canvas code can read a token with getComputedStyle(document.documentElement).getPropertyValue('--accent')."$'\n'"$(clip "$hits" 30)"
}

check_proto() {
	local out
	grep -qE '^proto(/|$)' <<<"$changed" || return
	has_proto_checkout || return
	command -v buf >/dev/null || return

	if ! out=$(cd proto && buf lint 2>&1); then
		fail "buf lint (cd proto && buf lint)" "$(clip "$out")"
	fi
	if ! out=$(buf format -d --exit-code proto 2>&1); then
		fail "buf format: run make proto-lint" "$(clip "$out")"
	fi
}

check_sql() {
	local out
	grep -qE '^(internal/database/(schema|query)\.sql|sqlc\.yaml)$' <<<"$changed" || return
	command -v sqlc >/dev/null || return

	if ! out=$(sqlc diff 2>&1); then
		fail "sqlc diff: generated models are stale, run make sqlc-gen" "$(clip "$out")"
	fi
}

base=$(merge_base)
changed=$(changed_files)
[ -n "$changed" ] || exit 0

state=$(tree_state)
pass_dir=$(state_dir stop-gate/pass)
block_dir=$(state_dir stop-gate/blocked)
find "$pass_dir" "$block_dir" -type f -mtime +7 -delete 2>/dev/null
[ -e "$pass_dir/$state" ] && exit 0
[ "$(cat "$block_dir/$session" 2>/dev/null)" = "$state" ] && exit 0

go_files=$(grep -E '\.go$' <<<"$changed" | grep -vE '^(internal/api/|internal/database/models/|\.claude/)' | existing)

check_go
check_snapshot_doc
check_frontend
check_lattice
check_proto
check_sql

if [ -z "$report" ]; then
	: >"$pass_dir/$state"
	exit 0
fi

printf '%s\n' "$state" >"$block_dir/$session"
{
	printf 'Definition-of-done gate failed for changes since %s.\n' "$(git rev-parse --short "$base")"
	printf 'Fix these before finishing. If one cannot be fixed (pre-existing, out of scope, or a false positive), tell the user why; this exact tree state is not blocked again this session.\n'
	printf '%s' "$report"
} >&2
exit 2
