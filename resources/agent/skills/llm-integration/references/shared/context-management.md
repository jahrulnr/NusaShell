# Context management and compaction

API- and protocol-level guidance for long conversations, tool-heavy agents, and
stateless replay. This is a technique layer, not an SDK recipe. Provider wire
contracts remain authoritative; use the provider links below for exact fields.

## What compaction must preserve

Treat the context as several layers with different retention rules:

| Layer | Default handling |
|---|---|
| System/developer instructions and policy | Preserve unless the provider contract explicitly replaces them |
| Tool schemas, permissions, and capability versions | Preserve for the active turn; re-negotiate after a deliberate change |
| Current user turn and pending tool calls | Preserve; never compact an in-flight side effect into an unverified summary |
| Completed tool results | Keep the outcome, identifiers, errors, and authorization evidence; bound large payloads |
| Old user/assistant transcript | Compact or summarize when it is no longer needed verbatim |
| Opaque reasoning/signature/compaction items | Follow the provider contract; replay unchanged or drop only when explicitly allowed |

Compaction reduces context size; it does not prove that an external action
completed. Keep action state and provider correlation ids outside the summary.

## When to compact

Compact before the next request would exceed the effective budget:

```text
effective_budget = context_limit - output_reserve - safety_margin
estimated_input = system + tools + active_turn + retained_history
```

Also consider compaction after a milestone in a long-running agent, after a
provider context-window error, or before resuming a persisted workflow. Do not
compact every turn: it adds latency, costs tokens, and can invalidate prompt
cache prefixes.

## Decision flow

1. Identify the state regime: provider-stored continuation, full stateless
   replay, provider-native compaction, or client-side compaction.
2. Count tokens with the provider's tokenizer/count endpoint where available;
   reserve space for the next answer, tool arguments, and error recovery.
3. Prefer provider-native compaction when its output can be replayed on the
   same provider and its retention/privacy behavior matches the product.
4. Otherwise compact only completed history. Preserve the active turn,
   unresolved tool calls, side-effect state, and required instructions.
5. Validate the compacted result structurally, persist it as a new checkpoint,
   and record which message/item ids it supersedes.
6. If compaction fails, return an explicit context-limit/incomplete state or
   apply a documented local fallback. Never silently claim continuity.

## Provider routing

| Surface | Read | Compaction note |
|---|---|---|
| Public OpenAI Responses | [../openai/responses.md](../openai/responses.md) | `context_management` and the public compaction endpoint are provider-specific; preserve opaque response items |
| Anthropic Messages | [../anthropic/messages.md](../anthropic/messages.md) and [../anthropic/thinking.md](../anthropic/thinking.md) | Context editing, thinking-block retention, and cache invalidation are model-specific |
| ChatGPT/Codex backend | [../codex/compact.md](../codex/compact.md) and [../codex/responses.md](../codex/responses.md) | Compatibility surface only; legacy compact and remote-v2 trigger are not interchangeable |
| Gemini | [../gemini/thinking.md](../gemini/thinking.md) | Preserve `thoughtSignature` on the exact parts required by the model/tool loop |
| Bedrock / OpenRouter / local | [../bedrock/README.md](../bedrock/README.md), [../openrouter/README.md](../openrouter/README.md), [local-inference.md](local-inference.md) | Do not invent a shared compaction wire; use provider features when documented, otherwise compact client-side |

## Provider-agnostic pseudocode

```text
state = {
  durable_instructions,
  tool_definitions,
  completed_history,
  active_turn,
  external_action_state,
  provider_opaque_items,
  checkpoint_id
}

before_call(state):
  budget = context_limit - output_reserve - safety_margin
  if estimate(state) <= budget:
      return state

  if provider_native_compaction_is_supported:
      compacted = provider_compact(state)
      verify_terminal_success(compacted)
      verify_required_opaque_items(compacted)
  else:
      compacted = summarize_completed_history(state)
      validate_summary(compacted)

  state = replace_only_compacted_history(state, compacted)
  persist_checkpoint_atomically(state)
  return state
```

## Safety and correctness rules

- A summary is model-generated data, not an authorization record. Re-check
  permissions and external state before executing a follow-up tool.
- Keep summaries bounded and redact credentials, secrets, PII, and untrusted
  payloads. Preserve provenance for facts that affect a decision.
- Never decode, edit, merge, or truncate opaque provider signatures or encrypted
  compaction items. Never replay them across providers.
- Do not compact concurrently with a tool execution or durable state write;
  use an atomic checkpoint and a recoverable state transition.
- Keep the compaction policy configurable by model, context limit, retention
  mode, and workload. A fixed message-count threshold is not a token budget.

## Verification checklist

- [ ] Compaction triggers before the effective context budget is exceeded
- [ ] System/policy/tool state and the active turn remain intact
- [ ] Pending and completed side effects are distinguishable
- [ ] Opaque provider items are preserved according to that provider's rules
- [ ] A failed compaction produces an explicit incomplete/error state
- [ ] Resume works from the persisted checkpoint without duplicate tool actions
- [ ] Token usage, compaction cost, and cache impact are recorded when exposed

Sources for the public OpenAI compaction contract:
<https://developers.openai.com/api/reference/java/resources/responses/methods/compact>
and <https://developers.openai.com/api/reference/cli/resources/responses/methods/create>.
