# Commit Message Rules

Every commit Claude creates in this repository **must** follow the Conventional Commits 1.0 specification (https://www.conventionalcommits.org/en/v1.0.0/). This is non-negotiable — the project relies on consistent commit history for change-log generation, release automation, and review.

---

## Format

```
<type>(<scope>): <subject>

[optional body]

[optional footer(s)]
```

- **type** — required. Lowercase. One of the allowed values below.
- **scope** — optional but strongly preferred. Lowercase, single word, in parentheses. Names the package/area touched (e.g., `comms`, `blos`, `mgmt`, `server`, `database`, `config`, `cmd`, `proto`, `frontend`, `rules`, `deps`).
- **subject** — required. Imperative mood ("add" not "added"/"adds"). Lowercase first letter. No trailing period. ≤ 72 chars total line length including type and scope.
- **body** — optional. Wrap at ~80 chars. Explain *why* and *what changed at a high level*; the diff already shows the *what* in detail.
- **footer** — optional. Used for `BREAKING CHANGE: …` notices and trailers like `Co-Authored-By:` or `Refs: #123`.

## Allowed types

| Type | When to use |
|---|---|
| `feat` | A new feature visible to a user, operator, or downstream consumer. |
| `fix` | A bug fix in code that was previously released or merged. |
| `chore` | Maintenance that doesn't change behavior: dep bumps, lint/format passes, file moves, build config tweaks. |
| `docs` | Documentation-only changes: README, comments, `.claude/rules/*`, design specs, plans. |
| `refactor` | Internal restructuring that preserves behavior — no new feature, no bug fix. |
| `test` | Test-only additions or changes. |
| `perf` | A change whose primary purpose is improving performance. |
| `ci` | Changes to CI config (`.github/workflows/`, lint config, build scripts). |
| `build` | Changes to the build system (`Makefile`, `go.mod` infra, `buf.gen.yaml`, `sqlc.yaml`). |
| `style` | Formatting only — no code changes. Rare; usually rolled into `chore`. |
| `revert` | Reverts a previous commit. Subject is the reverted commit's subject; footer carries `Reverts: <sha>`. |

If a single change touches multiple types, pick the dominant one. If two are genuinely co-equal, split the commit.

## Subject rules

- Imperative mood: `add talk group registry`, not `added talk group registry` or `adding talk group registry`.
- Lowercase first letter (after `:`): `add foo`, not `Add foo`.
- No trailing period.
- Describe the change, not the reason: `add session expiry index` (not `users complained about slow lookups`). The reason goes in the body.
- Keep references to issue/PR numbers in the footer, not the subject.

## When the body is required

Add a body when any of these apply:
- The change has a non-obvious motivation a future reader would want.
- The change has a non-obvious side effect (subtle behavior shift, reordering, performance footgun avoided).
- The change touches more than three files or three logical concerns.
- A breaking change exists — must be flagged with `BREAKING CHANGE:` in the footer or `!` after the type/scope (`feat(api)!: rename FooService → BarService`).

When in doubt, add a one-line body. Empty body is fine for trivial commits like `chore(lint): fix gosec G404 finding`.

## Examples (good)

```
feat(comms): add GPIO talk group selector watcher
```

```
fix(server): switch net.Listen to ListenConfig for context cancellation

Without context, the listener hangs on shutdown when ctx is already cancelled.
ListenConfig.Listen lets the bind respect ctx.
```

```
chore(lint): fix all golangci-lint findings
```

```
docs(rules): add linting rule and definition-of-done
```

```
refactor(comms): move talk group registry into its own package

The registry is shared by the RPC handlers and the GPIO selector; keeping
it inside comms created an import cycle.
```

```
build(deps): bump github.com/mattn/go-sqlite3 to v1.14.50
```

```
feat(api)!: rename HealthService.Check response field to status_code

BREAKING CHANGE: clients must update generated bindings.
```

## Examples (bad — fix before committing)

| Bad | Why | Fix |
|---|---|---|
| `Update files` | Vague, no type, no scope | `chore(rules): reorganize claude rule files` |
| `feat: Added new endpoint.` | Past tense, capitalized, trailing period | `feat(api): add list-sessions endpoint` |
| `fix bug` | No type colon, no scope, no detail | `fix(auth): handle expired session as no session` |
| `feat(server): WIP` | "WIP" should never be in main history | Squash before merge or amend with a real subject |
| `feat: do everything` | Multi-purpose subject | Split into separate commits per concern |

## Co-Authored-By trailer

Claude's commits NEVER include a trailer line

## Definition of done

A commit is not complete until:
1. Subject follows the format above.
2. The change passes `make lint` (see `linting.md`) and tests.
3. The body explains the *why* when the change isn't self-evident.
4. No Co-Authored-By or other trailer is present (see "Co-Authored-By trailer" above).

If any check fails, fix and re-stage; never commit a half-formed message hoping to amend later.

## How to verify before committing

Read your draft message and ask:
- Does the type match what the change actually is? (`fix` for a real bug, not for a refactor.)
- Is the subject imperative and lowercase?
- Could a reviewer understand the change from the subject alone?
- If non-obvious, does the body explain why?

If any answer is no, rewrite before running `git commit`.
