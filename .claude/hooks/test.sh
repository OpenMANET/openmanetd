#!/usr/bin/env bash
# Self-test for the Claude Code hooks in this directory.
#
#   bash .claude/hooks/test.sh
#
# Each case builds a throwaway git repo, feeds the hook the JSON Claude Code
# would send on stdin, and checks the exit code and output.
set -u
here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
export OPENMANETD_HOOK_STATE="$tmp/state"
unset OPENMANETD_STOP_GATE OPENMANETD_GATE_BASE

passed=0
failed=0
out=""
err=""
code=0

check() {
	if [ "$1" = 0 ]; then
		passed=$((passed + 1))
		return
	fi
	failed=$((failed + 1))
	printf 'FAIL: %s\n  exit=%s\n  stdout=%s\n  stderr=%s\n' "$2" "$code" "$out" "$err" >&2
}

# run <hook> <json>: runs a hook, capturing out, err, and code.
run() {
	out=$("$here/$1" <<<"$2" 2>"$tmp/stderr")
	code=$?
	err=$(cat "$tmp/stderr")
}

expect_code() {
	[ "$code" = "$1" ]
	check $? "$2"
}

expect_out() {
	grep -q -- "$1" <<<"$out"
	check $? "$2"
}

expect_err() {
	grep -q -- "$1" <<<"$err"
	check $? "$2"
}

# new_repo <name>: git repo on main with one commit; sets CLAUDE_PROJECT_DIR.
new_repo() {
	repo="$tmp/$1"
	mkdir -p "$repo"
	git -C "$repo" init -q -b main
	git -C "$repo" config user.email test@example.com
	git -C "$repo" config user.name test
	printf 'module example.com/hooktest\n\ngo 1.22\n' >"$repo/go.mod"
	git -C "$repo" add go.mod
	git -C "$repo" commit -q -m "chore: init"
	git -C "$repo" checkout -q -b feature
	export CLAUDE_PROJECT_DIR=$repo
}

edit_json() {
	jq -cn --arg f "$1" --arg s "${2:-}" '{tool_name: "Edit", tool_input: {file_path: $f, new_string: $s}}'
}

# with_cwd <dir>: jq filter fragment adding the session cwd when given.
with_cwd='+ (if $d == "" then {} else {cwd: $d} end)'

bash_json() {
	jq -cn --arg c "$1" --arg d "${2:-}" "{tool_name: \"Bash\", tool_input: {command: \$c}} $with_cwd"
}

stop_json() {
	jq -cn --arg s "$1" --arg d "${2:-}" "{hook_event_name: \"Stop\", session_id: \$s} $with_cwd"
}

# seed_main <message>: commit the current tree on main, then branch feature from it.
seed_main() {
	git -C "$repo" checkout -q main
	git -C "$repo" add -A
	git -C "$repo" commit -q -m "$1"
	git -C "$repo" checkout -q -B feature
}

# --- guard-generated -------------------------------------------------------
new_repo guard
run guard-generated.sh "$(edit_json "$repo/internal/api/foo/v1/foo.pb.go")"
expect_out '"permissionDecision":"deny"' "guard denies internal/api"
run guard-generated.sh "$(edit_json "$repo/frontend/src/gen/foo_pb.js")"
expect_out '"permissionDecision":"deny"' "guard denies frontend/src/gen"
run guard-generated.sh "$(edit_json "$repo/internal/database/models/query.sql.go")"
expect_out '"permissionDecision":"deny"' "guard denies database models"
run guard-generated.sh "$(edit_json "$repo/internal/comms/manager.go")"
expect_code 0 "guard allows hand-written code (exit)"
[ -z "$out" ]
check $? "guard allows hand-written code (no output)"

# --- check-static ----------------------------------------------------------
run check-static.sh "$(bash_json "go build ./...")"
expect_out 'static/ is empty' "check-static warns on go build with empty static/"
run check-static.sh "$(bash_json "make build")"
[ -z "$out" ]
check $? "check-static ignores make build"
mkdir -p "$repo/static" && touch "$repo/static/index.html"
run check-static.sh "$(bash_json "go build ./...")"
[ -z "$out" ]
check $? "check-static quiet when static/ is populated"

# --- post-edit -------------------------------------------------------------
new_repo postedit
mkdir -p "$repo/pkg"
printf 'package pkg\n\nfunc   F(  x int)int{\nreturn x}\n' >"$repo/pkg/f.go"
run post-edit.sh "$(edit_json "$repo/pkg/f.go")"
expect_code 0 "post-edit exits 0 for go"
[ -z "$(gofmt -l "$repo/pkg/f.go")" ]
check $? "post-edit gofmts the edited file"

mkdir -p "$repo/internal/comms" "$repo/docs"
printf 'package comms\n\ntype FooSnapshot struct {\n\tA int `json:"a"`\n}\n' >"$repo/internal/comms/snapshot.go"
touch "$repo/docs/instrumentation-snapshot.md"
git -C "$repo" add -A && git -C "$repo" commit -q -m "test: seed"
run post-edit.sh "$(edit_json "$repo/internal/comms/snapshot.go" 'A int `json:"a"`')"
expect_out 'instrumentation-snapshot.md' "post-edit reminds about the snapshot doc"
run post-edit.sh "$(edit_json "$repo/internal/comms/snapshot.go" 'func (s *FooSnapshot) x() {}')"
[ -z "$out" ]
check $? "post-edit skips reminder when no JSON tags changed"
printf 'changed\n' >"$repo/docs/instrumentation-snapshot.md"
run post-edit.sh "$(edit_json "$repo/internal/comms/snapshot.go" 'A int `json:"a"`')"
[ -z "$out" ]
check $? "post-edit skips reminder once the doc has changes"

# --- commit-msg ------------------------------------------------------------
new_repo commitmsg
commit_and_check() {
	git -C "$repo" commit -q --allow-empty -m "$1"
	run commit-msg.sh "$(bash_json "git commit -m msg")"
}
commit_and_check "feat(hooks): add definition-of-done gate"
expect_code 0 "commit-msg accepts a conventional subject"
commit_and_check "$(printf 'fix(comms/rtp): handle late packets\n\nBody explains why.\n\nRefs: #12')"
expect_code 0 "commit-msg accepts body, nested scope, and Refs trailer"
commit_and_check "deps: bump github.com/rs/zerolog from 1.33.0 to 1.34.0"
expect_code 0 "commit-msg accepts the deps type used by Dependabot"
commit_and_check "$(printf 'fix(blos): keep worker alive\n\nBody.\n\nSigned-off-by: Dev <dev@example.com>')"
expect_code 0 "commit-msg accepts a Signed-off-by trailer"
commit_and_check "Added Stuff."
expect_code 2 "commit-msg rejects a non-conventional subject"
expect_err "type feat|fix" "commit-msg explains the format"
commit_and_check "feat(ui): Add page"
expect_code 2 "commit-msg rejects an uppercase subject"
commit_and_check "fix: trailing period."
expect_code 2 "commit-msg rejects a trailing period"
commit_and_check "$(printf 'fix: x\n\nbody\n\nCo-Authored-By: Claude <noreply@anthropic.com>')"
expect_code 2 "commit-msg rejects Co-Authored-By"
commit_and_check "feat: $(printf 'a%.0s' $(seq 1 80))"
expect_code 2 "commit-msg rejects a long subject"
commit_and_check "Added Stuff."
run commit-msg.sh "$(bash_json "git status")"
expect_code 0 "commit-msg ignores non-commit commands"
commit_and_check "Merge branch 'main' into feature"
expect_code 0 "commit-msg skips merge commits"

# The commit is judged in the repository it landed in.
git -C "$repo" commit -q --allow-empty -m "feat: good root commit"
mkdir -p "$repo/sub"
git -C "$repo/sub" init -q -b main
git -C "$repo/sub" config user.email test@example.com
git -C "$repo/sub" config user.name test
git -C "$repo/sub" commit -q --allow-empty -m "Bad Nested."
run commit-msg.sh "$(bash_json "cd sub && git commit -m x" "$repo")"
expect_code 2 "commit-msg checks a commit made after cd into a nested repo"
expect_err "Bad Nested\|breaks the commit message rules" "commit-msg reports the nested commit"
run commit-msg.sh "$(bash_json "cd sub && git commit -m x" "$repo/sub")"
expect_code 2 "commit-msg handles a cwd that already reflects the cd"
run commit-msg.sh "$(bash_json "git -C sub commit -m x" "$repo")"
expect_code 2 "commit-msg checks a commit made with git -C"
run commit-msg.sh "$(bash_json "git commit -m x" "$repo")"
expect_code 0 "commit-msg judges the root commit when the command ran in the root"
git -C "$repo" worktree add -q "$tmp/commitmsg-wt" -b wt
git -C "$tmp/commitmsg-wt" commit -q --allow-empty -m "Bad Worktree."
run commit-msg.sh "$(bash_json "git commit -m x" "$tmp/commitmsg-wt")"
expect_code 2 "commit-msg checks the worktree the session is in"

# --- stop-gate -------------------------------------------------------------
new_repo stopgate
run stop-gate.sh "$(stop_json s1)"
expect_code 0 "stop-gate passes with no changes"

mkdir -p "$repo/pkg"
printf 'package pkg\n\nfunc   F(  x int)int{\nreturn x}\n' >"$repo/pkg/f.go"
run stop-gate.sh "$(stop_json s1)"
expect_code 2 "stop-gate blocks unformatted Go"
expect_err "gofmt" "stop-gate names gofmt"
run stop-gate.sh "$(stop_json s1)"
expect_code 0 "stop-gate blocks a given tree state once per session"
run stop-gate.sh "$(stop_json s2)"
expect_code 2 "stop-gate blocks the same state in a new session"
gofmt -w "$repo/pkg/f.go"
printf 'package pkg\n\nimport "os"\n\nfunc G() {\n\tos.Remove("x")\n}\n' >"$repo/pkg/g.go"
run stop-gate.sh "$(stop_json s1)"
expect_code 2 "stop-gate blocks golangci-lint findings"
expect_err "errcheck" "stop-gate reports the golangci-lint finding"
rm "$repo/pkg/g.go"
git -C "$repo" add -A && git -C "$repo" commit -q -m "feat: add f"
run stop-gate.sh "$(stop_json s1)"
expect_code 0 "stop-gate passes committed, formatted Go on the branch"
pass_files=$(find "$OPENMANETD_HOOK_STATE/stop-gate/pass" -type f | wc -l)
[ "$pass_files" -ge 1 ]
check $? "stop-gate caches a passing state"

mkdir -p "$repo/internal/comms"
printf 'package comms\n\ntype BarSnapshot struct {\n\tB int `json:"b"`\n}\n' >"$repo/internal/comms/snapshot.go"
run stop-gate.sh "$(stop_json s3)"
expect_code 2 "stop-gate blocks snapshot fields without doc update"
expect_err "instrumentation-snapshot.md" "stop-gate names the snapshot doc"
mkdir -p "$repo/docs" && printf '| `b` |\n' >"$repo/docs/instrumentation-snapshot.md"
run stop-gate.sh "$(stop_json s3)"
expect_code 0 "stop-gate passes once the doc changes"

mkdir -p "$repo/frontend/src/pages"
printf '.x {\n  color: #fff;\n  border-radius: 4px;\n  background: var(--surface);\n}\n.dot { border-radius: 50%%; }\n' >"$repo/frontend/src/pages/X.css"
run stop-gate.sh "$(stop_json s4)"
expect_code 2 "stop-gate blocks Lattice violations"
expect_err "color: #fff" "stop-gate reports the hex colour"
expect_err "border-radius: 4px" "stop-gate reports the radius"
grep -q 'X.css: .dot' <<<"$err"
[ $? = 1 ]
check $? "stop-gate allows 50% radius"

printf '.x {\n  color: var(--text);\n}\n' >"$repo/frontend/src/pages/X.css"
run stop-gate.sh "$(stop_json s4)"
expect_code 0 "stop-gate passes token-only CSS"

OPENMANETD_STOP_GATE=off run stop-gate.sh "$(stop_json s5)"
expect_code 0 "stop-gate can be disabled"

# Build runs for deletions and generated code, not only edited files.
new_repo build
mkdir -p "$repo/a" "$repo/b" "$repo/static"
echo x >"$repo/static/index.html"
printf 'package a\n\nfunc F() int { return 1 }\n' >"$repo/a/a.go"
printf 'package b\n\nimport "example.com/hooktest/a"\n\nfunc G() int { return a.F() }\n' >"$repo/b/b.go"
seed_main "feat: seed packages"
rm "$repo/a/a.go"
run stop-gate.sh "$(stop_json b1)"
expect_code 2 "stop-gate builds after a deletion-only Go change"
expect_err "go build" "stop-gate reports the build failure"
git -C "$repo" checkout -q -- a/a.go
mkdir -p "$repo/internal/api"
printf 'package api\n\nfunc Broken( {\n' >"$repo/internal/api/gen.go"
run stop-gate.sh "$(stop_json b2)"
expect_code 2 "stop-gate builds when only generated Go changed"
rm -rf "$repo/internal/api"

# Removed snapshot fields need the doc too.
new_repo snapdelete
mkdir -p "$repo/internal/comms" "$repo/docs"
printf 'package comms\n\ntype FooSnapshot struct {\n\tA int `json:"a"`\n}\n' >"$repo/internal/comms/snapshot.go"
printf '| `a` |\n' >"$repo/docs/instrumentation-snapshot.md"
seed_main "feat: seed snapshot"
git -C "$repo" rm -q internal/comms/snapshot.go
run stop-gate.sh "$(stop_json d1)"
expect_code 2 "stop-gate flags a deleted snapshot file"
expect_err "instrumentation-snapshot.md" "stop-gate names the doc for a deleted file"
git -C "$repo" checkout -q HEAD -- internal/comms/snapshot.go
printf 'package comms\n\ntype Foo struct{}\n' >"$repo/internal/comms/snapshot.go"
run stop-gate.sh "$(stop_json d2)"
expect_code 2 "stop-gate flags a removed snapshot struct"

# A pass recorded while a validator was missing is not reused once it exists.
new_repo eslint
printf 'frontend/node_modules/\n' >"$repo/.gitignore"
mkdir -p "$repo/frontend/src"
printf 'export const x = 1\n' >"$repo/frontend/src/x.js"
run stop-gate.sh "$(stop_json e1)"
expect_code 0 "stop-gate passes when eslint is unavailable"
expect_out "could not run: eslint" "stop-gate tells the user eslint was skipped"
mkdir -p "$repo/frontend/node_modules/.bin"
printf '#!/bin/sh\necho "x.js: lint error" >&2\nexit 1\n' >"$repo/frontend/node_modules/.bin/eslint"
chmod +x "$repo/frontend/node_modules/.bin/eslint"
run stop-gate.sh "$(stop_json e2)"
expect_code 2 "stop-gate re-checks once eslint is installed"
expect_err "lint error" "stop-gate reports the eslint failure"

# Hooks act on the worktree the session is in.
new_repo wtree
git -C "$repo" worktree add -q "$tmp/wtree-wt" -b wt-feature
mkdir -p "$tmp/wtree-wt/pkg"
printf 'package pkg\n\nfunc   F(  x int)int{\nreturn x}\n' >"$tmp/wtree-wt/pkg/f.go"
run stop-gate.sh "$(stop_json w1 "$tmp/wtree-wt")"
expect_code 2 "stop-gate checks the worktree the session is in"
run stop-gate.sh "$(stop_json w2 "$tmp")"
expect_code 0 "stop-gate falls back to the project outside any checkout"
mkdir -p "$tmp/wtree-wt/internal/comms"
printf 'package comms\n\ntype FooSnapshot struct {\n\tA int `json:"a"`\n}\n' >"$tmp/wtree-wt/internal/comms/snapshot.go"
run post-edit.sh "$(edit_json "$tmp/wtree-wt/internal/comms/snapshot.go" 'A int `json:"a"`')"
expect_out 'instrumentation-snapshot.md' "post-edit reminds about the snapshot doc inside a worktree"
run guard-generated.sh "$(edit_json "$tmp/wtree-wt/internal/api/foo.pb.go")"
expect_out '"permissionDecision":"deny"' "guard denies generated code inside a worktree"

# --- trace-instructions ----------------------------------------------------
OPENMANETD_TRACE_INSTRUCTIONS=1 OPENMANETD_TRACE_LOG="$tmp/trace.log" \
	run trace-instructions.sh '{"hook_event_name":"InstructionsLoaded","file_path":"/x/.claude/rules/proto.md","transcript_path":"/t"}'
grep -q 'proto.md' "$tmp/trace.log" 2>/dev/null && ! grep -q transcript_path "$tmp/trace.log"
check $? "trace-instructions logs the load without the transcript path"
OPENMANETD_TRACE_LOG="$tmp/trace2.log" run trace-instructions.sh '{}'
[ ! -e "$tmp/trace2.log" ]
check $? "trace-instructions is off by default"

printf '%d passed, %d failed\n' "$passed" "$failed"
[ "$failed" = 0 ]
