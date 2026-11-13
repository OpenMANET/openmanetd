---
paths:
  - "frontend/src/**/*.{js,jsx}"
---

# Frontend Testing

Every behavior change ships with tests in the same changeset; when expected behavior changes, update the existing test rather than deleting it. Vitest + jsdom + `@testing-library/react`; run `make test-frontend`.

## Structure

- Tests live in `frontend/src/__tests__/`, mirroring `src/`, named `*.test.js` / `*.test.jsx`.
- Group with `describe('PascalCaseFeature', ...)`.
- Reset modules, stubs, and timers around every test:
  ```js
  beforeEach(() => { vi.resetModules(); vi.stubGlobal(...); vi.useFakeTimers(); });
  afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks(); vi.unstubAllGlobals(); vi.resetModules(); });
  ```
- Don't test `src/gen/`. Use fake timers, never real waits.

## Mocking

- Whole modules: `vi.mock('../path/module.js', () => ({ ... }))`.
- Per-test transport overrides: `vi.doMock()` followed by a dynamic `import()` to get a fresh module.
- ConnectRPC: `createRouterTransport` from `@connectrpc/connect`, registering only the services under test (unregistered ones fail naturally) — not HTTP mocks. A handler that should fail throws `ConnectError`; a plain `Error` is sanitized to "internal error" and doesn't exercise real wire behavior.
- Browser globals: `vi.stubGlobal('AudioContext', vi.fn(function (opts) { this.method = vi.fn(); }))`; factory helpers such as `createMockAudioContext()` for deep hierarchies.

## Components

```js
function renderFoo(overrides = {}) {
  const props = { onPress: vi.fn(), label: 'default', ...overrides };
  return { ...render(<Foo {...props} />), props };
}
```

- Query with `screen` (`getByText`, `getByLabelText`); use `container.querySelector()` for class assertions.
- Simulate events with `fireEvent`.

## Data transformations

- Cover `null`, an empty object, empty arrays, and fully populated data.
- Cover missing or empty strings, zero, and maximum values.
- Assert display formatting (`1500000` → `"1.5 Mbps"`).

## WebSockets

- A `MockWebSocket` class with static `OPEN` / `CLOSED` constants and `vi.fn()` `send` / `close`.
- Collect instances in an array reset in `beforeEach` to track multiple connections.
- Drive events by calling handlers directly (`mockWsInstances[0].onopen()`); fake timers for reconnect and timeout logic.
