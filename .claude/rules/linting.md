---
paths:
  - "**/*.go"
  - ".golangci.yml"
---

# Go Lint Gate

`golangci-lint run --timeout 5m` must report `0 issues.` (it covers `go vet`). The Stop hook runs it on changed packages before a turn can end; `make lint-go` runs it repo-wide with `--fix`.

`.golangci.yml` enables the `gofmt` formatter, so `--fix` also applies gofmt's canonical formatting and no separate `gofmt -w` pass is needed. If `golangci-lint run --fix` is clean but `gofmt -l` is not, check that `gofmt` is still under `formatters.enable`.

## When lint flags something

Fix the code, not the linter. Suppress only when the pattern is intentional and idiomatic in context (cobra command vars, embed bridges) or a third-party contract makes the finding impossible to satisfy (an interface method that must accept an unused `context.Context`). In order of preference:

1. **Recurring pattern** — a rule under `linters.exclusions.rules` in `.golangci.yml`, with a comment when the reason isn't obvious.
2. **One-off** — `//nolint:<linter> // <reason>` at the call site.

Never a bare `//nolint`, never a linter disabled wholesale. If no clean fix exists, stop and ask.

## Common findings

| Finding | Fix |
|---|---|
| `gochecknoglobals` | Scope the var into a function. If it's genuinely needed (cobra command, embed bridge, registered driver), exclude it in config. |
| `gosec G101` | Production code reads credentials from env or config. A test-only default is documented in code and `G101` added to the gosec text exclusions. |
| `govet shadow` | Rename the inner variable: `err` → `decodeErr`, `dialErr`. |
| `nlreturn` | Blank line before `return`. |
| `noctx` | Context-aware form: `net.ListenConfig{}.Listen(ctx, ...)`, `http.NewRequestWithContext(ctx, ...)`. |
| `unparam` | Drop the parameter; if an interface requires it, name it `_`. |
| `wrapcheck` | `fmt.Errorf("operation: %w", err)`. |
