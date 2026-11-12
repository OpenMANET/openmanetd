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

# join_dir <base> <dir>: dir resolved against base (a leading ~ is $HOME).
join_dir() {
	local dir=${2/#\~/$HOME}
	case $dir in
	/*) printf '%s' "$dir" ;;
	*) printf '%s/%s' "$1" "$dir" ;;
	esac
}

# commit_repo <command>: top level of the repository the command's
# `git commit` ran in. Starts from the Bash tool's cwd (a worktree, or the
# project directory), then applies the last `cd <dir>` before the commit and
# any `git -C <dir>` on it, so commits in proto/ or another worktree are judged
# in their own repository.
commit_repo() {
	local cmd=$1 base dir match before word next cdir top
	local -a opts
	local re='(^|[^[:alnum:]_-])git(([[:space:]]+-[cC][[:space:]]+[^[:space:]]+)*)[[:space:]]+commit([[:space:]]|$)'
	[[ $cmd =~ $re ]] || return 1
	match=${BASH_REMATCH[0]}
	read -ra opts <<<"${BASH_REMATCH[2]}"

	base=$(input_field '.cwd')
	[ -d "$base" ] || base=$(project_dir)
	dir=$base

	before=${cmd%%"$match"*}
	cdir=$(grep -oE '(^|[;&|[:space:](])cd[[:space:]]+[^;&|[:space:])]+' <<<"$before" | tail -n 1 | awk '{print $NF}')
	cdir=${cdir//\"/}
	cdir=${cdir//\'/}
	if [ -n "$cdir" ] && [[ $cdir != *'$'* && $cdir != *'`'* && $cdir != - ]]; then
		dir=$(join_dir "$dir" "$cdir")
	fi

	next=""
	for word in "${opts[@]}"; do
		if [ "$next" = C ]; then
			word=${word//\"/}
			dir=$(join_dir "$dir" "${word//\'/}")
		fi
		next=""
		[ "$word" = -C ] && next=C
	done

	# If the cwd already reflects the cd, the joined path won't exist; fall back.
	top=$(git -C "$dir" rev-parse --show-toplevel 2>/dev/null) ||
		top=$(git -C "$base" rev-parse --show-toplevel 2>/dev/null) ||
		top=$(project_dir)
	printf '%s' "$top"
}

read_input
cmd=$(input_field '.tool_input.command')
repo=$(commit_repo "$cmd") || exit 0
cd "$repo" 2>/dev/null || exit 0

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
