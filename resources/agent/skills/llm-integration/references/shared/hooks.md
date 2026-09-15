# API-level lifecycle hooks

Hooks are extension points around an LLM request, tool call, context change, or
async job. This reference defines the portable contract. The
[agent-hooks implementation guide](../agent-hooks/README.md) explains how to
build the dispatcher and handler lifecycle; its companion runtime notes are
source material for comparison, not separate workflows to copy.

## Hook capabilities

Classify a hook by what it is allowed to do, not by its name:

| Capability | Contract | Typical use |
|---|---|---|
| Observe | Side effects only; return value is ignored | Metrics, traces, audit events |
| Inject | Adds bounded context to the next model turn | Tenant facts, request metadata |
| Gate | Allows or denies an operation | Tool permission, quota, consent |
| Mutate | Rewrites a request or tool argument | Add headers, normalize fields |
| Claim | Owns the event and returns a synthetic result | Cache hit, deduplicated job |

Do not let an observe hook accidentally become a policy hook. Make the return
contract explicit and reject unexpected policy shapes safely.

## LLM lifecycle map

```text
request.before      → validate route, auth mode, limits, and safe metadata
model.before        → add bounded context or provider options
stream.event        → observe/forward a chunk; never commit before terminal state
model.after         → record safe usage/cost and terminal status
tool.before         → authorize, validate args, and request confirmation
tool.after          → record outcome and bound the result before replay
context.before      → check budget and prepare compaction
context.after       → validate checkpoint and cache impact
job.callback        → verify signature, deduplicate, and transition job state
```

## Hook contract

Every behavior-changing hook should define:

- input schema and trust level;
- output/decision schema, including allow/deny/stop semantics;
- ordering and whether multiple handlers compose or first-wins;
- timeout, cancellation, retry, and idempotency behavior;
- failure policy: fail-closed for authorization/consent, fail-open only for
  non-critical observation;
- redaction and retention rules for payloads, headers, prompts, and tool data.

## Safety rules

- A hook is not a substitute for authentication or authorization in the
  application. Enforce permission in the actual tool/API handler.
- Treat model output, tool arguments/results, retrieved text, and webhook bodies
  as untrusted input. Validate them at the boundary.
- Destructive actions require an explicit user or policy authorization before
  execution. A post-tool hook cannot undo an action that already ran.
- Observe hooks should log only bounded, redacted metadata. Never log API keys,
  OAuth/session tokens, raw prompts, or unrestricted tool arguments/results.
- Treat tool results, tool metadata, annotations, and callback bodies as
  untrusted evidence. Validate and bound them before replaying them to the
  model; never let their text self-authorize a new tool call or change policy.
- Mutating a provider-signed or encrypted reasoning/compaction item can break
  replay. Preserve opaque fields exactly or leave the provider item untouched.
- A timeout stops awaiting a hook; it does not necessarily cancel work already
  running. Give hooks an owner-managed cancellation path and an idempotency key.

## Local hooks vs HTTP callbacks

| Surface | Primary concern |
|---|---|
| In-process hook | Ordering, cancellation, shared memory, handler isolation |
| External process | Input/output framing, stdout purity, trust and process limits |
| HTTP webhook/callback | HTTPS, signature verification, replay protection, idempotency, bounded body |
| Provider stream interceptor | Chunk framing, terminal event, partial output, cancellation |

Do not conflate provider response callbacks, HTTP webhooks, product lifecycle
hooks, Git hooks, and framework hooks. They have different trust and retry
semantics.

## Porting a hook

1. Identify the capability: observe, inject, gate, mutate, or claim.
2. Identify the exact lifecycle boundary and whether it is per-request,
   per-tool, per-session, or per-job.
3. Preserve the target's failure policy, ordering, cancellation, and idempotency
   semantics; do not port names mechanically.
4. Test the real dispatch seam, including timeout, duplicate delivery, denial,
   and handler failure—not only the callback in isolation.

When a concrete runtime must be supported, compare the companion source notes
under [agent-hooks](../agent-hooks/) after the portable contract is clear. Map
their observations back to the same capability, ordering, timeout, and failure
policy instead of splitting the implementation by runtime name.

## Verification checklist

- [ ] Hook capability and return contract are explicit
- [ ] Policy hooks fail closed; telemetry hooks fail open where appropriate
- [ ] User consent happens before a destructive operation
- [ ] Payloads and logs are bounded and redacted
- [ ] Timeout and cancellation behavior is documented
- [ ] Duplicate delivery cannot duplicate side effects
- [ ] The actual request/tool/job seam is covered by a test
