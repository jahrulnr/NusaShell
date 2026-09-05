# Application layer instructions

Applies to `application/` in addition to the repository root `AGENTS.md`.

## Boundary

This layer owns use cases, orchestration, ports, dispatchers, agent-turn
lifecycle, the event bus, and application services. Keep concrete filesystem,
database, HTTP-client, process, and UI behavior in adapters. Prefer narrow
ports defined by the use case over imports of concrete outer-layer services.

Some legacy composition imports remain in this package. Do not copy or expand
them as precedent. New boundaries should follow the dependency direction in
the root instructions.

## Reuse before creation

Start with these established seams before adding another service or runner:

- `ports.go` and `automation_ports.go` for effect boundaries.
- `app.go` plus the existing `*_dispatch.go` file for an RPC domain.
- `conversation_repository.go` for transcript creation, append, compaction,
  and persistence. Do not mutate formed transcripts through a parallel path.
- `bus.go` for shared lifecycle events and `roundstream.go` for live round
  deltas. Do not invent a second event channel.
- `app_runtime.go` and `App.goSafe` for fire-and-forget work.
- `service/` leaf packages for already-extracted pure helpers.
- Existing handlers, policy functions, and their fakes in the nearest
  `_test.go` file.

Search method strings, event constants, result types, and tests across
`application/`, `contracts/`, `transport/`, and `frontend/` before creating a
new API. Extend the matching domain dispatcher rather than adding a second
routing switch.

Add a port only for a real effect or replaceable boundary required now. Do not
create an interface solely to wrap one pure helper, mirror a concrete type, or
prepare for an imagined backend. Prefer adding a method to an existing
cohesive port when ownership and lifecycle are the same.

## Change shape

- Keep handlers thin: decode or validate application input, invoke domain
  decisions and ports, publish established events, return contract DTOs.
- Preserve cancellation, timeout, retry, ordering, backpressure, and cleanup
  behavior on asynchronous paths.
- Use injected clocks and fakes for deterministic tests.
- Never start an unmanaged goroutine. Tie work to context and lifecycle.
- A new RPC method normally requires a contracts constant/DTO, one existing
  dispatcher case, handler-level transport coverage, frontend wiring if
  visible, and documentation sync. It does not require a new transport route.

## Verification

Write the failing application test first and reuse existing fakes. Run the
narrow package/test, then:

```text
go test ./application/...
go test -race ./application/...
```

For an RPC, stream, or event change, also run the relevant `transport/` test
and follow `contracts/AGENTS.md`.
