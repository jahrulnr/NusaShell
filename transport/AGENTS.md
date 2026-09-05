# Transport layer instructions

Applies to `transport/` in addition to the repository root `AGENTS.md`.

## Boundary

Transport maps HTTP RPC, WebSocket events, round SSE streams, local files,
plugin routes, and static assets onto application use cases and contract DTOs.
It owns decoding, body limits, protocol status, connection lifecycle, and
response encoding. It does not own business rules or persistence.

## Reuse before creation

- Start at `server.go`; add routes to its existing mux and middleware rather
  than starting another server or router.
- RPC feature methods belong in the existing `POST /rpc/{method...}` path and
  an application dispatcher. Do not add a bespoke HTTP endpoint for ordinary
  commands or queries.
- Reuse `writeJSON`, contract envelopes/errors, and existing body-limit and
  request-decoding patterns.
- Reuse `ws.go` and the application `Bus` for lifecycle events. Reuse
  `stream.go` and `RoundStreamRegistry` for live round deltas. Do not send the
  same signal through a new side channel.
- Reuse `harness_test.go` and existing fake provider/MCP/ACP fixtures for
  cross-layer tests before creating another test server.

Search the contract constant, application dispatcher, frontend RPC/listener,
and existing transport tests before changing a route or event. One behavior
must have one authoritative route and one event vocabulary.

## Protocol requirements

- Keep cancellation and disconnect handling context-aware and leak-free.
- Preserve ordering, replay cursor, deduplication, terminal-event, timeout,
  body-limit, MIME, cache, deep-link, and 404 behavior relevant to the path.
- Do not expose internal errors, filesystem paths, secrets, or stack traces in
  wire responses.
- Maintain intentional JSON compatibility. Follow `contracts/AGENTS.md` for
  any shape, field, method, or event change.
- Static delivery must continue to support embedded assets and the documented
  development disk fallback.

## Verification

Add or extend a handler-level test that exercises the real transport and the
application path. Cover malformed input and disconnect/error behavior, not
only success. Run:

```text
go test ./transport/...
go test -race ./transport/...
```

For frontend-visible behavior, run the matching frontend test and inspect the
real UI when appearance or interaction changes.
