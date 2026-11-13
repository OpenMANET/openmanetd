---
paths:
  - "**/*.go"
---

# Resource Efficiency

Targets include embedded ARM boards and MIPS routers with little memory and CPU. Check every change against this list.

## Memory

- Preallocate slices and maps when the size is known or can be estimated: `make([]Node, 0, len(in))`, `make(map[string]int, len(keys))`.
- Hoist allocations out of hot loops; reuse slices with `s[:0]`; `sync.Pool` for frequently allocated short-lived buffers.
- Bound every cache, buffer, and queue with a size limit or eviction policy.
- Set large slices/maps in long-lived structs to `nil` once done so the GC can reclaim them.
- Prefer `[]byte` for read-transform-discard data; avoid repeated `string(b)` / `[]byte(s)` conversions.
- Pointer receivers on large structs.

## CPU

- Don't recompute what hasn't changed; cache expensive results (parsing, file reads) when inputs are stable.
- Compile regexps once at package level with `regexp.MustCompile`.
- `strings.Builder` for multi-step string building; `strconv` over `fmt.Sprintf` for simple conversions.
- No `reflect` or `%v` formatting in hot paths — concrete types and type switches.
- Maps for lookups, sorted slices with binary search when order matters, bitmasks for flag sets.

## I/O

- Buffered I/O (`bufio.Reader`/`Writer`) sized to the expected payload — the 4KB default is often too small for network reads.
- Stream large data line by line or in chunks instead of loading it whole.

## Goroutines

- Bound concurrency with a semaphore (`chan struct{}`) or worker pool.
- Prefer one goroutine with a ticker over a goroutine per event.
- A goroutine that never exits is a memory leak (see `concurrency.md`).
