---
paths:
  - "internal/openmanet/server/**"
  - "internal/openmanet/openmanet.go"
---

# API Handlers (ConnectRPC)

Scaffold a new service end to end with `/new-service <name>`. Proto naming, layout, and validation rules load with any file under `proto/` (`proto.md`); sqlc rules with `internal/database/` (`database.md`).

## Handler structs

Each service is a struct literal with injected dependencies — no constructors, no `New()`. Field order:

```go
type FooService struct {
	Log     zerolog.Logger      // always present
	Cfg     *config.Config      // if config is needed
	Manager FooManagerInterface // domain-specific manager
	DB      *models.Queries     // if database access is needed
}
```

- Dependencies are interfaces, never concrete types, so tests can use hand-written fakes.
- I/O that is hard to fake gets an overridable function field plus a private helper that falls back to the real call:
  ```go
  ParseBatHosts func(string) (*batmanadv.BatHosts, error)

  func (s *FooService) parseBatHosts(path string) (*batmanadv.BatHosts, error) {
  	if s.ParseBatHosts != nil {
  		return s.ParseBatHosts(path)
  	}

  	return batmanadv.ParseBatHostsFile(path)
  }
  ```
- A `mu sync.Mutex` field serializes concurrent writes to shared config.

## Errors

Always return `connect.NewError` with the right code:

| Code | When |
|------|------|
| `CodeInternal` | Database failures, I/O errors, service failures |
| `CodeInvalidArgument` | Missing required fields, input out of range |
| `CodeFailedPrecondition` | Service unavailable or in the wrong state |
| `CodeNotFound` | Resource does not exist |

```go
return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("list nodes: %w", err))
return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("auth_key is required"))
```

Log before returning — `.Error()` for `CodeInternal`, `.Warn()` for degraded-but-partial. Handlers are the one layer that both logs and returns an error.

```go
s.Log.Error().Err(err).Msg("Failed to persist config")
return nil, connect.NewError(connect.CodeInternal, err)
```

Responses assembled from several sources (dashboard) accumulate errors and return partial data:

```go
var errs []error
if v, err := s.Provider.GetSomething(); err != nil {
	errs = append(errs, err)
} else {
	res.Value = v
}

return res, errors.Join(errs...)
```

## Streaming

```go
// Server stream: always watch ctx.Done().
for {
	select {
	case <-ctx.Done():
		return nil
	case data, ok := <-source:
		if !ok {
			return nil
		}
		if err := stream.Send(&foov1.StreamFooResponse{ /* ... */ }); err != nil {
			return err
		}
	}
}

// Client stream: always check stream.Err() after the loop.
for stream.Receive() {
	msg := stream.Msg()
	// process msg
}
if err := stream.Err(); err != nil {
	return nil, err
}
```

The API server has **no `WriteTimeout`** — long-lived streams (CommsService) depend on that. Don't add one.

## Logging

The `Log` field; structured fields before `.Msg()`:

```go
s.Log.Error().Err(err).Str("id", id).Msg("Failed to load foo")
```

- `Debug`: request received, per-frame counters, verbose detail
- `Info`: lifecycle (startup, shutdown)
- `Warn`: degraded state that still produces a response
- `Error`: the RPC returns an error

## Registration

In `internal/openmanet/server/server.go`:

```go
mux.Handle(foov1connect.NewFooServiceHandler(&handlers.FooService{
	Log:     log.With().Str("service", "foo").Logger(),
	Manager: fooManager,
	DB:      db,
}, connect.WithInterceptors(validateInterceptor)))
```

Wire new dependencies through the `APIServer` struct in `server.go` and in `internal/openmanet/openmanet.go`.

Handler or server changes also need `make integration-test` (see `testing.md`).
