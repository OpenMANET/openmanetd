---
paths:
  - "**/*.go"
---

# Go Testing

Every behavior change ships with tests in the same changeset. When a change alters expected behavior, update the existing test to assert the new behavior — don't delete it. The test environment is not production: fake hardware, network interfaces, and the filesystem.

## Conventions

- Black-box test packages: `package <pkg>_test`; handlers use `package handlers_test` in `<service>_test.go`, next to the code.
- `testify/require` for setup and preconditions, `testify/assert` for checks in the body.
- `t.Helper()` in every helper; `t.Cleanup()`, not `defer`, for teardown in helpers.
- Logger: `zerolog.Nop()`.
- Table-driven with `t.Run()`; names like `TestParseConfig_emptyInput`. Loop variables are per-iteration since Go 1.22 — no `tc := tc`.
- No `time.Sleep` — channels, `t.TempDir()`, fake clocks.
- Don't test generated code (`internal/api/`, `internal/database/models/`). No live hardware, real interfaces, or on-disk SQLite.

## Fakes

No mock frameworks. Hand-written fakes named `fake<Interface>` live in the package's `mocks_test.go`: mutex-protected state, call counters behind mutex-guarded getters, and error injection through fields.

```go
type fakeFooManager struct {
	mu         sync.Mutex
	doFooCalls int
	doFooErr   error
}

func (f *fakeFooManager) DoFoo(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.doFooCalls++

	return f.doFooErr
}
```

## Database and config

- `newTestDB(t)` opens in-memory SQLite (`:memory:`), applies the package-level `schemaSQL` constant, and registers `t.Cleanup(db.Close)`. Keep `schemaSQL` in sync with `internal/database/schema.sql`.
- Config-backed handlers get a real `*config.Config`: a temp YAML in `t.TempDir()` via `setupTestConfig(t, yaml)`, or `config.NewWithoutWatch(v)` to avoid file-watcher races.

## Fixtures and factories

- File fixtures live under `testfixtures/<subsystem>/`, found at runtime by walking up from `runtime.Caller(0)` to the module root — never absolute paths.
- Small factories with sensible defaults for repeated domain objects (`makeInterface(name, ifType)`, `makeStation(mac, signal)`); service constructors are named `new<Service>(deps...)`.

## Unit tests

Construct the handler directly with its dependencies:

```go
svc := &handlers.FooService{DB: newTestDB(t), Log: zerolog.Nop(), Mgr: &fakeMgr{}}
```

## Integration tests

- `//go:build integration` is the first line, before `package`. Plain `go test ./...` never runs them; use `make integration-test` (required whenever handler or server code changes).
- `newTestServer(t)` wires every handler behind `httptest.NewServer` with `validate.NewInterceptor()` and registers `t.Cleanup(srv.Close)`.
- Clients: `services.NewFooServiceClient(http.DefaultClient, srv.URL, connect.WithGRPCWeb())`.

## ConnectRPC errors

```go
var connectErr *connect.Error
require.ErrorAs(t, err, &connectErr)
assert.Equal(t, connect.CodeInvalidArgument, connectErr.Code())
assert.Contains(t, connectErr.Message(), "expected fragment")
```

For streaming RPCs the error may surface on `stream.Receive()` rather than the initial call — handle both.

## Proto validation

Validate messages directly with the `protovalidate.Validator` from `newValidator(t)` — no server. Cover zero, negative, one below the minimum, the minimum, the maximum, one above it, and the empty string.
