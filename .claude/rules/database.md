---
paths:
  - "internal/database/**"
  - "sqlc.yaml"
---

# Database (sqlc + SQLite)

- Edit only `internal/database/schema.sql` and `internal/database/query.sql`. `internal/database/models/` is generated — the PostToolUse hook runs `make sqlc-gen` after each SQL edit, and the Stop hook fails if `sqlc diff` shows stale models.
- Query naming:
  ```sql
  -- name: GetFoo :one
  -- name: ListFoos :many
  -- name: CreateFoo :one   -- upserts: INSERT ... ON CONFLICT ... DO UPDATE ... RETURNING *
  -- name: UpdateFoo :exec
  -- name: DeleteFoo :exec
  ```
- Handlers pass the RPC's `ctx` to every query: `nodes, err := s.DB.ListFoos(ctx)`.
- Schema changes also update the `schemaSQL` constant that `newTestDB(t)` applies in tests.
