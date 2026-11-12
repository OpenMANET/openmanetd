---
name: gap-review
description: Use before reporting non-trivial work as done in this repository — executing a plan from ~/.claude/plans/, a feature that touches 3+ files or adds a package, schema, or RPC, a bug fix spanning several files, or a refactor. Re-reads the requirements, compares what was delivered item by item, fixes the gaps, and re-runs verification.
---

# Gap Review

Agents drift. Mid-task you satisfy whatever is in front of you and quietly skip line items from the original spec: verification steps, secondary edge cases, follow-up tests, docs listed in the plan's file table. This review catches the drift before it ships. It is part of the definition of done, not polish.

## When

Required for:

- Implementing a plan from `~/.claude/plans/` — every numbered step, every row of the "Files to modify" table, every item under "Verification".
- A multi-file feature (3+ files, a new package, a schema migration, a new RPC), even without a written plan: the conversation sets the acceptance criteria.
- A bug fix across files — not done until a test pins the regression and related code paths have been checked for the same defect.
- A refactor — the contract must be unchanged; run the full test suite and re-read the API surface.

Skip it for single-file edits with one obvious goal, read-only investigation, and conversational answers. When in doubt, do it — a minute of self-review is cheaper than the user finding the miss.

## Steps

1. **Re-read the source of truth** — the plan file, the user's last few messages, or the ticket. Don't work from memory of what was asked.
2. **Compare item by item** against what you delivered:
   - each numbered step in the plan;
   - each row of the "Files to modify" table — was the file touched, with the change the row described?
   - each item under "Verification" or "Definition of done" — did you actually run it?
   - each acceptance criterion implied by the request.
3. **Be honest about gaps.** "Mostly done" is a gap. "Equivalent but a different shape than the plan" is a gap — fix it or note the deliberate divergence. A skipped verification step is a gap.
4. **Fix every gap you can.** If something is genuinely out of scope (the plan said so), leave it but name it as deferred so the user can confirm.
5. **Surface what you can't fix.** A blocker (missing access, ambiguous requirement, scope creep, a system you lack context for) gets raised clearly — stop and ask rather than papering over it.
6. **Re-run verification after the fixes** — the definition-of-done commands in `CLAUDE.md` plus anything the plan lists. Never report from a pre-fix run.

## Report

The response to the user includes:

- A short gap-fix summary: what was missing and how it was closed — or, if nothing was, an explicit "Reviewed against <plan/request>; no gaps found."
- Verification results from the post-fix run.
- Each deliberate divergence from the plan with a one-line rationale.
- Deferred or blocked items, clearly labeled.

## Gaps this catches

- The plan listed a file (often a doc) to create; the feature shipped without it.
- The plan said to split A and B into separate files; they ended up in one.
- The plan said to run integration tests; only unit tests ran.
- A validator exists but no test pins the case it was written for.
- A new env var was never added to `bindEnvs`, so viper ignores it in production. Only a test that sets the variable and asserts the parsed config catches that.
- A rename missed `_test.go` files behind `//go:build integration`: `go test ./...` is green, `make integration-test` is broken.

## Anti-patterns

- A victory lap ("did everything in the plan!") without walking the plan.
- Checking the happy-path items and skipping verification.
- Relabeling gaps as "follow-ups" to avoid fixing them. A follow-up is a scope decision the user agreed to.
- Dropping the summary because everything passed.
