# Gap-Review Rule

After completing any non-trivial work in this repository, you MUST review what you actually did against the original requirements or plan, identify gaps, and fix them. This is part of the definition of done — not an optional polishing step.

The rule exists because agents drift. Mid-stream you focus on whatever's in front of you, satisfy the immediate sub-task, and quietly skip line items in the original spec — verification steps, secondary edge cases, follow-up tests, doc updates listed in the plan's file table. A self-review at the end catches the drift before it ships.

---

## When this rule fires

The gap review is mandatory for:

- **Implementing a plan from `/home/vscode/.claude/plans/`** — every numbered step, file in the "Files to modify" table, and item in the "Verification" section gets checked off explicitly.
- **Implementing a multi-file feature** (3+ files touched, new package added, schema migrated, RPC introduced) — even without a written plan, there's an implicit acceptance criteria from the conversation.
- **Fixing a bug across multiple files** — the fix isn't done until tests pin the regression and any related codepaths are checked for the same defect.
- **Refactoring** — the contract should be unchanged afterwards; verify by running the full test suite and re-reading the API surface.

The rule does **not** fire for:

- Single-file edits with one obvious goal (typo fix, rename, comment update).
- Read-only investigation, search, or research with no code change.
- Conversational answers where no code was modified.

If in doubt, do the review. The cost of a 60-second self-check is much less than the cost of the user re-reading your diff and finding what you missed.

## How to do the review

1. **Re-read the source of truth.** Open the plan file (if one exists), the user's last few messages, or the issue/ticket. Do not rely on your in-conversation memory of what was asked — re-read it.
2. **Compare item-by-item against what you actually delivered.** Walk every:
   - Numbered step in the plan's refactor section
   - Row in the plan's "Files to modify" table — was each file touched? With the change the row described?
   - Item in the plan's "Verification" or "Definition of done" section — did you actually run it?
   - Acceptance criterion implied by the user's request
3. **Be honest about gaps.** "Mostly done" is a gap. "Equivalent but in a different shape than the plan called for" is a gap (and either fix it or note the deliberate divergence in the response). Skipping a verification step is a gap.
4. **Fix every gap you can.** If a gap is genuinely out of scope (the plan said "in scope: A; out of scope: B"; you delivered A; B is still out of scope), don't fix it — but explicitly call it out as deferred so the user can confirm.
5. **Surface anything you cannot fix.** A blocker (missing access, ambiguous requirement, scope creep) gets called out clearly, not buried.

## What to do with the result

After the review, the response to the user includes:

- A short **gap-fix summary** describing what was missing and how you closed it.
- The verification results (`make lint`, `go test`, etc.) re-run after the fixes — not the pre-fix run.
- Any **deliberate divergences** from the plan, with a one-line rationale each (e.g. "EventHandler returns Result instead of error for symmetry with Queue.Handler — supports the same Ack/Drop/Retry/DeadLetter mapping for EH-backed consumers").
- Any **deferred or blocked items**, clearly labeled as such.

If the review surfaces no gaps, say so explicitly ("Reviewed against plan; no gaps found"). Saying nothing leaves the user wondering whether you reviewed or just hoped.

## Examples of gaps the review catches

These are real classes of drift the review is designed to catch — not all from this repo, but illustrative:

- **Plan listed file `X.md` to be created; you delivered the feature but skipped the doc.**
- **Plan said "split A and B into separate files"; you put both in one file because it was easier mid-implementation.**
- **Verification section said "run integration tests"; you ran unit tests and called it done.**
- **Plan said "add validation test for case Y"; the validator code is there, but no test pins it — the case is unverified.**
- **You introduced a new env var (`SUMMIT_QUEUE_BACKEND`) but didn't add it to `bindEnvs`, so viper never reads it from the environment in production. A test that sets the env var and asserts the parsed config catches this; running only the validator in isolation does not.**
- **You renamed a function in package A but missed its uses in `_test.go` files behind a build tag (`//go:build integration`). `go test ./...` is green; `make integration-test` is broken.**

The pattern: each of these would have shipped silently if the review didn't compare delivered-vs-promised line-by-line.

## Anti-patterns

- **Treating the review as a victory lap.** "I did everything in the plan!" without actually walking the plan is not a review.
- **Reviewing only the happy-path items and skipping verification.** If the plan listed `make lint` as a verification step, run it; don't claim done from a stale earlier run.
- **Marking gaps as "follow-ups" to avoid fixing them.** A follow-up is a deliberate scope decision the user agreed to, not a way to ship incomplete work.
- **Suppressing the gap-fix summary because everything passed.** Even when the review finds no gaps, the user benefits from knowing you checked.

## Definition of done (this rule)

A piece of work is not complete until:

1. The original requirements / plan / acceptance criteria have been re-read.
2. Each line item has been compared against what was delivered.
3. Every fixable gap has been fixed.
4. Every deliberate divergence has been noted in the response.
5. Verification commands listed in the plan or in `.claude/rules/linting.md` have been run **after** the gap fixes.
6. The response to the user includes either a gap-fix summary or an explicit "no gaps found".

## When in over your head

If the review surfaces a gap you cannot fix in scope (the right fix touches a system you don't have context for, or it's blocked on a question only the user can answer), stop and ask. Do not paper over it. The whole point of the review is to surface drift; hiding it defeats the rule.
