# AGENTS.md - NusaShell Light

Instructions for humans and coding agents working in this repository.

## Agent behavior

- Always acting as a Senior fullstack developer.
- Take end-to-end ownership of the work: understand the architecture, clarify boundaries, implement the smallest correct solution, test it, and verify non-functional behavior.
- Make decisions grounded in repository evidence and established project constraints; do not hide uncertainty or invent completed work.

## Architecture principles

- **TDD:** follow red → green → refactor. Write or extend a failing test for behavior before implementation; make the smallest change that passes; refactor only while tests are green.
- **Clean Architecture:** keep business rules independent from delivery, persistence, frameworks, and external services. Dependencies point inward.
- **Dependency rule:** `domain` imports no application, infrastructure, transport, HTTP, WebSocket, SSE, database, filesystem, MCP SDK, or UI code. Outer layers may depend on inner layers, never the reverse.
- **KISS:** prefer the smallest explicit design that solves the current requirement. Avoid speculative abstractions, framework-heavy solutions, and premature optimization.
- **SOLID:** keep responsibilities focused, depend on small interfaces at boundaries, preserve substitutability, and avoid forcing consumers to depend on unused APIs.
- **Testability:** keep I/O at adapters and inject clocks, filesystem, network clients, process runners, and other effects. Code must support deterministic unit tests and isolated integration tests.
- **Non-functional testing:** support tests for concurrency and race safety, cancellation, timeouts, ordering, backpressure, resource cleanup, startup/shutdown, compatibility, observability, and performance where the contract requires it.

## Layer responsibilities

- `domain/`: pure entities, value objects, policies, and domain services.
- `application/`: use cases, handlers, ports, orchestration, and application events.
- `contracts/`: wire types, serialization, protocol versions, and boundary validation.
- `infrastructure/`: implementations of application ports and external adapters; no business rules.
- `transport/`: HTTP/RPC, WebSocket, SSE, session/subscription handling, and protocol mapping.
- `cmd/nusashell/`: composition root, configuration, lifecycle, embedded frontend serving, and process entrypoint.
- `frontend/`: native JavaScript, HTML, CSS, and static assets. Use browser APIs and ES modules; do not require a production Node build.
- `testdata/`: stable fixtures, golden files, compatibility samples, and non-secret test assets.

## Protocol and frontend rules

- WebSocket is for event-driven updates, subscriptions, and real-time/agent streaming.
- HTTP request/response is for predictable commands, queries, polling, health, and read models.
- SSE is for reconnect-friendly server-to-client streams where bidirectional WebSocket behavior is unnecessary.
- Document the routing matrix, event IDs, sequence/order, deduplication, replay/cursor, reconnect, timeout, cancellation, and backpressure policies.
- Keep wire compatibility intentional: test JSON field names, casing, null/omission behavior, enum values, and errors with golden fixtures.
- Embed production frontend assets with Go (`embed.FS` or equivalent). Test MIME types, module imports, deep links, cache behavior, and 404 handling.
- Do not hide frontend traceability behind a required bundle. If an exception is approved, document source mapping and debugging procedure.

## Verification baseline

Before considering a change complete, run the narrowest relevant tests first, then the repository gates:

```text
gofmt
go test ./...
go test -race ./...
go vet ./...
go build ./...
```

Do not weaken or delete tests merely to make a suite pass. Do not commit secrets, generated binaries, or production credentials. Keep changes small and focused; do not auto-commit or auto-push.

## Change documentation

When a behavior or public wire contract changes, update the relevant package documentation and golden fixtures. Record intentional compatibility breaks and non-functional trade-offs explicitly.
