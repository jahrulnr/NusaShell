# Hermes hooks

Named plugin lifecycle hooks registered with `register_hook` and invoked
through the shared lifecycle bus (first-party observers, then plugins).

Gateway “builtin hooks” directories may exist as a stub — treat the plugin bus
as the real product surface.

## Related systems (not the same bus)

| System | Role |
|--------|------|
| Middleware | Can rewrite/wrap execution |
| Shell hooks (`hooks:` config) | Scripts on the same bus; plugins win block ties |
| Outbound webhooks | Notify-only HTTP; cannot influence flow |
| Memory / context-engine providers | Separate session methods (same names ≠ same API) |

## Named hooks (~24)

**Tools / transforms:** `pre_tool_call`, `post_tool_call`,
`transform_terminal_output`, `transform_tool_result`, `transform_llm_output`

**LLM / API:** `pre_llm_call`, `post_llm_call`, `pre_verify`, `pre_api_request`,
`post_api_request`, `api_request_error`

**Session:** `on_session_start`, `on_session_end`, `on_session_finalize`,
`on_session_reset`

**Other:** `on_skill_lifecycle`, `subagent_start`, `subagent_stop`,
`pre_gateway_dispatch`, `pre_approval_request`, `post_approval_response`,
`kanban_task_claimed`, `kanban_task_completed`, `kanban_task_blocked`

Unknown hook names typically **warn but still store** (forward-compat).

## Middleware (separate)

```text
tool_request, tool_execution, llm_request, llm_execution
```

Use when you must change what happens. Observer hooks report what happened.

## Directive contracts

| Hook | Return |
|------|--------|
| `pre_tool_call` | `{action:"block", message}` or `{action:"approve", …}` — first wins; approve → human gate (fail-closed on gate error) |
| `transform_*` | First non-empty `str` replaces payload |
| `pre_llm_call` | `{context:"..."}` or `str` → injected into **user** message only (ephemeral) |
| `pre_verify` | continue message or `{decision:"block", reason}` |
| `pre_gateway_dispatch` | `skip` / `rewrite` / `allow` |
| Approvals / most session / kanban / post_* | Observer — returns ignored |

Approval observer hooks **cannot veto** — use `pre_tool_call` for policy.

## Dispatch

1. Sync only. Do not rely on async coroutine returns as hooks.
2. Per-callback isolation; other listeners continue after a failure.
3. Gate expensive payload builds when no listeners (or built-in observers) are active.
4. Shell hooks: stdin JSON (`hook_event_name`, tool fields, extras); stdout JSON for directives.
5. Safe mode / missing consent → shell + outbound registration skipped.

## Lifecycle map (typical turn)

```text
on_session_start          # brand-new session only
  pre_llm_call
    [middleware llm_*]
    pre_api_request → post_api_request | api_request_error
    for each tool:
      pre_tool_call → [approval observers] → tool
        transform_terminal_output (terminal)
      post_tool_call → transform_tool_result
    pre_verify (if edited files & about to stop)
  transform_llm_output → post_llm_call
  on_session_end          # EVERY conversation-turn end (naming trap!)
```

True close: `on_session_finalize`.
`/new`: `on_session_reset` (plus memory/context-engine methods separately).

## Edge cases

1. **`on_session_end` vs finalize** — plugin hook is per-turn; provider
   `on_session_end` is a real session boundary. Do not conflate.
2. **Discovery** — ensure plugins are discovered before reading hook state
   from non-default entrypoints.
3. **Parallel tools** — resolve `pre_tool_call` once, then skip a second fire
   on nested tool dispatch.
4. **Kanban** — do not hold DB write locks while invoking hooks; claim vs
   complete/block may run in different processes.
5. **Oversized `pre_llm_call` context** — spill or truncate; keep injections bounded.
6. **Prompt cache** — no mid-conversation system/toolset mutation; context on
   user message only.
7. Do not assume turn hooks live only in one “agent runner” module — they fire
   from the turn/context/finalize pipeline.

## Best practices

1. No speculative hooks without a concrete consumer.
2. Prefer observer hooks; use middleware for rewrites/wraps.
3. Gate hot paths when nothing is listening.
4. Policy → `pre_tool_call`; teardown → `on_session_finalize`.
5. Keep `pre_llm_call` injections bounded and cache-safe.
6. Shell hooks: validate with the CLI doctor/test commands; plugins win block ties.
7. Outbound webhooks for notify only.
8. Plugins must not patch core — widen the generic hook/middleware surface instead.
