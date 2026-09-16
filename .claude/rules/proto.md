# Protobuf Rules

All `.proto` files in OpenMANETd live under `proto/` (a git submodule) and are managed with [buf](https://buf.build). Any change to a `.proto` file — adding, modifying, deleting — must pass `buf lint` with **zero issues** before it is committed.

This rule covers the gate. For naming conventions and RPC design see `.claude/rules/api-design.md`.

---

## Definition of done for proto changes

A change to anything under `proto/` is not complete until:

1. `make proto-lint` returns clean (runs `buf format -w proto` then `cd proto && buf lint`, exit 0, no output).
2. `make buf` regenerates Go and TypeScript stubs cleanly (no errors).
3. Generated code under `internal/api/` and `frontend/src/gen/` is committed alongside the `.proto` change in the same commit (or in two paired commits with a `Refs:` trailer linking them).
4. The change passes the broader code lint gate per `.claude/rules/linting.md` (Go consumers of the regenerated stubs still build and pass tests).
5. Commit message follows Conventional Commits per `.claude/rules/commits.md`. Use scope `proto` (e.g. `feat(proto): add ListSessions RPC`).

If `buf lint` reports any issue, fix it before committing. Do not bypass with `// buf:lint:ignore` directives unless explicitly required by an external schema we don't control — and document the reason in a comment immediately above the directive.

## Why we lint protos

`buf lint` enforces conventions that propagate into every language we generate from: Go, TypeScript, anything else added later. A naming or layout slip in `.proto` becomes a public API break in three places at once. Catching it at the schema is cheaper than fixing N generated SDKs.

The repo's `proto/buf.yaml` uses the `STANDARD` lint preset, which enforces:
- Field names: `snake_case`.
- Message names: `PascalCase`.
- Service names: `PascalCase` ending in `Service`.
- RPC method names: `PascalCase` verb-first (`Get`, `List`, `Create`, `Update`, `Delete`, `Set`, `Stream`).
- Enum values: `SCREAMING_SNAKE_CASE` with the enum name as a prefix.
- Files end with `_service.proto` or describe a single concern.
- File package matches the directory path (`proto/openmanet/comms/v1/comms.proto` ⇒ `package openmanet.comms.v1;`).
- Every file declares a package.
- Imports are explicit; no relying on transitive imports.

The full preset list is at https://buf.build/docs/lint/rules. If you don't recognize a rule the linter mentions, look it up there before suppressing.

## Common findings and fixes

| Finding | Fix |
|---|---|
| `FIELD_LOWER_SNAKE_CASE` | Rename `userID` → `user_id`. (Generated Go field will still be `UserID` because protoc-gen-go converts.) |
| `SERVICE_SUFFIX` | Add the `Service` suffix: `service Auth` → `service AuthService`. |
| `RPC_REQUEST_RESPONSE_UNIQUE` | Each RPC needs its own `FooRequest`/`FooResponse` even if they're empty. Exception: `google.protobuf.Empty` is allowed (`rpc_allow_google_protobuf_empty_requests: true` in `buf.yaml`) — use it when an RPC has no meaningful request or response, per `.claude/rules/api-design.md`. Prefer a dedicated request message when fields are likely to be added later, since swapping `Empty` out is a breaking change. |
| `PACKAGE_DIRECTORY_MATCH` | Move the file or rename the package so they match. |
| `IMPORT_USED` | Remove the unused import. |
| `import "X": file does not exist` | Run `cd proto && buf dep update` to refresh `buf.lock` and pull the dep into the local cache. |

## Working with protovalidate

We use `buf.build/bufbuild/protovalidate` for field constraints. Constraints live in the proto, not in handler code:

```proto
import "buf/validate/validate.proto";

message SetWidgetRequest {
  int32 size = 1 [(buf.validate.field).int32 = {gte: 1, lte: 32}];
  string name = 2 [(buf.validate.field).string = {min_len: 1, max_len: 64}];
}
```

The `ValidateInterceptor` wired into the server (see `.claude/rules/api-design.md`) enforces these automatically. **Never duplicate validation in handler code** — change the proto if a constraint needs adjusting.

## Workflow

```sh
# After editing a .proto file:
make proto-lint        # must report nothing — exits zero on success
make buf               # regenerates Go + TS stubs
go build ./...         # confirms generated code still compiles with consumers
go test ./internal/... # confirms behaviour is preserved
git add proto/ internal/api/ frontend/src/gen/
git commit -m "feat(proto): <subject>"
```

If `make proto-lint` flags issues, fix the proto before regenerating. Do not commit a `.proto` whose generation fails — broken proto = broken everything that imports it.

## Suppression

The `STANDARD` preset is the floor, not the ceiling. Suppression is allowed only when:
- An external schema we depend on requires a non-standard convention.
- A field name comes from a third-party API contract we cannot rename (e.g., reflecting an OAuth provider's response shape).

Two ways to suppress, in order of preference:
1. `proto/buf.yaml`'s `lint.except:` list — for an entire rule, when the project has decided collectively.
2. `// buf:lint:ignore <RULE_ID>` directly above the offending line — for one-off cases. Always include a comment explaining why on the next line.

Never disable rules wholesale for convenience. Rename the field instead.

## When in over your head

If `buf lint` flags something you can't resolve, stop and ask. Do not silently rename fields, restructure messages, or add suppression directives in a way that changes wire format compatibility. Proto is a public contract — even small changes can break clients.
