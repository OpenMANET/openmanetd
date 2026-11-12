---
paths:
  - "**/*.go"
---

# Idiomatic Go

Follow Effective Go and the Go Code Review Comments wiki. These are the points review enforces here.

## Naming

- MixedCaps only, never underscores. Acronyms all-caps: `ID`, `HTTP`, `URL`, `API`, `RPC`.
- Short names in short scopes (`i`, `r` for `*http.Request`, `ctx`); longer scopes get longer names.
- Single-method interfaces are method + `er` (`Reader`, `Closer`); larger ones name the capability.
- Getters omit `Get` (`Name()`); setters use `Set` (`SetName()`).
- Package names are lowercase single words that don't repeat in the identifier (`config.Config`, not `config.ConfigStruct`).

## Errors

- Check every returned error. Discarding one takes `_ =` plus a comment saying why.
- `error` is the last return value; `nil` means success.
- Wrap with context: `fmt.Errorf("operation: %w", err)`.
- Sentinels: `var ErrFoo = errors.New("foo")`, checked with `errors.Is`. Custom error types implement `Error() string` and are checked with `errors.As`.
- No `panic` for expected failures — only truly unrecoverable programmer errors (invalid state at init).
- Handle the error or return it; don't log and return the same error. RPC handlers are the exception: they log, then return a `connect.Error` (`api-design.md`).

## Control flow

- Early returns keep the happy path unindented; no `else` after a `return`.
- `switch` instead of if/else chains on one value; type switches instead of chained `x.(T)` assertions.

## Types and interfaces

- Accept interfaces, return concrete types.
- Define interfaces at the consumer; keep them to one or two methods and compose larger ones.
- Value receivers for small types that aren't mutated, pointer receivers for mutation or large structs. Never mix both on one type.
- Struct literals use field names: `Foo{Bar: 1}`.

## Slices and maps

- `var s []T` rather than `s := []T{}`; a nil slice is valid. `make` with a capacity when the size is known.
- `for i, v := range s`; `for range n` when only the count matters.
- Never modify a collection while ranging over it — collect, then change in a second pass.

## Context

- `ctx context.Context` is the first parameter, is never stored in a struct, and is passed to every downstream call (DB, HTTP, RPC).

## Packages

- One narrowly scoped purpose per package. `internal/` for implementation details.
- No `init()` unless it is unavoidable (registering a driver).
- Break import cycles with an interface or a third package.

## Portability

- Code must compile for `linux/amd64`, `linux/arm64`, and `linux/mipsle`. Use `golang.org/x/sys/unix`, not the frozen `syscall` package, for socket options and OS constants.
- miniaudio (`malgo`) applies `-msse2` to any non-arm target, which breaks mipsle. The OpenWrt SDK wrapper in the firmware repo builds mipsle with `-tags noasm` (scalar resampler/mixer) and, on soft-float, `-latomic`. Don't add SIMD-only CGO flags; amd64/arm64 keep the SIMD defaults.

## Tooling

- Code passes `gofmt`/`goimports` and `go vet` (see `linting.md`).
