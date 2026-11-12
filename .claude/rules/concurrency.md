---
paths:
  - "**/*.go"
---

# Concurrency Safety

All Go code must be safe under concurrent use. Verify new and modified code with `make test-race`.

## Shared state

- Guard mutable shared state with `sync.Mutex`, or `sync.RWMutex` when reads clearly dominate (`RLock` on read paths, `Lock` on write paths). Name it `mu` and place it directly above the fields it guards, with a comment saying so:
  ```go
  mu      sync.Mutex // protects the fields below
  counter int
  cache   map[string]string
  ```
- `defer mu.Unlock()` immediately after `mu.Lock()`.
- Keep critical sections short. Never hold a lock across I/O, network calls, or channel operations.
- Never copy a value containing a mutex; methods that touch it use pointer receivers.

## Maps

- No plain `map` read and written from multiple goroutines without a lock. `sync.Map` only when the access pattern fits (stable keys, read-heavy).

## Atomics

- `sync/atomic` / `atomic.Value` only for simple counters and flags. Anything more complex gets a mutex — clarity over micro-optimisation.

## Goroutines and channels

- Every goroutine has a shutdown path: `context.Context` cancellation or a `done` channel, with `case <-ctx.Done()` in every loop's `select`.
- Only the sender closes a channel, exactly once.
- Buffered channels are deliberate — comment why the buffer has its size.

## Tests

- Fakes holding mutable state are mutex-protected.
- Goroutines a test spawns are joined in `t.Cleanup()` before the test exits.
