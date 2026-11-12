---
paths:
  - "proto/**"
  - "buf.yaml"
  - "buf.gen.yaml"
---

# Protobuf

`.proto` files live in the `proto/` git submodule (if empty: `git submodule update --init --recursive`) and are managed with buf. Generated Go (`internal/api/`) and JS (`frontend/src/gen/`) are never edited by hand; the PostToolUse hook runs `make buf` after each proto edit.

## Definition of done

1. `make proto-lint` is clean (`buf format -w proto`, then `cd proto && buf lint`; exit 0, no output). The Stop hook runs the same checks.
2. `make buf` regenerates without errors.
3. Generated `internal/api/` and `frontend/src/gen/` are committed with the proto change — or in paired commits linked by a `Refs:` trailer.
4. Go consumers still build, lint, and pass tests (`linting.md`).
5. Commit scope `proto`: `feat(proto): add ListSessions RPC`.

A naming slip in a `.proto` becomes a public API break in every generated language at once — fix it at the schema.

## Naming and layout

`proto/buf.yaml` uses the `STANDARD` lint preset (rule reference: https://buf.build/docs/lint/rules — look up an unfamiliar rule before suppressing it).

- Layout: `proto/openmanet/{domain}/v1/` with `package openmanet.{domain}.v1;`:
  - `{domain}_service.proto` — RPC definitions
  - `{domain}.proto` — shared message types
  - `{domain}_config.proto`, `{domain}_status.proto` — when distinct
- Service: `{Domain}Service`.
- RPC verbs: `Get{Resource}` (one item or current status), `List{Resources}`, `Set{Property}` (one property), `Update{Resource}Config` (modify and persist), `Execute{Action}` (administrative action), `Send{Event}` (client-initiated state change), `Stream{Direction}` (`StreamAudioRx`, `StreamAudioTx`).
- Fields `snake_case`; messages `PascalCase`; enum values `SCREAMING_SNAKE_CASE` prefixed with the enum name.
- Every file declares a package; imports are explicit, never transitive.
- Each RPC has its own `FooRequest`/`FooResponse`. `google.protobuf.Empty` is allowed when there is no meaningful request or response, but prefer a dedicated message when fields are likely later — swapping `Empty` out is a breaking change.
- Time values use `durationpb` / `timestamppb`.

## Validation

Field constraints live in the proto via protovalidate; the `validate.NewInterceptor()` interceptor enforces them. Never duplicate validation in handler code — change the proto.

```proto
import "buf/validate/validate.proto";

message SetWidgetRequest {
  int32 size = 1 [(buf.validate.field).int32 = {gte: 1, lte: 32}];
  string name = 2 [(buf.validate.field).string = {min_len: 1, max_len: 64}];
}
```

## Common findings

| Finding | Fix |
|---|---|
| `FIELD_LOWER_SNAKE_CASE` | `userID` → `user_id` (generated Go is still `UserID`). |
| `SERVICE_SUFFIX` | `service Auth` → `service AuthService`. |
| `RPC_REQUEST_RESPONSE_UNIQUE` | A dedicated request/response per RPC, or `google.protobuf.Empty` as above. |
| `PACKAGE_DIRECTORY_MATCH` | Move the file or rename the package so they match. |
| `IMPORT_USED` | Remove the unused import. |
| `import "X": file does not exist` | `cd proto && buf dep update` to refresh `buf.lock`. |

## Suppression

Only when an external schema we depend on, or a third-party field name we can't rename, requires it:

1. `lint.except` in `proto/buf.yaml` for a whole rule the project has agreed on.
2. `// buf:lint:ignore <RULE_ID>` above the line, with the reason in a comment on the next line.

Never disable rules for convenience. If a fix would change wire compatibility, stop and ask — proto is a public contract.
