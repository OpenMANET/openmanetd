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
| Format + lint protos | `make proto-lint` |
| Generate protobuf code | `make buf` |
| Generate sqlc database code | `make sqlc-gen` |

`make build` runs fmt → vet → buf → sqlc-gen → frontend → compile in sequence.

Frontend-only binary (no local database or hardware): `./bin/openmanetd frontend --api-address http://<remote-host>:8087`.

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
internal/comms/                   Opus/RTP push-to-talk audio (malgo/miniaudio, CGO required)
internal/blos/                    BLOS/Tailscale overlay
internal/mgmt/                    Alfred mesh management + wireless config
internal/instrumentation/         Periodic JSON runtime snapshot (field reference: docs/instrumentation-snapshot.md)
proto/                            Git submodule — .proto source files
frontend/                         React SPA (Vite); build output → static/
frontend/src/gen/                 AUTO-GENERATED protobuf JS clients — DO NOT EDIT
static/                           Embedded build output (go:embed target in static.go)
```

Ports: API + WebSocket = **8087**. Frontend server = **8081** (proxies `/api/*` and `/ws` → 8087).

## Hard rules

- **Never edit generated code**: `internal/api/` and `frontend/src/gen/` (edit `proto/`, run `make buf`), `internal/database/models/` (edit `internal/database/schema.sql` / `query.sql`, run `make sqlc-gen`; config in `sqlc.yaml`). A hook denies these edits.
- **`proto/` is a git submodule.** If empty: `git submodule update --init --recursive`, then `make buf`.
- **`static/` must be populated** before compiling the root package — `//go:embed static/*` in `static.go` fails otherwise. Run `make frontend` first, or use `make build`.
- **CGO is required** for every build (`go-sqlite3`, `malgo`/miniaudio, `go-alfred`); there is no pure-Go fallback. `make build-lite` only drops the embedded whisper WASM and UPX-compresses. The DevContainer has the native libraries.
- **No `WriteTimeout` on the API server** — intentional; long-lived CommsService streams need it.
- **Targets are resource-constrained**: embedded ARM boards and MIPS routers. Go must compile for `linux/amd64`, `linux/arm64`, and `linux/mipsle`, be concurrency-safe, and be frugal with memory and CPU.
- **Dev ≠ runtime.** Development happens on `linux/amd64` or `linux/arm64`. Production binaries are built and cross-compiled by the OpenMANET firmware repository.

## Detailed rules load on demand

Files in `.claude/rules/` carry `paths:` frontmatter and load into context only when you read or edit a matching file. Before creating a file in an area you haven't touched yet, read a neighbouring file so its rules are loaded first.

| Working on | Rules |
|---|---|
| Any `*.go` | `idiomatic-go`, `concurrency`, `performance`, `linting`, `testing` |
| `internal/openmanet/server/**`, `internal/openmanet/openmanet.go` | `api-design` |
| `proto/**`, `buf*.yaml` | `proto` |
| `internal/database/**`, `sqlc.yaml` | `database` |
| `internal/{comms,blos,sysupgrade,wireless,instrumentation}/**`, `docs/instrumentation-snapshot.md` | `instrumentation` |
| `frontend/src/**` | `frontend` (Lattice design system), `frontend-testing` |

Skills in `.claude/skills/` load when invoked: `commit` (before any `git commit`), `gap-review` (before reporting non-trivial work done), `/new-service <name>` (scaffold a ConnectRPC service), `/gen` (run all code generation).

## Definition of done

1. Every behavior change has tests in the same changeset.
2. `go build ./...` and `go test -count=1 ./internal/...` pass. Handler or server changes also pass `make integration-test` (tagged `//go:build integration`; plain `go test ./...` never runs them). Concurrency-sensitive changes pass `make test-race`.
3. `golangci-lint run --timeout 5m` reports `0 issues.`
4. Proto changes: `make proto-lint` is clean, `make buf` regenerates, and the generated code is committed with the proto.
5. Frontend changes: `make lint-frontend` and `make test-frontend` pass, the page works at 360px wide, and `make frontend` has been re-run if the change ships through the binary.
6. Snapshot struct field changes are reflected in `docs/instrumentation-snapshot.md`.
7. Non-trivial work (a plan, 3+ files, a new package/schema/RPC, a multi-file fix, a refactor) has been through the `gap-review` skill.
8. Commits follow the `commit` skill: Conventional Commits, no trailers.

Hooks in `.claude/hooks/` enforce the mechanical parts. Before a turn ends, the Stop hook checks everything changed since the merge base with `main` — gofmt, golangci-lint, `go build`, eslint, buf lint/format, `sqlc diff`, Lattice colours/radii, the snapshot doc — and blocks once with the failures. Commit messages are checked right after `git commit`. Fix the cause rather than working around a hook; if a failure can't be fixed, tell the user why. Hook self-test: `bash .claude/hooks/test.sh`.
