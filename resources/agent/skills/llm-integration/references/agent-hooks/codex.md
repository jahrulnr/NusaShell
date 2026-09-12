# Codex hooks

Claude-style lifecycle hooks: config-declared command or MCP handlers that
receive JSON on stdin and return JSON on stdout. Feature is stable and on by
default.

## Event catalog (12)

```text
PreToolUse, PermissionRequest, PostToolUse,
PreCompact, PostCompact,
SessionStart, SessionEnd, UserPromptSubmit,
SubagentStart, SubagentStop, Stop, Interrupt
```

Matchers apply to tool/session/compact/subagent events. `UserPromptSubmit`,
`Stop`, and `Interrupt` ignore matchers for selection.

**Adjacent (not the 12):**

- Legacy `AfterAgent` / `notify` argv process hooks
- Executor-scoped plugin cleanup (always async; quieter in the UI)
- In-process extension lifecycle contributors (observe only — policy stays in product hooks)

## Handlers

| Kind | Status | Notes |
|------|--------|-------|
| `command` | Supported | Shell; stdin JSON; optional `async`, `timeout`, `statusMessage` |
| `mcp_tool` | Supported | Always sync; response text parsed like command output |
| `prompt` / `agent` | Config accepted | Not wired yet — skipped at discovery |

Config sources: managed requirements, user/project `hooks.json` and/or TOML,
plugins. Prefer **one** of JSON or TOML per layer (loading both warns).

## Capability matrix

| Event | Inject | Veto | Mutate |
|-------|--------|------|--------|
| SessionStart / SubagentStart | `additionalContext` | `continue: false` | — |
| UserPromptSubmit | yes | stop / block | — |
| PreToolUse | yes | **block tool** | **`updatedInput`** (last by completion order) |
| PermissionRequest | limited | — | reserved rewrite fields **fail closed** if present |
| PostToolUse | yes | block **result** (tool already ran) | MCP output rewrite unsupported (fail-open warn) |
| Pre/PostCompact | — | `continue: false` | — |
| Stop / SubagentStop | continuation fragments | stop **or** block→continue turn | — |
| SessionEnd | — | cannot block teardown | — |
| Interrupt | systemMessage | no | — |

Universal stdout: `continue` (default true), `stopReason`, `suppressOutput`,
`systemMessage`.

## Dispatch rules

1. Matching sync handlers run concurrently, then sorted to configured order for reporting.
2. Matcher alias sets (`apply_patch|Write|Edit`) still fire a handler **once** per tool call.
3. `async: true` → background work (bounded); **no** block/rewrite/stop.
4. SessionEnd forces sync (async config → warning); SessionEnd & Interrupt
   default timeout ~**1s**, clamp **1–3s**; others default **600s**.
5. Untrusted unmanaged hooks do not run until trust review (hash of normalized
   config). Managed-only mode belongs in requirements config, not user hooks.
6. Internal subagents (except thread-spawn children) skip SessionStart; all
   subagent sources skip SessionEnd.

## When each event fires

| Hook | When |
|------|------|
| SessionStart / SubagentStart | Thread start / resume / clear / compact source; thread-spawn child start |
| UserPromptSubmit | User input about to enter a turn |
| PreToolUse | Before a tool handler runs |
| PermissionRequest | Approval path before UI / guardian |
| PostToolUse | After successful tool output is adapted |
| Pre/PostCompact | Around compaction |
| Stop / SubagentStop | Turn ends without a model follow-up |
| Interrupt | User interrupt |
| SessionEnd | Unload / archive / delete / shutdown |
| AfterAgent | After Stop (legacy notify) |

No dedicated pre/post-LLM event — closest are UserPromptSubmit and Stop.

## Edge cases

- Serialize failure on PreToolUse → failed run events, does **not** auto-block.
- PostToolUse `decision: block` needs a non-empty `reason`.
- Stop block without continuation fragments → warning, ignore block.
- Memory-consolidation stop/block → hard error.
- `additionalContext` spilled around a ~2500-token default; injected as
  contextual fragments (not history rewrite).
- Parallel tools: independent Pre/Post waves.

## Best practices

1. Policy that must veto/rewrite → **sync** command or MCP.
2. Keep SessionEnd/Interrupt handlers ≤3s.
3. Trust unmanaged hooks before relying on them in CI/dev.
4. Matchers may use aliases; stdin `tool_name` is always **canonical**.
5. Do not use reserved PermissionRequest rewrite fields yet.
6. Prefer product hooks over extension contributors for payload policy.
