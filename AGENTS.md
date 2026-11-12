# OpenMANETd — Codex Guide

`CLAUDE.md` is the canonical repository guide. Before modifying this repository,
read it completely and follow its commands, architecture notes, generated-code
boundaries, definition of done, and platform gotchas.

The detailed rules under `.claude/rules/` also apply to Codex. Claude Code loads
them automatically from their `paths:` frontmatter; read the ones relevant to
the work before editing:

- All Go changes: `idiomatic-go.md`, `concurrency.md`, `performance.md`,
  `linting.md`, and `testing.md`.
- API handler and server changes: `api-design.md`.
- Proto changes: `proto.md`.
- Database schema or query changes: `database.md`.
- Frontend changes: `frontend.md` and `frontend-testing.md`.
- Instrumentation snapshot changes: `instrumentation.md`.

Procedures live as skills under `.claude/skills/`:

- Commit messages: `commit/SKILL.md`.
- Final self-review of non-trivial work: `gap-review/SKILL.md`.

Treat these referenced documents as repository instructions, not optional
background material. Keep this file as a small entrypoint so the Claude and
Codex guidance cannot drift into conflicting copies; update the canonical guide
or shared rule files when project policy changes.

At minimum, every behavior change must have tests, all Go changes must be
formatted and lint-clean, and concurrency-sensitive changes must pass the race
detector. Never directly edit generated files under `internal/api/`,
`frontend/src/gen/`, or `internal/database/models/`.
