---
name: commit
description: Use when writing or creating a git commit in this repository. Covers the Conventional Commits 1.0 format, allowed types and scopes, subject and body rules, and the no-trailer policy. A PostToolUse hook rejects commits that break these rules.
---

# Commit Messages

Every commit follows Conventional Commits 1.0 (https://www.conventionalcommits.org/en/v1.0.0/). The history drives change-log generation, release automation, and review, so this is not optional.

```
<type>(<scope>): <subject>

[optional body]

[optional footer(s)]
```

## Type

| Type | Use for |
|---|---|
| `feat` | A new feature visible to a user, operator, or downstream consumer |
| `fix` | A bug fix in previously released or merged code |
| `chore` | Maintenance with no behavior change: lint/format passes, file moves, build config tweaks |
| `deps` | Dependency bumps — Dependabot's format: `deps: bump github.com/rs/zerolog from 1.33.0 to 1.34.0` |
| `docs` | Documentation only: README, comments, `.claude/` rules and skills, design specs, plans |
| `refactor` | Restructuring that preserves behavior |
| `test` | Test-only additions or changes |
| `perf` | A change whose primary purpose is performance |
| `ci` | CI config: `.github/workflows/`, lint config, build scripts, `.claude/hooks/` |
| `build` | Build system: `Makefile`, `go.mod` infrastructure, `buf.gen.yaml`, `sqlc.yaml` |
| `style` | Formatting only (rare; usually folded into `chore`) |
| `revert` | Reverts a commit: the reverted subject as the subject, `Reverts: <sha>` footer |

Pick the dominant type. If two are genuinely co-equal, split the commit.

## Scope

Optional but strongly preferred: the package or area, lowercase, in parentheses — `comms`, `blos`, `mgmt`, `server`, `database`, `config`, `cmd`, `proto`, `frontend`, `rules`, `hooks`, `deps`.

## Subject

- Imperative mood: `add talk group registry`, not `added` / `adds` / `adding`.
- Lowercase first letter after the colon; no trailing period.
- The whole line, type and scope included, is at most 72 characters.
- Describe the change, not the reason (`add session expiry index`, not `users complained about slow lookups`). The reason goes in the body; issue and PR numbers go in the footer.

## Body

Add one when the motivation or a side effect isn't obvious, when the change touches more than three files or concerns, or when it's breaking. Wrap at about 80 columns and explain why and what changed at a high level — the diff shows the details. When in doubt, a one-line body is fine; trivial commits (`chore(lint): fix gosec G404 finding`) can skip it.

## Footer

- Breaking change: a `BREAKING CHANGE: …` footer, or `!` after the type/scope (`feat(api)!: rename FooService to BarService`).
- `Refs: #123` and paired-commit links are fine.
- `Signed-off-by:` is allowed.
- **No attribution trailers.** Never add `Co-Authored-By:` or a "Generated with Claude Code" line.

## Examples

```
feat(comms): add GPIO talk group selector watcher
```

```
fix(server): switch net.Listen to ListenConfig for context cancellation

Without context, the listener hangs on shutdown when ctx is already cancelled.
ListenConfig.Listen lets the bind respect ctx.
```

```
refactor(comms): move talk group registry into its own package

The registry is shared by the RPC handlers and the GPIO selector; keeping
it inside comms created an import cycle.
```

| Bad | Problem | Better |
|---|---|---|
| `Update files` | vague, no type or scope | `chore(rules): reorganize claude rule files` |
| `feat: Added new endpoint.` | past tense, capitalised, period | `feat(api): add list-sessions endpoint` |
| `fix bug` | no type colon, no scope, no detail | `fix(auth): handle expired session as no session` |
| `feat(server): WIP` | WIP never lands in main history | squash, or amend with a real subject |
| `feat: do everything` | several purposes | one commit per concern |

## Before running git commit

- The change meets the definition of done in `CLAUDE.md` (lint and tests pass).
- The type matches the change (`fix` for a real bug, not a refactor).
- The subject is imperative and lowercase, and a reviewer understands the change from it alone.
- The body explains any non-obvious why.

If the hook rejects the message, fix it immediately with `git commit --amend`.
