# Usecase patterns — chatbot vs agentic automation

Read this before writing any LLM integration code when the architecture or
agent behavior is in scope. It selects the high-level pattern and points to
shared techniques; provider files decide the wire format.

## Pattern selection

| Signal | Pattern |
|---|---|
| Conversational UX: Q&A, support, copilot chat | **Chatbot** |
| Multi-step task execution, external actions, workflows | **Agentic automation** |
| Document/batch in → structured out, no dialogue | **Batch/transform** |
| Both conversation and actions (e.g. ops assistant) | Agentic with chat UX |

## Chatbot blueprint

- **System prompt**: role, constraints, output format. Design it with the
  `prompt-engineer` skill — do not improvise prompt structure here.
- **History**: append each user + assistant turn; send full history (or
  `previous_response_id` on OpenAI Responses). Trim by tokens, never by
  message count alone.
- **Streaming is preferred** for interactive user-facing chat when the selected
  endpoint supports it — non-streamed first tokens can feel broken. Surface
  "thinking" state for reasoning models. Unary, batch, embedding, and async
  media endpoints have different lifecycles.
- **Structured output** for UI cards/panels: use a provider-supported strict
  schema when available; parse only after the response is complete.
- **Model tiering**: cheap/fast model for simple turns; escalate to a
  stronger model only when the task demands it (classification first, or
  user-triggered).
- **Abort**: user cancel must cancel the upstream request (streaming), not
  just hide the UI.

## Agentic automation blueprint

```text
limits = {max_steps, max_tool_calls, max_duration, max_spend}
state = {messages, tool_results, usage}
loop:
  resp = call_model(state.messages, tools)
  calls = extract_tool_calls(resp)
  if empty(calls): return final_text
  if limits exceeded: return explicit_incomplete_result or abort
  for call in calls:
      validate args (bad JSON → error output to model, not crash)
      authorize tool and arguments in application code
      destructive? → require human confirmation BEFORE execution
      result = execute(call) with per-tool timeout + output size cap
      append result
  persist state (crash-resume)
```

- **Allowlist tools per environment** (prod ≠ dev capabilities).
- **Model-call timeouts**: interactive streams need a connect timeout plus an
  idle timeout that resets on each chunk. Unary, batch, WebSocket, local
  cold-start, and async jobs need their own configurable deadline/polling
  policy. Per-tool execution timeouts are separate and fine to fix.
- **Idempotency**: tools may re-execute after retries. Prefer an
  application-generated idempotency key; use a provider `call_id` only when its
  stability is guaranteed. Make side-effecting tools idempotent or deduplicate
  them before execution.
- **Audit safely**: record a redacted event with tool name, stable call id,
  outcome, bounded metadata, and usage when available. Do not log raw
  arguments or results by default; they may contain secrets, PII, or prompt
  injection.
- When a step/token/time/spend limit is reached, return an explicit incomplete
  state or ask for continuation. Never claim that an unfinished action
  completed.
- Treat model output, tool results, and retrieved content as untrusted input;
  enforce validation and authorization outside the prompt.

## Context management

- Budget = system + tool schemas + history + expected output < context window.
- Trim oldest history first; **never** trim the system prompt or tool schemas.
- Critical instructions belong at the start (and repeat at the end for long
  contexts) — models attend worst to the middle.
- Summarize old turns instead of deleting when continuity matters.

## Cost & observability

- Record usage/cost metadata per request when the provider exposes it; alert on
  spikes (runaway agent loops show up here first).
- Reuse static prompt prefixes (prompt caching) where the provider supports it.
- Log model id and safe operational parameters with every response when useful
  for debugging regressions; redact prompts, credentials, PII, and large
  payloads.

## Technique references

- Provider/model capability selection → [capability-contract.md](capability-contract.md)
- Long-running context and compaction → [context-management.md](context-management.md)
- MCP tools/resources/prompts → [mcp.md](mcp.md)
- Lifecycle hooks, gates, and callbacks → [hooks.md](hooks.md)
- API boundary and agent verification → [verification.md](verification.md)
