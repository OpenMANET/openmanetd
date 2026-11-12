#!/usr/bin/env bash
# PostToolUse (Bash): check the commit Claude just made against the rules in
# .claude/skills/commit/SKILL.md. Exit 2 shows the problems to Claude so it
# amends the commit.
set -u
# shellcheck source=lib.sh
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

# commit_msg_errors <message>: prints one line per rule the message breaks.
commit_msg_errors() {
	local msg=$1 subject desc
	subject=$(head -n 1 <<<"$msg")
	case $subject in
	"Merge "* | "fixup! "* | "squash! "* | "amend! "*) return ;;
	esac

	if grep -qE '^(feat|fix|chore|docs|refactor|test|perf|ci|build|style|revert|deps)(\([a-z0-9][a-z0-9/_-]*\))?!?: [^[:space:]]' <<<"$subject"; then
		desc=${subject#*: }
		[[ $desc =~ ^[A-Z] ]] && echo "- start the subject with a lowercase letter after the colon"
	else
		echo "- subject must be '<type>(<scope>): <subject>' with type feat|fix|chore|docs|refactor|test|perf|ci|build|style|revert|deps and a lowercase scope"
	fi

	[[ $subject == *. ]] && echo "- drop the trailing period from the subject"
	[ "${#subject}" -le 72 ] || echo "- subject is ${#subject} characters; the limit is 72"
	[ -z "$(sed -n 2p <<<"$msg")" ] || echo "- leave a blank line between the subject and the body"
	grep -qiE '^co-authored-by:' <<<"$msg" && echo "- remove the Co-Authored-By trailer; commits carry no attribution trailers"
	grep -qiE 'generated with \[?claude code' <<<"$msg" && echo "- remove the 'Generated with Claude Code' line"

	return 0
}

read_input
cmd=$(input_field '.tool_input.command')
grep -qE '(^|[^[:alnum:]_-])git([[:space:]]+-[cC][[:space:]]+[^[:space:]]+)*[[:space:]]+commit([[:space:]]|$)' <<<"$cmd" || exit 0
cd "$(project_dir)" 2>/dev/null || exit 0

# Only judge a commit made just now; a failed `git commit` leaves an older HEAD.
committed=$(git log -1 --format=%ct 2>/dev/null) || exit 0
[ $(($(date +%s) - committed)) -le 300 ] || exit 0

errors=$(commit_msg_errors "$(git log -1 --format=%B)")
[ -n "$errors" ] || exit 0

{
	printf 'Commit %s breaks the commit message rules (.claude/skills/commit/SKILL.md):\n' "$(git log -1 --format=%h)"
	printf '%s\n' "$errors"
	printf 'Fix it now with git commit --amend.\n'
} >&2
exit 2
