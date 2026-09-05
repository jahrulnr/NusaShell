# Contracts layer instructions

Applies to `contracts/` in addition to the repository root `AGENTS.md`.

## Boundary

This layer is the canonical wire vocabulary shared by backend, transports,
frontend, plugins, and fixtures. It owns RPC method constants, event names,
request/result DTOs, JSON tags, protocol errors, and compatibility shapes. It
does not own business decisions, persistence models, dispatch logic, or
provider implementation details.

## Reuse before creation

1. Search `roster.go`, the smaller contract files, application dispatchers,
   frontend RPC strings, and golden fixtures for the behavior being changed.
2. Reuse the existing request/response envelopes, error codes, pagination
   conventions, IDs, event payloads, and DTOs when their semantics match.
3. Extend the existing feature section in `roster.go` or its focused contract
   file. Do not create a parallel DTO with the same meaning just to avoid
   updating callers.
4. Add a distinct DTO only when the wire contract is actually distinct. Do
   not expose domain structs directly merely to save mapping code.
5. Never add a second spelling for a method, event, JSON field, enum, or error
   code as an unrequested compatibility fallback.

JSON field names, casing, omission versus `null`, enum values, timestamps,
and error envelopes are behavior. Treat all of them as compatibility
contracts.

## Cross-layer change checklist

For a new or changed RPC/event, update the same vertical slice:

- Method/event constant and DTO here.
- Matching application dispatcher and handler.
- Transport handler-level coverage.
- Frontend caller/listener when user-visible.
- Relevant golden fixture under `contracts/testdata/golden/` or
  `testdata/golden/`.
- Agent docs and UI map when the public capability or interface changes.

Do not hand-edit a golden fixture to make a failing test pass without first
checking that the new serialized bytes are intentional. Breaking changes need
user confirmation before implementation.

## Verification

Add serialization and omission edge cases near the owning test. Run:

```text
go test ./contracts/...
go test ./transport/... -run '<relevant test>'
```

Inspect the golden diff as part of review. A contract change is incomplete if
only the producer or only the consumer was updated.
