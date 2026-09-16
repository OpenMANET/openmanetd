# Linting Rules

All Go code in OpenMANETd must pass `golangci-lint run --timeout 5m` cleanly. Lint output of "0 issues." is a precondition for considering any change "done".

---

## Definition of done

A change is not complete until:
1. `go build ./...` succeeds
2. `go test -count=1 ./internal/...` passes, and `make integration-test` passes when the change touches handler or server code (integration tests are tagged `//go:build integration` and never run via plain `go test ./...`)
3. `golangci-lint run --timeout 5m` reports `0 issues.`
4. `go vet ./...` is clean (covered by golangci-lint, but worth running standalone)
5. If the change touches `proto/`: `make proto-lint` reports zero issues per `.claude/rules/proto.md`.
6. The commit message follows Conventional Commits per `.claude/rules/commits.md`.

This applies to bug fixes, features, and refactors. There are no exceptions for "small" changes.

## When lint flags something

Default to fixing the code, not the linter. The linter rules in `.golangci.yml` were chosen deliberately. Suppress only when:
- The pattern is intentional and idiomatic in context (cobra command vars, embed bridges).
- A third-party library's contract makes the lint impossible to satisfy (e.g., interface methods that must accept a `context.Context` even when unused).

Two ways to suppress, in order of preference:
1. **Project-wide exclusion in `.golangci.yml`** — for patterns that recur across files (cobra globals, generated proto code, external libs that don't wrap errors). Add a new rule under `linters.exclusions.rules:` and document the reason in a comment if non-obvious.
2. **`//nolint:<linter> // <reason>`** at the call site — for one-off cases. Always include a reason after the `//`.

Never use `//nolint` without naming the specific linter. Never blanket-disable a linter.

## How to test the gate locally

```sh
make lint        # runs lint-go + lint-frontend
make lint-go     # golangci-lint run --fix --timeout 5m
```

`.golangci.yml`'s `formatters:` block enables the `gofmt` formatter, so `make lint-go`'s `--fix` applies the same canonical formatting a bare `gofmt -w` would — indentation, spacing, struct/const-block column alignment, and the rest of gofmt's rewrite rules — so a separate `gofmt -w` pass isn't needed. (Before this was wired in, `formatters.enable` was empty and `--fix` silently skipped gofmt-only breaks; if `golangci-lint run --fix` reports clean but `gofmt -l` doesn't, check that `gofmt` is still listed under `formatters.enable`.)

## Common findings and fixes

| Finding | Fix |
|---|---|
| `gochecknoglobals` | If the var is genuinely necessary (cobra command, embed bridge, registered driver), exclude in config. Otherwise scope the var into a function. |
| `gosec G101` (hardcoded credentials) | If it's a test-only default, document it in code and add `G101` to the gosec text exclusion list. Production code must read credentials from env or config. |
| `govet shadow` | Rename the inner variable. `err` → `decodeErr` / `dialErr` / etc. |
| `nlreturn` | Add a blank line before `return`. |
| `noctx` (net.Listen, http.Get, etc.) | Use the context-aware form: `net.ListenConfig{}.Listen(ctx, ...)`, `http.NewRequestWithContext(ctx, ...)`. |
| `unparam` | Drop the unused parameter. If it's required by an interface, prefix with `_`. |
| `wrapcheck` | Wrap with `fmt.Errorf("operation: %w", err)` so the caller can trace the source. |

## When in over your head

If a lint rule fires and you cannot find a clean fix, stop and ask. Do not silently add `//nolint` to make warnings disappear. The point of the gate is to catch real issues; suppressing them defeats it.
