# API integration verification

Verification for provider integrations should prove the behavior at the API
boundary, not only that a client wrapper compiles. Use deterministic fixtures
for normal paths and tightly controlled live requests for current capability
checks.

## Test layers

| Layer | Proves | Typical artifact |
|---|---|---|
| Contract | URL, auth mode, request fields, response envelope | Redacted request/response fixture |
| Parser | Chunk assembly, terminal event, usage, tool arguments | SSE/NDJSON/event-stream replay fixture |
| Failure | Status mapping, mid-stream errors, retry policy, cancellation | Error fixture + retry decision table |
| Agent | Validation, authorization, confirmation, idempotency, limits | Tool-loop scenario test |
| Context | Budget trigger, compaction, replay, checkpoint recovery | Before/after state fixture |
| MCP | Version/transport, list/call, schema, cancellation, input-required | JSON-RPC transcript |
| Privacy | Redaction and trust boundaries | Log-scrubbing assertion |
| Live smoke | Current model/endpoint capability and auth | Minimal, cost-bounded request; never store secrets |

## Minimum contract scenarios

For each endpoint family, cover the scenarios it supports:

- valid minimal request and complete response;
- malformed request/unsupported parameter;
- empty or truncated model output;
- usage present, absent, and located in a terminal event when streaming;
- tool call with valid arguments;
- malformed arguments and unknown tool name;
- multiple/parallel tool calls when supported;
- provider refusal/content-policy response;
- transient 429/5xx/network failure and `Retry-After` handling;
- client cancellation and connection close;
- retry after an unknown outcome without duplicating side effects;
- model/endpoint capability mismatch;
- context limit and compaction failure when relevant.

## Technique scenarios

### Compaction

- Trigger before the effective context budget is exceeded.
- Preserve active instructions, pending tool calls, action state, and required
  opaque provider items.
- Resume from a persisted checkpoint without replaying completed side effects.
- Surface an explicit incomplete state when compaction cannot complete.

### Hooks

- Verify observe/inject/gate/mutate/claim return contracts.
- Test ordering, timeout, cancellation, failure policy, duplicate delivery, and
  authorization before the side effect.
- Assert that logs contain redacted bounded metadata, not raw prompts or secrets.

### MCP

- Pin the protocol version and transport; test modern and legacy behavior only
  when dual-era support is intentional.
- Test paginated `tools/list`, auth-scoped cache invalidation, name collisions,
  `tools/call`, schema validation, `isError`, cancellation, and `input_required`.
- Test `resources/list`/`resources/read` and `prompts/list`/`prompts/get` when
  the integration exposes them; verify size, authorization, cache scope, and
  untrusted-content handling.
- Verify consent, server allowlist, origin/auth checks, and server-scoped
  credentials.

## Live smoke-test policy

Use a live request only when it answers a question fixtures cannot answer:

1. Select the smallest request and cheapest suitable model.
2. Set an explicit product cost/wait budget and use the correct transport.
3. Read the credential from the environment/secret manager; never print it.
4. Record only redacted status, request id, model/endpoint, usage/cost when
   returned, and terminal outcome.
5. Do not use a live request to test destructive tools or unbounded loops.
6. Treat a live pass as evidence for that account/region/model at that time,
   not a permanent capability guarantee.

## Failure evidence

When a check fails, retain the smallest reusable evidence:

- provider, endpoint, model, transport, and capability assumptions;
- status/error type and request id, without secrets or raw sensitive payloads;
- whether the request may have been processed before the failure;
- retry/rollback decision and the invariant that prevents recurrence.

Do not make a one-time test result a permanent model claim. Put durable rules in
the relevant reference and keep transient observations in the task/issue.

Related: [capability-contract.md](capability-contract.md),
[context-management.md](context-management.md), [hooks.md](hooks.md), and
[mcp.md](mcp.md).
