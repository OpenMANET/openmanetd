# OpenMANETd — Claude Code Guide

## Commands

| Task | Command |
|------|---------|
| Full build (CGO + all hardware deps) | `make build` |
| Lite build (excludes whisper WASM, UPX-compressed, ~5MB) | `make build-lite` |
| Build React frontend to `static/` | `make frontend` |
| Run Go tests | `make test` |
| Tests with race detector | `make test-race` |
| Integration tests (no hardware needed) | `make integration-test` |
| Frontend tests | `make test-frontend` |
| Lint React frontend | `make lint-frontend` |
| Format Go code | `make fmt` |
| Lint Go with auto-fix | `make lint-go` |
| Lint React & Go | `make lint` |
| Generate protobuf code | `make buf` |
| Generate sqlc database code | `make sqlc-gen` |

`make build` runs fmt → vet → buf → sqlc-gen → frontend → compile in sequence.

## Architecture

```
cmd/                              Cobra CLI: root.go (full daemon), frontend.go (frontend-only mode), setup_reset.go (re-open the setup wizard)
internal/openmanet/               Start() wires all subsystems
internal/openmanet/server/        ConnectRPC HTTP server setup + service registration
internal/openmanet/server/handlers/   Service handler implementations (one file per service)
internal/api/                     AUTO-GENERATED protobuf + ConnectRPC Go stubs — DO NOT EDIT
internal/frontend/                HTTP server: serves React SPA, proxies /api/* and /ws to port 8087
internal/database/                SQLite: schema.sql + query.sql; models/ is generated
internal/config/                  Viper-backed config (reads /etc/openmanetd/config.yml)
internal/comms/                   Opus/RTP audio (portaudio, CGO required)
internal/blos/                    BLOS/Tailscale overlay
internal/mgmt/                    Alfred mesh management + wireless config
proto/                            Git submodule — .proto source files
frontend/                         React SPA (Vite); build output → static/
frontend/src/gen/                 AUTO-GENERATED protobuf JS clients — DO NOT EDIT
static/                           Embedded build output (go:embed target in static.go)
```

Ports: API + WebSocket = **8087**. Frontend server = **8081** (proxies `/api/*` and `/ws` → 8087).

## Code Generation

**Protobuf (`make buf`)**
- Source: `proto/` git submodule — if empty: `git submodule update --init --recursive`
- Config: `buf.yaml`, `buf.gen.yaml`
- Go output: `internal/api/` — never edit
- JS output: `frontend/src/gen/` — never edit

**Database (`make sqlc-gen`)**
- Edit: `internal/database/schema.sql` (schema) and `internal/database/query.sql` (queries)
- Config: `sqlc.yaml`
- Output: `internal/database/models/` — never edit

## Adding a New API Service

1. Add `.proto` file in `proto/openmanet/<service>/v1/`
2. `make buf` — generates Go in `internal/api/` and JS in `frontend/src/gen/`
3. Create `internal/openmanet/server/handlers/<service>.go`:
   - Struct with `Log zerolog.Logger` + dependencies
   - Implement generated `<Service>Handler` interface
   - Return `connect.NewError(connect.CodeInternal, err)` for errors
4. Register in `internal/openmanet/server/server.go` — add `api.Handle(...)` call
5. Wire dependencies via `APIServer` struct in `server.go` and `internal/openmanet/openmanet.go`
6. Write comprehensive unit and integration tests.  The test environment is not the same as production.  Mock tests where appropriate.
7. All code should lint cleanly

## Frontend Development

**Vite hot-reload dev server:**
```bash
cd frontend && VITE_API_TARGET=http://<remote-host>:8081 pnpm run dev
```

Proxy rules (`frontend/vite.config.js`):
- `/ws`, `/api`, `/whisper` → `VITE_API_TARGET` (default `http://localhost:8080`)
- `/rpc` → `VITE_RPC_TARGET` (default `http://localhost:8087`), `/rpc` prefix stripped before forwarding

**Frontend-only binary mode** (no local database/hardware):
```bash
./bin/openmanetd frontend --api-address http://<remote-host>:8087
```

**Linting:**
Run `make lint-frontend` before committing frontend changes. The ESLint config at `frontend/eslint.config.js` covers all `.js`/`.jsx` files under `frontend/src/` (excluding `src/gen/`). To auto-fix fixable issues: `pnpm -C frontend run lint:fix`.

## Gotchas

- **CGO required** for full build (`go-sqlite3`, `malgo`/miniaudio, `go-alfred`). Use `make build-lite` to skip comms/whisper. The DevContainer has all required native libraries.
- **`proto/` is a git submodule.** If empty: `git submodule update --init --recursive`, then `make buf`.
- **`static/` must be populated** before compiling the Go binary. The `//go:embed static/*` directive in `static.go` fails at compile time if the directory is empty. Run `make frontend` first, or use `make build`.
- **Never edit `internal/api/`, `frontend/src/gen/`, or `internal/database/models/`** — generated, will be overwritten.
- **No WriteTimeout on the API server** — intentional, required for long-lived streaming RPCs in CommsService.
- **Cross-architecture builds**: The application must compile for `linux/amd64`, `linux/arm64`, and `linux/mipsle`. Use `golang.org/x/sys/unix` (not the frozen `syscall` package) for socket options and other OS-level constants to ensure portability. **`malgo` requires `-tags noasm` for non-SIMD targets** — miniaudio's CGO directives apply `-msse2` to any non-arm/non-arm64 target by default, which breaks `linux/mipsle` builds. The OpenWrt SDK build wrapper that produces the mipsle binary must pass `-tags noasm` so miniaudio's resampler/mixer fall back to scalar C; on soft-float mipsle the linker may also need `-latomic`. amd64 and arm64 builds are unaffected and use the SIMD-enabled defaults.
- **Concurrency safety is mandatory** for all Go code. Protect shared state with mutexes, ensure every goroutine has a shutdown path, and never access a plain `map` concurrently. Run `make test-race` to verify. See `.claude/rules/concurrency.md` for full rules.
- **Resource efficiency is mandatory** — target devices include embedded ARM board and MIPS routers (nice to have) with limited memory and CPU. Preallocate slices/maps, reuse buffers, avoid allocations in hot loops, compile regexps at package level, stream large data, and bound all caches and goroutine counts. See `.claude/rules/performance.md` for full rules.
- **Idiomatic Go is mandatory** — follow community patterns from Effective Go and the Go Code Review Comments wiki. Early returns, error wrapping with `%w`, accept interfaces / return concrete types, `context.Context` as the first parameter, short names in short scopes, all-caps acronyms. See `.claude/rules/idiomatic-go.md` for full rules.
- **Lattice is the frontend design system** — all React/CSS work must use the tokens and primitives in `frontend/src/styles/lattice.css` (panels, buttons, chips, kv rows, etc.). No third-party UI libraries, no border-radius, no hard-coded colors. Pages must work on a 360px mobile viewport. See `.claude/rules/frontend.md` for full rules.
- **Instrumentation snapshot doc stays in sync** — any addition, removal, or rename of fields on a snapshot struct (`CommsSnapshot`, `PortSnapshot`, `JitterBufferSnapshot`, `AudioEncoderSnapshot`, `BridgeSnapshot`, etc.) must be reflected in `docs/instrumentation-snapshot.md` in the same changeset. See `.claude/rules/instrumentation.md` for full rules.
- **Development Environment** - development environment is `linux/amd64` or `linux/arm64`.  The development environment does not match the runtime environment.
- **Production Build** - openmanetd is built as part of the OpenMANET firmware in a different repository.  It handles all cross compilation requirements for supported cpu architectures.

## Testing Patterns

- **Unit tests**: package `handlers_test`, file `<service>_test.go`. Use `newTestDB(t)` for in-memory SQLite, `zerolog.Nop()` for logger.
- **No mock framework**: hand-written fakes only (see `mocks_test.go` pattern in handlers/).
- **Integration tests**: build tag `//go:build integration`. Use `newTestServer(t)` + `httptest.NewServer`. Run with `make integration-test`.
- **Frontend tests**: Vitest + jsdom. Run with `make test-frontend`.
