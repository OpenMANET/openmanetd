---
name: new-service
description: Scaffold a new ConnectRPC API service with proto, handler, registration, and tests
disable-model-invocation: true
user-invocable: true
argument-hint: "<service-name> (e.g., location, mesh-monitor)"
---

# New API Service Scaffolding

Create a new ConnectRPC API service for OpenMANETd following established patterns.

## Arguments
- `$ARGUMENTS` contains the service name

## Steps

1. **Create proto definition** in `proto/openmanet/<service>/v1/`:
   - `<service>_service.proto` with service and RPC definitions
   - `<service>.proto` for shared message types
   - Follow naming and layout conventions from `.claude/rules/proto.md` (Get/List/Set/Update/Execute/Send/Stream verbs)
   - Use `buf.validate` annotations for field validation
   - Use `google.protobuf.Empty` for RPCs with no meaningful request/response
   - `make proto-lint` must be clean

2. **Generate code**: Run `make buf` to generate Go stubs in `internal/api/` and JS clients in `frontend/src/gen/`

3. **Create handler** at `internal/openmanet/server/handlers/<service>.go`:
   - Struct with `Log zerolog.Logger` + injected dependencies (interfaces, not concrete types)
   - Implement the generated handler interface
   - Use `connect.NewError()` with correct codes (see `.claude/rules/api-design.md`)
   - Log before returning errors
   - Add overrideable function fields for I/O operations

4. **Register in server** at `internal/openmanet/server/server.go`:
   - Add `api.Handle(...)` call with the handler struct literal
   - Include `connect.WithInterceptors(validateInterceptor)`

5. **Wire dependencies** in `internal/openmanet/openmanet.go` if needed

6. **Create tests**:
   - Unit tests in `internal/openmanet/server/handlers/<service>_test.go` (package `handlers_test`)
   - Add fakes to `mocks_test.go` following the `fake<Interface>` pattern with mutex-protected state
   - Use `newTestDB(t)` for database, `zerolog.Nop()` for logger
   - Add integration test cases in `integration_test.go`
   - Add proto validation tests if using `buf.validate` annotations

7. **Verify**: `make test`, `make integration-test`, and `make lint-go` pass; then run the `gap-review` skill and commit per the `commit` skill (generated `internal/api/` and `frontend/src/gen/` go in with the proto)
